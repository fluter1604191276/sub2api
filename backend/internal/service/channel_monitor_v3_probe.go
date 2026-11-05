package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

const channelMonitorV3MaxOutputTokens = 128

// ChannelMonitorV3ProbeRunner owns the process-private marker used by ordinary
// gateway requests issued for V3 monitoring. It never grants an auth, billing,
// or scheduler bypass.
type ChannelMonitorV3ProbeRunner struct {
	repo          ChannelMonitorV3Repository
	attempts      UpstreamAttemptRepository
	billing       *BillingService
	channels      *ChannelService
	accounts      AccountRepository
	groups        GroupRepository
	subscriptions UserSubscriptionRepository
	client        iqHTTPClient
	endpoint      string
	now           func() time.Time
	runMu         sync.Mutex
	requestToken  []byte
	requests      sync.Map
	ctx           context.Context
	cancel        context.CancelFunc
	startOnce     sync.Once
	stopOnce      sync.Once
	done          chan struct{}
}

func NewChannelMonitorV3ProbeRunner(repo ChannelMonitorV3Repository, attempts UpstreamAttemptRepository, billing *BillingService, channels *ChannelService, accounts AccountRepository, cfg *config.Config, groups GroupRepository, subscriptions UserSubscriptionRepository) *ChannelMonitorV3ProbeRunner {
	ctx, cancel := context.WithCancel(context.Background())
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		token = nil // trusted marking fails closed
	} else {
		token = []byte(base64.RawURLEncoding.EncodeToString(token))
	}
	host, port := "127.0.0.1", 8080
	if cfg != nil {
		if cfg.Server.Port > 0 {
			port = cfg.Server.Port
		}
		if strings.TrimSpace(cfg.Server.Host) == "::1" {
			host = "::1"
		}
	}
	client := &http.Client{Timeout: 60 * time.Second,
		Transport:     &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, ResponseHeaderTimeout: 30 * time.Second},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return &ChannelMonitorV3ProbeRunner{repo: repo, attempts: attempts, billing: billing, channels: channels, accounts: accounts,
		groups: groups, subscriptions: subscriptions, client: client, endpoint: "http://" + net.JoinHostPort(host, fmt.Sprint(port)), now: time.Now,
		requestToken: token, ctx: ctx, cancel: cancel, done: make(chan struct{})}
}

func (r *ChannelMonitorV3ProbeRunner) Start() {
	if r != nil {
		r.startOnce.Do(func() {
			go func() {
				defer close(r.done)
				// Give the ordinary HTTP server time to bind before loopback work.
				ticker := time.NewTicker(15 * time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-r.ctx.Done():
						return
					case <-ticker.C:
						if err := r.RunOnce(r.ctx); err != nil && r.ctx.Err() == nil {
							slog.Warn("channel_monitor_v3_probe_cycle_failed")
						}
					}
				}
			}()
		})
	}
}

func (r *ChannelMonitorV3ProbeRunner) Stop() {
	if r == nil {
		return
	}
	r.stopOnce.Do(r.cancel)
	r.Start()
	<-r.done
}

func (r *ChannelMonitorV3ProbeRunner) RunOnce(ctx context.Context) error {
	if r == nil || r.repo == nil {
		return nil
	}
	r.runMu.Lock()
	defer r.runMu.Unlock()
	store, ok := r.repo.(ChannelMonitorV3ProbeRepository)
	if !ok || r.attempts == nil {
		return fmt.Errorf("monitor probe ledger unavailable")
	}
	if err := r.reconcile(ctx, store); err != nil {
		return err
	}
	settings, err := r.repo.Settings(ctx)
	if err != nil || !settings.Enabled {
		return err
	}
	identityStore, ok := r.repo.(ChannelMonitorV3IdentityRepository)
	if !ok || len(r.requestToken) == 0 {
		return fmt.Errorf("monitor identity unavailable")
	}
	identity, err := identityStore.EnsureProbeIdentity(ctx)
	if err != nil {
		return err
	}
	if identity == nil || identity.Balance <= 0 {
		return fmt.Errorf("monitor identity requires a positive balance")
	}
	groups, err := r.repo.ListGroups(ctx)
	if err != nil {
		return err
	}
	for _, group := range groups {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !group.Visible || group.Status != StatusActive || group.ProbeIntervalSeconds == 0 || group.TestModel == "" {
			continue
		}
		actual, err := r.prepareGroup(ctx, group, identity.UserID)
		if err != nil {
			continue
		}
		path, body, err := r.probePayload(ctx, actual, group.TestModel)
		if err != nil {
			continue
		}
		estimate, err := r.estimate(ctx, actual, group.TestModel, body)
		if err != nil {
			continue
		}
		budget, err := store.ProbeBudget(ctx, channelMonitorV3BudgetDay(r.now()), settings.DailyBudgetUSD)
		if err != nil {
			return err
		}
		if budget.RemainingUSD <= 0 {
			break
		}
		if estimate > budget.RemainingUSD {
			continue
		}
		key, err := identityStore.ProbeGroupKey(ctx, group.GroupID)
		if err != nil || key == nil || key.Key == "" {
			continue
		}
		job, err := store.TryClaimProbe(ctx, group, r.now(), settings.DailyBudgetUSD)
		if err != nil {
			return err
		}
		if job == nil {
			continue
		}
		if err := r.dispatch(ctx, store, group, job, key.Key, path, body, estimate); err != nil {
			return err
		}
	}
	return nil
}

