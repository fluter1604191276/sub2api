package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type smartSchedulerStatsStub struct {
	quality           map[int64]AccountQualityStats
	qualityByScope    map[string]map[int64]AccountQualityStats
	errors            map[int64]SmartSchedulerErrorStats
	errorsByScope     map[string]map[int64]SmartSchedulerErrorStats
	accountErrors     map[int64]SmartSchedulerErrorStats
	qualityErr        error
	errorErr          error
	qualityCalls      int
	errorCalls        int
	accountErrorCalls int
	qualityCallScopes []string
	errorCallScopes   []string
	capacityLimited   int64
	capacityErr       error
}

type smartSchedulerAdminServiceStub struct {
	group    Group
	accounts []Account
}

type smartSchedulerRecoveryProbeRepoStub struct {
	GroupRecoveryProbeRepository
	states       map[int64]GroupRecoveryProbeState
	listCalls    int
	lastGroupID  int64
	lastModel    string
	lastAccounts []int64
}

func (s *smartSchedulerRecoveryProbeRepoStub) ListStates(_ context.Context, groupID int64, accountIDs []int64, model string) (map[int64]GroupRecoveryProbeState, error) {
	s.listCalls++
	s.lastGroupID = groupID
	s.lastModel = model
	s.lastAccounts = append([]int64(nil), accountIDs...)
	return s.states, nil
}

func (s *smartSchedulerAdminServiceStub) GetGroup(_ context.Context, _ int64) (*Group, error) {
	group := s.group
	return &group, nil
}

func (s *smartSchedulerAdminServiceStub) ListAccountsForSchedulerScoreFilter(_ context.Context, _, _, _, _ string, _ int64, _ string) ([]Account, error) {
	return append([]Account(nil), s.accounts...), nil
}

func smartSchedulerStatsScopeKey(requestedModel, endpoint string) string {
	return requestedModel + "|" + endpoint
}

func (s *smartSchedulerStatsStub) GetSmartSchedulerQualityStatsBatch(_ context.Context, accountIDs []int64, _ time.Time, requestedModel, endpoint string) (map[int64]AccountQualityStats, error) {
	s.qualityCalls++
	s.qualityCallScopes = append(s.qualityCallScopes, smartSchedulerStatsScopeKey(requestedModel, endpoint))
	if s.qualityErr != nil {
		return nil, s.qualityErr
	}
	quality := s.quality
	if scoped, ok := s.qualityByScope[smartSchedulerStatsScopeKey(requestedModel, endpoint)]; ok {
		quality = scoped
	}
	result := make(map[int64]AccountQualityStats, len(accountIDs))
	for _, accountID := range accountIDs {
		result[accountID] = quality[accountID]
	}
	return result, nil
}

func (s *smartSchedulerStatsStub) GetSmartSchedulerErrorStatsBatch(_ context.Context, accountIDs []int64, _ time.Time, requestedModel, endpoint string) (map[int64]SmartSchedulerErrorStats, error) {
	s.errorCalls++
	s.errorCallScopes = append(s.errorCallScopes, smartSchedulerStatsScopeKey(requestedModel, endpoint))
	if s.errorErr != nil {
		return nil, s.errorErr
	}
	errors := s.errors
	if scoped, ok := s.errorsByScope[smartSchedulerStatsScopeKey(requestedModel, endpoint)]; ok {
		errors = scoped
	}
	result := make(map[int64]SmartSchedulerErrorStats, len(accountIDs))
	for _, accountID := range accountIDs {
		result[accountID] = errors[accountID]
	}
	return result, nil
}

func (s *smartSchedulerStatsStub) GetSmartSchedulerCapacityLimitedCount(_ context.Context, _ int64, _ time.Time, _, _ string) (int64, error) {
	return s.capacityLimited, s.capacityErr
}

func (s *smartSchedulerStatsStub) GetSmartSchedulerAccountCircuitStatsBatch(_ context.Context, accountIDs []int64, _ time.Time) (map[int64]SmartSchedulerErrorStats, error) {
	s.accountErrorCalls++
	result := make(map[int64]SmartSchedulerErrorStats, len(accountIDs))
	for _, accountID := range accountIDs {
		result[accountID] = s.accountErrors[accountID]
	}
	return result, nil
}

func smartSchedulerTestQuality(score int) AccountQualityStats {
	return AccountQualityStats{
		Recent1h: AccountQualityPeriod{
			Last10:      qualityWindowForPreview(score, 10),
			Last100:     qualityWindowForPreview(score, 20),
			WindowHours: 1,
		},
		Last10:      qualityWindowForPreview(score, 10),
		Last100:     qualityWindowForPreview(score, 20),
		WindowHours: 24,
		Activity: AccountQualityActivity{
			State:                  accountQualityActivityActive,
			SuccessfulRequestCount: 20,
		},
	}
}

