//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func modelSet(values ...string) map[string]string {
	result := make(map[string]string, len(values))
	for _, value := range values {
		addCanonicalModel(result, value)
	}
	return result
}

func TestCalibrateChannelPlatformSinglePricingRowAddsAndPreservesStaleModels(t *testing.T) {
	preview := calibrateChannelPlatform(7, PlatformAnthropic, []ChannelModelPricing{
		{ID: 11, ChannelID: 7, Platform: PlatformAnthropic, BillingMode: BillingModeToken, Models: []string{"claude-old", "claude-sonnet-4-6"}},
	}, modelSet("claude-sonnet-4-6", "claude-opus-5"), nil)

	require.True(t, preview.Applicable)
	require.True(t, preview.Changed)
	require.Equal(t, []string{"claude-opus-5"}, preview.Additions)
	require.Empty(t, preview.Removals)
	require.Equal(t, 1, preview.UnchangedCount)
	require.Len(t, preview.updates, 1)
	require.Equal(t, []string{"claude-old", "claude-sonnet-4-6"}, preview.updates[0].PreviousModels)
	require.Equal(t, []string{"claude-old", "claude-sonnet-4-6", "claude-opus-5"}, preview.updates[0].Models)
	require.Contains(t, preview.Skipped, ModelCalibrationSkippedItem{Platform: PlatformAnthropic, Model: "claude-old", Reason: ModelCalibrationSkipStaleModelReview})
}

func TestCalibrateChannelPlatformSkipsAmbiguousAdditionsAndPreservesExistingModels(t *testing.T) {
	preview := calibrateChannelPlatform(7, PlatformOpenAI, []ChannelModelPricing{
		{ID: 11, ChannelID: 7, Platform: PlatformOpenAI, BillingMode: BillingModeToken, Models: []string{"gpt-keep", "gpt-old"}},
		{ID: 12, ChannelID: 7, Platform: PlatformOpenAI, BillingMode: BillingModeToken, Models: []string{"gpt-other"}},
	}, modelSet("gpt-keep", "gpt-new"), nil)

	require.False(t, preview.Applicable)
	require.False(t, preview.Changed)
	require.Empty(t, preview.Removals)
	require.Empty(t, preview.updates)
	require.Contains(t, preview.Skipped, ModelCalibrationSkippedItem{Platform: PlatformOpenAI, Model: "gpt-new", Reason: ModelCalibrationSkipAmbiguousPricing})
	require.Contains(t, preview.Skipped, ModelCalibrationSkippedItem{Platform: PlatformOpenAI, Model: "gpt-old", Reason: ModelCalibrationSkipStaleModelReview})
	require.Contains(t, preview.Skipped, ModelCalibrationSkippedItem{Platform: PlatformOpenAI, Model: "gpt-other", Reason: ModelCalibrationSkipStaleModelReview})
}

func TestCalibrateChannelPlatformPreservesWildcardsAndChannelMappingSources(t *testing.T) {
	preview := calibrateChannelPlatform(7, PlatformAnthropic, []ChannelModelPricing{
		{ID: 11, ChannelID: 7, Platform: PlatformAnthropic, Models: []string{"claude-*", "public-alias"}},
	}, modelSet("claude-opus-5"), modelSet("public-alias"))

	require.True(t, preview.Applicable)
	require.False(t, preview.Changed)
	require.Equal(t, 1, preview.UnchangedCount)
	require.Empty(t, preview.Additions)
	require.Empty(t, preview.Removals)
	require.Contains(t, preview.Skipped, ModelCalibrationSkippedItem{Platform: PlatformAnthropic, Model: "public-alias", Reason: ModelCalibrationSkipChannelMappingSource})
}

func TestCalibrateChannelPlatformDoesNotAddTextModelsToImagePricing(t *testing.T) {
	preview := calibrateChannelPlatform(19, PlatformOpenAI, []ChannelModelPricing{
		{
			ID:          33,
			ChannelID:   19,
			Platform:    PlatformOpenAI,
			BillingMode: BillingModeImage,
			Models:      []string{"gpt-image-1"},
		},
	}, modelSet("gpt-image-1", "gpt-image-2", "gpt-5.6"), nil)

	require.True(t, preview.Applicable)
	require.True(t, preview.Changed)
	require.Equal(t, []string{"gpt-image-2"}, preview.Additions)
	require.Len(t, preview.updates, 1)
	require.Equal(t, []string{"gpt-image-1", "gpt-image-2"}, preview.updates[0].Models)
	require.Contains(t, preview.Skipped, ModelCalibrationSkippedItem{Platform: PlatformOpenAI, Model: "gpt-5.6", Reason: ModelCalibrationSkipBillingModeMismatch})
}

type calibrationAccountRepoStub struct {
	byGroup map[int64][]Account
}

func (s *calibrationAccountRepoStub) ListByGroup(_ context.Context, groupID int64) ([]Account, error) {
	return s.byGroup[groupID], nil
}

type calibrationChannelRepoStub struct {
	*mockChannelRepository
	applied []ChannelPricingModelsUpdate
}

func (s *calibrationChannelRepoStub) ApplyModelCalibration(_ context.Context, updates []ChannelPricingModelsUpdate) error {
	s.applied = append(s.applied, updates...)
	return nil
}

func TestApplyModelCalibrationAppliesSafeAdditionsWhileLeavingSkippedItemsForReview(t *testing.T) {
	channel := Channel{
		ID:       7,
		Name:     "Claude",
		GroupIDs: []int64{3},
		ModelPricing: []ChannelModelPricing{
			{ID: 11, ChannelID: 7, Platform: PlatformAnthropic, Models: []string{"claude-old"}},
		},
	}
	repo := &calibrationChannelRepoStub{mockChannelRepository: makeStandardRepo(channel, map[int64]string{3: PlatformAnthropic})}
	accounts := &calibrationAccountRepoStub{byGroup: map[int64][]Account{
		3: {
			{Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"model_mapping": map[string]any{"claude-opus-5": "upstream-opus-5"}}},
			{Platform: PlatformAnthropic, Status: StatusDisabled, Schedulable: true, Credentials: map[string]any{"model_mapping": map[string]any{"disabled-model": "disabled"}}},
			{Platform: PlatformAnthropic, Status: StatusActive, Schedulable: false, Credentials: map[string]any{"model_mapping": map[string]any{"paused-model": "paused"}}},
		},
	}}
	auth := &mockChannelAuthCacheInvalidator{}
	svc := NewChannelService(repo, nil, auth, nil, accounts)

	preview, err := svc.ApplyModelCalibration(context.Background())

	require.NoError(t, err)
	require.Equal(t, 1, preview.AppliedPricingRows)
	require.Equal(t, 1, preview.SkippedCount)
	require.Contains(t, preview.Channels[0].Skipped, ModelCalibrationSkippedItem{
		Platform: PlatformAnthropic,
		Model:    "claude-old",
		Reason:   ModelCalibrationSkipStaleModelReview,
	})
	require.Len(t, repo.applied, 1)
	require.Equal(t, []string{"claude-old"}, repo.applied[0].PreviousModels)
	require.Equal(t, []string{"claude-old", "claude-opus-5"}, repo.applied[0].Models)
	require.Equal(t, []int64{3}, auth.invalidatedGroupIDs)
}
