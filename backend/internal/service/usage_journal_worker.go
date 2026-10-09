package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

type UsageJournalProcessor interface {
	ProcessNext(context.Context) (bool, error)
}
type UsageJournalControl interface {
	ValidateSchema(context.Context) error
	Observe(context.Context) (UsageJournalObservation, error)
	RollbackReady(context.Context) error
}

// Limits are explicit operational inputs, not automatic retention or money rules.
type UsageJournalObservation struct {
	Pending              int64            `json:"pending"`
	Quarantined          int64            `json:"quarantined"`
	OldestPendingSeconds float64          `json:"oldest_pending_seconds"`
	MaxAttempts          int64            `json:"max_attempts"`
	RelationBytes        int64            `json:"relation_bytes"`
	ErrorClasses         map[string]int64 `json:"error_classes"`
	ObservedAt           time.Time        `json:"observed_at"`
	Known                bool             `json:"known"`
	Healthy              bool             `json:"healthy"`
	Breaches             []string         `json:"breaches"`
	WorkerAttempts       uint64           `json:"worker_attempts"`
	WorkerErrors         uint64           `json:"worker_errors"`
	InFlight             int64            `json:"in_flight_http"`
	Accepting            bool             `json:"accepting"`
}

type UsageJournalWorkerRuntime struct {
	processor   UsageJournalProcessor
	cfg         *config.Config
	enabled     bool
	mu          sync.Mutex
	cancel      context.CancelFunc
	done        chan struct{}
	closing     bool
	inFlight    sync.WaitGroup
	observation UsageJournalObservation
}

func NewUsageJournalWorkerRuntime(cfg *config.Config, p UsageJournalProcessor) *UsageJournalWorkerRuntime {
	return &UsageJournalWorkerRuntime{cfg: cfg, processor: p, enabled: usageJournalDraftEnabled(cfg)}
}
func (r *UsageJournalWorkerRuntime) Enabled() bool { return r != nil && r.enabled }

// Start is fail-closed and read-only before either goroutine starts. No DDL.
func (r *UsageJournalWorkerRuntime) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.enabled {
		return nil
	}
	if r.cancel != nil {
		return errors.New("usage journal runtime already started")
	}
	if r.closing {
		return errors.New("usage journal runtime stopped")
	}
	if e := r.cfg.ValidateUsageJournal(); e != nil {
		return e
	}
	control, ok := r.processor.(UsageJournalControl)
	if !ok {
		return errors.New("usage journal schema/observation control missing")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	e := control.ValidateSchema(ctx)
	cancel()
	if e != nil {
		return fmt.Errorf("usage journal startup schema: %w", e)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
	observation, e := control.Observe(ctx)
	cancel()
	if e != nil {
		return fmt.Errorf("usage journal startup observation: %w", e)
	}
	r.observation = r.evaluate(observation)
	r.observation.Accepting = true
	runCtx, stop := context.WithCancel(context.Background())
	r.cancel = stop
	r.done = make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); r.run(runCtx) }()
		go func() { defer wg.Done(); r.monitor(runCtx, control) }()
		wg.Wait()
		close(r.done)
	}()
	return nil
}
func (r *UsageJournalWorkerRuntime) evaluate(o UsageJournalObservation) UsageJournalObservation {
	o.Known = true
	o.ObservedAt = time.Now().UTC()
	o.Breaches = nil
	if o.Pending > int64(r.cfg.Gateway.UsageJournalMaxPending) {
		o.Breaches = append(o.Breaches, "pending_count")
	}
	if o.OldestPendingSeconds > float64(r.cfg.Gateway.UsageJournalMaxPendingAgeSeconds) {
		o.Breaches = append(o.Breaches, "pending_age")
	}
	if o.RelationBytes > r.cfg.Gateway.UsageJournalMaxRelationBytes {
		o.Breaches = append(o.Breaches, "journal_bytes")
	}
	if o.Quarantined > 0 {
		o.Breaches = append(o.Breaches, "quarantined")
	}
	o.Healthy = len(o.Breaches) == 0
	return o
}
func (r *UsageJournalWorkerRuntime) run(ctx context.Context) {
	for ctx.Err() == nil {
		attempt, cancel := context.WithTimeout(ctx, 10*time.Second)
		worked, e := r.processor.ProcessNext(attempt)
		cancel()
		r.mu.Lock()
		if worked {
			r.observation.WorkerAttempts++
		}
		if e != nil {
			r.observation.WorkerErrors++
		}
		r.mu.Unlock()
		if e != nil && ctx.Err() == nil {
			slog.Warn("usage journal worker attempt failed; snapshot retained", "error_class", "processing_failed")
		}
		if worked && e == nil {
			continue
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
func (r *UsageJournalWorkerRuntime) monitor(ctx context.Context, control UsageJournalControl) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.Sample(ctx, control)
		}
	}
}

