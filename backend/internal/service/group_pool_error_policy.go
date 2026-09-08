package service

import (
	"fmt"
	"sort"
)

const (
	groupPoolModeRetryCountMin = 0
	groupPoolModeRetryCountMax = 10
)

// normalizeGroupPolicyCodes validates and canonicalizes an explicitly supplied
// HTTP status-code list. A non-nil empty list remains empty so callers can
// intentionally clear an inherited account policy.
func normalizeGroupPolicyCodes(codes *[]int) (*[]int, error) {
	if codes == nil {
		return nil, nil
	}
	seen := make(map[int]struct{}, len(*codes))
	result := make([]int, 0, len(*codes))
	for _, code := range *codes {
		if code < 100 || code > 599 {
			return nil, fmt.Errorf("status code %d must be between 100 and 599", code)
		}
		if _, exists := seen[code]; exists {
			continue
		}
		seen[code] = struct{}{}
		result = append(result, code)
	}
	sort.Ints(result)
	return &result, nil
}

func normalizeGroupPoolModeRetryCount(count *int) (*int, error) {
	if count == nil {
		return nil, nil
	}
	if *count < groupPoolModeRetryCountMin || *count > groupPoolModeRetryCountMax {
		return nil, fmt.Errorf("pool_mode_retry_count must be between %d and %d", groupPoolModeRetryCountMin, groupPoolModeRetryCountMax)
	}
	value := *count
	return &value, nil
}

// NormalizeGroupPoolErrorPolicy validates the nullable group-level overrides.
// nil means inherit; an empty list means explicitly clear the inherited list.
func NormalizeGroupPoolErrorPolicy(poolModeRetryCount *int, poolModeRetryStatusCodes, customErrorCodes *[]int) (*int, *[]int, *[]int, error) {
	retryCount, err := normalizeGroupPoolModeRetryCount(poolModeRetryCount)
	if err != nil {
		return nil, nil, nil, err
	}
	retryCodes, err := normalizeGroupPolicyCodes(poolModeRetryStatusCodes)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("pool_mode_retry_status_codes: %w", err)
	}
	customCodes, err := normalizeGroupPolicyCodes(customErrorCodes)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("custom_error_codes: %w", err)
	}
	return retryCount, retryCodes, customCodes, nil
}
