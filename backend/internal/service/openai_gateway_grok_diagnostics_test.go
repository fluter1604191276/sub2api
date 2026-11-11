//go:build unit

package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Exercise the public gateway entrypoint: Grok Responses uses the shared
// JSON/SSE handling path, so diagnostics must survive both normal forms.
func TestGatewayForwardGrokResponsesPublishesDiagnosticsForJSONAndSSE(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name, request, contentType, response, responseID string
		stream                                            bool
	}{
		{
			name:        "json",
			request:     `{"model":"grok","input":"hi","stream":false,"max_output_tokens":31}`,
			contentType: "application/json",
			response:    `{"id":"resp_grok_json","status":"completed","output":[],"usage":{"input_tokens":7,"output_tokens":3}}`,
			responseID:  "resp_grok_json",
		},
		{
			name:        "sse",
			request:     `{"model":"grok","input":"hi","stream":true,"max_output_tokens":47}`,
			contentType: "text/event-stream",
			response: "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_grok_sse\",\"status\":\"completed\",\"usage\":{\"input_tokens\":5,\"output_tokens\":2}}}\n\n",
			responseID: "resp_grok_sse",
			stream:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := []byte(tt.request)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
			account := &Account{ID: 9201, Platform: PlatformGrok, Type: AccountTypeAPIKey, Concurrency: 1, Credentials: map[string]any{"api_key": "test-key", "base_url": "https://api.x.ai/v1"}}
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{tt.contentType}, "Xai-Request-Id": []string{"grok-diagnostic-request"}},
				Body:       io.NopCloser(strings.NewReader(tt.response)),
			}}

			result, err := (&OpenAIGatewayService{httpUpstream: upstream}).Forward(context.Background(), c, account, body)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, tt.stream, result.Stream)
			got, ok := OpenAIResponseDiagnosticSnapshotFromContext(c)
			require.True(t, ok)
			require.True(t, got.Active)
			require.True(t, got.Terminal)
			require.Equal(t, int64(9201), got.AccountID)
			require.Equal(t, tt.responseID, got.ResponseID)
			require.Equal(t, "completed", got.Status)
			require.Equal(t, "grok-diagnostic-request", got.UpstreamRequestID)
			if tt.stream {
				require.EqualValues(t, 47, *got.RequestedMaxOutputTokens)
				require.EqualValues(t, 47, *got.ForwardedMaxOutputTokens)
			} else {
				require.EqualValues(t, 31, *got.RequestedMaxOutputTokens)
				require.EqualValues(t, 31, *got.ForwardedMaxOutputTokens)
			}
		})
	}
}
