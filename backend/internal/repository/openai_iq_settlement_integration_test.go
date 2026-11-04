//go:build integration

package repository

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type iqSettlementFixture struct {
	repo      *openAIIQRepository
	runID     int64
	groupIDs  []int64
	keys      []*service.APIKey
	userID    int64
	otherUser int64
	accountID int64
	requestID string
}

func newIQSettlementFixture(t *testing.T) iqSettlementFixture {
	t.Helper()
	ctx := context.Background()
	f := iqSettlementFixture{
		repo:      NewOpenAIIQRepository(integrationDB).(*openAIIQRepository),
		groupIDs:  openAIIQGroups(t, 2),
		requestID: "client:iq-" + uuid.NewString(),
	}
	identity, err := f.repo.EnsureIdentity(ctx, f.groupIDs)
	require.NoError(t, err)
	f.userID = identity.UserID
	for _, groupID := range f.groupIDs {
		key, err := f.repo.GroupKey(ctx, f.userID, groupID)
		require.NoError(t, err)
		f.keys = append(f.keys, key)
	}
	f.runID = openAIIQRun(t, "actual-expense-"+uuid.NewString(), time.Now(), time.Now().Add(time.Hour))
	openAIIQPendingResults(t, f.runID, f.groupIDs)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO accounts (name, platform, type)
		VALUES ($1, 'openai', 'apikey') RETURNING id`, fmt.Sprintf("iq-expense-%d", f.runID)).Scan(&f.accountID))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO users (email,password_hash)
		VALUES ($1,'synthetic-test-hash') RETURNING id`, uuid.NewString()+"@internal.invalid").Scan(&f.otherUser))
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(), `DELETE FROM usage_logs WHERE account_id=$1`, f.accountID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(context.Background(), `DELETE FROM accounts WHERE id=$1`, f.accountID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(context.Background(), `DELETE FROM users WHERE id=$1`, f.otherUser)
		require.NoError(t, err)
	})
	ok, err := f.repo.ReserveBudget(ctx, f.runID, f.groupIDs[0], service.IQBudgetDate(), .1, 1)
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, f.repo.BindBudgetRequest(ctx, f.runID, f.groupIDs[0], f.keys[0].ID, f.requestID))
	return f
}

func TestOpenAIIQBudgetPostgresKeepsUnconfirmedHolds(t *testing.T) {
	for _, scenario := range []string{
		"no_usage", "wrong_request", "wrong_key", "wrong_user", "wrong_group", "wrong_agent",
		"negative_cost", "nan_cost", "negative_rate", "nan_rate",
	} {
		t.Run(scenario, func(t *testing.T) {
			f := newIQSettlementFixture(t)
			requestID, keyID, userID, groupID := f.requestID, f.keys[0].ID, f.userID, f.groupIDs[0]
			userAgent, base, rate := "sub2api-openai-iq", ".009", "2"
			switch scenario {
			case "wrong_request":
				requestID = "client:iq-" + uuid.NewString()
			case "wrong_key":
				keyID = f.keys[1].ID
			case "wrong_user":
				userID = f.otherUser
			case "wrong_group":
				groupID = f.groupIDs[1]
			case "wrong_agent":
				userAgent = "ordinary-client"
			case "negative_cost":
				base = "-1"
			case "nan_cost":
				base = "NaN"
			case "negative_rate":
				rate = "-1"
			case "nan_rate":
				rate = "NaN"
			}
			if scenario != "no_usage" {
				_, err := integrationDB.ExecContext(context.Background(), `INSERT INTO usage_logs
					(user_id,api_key_id,account_id,group_id,request_id,model,total_cost,account_stats_cost,account_rate_multiplier,user_agent)
					VALUES ($1,$2,$3,$4,$5,'gpt-6-astra',.9,$6::numeric,$7::numeric,$8)`, userID, keyID, f.accountID, groupID, requestID, base, rate, userAgent)
				require.NoError(t, err)
			}
			budget, err := f.repo.Budget(context.Background(), 1)
			require.NoError(t, err)
			require.Zero(t, budget.SpentUSD)
			require.InDelta(t, .1, budget.ReservedUSD, 1e-10)
			require.InDelta(t, .9, budget.RemainingUSD, 1e-10)
		})
	}
}

