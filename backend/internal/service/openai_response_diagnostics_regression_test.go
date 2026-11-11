package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIResponseDiagnosticsNonStreamingIncompleteMissingUsage(t *testing.T) {
	for _, format := range []string{"json_no_usage", "sse_no_usage", "json_with_usage"} {
		t.Run(format, func(t *testing.T) {
			sse := format == "sse_no_usage"
			hasUsage := format == "json_with_usage"
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			beginOpenAIResponseDiagnostics(c, []byte(`{"stream":false}`))
			observeOpenAIResponseDiagnosticForwarded(c, []byte(`{"stream":false}`))
			body := `{"id":"resp_fixture","object":"response","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[]}`
			if hasUsage {
				body = strings.TrimSuffix(body, "}") + `,"usage":{"input_tokens":8,"output_tokens":3}}`
			}
			contentType := "application/json"
			if sse {
				body = "event: response.incomplete\ndata: {\"type\":\"response.incomplete\",\"response\":" + body + "}\n\n"
				contentType = "text/event-stream"
			}
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body))}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, toolCorrector: NewCodexToolCorrector()}
			_, err := svc.handleNonStreamingResponse(context.Background(), resp, c, &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, "gpt-test", "gpt-test")
			if sse || hasUsage {
				require.NoError(t, err)
			} else {
				// Existing JSON forwarding rejects absent usage. Observation must
				// retain the upstream terminal without changing that behavior.
				require.ErrorContains(t, err, "parse response: invalid json response")
			}
			got, ok := OpenAIResponseDiagnosticSnapshotFromContext(c)
			require.True(t, ok)
			require.Equal(t, "incomplete", got.Status)
			require.Equal(t, "max_output_tokens", got.Reason)
			if hasUsage {
				require.EqualValues(t, 8, *got.InputTokens)
				require.EqualValues(t, 3, *got.OutputTokens)
			} else {
				require.Nil(t, got.InputTokens)
				require.Nil(t, got.OutputTokens)
			}
			require.Nil(t, got.UpstreamMaxOutputTokens)
		})
	}
}

func TestOpenAIResponseDiagnosticsDuplicateAndFailoverMetadata(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	beginOpenAIResponseDiagnostics(c, []byte(`{"max_output_tokens":100}`))
	observeOpenAIResponseDiagnosticForwarded(c, []byte(`{"max_output_tokens":50}`))
	first := []byte(`{"type":"response.incomplete","response":{"status":"incomplete","usage":{"output_tokens":5}}}`)
	observeOpenAIResponseDiagnosticTerminal(c, first, "response.incomplete", nil)
	duplicate := []byte(`{"type":"response.completed","response":{"status":"completed","usage":{"output_tokens":99}}}`)
	observeOpenAIResponseDiagnosticUsage(c, duplicate)
	observeOpenAIResponseDiagnosticTerminal(c, duplicate, "response.completed", nil)
	got, _ := OpenAIResponseDiagnosticSnapshotFromContext(c)
	require.Equal(t, "incomplete", got.Status)
	require.EqualValues(t, 5, *got.OutputTokens)
	// A non-native fallback must not retain the previous native attempt.
	beginOpenAIResponseDiagnostics(c, []byte(`{}`))
	got, _ = OpenAIResponseDiagnosticSnapshotFromContext(c)
	require.False(t, got.Active)
	require.Empty(t, got.Status)
	// A new native attempt keeps the original client budget, not a transformed one.
	observeOpenAIResponseDiagnosticForwarded(c, []byte(`{}`))
	observeOpenAIResponseDiagnosticTerminal(c, duplicate, gjson.GetBytes(duplicate, "type").String(), nil)
	got, _ = OpenAIResponseDiagnosticSnapshotFromContext(c)
	require.EqualValues(t, 100, *got.RequestedMaxOutputTokens)
	require.Nil(t, got.ForwardedMaxOutputTokens)
	require.Equal(t, "completed", got.Status)
}