func channelMonitorV3BudgetDay(now time.Time) string {
	return now.In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("2006-01-02")
}

func (r *ChannelMonitorV3ProbeRunner) Budget(ctx context.Context, limit float64) (ChannelMonitorV3Budget, error) {
	store, ok := r.repo.(ChannelMonitorV3ProbeRepository)
	if !ok {
		return ChannelMonitorV3Budget{}, fmt.Errorf("monitor budget unavailable")
	}
	return store.ProbeBudget(ctx, channelMonitorV3BudgetDay(r.now()), limit)
}

func (r *ChannelMonitorV3ProbeRunner) reconcile(ctx context.Context, store ChannelMonitorV3ProbeRepository) error {
	jobs, err := store.ListPendingProbes(ctx)
	if err != nil {
		return err
	}
	for _, job := range jobs {
		cost, err := r.attempts.SumByClientRequestID(ctx, "channel_monitor_v3", job.ClientRequestID)
		if err != nil {
			return err
		}
		// Shadow writes are bounded asynchronous work. Give a completed loopback
		// response one minute to flush before treating zero evidence as unknown.
		if cost.AttemptCount == 0 && r.now().Sub(job.CompletedAt) < time.Minute {
			continue
		}
		if err := store.SettleProbe(ctx, job.DispatchID, cost); err != nil {
			return err
		}
	}
	return nil
}

func (r *ChannelMonitorV3ProbeRunner) prepareGroup(ctx context.Context, group ChannelMonitorV3GroupConfig, userID int64) (*Group, error) {
	if r.groups == nil {
		return nil, fmt.Errorf("group repository unavailable")
	}
	actual, err := r.groups.GetByID(ctx, group.GroupID)
	if err != nil {
		return nil, err
	}
	if actual == nil || actual.Status != StatusActive || actual.Platform != group.Platform {
		return nil, fmt.Errorf("group configuration changed")
	}
	if !actual.ModelAllowlist.Allows(group.TestModel) {
		return nil, fmt.Errorf("test model is outside the group allowlist")
	}
	if actual.IsSubscriptionType() {
		if r.subscriptions == nil {
			return nil, fmt.Errorf("monitor subscription unavailable")
		}
		sub, err := r.subscriptions.GetActiveByUserIDAndGroupID(ctx, userID, group.GroupID)
		if err != nil || sub == nil {
			return nil, fmt.Errorf("monitor identity requires its own subscription")
		}
	}
	return actual, nil
}

func (r *ChannelMonitorV3ProbeRunner) dispatch(ctx context.Context, store ChannelMonitorV3ProbeRepository, group ChannelMonitorV3GroupConfig, job *ChannelMonitorV3ProbeJob, key, path string, body []byte, estimate float64) error {
	settings, err := r.repo.Settings(ctx)
	if err != nil {
		return err
	}
	current, err := r.repo.ListGroups(ctx)
	if err != nil {
		return err
	}
	unchanged := false
	for _, value := range current {
		if value.GroupID == group.GroupID && value.Visible && value.Status == StatusActive && value.Platform == group.Platform && value.TestModel == group.TestModel && value.ProbeIntervalSeconds == group.ProbeIntervalSeconds {
			unchanged = true
			break
		}
	}
	real, err := store.HasRealTrafficSince(ctx, group.GroupID, r.now().Add(-time.Duration(group.ProbeIntervalSeconds)*time.Second))
	if err != nil {
		return err
	}
	budget, err := store.ProbeBudget(ctx, job.BudgetDay, settings.DailyBudgetUSD)
	if err != nil {
		return err
	}
	allowance := settings.DailyBudgetUSD - budget.SpentUSD - (budget.ReservedUSD - job.ReservedUSD)
	if !settings.Enabled || !unchanged || estimate > job.ReservedUSD || estimate > allowance || job.BudgetDay != channelMonitorV3BudgetDay(r.now()) || ctx.Err() != nil || real {
		return releaseChannelMonitorV3Probe(ctx, store, job.DispatchID)
	}
	requestCtx, cancelRequest := context.WithTimeout(ctx, 60*time.Second)
	defer cancelRequest()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, r.endpoint+path, bytes.NewReader(body))
	if err != nil {
		return releaseChannelMonitorV3Probe(ctx, store, job.DispatchID)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	// The existing ordinary-account quality pipeline excludes this monitor
	// family. The V3 historical query deliberately includes it.
	req.Header.Set("User-Agent", "sub2api-channel-monitor/v3")
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set(channelMonitorV3RequestHeader, string(r.requestToken))
	req.Header.Set(channelMonitorV3DispatchHeader, job.DispatchID)
	r.requests.Store(job.DispatchID, &channelMonitorV3AttemptGuard{day: job.BudgetDay, limit: job.ReservedUSD, now: r.now})
	defer r.requests.Delete(job.DispatchID)
	if err := store.MarkProbeDispatched(ctx, job.DispatchID); err != nil {
		return err
	}
	resp, requestErr := r.client.Do(req)
	completed := false
	if resp != nil && resp.Body != nil {
		// Only read a bounded response and never retain its generated text.
		count, readErr := io.Copy(io.Discard, io.LimitReader(resp.Body, (4<<20)+1))
		_ = resp.Body.Close()
		completed = requestErr == nil && readErr == nil && count <= 4<<20
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := store.FinishProbe(finishCtx, job.DispatchID, completed); err != nil {
		return err
	}
	return r.reconcile(finishCtx, store)
}

func releaseChannelMonitorV3Probe(ctx context.Context, store ChannelMonitorV3ProbeRepository, id string) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return store.ReleaseUndispatchedProbe(ctx, id)
}

