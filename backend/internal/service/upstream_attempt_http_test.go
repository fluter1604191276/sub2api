//go:build unit

package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

type attemptCaptureRepo struct {
	UpstreamAttemptRepository
	mu    sync.Mutex
	items []*UpstreamAttempt
}

func (r *attemptCaptureRepo) Insert(_ context.Context, a *UpstreamAttempt) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	copy := *a
	r.items = append(r.items, &copy)
	return nil
}

func (r *attemptCaptureRepo) wait(t *testing.T, count int) {
	t.Helper()
	require.Eventually(t, func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return len(r.items) == count
	}, 3*time.Second, time.Millisecond)
	r.mu.Lock()
	sort.Slice(r.items, func(i, j int) bool { return r.items[i].CreatedAt.Before(r.items[j].CreatedAt) })
	r.mu.Unlock()
}

type attemptHTTPStub struct {
	body        string
	contentType string
	status      int
	err         error
	tlsCalls    int
}

func (s *attemptHTTPStub) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &http.Response{StatusCode: s.status, Header: http.Header{
		"Content-Type": []string{s.contentType}, "X-Request-Id": []string{"same-upstream-id"}},
		Body: io.NopCloser(strings.NewReader(s.body)), Request: req}, nil
}

func (s *attemptHTTPStub) DoWithTLS(req *http.Request, proxy string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	s.tlsCalls++
	return s.Do(req, proxy, accountID, concurrency)
}

func attemptTestRequest(t *testing.T, path string) *http.Request {
	t.Helper()
	ctx := context.WithValue(context.Background(), ctxkey.RequestID, "req-shared")
	ctx = context.WithValue(ctx, ctxkey.ClientRequestID, "dispatch-shared")
	ctx = context.WithValue(ctx, ctxkey.UserID, int64(12))
	ctx = context.WithValue(ctx, ctxkey.Group, &Group{ID: 20})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://upstream.invalid"+path,
		strings.NewReader(`{"model":"mapped-model","messages":[{"role":"user","content":"private-prompt"}]}`))
	require.NoError(t, err)
	return req
}

func TestUpstreamAttemptHTTPPreservesFailureUsageAndRetryIdentity(t *testing.T) {
	repo := &attemptCaptureRepo{}
	stub := &attemptHTTPStub{status: 503, contentType: "application/json",
		body: `{"error":{"message":"private-upstream-error"},"usage":{"prompt_tokens":40,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":30}}}`}
	client := NewObservedHTTPUpstream(stub, NewUpstreamAttemptRecorder(repo, nil))
	req := attemptTestRequest(t, "/v1/chat/completions")
	for range 2 {
		resp, err := client.Do(req, "", 81, 1)
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, stub.body, string(body))
		require.NoError(t, resp.Body.Close())
		require.NoError(t, resp.Body.Close())
	}
	repo.wait(t, 2)
	require.Len(t, repo.items, 2)
	a := repo.items[0]
	require.NotEqual(t, a.AttemptID, repo.items[1].AttemptID)
	require.Equal(t, "same-upstream-id", a.UpstreamRequestID)
	require.Equal(t, "req-shared", a.RequestID)
	require.Equal(t, "dispatch-shared", a.ClientRequestID)
	require.Equal(t, "failed", a.Outcome)
	require.True(t, a.UsagePresent)
	require.EqualValues(t, 10, *a.InputTokens)
	require.EqualValues(t, 30, *a.CacheReadTokens)
	require.EqualValues(t, 5, *a.OutputTokens)
	require.EqualValues(t, 20, *a.GroupID)
	require.EqualValues(t, 12, *a.UserID)
	requestBody, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	require.Contains(t, string(requestBody), "private-prompt")
}

func TestUpstreamAttemptHTTPStreamingMetadata(t *testing.T) {
	tests := []struct {
		name, path, body, outcome string
		input, cache, output      int64
	}{
		{"responses_incomplete", "/v1/responses", `data: {"type":"response.incomplete","response":{"status":"incomplete","usage":{"input_tokens":30,"output_tokens":3,"input_tokens_details":{"cached_tokens":30}}}}` + "\n\n", "incomplete", 0, 30, 3},
		{"anthropic_deltas", "/v1/messages", `data: {"type":"message_start","message":{"usage":{"input_tokens":7,"cache_read_input_tokens":30,"output_tokens":1}}}` + "\n\n" + `data: {"type":"message_delta","usage":{"output_tokens":8}}` + "\n\n" + `data: {"type":"message_stop"}` + "\n\n", "success", 7, 30, 8},
		{"openai_partial_update", "/v1/chat/completions", `data: {"usage":{"prompt_tokens":40,"prompt_tokens_details":{"cached_tokens":30}}}` + "\n\n" + `data: {"usage":{"completion_tokens":8}}` + "\n\ndata: [DONE]\n\n", "success", 10, 30, 8},
		{"gemini_thinking", "/v1beta/models/gemini-test:streamGenerateContent", `data: {"usageMetadata":{"promptTokenCount":40,"cachedContentTokenCount":30,"candidatesTokenCount":3,"thoughtsTokenCount":5}}` + "\n\n" + `data: {"usageMetadata":{"candidatesTokenCount":4}}` + "\n\ndata: [DONE]\n\n", "success", 10, 30, 9},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := &attemptCaptureRepo{}
			stub := &attemptHTTPStub{body: tc.body, status: 200, contentType: "text/event-stream"}
			client := NewObservedHTTPUpstream(stub, NewUpstreamAttemptRecorder(repo, nil))
			resp, err := client.DoWithTLS(attemptTestRequest(t, tc.path), "", 81, 1, nil)
			require.NoError(t, err)
			// Single-byte reads exercise frame splits without changing output.
			var actual bytes.Buffer
			_, err = io.CopyBuffer(&actual, resp.Body, make([]byte, 1))
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			require.Equal(t, tc.body, actual.String())
			repo.wait(t, 1)
			require.Len(t, repo.items, 1)
			a := repo.items[0]
			require.Equal(t, tc.outcome, a.Outcome)
			require.Equal(t, tc.input, *a.InputTokens)
			require.Equal(t, tc.cache, *a.CacheReadTokens)
			require.Equal(t, tc.output, *a.OutputTokens)
			require.Equal(t, 1, stub.tlsCalls)
		})
	}
}

