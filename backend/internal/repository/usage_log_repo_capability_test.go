package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestUsageLogRepositoryGetAccountCapabilitySummaryBatchAggregatesAcrossProtocols(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	accountIDs := []int64{11, 22}
	lastSuccess := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	updated := time.Date(2026, 9, 22, 12, 1, 0, 0, time.UTC)
	rows := sqlmock.NewRows([]string{
		"account_id", "capability_type", "sample_count", "success_count", "failure_count",
		"last_success_at", "last_failure_at", "last_reason", "updated_at",
	}).AddRow(
		int64(11), "function_tool", int64(4), int64(3), int64(1),
		lastSuccess, nil, "tool_roundtrip_observed", updated,
	).AddRow(
		int64(11), "terminal_contract", int64(2), int64(0), int64(2),
		nil, updated, "terminal_not_supported", updated,
	)

	mock.ExpectQuery(`(?s)SELECT\s+account_id,\s+capability_type.*FROM account_capability_states.*WHERE account_id = ANY\(\$1\).*GROUP BY account_id, capability_type`).
		WithArgs(pq.Array(accountIDs)).
		WillReturnRows(rows)

	repo := newUsageLogRepositoryWithSQL(nil, db)
	stats, err := repo.GetAccountCapabilitySummaryBatch(context.Background(), accountIDs)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())

	require.Equal(t, int64(4), stats[11].FunctionTool.SampleCount)
	require.Equal(t, int64(3), stats[11].FunctionTool.SuccessCount)
	require.Equal(t, "capable", stats[11].FunctionTool.State)
	require.Equal(t, int64(2), stats[11].TerminalContract.FailureCount)
	require.Equal(t, "unsupported", stats[11].TerminalContract.State)
	require.Equal(t, "unknown", stats[22].FunctionTool.State)
}
