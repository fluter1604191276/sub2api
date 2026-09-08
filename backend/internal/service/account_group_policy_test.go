//go:build unit

package service

import "testing"

func TestApplyGroupPoolErrorPolicyPrecedence(t *testing.T) {
	groupEnabled := false
	groupRetryCount := 7
	groupCodes := []int{529, 503}
	groupCustomEnabled := true
	groupCustomCodes := []int{529}

	account := &Account{
		Type:     AccountTypeAPIKey,
		Platform: PlatformOpenAI,
		Credentials: map[string]any{
			"pool_mode":                    true,
			"pool_mode_retry_status_codes": []any{},
		},
	}
	group := &Group{
		PoolModeEnabled:          &groupEnabled,
		PoolModeRetryCount:       &groupRetryCount,
		PoolModeRetryStatusCodes: &groupCodes,
		CustomErrorCodesEnabled:  &groupCustomEnabled,
		CustomErrorCodes:         &groupCustomCodes,
	}

	resolved := account.ApplyGroupPoolErrorPolicy(group)
	if resolved == account {
		t.Fatal("expected request-local copy")
	}
	if resolved.IsPoolMode() != groupEnabled {
		t.Fatal("configured group pool_mode must override account value")
	}
	if got := resolved.GetPoolModeRetryCount(); got != groupRetryCount {
		t.Fatalf("group retry count not applied: got %d", got)
	}
	if got := resolved.GetPoolModeRetryStatusCodes(); len(got) != 2 || got[0] != 503 || got[1] != 529 {
		t.Fatalf("group retry status codes must override account list, got %#v", got)
	}
	if !resolved.IsCustomErrorCodesEnabled() {
		t.Fatal("group custom error switch should apply when account key is missing")
	}
	if got := resolved.GetCustomErrorCodes(); len(got) != 1 || got[0] != 529 {
		t.Fatalf("group custom error codes not applied: %#v", got)
	}
	if _, ok := account.Credentials["custom_error_codes"]; ok {
		t.Fatal("original account credentials were mutated")
	}
	if !account.IsPoolMode() {
		t.Fatal("original account credentials were mutated")
	}
}

func TestApplyGroupPoolErrorPolicyAllowsExplicitEmptyGroupLists(t *testing.T) {
	empty := []int{}
	group := &Group{PoolModeRetryStatusCodes: &empty, CustomErrorCodes: &empty}
	account := &Account{Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Credentials: map[string]any{}}
	resolved := account.ApplyGroupPoolErrorPolicy(group)
	if got := resolved.GetPoolModeRetryStatusCodes(); got == nil || len(got) != 0 {
		t.Fatalf("expected explicit empty retry list, got %#v", got)
	}
	if got := resolved.GetCustomErrorCodes(); got == nil || len(got) != 0 {
		t.Fatalf("expected explicit empty custom list, got %#v", got)
	}
}
