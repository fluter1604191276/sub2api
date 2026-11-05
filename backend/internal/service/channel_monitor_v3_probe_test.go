//go:build unit

package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type cmv3ProbeRepoFake struct {
	ChannelMonitorV3Repository
	ChannelMonitorV3ProbeRepository
	ChannelMonitorV3IdentityRepository
	settings                                        ChannelMonitorV3Settings
	groups                                          []ChannelMonitorV3GroupConfig
	budget                                          ChannelMonitorV3Budget
	identity                                        *ChannelMonitorV3Identity
	identityCalls                                   int
	job                                             *ChannelMonitorV3ProbeJob
	real                                            bool
	claims, dispatched, finished, released, settled int
	finishedOK                                      bool
}

func (f *cmv3ProbeRepoFake) Settings(context.Context) (ChannelMonitorV3Settings, error) {
	return f.settings, nil
}
func (f *cmv3ProbeRepoFake) SaveSettings(_ context.Context, v ChannelMonitorV3Settings) (ChannelMonitorV3Settings, error) {
	f.settings = v
	return v, nil
}
func (f *cmv3ProbeRepoFake) ListGroups(context.Context) ([]ChannelMonitorV3GroupConfig, error) {
	return append([]ChannelMonitorV3GroupConfig(nil), f.groups...), nil
}
func (f *cmv3ProbeRepoFake) SaveGroup(_ context.Context, group ChannelMonitorV3GroupConfig) (*ChannelMonitorV3GroupConfig, error) {
	return &group, nil
}
func (f *cmv3ProbeRepoFake) EnsureProbeIdentity(context.Context) (*ChannelMonitorV3Identity, error) {
	f.identityCalls++
	return f.identity, nil
}
func (f *cmv3ProbeRepoFake) ProbeGroupKey(context.Context, int64) (*APIKey, error) {
	return &APIKey{Key: "monitor-key"}, nil
}
func (f *cmv3ProbeRepoFake) ListPendingProbes(context.Context) ([]ChannelMonitorV3ProbeJob, error) {
	if f.job != nil && f.finishedOK && f.settled == 0 {
		return []ChannelMonitorV3ProbeJob{*f.job}, nil
	}
	return nil, nil
}
func (f *cmv3ProbeRepoFake) TryClaimProbe(_ context.Context, group ChannelMonitorV3GroupConfig, now time.Time, _ float64) (*ChannelMonitorV3ProbeJob, error) {
	f.claims++
	id := uuid.NewString()
	f.job = &ChannelMonitorV3ProbeJob{DispatchID: id, ClientRequestID: "cmv3-" + id, GroupID: group.GroupID, Model: group.TestModel, Platform: group.Platform, BudgetDay: channelMonitorV3BudgetDay(now), ReservedUSD: 1, CreatedAt: now}
	return f.job, nil
}
func (f *cmv3ProbeRepoFake) MarkProbeDispatched(context.Context, string) error {
	f.dispatched++
	return nil
}
func (f *cmv3ProbeRepoFake) FinishProbe(_ context.Context, _ string, complete bool) error {
	f.finished++
	f.finishedOK = complete
	return nil
}
func (f *cmv3ProbeRepoFake) ReleaseUndispatchedProbe(context.Context, string) error {
	f.released++
	return nil
}
func (f *cmv3ProbeRepoFake) SettleProbe(context.Context, string, UpstreamAttemptCostSummary) error {
	f.settled++
	return nil
}
func (f *cmv3ProbeRepoFake) HasRealTrafficSince(context.Context, int64, time.Time) (bool, error) {
	return f.real, nil
}
func (f *cmv3ProbeRepoFake) ProbeBudget(context.Context, string, float64) (ChannelMonitorV3Budget, error) {
	return f.budget, nil
}

type cmv3AttemptsFake struct {
	UpstreamAttemptRepository
	summary UpstreamAttemptCostSummary
	calls   int
}

func (f *cmv3AttemptsFake) SumByClientRequestID(context.Context, string, string) (UpstreamAttemptCostSummary, error) {
	f.calls++
	return f.summary, nil
}

