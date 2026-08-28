package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type staleRecoveryProbeRepositoryStub struct {
	mu         sync.Mutex
	auditCount int
}

func (r *staleRecoveryProbeRepositoryStub) ClaimDue(context.Context, time.Time, int) ([]GroupRecoveryProbeJob, error) {
	return nil, nil
}

func (r *staleRecoveryProbeRepositoryStub) Complete(context.Context, GroupRecoveryProbeCompletion) (bool, error) {
	return false, nil
}

func (r *staleRecoveryProbeRepositoryStub) CreateAudit(context.Context, GroupRecoveryProbeAudit) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.auditCount++
	return nil
}

func (r *staleRecoveryProbeRepositoryStub) ListStates(context.Context, int64, []int64, string) (map[int64]GroupRecoveryProbeState, error) {
	return nil, nil
}

func (r *staleRecoveryProbeRepositoryStub) ReconcileRealUsage(context.Context, time.Time) (int64, error) {
	return 0, nil
}

type successfulRecoveryProbeAccountTesterStub struct{}

func (successfulRecoveryProbeAccountTesterStub) RunTestBackground(context.Context, int64, string) (*ScheduledTestResult, error) {
	return &ScheduledTestResult{Status: "success", LatencyMs: 100}, nil
}

func TestGroupRecoveryProbeRunnerStaleCompletionSkipsAuditAndSettlement(t *testing.T) {
	repo := &staleRecoveryProbeRepositoryStub{}
	runner := &GroupRecoveryProbeRunner{
		repo:           repo,
		accountTestSvc: successfulRecoveryProbeAccountTesterStub{},
		randomFloat:    func() float64 { return 0.5 },
	}
	runner.runJob(context.Background(), GroupRecoveryProbeJob{
		State: GroupRecoveryProbeState{
			ID: 11, GroupID: 7, AccountID: 19, Model: "gpt-test",
		},
		PhysicalStateID:   23,
		BeneficiaryGroups: 2,
		Mode:              GroupRecoveryProbeModeSmart,
		IntervalSeconds:   GroupRecoveryProbeDefaultIntervalSeconds,
		AttemptsPerRound:  1,
		ClaimedAt:         time.Now().Add(-time.Minute),
	})

	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.Zero(t, repo.auditCount)
}

func TestProvideGroupRecoveryProbeRunnerInjectsRepositoryIntoGatewaySchedulers(t *testing.T) {
	repo := &staleRecoveryProbeRepositoryStub{}
	previewScheduler := NewSmartSchedulerPreviewService(nil, nil, nil)
	gatewayScheduler := NewSmartSchedulerPreviewService(nil, nil, nil)
	openAIScheduler := NewSmartSchedulerPreviewService(nil, nil, nil)
	gateway := &GatewayService{smartScheduler: gatewayScheduler}
	openAIGateway := &OpenAIGatewayService{smartScheduler: openAIScheduler}

	runner := ProvideGroupRecoveryProbeRunner(repo, nil, nil, previewScheduler, gateway, openAIGateway)
	t.Cleanup(runner.Stop)

	require.NotNil(t, runner)
	require.Equal(t, repo, previewScheduler.recoveryProbe)
	require.Equal(t, repo, gatewayScheduler.recoveryProbe)
	require.Equal(t, repo, openAIScheduler.recoveryProbe)
}

func TestGroupRecoveryProbeRunnerInvalidatesAllSchedulerOrderingCaches(t *testing.T) {
	previewScheduler := smartSchedulerServiceWithCachedOrdering("preview")
	gatewayScheduler := smartSchedulerServiceWithCachedOrdering("gateway")
	openAIScheduler := smartSchedulerServiceWithCachedOrdering("openai")
	runner := &GroupRecoveryProbeRunner{
		schedulerCaches: []*SmartSchedulerPreviewService{previewScheduler, gatewayScheduler, openAIScheduler},
	}

	runner.invalidateSchedulerOrderingCaches()

	require.Empty(t, previewScheduler.orderingCache)
	require.Empty(t, gatewayScheduler.orderingCache)
	require.Empty(t, openAIScheduler.orderingCache)
}

