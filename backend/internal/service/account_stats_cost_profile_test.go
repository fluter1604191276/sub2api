//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAccountCostProfileReconcilesFlashBillWithoutRepricingBarePro(t *testing.T) {
	channel := &Channel{ID: 1, Status: StatusActive, AccountStatsPricingRules: []AccountStatsPricingRule{{
		AccountIDs: []int64{1}, Pricing: []ChannelModelPricing{{
			Platform: PlatformDeepseek, Models: []string{"deepseek-v4.1-flash", "deepseek-v4.1-flash-0910"},
			InputPrice: testPtrFloat64(2e-6), OutputPrice: testPtrFloat64(8e-6), CacheReadPrice: testPtrFloat64(0.04e-6),
		}},
	}}}
	cs := newTestChannelServiceForStats(t, channel, 10, PlatformDeepseek)
	bs := newTestBillingService()
	peak := time.Date(2026, 9, 14, 6, 13, 22, 0, time.UTC)
	flash := resolveAccountStatsCost(context.Background(), cs, bs, 1, 10, "deepseek-v4.1-flash",
		UsageTokens{InputTokens: 777136, OutputTokens: 22753, CacheReadTokens: 2379008}, 1, 1.879341328, "", peak)
	require.NotNil(t, flash)
	require.InDelta(t, 0.274718448, *flash*0.15, 1e-12)
	// The undated upstream Pro bill used the default peak card, not the dated
	// model's 9/27 card. An exact Flash profile must not change that path.
	proTokens := UsageTokens{InputTokens: 1786, OutputTokens: 1333, CacheReadTokens: 45056}
	pro := resolveAccountStatsCost(context.Background(), cs, bs, 1, 10, "deepseek-v4-pro", proTokens, 1, 0.002099948, "", peak)
	require.NotNil(t, pro)
	require.InDelta(t, 0.009618664, *pro, 1e-12)
	require.InDelta(t, 0.0014427996, *pro*0.15, 1e-12)
	require.InDelta(t, 0.3086730324, *flash*0.15+0.0339545844, 1e-12)
}

func TestAccountCostProfileTimePricingUsesRequestTimeAndAccountScope(t *testing.T) {
	for _, weekdaysOnly := range []bool{false, true} {
		pricing := ChannelModelPricing{
			Platform: PlatformDeepseek, Models: []string{"deepseek-v4-pro"},
			InputPrice: testPtrFloat64(4.5e-6), OutputPrice: testPtrFloat64(13.5e-6), CacheReadPrice: testPtrFloat64(0.15e-6),
			TimePricing: &ChannelTimePricing{Timezone: "Asia/Shanghai", WeekdaysOnly: weekdaysOnly, Periods: []ChannelTimePricingPeriod{
				{StartTime: "09:00", EndTime: "12:00", Multiplier: 2},
				{StartTime: "14:00", EndTime: "18:00", Multiplier: 2},
			}},
		}
		channel := &Channel{ID: 1, Status: StatusActive, ApplyPricingToAccountStats: true, AccountStatsPricingRules: []AccountStatsPricingRule{{AccountIDs: []int64{1}, Pricing: []ChannelModelPricing{pricing}}}}
		cs := newTestChannelServiceForStats(t, channel, 10, PlatformDeepseek)
		tokens := UsageTokens{InputTokens: 1000000, OutputTokens: 100000, CacheReadTokens: 500000}
		for _, slot := range []struct {
			at         string
			multiplier float64
		}{
			{"2026-09-14T08:59:59+08:00", 1}, {"2026-09-14T09:00:00+08:00", 2},
			{"2026-09-14T12:00:00+08:00", 1}, {"2026-09-14T14:00:00+08:00", 2},
			{"2026-09-14T18:00:00+08:00", 1}, {"2026-09-13T10:00:00+08:00", 2},
		} {
			at, err := time.Parse(time.RFC3339, slot.at)
			require.NoError(t, err)
			want := slot.multiplier
			if weekdaysOnly && at.Weekday() == time.Sunday {
				want = 1
			}
			cost := resolveAccountStatsCost(context.Background(), cs, nil, 1, 10, "deepseek-v4-pro", tokens, 1, 99.0, "", at)
			require.NotNil(t, cost)
			require.InDelta(t, (4.5+1.35+0.075)*want, *cost, 1e-12, slot.at)
			other := resolveAccountStatsCost(context.Background(), cs, nil, 2, 10, "deepseek-v4-pro", tokens, 1, 99.0, "", at)
			require.Equal(t, 99.0, *other)
		}
	}
}

func TestApplyAccountStatsCostPreservesCacheWriteBreakdown(t *testing.T) {
	channel := &Channel{ID: 1, Status: StatusActive, AccountStatsPricingRules: []AccountStatsPricingRule{{AccountIDs: []int64{1}, Pricing: []ChannelModelPricing{{
		Platform: PlatformAnthropic, Models: []string{"claude-sonnet-4-6"},
		CacheWritePrice: testPtrFloat64(3.75e-6), CacheWrite1hPrice: testPtrFloat64(6e-6),
	}}}}}
	cs := newTestChannelServiceForStats(t, channel, 10, PlatformAnthropic)
	log := &UsageLog{TotalCost: 1, ActualCost: 0.6, RateMultiplier: 0.6, CacheCreationTokens: 3000, CacheCreation5mTokens: 1000, CacheCreation1hTokens: 2000}
	applyAccountStatsCost(context.Background(), log, cs, nil, 1, 10, "claude-sonnet-4-6", "claude-sonnet-4-6",
		UsageTokens{CacheCreationTokens: 3000}, 1.0, time.Now())
	require.NotNil(t, log.AccountStatsCost)
	require.InDelta(t, 1000*3.75e-6+2000*6e-6, *log.AccountStatsCost, 1e-12)
	require.Equal(t, 1.0, log.TotalCost)
	require.Equal(t, 0.6, log.ActualCost)
	require.Equal(t, 0.6, log.RateMultiplier)
}

