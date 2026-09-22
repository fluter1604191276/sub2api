package service

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestClassifyCapabilityError(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		message    string
		detail     string
		wantType   CapabilityType
		wantReason string
		wantRecord bool
	}{
		{
			name:       "terminal contract failure",
			status:     400,
			message:    "invalid local_shell tool definition",
			wantType:   CapabilityTerminalContract,
			wantReason: "terminal_not_supported",
			wantRecord: true,
		},
		{
			name:       "tool round trip failure",
			status:     400,
			message:    "tool_result was rejected by upstream",
			wantType:   CapabilityToolRoundtrip,
			wantReason: "tool_roundtrip_failed",
			wantRecord: true,
		},
		{
			name:       "function tool failure",
			status:     400,
			message:    "custom_tool_call is not supported",
			wantType:   CapabilityFunctionTool,
			wantReason: "unsupported_tool",
			wantRecord: true,
		},
		{
			name:       "provider overload excluded",
			status:     503,
			message:    "Our servers are currently overloaded",
			wantRecord: false,
		},
		{
			name:       "client cancellation excluded",
			status:     499,
			message:    "context canceled by client",
			wantRecord: false,
		},
		{
			name:       "balance failure excluded",
			status:     402,
			message:    "insufficient_quota",
			wantRecord: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotType, gotReason, gotRecord := ClassifyCapabilityError(tt.status, tt.message, tt.detail)
			if gotType != tt.wantType || gotReason != tt.wantReason || gotRecord != tt.wantRecord {
				t.Fatalf("ClassifyCapabilityError() = (%q, %q, %v), want (%q, %q, %v)", gotType, gotReason, gotRecord, tt.wantType, tt.wantReason, tt.wantRecord)
			}
		})
	}
}

func TestCapabilityRequestEvidenceRequiresToolEvidence(t *testing.T) {
	plainTools := []byte(`{"model":"test","tools":[{"type":"function","name":"search"}],"input":"hello"}`)
	types, hasIntent, hasRoundtrip, hasTerminal := capabilityRequestEvidence(plainTools)
	if !hasIntent || hasRoundtrip || hasTerminal || len(types) != 1 || types[0] != CapabilityFunctionTool {
		t.Fatalf("declaration evidence = (%v, %v, %v, %v), want function intent without round-trip", types, hasIntent, hasRoundtrip, hasTerminal)
	}

	roundtrip := []byte(`{"model":"test","tools":[{"type":"function","name":"search"}],"input":[{"type":"function_call_output","call_id":"1"}]}`)
	types, hasIntent, hasRoundtrip, hasTerminal = capabilityRequestEvidence(roundtrip)
	if !hasIntent || !hasRoundtrip || hasTerminal || len(types) != 3 {
		t.Fatalf("round-trip evidence = (%v, %v, %v, %v), want function/tool/client evidence", types, hasIntent, hasRoundtrip, hasTerminal)
	}
}