// Sample is also used for final read-only acceptance; failures never become zero.
func (r *UsageJournalWorkerRuntime) Sample(parent context.Context, control UsageJournalControl) error {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	o, e := control.Observe(ctx)
	r.mu.Lock()
	if e != nil {
		r.observation.Known = false
		r.observation.Healthy = false
		r.observation.Breaches = []string{"observation_unknown"}
	} else {
		o = r.evaluate(o)
		o.WorkerAttempts = r.observation.WorkerAttempts
		o.WorkerErrors = r.observation.WorkerErrors
		o.InFlight = r.observation.InFlight
		o.Accepting = !r.closing
		r.observation = o
	}
	status := r.copyObservationLocked()
	r.mu.Unlock()
	slog.LogAttrs(context.Background(), slog.LevelInfo, "usage_journal_status", slog.Any("status", status))
	return e
}
func (r *UsageJournalWorkerRuntime) copyObservationLocked() UsageJournalObservation {
	o := r.observation
	o.Breaches = append([]string(nil), o.Breaches...)
	o.ErrorClasses = map[string]int64{}
	for k, v := range r.observation.ErrorClasses {
		o.ErrorClasses[k] = v
	}
	return o
}
func (r *UsageJournalWorkerRuntime) Stats() UsageJournalObservation {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.copyObservationLocked()
}

// The enabled release scope is ordinary HTTP text generation. Unsupported money
// events and all gateway upgrades are rejected BEFORE scheduling/forwarding.
func journalGatewayPath(p string) bool {
	for _, prefix := range []string{"/responses", "/chat/completions", "/models", "/messages", "/alpha", "/embeddings", "/images", "/video", "/videos", "/tts", "/stt", "/custom-voices", "/realtime", "/web_search", "/x_search", "/v1", "/v1beta", "/backend-api/codex", "/antigravity"} {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return true
		}
	}
	return false
}
func journalSupportedHTTP(req *http.Request) bool {
	p := strings.TrimPrefix(req.URL.Path, "/antigravity")
	if req.Method == http.MethodGet {
		return p == "/models" || p == "/antigravity/models" || p == "/v1/models" || p == "/v1/usage" || p == "/v1beta/models"
	}
	if req.Method != http.MethodPost {
		return false
	}
	switch p {
	case "/v1/messages", "/v1/responses", "/v1/chat/completions", "/responses", "/chat/completions", "/backend-api/codex/responses":
		return true
	}
	return strings.HasPrefix(p, "/v1beta/models/") && (strings.HasSuffix(p, ":generateContent") || strings.HasSuffix(p, ":streamGenerateContent"))
}
func (r *UsageJournalWorkerRuntime) WrapHTTP(next http.Handler) http.Handler {
	if !r.Enabled() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !journalGatewayPath(req.URL.Path) {
			next.ServeHTTP(w, req)
			return
		}
		code := ""
		if !journalSupportedHTTP(req) || strings.TrimSpace(req.Header.Get("Upgrade")) != "" {
			code = "usage_journal_entry_unsupported"
		}
		r.mu.Lock()
		if code == "" {
			if r.closing {
				code = "usage_journal_draining"
			} else if !r.observation.Known || !r.observation.Healthy {
				code = "usage_journal_capacity_unready"
			} else {
				r.inFlight.Add(1)
				r.observation.InFlight++
			}
		}
		r.mu.Unlock()
		if code != "" {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "5")
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintf(w, `{"error":{"type":"service_unavailable","code":%q,"message":"Usage persistence is not ready for this request"}}`, code)
			return
		}
		defer func() { r.mu.Lock(); r.observation.InFlight--; r.mu.Unlock(); r.inFlight.Done() }()
		if req.Method == http.MethodPost && !r.supportedPayload(req) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			io.WriteString(w, `{"error":{"type":"service_unavailable","code":"usage_journal_payload_unsupported","message":"This usage persistence scope requires a text request without hosted tools"}}`)
			return
		}
		next.ServeHTTP(w, req)
	})
}

