package service

// This file holds the deliberately small request-scoped observation used by
// ops response diagnostics.  It never retains a request/response body: only
// scalar protocol metadata which is useful when a Responses request returns a
// terminal in-band failure (HTTP 200 + response.incomplete, for example).

import (
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const openAIResponseDiagnosticsContextKey = "openai_response_diagnostics"

// OpenAIResponseDiagnosticSnapshot is a scalar-only view of one Responses
// exchange. Pointer token values preserve the distinction between absent and
// an explicit zero supplied by an upstream.
type OpenAIResponseDiagnosticSnapshot struct {
	// Active is false until a native Responses HTTP attempt is constructed.
	Active                   bool
	ResponseID               string
	UpstreamRequestID        string
	Status                   string
	Reason                   string
	RequestedMaxOutputTokens *int64
	ForwardedMaxOutputTokens *int64
	UpstreamMaxOutputTokens  *int64
	InputTokens              *int64
	CacheReadTokens          *int64
	CacheCreationTokens      *int64
	OutputTokens             *int64
	ReasoningTokens          *int64
	Terminal                 bool
	ClientDisconnected       bool
}

type openAIResponseDiagnostics struct {
	mu                sync.RWMutex
	snapshot          OpenAIResponseDiagnosticSnapshot
	terminalEventType string
}

func beginOpenAIResponseDiagnostics(c *gin.Context, body []byte) *openAIResponseDiagnostics {
	if c == nil || isOpenAIResponsesCompactPath(c) {
		return nil
	}
	if existing := openAIResponseDiagnosticsFromContext(c); existing != nil {
		// A handler can call Forward again after account failover. Until the next
		// native attempt is actually built, suppress the prior attempt so a
		// bridge/local return cannot be attributed to stale metadata.
		existing.mu.Lock()
		requested := existing.snapshot.RequestedMaxOutputTokens
		existing.snapshot = OpenAIResponseDiagnosticSnapshot{RequestedMaxOutputTokens: requested}
		existing.terminalEventType = ""
		existing.mu.Unlock()
		return existing
	}
	d := &openAIResponseDiagnostics{}
	d.snapshot.RequestedMaxOutputTokens = jsonInt64(body, "max_output_tokens")
	c.Set(openAIResponseDiagnosticsContextKey, d)
	return d
}

func openAIResponseDiagnosticsFromContext(c *gin.Context) *openAIResponseDiagnostics {
	if c == nil {
		return nil
	}
	v, ok := c.Get(openAIResponseDiagnosticsContextKey)
	if !ok {
		return nil
	}
	d, _ := v.(*openAIResponseDiagnostics)
	return d
}

// OpenAIResponseDiagnosticSnapshotFromContext returns an immutable copy.
func OpenAIResponseDiagnosticSnapshotFromContext(c *gin.Context) (OpenAIResponseDiagnosticSnapshot, bool) {
	d := openAIResponseDiagnosticsFromContext(c)
	if d == nil {
		return OpenAIResponseDiagnosticSnapshot{}, false
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.snapshot, true
}

func observeOpenAIResponseDiagnosticForwarded(c *gin.Context, body []byte) {
	d := openAIResponseDiagnosticsFromContext(c)
	if d == nil {
		return
	}
	d.mu.Lock()
	// A retry/failover selects a fresh native attempt. Keep the original client
	// cap while replacing attempt-local terminal metadata.
	requested := d.snapshot.RequestedMaxOutputTokens
	d.snapshot = OpenAIResponseDiagnosticSnapshot{
		Active:                   true,
		RequestedMaxOutputTokens: requested,
		ForwardedMaxOutputTokens: jsonInt64(body, "max_output_tokens"),
	}
	d.terminalEventType = ""
	d.mu.Unlock()
}

func observeOpenAIResponseDiagnosticHeaders(c *gin.Context, upstreamRequestID string) {
	d := openAIResponseDiagnosticsFromContext(c)
	if d == nil {
		return
	}
	d.mu.Lock()
	if id := boundedOpenAIResponseDiagnosticString(upstreamRequestID, 256); id != "" {
		d.snapshot.UpstreamRequestID = id
	}
	d.mu.Unlock()
}

func observeOpenAIResponseDiagnosticTerminal(c *gin.Context, payload []byte, eventType string, _ *OpenAIUsage) {
	d := openAIResponseDiagnosticsFromContext(c)
	if d == nil {
		return
	}
	value := gjson.ParseBytes(payload)
	d.mu.Lock()
	defer d.mu.Unlock()

	eventType = strings.TrimSpace(eventType)
	// Codex can send a provisional bare error followed by an authoritative
	// response.failed or response.completed. Keep the latter; otherwise the
	// first terminal remains authoritative and persistence stays one row.
	status := normalizeOpenAIResponseDiagnosticStatus(firstString(value, "response.status", "status"))
	if status == "" {
		status = terminalStatusFromEvent(eventType)
	}
	if status == "" {
		return
	}
	if d.snapshot.Terminal && !(d.terminalEventType == "error" && eventType != "error") {
		return
	}
	d.snapshot.Terminal = true
	d.terminalEventType = eventType
	d.snapshot.Status = status
	d.snapshot.Reason = firstStringLimit(value, 128,
		"response.incomplete_details.reason", "incomplete_details.reason",
		"response.error.code", "error.code", "code")
	d.snapshot.ResponseID = firstString(value, "response.id", "id")
	d.snapshot.UpstreamMaxOutputTokens = firstInt64(value,
		"response.max_output_tokens", "max_output_tokens")
	observeOpenAIResponseDiagnosticUsageLocked(&d.snapshot, value)
	d.snapshot.ReasoningTokens = firstInt64(value,
		"response.usage.output_tokens_details.reasoning_tokens",
		"usage.output_tokens_details.reasoning_tokens",
		"response.usage.completion_tokens_details.reasoning_tokens",
		"usage.completion_tokens_details.reasoning_tokens")
}

// observeOpenAIResponseDiagnosticUsage retains only fields which were present
// as JSON integer values. A missing usage object must remain NULL in storage;
// it must never be converted into a synthetic zero by an accumulator.
func observeOpenAIResponseDiagnosticUsage(c *gin.Context, payload []byte) {
	d := openAIResponseDiagnosticsFromContext(c)
	if d == nil {
		return
	}
	d.mu.Lock()
	if d.snapshot.Terminal && d.terminalEventType != "error" {
		d.mu.Unlock()
		return
	}
	observeOpenAIResponseDiagnosticUsageLocked(&d.snapshot, gjson.ParseBytes(payload))
	d.mu.Unlock()
}

func observeOpenAIResponseDiagnosticUsageLocked(dst *OpenAIResponseDiagnosticSnapshot, value gjson.Result) {
	if dst == nil {
		return
	}
	if v := firstInt64(value, "response.usage.input_tokens", "usage.input_tokens", "response.usage.prompt_tokens", "usage.prompt_tokens"); v != nil {
		dst.InputTokens = v
	}
	if v := firstInt64(value, "response.usage.input_tokens_details.cached_tokens", "usage.input_tokens_details.cached_tokens", "response.usage.prompt_tokens_details.cached_tokens", "usage.prompt_tokens_details.cached_tokens", "response.usage.cache_read_tokens", "usage.cache_read_tokens"); v != nil {
		dst.CacheReadTokens = v
	}
	if v := firstInt64(value, "response.usage.cache_creation_input_tokens", "usage.cache_creation_input_tokens"); v != nil {
		dst.CacheCreationTokens = v
	}
	if v := firstInt64(value, "response.usage.output_tokens", "usage.output_tokens", "response.usage.completion_tokens", "usage.completion_tokens"); v != nil {
		dst.OutputTokens = v
	}
}

func observeOpenAIResponseDiagnosticClientDisconnected(c *gin.Context) {
	d := openAIResponseDiagnosticsFromContext(c)
	if d == nil {
		return
	}
	d.mu.Lock()
	d.snapshot.ClientDisconnected = true
	d.mu.Unlock()
}

func jsonInt64(body []byte, path string) *int64 {
	if len(body) == 0 {
		return nil
	}
	return resultInt64(gjson.GetBytes(body, path))
}

func firstInt64(value gjson.Result, paths ...string) *int64 {
	for _, path := range paths {
		if n := resultInt64(value.Get(path)); n != nil {
			return n
		}
	}
	return nil
}

func resultInt64(value gjson.Result) *int64 {
	if !value.Exists() || value.Type != gjson.Number {
		return nil
	}
	n, err := strconv.ParseInt(value.Raw, 10, 64)
	if err != nil || n < 0 {
		return nil
	}
	return openAIResponseDiagnosticInt64Ptr(n)
}

func openAIResponseDiagnosticInt64Ptr(value int64) *int64 { return &value }

func firstString(value gjson.Result, paths ...string) string {
	return firstStringLimit(value, 256, paths...)
}

func firstStringLimit(value gjson.Result, limit int, paths ...string) string {
	for _, path := range paths {
		if candidate := boundedOpenAIResponseDiagnosticString(value.Get(path).String(), limit); candidate != "" {
			return candidate
		}
	}
	return ""
}

func boundedOpenAIResponseDiagnosticString(value string, limit int) string {
	value = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, value))
	if len(value) > limit {
		return ""
	}
	return value
}

func terminalStatusFromEvent(eventType string) string {
	switch strings.TrimSpace(eventType) {
	case "response.completed", "response.done":
		return "completed"
	case "response.incomplete":
		return "incomplete"
	case "response.failed", "error":
		return "failed"
	case "response.cancelled", "response.canceled":
		// The persistence contract has no separate cancellation status. Retain
		// it as a controlled failure rather than rejecting the entire record.
		return "failed"
	default:
		return ""
	}
}

func normalizeOpenAIResponseDiagnosticStatus(value string) string {
	switch strings.TrimSpace(value) {
	case "completed", "incomplete", "failed":
		return strings.TrimSpace(value)
	default:
		return ""
	}
}
