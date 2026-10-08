//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUsageLogSyncFallbackRequiresSafeReplay(t *testing.T) {
	unknown := errors.New("synthetic uncertain commit")
	for _, tc := range []struct {
		name, requestID string
		apiKeyID        int64
		err             error
		wantCreates     int
	}{
		{"keyed_unknown", "synthetic-key", 2, unknown, 1},
		{"keyed_dropped_after_enqueue", "synthetic-key", 2, MarkUsageLogCreateDropped(context.DeadlineExceeded), 1},
		{"unkeyed_unknown", "", 2, unknown, 0},
		{"unkeyed_whitespace", "  ", 2, unknown, 0},
		{"unkeyed_dropped_after_enqueue", "", 2, MarkUsageLogCreateDropped(context.DeadlineExceeded), 0},
		{"missing_api_key_unknown", "synthetic-key", 0, unknown, 0},
		{"unkeyed_proven_not_persisted", "", 2, MarkUsageLogCreateNotPersisted(context.Canceled), 1},
		{"success_never_replayed", "synthetic-key", 2, nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &openAIRecordUsageBestEffortLogRepoStub{bestEffortErr: tc.err}
			log := &UsageLog{RequestID: tc.requestID, APIKeyID: tc.apiKeyID}
			writeUsageLogBestEffort(context.Background(), repo, log, "synthetic")
			require.Equal(t, 1, repo.bestEffortCalls)
			require.Equal(t, tc.wantCreates, repo.createCalls)
			require.Equal(t, tc.requestID, log.RequestID, "never manufacture another idempotency key")
			require.Equal(t, tc.apiKeyID, log.APIKeyID)
		})
	}
}
