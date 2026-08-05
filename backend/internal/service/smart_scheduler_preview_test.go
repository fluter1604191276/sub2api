package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func qualityWindowForPreview(score, samples int) AccountQualityWindow {
	return AccountQualityWindow{
		SampleCount:           int64(samples),
		FirstTokenSampleCount: int64(samples),
		QualityScore:          &score,
	}
}

func TestSmartSchedulerPreviewModelMappingAndPools(t *testing.T) {
	account := &Account{
		ID:          7,
		Name:        "mapped",
		Platform:    PlatformAnthropic,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: map[string]any{"model_mapping": map[string]any{"claude-sonnet-4-6": "claude-sonnet-upstream"}},
	}
	group := &Group{ID: 3, Platform: PlatformAnthropic}
	quality := AccountQualityStats{
		Recent1h: AccountQualityPeriod{Last10: qualityWindowForPreview(92, 10), WindowHours: 1},
		Last10:   qualityWindowForPreview(88, 20),
		Activity: AccountQualityActivity{State: accountQualityActivityActive, SuccessfulRequestCount: 20},
	}
	item := buildSmartSchedulerPreviewItem(account, group, "claude-sonnet-4-6", "any", quality, SmartSchedulerErrorStats{}, nil)
	require.Equal(t, "primary", item.Pool)
	require.Equal(t, "claude-sonnet-upstream", item.ModelMapping)

	unsupported := buildSmartSchedulerPreviewItem(account, group, "claude-opus-5", "any", quality, SmartSchedulerErrorStats{}, nil)
	require.Equal(t, "isolated", unsupported.Pool)
	require.Equal(t, "模型不支持", unsupported.Reason)

	account.Schedulable = false
	paused := buildSmartSchedulerPreviewItem(account, group, "claude-sonnet-4-6", "any", quality, SmartSchedulerErrorStats{}, nil)
	require.Equal(t, "isolated", paused.Pool)
	require.Equal(t, "当前账号已暂停调度", paused.Reason)
}

func TestSmartSchedulerPreviewUsesV2AlgorithmVersion(t *testing.T) {
	require.Equal(t, "preview-v2", SmartSchedulerPreviewAlgorithmVersion)
}

func TestSmartSchedulerPreviewSupportsWildcardModelMapping(t *testing.T) {
	account := &Account{
		ID:          8,
		Name:        "wildcard",
		Platform:    PlatformAnthropic,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: map[string]any{"model_mapping": map[string]any{"claude-*": "claude-sonnet-upstream"}},
	}
	group := &Group{ID: 3, Platform: PlatformAnthropic}
	quality := AccountQualityStats{
		Recent1h: AccountQualityPeriod{Last10: qualityWindowForPreview(92, 10), WindowHours: 1},
		Last10:   qualityWindowForPreview(88, 20),
		Activity: AccountQualityActivity{State: accountQualityActivityActive, SuccessfulRequestCount: 20},
	}
	item := buildSmartSchedulerPreviewItem(account, group, "claude-opus-5", "any", quality, SmartSchedulerErrorStats{}, nil)
	require.Equal(t, "primary", item.Pool)
	require.Equal(t, "claude-sonnet-upstream", item.ModelMapping)
}

func TestSmartSchedulerPreviewDoesNotIsolateClientOnlyFailures(t *testing.T) {
	account := &Account{ID: 9, Name: "client-errors", Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true}
	group := &Group{ID: 2, Platform: PlatformOpenAI}
	quality := AccountQualityStats{Activity: AccountQualityActivity{State: accountQualityActivityFailing, FailedRequestCount: 5}}
	errors := SmartSchedulerErrorStats{ClientExcludedCount: 5}
	item := buildSmartSchedulerPreviewItem(account, group, "gpt-5", "any", quality, errors, nil)
	require.Equal(t, "warm", item.Pool)
	require.Equal(t, "observe", item.Decision)
}

func TestSmartSchedulerPreviewExcludesClientFailuresFromActivityState(t *testing.T) {
	perfect := 100
	account := &Account{ID: 9, Name: "client-errors", Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true}
	group := &Group{ID: 2, Platform: PlatformOpenAI}
	quality := AccountQualityStats{
		Recent1h: AccountQualityPeriod{Last10: qualityWindowForPreview(perfect, 10)},
		Last10:   qualityWindowForPreview(perfect, 10),
		Activity: AccountQualityActivity{
			State:                  accountQualityActivityDegraded,
			SuccessfulRequestCount: 10,
			FailedRequestCount:     5,
		},
	}
	errors := SmartSchedulerErrorStats{SuccessfulRequestCount: 10, ClientExcludedCount: 5}

	item := buildSmartSchedulerPreviewItem(account, group, "gpt-5", "any", quality, errors, nil)
	require.Equal(t, accountQualityActivityActive, item.Activity.State)
	require.Zero(t, item.Activity.FailedRequestCount)
	require.Equal(t, 100.0, *smartSchedulerScore(item, []SmartSchedulerPreviewItem{item}))
}

