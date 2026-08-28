package service

import "encoding/json"

// ApplyGroupPoolErrorPolicy returns a request-local account copy with explicit
// group overrides applied. Nil group fields inherit the account value; a
// configured group field is authoritative, including false and empty arrays.
// The copy is deliberate: one account may be used by multiple groups with
// different retry/error policies, and shared scheduler snapshots must not be mutated.
func (a *Account) ApplyGroupPoolErrorPolicy(group *Group) *Account {
	if a == nil || group == nil {
		return a
	}
	if !group.HasPoolErrorPolicyOverride() {
		return a
	}

	copyAccount := *a
	copyAccount.Credentials = cloneCredentials(a.Credentials)

	if group.PoolModeEnabled != nil {
		copyAccount.Credentials["pool_mode"] = *group.PoolModeEnabled
	}
	if group.PoolModeRetryCount != nil {
		copyAccount.Credentials["pool_mode_retry_count"] = *group.PoolModeRetryCount
	}
	if group.PoolModeRetryStatusCodes != nil {
		copyAccount.Credentials["pool_mode_retry_status_codes"] = cloneIntSliceAsJSONValues(*group.PoolModeRetryStatusCodes)
	}
	if group.CustomErrorCodesEnabled != nil {
		copyAccount.Credentials["custom_error_codes_enabled"] = *group.CustomErrorCodesEnabled
	}
	if group.CustomErrorCodes != nil {
		copyAccount.Credentials["custom_error_codes"] = cloneIntSliceAsJSONValues(*group.CustomErrorCodes)
	}
	return &copyAccount
}

func cloneCredentials(credentials map[string]any) map[string]any {
	if credentials == nil {
		return make(map[string]any)
	}
	cloned := make(map[string]any, len(credentials))
	for key, value := range credentials {
		cloned[key] = value
	}
	return cloned
}

func cloneIntSliceAsJSONValues(values []int) []any {
	cloned := make([]any, len(values))
	for i, value := range values {
		// Credentials are JSON-backed, so numeric values are decoded as float64.
		// Keep request-local group overrides identical to persisted account values.
		cloned[i] = float64(value)
	}
	return cloned
}

// HasPoolErrorPolicyOverride reports whether at least one group policy field is
// explicitly configured. It is kept separate so the hot path can avoid copying
// credentials for the common inherited/default case.
func (g *Group) HasPoolErrorPolicyOverride() bool {
	return g != nil && (g.PoolModeEnabled != nil ||
		g.PoolModeRetryCount != nil ||
		g.PoolModeRetryStatusCodes != nil ||
		g.CustomErrorCodesEnabled != nil ||
		g.CustomErrorCodes != nil)
}

// normalizePolicyJSON is used by tests and future API adapters that receive
// JSON-number arrays instead of native integer slices.
func normalizePolicyJSON(value any) ([]int, bool) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	var result []int
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, false
	}
	return result, true
}
