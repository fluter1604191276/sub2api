package service

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

const (
	GroupRecoveryProbeModeManual = "manual"
	GroupRecoveryProbeModeSmart  = "smart"

	GroupRecoveryProbeStatusPending  = "pending"
	GroupRecoveryProbeStatusProbing  = "probing"
	GroupRecoveryProbeStatusWarm     = "warm"
	GroupRecoveryProbeStatusEligible = "eligible"
	GroupRecoveryProbeStatusFailed   = "failed"
	GroupRecoveryProbeStatusPaused   = "paused"

	GroupRecoveryProbeErrorNone      = ""
	GroupRecoveryProbeErrorTransient = "transient"
	GroupRecoveryProbeErrorPermanent = "permanent"

	GroupRecoveryProbeDefaultIntervalSeconds   = 15 * 60
	GroupRecoveryProbeDefaultBackoffCapSeconds = 30 * 60
	GroupRecoveryProbeIdleThresholdSeconds     = 60 * 60
	GroupRecoveryProbeMinIntervalSeconds       = 60
	GroupRecoveryProbeMaxIntervalSeconds       = 24 * 60 * 60
	GroupRecoveryProbeMaxAttemptsPerRound      = 5
	// Healthy accounts only need periodic recovery confirmation. A shorter
	// interval is still used for the first warm-up success.
	GroupRecoveryProbeSmartEligibleMinIntervalSeconds = 60 * 60

	groupRecoveryProbeDefaultWorkers = 4
	groupRecoveryProbeTickInterval   = 30 * time.Second
	groupRecoveryProbeAttemptTimeout = 3 * time.Minute
	groupRecoveryProbeMaxErrorBytes  = 500
)

const GroupRecoveryProbePermanentRetryInterval = 6 * time.Hour

type GroupRecoveryProbeConfig struct {
	Enabled              bool
	Mode                 string
	Model                string
	IntervalSeconds      int
	AttemptsPerRound     int
	IdleThresholdSeconds int
	BackoffCapSeconds    int
}

func NormalizeGroupRecoveryProbeConfig(cfg GroupRecoveryProbeConfig) (GroupRecoveryProbeConfig, error) {
	cfg.Mode = strings.ToLower(strings.TrimSpace(cfg.Mode))
	if cfg.Mode == "" {
		cfg.Mode = GroupRecoveryProbeModeSmart
	}
	if cfg.Mode != GroupRecoveryProbeModeManual && cfg.Mode != GroupRecoveryProbeModeSmart {
		return cfg, fmt.Errorf("recovery_probe_mode must be manual or smart")
	}
	cfg.Model = strings.TrimSpace(cfg.Model)
	if cfg.Enabled && cfg.Model == "" {
		return cfg, fmt.Errorf("recovery_probe_model is required when recovery probes are enabled")
	}
	if cfg.IntervalSeconds == 0 {
		cfg.IntervalSeconds = GroupRecoveryProbeDefaultIntervalSeconds
	}
	if cfg.IntervalSeconds < GroupRecoveryProbeMinIntervalSeconds || cfg.IntervalSeconds > GroupRecoveryProbeMaxIntervalSeconds {
		return cfg, fmt.Errorf("recovery_probe_interval_seconds must be between %d and %d", GroupRecoveryProbeMinIntervalSeconds, GroupRecoveryProbeMaxIntervalSeconds)
	}
	if cfg.AttemptsPerRound == 0 {
		cfg.AttemptsPerRound = 1
	}
	if cfg.AttemptsPerRound < 1 || cfg.AttemptsPerRound > GroupRecoveryProbeMaxAttemptsPerRound {
		return cfg, fmt.Errorf("recovery_probe_attempts_per_round must be between 1 and %d", GroupRecoveryProbeMaxAttemptsPerRound)
	}
	if cfg.IdleThresholdSeconds == 0 {
		cfg.IdleThresholdSeconds = GroupRecoveryProbeIdleThresholdSeconds
	}
	if cfg.IdleThresholdSeconds != GroupRecoveryProbeIdleThresholdSeconds {
		return cfg, fmt.Errorf("recovery_probe_idle_threshold_seconds must be %d", GroupRecoveryProbeIdleThresholdSeconds)
	}
	if cfg.BackoffCapSeconds == 0 {
		cfg.BackoffCapSeconds = GroupRecoveryProbeDefaultBackoffCapSeconds
	}
	if cfg.BackoffCapSeconds < GroupRecoveryProbeMinIntervalSeconds || cfg.BackoffCapSeconds > GroupRecoveryProbeMaxIntervalSeconds {
		return cfg, fmt.Errorf("recovery_probe_backoff_cap_seconds must be between %d and %d", GroupRecoveryProbeMinIntervalSeconds, GroupRecoveryProbeMaxIntervalSeconds)
	}
	return cfg, nil
}

