package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestUsageLogRepositoryGetSmartSchedulerQualityStatsBatch(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	accountIDs := []int64{11}
	start := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	realtimeStart := start.Add(23 * time.Hour)
	end := start.Add(24 * time.Hour)
	lastSuccess := end.Add(-time.Minute)
	rows := sqlmock.NewRows([]string{
		"account_id",
		"realtime_last_10_count", "realtime_last_10_first_count", "realtime_last_10_first_p50", "realtime_last_10_first_p90", "realtime_last_10_generation_count", "realtime_last_10_generation_p50", "realtime_last_10_generation_p10",
		"realtime_last_100_count", "realtime_last_100_first_count", "realtime_last_100_first_p50", "realtime_last_100_first_p90", "realtime_last_100_generation_count", "realtime_last_100_generation_p50", "realtime_last_100_generation_p10",
		"last_10_count", "last_10_first_count", "last_10_first_p50", "last_10_first_p90", "last_10_generation_count", "last_10_generation_p50", "last_10_generation_p10",
		"last_100_count", "last_100_first_count", "last_100_first_p50", "last_100_first_p90", "last_100_generation_count", "last_100_generation_p50", "last_100_generation_p10",
		"successful_requests_1h", "last_success_at",
	}).AddRow(
		int64(11),
		int64(10), int64(10), 900.0, 2500.0, int64(9), 52.0, 24.0,
		int64(18), int64(18), 1000.0, 3200.0, int64(16), 50.0, 22.0,
		int64(10), int64(10), 1100.0, 3500.0, int64(9), 48.0, 20.0,
		int64(72), int64(70), 1200.0, 4200.0, int64(65), 46.0, 18.0,
		int64(18), lastSuccess,
	)

	mock.ExpectQuery(`(?s)WITH successful AS MATERIALIZED.*requested_model.*inbound_endpoint.*PERCENTILE_CONT\(0\.5\).*PERCENTILE_CONT\(0\.9\).*generation_tokens_per_second`).
		WithArgs(pq.Array(accountIDs), start, realtimeStart, end, "gpt-5", "/v1/responses").
		WillReturnRows(rows)

	repo := newUsageLogRepositoryWithSQL(nil, db)
	stats, err := repo.GetSmartSchedulerQualityStatsBatch(context.Background(), accountIDs, start, realtimeStart, end, "gpt-5", "/v1/responses")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())

	window := stats[11].Recent1h.Last10
	require.Equal(t, int64(10), window.SampleCount)
	require.Equal(t, int64(9), window.GenerationSampleCount)
	require.InDelta(t, 900, *window.P50FirstTokenMs, 0.001)
	require.InDelta(t, 2500, *window.P90FirstTokenMs, 0.001)
	require.InDelta(t, 52, *window.P50GenerationTokensPerSecond, 0.001)
	require.InDelta(t, 24, *window.P10GenerationTokensPerSecond, 0.001)
	require.Equal(t, int64(72), stats[11].Last24h.Last100.SampleCount)
	require.Equal(t, int64(18), stats[11].SuccessfulRequests1h)
	require.Equal(t, &lastSuccess, stats[11].LastSuccessAt)
}

func TestUsageLogRepositoryGetSmartSchedulerQualityStatsBatchEmpty(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	repo := newUsageLogRepositoryWithSQL(nil, db)
	stats, err := repo.GetSmartSchedulerQualityStatsBatch(
		context.Background(),
		nil,
		time.Time{},
		time.Time{},
		time.Now(),
		"gpt-5",
		"/v1/responses",
	)
	require.NoError(t, err)
	require.Empty(t, stats)
	require.NoError(t, mock.ExpectationsWereMet())
}
