//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestChannelMonitorV3ProbeClaimLifecycle(t *testing.T) {
	ctx := context.Background()
	var groupID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO groups(name,platform,status) VALUES('cmv3-probe-it','openai','active') RETURNING id`).Scan(&groupID))
	t.Cleanup(func() { _, _ = integrationDB.ExecContext(ctx, `DELETE FROM groups WHERE id=$1`, groupID) })
	repo := NewChannelMonitorV3Repository(integrationDB).(*channelMonitorV3Repository)
	_, err := integrationDB.ExecContext(ctx, `INSERT INTO channel_monitor_v3_settings(singleton,enabled,daily_budget_usd) VALUES(true,true,2) ON CONFLICT(singleton) DO UPDATE SET enabled=true,daily_budget_usd=2`)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `INSERT INTO channel_monitor_v3_groups(group_id,visible,probe_interval_seconds,test_model) VALUES($1,true,60,'gpt-4o')`, groupID)
	require.NoError(t, err)
	cfg := service.ChannelMonitorV3GroupConfig{GroupID: groupID, Platform: "openai", Status: "active", Visible: true, ProbeIntervalSeconds: 60, TestModel: "gpt-4o"}
	job, err := repo.TryClaimProbe(ctx, cfg, time.Now().UTC(), 2)
	require.NoError(t, err)
	require.NotNil(t, job)
	again, err := repo.TryClaimProbe(ctx, cfg, time.Now().UTC(), 2)
	require.NoError(t, err)
	require.Nil(t, again)
	require.NoError(t, repo.SettleProbe(ctx, job.DispatchID, service.UpstreamAttemptCostSummary{AttemptCount: 1, KnownCostUSD: .1}))
	var status string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT status FROM channel_monitor_v3_probe_dispatches WHERE dispatch_id=$1::uuid`, job.DispatchID).Scan(&status))
	require.Equal(t, "claimed", status)
	require.NoError(t, repo.MarkProbeDispatched(ctx, job.DispatchID))
	require.Error(t, repo.MarkProbeDispatched(ctx, job.DispatchID))
	require.NoError(t, repo.FinishProbe(ctx, job.DispatchID, true))
	require.NoError(t, repo.SettleProbe(ctx, job.DispatchID, service.UpstreamAttemptCostSummary{AttemptCount: 1, KnownCostUSD: .1}))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT status FROM channel_monitor_v3_probe_dispatches WHERE dispatch_id=$1::uuid`, job.DispatchID).Scan(&status))
	require.Equal(t, "settled", status)

	_, err = integrationDB.ExecContext(ctx, `UPDATE channel_monitor_v3_groups SET last_probe_at=NULL WHERE group_id=$1`, groupID)
	require.NoError(t, err)
	unknown, err := repo.TryClaimProbe(ctx, cfg, time.Now().UTC(), 2)
	require.NoError(t, err)
	require.NotNil(t, unknown)
	require.NoError(t, repo.MarkProbeDispatched(ctx, unknown.DispatchID))
	require.NoError(t, repo.FinishProbe(ctx, unknown.DispatchID, true))
	require.NoError(t, repo.SettleProbe(ctx, unknown.DispatchID, service.UpstreamAttemptCostSummary{AttemptCount: 1, UnknownCount: 1}))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT status FROM channel_monitor_v3_probe_dispatches WHERE dispatch_id=$1::uuid`, unknown.DispatchID).Scan(&status))
	require.Equal(t, "uncertain", status)
}
