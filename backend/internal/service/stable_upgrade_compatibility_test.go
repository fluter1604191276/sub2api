package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestStableUpgradeReasoningPreservesProductionDefaults(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values map[string]float64
		want   float64
	}{
		{"legacy absent", nil, 3}, {"migrated empty", map[string]float64{}, 3},
		{"unrelated explicit level", map[string]float64{"high": 1.5}, 3},
		{"explicit max disables legacy default", map[string]float64{"max": 1}, 1},
		{"explicit max replaces rather than multiplies legacy", map[string]float64{"max": 2}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, compatReasoningEffortMultiplier("claude-fable-5-1", "max", nil, tc.values))
		})
	}
	legacy := 1.25
	require.Equal(t, 1.25, compatReasoningEffortMultiplier("claude-fable-5-1", "max", &legacy, nil))
	require.Equal(t, 1.5, compatReasoningEffortMultiplier("claude-fable-5-1", "high", nil, map[string]float64{"high": 1.5}))
	require.Equal(t, 1.0, compatReasoningEffortMultiplier("claude-fable-5-1", "high", nil, nil))
}

func TestStableUpgradeDeepSeekKeepsFixedProductionCardAcrossTimes(t *testing.T) {
	svc := newStableUpgradeBillingService()
	for _, at := range []time.Time{time.Date(2026, 9, 10, 2, 0, 0, 0, time.UTC), time.Date(2026, 10, 3, 8, 0, 0, 0, time.UTC)} {
		for _, tc := range []struct {
			model                string
			input, output, cache float64
		}{
			{"deepseek-flash", 2e-6, 8e-6, .04e-6}, {"deepseek-v4-pro", 9e-6, 27e-6, .30e-6},
		} {
			p, err := svc.getModelPricingAt(tc.model, at)
			require.NoError(t, err)
			require.Equal(t, "CNY", p.Currency)
			require.InDelta(t, tc.input, p.InputPricePerToken, 1e-15)
			require.InDelta(t, tc.output, p.OutputPricePerToken, 1e-15)
			require.InDelta(t, tc.cache, p.CacheReadPricePerToken, 1e-15)
		}
	}
}

func TestStableUpgradeAccountStatsArgumentBridgePreservesMetadata(t *testing.T) {
	at := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	original := AccountStatsUsageContext{Tokens: UsageTokens{InputTokens: 10, CacheCreation5mTokens: 20, CacheCreation1hTokens: 30}, ImageCount: 2, ImageSize: "2K", ServiceTier: "priority", InboundEndpoint: "/v1/images/edits"}
	usage, count, total, tier, gotAt, effort := parseAccountStatsUsageArgs(original, []any{7.5, at, false, "max"})
	require.Equal(t, original, usage)
	require.Equal(t, 2, count)
	require.Equal(t, 7.5, total)
	require.Equal(t, "priority", tier)
	require.Equal(t, at, gotAt)
	require.Equal(t, "max", effort)
	_, count, total, tier, gotAt, effort = parseAccountStatsUsageArgs(original.Tokens, []any{2, 7.5, "priority", at, false, "max"})
	require.Equal(t, 2, count)
	require.Equal(t, 7.5, total)
	require.Equal(t, "priority", tier)
	require.Equal(t, at, gotAt)
	require.Equal(t, "max", effort)
}

// Assert monetary outcomes, independently of the compatibility helper.
func TestStableUpgradePreservesProductionBillingOutcomes(t *testing.T) {
	svc := newStableUpgradeBillingService()
	for _, count := range []int{271999, 272000, 272001} {
		cost, err := svc.CalculateCostWithServiceTier("gpt-6-astra", UsageTokens{InputTokens: count, OutputTokens: 10}, 1, "ultrafast")
		require.NoError(t, err)
		require.InDelta(t, float64(count)*20e-6, cost.InputCost, 1e-10)
		require.InDelta(t, 10*100e-6, cost.OutputCost, 1e-10)
	}
	standard, err := svc.CalculateTokenCostForRequest(TokenCostRequest{Model: "claude-fable-5-1", Tokens: UsageTokens{InputTokens: 1000, OutputTokens: 10}, RateMultiplier: 1, ReasoningEffort: "xhigh"})
	require.NoError(t, err)
	max, err := svc.CalculateTokenCostForRequest(TokenCostRequest{Model: "claude-fable-5-1", Tokens: UsageTokens{InputTokens: 1000, OutputTokens: 10}, RateMultiplier: 1, ReasoningEffort: "max"})
	require.NoError(t, err)
	require.InDelta(t, standard.InputCost*3, max.InputCost, 1e-12)
	require.InDelta(t, standard.OutputCost*3, max.OutputCost, 1e-12)
	require.InDelta(t, standard.TotalCost*3, max.TotalCost, 1e-12)
}

