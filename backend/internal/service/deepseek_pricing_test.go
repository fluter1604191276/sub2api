//go:build unit

package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// DeepSeek 统一采用官方峰价作为站内基础价，不按请求时刻切换峰谷。
// ---------------------------------------------------------------------------

func TestDeepseekFixedPeakPrices(t *testing.T) {
	require.Equal(t, 2e-6, deepseekFlashPeakInputPrice)
	require.Equal(t, 8e-6, deepseekFlashPeakOutputPrice)
	require.Equal(t, 0.04e-6, deepseekFlashPeakCacheRead)
	require.Equal(t, 9e-6, deepseekProPeakInputPrice)
	require.Equal(t, 27e-6, deepseekProPeakOutputPrice)
	require.Equal(t, 0.30e-6, deepseekProPeakCacheRead)
}

func TestIsDeepSeekModel(t *testing.T) {
	deepseek := []string{
		"deepseek-v4-flash", "deepseek-v4-pro", "deepseek-v4-flash-vision-exp",
		"deepseek-chat", "deepseek-reasoner", "deepseek-v3-2-251201",
		"deepseek-coder", "deepseek-foo", "deepseek-v4-pro-0813",
		"DEEPSEEK-V4-PRO", " deepseek-v4-flash ",
	}
	for _, m := range deepseek {
		require.True(t, isDeepSeekModel(m), "model %q should be deepseek", m)
	}

	nonDeepseek := []string{
		"gpt-5.4", "claude-sonnet-4", "deepseekcoder", // 无连字符不算 deepseek- 前缀
		"", " deepseek", // 无连字符后缀
	}
	for _, m := range nonDeepseek {
		require.False(t, isDeepSeekModel(m), "model %q should not be deepseek", m)
	}
}

// ---------------------------------------------------------------------------
// 默认价卡（Source=LiteLLM）固定按官方峰价计费；分组/渠道自定义定价不叠加
// ---------------------------------------------------------------------------

func TestCalculateCostUnified_DeepseekDefaultCardUsesFixedPeakBase(t *testing.T) {
	bs := newTestBillingService()
	resolver := NewModelPricingResolver(nil, bs)

	tokens := UsageTokens{InputTokens: 1000, OutputTokens: 500, CacheReadTokens: 1000}
	want := 1000*2e-6 + 500*8e-6 + 1000*0.04e-6
	for _, pricingAt := range []time.Time{
		time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC), // 原低谷
		time.Date(2026, 8, 24, 2, 0, 0, 0, time.UTC),  // 原高峰
		time.Date(2026, 8, 22, 2, 0, 0, 0, time.UTC),  // 周末
	} {
		cost, err := bs.CalculateCostUnified(CostInput{
			Ctx: context.Background(), Model: "deepseek-v4-flash", Tokens: tokens,
			RateMultiplier: 1.0, Resolver: resolver, PricingAt: pricingAt,
		})
		require.NoError(t, err)
		require.InDelta(t, want, cost.TotalCost, 1e-10, "pricingAt=%v", pricingAt)
	}
}

func TestCalculateCostUnified_DeepseekProDefaultCardUsesFixedPeakBase(t *testing.T) {
	bs := newTestBillingService()
	resolver := NewModelPricingResolver(nil, bs)

	tokens := UsageTokens{InputTokens: 1000, OutputTokens: 500, CacheReadTokens: 1000}
	want := 1000*9e-6 + 500*27e-6 + 1000*0.30e-6
	for _, pricingAt := range []time.Time{
		time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 24, 6, 30, 0, 0, time.UTC),
		time.Date(2026, 8, 23, 6, 30, 0, 0, time.UTC),
	} {
		cost, err := bs.CalculateCostUnified(CostInput{
			Ctx: context.Background(), Model: "deepseek-v4-pro", Tokens: tokens,
			RateMultiplier: 1.0, Resolver: resolver, PricingAt: pricingAt,
		})
		require.NoError(t, err)
		require.InDelta(t, want, cost.TotalCost, 1e-10, "pricingAt=%v", pricingAt)
	}
}

func TestCalculateCostUnified_DeepseekVersionedNameUsesFixedPeakBase(t *testing.T) {
	bs := newTestBillingService()
	resolver := NewModelPricingResolver(nil, bs)

	tokens := UsageTokens{InputTokens: 1000, OutputTokens: 500, CacheReadTokens: 1000}
	want := 1000*2e-6 + 500*8e-6 + 1000*0.04e-6
	for _, pricingAt := range []time.Time{
		time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 24, 2, 0, 0, 0, time.UTC),
	} {
		cost, err := bs.CalculateCostUnified(CostInput{
			Ctx: context.Background(), Model: "deepseek-v4-flash-0731", Tokens: tokens,
			RateMultiplier: 1.0, Resolver: resolver, PricingAt: pricingAt,
		})
		require.NoError(t, err)
		require.InDelta(t, want, cost.TotalCost, 1e-10, "pricingAt=%v", pricingAt)
	}
}

