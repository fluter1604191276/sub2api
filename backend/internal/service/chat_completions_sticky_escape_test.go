//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

const (
	chatStickyEscapeGroupID = int64(33)
	chatStickySlowAccountID = int64(7830)
	chatStickyPrimaryA      = int64(7993)
	chatStickyPrimaryB      = int64(7923)
)

func TestChatCompletionsWarmStickyEscapeLeavesSlowLowestAccount(t *testing.T) {
	fixture := newChatCompletionsStickyEscapeFixture(t, chatStickySlowQuality(59000, 10), 0)
	ctx, logs := chatStickyEscapeContext(t, "chat_completions")

	selection, decision, err := fixture.selectAccount(ctx, "")

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, chatStickyPrimaryA, selection.Account.ID)
	require.Equal(t, chatStickyPrimaryA, fixture.cache.sessionBindings["openai:"+fixture.session])
	require.Equal(t, 1, fixture.cache.deletedSessions["openai:"+fixture.session])
	require.Contains(t, logs.String(), "reason="+chatCompletionsWarmLatencyEscapeReason)
	require.Contains(t, logs.String(), "sticky.smart_scheduler_switched")
	require.Contains(t, logs.String(), "escape_latency_ms=59000")
	require.NotEqual(t, openAIAccountScheduleLayerPreviousResponse, decision.Layer)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func TestChatCompletionsWarmStickyEscapeExcludesSlowAccountFromLegacyReselection(t *testing.T) {
	tests := []struct {
		name            string
		batchEnabled    bool
		advancedEnabled string
	}{
		{name: "legacy_without_batch_loads", advancedEnabled: "false"},
		{name: "legacy_with_batch_loads", batchEnabled: true, advancedEnabled: "false"},
		{name: "advanced_without_batch_loads", advancedEnabled: "true"},
		{name: "advanced_with_batch_loads", batchEnabled: true, advancedEnabled: "true"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newChatCompletionsStickyEscapeFixture(t, chatStickySlowQuality(59000, 10), 0)
			fixture.svc.rateLimitService = newOpenAIAdvancedSchedulerRateLimitService(tt.advancedEnabled)
			fixture.svc.cfg.Gateway.Scheduling.LoadBatchEnabled = tt.batchEnabled
			fixture.orderer.pools = map[int64]string{
				chatStickySlowAccountID: "warm",
				chatStickyPrimaryA:      "warm",
				chatStickyPrimaryB:      "warm",
			}
			fixture.orderer.scores = map[int64]float64{
				chatStickySlowAccountID: 80,
				chatStickyPrimaryA:      81,
				chatStickyPrimaryB:      82,
			}
			fixture.orderer.costs = map[int64]float64{
				chatStickySlowAccountID: 0.5,
				chatStickyPrimaryA:      1,
				chatStickyPrimaryB:      1,
			}
			items := []SmartSchedulerPreviewItem{
				{AccountID: chatStickySlowAccountID, Pool: "warm", Score: previewFloat64Ptr(80), CostMultiplier: 0.5},
				{AccountID: chatStickyPrimaryA, Pool: "warm", Score: previewFloat64Ptr(81), CostMultiplier: 1},
				{AccountID: chatStickyPrimaryB, Pool: "warm", Score: previewFloat64Ptr(82), CostMultiplier: 1},
			}
			sortSmartSchedulerItems(items)
			require.Equal(t, chatStickySlowAccountID, items[0].AccountID)
			for index, item := range items {
				fixture.orderer.ranks[item.AccountID] = index + 1
			}
			ctx, logs := chatStickyEscapeContext(t, "chat_completions")

			selection, _, err := fixture.selectAccount(ctx, "")

			require.NoError(t, err)
			require.NotNil(t, selection)
			require.NotNil(t, selection.Account)
			require.NotEqual(t, chatStickySlowAccountID, selection.Account.ID, logs.String())
			require.Equal(t, selection.Account.ID, fixture.cache.sessionBindings["openai:"+fixture.session])
			require.Equal(t, 1, fixture.cache.deletedSessions["openai:"+fixture.session])
			require.Contains(t, logs.String(), "reason="+chatCompletionsWarmLatencyEscapeReason)
			if selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
			}
		})
	}
}

