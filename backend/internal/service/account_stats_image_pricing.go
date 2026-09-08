package service

import "strings"

// AccountStatsUsageContext carries immutable request usage details used to
// resolve account-stats upstream cost.
type AccountStatsUsageContext struct {
	Tokens             UsageTokens
	ServiceTier        string
	ImageCount         int
	ImageSize          string
	ImageSizeBreakdown map[string]int
	InboundEndpoint    string
}

func (u AccountStatsUsageContext) RequestCount() int {
	if u.ImageCount > 0 {
		return u.ImageCount
	}
	return 1
}

func deriveAccountStatsImageOperation(imageCount int, inboundEndpoint string) AccountStatsImageOperation {
	if imageCount <= 0 {
		return AccountStatsImageOperationAny
	}
	switch strings.TrimSpace(inboundEndpoint) {
	case "/v1/responses":
		return AccountStatsImageOperationResponses
	case "/v1/images/edits":
		return AccountStatsImageOperationEdit
	default:
		return AccountStatsImageOperationGeneration
	}
}

func findImagePricingForModel(
	pricingList []ChannelModelPricing,
	platform, modelLower string,
	operation AccountStatsImageOperation,
	usage AccountStatsUsageContext,
) *ChannelModelPricing {
	if usage.ImageCount <= 0 {
		return nil
	}
	passes := []struct {
		exactModel bool
		operation  AccountStatsImageOperation
	}{
		{exactModel: true, operation: operation},
		{exactModel: true, operation: AccountStatsImageOperationAny},
		{exactModel: false, operation: operation},
		{exactModel: false, operation: AccountStatsImageOperationAny},
	}
	for _, pass := range passes {
		for i := range pricingList {
			p := &pricingList[i]
			if p.BillingMode != BillingModeImage ||
				p.ImageOperation != pass.operation ||
				!isPlatformMatch(platform, p.Platform) ||
				!modelMatches(p.Models, modelLower, pass.exactModel) {
				continue
			}
			if calculateAccountStatsImageCost(p, usage) != nil {
				return p
			}
		}
	}
	return nil
}

func calculateAccountStatsImageCost(pricing *ChannelModelPricing, usage AccountStatsUsageContext) *float64 {
	if pricing == nil || usage.ImageCount <= 0 {
		return nil
	}
	if len(usage.ImageSizeBreakdown) > 0 {
		return calculateAccountStatsMixedImageCost(pricing, usage)
	}
	size := strings.TrimSpace(usage.ImageSize)
	unit := accountStatsImageUnitPrice(pricing, size)
	if unit == nil {
		return nil
	}
	cost := *unit * float64(usage.ImageCount)
	if cost <= 0 {
		return nil
	}
	return &cost
}

func calculateAccountStatsMixedImageCost(pricing *ChannelModelPricing, usage AccountStatsUsageContext) *float64 {
	var cost float64
	var count int
	for size, bucketCount := range usage.ImageSizeBreakdown {
		if bucketCount <= 0 {
			continue
		}
		count += bucketCount
		unit := accountStatsImageUnitPrice(pricing, strings.TrimSpace(size))
		if unit == nil {
			return nil
		}
		cost += *unit * float64(bucketCount)
	}
	if count != usage.ImageCount || cost <= 0 {
		return nil
	}
	return &cost
}

func accountStatsImageUnitPrice(pricing *ChannelModelPricing, size string) *float64 {
	normalizedSize, sizeOK := normalizeAccountStatsImageTier(size)
	if sizeOK {
		for i := range pricing.Intervals {
			iv := &pricing.Intervals[i]
			normalizedTier, tierOK := normalizeAccountStatsImageTier(iv.TierLabel)
			if tierOK && normalizedTier == normalizedSize && iv.PerRequestPrice != nil && *iv.PerRequestPrice > 0 {
				return iv.PerRequestPrice
			}
		}
	}
	if pricing.PerRequestPrice != nil && *pricing.PerRequestPrice > 0 {
		return pricing.PerRequestPrice
	}
	return nil
}

func normalizeAccountStatsImageTier(label string) (string, bool) {
	switch strings.ToUpper(strings.TrimSpace(label)) {
	case ImageBillingSize1K:
		return ImageBillingSize1K, true
	case ImageBillingSize2K:
		return ImageBillingSize2K, true
	case ImageBillingSize4K:
		return ImageBillingSize4K, true
	default:
		return "", false
	}
}

func modelMatches(models []string, modelLower string, exact bool) bool {
	for _, model := range models {
		candidate := strings.ToLower(model)
		if exact {
			if candidate == modelLower {
				return true
			}
			continue
		}
		if !strings.HasSuffix(candidate, "*") {
			continue
		}
		if strings.HasPrefix(modelLower, strings.TrimSuffix(candidate, "*")) {
			return true
		}
	}
	return false
}
