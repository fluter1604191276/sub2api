package service

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

const SmartSchedulerPreviewAlgorithmVersion = "preview-v5"

const smartSchedulerOrderingCacheTTL = 15 * time.Second

const (
	smartSchedulerRecentWeight                  = 0.45
	smartSchedulerStableWeight                  = 0.20
	smartSchedulerErrorWeight                   = 0.15
	smartSchedulerCostWeight                    = 0.15
	smartSchedulerLoadWeight                    = 0.05
	smartSchedulerRecentLast10                  = 0.70
	smartSchedulerRecentLast100                 = 0.30
	smartSchedulerStableLast10                  = 0.30
	smartSchedulerStableLast100                 = 0.70
	smartSchedulerRobustMedian                  = 0.70
	smartSchedulerRobustTail                    = 0.30
	smartSchedulerTTFTWeight                    = 0.90
	smartSchedulerGenerationWeight              = 0.10
	smartSchedulerRecentErrorWeight             = 0.60
	smartSchedulerStableErrorWeight             = 0.40
	smartSchedulerExplorationBase               = 0.05
	smartSchedulerExplorationMax                = 0.10
	smartSchedulerProbeBootstrapExplorationRate = 0.20
	smartSchedulerProbeBootstrapConfidence      = 0.30
	smartSchedulerProbeBootstrapScoreMin        = 35.0
	smartSchedulerProbeBootstrapScoreMax        = 79.0
	smartSchedulerProbeBootstrapQualityWeight   = 0.80
	smartSchedulerProbeBootstrapCostWeight      = 0.20
	smartSchedulerCostTolerance                 = 3.0
	smartSchedulerHysteresisLead                = 4.0
	smartSchedulerImmediateFailures             = 3
)

const (
	smartSchedulerEvidenceModelEndpoint = "model_endpoint"
	smartSchedulerEvidenceModel         = "model"
	smartSchedulerEvidenceEndpoint      = "endpoint"
	smartSchedulerEvidenceAccount       = "account"
)

const (
	smartSchedulerBasisTTFTGeneration = "routing_ttft_generation"
	smartSchedulerBasisTTFTOnly       = "routing_ttft_only"
	smartSchedulerBasisGenerationOnly = "routing_generation_only"
)

var smartSchedulerGenerationCurve = []accountQualityCurvePoint{
	{LatencyMs: 10, Score: 0},
	{LatencyMs: 20, Score: 40},
	{LatencyMs: 30, Score: 65},
	{LatencyMs: 40, Score: 80},
	{LatencyMs: 50, Score: 92},
	{LatencyMs: 70, Score: 100},
}

type SmartSchedulerErrorStats struct {
	SuccessfulRequestCount          int64 `json:"successful_request_count"`
	ProviderFailureCount            int64 `json:"provider_failure_count"`
	ProviderTransientFailureCount   int64 `json:"provider_transient_failure_count"`
	RateLimitCount                  int64 `json:"rate_limit_count"`
	ClientExcludedCount             int64 `json:"client_excluded_count"`
	PlatformFailureCount            int64 `json:"platform_failure_count"`
	UncertainFailureCount           int64 `json:"uncertain_failure_count"`
	RecentProviderFailureCount      int64 `json:"recent_provider_failure_count"`
	RecentProviderTransientCount    int64 `json:"recent_provider_transient_count"`
	RecentRateLimitCount            int64 `json:"recent_rate_limit_count"`
	RecentUncertainFailureCount     int64 `json:"recent_uncertain_failure_count"`
	ImmediateProviderFailureCount   int64 `json:"immediate_provider_failure_count"`
	ImmediateProviderTransientCount int64 `json:"immediate_provider_transient_count"`
	ImmediateRateLimitCount         int64 `json:"immediate_rate_limit_count"`
	ImmediateUncertainFailureCount  int64 `json:"immediate_uncertain_failure_count"`
}

type smartSchedulerQualityStatsReader interface {
	GetSmartSchedulerQualityStatsBatch(ctx context.Context, accountIDs []int64, startTime, realtimeStartTime, endTime time.Time, requestedModel, endpoint string) (map[int64]AccountQualitySamples, error)
}

type smartSchedulerErrorStatsReader interface {
	GetSmartSchedulerErrorStatsBatch(ctx context.Context, accountIDs []int64, startTime, endTime time.Time, requestedModel, endpoint string) (map[int64]SmartSchedulerErrorStats, error)
}

type smartSchedulerCapacityStatsReader interface {
	GetSmartSchedulerCapacityLimitedCount(ctx context.Context, groupID int64, startTime, endTime time.Time, requestedModel, endpoint string) (int64, error)
}

type smartSchedulerStatsService interface {
	GetSmartSchedulerQualityStatsBatch(ctx context.Context, accountIDs []int64, now time.Time, requestedModel, endpoint string) (map[int64]AccountQualityStats, error)
	GetSmartSchedulerErrorStatsBatch(ctx context.Context, accountIDs []int64, now time.Time, requestedModel, endpoint string) (map[int64]SmartSchedulerErrorStats, error)
	GetSmartSchedulerAccountCircuitStatsBatch(ctx context.Context, accountIDs []int64, now time.Time) (map[int64]SmartSchedulerErrorStats, error)
	GetSmartSchedulerCapacityLimitedCount(ctx context.Context, groupID int64, now time.Time, requestedModel, endpoint string) (int64, error)
}

// GetSmartSchedulerErrorStatsBatch reads only classified streaming errors for
// the preview. The optional repository interface keeps existing test doubles
// and alternate storage implementations compatible.
func (s *DashboardService) GetSmartSchedulerErrorStatsBatch(ctx context.Context, accountIDs []int64, now time.Time, requestedModel, endpoint string) (map[int64]SmartSchedulerErrorStats, error) {
	result := make(map[int64]SmartSchedulerErrorStats, len(accountIDs))
	if len(accountIDs) == 0 {
		return result, nil
	}
	reader, ok := s.usageRepo.(smartSchedulerErrorStatsReader)
	if !ok {
		return result, nil
	}
	end := now.UTC()
	stats, err := reader.GetSmartSchedulerErrorStatsBatch(ctx, accountIDs, end.Add(-24*time.Hour), end, requestedModel, endpoint)
	if err != nil {
		return nil, fmt.Errorf("get smart scheduler error stats failed: %w", err)
	}
	return stats, nil
}

func (s *DashboardService) GetSmartSchedulerAccountCircuitStatsBatch(ctx context.Context, accountIDs []int64, now time.Time) (map[int64]SmartSchedulerErrorStats, error) {
	result := make(map[int64]SmartSchedulerErrorStats, len(accountIDs))
	if len(accountIDs) == 0 {
		return result, nil
	}
	reader, ok := s.usageRepo.(smartSchedulerErrorStatsReader)
	if !ok {
		return result, nil
	}
	end := now.UTC()
	stats, err := reader.GetSmartSchedulerErrorStatsBatch(ctx, accountIDs, end.Add(-5*time.Minute), end, "", "any")
	if err != nil {
		return nil, fmt.Errorf("get smart scheduler account circuit stats failed: %w", err)
	}
	return stats, nil
}

func (s *DashboardService) GetSmartSchedulerCapacityLimitedCount(ctx context.Context, groupID int64, now time.Time, requestedModel, endpoint string) (int64, error) {
	reader, ok := s.usageRepo.(smartSchedulerCapacityStatsReader)
	if !ok {
		return 0, nil
	}
	end := now.UTC()
	count, err := reader.GetSmartSchedulerCapacityLimitedCount(ctx, groupID, end.Add(-time.Hour), end, requestedModel, smartSchedulerStoredEndpoint(endpoint))
	if err != nil {
		return 0, fmt.Errorf("get smart scheduler capacity stats failed: %w", err)
	}
	return count, nil
}

