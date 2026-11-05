//go:build integration

package repository

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestChannelMonitorV3ProbeClaimLifecycle(t *testing.T) {
	ctx := context.Background()
	var groupID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO groups(name,platform,status) VALUES('cmv3-probe-it','openai','active') RETURNING id`).Scan(&groupID))
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM channel_monitor_v3_probe_dispatches WHERE group_id=$1`, groupID)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM groups WHERE id=$1`, groupID)
	})
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

	// A known cost above its durable reservation remains reconcilable but holds
	// the reservation as uncertain; a repeated settlement is idempotent.
	overID := "00000000-0000-4000-8000-000000000099"
	_, err = integrationDB.ExecContext(ctx, `INSERT INTO channel_monitor_v3_probe_dispatches(dispatch_id,group_id,model,platform,budget_day,reserved_usd,client_request_id,status) VALUES($1::uuid,$2,'gpt-4o','openai',$3,.01,'cmv3-over','completed')`, overID, groupID, job.BudgetDay)
	require.NoError(t, err)
	require.NoError(t, repo.SettleProbe(ctx, overID, service.UpstreamAttemptCostSummary{AttemptCount: 1, KnownCostUSD: .1}))
	var actual float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT status,actual_cost_usd FROM channel_monitor_v3_probe_dispatches WHERE dispatch_id=$1::uuid`, overID).Scan(&status, &actual))
	require.Equal(t, "uncertain", status)
	require.InDelta(t, .1, actual, 1e-12)
	require.NoError(t, repo.SettleProbe(ctx, overID, service.UpstreamAttemptCostSummary{AttemptCount: 1, KnownCostUSD: .2}))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT status,actual_cost_usd FROM channel_monitor_v3_probe_dispatches WHERE dispatch_id=$1::uuid`, overID).Scan(&status, &actual))
	require.Equal(t, "uncertain", status)
	require.InDelta(t, .1, actual, 1e-12)
	var held float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT reserved_usd FROM channel_monitor_v3_probe_dispatches WHERE dispatch_id=$1::uuid`, overID).Scan(&held))
	require.InDelta(t, .01, held, 1e-12)
	budget, err := repo.ProbeBudget(ctx, job.BudgetDay, 3)
	require.NoError(t, err)
	require.InDelta(t, .2, budget.SpentUSD, 1e-10)
	require.InDelta(t, .9, budget.RemainingUSD, 1e-10)

	// Reconstructing the repository after a process interruption cannot replay
	// a sent dispatch or make its full daily reservation available again.
	crashID := "00000000-0000-4000-8000-000000000098"
	crashNow := time.Date(2001, 1, 3, 0, 0, 0, 0, time.UTC)
	_, err = integrationDB.ExecContext(ctx, `INSERT INTO channel_monitor_v3_probe_dispatches(dispatch_id,group_id,model,platform,budget_day,reserved_usd,client_request_id,status) VALUES($1::uuid,$2,'gpt-4o','openai','2001-01-03',2,'cmv3-crash','sent')`, crashID, groupID)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `UPDATE channel_monitor_v3_groups SET last_probe_at=NULL WHERE group_id=$1`, groupID)
	require.NoError(t, err)
	restarted := NewChannelMonitorV3Repository(integrationDB).(*channelMonitorV3Repository)
	pending, err := restarted.ListPendingProbes(ctx)
	require.NoError(t, err)
	for _, item := range pending {
		require.NotEqual(t, crashID, item.DispatchID)
	}
	replayed, err := restarted.TryClaimProbe(ctx, cfg, crashNow, 2)
	require.NoError(t, err)
	require.Nil(t, replayed)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT status FROM channel_monitor_v3_probe_dispatches WHERE dispatch_id=$1::uuid`, crashID).Scan(&status))
	require.Equal(t, "sent", status)
}

func channelMonitorV3ProbeIntegrationGroup(t *testing.T, name string) service.ChannelMonitorV3GroupConfig {
	t.Helper()
	var id int64
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), `INSERT INTO groups(name,platform,status) VALUES($1,'openai','active') RETURNING id`, name).Scan(&id))
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM channel_monitor_v3_probe_dispatches WHERE group_id=$1`, id)
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM groups WHERE id=$1`, id)
	})
	_, err := integrationDB.ExecContext(context.Background(), `INSERT INTO channel_monitor_v3_groups(group_id,visible,probe_interval_seconds,test_model) VALUES($1,true,60,'gpt-4o')`, id)
	require.NoError(t, err)
	return service.ChannelMonitorV3GroupConfig{GroupID: id, Platform: "openai", Status: "active", Visible: true, ProbeIntervalSeconds: 60, TestModel: "gpt-4o"}
}

func TestChannelMonitorV3ProbeConcurrentClaimsAndPartialOverrun(t *testing.T) {
	ctx := context.Background()
	repo := NewChannelMonitorV3Repository(integrationDB).(*channelMonitorV3Repository)
	_, err := integrationDB.ExecContext(ctx, `UPDATE channel_monitor_v3_settings SET enabled=true,daily_budget_usd=2 WHERE singleton=true`)
	require.NoError(t, err)
	groups := []service.ChannelMonitorV3GroupConfig{
		channelMonitorV3ProbeIntegrationGroup(t, "cmv3-concurrent-a"),
		channelMonitorV3ProbeIntegrationGroup(t, "cmv3-concurrent-b"),
	}
	type claimResult struct {
		job *service.ChannelMonitorV3ProbeJob
		err error
	}
	start := make(chan struct{})
	results := make(chan claimResult, 2)
	var wg sync.WaitGroup
	for _, group := range groups {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			job, err := repo.TryClaimProbe(ctx, group, time.Now(), 2)
			results <- claimResult{job, err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	var claimed *service.ChannelMonitorV3ProbeJob
	for result := range results {
		require.NoError(t, result.err)
		if result.job != nil {
			require.Nil(t, claimed, "concurrent workers cannot reserve the same daily allowance twice")
			claimed = result.job
		}
	}
	require.NotNil(t, claimed)
	require.NoError(t, repo.MarkProbeDispatched(ctx, claimed.DispatchID))
	require.NoError(t, repo.FinishProbe(ctx, claimed.DispatchID, true))
	require.NoError(t, repo.SettleProbe(ctx, claimed.DispatchID, service.UpstreamAttemptCostSummary{AttemptCount: 2, UnknownCount: 1, KnownCostUSD: 2.1}))
	budget, err := repo.ProbeBudget(ctx, claimed.BudgetDay, 2)
	require.NoError(t, err)
	require.InDelta(t, 2.1, budget.SpentUSD, 1e-10, "partial known costs remain visible even with another unknown attempt")
	require.Zero(t, budget.RemainingUSD)
	var status string
	var held float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT status,reserved_usd FROM channel_monitor_v3_probe_dispatches WHERE dispatch_id=$1::uuid`, claimed.DispatchID).Scan(&status, &held))
	require.Equal(t, "uncertain", status)
	require.InDelta(t, 2, held, 1e-10)
	for _, group := range groups {
		job, err := repo.TryClaimProbe(ctx, group, time.Now(), 2)
		require.NoError(t, err)
		require.Nil(t, job)
	}
}