func TestCapabilityStatusState(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name                  string
		sample, success, fail int64
		lastFailure           time.Time
		want                  string
	}{
		{name: "no evidence", want: capabilityUnknown},
		{name: "sparse success", sample: 1, success: 1, want: capabilityDegraded},
		{name: "repeated failures", sample: 2, fail: 2, want: capabilityUnsupported},
		{name: "healthy success", sample: 3, success: 3, want: capabilityCapable},
		{name: "recent failure keeps degraded", sample: 4, success: 3, fail: 1, lastFailure: now.Add(-10 * time.Minute), want: capabilityDegraded},
		{name: "old failure permits recovery", sample: 4, success: 3, fail: 1, lastFailure: now.Add(-31 * time.Minute), want: capabilityCapable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CapabilityStatusState(tt.sample, tt.success, tt.fail, tt.lastFailure, now)
			if got != tt.want {
				t.Fatalf("CapabilityStatusState() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCapabilityFailureObservationsDeduplicateRetriesPerAccountAndType(t *testing.T) {
	body := []byte(`{"model":"test","tools":[{"type":"function","name":"search"},{"type":"local_shell"}],"input":"hello"}`)
	events := []*OpsUpstreamErrorEvent{
		{AccountID: 11, UpstreamStatusCode: 400, Message: "custom_tool_call is not supported"},
		{AccountID: 11, UpstreamStatusCode: 400, Message: "custom_tool_call is not supported"},
		{AccountID: 11, UpstreamStatusCode: 400, Message: "invalid local_shell tool definition"},
		{AccountID: 22, UpstreamStatusCode: 400, Message: "custom_tool_call is not supported"},
	}

	observations := capabilityFailureObservations(body, "test", "request-1", events)
	if len(observations) != 3 {
		t.Fatalf("got %d observations, want one per account and capability type", len(observations))
	}
	if observations[0].AccountID != 11 || observations[0].CapabilityType != CapabilityFunctionTool {
		t.Fatalf("first observation = %+v", observations[0])
	}
	if observations[1].AccountID != 11 || observations[1].CapabilityType != CapabilityTerminalContract {
		t.Fatalf("second observation = %+v", observations[1])
	}
	if observations[2].AccountID != 22 || observations[2].CapabilityType != CapabilityFunctionTool {
		t.Fatalf("third observation = %+v", observations[2])
	}
}

func TestCapabilityFailureObservationsRequireMatchingRequestIntent(t *testing.T) {
	plainBody := []byte(`{"model":"test","input":"hello"}`)
	events := []*OpsUpstreamErrorEvent{{AccountID: 11, UpstreamStatusCode: 400, Message: "custom_tool_call is not supported"}}
	if observations := capabilityFailureObservations(plainBody, "test", "request-1", events); len(observations) != 0 {
		t.Fatalf("plain request produced capability observations: %+v", observations)
	}

	terminalBody := []byte(`{"model":"test","tools":[{"type":"function","name":"search"}],"input":"hello"}`)
	terminalEvents := []*OpsUpstreamErrorEvent{{AccountID: 11, UpstreamStatusCode: 400, Message: "local_shell is not supported"}}
	if observations := capabilityFailureObservations(terminalBody, "test", "request-2", terminalEvents); len(observations) != 0 {
		t.Fatalf("non-terminal intent produced terminal observations: %+v", observations)
	}
}

func TestCapabilitySuccessRequiresToolRoundtrip(t *testing.T) {
	repo := &capabilityObservationRecorder{}
	account := &Account{ID: 11}
	plainTools := []byte(`{"model":"test","tools":[{"type":"function","name":"search"}],"input":"hello"}`)
	ObserveCapabilitySuccess(context.Background(), repo, account, plainTools, "test", "request-1")
	if len(repo.observations) != 0 {
		t.Fatalf("tool declaration alone produced success observations: %+v", repo.observations)
	}

	roundtrip := []byte(`{"model":"test","tools":[{"type":"function","name":"search"}],"input":[{"type":"function_call_output","call_id":"1","output":"ok"}]}`)
	ObserveCapabilitySuccess(context.Background(), repo, account, roundtrip, "test", "request-2")
	if len(repo.observations) == 0 {
		t.Fatal("tool roundtrip did not produce a success observation")
	}
}

func TestObserveCapabilityAttemptDeduplicatesRecursiveGatewayEntry(t *testing.T) {
	repo := &capabilityObservationRecorder{}
	account := &Account{ID: 17}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	body := []byte(`{"model":"test","messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool-1","content":"ok"}]}],"tools":[{"name":"search"}]}`)

	ObserveCapabilityAttempt(context.Background(), c, repo, account, body, "test", "request-1", true)
	ObserveCapabilityAttempt(context.Background(), c, repo, account, body, "test", "request-1", true)

	if len(repo.observations) != 3 {
		t.Fatalf("got %d observations after recursive entry, want one observation per capability type", len(repo.observations))
	}
}

type capabilityObservationRecorder struct {
	UsageLogRepository
	observations []*AccountCapabilityObservation
}

func (r *capabilityObservationRecorder) RecordCapabilityObservation(_ context.Context, observation *AccountCapabilityObservation) error {
	r.observations = append(r.observations, observation)
	return nil
}

func (*capabilityObservationRecorder) GetAccountCapabilitySummaryBatch(context.Context, []int64) (map[int64]AccountCapabilitySummary, error) {
	return nil, nil
}

func TestAccountCapabilityStatusAggregatesUnknownSafely(t *testing.T) {
	status := AccountCapabilityStatusForRepository("capable", 0, 0, 0, time.Time{}, time.Time{}, time.Time{}, "", false, false, false)
	if status.State != capabilityUnknown {
		t.Fatalf("empty evidence must remain unknown, got %q", status.State)
	}

	now := time.Now().UTC()
	status = AccountCapabilityStatusForRepository("stale", 3, 3, 0, now, time.Time{}, now, "", true, false, true)
	if status.State != capabilityCapable || status.SuccessCount != 3 {
		t.Fatalf("aggregated success status = %+v", status)
	}
}
