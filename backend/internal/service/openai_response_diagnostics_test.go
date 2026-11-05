package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIResponseDiagnosticsCapturesOnlyPresentScalarUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	beginOpenAIResponseDiagnostics(c, []byte(`{"max_output_tokens":4096}`))
	observeOpenAIResponseDiagnosticForwarded(c, []byte(`{"max_output_tokens":2048}`))
	payload := []byte(`{"type":"response.incomplete","response":{"id":"resp_test","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":12,"output_tokens":3,"input_tokens_details":{"cached_tokens":7}}}}`)
	observeOpenAIResponseDiagnosticUsage(c, payload)
	observeOpenAIResponseDiagnosticTerminal(c, payload, "response.incomplete", nil)

	got, ok := OpenAIResponseDiagnosticSnapshotFromContext(c)
	require.True(t, ok)
	require.True(t, got.Active)
	require.Equal(t, "resp_test", got.ResponseID)
	require.Equal(t, "incomplete", got.Status)
	require.Equal(t, "max_output_tokens", got.Reason)
	require.EqualValues(t, 4096, *got.RequestedMaxOutputTokens)
	require.EqualValues(t, 2048, *got.ForwardedMaxOutputTokens)
	require.EqualValues(t, 12, *got.InputTokens)
	require.EqualValues(t, 7, *got.CacheReadTokens)
	require.EqualValues(t, 3, *got.OutputTokens)
	require.Nil(t, got.CacheCreationTokens)
	require.Nil(t, got.ReasoningTokens)
}

func TestOpenAIResponseDiagnosticsStreamingIncompletePreservesWireBytes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	input := []byte(`{"model":"gpt-test","stream":true,"max_output_tokens":64}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(input)))
	beginOpenAIResponseDiagnostics(c, input)
	observeOpenAIResponseDiagnosticForwarded(c, input)
	observeOpenAIResponseDiagnosticHeaders(c, "upstream_incomplete")
	wire := "event: response.incomplete\n" +
		`data: {"type":"response.incomplete","response":{"id":"resp_incomplete","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":9,"output_tokens":4}}}` + "\n\n"
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"upstream_incomplete"}}, Body: io.NopCloser(strings.NewReader(wire))}
	svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}, toolCorrector: NewCodexToolCorrector()}
	_, err := svc.handleStreamingResponse(context.Background(), resp, c, &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, time.Now(), "gpt-test", "gpt-test")
	require.NoError(t, err)
	// The existing Responses normalizer reconstructs output:[] for this compact
	// terminal. Diagnostic capture must not add protocol fields beyond that
	// established transformation.
	require.JSONEq(t, `{"type":"response.incomplete","response":{"id":"resp_incomplete","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":9,"output_tokens":4},"output":[]}}`, strings.TrimPrefix(strings.TrimSuffix(strings.TrimSpace(recorder.Body.String()), "\n\n"), "event: response.incomplete\ndata: "))
	got, ok := OpenAIResponseDiagnosticSnapshotFromContext(c)
	require.True(t, ok)
	require.Equal(t, "incomplete", got.Status)
	require.Equal(t, "max_output_tokens", got.Reason)
	require.Equal(t, "resp_incomplete", got.ResponseID)
	require.Equal(t, "upstream_incomplete", got.UpstreamRequestID)
	require.EqualValues(t, 9, *got.InputTokens)
	require.EqualValues(t, 4, *got.OutputTokens)
}

func TestOpenAIResponseDiagnosticsStreamingFailedCapturedBeforeEarlyReturn(t *testing.T) {
	gin.SetMode(gin.TestMode)
	input := []byte(`{"model":"gpt-test","stream":true}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(input)))
	beginOpenAIResponseDiagnostics(c, input)
	observeOpenAIResponseDiagnosticForwarded(c, input)
	wire := "event: response.failed\n" +
		`data: {"type":"response.failed","response":{"id":"resp_failed","status":"failed","error":{"code":"upstream_error"},"usage":{"input_tokens":5}}}` + "\n\n"
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(wire))}
	svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}, toolCorrector: NewCodexToolCorrector()}
	_, _ = svc.handleStreamingResponse(context.Background(), resp, c, &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, time.Now(), "gpt-test", "gpt-test")
	got, ok := OpenAIResponseDiagnosticSnapshotFromContext(c)
	require.True(t, ok)
	require.Equal(t, "failed", got.Status)
	require.Equal(t, "upstream_error", got.Reason)
	require.Equal(t, "resp_failed", got.ResponseID)
}