func (s *DashboardService) GetSmartSchedulerQualityStatsBatch(ctx context.Context, accountIDs []int64, now time.Time, requestedModel, endpoint string) (map[int64]AccountQualityStats, error) {
	uniqueIDs := normalizeQualityIDs(accountIDs)
	result := make(map[int64]AccountQualityStats, len(uniqueIDs))
	if len(uniqueIDs) == 0 {
		return result, nil
	}
	reader, ok := s.usageRepo.(smartSchedulerQualityStatsReader)
	if !ok {
		return result, nil
	}
	end := now.UTC()
	samples, err := reader.GetSmartSchedulerQualityStatsBatch(
		ctx,
		uniqueIDs,
		end.Add(-AccountQualityWindowHours*time.Hour),
		end.Add(-AccountQualityRealtimeWindowHours*time.Hour),
		end,
		requestedModel,
		endpoint,
	)
	if err != nil {
		return nil, fmt.Errorf("get smart scheduler quality stats failed: %w", err)
	}
	for _, id := range uniqueIDs {
		sample := samples[id]
		result[id] = AccountQualityStats{
			Last10:      applySmartSchedulerQualityScore(sample.Last24h.Last10),
			Last100:     applySmartSchedulerQualityScore(sample.Last24h.Last100),
			WindowHours: AccountQualityWindowHours,
			Recent1h: AccountQualityPeriod{
				Last10:      applySmartSchedulerQualityScore(sample.Recent1h.Last10),
				Last100:     applySmartSchedulerQualityScore(sample.Recent1h.Last100),
				WindowHours: AccountQualityRealtimeWindowHours,
			},
			Activity: AccountQualityActivity{
				State:                  classifyAccountQualityActivity(sample.SuccessfulRequests1h, 0),
				SuccessfulRequestCount: sample.SuccessfulRequests1h,
				LastSuccessAt:          sample.LastSuccessAt,
			},
			ScoreVersion: AccountQualityScoreVersion,
		}
	}
	return result, nil
}

type SmartSchedulerPreview struct {
	Group                   SmartSchedulerGroupSummary  `json:"group"`
	Platform                string                      `json:"platform"`
	RequestedModel          string                      `json:"requested_model"`
	Endpoint                string                      `json:"endpoint"`
	AlgorithmVersion        string                      `json:"algorithm_version"`
	GeneratedAt             time.Time                   `json:"generated_at"`
	TotalAccounts           int                         `json:"total_accounts"`
	PrimaryCount            int                         `json:"primary_count"`
	WarmCount               int                         `json:"warm_count"`
	IsolatedCount           int                         `json:"isolated_count"`
	ExplorationRate         float64                     `json:"exploration_rate"`
	ProductionControlActive bool                        `json:"production_control_active"`
	LoadSnapshotAvailable   bool                        `json:"load_snapshot_available"`
	CapacityLimitedCount1h  int64                       `json:"capacity_limited_count_1h"`
	Warnings                []string                    `json:"warnings"`
	Items                   []SmartSchedulerPreviewItem `json:"items"`
}

type SmartSchedulerGroupSummary struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type SmartSchedulerPreviewItem struct {
	Rank                            int                      `json:"rank"`
	AccountID                       int64                    `json:"account_id"`
	AccountName                     string                   `json:"account_name"`
	Platform                        string                   `json:"platform"`
	Priority                        *int                     `json:"priority,omitempty"`
	Status                          string                   `json:"status"`
	Schedulable                     bool                     `json:"schedulable"`
	Pool                            string                   `json:"pool"`
	Decision                        string                   `json:"decision"`
	Reason                          string                   `json:"reason"`
	Score                           *float64                 `json:"score,omitempty"`
	RawScore                        *float64                 `json:"raw_score,omitempty"`
	Confidence                      float64                  `json:"confidence"`
	ConfidenceLabel                 string                   `json:"confidence_label"`
	EvidenceScope                   string                   `json:"evidence_scope"`
	EvidenceFallback                bool                     `json:"evidence_fallback"`
	ExplorationCandidate            bool                     `json:"exploration_candidate"`
	ProbeBootstrap                  bool                     `json:"probe_bootstrap"`
	Quality1h                       AccountQualityPeriod     `json:"quality_1h"`
	Quality24h                      AccountQualityPeriod     `json:"quality_24h"`
	Activity                        AccountQualityActivity   `json:"activity"`
	ErrorSuccessfulRequestCount     int64                    `json:"error_successful_request_count"`
	ProviderFailureCount            int64                    `json:"provider_failure_count"`
	ProviderTransientFailureCount   int64                    `json:"provider_transient_failure_count"`
	RateLimitCount                  int64                    `json:"rate_limit_count"`
	ClientExcludedCount             int64                    `json:"client_excluded_count"`
	PlatformFailureCount            int64                    `json:"platform_failure_count"`
	UncertainFailureCount           int64                    `json:"uncertain_failure_count"`
	RecentProviderFailureCount      int64                    `json:"recent_provider_failure_count"`
	RecentProviderTransientCount    int64                    `json:"recent_provider_transient_count"`
	RecentRateLimitCount            int64                    `json:"recent_rate_limit_count"`
	RecentUncertainFailureCount     int64                    `json:"recent_uncertain_failure_count"`
	ImmediateProviderFailureCount   int64                    `json:"immediate_provider_failure_count"`
	ImmediateProviderTransientCount int64                    `json:"immediate_provider_transient_count"`
	ImmediateRateLimitCount         int64                    `json:"immediate_rate_limit_count"`
	ImmediateUncertainFailureCount  int64                    `json:"immediate_uncertain_failure_count"`
	CostMultiplier                  float64                  `json:"cost_multiplier"`
	Load                            *SmartSchedulerLoad      `json:"load,omitempty"`
	ModelSupported                  bool                     `json:"model_supported"`
	EndpointSupported               bool                     `json:"endpoint_supported"`
	ModelMapping                    string                   `json:"model_mapping,omitempty"`
	LastUsedAt                      *time.Time               `json:"last_used_at,omitempty"`
	RecoveryProbe                   *GroupRecoveryProbeState `json:"recovery_probe,omitempty"`
	SoftIsolation                   bool                     `json:"-"`
	SoftIsolationFailureCount       int64                    `json:"-"`
}

type smartSchedulerQualityScope struct {
	Name           string
	RequestedModel string
	Endpoint       string
	Fallback       bool
}

type smartSchedulerScopedQuality struct {
	Scope smartSchedulerQualityScope
	Stats map[int64]AccountQualityStats
}

type SmartSchedulerLoad struct {
	CurrentConcurrency int `json:"current_concurrency"`
	WaitingCount       int `json:"waiting_count"`
	LoadRate           int `json:"load_rate"`
	MaxConcurrency     int `json:"max_concurrency"`
}

type smartSchedulerAdminService interface {
	GetGroup(ctx context.Context, id int64) (*Group, error)
	ListAccountsForSchedulerScoreFilter(ctx context.Context, platform, accountType, status, search string, groupID int64, privacyMode string) ([]Account, error)
}

type SmartSchedulerPreviewService struct {
	adminService     smartSchedulerAdminService
	dashboardService smartSchedulerStatsService
	concurrency      *ConcurrencyService
	recoveryProbe    GroupRecoveryProbeRepository
	cacheMu          sync.Mutex
	orderingCache    map[string]smartSchedulerOrderingCacheEntry
	hysteresis       map[string]smartSchedulerHysteresisState
	exploration      map[string]smartSchedulerExplorationState
	randomFloat      func() float64
}

func (s *SmartSchedulerPreviewService) SetRecoveryProbeRepository(repo GroupRecoveryProbeRepository) {
	if s != nil {
		s.recoveryProbe = repo
	}
}

func (s *SmartSchedulerPreviewService) InvalidateOrderingCache() {
	if s == nil {
		return
	}
	s.cacheMu.Lock()
	s.orderingCache = make(map[string]smartSchedulerOrderingCacheEntry)
	s.cacheMu.Unlock()
}

type SmartSchedulerOrdering struct {
	Active            bool
	AlgorithmVersion  string
	RankByAccountID   map[int64]int
	ItemByAccountID   map[int64]SmartSchedulerPreviewItem
	Exploration       bool
	ExplorationRate   float64
	OrderedAccountIDs []int64
}

type smartSchedulerOrderingCacheEntry struct {
	expiresAt time.Time
	ordering  *SmartSchedulerOrdering
}

type smartSchedulerHysteresisState struct {
	accountID int64
	updatedAt time.Time
}

type smartSchedulerExplorationState struct {
	accountID int64
	updatedAt time.Time
}

func NewSmartSchedulerPreviewService(adminService AdminService, dashboardService *DashboardService, concurrency *ConcurrencyService) *SmartSchedulerPreviewService {
	return &SmartSchedulerPreviewService{
		adminService:     adminService,
		dashboardService: dashboardService,
		concurrency:      concurrency,
		orderingCache:    make(map[string]smartSchedulerOrderingCacheEntry),
		hysteresis:       make(map[string]smartSchedulerHysteresisState),
		exploration:      make(map[string]smartSchedulerExplorationState),
		randomFloat:      rand.Float64,
	}
}

