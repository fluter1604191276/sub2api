//go:build unit

package service

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type staticSmartSchedulerOrderer struct {
	ranks        map[int64]int
	isolated     map[int64]bool
	softIsolated map[int64]int64
	scores       map[int64]float64
	costs        map[int64]float64
	pools        map[int64]string
	err          error
	calls        int
	endpoint     string
}

func (s *staticSmartSchedulerOrderer) OrderCandidates(_ context.Context, _ *Group, _, endpoint string, accounts []*Account, _ time.Time) (*SmartSchedulerOrdering, error) {
	s.calls++
	s.endpoint = endpoint
	if s.err != nil {
		return nil, s.err
	}
	ordering := &SmartSchedulerOrdering{
		Active:            true,
		AlgorithmVersion:  "test",
		RankByAccountID:   make(map[int64]int, len(s.ranks)),
		ItemByAccountID:   make(map[int64]SmartSchedulerPreviewItem, len(s.ranks)),
		OrderedAccountIDs: make([]int64, 0, len(s.ranks)),
	}
	for accountID, rank := range s.ranks {
		score := float64(100 - rank)
		if configured, ok := s.scores[accountID]; ok {
			score = configured
		}
		pool := "primary"
		if configured, ok := s.pools[accountID]; ok {
			pool = configured
		}
		ordering.RankByAccountID[accountID] = rank
		ordering.ItemByAccountID[accountID] = SmartSchedulerPreviewItem{
			AccountID:      accountID,
			Pool:           pool,
			Score:          previewFloat64Ptr(score),
			CostMultiplier: s.costs[accountID],
		}
	}
	for accountID := range s.isolated {
		ordering.ItemByAccountID[accountID] = SmartSchedulerPreviewItem{AccountID: accountID, Pool: "isolated"}
	}
	for accountID, failureCount := range s.softIsolated {
		ordering.ItemByAccountID[accountID] = SmartSchedulerPreviewItem{
			AccountID:                       accountID,
			Pool:                            "isolated",
			SoftIsolation:                   true,
			SoftIsolationFailureCount:       failureCount,
			Schedulable:                     true,
			ModelSupported:                  true,
			EndpointSupported:               true,
			ImmediateProviderTransientCount: failureCount,
		}
	}
	for rank := 1; rank <= len(accounts); rank++ {
		for accountID, accountRank := range s.ranks {
			if accountRank == rank {
				ordering.OrderedAccountIDs = append(ordering.OrderedAccountIDs, accountID)
			}
		}
	}
	return ordering, nil
}

func TestGatewaySmartSchedulerUsesEndpointFromRequestContext(t *testing.T) {
	groupID := int64(77)
	accounts, byID := smartSchedulerRoutingAccounts(groupID)
	orderer := &staticSmartSchedulerOrderer{ranks: map[int64]int{2: 1, 1: 2}}
	svc := &GatewayService{
		accountRepo:    &mockAccountRepoForPlatform{accounts: accounts, accountsByID: byID},
		groupRepo:      &mockGroupRepoForGateway{groups: map[int64]*Group{groupID: {ID: groupID, Platform: PlatformAnthropic, SmartSchedulerEnabled: true}}},
		cache:          &mockGatewayCacheForPlatform{},
		cfg:            testConfig(),
		smartScheduler: orderer,
	}

	ctx := WithSmartSchedulerEndpoint(context.Background(), "responses")
	selected, err := svc.selectAccountForModelWithPlatform(ctx, &groupID, "", "", nil, PlatformAnthropic)

	require.NoError(t, err)
	require.Equal(t, int64(2), selected.ID)
	require.Equal(t, "responses", orderer.endpoint)
}

func smartSchedulerRoutingAccounts(groupID int64) ([]Account, map[int64]*Account) {
	accounts := []Account{
		{ID: 1, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true, Priority: 1, Concurrency: 1, AccountGroups: []AccountGroup{{GroupID: groupID}}},
		{ID: 2, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true, Priority: 100, Concurrency: 1, AccountGroups: []AccountGroup{{GroupID: groupID}}},
	}
	byID := make(map[int64]*Account, len(accounts))
	for i := range accounts {
		byID[accounts[i].ID] = &accounts[i]
	}
	return accounts, byID
}

func TestGatewaySmartSchedulerOverridesPriorityAfterAdmission(t *testing.T) {
	groupID := int64(77)
	accounts, byID := smartSchedulerRoutingAccounts(groupID)
	orderer := &staticSmartSchedulerOrderer{ranks: map[int64]int{2: 1, 1: 2}}
	svc := &GatewayService{
		accountRepo:    &mockAccountRepoForPlatform{accounts: accounts, accountsByID: byID},
		groupRepo:      &mockGroupRepoForGateway{groups: map[int64]*Group{groupID: {ID: groupID, Platform: PlatformAnthropic, SmartSchedulerEnabled: true}}},
		cache:          &mockGatewayCacheForPlatform{},
		cfg:            testConfig(),
		smartScheduler: orderer,
	}

	selected, err := svc.selectAccountForModelWithPlatform(context.Background(), &groupID, "", "", nil, PlatformAnthropic)

	require.NoError(t, err)
	require.Equal(t, int64(2), selected.ID)
	require.Equal(t, 1, orderer.calls)
}

