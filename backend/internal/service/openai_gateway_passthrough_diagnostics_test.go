package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Exercise the native HTTP passthrough rather than its observation helpers:
// API-key Responses accounts may return a 200 response.incomplete as JSON,
// normal SSE, or SSE which must be collapsed to JSON for stream=false.
func TestOpenAIPassthroughResponseDiagnosticsCapturesIncompleteHTTP200(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const requestID = "upstream-passthrough-incomplete"
	const jsonResponse = `{"id":"resp_passthrough_incomplete","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":13,"output_tokens":5,"input_tokens_details":{"cached_tokens":8}}}`
	const terminal = `{"type":"response.incomplete","response":` + jsonResponse + `}`

	tests := []struct {
		name        string
		requestBody []byte
		contentType string
		response    string
		stream      bool
	}{
		{
			name:        "json",
			requestBody: []byte(`{"model":"gpt-test","stream":false,"max_output_tokens":64}`),
			contentType: "application/json",
			response:    jsonResponse,
		},
		{
			name:        "stream_sse",
			requestBody: []byte(`{"model":"gpt-test","stream":true,"max_output_tokens":64}`),
			contentType: "text/event-stream",
			response: strings.Join([]string{
				"event: response.output_text.delta\n" + `data: {"type":"response.output_text.delta","delta":"partial"}`,
				"event: response.incomplete\n" + "data: " + terminal,
			}, "\n\n") + "\n\n",
			stream: true,
		},
		{
			name:        "sse_to_json",
			requestBody: []byte(`{"model":"gpt-test","stream":false,"max_output_tokens":64}`),
			contentType: "text/event-stream",
			response:    "event: response.incomplete\n" + "data: " + terminal + "\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(tt.requestBody))

			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": []string{tt.contentType},
					"X-Request-Id": []string{requestID},
				},
				Body: io.NopCloser(strings.NewReader(tt.response)),
			}}
			svc := openAIClientToolsTestService(upstream)
			account := &Account{
				ID:          7419,
				Platform:    PlatformOpenAI,
				Type:        AccountTypeAPIKey,
				Credentials: map[string]any{"api_key": "test-key"},
				Extra:       map[string]any{"openai_passthrough": true},
			}

			result, err := svc.Forward(context.Background(), c, account, tt.requestBody)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Contains(t, recorder.Body.String(), "incomplete")

			got, ok := OpenAIResponseDiagnosticSnapshotFromContext(c)
			require.True(t, ok)
			require.True(t, got.Active)
			require.True(t, got.Terminal)
			require.EqualValues(t, account.ID, got.AccountID)
			require.Equal(t, requestID, got.UpstreamRequestID)
			require.Equal(t, "resp_passthrough_incomplete", got.ResponseID)
			require.Equal(t, "incomplete", got.Status)
			require.Equal(t, "max_output_tokens", got.Reason)
			require.EqualValues(t, 64, *got.RequestedMaxOutputTokens)
			require.EqualValues(t, 64, *got.ForwardedMaxOutputTokens)
			require.EqualValues(t, 13, *got.InputTokens)
			require.EqualValues(t, 8, *got.CacheReadTokens)
			require.EqualValues(t, 5, *got.OutputTokens)
		})
	}
}

// Observation is optional when this lower-level passthrough entry is called
// directly. It must not affect the selected client bytes or usage returned to
// billing for JSON, normal SSE, or SSE-to-JSON conversion.
func TestOpenAIPassthroughResponseDiagnosticsPreservesWireAndUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const jsonResponse = `{"id":"resp_passthrough_stable","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[],"usage":{"input_tokens":11,"output_tokens":4,"input_tokens_details":{"cached_tokens":6}}}`
	const terminal = `{"type":"response.incomplete","response":` + jsonResponse + `}`
	tests := []struct {
		name        string
		requestBody []byte
		contentType string
		response    string
		stream      bool
	}{
		{"json", []byte(`{"model":"gpt-test","stream":false,"max_output_tokens":64}`), "application/json", jsonResponse, false},
		{"stream_sse", []byte(`{"model":"gpt-test","stream":true,"max_output_tokens":64}`), "text/event-stream", "event: response.output_text.delta\n" + `data: {"type":"response.output_text.delta","delta":"partial"}` + "\n\nevent: response.incomplete\n" + "data: " + terminal + "\n\n", true},
		{"sse_to_json", []byte(`{"model":"gpt-test","stream":false,"max_output_tokens":64}`), "text/event-stream", "event: response.incomplete\n" + "data: " + terminal + "\n\n", false},
	}

	run := func(t *testing.T, tt struct {
		name        string
		requestBody []byte
		contentType string
		response    string
		stream      bool
	}, diagnosticsEnabled bool) ([]byte, OpenAIUsage) {
		t.Helper()
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(tt.requestBody))
		if diagnosticsEnabled {
			beginOpenAIResponseDiagnostics(c, tt.requestBody)
		}
		upstream := &httpUpstreamRecorder{resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{tt.contentType}},
			Body:       io.NopCloser(strings.NewReader(tt.response)),
		}}
		account := &Account{
			ID:          7420,
			Platform:    PlatformOpenAI,
			Type:        AccountTypeAPIKey,
			Credentials: map[string]any{"api_key": "test-key"},
		}
		result, err := openAIClientToolsTestService(upstream).forwardOpenAIPassthrough(
			context.Background(), c, account, tt.requestBody, tt.requestBody,
			"gpt-test", false, nil, tt.stream, time.Now(),
		)
		require.NoError(t, err)
		require.NotNil(t, result)
		return append([]byte(nil), recorder.Body.Bytes()...), result.Usage
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withoutDiagnosticsBody, withoutDiagnosticsUsage := run(t, tt, false)
			withDiagnosticsBody, withDiagnosticsUsage := run(t, tt, true)
			require.Equal(t, withoutDiagnosticsBody, withDiagnosticsBody)
			require.Equal(t, withoutDiagnosticsUsage, withDiagnosticsUsage)
		})
	}
}

func TestOpenAIPassthroughResponseDiagnosticsSSEKeepsFirstTerminal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	request := []byte(`{"model":"gpt-test","stream":false}`)
	first := `{"type":"response.incomplete","response":{"id":"resp_first","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":3,"output_tokens":1}}}`
	last := `{"type":"response.completed","response":{"id":"resp_last","status":"completed","output":[],"usage":{"input_tokens":3,"output_tokens":9}}}`
	upstreamSSE := "event: response.incomplete\n" + "data: " + first + "\n\n" +
		"event: response.completed\n" + "data: " + last + "\n\n"

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(request))
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamSSE)),
	}}
	account := &Account{
		ID:          7421,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "test-key"},
		Extra:       map[string]any{"openai_passthrough": true},
	}

	result, err := openAIClientToolsTestService(upstream).Forward(context.Background(), c, account, request)
	require.NoError(t, err)
	// Existing SSE-to-JSON semantics choose the final response and its usage.
	require.Contains(t, recorder.Body.String(), `"id":"resp_last"`)
	require.Equal(t, 9, result.Usage.OutputTokens)

	// Diagnostics independently preserve the first authoritative terminal.
	got, ok := OpenAIResponseDiagnosticSnapshotFromContext(c)
	require.True(t, ok)
	require.Equal(t, "incomplete", got.Status)
	require.Equal(t, "resp_first", got.ResponseID)
	require.Equal(t, "max_output_tokens", got.Reason)
	require.EqualValues(t, 1, *got.OutputTokens)
}
