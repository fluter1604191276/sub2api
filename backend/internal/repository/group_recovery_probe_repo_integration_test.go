//go:build integration

package repository

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// These calls deliberately execute the repository SQL against PostgreSQL. The
// unit tests only validate query shape; this catches untyped timestamp
// parameters being inferred as intervals by PostgreSQL.
func TestGroupRecoveryProbeRepositoryExecutesTimestampQueries(t *testing.T) {
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	groupName := fmt.Sprintf("recovery-probe-sql-%d", suffix)
	accountName := fmt.Sprintf("recovery-probe-account-%d", suffix)
	userEmail := fmt.Sprintf("recovery-probe-user-%d@example.com", suffix)
	apiKeyValue := fmt.Sprintf("sk-recovery-probe-%d", suffix)

	var groupID, siblingGroupID, accountID, userID, apiKeyID int64
	err := integrationDB.QueryRowContext(ctx, `
		INSERT INTO groups (
			name, platform, status, recovery_probe_enabled, recovery_probe_mode,
			recovery_probe_model, recovery_probe_interval_seconds,
			recovery_probe_attempts_per_round, recovery_probe_idle_threshold_seconds,
			recovery_probe_backoff_cap_seconds
		) VALUES ($1, 'openai', 'active', TRUE, 'smart', 'gpt-5.6-sol', 900, 1, 3600, 1800)
		RETURNING id
	`, groupName).Scan(&groupID)
	require.NoError(t, err)
	err = integrationDB.QueryRowContext(ctx, `
		INSERT INTO groups (
			name, platform, status, recovery_probe_enabled, recovery_probe_mode,
			recovery_probe_model, recovery_probe_interval_seconds,
			recovery_probe_attempts_per_round, recovery_probe_idle_threshold_seconds,
			recovery_probe_backoff_cap_seconds
		) VALUES ($1, 'openai', 'active', TRUE, 'manual', 'GPT-5.6-SOL', 7200, 2, 3600, 7200)
		RETURNING id
	`, groupName+"-sibling").Scan(&siblingGroupID)
	require.NoError(t, err)

	err = integrationDB.QueryRowContext(ctx, `
		INSERT INTO accounts (name, platform, type, status, schedulable, credentials, extra)
		VALUES ($1, 'openai', 'api_key', 'active', TRUE, '{}'::jsonb, '{}'::jsonb)
		RETURNING id
	`, accountName).Scan(&accountID)
	require.NoError(t, err)

	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM usage_logs WHERE api_key_id = $1", apiKeyID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM api_keys WHERE id = $1", apiKeyID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM users WHERE id = $1", userID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM group_recovery_probe_states WHERE group_id = ANY($1)", pq.Array([]int64{groupID, siblingGroupID}))
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM group_recovery_probe_audits WHERE account_id = $1", accountID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM group_recovery_probe_physical_states WHERE account_id = $1", accountID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM account_groups WHERE group_id = ANY($1)", pq.Array([]int64{groupID, siblingGroupID}))
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM scheduler_outbox WHERE group_id = ANY($1) OR account_id = $2", pq.Array([]int64{groupID, siblingGroupID}), accountID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM accounts WHERE id = $1", accountID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM groups WHERE id = ANY($1)", pq.Array([]int64{groupID, siblingGroupID}))
	})

	_, err = integrationDB.ExecContext(ctx, "INSERT INTO account_groups (account_id, group_id, priority) VALUES ($1, $2, 1)", accountID, groupID)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, "INSERT INTO account_groups (account_id, group_id, priority) VALUES ($1, $2, 1)", accountID, siblingGroupID)
	require.NoError(t, err)

	err = integrationDB.QueryRowContext(ctx, `
		INSERT INTO users (email, password_hash)
		VALUES ($1, 'test')
		RETURNING id
	`, userEmail).Scan(&userID)
	require.NoError(t, err)
	err = integrationDB.QueryRowContext(ctx, `
		INSERT INTO api_keys (user_id, key, name, group_id)
		VALUES ($1, $2, 'recovery probe test', $3)
		RETURNING id
	`, userID, apiKeyValue, groupID).Scan(&apiKeyID)
	require.NoError(t, err)

	repo := NewGroupRecoveryProbeRepository(integrationDB)
	probeNow := time.Now().UTC().Truncate(time.Microsecond)
	_, err = integrationDB.ExecContext(ctx, `
		INSERT INTO usage_logs (
			user_id, api_key_id, account_id, group_id, request_id, model,
			actual_cost, stream, request_type, created_at
		) VALUES ($1, $2, $3, $4, $5, 'gpt-5.6-sol', 0.01, TRUE, $6, $7)
	`, userID, apiKeyID, accountID, groupID, fmt.Sprintf("recovery-probe-ledger-%d", suffix), service.RequestTypeProbe, probeNow.Add(-time.Minute))
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `
		INSERT INTO usage_logs (
			user_id, api_key_id, account_id, group_id, request_id, model,
			actual_cost, stream, request_type, created_at
		) VALUES ($1, $2, $3, $4, $5, 'gpt-5.6-sol', 0, TRUE, 0, $6)
	`, userID, apiKeyID, accountID, groupID, fmt.Sprintf("recovery-zero-cost-%d", suffix), probeNow.Add(-30*time.Second))
	require.NoError(t, err)

	// Probe billing rows and zero-cost placeholders are not successful real
	// activity. Neither may prevent an idle account from being seeded. Even
	// though the account belongs to two enabled groups, only one physical
	// account/model probe may be claimed.
	type claimResult struct {
		jobs []service.GroupRecoveryProbeJob
		err  error
	}
	claims := make(chan claimResult, 2)
	var claimWG sync.WaitGroup
	for i := 0; i < 2; i++ {
		claimWG.Add(1)
		go func() {
			defer claimWG.Done()
			claimed, claimErr := repo.ClaimDue(ctx, probeNow, 4)
			claims <- claimResult{jobs: claimed, err: claimErr}
		}()
	}
	claimWG.Wait()
	close(claims)
	var jobs []service.GroupRecoveryProbeJob
	for result := range claims {
		require.NoError(t, result.err)
		jobs = append(jobs, result.jobs...)
	}
	require.Len(t, jobs, 1)
	require.Equal(t, groupID, jobs[0].State.GroupID)
	require.Equal(t, accountID, jobs[0].State.AccountID)
	require.Equal(t, "probing", jobs[0].State.Status)
	require.Positive(t, jobs[0].PhysicalStateID)
	require.Equal(t, 2, jobs[0].BeneficiaryGroups)
	require.Equal(t, service.GroupRecoveryProbeModeSmart, jobs[0].Mode, "the earliest due projection owns the physical probe policy")
	require.Equal(t, 1, jobs[0].AttemptsPerRound)
	var physicalStates int
	err = integrationDB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM group_recovery_probe_physical_states
		WHERE account_id = $1 AND model_key = 'gpt-5.6-sol'
	`, accountID).Scan(&physicalStates)
	require.NoError(t, err)
	require.Equal(t, 1, physicalStates, "case-variant group models must share one physical state")

	// Simulate a stale worker that previously claimed the sibling projection.
	// The newer physical result must heal it instead of leaving another probe
	// eligible to run immediately after this one completes.
	_, err = integrationDB.ExecContext(ctx, `
		UPDATE group_recovery_probe_states
		SET status = 'probing', last_probe_at = $3, updated_at = $3
		WHERE group_id = $1 AND account_id = $2
	`, siblingGroupID, accountID, jobs[0].ClaimedAt.Add(-30*time.Minute))
	require.NoError(t, err)

	failedAt := time.Now().UTC()
	nextProbeAt := failedAt.Add(time.Hour)
	accepted, err := repo.Complete(ctx, service.GroupRecoveryProbeCompletion{
		StateID:             jobs[0].State.ID,
		PhysicalStateID:     jobs[0].PhysicalStateID,
		ClaimedAt:           jobs[0].ClaimedAt,
		Status:              service.GroupRecoveryProbeStatusFailed,
		ConsecutiveFailures: 1,
		LastFailureAt:       &failedAt,
		NextProbeAt:         nextProbeAt,
		LastErrorClass:      service.GroupRecoveryProbeErrorTransient,
		LastError:           "timeout",
		AttemptCount:        1,
	})
	require.NoError(t, err)
	require.True(t, accepted)
	var failedStates int
	err = integrationDB.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM group_recovery_probe_states
		WHERE account_id = $1 AND LOWER(BTRIM(model)) = 'gpt-5.6-sol' AND status = 'failed'
	`, accountID).Scan(&failedStates)
	require.NoError(t, err)
	require.Equal(t, 2, failedStates, "one physical result should fan out to both group states")
	var smartNext, manualNext, physicalNext time.Time
	err = integrationDB.QueryRowContext(ctx, `
		SELECT
			MIN(next_probe_at) FILTER (WHERE group_id = $2),
			MIN(next_probe_at) FILTER (WHERE group_id = $3)
		FROM group_recovery_probe_states
		WHERE account_id = $1
	`, accountID, groupID, siblingGroupID).Scan(&smartNext, &manualNext)
	require.NoError(t, err)
	require.True(t, smartNext.Before(manualNext), "smart transient failure should retry before the manual projection")
	err = integrationDB.QueryRowContext(ctx, `
		SELECT next_probe_at FROM group_recovery_probe_physical_states WHERE id = $1
	`, jobs[0].PhysicalStateID).Scan(&physicalNext)
	require.NoError(t, err)
	require.WithinDuration(t, smartNext, physicalNext, time.Microsecond, "physical due time must follow the earliest projection")

	// A worker that still holds the older claim token must not overwrite a
	// newer physical claim for the same account/model.
	newerClaimedAt := jobs[0].ClaimedAt.Add(30 * time.Minute)
	_, err = integrationDB.ExecContext(ctx, `
		UPDATE group_recovery_probe_physical_states
		SET status = 'probing', last_probe_at = $2
		WHERE id = $1
	`, jobs[0].PhysicalStateID, newerClaimedAt)
	require.NoError(t, err)
	accepted, err = repo.Complete(ctx, service.GroupRecoveryProbeCompletion{
		StateID:              jobs[0].State.ID,
		PhysicalStateID:      jobs[0].PhysicalStateID,
		ClaimedAt:            jobs[0].ClaimedAt,
		Status:               service.GroupRecoveryProbeStatusEligible,
		ConsecutiveSuccesses: 99,
		NextProbeAt:          failedAt.Add(24 * time.Hour),
	})
	require.NoError(t, err)
	require.False(t, accepted)
	var physicalStatus string
	var physicalSuccesses int
	err = integrationDB.QueryRowContext(ctx, `
		SELECT status, consecutive_successes
		FROM group_recovery_probe_physical_states WHERE id = $1
	`, jobs[0].PhysicalStateID).Scan(&physicalStatus, &physicalSuccesses)
	require.NoError(t, err)
	require.Equal(t, service.GroupRecoveryProbeStatusProbing, physicalStatus)
	require.NotEqual(t, 99, physicalSuccesses)
	_, err = integrationDB.ExecContext(ctx, `
		UPDATE group_recovery_probe_physical_states
		SET status = 'failed', last_probe_at = $2
		WHERE id = $1
	`, jobs[0].PhysicalStateID, jobs[0].ClaimedAt)
	require.NoError(t, err)

	auditID, err := repo.CreateAuditWithID(ctx, service.GroupRecoveryProbeAudit{
		PhysicalStateID:   jobs[0].PhysicalStateID,
		BeneficiaryGroups: jobs[0].BeneficiaryGroups,
		GroupID:           jobs[0].State.GroupID,
		AccountID:         accountID,
		Model:             jobs[0].State.Model,
		StartedAt:         failedAt.Add(-time.Second),
		FinishedAt:        failedAt,
		Status:            service.GroupRecoveryProbeStatusFailed,
		Attempts:          1,
		FailureCount:      1,
		CostStatus:        service.GroupRecoveryProbeCostStatusUnavailable,
		SettlementStatus:  service.GroupRecoveryProbeSettlementUnavailable,
	})
	require.NoError(t, err)
	require.Positive(t, auditID)
	var ownerGroupID int64
	var beneficiaryCount int
	err = integrationDB.QueryRowContext(ctx, `
		SELECT group_id, beneficiary_group_count
		FROM group_recovery_probe_audits WHERE id = $1
	`, auditID).Scan(&ownerGroupID, &beneficiaryCount)
	require.NoError(t, err)
	require.Equal(t, groupID, ownerGroupID)
	require.Equal(t, 2, beneficiaryCount)

	// A probe ledger row after the failed probe must not masquerade as real
	// recovery traffic.
	usageAt := time.Now().UTC().Add(time.Millisecond)
	_, err = integrationDB.ExecContext(ctx, `
		INSERT INTO usage_logs (
			user_id, api_key_id, account_id, group_id, request_id, model,
			actual_cost, stream, request_type, created_at
		) VALUES ($1, $2, $3, $4, $5, 'gpt-5.6-sol', 0.01, TRUE, $6, $7)
	`, userID, apiKeyID, accountID, groupID, fmt.Sprintf("recovery-probe-after-failure-%d", suffix), service.RequestTypeProbe, usageAt)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `
		INSERT INTO usage_logs (
			user_id, api_key_id, account_id, group_id, request_id, model,
			actual_cost, stream, request_type, created_at
		) VALUES ($1, $2, $3, $4, $5, 'gpt-5.6-sol', 0, TRUE, 0, $6)
	`, userID, apiKeyID, accountID, groupID, fmt.Sprintf("recovery-zero-cost-after-failure-%d", suffix), usageAt.Add(time.Millisecond))
	require.NoError(t, err)

	count, err := repo.ReconcileRealUsage(ctx, usageAt.Add(time.Second))
	require.NoError(t, err)
	require.Zero(t, count)
	var status string
	err = integrationDB.QueryRowContext(ctx, `
		SELECT status FROM group_recovery_probe_states WHERE id = $1
	`, jobs[0].State.ID).Scan(&status)
	require.NoError(t, err)
	require.Equal(t, service.GroupRecoveryProbeStatusFailed, status)

	// A real request in the sibling group after the failed probe must make both
	// group projections eligible because account/model health is global.
	realUsageAt := usageAt.Add(2 * time.Second)
	_, err = integrationDB.ExecContext(ctx, `
		INSERT INTO usage_logs (
			user_id, api_key_id, account_id, group_id, request_id, model, requested_model,
			actual_cost, stream, created_at
		) VALUES ($1, $2, $3, $4, $5, 'wrong-model', '  GPT-5.6-SOL  ', 0.01, TRUE, $6)
	`, userID, apiKeyID, accountID, siblingGroupID, fmt.Sprintf("recovery-real-%d", suffix), realUsageAt)
	require.NoError(t, err)
	// Reconciliation must overwrite legacy projection-only counters and timing
	// fields from the physical account/model state, while keeping each group's
	// own next-probe policy.
	_, err = integrationDB.ExecContext(ctx, `
		UPDATE group_recovery_probe_states
		SET status = 'paused',
			consecutive_successes = 77,
			consecutive_failures = 77,
			last_probe_at = $3,
			last_success_at = $3,
			last_failure_at = NULL,
			last_error_class = 'permanent',
			last_error = 'stale projection',
			latency_ms = 9876,
			probe_count = 99,
			updated_at = $3
		WHERE group_id = $1 AND account_id = $2
	`, siblingGroupID, accountID, realUsageAt.Add(-24*time.Hour))
	require.NoError(t, err)

	reconciledAt := realUsageAt.Add(time.Second)
	count, err = repo.ReconcileRealUsage(ctx, reconciledAt)
	require.NoError(t, err)
	require.Equal(t, int64(2), count)
	err = integrationDB.QueryRowContext(ctx, `
		SELECT MIN(status) FROM group_recovery_probe_states WHERE account_id = $1
	`, accountID).Scan(&status)
	require.NoError(t, err)
	require.Equal(t, service.GroupRecoveryProbeStatusEligible, status)
	err = integrationDB.QueryRowContext(ctx, `
		SELECT
			MIN(next_probe_at) FILTER (WHERE group_id = $2),
			MIN(next_probe_at) FILTER (WHERE group_id = $3)
		FROM group_recovery_probe_states
		WHERE account_id = $1
	`, accountID, groupID, siblingGroupID).Scan(&smartNext, &manualNext)
	require.NoError(t, err)
	require.True(t, smartNext.Before(manualNext), "real traffic should preserve each projection's own eligible interval")
	require.WithinDuration(t, realUsageAt.Add(time.Hour), smartNext, time.Microsecond,
		"real traffic should defer the smart probe from the request time, not the reconciliation tick")
	require.WithinDuration(t, realUsageAt.Add(2*time.Hour), manualNext, time.Microsecond,
		"real traffic should defer the manual probe from the request time, not the reconciliation tick")
	var physicalLastSuccess, physicalUpdated time.Time
	err = integrationDB.QueryRowContext(ctx, `
		SELECT next_probe_at, last_success_at, updated_at
		FROM group_recovery_probe_physical_states WHERE id = $1
	`, jobs[0].PhysicalStateID).Scan(&physicalNext, &physicalLastSuccess, &physicalUpdated)
	require.NoError(t, err)
	require.WithinDuration(t, smartNext, physicalNext, time.Microsecond)
	require.WithinDuration(t, realUsageAt, physicalLastSuccess, time.Microsecond,
		"the consumed usage timestamp is the reconciliation watermark")
	var projectionMismatchCount int
	err = integrationDB.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM group_recovery_probe_states s
		JOIN group_recovery_probe_physical_states p ON p.id = s.physical_state_id
		WHERE p.id = $1 AND (
			s.status IS DISTINCT FROM p.status
			OR s.consecutive_successes IS DISTINCT FROM p.consecutive_successes
			OR s.consecutive_failures IS DISTINCT FROM p.consecutive_failures
			OR s.last_probe_at IS DISTINCT FROM p.last_probe_at
			OR s.last_success_at IS DISTINCT FROM p.last_success_at
			OR s.last_failure_at IS DISTINCT FROM p.last_failure_at
			OR s.last_error_class IS DISTINCT FROM p.last_error_class
			OR s.last_error IS DISTINCT FROM p.last_error
			OR s.latency_ms IS DISTINCT FROM p.latency_ms
			OR s.probe_count IS DISTINCT FROM p.probe_count
		)
	`, jobs[0].PhysicalStateID).Scan(&projectionMismatchCount)
	require.NoError(t, err)
	require.Zero(t, projectionMismatchCount, "real recovery should synchronize shared physical fields into every projection")
	var projectedUpdated time.Time
	err = integrationDB.QueryRowContext(ctx, `
		SELECT MAX(updated_at) FROM group_recovery_probe_states WHERE physical_state_id = $1
	`, jobs[0].PhysicalStateID).Scan(&projectedUpdated)
	require.NoError(t, err)

	// The runner executes frequently. Without a new real request, the same row
	// must not refresh success/updated/next-probe timestamps on every tick.
	count, err = repo.ReconcileRealUsage(ctx, reconciledAt.Add(30*time.Second))
	require.NoError(t, err)
	require.Zero(t, count)
	var unchangedLastSuccess, unchangedPhysicalUpdated, unchangedPhysicalNext, unchangedProjectedUpdated time.Time
	err = integrationDB.QueryRowContext(ctx, `
		SELECT next_probe_at, last_success_at, updated_at
		FROM group_recovery_probe_physical_states WHERE id = $1
	`, jobs[0].PhysicalStateID).Scan(&unchangedPhysicalNext, &unchangedLastSuccess, &unchangedPhysicalUpdated)
	require.NoError(t, err)
	err = integrationDB.QueryRowContext(ctx, `
		SELECT MAX(updated_at) FROM group_recovery_probe_states WHERE physical_state_id = $1
	`, jobs[0].PhysicalStateID).Scan(&unchangedProjectedUpdated)
	require.NoError(t, err)
	require.WithinDuration(t, physicalLastSuccess, unchangedLastSuccess, time.Microsecond)
	require.WithinDuration(t, physicalNext, unchangedPhysicalNext, time.Microsecond)
	require.WithinDuration(t, physicalUpdated, unchangedPhysicalUpdated, time.Microsecond)
	require.WithinDuration(t, projectedUpdated, unchangedProjectedUpdated, time.Microsecond)

	// Legacy projection drift is repaired even without new real usage. Shared
	// fields mirror the physical account/model, status is derived from the group
	// policy, and next-probe time remains group-specific.
	var projectionNextBefore time.Time
	_, err = integrationDB.ExecContext(ctx, `
		UPDATE group_recovery_probe_states
		SET status = 'paused',
			consecutive_successes = 88,
			consecutive_failures = 88,
			last_success_at = $3,
			last_error_class = 'permanent',
			last_error = 'legacy drift',
			latency_ms = 5432,
			probe_count = 123
		WHERE group_id = $1 AND account_id = $2
	`, siblingGroupID, accountID, realUsageAt.Add(-48*time.Hour))
	require.NoError(t, err)
	err = integrationDB.QueryRowContext(ctx, `
		SELECT next_probe_at
		FROM group_recovery_probe_states
		WHERE group_id = $1 AND account_id = $2
	`, siblingGroupID, accountID).Scan(&projectionNextBefore)
	require.NoError(t, err)
	count, err = repo.ReconcileRealUsage(ctx, reconciledAt.Add(time.Minute))
	require.NoError(t, err)
	require.Zero(t, count, "projection-only synchronization must not be reported as newly reconciled real usage")
	err = integrationDB.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM group_recovery_probe_states s
		JOIN group_recovery_probe_physical_states p ON p.id = s.physical_state_id
		WHERE p.id = $1 AND (
			s.status IS DISTINCT FROM p.status
			OR s.consecutive_successes IS DISTINCT FROM p.consecutive_successes
			OR s.consecutive_failures IS DISTINCT FROM p.consecutive_failures
			OR s.last_probe_at IS DISTINCT FROM p.last_probe_at
			OR s.last_success_at IS DISTINCT FROM p.last_success_at
			OR s.last_failure_at IS DISTINCT FROM p.last_failure_at
			OR s.last_error_class IS DISTINCT FROM p.last_error_class
			OR s.last_error IS DISTINCT FROM p.last_error
			OR s.latency_ms IS DISTINCT FROM p.latency_ms
			OR s.probe_count IS DISTINCT FROM p.probe_count
		)
	`, jobs[0].PhysicalStateID).Scan(&projectionMismatchCount)
	require.NoError(t, err)
	require.Zero(t, projectionMismatchCount)
	var projectionStatusAfter string
	var projectionNextAfter time.Time
	err = integrationDB.QueryRowContext(ctx, `
		SELECT status, next_probe_at
		FROM group_recovery_probe_states
		WHERE group_id = $1 AND account_id = $2
	`, siblingGroupID, accountID).Scan(&projectionStatusAfter, &projectionNextAfter)
	require.NoError(t, err)
	require.Equal(t, service.GroupRecoveryProbeStatusEligible, projectionStatusAfter)
	require.WithinDuration(t, projectionNextBefore, projectionNextAfter, time.Microsecond)

	// A genuinely newer real request advances the watermark exactly once.
	newerRealUsageAt := realUsageAt.Add(30 * time.Second)
	_, err = integrationDB.ExecContext(ctx, `
		INSERT INTO usage_logs (
			user_id, api_key_id, account_id, group_id, request_id, model,
			actual_cost, stream, created_at
		) VALUES ($1, $2, $3, $4, $5, 'gpt-5.6-sol', 0.01, TRUE, $6)
	`, userID, apiKeyID, accountID, groupID, fmt.Sprintf("recovery-real-newer-%d", suffix), newerRealUsageAt)
	require.NoError(t, err)
	count, err = repo.ReconcileRealUsage(ctx, newerRealUsageAt.Add(time.Second))
	require.NoError(t, err)
	require.Equal(t, int64(2), count)
	err = integrationDB.QueryRowContext(ctx, `
		SELECT last_success_at FROM group_recovery_probe_physical_states WHERE id = $1
	`, jobs[0].PhysicalStateID).Scan(&physicalLastSuccess)
	require.NoError(t, err)
	require.WithinDuration(t, newerRealUsageAt, physicalLastSuccess, time.Microsecond)
	count, err = repo.ReconcileRealUsage(ctx, newerRealUsageAt.Add(2*time.Second))
	require.NoError(t, err)
	require.Zero(t, count)

	// A permanent result is projected according to each beneficiary policy:
	// smart pauses for six hours, while manual keeps its configured cadence.
	permanentClaimedAt := realUsageAt.Add(2 * time.Minute)
	_, err = integrationDB.ExecContext(ctx, `
		UPDATE group_recovery_probe_physical_states
		SET status = 'probing', owner_group_id = $2, last_probe_at = $3
		WHERE id = $1
	`, jobs[0].PhysicalStateID, groupID, permanentClaimedAt)
	require.NoError(t, err)
	accepted, err = repo.Complete(ctx, service.GroupRecoveryProbeCompletion{
		StateID:             jobs[0].State.ID,
		PhysicalStateID:     jobs[0].PhysicalStateID,
		ClaimedAt:           permanentClaimedAt,
		Status:              service.GroupRecoveryProbeStatusPaused,
		ConsecutiveFailures: 1,
		NextProbeAt:         permanentClaimedAt.Add(6 * time.Hour),
		LastErrorClass:      service.GroupRecoveryProbeErrorPermanent,
		LastError:           "permission denied",
		AttemptCount:        1,
	})
	require.NoError(t, err)
	require.True(t, accepted)
	var smartStatus, manualStatus string
	err = integrationDB.QueryRowContext(ctx, `
		SELECT
			MIN(status) FILTER (WHERE group_id = $2),
			MIN(status) FILTER (WHERE group_id = $3),
			MIN(next_probe_at) FILTER (WHERE group_id = $2),
			MIN(next_probe_at) FILTER (WHERE group_id = $3)
		FROM group_recovery_probe_states
		WHERE account_id = $1
	`, accountID, groupID, siblingGroupID).Scan(&smartStatus, &manualStatus, &smartNext, &manualNext)
	require.NoError(t, err)
	require.Equal(t, service.GroupRecoveryProbeStatusPaused, smartStatus)
	require.Equal(t, service.GroupRecoveryProbeStatusFailed, manualStatus)
	require.True(t, manualNext.Before(smartNext), "manual policy must not inherit the smart six-hour pause")
	count, err = repo.ReconcileRealUsage(ctx, permanentClaimedAt.Add(time.Second))
	require.NoError(t, err)
	require.Zero(t, count)
	err = integrationDB.QueryRowContext(ctx, `
		SELECT
			MIN(status) FILTER (WHERE group_id = $2),
			MIN(status) FILTER (WHERE group_id = $3)
		FROM group_recovery_probe_states
		WHERE account_id = $1
	`, accountID, groupID, siblingGroupID).Scan(&smartStatus, &manualStatus)
	require.NoError(t, err)
	require.Equal(t, service.GroupRecoveryProbeStatusPaused, smartStatus)
	require.Equal(t, service.GroupRecoveryProbeStatusFailed, manualStatus)
	err = integrationDB.QueryRowContext(ctx, `
		SELECT next_probe_at FROM group_recovery_probe_physical_states WHERE id = $1
	`, jobs[0].PhysicalStateID).Scan(&physicalNext)
	require.NoError(t, err)
	require.WithinDuration(t, manualNext, physicalNext, time.Microsecond)

	// The next physical claim is owned by the manual projection. Reservation
	// failures must restore that projection's failed state, not the shared
	// physical paused state left by the previous smart owner.
	manualJobs, err := repo.ClaimDue(ctx, manualNext.Add(time.Second), 1)
	require.NoError(t, err)
	require.Len(t, manualJobs, 1)
	require.Equal(t, siblingGroupID, manualJobs[0].State.GroupID)
	require.Equal(t, service.GroupRecoveryProbeModeManual, manualJobs[0].Mode)
	require.Equal(t, service.GroupRecoveryProbeStatusFailed, manualJobs[0].PreviousStatus)
	accepted, err = repo.Complete(ctx, service.GroupRecoveryProbeCompletion{
		StateID:              manualJobs[0].State.ID,
		PhysicalStateID:      manualJobs[0].PhysicalStateID,
		ClaimedAt:            manualJobs[0].ClaimedAt,
		Status:               manualJobs[0].PreviousStatus,
		ConsecutiveSuccesses: manualJobs[0].State.ConsecutiveSuccesses,
		ConsecutiveFailures:  manualJobs[0].State.ConsecutiveFailures,
		NextProbeAt:          manualJobs[0].ClaimedAt.Add(time.Hour),
		LastErrorClass:       manualJobs[0].State.LastErrorClass,
		LastError:            manualJobs[0].State.LastError,
	})
	require.NoError(t, err)
	require.True(t, accepted)
	err = integrationDB.QueryRowContext(ctx, `
		SELECT MIN(status) FILTER (WHERE group_id = $2)
		FROM group_recovery_probe_states
		WHERE account_id = $1
	`, accountID, siblingGroupID).Scan(&manualStatus)
	require.NoError(t, err)
	require.Equal(t, service.GroupRecoveryProbeStatusFailed, manualStatus)
}
