package repository

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// upstreamAttemptLifecycleRepo is deliberately only an Insert sink: these tests
// exercise the real HTTP transport and observer lifecycle, not SQL persistence.
type upstreamAttemptLifecycleRepo struct {
	service.UpstreamAttemptRepository

	mu       sync.Mutex
	attempts []service.UpstreamAttempt
}

func (r *upstreamAttemptLifecycleRepo) Insert(_ context.Context, attempt *service.UpstreamAttempt) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.attempts = append(r.attempts, *attempt)
	return nil
}

func (r *upstreamAttemptLifecycleRepo) one(t *testing.T) service.UpstreamAttempt {
	t.Helper()
	require.Eventually(t, func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return len(r.attempts) == 1
	}, 3*time.Second, time.Millisecond)
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.attempts[0]
}

func observedLifecycleRequest(t *testing.T, url string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url+"/v1/responses", bytes.NewReader([]byte(`{"model":"gpt-5.1","input":"probe"}`)))
	require.NoError(t, err)
	return req
}

func TestObservedHTTPUpstreamLifecycleParsesExplicitGzipJSONUsage(t *testing.T) {
	payload := []byte(`{"status":"completed","usage":{"input_tokens":17,"output_tokens":8}}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		writer := gzip.NewWriter(w)
		_, _ = writer.Write(payload)
		_ = writer.Close()
	}))
	t.Cleanup(server.Close)

	repo := &upstreamAttemptLifecycleRepo{}
	upstream := service.NewObservedHTTPUpstream(NewHTTPUpstream(nil), service.NewUpstreamAttemptRecorder(repo, nil))
	req := observedLifecycleRequest(t, server.URL)
	// Explicit encoding makes the repository's decoder (rather than net/http's
	// automatic decoder) responsible for handing decoded bytes to the observer.
	req.Header.Set("Accept-Encoding", "gzip")

	resp, err := upstream.Do(req, "", 71, 1)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, payload, body, "the caller must receive decoded JSON")
	require.Empty(t, resp.Header.Get("Content-Encoding"))
	require.NoError(t, resp.Body.Close())

	attempt := repo.one(t)
	require.Equal(t, "success", attempt.Outcome)
	require.True(t, attempt.UsagePresent)
	require.NotNil(t, attempt.InputTokens)
	require.NotNil(t, attempt.OutputTokens)
	require.EqualValues(t, 17, *attempt.InputTokens)
	require.EqualValues(t, 8, *attempt.OutputTokens)
}

func TestObservedHTTPUpstreamLifecycleDrainsErrorUsageBeforeCancellation(t *testing.T) {
	prefix := []byte(`{"error":{"message":"retry later"},`)
	tail := []byte(`"usage":{"input_tokens":19,"output_tokens":6}}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write(prefix)
		_ = http.NewResponseController(w).Flush()
		time.Sleep(10 * time.Millisecond)
		_, _ = w.Write(tail)
	}))
	t.Cleanup(server.Close)

	repo := &upstreamAttemptLifecycleRepo{}
	upstream := service.NewObservedHTTPUpstream(NewHTTPUpstream(nil), service.NewUpstreamAttemptRecorder(repo, nil))
	resp, err := upstream.Do(observedLifecycleRequest(t, server.URL), "", 72, 1)
	require.NoError(t, err)
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)

	got := make([]byte, len(prefix))
	_, err = io.ReadFull(resp.Body, got)
	require.NoError(t, err)
	require.Equal(t, prefix, got)
	// The caller intentionally abandons the body. The observer must consume the
	// small delayed tail before cancelOnCloseBody cancels its transport context.
	require.NoError(t, resp.Body.Close())

	attempt := repo.one(t)
	require.Equal(t, http.StatusServiceUnavailable, attempt.HTTPStatus)
	require.Equal(t, "failed", attempt.Outcome)
	require.True(t, attempt.UsagePresent)
	require.NotNil(t, attempt.InputTokens)
	require.NotNil(t, attempt.OutputTokens)
	require.EqualValues(t, 19, *attempt.InputTokens)
	require.EqualValues(t, 6, *attempt.OutputTokens)
}
