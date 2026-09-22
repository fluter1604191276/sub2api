package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// CapabilityType identifies a client-visible tool capability. These values are
// deliberately protocol-neutral so one account can be summarized across all
// groups and ingress protocols.
type CapabilityType string

const (
	CapabilityFunctionTool     CapabilityType = "function_tool"
	CapabilityToolRoundtrip    CapabilityType = "tool_roundtrip"
	CapabilityClientTool       CapabilityType = "client_tool"
	CapabilityTerminalContract CapabilityType = "terminal_contract"
)

type CapabilityOutcome string

const (
	CapabilitySuccess  CapabilityOutcome = "success"
	CapabilityFailure  CapabilityOutcome = "failure"
	CapabilityExcluded CapabilityOutcome = "excluded"
)

type AccountCapabilityObservation struct {
	AccountID      int64
	UpstreamModel  string
	Protocol       string
	CapabilityType CapabilityType
	Outcome        CapabilityOutcome
	Reason         string
	RequestID      string
	CreatedAt      time.Time
}

type AccountCapabilityStatus struct {
	State         string     `json:"state"`
	SampleCount   int64      `json:"sample_count"`
	SuccessCount  int64      `json:"success_count"`
	FailureCount  int64      `json:"failure_count"`
	LastSuccessAt *time.Time `json:"last_success_at,omitempty"`
	LastFailureAt *time.Time `json:"last_failure_at,omitempty"`
	LastReason    string     `json:"last_reason,omitempty"`
	UpdatedAt     *time.Time `json:"updated_at,omitempty"`
}

type AccountCapabilitySummary struct {
	FunctionTool     AccountCapabilityStatus `json:"function_tool"`
	ToolRoundtrip    AccountCapabilityStatus `json:"tool_roundtrip"`
	ClientTool       AccountCapabilityStatus `json:"client_tool"`
	TerminalContract AccountCapabilityStatus `json:"terminal_contract"`
}

// UnknownAccountCapabilitySummary gives callers a stable display shape even
// when an account has no passive tool evidence yet.
func UnknownAccountCapabilitySummary() AccountCapabilitySummary {
	status := AccountCapabilityStatus{State: capabilityUnknown}
	return AccountCapabilitySummary{
		FunctionTool:     status,
		ToolRoundtrip:    status,
		ClientTool:       status,
		TerminalContract: status,
	}
}

// AccountCapabilityRepository is intentionally optional. Keeping it separate
// from UsageLogRepository avoids breaking the many lightweight repository
// stubs used by gateway tests and third-party integrations.
type AccountCapabilityRepository interface {
	RecordCapabilityObservation(context.Context, *AccountCapabilityObservation) error
	GetAccountCapabilitySummaryBatch(context.Context, []int64) (map[int64]AccountCapabilitySummary, error)
}

const (
	capabilityUnknown            = "unknown"
	capabilityCapable            = "capable"
	capabilityDegraded           = "degraded"
	capabilityUnsupported        = "unsupported"
	accountCapabilityObservedKey = "sub2api.account_capability_observed"
)

// ClassifyCapabilityError returns an observation only for errors that are
// meaningful evidence about account capability. Transport capacity and client
// cancellation are deliberately excluded from capability health.
func ClassifyCapabilityError(status int, message, detail string) (CapabilityType, string, bool) {
	text := strings.ToLower(strings.TrimSpace(message + " " + detail))
	if text == "" {
		return "", "", false
	}
	transient := status == 429 || status == 502 || status == 503 || status == 524 || status == 529 ||
		strings.Contains(text, "overloaded") || strings.Contains(text, "temporarily unavailable")
	if transient || strings.Contains(text, "context canceled") || strings.Contains(text, "client_gone") ||
		strings.Contains(text, "insufficient balance") || strings.Contains(text, "insufficient_quota") {
		return "", "", false
	}
	if strings.Contains(text, "local_shell") || strings.Contains(text, "computer_call") || strings.Contains(text, "terminal") ||
		strings.Contains(text, "shell tool") {
		return CapabilityTerminalContract, "terminal_not_supported", true
	}
	if strings.Contains(text, "tool_result") || strings.Contains(text, "tool roundtrip") {
		return CapabilityToolRoundtrip, "tool_roundtrip_failed", true
	}
	if strings.Contains(text, "function_call") || strings.Contains(text, "custom_tool_call") ||
		strings.Contains(text, "tool use") || strings.Contains(text, "tool_use") || strings.Contains(text, "tools") {
		return CapabilityFunctionTool, "unsupported_tool", true
	}
	return "", "", false
}