func TestGatewaySmartSchedulerOverridesLoadAwarePriorityAndLoad(t *testing.T) {
	groupID := int64(77)
	accounts, byID := smartSchedulerRoutingAccounts(groupID)
	orderer := &staticSmartSchedulerOrderer{ranks: map[int64]int{2: 1, 1: 2}}
	concurrencyCache := &mockConcurrencyCache{
		loadMap: map[int64]*AccountLoadInfo{
			1: {AccountID: 1, LoadRate: 10},
			2: {AccountID: 2, LoadRate: 80},
		},
	}
	cfg := testConfig()
	cfg.Gateway.Scheduling.LoadBatchEnabled = true
	svc := &GatewayService{
		accountRepo:        &mockAccountRepoForPlatform{accounts: accounts, accountsByID: byID},
		groupRepo:          &mockGroupRepoForGateway{groups: map[int64]*Group{groupID: {ID: groupID, Platform: PlatformAnthropic, SmartSchedulerEnabled: true}}},
		cache:              &mockGatewayCacheForPlatform{},
		cfg:                cfg,
		concurrencyService: NewConcurrencyService(concurrencyCache),
		smartScheduler:     orderer,
	}

	selection, err := svc.SelectAccountWithLoadAwareness(context.Background(), &groupID, "", "", nil, "", 0)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, int64(2), selection.Account.ID)
	require.Equal(t, 1, orderer.calls)
	require.Equal(t, 1, concurrencyCache.acquireAccountCalls)
}

func TestGatewaySmartSchedulerFallsBackToPriorityWhenStatsFail(t *testing.T) {
	groupID := int64(77)
	accounts, byID := smartSchedulerRoutingAccounts(groupID)
	orderer := &staticSmartSchedulerOrderer{err: errors.New("stats unavailable")}
	svc := &GatewayService{
		accountRepo:    &mockAccountRepoForPlatform{accounts: accounts, accountsByID: byID},
		groupRepo:      &mockGroupRepoForGateway{groups: map[int64]*Group{groupID: {ID: groupID, Platform: PlatformAnthropic, SmartSchedulerEnabled: true}}},
		cache:          &mockGatewayCacheForPlatform{},
		cfg:            testConfig(),
		smartScheduler: orderer,
	}

	selected, err := svc.selectAccountForModelWithPlatform(context.Background(), &groupID, "", "", nil, PlatformAnthropic)

	require.NoError(t, err)
	require.Equal(t, int64(1), selected.ID)
	require.Equal(t, 1, orderer.calls)
}

func TestGatewaySmartSchedulerDoesNotOverrideStickySession(t *testing.T) {
	groupID := int64(77)
	accounts, byID := smartSchedulerRoutingAccounts(groupID)
	orderer := &staticSmartSchedulerOrderer{ranks: map[int64]int{2: 1, 1: 2}}
	svc := &GatewayService{
		accountRepo: &mockAccountRepoForPlatform{accounts: accounts, accountsByID: byID},
		groupRepo:   &mockGroupRepoForGateway{groups: map[int64]*Group{groupID: {ID: groupID, Platform: PlatformAnthropic, SmartSchedulerEnabled: true}}},
		cache: &mockGatewayCacheForPlatform{
			sessionBindings: map[string]int64{"sticky": 1},
		},
		cfg:            testConfig(),
		smartScheduler: orderer,
	}

	selected, err := svc.selectAccountForModelWithPlatform(context.Background(), &groupID, "sticky", "", nil, PlatformAnthropic)

	require.NoError(t, err)
	require.Equal(t, int64(1), selected.ID)
	require.Zero(t, orderer.calls)
}

func TestOpenAIGatewaySmartSchedulerOverridesPriority(t *testing.T) {
	groupID := int64(88)
	accounts := []Account{
		{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Priority: 1, Concurrency: 1},
		{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Priority: 100, Concurrency: 1},
	}
	orderer := &staticSmartSchedulerOrderer{ranks: map[int64]int{2: 1, 1: 2}}
	svc := &OpenAIGatewayService{
		accountRepo: stubOpenAIAccountRepo{accounts: accounts},
		groupRepo: &mockGroupRepoForGateway{groups: map[int64]*Group{
			groupID: {ID: groupID, Platform: PlatformOpenAI, SmartSchedulerEnabled: true},
		}},
		smartScheduler: orderer,
	}

	selection, err := svc.SelectAccountWithLoadAwareness(context.Background(), &groupID, "", "", nil)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, int64(2), selection.Account.ID)
	require.Equal(t, 1, orderer.calls)
}

func TestOpenAIGatewaySmartSchedulerWeakStickyMovesToBetterAccount(t *testing.T) {
	groupID := int64(188)
	accounts := []Account{
		{ID: 11, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Priority: 1, Concurrency: 1, GroupIDs: []int64{groupID}},
		{ID: 12, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Priority: 100, Concurrency: 1, GroupIDs: []int64{groupID}},
	}
	orderer := &staticSmartSchedulerOrderer{
		ranks:  map[int64]int{12: 1, 11: 2},
		scores: map[int64]float64{11: 64, 12: 82},
		costs:  map[int64]float64{11: 0.08, 12: 0.07},
	}
	cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:weak-sticky": 11}}
	svc := &OpenAIGatewayService{
		accountRepo:    schedulerGroupAwareOpenAIAccountRepo{schedulerTestOpenAIAccountRepo{accounts: accounts}},
		groupRepo:      &mockGroupRepoForGateway{groups: map[int64]*Group{groupID: {ID: groupID, Platform: PlatformOpenAI, SmartSchedulerEnabled: true}}},
		cache:          cache,
		smartScheduler: orderer,
	}

	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })
	ctx := WithSmartSchedulerEndpoint(context.Background(), "responses")

	selection, err := svc.SelectAccountWithLoadAwareness(ctx, &groupID, "weak-sticky", "gpt-test", nil)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, int64(12), selection.Account.ID)
	require.Equal(t, int64(12), cache.sessionBindings["openai:weak-sticky"])
	require.Contains(t, logs.String(), "sticky.smart_scheduler_switch_applied")
	require.Contains(t, logs.String(), "previous_account_id=11")
	require.Contains(t, logs.String(), "account_id=12")
	require.NotContains(t, logs.String(), "weak-sticky")
}