func TestSmartSchedulerPreviewDoesNotPromoteInsufficientEvidence(t *testing.T) {
	account := &Account{ID: 1, Name: "idle", Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true}
	group := &Group{ID: 2, Platform: PlatformOpenAI}
	item := buildSmartSchedulerPreviewItem(account, group, "gpt-5", "any", AccountQualityStats{}, SmartSchedulerErrorStats{}, nil)
	require.Equal(t, "warm", item.Pool)
	require.Equal(t, "observe", item.Decision)
	require.Nil(t, smartSchedulerScore(item, []SmartSchedulerPreviewItem{item}))
	require.Equal(t, "low", smartSchedulerConfidenceLabel(smartSchedulerConfidence(item)))
}

func TestSmartSchedulerPreviewFiltersUnsupportedTextEndpoint(t *testing.T) {
	account := &Account{
		ID:          10,
		Name:        "embeddings-only",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: map[string]any{"openai_capabilities": []any{"embeddings"}},
	}
	group := &Group{ID: 2, Platform: PlatformOpenAI}
	item := buildSmartSchedulerPreviewItem(account, group, "gpt-5", "responses", AccountQualityStats{}, SmartSchedulerErrorStats{}, nil)
	require.Equal(t, "isolated", item.Pool)
	require.False(t, item.EndpointSupported)
	require.Equal(t, "账号不支持所选端点", item.Reason)
}

func TestSmartSchedulerPreviewDemotesSaturatedAccountToWarmPool(t *testing.T) {
	perfect := 100
	account := &Account{ID: 10, Name: "busy", Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true}
	group := &Group{ID: 2, Platform: PlatformOpenAI}
	quality := AccountQualityStats{
		Recent1h: AccountQualityPeriod{Last10: qualityWindowForPreview(perfect, 10)},
		Last10:   qualityWindowForPreview(perfect, 10),
		Activity: AccountQualityActivity{State: accountQualityActivityActive, SuccessfulRequestCount: 10},
	}
	load := &AccountLoadInfo{LoadRate: 95}

	item := buildSmartSchedulerPreviewItem(account, group, "gpt-5", "any", quality, SmartSchedulerErrorStats{}, load)
	require.Equal(t, "warm", item.Pool)
	require.Equal(t, "observe", item.Decision)
	require.Equal(t, "实时负载接近饱和", item.Reason)
}

func TestSmartSchedulerPreviewSortsPoolBeforeScoreAndPriority(t *testing.T) {
	primaryLow := SmartSchedulerPreviewItem{AccountID: 2, Pool: "primary", Score: previewFloat64Ptr(60), Priority: previewIntPtr(20)}
	primaryHigh := SmartSchedulerPreviewItem{AccountID: 1, Pool: "primary", Score: previewFloat64Ptr(80), Priority: previewIntPtr(30)}
	warm := SmartSchedulerPreviewItem{AccountID: 3, Pool: "warm", Score: previewFloat64Ptr(99), Priority: previewIntPtr(1)}
	isolated := SmartSchedulerPreviewItem{AccountID: 4, Pool: "isolated", Priority: previewIntPtr(0)}
	items := []SmartSchedulerPreviewItem{isolated, warm, primaryLow, primaryHigh}
	sortSmartSchedulerItems(items)
	require.Equal(t, []int64{1, 2, 3, 4}, []int64{items[0].AccountID, items[1].AccountID, items[2].AccountID, items[3].AccountID})
}

func TestSmartSchedulerScoreUsesDeclaredWeights(t *testing.T) {
	perfect := 100
	item := SmartSchedulerPreviewItem{
		AccountID:      1,
		Pool:           "primary",
		CostMultiplier: 1,
		Quality1h: AccountQualityPeriod{
			Last10:  qualityWindowForPreview(perfect, 10),
			Last100: qualityWindowForPreview(perfect, 100),
		},
		Quality24h: AccountQualityPeriod{
			Last10:  qualityWindowForPreview(perfect, 10),
			Last100: qualityWindowForPreview(perfect, 100),
		},
		Activity: AccountQualityActivity{SuccessfulRequestCount: 100},
		Load:     &SmartSchedulerLoad{LoadRate: 0},
	}
	moreExpensive := item
	moreExpensive.AccountID = 2
	moreExpensive.CostMultiplier = 2

	score := smartSchedulerScore(item, []SmartSchedulerPreviewItem{item, moreExpensive})
	require.NotNil(t, score)
	require.Equal(t, 100.0, *score)
}

