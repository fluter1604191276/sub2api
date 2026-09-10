package repository

import (
	"context"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestMonitorDailyBillsDoesNotReadReservations(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := &channelMonitorRepository{db: db}
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.FixedZone("CST", 28800))
	mock.ExpectBegin()
	mock.ExpectExec(`SELECT pg_advisory_xact_lock`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`WITH candidates AS`).WithArgs(now, now).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectQuery(`FROM channel_monitor_daily_bills b`).
		WithArgs("2026-09-10", "2026-09-10", now).
		WillReturnRows(sqlmock.NewRows([]string{"date", "cost", "checks", "unknown", "failed", "partial", "account_cost", "costed"}).
			AddRow("2026-09-10", 0.25, 3, 1, 1, true, 0.025, 1))
	rows, err := repo.DailyBills(context.Background(), now, now)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, 0.25, rows[0].BaseCostUSD)
	require.EqualValues(t, 1, rows[0].UnknownCostChecks)
	require.NoError(t, mock.ExpectationsWereMet())
}
