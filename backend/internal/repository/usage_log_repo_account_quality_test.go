package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestUsageLogRepositoryGetAccountQualityStatsBatch(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	accountIDs := []int64{11, 22}
	start := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	realtimeStart := start.Add(23 * time.Hour)
	end := start.Add(24 * time.Hour)
	lastSuccess := end.Add(-5 * time.Minute)
	lastError := end.Add(-8 * time.Minute)
	rows := sqlmock.NewRows(accountQualityResultColumns("account_id")).AddRow(
		int64(11),
		int64(10), int64(8), 700.0, 3900.0, 650.0, 1800.0, int64(7), 48.0, 22.0,
		int64(18), int64(15), 760.0, 4100.0, 700.0, 2100.0, int64(14), 46.0, 20.0,
		int64(10), int64(8), 825.5, 4200.0, 780.0, 2400.0, int64(8), 44.0, 18.0,
		int64(72), int64(60), 930.25, 5100.0, 850.0, 3000.0, int64(55), 42.0, 16.0,
		int64(18), int64(2), lastSuccess, lastError,
	).AddRow(
		int64(22),
		int64(0), int64(0), nil, nil, nil, nil, int64(0), nil, nil,
		int64(0), int64(0), nil, nil, nil, nil, int64(0), nil, nil,
		int64(4), int64(0), nil, 7000.0, nil, nil, int64(0), nil, nil,
		int64(4), int64(0), nil, 7000.0, nil, nil, int64(0), nil, nil,
		int64(0), int64(0), end.Add(-2*time.Hour), nil,
	)

	mock.ExpectQuery(`(?s)WITH successful AS MATERIALIZED.*output_tokens >= 32.*first_token_ms >= 0.*duration_ms - ul\.first_token_ms >= 1000.*ul\.request_type <> 6.*ul\.stream = TRUE.*sub2api-channel-monitor/.*quality_input AS.*request_rank <= 100.*PERCENTILE_CONT\(0\.5\).*PERCENTILE_CONT\(0\.9\).*activity AS.*ops_error_logs`).
		WithArgs(pq.Array(accountIDs), start, realtimeStart, end).
		WillReturnRows(rows)

	repo := newUsageLogRepositoryWithSQL(nil, db)
	stats, err := repo.GetAccountQualityStatsBatch(context.Background(), accountIDs, start, realtimeStart, end)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())

	require.Equal(t, int64(10), stats[11].Recent1h.Last10.SampleCount)
	require.Equal(t, int64(8), stats[11].Recent1h.Last10.FirstTokenSampleCount)
	require.NotNil(t, stats[11].Recent1h.Last10.AverageFirstTokenMs)
	require.InDelta(t, 700.0, *stats[11].Recent1h.Last10.AverageFirstTokenMs, 0.001)
	require.InDelta(t, 3900.0, *stats[11].Recent1h.Last10.AverageDurationMs, 0.001)
	require.InDelta(t, 650.0, *stats[11].Recent1h.Last10.P50FirstTokenMs, 0.001)
	require.InDelta(t, 1800.0, *stats[11].Recent1h.Last10.P90FirstTokenMs, 0.001)
	require.Equal(t, int64(7), stats[11].Recent1h.Last10.GenerationSampleCount)
	require.InDelta(t, 48.0, *stats[11].Recent1h.Last10.P50GenerationTokensPerSecond, 0.001)
	require.InDelta(t, 22.0, *stats[11].Recent1h.Last10.P10GenerationTokensPerSecond, 0.001)
	require.Equal(t, int64(72), stats[11].Last24h.Last100.SampleCount)
	require.Equal(t, int64(18), stats[11].SuccessfulRequests1h)
	require.Equal(t, int64(2), stats[11].FailedRequests1h)
	require.Equal(t, &lastSuccess, stats[11].LastSuccessAt)
	require.Equal(t, &lastError, stats[11].LastErrorAt)
	require.Nil(t, stats[22].Last24h.Last10.AverageFirstTokenMs)
	require.NotNil(t, stats[22].Last24h.Last10.AverageDurationMs)
}

func TestUsageLogRepositoryGetAccountQualityStatsBatchEmpty(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	repo := newUsageLogRepositoryWithSQL(nil, db)
	stats, err := repo.GetAccountQualityStatsBatch(context.Background(), nil, time.Time{}, time.Time{}, time.Now())
	require.NoError(t, err)
	require.Empty(t, stats)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUsageLogRepositoryGetGroupQualityStatsBatch(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	start := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	realtimeStart := end.Add(-time.Hour)
	groupIDs := []int64{7, 9}
	rows := sqlmock.NewRows(accountQualityResultColumns("group_id")).AddRow(
		7,
		int64(5), int64(5), 540.0, 5900.0, 500.0, 1200.0, int64(5), 55.0, 30.0,
		int64(5), int64(5), 540.0, 5900.0, 500.0, 1200.0, int64(5), 55.0, 30.0,
		int64(10), int64(9), 640.0, 6100.0, 600.0, 1800.0, int64(9), 50.0, 25.0,
		int64(84), int64(70), 920.0, 7300.0, 850.0, 3200.0, int64(68), 45.0, 20.0,
		int64(5), int64(0), end.Add(-time.Minute), nil,
	)

	mock.ExpectQuery(`(?s)WITH successful AS MATERIALIZED.*ul\.group_id.*ul\.request_type <> 6.*ul\.stream = TRUE.*sub2api-channel-monitor/.*PARTITION BY group_id.*quality_input AS.*activity AS.*ops_error_logs`).
		WithArgs(pq.Array(groupIDs), start, realtimeStart, end).
		WillReturnRows(rows)

	repo := newUsageLogRepositoryWithSQL(nil, db)
	stats, err := repo.GetGroupQualityStatsBatch(context.Background(), groupIDs, start, realtimeStart, end)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())

	require.Equal(t, int64(5), stats[7].Recent1h.Last10.SampleCount)
	require.Equal(t, int64(10), stats[7].Last24h.Last10.SampleCount)
	require.Equal(t, int64(9), stats[7].Last24h.Last10.FirstTokenSampleCount)
	require.NotNil(t, stats[7].Last24h.Last100.AverageDurationMs)
	require.InDelta(t, 7300, *stats[7].Last24h.Last100.AverageDurationMs, 0.001)
	require.InDelta(t, 850, *stats[7].Last24h.Last100.P50FirstTokenMs, 0.001)
	require.InDelta(t, 20, *stats[7].Last24h.Last100.P10GenerationTokensPerSecond, 0.001)
}

func accountQualityResultColumns(entity string) []string {
	columns := []string{entity}
	for _, prefix := range []string{"realtime_last_10", "realtime_last_100", "last_10", "last_100"} {
		columns = append(columns,
			prefix+"_count",
			prefix+"_first_count",
			prefix+"_first_avg",
			prefix+"_duration_avg",
			prefix+"_first_p50",
			prefix+"_first_p90",
			prefix+"_generation_count",
			prefix+"_generation_p50",
			prefix+"_generation_p10",
		)
	}
	return append(columns, "successful_requests_1h", "failed_requests_1h", "last_success_at", "last_error_at")
}
