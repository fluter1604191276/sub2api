package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

type upstreamAttemptSourceKey struct{}

// WithUpstreamAttemptSource classifies trusted internal work without changing
// authentication, billing, account selection, or the request sent upstream.
func WithUpstreamAttemptSource(ctx context.Context, source string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, upstreamAttemptSourceKey{}, source)
}

// UpstreamAttemptRecorder writes an independent shadow ledger. Its inputs contain
// only protocol metadata. Neither request nor response bodies enter the ledger.
type UpstreamAttemptRecorder struct {
	repo  UpstreamAttemptRepository
	cost  *UpstreamAttemptCostCalculator
	queue *upstreamAttemptQueue
}

func NewUpstreamAttemptRecorder(repo UpstreamAttemptRepository, cost *UpstreamAttemptCostCalculator) *UpstreamAttemptRecorder {
	return &UpstreamAttemptRecorder{repo: repo, cost: cost, queue: defaultUpstreamAttemptQueue}
}

func (r *UpstreamAttemptRecorder) record(ctx context.Context, attempt *UpstreamAttempt) {
	if r == nil || r.repo == nil || attempt == nil {
		return
	}
	copy := *attempt
	// Price uses the already frozen snapshot and performs no repository reads.
	// Release the pricing graph before queueing scalar persistence metadata.
	if r.cost != nil {
		if err := r.cost.Price(context.Background(), &copy); err != nil {
			slog.Warn("upstream_attempt_price_failed", "attempt_id", copy.AttemptID)
		}
	}
	copy.accountStatsChannel = nil
	copy.pricingSnapshot = nil
	r.queue.enqueue(upstreamAttemptJob{repository: r.repo, attempt: copy})
}

type observedHTTPUpstream struct {
	base     HTTPUpstream
	recorder *UpstreamAttemptRecorder
}

func NewObservedHTTPUpstream(base HTTPUpstream, recorder *UpstreamAttemptRecorder) HTTPUpstream {
	return &observedHTTPUpstream{base: base, recorder: recorder}
}

func (u *observedHTTPUpstream) Do(req *http.Request, proxyURL string, accountID int64, concurrency int) (*http.Response, error) {
	attempt := u.start(req, accountID)
	if err := u.recorder.guardChannelMonitorV3Attempt(req, attempt); err != nil {
		return nil, err
	}
	tracked, observed := u.trackRoundTrips(req, attempt, accountID)
	resp, err := u.base.Do(tracked, proxyURL, accountID, concurrency)
	if observed.Load() {
		return resp, err
	}
	return u.observe(req, attempt, resp, err)
}

func (u *observedHTTPUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	attempt := u.start(req, accountID)
	if err := u.recorder.guardChannelMonitorV3Attempt(req, attempt); err != nil {
		return nil, err
	}
	tracked, observed := u.trackRoundTrips(req, attempt, accountID)
	resp, err := u.base.DoWithTLS(tracked, proxyURL, accountID, concurrency, profile)
	if observed.Load() {
		return resp, err
	}
	return u.observe(req, attempt, resp, err)
}

func upstreamAttemptProtocol(req *http.Request) string {
	if req == nil || req.URL == nil || req.Method != http.MethodPost {
		return ""
	}
	path := strings.TrimRight(req.URL.Path, "/")
	switch {
	case strings.HasSuffix(path, "/chat/completions"):
		return "chat_completions"
	case strings.HasSuffix(path, "/responses") || strings.HasSuffix(path, "/responses/compact"):
		return "responses"
	case strings.HasSuffix(path, "/messages"):
		return "anthropic"
	case strings.Contains(path, ":generateContent") || strings.Contains(path, ":streamGenerateContent"):
		return "gemini"
	}
	return ""
}

func (u *observedHTTPUpstream) start(req *http.Request, accountID int64) *UpstreamAttempt {
	protocol := upstreamAttemptProtocol(req)
	if protocol == "" || accountID <= 0 || u.recorder == nil {
		return nil
	}
	ctx := req.Context()
	a := &UpstreamAttempt{AttemptID: uuid.NewString(), AccountID: accountID,
		CreatedAt: time.Now().UTC(), Protocol: protocol, Source: "request", Outcome: "unknown"}
	a.RequestID, _ = ctx.Value(ctxkey.RequestID).(string)
	a.ClientRequestID, _ = ctx.Value(ctxkey.ClientRequestID).(string)
	if id, ok := ctx.Value(ctxkey.UserID).(int64); ok && id > 0 {
		a.UserID = &id
	}
	if group, ok := ctx.Value(ctxkey.Group).(*Group); ok && group != nil {
		id := group.ID
		a.GroupID = &id
	}
	if source, ok := ctx.Value(upstreamAttemptSourceKey{}).(string); ok {
		a.Source = source
	} else if IsOpenAIIQRequest(ctx) {
		a.Source = "openai_iq"
	}
	// GetBody reads a separate copy and never consumes or replaces the actual
	// upstream payload. Decode only the whitelisted price-selection metadata.
	if req.GetBody != nil {
		if body, err := req.GetBody(); err == nil {
			var meta struct {
				Model       string `json:"model"`
				ServiceTier string `json:"service_tier"`
				Reasoning   struct {
					Effort string `json:"effort"`
				} `json:"reasoning"`
			}
			if json.NewDecoder(io.LimitReader(body, 4<<20)).Decode(&meta) == nil {
				a.Model = meta.Model
				a.ServiceTier = meta.ServiceTier
				a.ReasoningEffort = meta.Reasoning.Effort
			}
			_ = body.Close()
		}
	}
	if a.Model == "" && protocol == "gemini" {
		if _, model, ok := strings.Cut(req.URL.Path, "/models/"); ok {
			a.Model, _, _ = strings.Cut(model, ":")
		}
	}
	if u.recorder.cost != nil {
		if err := u.recorder.cost.Snapshot(ctx, a); err != nil {
			slog.Warn("upstream_attempt_snapshot_failed", "attempt_id", a.AttemptID)
		}
	}
	return a
}

