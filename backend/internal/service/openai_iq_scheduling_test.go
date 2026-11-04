package service

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// iqSchedulingRepo models the repository's two independent budget buckets:
// full request holds and later upstream-cost settlements.  Budget reconciles
// requested settlements because the production Budget query observes durable
// gateway usage that may arrive after a request completes.
type iqSchedulingRepo struct {
	*iqRunnerRepoStub

	mu                    sync.Mutex
	holds                 map[int64]float64
	spent                 float64
	settleRequested       int
	actualCost            float64
	maxCommitted          float64
	budgetWait            chan struct{}
	budgetFailures        chan struct{}
	budgetErrorsRemaining int
	completedSignal       chan struct{}
	completeGate          <-chan struct{}
}

func newIQSchedulingRepo(run *IQRun, results []IQResult) *iqSchedulingRepo {
	return &iqSchedulingRepo{
		iqRunnerRepoStub: &iqRunnerRepoStub{
			identity: &IQIdentity{UserID: 12, Balance: 1}, run: run, results: results,
		},
		holds:           make(map[int64]float64),
		actualCost:      0.018,
		completedSignal: make(chan struct{}, len(results)),
	}
}

func (r *iqSchedulingRepo) ReserveBudget(_ context.Context, _ int64, groupID int64, _ time.Time, hold, daily float64) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	committed := r.spent
	for _, amount := range r.holds {
		committed += amount
	}
	if committed+hold > daily+1e-9 {
		if r.budgetWait != nil {
			select {
			case r.budgetWait <- struct{}{}:
			default:
			}
		}
		return false, nil
	}
	r.holds[groupID] = hold
	committed += hold
	if committed > r.maxCommitted {
		r.maxCommitted = committed
	}
	r.iqRunnerRepoStub.mu.Lock()
	r.reserveCalls++
	r.iqRunnerRepoStub.mu.Unlock()
	return true, nil
}

func (r *iqSchedulingRepo) settleLocked() {
	for groupID, hold := range r.holds {
		if r.settleRequested == 0 {
			return
		}
		delete(r.holds, groupID)
		r.spent += math.Min(hold, r.actualCost)
		r.settleRequested--
	}
}

func (r *iqSchedulingRepo) ReconcileBudget(context.Context) error {
	r.mu.Lock()
	r.settleLocked()
	r.mu.Unlock()
	return nil
}

func (r *iqSchedulingRepo) Budget(_ context.Context, daily float64) (IQBudget, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.budgetErrorsRemaining > 0 {
		r.budgetErrorsRemaining--
		if r.budgetFailures != nil {
			select {
			case r.budgetFailures <- struct{}{}:
			default:
			}
		}
		return IQBudget{}, errors.New("temporary budget read failure")
	}
	r.settleLocked()
	reserved := 0.0
	for _, hold := range r.holds {
		reserved += hold
	}
	return IQBudget{SpentUSD: r.spent, ReservedUSD: reserved, RemainingUSD: daily - r.spent - reserved}, nil
}

func (r *iqSchedulingRepo) requestSettlement(count int) {
	r.mu.Lock()
	r.settleRequested += count
	r.mu.Unlock()
}