func smartSchedulerServiceWithCachedOrdering(key string) *SmartSchedulerPreviewService {
	return &SmartSchedulerPreviewService{
		orderingCache: map[string]smartSchedulerOrderingCacheEntry{
			key: {expiresAt: time.Now().Add(time.Minute), ordering: &SmartSchedulerOrdering{Active: true}},
		},
	}
}

func TestBuildGroupRecoveryProbeReservationFailureDoesNotPenalizeAccount(t *testing.T) {
	now := time.Date(2026, 8, 11, 2, 0, 0, 0, time.UTC)
	job := GroupRecoveryProbeJob{
		State: GroupRecoveryProbeState{
			ID: 11, Status: GroupRecoveryProbeStatusProbing,
			ConsecutiveSuccesses: 3, ConsecutiveFailures: 0, LatencyMs: 800,
		},
		PreviousStatus: GroupRecoveryProbeStatusEligible,
		ClaimedAt:      now.Add(-time.Second),
	}

	completion, settlement := buildGroupRecoveryProbeReservationFailure(job, errors.New("database unavailable"), now)
	require.Equal(t, GroupRecoveryProbeStatusEligible, completion.Status)
	require.Equal(t, GroupRecoveryProbeErrorTransient, completion.LastErrorClass)
	require.Equal(t, now.Add(time.Minute), completion.NextProbeAt)
	require.Equal(t, 3, completion.ConsecutiveSuccesses)
	require.Equal(t, GroupRecoveryProbeSettlementFailed, settlement)
}

func TestBuildGroupRecoveryProbeReservationFailureBudgetBlockPreservesPriorState(t *testing.T) {
	now := time.Date(2026, 8, 11, 2, 0, 0, 0, time.UTC)
	job := GroupRecoveryProbeJob{
		State: GroupRecoveryProbeState{
			ID: 11, Status: GroupRecoveryProbeStatusProbing,
			ConsecutiveFailures: 2, LastErrorClass: GroupRecoveryProbeErrorTransient, LastError: "upstream 503",
		},
		PreviousStatus: GroupRecoveryProbeStatusFailed,
		ClaimedAt:      now.Add(-time.Second),
	}

	completion, settlement := buildGroupRecoveryProbeReservationFailure(job, ErrGroupRecoveryProbeBudgetExceeded, now)
	require.Equal(t, GroupRecoveryProbeStatusFailed, completion.Status)
	require.Equal(t, GroupRecoveryProbeErrorTransient, completion.LastErrorClass)
	require.Equal(t, "upstream 503", completion.LastError)
	require.Equal(t, now.Add(time.Hour), completion.NextProbeAt)
	require.Equal(t, 2, completion.ConsecutiveFailures)
	require.Equal(t, GroupRecoveryProbeSettlementBudgetBlocked, settlement)
}

func TestNormalizeGroupRecoveryProbeConfig_DefaultsDisabled(t *testing.T) {
	cfg, err := NormalizeGroupRecoveryProbeConfig(GroupRecoveryProbeConfig{})
	require.NoError(t, err)
	require.False(t, cfg.Enabled)
	require.Equal(t, GroupRecoveryProbeModeSmart, cfg.Mode)
	require.Equal(t, GroupRecoveryProbeDefaultIntervalSeconds, cfg.IntervalSeconds)
	require.Equal(t, 1, cfg.AttemptsPerRound)
	require.Equal(t, GroupRecoveryProbeIdleThresholdSeconds, cfg.IdleThresholdSeconds)
	require.Equal(t, GroupRecoveryProbeDefaultBackoffCapSeconds, cfg.BackoffCapSeconds)
}

func TestNormalizeGroupRecoveryProbeConfig_EnabledRequiresModel(t *testing.T) {
	_, err := NormalizeGroupRecoveryProbeConfig(GroupRecoveryProbeConfig{Enabled: true})
	require.ErrorContains(t, err, "recovery_probe_model")
}

func TestNormalizeGroupRecoveryProbeConfig_RejectsOutOfRangeValues(t *testing.T) {
	_, err := NormalizeGroupRecoveryProbeConfig(GroupRecoveryProbeConfig{
		Enabled:          true,
		Mode:             GroupRecoveryProbeModeManual,
		Model:            "gpt-test",
		IntervalSeconds:  30,
		AttemptsPerRound: 6,
	})
	require.Error(t, err)
}

