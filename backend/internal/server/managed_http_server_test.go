//go:build unit

package server

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"
)

func managedTestServer(t *testing.T, h1Idle, h2Idle int, handler http.Handler) *ManagedHTTPServer {
	t.Helper()
	cfg := ingressTestConfig()
	cfg.Server.IdleTimeout = h1Idle
	cfg.Server.H2C = config.H2CConfig{Enabled: true, IdleTimeout: h2Idle, MaxConcurrentStreams: 7, MaxReadFrameSize: 64 * 1024, MaxUploadBufferPerConnection: 1024 * 1024, MaxUploadBufferPerStream: 256 * 1024}
	srv := ProvideHTTPServer(cfg, gin.New())
	srv.Handler = handler
	return srv
}

func managedServe(t *testing.T, srv *ManagedHTTPServer) (string, <-chan error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		err := srv.Shutdown(ctx)
		if err != nil {
			require.ErrorIs(t, err, context.DeadlineExceeded)
			require.NoError(t, srv.Close())
		}
	})
	return ln.Addr().String(), done
}

func managedTransport(t *testing.T, h2 bool) *http.Transport {
	t.Helper()
	p := new(http.Protocols)
	p.SetHTTP1(!h2)
	p.SetUnencryptedHTTP2(h2)
	tr := &http.Transport{Protocols: p}
	t.Cleanup(tr.CloseIdleConnections)
	return tr
}

func managedGet(t *testing.T, client *http.Client, target string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	require.NoError(t, err)
	res, err := client.Do(req)
	require.NoError(t, err)
	defer func() { require.NoError(t, res.Body.Close()) }()
	require.Equal(t, http.StatusOK, res.StatusCode)
	body, err := io.ReadAll(io.LimitReader(res.Body, 1024))
	require.NoError(t, err)
	return string(body)
}

func TestManagedHTTPServerIndependentIdleTimeouts(t *testing.T) {
	for _, tc := range []struct {
		name               string
		h1, h2             int
		h1Reused, h2Reused bool
	}{{"H1 expires first", 1, 4, false, true}, {"H2 expires first", 4, 1, true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			srv := managedTestServer(t, tc.h1, tc.h2, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, err := fmt.Fprint(w, r.RemoteAddr)
				if err != nil {
					t.Errorf("write response: %v", err)
				}
			}))
			addr, _ := managedServe(t, srv)
			c1 := &http.Client{Transport: managedTransport(t, false)}
			c2 := &http.Client{Transport: managedTransport(t, true)}
			a1 := managedGet(t, c1, "http://"+addr+"/")
			a2 := managedGet(t, c2, "http://"+addr+"/")
			time.Sleep(2400 * time.Millisecond)
			b1 := managedGet(t, c1, "http://"+addr+"/")
			b2 := managedGet(t, c2, "http://"+addr+"/")
			require.Equal(t, tc.h1Reused, a1 == b1, "HTTP/1 independent idle")
			require.Equal(t, tc.h2Reused, a2 == b2, "HTTP/2 independent idle")
		})
	}
}

