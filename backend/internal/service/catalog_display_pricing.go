package service

import "math"

// A model newly discovered from account configuration may have no price at
// all. An empty wildcard token card can nevertheless produce a synthesized
// all-zero schedule. Keep that distinct from an explicitly configured free
// card, without changing the billing resolver or any persisted rule.
func plazaScheduleHasPrice(raw *ChannelModelPricing, schedule *ContextPricingSchedule) bool {
	if raw != nil && !pricingNeedsFallback(raw) {
		return true // A configured price pointer, including an intentional zero.
	}
	if schedule == nil {
		return false
	}
	for _, tier := range schedule.Tiers {
		for _, price := range []*float64{tier.Input, tier.Output, tier.CacheWrite, tier.CacheWrite1h, tier.CacheRead} {
			if price != nil && *price > 0 && !math.IsNaN(*price) && !math.IsInf(*price, 0) {
				return true
			}
		}
	}
	return false
}