// OrderCandidates ranks only the candidates that already passed the gateway's
// existing admission checks. A disabled group never reads statistics, while a
// statistics error is returned so the caller can preserve legacy scheduling.
func (s *SmartSchedulerPreviewService) OrderCandidates(
	ctx context.Context,
	group *Group,
	requestedModel string,
	endpoint string,
	accounts []*Account,
	now time.Time,
) (*SmartSchedulerOrdering, error) {
	if group == nil || !group.SmartSchedulerEnabled {
		return &SmartSchedulerOrdering{Active: false}, nil
	}
	if s == nil || s.dashboardService == nil {
		return nil, fmt.Errorf("smart scheduler statistics service is unavailable")
	}

	requestedModel = strings.TrimSpace(requestedModel)
	endpoint = normalizeSmartSchedulerEndpoint(endpoint)
	cacheKey := smartSchedulerOrderingCacheKey(group.ID, requestedModel, endpoint, accounts)
	if cached := s.loadCachedOrdering(cacheKey, now); cached != nil {
		if smartSchedulerStableOrderingRequested(ctx) {
			return cached, nil
		}
		return s.applySmartSchedulerExploration(cached, cacheKey, now), nil
	}

	accountIDs := make([]int64, 0, len(accounts))
	loadRequests := make([]AccountWithConcurrency, 0, len(accounts))
	for _, account := range accounts {
		if account == nil {
			continue
		}
		accountIDs = append(accountIDs, account.ID)
		loadRequests = append(loadRequests, AccountWithConcurrency{ID: account.ID, MaxConcurrency: account.EffectiveLoadFactor()})
	}
	if len(accountIDs) == 0 {
		ordering := &SmartSchedulerOrdering{
			Active:            true,
			AlgorithmVersion:  SmartSchedulerPreviewAlgorithmVersion,
			RankByAccountID:   map[int64]int{},
			ItemByAccountID:   map[int64]SmartSchedulerPreviewItem{},
			OrderedAccountIDs: []int64{},
		}
		s.storeCachedOrdering(cacheKey, ordering, now)
		return cloneSmartSchedulerOrdering(ordering), nil
	}

	qualityScopes := smartSchedulerQualityScopes(requestedModel, endpoint)
	scopedQuality := make([]smartSchedulerScopedQuality, 0, len(qualityScopes))
	for _, scope := range qualityScopes {
		quality, err := s.dashboardService.GetSmartSchedulerQualityStatsBatch(ctx, accountIDs, now, scope.RequestedModel, scope.Endpoint)
		if err != nil {
			return nil, err
		}
		scopedQuality = append(scopedQuality, smartSchedulerScopedQuality{Scope: scope, Stats: quality})
	}
	exactScope := qualityScopes[0]
	errorStats, err := s.dashboardService.GetSmartSchedulerErrorStatsBatch(ctx, accountIDs, now, exactScope.RequestedModel, exactScope.Endpoint)
	if err != nil {
		return nil, err
	}
	accountErrorStats := errorStats
	if exactScope.RequestedModel != "" || exactScope.Endpoint != "any" {
		accountErrorStats, err = s.dashboardService.GetSmartSchedulerAccountCircuitStatsBatch(ctx, accountIDs, now)
		if err != nil {
			return nil, err
		}
	}

	loads := make(map[int64]*AccountLoadInfo)
	if s.concurrency != nil && len(loadRequests) > 0 {
		if snapshot, loadErr := s.concurrency.GetAccountsLoadBatch(ctx, loadRequests); loadErr == nil {
			loads = snapshot
		}
	}
	recoveryProbeStates := make(map[int64]GroupRecoveryProbeState)
	if probeModel, ok := smartSchedulerRecoveryProbeModel(group); ok && s.recoveryProbe != nil {
		states, stateErr := s.recoveryProbe.ListStates(ctx, group.ID, accountIDs, probeModel)
		if stateErr != nil {
			logger.LegacyPrintf("service.smart_scheduler", "[SmartScheduler] recovery probe state load failed: group=%d model=%s err=%v", group.ID, probeModel, stateErr)
		} else {
			recoveryProbeStates = states
		}
	}

	items := make([]SmartSchedulerPreviewItem, 0, len(accounts))
	for _, account := range accounts {
		if account == nil {
			continue
		}
		quality, evidenceScope, evidenceFallback := selectSmartSchedulerQualityEvidence(account.ID, scopedQuality)
		item := buildSmartSchedulerPreviewItem(account, group, requestedModel, endpoint, quality, errorStats[account.ID], loads[account.ID])
		item.EvidenceScope = evidenceScope
		item.EvidenceFallback = evidenceFallback
		applySmartSchedulerEvidencePolicy(&item)
		applySmartSchedulerAccountCircuitBreaker(&item, accountErrorStats[account.ID])
		if state, ok := recoveryProbeStates[account.ID]; ok {
			applyGroupRecoveryProbeStateToSchedulerItem(&item, &state)
		}
		items = append(items, item)
	}
	for i := range items {
		items[i].RawScore = smartSchedulerScore(items[i], items)
		items[i].Confidence = smartSchedulerConfidence(items[i])
		items[i].ConfidenceLabel = smartSchedulerConfidenceLabel(items[i].Confidence)
	}
	applySmartSchedulerConfidenceAdjustment(items)
	explorationRate := applySmartSchedulerExplorationPreview(items)
	sortSmartSchedulerItems(items)
	s.applySmartSchedulerHysteresis(cacheKey, items, now)

	ordering := &SmartSchedulerOrdering{
		Active:            true,
		AlgorithmVersion:  SmartSchedulerPreviewAlgorithmVersion,
		RankByAccountID:   make(map[int64]int, len(items)),
		ItemByAccountID:   make(map[int64]SmartSchedulerPreviewItem, len(items)),
		ExplorationRate:   explorationRate,
		OrderedAccountIDs: make([]int64, 0, len(items)),
	}
	for _, item := range items {
		ordering.ItemByAccountID[item.AccountID] = item
		if item.Pool == "isolated" {
			continue
		}
		rank := len(ordering.OrderedAccountIDs) + 1
		ordering.RankByAccountID[item.AccountID] = rank
		ordering.OrderedAccountIDs = append(ordering.OrderedAccountIDs, item.AccountID)
	}
	s.storeCachedOrdering(cacheKey, ordering, now)
	if smartSchedulerStableOrderingRequested(ctx) {
		return cloneSmartSchedulerOrdering(ordering), nil
	}
	return s.applySmartSchedulerExploration(cloneSmartSchedulerOrdering(ordering), cacheKey, now), nil
}

func (s *SmartSchedulerPreviewService) applySmartSchedulerExploration(ordering *SmartSchedulerOrdering, key string, now time.Time) *SmartSchedulerOrdering {
	if ordering == nil || !ordering.Active || ordering.ExplorationRate <= 0 || s.smartSchedulerRandomFloat() >= ordering.ExplorationRate {
		return ordering
	}
	candidates := make([]int64, 0, len(ordering.OrderedAccountIDs))
	bootstrapCandidates := make([]int64, 0, len(ordering.OrderedAccountIDs))
	for _, accountID := range ordering.OrderedAccountIDs {
		item := ordering.ItemByAccountID[accountID]
		if item.Pool == "warm" && item.ExplorationCandidate {
			candidates = append(candidates, accountID)
			if item.ProbeBootstrap {
				bootstrapCandidates = append(bootstrapCandidates, accountID)
			}
		}
	}
	if len(bootstrapCandidates) > 0 {
		candidates = bootstrapCandidates
	}
	if len(candidates) == 0 {
		return ordering
	}

	s.cacheMu.Lock()
	if s.exploration == nil {
		s.exploration = make(map[string]smartSchedulerExplorationState)
	}
	selected := candidates[0]
	if previous, ok := s.exploration[key]; ok {
		for i, accountID := range candidates {
			if accountID == previous.accountID {
				selected = candidates[(i+1)%len(candidates)]
				break
			}
		}
	}
	s.exploration[key] = smartSchedulerExplorationState{accountID: selected, updatedAt: now}
	s.pruneSmartSchedulerStateLocked(now)
	s.cacheMu.Unlock()

	selectedIndex := -1
	for i, accountID := range ordering.OrderedAccountIDs {
		if accountID == selected {
			selectedIndex = i
			break
		}
	}
	if selectedIndex < 0 {
		return ordering
	}
	copy(ordering.OrderedAccountIDs[1:selectedIndex+1], ordering.OrderedAccountIDs[0:selectedIndex])
	ordering.OrderedAccountIDs[0] = selected
	ordering.RankByAccountID = make(map[int64]int, len(ordering.OrderedAccountIDs))
	for index, orderedID := range ordering.OrderedAccountIDs {
		ordering.RankByAccountID[orderedID] = index + 1
	}
	ordering.Exploration = true
	return ordering
}