func TestManagedHTTPServerAdvertisesConfiguredSettings(t *testing.T) {
	srv := managedTestServer(t, 4, 4, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	addr, _ := managedServe(t, srv)
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	require.NoError(t, err)
	defer func() { require.NoError(t, conn.Close()) }()
	require.NoError(t, conn.SetDeadline(time.Now().Add(2*time.Second)))
	_, err = io.WriteString(conn, http2.ClientPreface)
	require.NoError(t, err)
	fr := http2.NewFramer(conn, conn)
	require.NoError(t, fr.WriteSettings())
	settings := map[http2.SettingID]uint32{}
	var window uint32
	for i := 0; i < 8 && (len(settings) == 0 || window == 0); i++ {
		f, readErr := fr.ReadFrame()
		require.NoError(t, readErr)
		switch v := f.(type) {
		case *http2.SettingsFrame:
			if !v.IsAck() {
				require.NoError(t, v.ForeachSetting(func(s http2.Setting) error { settings[s.ID] = s.Val; return nil }))
				require.NoError(t, fr.WriteSettingsAck())
			}
		case *http2.WindowUpdateFrame:
			if v.StreamID == 0 {
				window = v.Increment
			}
		}
	}
	require.Equal(t, uint32(7), settings[http2.SettingMaxConcurrentStreams])
	require.Equal(t, uint32(64*1024), settings[http2.SettingMaxFrameSize])
	require.Equal(t, uint32(256*1024), settings[http2.SettingInitialWindowSize])
	require.Equal(t, uint32(1024*1024), window+65535)
	ping := [8]byte{'l', 'o', 'o', 'p', 'b', 'a', 'c', 'k'}
	require.NoError(t, fr.WritePing(false, ping))
	acked := false
	for i := 0; i < 8 && !acked; i++ {
		frame, err := fr.ReadFrame()
		require.NoError(t, err)
		if p, ok := frame.(*http2.PingFrame); ok && p.IsAck() {
			require.Equal(t, ping, p.Data)
			acked = true
		}
	}
	require.True(t, acked, "valid HTTP/2 PING receives acknowledgement")
}

func TestManagedHTTPServerPartialPrefaceAcceptBudget(t *testing.T) {
	for _, prefix := range []string{"", "G", "PRI * HTTP/2.0", "PRI * HTTP/2.0\r\n\r\nSM"} {
		t.Run(fmt.Sprintf("%q", prefix), func(t *testing.T) {
			srv := managedTestServer(t, 4, 4, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
			srv.ReadHeaderTimeout = 300 * time.Millisecond
			addr, _ := managedServe(t, srv)
			c, err := net.DialTimeout("tcp", addr, time.Second)
			require.NoError(t, err)
			defer func() { require.NoError(t, c.Close()) }()
			start := time.Now()
			if prefix != "" {
				_, err = io.WriteString(c, prefix)
				require.NoError(t, err)
			}
			require.NoError(t, c.SetReadDeadline(start.Add(time.Second)))
			_, err = io.ReadAll(io.LimitReader(c, 4096))
			require.NoError(t, err)
			require.Less(t, time.Since(start), 750*time.Millisecond)
		})
	}
	// A classifier must not grant a second complete header budget after dispatch.
	t.Run("delayed complete preface to incomplete HTTP1 header", func(t *testing.T) {
		srv := managedTestServer(t, 4, 4, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
		srv.ReadHeaderTimeout = 400 * time.Millisecond
		addr, _ := managedServe(t, srv)
		c, err := net.DialTimeout("tcp", addr, time.Second)
		require.NoError(t, err)
		defer func() { require.NoError(t, c.Close()) }()
		start := time.Now()
		_, err = io.WriteString(c, "G")
		require.NoError(t, err)
		time.Sleep(270 * time.Millisecond)
		_, err = io.WriteString(c, "ET / HTTP/1.1\r\nHost: test\r\nX-Slow:")
		require.NoError(t, err)
		require.NoError(t, c.SetReadDeadline(start.Add(time.Second)))
		_, err = io.ReadAll(io.LimitReader(c, 4096))
		require.NoError(t, err)
		require.Less(t, time.Since(start), 650*time.Millisecond)
	})
}

func TestManagedHTTPServerActiveStreamCancellationReuse(t *testing.T) {
	started := make(chan string, 2)
	canceled := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cancel" {
			started <- r.RemoteAddr
			<-r.Context().Done()
			once.Do(func() { close(canceled) })
			return
		}
		if r.URL.Path == "/held" {
			started <- r.RemoteAddr
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		_, err := fmt.Fprint(w, r.RemoteAddr)
		if err != nil {
			t.Errorf("write: %v", err)
		}
	})
	srv := managedTestServer(t, 4, 1, handler)
	addr, _ := managedServe(t, srv)
	client := &http.Client{Transport: managedTransport(t, true)}
	cancelCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(cancelCtx, "GET", "http://"+addr+"/cancel", nil)
	require.NoError(t, err)
	canceledResult := make(chan error, 1)
	go func() {
		res, e := client.Do(req)
		if res != nil {
			_ = res.Body.Close()
		}
		canceledResult <- e
	}()
	var first string
	select {
	case first = <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("cancel stream did not start")
	}
	heldResult := make(chan string, 1)
	heldErr := make(chan error, 1)
	go func() {
		ctx, cancelHeld := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancelHeld()
		r, e := http.NewRequestWithContext(ctx, "GET", "http://"+addr+"/held", nil)
		if e != nil {
			heldErr <- e
			return
		}
		res, e := client.Do(r)
		if e != nil {
			heldErr <- e
			return
		}
		b, e := io.ReadAll(res.Body)
		closeErr := res.Body.Close()
		if e == nil {
			e = closeErr
		}
		if e != nil {
			heldErr <- e
			return
		}
		heldResult <- string(b)
	}()
	select {
	case second := <-started:
		require.Equal(t, first, second, "concurrent streams use same connection")
	case <-time.After(2 * time.Second):
		t.Fatal("held stream did not start")
	}
	cancel()
	select {
	case e := <-canceledResult:
		require.ErrorIs(t, e, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("canceled request did not return")
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("server context not canceled")
	}
	time.Sleep(1500 * time.Millisecond)
	close(release)
	select {
	case result := <-heldResult:
		require.Equal(t, first, result)
	case e := <-heldErr:
		t.Fatal(e)
	case <-time.After(2 * time.Second):
		t.Fatal("active stream stopped across idle interval")
	}
	require.Equal(t, first, managedGet(t, client, "http://"+addr+"/after"), "connection remains reusable")
}

func TestManagedHTTPServerShutdownWaitsForBothProtocols(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	results := make(chan error, 2)
	srv := managedTestServer(t, 4, 4, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		select {
		case <-release:
			w.WriteHeader(200)
		case <-r.Context().Done():
		}
	}))
	addr, done := managedServe(t, srv)
	for _, h2 := range []bool{false, true} {
		client := &http.Client{Transport: managedTransport(t, h2)}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			req, e := http.NewRequestWithContext(ctx, "GET", "http://"+addr+"/", nil)
			if e != nil {
				results <- e
				return
			}
			res, e := client.Do(req)
			if res != nil {
				closeErr := res.Body.Close()
				if e == nil {
					e = closeErr
				}
			}
			results <- e
		}()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("protocol request did not start")
		}
	}
	// Include an accepted connection that has no classification bytes yet.
	pending, err := net.DialTimeout("tcp", addr, time.Second)
	require.NoError(t, err)
	defer func() { require.NoError(t, pending.Close()) }()
	shutdownResult := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		shutdownResult <- srv.Shutdown(ctx)
	}()
	select {
	case err := <-shutdownResult:
		t.Fatalf("Shutdown returned while requests active: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	close(release)
	for i := 0; i < 2; i++ {
		select {
		case err := <-results:
			require.NoError(t, err)
		case <-time.After(2 * time.Second):
			t.Fatal("request failed to finish")
		}
	}
	select {
	case err := <-shutdownResult:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not join both protocols")
	}
	select {
	case err := <-done:
		require.ErrorIs(t, err, http.ErrServerClosed)
	case <-time.After(time.Second):
		t.Fatal("Serve did not return")
	}
	require.NoError(t, pending.SetReadDeadline(time.Now().Add(time.Second)))
	_, err = bufio.NewReader(pending).ReadByte()
	require.Error(t, err)
	require.NoError(t, srv.Close())
	require.NoError(t, srv.Shutdown(context.Background()))
}

func TestManagedHTTPServerCloseCancelsBothProtocols(t *testing.T) {
	started := make(chan struct{}, 2)
	canceled := make(chan struct{}, 2)
	srv := managedTestServer(t, 4, 4, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-r.Context().Done()
		canceled <- struct{}{}
	}))
	addr, done := managedServe(t, srv)
	for _, h2 := range []bool{false, true} {
		client := &http.Client{Transport: managedTransport(t, h2)}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			req, e := http.NewRequestWithContext(ctx, "GET", "http://"+addr+"/", nil)
			if e == nil {
				res, _ := client.Do(req)
				if res != nil {
					_ = res.Body.Close()
				}
			}
		}()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("request did not start")
		}
	}
	require.NoError(t, srv.Close())
	require.NoError(t, srv.Close())
	for i := 0; i < 2; i++ {
		select {
		case <-canceled:
		case <-time.After(time.Second):
			t.Fatal("Close did not cancel request context")
		}
	}
	select {
	case err := <-done:
		require.ErrorIs(t, err, http.ErrServerClosed)
	case <-time.After(time.Second):
		t.Fatal("Serve did not return")
	}
}

