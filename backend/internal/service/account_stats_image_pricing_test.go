//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeriveAccountStatsImageOperation(t *testing.T) {
	tests := []struct {
		name          string
		imageCount    int
		endpoint      string
		wantOperation AccountStatsImageOperation
	}{
		{
			name:          "non image request uses any",
			imageCount:    0,
			endpoint:      "/v1/images/generations",
			wantOperation: AccountStatsImageOperationAny,
		},
		{
			name:          "responses endpoint",
			imageCount:    1,
			endpoint:      "  /v1/responses  ",
			wantOperation: AccountStatsImageOperationResponses,
		},
		{
			name:          "image edits endpoint",
			imageCount:    1,
			endpoint:      " /v1/images/edits ",
			wantOperation: AccountStatsImageOperationEdit,
		},
		{
			name:          "image generation default",
			imageCount:    1,
			endpoint:      "/v1/images/generations",
			wantOperation: AccountStatsImageOperationGeneration,
		},
		{
			name:          "empty endpoint defaults to generation",
			imageCount:    1,
			endpoint:      "",
			wantOperation: AccountStatsImageOperationGeneration,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.wantOperation, deriveAccountStatsImageOperation(tt.imageCount, tt.endpoint))
		})
	}
}

func TestFindImagePricing_ExactModelAndOperationWins(t *testing.T) {
	pricing := []ChannelModelPricing{
		imagePricing(1, []string{"gpt-image-*"}, AccountStatsImageOperationResponses, 0.03),
		imagePricing(2, []string{"gpt-image-1"}, AccountStatsImageOperationAny, 0.02),
		imagePricing(3, []string{"gpt-image-1"}, AccountStatsImageOperationResponses, 0.01),
	}

	got := findImagePricingForModel(pricing, "", "gpt-image-1", AccountStatsImageOperationResponses, AccountStatsUsageContext{ImageCount: 1})
	require.NotNil(t, got)
	require.Equal(t, int64(3), got.ID)
}

func TestFindImagePricing_ExactModelAnyOperationBeatsWildcardExactOperation(t *testing.T) {
	pricing := []ChannelModelPricing{
		imagePricing(1, []string{"gpt-image-*"}, AccountStatsImageOperationResponses, 0.03),
		imagePricing(2, []string{"gpt-image-1"}, AccountStatsImageOperationAny, 0.02),
	}

	got := findImagePricingForModel(pricing, "", "gpt-image-1", AccountStatsImageOperationResponses, AccountStatsUsageContext{ImageCount: 1})
	require.NotNil(t, got)
	require.Equal(t, int64(2), got.ID)
}

func TestCalculateAccountStatsImageCost_UsesExactTier(t *testing.T) {
	pricing := imagePricing(1, []string{"gpt-image-1"}, AccountStatsImageOperationGeneration, 0.08)
	pricing.Intervals = []PricingInterval{
		{TierLabel: "1K", PerRequestPrice: testPtrFloat64(0.04)},
		{TierLabel: "4K", PerRequestPrice: testPtrFloat64(0.16)},
	}

	got := calculateAccountStatsImageCost(&pricing, AccountStatsUsageContext{ImageCount: 2, ImageSize: "4K"})
	require.NotNil(t, got)
	require.InDelta(t, 0.32, *got, 1e-12)
}

func TestCalculateAccountStatsImageCost_UsesDefaultWhenTierMissing(t *testing.T) {
	pricing := imagePricing(1, []string{"gpt-image-1"}, AccountStatsImageOperationGeneration, 0.08)
	pricing.Intervals = []PricingInterval{
		{TierLabel: "1K", PerRequestPrice: testPtrFloat64(0.04)},
	}

	got := calculateAccountStatsImageCost(&pricing, AccountStatsUsageContext{ImageCount: 2, ImageSize: "4K"})
	require.NotNil(t, got)
	require.InDelta(t, 0.16, *got, 1e-12)
}

func TestCalculateAccountStatsImageCost_SumsMixedSizeBreakdown(t *testing.T) {
	pricing := imagePricing(1, []string{"gpt-image-1"}, AccountStatsImageOperationGeneration, 0)
	pricing.Intervals = []PricingInterval{
		{TierLabel: "1K", PerRequestPrice: testPtrFloat64(0.04)},
		{TierLabel: "4K", PerRequestPrice: testPtrFloat64(0.16)},
	}

	got := calculateAccountStatsImageCost(&pricing, AccountStatsUsageContext{
		ImageCount:         3,
		ImageSizeBreakdown: map[string]int{"1K": 2, "4K": 1},
		InboundEndpoint:    "/v1/images/generations",
	})
	require.NotNil(t, got)
	require.InDelta(t, 0.24, *got, 1e-12)
}

func TestCalculateAccountStatsImageCost_RejectsPartialMixedSizeCost(t *testing.T) {
	pricing := imagePricing(1, []string{"gpt-image-1"}, AccountStatsImageOperationGeneration, 0)
	pricing.Intervals = []PricingInterval{
		{TierLabel: "1K", PerRequestPrice: testPtrFloat64(0.04)},
	}

	got := calculateAccountStatsImageCost(&pricing, AccountStatsUsageContext{
		ImageCount:         3,
		ImageSizeBreakdown: map[string]int{"1K": 2, "4K": 1},
	})
	require.Nil(t, got)
}

func TestCalculateAccountStatsImageCost_RejectsIncompleteBreakdownCount(t *testing.T) {
	pricing := imagePricing(1, []string{"gpt-image-1"}, AccountStatsImageOperationGeneration, 0.08)

	got := calculateAccountStatsImageCost(&pricing, AccountStatsUsageContext{
		ImageCount:         3,
		ImageSizeBreakdown: map[string]int{"1K": 2},
	})
	require.Nil(t, got)
}

func TestCalculateAccountStatsImageCost_LegacyPerRequestFallback(t *testing.T) {
	pricing := ChannelModelPricing{
		BillingMode:     BillingModePerRequest,
		Models:          []string{"gpt-image-1"},
		PerRequestPrice: testPtrFloat64(0.05),
	}

	got := calculateStatsCost(&pricing, AccountStatsUsageContext{ImageCount: 3})
	require.NotNil(t, got)
	require.InDelta(t, 0.15, *got, 1e-12)
}

func imagePricing(id int64, models []string, operation AccountStatsImageOperation, defaultPrice float64) ChannelModelPricing {
	return ChannelModelPricing{
		ID:              id,
		Models:          models,
		BillingMode:     BillingModeImage,
		ImageOperation:  operation,
		PerRequestPrice: testPtrFloat64(defaultPrice),
	}
}