func (s *SmartSchedulerPreviewService) applySmartSchedulerHysteresis(key string, items []SmartSchedulerPreviewItem, now time.Time) {
	if len(items) == 0 {
		return
	}
	challenger := items[0]
	if challenger.Pool != "primary" || challenger.Score == nil {
		return
	}

	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if s.hysteresis == nil {
		s.hysteresis = make(map[string]smartSchedulerHysteresisState)
	}
	state, ok := s.hysteresis[key]
	if !ok {
		s.hysteresis[key] = smartSchedulerHysteresisState{accountID: challenger.AccountID, updatedAt: now}
		s.pruneSmartSchedulerStateLocked(now)
		return
	}
	incumbentIndex := -1
	for i := range items {
		if items[i].AccountID == state.accountID && items[i].Pool == "primary" && items[i].Score != nil {
			incumbentIndex = i
			break
		}
	}
	if incumbentIndex < 0 {
		s.hysteresis[key] = smartSchedulerHysteresisState{accountID: challenger.AccountID, updatedAt: now}
		return
	}
	incumbent := items[incumbentIndex]
	if challenger.AccountID != incumbent.AccountID && *challenger.Score-*incumbent.Score < smartSchedulerHysteresisLead {
		copy(items[1:incumbentIndex+1], items[0:incumbentIndex])
		items[0] = incumbent
		state.updatedAt = now
		s.hysteresis[key] = state
		return
	}
	s.hysteresis[key] = smartSchedulerHysteresisState{accountID: challenger.AccountID, updatedAt: now}
}

func (s *SmartSchedulerPreviewService) pruneSmartSchedulerStateLocked(now time.Time) {
	const stateTTL = 24 * time.Hour
	if len(s.hysteresis) > 512 {
		for key, state := range s.hysteresis {
			if now.Sub(state.updatedAt) >= stateTTL {
				delete(s.hysteresis, key)
			}
		}
		for key := range s.hysteresis {
			if len(s.hysteresis) <= 512 {
				break
			}
			delete(s.hysteresis, key)
		}
	}
	if len(s.exploration) > 512 {
		for key, state := range s.exploration {
			if now.Sub(state.updatedAt) >= stateTTL {
				delete(s.exploration, key)
			}
		}
		for key := range s.exploration {
			if len(s.exploration) <= 512 {
				break
			}
			delete(s.exploration, key)
		}
	}
}

func (s *SmartSchedulerPreviewService) smartSchedulerRandomFloat() float64 {
	if s.randomFloat != nil {
		return s.randomFloat()
	}
	return rand.Float64()
}

func smartSchedulerOrderingCacheKey(groupID int64, requestedModel, endpoint string, accounts []*Account) string {
	accountIDs := make([]int64, 0, len(accounts))
	for _, account := range accounts {
		if account != nil {
			accountIDs = append(accountIDs, account.ID)
		}
	}
	sort.Slice(accountIDs, func(i, j int) bool { return accountIDs[i] < accountIDs[j] })
	return fmt.Sprintf("%d|%s|%s|%v", groupID, strings.ToLower(requestedModel), endpoint, accountIDs)
}

func (s *SmartSchedulerPreviewService) loadCachedOrdering(key string, now time.Time) *SmartSchedulerOrdering {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	entry, ok := s.orderingCache[key]
	if !ok || !now.Before(entry.expiresAt) {
		if ok {
			delete(s.orderingCache, key)
		}
		return nil
	}
	return cloneSmartSchedulerOrdering(entry.ordering)
}

func (s *SmartSchedulerPreviewService) storeCachedOrdering(key string, ordering *SmartSchedulerOrdering, now time.Time) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if s.orderingCache == nil {
		s.orderingCache = make(map[string]smartSchedulerOrderingCacheEntry)
	}
	if len(s.orderingCache) >= 512 {
		for cachedKey, entry := range s.orderingCache {
			if !now.Before(entry.expiresAt) {
				delete(s.orderingCache, cachedKey)
			}
		}
		for cachedKey := range s.orderingCache {
			if len(s.orderingCache) < 512 {
				break
			}
			delete(s.orderingCache, cachedKey)
		}
	}
	s.orderingCache[key] = smartSchedulerOrderingCacheEntry{
		expiresAt: now.Add(smartSchedulerOrderingCacheTTL),
		ordering:  cloneSmartSchedulerOrdering(ordering),
	}
}

func cloneSmartSchedulerOrdering(ordering *SmartSchedulerOrdering) *SmartSchedulerOrdering {
	if ordering == nil {
		return nil
	}
	cloned := *ordering
	cloned.RankByAccountID = make(map[int64]int, len(ordering.RankByAccountID))
	for accountID, rank := range ordering.RankByAccountID {
		cloned.RankByAccountID[accountID] = rank
	}
	cloned.ItemByAccountID = make(map[int64]SmartSchedulerPreviewItem, len(ordering.ItemByAccountID))
	for accountID, item := range ordering.ItemByAccountID {
		cloned.ItemByAccountID[accountID] = item
	}
	cloned.OrderedAccountIDs = append([]int64(nil), ordering.OrderedAccountIDs...)
	return &cloned
}

