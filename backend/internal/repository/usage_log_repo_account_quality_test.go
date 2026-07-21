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
	end := start.Add(24 * time.Hour)
	rows := sqlmock.NewRows([]string{
		"account_id",
		"last_10_count",
		"last_10_first_count",
		"last_10_first_avg",
		"last_10_duration_avg",
		"last_100_count",
		"last_100_first_count",
		"last_100_first_avg",
		"last_100_duration_avg",
	}).AddRow(int64(11), int64(10), int64(8), 825.5, 4200.0, int64(72), int64(60), 930.25, 5100.0).
		AddRow(int64(22), int64(4), int64(0), nil, 7000.0, int64(4), int64(0), nil, 7000.0)

	mock.ExpectQuery(`(?s)WITH ranked AS.*AND ul\.stream = TRUE`).
		WithArgs(pq.Array(accountIDs), start, end).
		WillReturnRows(rows)

	repo := newUsageLogRepositoryWithSQL(nil, db)
	stats, err := repo.GetAccountQualityStatsBatch(context.Background(), accountIDs, start, end)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())

	require.Equal(t, int64(10), stats[11].Last10.SampleCount)
	require.Equal(t, int64(8), stats[11].Last10.FirstTokenSampleCount)
	require.NotNil(t, stats[11].Last10.AverageFirstTokenMs)
	require.InDelta(t, 825.5, *stats[11].Last10.AverageFirstTokenMs, 0.001)
	require.Equal(t, int64(72), stats[11].Last100.SampleCount)
	require.Nil(t, stats[22].Last10.AverageFirstTokenMs)
	require.NotNil(t, stats[22].Last10.AverageDurationMs)
}

func TestUsageLogRepositoryGetAccountQualityStatsBatchEmpty(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	repo := newUsageLogRepositoryWithSQL(nil, db)
	stats, err := repo.GetAccountQualityStatsBatch(context.Background(), nil, time.Time{}, time.Now())
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
	groupIDs := []int64{7, 9}
	rows := sqlmock.NewRows([]string{
		"group_id",
		"last_10_count", "last_10_first_count", "last_10_first_avg", "last_10_duration_avg",
		"last_100_count", "last_100_first_count", "last_100_first_avg", "last_100_duration_avg",
	}).AddRow(7, int64(10), int64(9), 640.0, 6100.0, int64(84), int64(70), 920.0, 7300.0)

	mock.ExpectQuery(`(?s)PARTITION BY ul\.group_id.*AND ul\.stream = TRUE`).
		WithArgs(pq.Array(groupIDs), start, end).
		WillReturnRows(rows)

	repo := newUsageLogRepositoryWithSQL(nil, db)
	stats, err := repo.GetGroupQualityStatsBatch(context.Background(), groupIDs, start, end)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())

	require.Equal(t, int64(10), stats[7].Last10.SampleCount)
	require.Equal(t, int64(9), stats[7].Last10.FirstTokenSampleCount)
	require.NotNil(t, stats[7].Last100.AverageDurationMs)
	require.InDelta(t, 7300, *stats[7].Last100.AverageDurationMs, 0.001)
}
