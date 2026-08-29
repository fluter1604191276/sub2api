package usagestats

// AccountStats 账号使用统计
//
// cost: 账号口径费用（使用 total_cost * account_rate_multiplier）
// standard_cost: 标准费用（使用 total_cost，不含倍率）
// user_cost: 用户/API Key 口径费用（使用 actual_cost，受分组倍率影响）
type AccountStats struct {
	Requests     int64   `json:"requests"`
	Tokens       int64   `json:"tokens"`
	Cost         float64 `json:"cost"`
	StandardCost float64 `json:"standard_cost"`
	UserCost     float64 `json:"user_cost"`
}

// CacheHitStats is the rolling cache usage summary for one account.
// InputTokens is the uncached input token count stored in usage_logs.
type CacheHitStats struct {
	Requests            int64    `json:"requests"`
	InputTokens         int64    `json:"input_tokens"`
	CacheCreationTokens int64    `json:"cache_creation_tokens"`
	CacheReadTokens     int64    `json:"cache_read_tokens"`
	CacheHitRate        *float64 `json:"cache_hit_rate"`
}

// CalculateCacheHitRate returns a token-weighted cache hit percentage.
// Cache creation is counted as a miss, while output tokens are excluded.
func CalculateCacheHitRate(inputTokens, cacheCreationTokens, cacheReadTokens int64) (float64, bool) {
	if inputTokens < 0 {
		inputTokens = 0
	}
	if cacheCreationTokens < 0 {
		cacheCreationTokens = 0
	}
	if cacheReadTokens < 0 {
		cacheReadTokens = 0
	}

	cacheableTokens := inputTokens + cacheCreationTokens + cacheReadTokens
	if cacheableTokens == 0 {
		return 0, false
	}
	return float64(cacheReadTokens) / float64(cacheableTokens) * 100, true
}