func (s *SmartSchedulerPreviewService) Preview(ctx context.Context, groupID int64, requestedModel, endpoint string, now time.Time) (*SmartSchedulerPreview, error) {
	if s == nil || s.adminService == nil {
		return nil, fmt.Errorf("smart scheduler preview service is unavailable")
	}
	if groupID <= 0 {
		return nil, fmt.Errorf("group id must be positive")
	}
	group, err := s.adminService.GetGroup(ctx, groupID)
	if err != nil {
		return nil, fmt.Errorf("get group: %w", err)
	}
	requestedModel = strings.TrimSpace(requestedModel)
	endpoint = normalizeSmartSchedulerEndpoint(endpoint)
	accounts, err := s.adminService.ListAccountsForSchedulerScoreFilter(ctx, "", "", "", "", groupID, "")
	if err != nil {
		return nil, fmt.Errorf("list group accounts: %w", err)
	}

	accountIDs := make([]int64, 0, len(accounts))
	loadRequests := make([]AccountWithConcurrency, 0, len(accounts))
	for i := range accounts {
		accountIDs = append(accountIDs, accounts[i].ID)
		loadRequests = append(loadRequests, AccountWithConcurrency{ID: accounts[i].ID, MaxConcurrency: accounts[i].EffectiveLoadFactor()})
	}
	qualityScopes := smartSchedulerQualityScopes(requestedModel, endpoint)
	scopedQuality := make([]smartSchedulerScopedQuality, 0, len(qualityScopes))
	if s.dashboardService != nil {
		for _, scope := range qualityScopes {
			quality, qualityErr := s.dashboardService.GetSmartSchedulerQualityStatsBatch(ctx, accountIDs, now, scope.RequestedModel, scope.Endpoint)
			if qualityErr != nil {
				return nil, qualityErr
			}
			scopedQuality = append(scopedQuality, smartSchedulerScopedQuality{Scope: scope, Stats: quality})
		}
	}
	errors := make(map[int64]SmartSchedulerErrorStats, len(accounts))
	accountErrors := make(map[int64]SmartSchedulerErrorStats, len(accounts))
	capacityLimitedCount := int64(0)
	if s.dashboardService != nil {
		exactScope := qualityScopes[0]
		errors, err = s.dashboardService.GetSmartSchedulerErrorStatsBatch(ctx, accountIDs, now, exactScope.RequestedModel, exactScope.Endpoint)
		if err != nil {
			return nil, err
		}
		accountErrors = errors
		if exactScope.RequestedModel != "" || exactScope.Endpoint != "any" {
			accountErrors, err = s.dashboardService.GetSmartSchedulerAccountCircuitStatsBatch(ctx, accountIDs, now)
			if err != nil {
				return nil, err
			}
		}
		capacityLimitedCount, err = s.dashboardService.GetSmartSchedulerCapacityLimitedCount(ctx, groupID, now, requestedModel, endpoint)
		if err != nil {
			return nil, err
		}
	}
	loads := make(map[int64]*AccountLoadInfo)
	loadSnapshotAvailable := len(loadRequests) == 0
	warnings := make([]string, 0, 1)
	if s.concurrency != nil && len(loadRequests) > 0 {
		loads, err = s.concurrency.GetAccountsLoadBatch(ctx, loadRequests)
		if err != nil {
			loads = nil
			warnings = append(warnings, "实时负载快照读取失败，本次评分已剔除负载因子")
		} else if len(loads) != len(loadRequests) {
			warnings = append(warnings, "实时负载快照不完整，缺失账号的评分已剔除负载因子")
		} else {
			loadSnapshotAvailable = true
		}
	} else if len(loadRequests) > 0 {
		warnings = append(warnings, "实时负载服务不可用，本次评分已剔除负载因子")
	}
	recoveryProbeStates := make(map[int64]GroupRecoveryProbeState)
	if probeModel, ok := smartSchedulerRecoveryProbeModel(group); ok && s.recoveryProbe != nil {
		states, stateErr := s.recoveryProbe.ListStates(ctx, group.ID, accountIDs, probeModel)
		if stateErr != nil {
			warnings = append(warnings, "恢复探针状态读取失败，本次预览沿用既有智能调度结果")
		} else {
			recoveryProbeStates = states
		}
	}
	items := make([]SmartSchedulerPreviewItem, 0, len(accounts))
	for i := range accounts {
		quality, evidenceScope, evidenceFallback := selectSmartSchedulerQualityEvidence(accounts[i].ID, scopedQuality)
		item := buildSmartSchedulerPreviewItem(&accounts[i], group, requestedModel, endpoint, quality, errors[accounts[i].ID], loads[accounts[i].ID])
		item.EvidenceScope = evidenceScope
		item.EvidenceFallback = evidenceFallback
		applySmartSchedulerEvidencePolicy(&item)
		applySmartSchedulerAccountCircuitBreaker(&item, accountErrors[accounts[i].ID])
		if state, ok := recoveryProbeStates[accounts[i].ID]; ok {
			applyGroupRecoveryProbeStateToSchedulerItem(&item, &state)
		}
		items = append(items, item)
	}
	for i := range items {
		items[i].RawScore = smartSchedulerScore(items[i], items)
		items[i].Confidence = smartSchedulerConfidence(items[i])
		items[i].ConfidenceLabel = smartSchedulerConfidenceLabel(items[i].Confidence)
	}
	applySmartSchedulerConfidenceAdjustment(items)
	explorationRate := applySmartSchedulerExplorationPreview(items)
	sortSmartSchedulerItems(items)
	for i := range items {
		items[i].Rank = i + 1
	}

	preview := &SmartSchedulerPreview{
		Group:                   SmartSchedulerGroupSummary{ID: group.ID, Name: group.Name},
		Platform:                group.Platform,
		RequestedModel:          requestedModel,
		Endpoint:                endpoint,
		AlgorithmVersion:        SmartSchedulerPreviewAlgorithmVersion,
		GeneratedAt:             now.UTC(),
		TotalAccounts:           len(items),
		ExplorationRate:         explorationRate,
		ProductionControlActive: group.SmartSchedulerEnabled,
		LoadSnapshotAvailable:   loadSnapshotAvailable,
		CapacityLimitedCount1h:  capacityLimitedCount,
		Warnings:                warnings,
		Items:                   items,
	}
	for _, item := range items {
		switch item.Pool {
		case "primary":
			preview.PrimaryCount++
		case "warm":
			preview.WarmCount++
		default:
			preview.IsolatedCount++
		}
	}
	return preview, nil
}

func smartSchedulerQualityScopes(requestedModel, endpoint string) []smartSchedulerQualityScope {
	requestedModel = strings.TrimSpace(requestedModel)
	endpoint = smartSchedulerStoredEndpoint(endpoint)
	hasModel := requestedModel != ""
	hasEndpoint := endpoint != "any"

	scopes := make([]smartSchedulerQualityScope, 0, 4)
	switch {
	case hasModel && hasEndpoint:
		scopes = append(scopes,
			smartSchedulerQualityScope{Name: smartSchedulerEvidenceModelEndpoint, RequestedModel: requestedModel, Endpoint: endpoint},
			smartSchedulerQualityScope{Name: smartSchedulerEvidenceModel, RequestedModel: requestedModel, Endpoint: "any", Fallback: true},
			smartSchedulerQualityScope{Name: smartSchedulerEvidenceEndpoint, Endpoint: endpoint, Fallback: true},
			smartSchedulerQualityScope{Name: smartSchedulerEvidenceAccount, Endpoint: "any", Fallback: true},
		)
	case hasModel:
		scopes = append(scopes,
			smartSchedulerQualityScope{Name: smartSchedulerEvidenceModel, RequestedModel: requestedModel, Endpoint: "any"},
			smartSchedulerQualityScope{Name: smartSchedulerEvidenceAccount, Endpoint: "any", Fallback: true},
		)
	case hasEndpoint:
		scopes = append(scopes,
			smartSchedulerQualityScope{Name: smartSchedulerEvidenceEndpoint, Endpoint: endpoint},
			smartSchedulerQualityScope{Name: smartSchedulerEvidenceAccount, Endpoint: "any", Fallback: true},
		)
	default:
		scopes = append(scopes, smartSchedulerQualityScope{Name: smartSchedulerEvidenceAccount, Endpoint: "any"})
	}
	return scopes
}

func smartSchedulerStoredEndpoint(endpoint string) string {
	switch normalizeSmartSchedulerEndpoint(endpoint) {
	case "chat_completions":
		return "/v1/chat/completions"
	case "responses":
		return "/v1/responses"
	case "messages":
		return "/v1/messages"
	case "gemini_models":
		return "/v1beta/models"
	default:
		return "any"
	}
}

func smartSchedulerRecoveryProbeModel(group *Group) (string, bool) {
	if group == nil || !group.RecoveryProbeEnabled {
		return "", false
	}
	configured := strings.TrimSpace(group.RecoveryProbeModel)
	if configured == "" {
		return "", false
	}
	// The configured probe is the account-level recovery gate for the whole
	// group. Its state must therefore apply even when live traffic requests a
	// different model from the inexpensive model used by the probe itself.
	return configured, true
}

func selectSmartSchedulerQualityEvidence(accountID int64, scoped []smartSchedulerScopedQuality) (AccountQualityStats, string, bool) {
	if len(scoped) == 0 {
		return AccountQualityStats{}, smartSchedulerEvidenceAccount, false
	}
	exact := scoped[0].Stats[accountID]
	for _, candidate := range scoped {
		quality := candidate.Stats[accountID]
		if !hasSmartSchedulerQualityEvidence(quality) {
			continue
		}
		quality.Activity = exact.Activity
		return quality, candidate.Scope.Name, candidate.Scope.Fallback
	}
	return exact, scoped[0].Scope.Name, false
}

func hasSmartSchedulerQualityEvidence(quality AccountQualityStats) bool {
	return quality.Recent1h.Last10.QualityScore != nil ||
		quality.Recent1h.Last100.QualityScore != nil ||
		quality.Last10.QualityScore != nil ||
		quality.Last100.QualityScore != nil
}

func applySmartSchedulerEvidencePolicy(item *SmartSchedulerPreviewItem) {
	if item == nil || !item.EvidenceFallback || item.Pool != "primary" {
		return
	}
	item.Pool = "warm"
	item.Decision = "observe"
	item.Reason = "仅有回退质量证据，需探索验证"
}

func normalizeSmartSchedulerEndpoint(endpoint string) string {
	endpoint = strings.ToLower(strings.TrimSpace(endpoint))
	if endpoint == "" {
		return "any"
	}
	switch endpoint {
	case "chat", "chat_completions", "/v1/chat/completions":
		return "chat_completions"
	case "responses", "/v1/responses":
		return "responses"
	case "messages", "/v1/messages":
		return "messages"
	case "gemini", "gemini_models", "/v1beta/models":
		return "gemini_models"
	case "any":
		return "any"
	default:
		return "any"
	}
}