func TestOpenAIIQBudgetPostgresSettlesDelayedUsageConcurrently(t *testing.T) {
	f := newIQSettlementFixture(t)
	ctx := context.Background()
	before, err := f.repo.Budget(ctx, 1)
	require.NoError(t, err)
	require.InDelta(t, .1, before.ReservedUSD, 1e-10)
	_, err = integrationDB.ExecContext(ctx, `INSERT INTO usage_logs
		(user_id,api_key_id,account_id,group_id,request_id,model,total_cost,actual_cost,account_stats_cost,account_rate_multiplier,user_agent)
		VALUES ($1,$2,$3,$4,$5,'gpt-6-astra',.9,.777,.009,2,'sub2api-openai-iq')`, f.userID, f.keys[0].ID, f.accountID, f.groupIDs[0], f.requestID)
	require.NoError(t, err)
	var wg sync.WaitGroup
	errors := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errors <- NewOpenAIIQRepository(integrationDB).ReconcileBudget(ctx)
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	budget, err := f.repo.Budget(ctx, 1)
	require.NoError(t, err)
	require.InDelta(t, .018, budget.SpentUSD, 1e-10)
	require.Zero(t, budget.ReservedUSD)
	require.InDelta(t, .982, budget.RemainingUSD, 1e-10)
	_, err = integrationDB.ExecContext(ctx, `UPDATE usage_logs SET account_stats_cost=.5 WHERE request_id=$1`, f.requestID)
	require.NoError(t, err)
	after, err := f.repo.Budget(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, budget, after, "settled expense must remain immutable")
	ok, err := f.repo.ReserveBudget(ctx, f.runID, f.groupIDs[1], service.IQBudgetDate(), .1, .118)
	require.NoError(t, err)
	require.True(t, ok, "released difference must admit the next request at the exact cap")
}

func TestOpenAIIQBudgetPostgresSettlesFallbackZeroAndOverrun(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		total    string
		override any
		rate     any
		expected float64
	}{
		{name: "legacy_fallback", total: ".05", expected: .05},
		{name: "zero_actual", total: ".9", override: "0", rate: "2", expected: 0},
		{name: "overrun", total: ".9", override: ".12", rate: "1", expected: .12},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			f := newIQSettlementFixture(t)
			ctx := context.Background()
			_, err := integrationDB.ExecContext(ctx, `INSERT INTO usage_logs
				(user_id,api_key_id,account_id,group_id,request_id,model,total_cost,account_stats_cost,account_rate_multiplier,user_agent)
				VALUES ($1,$2,$3,$4,$5,'gpt-6-astra',$6::numeric,$7::numeric,$8::numeric,'sub2api-openai-iq')`, f.userID, f.keys[0].ID, f.accountID, f.groupIDs[0], f.requestID, scenario.total, scenario.override, scenario.rate)
			require.NoError(t, err)
			budget, err := f.repo.Budget(ctx, .1)
			require.NoError(t, err)
			require.InDelta(t, scenario.expected, budget.SpentUSD, 1e-10)
			require.Zero(t, budget.ReservedUSD)
			if scenario.name == "overrun" {
				require.Zero(t, budget.RemainingUSD)
				ok, err := f.repo.ReserveBudget(ctx, f.runID, f.groupIDs[1], service.IQBudgetDate(), .01, .1)
				require.NoError(t, err)
				require.False(t, ok)
			}
		})
	}
}

func TestOpenAIIQBudgetPostgresBindingIsImmutableAndChecksOwnership(t *testing.T) {
	f := newIQSettlementFixture(t)
	ctx := context.Background()
	require.NoError(t, f.repo.BindBudgetRequest(ctx, f.runID, f.groupIDs[0], f.keys[0].ID, f.requestID))
	require.Error(t, f.repo.BindBudgetRequest(ctx, f.runID, f.groupIDs[0], f.keys[0].ID, "client:iq-"+uuid.NewString()))
	require.Error(t, f.repo.BindBudgetRequest(ctx, f.runID, f.groupIDs[0], f.keys[1].ID, f.requestID))
	ok, err := f.repo.ReserveBudget(ctx, f.runID, f.groupIDs[1], service.IQBudgetDate(), .1, 1)
	require.NoError(t, err)
	require.True(t, ok)
	require.Error(t, f.repo.BindBudgetRequest(ctx, f.runID, f.groupIDs[1], f.keys[0].ID, "client:iq-"+uuid.NewString()))
}
