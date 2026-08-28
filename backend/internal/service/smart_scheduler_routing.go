package service

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	smartStickyStrongMinScore        = 70.0
	smartStickyEliteMinScore         = 85.0
	smartStickyWeakQualityLead       = 3.0
	smartStickyStrongQualityLead     = 6.0
	smartStickyEliteQualityLead      = 10.0
	smartStickyStrongReviewInterval  = 5 * time.Minute
	smartStickyWeakReviewInterval    = time.Minute
	smartStickyReviewClaimTimeout    = 15 * time.Second
	smartStickyReviewStateTTL        = 24 * time.Hour
	smartStickyReviewStateLimit      = 4096
	smartStickyStrongSwitchCooldown  = 10 * time.Minute
	smartStickyWeakSwitchCooldown    = 2 * time.Minute
	smartStickyFragileSwitchCooldown = time.Minute
	smartStickyEliteConfirmations    = 2
	smartStickyRetainedLogInterval   = 15 * time.Minute
)

const (
	ChannelMonitorProbeHeader      = "X-Sub2API-Channel-Monitor"
	ChannelMonitorProbeHeaderValue = "1"
	channelMonitorProbeUserAgent   = "sub2api-channel-monitor/1"
)

type smartSchedulerEndpointContextKey struct{}

type smartSchedulerStableOrderingContextKey struct{}

type channelMonitorProbeContextKey struct{}

type smartStickySwitchTraceContextKey struct{}

type smartStickySwitchTrace struct {
	mu      sync.Mutex
	pending *smartStickySwitchPending
	applied bool
}

type smartStickySwitchPending struct {
	groupID                 int64
	model                   string
	endpoint                string
	reviewKey               string
	reason                  string
	previousAccountID       int64
	expectedChallengerID    int64
	previousScore           *float64
	expectedChallengerScore *float64
}

type smartStickyReviewState struct {
	nextAt                   time.Time
	updatedAt                time.Time
	cooldownUntil            time.Time
	lastSwitchedAccountID    int64
	pendingChallengerID      int64
	pendingConfirmationCount int
	lastRetainedLogAt        time.Time
}

type openAISmartStickyReviewRequest struct {
	GroupID                 *int64
	SessionHash             string
	Platform                string
	RequestedModel          string
	ExcludedIDs             map[int64]struct{}
	RequireCompact          bool
	RequiredTransport       OpenAIUpstreamTransport
	RequiredCapability      OpenAIEndpointCapability
	RequiredImageCapability OpenAIImagesCapability
}

type openAISmartStickyReviewDecision struct {
	Reviewed             bool
	Switch               bool
	Strong               bool
	ChallengerID         int64
	Reason               string
	ProposedReason       string
	CurrentPool          string
	CurrentScore         *float64
	ChallengerPool       string
	ChallengerScore      *float64
	CurrentCost          float64
	ChallengerCost       float64
	RequiredQualityLead  float64
	QualityLead          float64
	RequiresConfirmation bool
	ConfirmationPending  bool
	ConfirmationCount    int
	Cooldown             bool
	CooldownRemaining    time.Duration
}

func WithSmartSchedulerEndpoint(ctx context.Context, endpoint string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = context.WithValue(ctx, smartSchedulerEndpointContextKey{}, normalizeSmartSchedulerEndpoint(endpoint))
	if _, ok := ctx.Value(smartStickySwitchTraceContextKey{}).(*smartStickySwitchTrace); !ok {
		ctx = context.WithValue(ctx, smartStickySwitchTraceContextKey{}, &smartStickySwitchTrace{})
	}
	return ctx
}

func WithChannelMonitorProbe(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, channelMonitorProbeContextKey{}, true)
}

func IsChannelMonitorProbe(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	probe, _ := ctx.Value(channelMonitorProbeContextKey{}).(bool)
	return probe
}

func smartSchedulerEndpointFromContext(ctx context.Context) string {
	if ctx == nil {
		return "any"
	}
	endpoint, _ := ctx.Value(smartSchedulerEndpointContextKey{}).(string)
	return normalizeSmartSchedulerEndpoint(endpoint)
}

func withSmartSchedulerStableOrdering(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, smartSchedulerStableOrderingContextKey{}, true)
}

func smartSchedulerStableOrderingRequested(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	requested, _ := ctx.Value(smartSchedulerStableOrderingContextKey{}).(bool)
	return requested
}

type smartSchedulerCandidateOrderer interface {
	OrderCandidates(ctx context.Context, group *Group, requestedModel, endpoint string, accounts []*Account, now time.Time) (*SmartSchedulerOrdering, error)
}