func smartSchedulerTestAccount(id int64, priority int) *Account {
	return &Account{
		ID:          id,
		Name:        "scheduler-test",
		Platform:    PlatformOpenAI,
		Status:      StatusActive,
		Schedulable: true,
		Priority:    priority,
	}
}

func TestSmartSchedulerOrderCandidatesDisabledDoesNotReadStats(t *testing.T) {
	stats := &smartSchedulerStatsStub{}
	service := &SmartSchedulerPreviewService{dashboardService: stats}
	group := &Group{ID: 7, Platform: PlatformOpenAI}

	ordering, err := service.OrderCandidates(context.Background(), group, "gpt-5", "responses", []*Account{smartSchedulerTestAccount(1, 1)}, time.Now())

	require.NoError(t, err)
	require.False(t, ordering.Active)
	require.Zero(t, stats.qualityCalls)
	require.Zero(t, stats.errorCalls)
}

func TestSmartSchedulerOrderCandidatesAppliesConfiguredRecoveryProbeStateAcrossModels(t *testing.T) {
	stats := &smartSchedulerStatsStub{
		quality: map[int64]AccountQualityStats{
			1: smartSchedulerTestQuality(90),
			2: smartSchedulerTestQuality(95),
		},
	}
	probeRepo := &smartSchedulerRecoveryProbeRepoStub{
		states: map[int64]GroupRecoveryProbeState{
			2: {
				GroupID:             7,
				AccountID:           2,
				Model:               "gpt-5.6-sol",
				Status:              GroupRecoveryProbeStatusFailed,
				ConsecutiveFailures: 4,
			},
		},
	}
	service := &SmartSchedulerPreviewService{
		dashboardService: stats,
		recoveryProbe:    probeRepo,
		randomFloat:      func() float64 { return 0 },
	}
	group := &Group{
		ID:                    7,
		Platform:              PlatformOpenAI,
		SmartSchedulerEnabled: true,
		RecoveryProbeEnabled:  true,
		RecoveryProbeModel:    "gpt-5.6-sol",
	}

	ordering, err := service.OrderCandidates(
		context.Background(),
		group,
		"gpt-5.5",
		"responses",
		[]*Account{smartSchedulerTestAccount(1, 1), smartSchedulerTestAccount(2, 2)},
		time.Now(),
	)

	require.NoError(t, err)
	require.Equal(t, 1, probeRepo.listCalls)
	require.Equal(t, int64(7), probeRepo.lastGroupID)
	require.Equal(t, "gpt-5.6-sol", probeRepo.lastModel)
	require.ElementsMatch(t, []int64{1, 2}, probeRepo.lastAccounts)
	require.Equal(t, []int64{1}, ordering.OrderedAccountIDs)
	failed := ordering.ItemByAccountID[2]
	require.Equal(t, "isolated", failed.Pool)
	require.Equal(t, "recovery_probe_failed", failed.Decision)
	require.False(t, failed.ExplorationCandidate)
}

func TestSmartSchedulerOrderCandidatesRanksEligibleAccountsByScoreAndCachesResult(t *testing.T) {
	stats := &smartSchedulerStatsStub{
		quality: map[int64]AccountQualityStats{
			1: smartSchedulerTestQuality(35),
			2: smartSchedulerTestQuality(95),
		},
		errors: map[int64]SmartSchedulerErrorStats{
			1: {SuccessfulRequestCount: 20},
			2: {SuccessfulRequestCount: 20},
		},
	}
	service := &SmartSchedulerPreviewService{dashboardService: stats}
	group := &Group{ID: 7, Platform: PlatformOpenAI, SmartSchedulerEnabled: true}
	accounts := []*Account{smartSchedulerTestAccount(1, 1), smartSchedulerTestAccount(2, 100)}
	now := time.Now()

	first, err := service.OrderCandidates(context.Background(), group, "", "any", accounts, now)
	require.NoError(t, err)
	require.True(t, first.Active)
	require.Equal(t, 1, first.RankByAccountID[2])
	require.Equal(t, 2, first.RankByAccountID[1])

	second, err := service.OrderCandidates(context.Background(), group, "", "any", accounts, now.Add(time.Second))
	require.NoError(t, err)
	require.Equal(t, first.RankByAccountID, second.RankByAccountID)
	require.Equal(t, 1, stats.qualityCalls)
	require.Equal(t, 1, stats.errorCalls)
}