func TestOpenAIGatewaySmartSchedulerAStickyMovesToMateriallyBetterAccount(t *testing.T) {
	groupID := int64(189)
	accounts := []Account{
		{ID: 21, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Priority: 1, Concurrency: 1, GroupIDs: []int64{groupID}},
		{ID: 22, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Priority: 100, Concurrency: 1, GroupIDs: []int64{groupID}},
	}
	orderer := &staticSmartSchedulerOrderer{
		ranks:  map[int64]int{22: 1, 21: 2},
		scores: map[int64]float64{21: 75, 22: 92},
		costs:  map[int64]float64{21: 0.08, 22: 0.07},
	}
	cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:strong-sticky": 21}}
	svc := &OpenAIGatewayService{
		accountRepo:    schedulerGroupAwareOpenAIAccountRepo{schedulerTestOpenAIAccountRepo{accounts: accounts}},
		groupRepo:      &mockGroupRepoForGateway{groups: map[int64]*Group{groupID: {ID: groupID, Platform: PlatformOpenAI, SmartSchedulerEnabled: true}}},
		cache:          cache,
		smartScheduler: orderer,
	}

	ctx := WithSmartSchedulerEndpoint(context.Background(), "responses")
	selection, err := svc.SelectAccountWithLoadAwareness(ctx, &groupID, "strong-sticky", "gpt-test", nil)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, int64(22), selection.Account.ID)
	require.Equal(t, int64(22), cache.sessionBindings["openai:strong-sticky"])
	reviewKey := smartStickyReviewKeyWithContext(ctx, openAISmartStickyReviewRequest{
		GroupID:        &groupID,
		SessionHash:    "strong-sticky",
		Platform:       PlatformOpenAI,
		RequestedModel: "gpt-test",
	})
	require.WithinDuration(t, time.Now().Add(smartStickyStrongSwitchCooldown), svc.smartStickyReviews[reviewKey].cooldownUntil, 2*time.Second)
}

func TestOpenAIGatewaySmartSchedulerSoftIsolatedStickyMovesImmediately(t *testing.T) {
	groupID := int64(190)
	accounts := []Account{
		{ID: 31, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Priority: 1, Concurrency: 1, GroupIDs: []int64{groupID}},
		{ID: 32, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Priority: 100, Concurrency: 1, GroupIDs: []int64{groupID}},
	}
	orderer := &staticSmartSchedulerOrderer{
		ranks:        map[int64]int{32: 1},
		scores:       map[int64]float64{32: 64},
		softIsolated: map[int64]int64{31: 3},
	}
	cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:isolated-sticky": 31}}
	svc := &OpenAIGatewayService{
		accountRepo:    schedulerGroupAwareOpenAIAccountRepo{schedulerTestOpenAIAccountRepo{accounts: accounts}},
		groupRepo:      &mockGroupRepoForGateway{groups: map[int64]*Group{groupID: {ID: groupID, Platform: PlatformOpenAI, SmartSchedulerEnabled: true}}},
		cache:          cache,
		smartScheduler: orderer,
	}

	selection, err := svc.SelectAccountWithLoadAwareness(context.Background(), &groupID, "isolated-sticky", "gpt-test", nil)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, int64(32), selection.Account.ID)
	require.Equal(t, int64(32), cache.sessionBindings["openai:isolated-sticky"])
}

func TestOpenAIAdvancedSchedulerSmartStickyMovesToPreferredAccount(t *testing.T) {
	groupID := int64(191)
	accounts := []Account{
		{ID: 41, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Priority: 1, Concurrency: 1, GroupIDs: []int64{groupID}},
		{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Priority: 100, Concurrency: 1, GroupIDs: []int64{groupID}},
	}
	orderer := &staticSmartSchedulerOrderer{
		ranks:  map[int64]int{42: 1, 41: 2},
		scores: map[int64]float64{41: 60, 42: 85},
		costs:  map[int64]float64{41: 0.08, 42: 0.07},
	}
	cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:advanced-weak-sticky": 41}}
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerGroupAwareOpenAIAccountRepo{schedulerTestOpenAIAccountRepo{accounts: accounts}},
		groupRepo:          &mockGroupRepoForGateway{groups: map[int64]*Group{groupID: {ID: groupID, Platform: PlatformOpenAI, SmartSchedulerEnabled: true}}},
		cache:              cache,
		cfg:                &config.Config{},
		rateLimitService:   newOpenAIAdvancedSchedulerRateLimitService("true"),
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
		smartScheduler:     orderer,
	}

	selection, _, err := svc.SelectAccountWithScheduler(
		context.Background(),
		&groupID,
		"",
		"advanced-weak-sticky",
		"gpt-test",
		nil,
		OpenAIUpstreamTransportAny,
		false,
	)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, int64(42), selection.Account.ID)
	require.Equal(t, int64(42), cache.sessionBindings["openai:advanced-weak-sticky"])
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func TestDecideOpenAISmartStickyReviewExplorationCannotMoveSession(t *testing.T) {
	ordering := &SmartSchedulerOrdering{
		Active:            true,
		Exploration:       true,
		OrderedAccountIDs: []int64{2, 1},
		RankByAccountID:   map[int64]int{2: 1, 1: 2},
		ItemByAccountID: map[int64]SmartSchedulerPreviewItem{
			1: {AccountID: 1, Pool: "primary", Score: previewFloat64Ptr(60)},
			2: {AccountID: 2, Pool: "warm", Score: previewFloat64Ptr(90), ExplorationCandidate: true},
		},
	}

	decision := decideOpenAISmartStickyReview(ordering, 1)

	require.True(t, decision.Reviewed)
	require.False(t, decision.Switch)
	require.Equal(t, "exploration_deferred", decision.Reason)
}

