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
		name         string
		batchEnabled bool
	}{
		{name: "without_batch_loads"},
		{name: "with_batch_loads", batchEnabled: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newChatCompletionsStickyEscapeFixture(t, chatStickySlowQuality(59000, 10), 0)
			fixture.svc.rateLimitService = newOpenAIAdvancedSchedulerRateLimitService("false")
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

	ordering, err := preview.OrderCandidates(context.Background(), group, "gpt-5.5", "chat_completions", accounts, time.Now())

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
	return f.svc.SelectAccountWithSchedulerForCapability(
		ctx,
		int64PtrForChatSticky(chatStickyEscapeGroupID),
		previousResponseID,
		f.session,
		"gpt-5.5",
		nil,
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
