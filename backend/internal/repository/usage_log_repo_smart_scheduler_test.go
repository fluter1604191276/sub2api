package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestUsageLogRepositoryGetSmartSchedulerErrorStatsBatch(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	accountIDs := []int64{11, 22}
	start := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	rows := sqlmock.NewRows([]string{
		"account_id",
		"successful_request_count",
		"provider_failure_count",
		"provider_transient_count",
		"rate_limit_count",
		"client_excluded_count",
		"platform_failure_count",
		"uncertain_count",
		"recent_provider_failure_count",
		"recent_provider_transient_count",
		"recent_rate_limit_count",
		"recent_uncertain_count",
	}).AddRow(int64(11), int64(90), int64(2), int64(3), int64(4), int64(5), int64(6), int64(7), int64(1), int64(2), int64(3), int64(4))

	mock.ExpectQuery(`(?s)WITH successful AS .*FROM usage_logs.*actual_cost > 0.*ul\.stream = TRUE.*requested_model.*inbound_endpoint.*classified AS .*FROM ops_error_logs.*oe\.stream = TRUE.*requested_model.*inbound_endpoint.*failures AS .*FULL OUTER JOIN failures`).
		WithArgs(pq.Array(accountIDs), start, end, "gpt-5", "/v1/responses").
		WillReturnRows(rows)

	repo := newUsageLogRepositoryWithSQL(nil, db)
	stats, err := repo.GetSmartSchedulerErrorStatsBatch(context.Background(), accountIDs, start, end, "gpt-5", "/v1/responses")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	require.Equal(t, int64(90), stats[11].SuccessfulRequestCount)
	require.Equal(t, int64(2), stats[11].ProviderFailureCount)
	require.Equal(t, int64(3), stats[11].ProviderTransientFailureCount)
	require.Equal(t, int64(4), stats[11].RateLimitCount)
	require.Equal(t, int64(5), stats[11].ClientExcludedCount)
	require.Equal(t, int64(6), stats[11].PlatformFailureCount)
	require.Equal(t, int64(7), stats[11].UncertainFailureCount)
	require.Equal(t, int64(1), stats[11].RecentProviderFailureCount)
	require.Equal(t, int64(2), stats[11].RecentProviderTransientCount)
	require.Equal(t, int64(3), stats[11].RecentRateLimitCount)
	require.Equal(t, int64(4), stats[11].RecentUncertainFailureCount)
}

func TestUsageLogRepositoryGetSmartSchedulerErrorStatsBatchEmpty(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	repo := newUsageLogRepositoryWithSQL(nil, db)
	stats, err := repo.GetSmartSchedulerErrorStatsBatch(context.Background(), nil, time.Time{}, time.Now(), "", "any")
	require.NoError(t, err)
	require.Empty(t, stats)
	require.NoError(t, mock.ExpectationsWereMet())
}