func TestDecideOpenAISmartStickyReviewUsesGradedQualityLeadThresholds(t *testing.T) {
	tests := []struct {
		name            string
		currentScore    float64
		challengerScore float64
		wantSwitch      bool
	}{
		{name: "below_a_minus_switches_at_three_points", currentScore: 68, challengerScore: 71, wantSwitch: true},
		{name: "a_minus_stays_below_six_points", currentScore: 70, challengerScore: 75.99, wantSwitch: false},
		{name: "a_minus_switches_at_six_points", currentScore: 70, challengerScore: 76, wantSwitch: true},
		{name: "s_minus_stays_below_ten_points", currentScore: 85, challengerScore: 94.99, wantSwitch: false},
		{name: "s_minus_can_switch_at_ten_points", currentScore: 85, challengerScore: 95, wantSwitch: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ordering := &SmartSchedulerOrdering{
				Active:            true,
				OrderedAccountIDs: []int64{2, 1},
				RankByAccountID:   map[int64]int{2: 1, 1: 2},
				ItemByAccountID: map[int64]SmartSchedulerPreviewItem{
					1: {AccountID: 1, Pool: "primary", Score: previewFloat64Ptr(tt.currentScore), CostMultiplier: 0.07},
					2: {AccountID: 2, Pool: "primary", Score: previewFloat64Ptr(tt.challengerScore), CostMultiplier: 0.08},
				},
			}

			decision := decideOpenAISmartStickyReview(ordering, 1)

			require.Equal(t, tt.wantSwitch, decision.Switch)
			if tt.wantSwitch {
				require.Equal(t, "better_quality", decision.Reason)
			}
		})
	}
}

func TestDecideOpenAISmartStickyReviewQualityLeadThreshold(t *testing.T) {
	tests := []struct {
		name       string
		challenger float64
		wantSwitch bool
	}{
		{name: "three_point_lead_switches", challenger: 71, wantSwitch: true},
		{name: "sub_three_point_lead_stays", challenger: 70.99, wantSwitch: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ordering := &SmartSchedulerOrdering{
				Active:            true,
				OrderedAccountIDs: []int64{2, 1},
				RankByAccountID:   map[int64]int{2: 1, 1: 2},
				ItemByAccountID: map[int64]SmartSchedulerPreviewItem{
					1: {AccountID: 1, Pool: "primary", Score: previewFloat64Ptr(68), CostMultiplier: 0.07},
					2: {AccountID: 2, Pool: "primary", Score: previewFloat64Ptr(tt.challenger), CostMultiplier: 0.08},
				},
			}

			decision := decideOpenAISmartStickyReview(ordering, 1)

			require.Equal(t, tt.wantSwitch, decision.Switch)
			if tt.wantSwitch {
				require.Equal(t, "better_quality", decision.Reason)
			}
		})
	}
}

func TestDecideOpenAISmartStickyReviewPrimaryPoolCanOverrideWarmBPlus(t *testing.T) {
	ordering := &SmartSchedulerOrdering{
		Active:            true,
		OrderedAccountIDs: []int64{2, 1},
		RankByAccountID:   map[int64]int{2: 1, 1: 2},
		ItemByAccountID: map[int64]SmartSchedulerPreviewItem{
			1: {AccountID: 1, Pool: "warm", Score: previewFloat64Ptr(69)},
			2: {AccountID: 2, Pool: "primary", Score: previewFloat64Ptr(60)},
		},
	}

	decision := decideOpenAISmartStickyReview(ordering, 1)

	require.True(t, decision.Switch)
	require.Equal(t, "primary_over_warm", decision.Reason)
}

func TestDecideOpenAISmartStickyReviewPrimaryPoolCannotBypassStrongQualityThreshold(t *testing.T) {
	ordering := &SmartSchedulerOrdering{
		Active:            true,
		OrderedAccountIDs: []int64{2, 1},
		RankByAccountID:   map[int64]int{2: 1, 1: 2},
		ItemByAccountID: map[int64]SmartSchedulerPreviewItem{
			1: {AccountID: 1, Pool: "warm", Score: previewFloat64Ptr(92), CostMultiplier: 0.07},
			2: {AccountID: 2, Pool: "primary", Score: previewFloat64Ptr(96), CostMultiplier: 0.08},
		},
	}

	decision := decideOpenAISmartStickyReview(ordering, 1)

	require.False(t, decision.Switch)
	require.True(t, decision.Strong)
	require.Equal(t, smartStickyEliteQualityLead, decision.RequiredQualityLead)
}

func TestDecideOpenAISmartStickyReviewIsolatedCurrentMovesImmediately(t *testing.T) {
	ordering := &SmartSchedulerOrdering{
		Active:            true,
		OrderedAccountIDs: []int64{2},
		RankByAccountID:   map[int64]int{2: 1},
		ItemByAccountID: map[int64]SmartSchedulerPreviewItem{
			1: {AccountID: 1, Pool: "isolated", Score: previewFloat64Ptr(95)},
			2: {AccountID: 2, Pool: "warm", Score: previewFloat64Ptr(50)},
		},
	}

	decision := decideOpenAISmartStickyReview(ordering, 1)

	require.True(t, decision.Switch)
	require.Equal(t, "current_isolated", decision.Reason)
}