func (r *iqSchedulingRepo) CompleteResult(ctx context.Context, runID int64, result IQResult) error {
	if r.completeGate != nil {
		select {
		case <-r.completeGate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	err := r.iqRunnerRepoStub.CompleteResult(ctx, runID, result)
	if err == nil && r.completedSignal != nil {
		r.completedSignal <- struct{}{}
	}
	return err
}

type iqBlockingTransport struct {
	entered chan struct{}
	release chan struct{}
	active  atomic.Int32
	peak    atomic.Int32
}

func (t *iqBlockingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	active := t.active.Add(1)
	for {
		previous := t.peak.Load()
		if active <= previous || t.peak.CompareAndSwap(previous, active) {
			break
		}
	}
	select {
	case t.entered <- struct{}{}:
	case <-req.Context().Done():
		t.active.Add(-1)
		return nil, req.Context().Err()
	}
	select {
	case <-t.release:
	case <-req.Context().Done():
		t.active.Add(-1)
		return nil, req.Context().Err()
	}
	t.active.Add(-1)
	return iqSchedulingResponse(), nil
}

func iqSchedulingResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"status":"completed","model":"gpt-6-astra","output":[{"content":[{"type":"output_text","text":"FINAL_ANSWER: 15"}]}]}`)),
	}
}

func iqSchedulingService(repo OpenAIIQRepository, settings IQSettings, transport http.RoundTripper) *OpenAIIQService {
	group := &Group{Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 1}
	svc := NewOpenAIIQService(
		&iqRunnerSettingsStub{settings: settings}, &iqRunnerGroupsStub{group: group}, nil, nil, repo,
		NewBillingService(nil, nil), nil,
		&iqRunnerAccountsStub{accounts: []Account{{ID: 1, Status: StatusActive, Schedulable: true}}}, nil,
	)
	svc.endpoint = "http://iq.test/v1/responses"
	svc.client = &http.Client{Transport: transport}
	return svc
}

func iqSchedulingResults(count int) []IQResult {
	results := make([]IQResult, count)
	for i := range results {
		results[i] = IQResult{GroupID: int64(i + 1), GroupName: "group", Status: "pending"}
	}
	return results
}

func waitIQScheduling(t *testing.T, ch <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func TestOpenAIIQDelayedSettlementKeepsQueuedGroupsUntilBudgetFrees(t *testing.T) {
	settings := iqRunnerSettings()
	settings.GroupIDs = make([]int64, 12)
	settings.DailyBudgetUSD, settings.PerRequestLimitUSD = 1, 0.1
	for i := range settings.GroupIDs {
		settings.GroupIDs[i] = int64(i + 1)
	}
	run := &IQRun{ID: 10, Question: "q", Answer: "15", Model: "gpt-6-astra"}
	results := iqSchedulingResults(12)
	repo := newIQSchedulingRepo(run, results)
	transport := &iqBlockingTransport{entered: make(chan struct{}, 12), release: make(chan struct{}, 12)}
	svc := iqSchedulingService(repo, settings, transport)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- svc.runIQGroups(ctx, run, settings, 12, results) }()
	for range OpenAIIQConcurrency - 6 { // exactly ten holds fit in the one-dollar budget
		waitIQScheduling(t, transport.entered, "initial admitted request")
	}
	for range OpenAIIQConcurrency - 6 {
		transport.release <- struct{}{}
	}
	for range OpenAIIQConcurrency - 6 {
		waitIQScheduling(t, repo.completedSignal, "initial result write")
	}

	// The two remaining groups are budget-waiting, not completed as skipped.
	repo.iqRunnerRepoStub.mu.Lock()
	completedBeforeSettlement := append([]IQResult(nil), repo.completed...)
	reservesBeforeSettlement := repo.reserveCalls
	repo.iqRunnerRepoStub.mu.Unlock()
	require.Equal(t, 10, reservesBeforeSettlement)
	require.Len(t, completedBeforeSettlement, 10)
	for _, result := range completedBeforeSettlement {
		require.NotEqual(t, "skipped", result.Status)
	}

	// Two delayed actual-cost records do not over-admit: with eight full holds
	// still outstanding, they free room for exactly one more $0.10 hold.
	repo.requestSettlement(2)
	waitIQScheduling(t, transport.entered, "first request admitted after settlement")
	transport.release <- struct{}{}
	select {
	case <-transport.entered:
		t.Fatal("two settlements admitted a second full hold beyond the daily budget")
	case <-time.After(100 * time.Millisecond):
	}
	// A third settlement creates room for the final waiting request.
	repo.requestSettlement(1)
	waitIQScheduling(t, transport.entered, "final request admitted after settlement")
	transport.release <- struct{}{}
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("queued groups were not resumed after settlement")
	}

	repo.iqRunnerRepoStub.mu.Lock()
	completed := append([]IQResult(nil), repo.completed...)
	reserves := repo.reserveCalls
	repo.iqRunnerRepoStub.mu.Unlock()
	repo.mu.Lock()
	maxCommitted := repo.maxCommitted
	repo.mu.Unlock()
	require.Equal(t, 12, reserves)
	require.Len(t, completed, 12)
	require.LessOrEqual(t, maxCommitted, settings.DailyBudgetUSD+1e-9)
}

func TestOpenAIIQSchedulingRetainsBudgetWaitersAfterTransientBudgetError(t *testing.T) {
	settings := iqRunnerSettings()
	settings.GroupIDs = make([]int64, 12)
	settings.DailyBudgetUSD, settings.PerRequestLimitUSD = 1, 0.1
	for i := range settings.GroupIDs {
		settings.GroupIDs[i] = int64(i + 1)
	}
	run := &IQRun{ID: 13, Question: "q", Answer: "15", Model: "gpt-6-astra"}
	results := iqSchedulingResults(12)
	repo := newIQSchedulingRepo(run, results)
	repo.budgetErrorsRemaining, repo.budgetFailures = 1, make(chan struct{}, 1)
	transport := &iqBlockingTransport{entered: make(chan struct{}, 12), release: make(chan struct{}, 12)}
	svc := iqSchedulingService(repo, settings, transport)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- svc.runIQGroups(ctx, run, settings, 12, results) }()
	for range OpenAIIQConcurrency - 6 {
		waitIQScheduling(t, transport.entered, "initial admitted request")
	}
	for range OpenAIIQConcurrency - 6 {
		transport.release <- struct{}{}
	}
	waitIQScheduling(t, repo.budgetFailures, "transient budget read")
	for range OpenAIIQConcurrency - 6 {
		waitIQScheduling(t, repo.completedSignal, "initial result write")
	}
	repo.iqRunnerRepoStub.mu.Lock()
	reservesBeforeRecovery := repo.reserveCalls
	completedBeforeRecovery := append([]IQResult(nil), repo.completed...)
	repo.iqRunnerRepoStub.mu.Unlock()
	require.Equal(t, 10, reservesBeforeRecovery)
	for _, result := range completedBeforeRecovery {
		require.NotEqual(t, "skipped", result.Status)
	}

	// The next budget observation contains three actual settlements, enough to
	// admit both retained waiters without relaxing the full-hold admission rule.
	repo.requestSettlement(3)
	for range 2 {
		waitIQScheduling(t, transport.entered, "retained waiter admitted after recovery")
		transport.release <- struct{}{}
	}
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("transient budget failure discarded queued groups")
	}
	repo.iqRunnerRepoStub.mu.Lock()
	defer repo.iqRunnerRepoStub.mu.Unlock()
	require.Equal(t, 12, repo.reserveCalls)
	require.Len(t, repo.completed, 12)
}

func TestOpenAIIQCancelWhileBudgetWaitingJoinsWritesBeforeFinish(t *testing.T) {
	settings := iqRunnerSettings()
	settings.GroupIDs = make([]int64, 12)
	settings.DailyBudgetUSD, settings.PerRequestLimitUSD = 1, 0.1
	for i := range settings.GroupIDs {
		settings.GroupIDs[i] = int64(i + 1)
	}
	run := &IQRun{ID: 11, Question: "q", Answer: "15", Model: "gpt-6-astra"}
	repo := newIQSchedulingRepo(run, iqSchedulingResults(12))
	repo.budgetWait = make(chan struct{}, 1)
	writeRelease := make(chan struct{}, OpenAIIQConcurrency)
	repo.completeGate = writeRelease
	transport := &iqBlockingTransport{entered: make(chan struct{}, 12), release: make(chan struct{}, 12)}
	for range OpenAIIQConcurrency - 6 {
		transport.release <- struct{}{}
	}
	svc := iqSchedulingService(repo, settings, transport)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- svc.runIQRound(ctx) }()
	waitIQScheduling(t, repo.budgetWait, "budget queue admission failure")
	cancel()

	select {
	case <-done:
		t.Fatal("round finished before dispatched result writes were allowed")
	case <-time.After(100 * time.Millisecond):
	}
	repo.iqRunnerRepoStub.mu.Lock()
	require.Zero(t, repo.finishCalls)
	require.Equal(t, 10, repo.reserveCalls)
	repo.iqRunnerRepoStub.mu.Unlock()
	for range OpenAIIQConcurrency - 6 {
		writeRelease <- struct{}{}
	}
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled round did not join started writes")
	}
	repo.iqRunnerRepoStub.mu.Lock()
	defer repo.iqRunnerRepoStub.mu.Unlock()
	require.Equal(t, 1, repo.finishCalls)
	require.Len(t, repo.completed, 10)
}

func TestOpenAIIQSchedulingConcurrencyCaps64GroupsAt16(t *testing.T) {
	settings := iqRunnerSettings()
	settings.GroupIDs = make([]int64, 64)
	settings.DailyBudgetUSD, settings.PerRequestLimitUSD = 64, 1
	for i := range settings.GroupIDs {
		settings.GroupIDs[i] = int64(i + 1)
	}
	run := &IQRun{ID: 12, Question: "q", Answer: "15", Model: "gpt-6-astra"}
	results := iqSchedulingResults(64)
	repo := newIQSchedulingRepo(run, results)
	transport := &iqBlockingTransport{entered: make(chan struct{}, 64), release: make(chan struct{}, 64)}
	svc := iqSchedulingService(repo, settings, transport)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- svc.runIQGroups(ctx, run, settings, 12, results) }()
	for wave := 0; wave < 4; wave++ {
		for range OpenAIIQConcurrency {
			waitIQScheduling(t, transport.entered, "concurrency-limited request")
		}
		select {
		case <-transport.entered:
			t.Fatal("a seventeenth request started before a worker slot was released")
		case <-time.After(100 * time.Millisecond):
		}
		for range OpenAIIQConcurrency {
			transport.release <- struct{}{}
		}
	}
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("all concurrency-limited groups did not complete")
	}
	repo.iqRunnerRepoStub.mu.Lock()
	completed := append([]IQResult(nil), repo.completed...)
	reserves := repo.reserveCalls
	repo.iqRunnerRepoStub.mu.Unlock()
	require.Equal(t, 64, reserves)
	require.Len(t, completed, 64)
	require.LessOrEqual(t, transport.peak.Load(), int32(OpenAIIQConcurrency))
	require.Less(t, time.Duration((64/OpenAIIQConcurrency))*iqHTTPTimeout+iqResultWriteTimeout, iqRoundTimeout)
}
