//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestChannelMonitorV3Identity(t *testing.T) {
	ctx := context.Background()
	repo := NewChannelMonitorV3Repository(integrationDB).(*channelMonitorV3Repository)
	_, _ = integrationDB.ExecContext(ctx, `DELETE FROM channel_monitor_v3_keys; DELETE FROM channel_monitor_v3_identity; DELETE FROM channel_monitor_v3_settings`)
	var groupID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO groups(name,platform,status,recovery_probe_model) VALUES('v3-identity','openai','active','m') RETURNING id`).Scan(&groupID))
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM channel_monitor_v3_keys; DELETE FROM channel_monitor_v3_identity; DELETE FROM channel_monitor_v3_settings; DELETE FROM groups WHERE id=$1`, groupID)
	})
	_, err := repo.SaveGroup(ctx, service.ChannelMonitorV3GroupConfig{GroupID: groupID, Visible: true, ProbeIntervalSeconds: 60, TestModel: "m"})
	require.NoError(t, err)
	_, err = repo.SaveSettings(ctx, service.ChannelMonitorV3Settings{Enabled: true, DailyBudgetUSD: 2})
	require.NoError(t, err)
	i1, err := repo.EnsureProbeIdentity(ctx)
	require.NoError(t, err)
	i2, err := repo.EnsureProbeIdentity(ctx)
	require.NoError(t, err)
	require.Equal(t, i1.UserID, i2.UserID)
	require.Equal(t, 1.0, i1.Balance)
	k1, err := repo.ProbeGroupKey(ctx, groupID)
	require.NoError(t, err)
	k2, err := repo.ProbeGroupKey(ctx, groupID)
	require.NoError(t, err)
	require.Equal(t, k1.ID, k2.ID)
	require.True(t, sameLoopbackIPs(k1.IPWhitelist))
	var rate float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT rate_multiplier FROM user_group_rate_multipliers WHERE user_id=$1 AND group_id=$2`, i1.UserID, groupID).Scan(&rate))
	require.Equal(t, 0.001, rate)
	var otherGroupID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO groups(name,platform,status) VALUES('v3-key-mismatch','openai','active') RETURNING id`).Scan(&otherGroupID))
	t.Cleanup(func() { _, _ = integrationDB.ExecContext(ctx, `DELETE FROM groups WHERE id=$1`, otherGroupID) })
	_, err = integrationDB.ExecContext(ctx, `UPDATE api_keys SET group_id=$1 WHERE id=$2`, otherGroupID, k1.ID)
	require.NoError(t, err)
	_, err = repo.ProbeGroupKey(ctx, groupID)
	require.ErrorContains(t, err, "ownership validation")
	_, err = integrationDB.ExecContext(ctx, `UPDATE api_keys SET group_id=$1 WHERE id=$2`, groupID, k1.ID)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `UPDATE user_group_rate_multipliers SET rate_multiplier=1 WHERE user_id=$1 AND group_id=$2`, i1.UserID, groupID)
	require.NoError(t, err)
	_, err = repo.ProbeGroupKey(ctx, groupID)
	require.ErrorContains(t, err, "multiplier validation")
	_, err = integrationDB.ExecContext(ctx, `UPDATE channel_monitor_v3_identity SET identity_marker='tampered'`)
	require.NoError(t, err)
	_, err = repo.EnsureProbeIdentity(ctx)
	require.Error(t, err)
}
