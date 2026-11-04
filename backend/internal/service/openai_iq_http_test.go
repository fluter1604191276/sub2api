package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestIQResponseSSECompletedPrefersSnapshot(t *testing.T) {
	raw := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello \"}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"model\":\"tested\",\"output\":[{\"content\":[{\"type\":\"output_text\",\"text\":\"hello world\"}]}]}}\n\n"
	output, model, err := parseIQResponse([]byte(raw), true)
	require.NoError(t, err)
	require.Equal(t, "hello world", output)
	require.Equal(t, "tested", model)
}

func TestIQResponseJSONCompleted(t *testing.T) {
	output, model, err := parseIQResponse([]byte(`{"status":"completed","model":"m","output":[{"content":[{"type":"output_text","text":"answer"}]}]}`), false)
	require.NoError(t, err)
	require.Equal(t, "answer", output)
	require.Equal(t, "m", model)
}

func TestIQResponseRejectsIncomplete(t *testing.T) {
	output, _, err := parseIQResponse([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n"), true)
	require.Error(t, err)
	require.Equal(t, "partial", output)
}

func TestIQResponseRejectsConflictingTerminalSequence(t *testing.T) {
	raw := "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[]}}\n\n" +
		"data: {\"type\":\"response.incomplete\",\"response\":{\"status\":\"incomplete\"}}\n\n"
	_, _, err := parseIQResponse([]byte(raw), true)
	require.Error(t, err)
}

func TestIQResponseRejectsMultipleCompletionEvents(t *testing.T) {
	raw := "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[]}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[]}}\n\n"
	_, _, err := parseIQResponse([]byte(raw), true)
	require.Error(t, err)
}

func TestIQResponseRejectsSemanticEventAfterCompletion(t *testing.T) {
	raw := "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[]}}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"late\"}\n\n"
	_, _, err := parseIQResponse([]byte(raw), true)
	require.Error(t, err)
}

func TestIQResponseRejectsDoneOnlyAndMalformedSSE(t *testing.T) {
	for _, raw := range []string{"data: [DONE]\n\n", "data: {not-json}\n\n", "data: null\n\n"} {
		_, _, err := parseIQResponse([]byte(raw), true)
		require.Error(t, err, raw)
	}
}

func TestIQResponseModelIsBoundedAndKeyRedacted(t *testing.T) {
	const key = "test-key-only"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"completed","model":"` + key + strings.Repeat("m", 250) + `","output":[{"content":[{"type":"output_text","text":"` + key + ` answer"}]}]}`))
	}))
	defer server.Close()
	s := &OpenAIIQService{endpoint: server.URL, client: server.Client()}
	output, model, err := s.requestIQ(context.Background(), key, []byte(`{}`), "00000000-0000-4000-8000-000000000001")
	require.NoError(t, err)
	require.NotContains(t, output, key)
	require.NotContains(t, model, key)
	require.LessOrEqual(t, len(model), 200)
}

func TestIQHTTPRejectsNonSuccessWithoutLeakingBody(t *testing.T) {
	const key = "test-key-only"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, key+" secret", http.StatusBadGateway) }))
	defer server.Close()
	s := &OpenAIIQService{endpoint: server.URL, client: server.Client()}
	output, model, err := s.requestIQ(context.Background(), key, []byte(`{}`), "00000000-0000-4000-8000-000000000001")
	require.Error(t, err)
	require.Empty(t, output)
	require.Empty(t, model)
	require.NotContains(t, err.Error(), key)
}

func TestIQHTTPRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", iqHTTPMaxBodyBytes+2)))
	}))
	defer server.Close()
	s := &OpenAIIQService{endpoint: server.URL, client: server.Client()}
	_, _, err := s.requestIQ(context.Background(), "test-key", []byte(`{}`), "00000000-0000-4000-8000-000000000001")
	require.Error(t, err)
}

func TestIQHTTPDoesNotFollowRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.invalid/secret", http.StatusFound)
	}))
	defer server.Close()
	s := &OpenAIIQService{endpoint: server.URL, client: (&OpenAIIQService{cfg: &config.Config{}}).configuredClientForTest()}
	_, _, err := s.requestIQ(context.Background(), "test-key", []byte(`{}`), "00000000-0000-4000-8000-000000000001")
	require.Error(t, err)
}

func TestIQHTTPCancellationDoesNotRetry(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := &OpenAIIQService{endpoint: server.URL, client: server.Client()}
	_, _, err := s.requestIQ(ctx, "test-key", []byte(`{}`), "00000000-0000-4000-8000-000000000001")
	require.Error(t, err)
	time.Sleep(10 * time.Millisecond)
	require.LessOrEqual(t, requests, 1)
}

func TestIQHTTPConfiguredClientIgnoresEnvironmentProxy(t *testing.T) {
	s := &OpenAIIQService{cfg: &config.Config{}}
	s.configureIQHTTP()
	client, ok := s.client.(*http.Client)
	require.True(t, ok)
	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok)
	require.Nil(t, transport.Proxy)
	require.LessOrEqual(t, client.Timeout, 120*time.Second)
}

func (s *OpenAIIQService) configuredClientForTest() iqHTTPClient {
	s.configureIQHTTP()
	return s.client
}