type cmv3GroupsFake struct {
	GroupRepository
	group *Group
}

func (f *cmv3GroupsFake) GetByID(context.Context, int64) (*Group, error) { return f.group, nil }

type cmv3AccountsFake struct {
	AccountRepository
	accounts []Account
}

func (f *cmv3AccountsFake) ListByGroup(context.Context, int64) ([]Account, error) {
	return f.accounts, nil
}
func (f *cmv3AccountsFake) GetByID(_ context.Context, id int64) (*Account, error) {
	for index := range f.accounts {
		if f.accounts[index].ID == id {
			return &f.accounts[index], nil
		}
	}
	return nil, nil
}

type cmv3ClientFake struct {
	called   int
	request  *http.Request
	response *http.Response
	err      error
}

func (f *cmv3ClientFake) Do(req *http.Request) (*http.Response, error) {
	f.called++
	f.request = req
	return f.response, f.err
}

func newCMV3Runner(now time.Time, repo *cmv3ProbeRepoFake, client *cmv3ClientFake) *ChannelMonitorV3ProbeRunner {
	actual := &Group{ID: 11, Platform: PlatformAnthropic, Status: StatusActive, ModelAllowlist: GroupModelAllowlist{Enabled: true, Models: []string{"claude-sonnet-4"}}}
	accounts := &cmv3AccountsFake{accounts: []Account{{ID: 31, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true}}}
	r := NewChannelMonitorV3ProbeRunner(repo, &cmv3AttemptsFake{summary: UpstreamAttemptCostSummary{KnownCostUSD: .12, AttemptCount: 1}}, NewBillingService(&config.Config{}, nil), nil, accounts, &config.Config{}, &cmv3GroupsFake{group: actual}, nil)
	r.client, r.now, r.endpoint = client, func() time.Time { return now }, "http://127.0.0.1:18080"
	r.requestToken = []byte("trusted-monitor-token")
	return r
}

func cmv3EnabledRepo() *cmv3ProbeRepoFake {
	return &cmv3ProbeRepoFake{settings: ChannelMonitorV3Settings{Enabled: true, DailyBudgetUSD: 2}, groups: []ChannelMonitorV3GroupConfig{{GroupID: 11, Platform: PlatformAnthropic, Status: StatusActive, Visible: true, ProbeIntervalSeconds: 600, TestModel: "claude-sonnet-4"}}, budget: ChannelMonitorV3Budget{RemainingUSD: 2}, identity: &ChannelMonitorV3Identity{UserID: 99, Balance: 1}}
}

func TestChannelMonitorV3ProbeRunOnceDisabledDoesNotCreateIdentityOrSend(t *testing.T) {
	repo := cmv3EnabledRepo()
	repo.settings.Enabled = false
	client := &cmv3ClientFake{}
	r := newCMV3Runner(time.Date(2026, 11, 5, 1, 0, 0, 0, time.UTC), repo, client)
	require.NoError(t, r.RunOnce(context.Background()))
	require.Zero(t, repo.identityCalls)
	require.Zero(t, repo.claims)
	require.Zero(t, client.called)
}