func buildSmartSchedulerPreviewItem(account *Account, group *Group, requestedModel, endpoint string, quality AccountQualityStats, errors SmartSchedulerErrorStats, load *AccountLoadInfo) SmartSchedulerPreviewItem {
	item := SmartSchedulerPreviewItem{
		AccountID:                       account.ID,
		AccountName:                     account.Name,
		Platform:                        account.Platform,
		Priority:                        accountGroupPriority(account, group.ID),
		Status:                          account.Status,
		Schedulable:                     account.Schedulable,
		Pool:                            "warm",
		Decision:                        "observe",
		Reason:                          "近1小时无足够真实流式样本",
		Quality1h:                       quality.Recent1h,
		Quality24h:                      AccountQualityPeriod{Last10: quality.Last10, Last100: quality.Last100, WindowHours: quality.WindowHours},
		Activity:                        quality.Activity,
		ErrorSuccessfulRequestCount:     errors.SuccessfulRequestCount,
		ProviderFailureCount:            errors.ProviderFailureCount,
		ProviderTransientFailureCount:   errors.ProviderTransientFailureCount,
		RateLimitCount:                  errors.RateLimitCount,
		ClientExcludedCount:             errors.ClientExcludedCount,
		PlatformFailureCount:            errors.PlatformFailureCount,
		UncertainFailureCount:           errors.UncertainFailureCount,
		RecentProviderFailureCount:      errors.RecentProviderFailureCount,
		RecentProviderTransientCount:    errors.RecentProviderTransientCount,
		RecentRateLimitCount:            errors.RecentRateLimitCount,
		RecentUncertainFailureCount:     errors.RecentUncertainFailureCount,
		ImmediateProviderFailureCount:   errors.ImmediateProviderFailureCount,
		ImmediateProviderTransientCount: errors.ImmediateProviderTransientCount,
		ImmediateRateLimitCount:         errors.ImmediateRateLimitCount,
		ImmediateUncertainFailureCount:  errors.ImmediateUncertainFailureCount,
		CostMultiplier:                  account.BillingRateMultiplier(),
		LastUsedAt:                      account.LastUsedAt,
		ModelSupported:                  true,
		EndpointSupported:               true,
	}
	if load != nil {
		item.Load = &SmartSchedulerLoad{
			CurrentConcurrency: load.CurrentConcurrency,
			WaitingCount:       load.WaitingCount,
			LoadRate:           load.LoadRate,
			MaxConcurrency:     account.EffectiveLoadFactor(),
		}
	}
	relevantRecentFailures := smartSchedulerRecentSupplierFailures(errors)
	item.Activity.FailedRequestCount = relevantRecentFailures
	item.Activity.State = classifyAccountQualityActivity(item.Activity.SuccessfulRequestCount, relevantRecentFailures)

	if group.Platform != "composite" && !strings.EqualFold(account.Platform, group.Platform) {
		return isolateSmartSchedulerItem(item, "账号平台与分组不匹配")
	}
	if !smartSchedulerModelSupported(account, requestedModel, &item.ModelMapping) {
		item.ModelSupported = false
		return isolateSmartSchedulerItem(item, "模型不支持")
	}
	capabilityCtx := WithSmartSchedulerEndpoint(context.Background(), endpoint)
	if account.getDynamicModelCapabilityRemainingWithContext(capabilityCtx, requestedModel) > 0 {
		item.ModelSupported = false
		return isolateSmartSchedulerItem(item, "模型能力暂时未验证，冷却后自动重试")
	}
	if !smartSchedulerEndpointSupported(account, endpoint) {
		item.EndpointSupported = false
		return isolateSmartSchedulerItem(item, "账号不支持所选端点")
	}
	if !account.IsSchedulable() {
		return isolateSmartSchedulerItem(item, smartSchedulerUnschedulableReason(account))
	}
	if quality.Activity.SuccessfulRequestCount == 0 && smartSchedulerRecentSupplierFailures(errors) >= accountQualityFailingMinErrors {
		if smartSchedulerRecentHardFailures(errors) > 0 {
			return isolateSmartSchedulerItem(item, "近1小时上游持续失败")
		}
		return isolateSmartSchedulerSoftFailure(item, "近1小时上游持续失败")
	}
	if quality.Recent1h.Last10.QualityScore != nil || quality.Recent1h.Last100.QualityScore != nil {
		item.Pool = "primary"
		item.Decision = "primary_candidate"
		item.Reason = "有近期真实流式质量证据"
	}
	if item.Pool == "primary" && smartSchedulerImmediateSupplierFailures(errors) >= smartSchedulerImmediateFailures {
		item.Pool = "warm"
		item.Decision = "observe"
		item.Reason = "近5分钟上游连续失败，临时降级观察"
	}
	if item.Load != nil && (item.Load.LoadRate >= 90 || item.Load.WaitingCount > 0) {
		item.Pool = "warm"
		item.Decision = "observe"
		item.Reason = "实时负载接近饱和"
	}
	return item
}

func isolateSmartSchedulerItem(item SmartSchedulerPreviewItem, reason string) SmartSchedulerPreviewItem {
	item.Pool = "isolated"
	item.Decision = "excluded"
	item.Reason = reason
	item.SoftIsolation = false
	return item
}

func isolateSmartSchedulerSoftFailure(item SmartSchedulerPreviewItem, reason string) SmartSchedulerPreviewItem {
	item = isolateSmartSchedulerItem(item, reason)
	item.SoftIsolation = true
	return item
}

func applySmartSchedulerAccountCircuitBreaker(item *SmartSchedulerPreviewItem, stats SmartSchedulerErrorStats) {
	if item == nil || (item.Pool == "isolated" && !item.SoftIsolation) {
		return
	}
	hardFailures := smartSchedulerImmediateHardFailures(stats)
	softFailures := smartSchedulerImmediateSoftFailures(stats)
	totalFailures := hardFailures + softFailures
	if totalFailures >= smartSchedulerImmediateFailures {
		if hardFailures > 0 {
			*item = isolateSmartSchedulerItem(*item, "账号近5分钟跨模型连续硬失败，短暂熔断")
			return
		}
		*item = isolateSmartSchedulerSoftFailure(*item, "账号近5分钟跨模型连续上游失败，短暂熔断")
		item.SoftIsolationFailureCount = totalFailures
		return
	}
	if softFailures >= smartSchedulerImmediateFailures-1 && item.Pool == "primary" {
		item.Pool = "warm"
		item.Decision = "observe"
		item.Reason = "账号级熔断恢复观察，等待单次探测"
	}
}

func accountGroupPriority(account *Account, groupID int64) *int {
	for _, binding := range account.AccountGroups {
		if binding.GroupID == groupID {
			priority := binding.Priority
			return &priority
		}
	}
	return nil
}

func smartSchedulerModelSupported(account *Account, requestedModel string, mappingOut *string) bool {
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" {
		return true
	}
	if account.Platform == PlatformAntigravity {
		mapped := mapAntigravityModel(account, requestedModel)
		if mapped == "" {
			return false
		}
		setSmartSchedulerModelMapping(mappingOut, requestedModel, mapped)
		return true
	}
	if account.IsBedrock() {
		mapped, ok := ResolveBedrockModelID(account, requestedModel)
		if ok {
			setSmartSchedulerModelMapping(mappingOut, requestedModel, mapped)
		}
		return ok
	}
	if account.Platform == PlatformOpenAI && account.IsOpenAIPassthroughEnabled() {
		return true
	}
	lookupModel := requestedModel
	if account.Platform == PlatformAnthropic && account.Type != AccountTypeAPIKey {
		if account.Type == AccountTypeServiceAccount {
			lookupModel = normalizeVertexAnthropicModelID(claude.NormalizeModelID(requestedModel))
		} else {
			lookupModel = claude.NormalizeModelID(requestedModel)
		}
	}
	if !account.IsModelSupported(lookupModel) {
		return false
	}
	mapped, matched := account.ResolveMappedModel(lookupModel)
	if matched {
		setSmartSchedulerModelMapping(mappingOut, requestedModel, mapped)
	} else {
		setSmartSchedulerModelMapping(mappingOut, requestedModel, lookupModel)
	}
	return true
}

func setSmartSchedulerModelMapping(mappingOut *string, requestedModel, mappedModel string) {
	if mappingOut == nil || strings.EqualFold(strings.TrimSpace(requestedModel), strings.TrimSpace(mappedModel)) {
		return
	}
	*mappingOut = mappedModel
}

func smartSchedulerRecentSupplierFailures(stats SmartSchedulerErrorStats) int64 {
	return stats.RecentProviderFailureCount + stats.RecentProviderTransientCount + stats.RecentRateLimitCount + stats.RecentUncertainFailureCount
}

