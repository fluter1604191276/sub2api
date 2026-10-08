//go:build unit

package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	gocache "github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/require"
)

func TestUsageLogFallbackAfterBatchDeadline(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()); require.NoError(t, mock.ExpectationsWereMet()) })
	log := &service.UsageLog{UserID: 1, APIKeyID: 2, AccountID: 3, RequestID: "synthetic-batch-deadline", Model: "synthetic"}
	req := usageLogBestEffortRequest{prepared: prepareUsageLogInsert(log), apiKeyID: log.APIKeyID, resultCh: make(chan error, 1)}
	mock.ExpectExec("WITH input").WillDelayFor(11 * time.Second).WillReturnError(context.DeadlineExceeded)
	mock.ExpectExec("INSERT INTO usage_logs").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectClose()
	repo := &usageLogRepository{db: db}
	repo.flushBestEffortBatch(db, []usageLogBestEffortRequest{req})
	require.NoError(t, <-req.resultCh, "log-only fallback must have its own live bounded context")
}

func fallbackRequest(requestID string, apiKeyID int64) usageLogBestEffortRequest {
	log := &service.UsageLog{UserID: 1, APIKeyID: apiKeyID, AccountID: 3, RequestID: requestID, Model: "synthetic"}
	return usageLogBestEffortRequest{prepared: prepareUsageLogInsert(log), apiKeyID: apiKeyID, resultCh: make(chan error, 1)}
}

func TestUsageLogFallbackSuccessfulBatchNeverReplays(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()); require.NoError(t, mock.ExpectationsWereMet()) })
	a, b := fallbackRequest("synthetic-same-key", 2), fallbackRequest("synthetic-same-key", 2)
	mock.ExpectExec("WITH input").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectClose() // Any fallback would be an unexpected INSERT.
	repo := &usageLogRepository{db: db, bestEffortRecent: gocache.New(time.Minute, 0)}
	repo.flushBestEffortBatch(db, []usageLogBestEffortRequest{a, b})
	require.NoError(t, <-a.resultCh)
	require.NoError(t, <-b.resultCh)
	require.Equal(t, 1, repo.bestEffortRecent.ItemCount())
}

func TestUsageLogFallbackUncertainBatchUsesOriginalConflictKeyOnce(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()); require.NoError(t, mock.ExpectationsWereMet()) })
	a, b := fallbackRequest("synthetic-existing-key", 2), fallbackRequest("synthetic-existing-key", 2)
	mock.ExpectExec("WITH input").WillReturnError(errors.New("synthetic response lost after unknown commit"))
	// Zero rows affected represents the existing row winning the conflict;
	// the durable key and SQL must not change to manufacture another row.
	mock.ExpectExec(`(?s)INSERT INTO usage_logs.*ON CONFLICT \(request_id, api_key_id\) DO NOTHING`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectClose()
	repo := &usageLogRepository{db: db, bestEffortRecent: gocache.New(time.Minute, 0)}
	repo.flushBestEffortBatch(db, []usageLogBestEffortRequest{a, b})
	require.NoError(t, <-a.resultCh)
	require.NoError(t, <-b.resultCh)
	require.Equal(t, "synthetic-existing-key", a.prepared.requestID)
	require.Equal(t, int64(2), a.apiKeyID)
	require.Equal(t, 1, repo.bestEffortRecent.ItemCount())
}

func TestUsageLogFallbackWithoutDurableKeyFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name, requestID string
		apiKeyID        int64
	}{
		{"missing_request_id", "", 2},
		{"missing_api_key", "synthetic", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, db.Close()); require.NoError(t, mock.ExpectationsWereMet()) })
			batchErr := errors.New("synthetic unknown batch outcome")
			req := fallbackRequest(tc.requestID, tc.apiKeyID)
			mock.ExpectExec("WITH input").WillReturnError(batchErr)
			mock.ExpectClose()
			repo := &usageLogRepository{db: db, bestEffortRecent: gocache.New(time.Minute, 0)}
			repo.flushBestEffortBatch(db, []usageLogBestEffortRequest{req})
			require.ErrorIs(t, <-req.resultCh, batchErr)
			require.Zero(t, repo.bestEffortRecent.ItemCount())
		})
	}
}

func TestUsageLogFallbackHasOneSharedBoundedWindow(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()); require.NoError(t, mock.ExpectationsWereMet()) })
	a, b := fallbackRequest("synthetic-slow", 2), fallbackRequest("synthetic-not-attempted", 2)
	mock.ExpectExec("WITH input").WillReturnError(errors.New("synthetic failed batch"))
	mock.ExpectExec("INSERT INTO usage_logs").WillDelayFor(11 * time.Second).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectClose() // No renewed 10-second INSERT for the second group.
	repo := &usageLogRepository{db: db, bestEffortRecent: gocache.New(time.Minute, 0)}
	started := time.Now()
	repo.flushBestEffortBatch(db, []usageLogBestEffortRequest{a, b})
	require.Error(t, <-a.resultCh)
	require.ErrorIs(t, <-b.resultCh, context.DeadlineExceeded)
	require.Less(t, time.Since(started), 12*time.Second)
	require.Zero(t, repo.bestEffortRecent.ItemCount())
}
