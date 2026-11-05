package repository

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestChannelMonitorV3ListGroupsIncludesDisabledAndAllPlatforms(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	rows := sqlmock.NewRows([]string{"id", "name", "platform", "status", "visible", "probe_interval_seconds", "test_model"}).
		AddRow(1, "disabled", "gemini", "disabled", false, 600, "").
		AddRow(2, "other", "custom-provider", "active", true, 60, "m")
	mock.ExpectQuery(`(?s)FROM groups g LEFT JOIN channel_monitor_v3_groups.*WHERE g\.deleted_at IS NULL.*ORDER BY g\.sort_order,g\.id`).WillReturnRows(rows)
	got, err := (&channelMonitorV3Repository{db: db}).ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "disabled", got[0].Status)
	require.Equal(t, "custom-provider", got[1].Platform)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestChannelMonitorV3SaveSettingsRoundTrip(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectBegin()
	mock.ExpectExec(`SELECT pg_advisory_xact_lock`).WithArgs(channelMonitorV3IdentityLock).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`(?s)INSERT INTO channel_monitor_v3_settings.*ON CONFLICT\(singleton\).*RETURNING enabled,daily_budget_usd`).
		WithArgs(false, 2.0).
		WillReturnRows(sqlmock.NewRows([]string{"enabled", "daily_budget_usd"}).AddRow(false, 2.0))
	mock.ExpectCommit()
	got, err := (&channelMonitorV3Repository{db: db}).SaveSettings(context.Background(), service.ChannelMonitorV3Settings{Enabled: false, DailyBudgetUSD: 2})
	require.NoError(t, err)
	require.False(t, got.Enabled)
	require.Equal(t, 2.0, got.DailyBudgetUSD)
}

func TestChannelMonitorV3SaveSettingsEnabledIdentityFailureRollsBack(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectBegin()
	mock.ExpectExec(`SELECT pg_advisory_xact_lock`).WithArgs(channelMonitorV3IdentityLock).WillReturnResult(sqlmock.NewResult(0, 1))
	// Identity lookup fails before the settings upsert, so enabling cannot leave
	// a partially persisted enabled flag.
	mock.ExpectQuery(`(?s)SELECT m\.user_id,u\.balance,m\.identity_marker.*channel_monitor_v3_identity`).WillReturnError(sqlmock.ErrCancelled)
	mock.ExpectRollback()
	_, err = (&channelMonitorV3Repository{db: db}).SaveSettings(context.Background(), service.ChannelMonitorV3Settings{Enabled: true, DailyBudgetUSD: 2})
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
