package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// GroupRecoveryProbeProtocolStatus is the health of one business protocol.
// Adaptive serial probes must not collapse Chat, Anthropic and Responses into
// a single status: an Anthropic failure does not revoke Responses eligibility,
// and a Responses success does not grant Anthropic eligibility.
type GroupRecoveryProbeProtocolStatus struct {
	Status               string `json:"status"`
	ConsecutiveSuccesses int    `json:"consecutive_successes"`
	ConsecutiveFailures  int    `json:"consecutive_failures"`
	LastErrorClass       string `json:"last_error_class,omitempty"`
	LastError            string `json:"last_error,omitempty"`
}

type groupRecoveryProbeProtocolStore interface {
	GetProtocolEligibility(ctx context.Context, physicalStateID int64) (map[string]GroupRecoveryProbeProtocolStatus, error)
	SaveProtocolEligibility(ctx context.Context, physicalStateID int64, eligibility map[string]GroupRecoveryProbeProtocolStatus) error
}

type businessProtocolProbeTester interface {
	ProbeBusinessProtocols(ctx context.Context, accountID int64, modelID string) (map[string]*ScheduledTestResult, bool, error)
}

type businessProtocolAttemptPlanner interface {
	PlannedBusinessProtocolAttempts(ctx context.Context, accountID int64, modelID string) (int, bool)
}

const adaptiveBusinessProtocolFanoutUpperBound = 3

func (r *GroupRecoveryProbeRunner) plannedProbeAttempts(ctx context.Context, job GroupRecoveryProbeJob) int {
	attempts := job.AttemptsPerRound
	if attempts < 1 {
		attempts = 1
	}
	if r == nil || r.accountTestSvc == nil {
		return attempts
	}
	if planner, ok := r.accountTestSvc.(businessProtocolAttemptPlanner); ok {
		if count, split := planner.PlannedBusinessProtocolAttempts(ctx, job.State.AccountID, job.State.Model); split && count > 0 {
			return count * attempts
		}
		return attempts
	}
	if _, ok := r.accountTestSvc.(businessProtocolProbeTester); ok {
		return adaptiveBusinessProtocolFanoutUpperBound * attempts
	}
	return attempts
}

func businessProtocolForEndpoint(endpoint string) string {
	switch normalizeSmartSchedulerEndpoint(endpoint) {
	case "responses":
		return APIProtocolResponses
	case "messages":
		return APIProtocolAnthropic
	case "chat_completions":
		return APIProtocolChatCompletions
	default:
		return ""
	}
}

func adaptiveBusinessProbeProtocols(account *Account) []string {
	if account == nil || !account.IsCNProvider() || !account.IsAdaptiveAPIProtocol() {
		return nil
	}
	protocols := []string{APIProtocolChatCompletions, APIProtocolAnthropic}
	if account.SupportsNativeCNResponses() {
		protocols = append(protocols, APIProtocolResponses)
	}
	return protocols
}

// DecodeGroupRecoveryProbeProtocolEligibility parses the persisted per-protocol
// map. Empty and invalid payloads mean no protocol has been qualified.
func DecodeGroupRecoveryProbeProtocolEligibility(raw []byte) map[string]GroupRecoveryProbeProtocolStatus {
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "{}" || string(raw) == "null" {
		return nil
	}
	var decoded map[string]GroupRecoveryProbeProtocolStatus
	if err := json.Unmarshal(raw, &decoded); err != nil || len(decoded) == 0 {
		return nil
	}
	return decoded
}

