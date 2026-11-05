package repository

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestChannelMonitorV3GroupQualityWindowsIncludesObservedZeroChargeAndNoTimeFloor(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	rows := sqlmock.NewRows([]string{"group_id"})
	// Only query-shape assertions matter here: V3's raw metrics stay internal.
	mock.ExpectQuery(`(?s)WITH requested AS.*unnest\(\$1::bigint\[\]\).*CROSS JOIN LATERAL.*ul\.group_id = wanted\.group_id.*ul\.first_token_ms >= 0.*ul\.duration_ms >= 0.*ops_error_logs.*oe\.request_id = NULLIF\(ul\.request_id.*status_code.*cyber_policy.*ORDER BY ul\.created_at DESC, ul\.id DESC.*LIMIT 100.*ROW_NUMBER\(\).*request_rank <= 100`).
		WithArgs(sqlmock.AnyArg()).WillReturnRows(rows)
	repo := &usageLogRepository{sql: db}
	got, err := repo.GetChannelMonitorV3GroupQualityWindows(context.Background(), []int64{7})
	require.NoError(t, err)
	require.Empty(t, got)
	require.NoError(t, mock.ExpectationsWereMet())
}
