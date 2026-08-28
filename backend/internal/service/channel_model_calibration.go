package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

const (
	ModelCalibrationSkipNoTargetModels       = "no_target_models"
	ModelCalibrationSkipNoPricing            = "no_pricing"
	ModelCalibrationSkipAmbiguousPricing     = "ambiguous_pricing"
	ModelCalibrationSkipWouldEmptyPricing    = "would_empty_pricing"
	ModelCalibrationSkipChannelMappingSource = "channel_mapping_source"
	ModelCalibrationSkipModelPatternConflict = "model_pattern_conflict"
)

type channelModelCalibrationAccountRepository interface {
	ListByGroup(ctx context.Context, groupID int64) ([]Account, error)
}

type channelModelCalibrationWriteRepository interface {
	ApplyModelCalibration(ctx context.Context, updates []ChannelPricingModelsUpdate) error
}

// ChannelPricingModelsUpdate is the minimal persistent change made by model calibration.
// Pricing values, intervals, billing modes, and mappings are intentionally excluded.
type ChannelPricingModelsUpdate struct {
	ChannelID int64
	PricingID int64
	Models    []string
}

type ModelCalibrationSkippedItem struct {
	Platform string `json:"platform"`
	Model    string `json:"model,omitempty"`
	Reason   string `json:"reason"`
}

type ChannelModelCalibrationPlatformPreview struct {
	Platform             string                        `json:"platform"`
	CurrentModelCount    int                           `json:"current_model_count"`
	TargetModelCount     int                           `json:"target_model_count"`
	CalibratedModelCount int                           `json:"calibrated_model_count"`
	UnchangedCount       int                           `json:"unchanged_count"`
	Additions            []string                      `json:"additions"`
	Removals             []string                      `json:"removals"`
	Skipped              []ModelCalibrationSkippedItem `json:"skipped"`
	Applicable           bool                          `json:"applicable"`
	Changed              bool                          `json:"changed"`

	updates []ChannelPricingModelsUpdate
}

type ChannelModelCalibrationChannelPreview struct {
	ChannelID            int64                                    `json:"channel_id"`
	ChannelName          string                                   `json:"channel_name"`
	CurrentModelCount    int                                      `json:"current_model_count"`
	TargetModelCount     int                                      `json:"target_model_count"`
	CalibratedModelCount int                                      `json:"calibrated_model_count"`
	UnchangedCount       int                                      `json:"unchanged_count"`
	Additions            []string                                 `json:"additions"`
	Removals             []string                                 `json:"removals"`
	Skipped              []ModelCalibrationSkippedItem            `json:"skipped"`
	Applicable           bool                                     `json:"applicable"`
	Changed              bool                                     `json:"changed"`
	Platforms            []ChannelModelCalibrationPlatformPreview `json:"platforms"`

	groupIDs []int64
	updates  []ChannelPricingModelsUpdate
}

type ChannelModelCalibrationPreview struct {
	Channels           []ChannelModelCalibrationChannelPreview `json:"channels"`
	TotalChannels      int                                     `json:"total_channels"`
	ApplicableChannels int                                     `json:"applicable_channels"`
	ChangedChannels    int                                     `json:"changed_channels"`
	AdditionCount      int                                     `json:"addition_count"`
	RemovalCount       int                                     `json:"removal_count"`
	SkippedCount       int                                     `json:"skipped_count"`
	PricingRowsChanged int                                     `json:"pricing_rows_changed"`
	AppliedPricingRows int                                     `json:"applied_pricing_rows"`
}

func (s *ChannelService) PreviewModelCalibration(ctx context.Context) (*ChannelModelCalibrationPreview, error) {
	return s.buildModelCalibrationPreview(ctx)
}

