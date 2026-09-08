//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type publicCatalogSettingRepoStub struct {
	SettingRepository
	values map[string]string
	getErr error
	setErr error
}

func (r *publicCatalogSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	if r.getErr != nil {
		return "", r.getErr
	}
	if value, ok := r.values[key]; ok {
		return value, nil
	}
	return "", ErrSettingNotFound
}

func (r *publicCatalogSettingRepoStub) Set(_ context.Context, key, value string) error {
	if r.setErr != nil {
		return r.setErr
	}
	if r.values == nil {
		r.values = make(map[string]string)
	}
	r.values[key] = value
	return nil
}

func TestPublicCatalogVisibilityDefaultPolicy(t *testing.T) {
	cfg := DefaultPublicCatalogVisibilityConfig()

	require.True(t, cfg.IsVisible("openai", "gpt-5.6-sol", BillingModeToken))
	require.True(t, cfg.IsVisible("openai", "gpt-image-1.5", BillingModeImage))
	require.False(t, cfg.IsVisible("gemini", "gemini-3.1-flash-image", BillingModeImage))
	require.False(t, cfg.IsVisible("video", "provider-render-v1", BillingModeVideo))
}

func TestPublicCatalogVisibilityExplicitOverridesAreCaseInsensitive(t *testing.T) {
	cfg, err := ValidateAndNormalizePublicCatalogVisibility(PublicCatalogVisibilityConfig{
		DefaultMediaVisibility: PublicCatalogMediaHidden,
		Models: map[string]bool{
			" OpenAI:GPT-5.6-SOL ":            false,
			" Gemini:Gemini-3.1-Flash-Image ": true,
		},
	})
	require.NoError(t, err)
	require.Equal(t, map[string]bool{
		"openai:gpt-5.6-sol":            false,
		"gemini:gemini-3.1-flash-image": true,
	}, cfg.Models)

	require.False(t, cfg.IsVisible("OPENAI", "GPT-5.6-SOL", BillingModeToken))
	require.True(t, cfg.IsVisible("GEMINI", "GEMINI-3.1-FLASH-IMAGE", BillingModeImage))
}

func TestValidatePublicCatalogVisibilityRejectsInvalidPolicyAndKeys(t *testing.T) {
	_, err := ValidateAndNormalizePublicCatalogVisibility(PublicCatalogVisibilityConfig{
		DefaultMediaVisibility: "sometimes",
	})
	require.Error(t, err)

	_, err = ValidateAndNormalizePublicCatalogVisibility(PublicCatalogVisibilityConfig{
		DefaultMediaVisibility: PublicCatalogMediaHidden,
		Models:                 map[string]bool{"missing-platform": true},
	})
	require.Error(t, err)
}

func TestGetPublicCatalogVisibilityFallsBackOnMissingMalformedOrReadError(t *testing.T) {
	for name, repo := range map[string]*publicCatalogSettingRepoStub{
		"missing":    {values: map[string]string{}},
		"malformed":  {values: map[string]string{SettingKeyPublicCatalogVisibility: "{not-json"}},
		"read error": {getErr: errors.New("database unavailable")},
	} {
		t.Run(name, func(t *testing.T) {
			svc := NewSettingService(repo, &config.Config{})
			got := svc.GetPublicCatalogVisibility(context.Background())
			require.Equal(t, PublicCatalogMediaHidden, got.DefaultMediaVisibility)
			require.Empty(t, got.Models)
			require.False(t, got.IsVisible("gemini", "gemini-image", BillingModeImage))
		})
	}
}

func TestUpdatePublicCatalogVisibilityPersistsCanonicalConfigAndInvalidatesCaches(t *testing.T) {
	repo := &publicCatalogSettingRepoStub{values: map[string]string{}}
	svc := NewSettingService(repo, &config.Config{})
	invalidated := 0
	svc.SetOnUpdateCallback(func() { invalidated++ })

	updated, err := svc.UpdatePublicCatalogVisibility(context.Background(), PublicCatalogVisibilityConfig{
		DefaultMediaVisibility: PublicCatalogMediaVisible,
		Models:                 map[string]bool{" OpenAI:GPT-IMAGE-1.5 ": false},
	})
	require.NoError(t, err)
	require.Equal(t, 1, invalidated)
	require.Equal(t, map[string]bool{"openai:gpt-image-1.5": false}, updated.Models)

	var stored PublicCatalogVisibilityConfig
	require.NoError(t, json.Unmarshal([]byte(repo.values[SettingKeyPublicCatalogVisibility]), &stored))
	require.Equal(t, updated, stored)
}

func TestBuildPublicCatalogModelCandidatesUsesActiveChannelsAndStableDeduplication(t *testing.T) {
	cfg := DefaultPublicCatalogVisibilityConfig()
	channels := []AvailableChannel{
		{
			Name:   "b",
			Status: StatusActive,
			SupportedModels: []SupportedModel{
				{Name: "gpt-5.6-sol", Platform: "openai", Pricing: &ChannelModelPricing{BillingMode: BillingModeToken}},
				{Name: "Gemini-3.1-Flash-Image", Platform: "Gemini", Pricing: &ChannelModelPricing{BillingMode: BillingModeImage}},
			},
		},
		{
			Name:   "a",
			Status: StatusActive,
			SupportedModels: []SupportedModel{
				{Name: "GEMINI-3.1-FLASH-IMAGE", Platform: "GEMINI"},
			},
		},
		{
			Name:   "disabled",
			Status: StatusDisabled,
			SupportedModels: []SupportedModel{
				{Name: "sora-2", Platform: "video", Pricing: &ChannelModelPricing{BillingMode: BillingModeVideo}},
			},
		},
	}

	got := BuildPublicCatalogModelCandidates(channels, cfg)
	require.Len(t, got, 2)
	require.Equal(t, "gemini:gemini-3.1-flash-image", got[0].Key)
	require.Equal(t, BillingModeImage, got[0].BillingMode)
	require.True(t, got[0].IsMedia)
	require.False(t, got[0].DefaultVisible)
	require.False(t, got[0].Visible)
	require.Equal(t, "openai:gpt-5.6-sol", got[1].Key)
	require.False(t, got[1].IsMedia)
	require.True(t, got[1].DefaultVisible)
	require.True(t, got[1].Visible)
}