func TestGroupRecoveryProbeSmartBackoff(t *testing.T) {
	tests := []struct {
		failures int
		want     time.Duration
	}{
		{failures: 1, want: time.Minute},
		{failures: 2, want: 2 * time.Minute},
		{failures: 3, want: 5 * time.Minute},
		{failures: 4, want: 10 * time.Minute},
		{failures: 5, want: 15 * time.Minute},
		{failures: 6, want: 30 * time.Minute},
		{failures: 20, want: 30 * time.Minute},
	}
	for _, tt := range tests {
		require.Equal(t, tt.want, groupRecoveryProbeSmartBackoff(tt.failures, 30*time.Minute))
	}
}

func TestNormalizedGroupRecoveryProbeWorkerCount(t *testing.T) {
	require.Equal(t, groupRecoveryProbeDefaultWorkers, normalizedGroupRecoveryProbeWorkerCount(0))
	require.Equal(t, groupRecoveryProbeDefaultWorkers, normalizedGroupRecoveryProbeWorkerCount(-1))
	require.Equal(t, 2, normalizedGroupRecoveryProbeWorkerCount(2))
}

func TestClassifyGroupRecoveryProbeError(t *testing.T) {
	require.Equal(t, GroupRecoveryProbeErrorPermanent, classifyGroupRecoveryProbeError("API returned 401: invalid API key"))
	require.Equal(t, GroupRecoveryProbeErrorPermanent, classifyGroupRecoveryProbeError("insufficient balance"))
	require.Equal(t, GroupRecoveryProbeErrorTransient, classifyGroupRecoveryProbeError("upstream HTTP 502"))
	require.Equal(t, GroupRecoveryProbeErrorTransient, classifyGroupRecoveryProbeError("context deadline exceeded"))
}

func TestBuildGroupRecoveryProbeCompletion_ManualFailureUsesFixedInterval(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	job := GroupRecoveryProbeJob{
		State:           GroupRecoveryProbeState{ConsecutiveFailures: 2},
		Mode:            GroupRecoveryProbeModeManual,
		IntervalSeconds: 600,
	}
	completion := buildGroupRecoveryProbeCompletion(job, GroupRecoveryProbeRoundResult{
		Attempts:      1,
		FailureCount:  1,
		LastError:     "upstream HTTP 502",
		LastLatencyMs: 1200,
	}, now)
	require.Equal(t, GroupRecoveryProbeStatusFailed, completion.Status)
	require.Equal(t, 3, completion.ConsecutiveFailures)
	require.Equal(t, now.Add(10*time.Minute), completion.NextProbeAt)
}

func TestBuildGroupRecoveryProbeCompletion_SmartRecoveryProgression(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	job := GroupRecoveryProbeJob{
		State:             GroupRecoveryProbeState{},
		Mode:              GroupRecoveryProbeModeSmart,
		IntervalSeconds:   900,
		BackoffCapSeconds: 1800,
	}

	warm := buildGroupRecoveryProbeCompletion(job, GroupRecoveryProbeRoundResult{
		Attempts:     1,
		SuccessCount: 1,
	}, now)
	require.Equal(t, GroupRecoveryProbeStatusWarm, warm.Status)
	require.Equal(t, 1, warm.ConsecutiveSuccesses)

	job.State.ConsecutiveSuccesses = warm.ConsecutiveSuccesses
	eligible := buildGroupRecoveryProbeCompletion(job, GroupRecoveryProbeRoundResult{
		Attempts:     1,
		SuccessCount: 1,
	}, now)
	require.Equal(t, GroupRecoveryProbeStatusEligible, eligible.Status)
	require.Equal(t, 2, eligible.ConsecutiveSuccesses)
	require.Equal(t, now.Add(time.Hour), eligible.NextProbeAt)

	job.State.ConsecutiveSuccesses = eligible.ConsecutiveSuccesses
	thirdSuccess := buildGroupRecoveryProbeCompletion(job, GroupRecoveryProbeRoundResult{
		Attempts:     1,
		SuccessCount: 1,
	}, now)
	require.Equal(t, 3, thirdSuccess.ConsecutiveSuccesses)
	require.Equal(t, now.Add(2*time.Hour), thirdSuccess.NextProbeAt)

	job.State.ConsecutiveSuccesses = thirdSuccess.ConsecutiveSuccesses
	fourthSuccess := buildGroupRecoveryProbeCompletion(job, GroupRecoveryProbeRoundResult{
		Attempts:     1,
		SuccessCount: 1,
	}, now)
	require.Equal(t, 4, fourthSuccess.ConsecutiveSuccesses)
	require.Equal(t, now.Add(4*time.Hour), fourthSuccess.NextProbeAt)
}