func TestSmartStickySwitchExcludesPreviousAccountAcrossLegacyLoadBatchModes(t *testing.T) {
	tests := []struct {
		name   string
		scores map[int64]float64
		costs  map[int64]float64
		pools  map[int64]string
	}{
		{
			name:   "better_quality",
			scores: map[int64]float64{chatStickySlowAccountID: 70, chatStickyPrimaryA: 80, chatStickyPrimaryB: 75},
			pools:  map[int64]string{chatStickySlowAccountID: "warm", chatStickyPrimaryA: "primary", chatStickyPrimaryB: "warm"},
		},
		{
			name:   "primary_over_warm",
			scores: map[int64]float64{chatStickySlowAccountID: 69, chatStickyPrimaryA: 70, chatStickyPrimaryB: 68},
			pools:  map[int64]string{chatStickySlowAccountID: "warm", chatStickyPrimaryA: "primary", chatStickyPrimaryB: "warm"},
		},
		{
			name:   "cheaper_within_tolerance",
			scores: map[int64]float64{chatStickySlowAccountID: 80, chatStickyPrimaryA: 78, chatStickyPrimaryB: 77},
			costs:  map[int64]float64{chatStickySlowAccountID: 1, chatStickyPrimaryA: 0.5, chatStickyPrimaryB: 1},
			pools:  map[int64]string{chatStickySlowAccountID: "primary", chatStickyPrimaryA: "primary", chatStickyPrimaryB: "primary"},
		},
	}
	for _, test := range tests {
		for _, batchEnabled := range []bool{false, true} {
			t.Run(test.name+"/"+map[bool]string{false: "without_batch_loads", true: "with_batch_loads"}[batchEnabled], func(t *testing.T) {
				fixture := newChatCompletionsStickyEscapeFixture(t, chatStickySlowQuality(12000, 10), 0)
				fixture.svc.rateLimitService = newOpenAIAdvancedSchedulerRateLimitService("false")
				fixture.svc.cfg.Gateway.Scheduling.LoadBatchEnabled = batchEnabled
				fixture.orderer.ranks = map[int64]int{chatStickyPrimaryA: 1, chatStickySlowAccountID: 2, chatStickyPrimaryB: 3}
				fixture.orderer.scores = test.scores
				fixture.orderer.costs = test.costs
				fixture.orderer.pools = test.pools
				fixture.svc.concurrencyService = NewConcurrencyService(schedulerTestConcurrencyCache{
					loadMap:        map[int64]*AccountLoadInfo{chatStickyPrimaryA: {AccountID: chatStickyPrimaryA, CurrentConcurrency: 1, LoadRate: 100}},
					acquireResults: map[int64]bool{chatStickyPrimaryA: false},
				})

				selection, _, err := fixture.selectAccount(WithSmartSchedulerEndpoint(context.Background(), "chat_completions"), "")

				require.NoError(t, err)
				require.NotNil(t, selection)
				require.NotNil(t, selection.Account)
				expectedAccountID := chatStickyPrimaryA
				if batchEnabled {
					expectedAccountID = chatStickySlowAccountID
				}
				require.Equal(t, expectedAccountID, selection.Account.ID, "non-latency switch keeps the existing full-challenger fallback")
				require.Equal(t, 1, fixture.cache.deletedSessions["openai:"+fixture.session])
				if selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
				}
			})
		}
	}
}

