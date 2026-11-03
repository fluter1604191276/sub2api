package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReviewWarmConfirmationSchedulesSecondRoundSoon(t *testing.T) {
	repo := &reviewScheduleRepo{reviewProtocolRepo: reviewProtocolRepo{accept: true}}
	runner := &GroupRecoveryProbeRunner{
		repo:           repo,
		accountTestSvc: &reviewBusinessTester{},
		randomFloat:    func() float64 { return 0.5 },
	}
	started := time.Now()
	runner.runJob(context.Background(), reviewProbeJob())

	require.Equal(t, GroupRecoveryProbeStatusWarm, repo.completion.Status)
	require.False(t, repo.completion.NextProbeAt.After(started.Add(6*time.Minute)),
		"first warm confirmation was scheduled at %s, want within 6 minutes", repo.completion.NextProbeAt)
	require.True(t, repo.completion.NextProbeAt.After(started.Add(4*time.Minute)))

	repo.eligibility = map[string]GroupRecoveryProbeProtocolStatus{
		APIProtocolChatCompletions: {Status: GroupRecoveryProbeStatusWarm, ConsecutiveSuccesses: 1},
		APIProtocolAnthropic:       {Status: GroupRecoveryProbeStatusWarm, ConsecutiveSuccesses: 1},
		APIProtocolResponses:       {Status: GroupRecoveryProbeStatusWarm, ConsecutiveSuccesses: 1},
	}
	runner.runJob(context.Background(), reviewProbeJob())
	require.Equal(t, GroupRecoveryProbeStatusEligible, repo.completion.Status)
	for _, protocol := range []string{APIProtocolChatCompletions, APIProtocolAnthropic, APIProtocolResponses} {
		require.Equal(t, GroupRecoveryProbeStatusEligible, repo.completion.ProtocolEligibility[protocol].Status)
	}
}

type reviewScheduleRepo struct {
	reviewProtocolRepo
	completion  GroupRecoveryProbeCompletion
	eligibility map[string]GroupRecoveryProbeProtocolStatus
}

func (r *reviewScheduleRepo) Complete(_ context.Context, completion GroupRecoveryProbeCompletion) (bool, error) {
	r.completion = completion
	return r.accept, nil
}

func (r *reviewScheduleRepo) GetProtocolEligibility(context.Context, int64) (map[string]GroupRecoveryProbeProtocolStatus, error) {
	return r.eligibility, nil
}

type countingProbeRepo struct {
	GroupRecoveryProbeRepository
	reads  int
	states map[int64]GroupRecoveryProbeState
}

func (r *countingProbeRepo) ListStates(context.Context, int64, []int64, string) (map[int64]GroupRecoveryProbeState, error) {
	r.reads++
	return r.states, nil
}

func TestReviewEligibleAccountSurvivesOrderingFallback(t *testing.T) {
	account := deepSeekResponsesTestAccount()
	unqualified := deepSeekResponsesTestAccount()
	unqualified.ID = account.ID + 1
	eligible := GroupRecoveryProbeState{
		AccountID: account.ID,
		Status:    GroupRecoveryProbeStatusEligible,
		Model:     "deepseek-v4-pro",
		ProtocolEligibility: map[string]GroupRecoveryProbeProtocolStatus{
			APIProtocolResponses: {Status: GroupRecoveryProbeStatusEligible, ConsecutiveSuccesses: 2},
		},
	}
	repo := &countingProbeRepo{states: map[int64]GroupRecoveryProbeState{account.ID: eligible}}
	preview := &SmartSchedulerPreviewService{}
	preview.SetRecoveryProbeRepository(repo)
	group := &Group{
		ID:                   54,
		RecoveryProbeEnabled: true,
		RecoveryProbeModel:   "deepseek-v4-pro",
		Platform:             PlatformOpenAI,
	}
	ctx := WithOpenAIFailoverSelection(context.Background())
	accounts := []*Account{unqualified, account}

	t.Run("scheduler disabled", func(t *testing.T) {
		repo.reads = 0
		group.SmartSchedulerEnabled = false
		got, _ := runSmartSchedulerOrdering(ctx, preview, group, "deepseek-v4-pro", "responses", accounts)
		require.Greater(t, repo.reads, 0)
		require.Equal(t, []*Account{account}, got)
	})
	t.Run("statistics unavailable", func(t *testing.T) {
		repo.reads = 0
		group.SmartSchedulerEnabled = true
		got, _ := runSmartSchedulerOrdering(ctx, preview, group, "deepseek-v4-pro", "responses", accounts)
		require.Greater(t, repo.reads, 0)
		require.Equal(t, []*Account{account}, got)
	})
}

func TestReviewModelRoutedPlatformsKeepSharedEligibility(t *testing.T) {
	for _, platform := range []string{PlatformOpenCodeGo, PlatformCommandCode} {
		t.Run(platform, func(t *testing.T) {
			account := &Account{ID: 70, Platform: platform, Type: AccountTypeAPIKey}
			require.True(t, account.IsAdaptiveAPIProtocol())
			state := &GroupRecoveryProbeState{AccountID: account.ID, Status: GroupRecoveryProbeStatusEligible}
			ordering := &SmartSchedulerOrdering{
				Active:            true,
				OrderedAccountIDs: []int64{account.ID},
				RankByAccountID:   map[int64]int{account.ID: 1},
				ItemByAccountID: map[int64]SmartSchedulerPreviewItem{
					account.ID: {AccountID: account.ID, Pool: "primary", RecoveryProbe: state},
				},
			}
			group := &Group{ID: 9, RecoveryProbeEnabled: true, SmartSchedulerEnabled: true, RecoveryProbeModel: "gpt-5.6-sol"}
			got, _ := runSmartSchedulerOrdering(
				WithOpenAIFailoverSelection(context.Background()),
				reviewCandidateOrderer{ordering: ordering},
				group,
				"gpt-5.6-sol",
				"responses",
				[]*Account{account},
			)
			require.Equal(t, []*Account{account}, got)
		})
	}
}

func TestReviewNullErrorEmptyCompletionFailsOver(t *testing.T) {
	streams := []string{
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_null\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":0,\"output_tokens\":0,\"total_tokens\":0},\"error\":null}}\n\n",
		"data: {\"type\":\"response.completed\",\"error\":null,\"response\":{\"id\":\"resp_null_top\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":0,\"output_tokens\":0}}}\n\n",
	}
	for _, stream := range streams {
		t.Run(stream, func(t *testing.T) {
			_, recorder, svc, err := forwardDeepSeekSSE(t, strings.NewReader(stream), "req-null-error")
			var failoverErr *UpstreamFailoverError
			require.ErrorAs(t, err, &failoverErr)
			require.Empty(t, recorder.Body.String())
			require.False(t, svc.isOpenAIAccountModelRuntimeBlocked(deepSeekResponsesTestAccount(), "deepseek-v4-pro"))
		})
	}
}

func TestReviewAdaptiveAttemptsPerRoundDrivesExecution(t *testing.T) {
	repo := &reviewProtocolRepo{accept: true}
	tester := &reviewBusinessTester{}
	runner := &GroupRecoveryProbeRunner{
		repo:           repo,
		accountTestSvc: tester,
		randomFloat:    func() float64 { return 0.5 },
	}
	job := reviewProbeJob()
	job.AttemptsPerRound = 2
	runner.runJob(context.Background(), job)
	require.Equal(t, 2, tester.calls)
	require.Len(t, repo.audits, 1)
	require.Equal(t, 6, repo.audits[0].Attempts)
}
