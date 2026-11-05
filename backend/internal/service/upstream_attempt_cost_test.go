//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type attemptCostAccountRepo struct {
	AccountRepository
	multiplier float64
}

func (r attemptCostAccountRepo) GetByID(context.Context, int64) (*Account, error) {
	return &Account{ID: 1, RateMultiplier: &r.multiplier}, nil
}

func TestUpstreamAttemptCostUsesAccountRuleAndMultiplier(t *testing.T) {
	channel := &Channel{
		ID:                         1,
		Status:                     StatusActive,
		ApplyPricingToAccountStats: true,
		AccountStatsPricingRules: []AccountStatsPricingRule{{
			GroupIDs: []int64{10},
			Pricing: []ChannelModelPricing{{
				Models:         []string{"mapped-model"},
				InputPrice:     testPtrFloat64(0.05),
				OutputPrice:    testPtrFloat64(0.03),
				CacheReadPrice: testPtrFloat64(0.002),
			}},
		}},
	}
	calculator := NewUpstreamAttemptCostCalculator(attemptCostAccountRepo{multiplier: 0.2},
		newTestChannelServiceForStats(t, channel, 10, PlatformOpenAI),
		newTestBillingServiceWithPrices(map[string]*ModelPricing{}))
	zero, cache, output, groupID := int64(0), int64(1000), int64(2), int64(10)
	a := &UpstreamAttempt{AttemptID: "rule-cost", AccountID: 1, GroupID: &groupID,
		Model: "mapped-model", Protocol: "responses", Source: "request", Outcome: "failed",
		UsagePresent: true, InputTokens: &zero, CacheReadTokens: &cache, OutputTokens: &output,
	}
	a.CreatedAt = time.Now().UTC()
	require.NoError(t, calculator.Snapshot(context.Background(), a))
	require.NoError(t, calculator.Price(context.Background(), a))
	require.Equal(t, "known", a.CostStatus)
	require.InDelta(t, (1000*0.002+2*0.03)*0.2, *a.CostUSD, 1e-10)
}

func TestUpstreamAttemptCostKnownPricingAndUnknownRemainDistinct(t *testing.T) {
	billing := newTestBillingServiceWithPrices(map[string]*ModelPricing{
		"claude-sonnet-4": {InputPricePerToken: 0.001, OutputPricePerToken: 0.002, CacheReadPricePerToken: 0.0001},
	})
	calculator := NewUpstreamAttemptCostCalculator(attemptCostAccountRepo{multiplier: 0.1}, nil, billing)
	zero, cache := int64(0), int64(1000)
	for _, test := range []struct {
		name, model, tier string
		cache             *int64
		want              *float64
	}{
		{name: "cache_only", model: "claude-sonnet-4", cache: &cache, want: testPtrFloat64(0.01)},
		{name: "zero_usage", model: "claude-sonnet-4", want: testPtrFloat64(0)},
		{name: "unknown_model", model: "totally-unknown-upstream-model"},
	} {
		t.Run(test.name, func(t *testing.T) {
			a := &UpstreamAttempt{AttemptID: test.name, AccountID: 1, Model: test.model,
				Protocol: "responses", Source: "request", Outcome: "success", UsagePresent: true,
				InputTokens: &zero, OutputTokens: &zero, CacheReadTokens: test.cache,
				ServiceTier: test.tier}
			a.CreatedAt = time.Now().UTC()
			require.NoError(t, calculator.Snapshot(context.Background(), a))
			require.NotNil(t, a.pricingSnapshot)
			require.NoError(t, calculator.Price(context.Background(), a))
			if test.want == nil {
				require.Nil(t, a.CostUSD)
				require.Equal(t, "unknown_pricing", a.CostStatus)
				return
			}
			require.Equal(t, "known", a.CostStatus)
			require.NotNil(t, a.CostUSD)
			require.InDelta(t, *test.want, *a.CostUSD, 1e-10)
		})
	}
}

func TestUpstreamAttemptSnapshotHonorsApplyPricingToAccountStats(t *testing.T) {
	for _, test := range []struct {
		name         string
		applyPricing bool
		want         float64
	}{
		{name: "disabled_uses_upstream_model_card", applyPricing: false, want: 1},
		{name: "enabled_uses_group_channel_card", applyPricing: true, want: 999},
	} {
		t.Run(test.name, func(t *testing.T) {
			channelPrice := 999.0
			channel := &Channel{
				ID:                         1,
				Status:                     StatusActive,
				ApplyPricingToAccountStats: test.applyPricing,
				ModelPricing:               []ChannelModelPricing{{Models: []string{"claude-sonnet-4"}, InputPrice: &channelPrice}},
			}
			calculator := NewUpstreamAttemptCostCalculator(
				attemptCostAccountRepo{multiplier: 1},
				newTestChannelServiceForStats(t, channel, 10, PlatformOpenAI),
				newTestBillingServiceWithPrices(map[string]*ModelPricing{"claude-sonnet-4": {InputPricePerToken: 1}}),
			)
			input, groupID := int64(1), int64(10)
			attempt := &UpstreamAttempt{AttemptID: test.name, AccountID: 1, GroupID: &groupID,
				Model: "claude-sonnet-4", Protocol: "responses", Source: "request", Outcome: "success",
				UsagePresent: true, InputTokens: &input, CreatedAt: time.Now().UTC()}

			require.NoError(t, calculator.Snapshot(context.Background(), attempt))
			require.True(t, attempt.CostSnapshotAvailable)
			require.NotNil(t, attempt.pricingSnapshot)
			require.NoError(t, calculator.Price(context.Background(), attempt))
			require.NotNil(t, attempt.CostUSD)
			require.InDelta(t, test.want, *attempt.CostUSD, 1e-12)
		})
	}
}

func TestUpstreamAttemptSnapshotFreezesInFlightChannelPrice(t *testing.T) {
	oldPrice, newPrice := 2.0, 5.0
	channel := &Channel{
		ID:                         1,
		Status:                     StatusActive,
		ApplyPricingToAccountStats: true,
		ModelPricing:               []ChannelModelPricing{{Models: []string{"claude-sonnet-4"}, InputPrice: &oldPrice}},
	}
	calculator := NewUpstreamAttemptCostCalculator(
		attemptCostAccountRepo{multiplier: 1},
		newTestChannelServiceForStats(t, channel, 10, PlatformOpenAI),
		newTestBillingServiceWithPrices(map[string]*ModelPricing{"claude-sonnet-4": {InputPricePerToken: 1}}),
	)
	input, groupID := int64(1), int64(10)
	makeAttempt := func(id string) *UpstreamAttempt {
		return &UpstreamAttempt{AttemptID: id, AccountID: 1, GroupID: &groupID, Model: "claude-sonnet-4", Protocol: "responses", Source: "request", Outcome: "success", UsagePresent: true, InputTokens: &input, CreatedAt: time.Now().UTC()}
	}
	oldAttempt := makeAttempt("old-attempt")
	require.NoError(t, calculator.Snapshot(context.Background(), oldAttempt))

	channel.ModelPricing[0].InputPrice = &newPrice
	newAttempt := makeAttempt("new-attempt")
	require.NoError(t, calculator.Snapshot(context.Background(), newAttempt))
	require.NoError(t, calculator.Price(context.Background(), oldAttempt))
	require.NoError(t, calculator.Price(context.Background(), newAttempt))
	require.InDelta(t, oldPrice, *oldAttempt.CostUSD, 1e-12)
	require.InDelta(t, newPrice, *newAttempt.CostUSD, 1e-12)
}