func TestOpenAIResponseDiagnosticsNonStreamingCompletedAndAbsentUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	input := []byte(`{"model":"gpt-test","stream":false}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(input)))
	beginOpenAIResponseDiagnostics(c, input)
	observeOpenAIResponseDiagnosticForwarded(c, input)
	body := `{"id":"resp_complete","status":"completed","output":[],"usage":{"input_tokens":3,"output_tokens":0,"input_tokens_details":{"cached_tokens":1}}}`
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, toolCorrector: NewCodexToolCorrector()}
	_, err := svc.handleNonStreamingResponse(context.Background(), resp, c, &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, "gpt-test", "gpt-test")
	require.NoError(t, err)
	got, ok := OpenAIResponseDiagnosticSnapshotFromContext(c)
	require.True(t, ok)
	require.Equal(t, "completed", got.Status)
	require.Equal(t, "resp_complete", got.ResponseID)
	require.EqualValues(t, 3, *got.InputTokens)
	require.EqualValues(t, 1, *got.CacheReadTokens)
	require.EqualValues(t, 0, *got.OutputTokens)
	require.Nil(t, got.CacheCreationTokens)
	require.Nil(t, got.ReasoningTokens)
}

func TestOpenAIResponseDiagnosticsAuthoritativeTerminalSupersedesBareError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	beginOpenAIResponseDiagnostics(c, []byte(`{"max_output_tokens":4096}`))
	observeOpenAIResponseDiagnosticForwarded(c, []byte(`{"max_output_tokens":2048}`))
	first := []byte(`{"type":"error","error":{"code":"upstream_error"}}`)
	observeOpenAIResponseDiagnosticTerminal(c, first, "error", nil)
	second := []byte(`{"type":"response.completed","response":{"id":"resp_second","status":"completed"}}`)
	observeOpenAIResponseDiagnosticTerminal(c, second, "response.completed", nil)
	observeOpenAIResponseDiagnosticTerminal(c, []byte(`{"type":"response.failed","response":{"id":"must_not_replace","status":"failed"}}`), "response.failed", nil)

	got, ok := OpenAIResponseDiagnosticSnapshotFromContext(c)
	require.True(t, ok)
	require.Equal(t, "resp_second", got.ResponseID)
	require.Equal(t, "completed", got.Status)
	require.EqualValues(t, 4096, *got.RequestedMaxOutputTokens)
	require.EqualValues(t, 2048, *got.ForwardedMaxOutputTokens)
}

func TestOpenAIResponseDiagnosticsRejectsNonIntegerTokenMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	beginOpenAIResponseDiagnostics(c, []byte(`{"max_output_tokens":"512"}`))
	observeOpenAIResponseDiagnosticForwarded(c, []byte(`{}`))
	payload := []byte(`{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":1.5,"output_tokens":"4"}}}`)
	observeOpenAIResponseDiagnosticUsage(c, payload)
	observeOpenAIResponseDiagnosticTerminal(c, payload, "response.completed", nil)
	got, _ := OpenAIResponseDiagnosticSnapshotFromContext(c)
	require.Nil(t, got.RequestedMaxOutputTokens)
	require.Nil(t, got.InputTokens)
	require.Nil(t, got.OutputTokens)
}

func TestOpenAIResponseDiagnosticsMarksClientDisconnectWithoutTerminal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	beginOpenAIResponseDiagnostics(c, []byte(`{}`))
	observeOpenAIResponseDiagnosticForwarded(c, []byte(`{}`))
	observeOpenAIResponseDiagnosticClientDisconnected(c)
	got, ok := OpenAIResponseDiagnosticSnapshotFromContext(c)
	require.True(t, ok)
	require.True(t, got.Active)
	require.False(t, got.Terminal)
	require.True(t, got.ClientDisconnected)
}

func TestOpenAIResponseDiagnosticsSkipsCompactResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", nil)
	require.Nil(t, beginOpenAIResponseDiagnostics(c, []byte(`{"max_output_tokens":128}`)))
	_, ok := OpenAIResponseDiagnosticSnapshotFromContext(c)
	require.False(t, ok)
}

