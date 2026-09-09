//go:build integration

package repository

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func channelMonitorBudgetFixture(t *testing.T, dayOfMonth int) (*channelMonitorRepository, time.Time) {
	t.Helper()
	day := time.Date(2099, 9, dayOfMonth, 0, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(),
			"DELETE FROM channel_monitor_daily_budget_ledger WHERE budget_date = $1::date", day.Format("2006-01-02"))
		require.NoError(t, err)
	})
	return &channelMonitorRepository{db: integrationDB}, day
}

func TestChannelMonitorBudgetPostgresFiniteCapAndSettlement(t *testing.T) {
	repo, day := channelMonitorBudgetFixture(t, 9)
	ctx := context.Background()
	admitted, err := repo.ReserveDailyBudget(ctx, day, 1.25, 1)
	require.NoError(t, err)
	require.False(t, admitted, "an oversized first reservation must not create spend")
	cost, err := repo.TodayEstimatedCost(ctx, day)
	require.NoError(t, err)
	require.Zero(t, cost)

	for i := 0; i < 2; i++ {
		admitted, err = repo.ReserveDailyBudget(ctx, day, 0.5, 1)
		require.NoError(t, err)
		require.True(t, admitted, "both INSERT and ON CONFLICT must admit up to the cap")
	}
	admitted, err = repo.ReserveDailyBudget(ctx, day, 0.125, 1)
	require.NoError(t, err)
	require.False(t, admitted)

	require.NoError(t, repo.SettleDailyBudget(ctx, day, 0.5, 0.125))
	cost, err = repo.TodayEstimatedCost(ctx, day)
	require.NoError(t, err)
	require.Equal(t, 0.625, cost)
	admitted, err = repo.ReserveDailyBudget(ctx, day, 0.375, 1)
	require.NoError(t, err)
	require.True(t, admitted, "known lower cost releases only the unused reservation")
	cost, err = repo.TodayEstimatedCost(ctx, day)
	require.NoError(t, err)
	require.Equal(t, 1.0, cost)

	previousDayCost, err := repo.TodayEstimatedCost(ctx, day.AddDate(0, 0, -1))
	require.NoError(t, err)
	require.Zero(t, previousDayCost, "use the application's calendar day, not its UTC date")
}

func TestChannelMonitorBudgetPostgresUnlimitedAndUnpriced(t *testing.T) {
	repo, day := channelMonitorBudgetFixture(t, 10)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		admitted, err := repo.ReserveDailyBudget(ctx, day, 0.25, 0)
		require.NoError(t, err)
		require.True(t, admitted)
	}
	require.NoError(t, repo.RecordUnpricedProbe(ctx, day))
	admitted, err := repo.ReserveDailyBudget(ctx, day, 0.125, 1)
	require.NoError(t, err)
	require.False(t, admitted, "enabling a cap must fail closed when today's costs are unknown")
	admitted, err = repo.ReserveDailyBudget(ctx, day, 0.125, 0)
	require.NoError(t, err)
	require.True(t, admitted, "unlimited mode still records priced spend")
	require.NoError(t, repo.SettleDailyBudget(ctx, day, 0.125, 0))
	cost, err := repo.TodayEstimatedCost(ctx, day)
	require.NoError(t, err)
	require.Equal(t, 0.5, cost)
	count, err := repo.UnpricedProbeCount(ctx, day)
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
}

func TestChannelMonitorBudgetPostgresUnpricedFirstAndMissingSettlement(t *testing.T) {
	repo, day := channelMonitorBudgetFixture(t, 11)
	ctx := context.Background()
	require.ErrorContains(t, repo.SettleDailyBudget(ctx, day, 0.125, 0), "ledger row missing")
	require.NoError(t, repo.RecordUnpricedProbe(ctx, day))
	admitted, err := repo.ReserveDailyBudget(ctx, day, 0.125, 1)
	require.NoError(t, err)
	require.False(t, admitted)
}

func TestChannelMonitorBudgetPostgresConcurrentReservationsRespectCap(t *testing.T) {
	repo, day := channelMonitorBudgetFixture(t, 12)
	ctx := context.Background()
	var admittedCount atomic.Int64
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	start := make(chan struct{})
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			admitted, err := repo.ReserveDailyBudget(ctx, day, 0.125, 1)
			if err != nil {
				errs <- err
			} else if admitted {
				admittedCount.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.EqualValues(t, 8, admittedCount.Load())
	cost, err := repo.TodayEstimatedCost(ctx, day)
	require.NoError(t, err)
	require.Equal(t, 1.0, cost)
}