// mergeBusinessProtocolProbe updates one protocol without touching the others.
// The second consecutive success becomes eligible. A failure does not rewrite
// any other protocol's status.
func mergeBusinessProtocolProbe(prev GroupRecoveryProbeProtocolStatus, success bool, errText string) GroupRecoveryProbeProtocolStatus {
	if success {
		prev.ConsecutiveSuccesses++
		prev.ConsecutiveFailures = 0
		prev.LastError = ""
		prev.LastErrorClass = GroupRecoveryProbeErrorNone
		if prev.ConsecutiveSuccesses >= 2 {
			prev.Status = GroupRecoveryProbeStatusEligible
		} else {
			prev.Status = GroupRecoveryProbeStatusWarm
		}
		return prev
	}
	prev.ConsecutiveSuccesses = 0
	prev.ConsecutiveFailures++
	prev.LastErrorClass = classifyGroupRecoveryProbeError(errText)
	prev.LastError = sanitizeGroupRecoveryProbeError(errText)
	prev.Status = GroupRecoveryProbeStatusFailed
	if prev.LastErrorClass == GroupRecoveryProbeErrorPermanent {
		prev.Status = GroupRecoveryProbeStatusPaused
	}
	return prev
}

func aggregateBusinessProtocolStatus(eligibility map[string]GroupRecoveryProbeProtocolStatus) string {
	if len(eligibility) == 0 {
		return GroupRecoveryProbeStatusPending
	}
	sawWarm := false
	sawFailed := false
	for _, status := range eligibility {
		switch status.Status {
		case GroupRecoveryProbeStatusEligible:
			return GroupRecoveryProbeStatusEligible
		case GroupRecoveryProbeStatusWarm:
			sawWarm = true
		case GroupRecoveryProbeStatusFailed, GroupRecoveryProbeStatusPaused:
			sawFailed = true
		}
	}
	if sawWarm {
		return GroupRecoveryProbeStatusWarm
	}
	if sawFailed {
		return GroupRecoveryProbeStatusFailed
	}
	return GroupRecoveryProbeStatusPending
}

// recoveryProbeAllowsFailover reports whether an account may receive failover
// traffic for the request's business protocol. Missing, pending and warm
// states are not qualified. A Responses success does not qualify Anthropic.
func recoveryProbeAllowsFailover(account *Account, endpoint string, state *GroupRecoveryProbeState) bool {
	if state == nil {
		return false
	}
	protocol := businessProtocolForEndpoint(endpoint)
	if protocol != "" {
		if status, ok := state.ProtocolEligibility[protocol]; ok {
			return status.Status == GroupRecoveryProbeStatusEligible
		}
		if accountRequiresPerProtocolEligibility(account) {
			return false
		}
	}
	return state.Status == GroupRecoveryProbeStatusEligible
}

func protocolProbeView(account *Account, endpoint string, state *GroupRecoveryProbeState) (*GroupRecoveryProbeState, bool) {
	if state == nil {
		return nil, false
	}
	protocol := businessProtocolForEndpoint(endpoint)
	if protocol != "" {
		if status, ok := state.ProtocolEligibility[protocol]; ok {
			cloned := *state
			cloned.Status = status.Status
			cloned.ConsecutiveSuccesses = status.ConsecutiveSuccesses
			cloned.ConsecutiveFailures = status.ConsecutiveFailures
			cloned.LastErrorClass = status.LastErrorClass
			cloned.LastError = status.LastError
			return &cloned, false
		}
		if accountRequiresPerProtocolEligibility(account) {
			return nil, false
		}
	}
	return nil, true
}

// accountRequiresPerProtocolEligibility is the producer set: only CN adaptive
// accounts receive a per-protocol probe. OpenCode Go and Command Code are
// adaptive by model, but their probe still records one shared status.
func accountRequiresPerProtocolEligibility(account *Account) bool {
	return len(adaptiveBusinessProbeProtocols(account)) > 0
}

func protocolConfirmationSuccesses(eligibility map[string]GroupRecoveryProbeProtocolStatus) int {
	minSuccesses := 0
	seen := false
	for _, status := range eligibility {
		switch status.Status {
		case GroupRecoveryProbeStatusFailed, GroupRecoveryProbeStatusPaused:
			continue
		}
		if !seen || status.ConsecutiveSuccesses < minSuccesses {
			minSuccesses = status.ConsecutiveSuccesses
			seen = true
		}
	}
	return minSuccesses
}