func TestOpenAISmartStickyReviewEliteSwitchRequiresTwoConfirmations(t *testing.T) {
	ordering := &SmartSchedulerOrdering{
		Active:            true,
		OrderedAccountIDs: []int64{2, 1},
		RankByAccountID:   map[int64]int{2: 1, 1: 2},
		ItemByAccountID: map[int64]SmartSchedulerPreviewItem{
			1: {AccountID: 1, Pool: "primary", Score: previewFloat64Ptr(85), CostMultiplier: 0.07},
			2: {AccountID: 2, Pool: "primary", Score: previewFloat64Ptr(95), CostMultiplier: 0.08},
		},
	}
	baseDecision := decideOpenAISmartStickyReview(ordering, 1)
	require.True(t, baseDecision.Switch)

	svc := &OpenAIGatewayService{}
	now := time.Now()
	first := svc.applySmartStickyReviewState("elite", now, baseDecision)
	require.False(t, first.Switch)
	require.True(t, first.ConfirmationPending)
	require.Equal(t, 1, first.ConfirmationCount)
	require.Equal(t, "switch_confirmation_pending", first.Reason)

	svc.finishSmartStickyReview("elite", now, smartStickyWeakReviewInterval)
	secondReviewAt := now.Add(smartStickyWeakReviewInterval + time.Second)
	require.True(t, svc.claimSmartStickyReview("elite", secondReviewAt))
	second := svc.applySmartStickyReviewState("elite", secondReviewAt, baseDecision)
	require.True(t, second.Switch)
	require.False(t, second.ConfirmationPending)
	require.Equal(t, 2, second.ConfirmationCount)
	require.Equal(t, "better_quality", second.Reason)
}

func TestOpenAISmartStickyReviewCooldownPreventsImmediateReversal(t *testing.T) {
	ordering := &SmartSchedulerOrdering{
		Active:            true,
		OrderedAccountIDs: []int64{1, 2},
		RankByAccountID:   map[int64]int{1: 1, 2: 2},
		ItemByAccountID: map[int64]SmartSchedulerPreviewItem{
			1: {AccountID: 1, Pool: "primary", Score: previewFloat64Ptr(90), CostMultiplier: 0.08},
			2: {AccountID: 2, Pool: "primary", Score: previewFloat64Ptr(70), CostMultiplier: 0.07},
		},
	}
	baseDecision := decideOpenAISmartStickyReview(ordering, 2)
	require.True(t, baseDecision.Switch)

	svc := &OpenAIGatewayService{}
	now := time.Now()
	svc.markSmartStickySwitchApplied("cooldown", 2, now, baseDecision.ChallengerScore)
	secondReviewAt := now.Add(smartStickyWeakReviewInterval + time.Second)
	require.True(t, svc.claimSmartStickyReview("cooldown", secondReviewAt))
	decision := svc.applySmartStickyReviewState("cooldown", secondReviewAt, baseDecision)

	require.False(t, decision.Switch)
	require.True(t, decision.Cooldown)
	require.Equal(t, "better_quality", decision.ProposedReason)
	require.Equal(t, "switch_cooldown", decision.Reason)
	require.Greater(t, decision.CooldownRemaining, 8*time.Minute)
}

func TestOpenAISmartStickyReviewCooldownDoesNotBlockIsolationEscape(t *testing.T) {
	ordering := &SmartSchedulerOrdering{
		Active:            true,
		OrderedAccountIDs: []int64{1},
		RankByAccountID:   map[int64]int{1: 1},
		ItemByAccountID: map[int64]SmartSchedulerPreviewItem{
			1: {AccountID: 1, Pool: "warm", Score: previewFloat64Ptr(60)},
			2: {AccountID: 2, Pool: "isolated", Score: previewFloat64Ptr(95)},
		},
	}
	baseDecision := decideOpenAISmartStickyReview(ordering, 2)
	require.True(t, baseDecision.Switch)
	require.Equal(t, "current_isolated", baseDecision.Reason)

	svc := &OpenAIGatewayService{}
	now := time.Now()
	svc.markSmartStickySwitchApplied("isolated-cooldown", 2, now)
	decision := svc.applySmartStickyReviewState("isolated-cooldown", now.Add(time.Minute), baseDecision)

	require.True(t, decision.Switch)
	require.False(t, decision.Cooldown)
	require.Equal(t, "current_isolated", decision.Reason)
}

func TestOpenAISmartStickyReviewLogsNoteworthyRetentionWithoutRawSession(t *testing.T) {
	groupID := int64(193)
	accounts := []Account{
		{ID: 51, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Priority: 1, Concurrency: 1, GroupIDs: []int64{groupID}},
		{ID: 52, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Priority: 100, Concurrency: 1, GroupIDs: []int64{groupID}},
	}
	orderer := &staticSmartSchedulerOrderer{
		ranks:  map[int64]int{52: 1, 51: 2},
		scores: map[int64]float64{51: 70, 52: 75.5},
		costs:  map[int64]float64{51: 0.07, 52: 0.08},
	}
	const sessionHash = "private-retained-session"
	cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:" + sessionHash: 51}}
	svc := &OpenAIGatewayService{
		accountRepo:    schedulerGroupAwareOpenAIAccountRepo{schedulerTestOpenAIAccountRepo{accounts: accounts}},
		groupRepo:      &mockGroupRepoForGateway{groups: map[int64]*Group{groupID: {ID: groupID, Platform: PlatformOpenAI, SmartSchedulerEnabled: true}}},
		cache:          cache,
		smartScheduler: orderer,
	}

	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })
	ctx := WithSmartSchedulerEndpoint(context.Background(), "responses")

	selection, err := svc.SelectAccountWithLoadAwareness(ctx, &groupID, sessionHash, "gpt-test", nil)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, int64(51), selection.Account.ID)
	require.Contains(t, logs.String(), "sticky.smart_scheduler_kept")
	require.Contains(t, logs.String(), "session_fingerprint="+smartStickySessionFingerprint(smartStickyReviewKeyWithContext(ctx, openAISmartStickyReviewRequest{
		GroupID:        &groupID,
		SessionHash:    sessionHash,
		Platform:       PlatformOpenAI,
		RequestedModel: "gpt-test",
	})))
	require.NotContains(t, logs.String(), sessionHash)
}