func TestManagedHTTPServerDefaultDisabledAndHTTP1Upgrade(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("enabled=%t", enabled), func(t *testing.T) {
			cfg := ingressTestConfig()
			cfg.Server.H2C.Enabled = enabled
			srv := ProvideHTTPServer(cfg, gin.New())
			srv.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, err := fmt.Fprint(w, r.Proto)
				if err != nil {
					t.Errorf("write: %v", err)
				}
			})
			addr, _ := managedServe(t, srv)
			require.Equal(t, "HTTP/1.1", managedGet(t, &http.Client{Transport: managedTransport(t, false)}, "http://"+addr+"/"))
			c, err := net.DialTimeout("tcp", addr, time.Second)
			require.NoError(t, err)
			defer func() { require.NoError(t, c.Close()) }()
			require.NoError(t, c.SetDeadline(time.Now().Add(time.Second)))
			_, err = io.WriteString(c, "GET / HTTP/1.1\r\nHost: local\r\nConnection: Upgrade, HTTP2-Settings\r\nUpgrade: h2c\r\nHTTP2-Settings: AAMAAABk\r\n\r\n")
			require.NoError(t, err)
			res, err := http.ReadResponse(bufio.NewReader(c), nil)
			require.NoError(t, err)
			b, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, 200, res.StatusCode)
			require.Equal(t, "HTTP/1.1", string(b))
			if !enabled {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				req, err := http.NewRequestWithContext(ctx, "GET", "http://"+addr+"/", nil)
				require.NoError(t, err)
				res, err = (&http.Client{Transport: managedTransport(t, true)}).Do(req)
				if res != nil {
					require.NoError(t, res.Body.Close())
				}
				require.Error(t, err, "default must not admit cleartext H2")
			}
		})
	}
}

