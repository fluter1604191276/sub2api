package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestGroupRecoveryProbeRepositoryClaimDue(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	now := time.Date(2026, 8, 9, 10, 30, 0, 0, time.UTC)
	lastSuccess := now.Add(-2 * time.Hour)
	lastFailure := now.Add(-time.Hour)
	updatedAt := now.Add(-30 * time.Minute)
	rows := sqlmock.NewRows([]string{
		"id", "physical_state_id", "group_id", "account_id", "model", "status",
		"previous_status",
		"consecutive_successes", "consecutive_failures", "last_probe_at",
		"next_probe_at", "last_success_at", "last_failure_at",
		"last_error_class", "last_error", "latency_ms", "probe_count", "updated_at",
		"recovery_probe_mode", "recovery_probe_interval_seconds",
		"recovery_probe_attempts_per_round", "recovery_probe_backoff_cap_seconds",
		"beneficiary_group_count",
	}).AddRow(
		int64(31), int64(88), int64(7), int64(19), "claude-sonnet-4-6", service.GroupRecoveryProbeStatusProbing,
		service.GroupRecoveryProbeStatusEligible,
		1, 2, now,
		now.Add(-time.Minute), lastSuccess, lastFailure,
		service.GroupRecoveryProbeErrorTransient, "temporary failure", int64(1250), int64(4), updatedAt,
		service.GroupRecoveryProbeModeSmart, 900, 2, 1800, 2,
	)

	mock.ExpectExec(`(?s)DELETE FROM group_recovery_probe_states s.*LOWER\(BTRIM\(target\.model\)\) <> LOWER\(BTRIM\(g\.recovery_probe_model\)\).*ag\.account_id IS NULL`).
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec(`(?s)UPDATE group_recovery_probe_states s.*SET model = BTRIM\(g\.recovery_probe_model\).*LOWER\(BTRIM\(s\.model\)\) = LOWER\(BTRIM\(g\.recovery_probe_model\)\)`).
		WithArgs(now).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`(?s)DELETE FROM group_recovery_probe_physical_states p.*NOT EXISTS.*s\.physical_state_id = p\.id`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`(?s)INSERT INTO group_recovery_probe_states.*ul\.actual_cost > 0.*ul\.request_type <> 6.*\$1::timestamptz.*ON CONFLICT DO NOTHING`).
		WithArgs(now).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`(?s)INSERT INTO group_recovery_probe_physical_states.*LOWER\(BTRIM\(s\.model\)\).*ON CONFLICT \(account_id, model_key\) DO NOTHING`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`(?s)UPDATE group_recovery_probe_states s.*SET physical_state_id = p\.id.*p\.model_key = LOWER\(BTRIM\(s\.model\)\)`).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)WITH memberships AS.*MIN\(s\.next_probe_at\) AS due_at.*COUNT\(DISTINCT g\.id\) AS beneficiary_group_count.*JOIN LATERAL.*ORDER BY s\.next_probe_at ASC, s\.group_id ASC.*membership\.due_at <= \$1::timestamptz.*ul\.actual_cost > 0.*FOR UPDATE OF p SKIP LOCKED.*UPDATE group_recovery_probe_physical_states p.*UPDATE group_recovery_probe_states s`).
		WithArgs(now, 4).
		WillReturnRows(rows)
	mock.ExpectCommit()

	repo := &groupRecoveryProbeRepository{db: db}
	jobs, err := repo.ClaimDue(context.Background(), now, 4)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	require.Len(t, jobs, 1)
	require.Equal(t, int64(31), jobs[0].State.ID)
	require.Equal(t, int64(88), jobs[0].PhysicalStateID)
	require.Equal(t, int64(7), jobs[0].State.GroupID)
	require.Equal(t, int64(19), jobs[0].State.AccountID)
	require.Equal(t, service.GroupRecoveryProbeStatusProbing, jobs[0].State.Status)
	require.Equal(t, service.GroupRecoveryProbeStatusEligible, jobs[0].PreviousStatus)
	require.Equal(t, service.GroupRecoveryProbeModeSmart, jobs[0].Mode)
	require.Equal(t, 2, jobs[0].AttemptsPerRound)
	require.Equal(t, 2, jobs[0].BeneficiaryGroups)
	require.Equal(t, now, jobs[0].ClaimedAt)
	require.NotNil(t, jobs[0].State.LastSuccessAt)
	require.Equal(t, lastSuccess, *jobs[0].State.LastSuccessAt)
}

func TestGroupRecoveryProbeRepositoryCompleteUsesClaimOwnershipGuard(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	claimedAt := time.Date(2026, 8, 9, 11, 0, 0, 0, time.UTC)
	completedAt := claimedAt.Add(8 * time.Second)
	nextProbeAt := completedAt.Add(15 * time.Minute)
	completion := service.GroupRecoveryProbeCompletion{
		StateID:              44,
		PhysicalStateID:      91,
		ClaimedAt:            claimedAt,
		Status:               service.GroupRecoveryProbeStatusWarm,
		ConsecutiveSuccesses: 1,
		ConsecutiveFailures:  0,
		LastSuccessAt:        &completedAt,
		NextProbeAt:          nextProbeAt,
		LastErrorClass:       service.GroupRecoveryProbeErrorNone,
		LastError:            "",
		LatencyMs:            820,
		AttemptCount:         1,
	}

	// A stale worker must not overwrite a newer claim. Returning zero affected
	// rows is therefore an expected no-op, not a repository failure.
	mock.ExpectQuery(`(?s)WITH target AS.*p\.id = \$1 AND p\.status = 'probing' AND p\.last_probe_at = \$2.*FOR UPDATE OF p.*projected AS.*UPDATE group_recovery_probe_states s.*projection_due AS.*MIN\(next_probe_at\).*completed AS.*UPDATE group_recovery_probe_physical_states p.*SELECT COUNT\(\*\) FROM completed`).
		WithArgs(
			completion.PhysicalStateID, claimedAt, completion.Status,
			completion.ConsecutiveSuccesses, completion.ConsecutiveFailures,
			completion.LastSuccessAt, completion.LastFailureAt, nextProbeAt,
			completion.LastErrorClass, completion.LastError, completion.LatencyMs, completion.AttemptCount,
			service.GroupRecoveryProbeSmartEligibleMinIntervalSeconds,
		).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(0)))

	repo := &groupRecoveryProbeRepository{db: db}
	accepted, err := repo.Complete(context.Background(), completion)
	require.NoError(t, err)
	require.False(t, accepted)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGroupRecoveryProbeRepositoryCreateAuditPreservesEstimatedCost(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	startedAt := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	finishedAt := startedAt.Add(1500 * time.Millisecond)
	audit := service.GroupRecoveryProbeAudit{
		PhysicalStateID:   91,
		BeneficiaryGroups: 2,
		GroupID:           7,
		AccountID:         19,
		Model:             "gpt-5.6-sol",
		StartedAt:         startedAt,
		FinishedAt:        finishedAt,
		Status:            service.GroupRecoveryProbeStatusEligible,
		Attempts:          1,
		SuccessCount:      1,
		LatencyMs:         1500,
		EstimatedCost:     float64PointerForGroupRecoveryProbeRepoTest(0.00042),
		CostStatus:        service.GroupRecoveryProbeCostStatusEstimated,
	}

	mock.ExpectQuery(`(?s)INSERT INTO group_recovery_probe_audits.*actual_cost.*cost_status.*settlement_status.*NOW\(\).*RETURNING id`).
		WithArgs(int64(91), 2, int64(7), int64(19), "gpt-5.6-sol", startedAt, finishedAt,
			service.GroupRecoveryProbeStatusEligible, 1, 1, 0, 1500, "", "", nil, float64PointerForGroupRecoveryProbeRepoTest(0.00042),
			service.GroupRecoveryProbeCostStatusEstimated, int64(0), int64(0), int64(0), int64(0),
			service.GroupRecoveryProbeSettlementPending).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(1)))

	repo := &groupRecoveryProbeRepository{db: db}
	require.NoError(t, repo.CreateAudit(context.Background(), audit))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGroupRecoveryProbeRepositoryListStates(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	now := time.Date(2026, 8, 9, 11, 30, 0, 0, time.UTC)
	accountIDs := []int64{19, 23}
	rows := sqlmock.NewRows([]string{
		"id", "group_id", "account_id", "model", "status",
		"consecutive_successes", "consecutive_failures", "last_probe_at",
		"next_probe_at", "last_success_at", "last_failure_at",
		"last_error_class", "last_error", "latency_ms", "probe_count", "updated_at",
	}).AddRow(
		int64(51), int64(7), int64(19), "gpt-5.6-sol", service.GroupRecoveryProbeStatusWarm,
		1, 0, now.Add(-time.Minute), now.Add(14*time.Minute), now.Add(-time.Minute), nil,
		"", "", int64(640), int64(2), now,
	).AddRow(
		int64(52), int64(7), int64(23), "gpt-5.6-sol", service.GroupRecoveryProbeStatusFailed,
		0, 3, now.Add(-2*time.Minute), now.Add(3*time.Minute), nil, now.Add(-2*time.Minute),
		service.GroupRecoveryProbeErrorTransient, "timeout", int64(0), int64(5), now,
	)

	mock.ExpectQuery(`(?s)SELECT id, group_id, account_id, model, status.*WHERE group_id = \$1 AND account_id = ANY\(\$2\).*LOWER\(BTRIM\(model\)\) = LOWER\(BTRIM\(\$3\)\)`).
		WithArgs(int64(7), pq.Array(accountIDs), "gpt-5.6-sol").
		WillReturnRows(rows)

	repo := &groupRecoveryProbeRepository{db: db}
	states, err := repo.ListStates(context.Background(), 7, accountIDs, "gpt-5.6-sol")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	require.Len(t, states, 2)
	require.Equal(t, service.GroupRecoveryProbeStatusWarm, states[19].Status)
	require.Equal(t, service.GroupRecoveryProbeStatusFailed, states[23].Status)
	require.Equal(t, 3, states[23].ConsecutiveFailures)
}

func TestGroupRecoveryProbeRepositoryReconcileRealUsage(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`(?s)WITH target AS.*group_recovery_probe_physical_states p.*JOIN LATERAL.*MAX\(ul\.created_at\).*ul\.actual_cost > 0.*ul\.request_type <> 6.*ul\.created_at > GREATEST.*real_usage\.real_usage_at > GREATEST.*p\.last_probe_at.*p\.last_success_at.*FOR UPDATE OF p.*synchronization_target AS.*NOT EXISTS \(SELECT 1 FROM target.*recovery_probe_mode = 'smart'.*FOR UPDATE OF p.*synchronized AS.*UPDATE group_recovery_probe_states s.*p\.status = 'probing' THEN s\.status.*probe_count = p\.probe_count.*FROM synchronization_target p.*projected AS.*UPDATE group_recovery_probe_states s.*last_success_at = t\.real_usage_at.*probe_count = t\.probe_count.*projection_due AS.*MIN\(next_probe_at\).*healed AS.*UPDATE group_recovery_probe_physical_states p.*last_success_at = t\.real_usage_at.*SELECT COUNT\(\*\) FROM projected`).
		WithArgs(now, service.GroupRecoveryProbeSmartEligibleMinIntervalSeconds).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(2)))

	repo := &groupRecoveryProbeRepository{db: db}
	count, err := repo.ReconcileRealUsage(context.Background(), now)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	require.Equal(t, int64(2), count)
}

func float64PointerForGroupRecoveryProbeRepoTest(value float64) *float64 {
	return &value
}