func capabilityProtocolFromBody(body []byte) string {
	if gjson.GetBytes(body, "input").Exists() || gjson.GetBytes(body, "instructions").Exists() {
		return "openai_responses"
	}
	if gjson.GetBytes(body, "messages").Exists() && gjson.GetBytes(body, "max_tokens").Exists() {
		return "anthropic_messages"
	}
	if gjson.GetBytes(body, "messages").Exists() {
		return "openai_chat"
	}
	return "unknown"
}

func capabilityRequestEvidence(body []byte) (types []CapabilityType, hasIntent, hasRoundtrip, hasTerminal bool) {
	if len(body) == 0 || !json.Valid(body) {
		return nil, false, false, false
	}
	protocol := capabilityProtocolFromBody(body)
	hasTools := gjson.GetBytes(body, "tools").Exists() && len(gjson.GetBytes(body, "tools").Array()) > 0
	hasToolHistory := strings.Contains(string(body), `"tool_result"`) || strings.Contains(string(body), `"tool_calls"`) ||
		strings.Contains(string(body), `"function_call_output"`)
	hasTerminal = strings.Contains(string(body), `"local_shell"`) || strings.Contains(string(body), `"computer_call"`) ||
		strings.Contains(string(body), `"computer_use"`) || strings.Contains(strings.ToLower(string(body)), "exec_command")
	hasIntent = hasTools || hasToolHistory || hasTerminal
	if hasTools || hasToolHistory {
		types = append(types, CapabilityFunctionTool)
	}
	if hasToolHistory {
		hasRoundtrip = true
		types = append(types, CapabilityToolRoundtrip, CapabilityClientTool)
	}
	if hasTerminal {
		types = append(types, CapabilityTerminalContract)
	}
	_ = protocol
	return uniqueCapabilityTypes(types), hasIntent, hasRoundtrip, hasTerminal
}

func uniqueCapabilityTypes(types []CapabilityType) []CapabilityType {
	seen := make(map[CapabilityType]struct{}, len(types))
	out := make([]CapabilityType, 0, len(types))
	for _, typ := range types {
		if typ == "" {
			continue
		}
		if _, ok := seen[typ]; ok {
			continue
		}
		seen[typ] = struct{}{}
		out = append(out, typ)
	}
	return out
}

func capabilityObservation(model string, protocol string, accountID int64, typ CapabilityType, outcome CapabilityOutcome, reason, requestID string) *AccountCapabilityObservation {
	return &AccountCapabilityObservation{
		AccountID: accountID, UpstreamModel: strings.TrimSpace(model), Protocol: strings.TrimSpace(protocol),
		CapabilityType: typ, Outcome: outcome, Reason: strings.TrimSpace(reason), RequestID: strings.TrimSpace(requestID), CreatedAt: time.Now().UTC(),
	}
}

func recordCapabilityObservation(ctx context.Context, repo UsageLogRepository, observation *AccountCapabilityObservation) {
	if observation == nil || observation.AccountID <= 0 {
		return
	}
	reader, ok := repo.(AccountCapabilityRepository)
	if !ok {
		return
	}
	if err := reader.RecordCapabilityObservation(ctx, observation); err != nil {
		// Capability observation must never affect request success or scheduling.
		_ = err
	}
}

// ObserveCapabilitySuccess records only evidence already present in a client
// tool round-trip. It intentionally does not infer support from a tools
// declaration followed by an ordinary text answer.
func ObserveCapabilitySuccess(ctx context.Context, repo UsageLogRepository, account *Account, body []byte, model, requestID string) {
	if account == nil {
		return
	}
	types, hasIntent, hasRoundtrip, _ := capabilityRequestEvidence(body)
	if !hasIntent || !hasRoundtrip {
		return
	}
	protocol := capabilityProtocolFromBody(body)
	for _, typ := range types {
		if typ == CapabilityFunctionTool && !strings.Contains(string(body), `"tool_result"`) &&
			!strings.Contains(string(body), `"function_call_output"`) && !strings.Contains(string(body), `"tool_calls"`) {
			continue
		}
		recordCapabilityObservation(ctx, repo, capabilityObservation(model, protocol, account.ID, typ, CapabilitySuccess, "tool_roundtrip_observed", requestID))
	}
}

func ObserveCapabilityFailure(ctx context.Context, repo UsageLogRepository, body []byte, model, requestID string, events []*OpsUpstreamErrorEvent) {
	for _, observation := range capabilityFailureObservations(body, model, requestID, events) {
		recordCapabilityObservation(ctx, repo, observation)
	}
}