func TestSmartSchedulerOrderCandidatesReturnsErrorForStatsFailure(t *testing.T) {
	stats := &smartSchedulerStatsStub{qualityErr: errors.New("statistics unavailable")}
	service := &SmartSchedulerPreviewService{dashboardService: stats}
	group := &Group{ID: 7, Platform: PlatformOpenAI, SmartSchedulerEnabled: true}

	ordering, err := service.OrderCandidates(context.Background(), group, "", "any", []*Account{smartSchedulerTestAccount(1, 1)}, time.Now())

	require.ErrorContains(t, err, "statistics unavailable")
	require.Nil(t, ordering)
}

func TestSmartSchedulerOrderCandidatesUsesAccountFallbackWithoutBorrowingActivity(t *testing.T) {
	exactQuality := AccountQualityStats{
		Activity: AccountQualityActivity{State: accountQualityActivityIdle},
	}
	fallbackQuality := smartSchedulerTestQuality(92)
	stats := &smartSchedulerStatsStub{
		qualityByScope: map[string]map[int64]AccountQualityStats{
			"gpt-5|any": {1: exactQuality},
			"|any":      {1: fallbackQuality},
		},
		errors: map[int64]SmartSchedulerErrorStats{1: {}},
	}
	service := &SmartSchedulerPreviewService{dashboardService: stats}
	group := &Group{ID: 7, Platform: PlatformOpenAI, SmartSchedulerEnabled: true}

	ordering, err := service.OrderCandidates(context.Background(), group, "gpt-5", "any", []*Account{smartSchedulerTestAccount(1, 1)}, time.Now())

	require.NoError(t, err)
	require.Equal(t, []string{"gpt-5|any", "|any"}, stats.qualityCallScopes)
	require.Equal(t, []string{"gpt-5|any"}, stats.errorCallScopes)
	require.Equal(t, 1, stats.accountErrorCalls)
	item := ordering.ItemByAccountID[1]
	require.Equal(t, smartSchedulerEvidenceAccount, item.EvidenceScope)
	require.True(t, item.EvidenceFallback)
	require.Equal(t, "warm", item.Pool)
	require.Zero(t, item.Activity.SuccessfulRequestCount)
}

func TestSmartSchedulerOrderCandidatesTripsAccountWideBreakerAcrossModelScopes(t *testing.T) {
	stats := &smartSchedulerStatsStub{
		quality: map[int64]AccountQualityStats{
			1: smartSchedulerTestQuality(95),
			2: smartSchedulerTestQuality(90),
		},
		errorsByScope: map[string]map[int64]SmartSchedulerErrorStats{
			"gpt-5|/v1/responses": {
				1: {SuccessfulRequestCount: 20},
				2: {SuccessfulRequestCount: 20},
			},
		},
		accountErrors: map[int64]SmartSchedulerErrorStats{
			1: {ImmediateProviderTransientCount: smartSchedulerImmediateFailures},
			2: {},
		},
	}
	service := &SmartSchedulerPreviewService{dashboardService: stats}
	group := &Group{ID: 7, Platform: PlatformOpenAI, SmartSchedulerEnabled: true}
	accounts := []*Account{smartSchedulerTestAccount(1, 1), smartSchedulerTestAccount(2, 2)}

	ordering, err := service.OrderCandidates(context.Background(), group, "gpt-5", "responses", accounts, time.Now())

	require.NoError(t, err)
	require.Equal(t, []string{"gpt-5|/v1/responses"}, stats.errorCallScopes)
	require.Equal(t, 1, stats.accountErrorCalls)
	require.Equal(t, "isolated", ordering.ItemByAccountID[1].Pool)
	require.True(t, ordering.ItemByAccountID[1].SoftIsolation)
	require.NotContains(t, ordering.RankByAccountID, int64(1))
	require.Equal(t, 1, ordering.RankByAccountID[2])
}

func TestSmartSchedulerOrderCandidatesKeepsRateLimitBreakerHard(t *testing.T) {
	stats := &smartSchedulerStatsStub{
		quality: map[int64]AccountQualityStats{1: smartSchedulerTestQuality(95)},
		errorsByScope: map[string]map[int64]SmartSchedulerErrorStats{
			"gpt-5|/v1/responses": {1: {SuccessfulRequestCount: 20}},
		},
		accountErrors: map[int64]SmartSchedulerErrorStats{1: {ImmediateRateLimitCount: smartSchedulerImmediateFailures}},
	}
	service := &SmartSchedulerPreviewService{dashboardService: stats}
	group := &Group{ID: 7, Platform: PlatformOpenAI, SmartSchedulerEnabled: true}

	ordering, err := service.OrderCandidates(context.Background(), group, "gpt-5", "responses", []*Account{smartSchedulerTestAccount(1, 1)}, time.Now())

	require.NoError(t, err)
	require.Equal(t, "isolated", ordering.ItemByAccountID[1].Pool)
	require.False(t, ordering.ItemByAccountID[1].SoftIsolation)
	require.Empty(t, ordering.RankByAccountID)
}

