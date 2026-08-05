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

func previewFloat64Ptr(value float64) *float64 { return &value }
func previewIntPtr(value int) *int             { return &value }