func (s *ChannelService) ApplyModelCalibration(ctx context.Context) (*ChannelModelCalibrationPreview, error) {
	preview, err := s.buildModelCalibrationPreview(ctx)
	if err != nil {
		return nil, err
	}

	updates := make([]ChannelPricingModelsUpdate, 0, preview.PricingRowsChanged)
	changedGroupIDs := make([]int64, 0)
	for i := range preview.Channels {
		channelPreview := &preview.Channels[i]
		if len(channelPreview.updates) == 0 {
			continue
		}
		updates = append(updates, channelPreview.updates...)
		changedGroupIDs = append(changedGroupIDs, channelPreview.groupIDs...)
	}
	if len(updates) == 0 {
		return preview, nil
	}

	writeRepo, ok := s.repo.(channelModelCalibrationWriteRepository)
	if !ok {
		return nil, fmt.Errorf("channel repository does not support model calibration")
	}
	if err := writeRepo.ApplyModelCalibration(ctx, updates); err != nil {
		return nil, fmt.Errorf("apply channel model calibration: %w", err)
	}

	preview.AppliedPricingRows = len(updates)
	s.invalidateCache()
	s.invalidateAuthCacheForGroups(ctx, nil, changedGroupIDs)
	return preview, nil
}

func (s *ChannelService) buildModelCalibrationPreview(ctx context.Context) (*ChannelModelCalibrationPreview, error) {
	if s.accountRepo == nil {
		return nil, fmt.Errorf("account repository is not configured for channel model calibration")
	}

	channels, err := s.repo.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("list channels for model calibration: %w", err)
	}

	groupIDs := uniqueChannelGroupIDs(channels)
	groupPlatforms := make(map[int64]string)
	if len(groupIDs) > 0 {
		groupPlatforms, err = s.repo.GetGroupPlatforms(ctx, groupIDs)
		if err != nil {
			return nil, fmt.Errorf("get channel group platforms for model calibration: %w", err)
		}
	}

	accountsByGroup := make(map[int64][]Account, len(groupIDs))
	for _, groupID := range groupIDs {
		accounts, listErr := s.accountRepo.ListByGroup(ctx, groupID)
		if listErr != nil {
			return nil, fmt.Errorf("list accounts for group %d: %w", groupID, listErr)
		}
		accountsByGroup[groupID] = accounts
	}

	return buildChannelModelCalibrationPreview(channels, groupPlatforms, accountsByGroup), nil
}