func TestSmartStickyReviewKeySeparatesCandidateRequestShapes(t *testing.T) {
	groupID := int64(194)
	ctx := WithSmartSchedulerEndpoint(context.Background(), "responses")
	base := openAISmartStickyReviewRequest{
		GroupID:        &groupID,
		SessionHash:    "hashed-session",
		Platform:       PlatformOpenAI,
		RequestedModel: "gpt-test",
	}
	baseKey := smartStickyReviewKeyWithContext(ctx, base)
	require.NotEmpty(t, baseKey)

	tests := []struct {
		name   string
		mutate func(*openAISmartStickyReviewRequest)
	}{
		{name: "platform", mutate: func(req *openAISmartStickyReviewRequest) { req.Platform = PlatformGrok }},
		{name: "compact", mutate: func(req *openAISmartStickyReviewRequest) { req.RequireCompact = true }},
		{name: "transport", mutate: func(req *openAISmartStickyReviewRequest) {
			req.RequiredTransport = OpenAIUpstreamTransportResponsesWebsocketV2
		}},
		{name: "endpoint_capability", mutate: func(req *openAISmartStickyReviewRequest) { req.RequiredCapability = OpenAIEndpointCapabilityEmbeddings }},
		{name: "image_capability", mutate: func(req *openAISmartStickyReviewRequest) { req.RequiredImageCapability = OpenAIImagesCapabilityNative }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changed := base
			tt.mutate(&changed)
			require.NotEqual(t, baseKey, smartStickyReviewKeyWithContext(ctx, changed))
		})
	}
}

func TestOpenAISmartStickyReviewSkipsChannelMonitorProbe(t *testing.T) {
	groupID := int64(192)
	orderer := &staticSmartSchedulerOrderer{ranks: map[int64]int{2: 1, 1: 2}}
	svc := &OpenAIGatewayService{
		groupRepo: &mockGroupRepoForGateway{groups: map[int64]*Group{
			groupID: {ID: groupID, Platform: PlatformOpenAI, SmartSchedulerEnabled: true},
		}},
		smartScheduler: orderer,
	}
	ctx := WithChannelMonitorProbe(WithSmartSchedulerEndpoint(context.Background(), "responses"))

	decision := svc.reviewOpenAISmartStickySession(ctx, openAISmartStickyReviewRequest{
		GroupID:        &groupID,
		SessionHash:    "monitor-probe",
		Platform:       PlatformOpenAI,
		RequestedModel: "gpt-test",
	}, 1)

	require.False(t, decision.Reviewed)
	require.Zero(t, orderer.calls)
}

func TestOpenAISmartStickyReviewThrottleUsesWeakAndStrongIntervals(t *testing.T) {
	svc := &OpenAIGatewayService{}
	now := time.Now()
	require.True(t, svc.claimSmartStickyReview("weak", now))
	require.False(t, svc.claimSmartStickyReview("weak", now.Add(time.Second)))

	svc.finishSmartStickyReview("weak", now, smartStickyWeakReviewInterval)
	require.False(t, svc.claimSmartStickyReview("weak", now.Add(smartStickyWeakReviewInterval-time.Second)))
	require.True(t, svc.claimSmartStickyReview("weak", now.Add(smartStickyWeakReviewInterval+time.Second)))

	svc.finishSmartStickyReview("strong", now, smartStickyStrongReviewInterval)
	require.False(t, svc.claimSmartStickyReview("strong", now.Add(smartStickyStrongReviewInterval-time.Second)))
	require.True(t, svc.claimSmartStickyReview("strong", now.Add(smartStickyStrongReviewInterval+time.Second)))
}

func TestOpenAISmartStickyReviewFinishPreservesCooldownState(t *testing.T) {
	svc := &OpenAIGatewayService{}
	now := time.Now()
	strongScore := 90.0
	svc.markSmartStickySwitchApplied("cooldown", 2, now, &strongScore)

	svc.finishSmartStickyReview("cooldown", now.Add(time.Second), smartStickyStrongReviewInterval)

	state := svc.smartStickyReviews["cooldown"]
	require.Equal(t, int64(2), state.lastSwitchedAccountID)
	require.WithinDuration(t, now.Add(smartStickyStrongSwitchCooldown), state.cooldownUntil, time.Millisecond)
	require.WithinDuration(t, now.Add(time.Second+smartStickyWeakReviewInterval), state.nextAt, time.Millisecond)
}

func TestSmartStickyCooldownWeakSessionCanBreakForClearQualityLead(t *testing.T) {
	weakScore := 64.0
	decision := openAISmartStickyReviewDecision{CurrentScore: &weakScore, QualityLead: 8}

	require.True(t, smartStickyCooldownCanBeBroken(decision, smartStickyWeakSwitchCooldown))
	decision.QualityLead = 7.9
	require.False(t, smartStickyCooldownCanBeBroken(decision, smartStickyWeakSwitchCooldown))
}

func TestSmartStickyCooldownStrongSessionCannotBreakEarly(t *testing.T) {
	strongScore := smartStickyStrongMinScore
	decision := openAISmartStickyReviewDecision{CurrentScore: &strongScore, QualityLead: 30}

	require.False(t, smartStickyCooldownCanBeBroken(decision, time.Second))
}