func TestSmartStickySwitchFallsBackToAlternateAccountWhenChallengerIsFull(t *testing.T) {
	fixture := newChatCompletionsStickyEscapeFixture(t, chatStickySlowQuality(12000, 10), 0)
	fixture.svc.rateLimitService = newOpenAIAdvancedSchedulerRateLimitService("true")
	fixture.orderer.ranks = map[int64]int{chatStickyPrimaryA: 1, chatStickySlowAccountID: 2, chatStickyPrimaryB: 3}
	fixture.orderer.scores = map[int64]float64{chatStickySlowAccountID: 70, chatStickyPrimaryA: 80, chatStickyPrimaryB: 75}
	fixture.orderer.pools = map[int64]string{chatStickySlowAccountID: "warm", chatStickyPrimaryA: "primary", chatStickyPrimaryB: "warm"}
	fixture.svc.concurrencyService = NewConcurrencyService(schedulerTestConcurrencyCache{
		loadMap: map[int64]*AccountLoadInfo{chatStickyPrimaryA: {AccountID: chatStickyPrimaryA, CurrentConcurrency: 1, LoadRate: 100}},
	})

	selection, _, err := fixture.selectAccount(WithSmartSchedulerEndpoint(context.Background(), "chat_completions"), "")

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, chatStickyPrimaryB, selection.Account.ID)
	markedFallback := false
	for _, state := range fixture.svc.smartStickyReviews {
		if state.lastSwitchedAccountID == chatStickyPrimaryB && !state.cooldownUntil.IsZero() && len(state.escapeTimes) == 1 {
			markedFallback = true
		}
	}
	require.True(t, markedFallback, "unexpected fallback must consume the reviewed switch cooldown and budget")
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func TestChatCompletionsStickyEscapeSelectionTraceIsolatedUntilAdmission(t *testing.T) {
	fixture := newChatCompletionsStickyEscapeFixture(t, chatStickySlowQuality(59000, 10), 0)
	fixture.svc.rateLimitService = newOpenAIAdvancedSchedulerRateLimitService("false")
	fixture.svc.cfg.Gateway.Scheduling.LoadBatchEnabled = false
	excluded := map[int64]struct{}{chatStickyPrimaryB: {}}
	ctx := WithSmartSchedulerEndpoint(context.Background(), "chat_completions")
	callerTrace, _ := ctx.Value(smartStickySwitchTraceContextKey{}).(*smartStickySwitchTrace)
	require.NotNil(t, callerTrace)

	// The first selection can only offer the challenger as a wait plan. It must
	// not consume the reviewed escape before a slot is actually admitted.
	fixture.svc.concurrencyService = NewConcurrencyService(schedulerTestConcurrencyCache{
		acquireResults: map[int64]bool{chatStickyPrimaryA: false},
	})
	first, _, err := fixture.selectAccountWithExclusions(ctx, "", excluded)
	require.NoError(t, err)
	require.NotNil(t, first)
	require.Equal(t, chatStickyPrimaryA, first.Account.ID)
	require.False(t, first.Acquired)
	require.NotNil(t, first.WaitPlan)
	require.NotNil(t, first.stickySwitchTrace)
	require.NotSame(t, callerTrace, first.stickySwitchTrace)
	first.stickySwitchTrace.mu.Lock()
	require.False(t, first.stickySwitchTrace.applied)
	first.stickySwitchTrace.mu.Unlock()
	require.Equal(t, map[int64]struct{}{chatStickyPrimaryB: {}}, excluded)

	// A retry on the same caller context receives a fresh trace. Its admission
	// cannot consume the first selection's pending review.
	fixture.svc.concurrencyService = NewConcurrencyService(schedulerTestConcurrencyCache{})
	second, _, err := fixture.selectAccountWithExclusions(ctx, "", excluded)
	require.NoError(t, err)
	require.NotNil(t, second)
	require.True(t, second.Acquired)
	require.Equal(t, chatStickyPrimaryA, second.Account.ID)
	require.NotNil(t, second.stickySwitchTrace)
	require.NotSame(t, first.stickySwitchTrace, second.stickySwitchTrace)
	require.Equal(t, map[int64]struct{}{chatStickyPrimaryB: {}}, excluded)

	handlerCtx := ContextWithSelectionProfitGate(ctx, second)
	require.NoError(t, fixture.svc.BindStickySessionAfterProfitAdmission(handlerCtx, int64PtrForChatSticky(chatStickyEscapeGroupID), fixture.session, second.Account.ID))
	second.stickySwitchTrace.mu.Lock()
	require.False(t, second.stickySwitchTrace.applied)
	second.stickySwitchTrace.mu.Unlock()
	first.stickySwitchTrace.mu.Lock()
	require.False(t, first.stickySwitchTrace.applied)
	first.stickySwitchTrace.mu.Unlock()

	// The retry had no new review claim, so admitting it must not consume the
	// first selection's pending escape or create a synthetic cooldown.
	for _, state := range fixture.svc.smartStickyReviews {
		require.NotEqual(t, second.Account.ID, state.lastSwitchedAccountID)
	}
	if second.ReleaseFunc != nil {
		second.ReleaseFunc()
	}
}