func TestCalculateCostUnified_DeepseekGroupPricingNotScaledByPeak(t *testing.T) {
	bs := newTestBillingService()
	resolver := NewModelPricingResolver(nil, bs)

	inputPrice := 1e-6
	outputPrice := 2e-6
	group := &Group{
		ID: 1, Name: "ds-group", Platform: PlatformDeepseek, Status: StatusActive,
		ModelPricing: []ChannelModelPricing{{
			Models: []string{"deepseek-v4-flash"}, BillingMode: BillingModeToken,
			InputPrice: &inputPrice, OutputPrice: &outputPrice,
		}},
	}
	resolved := resolver.Resolve(context.Background(), PricingInput{Model: "deepseek-v4-flash", Group: group})
	require.Equal(t, PricingSourceGroup, resolved.Source)

	tokens := UsageTokens{InputTokens: 1000, OutputTokens: 500, CacheReadTokens: 1000}
	// 分组输入/输出沿用显式价卡，缓存读缺省回落到固定峰价。
	groupTotal := 1000*1e-6 + 500*2e-6 + 1000*0.04e-6

	for _, pricingAt := range []time.Time{
		time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC), // 低谷
		time.Date(2026, 8, 24, 2, 0, 0, 0, time.UTC),  // 高峰
	} {
		cost, err := bs.CalculateCostUnified(CostInput{
			Ctx: context.Background(), Model: "deepseek-v4-flash", Group: group,
			Tokens: tokens, RateMultiplier: 1.0, Resolver: resolver, PricingAt: pricingAt,
		})
		require.NoError(t, err)
		require.InDelta(t, groupTotal, cost.TotalCost, 1e-10,
			"分组自定义定价不应叠加官方峰谷倍率（pricingAt=%v）", pricingAt)
	}
}

func TestCalculateCostUnified_NonDeepseekDefaultCardNotScaledByPeak(t *testing.T) {
	bs := newTestBillingService()
	resolver := NewModelPricingResolver(nil, bs)

	tokens := UsageTokens{InputTokens: 1000, OutputTokens: 500}
	total := 1000*3e-6 + 500*15e-6 // claude-sonnet-4 fallback

	for _, pricingAt := range []time.Time{
		time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 24, 2, 0, 0, 0, time.UTC),
	} {
		cost, err := bs.CalculateCostUnified(CostInput{
			Ctx: context.Background(), Model: "claude-sonnet-4", Tokens: tokens,
			RateMultiplier: 1.0, Resolver: resolver, PricingAt: pricingAt,
		})
		require.NoError(t, err)
		require.InDelta(t, total, cost.TotalCost, 1e-10,
			"非 DeepSeek 模型不应受官方峰谷倍率影响（pricingAt=%v）", pricingAt)
	}
}

// ---------------------------------------------------------------------------
// 官方峰价强制覆盖（远端旧价兜底）与未知 deepseek-* flash 兜底
// ---------------------------------------------------------------------------