func TestManagedHTTPServerTLSNegotiationAndRequestTLS(t *testing.T) {
	certServer := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	certs := certServer.TLS.Certificates
	certServer.Close()
	for _, direct := range []bool{false, true} {
		t.Run(fmt.Sprintf("directTLSListener=%t", direct), func(t *testing.T) {
			srv := managedTestServer(t, 4, 4, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, err := fmt.Fprintf(w, "%s tls=%t", r.Proto, r.TLS != nil)
				if err != nil {
					t.Errorf("write: %v", err)
				}
			}))
			if srv.TLSConfig == nil {
				srv.TLSConfig = &tls.Config{}
			}
			srv.TLSConfig.Certificates = certs
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			done := make(chan error, 1)
			if direct {
				go func() { done <- srv.Serve(tls.NewListener(ln, srv.TLSConfig.Clone())) }()
			} else {
				go func() { done <- srv.ServeTLS(ln, "", "") }()
			}
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				require.NoError(t, srv.Shutdown(ctx))
				select {
				case err := <-done:
					require.ErrorIs(t, err, http.ErrServerClosed)
				case <-time.After(time.Second):
					t.Error("TLS Serve did not return")
				}
			}()
			conn, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", ln.Addr().String(), &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"h2", "http/1.1"}})
			require.NoError(t, err)
			wantALPN := "http/1.1"
			if direct {
				wantALPN = "h2"
			}
			require.Equal(t, wantALPN, conn.ConnectionState().NegotiatedProtocol)
			require.NoError(t, conn.Close())
			for _, h2 := range []bool{false, true} {
				if h2 && !direct {
					continue
				}
				p := new(http.Protocols)
				p.SetHTTP1(!h2)
				p.SetHTTP2(h2)
				tr := &http.Transport{Protocols: p, TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
				defer tr.CloseIdleConnections()
				proto := "HTTP/1.1"
				if h2 {
					proto = "HTTP/2.0"
				}
				require.Equal(t, proto+" tls=true", managedGet(t, &http.Client{Transport: tr}, "https://"+ln.Addr().String()+"/"))
			}
		})
	}
}