type GroupRecoveryProbeState struct {
	ID                   int64      `json:"-"`
	GroupID              int64      `json:"group_id"`
	AccountID            int64      `json:"account_id"`
	Model                string     `json:"model"`
	Status               string     `json:"status"`
	ConsecutiveSuccesses int        `json:"consecutive_successes"`
	ConsecutiveFailures  int        `json:"consecutive_failures"`
	LastProbeAt          *time.Time `json:"last_probe_at,omitempty"`
	NextProbeAt          *time.Time `json:"next_probe_at,omitempty"`
	LastSuccessAt        *time.Time `json:"last_success_at,omitempty"`
	LastFailureAt        *time.Time `json:"last_failure_at,omitempty"`
	LastErrorClass       string     `json:"last_error_class,omitempty"`
	LastError            string     `json:"last_error,omitempty"`
	LatencyMs            int64      `json:"latency_ms"`
	ProbeCount           int64      `json:"probe_count"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

type GroupRecoveryProbeJob struct {
	State             GroupRecoveryProbeState
	PhysicalStateID   int64
	BeneficiaryGroups int
	PreviousStatus    string
	Mode              string
	IntervalSeconds   int
	AttemptsPerRound  int
	BackoffCapSeconds int
	ClaimedAt         time.Time
}

func groupRecoveryProbeSmartEligibleInterval(job GroupRecoveryProbeJob) time.Duration {
	interval := time.Duration(job.IntervalSeconds) * time.Second
	if interval < time.Minute {
		interval = time.Duration(GroupRecoveryProbeDefaultIntervalSeconds) * time.Second
	}
	if job.Mode == GroupRecoveryProbeModeSmart {
		minimumSeconds := GroupRecoveryProbeSmartEligibleMinIntervalSeconds
		switch {
		case job.State.ConsecutiveSuccesses >= 4:
			minimumSeconds = 4 * 60 * 60
		case job.State.ConsecutiveSuccesses >= 3:
			minimumSeconds = 2 * 60 * 60
		}
		minimum := time.Duration(minimumSeconds) * time.Second
		if interval < minimum {
			interval = minimum
		}
	}
	return interval
}

type GroupRecoveryProbeCompletion struct {
	StateID              int64
	PhysicalStateID      int64
	ClaimedAt            time.Time
	Status               string
	ConsecutiveSuccesses int
	ConsecutiveFailures  int
	LastSuccessAt        *time.Time
	LastFailureAt        *time.Time
	NextProbeAt          time.Time
	LastErrorClass       string
	LastError            string
	LatencyMs            int64
	AttemptCount         int
}

// GroupRecoveryProbeAudit records one completed probe round independently from
// usage_logs. When probe billing is enabled, settlement links the audit to a
// dedicated probe usage row; otherwise its provider cost remains an estimate.
type GroupRecoveryProbeAudit struct {
	ID                 int64       `json:"id"`
	PhysicalStateID    int64       `json:"physical_state_id"`
	BeneficiaryGroups  int         `json:"beneficiary_group_count"`
	GroupID            int64       `json:"group_id"`
	AccountID          int64       `json:"account_id"`
	Model              string      `json:"model"`
	StartedAt          time.Time   `json:"started_at"`
	FinishedAt         time.Time   `json:"finished_at"`
	Status             string      `json:"status"`
	Attempts           int         `json:"attempts"`
	SuccessCount       int         `json:"success_count"`
	FailureCount       int         `json:"failure_count"`
	LatencyMs          int64       `json:"latency_ms"`
	ErrorClass         string      `json:"error_class,omitempty"`
	SanitizedError     string      `json:"sanitized_error,omitempty"`
	UpstreamStatusCode *int        `json:"upstream_status_code,omitempty"`
	EstimatedCost      *float64    `json:"estimated_cost,omitempty"`
	CostStatus         string      `json:"cost_status"`
	UsageTokens        UsageTokens `json:"usage_tokens"`
	SettlementStatus   string      `json:"settlement_status"`
	SettledCost        *float64    `json:"settled_cost,omitempty"`
	UsageLogID         *int64      `json:"usage_log_id,omitempty"`
	BillingUserID      *int64      `json:"billing_user_id,omitempty"`
	BillingAPIKeyID    *int64      `json:"billing_api_key_id,omitempty"`
	SettlementError    string      `json:"settlement_error,omitempty"`
	CreatedAt          time.Time   `json:"created_at"`
}

const GroupRecoveryProbeCostStatusUnavailable = "unavailable"
const GroupRecoveryProbeCostStatusEstimated = "estimated"
const GroupRecoveryProbeCostStatusActual = "actual"

const (
	GroupRecoveryProbeSettlementPending       = "pending"
	GroupRecoveryProbeSettlementSettled       = "settled"
	GroupRecoveryProbeSettlementUnavailable   = "unavailable"
	GroupRecoveryProbeSettlementBudgetBlocked = "budget_blocked"
	GroupRecoveryProbeSettlementFailed        = "failed"
)

type GroupRecoveryProbeRoundResult struct {
	Attempts      int
	SuccessCount  int
	FailureCount  int
	LastError     string
	LastLatencyMs int64
	EstimatedCost *float64
	CostStatus    string
	UsageTokens   UsageTokens
	UsageModel    string
}

func buildGroupRecoveryProbeAudit(job GroupRecoveryProbeJob, result GroupRecoveryProbeRoundResult, completion GroupRecoveryProbeCompletion, startedAt, finishedAt time.Time) GroupRecoveryProbeAudit {
	model := strings.TrimSpace(result.UsageModel)
	if model == "" {
		model = job.State.Model
	}
	return GroupRecoveryProbeAudit{
		PhysicalStateID:   job.PhysicalStateID,
		BeneficiaryGroups: job.BeneficiaryGroups,
		GroupID:           job.State.GroupID,
		AccountID:         job.State.AccountID,
		Model:             model,
		StartedAt:         startedAt,
		FinishedAt:        finishedAt,
		Status:            completion.Status,
		Attempts:          result.Attempts,
		SuccessCount:      result.SuccessCount,
		FailureCount:      result.FailureCount,
		LatencyMs:         result.LastLatencyMs,
		ErrorClass:        completion.LastErrorClass,
		SanitizedError:    sanitizeGroupRecoveryProbeError(result.LastError),
		EstimatedCost:     result.EstimatedCost,
		CostStatus:        groupRecoveryProbeAuditCostStatus(result.CostStatus, result.EstimatedCost),
		UsageTokens:       result.UsageTokens,
		SettlementStatus:  GroupRecoveryProbeSettlementPending,
	}
}

func groupRecoveryProbeAuditCostStatus(status string, estimatedCost *float64) string {
	if estimatedCost != nil && status == GroupRecoveryProbeCostStatusEstimated {
		return GroupRecoveryProbeCostStatusEstimated
	}
	return GroupRecoveryProbeCostStatusUnavailable
}

type GroupRecoveryProbeRepository interface {
	ClaimDue(ctx context.Context, now time.Time, limit int) ([]GroupRecoveryProbeJob, error)
	Complete(ctx context.Context, completion GroupRecoveryProbeCompletion) (bool, error)
	CreateAudit(ctx context.Context, audit GroupRecoveryProbeAudit) error
	ListStates(ctx context.Context, groupID int64, accountIDs []int64, model string) (map[int64]GroupRecoveryProbeState, error)
	ReconcileRealUsage(ctx context.Context, now time.Time) (int64, error)
}

type GroupRecoveryProbeAuditIDWriter interface {
	CreateAuditWithID(ctx context.Context, audit GroupRecoveryProbeAudit) (int64, error)
}

func groupRecoveryProbeSmartBackoff(consecutiveFailures int, capDuration time.Duration) time.Duration {
	if capDuration < time.Minute {
		capDuration = time.Minute
	}
	steps := []time.Duration{time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute, 15 * time.Minute, 30 * time.Minute}
	index := consecutiveFailures - 1
	if index < 0 {
		index = 0
	}
	if index >= len(steps) {
		index = len(steps) - 1
	}
	delay := steps[index]
	if delay > capDuration {
		return capDuration
	}
	return delay
}

func classifyGroupRecoveryProbeError(message string) string {
	message = strings.ToLower(strings.TrimSpace(message))
	if message == "" {
		return GroupRecoveryProbeErrorTransient
	}
	permanentMarkers := []string{
		"401", "403", "unauthorized", "forbidden", "permission denied", "permissiondenied",
		"invalid api key", "api key is missing", "no api key", "no access token",
		"insufficient balance", "insufficient quota", "insufficient_quota", "balance exhausted", "余额不足",
		"model not found", "model_not_found", "does not have access", "model access unavailable",
	}
	for _, marker := range permanentMarkers {
		if strings.Contains(message, marker) {
			return GroupRecoveryProbeErrorPermanent
		}
	}
	return GroupRecoveryProbeErrorTransient
}

func buildGroupRecoveryProbeCompletion(job GroupRecoveryProbeJob, result GroupRecoveryProbeRoundResult, now time.Time) GroupRecoveryProbeCompletion {
	completion := GroupRecoveryProbeCompletion{
		StateID:         job.State.ID,
		PhysicalStateID: job.PhysicalStateID,
		ClaimedAt:       job.ClaimedAt,
		LatencyMs:       result.LastLatencyMs,
		AttemptCount:    result.Attempts,
		LastError:       sanitizeGroupRecoveryProbeError(result.LastError),
	}
	interval := time.Duration(job.IntervalSeconds) * time.Second
	if interval < time.Minute {
		interval = time.Duration(GroupRecoveryProbeDefaultIntervalSeconds) * time.Second
	}

	if result.SuccessCount == result.Attempts && result.Attempts > 0 {
		successes := job.State.ConsecutiveSuccesses + result.SuccessCount
		completion.ConsecutiveSuccesses = successes
		completion.ConsecutiveFailures = 0
		completion.LastSuccessAt = timePointer(now)
		completion.LastError = ""
		completion.LastErrorClass = GroupRecoveryProbeErrorNone
		if successes >= 2 {
			completion.Status = GroupRecoveryProbeStatusEligible
			if job.Mode == GroupRecoveryProbeModeSmart {
				intervalJob := job
				intervalJob.State.ConsecutiveSuccesses = successes
				completion.NextProbeAt = now.Add(groupRecoveryProbeSmartEligibleInterval(intervalJob))
			} else {
				completion.NextProbeAt = now.Add(interval)
			}
		} else {
			completion.Status = GroupRecoveryProbeStatusWarm
			if job.Mode == GroupRecoveryProbeModeSmart {
				completion.NextProbeAt = now.Add(5 * time.Minute)
			} else {
				completion.NextProbeAt = now.Add(interval)
			}
		}
		return completion
	}

	if result.SuccessCount > 0 {
		completion.Status = GroupRecoveryProbeStatusWarm
		completion.ConsecutiveSuccesses = 1
		completion.ConsecutiveFailures = 0
		completion.LastSuccessAt = timePointer(now)
		completion.LastErrorClass = classifyGroupRecoveryProbeError(completion.LastError)
		if job.Mode == GroupRecoveryProbeModeSmart {
			completion.NextProbeAt = now.Add(5 * time.Minute)
		} else {
			completion.NextProbeAt = now.Add(interval)
		}
		return completion
	}

	failures := job.State.ConsecutiveFailures + 1
	completion.Status = GroupRecoveryProbeStatusFailed
	completion.ConsecutiveSuccesses = 0
	completion.ConsecutiveFailures = failures
	completion.LastFailureAt = timePointer(now)
	completion.LastErrorClass = classifyGroupRecoveryProbeError(completion.LastError)
	if job.Mode == GroupRecoveryProbeModeSmart {
		if completion.LastErrorClass == GroupRecoveryProbeErrorPermanent {
			completion.Status = GroupRecoveryProbeStatusPaused
			completion.NextProbeAt = now.Add(GroupRecoveryProbePermanentRetryInterval)
		} else {
			capDuration := time.Duration(job.BackoffCapSeconds) * time.Second
			completion.NextProbeAt = now.Add(groupRecoveryProbeSmartBackoff(failures, capDuration))
		}
	} else {
		completion.NextProbeAt = now.Add(interval)
	}
	return completion
}

func sanitizeGroupRecoveryProbeError(message string) string {
	message = sanitizeClientVisibleUpstreamErrorMessage(strings.TrimSpace(message))
	if len(message) > groupRecoveryProbeMaxErrorBytes {
		message = message[:groupRecoveryProbeMaxErrorBytes]
	}
	return message
}

func timePointer(value time.Time) *time.Time {
	return &value
}

type GroupRecoveryProbeRunner struct {
	repo            GroupRecoveryProbeRepository
	accountTestSvc  groupRecoveryProbeAccountTester
	billing         *GroupRecoveryProbeBillingService
	schedulerCaches []*SmartSchedulerPreviewService
	workers         int
	tickInterval    time.Duration
	randomFloat     func() float64

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}
}

type groupRecoveryProbeAccountTester interface {
	RunTestBackground(ctx context.Context, accountID int64, modelID string) (*ScheduledTestResult, error)
}

func NewGroupRecoveryProbeRunner(repo GroupRecoveryProbeRepository, accountTestSvc *AccountTestService) *GroupRecoveryProbeRunner {
	return &GroupRecoveryProbeRunner{
		repo:           repo,
		accountTestSvc: accountTestSvc,
		workers:        groupRecoveryProbeDefaultWorkers,
		tickInterval:   groupRecoveryProbeTickInterval,
		randomFloat:    rand.Float64,
		stopCh:         make(chan struct{}),
		doneCh:         make(chan struct{}),
	}
}

func (r *GroupRecoveryProbeRunner) SetBillingService(billing *GroupRecoveryProbeBillingService) {
	if r != nil {
		r.billing = billing
	}
}

func (r *GroupRecoveryProbeRunner) SetSchedulerCaches(caches ...*SmartSchedulerPreviewService) {
	if r == nil {
		return
	}
	r.schedulerCaches = append([]*SmartSchedulerPreviewService(nil), caches...)
}

func (r *GroupRecoveryProbeRunner) invalidateSchedulerOrderingCaches() {
	if r == nil {
		return
	}
	for _, cache := range r.schedulerCaches {
		cache.InvalidateOrderingCache()
	}
}

func (r *GroupRecoveryProbeRunner) Start() {
	if r == nil || r.repo == nil || r.accountTestSvc == nil {
		return
	}
	r.startOnce.Do(func() {
		go r.loop()
		logger.LegacyPrintf("service.group_recovery_probe", "[GroupRecoveryProbe] started (tick=%s workers=%d)", r.tickInterval, r.workers)
	})
}

func (r *GroupRecoveryProbeRunner) Stop() {
	if r == nil {
		return
	}
	r.stopOnce.Do(func() {
		close(r.stopCh)
		select {
		case <-r.doneCh:
		case <-time.After(5 * time.Second):
			logger.LegacyPrintf("service.group_recovery_probe", "[GroupRecoveryProbe] stop timed out")
		}
	})
}

func (r *GroupRecoveryProbeRunner) loop() {
	defer close(r.doneCh)
	ticker := time.NewTicker(r.tickInterval)
	defer ticker.Stop()
	r.runOnce()
	for {
		select {
		case <-ticker.C:
			r.runOnce()
		case <-r.stopCh:
			return
		}
	}
}

func (r *GroupRecoveryProbeRunner) runOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	now := time.Now()
	if reconciled, err := r.repo.ReconcileRealUsage(ctx, now); err != nil {
		logger.LegacyPrintf("service.group_recovery_probe", "[GroupRecoveryProbe] real-usage reconciliation failed: %v", err)
	} else if reconciled > 0 {
		r.invalidateSchedulerOrderingCaches()
	}
	workerCount := normalizedGroupRecoveryProbeWorkerCount(r.workers)
	jobs, err := r.repo.ClaimDue(ctx, now, workerCount)
	if err != nil {
		logger.LegacyPrintf("service.group_recovery_probe", "[GroupRecoveryProbe] claim failed: %v", err)
		return
	}
	if len(jobs) == 0 {
		return
	}
	// ClaimDue changes the selected accounts to probing. Drop any ordering that
	// may still classify those accounts as eligible before the probes run.
	r.invalidateSchedulerOrderingCaches()
	sem := make(chan struct{}, workerCount)
	var wg sync.WaitGroup
	for i := range jobs {
		job := jobs[i]
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			r.runJob(ctx, job)
		}()
	}
	wg.Wait()
}

func normalizedGroupRecoveryProbeWorkerCount(configured int) int {
	if configured < 1 {
		return groupRecoveryProbeDefaultWorkers
	}
	return configured
}

func (r *GroupRecoveryProbeRunner) runJob(ctx context.Context, job GroupRecoveryProbeJob) {
	reservation, reserveErr := r.reserveProbeBudget(ctx, job)
	if reserveErr != nil {
		r.finishReservationFailedJob(ctx, job, reserveErr)
		return
	}
	if reservation != nil {
		defer reservation.Release()
	}
	startedAt := time.Now()
	result := GroupRecoveryProbeRoundResult{}
	attempts := job.AttemptsPerRound
	if attempts < 1 {
		attempts = 1
	}
	for i := 0; i < attempts; i++ {
		attemptCtx, cancel := context.WithTimeout(ctx, groupRecoveryProbeAttemptTimeout)
		testResult, err := r.accountTestSvc.RunTestBackground(attemptCtx, job.State.AccountID, job.State.Model)
		cancel()
		result.Attempts++
		if err == nil && testResult != nil && testResult.Status == "success" {
			result.SuccessCount++
			result.LastLatencyMs = testResult.LatencyMs
			accumulateGroupRecoveryProbeUsage(&result, testResult)
			continue
		}
		accumulateGroupRecoveryProbeUsage(&result, testResult)
		result.FailureCount++
		if testResult != nil {
			result.LastLatencyMs = testResult.LatencyMs
			result.LastError = testResult.ErrorMessage
		}
		if result.LastError == "" && err != nil {
			result.LastError = err.Error()
		}
		if classifyGroupRecoveryProbeError(result.LastError) == GroupRecoveryProbeErrorPermanent {
			break
		}
	}
	completedAt := time.Now()
	completion := buildGroupRecoveryProbeCompletion(job, result, completedAt)
	if job.Mode == GroupRecoveryProbeModeSmart && completion.Status != GroupRecoveryProbeStatusPaused {
		completion.NextProbeAt = addGroupRecoveryProbeJitter(completedAt, completion.NextProbeAt, r.randomFloat)
	}
	accepted, err := r.repo.Complete(ctx, completion)
	if err != nil {
		logger.LegacyPrintf("service.group_recovery_probe", "[GroupRecoveryProbe] completion failed: state=%d account=%d err=%v", job.State.ID, job.State.AccountID, err)
		return
	}
	if !accepted {
		logger.LegacyPrintf("service.group_recovery_probe", "[GroupRecoveryProbe] stale completion ignored: state=%d account=%d", job.State.ID, job.State.AccountID)
		return
	}
	r.invalidateSchedulerOrderingCaches()
	finishedAt := time.Now()
	audit := buildGroupRecoveryProbeAudit(job, result, completion, startedAt, finishedAt)
	auditID, err := r.createAudit(ctx, audit)
	if err != nil {
		// Audit persistence must not turn a completed probe into a failed probe.
		logger.LegacyPrintf("service.group_recovery_probe", "[GroupRecoveryProbe] audit write failed: group=%d account=%d err=%v", job.State.GroupID, job.State.AccountID, err)
		return
	}
	if r.billing != nil && auditID > 0 {
		audit.ID = auditID
		if err := r.billing.Settle(ctx, audit, reservation); err != nil {
			logger.LegacyPrintf("service.group_recovery_probe", "[GroupRecoveryProbe] settlement failed: audit=%d group=%d account=%d err=%v", auditID, job.State.GroupID, job.State.AccountID, err)
		}
	}
}

func accumulateGroupRecoveryProbeUsage(result *GroupRecoveryProbeRoundResult, testResult *ScheduledTestResult) {
	if result == nil || testResult == nil {
		return
	}
	result.UsageTokens.InputTokens += testResult.UsageTokens.InputTokens
	result.UsageTokens.OutputTokens += testResult.UsageTokens.OutputTokens
	result.UsageTokens.CacheCreationTokens += testResult.UsageTokens.CacheCreationTokens
	result.UsageTokens.CacheReadTokens += testResult.UsageTokens.CacheReadTokens
	if strings.TrimSpace(testResult.BillingModel) != "" {
		result.UsageModel = strings.TrimSpace(testResult.BillingModel)
	}
	if testResult.EstimatedCost == nil || testResult.CostStatus != GroupRecoveryProbeCostStatusEstimated {
		return
	}
	total := *testResult.EstimatedCost
	if result.EstimatedCost != nil {
		total += *result.EstimatedCost
	}
	result.EstimatedCost = &total
	result.CostStatus = GroupRecoveryProbeCostStatusEstimated
}

func (r *GroupRecoveryProbeRunner) createAudit(ctx context.Context, audit GroupRecoveryProbeAudit) (int64, error) {
	if writer, ok := r.repo.(GroupRecoveryProbeAuditIDWriter); ok {
		return writer.CreateAuditWithID(ctx, audit)
	}
	return 0, r.repo.CreateAudit(ctx, audit)
}

func (r *GroupRecoveryProbeRunner) reserveProbeBudget(ctx context.Context, job GroupRecoveryProbeJob) (*GroupRecoveryProbeBillingReservation, error) {
	if r == nil || r.billing == nil {
		return nil, nil
	}
	return r.billing.Reserve(ctx, job.State.GroupID, job.AttemptsPerRound)
}

func buildGroupRecoveryProbeReservationFailure(job GroupRecoveryProbeJob, reserveErr error, now time.Time) (GroupRecoveryProbeCompletion, string) {
	status := strings.TrimSpace(job.PreviousStatus)
	switch status {
	case GroupRecoveryProbeStatusPending, GroupRecoveryProbeStatusWarm, GroupRecoveryProbeStatusEligible,
		GroupRecoveryProbeStatusFailed, GroupRecoveryProbeStatusPaused:
	default:
		status = GroupRecoveryProbeStatusPending
	}
	completion := GroupRecoveryProbeCompletion{
		StateID:              job.State.ID,
		PhysicalStateID:      job.PhysicalStateID,
		ClaimedAt:            job.ClaimedAt,
		Status:               status,
		ConsecutiveSuccesses: job.State.ConsecutiveSuccesses,
		ConsecutiveFailures:  job.State.ConsecutiveFailures,
		NextProbeAt:          now.Add(time.Minute),
		LastErrorClass:       GroupRecoveryProbeErrorTransient,
		LastError:            sanitizeGroupRecoveryProbeError(reserveErr.Error()),
		LatencyMs:            job.State.LatencyMs,
	}
	settlementStatus := GroupRecoveryProbeSettlementFailed
	if errors.Is(reserveErr, ErrGroupRecoveryProbeBudgetExceeded) {
		completion.NextProbeAt = now.Add(time.Hour)
		completion.LastErrorClass = job.State.LastErrorClass
		completion.LastError = job.State.LastError
		settlementStatus = GroupRecoveryProbeSettlementBudgetBlocked
	}
	return completion, settlementStatus
}

func (r *GroupRecoveryProbeRunner) finishReservationFailedJob(ctx context.Context, job GroupRecoveryProbeJob, reserveErr error) {
	now := time.Now()
	result := GroupRecoveryProbeRoundResult{LastError: reserveErr.Error(), CostStatus: GroupRecoveryProbeCostStatusUnavailable}
	completion, settlementStatus := buildGroupRecoveryProbeReservationFailure(job, reserveErr, now)
	accepted, err := r.repo.Complete(ctx, completion)
	if err != nil {
		logger.LegacyPrintf("service.group_recovery_probe", "[GroupRecoveryProbe] reservation-failure completion failed: state=%d err=%v", job.State.ID, err)
		return
	}
	if !accepted {
		logger.LegacyPrintf("service.group_recovery_probe", "[GroupRecoveryProbe] stale reservation failure ignored: state=%d account=%d", job.State.ID, job.State.AccountID)
		return
	}
	r.invalidateSchedulerOrderingCaches()
	audit := buildGroupRecoveryProbeAudit(job, result, completion, now, time.Now())
	audit.SettlementStatus = settlementStatus
	if _, err := r.createAudit(ctx, audit); err != nil {
		logger.LegacyPrintf("service.group_recovery_probe", "[GroupRecoveryProbe] reservation-failure audit failed: group=%d account=%d err=%v", job.State.GroupID, job.State.AccountID, err)
	}
}

func addGroupRecoveryProbeJitter(now, scheduled time.Time, randomFloat func() float64) time.Time {
	delay := scheduled.Sub(now)
	if delay <= 0 || randomFloat == nil {
		return scheduled
	}
	// Smart probes spread over a bounded +/-10% window to avoid bursty rounds.
	jitter := (randomFloat()*0.2 - 0.1) * float64(delay)
	adjusted := scheduled.Add(time.Duration(jitter))
	if adjusted.Before(now.Add(time.Second)) {
		return now.Add(time.Second)
	}
	return adjusted
}

func applyGroupRecoveryProbeStateToSchedulerItem(item *SmartSchedulerPreviewItem, state *GroupRecoveryProbeState) {
	if item == nil || state == nil {
		return
	}
	summary := *state
	item.RecoveryProbe = &summary
	switch summary.Status {
	case GroupRecoveryProbeStatusFailed, GroupRecoveryProbeStatusPaused, GroupRecoveryProbeStatusProbing:
		item.Pool = "isolated"
		item.Decision = "recovery_probe_failed"
		item.Reason = "恢复探针尚未确认账号可用"
		item.SoftIsolation = false
	case GroupRecoveryProbeStatusWarm:
		if item.Pool == "primary" {
			item.Pool = "warm"
		}
		item.Decision = "recovery_probe_warm"
		item.Reason = "恢复探针首次成功，等待再次验证或真实流量"
	case GroupRecoveryProbeStatusEligible:
		if item.Pool == "warm" && !smartSchedulerHasRealQualityEvidence(*item) && summary.ConsecutiveSuccesses >= 2 {
			item.ProbeBootstrap = true
			item.Decision = "recovery_probe_bootstrap"
			item.Reason = "恢复探针连续成功，进入受控真实流量预热"
		}
	}
}

func smartSchedulerHasRealQualityEvidence(item SmartSchedulerPreviewItem) bool {
	return item.Quality1h.Last10.QualityScore != nil ||
		item.Quality1h.Last100.QualityScore != nil ||
		item.Quality24h.Last10.QualityScore != nil ||
		item.Quality24h.Last100.QualityScore != nil
}
