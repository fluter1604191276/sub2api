package repository

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func http2KeepAliveTestPoolSettings() poolSettings {
	return poolSettings{
		maxIdleConns:          10,
		maxIdleConnsPerHost:   5,
		maxConnsPerHost:       10,
		idleConnTimeout:       90 * time.Second,
		responseHeaderTimeout: time.Minute,
	}
}

// requireHTTP2Configured checks explicit native HTTP/2 protocol selection.
func requireHTTP2Configured(t *testing.T, tr *http.Transport, msg string) {
	t.Helper()
	require.NotNil(t, tr.Protocols, msg)
	require.True(t, tr.Protocols.HTTP2(), msg)
	require.NotNil(t, tr.HTTP2, msg)
}

// 长流 / OpenAI 上游改走 HTTP/2 后，池化连接被代理/NAT 静默掐断会成为“死连接”：
// 两端都以为连接存活，请求落上去会挂到 TCP 重传超时（分钟级）才失败。Go 的
// http2.Transport 默认 ReadIdleTimeout=0（不发健康 PING），无法检测这种死连接。
// 必须显式启用主动 PING 探测，让死连接被提前剔除，而不是只靠 ResponseHeaderTimeout
// 事后兜底。
func TestEnableHTTP2KeepAlive_EnablesPingHealthCheck(t *testing.T) {
	for _, tc := range []struct {
		mode            string
		readIdleTimeout time.Duration
		pingTimeout     time.Duration
	}{
		{upstreamProtocolModeOpenAIH2, 15 * time.Second, 15 * time.Second},
		{upstreamProtocolModeLongStreamH2, 10 * time.Second, 5 * time.Second},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			tr := &http.Transport{}
			h2 := enableHTTP2KeepAlive(tr, tc.mode)
			require.NotNil(t, h2, "必须返回已配置的 *http.HTTP2Config")
			require.Equal(t, tc.readIdleTimeout, h2.SendPingTimeout)
			require.Equal(t, tc.pingTimeout, h2.PingTimeout, "各模式应使用独立的 PING 应答期限")
			requireHTTP2Configured(t, tr, "http2 必须已挂到底层 http.Transport 上")
		})
	}
}

func TestEnableHTTP2KeepAlivePreservesOtherTransportSettings(t *testing.T) {
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	h2 := &http.HTTP2Config{MaxReadFrameSize: 64 * 1024, StrictMaxConcurrentRequests: true}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	tr := &http.Transport{Protocols: protocols, HTTP2: h2, TLSClientConfig: tlsConfig}
	require.Same(t, h2, enableHTTP2KeepAlive(tr, upstreamProtocolModeOpenAIH2))
	require.Same(t, protocols, tr.Protocols)
	require.False(t, tr.Protocols.HTTP1(), "preserve an explicit HTTP/1 selection")
	require.True(t, tr.Protocols.UnencryptedHTTP2())
	require.True(t, tr.Protocols.HTTP2())
	require.Same(t, tlsConfig, tr.TLSClientConfig)
	require.Equal(t, 64*1024, tr.HTTP2.MaxReadFrameSize)
	require.True(t, tr.HTTP2.StrictMaxConcurrentRequests)
	require.Equal(t, openAIHTTP2ReadIdleTimeout, tr.HTTP2.SendPingTimeout)
	require.Equal(t, openAIHTTP2PingTimeout, tr.HTTP2.PingTimeout)
}

// long_stream_h2 模式构建的 Transport 必须带上 H2 PING 健康探测，从源头剔除死连接。
func TestBuildUpstreamTransport_LongStreamH2_EnablesPingHealthCheck(t *testing.T) {
	tr, err := buildUpstreamTransport(http2KeepAliveTestPoolSettings(), nil, upstreamProtocolModeLongStreamH2)
	require.NoError(t, err)
	require.True(t, tr.ForceAttemptHTTP2, "long_stream_h2 必须启用 HTTP/2")
	requireHTTP2Configured(t, tr, "long_stream_h2 必须显式配置 http2 以启用 ReadIdleTimeout")
}

// 默认、Grok 和显式 H1 模式不应主动启用 HTTP/2 保活，避免影响其他平台的传输策略。
func TestBuildUpstreamTransport_NonHTTP2_NotEagerlyConfigured(t *testing.T) {
	for _, mode := range []string{upstreamProtocolModeDefault, upstreamProtocolModeGrok, upstreamProtocolModeOpenAIH1, upstreamProtocolModeOpenAIH1Fallback} {
		t.Run(mode, func(t *testing.T) {
			tr, err := buildUpstreamTransport(http2KeepAliveTestPoolSettings(), nil, mode)
			require.NoError(t, err)
			require.Nil(t, tr.Protocols, "非 H2 模式不应主动配置 http2 keepalive")
			require.Nil(t, tr.HTTP2, "非 H2 模式不应主动配置 PING")
			require.Nil(t, tr.TLSNextProto["h2"], "非 H2 模式不应主动配置 http2 keepalive")
		})
	}
}

