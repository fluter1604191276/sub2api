//go:build unit

package service

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type channelMonitorBudgetRepoStub struct {
	ChannelMonitorRepository
	monitor       *ChannelMonitor
	reserveCalls  int
	settleCalls   int
	reserveLimit  float64
	reservation   float64
	settledActual float64
	admitted      bool
	reserveErr    error
	unpricedCalls int
	unpricedCount int64
	todayCost     float64
	settleDay     time.Time
	settleErr     error
	settleObserve func(context.Context)
}

func (r *channelMonitorBudgetRepoStub) GetByID(context.Context, int64) (*ChannelMonitor, error) {
	if r.monitor == nil {
		return nil, ErrChannelMonitorNotFound
	}
	clone := *r.monitor
	return &clone, nil
}

func (r *channelMonitorBudgetRepoStub) ReserveDailyBudget(_ context.Context, _ time.Time, amount, limit float64) (bool, error) {
	r.reserveCalls++
	r.reservation = amount
	r.reserveLimit = limit
	return r.admitted, r.reserveErr
}

func (r *channelMonitorBudgetRepoStub) SettleDailyBudget(ctx context.Context, day time.Time, reservation, actual float64) error {
	r.settleCalls++
	r.reservation = reservation
	r.settledActual = actual
	r.settleDay = day
	if r.settleObserve != nil {
		r.settleObserve(ctx)
	}
	return r.settleErr
}

func (r *channelMonitorBudgetRepoStub) TodayEstimatedCost(context.Context, time.Time) (float64, error) {
	return r.todayCost, nil
}

func (r *channelMonitorBudgetRepoStub) RecordUnpricedProbe(context.Context, time.Time) error {
	r.unpricedCalls++
	r.unpricedCount++
	return nil
}

func (r *channelMonitorBudgetRepoStub) UnpricedProbeCount(context.Context, time.Time) (int64, error) {
	return r.unpricedCount, nil
}

func (r *channelMonitorBudgetRepoStub) InsertHistoryBatch(context.Context, []*ChannelMonitorHistoryRow) error {
	return nil
}

func (r *channelMonitorBudgetRepoStub) MarkChecked(context.Context, int64, time.Time) error {
	return nil
}

func TestReserveProbeBudgetUnlimitedStillRecordsPaidProbe(t *testing.T) {
	repo := &channelMonitorBudgetRepoStub{admitted: true}
	svc := NewChannelMonitorService(repo, nil)
	svc.SetBillingService(NewBillingService(&config.Config{}, nil))

	reservation, err := svc.reserveProbeBudget(context.Background(), &ChannelMonitor{
		Provider:     MonitorProviderAnthropic,
		PrimaryModel: "claude-sonnet-4",
	}, 0)

	require.NoError(t, err)
	require.NotNil(t, reservation)
	require.Equal(t, 1, repo.reserveCalls)
	require.Zero(t, repo.reserveLimit)
	require.Positive(t, repo.reservation)
}

func TestReserveProbeBudgetFailsClosedOnLedgerError(t *testing.T) {
	repo := &channelMonitorBudgetRepoStub{admitted: true, reserveErr: errors.New("database unavailable")}
	svc := NewChannelMonitorService(repo, nil)
	svc.SetBillingService(NewBillingService(&config.Config{}, nil))

	reservation, err := svc.reserveProbeBudget(context.Background(), &ChannelMonitor{
		Provider:     MonitorProviderAnthropic,
		PrimaryModel: "claude-sonnet-4",
	}, 1)

	require.ErrorContains(t, err, "database unavailable")
	require.Nil(t, reservation)
}

func TestReserveProbeBudgetUnknownPricingUnlimitedRecordsUnpriced(t *testing.T) {
	repo := &channelMonitorBudgetRepoStub{}
	svc := NewChannelMonitorService(repo, nil)
	svc.SetBillingService(NewBillingService(&config.Config{}, nil))

	reservation, err := svc.reserveProbeBudget(context.Background(), &ChannelMonitor{Provider: MonitorProviderAnthropic, PrimaryModel: "unknown-model"}, 0)
	require.NoError(t, err)
	require.Nil(t, reservation)
	require.Equal(t, 1, repo.unpricedCalls)
}