func TestOpenAIResponseDiagnosticsBareErrorThenDone(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	beginOpenAIResponseDiagnostics(c, []byte(`{}`))
	observeOpenAIResponseDiagnosticForwarded(c, []byte(`{}`))
	observeOpenAIResponseDiagnosticTerminal(c, []byte(`{"error":{"code":"upstream_error"}}`), "error", nil)
	observeOpenAIResponseDiagnosticTerminal(c, []byte(`[DONE]`), "", nil)
	got, _ := OpenAIResponseDiagnosticSnapshotFromContext(c)
	require.Equal(t, "failed", got.Status)
	require.Equal(t, "upstream_error", got.Reason)
	observeOpenAIResponseDiagnosticTerminal(c, []byte(`{"status":"completed"}`), "response.completed", nil)
	got, _ = OpenAIResponseDiagnosticSnapshotFromContext(c)
	require.Equal(t, "completed", got.Status)
}

func TestOpenAIResponseDiagnosticsSSEToJSONPreservesFirstTerminal(t *testing.T) {
	cases := []struct {
		name, body, status, reason string
		output                     int64
	}{
		{"incomplete_then_completed", "data: {\"type\":\"response.incomplete\",\"response\":{\"id\":\"first\",\"status\":\"incomplete\",\"incomplete_details\":{\"reason\":\"max_output_tokens\"},\"output\":[],\"usage\":{\"input_tokens\":7,\"output_tokens\":5}}}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"last\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":20,\"output_tokens\":99}}}\n\n", "incomplete", "max_output_tokens", 5},
		{"bare_error_then_completed", "event: error\ndata: {\"error\":{\"code\":\"upstream_error\"}}\n\nevent: response.completed\ndata: {\"response\":{\"id\":\"last\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":20,\"output_tokens\":99}}}\n\n", "completed", "", 99},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var bodies []string
			var outputs []int
			for _, enabled := range []bool{false, true} {
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				if enabled {
					beginOpenAIResponseDiagnostics(c, []byte(`{}`))
					observeOpenAIResponseDiagnosticForwarded(c, []byte(`{}`), 71)
				}
				resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(tc.body))}
				svc := &OpenAIGatewayService{cfg: &config.Config{}, toolCorrector: NewCodexToolCorrector()}
				result, err := svc.handleNonStreamingResponse(context.Background(), resp, c, &Account{ID: 71, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, "gpt-test", "gpt-test")
				require.NoError(t, err)
				bodies = append(bodies, recorder.Body.String())
				outputs = append(outputs, result.usage.OutputTokens)
				if enabled {
					got, _ := OpenAIResponseDiagnosticSnapshotFromContext(c)
					require.Equal(t, tc.status, got.Status)
					require.Equal(t, tc.reason, got.Reason)
					require.NotNil(t, got.OutputTokens)
					require.Equal(t, tc.output, *got.OutputTokens)
					require.EqualValues(t, 71, got.AccountID)
				}
			}
			require.Equal(t, bodies[0], bodies[1], "diagnostic observer must not change converted response")
			require.Equal(t, outputs[0], outputs[1], "diagnostic observer must not change billed usage")
		})
	}
}

func TestOpenAIResponseDiagnosticsAttemptIdentityResetsTogether(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	beginOpenAIResponseDiagnostics(c, []byte(`{"max_output_tokens":100}`))
	observeOpenAIResponseDiagnosticForwarded(c, []byte(`{"max_output_tokens":50}`), 11)
	observeOpenAIResponseDiagnosticHeaders(c, "upstream-A")
	observeOpenAIResponseDiagnosticTerminal(c, []byte(`{"status":"failed","id":"response-A"}`), "response.failed", nil)
	got, _ := OpenAIResponseDiagnosticSnapshotFromContext(c)
	require.EqualValues(t, 11, got.AccountID)
	// Once B reaches an actual outbound attempt, all attempt-local fields move together.
	observeOpenAIResponseDiagnosticForwarded(c, []byte(`{"max_output_tokens":30}`), 22)
	got, _ = OpenAIResponseDiagnosticSnapshotFromContext(c)
	require.EqualValues(t, 22, got.AccountID)
	require.Empty(t, got.UpstreamRequestID)
	require.Empty(t, got.ResponseID)
	require.False(t, got.Terminal)
	require.EqualValues(t, 100, *got.RequestedMaxOutputTokens)
	require.EqualValues(t, 30, *got.ForwardedMaxOutputTokens)
}
