//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type upstreamAttemptWriteFailConn struct{ *stagedPassthroughConn }

func (c *upstreamAttemptWriteFailConn) WriteFrame(context.Context, coderws.MessageType, []byte) error {
	return errors.New("write may have reached upstream")
}

func newWebSocketAttemptService(repo *attemptCaptureRepo) *OpenAIGatewayService {
	return &OpenAIGatewayService{
		httpUpstream: &observedHTTPUpstream{recorder: NewUpstreamAttemptRecorder(repo, nil)},
	}
}

func TestUpstreamAttemptWebSocketRecordsCachedFailureUsageOnce(t *testing.T) {
	repo := &attemptCaptureRepo{}
	svc := newWebSocketAttemptService(repo)
	request := attemptTestRequest(t, "/v1/responses")

	observer := svc.startUpstreamAttemptWebSocket(request.Context(), nil, &Account{ID: 81}, "mapped-model", "priority", "high")
	require.NotNil(t, observer)
	observer.setUpstreamRequestID(http.Header{"X-Request-Id": {"upstream-handshake-id"}})
	observer.observe([]byte(`{"type":"response.failed","response":{"id":"resp_not_a_request_id","usage":{"input_tokens":40,"output_tokens":0,"input_tokens_details":{"cached_tokens":40}}}}`))
	observer.finish()
	observer.finish()

	repo.wait(t, 1)

	require.Len(t, repo.items, 1)
	attempt := repo.items[0]
	require.Equal(t, "failed", attempt.Outcome)
	require.True(t, attempt.UsagePresent)
	require.EqualValues(t, 0, *attempt.InputTokens)
	require.EqualValues(t, 40, *attempt.CacheReadTokens)
	require.EqualValues(t, 0, *attempt.OutputTokens)
	require.Equal(t, "upstream-handshake-id", attempt.UpstreamRequestID)
	require.Equal(t, "req-shared", attempt.RequestID)
	require.Equal(t, "dispatch-shared", attempt.ClientRequestID)
	require.EqualValues(t, 81, attempt.AccountID)
	require.EqualValues(t, 12, *attempt.UserID)
	require.EqualValues(t, 20, *attempt.GroupID)
}

func TestUpstreamAttemptWebSocketInterruptedTurnRetainsUsageAndIdentity(t *testing.T) {
	repo := &attemptCaptureRepo{}
	svc := newWebSocketAttemptService(repo)
	request := attemptTestRequest(t, "/v1/responses")

	first := svc.startUpstreamAttemptWebSocket(request.Context(), nil, &Account{ID: 81}, "mapped-model", "", "")
	first.observe([]byte(`{"type":"response.in_progress","response":{"usage":{"input_tokens":7,"output_tokens":2}}}`))
	// The connection ends before a terminal event. The partial usage is still
	// recorded and the observer marks that turn failed.
	first.finish()

	second := svc.startUpstreamAttemptWebSocket(request.Context(), nil, &Account{ID: 81}, "mapped-model", "", "")
	second.observe([]byte(`{"type":"response.completed","response":{"id":"resp_second","usage":{"input_tokens":0,"output_tokens":0}}}`))
	second.finish()

	repo.wait(t, 2)

	require.Len(t, repo.items, 2)
	require.NotEqual(t, repo.items[0].AttemptID, repo.items[1].AttemptID)
	require.Equal(t, "failed", repo.items[0].Outcome)
	require.True(t, repo.items[0].UsagePresent)
	require.EqualValues(t, 7, *repo.items[0].InputTokens)
	require.EqualValues(t, 2, *repo.items[0].OutputTokens)
	require.Equal(t, "success", repo.items[1].Outcome)
	// A present all-zero usage object is different from absent usage.
	require.True(t, repo.items[1].UsagePresent)
	require.EqualValues(t, 0, *repo.items[1].InputTokens)
	require.EqualValues(t, 0, *repo.items[1].OutputTokens)
	require.Empty(t, repo.items[1].UpstreamRequestID)
	for _, attempt := range repo.items {
		require.Equal(t, "req-shared", attempt.RequestID)
		require.Equal(t, "dispatch-shared", attempt.ClientRequestID)
	}
}

