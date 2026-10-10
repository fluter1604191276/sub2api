//go:build unit

package server

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"
)

func boundedH2CTestConfig(h1Idle, h2Idle int) *config.Config {
	cfg := ingressTestConfig()
	cfg.Server.IdleTimeout = h1Idle
	cfg.Server.H2C = config.H2CConfig{
		Enabled: true, MaxConcurrentStreams: 25, IdleTimeout: h2Idle,
		MaxReadFrameSize:             64 * 1024,
		MaxUploadBufferPerConnection: 1024 * 1024,
		MaxUploadBufferPerStream:     256 * 1024,
	}
	return cfg
}

// Read actual peer SETTINGS and connection WINDOW_UPDATE; checking only the
// Go fields would miss an external x/net configuration overriding HTTP2Config.
func openBoundedH2C(t *testing.T, addr string) (net.Conn, *http2.Framer) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))
	_, err = io.WriteString(conn, http2.ClientPreface)
	require.NoError(t, err)
	fr := http2.NewFramer(conn, conn)
	require.NoError(t, fr.WriteSettings())
	var settingsSeen, windowSeen bool
	for !settingsSeen || !windowSeen {
		frame, err := fr.ReadFrame()
		require.NoError(t, err)
		switch frame := frame.(type) {
		case *http2.SettingsFrame:
			if frame.IsAck() {
				continue
			}
			for id, want := range map[http2.SettingID]uint32{
				http2.SettingMaxConcurrentStreams: 25,
				http2.SettingMaxFrameSize:         64 * 1024,
				http2.SettingInitialWindowSize:    256 * 1024,
			} {
				got, ok := frame.Value(id)
				require.True(t, ok, "missing peer setting %v", id)
				require.Equal(t, want, got, "peer setting %v", id)
			}
			require.NoError(t, fr.WriteSettingsAck())
			settingsSeen = true
		case *http2.WindowUpdateFrame:
			if frame.StreamID == 0 {
				require.Equal(t, uint32(1024*1024-65535), frame.Increment)
				windowSeen = true
			}
		}
	}
	return conn, fr
}

func readIdleGoAway(t *testing.T, conn net.Conn, fr *http2.Framer) {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	for {
		frame, err := fr.ReadFrame()
		require.NoError(t, err)
		if frame, ok := frame.(*http2.GoAwayFrame); ok {
			require.Equal(t, http2.ErrCodeNo, frame.ErrCode)
			return
		}
	}
}

func h1Get(t *testing.T, conn net.Conn, br *bufio.Reader) {
	t.Helper()
	require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))
	_, err := io.WriteString(conn, "GET /ok HTTP/1.1\r\nHost: fixture\r\n\r\n")
	require.NoError(t, err)
	resp, err := http.ReadResponse(br, nil)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "ok", string(body))
}

func TestIngressHTTP2PublicLimitsPreserveIndependentIdle(t *testing.T) {
	for _, tc := range []struct {
		name           string
		h1Idle, h2Idle int
	}{
		{"H2 closes before H1", 4, 1},
		{"H1 closes before H2", 1, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.GET("/ok", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
			srv := ProvideHTTPServer(boundedH2CTestConfig(tc.h1Idle, tc.h2Idle), router)
			addr, stop := serveIngressTestServer(t, srv)
			defer stop()
			h1, err := net.DialTimeout("tcp", addr, time.Second)
			require.NoError(t, err)
			defer func() { _ = h1.Close() }()
			reader := bufio.NewReader(h1)
			h1Get(t, h1, reader)
			h2, fr := openBoundedH2C(t, addr)
			if tc.h2Idle < tc.h1Idle {
				readIdleGoAway(t, h2, fr)
				h1Get(t, h1, reader)
			} else {
				require.NoError(t, h1.SetReadDeadline(time.Now().Add(3*time.Second)))
				_, err = reader.ReadByte()
				require.ErrorIs(t, err, io.EOF, "H1 must expire at its own shorter timeout")
				data := [8]byte{1, 2, 3, 4, 5, 6, 7, 8}
				require.NoError(t, fr.WritePing(false, data))
				require.NoError(t, h2.SetReadDeadline(time.Now().Add(2*time.Second)))
				for {
					frame, err := fr.ReadFrame()
					require.NoError(t, err)
					if ping, ok := frame.(*http2.PingFrame); ok && ping.IsAck() {
						require.Equal(t, data, ping.Data)
						break
					}
					require.IsNotType(t, &http2.GoAwayFrame{}, frame, "H2 must outlive H1")
				}
				readIdleGoAway(t, h2, fr)
			}
		})
	}
}