func uniqueChannelGroupIDs(channels []Channel) []int64 {
	seen := make(map[int64]struct{})
	ids := make([]int64, 0)
	for i := range channels {
		for _, groupID := range channels[i].GroupIDs {
			if groupID <= 0 {
				continue
			}
			if _, ok := seen[groupID]; ok {
				continue
			}
			seen[groupID] = struct{}{}
			ids = append(ids, groupID)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func buildChannelModelCalibrationPreview(channels []Channel, groupPlatforms map[int64]string, accountsByGroup map[int64][]Account) *ChannelModelCalibrationPreview {
	preview := &ChannelModelCalibrationPreview{
		Channels:      make([]ChannelModelCalibrationChannelPreview, 0, len(channels)),
		TotalChannels: len(channels),
	}

	for i := range channels {
		channelPreview := buildSingleChannelModelCalibration(channels[i], groupPlatforms, accountsByGroup)
		preview.Channels = append(preview.Channels, channelPreview)
		if channelPreview.Applicable {
			preview.ApplicableChannels++
		}
		if channelPreview.Changed {
			preview.ChangedChannels++
		}
		preview.AdditionCount += len(channelPreview.Additions)
		preview.RemovalCount += len(channelPreview.Removals)
		preview.SkippedCount += len(channelPreview.Skipped)
		preview.PricingRowsChanged += len(channelPreview.updates)
	}

	return preview
}

func buildSingleChannelModelCalibration(channel Channel, groupPlatforms map[int64]string, accountsByGroup map[int64][]Account) ChannelModelCalibrationChannelPreview {
	targetsByPlatform := make(map[string]map[string]string)
	for _, groupID := range channel.GroupIDs {
		platform := strings.TrimSpace(groupPlatforms[groupID])
		if platform == "" {
			continue
		}
		if targetsByPlatform[platform] == nil {
			targetsByPlatform[platform] = make(map[string]string)
		}
		for i := range accountsByGroup[groupID] {
			account := &accountsByGroup[groupID][i]
			if account.Status != StatusActive || !account.Schedulable || account.Platform != platform {
				continue
			}
			for model := range account.GetModelMapping() {
				addCanonicalModel(targetsByPlatform[platform], model)
			}
		}
	}

	pricingByPlatform := make(map[string][]ChannelModelPricing)
	for i := range channel.ModelPricing {
		platform := strings.TrimSpace(channel.ModelPricing[i].Platform)
		pricingByPlatform[platform] = append(pricingByPlatform[platform], channel.ModelPricing[i])
	}

	platformSet := make(map[string]struct{}, len(targetsByPlatform)+len(pricingByPlatform))
	for platform := range targetsByPlatform {
		platformSet[platform] = struct{}{}
	}
	for platform := range pricingByPlatform {
		platformSet[platform] = struct{}{}
	}
	platforms := make([]string, 0, len(platformSet))
	for platform := range platformSet {
		platforms = append(platforms, platform)
	}
	sort.Strings(platforms)

	result := ChannelModelCalibrationChannelPreview{
		ChannelID:   channel.ID,
		ChannelName: channel.Name,
		Applicable:  len(platforms) > 0,
		Platforms:   make([]ChannelModelCalibrationPlatformPreview, 0, len(platforms)),
		Additions:   []string{},
		Removals:    []string{},
		Skipped:     []ModelCalibrationSkippedItem{},
		groupIDs:    append([]int64(nil), channel.GroupIDs...),
	}
	for _, platform := range platforms {
		protected := make(map[string]string)
		for model := range channel.ModelMapping[platform] {
			addCanonicalModel(protected, model)
		}
		platformPreview := calibrateChannelPlatform(channel.ID, platform, pricingByPlatform[platform], targetsByPlatform[platform], protected)
		result.Platforms = append(result.Platforms, platformPreview)
		result.CurrentModelCount += platformPreview.CurrentModelCount
		result.TargetModelCount += platformPreview.TargetModelCount
		result.CalibratedModelCount += platformPreview.CalibratedModelCount
		result.UnchangedCount += platformPreview.UnchangedCount
		result.Additions = append(result.Additions, platformPreview.Additions...)
		result.Removals = append(result.Removals, platformPreview.Removals...)
		result.Skipped = append(result.Skipped, platformPreview.Skipped...)
		result.updates = append(result.updates, platformPreview.updates...)
		result.Applicable = result.Applicable && platformPreview.Applicable
		result.Changed = result.Changed || platformPreview.Changed
	}
	sort.Strings(result.Additions)
	sort.Strings(result.Removals)
	return result
}

func calibrateChannelPlatform(channelID int64, platform string, pricingRows []ChannelModelPricing, targets, protected map[string]string) ChannelModelCalibrationPlatformPreview {
	result := ChannelModelCalibrationPlatformPreview{
		Platform:   platform,
		Applicable: true,
		Additions:  []string{},
		Removals:   []string{},
		Skipped:    []ModelCalibrationSkippedItem{},
	}

	currentPatterns := uniquePricingPatterns(pricingRows)
	targetModels := canonicalModels(targets)
	result.CurrentModelCount = len(currentPatterns)
	result.TargetModelCount = len(targetModels)
	if len(targetModels) == 0 {
		result.Applicable = false
		result.CalibratedModelCount = result.CurrentModelCount
		result.Skipped = append(result.Skipped, ModelCalibrationSkippedItem{Platform: platform, Reason: ModelCalibrationSkipNoTargetModels})
		return result
	}
	if len(pricingRows) == 0 {
		result.Applicable = false
		for _, model := range targetModels {
			result.Skipped = append(result.Skipped, ModelCalibrationSkippedItem{Platform: platform, Model: model, Reason: ModelCalibrationSkipNoPricing})
		}
		return result
	}

	updatedModels := make(map[int64][]string, len(pricingRows))
	for i := range pricingRows {
		row := pricingRows[i]
		kept := make([]string, 0, len(row.Models))
		stale := make([]string, 0)
		for _, rawModel := range row.Models {
			model := strings.TrimSpace(rawModel)
			if model == "" {
				continue
			}
			if isWildcardPattern(model) || anyPatternCoversModel(targetModels, model) {
				kept = append(kept, model)
				continue
			}
			if anyPatternCoversModel(canonicalModels(protected), model) {
				kept = append(kept, model)
				result.Skipped = append(result.Skipped, ModelCalibrationSkippedItem{Platform: platform, Model: model, Reason: ModelCalibrationSkipChannelMappingSource})
				continue
			}
			stale = append(stale, model)
		}

		if len(kept) == 0 && len(stale) > 0 && (len(pricingRows) > 1 || len(targetModels) == 0) {
			kept = append(kept, stale...)
			for _, model := range stale {
				result.Skipped = append(result.Skipped, ModelCalibrationSkippedItem{Platform: platform, Model: model, Reason: ModelCalibrationSkipWouldEmptyPricing})
			}
		} else {
			result.Removals = append(result.Removals, stale...)
		}
		updatedModels[row.ID] = kept
	}

	missing := make([]string, 0)
	for _, model := range targetModels {
		if !pricingRowsCoverModel(updatedModels, model) {
			missing = append(missing, model)
		} else {
			result.UnchangedCount++
		}
	}

	if len(missing) > 0 {
		if len(pricingRows) != 1 {
			result.Applicable = false
			for _, model := range missing {
				result.Skipped = append(result.Skipped, ModelCalibrationSkippedItem{Platform: platform, Model: model, Reason: ModelCalibrationSkipAmbiguousPricing})
			}
		} else {
			rowID := pricingRows[0].ID
			for _, model := range missing {
				if conflictsWithPatterns(updatedModels[rowID], model) {
					result.Applicable = false
					result.Skipped = append(result.Skipped, ModelCalibrationSkippedItem{Platform: platform, Model: model, Reason: ModelCalibrationSkipModelPatternConflict})
					continue
				}
				updatedModels[rowID] = append(updatedModels[rowID], model)
				result.Additions = append(result.Additions, model)
			}
		}
	}

	for i := range pricingRows {
		row := pricingRows[i]
		models := updatedModels[row.ID]
		if stringSlicesEqual(row.Models, models) {
			continue
		}
		result.updates = append(result.updates, ChannelPricingModelsUpdate{
			ChannelID: channelID,
			PricingID: row.ID,
			Models:    append([]string(nil), models...),
		})
	}

	result.Changed = len(result.updates) > 0
	result.CalibratedModelCount = countUniqueUpdatedPatterns(updatedModels)
	sort.Strings(result.Additions)
	sort.Strings(result.Removals)
	if len(result.Skipped) > 0 {
		result.Applicable = false
	}
	return result
}

func addCanonicalModel(models map[string]string, raw string) {
	model := strings.TrimSpace(raw)
	if model == "" {
		return
	}
	key := strings.ToLower(model)
	if _, exists := models[key]; !exists {
		models[key] = model
	}
}

func canonicalModels(models map[string]string) []string {
	result := make([]string, 0, len(models))
	for _, model := range models {
		result = append(result, model)
	}
	sort.Slice(result, func(i, j int) bool { return strings.ToLower(result[i]) < strings.ToLower(result[j]) })
	return result
}

func uniquePricingPatterns(rows []ChannelModelPricing) []string {
	models := make(map[string]string)
	for i := range rows {
		for _, model := range rows[i].Models {
			addCanonicalModel(models, model)
		}
	}
	return canonicalModels(models)
}

func countUniqueUpdatedPatterns(rows map[int64][]string) int {
	models := make(map[string]string)
	for _, rowModels := range rows {
		for _, model := range rowModels {
			addCanonicalModel(models, model)
		}
	}
	return len(models)
}

func isWildcardPattern(pattern string) bool {
	return strings.HasSuffix(strings.TrimSpace(pattern), "*")
}

func patternCoversModel(pattern, model string) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	model = strings.ToLower(strings.TrimSpace(model))
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(model, strings.TrimSuffix(pattern, "*"))
	}
	return pattern == model
}

func anyPatternCoversModel(patterns []string, model string) bool {
	for _, pattern := range patterns {
		if patternCoversModel(pattern, model) {
			return true
		}
	}
	return false
}

func pricingRowsCoverModel(rows map[int64][]string, model string) bool {
	for _, patterns := range rows {
		if anyPatternCoversModel(patterns, model) {
			return true
		}
	}
	return false
}

func conflictsWithPatterns(patterns []string, candidate string) bool {
	candidateEntry := toModelEntry(candidate)
	for _, pattern := range patterns {
		if conflictsBetween(toModelEntry(pattern), candidateEntry) {
			return true
		}
	}
	return false
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