func TestForwardOpenAIWSV2RecordsAttemptAndResponseDiagnostics(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &attemptCaptureRepo{}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
	cfg.Gateway.OpenAIWS.QueueLimitPerConn = 4
	cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
	pool := newOpenAIWSConnPool(cfg)
	pool.setClientDialerForTest(&openAIWSCaptureDialer{conn: &openAIWSCaptureConn{events: [][]byte{
		[]byte(`{"type":"response.completed","response":{"id":"resp_attempt_ws","status":"completed","usage":{"input_tokens":7,"output_tokens":3}}}`),
	}}})
	svc := &OpenAIGatewayService{
		cfg:              cfg,
		httpUpstream:     NewObservedHTTPUpstream(&httpUpstreamRecorder{}, NewUpstreamAttemptRecorder(repo, nil)),
		cache:            &stubGatewayCache{},
		openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
		toolCorrector:    NewCodexToolCorrector(),
		openaiWSPool:     pool,
	}
	account := &Account{ID: 81, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test"}, Extra: map[string]any{"responses_websockets_v2_enabled": true}}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	result, err := svc.Forward(context.Background(), c, account, []byte(`{"model":"gpt-5.5","stream":false,"max_output_tokens":128,"input":"hello"}`))
	require.NoError(t, err)
	require.NotNil(t, result)
	repo.wait(t, 1)
	require.Len(t, repo.items, 1)
	attempt := repo.items[0]
	require.Equal(t, int64(81), attempt.AccountID)
	require.Equal(t, "responses", attempt.Protocol)
	require.Equal(t, "success", attempt.Outcome)
	require.EqualValues(t, 7, *attempt.InputTokens)
	require.EqualValues(t, 3, *attempt.OutputTokens)

	diagnostic, ok := OpenAIResponseDiagnosticSnapshotFromContext(c)
	require.True(t, ok)
	require.True(t, diagnostic.Active)
	require.Equal(t, int64(81), diagnostic.AccountID)
	require.True(t, diagnostic.Terminal)
	require.Equal(t, "completed", diagnostic.Status)
	require.EqualValues(t, 7, *diagnostic.InputTokens)
	require.EqualValues(t, 3, *diagnostic.OutputTokens)
}

func TestPassthroughResponsesWebSocketRecordsEachTurnAndDiagnostics(t *testing.T) {
	gin.SetMode(gin.TestMode)
	controlCtx, cancelControl := context.WithCancel(context.Background())
	defer cancelControl()
	upstream := newStagedPassthroughConn()
	repo := &attemptCaptureRepo{}
	svc := newPassthroughLifecycleService(passthroughLifecycleConfig(), upstream)
	svc.httpUpstream = NewObservedHTTPUpstream(&httpUpstreamRecorder{}, NewUpstreamAttemptRecorder(repo, nil))
	var capturedContext *gin.Context
	var diagnosticsMu sync.Mutex
	var publishedDiagnostics []OpenAIResponseDiagnosticSnapshot
	server, serverErr := startPassthroughLifecycleServerWithHooks(t, controlCtx, svc, passthroughLifecycleAccount(), func(c *gin.Context) *OpenAIWSIngressHooks {
		capturedContext = c
		BindOpenAIResponseDiagnosticSink(c, func(snapshot OpenAIResponseDiagnosticSnapshot) {
			diagnosticsMu.Lock()
			publishedDiagnostics = append(publishedDiagnostics, snapshot)
			diagnosticsMu.Unlock()
		})
		return nil
	})
	defer server.Close()
	clientConn := dialPassthroughLifecycleClientWithPayload(t, server, `{"type":"response.create","model":"gpt-5.1","stream":false,"max_output_tokens":11}`)
	defer func() { _ = clientConn.CloseNow() }()
	require.Equal(t, "response.create", gjson.GetBytes(requirePassthroughUpstreamWrite(t, upstream, time.Second), "type").String())
	upstream.Send(`{"type":"response.incomplete","response":{"id":"resp_a","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":2,"output_tokens":1}}}`)
	_, err := readPassthroughLifecycleFrame(t, clientConn, time.Second)
	require.NoError(t, err)

	writeCtx, cancelWrite := context.WithTimeout(context.Background(), time.Second)
	err = clientConn.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.1","stream":false,"max_output_tokens":22}`))
	cancelWrite()
	require.NoError(t, err)
	require.Equal(t, "response.create", gjson.GetBytes(requirePassthroughUpstreamWrite(t, upstream, time.Second), "type").String())
	upstream.Send(`{"type":"response.completed","response":{"id":"resp_b","status":"completed","usage":{"input_tokens":5,"output_tokens":3}}}`)
	_, err = readPassthroughLifecycleFrame(t, clientConn, time.Second)
	require.NoError(t, err)

	repo.wait(t, 2)
	require.Len(t, repo.items, 2)
	for _, attempt := range repo.items {
		require.Equal(t, "responses", attempt.Protocol)
		require.Equal(t, int64(901), attempt.AccountID)
	}
	require.Equal(t, "incomplete", repo.items[0].Outcome)
	require.Equal(t, "success", repo.items[1].Outcome)
	require.NotEqual(t, repo.items[0].AttemptID, repo.items[1].AttemptID)
	require.EqualValues(t, 2, *repo.items[0].InputTokens)
	require.EqualValues(t, 5, *repo.items[1].InputTokens)
	require.NotNil(t, capturedContext)
	diagnosticsMu.Lock()
	published := append([]OpenAIResponseDiagnosticSnapshot(nil), publishedDiagnostics...)
	diagnosticsMu.Unlock()
	require.Len(t, published, 2)
	require.Equal(t, int64(901), published[0].AccountID)
	require.Equal(t, "resp_a", published[0].ResponseID)
	require.Equal(t, "incomplete", published[0].Status)
	require.EqualValues(t, 11, *published[0].RequestedMaxOutputTokens)
	require.EqualValues(t, 11, *published[0].ForwardedMaxOutputTokens)
	require.Equal(t, int64(901), published[1].AccountID)
	require.Equal(t, "resp_b", published[1].ResponseID)
	require.Equal(t, "completed", published[1].Status)
	require.EqualValues(t, 22, *published[1].RequestedMaxOutputTokens)
	require.EqualValues(t, 22, *published[1].ForwardedMaxOutputTokens)
	// A duplicate formal terminal after a completed WS turn cannot re-publish.
	observeOpenAIResponseDiagnosticTerminal(capturedContext, []byte(`{"type":"response.completed","response":{"id":"resp_b","status":"completed"}}`), "response.completed", nil)
	diagnosticsMu.Lock()
	require.Len(t, publishedDiagnostics, 2)
	diagnosticsMu.Unlock()
	_, publishedByFallback := TakeOpenAIResponseDiagnosticSnapshot(capturedContext)
	require.False(t, publishedByFallback)
	diagnostic, ok := OpenAIResponseDiagnosticSnapshotFromContext(capturedContext)
	require.True(t, ok)
	require.True(t, diagnostic.Active)
	require.Equal(t, int64(901), diagnostic.AccountID)
	require.True(t, diagnostic.Terminal)
	require.EqualValues(t, 5, *diagnostic.InputTokens)
	require.EqualValues(t, 3, *diagnostic.OutputTokens)

	_ = clientConn.CloseNow()
	cancelControl()
	select {
	case <-serverErr:
	case <-time.After(3 * time.Second):
		t.Fatal("passthrough server did not exit")
	}
}

func TestPassthroughResponsesWebSocketWriteFailureRecordsPossibleAttempt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	controlCtx, cancelControl := context.WithCancel(context.Background())
	defer cancelControl()
	repo := &attemptCaptureRepo{}
	failingUpstream := &upstreamAttemptWriteFailConn{stagedPassthroughConn: newStagedPassthroughConn()}
	svc := newPassthroughLifecycleService(passthroughLifecycleConfig(), failingUpstream.stagedPassthroughConn)
	svc.openaiWSPassthroughDialer = &stagedPassthroughDialer{conn: failingUpstream}
	svc.httpUpstream = NewObservedHTTPUpstream(&httpUpstreamRecorder{}, NewUpstreamAttemptRecorder(repo, nil))
	server, serverErr := startPassthroughLifecycleServer(t, controlCtx, svc, passthroughLifecycleAccount())
	defer server.Close()
	clientConn := dialPassthroughLifecycleClient(t, server)
	defer func() { _ = clientConn.CloseNow() }()

	repo.wait(t, 1)
	require.Len(t, repo.items, 1)
	require.Equal(t, "failed", repo.items[0].Outcome)
	require.Equal(t, "responses", repo.items[0].Protocol)
	require.Equal(t, int64(901), repo.items[0].AccountID)
	select {
	case err := <-serverErr:
		require.Error(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("write-failure passthrough server did not exit")
	}
}