func TestSmartStickyFallbackDoesNotEnterSuccessfulSwitchCooldown(t *testing.T) {
	svc := &OpenAIGatewayService{}
	ctx := WithSmartSchedulerEndpoint(context.Background(), "responses")
	groupID := int64(15)
	trace := ctx.Value(smartStickySwitchTraceContextKey{}).(*smartStickySwitchTrace)
	trace.pending = &smartStickySwitchPending{
		groupID:              groupID,
		reviewKey:            "strict-fallback",
		previousAccountID:    1,
		expectedChallengerID: 2,
	}

	svc.logSmartStickySwitchApplied(ctx, &groupID, 3)

	_, exists := svc.smartStickyReviews["strict-fallback"]
	require.False(t, exists)
}

func TestOpenAIGatewaySmartSchedulerOverridesLoadAwarePriorityAndLoad(t *testing.T) {
	groupID := int64(88)
	accounts := []Account{
		{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Priority: 1, Concurrency: 1},
		{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Priority: 100, Concurrency: 1},
	}
	orderer := &staticSmartSchedulerOrderer{ranks: map[int64]int{2: 1, 1: 2}}
	concurrencyCache := stubConcurrencyCache{
		loadMap: map[int64]*AccountLoadInfo{
			1: {AccountID: 1, LoadRate: 10},
			2: {AccountID: 2, LoadRate: 80},
		},
	}
	cfg := testConfig()
	cfg.Gateway.Scheduling.LoadBatchEnabled = true
	svc := &OpenAIGatewayService{
		accountRepo:        stubOpenAIAccountRepo{accounts: accounts},
		groupRepo:          &mockGroupRepoForGateway{groups: map[int64]*Group{groupID: {ID: groupID, Platform: PlatformOpenAI, SmartSchedulerEnabled: true}}},
		cfg:                cfg,
		concurrencyService: NewConcurrencyService(concurrencyCache),
		smartScheduler:     orderer,
	}

	selection, err := svc.SelectAccountWithLoadAwareness(context.Background(), &groupID, "", "", nil)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, int64(2), selection.Account.ID)
	require.Equal(t, 1, orderer.calls)
}

func TestOpenAIGatewaySmartSchedulerIsolatedAccountIsNotSelected(t *testing.T) {
	groupID := int64(88)
	accounts := []Account{
		{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Priority: 1, Concurrency: 1},
		{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Priority: 100, Concurrency: 1},
	}
	orderer := &staticSmartSchedulerOrderer{
		ranks:    map[int64]int{2: 1},
		isolated: map[int64]bool{1: true},
	}
	svc := &OpenAIGatewayService{
		accountRepo: stubOpenAIAccountRepo{accounts: accounts},
		groupRepo: &mockGroupRepoForGateway{groups: map[int64]*Group{
			groupID: {ID: groupID, Platform: PlatformOpenAI, SmartSchedulerEnabled: true},
		}},
		smartScheduler: orderer,
	}

	selection, err := svc.SelectAccountWithLoadAwareness(context.Background(), &groupID, "", "", nil)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, int64(2), selection.Account.ID)
	require.Equal(t, 1, orderer.calls)
}

func TestOpenAIGatewaySmartSchedulerSelectsLeastBadWhenAllCandidatesAreSoftIsolated(t *testing.T) {
	groupID := int64(88)
	accounts := []Account{
		{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Priority: 1, Concurrency: 1},
		{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Priority: 100, Concurrency: 1},
	}
	orderer := &staticSmartSchedulerOrderer{softIsolated: map[int64]int64{1: 5, 2: 3}}
	svc := &OpenAIGatewayService{
		accountRepo: stubOpenAIAccountRepo{accounts: accounts},
		groupRepo: &mockGroupRepoForGateway{groups: map[int64]*Group{
			groupID: {ID: groupID, Platform: PlatformOpenAI, SmartSchedulerEnabled: true},
		}},
		smartScheduler: orderer,
	}

	selection, err := svc.SelectAccountWithLoadAwareness(context.Background(), &groupID, "", "", nil)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, int64(2), selection.Account.ID)
	require.Equal(t, 1, orderer.calls)
}

func TestOpenAIGatewaySmartSchedulerDoesNotRecoverHardIsolatedCandidates(t *testing.T) {
	groupID := int64(88)
	accounts := []Account{
		{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Priority: 1, Concurrency: 1},
		{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Priority: 2, Concurrency: 1},
	}
	orderer := &staticSmartSchedulerOrderer{isolated: map[int64]bool{1: true, 2: true}}
	svc := &OpenAIGatewayService{
		accountRepo: stubOpenAIAccountRepo{accounts: accounts},
		groupRepo: &mockGroupRepoForGateway{groups: map[int64]*Group{
			groupID: {ID: groupID, Platform: PlatformOpenAI, SmartSchedulerEnabled: true},
		}},
		smartScheduler: orderer,
	}

	selection, err := svc.SelectAccountWithLoadAwareness(context.Background(), &groupID, "", "", nil)

	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Nil(t, selection)
	require.Equal(t, 1, orderer.calls)
}

func TestOpenAIGatewaySmartSchedulerLoadBatchDoesNotRecoverHardIsolatedModelCooldownCandidates(t *testing.T) {
	groupID := int64(88)
	now := time.Now()
	accounts := []Account{
		{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Priority: 1, Concurrency: 1},
		{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Priority: 2, Concurrency: 1},
	}
	orderer := &staticSmartSchedulerOrderer{isolated: map[int64]bool{1: true, 2: true}}
	state := newOpenAIAccountModelTransientState(128)
	state.forceBlock(accounts[0].ID, "gpt-5.6-sol", now, 30*time.Second)
	state.forceBlock(accounts[1].ID, "gpt-5.6-sol", now, 20*time.Second)
	cfg := &config.Config{}
	cfg.Gateway.Scheduling.LoadBatchEnabled = true
	svc := &OpenAIGatewayService{
		accountRepo: stubOpenAIAccountRepo{accounts: accounts},
		groupRepo: &mockGroupRepoForGateway{groups: map[int64]*Group{
			groupID: {ID: groupID, Platform: PlatformOpenAI, SmartSchedulerEnabled: true},
		}},
		cfg:                  cfg,
		concurrencyService:   NewConcurrencyService(schedulerTestConcurrencyCache{}),
		openaiModelTransient: state,
		smartScheduler:       orderer,
	}

	selection, err := svc.SelectAccountWithLoadAwareness(context.Background(), &groupID, "", "gpt-5.6-sol", nil)

	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Nil(t, selection)
	require.Equal(t, 1, orderer.calls)
}