func (u *observedHTTPUpstream) observe(req *http.Request, a *UpstreamAttempt, resp *http.Response, err error) (*http.Response, error) {
	if a == nil {
		return resp, err
	}
	if resp == nil || resp.Body == nil {
		a.Outcome = "failed"
		u.recorder.record(req.Context(), a)
		return resp, err
	}
	a.HTTPStatus = resp.StatusCode
	headers := []string{a.UpstreamRequestIDHeader, "x-request-id", "request-id", "x-amzn-requestid", "x-goog-request-id"}
	for _, header := range headers {
		switch strings.ToLower(header) {
		case "", "authorization", "proxy-authorization", "cookie", "set-cookie", "x-api-key", "api-key", "x-auth-token", "x-access-token":
			continue
		}
		if id := resp.Header.Get(header); id != "" {
			a.UpstreamRequestID = id
			break
		}
	}
	if resp.StatusCode >= 400 || err != nil {
		a.Outcome = "failed"
	}
	resp.Body = &upstreamAttemptBody{body: resp.Body, ctx: req.Context(), recorder: u.recorder,
		attempt: a, stream: strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream")}
	return resp, err
}

// A bounded parser observes exactly the bytes the existing caller reads. It
// never drains a cancelled response, rewrites bytes, or drives another request.
const upstreamAttemptFrameLimit = 4 << 20

type upstreamAttemptBody struct {
	body        io.ReadCloser
	ctx         context.Context
	recorder    *UpstreamAttemptRecorder
	attempt     *UpstreamAttempt
	stream      bool
	buffer      []byte
	overflow    bool
	inputTotal  *int64
	outputTotal *int64
	mu          sync.Mutex
	once        sync.Once
	persist     sync.Once
	tailOnce    sync.Once
	bytesRead   atomic.Int64
	eof         atomic.Bool
	closeErr    error
}

func (b *upstreamAttemptBody) Read(p []byte) (int, error) {
	b.mu.Lock()
	n, err := b.body.Read(p)
	b.bytesRead.Add(int64(n))
	b.feed(p[:n])
	if err != nil {
		if err == io.EOF {
			b.eof.Store(true)
		}
		b.finish(err == io.EOF)
	}
	b.mu.Unlock()
	return n, err
}

func (b *upstreamAttemptBody) Close() error {
	b.persist.Do(func() {
		b.ObserveUpstreamAttemptErrorTail()
		b.closeErr = b.body.Close()
		b.mu.Lock()
		b.finish(false)
		b.recorder.record(b.ctx, b.attempt)
		b.mu.Unlock()
	})
	return b.closeErr
}

// ObserveUpstreamAttemptErrorTail captures the bounded unread portion of an
// error response before an outer transport wrapper cancels or closes it. It is
// safe for both the repository transport and this body's Close to call; only
// the first call performs the bounded drain.
func (b *upstreamAttemptBody) ObserveUpstreamAttemptErrorTail() {
	if b == nil {
		return
	}
	b.tailOnce.Do(func() {
		if b.attempt == nil || b.attempt.HTTPStatus < http.StatusBadRequest || b.eof.Load() {
			return
		}
		// Error handlers often read a prefix. Capture a bounded tail while the
		// request still owns the connection, then release it promptly.
		remaining := int64(upstreamAttemptFrameLimit+1) - b.bytesRead.Load()
		if remaining <= 0 {
			return
		}
		done := make(chan struct{})
		go func() { _, _ = io.CopyN(io.Discard, b, remaining); close(done) }()
		select {
		case <-done:
		case <-time.After(100 * time.Millisecond):
		}
	})
}

func (b *upstreamAttemptBody) feed(data []byte) {
	if !b.stream {
		if !b.overflow && len(b.buffer)+len(data) <= upstreamAttemptFrameLimit {
			b.buffer = append(b.buffer, data...)
		} else {
			b.buffer = nil
			b.overflow = true
		}
		trimmed := bytes.TrimSpace(b.buffer)
		if !b.overflow && (bytes.HasPrefix(trimmed, []byte("data:")) || bytes.HasPrefix(trimmed, []byte("event:")) || bytes.HasPrefix(trimmed, []byte(":"))) {
			buffered := b.buffer
			b.buffer = nil
			b.stream = true
			b.feed(buffered)
		}
		return
	}
	for len(data) > 0 {
		line, rest, complete := bytes.Cut(data, []byte{'\n'})
		if !b.overflow && len(b.buffer)+len(line) <= upstreamAttemptFrameLimit {
			b.buffer = append(b.buffer, line...)
		} else {
			b.buffer = nil
			b.overflow = true
		}
		if !complete {
			return
		}
		if !b.overflow {
			b.observeFrame(bytes.TrimSpace(bytes.TrimPrefix(b.buffer, []byte("data:"))))
		}
		b.buffer = nil
		b.overflow = false
		data = rest
	}
}

func (b *upstreamAttemptBody) finish(eof bool) {
	b.once.Do(func() {
		if !b.overflow {
			b.observeFrame(bytes.TrimSpace(bytes.TrimPrefix(b.buffer, []byte("data:"))))
		}
		b.buffer = nil
		if b.attempt.Outcome == "unknown" {
			if eof && !b.stream && b.attempt.HTTPStatus >= 200 && b.attempt.HTTPStatus < 300 {
				b.attempt.Outcome = "success"
			} else {
				b.attempt.Outcome = "failed"
			}
		}
	})
}

func (b *upstreamAttemptBody) observeFrame(data []byte) {
	if bytes.Equal(data, []byte("[DONE]")) {
		if b.attempt.Outcome == "unknown" {
			b.attempt.Outcome = "success"
		}
		return
	}
	if !gjson.ValidBytes(data) {
		return
	}
	root := gjson.ParseBytes(data)
	a := b.attempt
	switch root.Get("type").String() {
	case "response.completed":
		a.Outcome = "success"
	case "message_stop":
		if a.Outcome == "unknown" {
			a.Outcome = "success"
		}
	case "response.incomplete":
		a.Outcome = "incomplete"
	case "response.failed", "error":
		a.Outcome = "failed"
	}
	if response := root.Get("response"); response.IsObject() {
		root = response
	} else if message := root.Get("message"); message.IsObject() {
		root = message
	}
	if a.Model == "" {
		a.Model = root.Get("model").String()
	}
	if tier := root.Get("service_tier").String(); tier != "" {
		a.ServiceTier = tier
	}
	if reason := root.Get("delta.stop_reason").String(); reason == "max_tokens" {
		a.Outcome = "incomplete"
	}
	switch root.Get("candidates.0.finishReason").String() {
	case "STOP":
		a.Outcome = "success"
	case "MAX_TOKENS":
		a.Outcome = "incomplete"
	case "SAFETY", "RECITATION", "PROHIBITED_CONTENT":
		a.Outcome = "failed"
	}
	switch root.Get("status").String() {
	case "completed":
		a.Outcome = "success"
	case "incomplete":
		a.Outcome = "incomplete"
	case "failed", "cancelled":
		a.Outcome = "failed"
	}
	usage := root.Get("usage")
	if !usage.IsObject() {
		usage = root.Get("usageMetadata")
	}
	if !usage.IsObject() {
		return
	}
	a.UsagePresent = true
	setAttemptToken(&b.inputTotal, usage, "input_tokens", "prompt_tokens", "promptTokenCount")
	setAttemptToken(&b.outputTotal, usage, "output_tokens", "completion_tokens", "candidatesTokenCount")
	a.InputTokens = b.inputTotal
	a.OutputTokens = b.outputTotal
	setAttemptToken(&a.CacheReadTokens, usage, "cache_read_input_tokens", "input_tokens_details.cached_tokens", "prompt_tokens_details.cached_tokens", "cachedContentTokenCount")
	setAttemptToken(&a.CacheCreationTokens, usage, "cache_creation_input_tokens")
	setAttemptToken(&a.CacheCreation5mTokens, usage, "cache_creation.ephemeral_5m_input_tokens")
	setAttemptToken(&a.CacheCreation1hTokens, usage, "cache_creation.ephemeral_1h_input_tokens")
	setAttemptToken(&a.ReasoningTokens, usage, "output_tokens_details.reasoning_tokens", "completion_tokens_details.reasoning_tokens", "thoughtsTokenCount")
	// OpenAI and Gemini include cache reads in their input count. Anthropic
	// reports non-cache input separately. The existing cost engine expects the
	// latter, exclusive form. Keep reasoning separate unless Gemini excludes it.
	if a.Protocol != "anthropic" && a.InputTokens != nil && a.CacheReadTokens != nil {
		n := *a.InputTokens - *a.CacheReadTokens
		if n < 0 {
			n = 0
		}
		a.InputTokens = &n
	}
	if a.Protocol == "gemini" && a.OutputTokens != nil && a.ReasoningTokens != nil {
		n := *a.OutputTokens + *a.ReasoningTokens
		a.OutputTokens = &n
	}
}

func setAttemptToken(dst **int64, usage gjson.Result, paths ...string) {
	for _, path := range paths {
		v := usage.Get(path)
		if v.Type == gjson.Number && v.Int() >= 0 && v.Float() == float64(v.Int()) {
			n := v.Int()
			*dst = &n
			return
		}
	}
}