func TestManagedHTTPServerHijackCONNECTRemainsCallerOwned(t *testing.T) {
	hijacked := make(chan net.Conn, 1)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" {
			w.WriteHeader(405)
			return
		}
		h, ok := w.(http.Hijacker)
		if !ok {
			t.Error("missing hijacker")
			return
		}
		conn, rw, err := h.Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		if _, err = rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err == nil {
			err = rw.Flush()
		}
		if err != nil {
			t.Errorf("flush: %v", err)
			_ = conn.Close()
			return
		}
		hijacked <- conn
	})
	srv := managedTestServer(t, 4, 4, handler)
	addr, _ := managedServe(t, srv)
	client, err := net.DialTimeout("tcp", addr, time.Second)
	require.NoError(t, err)
	defer func() { require.NoError(t, client.Close()) }()
	require.NoError(t, client.SetDeadline(time.Now().Add(2*time.Second)))
	_, err = io.WriteString(client, "CONNECT local:443 HTTP/1.1\r\nHost: local:443\r\n\r\n")
	require.NoError(t, err)
	br := bufio.NewReader(client)
	line, err := br.ReadString('\n')
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(line, "HTTP/1.1 200"))
	_, err = br.ReadString('\n')
	require.NoError(t, err)
	var serverConn net.Conn
	select {
	case serverConn = <-hijacked:
	case <-time.After(time.Second):
		t.Fatal("CONNECT not hijacked")
	}
	defer func() { require.NoError(t, serverConn.Close()) }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, srv.Shutdown(ctx))
	require.NoError(t, srv.Close())
	// net/http deliberately excludes hijacked connections from Close and Shutdown.
	_, err = serverConn.Write([]byte("alive"))
	require.NoError(t, err)
	b := make([]byte, 5)
	_, err = io.ReadFull(br, b)
	require.NoError(t, err)
	require.Equal(t, "alive", string(b))
}

func TestManagedHTTPServerZeroH2IdleFallsBack(t *testing.T) {
	for _, useReadTimeout := range []bool{false, true} {
		t.Run(fmt.Sprintf("readTimeoutFallback=%t", useReadTimeout), func(t *testing.T) {
			srv := managedTestServer(t, 1, 0, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, err := fmt.Fprint(w, r.RemoteAddr)
				if err != nil {
					t.Errorf("write: %v", err)
				}
			}))
			if useReadTimeout {
				srv.IdleTimeout = 0
				srv.ReadTimeout = time.Second
			}
			addr, _ := managedServe(t, srv)
			client := &http.Client{Transport: managedTransport(t, true)}
			first := managedGet(t, client, "http://"+addr+"/")
			time.Sleep(2400 * time.Millisecond)
			next := managedGet(t, client, "http://"+addr+"/")
			require.NotEqual(t, first, next, "zero configured H2 idle inherits H1 idle, then read timeout")
		})
	}
}

func TestManagedHTTPServerHeaderBudgetClearedForBody(t *testing.T) {
	srv := managedTestServer(t, 4, 4, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(io.LimitReader(r.Body, 2))
		if err != nil {
			t.Errorf("body read: %v", err)
			w.WriteHeader(500)
			return
		}
		_, err = w.Write(b)
		if err != nil {
			t.Errorf("write: %v", err)
		}
	}))
	srv.ReadHeaderTimeout = 200 * time.Millisecond
	addr, _ := managedServe(t, srv)
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	require.NoError(t, err)
	defer func() { require.NoError(t, conn.Close()) }()
	require.NoError(t, conn.SetDeadline(time.Now().Add(2*time.Second)))
	_, err = io.WriteString(conn, "POST / HTTP/1.1\r\nHost: local\r\nContent-Length: 1\r\n\r\n")
	require.NoError(t, err)
	time.Sleep(450 * time.Millisecond)
	_, err = io.WriteString(conn, "x")
	require.NoError(t, err)
	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err)
	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, 200, res.StatusCode)
	require.Equal(t, "x", string(b))
}

