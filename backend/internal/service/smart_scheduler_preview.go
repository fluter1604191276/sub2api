package service

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
)

const SmartSchedulerPreviewAlgorithmVersion = "preview-v1"

const (
	smartSchedulerRecentWeight  = 0.45
	smartSchedulerStableWeight  = 0.20
	smartSchedulerErrorWeight   = 0.15
	smartSchedulerCostWeight    = 0.15
	smartSchedulerLoadWeight    = 0.05
	smartSchedulerRecentLast10  = 0.70
	smartSchedulerRecentLast100 = 0.30
	smartSchedulerStableLast10  = 0.30
	smartSchedulerStableLast100 = 0.70
)

type SmartSchedulerErrorStats struct {
	SuccessfulRequestCount        int64 `json:"successful_request_count"`
	ProviderFailureCount          int64 `json:"provider_failure_count"`
	ProviderTransientFailureCount int64 `json:"provider_transient_failure_count"`
	RateLimitCount                int64 `json:"rate_limit_count"`
	ClientExcludedCount           int64 `json:"client_excluded_count"`
	PlatformFailureCount          int64 `json:"platform_failure_count"`
	UncertainFailureCount         int64 `json:"uncertain_failure_count"`
	RecentProviderFailureCount    int64 `json:"recent_provider_failure_count"`
	RecentProviderTransientCount  int64 `json:"recent_provider_transient_count"`
	RecentRateLimitCount          int64 `json:"recent_rate_limit_count"`
}

type smartSchedulerErrorStatsReader interface {
	GetSmartSchedulerErrorStatsBatch(ctx context.Context, accountIDs []int64, startTime, endTime time.Time) (map[int64]SmartSchedulerErrorStats, error)
}

// GetSmartSchedulerErrorStatsBatch reads only classified streaming errors for
// the preview. The optional repository interface keeps existing test doubles
// and alternate storage implementations compatible.
func (s *DashboardService) GetSmartSchedulerErrorStatsBatch(ctx context.Context, accountIDs []int64, now time.Time) (map[int64]SmartSchedulerErrorStats, error) {
	result := make(map[int64]SmartSchedulerErrorStats, len(accountIDs))
	if len(accountIDs) == 0 {
		return result, nil
	}
	reader, ok := s.usageRepo.(smartSchedulerErrorStatsReader)
	if !ok {
		return result, nil
	}
	end := now.UTC()
	stats, err := reader.GetSmartSchedulerErrorStatsBatch(ctx, accountIDs, end.Add(-24*time.Hour), end)
	if err != nil {
		return nil, fmt.Errorf("get smart scheduler error stats failed: %w", err)
	}
	return stats, nil
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
	Warnings                []string                    `json:"warnings"`
	Items                   []SmartSchedulerPreviewItem `json:"items"`
}

type SmartSchedulerGroupSummary struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type SmartSchedulerPreviewItem struct {
	Rank                          int                    `json:"rank"`
	AccountID                     int64                  `json:"account_id"`
	AccountName                   string                 `json:"account_name"`
	Platform                      string                 `json:"platform"`
	Priority                      *int                   `json:"priority,omitempty"`
	Status                        string                 `json:"status"`
	Schedulable                   bool                   `json:"schedulable"`
	Pool                          string                 `json:"pool"`
	Decision                      string                 `json:"decision"`
	Reason                        string                 `json:"reason"`
	Score                         *float64               `json:"score,omitempty"`
	Confidence                    float64                `json:"confidence"`
	ConfidenceLabel               string                 `json:"confidence_label"`
	Quality1h                     AccountQualityPeriod   `json:"quality_1h"`
	Quality24h                    AccountQualityPeriod   `json:"quality_24h"`
	Activity                      AccountQualityActivity `json:"activity"`
	ErrorSuccessfulRequestCount   int64                  `json:"error_successful_request_count"`
	ProviderFailureCount          int64                  `json:"provider_failure_count"`
	ProviderTransientFailureCount int64                  `json:"provider_transient_failure_count"`
	RateLimitCount                int64                  `json:"rate_limit_count"`
	ClientExcludedCount           int64                  `json:"client_excluded_count"`
	PlatformFailureCount          int64                  `json:"platform_failure_count"`
	UncertainFailureCount         int64                  `json:"uncertain_failure_count"`
	CostMultiplier                float64                `json:"cost_multiplier"`
	Load                          *SmartSchedulerLoad    `json:"load,omitempty"`
	ModelSupported                bool                   `json:"model_supported"`
	EndpointSupported             bool                   `json:"endpoint_supported"`
	ModelMapping                  string                 `json:"model_mapping,omitempty"`
	LastUsedAt                    *time.Time             `json:"last_used_at,omitempty"`
}

