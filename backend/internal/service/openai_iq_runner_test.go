package service

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestOpenAIIQRunnerRequestUsesClaimedSnapshotWithoutExpectedAnswer(t *testing.T) {
	run := &IQRun{ID: 1, Question: "claimed question", Answer: "15", Model: "gpt-6-astra"}
	body, err := buildIQRequest(run, 32)
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	require.Equal(t, "gpt-6-astra", payload["model"])
	require.Contains(t, payload["input"], "claimed question")
	require.NotContains(t, payload["input"], "15")
}

type iqRunnerSettingsStub struct {
	SettingRepository
	mu       sync.Mutex
	settings IQSettings
}

func (s *iqRunnerSettingsStub) GetValue(context.Context, string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, _ := json.Marshal(s.settings)
	return string(raw), nil
}

type iqRunnerGroupsStub struct {
	GroupRepository
	group *Group
}

func (s *iqRunnerGroupsStub) GetByID(_ context.Context, id int64) (*Group, error) {
	copy := *s.group
	copy.ID = id
	return &copy, nil
}

type iqRunnerAccountsStub struct {
	AccountRepository
	accounts []Account
}

func (s *iqRunnerAccountsStub) ListByGroup(context.Context, int64) ([]Account, error) {
	return s.accounts, nil
}

type iqRunnerRepoStub struct {
	mu                                              sync.Mutex
	identity                                        *IQIdentity
	run                                             *IQRun
	results                                         []IQResult
	claimCalls, reserveCalls, keyCalls, finishCalls int
	reconcileCalls                                  int
	bindErr, reconcileErr                           error
	boundRequests                                   []string
	reserve                                         bool
	completed                                       []IQResult
}

func (r *iqRunnerRepoStub) Latest(context.Context, []int64) (*IQView, error)  { return nil, nil }
func (r *iqRunnerRepoStub) Budget(context.Context, float64) (IQBudget, error) { return IQBudget{}, nil }
func (r *iqRunnerRepoStub) EnsureIdentity(context.Context, []int64) (*IQIdentity, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.identity, nil
}
func (r *iqRunnerRepoStub) Identity(context.Context) (*IQIdentity, error) { return r.identity, nil }
func (r *iqRunnerRepoStub) ClaimDue(context.Context, IQSettings) (*IQRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.claimCalls++
	return r.run, nil
}
func (r *iqRunnerRepoStub) ReserveBudget(context.Context, int64, int64, time.Time, float64, float64) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reserveCalls++
	return r.reserve, nil
}
func (r *iqRunnerRepoStub) BindBudgetRequest(_ context.Context, _, _, _ int64, requestID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.boundRequests = append(r.boundRequests, requestID)
	return r.bindErr
}
func (r *iqRunnerRepoStub) ReconcileBudget(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reconcileCalls++
	return r.reconcileErr
}
func (r *iqRunnerRepoStub) GroupKey(context.Context, int64, int64) (*APIKey, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.keyCalls++
	return &APIKey{ID: 91, Key: "iq-only-key", Status: StatusActive}, nil
}
func (r *iqRunnerRepoStub) RunResults(context.Context, int64) ([]IQResult, error) {
	return r.results, nil
}
func (r *iqRunnerRepoStub) CompleteResult(_ context.Context, _ int64, result IQResult) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.completed = append(r.completed, result)
	return nil
}
func (r *iqRunnerRepoStub) FinishRun(context.Context, int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finishCalls++
	return nil
}
func (r *iqRunnerRepoStub) SaveSettings(context.Context, IQSettings) error { return nil }

func TestOpenAIIQRunnerDisabledMakesNoClaim(t *testing.T) {
	disabled := iqRunnerSettings()
	disabled.Enabled = false
	settings := &iqRunnerSettingsStub{settings: disabled}
	repo := &iqRunnerRepoStub{identity: &IQIdentity{UserID: 1, Balance: 1}}
	svc := NewOpenAIIQService(settings, nil, nil, nil, repo, nil, nil, nil, nil)
	require.NoError(t, svc.runIQRound(context.Background()))
	require.Zero(t, repo.claimCalls)
	require.Equal(t, 1, repo.reconcileCalls, "disabled detection must still settle delayed usage")
}

func TestOpenAIIQRunnerSettlementFailureDoesNotClaim(t *testing.T) {
	settings := &iqRunnerSettingsStub{settings: iqRunnerSettings()}
	repo := &iqRunnerRepoStub{reconcileErr: errors.New("usage reconciliation unavailable")}
	svc := NewOpenAIIQService(settings, nil, nil, nil, repo, nil, nil, nil, nil)
	require.Error(t, svc.runIQRound(context.Background()))
	require.Zero(t, repo.claimCalls)
}

func TestOpenAIIQRunnerUnfundedIdentityMakesNoClaim(t *testing.T) {
	for _, balance := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		settings := &iqRunnerSettingsStub{settings: iqRunnerSettings()}
		repo := &iqRunnerRepoStub{identity: &IQIdentity{UserID: 1, Balance: balance}}
		svc := NewOpenAIIQService(settings, nil, nil, nil, repo, nil, nil, nil, nil)
		require.NoError(t, svc.runIQRound(context.Background()))
		require.Zero(t, repo.claimCalls)
	}
}

