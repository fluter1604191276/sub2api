package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

const smartStickyPolicyKeyPrefix = "smart_scheduler_sticky_policy:"

// SmartStickyPolicy controls per-group sticky-session quality escape behavior.
// Zero values are filled from RecommendedSmartStickyPolicy.
type SmartStickyPolicy struct {
	Preset                string  `json:"preset"`
	TargetScore           float64 `json:"target_score"`
	ReviewIntervalSeconds int     `json:"review_interval_seconds"`
	SwitchCooldownSeconds int     `json:"switch_cooldown_seconds"`
	QualityLead           float64 `json:"quality_lead"`
	MaxEscapes            int     `json:"max_escapes"`
	EscapeWindowSeconds   int     `json:"escape_window_seconds"`
	EliteConfirmations    int     `json:"elite_confirmations"`
	configured            bool
}

func (p SmartStickyPolicy) usesLegacyDynamics() bool {
	if !p.configured {
		return true
	}
	recommended := RecommendedSmartStickyPolicy()
	return p.TargetScore == recommended.TargetScore &&
		p.ReviewIntervalSeconds == recommended.ReviewIntervalSeconds &&
		p.SwitchCooldownSeconds == recommended.SwitchCooldownSeconds &&
		p.QualityLead == recommended.QualityLead &&
		p.MaxEscapes == recommended.MaxEscapes &&
		p.EscapeWindowSeconds == recommended.EscapeWindowSeconds &&
		p.EliteConfirmations == recommended.EliteConfirmations
}

func (p SmartStickyPolicy) hasExplicitValues() bool {
	return p.TargetScore != 0 || p.ReviewIntervalSeconds != 0 || p.SwitchCooldownSeconds != 0 || p.QualityLead != 0 || p.MaxEscapes != 0 || p.EscapeWindowSeconds != 0 || p.EliteConfirmations != 0
}

func RecommendedSmartStickyPolicy() SmartStickyPolicy {
	return SmartStickyPolicy{Preset: "recommended", TargetScore: 70, ReviewIntervalSeconds: int(smartStickyWeakReviewInterval / time.Second), SwitchCooldownSeconds: int(smartStickyWeakSwitchCooldown / time.Second), QualityLead: smartStickyWeakQualityLead, MaxEscapes: 3, EscapeWindowSeconds: 3600, EliteConfirmations: smartStickyEliteConfirmations}
}

func StableSmartStickyPolicy() SmartStickyPolicy {
	return SmartStickyPolicy{Preset: "stability", TargetScore: 80, ReviewIntervalSeconds: 15, SwitchCooldownSeconds: 15, QualityLead: 2, MaxEscapes: 12, EscapeWindowSeconds: 3600, EliteConfirmations: 1, configured: true}
}

func (p SmartStickyPolicy) Normalize() (SmartStickyPolicy, error) {
	custom := p.configured || strings.EqualFold(p.Preset, "custom") || strings.EqualFold(p.Preset, "stability")
	if p.TargetScore == 0 {
		p.TargetScore = RecommendedSmartStickyPolicy().TargetScore
	}
	if p.ReviewIntervalSeconds == 0 {
		p.ReviewIntervalSeconds = RecommendedSmartStickyPolicy().ReviewIntervalSeconds
	}
	if p.SwitchCooldownSeconds == 0 && !custom {
		p.SwitchCooldownSeconds = RecommendedSmartStickyPolicy().SwitchCooldownSeconds
	}
	if p.QualityLead == 0 && !custom {
		p.QualityLead = RecommendedSmartStickyPolicy().QualityLead
	}
	if p.MaxEscapes == 0 {
		p.MaxEscapes = RecommendedSmartStickyPolicy().MaxEscapes
	}
	if p.EscapeWindowSeconds == 0 {
		p.EscapeWindowSeconds = RecommendedSmartStickyPolicy().EscapeWindowSeconds
	}
	if p.EliteConfirmations == 0 {
		p.EliteConfirmations = RecommendedSmartStickyPolicy().EliteConfirmations
	}
	if math.IsNaN(p.TargetScore) || math.IsInf(p.TargetScore, 0) || math.IsNaN(p.QualityLead) || math.IsInf(p.QualityLead, 0) || p.TargetScore < 0 || p.TargetScore > 100 || p.ReviewIntervalSeconds < 15 || p.ReviewIntervalSeconds > 3600 || p.SwitchCooldownSeconds < 0 || p.SwitchCooldownSeconds > 3600 || p.QualityLead < 0 || p.QualityLead > 50 || p.MaxEscapes < 1 || p.MaxEscapes > 100 || p.EscapeWindowSeconds < 60 || p.EscapeWindowSeconds > 86400 || p.EliteConfirmations < 1 || p.EliteConfirmations > 5 {
		return SmartStickyPolicy{}, fmt.Errorf("invalid smart sticky policy")
	}
	return p, nil
}

func smartStickyPolicyKey(groupID int64) string {
	return smartStickyPolicyKeyPrefix + strconv.FormatInt(groupID, 10)
}

func (s *adminServiceImpl) GetSmartStickyPolicy(ctx context.Context, groupID int64) (SmartStickyPolicy, error) {
	p := RecommendedSmartStickyPolicy()
	if groupID <= 0 || s.settingService == nil || s.settingService.settingRepo == nil {
		return p, nil
	}
	raw, err := s.settingService.settingRepo.GetValue(ctx, smartStickyPolicyKey(groupID))
	if err != nil {
		return p, nil
	}
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return RecommendedSmartStickyPolicy(), nil
	}
	p.configured = true
	return p.Normalize()
}

func (s *adminServiceImpl) UpdateSmartStickyPolicy(ctx context.Context, groupID int64, policy SmartStickyPolicy) (SmartStickyPolicy, error) {
	if groupID <= 0 {
		return SmartStickyPolicy{}, fmt.Errorf("invalid group id")
	}
	if !policy.hasExplicitValues() && strings.EqualFold(policy.Preset, "recommended") {
		policy = RecommendedSmartStickyPolicy()
	}
	if !policy.hasExplicitValues() && strings.EqualFold(policy.Preset, "stability") {
		policy = StableSmartStickyPolicy()
	}
	policy.configured = true
	policy, err := policy.Normalize()
	if err != nil {
		return SmartStickyPolicy{}, err
	}
	if s.settingService == nil || s.settingService.settingRepo == nil {
		return policy, nil
	}
	raw, err := json.Marshal(policy)
	if err != nil {
		return SmartStickyPolicy{}, err
	}
	if err := s.settingService.settingRepo.Set(ctx, smartStickyPolicyKey(groupID), string(raw)); err != nil {
		return SmartStickyPolicy{}, err
	}
	return policy, nil
}

func (s *OpenAIGatewayService) smartStickyPolicy(ctx context.Context, groupID int64) SmartStickyPolicy {
	p := RecommendedSmartStickyPolicy()
	if s == nil || s.settingService == nil || s.settingService.settingRepo == nil || groupID <= 0 {
		return p
	}
	raw, err := s.settingService.settingRepo.GetValue(ctx, smartStickyPolicyKey(groupID))
	if err != nil {
		return p
	}
	if json.Unmarshal([]byte(raw), &p) != nil {
		return RecommendedSmartStickyPolicy()
	}
	p.configured = true
	if normalized, err := p.Normalize(); err == nil {
		return normalized
	}
	return RecommendedSmartStickyPolicy()
}
