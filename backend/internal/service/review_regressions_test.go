package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestReviewEmptyIsolationRejectsSelectionWhenAllCandidatesIsolated(t *testing.T) {
	now := time.Now()
	account := Account{ID: 21638, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1}
	state := newOpenAIAccountModelTransientState(128)
	state.recordEmptyStream(account.ID, "gpt-5.6-sol", "real-1", now.Add(-time.Second))
	state.recordEmptyStream(account.ID, "gpt-5.6-sol", "real-2", now)
	require.True(t, state.isBlocked(account.ID, "gpt-5.6-sol", now))
	svc := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{account}}, cfg: &config.Config{}, openaiModelTransient: state}
	selection, err := svc.SelectAccountWithLoadAwareness(context.Background(), nil, "", "gpt-5.6-sol", nil)
	if selection != nil {
		t.Fatalf("30-minute empty isolation bypassed: selected account %d, err=%v", selection.Account.ID, err)
	}
	require.Error(t, err)
}

type reviewCandidateOrderer struct {
	ordering *SmartSchedulerOrdering
	err      error
}

func (o reviewCandidateOrderer) OrderCandidates(context.Context, *Group, string, string, []*Account, time.Time) (*SmartSchedulerOrdering, error) {
	return o.ordering, o.err
}

func TestReviewFailoverEligibilitySurvivesOrderingFallback(t *testing.T) {
	cases := []struct {
		name    string
		enabled bool
		orderer smartSchedulerCandidateOrderer
	}{
		{"scheduler disabled", false, nil},
		{"ordering error", true, reviewCandidateOrderer{err: errors.New("statistics unavailable")}},
		{"ordering inactive", true, reviewCandidateOrderer{ordering: &SmartSchedulerOrdering{Active: false}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			account := deepSeekResponsesTestAccount()
			group := &Group{ID: 54, RecoveryProbeEnabled: true, SmartSchedulerEnabled: tc.enabled}
			ctx := WithOpenAIFailoverSelection(context.Background())
			got, _ := runSmartSchedulerOrdering(ctx, tc.orderer, group, "deepseek-v4-pro", "responses", []*Account{account})
			require.Empty(t, got, "unqualified account must never receive failover even when ordering cannot run")
		})
	}
}

type reviewProtocolRepo struct {
	GroupRecoveryProbeRepository
	accept bool
	saves  int
	audits []GroupRecoveryProbeAudit
}

func (r *reviewProtocolRepo) Complete(context.Context, GroupRecoveryProbeCompletion) (bool, error) {
	return r.accept, nil
}
func (r *reviewProtocolRepo) GetProtocolEligibility(context.Context, int64) (map[string]GroupRecoveryProbeProtocolStatus, error) {
	return nil, nil
}
func (r *reviewProtocolRepo) SaveProtocolEligibility(context.Context, int64, map[string]GroupRecoveryProbeProtocolStatus) error {
	r.saves++
	return nil
}
func (r *reviewProtocolRepo) CreateAudit(_ context.Context, a GroupRecoveryProbeAudit) error {
	r.audits = append(r.audits, a)
	return nil
}
func (r *reviewProtocolRepo) CreateAuditWithID(_ context.Context, a GroupRecoveryProbeAudit) (int64, error) {
	r.audits = append(r.audits, a)
	return 41, nil
}

type reviewBusinessTester struct{ calls int }

func (t *reviewBusinessTester) RunTestBackground(context.Context, int64, string) (*ScheduledTestResult, error) {
	return &ScheduledTestResult{Status: "success"}, nil
}
func (t *reviewBusinessTester) ProbeBusinessProtocols(context.Context, int64, string) (map[string]*ScheduledTestResult, bool, error) {
	t.calls++
	results := make(map[string]*ScheduledTestResult)
	for _, protocol := range []string{APIProtocolChatCompletions, APIProtocolAnthropic, APIProtocolResponses} {
		cost := 0.0001
		results[protocol] = &ScheduledTestResult{Status: "success", BillingModel: "gpt-5.6-sol", UsageTokens: UsageTokens{InputTokens: 100, OutputTokens: 10}, EstimatedCost: &cost, CostStatus: GroupRecoveryProbeCostStatusEstimated}
	}
	return results, true, nil
}
func reviewProbeJob() GroupRecoveryProbeJob {
	return GroupRecoveryProbeJob{
		State:           GroupRecoveryProbeState{ID: 11, GroupID: 7, AccountID: 19, Model: "gpt-5.6-sol"},
		PhysicalStateID: 23, PreviousStatus: GroupRecoveryProbeStatusPending, Mode: GroupRecoveryProbeModeSmart,
		IntervalSeconds: 900, AttemptsPerRound: 1, ClaimedAt: time.Now(),
	}
}