func TestSmartSchedulerRecentSupplierFailuresIncludesUncertainFailures(t *testing.T) {
	stats := SmartSchedulerErrorStats{
		RecentProviderFailureCount:   1,
		RecentProviderTransientCount: 2,
		RecentRateLimitCount:         3,
		RecentUncertainFailureCount:  4,
	}

	require.EqualValues(t, 10, smartSchedulerRecentSupplierFailures(stats))
}

func TestApplySmartSchedulerOrderingFiltersIsolatedAndUsesRank(t *testing.T) {
	accounts := []*Account{
		smartSchedulerTestAccount(1, 1),
		smartSchedulerTestAccount(2, 100),
		smartSchedulerTestAccount(3, 0),
	}
	ordering := &SmartSchedulerOrdering{
		Active:          true,
		RankByAccountID: map[int64]int{1: 2, 2: 1},
	}

	ordered := applySmartSchedulerOrderingToAccounts(accounts, ordering)

	require.Equal(t, []int64{2, 1}, []int64{ordered[0].ID, ordered[1].ID})
	require.Equal(t, -1, smartSchedulerRankCompare(ordering, 2, 1))
	require.Equal(t, 1, smartSchedulerRankCompare(ordering, 1, 2))
}

func TestApplySmartSchedulerOrderingLeavesLegacyCandidatesUntouchedWhenInactive(t *testing.T) {
	accounts := []*Account{smartSchedulerTestAccount(1, 10), smartSchedulerTestAccount(2, 1)}

	ordered := applySmartSchedulerOrderingToAccounts(accounts, &SmartSchedulerOrdering{Active: false})

	require.Equal(t, accounts, ordered)
}

func TestApplySmartSchedulerOrderingRecoversOneLeastBadSoftIsolatedCandidate(t *testing.T) {
	accounts := []*Account{smartSchedulerTestAccount(1, 1), smartSchedulerTestAccount(2, 2)}
	ordering := &SmartSchedulerOrdering{
		Active:          true,
		RankByAccountID: map[int64]int{},
		ItemByAccountID: map[int64]SmartSchedulerPreviewItem{
			1: {AccountID: 1, Pool: "isolated", SoftIsolation: true, Schedulable: true, ModelSupported: true, EndpointSupported: true, ImmediateProviderTransientCount: 5, CostMultiplier: 0.04},
			2: {AccountID: 2, Pool: "isolated", SoftIsolation: true, Schedulable: true, ModelSupported: true, EndpointSupported: true, ImmediateProviderTransientCount: 3, CostMultiplier: 0.08},
		},
	}

	ordered := applySmartSchedulerOrderingToAccounts(accounts, ordering)

	require.Len(t, ordered, 1)
	require.Equal(t, int64(2), ordered[0].ID)
}

func TestApplySmartSchedulerOrderingDoesNotRecoverHardIsolatedCandidate(t *testing.T) {
	accounts := []*Account{smartSchedulerTestAccount(1, 1)}
	ordering := &SmartSchedulerOrdering{
		Active:          true,
		RankByAccountID: map[int64]int{},
		ItemByAccountID: map[int64]SmartSchedulerPreviewItem{
			1: {AccountID: 1, Pool: "isolated", SoftIsolation: false, Schedulable: true, ModelSupported: true, EndpointSupported: true, ImmediateRateLimitCount: 5},
		},
	}

	ordered := applySmartSchedulerOrderingToAccounts(accounts, ordering)

	require.Empty(t, ordered)
}