func TestChatCompletionsStickyEscapeProfitGateDefersAcquiredSwitchAccounting(t *testing.T) {
	fixture := newChatCompletionsStickyEscapeFixture(t, chatStickySlowQuality(59000, 10), 0)
	fixture.svc.rateLimitService = newOpenAIAdvancedSchedulerRateLimitService("false")
	fixture.svc.cfg.Gateway.Scheduling.LoadBatchEnabled = false
	repo := fixture.svc.accountRepo.(schedulerGroupAwareOpenAIAccountRepo)
	for index := range repo.accounts {
		rate := 0.1
		repo.accounts[index].RateMultiplier = &rate
	}
	fixture.svc.accountRepo = repo
	ctx := context.WithValue(
		WithSmartSchedulerEndpoint(context.Background(), "chat_completions"),
		openAIProfitControlGateCtxKey{},
		&openAIProfitControlGate{groupID: chatStickyEscapeGroupID, platform: PlatformOpenAI, threshold: 0.5, pricingAt: time.Now()},
	)

	selection, _, err := fixture.selectAccount(ctx, "")

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.True(t, selection.Acquired)
	require.Equal(t, chatStickyPrimaryA, selection.Account.ID)
	require.NotNil(t, selection.stickySwitchTrace)
	selection.stickySwitchTrace.mu.Lock()
	require.False(t, selection.stickySwitchTrace.applied)
	selection.stickySwitchTrace.mu.Unlock()
	for _, state := range fixture.svc.smartStickyReviews {
		require.NotEqual(t, selection.Account.ID, state.lastSwitchedAccountID)
	}
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func TestChatCompletionsStickyEscapeProfitAdmissionAccountsWithoutRebinding(t *testing.T) {
	fixture := newChatCompletionsStickyEscapeFixture(t, chatStickySlowQuality(59000, 10), 0)
	fixture.svc.rateLimitService = newOpenAIAdvancedSchedulerRateLimitService("false")
	fixture.svc.cfg.Gateway.Scheduling.LoadBatchEnabled = false
	repo := fixture.svc.accountRepo.(schedulerGroupAwareOpenAIAccountRepo)
	for index := range repo.accounts {
		rate := 0.1
		repo.accounts[index].RateMultiplier = &rate
	}
	fixture.svc.accountRepo = repo
	ctx := context.WithValue(
		WithSmartSchedulerEndpoint(context.Background(), "chat_completions"),
		openAIProfitControlGateCtxKey{},
		&openAIProfitControlGate{groupID: chatStickyEscapeGroupID, platform: PlatformOpenAI, threshold: 0.5, pricingAt: time.Now()},
	)

	selection, _, err := fixture.selectAccount(ctx, "")

	require.NoError(t, err)
	require.True(t, selection.Acquired)
	require.Equal(t, chatStickyPrimaryA, selection.Account.ID)
	// Another admitted request can restore a sticky binding while this request
	// waits for its terminal profit check.
	fixture.cache.sessionBindings["openai:"+fixture.session] = chatStickySlowAccountID
	handlerCtx := ContextWithSelectionProfitGate(ctx, selection)
	require.NoError(t, fixture.svc.BindStickySessionAfterProfitAdmission(handlerCtx, int64PtrForChatSticky(chatStickyEscapeGroupID), fixture.session, selection.Account.ID))
	// The pre-existing binding remains under a profit gate, but the admitted
	// fallback still consumes exactly its own reviewed switch.
	require.Equal(t, chatStickySlowAccountID, fixture.cache.sessionBindings["openai:"+fixture.session])
	selection.stickySwitchTrace.mu.Lock()
	require.True(t, selection.stickySwitchTrace.applied)
	selection.stickySwitchTrace.mu.Unlock()
	var applied smartStickyReviewState
	found := false
	for _, state := range fixture.svc.smartStickyReviews {
		if state.lastSwitchedAccountID == selection.Account.ID {
			applied = state
			found = true
		}
	}
	require.True(t, found)
	require.Len(t, applied.escapeTimes, 1)

	// A later failover binding attempt cannot consume the already-settled trace
	// for a different account.
	require.NoError(t, fixture.svc.BindStickySessionAfterProfitAdmission(handlerCtx, int64PtrForChatSticky(chatStickyEscapeGroupID), fixture.session, chatStickyPrimaryB))
	for _, state := range fixture.svc.smartStickyReviews {
		require.NotEqual(t, chatStickyPrimaryB, state.lastSwitchedAccountID)
	}
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func TestChatCompletionsStickyEscapeGuardianAdmissionDoesNotConsumeTrace(t *testing.T) {
	svc := &OpenAIGatewayService{}
	groupID := chatStickyEscapeGroupID
	sessionHash := "guardian-session"
	ctx := WithSmartSchedulerEndpoint(context.Background(), "chat_completions")
	trace := ctx.Value(smartStickySwitchTraceContextKey{}).(*smartStickySwitchTrace)
	trace.pending = &smartStickySwitchPending{groupID: groupID, reviewKey: "guardian", previousAccountID: chatStickySlowAccountID, expectedChallengerID: chatStickyPrimaryA, selectedAccountID: chatStickyPrimaryA}
	ctx = context.WithValue(ctx, openAIProfitControlGateCtxKey{}, &openAIProfitControlGate{groupID: groupID, platform: PlatformOpenAI, threshold: 0.5})
	ctx = context.WithValue(ctx, openAIGuardianParentAffinityContextKey{}, openAIGuardianParentAffinity{currentSessionHash: sessionHash})

	require.NoError(t, svc.BindStickySessionAfterProfitAdmission(ctx, &groupID, sessionHash, chatStickyPrimaryA))
	trace.mu.Lock()
	require.False(t, trace.applied)
	trace.mu.Unlock()
	_, exists := svc.smartStickyReviews["guardian"]
	require.False(t, exists)
}

func TestChatCompletionsWarmStickyEscapeDoesNotLeaveHealthyOrIncomparableAccounts(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		quality  AccountQualityStats
		pools    map[int64]string
		scores   map[int64]float64
		policyMs int
	}{
		{
			name:     "higher primary score but first token still healthy",
			endpoint: "chat_completions",
			quality:  chatStickySlowQuality(12000, 10),
		},
		{
			name:     "p90 exactly at the 30s threshold",
			endpoint: "chat_completions",
			quality:  chatStickySlowQuality(30000, 10),
		},
		{
			name:     "fewer than three comparable first-token samples",
			endpoint: "chat_completions",
			quality:  chatStickySlowQuality(59000, 2),
		},
		{
			name:     "responses endpoint keeps the session sticky",
			endpoint: "responses",
			quality:  chatStickySlowQuality(59000, 10),
		},
		{
			name:     "bound account is not the unique lowest score",
			endpoint: "chat_completions",
			quality:  chatStickySlowQuality(59000, 10),
			scores:   map[int64]float64{chatStickySlowAccountID: 80, chatStickyPrimaryA: 84, chatStickyPrimaryB: 79},
			pools:    map[int64]string{chatStickySlowAccountID: "warm", chatStickyPrimaryA: "primary", chatStickyPrimaryB: "warm"},
		},
		{
			name:     "slow account is still in the primary pool",
			endpoint: "chat_completions",
			quality:  chatStickySlowQuality(59000, 10),
			pools:    map[int64]string{chatStickySlowAccountID: "primary", chatStickyPrimaryA: "primary", chatStickyPrimaryB: "primary"},
		},
		{
			name:     "configured threshold above the observed p90",
			endpoint: "chat_completions",
			quality:  chatStickySlowQuality(59000, 10),
			policyMs: 60000,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newChatCompletionsStickyEscapeFixture(t, tt.quality, tt.policyMs)
			if tt.scores != nil {
				fixture.orderer.scores = tt.scores
			}
			if tt.pools != nil {
				fixture.orderer.pools = tt.pools
			}
			ctx, logs := chatStickyEscapeContext(t, tt.endpoint)

			selection, _, err := fixture.selectAccount(ctx, "")

			require.NoError(t, err)
			require.NotNil(t, selection)
			require.NotNil(t, selection.Account)
			require.Equal(t, chatStickySlowAccountID, selection.Account.ID, logs.String())
			require.Equal(t, chatStickySlowAccountID, fixture.cache.sessionBindings["openai:"+fixture.session])
			require.NotContains(t, logs.String(), chatCompletionsWarmLatencyEscapeReason)
			require.NotContains(t, logs.String(), "sticky.smart_scheduler_switched")
			require.GreaterOrEqual(t, fixture.orderer.calls, 1)
			if selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
			}
		})
	}
}

