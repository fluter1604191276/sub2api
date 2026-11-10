package service

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestResponseDiagnosticRetentionUsesExistingZeroSemantics(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	s := &OpsCleanupService{db: db, cfg: &config.Config{}, effective: opsCleanupEffectiveConfig{OpsCleanupConfig: config.OpsCleanupConfig{ErrorLogRetentionDays: 0, MinuteMetricsRetentionDays: -1, HourlyMetricsRetentionDays: -1}, SystemLogRetentionDays: -1}}
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM ops_error_logs`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM ops_response_diagnostics`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))
	mock.ExpectExec(`TRUNCATE TABLE ops_response_diagnostics`).WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM ops_ingress_reject_aggregates`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM ops_alert_events`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	counts, err := s.runCleanupOnce(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 3, counts.responseDiagnostics)
	require.NoError(t, mock.ExpectationsWereMet())
}