func (r *ChannelMonitorV3ProbeRunner) probePayload(ctx context.Context, group *Group, model string) (string, []byte, error) {
	protocol := "chat_completions"
	switch group.Platform {
	case PlatformOpenAI:
		protocol = "responses"
	case PlatformAnthropic:
		protocol = "anthropic"
	case PlatformGemini, PlatformAntigravity:
		protocol = "gemini"
	}
	if r.accounts != nil && protocol != "gemini" {
		accounts, err := r.accounts.ListByGroup(ctx, group.ID)
		if err != nil {
			return "", nil, err
		}
		for _, account := range accounts {
			if !account.IsSchedulable() {
				continue
			}
			native := strings.TrimSpace(account.GetCredential("api_protocol"))
			if native == APIProtocolResponses || native == APIProtocolAnthropic || native == APIProtocolChatCompletions {
				protocol = native
				break
			}
		}
	}
	prompt := "Reply exactly OK."
	path := "/v1/chat/completions"
	payload := map[string]any{"model": model, "stream": true, "max_tokens": channelMonitorV3MaxOutputTokens,
		"messages": []map[string]string{{"role": "user", "content": prompt}}}
	switch protocol {
	case "responses":
		path = "/v1/responses"
		payload = map[string]any{"model": model, "stream": true, "store": false, "input": prompt, "max_output_tokens": channelMonitorV3MaxOutputTokens}
	case "anthropic":
		path = "/v1/messages"
	case "gemini":
		path = "/v1beta/models/" + url.PathEscape(model) + ":streamGenerateContent?alt=sse"
		payload = map[string]any{"contents": []map[string]any{{"role": "user", "parts": []map[string]string{{"text": prompt}}}}, "generationConfig": map[string]int{"maxOutputTokens": channelMonitorV3MaxOutputTokens}}
	}
	if protocol == "chat_completions" && group.Platform == PlatformOpenAI {
		delete(payload, "max_tokens")
		payload["max_completion_tokens"] = channelMonitorV3MaxOutputTokens
	}
	body, err := json.Marshal(payload)
	return path, body, err
}

func (r *ChannelMonitorV3ProbeRunner) estimate(ctx context.Context, group *Group, model string, body []byte) (float64, error) {
	if r.accounts == nil || r.billing == nil {
		return 0, fmt.Errorf("upstream pricing unavailable")
	}
	accounts, err := r.accounts.ListByGroup(ctx, group.ID)
	if err != nil {
		return 0, err
	}
	input, output := int64(len(body)+1024), int64(channelMonitorV3MaxOutputTokens)
	ctx = context.WithValue(ctx, ctxkey.Group, group)
	calculator := NewUpstreamAttemptCostCalculator(r.accounts, r.channels, r.billing)
	upper := float64(-1)
	for _, account := range accounts {
		if !account.IsSchedulable() {
			continue
		}
		mapped, _ := account.ResolveMappedModel(model)
		attempt := &UpstreamAttempt{AttemptID: "monitor-estimate", AccountID: account.ID, GroupID: &group.ID, CreatedAt: r.now(),
			Model: mapped, Protocol: "responses", Source: "channel_monitor_v3", Outcome: "unknown", UsagePresent: true,
			InputTokens: &input, OutputTokens: &output, CacheCreationTokens: &input, ReasoningEffort: "xhigh"}
		if group.ForceOpenAIFast {
			attempt.ServiceTier = "priority"
		}
		if err := calculator.Snapshot(ctx, attempt); err != nil {
			return 0, err
		}
		bound, err := channelMonitorV3HTTPUpperBound(ctx, calculator, attempt, input, output)
		if err != nil {
			return 0, err
		}
		upper = math.Max(upper, bound)
	}
	if upper < 0 {
		return 0, fmt.Errorf("no schedulable upstream pricing")
	}
	// A trusted V3 dispatch permits one upstream attempt. The actual selected
	// account's frozen price is checked again at the transport boundary.
	estimate := upper
	if math.IsInf(estimate, 0) || math.IsNaN(estimate) {
		return 0, fmt.Errorf("invalid upstream estimate")
	}
	return math.Ceil(estimate*1e10) / 1e10, nil
}