func TestStableUpgradeCostRulesPreserveMetadataAndExplicitLevels(t *testing.T) {
	price := 0.1
	channel := &Channel{AccountStatsPricingRules: []AccountStatsPricingRule{{AccountIDs: []int64{1}, Pricing: []ChannelModelPricing{{Models: []string{"claude-fable-5-1"}, BillingMode: BillingModeImage, PerRequestPrice: &price, ReasoningEffortMultipliers: map[string]float64{"high": 1.5, "max": 1}}}}}}
	for _, effort := range []string{"high", "max"} {
		want := 0.2
		if effort == "high" {
			want = 0.3
		}
		direct := tryCustomRules(channel, 1, 10, PlatformAnthropic, "claude-fable-5-1", UsageTokens{}, 2, effort)
		require.NotNil(t, direct)
		require.InDelta(t, want, *direct, 1e-12)
		contextual := tryCustomRules(channel, 1, 10, PlatformAnthropic, "claude-fable-5-1", AccountStatsUsageContext{ImageCount: 2, InboundEndpoint: "/v1/images/edits"}, time.Time{}, effort)
		require.NotNil(t, contextual)
		require.InDelta(t, want, *contextual, 1e-12)
	}
	// Token-only production requests must not match image-cost rules accidentally.
	require.Nil(t, tryCustomRules(channel, 1, 10, PlatformAnthropic, "claude-fable-5-1", AccountStatsUsageContext{}, time.Time{}, "high"))
}

func TestStableUpgradeReasoningJSONToBillingAndDisplay(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       float64
	}{
		{"omitted", `{}`, 3}, {"null", `{"reasoning_effort_multipliers":null}`, 3},
		{"cleared", `{"reasoning_effort_multipliers":{}}`, 3},
		{"legacy explicit", `{"max_reasoning_effort_multiplier":1.25}`, 1.25},
		{"explicit one", `{"reasoning_effort_multipliers":{"max":1}}`, 1},
		{"high only", `{"reasoning_effort_multipliers":{"high":1.5}}`, 3},
		{"null replaces stale scalar", `{"max_reasoning_effort_multiplier":2,"reasoning_effort_multipliers":null}`, 3},
		{"map replaces stale scalar", `{"max_reasoning_effort_multiplier":2,"reasoning_effort_multipliers":{}}`, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var p ChannelModelPricing
			require.NoError(t, json.Unmarshal([]byte(tc.body), &p))
			// Same canonical representation used by repositories; SQL serializes nil as {}.
			canonical := EffectiveReasoningEffortMultipliers(&p)
			if canonical == nil {
				canonical = map[string]float64{}
			}
			stored, err := json.Marshal(canonical)
			require.NoError(t, err)
			var loaded map[string]float64
			require.NoError(t, json.Unmarshal(stored, &loaded))
			price := &ChannelModelPricing{ReasoningEffortMultipliers: loaded}
			require.Equal(t, tc.want, compatReasoningEffortMultiplier("claude-fable-5-1", "max", p.MaxReasoningEffortMultiplier, canonical))
			display := withDisplayReasoningEffortMultipliers(price, "claude-fable-5-1")
			require.Equal(t, tc.want, display.ReasoningEffortMultipliers["max"])
			require.NotNil(t, display.MaxReasoningEffortMultiplier)
			require.Equal(t, tc.want, *display.MaxReasoningEffortMultiplier)
			require.Equal(t, 1.0, compatReasoningEffortMultiplier("ordinary-model", "max", nil, map[string]float64{}))
			require.Equal(t, loaded, price.ReasoningEffortMultipliers, "display must not persist defaults")
		})
	}
}

func TestStableUpgradeExplicitPricingInputsRemainAvailable(t *testing.T) {
	svc := newStableUpgradeBillingService()
	// New multiplier support is testable with an explicit pricing input; the Astra
	// production default remains 2x and is covered independently above.
	pricing := &ModelPricing{InputPricePerToken: 10e-6, OutputPricePerToken: 50e-6, UltrafastMultiplier: 6}
	cost := svc.computeTokenBreakdown(pricing, UsageTokens{InputTokens: 1000, OutputTokens: 10}, 1, "ultrafast", false)
	require.InDelta(t, 0.06, cost.InputCost, 1e-12)
	require.InDelta(t, 0.003, cost.OutputCost, 1e-12)
	// Explicit image prices still use those prices, rather than the nil-price policy.
	channel := &ChannelModelPricing{ImageInputPrice: stableUpgradeFloat64Ptr(8e-6), ImageOutputPrice: stableUpgradeFloat64Ptr(30e-6)}
	p, err := svc.GetModelPricingWithChannel("claude-sonnet-4", channel)
	require.NoError(t, err)
	imageCost := svc.computeTokenBreakdown(p, UsageTokens{InputTokens: 1000, ImageInputTokens: 600, OutputTokens: 500, ImageOutputTokens: 500}, 1, "", false)
	require.InDelta(t, 600*8e-6, imageCost.ImageInputCost, 1e-12)
	require.InDelta(t, 500*30e-6, imageCost.ImageOutputCost, 1e-12)
}

// These helpers are available in both default and unit-tag release suites.
func newStableUpgradeBillingService() *BillingService {
	return NewBillingService(&config.Config{}, nil)
}

func stableUpgradeFloat64Ptr(value float64) *float64 { return &value }