func TestApplySmartSchedulerOrderingRecoversOnlyLeastBadRecoveryProbeCandidateWhenPoolIsEmpty(t *testing.T) {
	older := time.Date(2026, 8, 13, 1, 0, 0, 0, time.UTC)
	newer := older.Add(time.Hour)
	accounts := []*Account{
		smartSchedulerTestAccount(1, 1),
		smartSchedulerTestAccount(2, 2),
		smartSchedulerTestAccount(3, 3),
	}
	ordering := &SmartSchedulerOrdering{
		Active:          true,
		RankByAccountID: map[int64]int{},
		ItemByAccountID: map[int64]SmartSchedulerPreviewItem{
			1: {
				AccountID:         1,
				Pool:              "isolated",
				Schedulable:       true,
				ModelSupported:    true,
				EndpointSupported: true,
				CostMultiplier:    0.03,
				RecoveryProbe: &GroupRecoveryProbeState{
					Status:              GroupRecoveryProbeStatusFailed,
					LastErrorClass:      GroupRecoveryProbeErrorTransient,
					ConsecutiveFailures: 4,
					LastProbeAt:         &newer,
				},
			},
			2: {
				AccountID:                     2,
				Pool:                          "isolated",
				Schedulable:                   true,
				ModelSupported:                true,
				EndpointSupported:             true,
				ImmediateProviderFailureCount: 1,
				CostMultiplier:                0.08,
				RecoveryProbe: &GroupRecoveryProbeState{
					Status:              GroupRecoveryProbeStatusFailed,
					LastErrorClass:      GroupRecoveryProbeErrorTransient,
					ConsecutiveFailures: 2,
					LastProbeAt:         &older,
				},
			},
			3: {
				AccountID:         3,
				Pool:              "isolated",
				Schedulable:       true,
				ModelSupported:    true,
				EndpointSupported: true,
				CostMultiplier:    0.04,
				RecoveryProbe: &GroupRecoveryProbeState{
					Status:              GroupRecoveryProbeStatusFailed,
					LastErrorClass:      GroupRecoveryProbeErrorTransient,
					ConsecutiveFailures: 2,
					LastProbeAt:         &newer,
				},
			},
		},
	}

	ordered := applySmartSchedulerOrderingToAccounts(accounts, ordering)

	require.Len(t, ordered, 1)
	require.Equal(t, int64(3), ordered[0].ID)
}

func TestApplySmartSchedulerOrderingDoesNotRecoverOrdinaryHardIsolationAlongsideProbeIsolation(t *testing.T) {
	accounts := []*Account{smartSchedulerTestAccount(1, 1), smartSchedulerTestAccount(2, 2)}
	ordering := &SmartSchedulerOrdering{
		Active:          true,
		RankByAccountID: map[int64]int{},
		ItemByAccountID: map[int64]SmartSchedulerPreviewItem{
			1: {
				AccountID:         1,
				Pool:              "isolated",
				Schedulable:       true,
				ModelSupported:    true,
				EndpointSupported: true,
			},
			2: {
				AccountID:         2,
				Pool:              "isolated",
				Schedulable:       true,
				ModelSupported:    true,
				EndpointSupported: true,
				RecoveryProbe: &GroupRecoveryProbeState{
					Status:              GroupRecoveryProbeStatusFailed,
					LastErrorClass:      GroupRecoveryProbeErrorTransient,
					ConsecutiveFailures: 3,
				},
			},
		},
	}

	ordered := applySmartSchedulerOrderingToAccounts(accounts, ordering)

	require.Len(t, ordered, 1)
	require.Equal(t, int64(2), ordered[0].ID)
}

func TestApplySmartSchedulerOrderingDoesNotRecoverPausedOrPermanentProbeIsolation(t *testing.T) {
	accounts := []*Account{smartSchedulerTestAccount(1, 1), smartSchedulerTestAccount(2, 2)}
	ordering := &SmartSchedulerOrdering{
		Active:          true,
		RankByAccountID: map[int64]int{},
		ItemByAccountID: map[int64]SmartSchedulerPreviewItem{
			1: {
				AccountID:         1,
				Pool:              "isolated",
				Schedulable:       true,
				ModelSupported:    true,
				EndpointSupported: true,
				RecoveryProbe: &GroupRecoveryProbeState{
					Status:         GroupRecoveryProbeStatusPaused,
					LastErrorClass: GroupRecoveryProbeErrorPermanent,
				},
			},
			2: {
				AccountID:         2,
				Pool:              "isolated",
				Schedulable:       true,
				ModelSupported:    true,
				EndpointSupported: true,
				RecoveryProbe: &GroupRecoveryProbeState{
					Status:         GroupRecoveryProbeStatusFailed,
					LastErrorClass: GroupRecoveryProbeErrorPermanent,
				},
			},
		},
	}

	ordered := applySmartSchedulerOrderingToAccounts(accounts, ordering)

	require.Empty(t, ordered)
}

func TestSmartSchedulerRecoveryProbeFallbackRequiresExplicitTransientFailure(t *testing.T) {
	tests := []struct {
		name       string
		status     string
		errorClass string
	}{
		{name: "probing transient", status: GroupRecoveryProbeStatusProbing, errorClass: GroupRecoveryProbeErrorTransient},
		{name: "paused transient", status: GroupRecoveryProbeStatusPaused, errorClass: GroupRecoveryProbeErrorTransient},
		{name: "failed permanent", status: GroupRecoveryProbeStatusFailed, errorClass: GroupRecoveryProbeErrorPermanent},
		{name: "failed empty", status: GroupRecoveryProbeStatusFailed},
		{name: "failed unknown", status: GroupRecoveryProbeStatusFailed, errorClass: "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := SmartSchedulerPreviewItem{
				Pool:              "isolated",
				Schedulable:       true,
				ModelSupported:    true,
				EndpointSupported: true,
				RecoveryProbe: &GroupRecoveryProbeState{
					Status:         tt.status,
					LastErrorClass: tt.errorClass,
				},
			}

			require.False(t, smartSchedulerRecoveryProbeFallbackEligible(item))
		})
	}
}

