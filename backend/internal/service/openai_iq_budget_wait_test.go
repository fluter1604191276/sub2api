package service

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenAIIQBudgetWaitDistinguishesActiveUnconfirmedAndSpent(t *testing.T) {
	now := time.Now()
	recent, old := now.Add(-time.Second), now.Add(-time.Minute)
	for _, tc := range []struct {
		name   string
		budget IQBudget
		want   string
	}{
		{"short grace", IQBudget{SpentUSD: .077, ReservedUSD: .5, RemainingUSD: .423, UnconfirmedUSD: .5, UnconfirmedSince: &recent}, ""},
		{"old unknown hold", IQBudget{SpentUSD: .077, ReservedUSD: .5, RemainingUSD: .423, UnconfirmedUSD: .5, UnconfirmedSince: &old}, iqUnconfirmedBudgetReason},
		{"active request can free enough", IQBudget{ReservedUSD: .6, RemainingUSD: .4, UnconfirmedUSD: .1, UnconfirmedSince: &old}, ""},
		{"active request cannot free enough", IQBudget{SpentUSD: .077, ReservedUSD: .6, RemainingUSD: .323, UnconfirmedUSD: .5, UnconfirmedSince: &old}, iqUnconfirmedBudgetReason},
		{"no terminal holds yet", IQBudget{SpentUSD: .077, ReservedUSD: .5, RemainingUSD: .423}, ""},
		{"settled expenditure blocks admission", IQBudget{SpentUSD: .6, ReservedUSD: .3, RemainingUSD: .1, UnconfirmedUSD: .3, UnconfirmedSince: &old}, "今日实际支出已达预算准入上限"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, iqBudgetStopReason(tc.budget, 1, .5, now))
		})
	}
}

type iqUnknownExpenseRepo struct {
	*iqSchedulingRepo
	unknownMu sync.Mutex
	unknown   map[int64]time.Time
}

func (r *iqUnknownExpenseRepo) CompleteResult(ctx context.Context, runID int64, result IQResult) error {
	if err := r.iqSchedulingRepo.CompleteResult(ctx, runID, result); err != nil {
		return err
	}
	r.unknownMu.Lock()
	r.unknown[result.GroupID] = time.Now()
	r.unknownMu.Unlock()
	return nil
}

func (r *iqUnknownExpenseRepo) Budget(ctx context.Context, daily float64) (IQBudget, error) {
	b, err := r.iqSchedulingRepo.Budget(ctx, daily)
	if err != nil {
		return b, err
	}
	r.unknownMu.Lock()
	defer r.unknownMu.Unlock()
	r.iqSchedulingRepo.mu.Lock()
	defer r.iqSchedulingRepo.mu.Unlock()
	for groupID, since := range r.unknown {
		if hold, ok := r.holds[groupID]; ok {
			b.UnconfirmedUSD += hold
			if b.UnconfirmedSince == nil || since.After(*b.UnconfirmedSince) {
				timestamp := since
				b.UnconfirmedSince = &timestamp
			}
		}
	}
	return b, nil
}

type iqBudgetTransportFunc func(*http.Request) (*http.Response, error)

func (f iqBudgetTransportFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func iqUnknownExpenseFixture(count int) (*iqUnknownExpenseRepo, *OpenAIIQService, IQSettings, *IQRun, []IQResult) {
	settings := iqRunnerSettings()
	results := iqSchedulingResults(count)
	settings.GroupIDs = make([]int64, count)
	for i := range results {
		settings.GroupIDs[i] = results[i].GroupID
	}
	settings.DailyBudgetUSD, settings.PerRequestLimitUSD = 1, .5
	run := &IQRun{ID: 147, Question: "synthetic", Answer: "15", Model: "gpt-6-astra"}
	repo := &iqUnknownExpenseRepo{iqSchedulingRepo: newIQSchedulingRepo(run, results), unknown: map[int64]time.Time{}}
	repo.spent = .077
	svc := iqSchedulingService(repo, settings, iqBudgetTransportFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("synthetic disconnected response")
	}))
	return repo, svc, settings, run, results
}