type SmartSchedulerLoad struct {
	CurrentConcurrency int `json:"current_concurrency"`
	WaitingCount       int `json:"waiting_count"`
	LoadRate           int `json:"load_rate"`
	MaxConcurrency     int `json:"max_concurrency"`
}

type SmartSchedulerPreviewService struct {
	adminService     AdminService
	dashboardService *DashboardService
	concurrency      *ConcurrencyService
}

func NewSmartSchedulerPreviewService(adminService AdminService, dashboardService *DashboardService, concurrency *ConcurrencyService) *SmartSchedulerPreviewService {
	return &SmartSchedulerPreviewService{
		adminService:     adminService,
		dashboardService: dashboardService,
		concurrency:      concurrency,
	}
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
	quality := make(map[int64]AccountQualityStats, len(accounts))
	if s.dashboardService != nil {
		quality, err = s.dashboardService.GetAccountQualityStatsBatch(ctx, accountIDs, now)
		if err != nil {
			return nil, err
		}
	}
	errors := make(map[int64]SmartSchedulerErrorStats, len(accounts))
	if s.dashboardService != nil {
		errors, err = s.dashboardService.GetSmartSchedulerErrorStatsBatch(ctx, accountIDs, now)
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

	items := make([]SmartSchedulerPreviewItem, 0, len(accounts))
	for i := range accounts {
		item := buildSmartSchedulerPreviewItem(&accounts[i], group, requestedModel, endpoint, quality[accounts[i].ID], errors[accounts[i].ID], loads[accounts[i].ID])
		items = append(items, item)
	}
	for i := range items {
		items[i].Score = smartSchedulerScore(items[i], items)
		items[i].Confidence = smartSchedulerConfidence(items[i])
		items[i].ConfidenceLabel = smartSchedulerConfidenceLabel(items[i].Confidence)
	}
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
		ExplorationRate:         0,
		ProductionControlActive: false,
		LoadSnapshotAvailable:   loadSnapshotAvailable,
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
	case "any":
		return "any"
	default:
		return "any"
	}
}

func buildSmartSchedulerPreviewItem(account *Account, group *Group, requestedModel, endpoint string, quality AccountQualityStats, errors SmartSchedulerErrorStats, load *AccountLoadInfo) SmartSchedulerPreviewItem {
	item := SmartSchedulerPreviewItem{
		AccountID:                     account.ID,
		AccountName:                   account.Name,
		Platform:                      account.Platform,
		Priority:                      accountGroupPriority(account, group.ID),
		Status:                        account.Status,
		Schedulable:                   account.Schedulable,
		Pool:                          "warm",
		Decision:                      "observe",
		Reason:                        "近1小时无足够真实流式样本",
		Quality1h:                     quality.Recent1h,
		Quality24h:                    AccountQualityPeriod{Last10: quality.Last10, Last100: quality.Last100, WindowHours: quality.WindowHours},
		Activity:                      quality.Activity,
		ErrorSuccessfulRequestCount:   errors.SuccessfulRequestCount,
		ProviderFailureCount:          errors.ProviderFailureCount,
		ProviderTransientFailureCount: errors.ProviderTransientFailureCount,
		RateLimitCount:                errors.RateLimitCount,
		ClientExcludedCount:           errors.ClientExcludedCount,
		PlatformFailureCount:          errors.PlatformFailureCount,
		UncertainFailureCount:         errors.UncertainFailureCount,
		CostMultiplier:                account.BillingRateMultiplier(),
		LastUsedAt:                    account.LastUsedAt,
		ModelSupported:                true,
		EndpointSupported:             true,
	}
	if load != nil {
		item.Load = &SmartSchedulerLoad{
			CurrentConcurrency: load.CurrentConcurrency,
			WaitingCount:       load.WaitingCount,
			LoadRate:           load.LoadRate,
			MaxConcurrency:     account.EffectiveLoadFactor(),
		}
	}

	if group.Platform != "composite" && !strings.EqualFold(account.Platform, group.Platform) {
		return isolateSmartSchedulerItem(item, "账号平台与分组不匹配")
	}
	if !smartSchedulerModelSupported(account, requestedModel, &item.ModelMapping) {
		item.ModelSupported = false
		return isolateSmartSchedulerItem(item, "模型不支持")
	}
	if !smartSchedulerEndpointSupported(account, endpoint) {
		item.EndpointSupported = false
		return isolateSmartSchedulerItem(item, "账号不支持所选端点")
	}
	if !account.IsSchedulable() {
		return isolateSmartSchedulerItem(item, smartSchedulerUnschedulableReason(account))
	}
	if quality.Activity.SuccessfulRequestCount == 0 && smartSchedulerRecentSupplierFailures(errors) >= accountQualityFailingMinErrors {
		return isolateSmartSchedulerItem(item, "近1小时上游持续失败")
	}
	if quality.Recent1h.Last10.QualityScore != nil || quality.Recent1h.Last100.QualityScore != nil {
		item.Pool = "primary"
		item.Decision = "primary_candidate"
		item.Reason = "有近期真实流式质量证据"
	}
	return item
}

