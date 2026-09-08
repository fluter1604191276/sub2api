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

func TestWebSearchUsageTracker_ExplicitCounters(t *testing.T) {
	var tracker webSearchUsageTracker
	tracker.ObserveJSON([]byte(`{"usage":{"web_search_calls":2}}`))
	tracker.ObserveJSON([]byte(`{"tool_usage":{"web_search":{"calls":5}}}`))
	tracker.ObserveJSON([]byte(`{"usage":{"web_search_call_count":3}}`))

	require.Equal(t, 5, tracker.Count(), "counters are cumulative snapshots, not additive chunks")
}

func TestWebSearchUsageTracker_DeduplicatesResponsesOutputItems(t *testing.T) {
	var tracker webSearchUsageTracker
	tracker.ObserveJSON([]byte(`{"type":"response.output_item.added","item":{"type":"web_search_call","id":"call_1"}}`))
	tracker.ObserveJSON([]byte(`{"type":"response.output_item.done","item":{"type":"web_search_call","id":"call_1"}}`))
	tracker.ObserveJSON([]byte(`{"type":"response.output_item.done","item":{"type":"web_search_call","id":"call_2"}}`))

	require.Equal(t, 2, tracker.Count())
}

func TestWebSearchUsageTracker_DoesNotCountClientFunctionToolCalls(t *testing.T) {
	var tracker webSearchUsageTracker
	tracker.ObserveJSON([]byte(`{"choices":[{"message":{"tool_calls":[{"type":"function","function":{"name":"web_search"}}]}}]}`))

	require.Zero(t, tracker.Count())
}

func TestForwardAsRawChatCompletions_CapturesWebSearchCallsNonStreaming(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"deepseek-v4-flash","messages":[{"role":"user","content":"search"}],"stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"chatcmpl_search","choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":2,"completion_tokens":1,"web_search_calls":5}}`)),
	}}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}

	result, err := svc.forwardAsRawChatCompletions(context.Background(), c, rawChatCompletionsTestAccount(), body, "")

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 5, result.WebSearchCalls)
}

func TestForwardAsRawChatCompletions_CapturesWebSearchCallsStreaming(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"deepseek-v4-flash","messages":[{"role":"user","content":"search"}],"stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_search","choices":[{"delta":{"content":"ok"}}]}`,
		"",
		`data: {"id":"chatcmpl_search","choices":[],"usage":{"prompt_tokens":2,"completion_tokens":1,"web_search_calls":5}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}

	result, err := svc.forwardAsRawChatCompletions(context.Background(), c, rawChatCompletionsTestAccount(), body, "")

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 5, result.WebSearchCalls)
}
