//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	pluginv1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	hcplugin "github.com/hashicorp/go-plugin"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type channelMonitorV3CountingWSDialer struct{ calls atomic.Int32 }

func (d *channelMonitorV3CountingWSDialer) Dial(context.Context, string, http.Header, string) (openAIWSClientConn, int, http.Header, error) {
	d.calls.Add(1)
	return nil, 0, nil, errors.New("unexpected websocket dial")
}

type channelMonitorV3CountingHTTPUpstream struct{ calls atomic.Int32 }

func (u *channelMonitorV3CountingHTTPUpstream) Do(*http.Request, string, int64, int) (*http.Response, error) {
	u.calls.Add(1)
	return nil, errors.New("unexpected HTTP upstream")
}

func (u *channelMonitorV3CountingHTTPUpstream) DoWithTLS(request *http.Request, proxyURL string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(request, proxyURL, accountID, concurrency)
}

// channelMonitorV3CountingPluginClient implements the actual Forward RPC edge
// while embedding the stable generated methods that this test does not use.
type channelMonitorV3CountingPluginClient struct {
	pluginv1.TransportPluginClient
	forwardCalls atomic.Int32
}

func (c *channelMonitorV3CountingPluginClient) Forward(context.Context, ...grpc.CallOption) (grpc.BidiStreamingClient[pluginv1.ForwardRequest, pluginv1.ForwardResponse], error) {
	c.forwardCalls.Add(1)
	return nil, errors.New("unexpected plugin Forward RPC")
}

func trustedChannelMonitorV3TransportContext() context.Context {
	return context.WithValue(context.Background(), channelMonitorV3RequestContextKey{}, true)
}

func channelMonitorV3TransportConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	cfg.Gateway.OpenAIWS.IngressModeDefault = OpenAIWSIngressModePassthrough
	cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 1
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 1
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 1
	return cfg
}

func channelMonitorV3WSAccount() *Account {
	return &Account{
		ID: 9301, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test"},
		Extra:       map[string]any{"responses_websockets_v2_enabled": true, "openai_apikey_responses_websockets_v2_mode": OpenAIWSIngressModePassthrough},
	}
}

func TestChannelMonitorV3TransportGuardRejectsWSV2BeforeDial(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := channelMonitorV3TransportConfig()
	dialer := &channelMonitorV3CountingWSDialer{}
	pool := newOpenAIWSConnPool(cfg)
	pool.setClientDialerForTest(dialer)
	svc := &OpenAIGatewayService{cfg: cfg, openaiWSPool: pool, openaiWSResolver: NewOpenAIWSProtocolResolver(cfg)}
	account := channelMonitorV3WSAccount()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	recoveryTried := false

	for turn := 0; turn < 3; turn++ {
		result, err := svc.forwardOpenAIWSV2(
			trustedChannelMonitorV3TransportContext(), c, account,
			map[string]any{"model": "gpt-5.5", "type": "response.create"}, "", "", "sk-test",
			OpenAIWSProtocolDecision{}, false, false, "gpt-5.5", "gpt-5.5", time.Now(), turn, "", &recoveryTried,
		)
		require.ErrorContains(t, err, "requires native HTTP upstream transport")
		require.Nil(t, result)
	}
	require.Zero(t, dialer.calls.Load(), "V3 must reject before any WS dial")
}

func TestChannelMonitorV3TransportGuardRejectsPassthroughBeforeDialOrWrite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for turn := 0; turn < 3; turn++ {
		dialer := &channelMonitorV3CountingWSDialer{}
		cfg := channelMonitorV3TransportConfig()
		svc := &OpenAIGatewayService{
			cfg:                       cfg,
			openaiWSResolver:          NewOpenAIWSProtocolResolver(cfg),
			openaiWSPassthroughDialer: dialer,
		}
		serverErr := make(chan error, 1)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			client, err := coderws.Accept(w, request, nil)
			if err != nil {
				serverErr <- err
				return
			}
			defer client.CloseNow()
			_, first, err := ReadOpenAIWSClientMessage(trustedChannelMonitorV3TransportContext(), client, time.Second, coderws.StatusPolicyViolation, "missing request")
			if err != nil {
				serverErr <- err
				return
			}
			ginRecorder := httptest.NewRecorder()
			ginCtx, _ := gin.CreateTestContext(ginRecorder)
			ginCtx.Request = request.WithContext(trustedChannelMonitorV3TransportContext())
			serverErr <- svc.ProxyResponsesWebSocketFromClient(trustedChannelMonitorV3TransportContext(), ginCtx, client, channelMonitorV3WSAccount(), "sk-test", first, nil)
		}))

		clientCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		client, _, err := coderws.Dial(clientCtx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
		cancel()
		require.NoError(t, err)
		writeCtx, cancelWrite := context.WithTimeout(context.Background(), time.Second)
		err = client.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.5"}`))
		cancelWrite()
		require.NoError(t, err)
		select {
		case err := <-serverErr:
			require.ErrorContains(t, err, "requires native HTTP upstream transport")
		case <-time.After(time.Second):
			t.Fatal("passthrough V3 guard did not return")
		}
		_ = client.CloseNow()
		server.Close()
		require.Zero(t, dialer.calls.Load(), "V3 must reject before passthrough dial/write")
	}
}

func TestChannelMonitorV3TransportGuardRejectsPluginRouteBeforeHTTPFallback(t *testing.T) {
	pluginClient := &channelMonitorV3CountingPluginClient{}
	manager := &PluginManager{}
	// NewClient provides the synchronization state Exited needs; no plugin is
	// started. If the V3 guard moves below RoundTrip, its real Forward edge is
	// reached and increments pluginClient.forwardCalls.
	runtime := &pluginRuntime{
		client: hcplugin.NewClient(&hcplugin.ClientConfig{}),
		api:    pluginClient,
		done:   make(chan struct{}),
	}
	manager.route.Store(&pluginRoute{pluginID: 1, runtime: runtime, rolloutPercent: 100})
	upstream := &channelMonitorV3CountingHTTPUpstream{}
	svc := &OpenAIGatewayService{pluginManager: manager, httpUpstream: upstream}
	account := &Account{ID: 9302, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 1}

	for retry := 0; retry < 3; retry++ {
		req, err := http.NewRequestWithContext(trustedChannelMonitorV3TransportContext(), http.MethodPost, "https://upstream.invalid/v1/responses", nil)
		require.NoError(t, err)
		response, err := svc.doOpenAIUpstream(req, "", account)
		require.ErrorContains(t, err, "does not admit OAuth plugin transport")
		require.Nil(t, response)
	}
	require.Zero(t, pluginClient.forwardCalls.Load(), "V3 must reject before plugin Forward RPC")
	require.Zero(t, upstream.calls.Load(), "V3 must not fall through to HTTP after a plugin route match")
}