func TestGetModelPricing_DeepseekForcesOfficialRatesOverJSON(t *testing.T) {
	// JSON 给任意价（模拟远端旧价/占位价），deepseek-* 必须被强制覆盖为官方峰价。
	pricingSvc := &PricingService{pricingData: map[string]*LiteLLMModelPricing{
		"deepseek-v4-flash":            {InputCostPerToken: 1e-6, OutputCostPerToken: 2e-6, CacheReadInputTokenCost: 1e-8},
		"deepseek-v4-pro":              {InputCostPerToken: 1e-6, OutputCostPerToken: 2e-6, CacheReadInputTokenCost: 1e-8},
		"deepseek-v4-flash-vision-exp": {InputCostPerToken: 1e-6, OutputCostPerToken: 2e-6, CacheReadInputTokenCost: 1e-8},
		"deepseek-chat":                {InputCostPerToken: 1e-6, OutputCostPerToken: 2e-6, CacheReadInputTokenCost: 1e-8},
		"deepseek-reasoner":            {InputCostPerToken: 1e-6, OutputCostPerToken: 2e-6, CacheReadInputTokenCost: 1e-8},
	}}
	bs := NewBillingService(&config.Config{}, pricingSvc)

	tests := []struct {
		model                    string
		input, output, cacheRead float64
	}{
		{"deepseek-v4-flash", 2e-6, 8e-6, 0.04e-6},
		{"deepseek-v4-flash-vision-exp", 2e-6, 8e-6, 0.04e-6},
		{"deepseek-v4-pro", 9e-6, 27e-6, 0.30e-6},
		// 已停服的 chat/reasoner：即使 JSON 有旧条目也按 flash 价兜底。
		{"deepseek-chat", 2e-6, 8e-6, 0.04e-6},
		{"deepseek-reasoner", 2e-6, 8e-6, 0.04e-6},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			pricing, err := bs.GetModelPricing(tt.model)
			require.NoError(t, err)
			require.InDelta(t, tt.input, pricing.InputPricePerToken, 1e-15)
			require.InDelta(t, tt.output, pricing.OutputPricePerToken, 1e-15)
			require.InDelta(t, tt.cacheRead, pricing.CacheReadPricePerToken, 1e-15)
			require.True(t, bs.HasIdentifiedTokenPricing(tt.model))
		})
	}

	// 版本化名称（不在 JSON / fallbackPrices 精确表中）：按子串归档计价。
	versioned := []struct {
		model                    string
		input, output, cacheRead float64
	}{
		{"deepseek-v4-pro-0813", 9e-6, 27e-6, 0.30e-6},
		{"deepseek-v4-flash-0731", 2e-6, 8e-6, 0.04e-6},
	}
	for _, tt := range versioned {
		t.Run(tt.model, func(t *testing.T) {
			pricing, err := bs.GetModelPricing(tt.model)
			require.NoError(t, err)
			require.InDelta(t, tt.input, pricing.InputPricePerToken, 1e-15)
			require.InDelta(t, tt.output, pricing.OutputPricePerToken, 1e-15)
			require.InDelta(t, tt.cacheRead, pricing.CacheReadPricePerToken, 1e-15)
		})
	}
}

func TestGetModelPricing_UnknownDeepseekMapsToFlash(t *testing.T) {
	// JSON 含 $0 占位条目（如旧 deepseek-v3-2-251201）：未知 deepseek-* 不再
	// fail-closed，统一按 flash 峰价兜底（2e-6/8e-6/0.04e-6），不得按 $0 计费。
	pricingSvc := &PricingService{pricingData: map[string]*LiteLLMModelPricing{
		"deepseek-v3-2-251201": {InputCostPerToken: 0, OutputCostPerToken: 0},
	}}
	bs := NewBillingService(&config.Config{}, pricingSvc)

	for _, m := range []string{"deepseek-v3-2-251201", "deepseek-chat", "deepseek-reasoner", "deepseek-foo"} {
		t.Run(m, func(t *testing.T) {
			pricing, err := bs.GetModelPricing(m)
			require.NoError(t, err)
			require.InDelta(t, 2e-6, pricing.InputPricePerToken, 1e-15)
			require.InDelta(t, 8e-6, pricing.OutputPricePerToken, 1e-15)
			require.InDelta(t, 0.04e-6, pricing.CacheReadPricePerToken, 1e-15)
		})
	}
}

// ---------------------------------------------------------------------------
// 本地兜底 JSON：无 $0 占位条目，官方模型价格为官方峰价
// ---------------------------------------------------------------------------

func TestDeepseekPricingFileMatchesOfficialRates(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "resources", "model-pricing", "model_prices_and_context_window.json"))
	require.NoError(t, err)

	pricingSvc := &PricingService{}
	pricingData, err := pricingSvc.parsePricingData(data)
	require.NoError(t, err)

	_, ok := pricingData["deepseek-v3-2-251201"]
	require.False(t, ok, "deepseek-v3-2-251201（$0 占位条目）必须从价格表中移除")
	for _, discontinued := range []string{"deepseek-chat", "deepseek-reasoner"} {
		_, ok := pricingData[discontinued]
		require.False(t, ok, "%s 已停止服务，必须从价格表中移除", discontinued)
	}

	tests := []struct {
		model                    string
		input, output, cacheRead float64
	}{
		{"deepseek-v4-flash", 2e-6, 8e-6, 0.04e-6},
		{"deepseek-v4-flash-vision-exp", 2e-6, 8e-6, 0.04e-6},
		{"deepseek-v4-pro", 9e-6, 27e-6, 0.30e-6},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			entry, ok := pricingData[tt.model]
			require.True(t, ok, "model %s must exist in pricing file", tt.model)
			require.InDelta(t, tt.input, entry.InputCostPerToken, 1e-15)
			require.InDelta(t, tt.output, entry.OutputCostPerToken, 1e-15)
			require.InDelta(t, tt.cacheRead, entry.CacheReadInputTokenCost, 1e-15)
		})
	}
}