func TestOpenAIGatewaySmartSchedulerRankOverridesLegacyLowRateOrder(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	defer resetOpenAIAdvancedSchedulerSettingCacheForTest()

	groupID := int64(88)
	now := time.Now()
	cheap := upstreamCostTestAccount(1, UpstreamBillingProbeStatusOK, 0.02, now.Add(-time.Minute), 30*time.Minute)
	expensive := upstreamCostTestAccount(2, UpstreamBillingProbeStatusOK, 0.8, now.Add(-time.Minute), 30*time.Minute)
	for _, account := range []*Account{cheap, expensive} {
		account.Status = StatusActive
		account.Schedulable = true
		account.Concurrency = 1
	}
	settings := &openAIAdvancedSchedulerSettingRepoStub{values: map[string]string{
		SettingKeyOpenAILowUpstreamRatePriorityEnabled: "true",
	}}
	cfg := testConfig()
	svc := &OpenAIGatewayService{
		accountRepo: stubOpenAIAccountRepo{accounts: []Account{*cheap, *expensive}},
		groupRepo: &mockGroupRepoForGateway{groups: map[int64]*Group{
			groupID: {ID: groupID, Platform: PlatformOpenAI, SmartSchedulerEnabled: true},
		}},
		cfg:              cfg,
		rateLimitService: &RateLimitService{settingService: NewSettingService(settings, cfg)},
		smartScheduler:   &staticSmartSchedulerOrderer{ranks: map[int64]int{2: 1, 1: 2}},
	}

	selection, err := svc.SelectAccountWithLoadAwareness(context.Background(), &groupID, "", "", nil)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, expensive.ID, selection.Account.ID)
}

func TestGatewaySmartSchedulerIsolatedAccountIsNotSelectedInLoadAwareChain(t *testing.T) {
	groupID := int64(77)
	accounts := []Account{
		{ID: 1, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true, Priority: 1, Concurrency: 1, AccountGroups: []AccountGroup{{GroupID: groupID}}},
		{ID: 2, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true, Priority: 100, Concurrency: 1, AccountGroups: []AccountGroup{{GroupID: groupID}}},
	}
	byID := make(map[int64]*Account, len(accounts))
	for i := range accounts {
		byID[accounts[i].ID] = &accounts[i]
	}
	orderer := &staticSmartSchedulerOrderer{
		ranks:    map[int64]int{2: 1},
		isolated: map[int64]bool{1: true},
	}
	concurrencyCache := &mockConcurrencyCache{
		loadMap: map[int64]*AccountLoadInfo{
			1: {AccountID: 1, LoadRate: 0},
			2: {AccountID: 2, LoadRate: 80},
		},
	}
	cfg := testConfig()
	cfg.Gateway.Scheduling.LoadBatchEnabled = true
	svc := &GatewayService{
		accountRepo:        &mockAccountRepoForPlatform{accounts: accounts, accountsByID: byID},
		groupRepo:          &mockGroupRepoForGateway{groups: map[int64]*Group{groupID: {ID: groupID, Platform: PlatformAnthropic, SmartSchedulerEnabled: true}}},
		cache:              &mockGatewayCacheForPlatform{},
		cfg:                cfg,
		concurrencyService: NewConcurrencyService(concurrencyCache),
		smartScheduler:     orderer,
	}

	selection, err := svc.SelectAccountWithLoadAwareness(context.Background(), &groupID, "", "", nil, "", 0)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, int64(2), selection.Account.ID)
	require.Equal(t, 1, orderer.calls)
	require.Equal(t, 1, concurrencyCache.acquireAccountCalls)
}

func TestGatewaySmartSchedulerSelectsLeastBadWhenAllCandidatesAreSoftIsolatedInLoadAwareChain(t *testing.T) {
	groupID := int64(77)
	accounts, byID := smartSchedulerRoutingAccounts(groupID)
	orderer := &staticSmartSchedulerOrderer{softIsolated: map[int64]int64{1: 5, 2: 3}}
	concurrencyCache := &mockConcurrencyCache{
		loadMap: map[int64]*AccountLoadInfo{
			1: {AccountID: 1, LoadRate: 0},
			2: {AccountID: 2, LoadRate: 0},
		},
	}
	cfg := testConfig()
	cfg.Gateway.Scheduling.LoadBatchEnabled = true
	svc := &GatewayService{
		accountRepo:        &mockAccountRepoForPlatform{accounts: accounts, accountsByID: byID},
		groupRepo:          &mockGroupRepoForGateway{groups: map[int64]*Group{groupID: {ID: groupID, Platform: PlatformAnthropic, SmartSchedulerEnabled: true}}},
		cache:              &mockGatewayCacheForPlatform{},
		cfg:                cfg,
		concurrencyService: NewConcurrencyService(concurrencyCache),
		smartScheduler:     orderer,
	}

	selection, err := svc.SelectAccountWithLoadAwareness(context.Background(), &groupID, "", "", nil, "", 0)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, int64(2), selection.Account.ID)
	require.Equal(t, 1, orderer.calls)
	require.Equal(t, 1, concurrencyCache.acquireAccountCalls)
}