func TestOpenAIIQRunnerStopsWithoutMakingRequests(t *testing.T) {
	disabled := iqRunnerSettings()
	disabled.Enabled = false
	settings := &iqRunnerSettingsStub{settings: disabled}
	repo := &iqRunnerRepoStub{identity: &IQIdentity{UserID: 1, Balance: 1}}
	svc := NewOpenAIIQService(settings, nil, nil, nil, repo, nil, nil, nil, nil)
	svc.Start()
	svc.Stop()
	require.Zero(t, repo.claimCalls)
	require.Zero(t, repo.keyCalls)
}

func TestOpenAIIQRunnerConfigRemovalSkipsBeforeKeyOrHTTP(t *testing.T) {
	disabled := iqRunnerSettings()
	disabled.Enabled = false
	settings := &iqRunnerSettingsStub{settings: disabled}
	repo := &iqRunnerRepoStub{}
	svc := NewOpenAIIQService(settings, nil, nil, nil, repo, nil, nil, nil, nil)
	result := svc.runIQGroup(context.Background(), &IQRun{ID: 1, Question: "immutable", Answer: "15", Model: "gpt-6-astra"}, iqRunnerSettings(), 1, IQResult{GroupID: 7, Status: "pending"})
	require.Equal(t, "skipped", result.Status)
	require.Zero(t, repo.keyCalls)
	require.Zero(t, repo.reserveCalls)
}

func TestOpenAIIQRunnerReservedFailureDoesNotDispatch(t *testing.T) {
	settings := &iqRunnerSettingsStub{settings: iqRunnerSettings()}
	group := &Group{ID: 7, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 1}
	repo := &iqRunnerRepoStub{reserve: false}
	svc := NewOpenAIIQService(settings, &iqRunnerGroupsStub{group: group}, nil, nil, repo, NewBillingService(nil, nil), nil, &iqRunnerAccountsStub{accounts: []Account{{ID: 1}}}, nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("HTTP must not run when reservation is rejected")
	}))
	defer server.Close()
	svc.endpoint, svc.client = server.URL, server.Client()
	result := svc.runIQGroup(context.Background(), &IQRun{ID: 1, Question: "immutable", Answer: "15", Model: "gpt-6-astra"}, iqRunnerSettings(), 1, IQResult{GroupID: 7, Status: "pending"})
	require.Equal(t, "skipped", result.Status)
	require.Equal(t, 1, repo.reserveCalls)
}

func TestOpenAIIQRunnerUnknownPricingDoesNotReserveOrDispatch(t *testing.T) {
	settings := &iqRunnerSettingsStub{settings: iqRunnerSettings()}
	group := &Group{ID: 7, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 1}
	repo := &iqRunnerRepoStub{reserve: true}
	svc := NewOpenAIIQService(settings, &iqRunnerGroupsStub{group: group}, nil, nil, repo, nil, nil, nil, nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Fatal("HTTP must not run without identified pricing") }))
	defer server.Close()
	svc.endpoint, svc.client = server.URL, server.Client()

	result := svc.runIQGroup(context.Background(), &IQRun{ID: 1, Question: "immutable", Answer: "15", Model: "gpt-6-astra"}, iqRunnerSettings(), 1, IQResult{GroupID: 7, Status: "pending"})
	require.Equal(t, "skipped", result.Status)
	require.Zero(t, repo.reserveCalls)
}

func TestOpenAIIQRunnerBindingFailureDoesNotDispatch(t *testing.T) {
	settings := &iqRunnerSettingsStub{settings: iqRunnerSettings()}
	group := &Group{ID: 7, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 1}
	repo := &iqRunnerRepoStub{reserve: true, bindErr: errors.New("binding unavailable")}
	svc := NewOpenAIIQService(settings, &iqRunnerGroupsStub{group: group}, nil, nil, repo, NewBillingService(nil, nil), nil, &iqRunnerAccountsStub{accounts: []Account{{ID: 1}}}, nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("HTTP must not run before durable expense correlation is saved")
	}))
	defer server.Close()
	svc.endpoint, svc.client = server.URL, server.Client()
	result := svc.runIQGroup(context.Background(), &IQRun{ID: 1, Question: "immutable", Answer: "15", Model: "gpt-6-astra"}, iqRunnerSettings(), 1, IQResult{GroupID: 7, Status: "pending"})
	require.Equal(t, "skipped", result.Status)
	require.Equal(t, 1, repo.reserveCalls)
	require.Len(t, repo.boundRequests, 1)
}