func smartSchedulerRecentHardFailures(stats SmartSchedulerErrorStats) int64 {
	return stats.RecentProviderFailureCount + stats.RecentRateLimitCount
}

func smartSchedulerImmediateHardFailures(stats SmartSchedulerErrorStats) int64 {
	return stats.ImmediateProviderFailureCount + stats.ImmediateRateLimitCount
}

func smartSchedulerImmediateSoftFailures(stats SmartSchedulerErrorStats) int64 {
	return stats.ImmediateProviderTransientCount + stats.ImmediateUncertainFailureCount
}

func smartSchedulerImmediateSupplierFailures(stats SmartSchedulerErrorStats) int64 {
	return stats.ImmediateProviderFailureCount + stats.ImmediateProviderTransientCount + stats.ImmediateRateLimitCount + stats.ImmediateUncertainFailureCount
}

func smartSchedulerEndpointSupported(account *Account, endpoint string) bool {
	if account == nil || endpoint == "any" || endpoint == "" {
		return account != nil
	}
	if !account.IsOpenAICompatible() {
		return true
	}
	switch endpoint {
	case "chat_completions", "responses", "messages":
		// Text Responses and the OpenAI-compatible Messages bridge both use the
		// chat-completions scheduler capability in production. Image intent is
		// deliberately outside this text-quality preview.
		return account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityChatCompletions)
	default:
		return true
	}
}

func smartSchedulerUnschedulableReason(account *Account) string {
	now := time.Now()
	if !account.IsActive() {
		return "账号未启用"
	}
	if !account.Schedulable {
		return "当前账号已暂停调度"
	}
	if account.AutoPauseOnExpired && account.ExpiresAt != nil && !now.Before(*account.ExpiresAt) {
		return "账号已过期"
	}
	if account.RateLimitResetAt != nil && now.Before(*account.RateLimitResetAt) {
		return "账号处于限流冷却"
	}
	if account.OverloadUntil != nil && now.Before(*account.OverloadUntil) {
		return "账号处于过载冷却"
	}
	if account.TempUnschedulableUntil != nil && now.Before(*account.TempUnschedulableUntil) {
		if strings.TrimSpace(account.TempUnschedulableReason) != "" {
			return "临时不可调度：" + account.TempUnschedulableReason
		}
		return "账号处于临时不可调度状态"
	}
	if account.IsAPIKeyOrBedrock() && account.IsQuotaExceeded() {
		return "账号额度已耗尽"
	}
	return "当前不可调度"
}

func smartSchedulerScore(item SmartSchedulerPreviewItem, all []SmartSchedulerPreviewItem) *float64 {
	if item.Pool == "isolated" {
		return nil
	}
	recent := weightedQualityScore(item.Quality1h.Last10, item.Quality1h.Last100, smartSchedulerRecentLast10, smartSchedulerRecentLast100)
	stable := weightedQualityScore(item.Quality24h.Last10, item.Quality24h.Last100, smartSchedulerStableLast10, smartSchedulerStableLast100)
	qualityScore := smartSchedulerQualityComponent(recent, stable)
	if qualityScore == nil {
		if item.ProbeBootstrap {
			return smartSchedulerProbeBootstrapScore(item, all)
		}
		return nil
	}
	errorScore := smartSchedulerReliabilityScore(item)
	loadScore := 0.0
	if item.Load != nil {
		loadScore = math.Max(0, 100-float64(item.Load.LoadRate))
		if item.Load.WaitingCount > 0 {
			loadScore = math.Max(0, loadScore-float64(item.Load.WaitingCount)*5)
		}
	}
	costScore := relativeCostScore(item, all)
	score := *qualityScore + errorScore*smartSchedulerErrorWeight + costScore*smartSchedulerCostWeight
	appliedWeight := smartSchedulerRecentWeight + smartSchedulerStableWeight + smartSchedulerErrorWeight + smartSchedulerCostWeight
	if item.Load != nil {
		score += loadScore * smartSchedulerLoadWeight
		appliedWeight += smartSchedulerLoadWeight
	}
	score /= appliedWeight
	if item.Activity.State == accountQualityActivityDegraded {
		score = math.Min(score, 69)
	}
	if score > 100 {
		score = 100
	}
	score = math.Round(score*100) / 100
	return &score
}

func smartSchedulerProbeBootstrapScore(item SmartSchedulerPreviewItem, all []SmartSchedulerPreviewItem) *float64 {
	probeQuality := 60.0
	if item.RecoveryProbe != nil && item.RecoveryProbe.LatencyMs > 0 {
		latency := float64(item.RecoveryProbe.LatencyMs)
		if score, ok := qualityCurveScore(&latency, accountQualityTTFTCurve); ok {
			probeQuality = score
		}
	}
	costScore := relativeCostScore(item, all)
	score := probeQuality*smartSchedulerProbeBootstrapQualityWeight + costScore*smartSchedulerProbeBootstrapCostWeight
	score = math.Max(smartSchedulerProbeBootstrapScoreMin, math.Min(smartSchedulerProbeBootstrapScoreMax, score))
	score = math.Round(score*100) / 100
	return &score
}

func smartSchedulerReliabilityScore(item SmartSchedulerPreviewItem) float64 {
	recentScore, recentOK := smartSchedulerReliabilityWindowScore(
		item.Activity.SuccessfulRequestCount,
		item.RecentProviderFailureCount,
		item.RecentProviderTransientCount,
		item.RecentRateLimitCount,
		item.RecentUncertainFailureCount,
	)
	stableScore, stableOK := smartSchedulerReliabilityWindowScore(
		item.ErrorSuccessfulRequestCount,
		item.ProviderFailureCount,
		item.ProviderTransientFailureCount,
		item.RateLimitCount,
		item.UncertainFailureCount,
	)
	switch {
	case recentOK && stableOK:
		return recentScore*smartSchedulerRecentErrorWeight + stableScore*smartSchedulerStableErrorWeight
	case recentOK:
		return recentScore
	case stableOK:
		return stableScore
	default:
		return 100
	}
}

func smartSchedulerReliabilityWindowScore(successes, providerFailures, transientFailures, rateLimits, uncertainFailures int64) (float64, bool) {
	attempts := successes + providerFailures + transientFailures + rateLimits + uncertainFailures
	if attempts == 0 {
		return 0, false
	}
	burden := float64(providerFailures+transientFailures) + float64(rateLimits)*0.5 + float64(uncertainFailures)*0.25
	return math.Max(0, 100-burden/float64(attempts)*100), true
}

func smartSchedulerQualityComponent(recent, stable *float64) *float64 {
	if recent == nil && stable == nil {
		return nil
	}
	value := 0.0
	if recent != nil && stable != nil {
		value = *recent*smartSchedulerRecentWeight + *stable*smartSchedulerStableWeight
	} else if recent != nil {
		value = *recent * (smartSchedulerRecentWeight + smartSchedulerStableWeight)
	} else {
		value = *stable * (smartSchedulerRecentWeight + smartSchedulerStableWeight)
	}
	return &value
}

func weightedQualityScore(last10, last100 AccountQualityWindow, weight10, weight100 float64) *float64 {
	var sum, weight float64
	if last10.QualityScore != nil {
		sum += float64(*last10.QualityScore) * weight10
		weight += weight10
	}
	if last100.QualityScore != nil {
		sum += float64(*last100.QualityScore) * weight100
		weight += weight100
	}
	if weight == 0 {
		return nil
	}
	value := sum / weight
	return &value
}

func applySmartSchedulerQualityScore(window AccountQualityWindow) AccountQualityWindow {
	if window.SampleCount < accountQualityMinSamples {
		return window
	}

	var ttftScore *float64
	if window.FirstTokenSampleCount >= accountQualityMinTTFTSamples && window.P50FirstTokenMs != nil && window.P90FirstTokenMs != nil {
		routingTTFT := *window.P50FirstTokenMs*smartSchedulerRobustMedian + *window.P90FirstTokenMs*smartSchedulerRobustTail
		window.RoutingFirstTokenMs = &routingTTFT
		if score, ok := qualityCurveScore(&routingTTFT, accountQualityTTFTCurve); ok {
			ttftScore = &score
		}
	}

	var generationScore *float64
	if window.GenerationSampleCount >= accountQualityMinSamples && window.P50GenerationTokensPerSecond != nil && window.P10GenerationTokensPerSecond != nil {
		routingGeneration := *window.P50GenerationTokensPerSecond*smartSchedulerRobustMedian + *window.P10GenerationTokensPerSecond*smartSchedulerRobustTail
		window.RoutingGenerationTokensPerSecond = &routingGeneration
		score := smartSchedulerGenerationScore(routingGeneration)
		generationScore = &score
	}

	var score float64
	switch {
	case ttftScore != nil && generationScore != nil:
		score = *ttftScore*smartSchedulerTTFTWeight + *generationScore*smartSchedulerGenerationWeight
		window.ScoreBasis = smartSchedulerBasisTTFTGeneration
	case ttftScore != nil:
		score = *ttftScore
		window.ScoreBasis = smartSchedulerBasisTTFTOnly
	case generationScore != nil:
		score = math.Min(*generationScore, accountQualityDurationOnlyMax)
		window.ScoreBasis = smartSchedulerBasisGenerationOnly
	default:
		return applyAccountQualityScore(window)
	}

	rounded := int(math.Round(math.Max(0, math.Min(100, score))))
	window.QualityScore = &rounded
	window.QualityGrade = accountQualityGrade(rounded)
	return window
}

