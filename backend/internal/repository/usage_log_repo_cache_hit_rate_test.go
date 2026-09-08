package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestUsageLogRepositoryGetAccountCacheHitStatsBatch(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	accountIDs := []int64{11, 22}
	start := time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC)
	rows := sqlmock.NewRows([]string{
		"account_id",
		"requests",
		"input_tokens",
		"cache_creation_tokens",
		"cache_read_tokens",
	}).AddRow(
		int64(11),
		int64(4),
		int64(100),
		int64(50),
		int64(300),
	)

	mock.ExpectQuery(`(?s)SELECT\s+account_id,\s+COUNT\(\*\).*FROM usage_logs.*WHERE account_id = ANY\(\$1\) AND created_at >= \$2.*GROUP BY account_id`).
		WithArgs(pq.Array(accountIDs), start).
		WillReturnRows(rows)

	repo := newUsageLogRepositoryWithSQL(nil, db)
	stats, err := repo.GetAccountCacheHitStatsBatch(context.Background(), accountIDs, start)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())

	require.Equal(t, int64(4), stats[11].Requests)
	require.Equal(t, int64(100), stats[11].InputTokens)
	require.Equal(t, int64(50), stats[11].CacheCreationTokens)
	require.Equal(t, int64(300), stats[11].CacheReadTokens)
	require.NotNil(t, stats[11].CacheHitRate)
	require.InDelta(t, 66.6666667, *stats[11].CacheHitRate, 0.000001)

	// The API returns a zero-value summary for accounts with no rows, with no
	// misleading zero-percent rate.
	require.Equal(t, int64(0), stats[22].Requests)
	require.Nil(t, stats[22].CacheHitRate)
}

func TestUsageLogRepositoryGetAccountCacheHitStatsBatchEmpty(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	repo := newUsageLogRepositoryWithSQL(nil, db)
	stats, err := repo.GetAccountCacheHitStatsBatch(context.Background(), nil, time.Time{})
	require.NoError(t, err)
	require.Empty(t, stats)
	require.NoError(t, mock.ExpectationsWereMet())
}