func TestChatCompletionsWarmStickyEscapeDoesNotOverridePreviousResponsePin(t *testing.T) {
	fixture := newChatCompletionsStickyEscapeFixture(t, chatStickySlowQuality(59000, 10), 0)
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.StickySessionTTLSeconds = 1800
	cfg.Gateway.OpenAIWS.StickyResponseIDTTLSeconds = 3600
	fixture.svc.cfg = cfg
	ctx, logs := chatStickyEscapeContext(t, "chat_completions")
	store := fixture.svc.getOpenAIWSStateStore()
	require.NoError(t, store.BindResponseAccount(ctx, chatStickyEscapeGroupID, "resp_pinned_7830", chatStickySlowAccountID, time.Hour))

	selection, decision, err := fixture.selectAccount(ctx, "resp_pinned_7830")

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, chatStickySlowAccountID, selection.Account.ID)
	require.Equal(t, openAIAccountScheduleLayerPreviousResponse, decision.Layer)
	require.True(t, decision.StickyPreviousHit)
	require.Zero(t, fixture.cache.deletedSessions["openai:"+fixture.session])
	require.NotContains(t, logs.String(), chatCompletionsWarmLatencyEscapeReason)
	require.GreaterOrEqual(t, fixture.orderer.calls, 1)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func TestChatCompletionsWarmStickyEscapeUsesExactEndpointLatencyAfterScoreFallback(t *testing.T) {
	p90 := 59000.0
	exactSlow := AccountQualityStats{Recent1h: AccountQualityPeriod{Last10: AccountQualityWindow{
		SampleCount:           10,
		FirstTokenSampleCount: 10,
		P90FirstTokenMs:       &p90,
	}, WindowHours: 1}}
	stats := &smartSchedulerStatsStub{
		qualityByScope: map[string]map[int64]AccountQualityStats{
			"gpt-5.5|/v1/chat/completions": {
				chatStickySlowAccountID: exactSlow,
				chatStickyPrimaryA:      smartSchedulerTestQuality(86),
			},
			"gpt-5.5|any": {
				chatStickySlowAccountID: smartSchedulerTestQuality(82),
			},
		},
		errors: map[int64]SmartSchedulerErrorStats{
			chatStickySlowAccountID: {SuccessfulRequestCount: 10},
			chatStickyPrimaryA:      {SuccessfulRequestCount: 20},
		},
	}
	preview := &SmartSchedulerPreviewService{dashboardService: stats}
	group := &Group{ID: chatStickyEscapeGroupID, Platform: PlatformOpenAI, SmartSchedulerEnabled: true}
	accounts := []*Account{
		smartSchedulerTestAccount(chatStickySlowAccountID, 1),
		smartSchedulerTestAccount(chatStickyPrimaryA, 100),
	}

	ordering, err := preview.OrderCandidates(withSmartSchedulerStableOrdering(context.Background()), group, "gpt-5.5", "chat_completions", accounts, time.Now())

	require.NoError(t, err)
	slow := ordering.ItemByAccountID[chatStickySlowAccountID]
	require.Equal(t, "warm", slow.Pool)
	require.True(t, slow.EvidenceFallback)
	require.NotNil(t, slow.ExactEndpointLatency)
	require.NotNil(t, slow.ExactEndpointLatency.P90FirstTokenMs)
	require.InDelta(t, 59000, *slow.ExactEndpointLatency.P90FirstTokenMs, 0.001)
	decision := decideOpenAISmartStickyReviewWithPolicy(ordering, chatStickySlowAccountID, RecommendedSmartStickyPolicy())
	require.False(t, decision.Switch, "fallback score must not already satisfy the older sticky escape")
	decision = applyChatCompletionsWarmLatencyEscape("chat_completions", ordering, chatStickySlowAccountID, RecommendedSmartStickyPolicy(), "", decision)
	require.True(t, decision.Switch)
	require.Equal(t, chatCompletionsWarmLatencyEscapeReason, decision.Reason)
	require.Equal(t, chatStickyPrimaryA, decision.ChallengerID)
}