func TestBuildGroupRecoveryProbeCompletion_PermanentFailurePausesAtLowFrequency(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	completion := buildGroupRecoveryProbeCompletion(GroupRecoveryProbeJob{
		Mode:              GroupRecoveryProbeModeSmart,
		BackoffCapSeconds: 1800,
	}, GroupRecoveryProbeRoundResult{
		Attempts:     1,
		FailureCount: 1,
		LastError:    "permission denied: model access unavailable",
	}, now)
	require.Equal(t, GroupRecoveryProbeStatusPaused, completion.Status)
	require.Equal(t, GroupRecoveryProbeErrorPermanent, completion.LastErrorClass)
	require.Equal(t, now.Add(GroupRecoveryProbePermanentRetryInterval), completion.NextProbeAt)
}

func TestApplyGroupRecoveryProbeStateToSchedulerItem(t *testing.T) {
	probeAt := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	item := SmartSchedulerPreviewItem{Pool: "primary", Decision: "primary_candidate"}
	applyGroupRecoveryProbeStateToSchedulerItem(&item, &GroupRecoveryProbeState{
		Status:              GroupRecoveryProbeStatusFailed,
		ConsecutiveFailures: 3,
		LastProbeAt:         &probeAt,
		LastError:           "upstream HTTP 502",
		Model:               "gpt-test",
	})
	require.Equal(t, "isolated", item.Pool)
	require.Equal(t, "recovery_probe_failed", item.Decision)
	require.NotNil(t, item.RecoveryProbe)

	item = SmartSchedulerPreviewItem{Pool: "primary", Decision: "primary_candidate"}
	applyGroupRecoveryProbeStateToSchedulerItem(&item, &GroupRecoveryProbeState{
		Status:      GroupRecoveryProbeStatusWarm,
		LastProbeAt: &probeAt,
		Model:       "gpt-test",
	})
	require.Equal(t, "warm", item.Pool)
	require.Equal(t, "recovery_probe_warm", item.Decision)
}

func TestApplyGroupRecoveryProbeStateToSchedulerItem_EligibleStateDoesNotIsolate(t *testing.T) {
	item := SmartSchedulerPreviewItem{
		Pool:     "primary",
		Decision: "primary_candidate",
	}
	applyGroupRecoveryProbeStateToSchedulerItem(&item, &GroupRecoveryProbeState{
		Status: GroupRecoveryProbeStatusEligible,
		Model:  "gpt-test",
	})
	require.Equal(t, "primary", item.Pool)
	require.NotNil(t, item.RecoveryProbe)
	require.Equal(t, GroupRecoveryProbeStatusEligible, item.RecoveryProbe.Status)
}

func TestApplyGroupRecoveryProbeStateToSchedulerItem_EligibleColdAccountBootstrapsQuality(t *testing.T) {
	item := SmartSchedulerPreviewItem{
		Pool:              "warm",
		Decision:          "observe",
		Schedulable:       true,
		ModelSupported:    true,
		EndpointSupported: true,
	}
	applyGroupRecoveryProbeStateToSchedulerItem(&item, &GroupRecoveryProbeState{
		Status:               GroupRecoveryProbeStatusEligible,
		Model:                "gpt-test",
		ConsecutiveSuccesses: 2,
		LatencyMs:            2500,
	})

	require.Equal(t, "warm", item.Pool)
	require.True(t, item.ProbeBootstrap)
	require.Equal(t, "recovery_probe_bootstrap", item.Decision)
	rawScore := smartSchedulerScore(item, []SmartSchedulerPreviewItem{item})
	require.NotNil(t, rawScore)
	require.GreaterOrEqual(t, *rawScore, smartSchedulerProbeBootstrapScoreMin)
	require.LessOrEqual(t, *rawScore, smartSchedulerProbeBootstrapScoreMax)
	require.InDelta(t, smartSchedulerProbeBootstrapConfidence, smartSchedulerConfidence(item), 0.001)
}