func TestManagedHTTPServerPendingClassifiersDoNotBlockHealthyRequests(t *testing.T) {
	srv := managedTestServer(t, 4, 4, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	srv.ReadHeaderTimeout = time.Second
	addr, _ := managedServe(t, srv)
	pending := make([]net.Conn, 0, 65)
	defer func() {
		for _, conn := range pending {
			require.NoError(t, conn.Close())
		}
	}()
	for i := 0; i < 65; i++ {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		require.NoError(t, err)
		pending = append(pending, conn)
	}
	for _, h2 := range []bool{false, true} {
		start := time.Now()
		managedGet(t, &http.Client{Transport: managedTransport(t, h2)}, "http://"+addr+"/")
		require.Less(t, time.Since(start), 600*time.Millisecond, "pending unclassified sockets must not serialize admission")
	}
}

func TestManagedHTTPServerShutdownBeforeServeAndRepeatedClose(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("h2c=%t", enabled), func(t *testing.T) {
			cfg := ingressTestConfig()
			cfg.Server.H2C.Enabled = enabled
			srv := ProvideHTTPServer(cfg, gin.New())
			require.NoError(t, srv.Shutdown(context.Background()))
			require.NoError(t, srv.Shutdown(context.Background()))
			require.NoError(t, srv.Close())
			require.NoError(t, srv.Close())
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			defer func() { _ = ln.Close() }()
			require.ErrorIs(t, srv.Serve(ln), http.ErrServerClosed)
		})
	}
}

type managedTemporaryAcceptError struct{}

func (managedTemporaryAcceptError) Error() string   { return "synthetic temporary accept" }
func (managedTemporaryAcceptError) Temporary() bool { return true }
func (managedTemporaryAcceptError) Timeout() bool   { return false }

type managedTemporaryListener struct {
	net.Listener
	mu             sync.Mutex
	times          []time.Time
	repeat, closed bool
}

func (l *managedTemporaryListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil, net.ErrClosed
	}
	l.times = append(l.times, time.Now())
	temporary := l.repeat || len(l.times) <= 3
	l.mu.Unlock()
	if temporary {
		return nil, managedTemporaryAcceptError{}
	}
	return l.Listener.Accept()
}
func (l *managedTemporaryListener) Close() error {
	l.mu.Lock()
	l.closed = true
	l.mu.Unlock()
	return l.Listener.Close()
}
func (l *managedTemporaryListener) timestamps() []time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]time.Time(nil), l.times...)
}

