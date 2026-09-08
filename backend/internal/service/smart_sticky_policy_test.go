package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

type stickyPolicyRepoStub struct{ values map[string]string }

func (r *stickyPolicyRepoStub) Get(context.Context, string) (*Setting, error) {
	return nil, ErrSettingNotFound
}
func (r *stickyPolicyRepoStub) GetValue(_ context.Context, key string) (string, error) {
	v, ok := r.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return v, nil
}
func (r *stickyPolicyRepoStub) Set(_ context.Context, key, value string) error {
	if r.values == nil {
		r.values = map[string]string{}
	}
	r.values[key] = value
	return nil
}
func (r *stickyPolicyRepoStub) GetMultiple(context.Context, []string) (map[string]string, error) {
	return nil, nil
}
func (r *stickyPolicyRepoStub) SetMultiple(context.Context, map[string]string) error { return nil }
func (r *stickyPolicyRepoStub) GetAll(context.Context) (map[string]string, error) {
	return r.values, nil
}
func (r *stickyPolicyRepoStub) Delete(context.Context, string) error { return nil }

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

func TestSmartStickyPolicyCustomRecommendedLabelDoesNotHideEdits(t *testing.T) {
	p := RecommendedSmartStickyPolicy()
	p.Preset = "recommended"
	p.SwitchCooldownSeconds = 0
	p.QualityLead = 0
	p.configured = true
	normalized, err := p.Normalize()
	if err != nil || normalized.SwitchCooldownSeconds != 0 || normalized.QualityLead != 0 || normalized.usesLegacyDynamics() {
		t.Fatalf("edited recommended policy was not treated as custom: %+v (%v)", normalized, err)
	}
}

func TestSmartStickyPolicyPersistenceRoundTrip(t *testing.T) {
	repo := &stickyPolicyRepoStub{values: map[string]string{}}
	settings := &SettingService{settingRepo: repo}
	svc := &adminServiceImpl{settingService: settings}
	want := StableSmartStickyPolicy()
	want.SwitchCooldownSeconds = 0
	want.QualityLead = 0
	want.Preset = "custom"
	got, err := svc.UpdateSmartStickyPolicy(context.Background(), 9, want)
	if err != nil || got.SwitchCooldownSeconds != 0 || got.QualityLead != 0 {
		t.Fatalf("save failed: %+v (%v)", got, err)
	}
	var raw SmartStickyPolicy
	if err := json.Unmarshal([]byte(repo.values[smartStickyPolicyKey(9)]), &raw); err != nil {
		t.Fatal(err)
	}
	loaded, err := svc.GetSmartStickyPolicy(context.Background(), 9)
	if err != nil || loaded.SwitchCooldownSeconds != 0 || loaded.QualityLead != 0 || loaded.Preset != "custom" {
		t.Fatalf("round trip lost custom zero values: %+v (%v)", loaded, err)
	}
}

func TestSmartStickyPolicyEnforcesEscapeBudgetAndCooldown(t *testing.T) {
	policy := StableSmartStickyPolicy()
	policy.MaxEscapes = 1
	policy.EscapeWindowSeconds = 60
	policy.SwitchCooldownSeconds = 30
	svc := &OpenAIGatewayService{}
	now := time.Now()
	svc.markSmartStickySwitchAppliedWithPolicy("budget", 2, now, policy)
	decision := openAISmartStickyReviewDecision{Switch: true, Reason: "better_quality", ChallengerID: 3, CurrentScore: previewFloat64Ptr(78), ChallengerScore: previewFloat64Ptr(82), QualityLead: 4}
	blocked := svc.applySmartStickyReviewStateWithPolicy("budget", now.Add(time.Second), decision, policy)
	if blocked.Switch || blocked.Reason != "escape_budget_exhausted" {
		t.Fatalf("expected escape budget block, got %+v", blocked)
	}

	policy.MaxEscapes = 3
	cooldown := svc.applySmartStickyReviewStateWithPolicy("cooldown", now, decision, policy)
	if !cooldown.Switch {
		t.Fatalf("initial switch should be allowed: %+v", cooldown)
	}
	svc.markSmartStickySwitchAppliedWithPolicy("cooldown", 3, now, policy)
	blocked = svc.applySmartStickyReviewStateWithPolicy("cooldown", now.Add(time.Second), decision, policy)
	if blocked.Switch || blocked.Reason != "switch_cooldown" {
		t.Fatalf("expected cooldown block, got %+v", blocked)
	}
}

func TestSmartStickyPolicyUsesCustomEliteConfirmations(t *testing.T) {
	policy := StableSmartStickyPolicy()
	policy.EliteConfirmations = 3
	decision := openAISmartStickyReviewDecision{Switch: true, RequiresConfirmation: true, Reason: "better_quality", ChallengerID: 2}
	svc := &OpenAIGatewayService{}
	for i := 1; i <= 2; i++ {
		got := svc.applySmartStickyReviewStateWithPolicy("confirm", time.Now().Add(time.Duration(i)*time.Minute), decision, policy)
		if got.Switch || !got.ConfirmationPending {
			t.Fatalf("confirmation %d should remain pending: %+v", i, got)
		}
	}
	got := svc.applySmartStickyReviewStateWithPolicy("confirm", time.Now().Add(3*time.Minute), decision, policy)
	if !got.Switch || got.ConfirmationPending {
		t.Fatalf("third confirmation should switch: %+v", got)
	}
}