func TestIngressHTTP2ActiveStreamAndGracefulShutdown(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseStream := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseStream()
	router := gin.New()
	router.GET("/stream", func(c *gin.Context) {
		c.Writer.WriteHeader(http.StatusOK)
		_, _ = c.Writer.WriteString("started\n")
		c.Writer.Flush()
		select {
		case <-release:
		case <-c.Request.Context().Done():
		}
	})
	router.GET("/ok", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	srv := ProvideHTTPServer(boundedH2CTestConfig(4, 1), router)
	addr, stop := serveIngressTestServer(t, srv)
	defer stop()
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	transport := &http.Transport{Protocols: protocols}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	var connMu sync.Mutex
	var firstConn, secondConn net.Conn
	trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
		connMu.Lock()
		firstConn = info.Conn
		connMu.Unlock()
	}}
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(context.Background(), trace), http.MethodGet, "http://"+addr+"/stream", nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, 2, resp.ProtoMajor)
	time.Sleep(1500 * time.Millisecond)
	secondTrace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
		connMu.Lock()
		secondConn = info.Conn
		connMu.Unlock()
	}}
	req, err = http.NewRequestWithContext(httptrace.WithClientTrace(context.Background(), secondTrace), http.MethodGet, "http://"+addr+"/ok", nil)
	require.NoError(t, err)
	second, err := client.Do(req)
	require.NoError(t, err)
	connMu.Lock()
	first, secondConnection := firstConn, secondConn
	connMu.Unlock()
	require.Same(t, first, secondConnection, "active H2 stream must keep its connection reusable")
	require.Equal(t, 2, second.ProtoMajor)
	b, err := io.ReadAll(second.Body)
	require.NoError(t, err)
	require.NoError(t, second.Body.Close())
	require.Equal(t, "ok", string(b))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	shutdown := make(chan error, 1)
	go func() { shutdown <- srv.Shutdown(ctx) }()
	select {
	case err := <-shutdown:
		t.Fatalf("Shutdown completed before active stream drained: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	releaseStream()
	b, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "started\n", string(b))
	select {
	case err := <-shutdown:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("Shutdown did not complete after the active stream drained")
	}
}

func TestIngressHTTP2CloseCancelsActiveStream(t *testing.T) {
	canceled := make(chan struct{})
	router := gin.New()
	router.GET("/stream", func(c *gin.Context) {
		c.Status(http.StatusOK)
		c.Writer.Flush()
		<-c.Request.Context().Done()
		close(canceled)
	})
	srv := ProvideHTTPServer(boundedH2CTestConfig(4, 1), router)
	addr, stop := serveIngressTestServer(t, srv)
	defer stop()
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	transport := &http.Transport{Protocols: protocols}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	resp, err := client.Get("http://" + addr + "/stream")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.NoError(t, srv.Close())
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not cancel active H2 stream")
	}
	_, err = io.ReadAll(resp.Body)
	require.Error(t, err)
}

func TestIngressH2CDisabledRejectsPriorKnowledge(t *testing.T) {
	router := gin.New()
	router.GET("/ok", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	srv := ProvideHTTPServer(ingressTestConfig(), router)
	addr, stop := serveIngressTestServer(t, srv)
	defer stop()
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	require.NoError(t, conn.SetDeadline(time.Now().Add(3*time.Second)))
	_, err = io.WriteString(conn, http2.ClientPreface)
	require.NoError(t, err)
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, 1, resp.ProtoMajor, "disabled H2C must stay on HTTP/1")
	require.Equal(t, http.StatusNotFound, resp.StatusCode, "the PRI preface is not an application route")
	require.Nil(t, srv.HTTP2)
}