// applyProtocolConfirmationSchedule sets the next probe from the final
// aggregate status. A first round that succeeds on every protocol is still
// warm until each protocol has two successes, so it must not inherit the
// multi-hour eligible interval computed from the shared success count.
func applyProtocolConfirmationSchedule(completion *GroupRecoveryProbeCompletion, job GroupRecoveryProbeJob, now time.Time) {
	if completion == nil {
		return
	}
	interval := groupRecoveryProbeInterval(job)
	switch completion.Status {
	case GroupRecoveryProbeStatusEligible:
		if job.Mode == GroupRecoveryProbeModeSmart {
			intervalJob := job
			intervalJob.State.ConsecutiveSuccesses = completion.ConsecutiveSuccesses
			completion.NextProbeAt = now.Add(groupRecoveryProbeSmartEligibleInterval(intervalJob))
			return
		}
		completion.NextProbeAt = now.Add(interval)
	case GroupRecoveryProbeStatusWarm, GroupRecoveryProbeStatusPending:
		if job.Mode == GroupRecoveryProbeModeSmart {
			completion.NextProbeAt = now.Add(5 * time.Minute)
			return
		}
		completion.NextProbeAt = now.Add(interval)
	}
}

func applyGroupRecoveryProbeStateForRequest(item *SmartSchedulerPreviewItem, account *Account, endpoint string, state *GroupRecoveryProbeState) {
	if item == nil || state == nil {
		return
	}
	raw := *state
	item.RecoveryProbe = &raw
	view, applyLegacy := protocolProbeView(account, endpoint, state)
	if view != nil {
		applyGroupRecoveryProbeStateToSchedulerItem(item, view)
		item.RecoveryProbe = &raw
		return
	}
	if applyLegacy {
		applyGroupRecoveryProbeStateToSchedulerItem(item, state)
	}
}

type recoveryProbeStateSource interface {
	ListRecoveryProbeStates(ctx context.Context, groupID int64, accountIDs []int64, model string) (map[int64]GroupRecoveryProbeState, error)
}

func (s *SmartSchedulerPreviewService) ListRecoveryProbeStates(ctx context.Context, groupID int64, accountIDs []int64, model string) (map[int64]GroupRecoveryProbeState, error) {
	if s == nil || s.recoveryProbe == nil {
		return nil, fmt.Errorf("recovery probe repository is unavailable")
	}
	return s.recoveryProbe.ListStates(ctx, groupID, accountIDs, model)
}

func loadFailoverProbeStates(ctx context.Context, orderer smartSchedulerCandidateOrderer, group *Group, accounts []*Account) (map[int64]GroupRecoveryProbeState, bool, error) {
	source, ok := orderer.(recoveryProbeStateSource)
	if !ok || group == nil || !group.RecoveryProbeEnabled {
		return nil, false, nil
	}
	model, ok := smartSchedulerRecoveryProbeModel(group)
	if !ok {
		return nil, false, nil
	}
	ids := make([]int64, 0, len(accounts))
	for _, account := range accounts {
		if account != nil && account.ID > 0 {
			ids = append(ids, account.ID)
		}
	}
	if len(ids) == 0 {
		return map[int64]GroupRecoveryProbeState{}, true, nil
	}
	states, err := source.ListRecoveryProbeStates(ctx, group.ID, ids, model)
	if err != nil {
		return nil, true, err
	}
	if states == nil {
		states = map[int64]GroupRecoveryProbeState{}
	}
	return states, true, nil
}

func filterFailoverAdmission(ctx context.Context, group *Group, endpoint string, accounts []*Account, states map[int64]GroupRecoveryProbeState, statesLoaded bool, loadErr error, ordering *SmartSchedulerOrdering) []*Account {
	if !openAIFailoverSelectionFromContext(ctx) {
		return accounts
	}
	if loadErr != nil {
		return nil
	}
	return filterFailoverByProtocolEligibility(group, endpoint, accounts, states, statesLoaded, ordering)
}