func TestChannelMonitorV3ProbeSendAdmissionRechecksSavedSettings(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("disabled=%t", disabled), func(t *testing.T) {
			ctx := context.Background()
			repo := NewChannelMonitorV3Repository(integrationDB).(*channelMonitorV3Repository)
			_, err := integrationDB.ExecContext(ctx, `UPDATE channel_monitor_v3_settings SET enabled=true,daily_budget_usd=2 WHERE singleton=true`)
			require.NoError(t, err)
			group := channelMonitorV3ProbeIntegrationGroup(t, "cmv3-config-before-send")
			job, err := repo.TryClaimProbe(ctx, group, time.Now(), 2)
			require.NoError(t, err)
			require.NotNil(t, job)
			_, err = repo.SaveSettings(ctx, service.ChannelMonitorV3Settings{Enabled: !disabled, DailyBudgetUSD: 1})
			require.NoError(t, err)
			require.Error(t, repo.MarkProbeDispatched(ctx, job.DispatchID))
			var status string
			var reserved float64
			require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT status,reserved_usd FROM channel_monitor_v3_probe_dispatches WHERE dispatch_id=$1::uuid`, job.DispatchID).Scan(&status, &reserved))
			require.Equal(t, "undispatched", status)
			require.Zero(t, reserved)
		})
	}
}
