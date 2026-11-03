package service

import (
	"context"
	"encoding/json"
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
		if account != nil && account.IsAdaptiveAPIProtocol() {
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
		if account != nil && account.IsAdaptiveAPIProtocol() {
			return nil, false
		}
	}
	return nil, true
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

func filterFailoverByProtocolEligibility(group *Group, endpoint string, ordered []*Account, ordering *SmartSchedulerOrdering) []*Account {
	if group == nil || !group.RecoveryProbeEnabled || len(ordered) == 0 {
		return ordered
	}
	kept := make([]*Account, 0, len(ordered))
	for _, account := range ordered {
		if account == nil {
			continue
		}
		var state *GroupRecoveryProbeState
		if ordering != nil {
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

// runAdaptiveBusinessProtocolProbe probes each native protocol on its own.
// The admin adaptive connection test stays serial; only this business recovery
// path records per-protocol eligibility. It returns false when the account is
// not an adaptive CN provider so the existing single-protocol probe still runs.
func (r *GroupRecoveryProbeRunner) runAdaptiveBusinessProtocolProbe(ctx context.Context, job GroupRecoveryProbeJob, startedAt time.Time) bool {
	tester, ok := r.accountTestSvc.(businessProtocolProbeTester)
	if !ok || r == nil || r.repo == nil {
		return false
	}
	results, split, err := tester.ProbeBusinessProtocols(ctx, job.State.AccountID, job.State.Model)
	if err != nil || !split {
		return false
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
	eligibility := make(map[string]GroupRecoveryProbeProtocolStatus, len(previous)+len(results))
	for protocol, status := range previous {
		eligibility[protocol] = status
	}
	round := GroupRecoveryProbeRoundResult{CostStatus: GroupRecoveryProbeCostStatusUnavailable}
	var failureText string
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
		} else {
			round.SuccessCount++
		}
		eligibility[protocol] = mergeBusinessProtocolProbe(previous[protocol], success, errText)
	}
	round.LastError = failureText
	if store, ok := r.repo.(groupRecoveryProbeProtocolStore); ok && job.PhysicalStateID > 0 {
		if saveErr := store.SaveProtocolEligibility(ctx, job.PhysicalStateID, eligibility); saveErr != nil {
			logger.LegacyPrintf("service.group_recovery_probe", "[GroupRecoveryProbe] protocol eligibility save failed: state=%d err=%v", job.PhysicalStateID, saveErr)
		}
	}
	job.State.ProtocolEligibility = eligibility
	completion := buildGroupRecoveryProbeCompletion(job, round, time.Now())
	completion.Status = aggregateBusinessProtocolStatus(eligibility)
	if job.Mode == GroupRecoveryProbeModeSmart && completion.Status != GroupRecoveryProbeStatusPaused {
		completion.NextProbeAt = addGroupRecoveryProbeJitter(time.Now(), completion.NextProbeAt, r.randomFloat)
	}
	accepted, completeErr := r.repo.Complete(ctx, completion)
	if completeErr != nil || !accepted {
		return true
	}
	r.invalidateSchedulerOrderingCaches()
	finishedAt := time.Now()
	audit := buildGroupRecoveryProbeAudit(job, round, completion, startedAt, finishedAt)
	if _, auditErr := r.createAudit(ctx, audit); auditErr != nil {
		return true
	}
	return true
}
