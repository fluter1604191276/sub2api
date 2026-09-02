package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"unicode"
)

const (
	PublicCatalogMediaHidden  = "hidden"
	PublicCatalogMediaVisible = "visible"
	maxPublicCatalogOverrides = 10000
)

var publicCatalogMediaPlatforms = map[string]struct{}{
	"image": {}, "images": {}, "video": {}, "videos": {},
	"seedance": {}, "veo": {}, "sora": {},
}

// PublicCatalogVisibilityConfig controls only which models are rendered in the
// two user-facing catalogues. Models contains explicit platform:model overrides.
type PublicCatalogVisibilityConfig struct {
	DefaultMediaVisibility string          `json:"default_media_visibility"`
	Models                 map[string]bool `json:"models"`
}

// PublicCatalogModelCandidate is an active channel model available for an admin
// to show or hide. It intentionally contains no internal channel or group data.
type PublicCatalogModelCandidate struct {
	Key            string      `json:"key"`
	Platform       string      `json:"platform"`
	Model          string      `json:"model"`
	BillingMode    BillingMode `json:"billing_mode"`
	IsMedia        bool        `json:"is_media"`
	DefaultVisible bool        `json:"default_visible"`
	Visible        bool        `json:"visible"`
}

// PublicCatalogVisibilityView is the admin-facing configuration plus the
// current active-channel candidates. Stale overrides remain in Models even if a
// candidate temporarily disappears.
type PublicCatalogVisibilityView struct {
	DefaultMediaVisibility string                        `json:"default_media_visibility"`
	Models                 map[string]bool               `json:"models"`
	Candidates             []PublicCatalogModelCandidate `json:"candidates"`
}

func DefaultPublicCatalogVisibilityConfig() PublicCatalogVisibilityConfig {
	return PublicCatalogVisibilityConfig{
		DefaultMediaVisibility: PublicCatalogMediaHidden,
		Models:                 map[string]bool{},
	}
}

// ValidateAndNormalizePublicCatalogVisibility validates an admin payload and
// canonicalizes all lookup keys for case-insensitive matching.
func ValidateAndNormalizePublicCatalogVisibility(input PublicCatalogVisibilityConfig) (PublicCatalogVisibilityConfig, error) {
	policy := strings.ToLower(strings.TrimSpace(input.DefaultMediaVisibility))
	if policy != PublicCatalogMediaHidden && policy != PublicCatalogMediaVisible {
		return PublicCatalogVisibilityConfig{}, fmt.Errorf(
			"default_media_visibility must be %q or %q",
			PublicCatalogMediaHidden,
			PublicCatalogMediaVisible,
		)
	}
	if len(input.Models) > maxPublicCatalogOverrides {
		return PublicCatalogVisibilityConfig{}, fmt.Errorf("model visibility overrides exceed %d entries", maxPublicCatalogOverrides)
	}

	models := make(map[string]bool, len(input.Models))
	for raw, visible := range input.Models {
		platform, model, ok := strings.Cut(raw, ":")
		if !ok {
			return PublicCatalogVisibilityConfig{}, fmt.Errorf("invalid model visibility key %q: expected platform:model", raw)
		}
		key, err := canonicalPublicCatalogModelKey(platform, model)
		if err != nil {
			return PublicCatalogVisibilityConfig{}, fmt.Errorf("invalid model visibility key %q: %w", raw, err)
		}
		if existing, duplicate := models[key]; duplicate && existing != visible {
			return PublicCatalogVisibilityConfig{}, fmt.Errorf("conflicting visibility overrides for %q", key)
		}
		models[key] = visible
	}

	return PublicCatalogVisibilityConfig{
		DefaultMediaVisibility: policy,
		Models:                 models,
	}, nil
}

func canonicalPublicCatalogModelKey(platform, model string) (string, error) {
	platform = strings.ToLower(strings.TrimSpace(platform))
	model = strings.ToLower(strings.TrimSpace(model))
	if platform == "" || model == "" {
		return "", fmt.Errorf("platform and model must not be empty")
	}
	if len(platform) > 128 || len(model) > 512 {
		return "", fmt.Errorf("platform or model is too long")
	}
	if strings.Contains(platform, ":") {
		return "", fmt.Errorf("platform must not contain ':'")
	}
	if strings.IndexFunc(platform+model, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("platform and model must not contain control characters")
	}
	return platform + ":" + model, nil
}

func publicCatalogModelKey(platform, model string) string {
	key, err := canonicalPublicCatalogModelKey(platform, model)
	if err != nil {
		return ""
	}
	return key
}

// IsPublicCatalogMediaModel uses catalogue metadata and conservative name
// heuristics to identify image/video generation models.
func IsPublicCatalogMediaModel(platform, model string, billingMode BillingMode) bool {
	platform = strings.ToLower(strings.TrimSpace(platform))
	model = strings.ToLower(strings.TrimSpace(model))
	if billingMode == BillingModeImage || billingMode == BillingModeVideo {
		return true
	}
	if _, ok := publicCatalogMediaPlatforms[platform]; ok {
		return true
	}
	for _, prefix := range []string{"seedance", "veo", "sora"} {
		if model == prefix || strings.HasPrefix(model, prefix+"-") {
			return true
		}
	}
	return strings.Contains(model, "image") ||
		strings.Contains(model, "imagine") ||
		strings.Contains(model, "video")
}