func TestChatCompletionsWarmStickyEscapeRejectsFallbackLatencyWithoutExactSamples(t *testing.T) {
	p90 := 59000.0
	ordering := &SmartSchedulerOrdering{
		Active:            true,
		OrderedAccountIDs: []int64{chatStickyPrimaryA, chatStickySlowAccountID},
		ItemByAccountID: map[int64]SmartSchedulerPreviewItem{
			chatStickySlowAccountID: {
				AccountID:        chatStickySlowAccountID,
				Pool:             "warm",
				Score:            previewFloat64Ptr(80),
				EvidenceFallback: true,
				Quality1h: AccountQualityPeriod{Last10: AccountQualityWindow{
					SampleCount:           10,
					FirstTokenSampleCount: 10,
					P90FirstTokenMs:       &p90,
				}},
			},
			chatStickyPrimaryA: {AccountID: chatStickyPrimaryA, Pool: "primary", Score: previewFloat64Ptr(84)},
		},
	}
	decision := decideOpenAISmartStickyReviewWithPolicy(ordering, chatStickySlowAccountID, RecommendedSmartStickyPolicy())
	require.False(t, decision.Switch)

	decision = applyChatCompletionsWarmLatencyEscape("chat_completions", ordering, chatStickySlowAccountID, RecommendedSmartStickyPolicy(), "", decision)

	require.False(t, decision.Switch)
	require.NotEqual(t, chatCompletionsWarmLatencyEscapeReason, decision.Reason)
}