func filterFailoverByProtocolEligibility(group *Group, endpoint string, ordered []*Account, states map[int64]GroupRecoveryProbeState, statesLoaded bool, ordering *SmartSchedulerOrdering) []*Account {
	if group == nil || !group.RecoveryProbeEnabled || len(ordered) == 0 {
		return ordered
	}
	kept := make([]*Account, 0, len(ordered))
	for _, account := range ordered {
		if account == nil {
			continue
		}
		var state *GroupRecoveryProbeState
		if statesLoaded {
			if loaded, ok := states[account.ID]; ok {
				copied := loaded
				state = &copied
			}
		} else if ordering != nil {
			if item, ok := ordering.ItemByAccountID[account.ID]; ok {
				state = item.RecoveryProbe
			}
		}
		if recoveryProbeAllowsFailover(account, endpoint, state) {
			kept = append(kept, account)
		}
	}
	return kept
}

// collectAdaptiveProtocolProbe probes each native protocol on its own and
// returns the round for the shared completion, audit, and settlement path.
// It does not write eligibility. Complete persists that map only after the
// claim ownership check accepts the result.
func (r *GroupRecoveryProbeRunner) collectAdaptiveProtocolProbe(ctx context.Context, job GroupRecoveryProbeJob) (GroupRecoveryProbeRoundResult, map[string]GroupRecoveryProbeProtocolStatus, bool) {
	tester, ok := r.accountTestSvc.(businessProtocolProbeTester)
	if !ok || r == nil || r.repo == nil {
		return GroupRecoveryProbeRoundResult{}, nil, false
	}
	rounds := job.AttemptsPerRound
	if rounds < 1 {
		rounds = 1
	}
	previous := map[string]GroupRecoveryProbeProtocolStatus{}
	if store, ok := r.repo.(groupRecoveryProbeProtocolStore); ok && job.PhysicalStateID > 0 {
		loaded, loadErr := store.GetProtocolEligibility(ctx, job.PhysicalStateID)
		if loadErr != nil {
			logger.LegacyPrintf("service.group_recovery_probe", "[GroupRecoveryProbe] protocol eligibility read failed: state=%d err=%v", job.PhysicalStateID, loadErr)
		} else if loaded != nil {
			previous = loaded
		}
	}
	eligibility := make(map[string]GroupRecoveryProbeProtocolStatus, len(previous))
	for protocol, status := range previous {
		eligibility[protocol] = status
	}
	round := GroupRecoveryProbeRoundResult{CostStatus: GroupRecoveryProbeCostStatusUnavailable}
	var failureText string
	handled := false
	for attempt := 0; attempt < rounds; attempt++ {
		results, split, err := tester.ProbeBusinessProtocols(ctx, job.State.AccountID, job.State.Model)
		if err != nil || !split {
			if !handled {
				return GroupRecoveryProbeRoundResult{}, nil, false
			}
			break
		}
		handled = true
		permanent := false
		for protocol, result := range results {
			round.Attempts++
			success := result != nil && result.Status == "success"
			errText := ""
			if result != nil {
				round.LastLatencyMs = result.LatencyMs
				errText = result.ErrorMessage
				accumulateGroupRecoveryProbeUsage(&round, result)
			}
			if !success {
				round.FailureCount++
				if errText == "" {
					errText = "business protocol probe failed"
				}
				if failureText == "" {
					failureText = protocol + ": " + errText
				}
				if classifyGroupRecoveryProbeError(errText) == GroupRecoveryProbeErrorPermanent {
					permanent = true
				}
			} else {
				round.SuccessCount++
			}
			eligibility[protocol] = mergeBusinessProtocolProbe(eligibility[protocol], success, errText)
		}
		if permanent {
			break
		}
	}
	round.LastError = failureText
	return round, eligibility, handled
}