func TestReviewStaleAdaptiveCompletionDoesNotSaveEligibility(t *testing.T) {
	repo := &reviewProtocolRepo{accept: false}
	runner := &GroupRecoveryProbeRunner{repo: repo, accountTestSvc: &reviewBusinessTester{}, randomFloat: func() float64 { return 0.5 }}
	runner.runJob(context.Background(), reviewProbeJob())
	require.Zero(t, repo.saves, "a stale worker rejected by Complete must not change physical or projected eligibility")
	require.Empty(t, repo.audits)
}

func reviewProbeBilling(t *testing.T, dailyBudget float64) (*GroupRecoveryProbeBillingService, *recoveryProbeAtomicSettlementRepoStub) {
	owner := &User{ID: 7, Role: RoleAdmin}
	key := &APIKey{ID: 11, UserID: 7, User: owner}
	account := &Account{ID: 19, Type: AccountTypeAPIKey}
	settings := GroupRecoveryProbeBillingSettings{Enabled: true, OwnerUserID: 7, APIKeyID: 11, DailyBudgetUSD: dailyBudget, PerAttemptLimitUSD: 0.01}
	raw, err := json.Marshal(settings)
	require.NoError(t, err)
	atomic := &recoveryProbeAtomicSettlementRepoStub{applied: make(map[int64]bool)}
	return &GroupRecoveryProbeBillingService{
		settingRepo: &recoveryProbeBillingSettingRepoStub{values: map[string]string{SettingKeyGroupRecoveryProbeBilling: string(raw)}},
		apiKeyRepo:  &recoveryProbeBillingAPIKeyRepoStub{keys: map[int64]*APIKey{11: key}},
		accountRepo: &recoveryProbeBillingAccountRepoStub{accounts: map[int64]*Account{19: account}},
		auditRepo:   &recoveryProbeBillingAuditRepoStub{}, atomicRepo: atomic, billingSvc: NewBillingService(&config.Config{}, nil),
	}, atomic
}

func TestReviewAdaptiveProbeSettlesRecordedUsage(t *testing.T) {
	billing, atomic := reviewProbeBilling(t, 1)
	repo := &reviewProtocolRepo{accept: true}
	tester := &reviewBusinessTester{}
	runner := &GroupRecoveryProbeRunner{repo: repo, accountTestSvc: tester, billing: billing, randomFloat: func() float64 { return 0.5 }}
	runner.runJob(context.Background(), reviewProbeJob())
	require.Len(t, repo.audits, 1)
	require.True(t, hasUsageTokens(repo.audits[0].UsageTokens))
	require.Len(t, atomic.commands, 1, "adaptive probes with billable usage must follow the existing atomic settlement path")
}

func TestReviewAdaptiveBudgetRejectsBeforeThreeProtocolFanout(t *testing.T) {
	billing, _ := reviewProbeBilling(t, 0.02)
	repo := &reviewProtocolRepo{accept: true}
	tester := &reviewBusinessTester{}
	runner := &GroupRecoveryProbeRunner{repo: repo, accountTestSvc: tester, billing: billing, randomFloat: func() float64 { return 0.5 }}
	runner.runJob(context.Background(), reviewProbeJob())
	require.Zero(t, tester.calls, "remaining budget admits two attempts but not the planned three-protocol round")
}

func TestReviewPartialEOFEmitsFailureTerminal(t *testing.T) {
	_, recorder, _, err := forwardDeepSeekSSE(t, strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"already sent\"}\n\n"), "review-partial")
	require.Error(t, err)
	require.Contains(t, recorder.Body.String(), "already sent")
	require.Contains(t, recorder.Body.String(), "response.failed", "committed Responses must terminate with a protocol-correct failure event")
}