// 使用同时支持 H1/H2 的 TLS 上游核对实际协议，确保保活配置不会改变其他模式的协商行为。
func TestBuildUpstreamTransport_NegotiatesExpectedProtocol(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()
	for _, tc := range []struct {
		mode      string
		wantMajor int
	}{
		{upstreamProtocolModeOpenAIH2, 2},
		{upstreamProtocolModeLongStreamH2, 2},
		{upstreamProtocolModeDefault, 1},
		{upstreamProtocolModeGrok, 1},
		{upstreamProtocolModeOpenAIH1, 1},
		{upstreamProtocolModeOpenAIH1Fallback, 1},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			tr, err := buildUpstreamTransport(http2KeepAliveTestPoolSettings(), nil, tc.mode)
			require.NoError(t, err)
			defer tr.CloseIdleConnections()
			roots := x509.NewCertPool()
			roots.AddCert(srv.Certificate())
			if tr.TLSClientConfig == nil {
				tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
			}
			tr.TLSClientConfig.RootCAs = roots
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
			require.NoError(t, err)
			resp, err := tr.RoundTrip(req)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.Equal(t, tc.wantMajor, resp.ProtoMajor)
		})
	}
}

// 死连接在经 HTTP 代理（CONNECT 隧道）时最高发，这是带 proxy 账号的真实生产路径：
// 显式 http2 配置须与 Transport.Proxy 同时正确生效，不能相互干扰。
func TestBuildUpstreamTransport_HTTP2_WithHTTPProxy_EnablesKeepAlive(t *testing.T) {
	proxyURL, err := url.Parse("http://127.0.0.1:8080")
	require.NoError(t, err)
	for _, mode := range []string{upstreamProtocolModeOpenAIH2, upstreamProtocolModeLongStreamH2} {
		t.Run(mode, func(t *testing.T) {
			tr, err := buildUpstreamTransport(http2KeepAliveTestPoolSettings(), proxyURL, mode)
			require.NoError(t, err)
			require.True(t, tr.ForceAttemptHTTP2)
			requireHTTP2Configured(t, tr, "经代理的 H2 模式也必须启用 http2 keepalive")
			require.NotNil(t, tr.Proxy, "HTTP 代理仍须通过 Transport.Proxy 生效")
		})
	}
}

// Cancellation must reset only the selected stream, preserving a simultaneous
// response and the pooled connection used by the next legitimate request.
func TestNativeHTTP2TransportCancellationPreservesOtherStreamAndReuse(t *testing.T) {
	release := make(chan struct{})
	cancelObserved := make(chan struct{})
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = http.NewResponseController(w).Flush()
		switch r.URL.Path {
		case "/cancel":
			<-r.Context().Done()
			close(cancelObserved)
		case "/held":
			select {
			case <-release:
				_, _ = io.WriteString(w, "held complete")
			case <-r.Context().Done():
			}
		default:
			_, _ = io.WriteString(w, "next complete")
		}
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()
	defer srv.CloseClientConnections()
	tr, err := buildUpstreamTransport(http2KeepAliveTestPoolSettings(), nil, upstreamProtocolModeLongStreamH2)
	require.NoError(t, err)
	defer tr.CloseIdleConnections()
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	ctx, finish := context.WithTimeout(t.Context(), 5*time.Second)
	defer finish()
	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var originalConn net.Conn
	cancelCtx = httptrace.WithClientTrace(cancelCtx, &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { originalConn = info.Conn }})
	req, err := http.NewRequestWithContext(cancelCtx, http.MethodGet, srv.URL+"/cancel", nil)
	require.NoError(t, err)
	canceled, err := tr.RoundTrip(req)
	require.NoError(t, err)
	defer func() { _ = canceled.Body.Close() }()
	require.Equal(t, 2, canceled.ProtoMajor)
	var heldConn net.Conn
	heldCtx := httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { heldConn = info.Conn }})
	req, err = http.NewRequestWithContext(heldCtx, http.MethodGet, srv.URL+"/held", nil)
	require.NoError(t, err)
	held, err := tr.RoundTrip(req)
	require.NoError(t, err)
	defer func() { _ = held.Body.Close() }()
	require.Equal(t, 2, held.ProtoMajor)
	require.Same(t, originalConn, heldConn, "fixture must have two active streams on one connection")
	cancel()
	_, err = io.ReadAll(io.LimitReader(canceled.Body, 1024))
	require.ErrorIs(t, err, context.Canceled)
	select {
	case <-cancelObserved:
	case <-ctx.Done():
		t.Fatal("server did not observe stream cancellation")
	}
	close(release)
	body, err := io.ReadAll(io.LimitReader(held.Body, 1024))
	require.NoError(t, err)
	require.Equal(t, "held complete", string(body))
	require.NoError(t, held.Body.Close())
	var reused bool
	var nextConn net.Conn
	traceCtx := httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
		reused = info.Reused
		nextConn = info.Conn
	}})
	req, err = http.NewRequestWithContext(traceCtx, http.MethodGet, srv.URL+"/next", nil)
	require.NoError(t, err)
	next, err := tr.RoundTrip(req)
	require.NoError(t, err)
	defer func() { _ = next.Body.Close() }()
	body, err = io.ReadAll(io.LimitReader(next.Body, 1024))
	require.NoError(t, err)
	require.Equal(t, "next complete", string(body))
	require.True(t, reused, "stream cancellation must retain the pooled HTTP/2 connection")
	require.Same(t, originalConn, nextConn)
}