func applySmartSchedulerOrderingToAccounts(accounts []*Account, ordering *SmartSchedulerOrdering) []*Account {
	if ordering == nil || !ordering.Active {
		return accounts
	}
	ordered := make([]*Account, 0, len(accounts))
	for _, account := range accounts {
		if account == nil {
			continue
		}
		// An active ordering intentionally omits isolated accounts from RankByAccountID.
		// Treat that omission as a hard exclusion instead of a legacy-order fallback.
		if _, ok := ordering.RankByAccountID[account.ID]; ok {
			ordered = append(ordered, account)
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		return smartSchedulerRankCompare(ordering, ordered[i].ID, ordered[j].ID) < 0
	})
	if len(ordered) == 0 {
		if fallback := leastBadSoftIsolatedAccount(accounts, ordering); fallback != nil {
			ordered = append(ordered, fallback)
		} else if fallback := leastBadRecoveryProbeIsolatedAccount(accounts, ordering); fallback != nil {
			ordered = append(ordered, fallback)
		}
	}
	return ordered
}

func leastBadSoftIsolatedAccount(accounts []*Account, ordering *SmartSchedulerOrdering) *Account {
	var selected *Account
	for _, account := range accounts {
		if account == nil {
			continue
		}
		item, ok := ordering.ItemByAccountID[account.ID]
		if !ok || item.Pool != "isolated" || !item.SoftIsolation || !item.Schedulable || !item.ModelSupported || !item.EndpointSupported {
			continue
		}
		if selected == nil || smartSchedulerSoftFallbackPreferred(item, ordering.ItemByAccountID[selected.ID]) {
			selected = account
		}
	}
	return selected
}