func isDefaultPublicGPTImage(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return model == "gpt-image" || strings.HasPrefix(model, "gpt-image-")
}

// IsVisible resolves explicit overrides first, then the text/media defaults.
func (c PublicCatalogVisibilityConfig) IsVisible(platform, model string, billingMode BillingMode) bool {
	if key := publicCatalogModelKey(platform, model); key != "" {
		if visible, ok := c.Models[key]; ok {
			return visible
		}
	}
	if !IsPublicCatalogMediaModel(platform, model, billingMode) {
		return true
	}
	if isDefaultPublicGPTImage(model) {
		return true
	}
	return c.DefaultMediaVisibility == PublicCatalogMediaVisible
}

// GetPublicCatalogVisibility is fail-safe for user-facing reads: text remains
// visible, while unreviewed media remains hidden on any read or parse failure.
func (s *SettingService) GetPublicCatalogVisibility(ctx context.Context) PublicCatalogVisibilityConfig {
	fallback := DefaultPublicCatalogVisibilityConfig()
	if s == nil || s.settingRepo == nil {
		return fallback
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyPublicCatalogVisibility)
	if err != nil {
		if !errors.Is(err, ErrSettingNotFound) {
			slog.Warn("public_catalog_visibility_read_failed", "error", err)
		}
		return fallback
	}
	var stored PublicCatalogVisibilityConfig
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		slog.Warn("public_catalog_visibility_parse_failed", "error", err)
		return fallback
	}
	normalized, err := ValidateAndNormalizePublicCatalogVisibility(stored)
	if err != nil {
		slog.Warn("public_catalog_visibility_invalid", "error", err)
		return fallback
	}
	return normalized
}

func (s *SettingService) UpdatePublicCatalogVisibility(ctx context.Context, input PublicCatalogVisibilityConfig) (PublicCatalogVisibilityConfig, error) {
	if s == nil || s.settingRepo == nil {
		return PublicCatalogVisibilityConfig{}, fmt.Errorf("setting service is unavailable")
	}
	normalized, err := ValidateAndNormalizePublicCatalogVisibility(input)
	if err != nil {
		return PublicCatalogVisibilityConfig{}, err
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return PublicCatalogVisibilityConfig{}, fmt.Errorf("marshal public catalog visibility: %w", err)
	}
	if err := s.settingRepo.Set(ctx, SettingKeyPublicCatalogVisibility, string(raw)); err != nil {
		return PublicCatalogVisibilityConfig{}, fmt.Errorf("save public catalog visibility: %w", err)
	}
	if s.onUpdate != nil {
		s.onUpdate()
	}
	return normalized, nil
}

// BuildPublicCatalogModelCandidates deduplicates active-channel model metadata
// by canonical platform:model key. A priced duplicate may enrich one without a
// billing mode.
func BuildPublicCatalogModelCandidates(channels []AvailableChannel, config PublicCatalogVisibilityConfig) []PublicCatalogModelCandidate {
	byKey := make(map[string]PublicCatalogModelCandidate)
	for i := range channels {
		if channels[i].Status != StatusActive {
			continue
		}
		for j := range channels[i].SupportedModels {
			model := channels[i].SupportedModels[j]
			key, err := canonicalPublicCatalogModelKey(model.Platform, model.Name)
			if err != nil {
				continue
			}
			mode := BillingMode("")
			if model.Pricing != nil {
				mode = model.Pricing.BillingMode
			}
			candidate, exists := byKey[key]
			if !exists {
				candidate = PublicCatalogModelCandidate{
					Key:      key,
					Platform: strings.TrimSpace(model.Platform),
					Model:    strings.TrimSpace(model.Name),
				}
			}
			if candidate.BillingMode == "" && mode != "" {
				candidate.BillingMode = mode
			}
			byKey[key] = candidate
		}
	}

	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]PublicCatalogModelCandidate, 0, len(keys))
	for _, key := range keys {
		candidate := byKey[key]
		candidate.IsMedia = IsPublicCatalogMediaModel(candidate.Platform, candidate.Model, candidate.BillingMode)
		candidate.DefaultVisible = DefaultPublicCatalogVisibilityConfig().IsVisible(
			candidate.Platform,
			candidate.Model,
			candidate.BillingMode,
		)
		candidate.Visible = config.IsVisible(candidate.Platform, candidate.Model, candidate.BillingMode)
		out = append(out, candidate)
	}
	return out
}

func BuildPublicCatalogVisibilityView(channels []AvailableChannel, config PublicCatalogVisibilityConfig) PublicCatalogVisibilityView {
	models := make(map[string]bool, len(config.Models))
	for key, visible := range config.Models {
		models[key] = visible
	}
	return PublicCatalogVisibilityView{
		DefaultMediaVisibility: config.DefaultMediaVisibility,
		Models:                 models,
		Candidates:             BuildPublicCatalogModelCandidates(channels, config),
	}
}