func TestOpenAIResponseDiagnosticsSinkPublishesEachWebSocketTurnOnce(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	var published []OpenAIResponseDiagnosticSnapshot
	BindOpenAIResponseDiagnosticSink(c, func(snapshot OpenAIResponseDiagnosticSnapshot) {
		published = append(published, snapshot)
	})
	beginOpenAIResponseDiagnosticsForTurn(c, []byte(`{"max_output_tokens":64}`), true)
	observeOpenAIResponseDiagnosticForwarded(c, []byte(`{"max_output_tokens":32}`), 17)
	observeOpenAIResponseDiagnosticTerminal(c, []byte(`{"type":"response.incomplete","response":{"id":"resp_one","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":2,"output_tokens":1}}}`), "response.incomplete", nil)
	// Duplicate terminal frame must not enqueue another record.
	observeOpenAIResponseDiagnosticTerminal(c, []byte(`{"type":"response.incomplete","response":{"id":"resp_one","status":"incomplete"}}`), "response.incomplete", nil)
	beginOpenAIResponseDiagnosticsForTurn(c, []byte(`{"max_output_tokens":128}`), true)
	observeOpenAIResponseDiagnosticForwarded(c, []byte(`{"max_output_tokens":96}`), 17)
	observeOpenAIResponseDiagnosticTerminal(c, []byte(`{"type":"response.completed","response":{"id":"resp_two","status":"completed","usage":{"input_tokens":5,"output_tokens":3}}}`), "response.completed", nil)
	require.Len(t, published, 2)
	require.Equal(t, "resp_one", published[0].ResponseID)
	require.Equal(t, "incomplete", published[0].Status)
	require.EqualValues(t, 64, *published[0].RequestedMaxOutputTokens)
	require.EqualValues(t, 32, *published[0].ForwardedMaxOutputTokens)
	require.Equal(t, "resp_two", published[1].ResponseID)
	require.Equal(t, "completed", published[1].Status)
	require.EqualValues(t, 128, *published[1].RequestedMaxOutputTokens)
	require.EqualValues(t, 96, *published[1].ForwardedMaxOutputTokens)
	_, ok := TakeOpenAIResponseDiagnosticSnapshot(c)
	require.False(t, ok, "published terminals must not be emitted again by middleware fallback")
}

func TestOpenAIResponseDiagnosticsWebSocketTurnResetPublishesPendingBareError(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	var published []OpenAIResponseDiagnosticSnapshot
	BindOpenAIResponseDiagnosticSink(c, func(snapshot OpenAIResponseDiagnosticSnapshot) {
		published = append(published, snapshot)
	})
	beginOpenAIResponseDiagnosticsForTurn(c, []byte(`{"max_output_tokens":11}`), true)
	observeOpenAIResponseDiagnosticForwarded(c, []byte(`{"max_output_tokens":9}`), 17)
	observeOpenAIResponseDiagnosticTerminal(c, []byte(`{"error":{"code":"upstream_error"}}`), "error", nil)

	// A new WS turn must flush the prior scalar snapshot even though a bare
	// error waits for a potential formal terminal on ordinary HTTP.
	beginOpenAIResponseDiagnosticsForTurn(c, []byte(`{"max_output_tokens":22}`), true)
	require.Len(t, published, 1)
	require.Equal(t, int64(17), published[0].AccountID)
	require.True(t, published[0].Terminal)
	require.Equal(t, "failed", published[0].Status)
	require.Equal(t, "upstream_error", published[0].Reason)
	require.EqualValues(t, 11, *published[0].RequestedMaxOutputTokens)
	require.EqualValues(t, 9, *published[0].ForwardedMaxOutputTokens)

	observeOpenAIResponseDiagnosticForwarded(c, []byte(`{"max_output_tokens":20}`), 17)
	observeOpenAIResponseDiagnosticTerminal(c, []byte(`{"type":"response.completed","response":{"id":"resp_next","status":"completed"}}`), "response.completed", nil)
	require.Len(t, published, 2)
	require.Equal(t, "resp_next", published[1].ResponseID)
	require.EqualValues(t, 22, *published[1].RequestedMaxOutputTokens)
	require.EqualValues(t, 20, *published[1].ForwardedMaxOutputTokens)
	_, ok := TakeOpenAIResponseDiagnosticSnapshot(c)
	require.False(t, ok)
}
