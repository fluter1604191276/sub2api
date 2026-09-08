package service

import "testing"

func TestSmartStickyPolicyPresets(t *testing.T) {
	recommended, err := RecommendedSmartStickyPolicy().Normalize()
	if err != nil || recommended.TargetScore != 70 || recommended.MaxEscapes != 3 {
		t.Fatalf("unexpected recommended policy: %+v (%v)", recommended, err)
	}
	stable, err := StableSmartStickyPolicy().Normalize()
	if err != nil || stable.TargetScore != 80 || stable.ReviewIntervalSeconds != 15 {
		t.Fatalf("unexpected stability policy: %+v (%v)", stable, err)
	}
}

func TestDecideOpenAISmartStickyReviewTargetEscape(t *testing.T) {
	ordering := &SmartSchedulerOrdering{
		Active:            true,
		OrderedAccountIDs: []int64{2, 1},
		RankByAccountID:   map[int64]int{2: 1, 1: 2},
		ItemByAccountID: map[int64]SmartSchedulerPreviewItem{
			1: {AccountID: 1, Pool: "primary", Score: previewFloat64Ptr(78)},
			2: {AccountID: 2, Pool: "primary", Score: previewFloat64Ptr(82)},
		},
	}
	policy := StableSmartStickyPolicy()
	decision := decideOpenAISmartStickyReviewWithPolicy(ordering, 1, policy)
	if !decision.Switch || decision.QualityLead != 4 {
		t.Fatalf("expected stable target escape, got %+v", decision)
	}
}

func TestSmartStickyPolicyRejectsUnsafeValues(t *testing.T) {
	_, err := (SmartStickyPolicy{TargetScore: 101}).Normalize()
	if err == nil {
		t.Fatal("expected target score validation error")
	}
}
