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
