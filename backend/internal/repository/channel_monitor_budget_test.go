package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestChannelMonitorReserveDailyBudgetUnlimitedUsesCalendarDateAndAccumulates(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	day := time.Date(2026, time.September, 9, 0, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	mock.ExpectQuery(`(?s)INSERT INTO channel_monitor_daily_budget_ledger.*\$3 = 0.*RETURNING estimated_cost_usd`).
		WithArgs("2026-09-09", 0.125, 0.0).
		WillReturnRows(sqlmock.NewRows([]string{"estimated_cost_usd"}).AddRow(0.375))

	repo := &channelMonitorRepository{db: db}
	admitted, err := repo.ReserveDailyBudget(context.Background(), day, 0.125, 0)
	require.NoError(t, err)
	require.True(t, admitted)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestChannelMonitorReserveDailyBudgetRejectsInsufficientBudget(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectQuery(`(?s)INSERT INTO channel_monitor_daily_budget_ledger.*RETURNING estimated_cost_usd`).
		WithArgs("2026-09-09", 0.6, 1.0).
		WillReturnRows(sqlmock.NewRows([]string{"estimated_cost_usd"}))

	repo := &channelMonitorRepository{db: db}
	admitted, err := repo.ReserveDailyBudget(context.Background(), time.Date(2026, 9, 9, 22, 0, 0, 0, time.UTC), 0.6, 1)
	require.NoError(t, err)
	require.False(t, admitted)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestChannelMonitorReserveDailyBudgetFailsClosedOnDatabaseError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectQuery(`(?s)INSERT INTO channel_monitor_daily_budget_ledger`).
		WithArgs("2026-09-09", 0.2, 1.0).
		WillReturnError(errors.New("ledger unavailable"))

	repo := &channelMonitorRepository{db: db}
	admitted, err := repo.ReserveDailyBudget(context.Background(), time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC), 0.2, 1)
	require.ErrorContains(t, err, "ledger unavailable")
	require.False(t, admitted)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestChannelMonitorSettleDailyBudgetUsesCalendarDate(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectExec(`(?s)UPDATE channel_monitor_daily_budget_ledger.*budget_date = \$1::date`).
		WithArgs("2026-09-10", 0.4, 0.15).
		WillReturnResult(sqlmock.NewResult(0, 1))

	repo := &channelMonitorRepository{db: db}
	err = repo.SettleDailyBudget(context.Background(), time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC), 0.4, 0.15)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestChannelMonitorRecordAndReadUnpricedProbeCount(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectExec(`(?s)INSERT INTO channel_monitor_daily_budget_ledger.*unpriced_probes`).
		WithArgs("2026-09-09").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`(?s)SELECT COALESCE.*unpriced_probes.*\$1::date`).
		WithArgs("2026-09-09").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(2)))

	repo := &channelMonitorRepository{db: db}
	day := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	require.NoError(t, repo.RecordUnpricedProbe(context.Background(), day))
	count, err := repo.UnpricedProbeCount(context.Background(), day)
	require.NoError(t, err)
	require.Equal(t, int64(2), count)
	require.NoError(t, mock.ExpectationsWereMet())
}
