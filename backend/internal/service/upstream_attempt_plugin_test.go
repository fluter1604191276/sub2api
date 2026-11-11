//go:build unit

package service

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	pluginv1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
	hcplugin "github.com/hashicorp/go-plugin"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type attemptPluginServer struct {
	pluginv1.UnimplementedTransportPluginServer
	beforeForward func()
}

func (s *attemptPluginServer) Forward(stream grpc.BidiStreamingServer[pluginv1.ForwardRequest, pluginv1.ForwardResponse]) error {
	var start *pluginv1.ForwardRequestStart
	var body bytes.Buffer
	for {
		frame, err := stream.Recv()
		if err != nil {
			return err
		}
		if frame.GetStart() != nil {
			start = frame.GetStart()
		}
		body.Write(frame.GetBodyChunk())
		if frame.GetBodyEnd() {
			break
		}
	}
	if s.beforeForward != nil {
		s.beforeForward()
	}
	req, err := http.NewRequestWithContext(stream.Context(), start.Method, start.Url, &body)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err = stream.Send(&pluginv1.ForwardResponse{Frame: &pluginv1.ForwardResponse_Start{Start: &pluginv1.ForwardResponseStart{StatusCode: int32(resp.StatusCode), Headers: headersToPlugin(resp.Header)}}}); err != nil {
		return err
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if err = stream.Send(&pluginv1.ForwardResponse{Frame: &pluginv1.ForwardResponse_BodyChunk{BodyChunk: raw}}); err != nil {
		return err
	}
	return stream.Send(&pluginv1.ForwardResponse{Frame: &pluginv1.ForwardResponse_End{End: &pluginv1.ForwardResponseEnd{BytesReceived: int64(len(raw))}}})
}

func newAttemptPluginManager(t *testing.T) *PluginManager {
	return newAttemptPluginManagerWithServer(t, &attemptPluginServer{})
}

func newAttemptPluginManagerWithServer(t *testing.T, pluginServer *attemptPluginServer) *PluginManager {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	pluginv1.RegisterTransportPluginServer(server, pluginServer)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	t.Cleanup(func() { _ = listener.Close() })
	conn, err := grpc.NewClient("passthrough:///attempt-plugin", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	runtime := &pluginRuntime{client: hcplugin.NewClient(&hcplugin.ClientConfig{}), api: pluginv1.NewTransportPluginClient(conn), done: make(chan struct{})}
	manager := &PluginManager{}
	manager.route.Store(&pluginRoute{pluginID: 1, rolloutPercent: 100, runtime: runtime})
	return manager
}

func TestUpstreamAttemptPluginUnavailableDoesNotCreatePhantomAttempt(t *testing.T) {
	repo := &attemptCaptureRepo{}
	legacy := &pluginRoutingHTTPUpstream{}
	upstream := NewObservedHTTPUpstream(legacy, NewUpstreamAttemptRecorder(repo, nil))
	manager := &PluginManager{}
	manager.route.Store(&pluginRoute{pluginID: 1, rolloutPercent: 100, unavailable: "local runtime unavailable"})
	gateway := &OpenAIGatewayService{httpUpstream: upstream, pluginManager: manager}
	account := &Account{ID: 81, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 1}

	resp, err := gateway.doOpenAIUpstream(attemptTestRequest(t, "/v1/responses"), "", account)
	require.Error(t, err)
	require.Nil(t, resp)
	require.Zero(t, legacy.doCalls)
	repo.mu.Lock()
	require.Empty(t, repo.items)
	repo.mu.Unlock()
}

func TestUpstreamAttemptPluginKeepsCapturedRouteDuringRPC(t *testing.T) {
	var manager *PluginManager
	pluginServer := &attemptPluginServer{beforeForward: func() {
		// Simulate reconciliation publishing a new unavailable route while this
		// already-dispatched RPC is in flight. The request must retain the
		// route/runtime captured before dispatch and finish its own ledger row.
		manager.route.Store(&pluginRoute{pluginID: 2, rolloutPercent: 100, unavailable: "replacement starting"})
	}}
	manager = newAttemptPluginManagerWithServer(t, pluginServer)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"usage":{"input_tokens":4,"output_tokens":2}}`)
	}))
	t.Cleanup(provider.Close)
	repo := &attemptCaptureRepo{}
	legacy := &pluginRoutingHTTPUpstream{}
	upstream := NewObservedHTTPUpstream(legacy, NewUpstreamAttemptRecorder(repo, nil))
	gateway := &OpenAIGatewayService{httpUpstream: upstream, pluginManager: manager}
	account := &Account{ID: 81, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 1}
	req, err := http.NewRequestWithContext(attemptTestRequest(t, "/v1/responses").Context(), http.MethodPost, provider.URL+"/v1/responses", bytes.NewBufferString(`{"model":"gpt-test"}`))
	require.NoError(t, err)

	resp, err := gateway.doOpenAIUpstream(req, "", account)
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	repo.wait(t, 1)
	require.Equal(t, "success", repo.items[0].Outcome)
	require.Equal(t, int64(81), repo.items[0].AccountID)
	require.Zero(t, legacy.doCalls)
	route := manager.route.Load()
	require.NotNil(t, route)
	require.Equal(t, int64(2), route.pluginID)
	require.Nil(t, route.runtime)
}

func TestUpstreamAttemptPluginRecordsEveryGatewayAndProbeRoundTrip(t *testing.T) {
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-Id", "same-provider-request")
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
		}
		_, _ = io.WriteString(w, `{"usage":{"input_tokens":12,"output_tokens":3}}`)
	}))
	t.Cleanup(provider.Close)
	repo := &attemptCaptureRepo{}
	legacy := &pluginRoutingHTTPUpstream{}
	calculator := NewUpstreamAttemptCostCalculator(attemptCostAccountRepo{multiplier: .2}, nil, newTestBillingServiceWithPrices(map[string]*ModelPricing{"claude-sonnet-4": {InputPricePerToken: .001, OutputPricePerToken: .002}}))
	upstream := NewObservedHTTPUpstream(legacy, NewUpstreamAttemptRecorder(repo, calculator))
	manager := newAttemptPluginManager(t)
	gateway := &OpenAIGatewayService{httpUpstream: upstream, pluginManager: manager}
	probe := &AccountTestService{httpUpstream: upstream, pluginManager: manager}
	account := &Account{ID: 81, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 1}
	for i := range 2 {
		req := attemptTestRequest(t, "/v1/responses")
		target, err := http.NewRequestWithContext(req.Context(), http.MethodPost, provider.URL+"/v1/responses", bytes.NewBufferString(`{"model":"claude-sonnet-4","max_output_tokens":128}`))
		require.NoError(t, err)
		req = target
		var resp *http.Response
		if i == 0 {
			resp, err = gateway.doOpenAIUpstream(req, "", account)
		} else {
			resp, err = probe.doOpenAIAccountTestUpstream(req, "", account, false)
		}
		require.NoError(t, err)
		_, err = io.Copy(io.Discard, resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
	}
	repo.wait(t, 2)
	require.EqualValues(t, 2, calls.Load())
	require.Zero(t, legacy.doCalls)
	require.NotEqual(t, repo.items[0].AttemptID, repo.items[1].AttemptID)
	for _, attempt := range repo.items {
		require.Equal(t, "same-provider-request", attempt.UpstreamRequestID)
		require.Equal(t, "known", attempt.CostStatus)
		require.InDelta(t, .0036, *attempt.CostUSD, 1e-10)
	}
}