func TestReserveProbeBudgetUnknownPricingWithCapFailsClosed(t *testing.T) {
	repo := &channelMonitorBudgetRepoStub{}
	svc := NewChannelMonitorService(repo, nil)
	svc.SetBillingService(NewBillingService(&config.Config{}, nil))

	reservation, err := svc.reserveProbeBudget(context.Background(), &ChannelMonitor{Provider: MonitorProviderAnthropic, PrimaryModel: "unknown-model"}, 1)
	require.Error(t, err)
	require.Nil(t, reservation)
	require.Zero(t, repo.unpricedCalls)
}

func TestSettleProbeBudgetRetainsReservationWhenCostUnknown(t *testing.T) {
	repo := &channelMonitorBudgetRepoStub{}
	svc := NewChannelMonitorService(repo, nil)

	svc.settleProbeBudget(context.Background(), &ChannelMonitorBudgetReservation{
		Day: time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC), Reserved: 0.4,
	}, []*CheckResult{{
		Usage: UsageTokens{InputTokens: 10, OutputTokens: 2}, UsageComplete: true, EstimatedCostUSD: 0, EstimatedCostKnown: false,
	}})

	require.Zero(t, repo.settleCalls)
}

func TestSettleProbeBudgetSettlesOnlyKnownCost(t *testing.T) {
	repo := &channelMonitorBudgetRepoStub{}
	svc := NewChannelMonitorService(repo, nil)

	svc.settleProbeBudget(context.Background(), &ChannelMonitorBudgetReservation{
		Day: time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC), Reserved: 0.4,
	}, []*CheckResult{{
		Usage: UsageTokens{InputTokens: 10, OutputTokens: 2}, UsageComplete: true, EstimatedCostUSD: 0.15, EstimatedCostKnown: true,
	}})

	require.Equal(t, 1, repo.settleCalls)
	require.Equal(t, 0.4, repo.reservation)
	require.Equal(t, 0.15, repo.settledActual)
}

func TestSettleProbeBudgetRetainsReservationForNonFiniteCost(t *testing.T) {
	for _, cost := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		repo := &channelMonitorBudgetRepoStub{}
		svc := NewChannelMonitorService(repo, nil)
		svc.settleProbeBudget(context.Background(), &ChannelMonitorBudgetReservation{Day: time.Now(), Reserved: 0.4}, []*CheckResult{{
			Usage: UsageTokens{InputTokens: 10}, UsageComplete: true, EstimatedCostKnown: true, EstimatedCostUSD: cost,
		}})
		require.Zero(t, repo.settleCalls, "cost=%v", cost)
	}
}

func TestBudgetStatusIncludesUnpricedProbesWhenCapEnabled(t *testing.T) {
	repo := &channelMonitorBudgetRepoStub{unpricedCount: 1, todayCost: 0.2}
	svc := NewChannelMonitorService(repo, nil)
	svc.SetRuntimeReader(channelMonitorRuntimeStub{rt: ChannelMonitorRuntime{Enabled: true, Mode: ChannelMonitorModeV1, DailyBudgetUSD: 1}})

	status, err := svc.BudgetStatus(context.Background())
	require.NoError(t, err)
	require.True(t, status.Exhausted)
}

func TestBudgetStatusNoCapStillReportsKnownSpend(t *testing.T) {
	repo := &channelMonitorBudgetRepoStub{todayCost: 0.2}
	svc := NewChannelMonitorService(repo, nil)
	svc.SetRuntimeReader(channelMonitorRuntimeStub{rt: ChannelMonitorRuntime{Enabled: true, Mode: ChannelMonitorModeV1, DailyBudgetUSD: 0}})

	status, err := svc.BudgetStatus(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0.2, status.TodayEstimatedCostUSD)
}

func TestRunCheckQuotaOnlyDoesNotReserveBudget(t *testing.T) {
	repo := &channelMonitorBudgetRepoStub{monitor: &ChannelMonitor{
		ID: 7, Name: "quota", Provider: MonitorProviderKimi, PrimaryModel: MonitorDefaultQuotaModel,
		CheckMode: MonitorCheckModeQuota,
	}}
	svc := NewChannelMonitorService(repo, &duplicateChannelMonitorEncryptor{})
	svc.SetRuntimeReader(channelMonitorRuntimeStub{rt: ChannelMonitorRuntime{
		Enabled: true, Mode: ChannelMonitorModeV1, DailyBudgetUSD: 1,
	}})

	results, err := svc.RunCheck(context.Background(), 7)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Zero(t, repo.reserveCalls)
}