func TestSmartSchedulerInvalidateOrderingCacheForcesRecoveryProbeStateReload(t *testing.T) {
	now := time.Now()
	group := &Group{
		ID:                    7,
		Platform:              PlatformOpenAI,
		SmartSchedulerEnabled: true,
		RecoveryProbeEnabled:  true,
		RecoveryProbeModel:    "gpt-5.6-sol",
	}
	accounts := []*Account{smartSchedulerTestAccount(1, 1)}
	cacheKey := smartSchedulerOrderingCacheKey(group.ID, "gpt-5.5", "responses", accounts)
	probeRepo := &smartSchedulerRecoveryProbeRepoStub{
		states: map[int64]GroupRecoveryProbeState{
			1: {AccountID: 1, Status: GroupRecoveryProbeStatusFailed, Model: "gpt-5.6-sol"},
		},
	}
	service := &SmartSchedulerPreviewService{
		dashboardService: &smartSchedulerStatsStub{quality: map[int64]AccountQualityStats{1: smartSchedulerTestQuality(90)}},
		recoveryProbe:    probeRepo,
		orderingCache: map[string]smartSchedulerOrderingCacheEntry{
			cacheKey: {
				expiresAt: now.Add(time.Minute),
				ordering: &SmartSchedulerOrdering{
					Active:            true,
					RankByAccountID:   map[int64]int{1: 1},
					ItemByAccountID:   map[int64]SmartSchedulerPreviewItem{1: {AccountID: 1, Pool: "primary"}},
					OrderedAccountIDs: []int64{1},
				},
			},
		},
	}

	service.InvalidateOrderingCache()
	ordering, err := service.OrderCandidates(context.Background(), group, "gpt-5.5", "responses", accounts, now)

	require.NoError(t, err)
	require.Equal(t, 1, probeRepo.listCalls)
	require.Empty(t, ordering.OrderedAccountIDs)
	require.Equal(t, "recovery_probe_failed", ordering.ItemByAccountID[1].Decision)
}

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

func TestSmartSchedulerPreviewShowsDynamicCapabilityCooldownAsRecoverable(t *testing.T) {
	account := &Account{
		ID:          71,
		Name:        "luna-dynamic",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: map[string]any{"model_mapping": map[string]any{"gpt-5.6-luna": "luna-upstream"}},
		Extra: map[string]any{
			modelRateLimitsKey: map[string]any{
				dynamicModelCapabilityRateLimitKey("responses", "luna-upstream"): map[string]any{
					"rate_limit_reset_at": time.Now().Add(time.Hour).Format(time.RFC3339),
				},
			},
		},
	}
	group := &Group{ID: 2, Platform: PlatformOpenAI}

	item := buildSmartSchedulerPreviewItem(account, group, "gpt-5.6-luna", "responses", AccountQualityStats{}, SmartSchedulerErrorStats{}, nil)

	require.Equal(t, "isolated", item.Pool)
	require.False(t, item.ModelSupported)
	require.Equal(t, "模型能力暂时未验证，冷却后自动重试", item.Reason)
	require.Equal(t, "luna-upstream", item.ModelMapping)
}