func isolateSmartSchedulerItem(item SmartSchedulerPreviewItem, reason string) SmartSchedulerPreviewItem {
	item.Pool = "isolated"
	item.Decision = "excluded"
	item.Reason = reason
	return item
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
	return stats.RecentProviderFailureCount + stats.RecentProviderTransientCount + stats.RecentRateLimitCount
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
		return nil
	}
	attempts := errorsEvaluatedRequestCount(item)
	errorBurden := float64(item.ProviderFailureCount+item.ProviderTransientFailureCount) + float64(item.RateLimitCount)*0.5 + float64(item.UncertainFailureCount)*0.25
	errorScore := 100.0
	if attempts > 0 {
		errorScore = math.Max(0, 100-(errorBurden/float64(attempts))*100)
	}
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

func errorsEvaluatedRequestCount(item SmartSchedulerPreviewItem) int64 {
	return item.ErrorSuccessfulRequestCount +
		item.ProviderFailureCount +
		item.ProviderTransientFailureCount +
		item.RateLimitCount +
		item.UncertainFailureCount
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

func relativeCostScore(item SmartSchedulerPreviewItem, all []SmartSchedulerPreviewItem) float64 {
	minCost, maxCost := math.Inf(1), math.Inf(-1)
	for _, candidate := range all {
		if candidate.Pool == "isolated" {
			continue
		}
		minCost = math.Min(minCost, candidate.CostMultiplier)
		maxCost = math.Max(maxCost, candidate.CostMultiplier)
	}
	if math.IsInf(minCost, 1) || maxCost <= minCost {
		return 50
	}
	return (maxCost - item.CostMultiplier) / (maxCost - minCost) * 100
}

func smartSchedulerConfidence(item SmartSchedulerPreviewItem) float64 {
	if item.Pool == "isolated" {
		return 0
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
	return math.Round(confidence*100) / 100
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
	sort.SliceStable(items, func(i, j int) bool {
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
		leftPool, rightPool := poolRank(items[i].Pool), poolRank(items[j].Pool)
		if leftPool != rightPool {
			return leftPool < rightPool
		}
		leftScore, rightScore := -1.0, -1.0
		if items[i].Score != nil {
			leftScore = *items[i].Score
		}
		if items[j].Score != nil {
			rightScore = *items[j].Score
		}
		if leftScore != rightScore {
			return leftScore > rightScore
		}
		leftPriority, rightPriority := 1<<30, 1<<30
		if items[i].Priority != nil {
			leftPriority = *items[i].Priority
		}
		if items[j].Priority != nil {
			rightPriority = *items[j].Priority
		}
		if leftPriority != rightPriority {
			return leftPriority < rightPriority
		}
		return items[i].AccountID < items[j].AccountID
	})
}