func TestRunCheckUnlimitedWithoutBudgetDependenciesKeepsLegacyCompatibility(t *testing.T) {
	repo := &channelMonitorBudgetRepoStub{monitor: &ChannelMonitor{
		ID: 8, Name: "quota", Provider: MonitorProviderKimi, PrimaryModel: MonitorDefaultQuotaModel,
		CheckMode: MonitorCheckModeQuota,
	}}
	svc := NewChannelMonitorService(repo, &duplicateChannelMonitorEncryptor{})
	svc.SetRuntimeReader(channelMonitorRuntimeStub{rt: ChannelMonitorRuntime{
		Enabled: true, Mode: ChannelMonitorModeV1, DailyBudgetUSD: 0,
	}})

	_, err := svc.RunCheck(context.Background(), 8)
	require.NoError(t, err)
}

func TestSettleProbeBudgetCanceledKnownUsageKeepsBoundedSingleSettlement(t *testing.T) {
	type settlementValueKey struct{}
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), settlementValueKey{}, "synthetic-value"))
	cancel()
	day := time.Date(2026, 9, 9, 0, 0, 0, 0, time.FixedZone("fixture", 8*60*60))
	var observed context.Context
	repo := &channelMonitorBudgetRepoStub{settleObserve: func(settlementCtx context.Context) {
		observed = settlementCtx
		require.NoError(t, settlementCtx.Err(), "task cancellation must not discard already known usage settlement")
		require.Equal(t, "synthetic-value", settlementCtx.Value(settlementValueKey{}))
		deadline, ok := settlementCtx.Deadline()
		require.True(t, ok)
		require.Greater(t, time.Until(deadline), time.Duration(0))
		require.LessOrEqual(t, time.Until(deadline), postUsageBillingTimeout)
	}}
	svc := NewChannelMonitorService(repo, nil)
	svc.settleProbeBudget(ctx, &ChannelMonitorBudgetReservation{Day: day, Reserved: 0.4}, []*CheckResult{{UsageComplete: true, EstimatedCostKnown: true, EstimatedCostUSD: 0.15}})
	require.Equal(t, 1, repo.settleCalls)
	require.Equal(t, day, repo.settleDay, "keep originating reservation day across cancellation/day rollover")
	require.Equal(t, 0.4, repo.reservation)
	require.Equal(t, 0.15, repo.settledActual)
	require.ErrorIs(t, observed.Err(), context.Canceled, "cleanup context must be released after settlement")
}

func TestSettleProbeBudgetCanceledUnknownUsageKeepsReservation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, result := range []*CheckResult{
		{UsageComplete: false, EstimatedCostKnown: true, EstimatedCostUSD: 0.15},
		{UsageComplete: true, EstimatedCostKnown: false, EstimatedCostUSD: 0.15},
		{UsageComplete: true, EstimatedCostKnown: true, EstimatedCostUSD: math.Inf(1)},
	} {
		repo := &channelMonitorBudgetRepoStub{}
		NewChannelMonitorService(repo, nil).settleProbeBudget(ctx, &ChannelMonitorBudgetReservation{Day: time.Now(), Reserved: 0.4}, []*CheckResult{result})
		require.Zero(t, repo.settleCalls)
	}
}

func TestSettleProbeBudgetCanceledRepositoryErrorDoesNotRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	repo := &channelMonitorBudgetRepoStub{settleErr: errors.New("synthetic ledger failure"), settleObserve: func(settlementCtx context.Context) {
		require.NoError(t, settlementCtx.Err())
		deadline, ok := settlementCtx.Deadline()
		require.True(t, ok)
		require.LessOrEqual(t, time.Until(deadline), postUsageBillingTimeout)
	}}
	NewChannelMonitorService(repo, nil).settleProbeBudget(ctx, &ChannelMonitorBudgetReservation{Day: time.Now(), Reserved: 0.4}, []*CheckResult{{UsageComplete: true, EstimatedCostKnown: true, EstimatedCostUSD: 0.15}})
	require.Equal(t, 1, repo.settleCalls, "non-idempotent daily ledger must not be automatically retried")
}