func TestUpstreamAttemptHTTPTransportFailureAndNoUsage(t *testing.T) {
	for _, transportError := range []bool{false, true} {
		repo := &attemptCaptureRepo{}
		stub := &attemptHTTPStub{status: 200, contentType: "application/json", body: `{"choices":[]}`}
		if transportError {
			stub.err = errors.New("private-network-error")
		}
		client := NewObservedHTTPUpstream(stub, NewUpstreamAttemptRecorder(repo, nil))
		req := attemptTestRequest(t, "/v1/chat/completions")
		resp, err := client.Do(req, "", 81, 1)
		if transportError {
			require.ErrorIs(t, err, stub.err)
		} else {
			require.NoError(t, err)
			_, err = io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
		}
		repo.wait(t, 1)
		require.Len(t, repo.items, 1)
		require.False(t, repo.items[0].UsagePresent)
		require.Nil(t, repo.items[0].CostUSD)
	}
}

func TestUpstreamAttemptHTTPFrameOverflowRecovers(t *testing.T) {
	repo := &attemptCaptureRepo{}
	body := "data: " + strings.Repeat("x", upstreamAttemptFrameLimit+1) + "\n\n" +
		`data: {"type":"response.completed","response":{"usage":{"input_tokens":1,"output_tokens":2}}}` + "\n\n"
	client := NewObservedHTTPUpstream(&attemptHTTPStub{body: body, status: 200, contentType: "text/event-stream"}, NewUpstreamAttemptRecorder(repo, nil))
	resp, err := client.Do(attemptTestRequest(t, "/v1/responses"), "", 81, 1)
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	repo.wait(t, 1)
	require.Len(t, repo.items, 1)
	require.EqualValues(t, 2, *repo.items[0].OutputTokens)
}

func TestUpstreamAttemptHTTPSSEWithoutContentType(t *testing.T) {
	repo := &attemptCaptureRepo{}
	body := "event: response.incomplete\n" + `data: {"type":"response.incomplete","response":{"usage":{"input_tokens":2,"output_tokens":0}}}` + "\n\n"
	client := NewObservedHTTPUpstream(&attemptHTTPStub{body: body, status: 200}, NewUpstreamAttemptRecorder(repo, nil))
	resp, err := client.Do(attemptTestRequest(t, "/v1/responses"), "", 81, 1)
	require.NoError(t, err)
	actual, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, body, string(actual))
	repo.wait(t, 1)
	require.Len(t, repo.items, 1)
	require.True(t, repo.items[0].UsagePresent)
	require.EqualValues(t, 0, *repo.items[0].OutputTokens)
	require.Equal(t, "incomplete", repo.items[0].Outcome)
}

type attemptBlockingBody struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *attemptBlockingBody) Read([]byte) (int, error) {
	close(b.started)
	<-b.release
	return 0, context.Canceled
}

func (b *attemptBlockingBody) Close() error {
	b.once.Do(func() { close(b.release) })
	return nil
}

func TestUpstreamAttemptHTTPCloseUnblocksReadAndPersistsOnce(t *testing.T) {
	repo := &attemptCaptureRepo{}
	body := &attemptBlockingBody{started: make(chan struct{}), release: make(chan struct{})}
	observer := &upstreamAttemptBody{
		body: body, ctx: context.Background(), recorder: NewUpstreamAttemptRecorder(repo, nil), stream: true,
		attempt: &UpstreamAttempt{AttemptID: "close-cancel", AccountID: 81, Outcome: "unknown"},
	}
	readDone := make(chan struct{})
	go func() {
		_, _ = observer.Read(make([]byte, 1))
		close(readDone)
	}()
	<-body.started
	closeDone := make(chan error, 1)
	go func() { closeDone <- observer.Close() }()
	select {
	case err := <-closeDone:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel the blocked Read")
	}
	<-readDone
	require.NoError(t, observer.Close())
	repo.wait(t, 1)
	require.Len(t, repo.items, 1)
	require.Equal(t, "failed", repo.items[0].Outcome)
}
