//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type attemptSlowRepo struct {
	UpstreamAttemptRepository
	started chan struct{}
	release chan struct{}
}

func (r *attemptSlowRepo) Insert(ctx context.Context, _ *UpstreamAttempt) error {
	select {
	case r.started <- struct{}{}:
	default:
	}
	select {
	case <-r.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestUpstreamAttemptCloseDoesNotWaitForDatabase(t *testing.T) {
	repo := &attemptSlowRepo{started: make(chan struct{}, 2), release: make(chan struct{})}
	queue := &upstreamAttemptQueue{jobs: make(chan upstreamAttemptJob, 1)}
	recorder := NewUpstreamAttemptRecorder(repo, nil)
	recorder.queue = queue
	defer queue.stop()
	defer close(repo.release)
	client := NewObservedHTTPUpstream(&attemptHTTPStub{body: `{"usage":{"input_tokens":2}}`, status: 200}, recorder)
	resp, err := client.Do(attemptTestRequest(t, "/v1/responses"), "", 81, 1)
	require.NoError(t, err)
	_, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	closed := make(chan struct{})
	go func() { _ = resp.Body.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("close waited for database")
	}
	select {
	case <-repo.started:
	case <-time.After(time.Second):
		t.Fatal("metadata was not queued")
	}
}

func TestUpstreamAttemptQueueDropsWhenFull(t *testing.T) {
	repo := &attemptSlowRepo{started: make(chan struct{}, 2), release: make(chan struct{})}
	queue := &upstreamAttemptQueue{jobs: make(chan upstreamAttemptJob, 1)}
	recorder := NewUpstreamAttemptRecorder(repo, nil)
	recorder.queue = queue
	defer queue.stop()
	defer close(repo.release)
	job := upstreamAttemptJob{repository: recorder.repo, attempt: UpstreamAttempt{}}
	queue.enqueue(job)
	<-repo.started
	queue.enqueue(job)
	<-repo.started
	queue.enqueue(job)
	queue.enqueue(job)
	require.EqualValues(t, 1, queue.dropped.Load())
	require.EqualValues(t, 3, queue.enqueued.Load())
}

func TestUpstreamAttemptQueueDropsAfterStop(t *testing.T) {
	queue := &upstreamAttemptQueue{jobs: make(chan upstreamAttemptJob, 1)}
	queue.stop()
	queue.enqueue(upstreamAttemptJob{})
	require.EqualValues(t, 1, queue.dropped.Load())
	require.Zero(t, queue.enqueued.Load())
}

func TestUpstreamAttemptErrorPrefixCloseObservesTailUsage(t *testing.T) {
	repo := &attemptCaptureRepo{}
	body := `{"error":{"message":"` + strings.Repeat("x", 4096) + `"},"usage":{"prompt_tokens":12,"completion_tokens":3}}`
	client := NewObservedHTTPUpstream(&attemptHTTPStub{body: body, status: http.StatusBadGateway}, NewUpstreamAttemptRecorder(repo, nil))
	resp, err := client.Do(attemptTestRequest(t, "/v1/responses"), "", 81, 1)
	require.NoError(t, err)
	prefix := make([]byte, 10)
	_, err = io.ReadFull(resp.Body, prefix)
	require.NoError(t, err)
	require.Equal(t, body[:10], string(prefix))
	require.NoError(t, resp.Body.Close())
	repo.wait(t, 1)
	require.True(t, repo.items[0].UsagePresent)
	require.EqualValues(t, 12, *repo.items[0].InputTokens)
	require.EqualValues(t, 3, *repo.items[0].OutputTokens)
}