func TestChatCompletionsWarmStickyEscapeAcceptsEquivalentAverageWhenP90IsAbsent(t *testing.T) {
	average := 38000.0
	ordering := chatStickyEscapeOrdering(AccountQualityStats{Recent1h: AccountQualityPeriod{Last10: AccountQualityWindow{
		SampleCount:           10,
		FirstTokenSampleCount: 10,
		AverageFirstTokenMs:   &average,
	}}})
	decision := decideOpenAISmartStickyReviewWithPolicy(ordering, chatStickySlowAccountID, RecommendedSmartStickyPolicy())
	require.False(t, decision.Switch)

	decision = applyChatCompletionsWarmLatencyEscape("chat_completions", ordering, chatStickySlowAccountID, RecommendedSmartStickyPolicy(), "", decision)

	require.True(t, decision.Switch)
	require.Equal(t, chatCompletionsWarmLatencyEscapeReason, decision.Reason)
	require.NotNil(t, decision.EscapeLatencyMs)
	require.InDelta(t, 38000, *decision.EscapeLatencyMs, 0.001)
}

func TestChatCompletionsWarmLatencyThresholdValidation(t *testing.T) {
	policy := RecommendedSmartStickyPolicy()
	policy.ChatCompletionsWarmLatencyEscapeMs = 0
	normalized, err := policy.Normalize()
	require.NoError(t, err)
	require.Equal(t, defaultChatCompletionsWarmLatencyEscapeMs, normalized.ChatCompletionsWarmLatencyEscapeMs)

	for _, value := range []int{minChatCompletionsWarmLatencyEscapeMs - 1, maxChatCompletionsWarmLatencyEscapeMs + 1} {
		policy.ChatCompletionsWarmLatencyEscapeMs = value
		_, err = policy.Normalize()
		require.Error(t, err)
	}
}

type chatStickyEscapeFixture struct {
	svc     *OpenAIGatewayService
	cache   *schedulerTestGatewayCache
	orderer *staticSmartSchedulerOrderer
	session string
}