func TestOpenAIIQUnknownExpenseEndsSevenWaitersAfterShortGrace(t *testing.T) {
	repo, svc, settings, run, results := iqUnknownExpenseFixture(8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	started := time.Now()
	go func() { done <- svc.runIQGroups(ctx, run, settings, 12, results) }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("unknown expense kept the seven groups waiting past the short grace")
	}
	require.GreaterOrEqual(t, time.Since(started), iqUsageGracePeriod)
	require.Len(t, repo.completed, 8)
	require.Equal(t, 1, repo.reserveCalls)
	require.InDelta(t, .5, repo.holds[1], 1e-10, "grace expiry must never release unknown expense")
	for _, result := range repo.completed {
		if result.GroupID == 1 {
			require.Equal(t, "error", result.Status)
		} else {
			require.Equal(t, "skipped", result.Status)
			require.Equal(t, iqUnconfirmedBudgetReason, result.Error)
		}
	}
}

func TestOpenAIIQNextRoundRecognizesExistingUnconfirmedHold(t *testing.T) {
	repo, svc, settings, run, results := iqUnknownExpenseFixture(2)
	repo.holds[99] = .5
	repo.unknown[99] = time.Now().Add(-time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- svc.runIQGroups(ctx, run, settings, 12, results) }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("new round repeated the prior run's unknown-expense wait")
	}
	require.Zero(t, repo.reserveCalls)
	require.Len(t, repo.completed, 2)
	require.InDelta(t, .5, repo.holds[99], 1e-10)
	for _, result := range repo.completed {
		require.Equal(t, iqUnconfirmedBudgetReason, result.Error)
	}
}

func TestOpenAIIQLateExpenseDuringGraceResumesWaitingGroup(t *testing.T) {
	repo, svc, settings, run, results := iqUnknownExpenseFixture(2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- svc.runIQGroups(ctx, run, settings, 12, results) }()
	waitIQScheduling(t, repo.completedSignal, "failed request persisted before delayed usage")
	repo.requestSettlement(1)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(4 * time.Second):
		t.Fatal("valid delayed usage did not resume the waiting group within grace")
	}
	require.Equal(t, 2, repo.reserveCalls)
	require.Len(t, repo.completed, 2)
	for _, result := range repo.completed {
		require.Equal(t, "error", result.Status, "both fake requests ran; neither should be skipped")
	}
}

func TestOpenAIIQQueuedGroupsPersistDistinctStopReasons(t *testing.T) {
	for _, reason := range []string{"disabled", "deadline", "canceled", "removed"} {
		t.Run(reason, func(t *testing.T) {
			repo, svc, settings, run, results := iqUnknownExpenseFixture(2)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if reason == "deadline" {
				ctx, cancel = context.WithTimeout(ctx, time.Second)
				defer cancel()
			} else if reason == "canceled" {
				cancel()
			}
			done := make(chan error, 1)
			go func() { done <- svc.runIQGroups(ctx, run, settings, 12, results) }()
			if reason == "disabled" || reason == "removed" {
				waitIQScheduling(t, repo.completedSignal, "first completed request")
				stored := svc.settings.(*iqRunnerSettingsStub)
				stored.mu.Lock()
				if reason == "disabled" {
					stored.settings.Enabled = false
				} else {
					stored.settings.GroupIDs = []int64{1}
				}
				stored.mu.Unlock()
			}
			select {
			case err := <-done:
				if reason == "canceled" {
					require.ErrorIs(t, err, context.Canceled)
				} else {
					require.NoError(t, err)
				}
			case <-time.After(4 * time.Second):
				t.Fatal("queued groups did not receive a terminal reason")
			}
			require.Len(t, repo.completed, 2)
			want := map[string]string{"disabled": "检测已关闭", "deadline": "本轮剩余时间不足", "canceled": "本轮执行已取消", "removed": "分组已从检测配置中移除"}[reason]
			for _, result := range repo.completed {
				if result.Status == "skipped" {
					require.Contains(t, result.Error, want)
				}
			}
		})
	}
}