func TestManagedHTTPServerTemporaryAcceptBackoffAndStop(t *testing.T) {
	t.Run("recovers after backoff", func(t *testing.T) {
		srv := managedTestServer(t, 4, 4, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		wrapper := &managedTemporaryListener{Listener: ln}
		done := make(chan error, 1)
		go func() { done <- srv.Serve(wrapper) }()
		defer func() { require.NoError(t, srv.Close()); require.ErrorIs(t, <-done, http.ErrServerClosed) }()
		managedGet(t, &http.Client{Transport: managedTransport(t, false)}, "http://"+ln.Addr().String()+"/")
		times := wrapper.timestamps()
		require.GreaterOrEqual(t, len(times), 4)
		for i, min := range []time.Duration{3 * time.Millisecond, 7 * time.Millisecond, 15 * time.Millisecond} {
			require.GreaterOrEqual(t, times[i+1].Sub(times[i]), min, "temporary Accept must back off rather than spin")
		}
	})
	t.Run("close interrupts waiting backoff", func(t *testing.T) {
		srv := managedTestServer(t, 4, 4, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		wrapper := &managedTemporaryListener{Listener: ln, repeat: true}
		done := make(chan error, 1)
		go func() { done <- srv.Serve(wrapper) }()
		require.Eventually(t, func() bool { return len(wrapper.timestamps()) >= 6 }, time.Second, 5*time.Millisecond)
		start := time.Now()
		require.NoError(t, srv.Close())
		select {
		case err := <-done:
			require.ErrorIs(t, err, http.ErrServerClosed)
		case <-time.After(150 * time.Millisecond):
			t.Fatal("Close did not interrupt temporary Accept backoff")
		}
		require.Less(t, time.Since(start), 150*time.Millisecond)
	})
}

func TestManagedHTTPServerUnexpectedChildFailureClosesSiblings(t *testing.T) {
	for _, queueFailure := range []bool{false, true} {
		t.Run(fmt.Sprintf("queueFailure=%t", queueFailure), func(t *testing.T) {
			started := make(chan struct{}, 2)
			canceled := make(chan struct{}, 2)
			srv := managedTestServer(t, 4, 4, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				started <- struct{}{}
				<-r.Context().Done()
				canceled <- struct{}{}
			}))
			addr, done := managedServe(t, srv)
			results := make(chan error, 2)
			for _, h2 := range []bool{false, true} {
				client := &http.Client{Transport: managedTransport(t, h2)}
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
					defer cancel()
					req, err := http.NewRequestWithContext(ctx, "GET", "http://"+addr+"/", nil)
					if err != nil {
						results <- err
						return
					}
					res, err := client.Do(req)
					if res != nil {
						_ = res.Body.Close()
					}
					results <- err
				}()
			}
			for i := 0; i < 2; i++ {
				select {
				case <-started:
				case <-time.After(2 * time.Second):
					t.Fatal("both protocols did not start")
				}
			}
			pending, err := net.DialTimeout("tcp", addr, time.Second)
			require.NoError(t, err)
			defer func() { require.NoError(t, pending.Close()) }()
			require.Eventually(t, func() bool { srv.mu.Lock(); defer srv.mu.Unlock(); return len(srv.pending) > 0 }, time.Second, 5*time.Millisecond)
			// Inject only an owned listener/server lifecycle failure, never malformed traffic.
			srv.mu.Lock()
			child, queue := srv.children[0], srv.queues[1]
			srv.mu.Unlock()
			if queueFailure {
				require.NoError(t, queue.Close())
			} else {
				require.NoError(t, child.Close())
			}
			select {
			case err := <-done:
				require.Error(t, err)
				require.False(t, errors.Is(err, http.ErrServerClosed), "unexpected child failure must not look like ordinary owner shutdown")
			case <-time.After(time.Second):
				t.Fatal("Serve did not report unexpected child failure")
			}
			for i := 0; i < 2; i++ {
				select {
				case <-canceled:
				case <-time.After(time.Second):
					t.Fatal("failure did not cancel sibling handler")
				}
				select {
				case err := <-results:
					require.Error(t, err)
				case <-time.After(time.Second):
					t.Fatal("client request did not close after failure")
				}
			}
			srv.mu.Lock()
			pendingCount := len(srv.pending)
			srv.mu.Unlock()
			require.Zero(t, pendingCount, "failure cleanup must drain pending registry")
		})
	}
}

