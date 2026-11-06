//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOpenAIIQBudgetPostgresDistinguishesActiveAndUnknownExpenses(t *testing.T) {
	f := newIQSettlementFixture(t)
	ctx := context.Background()
	_, err := integrationDB.ExecContext(ctx, `UPDATE openai_iq_budget_reservations SET reserved_usd=.5 WHERE run_id=$1`, f.runID)
	require.NoError(t, err)
	b, err := f.repo.Budget(ctx, 1)
	require.NoError(t, err)
	require.InDelta(t, .5, b.ReservedUSD, 1e-10)
	require.Zero(t, b.UnconfirmedUSD, "a live pending request may still produce usage")
	require.Nil(t, b.UnconfirmedSince)

	require.NoError(t, f.repo.CompleteResult(ctx, f.runID, service.IQResult{GroupID: f.groupIDs[0], Status: "error", Error: "synthetic disconnect"}))
	var checked time.Time
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT checked_at FROM openai_iq_results WHERE run_id=$1 AND group_id=$2`, f.runID, f.groupIDs[0]).Scan(&checked))
	restarted := NewOpenAIIQRepository(integrationDB)
	b, err = restarted.Budget(ctx, 1)
	require.NoError(t, err)
	require.Zero(t, b.SpentUSD)
	require.InDelta(t, .5, b.ReservedUSD, 1e-10)
	require.InDelta(t, .5, b.UnconfirmedUSD, 1e-10)
	require.NotNil(t, b.UnconfirmedSince)
	require.True(t, checked.Equal(*b.UnconfirmedSince), "grace starts at completion, not on each budget read")

	require.NoError(t, restarted.FinishRun(ctx, f.runID))
	b, err = restarted.Budget(ctx, 1)
	require.NoError(t, err)
	require.InDelta(t, .5, b.ReservedUSD, 1e-10, "finish/restart must preserve unknown expense")
	_, err = integrationDB.ExecContext(ctx, `INSERT INTO usage_logs
		(user_id,api_key_id,account_id,group_id,request_id,model,total_cost,account_stats_cost,account_rate_multiplier,user_agent)
		VALUES ($1,$2,$3,$4,$5,'gpt-6-astra',.9,.009,2,'sub2api-openai-iq')`, f.userID, f.keys[0].ID, f.accountID, f.groupIDs[0], f.requestID)
	require.NoError(t, err)
	b, err = restarted.Budget(ctx, 1)
	require.NoError(t, err)
	require.InDelta(t, .018, b.SpentUSD, 1e-10)
	require.Zero(t, b.ReservedUSD)
	require.Zero(t, b.UnconfirmedUSD)
	require.Nil(t, b.UnconfirmedSince)
}

func TestOpenAIIQBudgetPostgresRecognizesExpiredLeaseHold(t *testing.T) {
	f := newIQSettlementFixture(t)
	ctx := context.Background()
	_, err := integrationDB.ExecContext(ctx, `UPDATE openai_iq_runs SET lease_expires_at=NOW()-INTERVAL '1 minute' WHERE id=$1`, f.runID)
	require.NoError(t, err)
	b, err := f.repo.Budget(ctx, 1)
	require.NoError(t, err)
	require.InDelta(t, .1, b.UnconfirmedUSD, 1e-10)
	require.NotNil(t, b.UnconfirmedSince)
	require.Greater(t, time.Since(*b.UnconfirmedSince), 50*time.Second)
}

func TestOpenAIIQFinishRunPostgresRecordsFallbackWithoutChangingHistory(t *testing.T) {
	f := newIQSettlementFixture(t)
	ctx := context.Background()
	require.NoError(t, f.repo.CompleteResult(ctx, f.runID, service.IQResult{GroupID: f.groupIDs[0], Status: "error", Error: "specific reason"}))
	require.NoError(t, f.repo.FinishRun(ctx, f.runID))
	var known, fallback, status string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT error FROM openai_iq_results WHERE run_id=$1 AND group_id=$2`, f.runID, f.groupIDs[0]).Scan(&known))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT error,status FROM openai_iq_results WHERE run_id=$1 AND group_id=$2`, f.runID, f.groupIDs[1]).Scan(&fallback, &status))
	require.Equal(t, "specific reason", known)
	require.Equal(t, "skipped", status)
	require.Contains(t, fallback, "具体终止原因")
	b, err := f.repo.Budget(ctx, 1)
	require.NoError(t, err)
	require.InDelta(t, .1, b.ReservedUSD, 1e-10)
}