func TestSmartSchedulerCostScoreTreatsEqualCostsAsFullyCompetitive(t *testing.T) {
	item := SmartSchedulerPreviewItem{AccountID: 1, Pool: "primary", CostMultiplier: 0.04}
	peer := SmartSchedulerPreviewItem{AccountID: 2, Pool: "primary", CostMultiplier: 0.04}

	require.Equal(t, 100.0, relativeCostScore(item, []SmartSchedulerPreviewItem{item, peer}))
}

func TestSmartSchedulerScoreBackfillsMissingQualityWindow(t *testing.T) {
	perfect := 100
	item := SmartSchedulerPreviewItem{
		AccountID:      1,
		Pool:           "primary",
		CostMultiplier: 1,
		Quality1h: AccountQualityPeriod{
			Last10: qualityWindowForPreview(perfect, 10),
		},
		Activity: AccountQualityActivity{SuccessfulRequestCount: 10},
		Load:     &SmartSchedulerLoad{LoadRate: 0},
	}
	moreExpensive := item
	moreExpensive.AccountID = 2
	moreExpensive.CostMultiplier = 2

	score := smartSchedulerScore(item, []SmartSchedulerPreviewItem{item, moreExpensive})
	require.NotNil(t, score)
	require.Equal(t, 100.0, *score)
}

func TestSmartSchedulerQualityUsesRobustTTFTAndGenerationSpeed(t *testing.T) {
	meanOutlier := 100000.0
	p50TTFT := 2000.0
	p90TTFT := 90000.0
	p50TPS := 50.0
	p10TPS := 20.0
	window := AccountQualityWindow{
		SampleCount:                  10,
		FirstTokenSampleCount:        10,
		AverageFirstTokenMs:          &meanOutlier,
		P50FirstTokenMs:              &p50TTFT,
		P90FirstTokenMs:              &p90TTFT,
		GenerationSampleCount:        10,
		P50GenerationTokensPerSecond: &p50TPS,
		P10GenerationTokensPerSecond: &p10TPS,
	}

	result := applySmartSchedulerQualityScore(window)
	require.NotNil(t, result.QualityScore)
	require.NotNil(t, result.RoutingFirstTokenMs)
	require.InDelta(t, 28400, *result.RoutingFirstTokenMs, 0.001)
	require.NotNil(t, result.RoutingGenerationTokensPerSecond)
	require.InDelta(t, 41, *result.RoutingGenerationTokensPerSecond, 0.001)
	require.Greater(t, *result.QualityScore, 30)
}

func TestSmartSchedulerScoreExcludesClientAndPlatformFailures(t *testing.T) {
	perfect := 100
	item := SmartSchedulerPreviewItem{
		AccountID:                   1,
		Pool:                        "primary",
		CostMultiplier:              1,
		ErrorSuccessfulRequestCount: 100,
		ClientExcludedCount:         100,
		PlatformFailureCount:        100,
		Quality1h: AccountQualityPeriod{
			Last10: qualityWindowForPreview(perfect, 10),
		},
		Load: &SmartSchedulerLoad{LoadRate: 0},
	}
	moreExpensive := item
	moreExpensive.AccountID = 2
	moreExpensive.CostMultiplier = 2

	score := smartSchedulerScore(item, []SmartSchedulerPreviewItem{item, moreExpensive})
	require.NotNil(t, score)
	require.Equal(t, 100.0, *score)
}

func TestSmartSchedulerEvidenceScopesPreferExactThenModelEndpointAndGlobal(t *testing.T) {
	scopes := smartSchedulerQualityScopes("gpt-5", "responses")
	require.Equal(t, []string{
		smartSchedulerEvidenceModelEndpoint,
		smartSchedulerEvidenceModel,
		smartSchedulerEvidenceEndpoint,
		smartSchedulerEvidenceAccount,
	}, []string{scopes[0].Name, scopes[1].Name, scopes[2].Name, scopes[3].Name})
	require.Equal(t, "/v1/responses", scopes[0].Endpoint)
	require.Equal(t, "any", scopes[1].Endpoint)
	require.Empty(t, scopes[2].RequestedModel)
	require.False(t, scopes[0].Fallback)
	require.True(t, scopes[1].Fallback)
}