func TestChannelMonitorV3ProbeRunOnceClaimsDispatchesAndSettles(t *testing.T) {
	repo := cmv3EnabledRepo()
	client := &cmv3ClientFake{response: &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"ok":true}`))}}
	r := newCMV3Runner(time.Date(2026, 11, 5, 1, 0, 0, 0, time.UTC), repo, client)
	require.NoError(t, r.RunOnce(context.Background()))
	require.Equal(t, 1, repo.claims)
	require.Equal(t, 1, repo.dispatched)
	require.Equal(t, 1, repo.finished)
	require.True(t, repo.finishedOK)
	require.Equal(t, 1, repo.settled)
	require.Equal(t, 1, client.called)
	require.Equal(t, "Bearer monitor-key", client.request.Header.Get("Authorization"))
	require.Equal(t, string(r.requestToken), client.request.Header.Get(channelMonitorV3RequestHeader))
	require.Equal(t, repo.job.DispatchID, client.request.Header.Get(channelMonitorV3DispatchHeader))
	require.Equal(t, "http://127.0.0.1:18080/v1/messages", client.request.URL.String())
	body, err := io.ReadAll(client.request.Body)
	require.NoError(t, err)
	require.Contains(t, string(body), `"max_tokens":128`)
}

func TestChannelMonitorV3ProbeRunOnceReleasesIfRealTrafficAppearsAfterClaim(t *testing.T) {
	repo := cmv3EnabledRepo()
	repo.real = true
	client := &cmv3ClientFake{}
	r := newCMV3Runner(time.Now(), repo, client)
	require.NoError(t, r.RunOnce(context.Background()))
	require.Equal(t, 1, repo.claims)
	require.Equal(t, 1, repo.released)
	require.Zero(t, client.called)
}

func TestChannelMonitorV3ProbeRunOnceSkipsExhaustedBudget(t *testing.T) {
	repo := cmv3EnabledRepo()
	repo.budget.RemainingUSD = 0
	client := &cmv3ClientFake{}
	r := newCMV3Runner(time.Now(), repo, client)
	require.NoError(t, r.RunOnce(context.Background()))
	require.Zero(t, repo.claims)
	require.Zero(t, client.called)
}

func TestChannelMonitorV3ProbeRunOnceSkipsUnpricedCandidate(t *testing.T) {
	repo := cmv3EnabledRepo()
	client := &cmv3ClientFake{}
	r := newCMV3Runner(time.Now(), repo, client)
	r.billing = nil
	require.NoError(t, r.RunOnce(context.Background()))
	require.Zero(t, repo.claims)
	require.Zero(t, client.called)
}

func TestChannelMonitorV3ProbeDispatchIncompleteResponseFinishesWithoutSettling(t *testing.T) {
	repo := cmv3EnabledRepo()
	client := &cmv3ClientFake{response: &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(bytes.Repeat([]byte("x"), (4<<20)+1)))}}
	r := newCMV3Runner(time.Now(), repo, client)
	require.NoError(t, r.RunOnce(context.Background()))
	require.Equal(t, 1, repo.finished)
	require.False(t, repo.finishedOK)
	require.Zero(t, repo.released)
	require.Zero(t, repo.settled)
}

func TestChannelMonitorV3ProbeDispatchReleasesClaimWhenBudgetDrops(t *testing.T) {
	now := time.Date(2026, 11, 5, 1, 0, 0, 0, time.UTC)
	repo := cmv3EnabledRepo()
	// The first budget check may have allowed the claim. Before send, known cost
	// consumes the cap, so the claimed reservation must be released.
	repo.budget = ChannelMonitorV3Budget{SpentUSD: 2, ReservedUSD: 1, RemainingUSD: 1}
	client := &cmv3ClientFake{}
	r := newCMV3Runner(now, repo, client)
	job := &ChannelMonitorV3ProbeJob{DispatchID: uuid.NewString(), BudgetDay: channelMonitorV3BudgetDay(now), ReservedUSD: 1}
	require.NoError(t, r.dispatch(context.Background(), repo, repo.groups[0], job, "monitor-key", "/v1/messages", []byte(`{}`), .5))
	require.Equal(t, 1, repo.released)
	require.Zero(t, client.called)
	require.Zero(t, repo.dispatched)
}

func TestChannelMonitorV3ProbeDispatchReleasesClaimAcrossBeijingDay(t *testing.T) {
	now := time.Date(2026, 11, 5, 16, 1, 0, 0, time.UTC) // Nov 6 in Asia/Shanghai
	repo := cmv3EnabledRepo()
	client := &cmv3ClientFake{}
	r := newCMV3Runner(now, repo, client)
	job := &ChannelMonitorV3ProbeJob{DispatchID: uuid.NewString(), BudgetDay: "2026-11-05", ReservedUSD: 1}
	require.NoError(t, r.dispatch(context.Background(), repo, repo.groups[0], job, "monitor-key", "/v1/messages", []byte(`{}`), .5))
	require.Equal(t, 1, repo.released)
	require.Zero(t, client.called)
	require.Zero(t, repo.dispatched)
}

func TestChannelMonitorV3MarkRequestRequiresLoopbackTokenAndUUID(t *testing.T) {
	r := newCMV3Runner(time.Now(), cmv3EnabledRepo(), &cmv3ClientFake{})
	id := uuid.NewString()
	r.requests.Store(id, &channelMonitorV3AttemptGuard{day: channelMonitorV3BudgetDay(r.now()), limit: 2, now: r.now})
	for _, tc := range []struct {
		name, remote, token, dispatch string
		marked                        bool
	}{
		{"spoofed", "8.8.8.8:12", string(r.requestToken), id, false},
		{"bad token", "127.0.0.1:12", "bad", id, false},
		{"bad dispatch", "127.0.0.1:12", string(r.requestToken), "not-a-uuid", false},
		{"valid", "127.0.0.1:12", string(r.requestToken), id, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "http://service/v1/messages", nil)
			req.RemoteAddr = tc.remote
			req.Header.Set(channelMonitorV3RequestHeader, tc.token)
			req.Header.Set(channelMonitorV3DispatchHeader, tc.dispatch)
			got := r.MarkRequest(req)
			require.Empty(t, got.Header.Get(channelMonitorV3RequestHeader))
			require.Empty(t, got.Header.Get(channelMonitorV3DispatchHeader))
			require.Equal(t, tc.marked, IsChannelMonitorV3Request(got.Context()))
			if tc.marked {
				require.Equal(t, "cmv3-"+id, got.Context().Value(ctxkey.ClientRequestID))
			}
		})
	}
}

func TestChannelMonitorV3LegacyMonitorHintDoesNotGrantV3Trust(t *testing.T) {
	r := newCMV3Runner(time.Now(), cmv3EnabledRepo(), &cmv3ClientFake{})
	req := httptest.NewRequest(http.MethodPost, "http://service/v1/messages", nil)
	req.Header.Set("User-Agent", "sub2api-channel-monitor/1.0")
	marked := r.MarkRequest(req)
	require.False(t, IsChannelMonitorV3Request(marked.Context()))
	require.Equal(t, "channel_monitor", marked.Context().Value(upstreamAttemptSourceKey{}))
	require.Nil(t, marked.Context().Value(channelMonitorV3AttemptGuardKey{}))
}

func TestChannelMonitorV3ProbePayloadUsesNativeProtocolAndBoundedTokens(t *testing.T) {
	r := newCMV3Runner(time.Now(), cmv3EnabledRepo(), &cmv3ClientFake{})
	for _, tc := range []struct {
		name, platform, native, wantPath, wantTokenField string
	}{
		{"responses", PlatformOpenAI, APIProtocolResponses, "/v1/responses", `"max_output_tokens":128`},
		{"chat", PlatformOpenAI, APIProtocolChatCompletions, "/v1/chat/completions", `"max_completion_tokens":128`},
		{"anthropic", PlatformAnthropic, APIProtocolAnthropic, "/v1/messages", `"max_tokens":128`},
		{"gemini", PlatformGemini, "", "/v1beta/models/claude-sonnet-4:streamGenerateContent?alt=sse", `"maxOutputTokens":128`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r.accounts = &cmv3AccountsFake{accounts: []Account{{ID: 31, Platform: tc.platform, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"api_protocol": tc.native}}}}
			path, body, err := r.probePayload(context.Background(), &Group{ID: 11, Platform: tc.platform}, "claude-sonnet-4")
			require.NoError(t, err)
			require.Equal(t, tc.wantPath, path)
			require.Contains(t, string(body), tc.wantTokenField)
		})
	}
}

func TestNewChannelMonitorV3ProbeRunnerDerivesLoopbackEndpoint(t *testing.T) {
	r := NewChannelMonitorV3ProbeRunner(nil, nil, nil, nil, nil, &config.Config{Server: config.ServerConfig{Host: "::1", Port: 19090}}, nil, nil)
	require.Equal(t, "http://[::1]:19090", r.endpoint)
}
