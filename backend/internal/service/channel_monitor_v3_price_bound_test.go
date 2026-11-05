//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const channelMonitorV3BoundGroupID = int64(901)

func newChannelMonitorV3BoundAttempt(t *testing.T, calculator *UpstreamAttemptCostCalculator, model string) *UpstreamAttempt {
	t.Helper()
	attempt := &UpstreamAttempt{
		AttemptID:    "channel-monitor-v3-bound",
		AccountID:    1,
		GroupID:      int64PtrForChannelMonitorV3(channelMonitorV3BoundGroupID),
		Model:        model,
		Protocol:     "responses",
		Source:       "channel_monitor_v3",
		Outcome:      "unknown",
		CreatedAt:    time.Now().UTC(),
		UsagePresent: true,
	}
	require.NoError(t, calculator.Snapshot(context.Background(), attempt))
	require.True(t, attempt.CostSnapshotAvailable)
	require.NotNil(t, attempt.pricingSnapshot)
	return attempt
}

func TestChannelMonitorV3HTTPUpperBoundUsesOneHourCacheWritePrice(t *testing.T) {
	billing := newTestBillingServiceWithPrices(map[string]*ModelPricing{
		"claude-sonnet-4": {
			InputPricePerToken:         1,
			OutputPricePerToken:        2,
			CacheCreation5mPrice:       3,
			CacheCreation1hPrice:       7,
			CacheReadPricePerToken:     4,
			SupportsCacheBreakdown:     true,
			CacheCreationPricePerToken: 3,
		},
	})
	calculator := NewUpstreamAttemptCostCalculator(attemptCostAccountRepo{multiplier: 1}, nil, billing)
	attempt := newChannelMonitorV3BoundAttempt(t, calculator, "claude-sonnet-4")
	control := cloneChannelMonitorV3BoundAttempt(attempt)
	output := int64(2)
	control.OutputTokens = &output
	control.UsagePresent = true
	require.NoError(t, calculator.Price(context.Background(), &control))
	require.NotNil(t, control.CostUSD)

	upper, err := channelMonitorV3HTTPUpperBound(context.Background(), calculator, attempt, 10, 2)

	require.NoError(t, err)
	// input(10) + output(2*2) + cache-write-1h(10*7) + cache-read(10*4)
	require.InDelta(t, 124, upper, 1e-10)
}

func TestChannelMonitorV3HTTPUpperBoundIncludesCacheReadContextTier(t *testing.T) {
	low, high := 1.0, 10.0
	limit := 20
	channel := &Channel{ID: 1, Status: StatusActive, ApplyPricingToAccountStats: true, ModelPricing: []ChannelModelPricing{{
		Models: []string{"claude-sonnet-4"}, InputPrice: &low, OutputPrice: &low, CacheWritePrice: &low, CacheReadPrice: &low,
		Intervals: []PricingInterval{
			{MinTokens: 0, MaxTokens: &limit, InputPrice: &low, OutputPrice: &low, CacheWritePrice: &low, CacheReadPrice: &low},
			{MinTokens: limit, InputPrice: &high, OutputPrice: &high, CacheWritePrice: &high, CacheReadPrice: &high},
		},
	}}}
	calculator := NewUpstreamAttemptCostCalculator(
		attemptCostAccountRepo{multiplier: 1},
		newTestChannelServiceForStats(t, channel, channelMonitorV3BoundGroupID, PlatformOpenAI),
		newTestBillingServiceWithPrices(map[string]*ModelPricing{"claude-sonnet-4": {InputPricePerToken: low}}),
	)
	attempt := newChannelMonitorV3BoundAttempt(t, calculator, "claude-sonnet-4")

	upper, err := channelMonitorV3HTTPUpperBound(context.Background(), calculator, attempt, 10, 2)

	require.NoError(t, err)
	require.GreaterOrEqual(t, upper, 320.0, "cache read must reach the higher context tier")
}

func TestChannelMonitorV3HTTPUpperBoundCoversNonMonotonicCustomRuleTier(t *testing.T) {
	expensive, cheap := 100.0, 1.0
	firstMax, secondMax := 10, 30
	channel := &Channel{ID: 1, Status: StatusActive, AccountStatsPricingRules: []AccountStatsPricingRule{{
		AccountIDs: []int64{1},
		Pricing: []ChannelModelPricing{{
			Models: []string{"claude-sonnet-4"}, InputPrice: &cheap, OutputPrice: &cheap, CacheWritePrice: &cheap, CacheReadPrice: &cheap,
			Intervals: []PricingInterval{
				{MinTokens: 0, MaxTokens: &firstMax, InputPrice: &expensive, OutputPrice: &expensive, CacheWritePrice: &expensive, CacheReadPrice: &expensive},
				{MinTokens: firstMax, MaxTokens: &secondMax, InputPrice: &cheap, OutputPrice: &cheap, CacheWritePrice: &cheap, CacheReadPrice: &cheap},
			},
		}},
	}}}
	calculator := NewUpstreamAttemptCostCalculator(
		attemptCostAccountRepo{multiplier: 1},
		newTestChannelServiceForStats(t, channel, channelMonitorV3BoundGroupID, PlatformOpenAI),
		newTestBillingServiceWithPrices(map[string]*ModelPricing{"claude-sonnet-4": {InputPricePerToken: cheap}}),
	)
	attempt := newChannelMonitorV3BoundAttempt(t, calculator, "claude-sonnet-4")

	upper, err := channelMonitorV3HTTPUpperBound(context.Background(), calculator, attempt, 10, 2)

	require.NoError(t, err)
	require.GreaterOrEqual(t, upper, 1000.0, "the first high-price custom interval is reachable even though max tokens enter a cheaper interval")
}