func TestManagedHTTPServerStoppedListenPrioritizesServerClosed(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { require.NoError(t, occupied.Close()) }()
	for _, tlsMode := range []bool{false, true} {
		t.Run(fmt.Sprintf("TLS=%t", tlsMode), func(t *testing.T) {
			srv := managedTestServer(t, 4, 4, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			srv.Addr = occupied.Addr().String()
			require.NoError(t, srv.Shutdown(context.Background()))
			if tlsMode {
				require.ErrorIs(t, srv.ListenAndServeTLS("", ""), http.ErrServerClosed)
			} else {
				require.ErrorIs(t, srv.ListenAndServe(), http.ErrServerClosed)
			}
		})
	}
}

func TestManagedHTTPServerInvalidCertificateListenerOwnership(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("h2c=%t", enabled), func(t *testing.T) {
			cfg := ingressTestConfig()
			cfg.Server.H2C.Enabled = enabled
			certPath := filepath.Join(t.TempDir(), "missing-cert.pem")
			keyPath := filepath.Join(t.TempDir(), "missing-key.pem")
			t.Run("ServeTLS retains caller listener", func(t *testing.T) {
				srv := ProvideHTTPServer(cfg, gin.New())
				srv.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
				ln, err := net.Listen("tcp", "127.0.0.1:0")
				require.NoError(t, err)
				defer func() { _ = ln.Close() }()
				require.Error(t, srv.ServeTLS(ln, certPath, keyPath))
				// A failed certificate setup has not transferred listener ownership to Serve.
				conn, err := net.DialTimeout("tcp", ln.Addr().String(), time.Second)
				require.NoError(t, err, "ServeTLS certificate error must preserve caller listener")
				require.NoError(t, conn.Close())
				done := make(chan error, 1)
				go func() { done <- srv.Serve(ln) }()
				defer func() {
					ctx, cancel := context.WithTimeout(context.Background(), time.Second)
					defer cancel()
					require.NoError(t, srv.Shutdown(ctx))
					select {
					case err := <-done:
						require.ErrorIs(t, err, http.ErrServerClosed)
					case <-time.After(time.Second):
						t.Error("reused listener did not shut down")
					}
				}()
				tr := &http.Transport{}
				defer tr.CloseIdleConnections()
				client := &http.Client{Transport: tr, Timeout: time.Second}
				res, err := client.Get("http://" + ln.Addr().String() + "/")
				require.NoError(t, err, "caller can reuse its listener after certificate failure")
				require.NoError(t, res.Body.Close())
				require.Equal(t, http.StatusOK, res.StatusCode)
			})
			t.Run("ListenAndServeTLS releases owned listener", func(t *testing.T) {
				reservation, err := net.Listen("tcp", "127.0.0.1:0")
				require.NoError(t, err)
				addr := reservation.Addr().String()
				require.NoError(t, reservation.Close())
				srv := ProvideHTTPServer(cfg, gin.New())
				srv.Addr = addr
				require.Error(t, srv.ListenAndServeTLS(certPath, keyPath))
				rebound, err := net.Listen("tcp", addr)
				require.NoError(t, err, "ListenAndServeTLS owns and must release listener on certificate failure")
				require.NoError(t, rebound.Close())
				require.NoError(t, srv.Close())
			})
		})
	}
}

func TestManagedHTTPServerTLSNilAndEmptyProtocols(t *testing.T) {
	certServer := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	certs := certServer.TLS.Certificates
	certServer.Close()
	for _, empty := range []bool{false, true} {
		t.Run(fmt.Sprintf("emptyProtocols=%t", empty), func(t *testing.T) {
			cfg := ingressTestConfig()
			cfg.Server.H2C.Enabled = true
			srv := ProvideHTTPServer(cfg, gin.New())
			if empty {
				srv.Protocols = new(http.Protocols)
			} else {
				srv.Protocols = nil
			}
			if srv.TLSConfig == nil {
				srv.TLSConfig = &tls.Config{}
			}
			srv.TLSConfig.Certificates = certs
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			done := make(chan error, 1)
			go func() { done <- srv.ServeTLS(ln, "", "") }()
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				require.NoError(t, srv.Shutdown(ctx))
				select {
				case err := <-done:
					require.ErrorIs(t, err, http.ErrServerClosed)
				case <-time.After(time.Second):
					t.Error("TLS server did not return")
				}
			}()
			conn, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", ln.Addr().String(), &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"h2", "http/1.1"}})
			require.NoError(t, err)
			want := "h2"
			if empty {
				want = "http/1.1"
			}
			require.Equal(t, want, conn.ConnectionState().NegotiatedProtocol)
			require.NoError(t, conn.Close())
		})
	}
}