func smartSchedulerSoftFallbackPreferred(left, right SmartSchedulerPreviewItem) bool {
	if left.SoftIsolationFailureCount != right.SoftIsolationFailureCount && left.SoftIsolationFailureCount > 0 && right.SoftIsolationFailureCount > 0 {
		return left.SoftIsolationFailureCount < right.SoftIsolationFailureCount
	}
	leftImmediate := smartSchedulerImmediateSupplierFailures(SmartSchedulerErrorStats{
		ImmediateProviderFailureCount:   left.ImmediateProviderFailureCount,
		ImmediateProviderTransientCount: left.ImmediateProviderTransientCount,
		ImmediateRateLimitCount:         left.ImmediateRateLimitCount,
		ImmediateUncertainFailureCount:  left.ImmediateUncertainFailureCount,
	})
	rightImmediate := smartSchedulerImmediateSupplierFailures(SmartSchedulerErrorStats{
		ImmediateProviderFailureCount:   right.ImmediateProviderFailureCount,
		ImmediateProviderTransientCount: right.ImmediateProviderTransientCount,
		ImmediateRateLimitCount:         right.ImmediateRateLimitCount,
		ImmediateUncertainFailureCount:  right.ImmediateUncertainFailureCount,
	})
	if leftImmediate != rightImmediate {
		return leftImmediate < rightImmediate
	}
	leftRecent := left.RecentProviderFailureCount + left.RecentProviderTransientCount + left.RecentRateLimitCount + left.RecentUncertainFailureCount
	rightRecent := right.RecentProviderFailureCount + right.RecentProviderTransientCount + right.RecentRateLimitCount + right.RecentUncertainFailureCount
	if leftRecent != rightRecent {
		return leftRecent < rightRecent
	}
	if left.CostMultiplier > 0 && right.CostMultiplier > 0 && left.CostMultiplier != right.CostMultiplier {
		return left.CostMultiplier < right.CostMultiplier
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

func leastBadRecoveryProbeIsolatedAccount(accounts []*Account, ordering *SmartSchedulerOrdering) *Account {
	var selected *Account
	for _, account := range accounts {
		if account == nil {
			continue
		}
		item, ok := ordering.ItemByAccountID[account.ID]
		if !ok || !smartSchedulerRecoveryProbeFallbackEligible(item) {
			continue
		}
		if selected == nil || smartSchedulerRecoveryProbeFallbackPreferred(item, ordering.ItemByAccountID[selected.ID]) {
			selected = account
		}
	}
	return selected
}

func smartSchedulerRecoveryProbeFallbackEligible(item SmartSchedulerPreviewItem) bool {
	if item.Pool != "isolated" || item.SoftIsolation || !item.Schedulable || !item.ModelSupported || !item.EndpointSupported || item.RecoveryProbe == nil {
		return false
	}
	if item.RecoveryProbe.Status != GroupRecoveryProbeStatusFailed || item.RecoveryProbe.LastErrorClass != GroupRecoveryProbeErrorTransient {
		return false
	}
	return true
}

func smartSchedulerRecoveryProbeFallbackPreferred(left, right SmartSchedulerPreviewItem) bool {
	leftProbe, rightProbe := left.RecoveryProbe, right.RecoveryProbe
	if leftProbe.ConsecutiveFailures != rightProbe.ConsecutiveFailures {
		return leftProbe.ConsecutiveFailures < rightProbe.ConsecutiveFailures
	}
	leftActivity := smartSchedulerRecoveryProbeActivityAt(leftProbe)
	rightActivity := smartSchedulerRecoveryProbeActivityAt(rightProbe)
	if !leftActivity.Equal(rightActivity) {
		return leftActivity.After(rightActivity)
	}
	leftImmediate := smartSchedulerImmediateSupplierFailures(SmartSchedulerErrorStats{
		ImmediateProviderFailureCount:   left.ImmediateProviderFailureCount,
		ImmediateProviderTransientCount: left.ImmediateProviderTransientCount,
		ImmediateRateLimitCount:         left.ImmediateRateLimitCount,
		ImmediateUncertainFailureCount:  left.ImmediateUncertainFailureCount,
	})
	rightImmediate := smartSchedulerImmediateSupplierFailures(SmartSchedulerErrorStats{
		ImmediateProviderFailureCount:   right.ImmediateProviderFailureCount,
		ImmediateProviderTransientCount: right.ImmediateProviderTransientCount,
		ImmediateRateLimitCount:         right.ImmediateRateLimitCount,
		ImmediateUncertainFailureCount:  right.ImmediateUncertainFailureCount,
	})
	if leftImmediate != rightImmediate {
		return leftImmediate < rightImmediate
	}
	if left.CostMultiplier > 0 && right.CostMultiplier > 0 && left.CostMultiplier != right.CostMultiplier {
		return left.CostMultiplier < right.CostMultiplier
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

func smartSchedulerRecoveryProbeActivityAt(state *GroupRecoveryProbeState) time.Time {
	if state == nil {
		return time.Time{}
	}
	if state.LastProbeAt != nil {
		return *state.LastProbeAt
	}
	return state.UpdatedAt
}

func smartSchedulerRankCompare(ordering *SmartSchedulerOrdering, leftAccountID, rightAccountID int64) int {
	if ordering == nil || !ordering.Active || leftAccountID == rightAccountID {
		return 0
	}
	leftRank, leftOK := ordering.RankByAccountID[leftAccountID]
	rightRank, rightOK := ordering.RankByAccountID[rightAccountID]
	switch {
	case leftOK && rightOK && leftRank < rightRank:
		return -1
	case leftOK && rightOK && leftRank > rightRank:
		return 1
	case leftOK && !rightOK:
		return -1
	case !leftOK && rightOK:
		return 1
	default:
		return 0
	}
}

func runSmartSchedulerOrdering(
	ctx context.Context,
	orderer smartSchedulerCandidateOrderer,
	group *Group,
	requestedModel string,
	endpoint string,
	accounts []*Account,
) ([]*Account, *SmartSchedulerOrdering) {
	if orderer == nil || group == nil || !group.SmartSchedulerEnabled || len(accounts) == 0 {
		return accounts, nil
	}
	ordering, err := orderer.OrderCandidates(ctx, group, requestedModel, endpoint, accounts, time.Now())
	if err != nil {
		slog.Warn("smart_scheduler.fallback",
			"group_id", group.ID,
			"model", requestedModel,
			"endpoint", endpoint,
			"reason", "statistics_unavailable",
		)
		return accounts, nil
	}
	if ordering == nil || !ordering.Active {
		return accounts, nil
	}
	ordered := applySmartSchedulerOrderingToAccounts(accounts, ordering)
	softFallback := false
	if len(ordered) == 1 {
		_, ranked := ordering.RankByAccountID[ordered[0].ID]
		softFallback = !ranked
	}
	attrs := []any{
		"group_id", group.ID,
		"model", requestedModel,
		"endpoint", endpoint,
		"candidate_count", len(accounts),
		"eligible_count", len(ordered),
		"exploration", ordering.Exploration,
		"exploration_rate", ordering.ExplorationRate,
		"version", ordering.AlgorithmVersion,
	}
	primaryCount, warmCount, isolatedCount := 0, 0, 0
	for _, item := range ordering.ItemByAccountID {
		switch item.Pool {
		case "primary":
			primaryCount++
		case "warm":
			warmCount++
		default:
			isolatedCount++
		}
	}
	attrs = append(attrs, "primary_count", primaryCount, "warm_count", warmCount, "isolated_count", isolatedCount)
	if softFallback {
		item := ordering.ItemByAccountID[ordered[0].ID]
		slog.Warn("smart_scheduler.soft_isolation_fallback",
			"group_id", group.ID,
			"model", requestedModel,
			"endpoint", endpoint,
			"account_id", ordered[0].ID,
			"reason", item.Reason,
			"immediate_failures", item.ImmediateProviderFailureCount+item.ImmediateProviderTransientCount+item.ImmediateUncertainFailureCount,
		)
		attrs = append(attrs, "soft_isolation_fallback", true)
	}
	if len(ordered) > 0 {
		firstID := ordered[0].ID
		attrs = append(attrs, "account_id", firstID, "rank", ordering.RankByAccountID[firstID])
		if item, ok := ordering.ItemByAccountID[firstID]; ok {
			attrs = append(attrs,
				"pool", item.Pool,
				"score", item.Score,
				"raw_score", item.RawScore,
				"confidence", item.Confidence,
				"cost_multiplier", item.CostMultiplier,
				"evidence_scope", item.EvidenceScope,
			)
		}
	}
	slog.Info("smart_scheduler.order_applied", attrs...)
	return ordered, ordering
}

func (s *GatewayService) smartSchedulerGroup(ctx context.Context, groupID *int64, known *Group) *Group {
	if known != nil {
		return known
	}
	if s == nil || groupID == nil || *groupID <= 0 {
		return nil
	}
	if group := s.groupFromContext(ctx, *groupID); group != nil {
		return group
	}
	if s.schedulerSnapshot != nil {
		if group, err := s.schedulerSnapshot.GetGroupByIDLite(ctx, *groupID); err == nil {
			return group
		}
	}
	if s.groupRepo != nil {
		group, _ := s.groupRepo.GetByIDLite(ctx, *groupID)
		return group
	}
	return nil
}

func (s *GatewayService) orderSmartSchedulerCandidates(ctx context.Context, groupID *int64, group *Group, requestedModel string, accounts []*Account) ([]*Account, *SmartSchedulerOrdering) {
	if s == nil {
		return accounts, nil
	}
	return runSmartSchedulerOrdering(ctx, s.smartScheduler, s.smartSchedulerGroup(ctx, groupID, group), requestedModel, smartSchedulerEndpointFromContext(ctx), accounts)
}

func (s *OpenAIGatewayService) smartSchedulerGroup(ctx context.Context, groupID *int64) *Group {
	if s == nil || groupID == nil || *groupID <= 0 {
		return nil
	}
	if s.schedulerSnapshot != nil {
		if group, err := s.schedulerSnapshot.GetGroupByIDLite(ctx, *groupID); err == nil {
			return group
		}
	}
	if s.groupRepo != nil {
		group, _ := s.groupRepo.GetByIDLite(ctx, *groupID)
		return group
	}
	return nil
}

func (s *OpenAIGatewayService) orderSmartSchedulerCandidates(ctx context.Context, groupID *int64, requestedModel string, accounts []*Account) ([]*Account, *SmartSchedulerOrdering) {
	if s == nil {
		return accounts, nil
	}
	return runSmartSchedulerOrdering(ctx, s.smartScheduler, s.smartSchedulerGroup(ctx, groupID), requestedModel, smartSchedulerEndpointFromContext(ctx), accounts)
}

func (s *OpenAIGatewayService) claimSmartStickyReview(key string, now time.Time) bool {
	if s == nil || key == "" {
		return false
	}
	s.smartStickyReviewMu.Lock()
	defer s.smartStickyReviewMu.Unlock()
	if state, ok := s.smartStickyReviews[key]; ok && now.Before(state.nextAt) {
		return false
	}
	if s.smartStickyReviews == nil {
		s.smartStickyReviews = make(map[string]smartStickyReviewState)
	}
	if len(s.smartStickyReviews) >= smartStickyReviewStateLimit {
		for stateKey, state := range s.smartStickyReviews {
			if now.Sub(state.updatedAt) >= smartStickyReviewStateTTL {
				delete(s.smartStickyReviews, stateKey)
			}
		}
		for stateKey := range s.smartStickyReviews {
			if len(s.smartStickyReviews) < smartStickyReviewStateLimit {
				break
			}
			delete(s.smartStickyReviews, stateKey)
		}
	}
	state := s.smartStickyReviews[key]
	state.nextAt = now.Add(smartStickyReviewClaimTimeout)
	state.updatedAt = now
	s.smartStickyReviews[key] = state
	return true
}

func (s *OpenAIGatewayService) finishSmartStickyReview(key string, now time.Time, interval time.Duration) {
	if s == nil || key == "" {
		return
	}
	if interval <= 0 {
		interval = smartStickyWeakReviewInterval
	}
	s.smartStickyReviewMu.Lock()
	defer s.smartStickyReviewMu.Unlock()
	if s.smartStickyReviews == nil {
		s.smartStickyReviews = make(map[string]smartStickyReviewState)
	}
	state := s.smartStickyReviews[key]
	if !state.cooldownUntil.IsZero() && now.Before(state.cooldownUntil) && interval > smartStickyWeakReviewInterval {
		interval = smartStickyWeakReviewInterval
	}
	state.nextAt = now.Add(interval)
	state.updatedAt = now
	s.smartStickyReviews[key] = state
}

func (s *OpenAIGatewayService) applySmartStickyReviewState(key string, now time.Time, decision openAISmartStickyReviewDecision) openAISmartStickyReviewDecision {
	if s == nil || strings.TrimSpace(key) == "" {
		return decision
	}
	s.smartStickyReviewMu.Lock()
	defer s.smartStickyReviewMu.Unlock()
	if s.smartStickyReviews == nil {
		s.smartStickyReviews = make(map[string]smartStickyReviewState)
	}
	state := s.smartStickyReviews[key]
	state.updatedAt = now

	if decision.Switch && decision.Reason != "current_isolated" && now.Before(state.cooldownUntil) &&
		!smartStickyCooldownCanBeBroken(decision, state.cooldownUntil.Sub(now)) {
		decision.ProposedReason = decision.Reason
		decision.Reason = "switch_cooldown"
		decision.Switch = false
		decision.Cooldown = true
		decision.CooldownRemaining = state.cooldownUntil.Sub(now)
		state.pendingChallengerID = 0
		state.pendingConfirmationCount = 0
		s.smartStickyReviews[key] = state
		return decision
	}

	if decision.RequiresConfirmation && decision.Switch {
		if state.pendingChallengerID == decision.ChallengerID {
			state.pendingConfirmationCount++
		} else {
			state.pendingChallengerID = decision.ChallengerID
			state.pendingConfirmationCount = 1
		}
		decision.ConfirmationCount = state.pendingConfirmationCount
		if state.pendingConfirmationCount < smartStickyEliteConfirmations {
			decision.ProposedReason = decision.Reason
			decision.Reason = "switch_confirmation_pending"
			decision.Switch = false
			decision.ConfirmationPending = true
		}
	} else if decision.Switch || decision.Reason == "current_isolated" {
		state.pendingChallengerID = 0
		state.pendingConfirmationCount = 0
	} else if decision.Reason != "switch_confirmation_pending" {
		state.pendingChallengerID = 0
		state.pendingConfirmationCount = 0
	}

	s.smartStickyReviews[key] = state
	return decision
}

func (s *OpenAIGatewayService) markSmartStickySwitchApplied(key string, accountID int64, now time.Time, scores ...*float64) {
	if s == nil || strings.TrimSpace(key) == "" || accountID <= 0 {
		return
	}
	var score *float64
	if len(scores) > 0 {
		score = scores[0]
	}
	s.smartStickyReviewMu.Lock()
	defer s.smartStickyReviewMu.Unlock()
	if s.smartStickyReviews == nil {
		s.smartStickyReviews = make(map[string]smartStickyReviewState)
	}
	state := s.smartStickyReviews[key]
	state.cooldownUntil = now.Add(smartStickySwitchCooldownForScore(score))
	state.lastSwitchedAccountID = accountID
	state.pendingChallengerID = 0
	state.pendingConfirmationCount = 0
	minimumNextAt := now.Add(smartStickyWeakReviewInterval)
	if state.nextAt.IsZero() || state.nextAt.After(minimumNextAt) {
		state.nextAt = minimumNextAt
	}
	state.updatedAt = now
	s.smartStickyReviews[key] = state
}

func smartStickySwitchCooldownForScore(score *float64) time.Duration {
	if score == nil {
		return smartStickyWeakSwitchCooldown
	}
	switch {
	case *score >= smartStickyStrongMinScore:
		return smartStickyStrongSwitchCooldown
	case *score >= 60:
		return smartStickyWeakSwitchCooldown
	default:
		return smartStickyFragileSwitchCooldown
	}
}

func smartStickyCooldownCanBeBroken(decision openAISmartStickyReviewDecision, remaining time.Duration) bool {
	if decision.CurrentScore == nil || *decision.CurrentScore >= smartStickyStrongMinScore {
		return false
	}
	// B/C sessions are deliberately weakly sticky. A clear quality lead may
	// break their short debounce window; A-or-better sessions retain the full
	// cooldown and S-class confirmation policy.
	return decision.QualityLead >= 8 && remaining <= smartStickyWeakSwitchCooldown
}

func smartStickySessionFingerprint(key string) string {
	if strings.TrimSpace(key) == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(key))
	return fmt.Sprintf("%x", sum[:6])
}

func smartStickyRetentionIsNoteworthy(decision openAISmartStickyReviewDecision) bool {
	if !decision.Reviewed {
		return false
	}
	if decision.Cooldown || decision.ConfirmationPending {
		return true
	}
	return decision.ChallengerID > 0 &&
		decision.RequiredQualityLead > 0 &&
		decision.QualityLead >= decision.RequiredQualityLead-1
}

func (s *OpenAIGatewayService) shouldLogSmartStickyRetention(key string, now time.Time, decision openAISmartStickyReviewDecision) bool {
	if s == nil || strings.TrimSpace(key) == "" || !smartStickyRetentionIsNoteworthy(decision) {
		return false
	}
	s.smartStickyReviewMu.Lock()
	defer s.smartStickyReviewMu.Unlock()
	if s.smartStickyReviews == nil {
		s.smartStickyReviews = make(map[string]smartStickyReviewState)
	}
	state := s.smartStickyReviews[key]
	if !state.lastRetainedLogAt.IsZero() && now.Sub(state.lastRetainedLogAt) < smartStickyRetainedLogInterval {
		return false
	}
	state.lastRetainedLogAt = now
	state.updatedAt = now
	s.smartStickyReviews[key] = state
	return true
}

func smartStickyReviewKeyWithContext(ctx context.Context, req openAISmartStickyReviewRequest) string {
	if req.GroupID == nil || *req.GroupID <= 0 || strings.TrimSpace(req.SessionHash) == "" {
		return ""
	}
	return fmt.Sprintf("%d|%s|%s|%s|%t|%s|%s|%s|%s",
		*req.GroupID,
		strings.ToLower(strings.TrimSpace(req.RequestedModel)),
		smartSchedulerEndpointFromContext(ctx),
		strings.ToLower(strings.TrimSpace(req.Platform)),
		req.RequireCompact,
		req.RequiredTransport,
		req.RequiredCapability,
		req.RequiredImageCapability,
		strings.TrimSpace(req.SessionHash),
	)
}

func smartStickyScoreGrade(score *float64) string {
	if score == nil {
		return ""
	}
	return accountQualityGrade(int(math.Round(math.Max(0, math.Min(100, *score)))))
}

func smartStickyScoreLogValue(score *float64) any {
	if score == nil {
		return nil
	}
	return *score
}

func smartStickyRequiredQualityLead(score *float64) float64 {
	if score == nil {
		return smartStickyWeakQualityLead
	}
	if *score >= smartStickyEliteMinScore {
		return smartStickyEliteQualityLead
	}
	if *score >= smartStickyStrongMinScore {
		return smartStickyStrongQualityLead
	}
	return smartStickyWeakQualityLead
}

func armSmartStickySwitchTrace(ctx context.Context, req openAISmartStickyReviewRequest, currentAccountID int64, decision openAISmartStickyReviewDecision) {
	trace, _ := ctx.Value(smartStickySwitchTraceContextKey{}).(*smartStickySwitchTrace)
	if trace == nil || !decision.Switch || currentAccountID <= 0 {
		return
	}
	trace.mu.Lock()
	defer trace.mu.Unlock()
	trace.pending = &smartStickySwitchPending{
		groupID:                 derefGroupID(req.GroupID),
		model:                   req.RequestedModel,
		endpoint:                smartSchedulerEndpointFromContext(ctx),
		reviewKey:               smartStickyReviewKeyWithContext(ctx, req),
		reason:                  decision.Reason,
		previousAccountID:       currentAccountID,
		expectedChallengerID:    decision.ChallengerID,
		previousScore:           decision.CurrentScore,
		expectedChallengerScore: decision.ChallengerScore,
	}
	trace.applied = false
}

func (s *OpenAIGatewayService) logSmartStickySwitchApplied(ctx context.Context, groupID *int64, accountID int64) {
	trace, _ := ctx.Value(smartStickySwitchTraceContextKey{}).(*smartStickySwitchTrace)
	if trace == nil || accountID <= 0 {
		return
	}
	trace.mu.Lock()
	if trace.applied || trace.pending == nil || trace.pending.groupID != derefGroupID(groupID) || trace.pending.previousAccountID == accountID {
		trace.mu.Unlock()
		return
	}
	pending := *trace.pending
	trace.applied = true
	trace.pending = nil
	trace.mu.Unlock()
	if pending.expectedChallengerID > 0 && accountID != pending.expectedChallengerID {
		slog.Warn("sticky.smart_scheduler_switch_fallback",
			"group_id", pending.groupID,
			"model", pending.model,
			"endpoint", pending.endpoint,
			"reason", pending.reason,
			"previous_account_id", pending.previousAccountID,
			"expected_challenger_id", pending.expectedChallengerID,
			"fallback_account_id", accountID,
			"session_fingerprint", smartStickySessionFingerprint(pending.reviewKey),
		)
		return
	}
	s.markSmartStickySwitchApplied(pending.reviewKey, accountID, time.Now(), pending.expectedChallengerScore)

	slog.Info("sticky.smart_scheduler_switch_applied",
		"group_id", pending.groupID,
		"model", pending.model,
		"endpoint", pending.endpoint,
		"reason", pending.reason,
		"previous_account_id", pending.previousAccountID,
		"expected_challenger_id", pending.expectedChallengerID,
		"account_id", accountID,
		"previous_score", smartStickyScoreLogValue(pending.previousScore),
		"expected_challenger_score", smartStickyScoreLogValue(pending.expectedChallengerScore),
		"session_fingerprint", smartStickySessionFingerprint(pending.reviewKey),
	)
}

func decideOpenAISmartStickyReview(ordering *SmartSchedulerOrdering, currentAccountID int64) openAISmartStickyReviewDecision {
	decision := openAISmartStickyReviewDecision{Reviewed: ordering != nil && ordering.Active}
	if ordering == nil || !ordering.Active || currentAccountID <= 0 {
		return decision
	}
	if ordering.Exploration {
		decision.Reason = "exploration_deferred"
		return decision
	}

	current, currentFound := ordering.ItemByAccountID[currentAccountID]
	if currentFound {
		decision.CurrentPool = current.Pool
		decision.CurrentScore = current.Score
		decision.CurrentCost = current.CostMultiplier
	}
	for _, accountID := range ordering.OrderedAccountIDs {
		if accountID == currentAccountID {
			break
		}
		challenger, ok := ordering.ItemByAccountID[accountID]
		if !ok || challenger.Pool == "isolated" {
			continue
		}
		decision.ChallengerID = accountID
		decision.ChallengerPool = challenger.Pool
		decision.ChallengerScore = challenger.Score
		decision.ChallengerCost = challenger.CostMultiplier
		break
	}
	if decision.ChallengerID == 0 && len(ordering.OrderedAccountIDs) > 0 && ordering.OrderedAccountIDs[0] != currentAccountID {
		accountID := ordering.OrderedAccountIDs[0]
		if challenger, ok := ordering.ItemByAccountID[accountID]; ok && challenger.Pool != "isolated" {
			decision.ChallengerID = accountID
			decision.ChallengerPool = challenger.Pool
			decision.ChallengerScore = challenger.Score
			decision.ChallengerCost = challenger.CostMultiplier
		}
	}

	if currentFound && current.Pool != "isolated" && current.Score != nil && *current.Score >= smartStickyStrongMinScore {
		decision.Strong = true
	}
	if decision.ChallengerID == 0 {
		decision.Reason = "no_better_candidate"
		return decision
	}
	if !currentFound || current.Pool == "isolated" {
		decision.Switch = true
		decision.Reason = "current_isolated"
		return decision
	}
	if current.Pool != "primary" && decision.ChallengerPool == "primary" &&
		(current.Score == nil || *current.Score < smartStickyStrongMinScore) {
		decision.Switch = true
		decision.Reason = "primary_over_warm"
		return decision
	}
	if current.Score == nil && decision.ChallengerScore != nil {
		decision.Switch = true
		decision.Reason = "scored_over_unscored"
		return decision
	}
	if current.Score != nil && decision.ChallengerScore != nil {
		decision.RequiredQualityLead = smartStickyRequiredQualityLead(current.Score)
		decision.QualityLead = *decision.ChallengerScore - *current.Score
		if decision.QualityLead >= decision.RequiredQualityLead {
			decision.Switch = true
			decision.Reason = "better_quality"
			decision.RequiresConfirmation = *current.Score >= smartStickyEliteMinScore
			return decision
		}
		if decision.ChallengerCost > 0 && current.CostMultiplier > 0 &&
			decision.ChallengerCost < current.CostMultiplier &&
			*decision.ChallengerScore >= *current.Score-smartSchedulerCostTolerance {
			decision.Switch = true
			decision.Reason = "cheaper_within_tolerance"
			return decision
		}
	}
	decision.Reason = "challenger_not_better"
	return decision
}

func (s *OpenAIGatewayService) openAISmartStickyReviewCandidates(ctx context.Context, req openAISmartStickyReviewRequest) ([]*Account, error) {
	accounts, err := s.listSchedulableAccounts(ctx, req.GroupID, req.Platform)
	if err != nil {
		return nil, err
	}
	needsUpstreamCheck := s.needsUpstreamChannelRestrictionCheck(ctx, req.GroupID)
	candidates := make([]*Account, 0, len(accounts))
	for i := range accounts {
		account := &accounts[i]
		if _, excluded := req.ExcludedIDs[account.ID]; excluded {
			continue
		}
		fresh := s.resolveFreshSchedulableOpenAIAccount(ctx, account, req.Platform, req.RequestedModel, req.RequireCompact, req.RequiredCapability)
		if fresh == nil || !s.isOpenAIAccountTransportCompatible(fresh, req.RequiredTransport) ||
			!accountSupportsOpenAICapabilities(fresh, req.RequiredCapability, req.RequiredImageCapability) {
			continue
		}
		if needsUpstreamCheck && s.isUpstreamModelRestrictedByChannel(ctx, *req.GroupID, fresh, req.RequestedModel, req.RequireCompact) {
			continue
		}
		candidates = append(candidates, fresh)
	}
	return candidates, nil
}

func (s *OpenAIGatewayService) reviewOpenAISmartStickySession(ctx context.Context, req openAISmartStickyReviewRequest, currentAccountID int64) openAISmartStickyReviewDecision {
	if IsChannelMonitorProbe(ctx) {
		return openAISmartStickyReviewDecision{}
	}
	group := s.smartSchedulerGroup(ctx, req.GroupID)
	if s == nil || s.smartScheduler == nil || group == nil || !group.SmartSchedulerEnabled || currentAccountID <= 0 {
		return openAISmartStickyReviewDecision{}
	}
	key := smartStickyReviewKeyWithContext(ctx, req)
	now := time.Now()
	if !s.claimSmartStickyReview(key, now) {
		return openAISmartStickyReviewDecision{}
	}
	interval := smartStickyWeakReviewInterval
	defer func() {
		s.finishSmartStickyReview(key, time.Now(), interval)
	}()

	candidates, err := s.openAISmartStickyReviewCandidates(ctx, req)
	if err != nil || len(candidates) == 0 {
		return openAISmartStickyReviewDecision{}
	}
	ordering, err := s.smartScheduler.OrderCandidates(
		withSmartSchedulerStableOrdering(ctx),
		group,
		req.RequestedModel,
		smartSchedulerEndpointFromContext(ctx),
		candidates,
		now,
	)
	if err != nil {
		return openAISmartStickyReviewDecision{}
	}
	decision := decideOpenAISmartStickyReview(ordering, currentAccountID)
	decision = s.applySmartStickyReviewState(key, now, decision)
	if decision.ConfirmationPending || decision.Cooldown {
		interval = smartStickyWeakReviewInterval
	} else if decision.Strong || decision.Switch {
		interval = smartStickyStrongReviewInterval
	}
	attrs := []any{
		"group_id", group.ID,
		"model", req.RequestedModel,
		"endpoint", smartSchedulerEndpointFromContext(ctx),
		"account_id", currentAccountID,
		"pool", decision.CurrentPool,
		"score", smartStickyScoreLogValue(decision.CurrentScore),
		"grade", smartStickyScoreGrade(decision.CurrentScore),
		"reason", decision.Reason,
		"challenger_id", decision.ChallengerID,
		"challenger_pool", decision.ChallengerPool,
		"challenger_score", smartStickyScoreLogValue(decision.ChallengerScore),
		"challenger_grade", smartStickyScoreGrade(decision.ChallengerScore),
		"quality_lead", decision.QualityLead,
		"required_quality_lead", decision.RequiredQualityLead,
		"proposed_reason", decision.ProposedReason,
		"confirmation_count", decision.ConfirmationCount,
		"cooldown_remaining_ms", decision.CooldownRemaining.Milliseconds(),
		"session_fingerprint", smartStickySessionFingerprint(key),
	}
	if decision.Switch {
		armSmartStickySwitchTrace(ctx, req, currentAccountID, decision)
		slog.Info("sticky.smart_scheduler_switched", attrs...)
	} else if s.shouldLogSmartStickyRetention(key, now, decision) {
		slog.Info("sticky.smart_scheduler_kept", attrs...)
	} else if decision.Reviewed {
		slog.Debug("sticky.smart_scheduler_kept", attrs...)
	}
	return decision
}

func selectLegacyGatewayCandidate(accounts []*Account, preferOAuth, mixed bool, ordering *SmartSchedulerOrdering) *Account {
	if len(accounts) == 0 {
		return nil
	}
	if ordering != nil && ordering.Active {
		return accounts[0]
	}
	selected := accounts[0]
	for _, candidate := range accounts[1:] {
		if candidate.Priority < selected.Priority {
			selected = candidate
			continue
		}
		if candidate.Priority > selected.Priority {
			continue
		}
		switch {
		case candidate.LastUsedAt == nil && selected.LastUsedAt != nil:
			selected = candidate
		case candidate.LastUsedAt != nil && selected.LastUsedAt == nil:
		case candidate.LastUsedAt == nil && selected.LastUsedAt == nil:
			candidateOAuthPreferred := preferOAuth && candidate.Type == AccountTypeOAuth && candidate.Type != selected.Type
			if mixed {
				candidateOAuthPreferred = candidateOAuthPreferred && candidate.Platform == PlatformGemini && selected.Platform == PlatformGemini
			}
			if candidateOAuthPreferred {
				selected = candidate
			}
		default:
			if candidate.LastUsedAt.Before(*selected.LastUsedAt) {
				selected = candidate
			}
		}
	}
	return selected
}