func TestAccountCostProfileProbeUsesStartTimeAndSingleAccountMultiplier(t *testing.T) {
	for _, peak := range []bool{false, true} {
		svc, atomicRepo := newProbeBillingSettlementTestService(t, 0.0008*0.3)
		audit := probeBillingSettlementAudit(901)
		audit.StartedAt = time.Date(2026, 9, 14, 0, 59, 59, 0, time.UTC)
		wantMultiplier := 1.0
		if peak {
			audit.StartedAt = time.Date(2026, 9, 14, 3, 59, 59, 0, time.UTC)
			wantMultiplier = 2
		}
		audit.FinishedAt = audit.StartedAt.Add(2 * time.Second)
		audit.UsageTokens = UsageTokens{InputTokens: 100, OutputTokens: 10, CacheCreationTokens: 30, CacheCreation5mTokens: 10, CacheCreation1hTokens: 20}
		channel := &Channel{ID: 1, Status: StatusActive, AccountStatsPricingRules: []AccountStatsPricingRule{{
			AccountIDs: []int64{audit.AccountID}, Pricing: []ChannelModelPricing{{
				Platform: PlatformOpenAI, Models: []string{audit.Model},
				InputPrice: testPtrFloat64(2e-6), OutputPrice: testPtrFloat64(8e-6),
				CacheWritePrice: testPtrFloat64(3.75e-6), CacheWrite1hPrice: testPtrFloat64(6e-6),
				TimePricing: &ChannelTimePricing{Timezone: "Asia/Shanghai", Periods: []ChannelTimePricingPeriod{{StartTime: "09:00", EndTime: "12:00", Multiplier: 2}}},
			}},
		}}}
		svc.channelSvc = newTestChannelServiceForStats(t, channel, audit.GroupID, PlatformOpenAI)
		reservation := &GroupRecoveryProbeBillingReservation{Settings: GroupRecoveryProbeBillingSettings{
			Enabled: true, OwnerUserID: 7, APIKeyID: 11, DailyBudgetUSD: 1, PerAttemptLimitUSD: 1,
		}}
		require.NoError(t, svc.Settle(context.Background(), audit, reservation))
		require.Len(t, atomicRepo.commands, 1)
		command := atomicRepo.commands[0]
		base := (100*2e-6 + 10*8e-6 + 10*3.75e-6 + 20*6e-6) * wantMultiplier
		require.InDelta(t, base, *command.UsageLog.AccountStatsCost, 1e-12)
		require.Equal(t, QuantizeUsageBillingAmount(base*0.3), command.SettledCostUSD)
		require.Equal(t, 10, command.UsageLog.CacheCreation5mTokens)
		require.Equal(t, 20, command.UsageLog.CacheCreation1hTokens)
	}
}

func TestAccountStatsArgumentParsingKeepsEmptyTierSeparateFromEffort(t *testing.T) {
	at := time.Date(2026, 9, 14, 6, 0, 0, 0, time.UTC)
	tokens := UsageTokens{InputTokens: 100}
	for _, tier := range []string{"", "default", "priority"} {
		t.Run("tier="+tier, func(t *testing.T) {
			for _, legacyContext := range []bool{false, true} {
				var input any = tokens
				args := []any{1, 0.2, tier, at, "max"}
				if legacyContext {
					input = AccountStatsUsageContext{Tokens: tokens, ServiceTier: tier}
					args = []any{0.2, at, "max"}
				}
				usage, count, total, gotTier, gotAt, effort := parseAccountStatsUsageArgs(input, args)
				require.Equal(t, tokens, usage.Tokens)
				require.Equal(t, 1, count)
				require.Equal(t, 0.2, total)
				require.Equal(t, tier, gotTier)
				require.Equal(t, at, gotAt)
				require.Equal(t, "max", effort)
			}
		})
	}
}

func TestResolveAccountStatsCostEmptyTierPreservesMaxEffortAcrossProfiles(t *testing.T) {
	for _, custom := range []bool{false, true} {
		channel := &Channel{ID: 1, Status: StatusActive}
		if custom {
			channel.AccountStatsPricingRules = []AccountStatsPricingRule{{AccountIDs: []int64{1}, Pricing: []ChannelModelPricing{{
				Platform: PlatformAnthropic, Models: []string{"claude-fable-5-1"}, InputPrice: testPtrFloat64(0.001),
			}}}}
		}
		cs := newTestChannelServiceForStats(t, channel, 10, PlatformAnthropic)
		bs := newTestBillingServiceWithPrices(map[string]*ModelPricing{
			"claude-fable-5-1": {InputPricePerToken: 0.001},
		})
		for _, legacyContext := range []bool{false, true} {
			var input any = UsageTokens{InputTokens: 100}
			args := []any{1, 0.1, "", time.Time{}, "max"}
			if legacyContext {
				input = AccountStatsUsageContext{Tokens: UsageTokens{InputTokens: 100}}
				args = []any{0.1, time.Time{}, "max"}
			}
			cost := resolveAccountStatsCost(context.Background(), cs, bs, 1, 10, "claude-fable-5-1", input, args...)
			require.NotNil(t, cost)
			require.InDelta(t, 0.3, *cost, 1e-12, "custom=%v legacy=%v", custom, legacyContext)
		}
	}
}