func TestSmartSchedulerPreviewUsesV5AlgorithmVersion(t *testing.T) {
	require.Equal(t, "preview-v5", SmartSchedulerPreviewAlgorithmVersion)
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

func TestSmartSchedulerPreviewDemotesAccountAfterImmediateSupplierFailures(t *testing.T) {
	perfect := 100
	account := &Account{ID: 10, Name: "flapping", Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true}
	group := &Group{ID: 2, Platform: PlatformOpenAI}
	quality := AccountQualityStats{
		Recent1h: AccountQualityPeriod{Last10: qualityWindowForPreview(perfect, 10)},
		Last10:   qualityWindowForPreview(perfect, 10),
		Activity: AccountQualityActivity{State: accountQualityActivityActive, SuccessfulRequestCount: 10},
	}
	errors := SmartSchedulerErrorStats{ImmediateRateLimitCount: 3}

	item := buildSmartSchedulerPreviewItem(account, group, "gpt-5", "responses", quality, errors, nil)
	require.Equal(t, "warm", item.Pool)
	require.Equal(t, "近5分钟上游连续失败，临时降级观察", item.Reason)
}

func TestSmartSchedulerPreviewDemotesAccountAfterImmediateUncertainFailures(t *testing.T) {
	perfect := 100
	account := &Account{ID: 10, Name: "uncertain", Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true}
	group := &Group{ID: 2, Platform: PlatformOpenAI}
	quality := AccountQualityStats{
		Recent1h: AccountQualityPeriod{Last10: qualityWindowForPreview(perfect, 10)},
		Last10:   qualityWindowForPreview(perfect, 10),
		Activity: AccountQualityActivity{State: accountQualityActivityActive, SuccessfulRequestCount: 10},
	}
	errors := SmartSchedulerErrorStats{ImmediateUncertainFailureCount: 3}

	item := buildSmartSchedulerPreviewItem(account, group, "gpt-5", "responses", quality, errors, nil)
	require.Equal(t, "warm", item.Pool)
	require.Equal(t, "近5分钟上游连续失败，临时降级观察", item.Reason)
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

func TestSmartSchedulerSortPrefersCheaperAccountInsideScoreTolerance(t *testing.T) {
	expensive := SmartSchedulerPreviewItem{AccountID: 1, Pool: "primary", Score: previewFloat64Ptr(80), CostMultiplier: 0.08}
	cheap := SmartSchedulerPreviewItem{AccountID: 2, Pool: "primary", Score: previewFloat64Ptr(78), CostMultiplier: 0.04}
	items := []SmartSchedulerPreviewItem{expensive, cheap}

	sortSmartSchedulerItems(items)
	require.Equal(t, []int64{2, 1}, []int64{items[0].AccountID, items[1].AccountID})

	expensive.Score = previewFloat64Ptr(83)
	items = []SmartSchedulerPreviewItem{cheap, expensive}
	sortSmartSchedulerItems(items)
	require.Equal(t, []int64{1, 2}, []int64{items[0].AccountID, items[1].AccountID})
}

func TestSmartSchedulerSortKeepsCostToleranceOrderingDeterministic(t *testing.T) {
	base := []SmartSchedulerPreviewItem{
		{AccountID: 1, Pool: "primary", Score: previewFloat64Ptr(80), CostMultiplier: 0.10},
		{AccountID: 2, Pool: "primary", Score: previewFloat64Ptr(78), CostMultiplier: 0.02},
		{AccountID: 3, Pool: "primary", Score: previewFloat64Ptr(76), CostMultiplier: 0.01},
	}
	permutations := [][]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}}
	for _, permutation := range permutations {
		items := []SmartSchedulerPreviewItem{base[permutation[0]], base[permutation[1]], base[permutation[2]]}
		sortSmartSchedulerItems(items)
		require.Equal(t, []int64{2, 1, 3}, []int64{items[0].AccountID, items[1].AccountID, items[2].AccountID})
	}
}