func smartSchedulerGenerationScore(tokensPerSecond float64) float64 {
	if tokensPerSecond <= smartSchedulerGenerationCurve[0].LatencyMs {
		return smartSchedulerGenerationCurve[0].Score
	}
	for i := 1; i < len(smartSchedulerGenerationCurve); i++ {
		current := smartSchedulerGenerationCurve[i]
		if tokensPerSecond > current.LatencyMs {
			continue
		}
		previous := smartSchedulerGenerationCurve[i-1]
		ratio := (tokensPerSecond - previous.LatencyMs) / (current.LatencyMs - previous.LatencyMs)
		return previous.Score + ratio*(current.Score-previous.Score)
	}
	return smartSchedulerGenerationCurve[len(smartSchedulerGenerationCurve)-1].Score
}

func relativeCostScore(item SmartSchedulerPreviewItem, all []SmartSchedulerPreviewItem) float64 {
	minCost := math.Inf(1)
	for _, candidate := range all {
		if candidate.Pool == "isolated" || candidate.CostMultiplier <= 0 {
			continue
		}
		minCost = math.Min(minCost, candidate.CostMultiplier)
	}
	if math.IsInf(minCost, 1) || item.CostMultiplier <= 0 {
		return 50
	}
	return math.Min(100, minCost/item.CostMultiplier*100)
}

func smartSchedulerConfidence(item SmartSchedulerPreviewItem) float64 {
	if item.Pool == "isolated" {
		return 0
	}
	if item.ProbeBootstrap {
		return smartSchedulerProbeBootstrapConfidence
	}
	count1h := item.Quality1h.Last100.SampleCount
	count24h := item.Quality24h.Last100.SampleCount
	confidence := math.Sqrt(math.Min(1, float64(count1h)/100)*0.6 + math.Min(1, float64(count24h)/100)*0.4)
	if item.Load == nil {
		confidence *= 0.9
	}
	if item.Activity.State == accountQualityActivityIdle {
		confidence *= 0.7
	}
	if item.EvidenceFallback {
		switch item.EvidenceScope {
		case smartSchedulerEvidenceModel, smartSchedulerEvidenceEndpoint:
			confidence *= 0.85
		case smartSchedulerEvidenceAccount:
			confidence *= 0.65
		default:
			confidence *= 0.75
		}
	}
	return math.Round(confidence*100) / 100
}

func applySmartSchedulerConfidenceAdjustment(items []SmartSchedulerPreviewItem) {
	rawScores := make([]float64, 0, len(items))
	for _, item := range items {
		if item.Pool != "isolated" && item.RawScore != nil {
			rawScores = append(rawScores, *item.RawScore)
		}
	}
	if len(rawScores) == 0 {
		return
	}
	sort.Float64s(rawScores)
	median := rawScores[len(rawScores)/2]
	if len(rawScores)%2 == 0 {
		median = (rawScores[len(rawScores)/2-1] + median) / 2
	}
	for i := range items {
		if items[i].Pool == "isolated" || items[i].RawScore == nil {
			items[i].Score = nil
			continue
		}
		confidence := math.Max(0, math.Min(1, items[i].Confidence))
		adjusted := median + confidence*(*items[i].RawScore-median)
		if items[i].ProbeBootstrap {
			adjusted = math.Min(adjusted, smartSchedulerProbeBootstrapScoreMax)
		}
		adjusted = math.Round(math.Max(0, math.Min(100, adjusted))*100) / 100
		items[i].Score = &adjusted
	}
}

func applySmartSchedulerExplorationPreview(items []SmartSchedulerPreviewItem) float64 {
	eligibleCount := 0
	candidateCount := 0
	bootstrapCandidateCount := 0
	for i := range items {
		item := &items[i]
		if !smartSchedulerExplorationEligible(*item) {
			continue
		}
		eligibleCount++
		if item.Pool == "warm" {
			item.ExplorationCandidate = true
			candidateCount++
			if item.ProbeBootstrap {
				bootstrapCandidateCount++
			}
		}
	}
	if candidateCount == 0 || eligibleCount == 0 {
		return 0
	}
	rate := smartSchedulerExplorationBase + (smartSchedulerExplorationMax-smartSchedulerExplorationBase)*float64(candidateCount)/float64(eligibleCount)
	if bootstrapCandidateCount > 0 {
		rate = math.Max(rate, smartSchedulerProbeBootstrapExplorationRate)
	}
	return math.Round(math.Min(smartSchedulerProbeBootstrapExplorationRate, rate)*1000) / 1000
}

func smartSchedulerExplorationEligible(item SmartSchedulerPreviewItem) bool {
	if item.Pool == "isolated" || !item.Schedulable || !item.ModelSupported || !item.EndpointSupported {
		return false
	}
	if item.Load != nil && (item.Load.LoadRate >= 90 || item.Load.WaitingCount > 0) {
		return false
	}
	return item.Activity.State != accountQualityActivityFailing && item.Activity.State != accountQualityActivityDegraded
}

func smartSchedulerConfidenceLabel(confidence float64) string {
	if confidence >= 0.75 {
		return "high"
	}
	if confidence >= 0.4 {
		return "medium"
	}
	return "low"
}

func sortSmartSchedulerItems(items []SmartSchedulerPreviewItem) {
	poolRank := func(pool string) int {
		switch pool {
		case "primary":
			return 0
		case "warm":
			return 1
		default:
			return 2
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		return poolRank(items[i].Pool) < poolRank(items[j].Pool)
	})
	for start := 0; start < len(items); {
		end := start + 1
		for end < len(items) && poolRank(items[end].Pool) == poolRank(items[start].Pool) {
			end++
		}
		sortSmartSchedulerPool(items[start:end])
		start = end
	}
}

func sortSmartSchedulerPool(items []SmartSchedulerPreviewItem) {
	for position := 0; position < len(items); position++ {
		bestScore := -1.0
		for i := position; i < len(items); i++ {
			if items[i].Score != nil {
				bestScore = math.Max(bestScore, *items[i].Score)
			}
		}
		selected := -1
		for i := position; i < len(items); i++ {
			score := -1.0
			if items[i].Score != nil {
				score = *items[i].Score
			}
			if bestScore >= 0 && (score < 0 || bestScore-score > smartSchedulerCostTolerance) {
				continue
			}
			if selected < 0 || smartSchedulerItemPreferred(items[i], items[selected]) {
				selected = i
			}
		}
		if selected < 0 {
			selected = position
		}
		if selected == position {
			continue
		}
		chosen := items[selected]
		copy(items[position+1:selected+1], items[position:selected])
		items[position] = chosen
	}
}

func smartSchedulerItemPreferred(left, right SmartSchedulerPreviewItem) bool {
	if left.CostMultiplier > 0 && right.CostMultiplier > 0 && left.CostMultiplier != right.CostMultiplier {
		return left.CostMultiplier < right.CostMultiplier
	}
	leftScore, rightScore := -1.0, -1.0
	if left.Score != nil {
		leftScore = *left.Score
	}
	if right.Score != nil {
		rightScore = *right.Score
	}
	if leftScore != rightScore {
		return leftScore > rightScore
	}
	leftPriority, rightPriority := 1<<30, 1<<30
	if left.Priority != nil {
		leftPriority = *left.Priority
	}
	if right.Priority != nil {
		rightPriority = *right.Priority
	}
	if leftPriority != rightPriority {
		return leftPriority < rightPriority
	}
	return left.AccountID < right.AccountID
}
