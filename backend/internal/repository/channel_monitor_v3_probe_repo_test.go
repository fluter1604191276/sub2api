package repository

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestChannelMonitorV3SettleProbeUnknownKeepsReservation(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE channel_monitor_v3_probe_dispatches SET status='uncertain',updated_at=NOW() WHERE dispatch_id=$1::uuid AND status='completed'")).
		WithArgs("00000000-0000-4000-8000-000000000001").WillReturnResult(sqlmock.NewResult(0, 1))
	repo := NewChannelMonitorV3Repository(db).(service.ChannelMonitorV3ProbeRepository)
	require.NoError(t, repo.SettleProbe(context.Background(), "00000000-0000-4000-8000-000000000001", service.UpstreamAttemptCostSummary{AttemptCount: 1, UnknownCount: 1}))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestChannelMonitorV3ReleaseOnlyUndispatchedClaim(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE channel_monitor_v3_probe_dispatches SET status='undispatched',reserved_usd=0,updated_at=NOW() WHERE dispatch_id=$1::uuid AND status='claimed'")).
		WithArgs("00000000-0000-4000-8000-000000000002").WillReturnResult(sqlmock.NewResult(0, 0))
	repo := NewChannelMonitorV3Repository(db).(service.ChannelMonitorV3ProbeRepository)
	require.NoError(t, repo.ReleaseUndispatchedProbe(context.Background(), "00000000-0000-4000-8000-000000000002"))
	require.NoError(t, mock.ExpectationsWereMet())
}
