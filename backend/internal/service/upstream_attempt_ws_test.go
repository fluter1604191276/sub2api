//go:build unit

package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func newWebSocketAttemptService(repo *attemptCaptureRepo) *OpenAIGatewayService {
	return &OpenAIGatewayService{
		httpUpstream: &observedHTTPUpstream{recorder: NewUpstreamAttemptRecorder(repo, nil)},
	}
}

func TestUpstreamAttemptWebSocketRecordsCachedFailureUsageOnce(t *testing.T) {
	repo := &attemptCaptureRepo{}
	svc := newWebSocketAttemptService(repo)
	request := attemptTestRequest(t, "/v1/responses")

	observer := svc.startUpstreamAttemptWebSocket(request.Context(), nil, &Account{ID: 81}, "mapped-model", "priority", "high")
	require.NotNil(t, observer)
	observer.setUpstreamRequestID(http.Header{"X-Request-Id": {"upstream-handshake-id"}})
	observer.observe([]byte(`{"type":"response.failed","response":{"id":"resp_not_a_request_id","usage":{"input_tokens":40,"output_tokens":0,"input_tokens_details":{"cached_tokens":40}}}}`))
	observer.finish()
	observer.finish()

	require.Len(t, repo.items, 1)
	attempt := repo.items[0]
	require.Equal(t, "failed", attempt.Outcome)
	require.True(t, attempt.UsagePresent)
	require.EqualValues(t, 0, *attempt.InputTokens)
	require.EqualValues(t, 40, *attempt.CacheReadTokens)
	require.EqualValues(t, 0, *attempt.OutputTokens)
	require.Equal(t, "upstream-handshake-id", attempt.UpstreamRequestID)
	require.Equal(t, "req-shared", attempt.RequestID)
	require.Equal(t, "dispatch-shared", attempt.ClientRequestID)
	require.EqualValues(t, 81, attempt.AccountID)
	require.EqualValues(t, 12, *attempt.UserID)
	require.EqualValues(t, 20, *attempt.GroupID)
}

func TestUpstreamAttemptWebSocketInterruptedTurnRetainsUsageAndIdentity(t *testing.T) {
	repo := &attemptCaptureRepo{}
	svc := newWebSocketAttemptService(repo)
	request := attemptTestRequest(t, "/v1/responses")

	first := svc.startUpstreamAttemptWebSocket(request.Context(), nil, &Account{ID: 81}, "mapped-model", "", "")
	first.observe([]byte(`{"type":"response.in_progress","response":{"usage":{"input_tokens":7,"output_tokens":2}}}`))
	// The connection ends before a terminal event. The partial usage is still
	// recorded and the observer marks that turn failed.
	first.finish()

	second := svc.startUpstreamAttemptWebSocket(request.Context(), nil, &Account{ID: 81}, "mapped-model", "", "")
	second.observe([]byte(`{"type":"response.completed","response":{"id":"resp_second","usage":{"input_tokens":0,"output_tokens":0}}}`))
	second.finish()

	require.Len(t, repo.items, 2)
	require.NotEqual(t, repo.items[0].AttemptID, repo.items[1].AttemptID)
	require.Equal(t, "failed", repo.items[0].Outcome)
	require.True(t, repo.items[0].UsagePresent)
	require.EqualValues(t, 7, *repo.items[0].InputTokens)
	require.EqualValues(t, 2, *repo.items[0].OutputTokens)
	require.Equal(t, "success", repo.items[1].Outcome)
	// A present all-zero usage object is different from absent usage.
	require.True(t, repo.items[1].UsagePresent)
	require.EqualValues(t, 0, *repo.items[1].InputTokens)
	require.EqualValues(t, 0, *repo.items[1].OutputTokens)
	require.Empty(t, repo.items[1].UpstreamRequestID)
	for _, attempt := range repo.items {
		require.Equal(t, "req-shared", attempt.RequestID)
		require.Equal(t, "dispatch-shared", attempt.ClientRequestID)
	}
}