func TestOpenAIIQEstimateUsesUpstreamAccountMultiplier(t *testing.T) {
	accountRate := 0.01
	group := &Group{ID: 7, Platform: PlatformOpenAI, RateMultiplier: 100, PeakRateEnabled: true, PeakRateMultiplier: 10}
	accounts := &iqRunnerAccountsStub{accounts: []Account{{ID: 1, RateMultiplier: &accountRate}}}
	svc := NewOpenAIIQService(nil, nil, nil, nil, nil, NewBillingService(nil, nil), nil, accounts, nil)
	estimated, err := svc.estimateIQCost(context.Background(), group, "gpt-6-astra", []byte(`{}`), 32)
	require.NoError(t, err)
	group.RateMultiplier, group.PeakRateEnabled = 1, false
	ordinaryRateEstimate, err := svc.estimateIQCost(context.Background(), group, "gpt-6-astra", []byte(`{}`), 32)
	require.NoError(t, err)
	require.Equal(t, ordinaryRateEstimate, estimated, "user/group sales multipliers must not set the upstream expense budget")
	accountRate = 1
	fullAccountEstimate, err := svc.estimateIQCost(context.Background(), group, "gpt-6-astra", []byte(`{}`), 32)
	require.NoError(t, err)
	require.Greater(t, fullAccountEstimate, estimated)
}

func TestOpenAIIQRunnerCompletesEachSelectedGroupUsingClaimedSnapshot(t *testing.T) {
	settings := &iqRunnerSettingsStub{settings: iqRunnerSettings()}
	settings.settings.GroupIDs = []int64{7, 8, 9}
	group := &Group{Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 1}
	repo := &iqRunnerRepoStub{
		identity: &IQIdentity{UserID: 12, Balance: 1},
		run:      &IQRun{ID: 99, Question: "claimed question", Answer: "15", Model: "gpt-6-astra"},
		results: []IQResult{
			{GroupID: 7, GroupName: "one", Status: "pending"},
			{GroupID: 8, GroupName: "two", Status: "pending"},
			{GroupID: 9, GroupName: "three", Status: "pending"},
		},
		reserve: true,
	}
	svc := NewOpenAIIQService(settings, &iqRunnerGroupsStub{group: group}, nil, nil, repo, NewBillingService(nil, nil), nil, &iqRunnerAccountsStub{accounts: []Account{{ID: 1}}}, nil)

	var requestInputs []string
	var requestModels []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		repo.mu.Lock()
		reservationsBeforeRequest := repo.reserveCalls
		bindingsBeforeRequest := append([]string(nil), repo.boundRequests...)
		repo.mu.Unlock()
		require.GreaterOrEqual(t, reservationsBeforeRequest, 1, "budget must be reserved before HTTP dispatch")
		dispatchID := r.Header.Get(iqDispatchHeader)
		_, err := uuid.Parse(dispatchID)
		require.NoError(t, err)
		require.Contains(t, bindingsBeforeRequest, "client:iq-"+dispatchID, "expense correlation must be durable before HTTP dispatch")

		var payload map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		input := payload["input"].(string)
		model := payload["model"].(string)

		settings.mu.Lock()
		requestInputs = append(requestInputs, input)
		requestModels = append(requestModels, model)
		requestNumber := len(requestInputs)
		if requestNumber == 1 {
			settings.settings.Question = "newly configured question"
		}
		settings.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch requestNumber {
		case 1:
			_, _ = w.Write([]byte(`{"status":"completed","model":"actual-gpt","output":[{"content":[{"type":"output_text","text":"reasoning\nFINAL_ANSWER: 15"}]}]}`))
		case 2:
			_, _ = w.Write([]byte(`{"status":"completed","model":"actual-gpt","output":[{"content":[{"type":"output_text","text":"reasoning\nFINAL_ANSWER: 14"}]}]}`))
		default:
			_, _ = w.Write([]byte(`{"status":"in_progress","model":"actual-gpt"}`))
		}
	}))
	defer server.Close()
	svc.endpoint, svc.client = server.URL, server.Client()

	require.NoError(t, svc.runIQRound(context.Background()))
	require.Equal(t, 3, len(requestInputs))
	require.Equal(t, 3, repo.reserveCalls)
	require.Len(t, repo.boundRequests, 3)
	require.Equal(t, 4, repo.reconcileCalls, "settlement runs initially and after every response, including incomplete responses")
	require.Equal(t, 1, repo.finishCalls)
	require.Len(t, repo.completed, 3)

	statuses := make([]string, 0, len(repo.completed))
	for _, result := range repo.completed {
		statuses = append(statuses, result.Status)
	}
	sort.Strings(statuses)
	require.Equal(t, []string{"correct", "error", "incorrect"}, statuses)
	for index := range requestInputs {
		require.Contains(t, requestInputs[index], "claimed question")
		require.NotContains(t, requestInputs[index], "newly configured question")
		require.NotContains(t, requestInputs[index], "15")
		require.Equal(t, "gpt-6-astra", requestModels[index])
	}
}

func iqRunnerSettings() IQSettings {
	return IQSettings{Enabled: true, GroupIDs: []int64{7}, Model: "gpt-6-astra", Question: "configured", ExpectedAnswer: "15", IntervalSeconds: 3600, DailyBudgetUSD: 1, PerRequestLimitUSD: 1, MaxOutputTokens: 32}
}