func TestSmartSchedulerFallbackQualityPreservesExactActivityAndStaysWarm(t *testing.T) {
	exact := AccountQualityStats{
		Activity: AccountQualityActivity{State: accountQualityActivityIdle},
	}
	fallback := AccountQualityStats{
		Recent1h: AccountQualityPeriod{Last10: qualityWindowForPreview(90, 10)},
		Activity: AccountQualityActivity{State: accountQualityActivityActive, SuccessfulRequestCount: 10},
	}
	scoped := []smartSchedulerScopedQuality{
		{Scope: smartSchedulerQualityScope{Name: smartSchedulerEvidenceModelEndpoint}, Stats: map[int64]AccountQualityStats{7: exact}},
		{Scope: smartSchedulerQualityScope{Name: smartSchedulerEvidenceModel, Fallback: true}, Stats: map[int64]AccountQualityStats{7: fallback}},
	}

	quality, scope, usedFallback := selectSmartSchedulerQualityEvidence(7, scoped)
	require.Equal(t, smartSchedulerEvidenceModel, scope)
	require.True(t, usedFallback)
	require.Equal(t, int64(0), quality.Activity.SuccessfulRequestCount)

	account := &Account{ID: 7, Name: "fallback", Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true}
	group := &Group{ID: 2, Platform: PlatformOpenAI}
	item := buildSmartSchedulerPreviewItem(account, group, "gpt-5", "responses", quality, SmartSchedulerErrorStats{}, nil)
	item.EvidenceScope = scope
	item.EvidenceFallback = usedFallback
	applySmartSchedulerEvidencePolicy(&item)
	require.Equal(t, "warm", item.Pool)
	require.Equal(t, "仅有回退质量证据，需探索验证", item.Reason)
}

func TestSmartSchedulerReliabilityWeightsRecentEvidenceMoreHeavily(t *testing.T) {
	item := SmartSchedulerPreviewItem{
		Activity:                    AccountQualityActivity{SuccessfulRequestCount: 8},
		ErrorSuccessfulRequestCount: 90,
		ProviderFailureCount:        10,
		RecentProviderFailureCount:  2,
	}
	require.InDelta(t, 84, smartSchedulerReliabilityScore(item), 0.001)
}

func TestSmartSchedulerConfidenceAdjustmentShrinksTowardGroupMedian(t *testing.T) {
	items := []SmartSchedulerPreviewItem{
		{AccountID: 1, Pool: "primary", RawScore: previewFloat64Ptr(100), Confidence: 1},
		{AccountID: 2, Pool: "warm", RawScore: previewFloat64Ptr(40), Confidence: 0.25},
	}

	applySmartSchedulerConfidenceAdjustment(items)
	require.Equal(t, 100.0, *items[0].Score)
	require.Equal(t, 62.5, *items[1].Score)
}

func TestSmartSchedulerExplorationPreviewMarksEligibleWarmAccounts(t *testing.T) {
	items := []SmartSchedulerPreviewItem{
		{AccountID: 1, Pool: "primary", Schedulable: true, ModelSupported: true, EndpointSupported: true},
		{AccountID: 2, Pool: "warm", Schedulable: true, ModelSupported: true, EndpointSupported: true, Activity: AccountQualityActivity{State: accountQualityActivityIdle}},
		{AccountID: 3, Pool: "warm", Schedulable: true, ModelSupported: true, EndpointSupported: true, Load: &SmartSchedulerLoad{LoadRate: 95}},
		{AccountID: 4, Pool: "isolated", Schedulable: true, ModelSupported: true, EndpointSupported: true},
	}

	rate := applySmartSchedulerExplorationPreview(items)
	require.True(t, items[1].ExplorationCandidate)
	require.False(t, items[2].ExplorationCandidate)
	require.InDelta(t, 0.075, rate, 0.0001)
}

func TestSmartSchedulerExplorationPreviewReturnsZeroWithoutWarmCandidates(t *testing.T) {
	items := []SmartSchedulerPreviewItem{
		{AccountID: 1, Pool: "primary", Schedulable: true, ModelSupported: true, EndpointSupported: true},
		{AccountID: 2, Pool: "isolated", Schedulable: true, ModelSupported: true, EndpointSupported: true},
	}

	rate := applySmartSchedulerExplorationPreview(items)
	require.Zero(t, rate)
	require.False(t, items[0].ExplorationCandidate)
	require.False(t, items[1].ExplorationCandidate)
}

func TestSmartSchedulerExplorationPreviewCapsAllWarmAccountsAtMaximum(t *testing.T) {
	items := []SmartSchedulerPreviewItem{
		{AccountID: 1, Pool: "warm", Schedulable: true, ModelSupported: true, EndpointSupported: true},
		{AccountID: 2, Pool: "warm", Schedulable: true, ModelSupported: true, EndpointSupported: true},
	}

	rate := applySmartSchedulerExplorationPreview(items)
	require.InDelta(t, smartSchedulerExplorationMax, rate, 0.0001)
	require.True(t, items[0].ExplorationCandidate)
	require.True(t, items[1].ExplorationCandidate)
}

func previewFloat64Ptr(value float64) *float64 { return &value }
func previewIntPtr(value int) *int             { return &value }