func newChatCompletionsStickyEscapeFixture(t *testing.T, quality AccountQualityStats, policyMs int) chatStickyEscapeFixture {
	t.Helper()
	session := "codex-desktop-g33"
	orderer := &staticSmartSchedulerOrderer{
		ranks: map[int64]int{chatStickyPrimaryA: 1, chatStickyPrimaryB: 2, chatStickySlowAccountID: 3},
		scores: map[int64]float64{
			chatStickySlowAccountID: 80,
			chatStickyPrimaryA:      84,
			chatStickyPrimaryB:      83,
		},
		pools: map[int64]string{
			chatStickySlowAccountID: "warm",
			chatStickyPrimaryA:      "primary",
			chatStickyPrimaryB:      "primary",
		},
		qualities: map[int64]AccountQualityStats{chatStickySlowAccountID: quality},
	}
	accounts := []Account{
		{ID: chatStickySlowAccountID, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Priority: 1, Concurrency: 1, GroupIDs: []int64{chatStickyEscapeGroupID}},
		{ID: chatStickyPrimaryA, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Priority: 2, Concurrency: 1, GroupIDs: []int64{chatStickyEscapeGroupID}},
		{ID: chatStickyPrimaryB, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Priority: 3, Concurrency: 1, GroupIDs: []int64{chatStickyEscapeGroupID}},
	}
	cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:" + session: chatStickySlowAccountID}}
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerGroupAwareOpenAIAccountRepo{schedulerTestOpenAIAccountRepo{accounts: accounts}},
		groupRepo:          &mockGroupRepoForGateway{groups: map[int64]*Group{chatStickyEscapeGroupID: {ID: chatStickyEscapeGroupID, Platform: PlatformOpenAI, SmartSchedulerEnabled: true}}},
		cache:              cache,
		cfg:                &config.Config{},
		rateLimitService:   newOpenAIAdvancedSchedulerRateLimitService("true"),
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
		smartScheduler:     orderer,
	}
	if policyMs > 0 {
		policy := RecommendedSmartStickyPolicy()
		policy.ChatCompletionsWarmLatencyEscapeMs = policyMs
		policy.configured = true
		raw, err := json.Marshal(policy)
		require.NoError(t, err)
		svc.settingService = &SettingService{settingRepo: &stickyPolicyRepoStub{values: map[string]string{
			smartStickyPolicyKey(chatStickyEscapeGroupID): string(raw),
		}}}
	}
	return chatStickyEscapeFixture{svc: svc, cache: cache, orderer: orderer, session: session}
}

func (f chatStickyEscapeFixture) selectAccount(ctx context.Context, previousResponseID string) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	return f.selectAccountWithExclusions(ctx, previousResponseID, nil)
}

func (f chatStickyEscapeFixture) selectAccountWithExclusions(ctx context.Context, previousResponseID string, excludedIDs map[int64]struct{}) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	return f.svc.SelectAccountWithSchedulerForCapability(
		ctx,
		int64PtrForChatSticky(chatStickyEscapeGroupID),
		previousResponseID,
		f.session,
		"gpt-5.5",
		excludedIDs,
		OpenAIUpstreamTransportAny,
		OpenAIEndpointCapabilityChatCompletions,
		false,
		false,
		true,
		PlatformOpenAI,
	)
}

func chatStickyEscapeContext(t *testing.T, endpoint string) (context.Context, *bytes.Buffer) {
	t.Helper()
	logs := &bytes.Buffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return WithSmartSchedulerEndpoint(context.Background(), endpoint), logs
}

func chatStickySlowQuality(p90 float64, samples int64) AccountQualityStats {
	return AccountQualityStats{Recent1h: AccountQualityPeriod{Last10: AccountQualityWindow{
		SampleCount:           samples,
		FirstTokenSampleCount: samples,
		P90FirstTokenMs:       &p90,
	}, WindowHours: 1}}
}

func chatStickyEscapeOrdering(quality AccountQualityStats) *SmartSchedulerOrdering {
	slow := SmartSchedulerPreviewItem{
		AccountID: chatStickySlowAccountID,
		Pool:      "warm",
		Score:     previewFloat64Ptr(80),
		Quality1h: quality.Recent1h,
	}
	if window, ok := comparableAccountQualityLatencyWindow(quality); ok {
		slow.ExactEndpointLatency = &window
	}
	return &SmartSchedulerOrdering{
		Active:            true,
		OrderedAccountIDs: []int64{chatStickyPrimaryA, chatStickySlowAccountID},
		ItemByAccountID: map[int64]SmartSchedulerPreviewItem{
			chatStickySlowAccountID: slow,
			chatStickyPrimaryA:      {AccountID: chatStickyPrimaryA, Pool: "primary", Score: previewFloat64Ptr(84)},
		},
	}
}

func int64PtrForChatSticky(value int64) *int64 {
	return &value
}
