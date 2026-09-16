package service

import "strings"

// Domestic references are display-only. Do not feed these rates back into
// BillingService: channel prices and account cost cards have separate owners.
func (s *ModelPlazaService) domesticOfficialPricing(model string) (*PlazaOfficialPricing, bool) {
	model = strings.ToLower(strings.TrimSpace(model))
	platform, known := DetectModelPlatform(model)
	if !known || PricingCurrencyForPlatform(platform) != "CNY" {
		return nil, false
	}
	reference := func(input, output, cache float64, source, note string) *PlazaOfficialPricing {
		return &PlazaOfficialPricing{
			Currency: "CNY", PriceBasis: "Official China pricing (CNY)",
			InputPrice: nonZeroPtr(input / 1e6), OutputPrice: nonZeroPtr(output / 1e6),
			CacheReadPrice: nonZeroPtr(cache / 1e6), SourceURL: source,
			VerifiedAt: "2026-09-17", ReferenceNote: note,
		}
	}
	const glmSource = "https://open.bigmodel.cn/pricing"
	const deepseekSource = "https://api-docs.deepseek.com/zh-cn/quick_start/pricing/"
	switch model {
	case "glm-5.2", "glm-5.3":
		return reference(8, 28, 2, glmSource, ""), true
	case "glm-5.3-flash":
		return reference(.8, 2.8, .23, glmSource,
			"常规定价；页面另列截至 09-09 的优惠价，未将该优惠视为当前有效。"), true
	case "glm-5.1":
		low := reference(6, 24, 1.3, glmSource, "按官方输入长度档位列示。")
		high := reference(8, 28, 2, glmSource, "")
		low.DisplayTiers = []PlazaOfficialDisplayTier{
			{Label: "<32K", InputPrice: low.InputPrice, OutputPrice: low.OutputPrice, CacheReadPrice: low.CacheReadPrice},
			{Label: "32K+", InputPrice: high.InputPrice, OutputPrice: high.OutputPrice, CacheReadPrice: high.CacheReadPrice},
		}
		return low, true
	case "deepseek-flash", "deepseek-v4.1-flash", "deepseek-v4-flash", "deepseek-v4-flash-vision-exp":
		return reference(1, 4, .02, deepseekSource,
			"V4.1 Flash 低谷价；北京时间工作日 09:00-12:00、14:00-18:00 为两倍高峰价，其余时段及周末为低谷价。"), true
	case "deepseek-v4-pro", "deepseek-v4-pro-0813":
		return reference(4.5, 13.5, .15, deepseekSource,
			"V4 Pro 0813 低谷价；北京时间工作日 09:00-12:00、14:00-18:00 为两倍高峰价，其余时段及周末为低谷价。"), true
	}
	if platform == PlatformDeepseek {
		// A reseller's date suffix is not evidence of an official model alias.
		return nil, true
	}
	// Only exact, explicitly sourced China-region fallback entries qualify.
	// Dynamic USD prices and broad family fallbacks must not become CNY references.
	pricing := s.billingService.fallbackPrices[model]
	if pricing == nil || pricing.Currency != "CNY" || !strings.Contains(strings.ToLower(pricing.PriceBasis), "official china") {
		return nil, true
	}
	return &PlazaOfficialPricing{
		Currency: pricing.Currency, PriceBasis: pricing.PriceBasis,
		InputPrice: nonZeroPtr(pricing.InputPricePerToken), OutputPrice: nonZeroPtr(pricing.OutputPricePerToken),
		CacheReadPrice: nonZeroPtr(pricing.CacheReadPricePerToken),
	}, true
}