func TestSmartSchedulerHysteresisKeepsIncumbentUntilQualityLeadIsMeaningful(t *testing.T) {
	service := &SmartSchedulerPreviewService{}
	now := time.Now()
	items := []SmartSchedulerPreviewItem{
		{AccountID: 1, Pool: "primary", Score: previewFloat64Ptr(80), CostMultiplier: 0.05},
		{AccountID: 2, Pool: "primary", Score: previewFloat64Ptr(78), CostMultiplier: 0.05},
	}
	sortSmartSchedulerItems(items)
	service.applySmartSchedulerHysteresis("group|model", items, now)
	require.Equal(t, int64(1), items[0].AccountID)

	items[0].Score = previewFloat64Ptr(80)
	items[1].Score = previewFloat64Ptr(82)
	sortSmartSchedulerItems(items)
	service.applySmartSchedulerHysteresis("group|model", items, now.Add(time.Minute))
	require.Equal(t, int64(1), items[0].AccountID)

	for i := range items {
		if items[i].AccountID == 2 {
			items[i].Score = previewFloat64Ptr(85)
		}
	}
	sortSmartSchedulerItems(items)
	service.applySmartSchedulerHysteresis("group|model", items, now.Add(2*time.Minute))
	require.Equal(t, int64(2), items[0].AccountID)
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

	geminiScopes := smartSchedulerQualityScopes("gemini-2.5-pro", "gemini_models")
	require.Equal(t, "/v1beta/models", geminiScopes[0].Endpoint)
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

func TestSmartSchedulerExplorationPreviewBoostsProbeBootstrapCandidates(t *testing.T) {
	items := []SmartSchedulerPreviewItem{
		{AccountID: 1, Pool: "primary", Schedulable: true, ModelSupported: true, EndpointSupported: true},
		{AccountID: 2, Pool: "warm", Schedulable: true, ModelSupported: true, EndpointSupported: true},
		{AccountID: 3, Pool: "warm", Schedulable: true, ModelSupported: true, EndpointSupported: true, ProbeBootstrap: true},
	}

	rate := applySmartSchedulerExplorationPreview(items)
	require.InDelta(t, smartSchedulerProbeBootstrapExplorationRate, rate, 0.0001)
	require.True(t, items[1].ExplorationCandidate)
	require.True(t, items[2].ExplorationCandidate)
}

func TestSmartSchedulerExplorationRotatesAcrossEligibleWarmAccounts(t *testing.T) {
	service := &SmartSchedulerPreviewService{randomFloat: func() float64 { return 0 }}
	ordering := &SmartSchedulerOrdering{
		Active:            true,
		ExplorationRate:   0.1,
		OrderedAccountIDs: []int64{1, 2, 3},
		RankByAccountID:   map[int64]int{1: 1, 2: 2, 3: 3},
		ItemByAccountID: map[int64]SmartSchedulerPreviewItem{
			1: {AccountID: 1, Pool: "primary"},
			2: {AccountID: 2, Pool: "warm", ExplorationCandidate: true},
			3: {AccountID: 3, Pool: "warm", ExplorationCandidate: true},
		},
	}

	first := service.applySmartSchedulerExploration(cloneSmartSchedulerOrdering(ordering), "group|model", time.Now())
	second := service.applySmartSchedulerExploration(cloneSmartSchedulerOrdering(ordering), "group|model", time.Now().Add(time.Second))

	require.Equal(t, int64(2), first.OrderedAccountIDs[0])
	require.Equal(t, int64(3), second.OrderedAccountIDs[0])
}

func TestSmartSchedulerExplorationPrioritizesProbeBootstrapCandidates(t *testing.T) {
	service := &SmartSchedulerPreviewService{randomFloat: func() float64 { return 0 }}
	ordering := &SmartSchedulerOrdering{
		Active:            true,
		ExplorationRate:   smartSchedulerProbeBootstrapExplorationRate,
		OrderedAccountIDs: []int64{1, 2, 3},
		RankByAccountID:   map[int64]int{1: 1, 2: 2, 3: 3},
		ItemByAccountID: map[int64]SmartSchedulerPreviewItem{
			1: {AccountID: 1, Pool: "primary"},
			2: {AccountID: 2, Pool: "warm", ExplorationCandidate: true},
			3: {AccountID: 3, Pool: "warm", ExplorationCandidate: true, ProbeBootstrap: true},
		},
	}

	result := service.applySmartSchedulerExploration(ordering, "group|model", time.Now())

	require.True(t, result.Exploration)
	require.Equal(t, int64(3), result.OrderedAccountIDs[0])
}

func TestSmartSchedulerStableOrderingContextSuppressesExploration(t *testing.T) {
	now := time.Now()
	group := &Group{ID: 7, Platform: PlatformOpenAI, SmartSchedulerEnabled: true}
	accounts := []*Account{{ID: 1}, {ID: 2}}
	cacheKey := smartSchedulerOrderingCacheKey(group.ID, "gpt-test", "responses", accounts)
	base := &SmartSchedulerOrdering{
		Active:            true,
		ExplorationRate:   0.1,
		OrderedAccountIDs: []int64{1, 2},
		RankByAccountID:   map[int64]int{1: 1, 2: 2},
		ItemByAccountID: map[int64]SmartSchedulerPreviewItem{
			1: {AccountID: 1, Pool: "primary"},
			2: {AccountID: 2, Pool: "warm", ExplorationCandidate: true},
		},
	}
	service := &SmartSchedulerPreviewService{
		dashboardService: &smartSchedulerStatsStub{},
		orderingCache: map[string]smartSchedulerOrderingCacheEntry{
			cacheKey: {expiresAt: now.Add(time.Minute), ordering: base},
		},
		randomFloat: func() float64 { return 0 },
	}

	stable, err := service.OrderCandidates(
		withSmartSchedulerStableOrdering(context.Background()),
		group,
		"gpt-test",
		"responses",
		accounts,
		now,
	)

	require.NoError(t, err)
	require.False(t, stable.Exploration)
	require.Equal(t, []int64{1, 2}, stable.OrderedAccountIDs)
}

func TestSmartSchedulerPreviewReportsGroupCapacityPressure(t *testing.T) {
	stats := &smartSchedulerStatsStub{capacityLimited: 7}
	service := &SmartSchedulerPreviewService{
		adminService:     &smartSchedulerAdminServiceStub{group: Group{ID: 7, Platform: PlatformOpenAI, SmartSchedulerEnabled: true}},
		dashboardService: stats,
	}

	preview, err := service.Preview(context.Background(), 7, "gpt-5", "responses", time.Now())
	require.NoError(t, err)
	require.Equal(t, int64(7), preview.CapacityLimitedCount1h)
	require.Empty(t, preview.Warnings)
}

func previewFloat64Ptr(value float64) *float64 { return &value }
func previewIntPtr(value int) *int             { return &value }