func TestApplyGroupRecoveryProbeStateToSchedulerItem_EligibleRealEvidenceDoesNotBootstrap(t *testing.T) {
	item := SmartSchedulerPreviewItem{
		Pool:      "primary",
		Decision:  "primary_candidate",
		Quality1h: AccountQualityPeriod{Last10: qualityWindowForPreview(91, accountQualityMinSamples)},
	}
	applyGroupRecoveryProbeStateToSchedulerItem(&item, &GroupRecoveryProbeState{
		Status:               GroupRecoveryProbeStatusEligible,
		Model:                "gpt-test",
		ConsecutiveSuccesses: 2,
		LatencyMs:            2500,
	})

	require.Equal(t, "primary", item.Pool)
	require.False(t, item.ProbeBootstrap)
}

func TestApplyGroupRecoveryProbeStateToSchedulerItem_WarmAndFailedStatesDoNotBootstrap(t *testing.T) {
	for _, status := range []string{GroupRecoveryProbeStatusWarm, GroupRecoveryProbeStatusFailed, GroupRecoveryProbeStatusPaused} {
		item := SmartSchedulerPreviewItem{Pool: "warm", Decision: "observe"}
		applyGroupRecoveryProbeStateToSchedulerItem(&item, &GroupRecoveryProbeState{
			Status:               status,
			Model:                "gpt-test",
			ConsecutiveSuccesses: 1,
			LatencyMs:            1200,
		})
		require.False(t, item.ProbeBootstrap, "status=%s", status)
	}
}

func TestBuildGroupRecoveryProbeAuditDoesNotFakeProbeCost(t *testing.T) {
	startedAt := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	finishedAt := startedAt.Add(2 * time.Second)
	job := GroupRecoveryProbeJob{
		State: GroupRecoveryProbeState{GroupID: 7, AccountID: 19, Model: "claude-sonnet-4-6"},
	}
	completion := GroupRecoveryProbeCompletion{
		Status:         GroupRecoveryProbeStatusFailed,
		LastErrorClass: GroupRecoveryProbeErrorTransient,
	}
	audit := buildGroupRecoveryProbeAudit(job, GroupRecoveryProbeRoundResult{
		Attempts:      2,
		FailureCount:  2,
		LastError:     "upstream URL https://secret.example/v1/messages?token=secret",
		LastLatencyMs: 2000,
	}, completion, startedAt, finishedAt)
	require.Equal(t, int64(7), audit.GroupID)
	require.Equal(t, 2, audit.Attempts)
	require.Equal(t, GroupRecoveryProbeCostStatusUnavailable, audit.CostStatus)
	require.Nil(t, audit.EstimatedCost)
	require.Empty(t, audit.UpstreamStatusCode)
	require.NotContains(t, audit.SanitizedError, "secret.example")
}

func TestBuildGroupRecoveryProbeAuditUsesEstimatedProbeCost(t *testing.T) {
	startedAt := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	finishedAt := startedAt.Add(2 * time.Second)
	estimatedCost := 0.00042
	job := GroupRecoveryProbeJob{
		State: GroupRecoveryProbeState{GroupID: 7, AccountID: 19, Model: "gpt-5.6-sol"},
	}
	completion := GroupRecoveryProbeCompletion{Status: GroupRecoveryProbeStatusEligible}
	audit := buildGroupRecoveryProbeAudit(job, GroupRecoveryProbeRoundResult{
		Attempts:      1,
		SuccessCount:  1,
		LastLatencyMs: 2000,
		EstimatedCost: &estimatedCost,
		CostStatus:    GroupRecoveryProbeCostStatusEstimated,
	}, completion, startedAt, finishedAt)

	require.Equal(t, GroupRecoveryProbeCostStatusEstimated, audit.CostStatus)
	require.NotNil(t, audit.EstimatedCost)
	require.InDelta(t, estimatedCost, *audit.EstimatedCost, 1e-12)
}