// Read at most the configured text endpoint limit, restore identical body bytes,
// and retain no payload in the journal/status. This is an activation restriction,
// not a new tool/media implementation. Counting begins before reading the body.
func (r *UsageJournalWorkerRuntime) supportedPayload(req *http.Request) bool {
	if req.Body == nil {
		return false
	}
	limit := r.cfg.Gateway.TextMaxBodySize
	if limit <= 0 {
		limit = 16 << 20
	}
	if r.cfg.Server.MaxRequestBodySize > 0 && r.cfg.Server.MaxRequestBodySize < limit {
		limit = r.cfg.Server.MaxRequestBodySize
	}
	body, e := io.ReadAll(io.LimitReader(req.Body, limit+1))
	if e != nil || int64(len(body)) > limit {
		return false
	}
	req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(body))
	var v map[string]json.RawMessage
	if json.Unmarshal(body, &v) != nil || v == nil {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if !journalUniqueJSON(decoder, 0) {
		return false
	}
	for _, field := range []string{"tools", "functions", "audio", "search_parameters", "output_format", "response_modalities", "image", "image_generation", "video"} {
		if value, ok := v[field]; ok && string(bytes.TrimSpace(value)) != "null" && string(bytes.TrimSpace(value)) != "[]" {
			return false
		}
	}
	for _, field := range []string{"modalities", "responseModalities"} {
		if value, ok := v[field]; ok {
			var values []string
			if json.Unmarshal(value, &values) != nil {
				return false
			}
			for _, mode := range values {
				if !strings.EqualFold(mode, "text") {
					return false
				}
			}
		}
	}
	if value, ok := v["generationConfig"]; ok {
		var generation map[string]json.RawMessage
		if json.Unmarshal(value, &generation) != nil {
			return false
		}
		for _, field := range []string{"responseModalities", "speechConfig"} {
			if raw, ok := generation[field]; ok {
				if field == "speechConfig" {
					return false
				}
				var modes []string
				if json.Unmarshal(raw, &modes) != nil {
					return false
				}
				for _, mode := range modes {
					if !strings.EqualFold(mode, "text") {
						return false
					}
				}
			}
		}
	}
	var model string
	if value, ok := v["model"]; ok && json.Unmarshal(value, &model) != nil {
		return false
	}
	model = strings.ToLower(model)
	if strings.Contains(req.URL.Path, "/v1beta/models/") {
		model += " " + strings.ToLower(req.URL.Path)
	}
	for _, kind := range []string{"image", "audio", "video", "dall-e", "tts", "realtime"} {
		if strings.Contains(model, kind) {
			return false
		}
	}
	return true
}

// Duplicate keys can make gateway parsers disagree about hosted-tool intent.
func journalUniqueJSON(d *json.Decoder, depth int) bool {
	if depth > 64 {
		return false
	}
	token, e := d.Token()
	if e != nil {
		return false
	}
	delim, container := token.(json.Delim)
	if !container {
		return true
	}
	if delim != '{' && delim != '[' {
		return false
	}
	seen := map[string]bool{}
	for d.More() {
		if delim == '{' {
			key, e := d.Token()
			if e != nil {
				return false
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return false
			}
			seen[name] = true
		}
		if !journalUniqueJSON(d, depth+1) {
			return false
		}
	}
	_, e = d.Token()
	return e == nil
}
func (r *UsageJournalWorkerRuntime) StopEnrollment() {
	r.mu.Lock()
	r.closing = true
	r.observation.Accepting = false
	r.mu.Unlock()
}
func (r *UsageJournalWorkerRuntime) WaitEnrollment(ctx context.Context) error {
	done := make(chan struct{})
	go func() { r.inFlight.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (r *UsageJournalWorkerRuntime) RollbackReady(ctx context.Context) error {
	control, ok := r.processor.(UsageJournalControl)
	if !ok {
		return errors.New("usage journal rollback control missing")
	}
	return control.RollbackReady(ctx)
}
func (r *UsageJournalWorkerRuntime) Stop() {
	r.StopEnrollment()
	r.mu.Lock()
	cancel, done := r.cancel, r.done
	r.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

// An unsuccessful drain leaves consumer/DB open and explicitly returns failure;
// it never declares in-flight requests/queued billing safely drained on timeout.
func DrainUsageJournal(ctx context.Context, srv *http.Server, pool *UsageRecordWorkerPool, r *UsageJournalWorkerRuntime) error {
	if !r.Enabled() {
		return nil
	}
	r.StopEnrollment()
	if srv != nil {
		if e := srv.Shutdown(ctx); e != nil {
			return fmt.Errorf("journal HTTP drain incomplete: %w", e)
		}
	}
	if e := r.WaitEnrollment(ctx); e != nil {
		return fmt.Errorf("journal enrollment drain incomplete: %w", e)
	}
	if pool != nil {
		done := make(chan struct{})
		go func() { pool.Stop(); close(done) }()
		select {
		case <-done:
		case <-ctx.Done():
			return fmt.Errorf("journal billing queue drain incomplete: %w", ctx.Err())
		}
	}
	r.Stop()
	return nil
}
