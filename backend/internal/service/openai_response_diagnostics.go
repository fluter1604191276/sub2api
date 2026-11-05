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
const openAIResponseDiagnosticsSinkContextKey = "openai_response_diagnostics_sink"

// OpenAIResponseDiagnosticSnapshot is a scalar-only view of one Responses
// exchange. Pointer token values preserve the distinction between absent and
// an explicit zero supplied by an upstream.
type OpenAIResponseDiagnosticSnapshot struct {
	// Active is false until a native Responses HTTP attempt is constructed.
	Active bool
	// AccountID belongs to the observed outbound attempt, never the next candidate.
	AccountID                int64
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
	sink              func(OpenAIResponseDiagnosticSnapshot)
	published         bool
}

func beginOpenAIResponseDiagnostics(c *gin.Context, body []byte) *openAIResponseDiagnostics {
	return beginOpenAIResponseDiagnosticsForTurn(c, body, false)
}

// beginOpenAIResponseDiagnosticsForTurn resets attempt-local fields. WebSocket
// turns supply their own client frame cap; ordinary HTTP failover preserves the
// original request cap for the whole request.
func beginOpenAIResponseDiagnosticsForTurn(c *gin.Context, body []byte, replaceRequestedCap bool) *openAIResponseDiagnostics {
	if c == nil || isOpenAIResponsesCompactPath(c) {
		return nil
	}
	if existing := openAIResponseDiagnosticsFromContext(c); existing != nil {
		// A handler can call Forward again after account failover. Until the next
		// native attempt is actually built, suppress the prior attempt so a
		// bridge/local return cannot be attributed to stale metadata.
		existing.mu.Lock()
		var pending OpenAIResponseDiagnosticSnapshot
		var sink func(OpenAIResponseDiagnosticSnapshot)
		// A WebSocket connection can start its next response.create before a
		// provisional bare error receives an authoritative terminal (or before
		// it receives any terminal). Preserve that completed attempt boundary;
		// ordinary HTTP failover deliberately keeps its existing fallback rule.
		if replaceRequestedCap && existing.snapshot.Active && !existing.published && existing.sink != nil {
			pending = existing.snapshot
			sink = existing.sink
			existing.published = true
		}
		requested := existing.snapshot.RequestedMaxOutputTokens
		if replaceRequestedCap {
			requested = jsonInt64(body, "max_output_tokens")
		}
		existing.snapshot = OpenAIResponseDiagnosticSnapshot{RequestedMaxOutputTokens: requested}
		existing.terminalEventType = ""
		existing.published = false
		existing.mu.Unlock()
		if sink != nil {
			sink(pending)
		}
		return existing
	}
	d := &openAIResponseDiagnostics{}
	if sink, ok := c.Get(openAIResponseDiagnosticsSinkContextKey); ok {
		d.sink, _ = sink.(func(OpenAIResponseDiagnosticSnapshot))
	}
	d.snapshot.RequestedMaxOutputTokens = jsonInt64(body, "max_output_tokens")
	c.Set(openAIResponseDiagnosticsContextKey, d)
	return d
}

// BindOpenAIResponseDiagnosticSink receives immutable scalar snapshots as
// formal Responses terminals arrive. It is deliberately request-scoped; the
// handler owns queueing and persistence.
func BindOpenAIResponseDiagnosticSink(c *gin.Context, sink func(OpenAIResponseDiagnosticSnapshot)) {
	if c == nil {
		return
	}
	c.Set(openAIResponseDiagnosticsSinkContextKey, sink)
	if d := openAIResponseDiagnosticsFromContext(c); d != nil {
		d.mu.Lock()
		d.sink = sink
		d.mu.Unlock()
	}
}

// TakeOpenAIResponseDiagnosticSnapshot returns a final fallback only when a
// terminal was not already delivered to the request sink.
func TakeOpenAIResponseDiagnosticSnapshot(c *gin.Context) (OpenAIResponseDiagnosticSnapshot, bool) {
	d := openAIResponseDiagnosticsFromContext(c)
	if d == nil {
		return OpenAIResponseDiagnosticSnapshot{}, false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.snapshot.Active || d.published {
		return OpenAIResponseDiagnosticSnapshot{}, false
	}
	d.published = true
	return d.snapshot, true
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

func observeOpenAIResponseDiagnosticForwarded(c *gin.Context, body []byte, accountIDs ...int64) {
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
		ForwardedMaxOutputTokens: firstDiagnosticCap(body),
	}
	if len(accountIDs) > 0 {
		d.snapshot.AccountID = accountIDs[0]
	}
	d.terminalEventType = ""
	d.published = false
	d.mu.Unlock()
}

func firstDiagnosticCap(body []byte) *int64 {
	for _, path := range []string{"max_output_tokens", "max_completion_tokens", "max_tokens", "generationConfig.maxOutputTokens", "output_config.max_tokens"} {
		if value := jsonInt64(body, path); value != nil {
			return value
		}
	}
	return nil
}

// Observe all frames for diagnostics, independently of the existing converter
// selecting its final client payload and usage.
func observeOpenAIResponseDiagnosticSSEBody(c *gin.Context, body []byte) {
	if openAIResponseDiagnosticsFromContext(c) == nil {
		return
	}
	forEachOpenAISSEFrame(string(body), func(eventType string, data []byte) {
		observeOpenAIResponseDiagnosticUsage(c, data)
		if openAIStreamEventIsTerminalWithType(string(data), eventType) {
			observeOpenAIResponseDiagnosticTerminal(c, data, eventType, nil)
		}
	})
}

// observeOpenAIResponseDiagnosticPayload records scalar metadata from a
// Responses-shaped payload produced by a protocol bridge. The caller retains
// ownership of the payload and may still write it unchanged to the client.
func observeOpenAIResponseDiagnosticPayload(c *gin.Context, payload []byte) {
	if openAIResponseDiagnosticsFromContext(c) == nil {
		return
	}
	observeOpenAIResponseDiagnosticUsage(c, payload)
	observeOpenAIResponseDiagnosticTerminal(c, payload, gjson.GetBytes(payload, "type").String(), nil)
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

	eventType = strings.TrimSpace(eventType)
	// Codex can send a provisional bare error followed by an authoritative
	// response.failed or response.completed. Keep the latter; otherwise the
	// first terminal remains authoritative and persistence stays one row.
	status := normalizeOpenAIResponseDiagnosticStatus(firstString(value, "response.status", "status"))
	if status == "" {
		status = terminalStatusFromEvent(eventType)
	}
	if status == "" {
		d.mu.Unlock()
		return
	}
	if d.snapshot.Terminal && !(d.terminalEventType == "error" && eventType != "error") {
		d.mu.Unlock()
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
	// A bare error may be superseded by an authoritative response terminal;
	// leave it for middleware fallback so we never publish two records for one
	// ordinary HTTP attempt.
	if eventType == "error" || d.published || d.sink == nil {
		d.mu.Unlock()
		return
	}
	snapshot := d.snapshot
	sink := d.sink
	d.published = true
	d.mu.Unlock()
	sink(snapshot)
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