// capabilityFailureObservations collapses retries of the same capability on
// the same account within one gateway request. A failover chain can contain
// multiple events for one account, but it must contribute one sample rather
// than inflating the account's failure count. Different accounts and
// capability types remain independent observations.
func capabilityFailureObservations(body []byte, model, requestID string, events []*OpsUpstreamErrorEvent) []*AccountCapabilityObservation {
	protocol := capabilityProtocolFromBody(body)
	requestedTypes, hasIntent, _, _ := capabilityRequestEvidence(body)
	if !hasIntent {
		return nil
	}
	requested := make(map[CapabilityType]struct{}, len(requestedTypes))
	for _, typ := range requestedTypes {
		requested[typ] = struct{}{}
	}
	observations := make([]*AccountCapabilityObservation, 0, len(events))
	seen := make(map[string]struct{}, len(events))
	for _, event := range events {
		if event == nil || event.AccountID <= 0 {
			continue
		}
		typ, reason, ok := ClassifyCapabilityError(event.UpstreamStatusCode, event.Message, event.Detail)
		if !ok {
			continue
		}
		if _, intended := requested[typ]; !intended {
			continue
		}
		key := fmt.Sprintf("%d\x00%s", event.AccountID, typ)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		observations = append(observations, capabilityObservation(model, protocol, event.AccountID, typ, CapabilityFailure, reason, requestID))
	}
	return observations
}

func capabilityEventsFromContext(c *gin.Context) []*OpsUpstreamErrorEvent {
	if c == nil {
		return nil
	}
	value, ok := c.Get(OpsUpstreamErrorsKey)
	if !ok {
		return nil
	}
	events, _ := value.([]*OpsUpstreamErrorEvent)
	return events
}

// ObserveCapabilityAttempt is the small gateway-facing adapter. It snapshots
// only the event slice and request metadata; no body or credentials are stored.
func ObserveCapabilityAttempt(ctx context.Context, c *gin.Context, repo UsageLogRepository, account *Account, body []byte, model, requestID string, success bool) {
	if c != nil {
		if observed, ok := c.Get(accountCapabilityObservedKey); ok && observed == true {
			return
		}
		c.Set(accountCapabilityObservedKey, true)
	}
	if account == nil {
		return
	}
	events := capabilityEventsFromContext(c)
	// A request can fail over through several accounts before succeeding. Keep
	// the failed capability evidence attached to the account from each event;
	// otherwise only the final successful account would ever get observed.
	if len(events) > 0 {
		ObserveCapabilityFailure(ctx, repo, body, model, requestID, events)
	}
	if success {
		ObserveCapabilitySuccess(ctx, repo, account, body, model, requestID)
		return
	}
	if len(events) == 0 {
		return
	}
}

func CapabilityStatusState(sample, success, failure int64, lastFailure, now time.Time) string {
	if sample == 0 {
		return capabilityUnknown
	}
	if failure >= 2 && success == 0 {
		return capabilityUnsupported
	}
	if success >= 3 && (failure == 0 || lastFailure.IsZero() || now.Sub(lastFailure) >= 30*time.Minute) {
		return capabilityCapable
	}
	return capabilityDegraded
}

func validateCapabilityType(typ CapabilityType) error {
	switch typ {
	case CapabilityFunctionTool, CapabilityToolRoundtrip, CapabilityClientTool, CapabilityTerminalContract:
		return nil
	default:
		return fmt.Errorf("unknown capability type %q", typ)
	}
}

// ValidateCapabilityTypeForRepository keeps SQL adapters from duplicating the
// capability enum and is intentionally narrow; it is not a general validator.
func ValidateCapabilityTypeForRepository(typ CapabilityType) error {
	return validateCapabilityType(typ)
}

// AccountCapabilityStatusForRepository converts nullable SQL timestamps into
// the API status shape without exposing database/sql in the service package.
func AccountCapabilityStatusForRepository(state string, sample, success, failure int64, lastSuccess, lastFailure, updated time.Time, lastReason string, hasLastSuccess, hasLastFailure, hasUpdated bool) AccountCapabilityStatus {
	resolvedState := CapabilityStatusState(sample, success, failure, lastFailure, time.Now().UTC())
	if sample == 0 && strings.TrimSpace(state) == capabilityUnknown {
		resolvedState = capabilityUnknown
	}
	status := AccountCapabilityStatus{State: resolvedState, SampleCount: sample, SuccessCount: success, FailureCount: failure, LastReason: strings.TrimSpace(lastReason)}
	if hasLastSuccess {
		value := lastSuccess
		status.LastSuccessAt = &value
	}
	if hasLastFailure {
		value := lastFailure
		status.LastFailureAt = &value
	}
	if hasUpdated {
		value := updated
		status.UpdatedAt = &value
	}
	return status
}