func TestChannelMonitorV3HTTPUpperBoundUsesHighestConfiguredReasoningEffort(t *testing.T) {
	billing := newTestBillingServiceWithPrices(map[string]*ModelPricing{
		"claude-sonnet-4": {InputPricePerToken: 1, OutputPricePerToken: 1, ReasoningEffortMultipliers: map[string]float64{"xhigh": 2, "max": 5}},
	})
	calculator := NewUpstreamAttemptCostCalculator(attemptCostAccountRepo{multiplier: 1}, nil, billing)
	attempt := newChannelMonitorV3BoundAttempt(t, calculator, "claude-sonnet-4")
	attempt.ReasoningEffort = "xhigh"

	upper, err := channelMonitorV3HTTPUpperBound(context.Background(), calculator, attempt, 10, 2)

	require.NoError(t, err)
	require.InDelta(t, 60, upper, 1e-10)
}

func TestChannelMonitorV3HTTPUpperBoundDoesNotCarryReservationTimeDiscount(t *testing.T) {
	unitPrice := 1.0
	schedule := &ChannelTimePricing{Timezone: "UTC", Periods: []ChannelTimePricingPeriod{
		{StartTime: "00:00", EndTime: "12:00", Multiplier: .5},
		{StartTime: "12:00", EndTime: "23:59", Multiplier: 2},
	}}
	channel := &Channel{ID: 1, Status: StatusActive, ApplyPricingToAccountStats: true, ModelPricing: []ChannelModelPricing{{
		Models: []string{"claude-sonnet-4"}, InputPrice: &unitPrice, OutputPrice: &unitPrice,
		CacheWritePrice: &unitPrice, CacheReadPrice: &unitPrice, TimePricing: schedule,
	}}}
	calculator := NewUpstreamAttemptCostCalculator(attemptCostAccountRepo{multiplier: 1},
		newTestChannelServiceForStats(t, channel, channelMonitorV3BoundGroupID, PlatformOpenAI),
		newTestBillingServiceWithPrices(map[string]*ModelPricing{"claude-sonnet-4": {InputPricePerToken: 1}}))
	attempt := newChannelMonitorV3BoundAttempt(t, calculator, "claude-sonnet-4")
	attempt.CreatedAt = time.Date(2026, 11, 5, 6, 0, 0, 0, time.UTC)
	upper, err := channelMonitorV3HTTPUpperBound(context.Background(), calculator, attempt, 10, 2)
	require.NoError(t, err)
	require.InDelta(t, 64, upper, 1e-10)
	require.NotNil(t, attempt.pricingSnapshot.channelPricing.TimePricing, "the original frozen snapshot must remain unchanged")
}

func TestChannelMonitorV3HTTPUpperBoundCoversNonMonotonicRequestTiers(t *testing.T) {
	expensive, cheap, limit := 100.0, 1.0, 10
	channel := &Channel{ID: 1, Status: StatusActive, ApplyPricingToAccountStats: true, ModelPricing: []ChannelModelPricing{{
		Models: []string{"claude-sonnet-4"}, BillingMode: BillingModePerRequest, PerRequestPrice: &cheap,
		Intervals: []PricingInterval{{MinTokens: 0, MaxTokens: &limit, PerRequestPrice: &expensive}, {MinTokens: limit, PerRequestPrice: &cheap}},
	}}}
	calculator := NewUpstreamAttemptCostCalculator(attemptCostAccountRepo{multiplier: 1},
		newTestChannelServiceForStats(t, channel, channelMonitorV3BoundGroupID, PlatformOpenAI),
		newTestBillingServiceWithPrices(map[string]*ModelPricing{"claude-sonnet-4": {InputPricePerToken: 1}}))
	attempt := newChannelMonitorV3BoundAttempt(t, calculator, "claude-sonnet-4")
	require.Empty(t, attempt.pricingSnapshot.Intervals)
	require.Len(t, attempt.pricingSnapshot.RequestTiers, 2)
	upper, err := channelMonitorV3HTTPUpperBound(context.Background(), calculator, attempt, 10, 2)
	require.NoError(t, err)
	require.InDelta(t, 100, upper, 1e-10)
}
