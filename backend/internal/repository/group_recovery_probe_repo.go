package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

const groupRecoveryProbeStaleClaimInterval = "25 minutes"

type groupRecoveryProbeRepository struct {
	db *sql.DB
}

func NewGroupRecoveryProbeRepository(db *sql.DB) *groupRecoveryProbeRepository {
	return &groupRecoveryProbeRepository{db: db}
}

func (r *groupRecoveryProbeRepository) ClaimDue(ctx context.Context, now time.Time, limit int) ([]service.GroupRecoveryProbeJob, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("group recovery probe repository is unavailable")
	}
	if limit <= 0 {
		return []service.GroupRecoveryProbeJob{}, nil
	}
	// Maintenance is intentionally autocommitted before the claim transaction.
	// These idempotent steps may touch projection rows, while Complete and
	// ReconcileRealUsage lock physical rows first. Releasing each maintenance
	// lock before claiming prevents a projection -> physical lock inversion.
	if _, err := r.db.ExecContext(ctx, `
		DELETE FROM group_recovery_probe_states s
		USING group_recovery_probe_states target
		LEFT JOIN groups g ON g.id = target.group_id
		LEFT JOIN accounts a ON a.id = target.account_id
		LEFT JOIN account_groups ag ON ag.group_id = target.group_id AND ag.account_id = target.account_id
		WHERE s.id = target.id
			AND (
				g.id IS NULL
				OR g.recovery_probe_enabled = FALSE
				OR g.status <> 'active'
				OR BTRIM(g.recovery_probe_model) = ''
				OR LOWER(BTRIM(target.model)) <> LOWER(BTRIM(g.recovery_probe_model))
				OR a.id IS NULL
				OR a.status <> 'active'
				OR a.schedulable = FALSE
				OR a.deleted_at IS NOT NULL
				OR ag.account_id IS NULL
			)
	`); err != nil {
		return nil, fmt.Errorf("cleanup stale group recovery probe states: %w", err)
	}
	if _, err := r.db.ExecContext(ctx, `
		UPDATE group_recovery_probe_states s
		SET model = BTRIM(g.recovery_probe_model), updated_at = $1::timestamptz
		FROM groups g
		WHERE g.id = s.group_id
			AND LOWER(BTRIM(s.model)) = LOWER(BTRIM(g.recovery_probe_model))
			AND s.model <> BTRIM(g.recovery_probe_model)
	`, now.UTC()); err != nil {
		return nil, fmt.Errorf("normalize group recovery probe state models: %w", err)
	}
	if _, err := r.db.ExecContext(ctx, `
		DELETE FROM group_recovery_probe_physical_states p
		WHERE NOT EXISTS (
			SELECT 1 FROM group_recovery_probe_states s
			WHERE s.physical_state_id = p.id
		)
	`); err != nil {
		return nil, fmt.Errorf("cleanup orphan physical recovery probe states: %w", err)
	}

	// A state is created only after a full idle window with no successful real
	// request for this account/model in any group. One account can belong to
	// several groups, but real traffic in one group proves the same upstream
	// account/model is alive for all of them.
	if _, err := r.db.ExecContext(ctx, `
		INSERT INTO group_recovery_probe_states (
			group_id, account_id, model, status, next_probe_at, created_at, updated_at
		)
		SELECT
			g.id, a.id, g.recovery_probe_model, 'pending', $1::timestamptz, $1::timestamptz, $1::timestamptz
		FROM groups g
		JOIN account_groups ag ON ag.group_id = g.id
		JOIN accounts a ON a.id = ag.account_id
		WHERE g.recovery_probe_enabled = TRUE
			AND g.status = 'active'
			AND BTRIM(g.recovery_probe_model) <> ''
			AND a.status = 'active'
			AND a.schedulable = TRUE
			AND a.deleted_at IS NULL
			AND NOT EXISTS (
				SELECT 1
				FROM usage_logs ul
							WHERE ul.account_id = a.id
								AND LOWER(BTRIM(COALESCE(NULLIF(BTRIM(ul.requested_model), ''), ul.model))) = LOWER(BTRIM(g.recovery_probe_model))
							AND ul.actual_cost > 0
							AND ul.request_type <> 6
						AND LOWER(COALESCE(ul.user_agent, '')) NOT LIKE '%sub2api-channel-monitor/%'
					AND ul.created_at >= $1::timestamptz - make_interval(secs => g.recovery_probe_idle_threshold_seconds)
				)
		ON CONFLICT DO NOTHING
	`, now.UTC()); err != nil {
		return nil, fmt.Errorf("seed group recovery probe states: %w", err)
	}
	if _, err := r.db.ExecContext(ctx, `
		INSERT INTO group_recovery_probe_physical_states (account_id, model_key, model)
		SELECT DISTINCT ON (s.account_id, LOWER(BTRIM(s.model)))
			s.account_id, LOWER(BTRIM(s.model)), BTRIM(s.model)
		FROM group_recovery_probe_states s
		WHERE s.physical_state_id IS NULL
		ORDER BY s.account_id, LOWER(BTRIM(s.model)), s.id
		ON CONFLICT (account_id, model_key) DO NOTHING
	`); err != nil {
		return nil, fmt.Errorf("seed physical recovery probe states: %w", err)
	}
	if _, err := r.db.ExecContext(ctx, `
		UPDATE group_recovery_probe_states s
		SET physical_state_id = p.id
		FROM group_recovery_probe_physical_states p
		WHERE s.physical_state_id IS NULL
			AND p.account_id = s.account_id
			AND p.model_key = LOWER(BTRIM(s.model))
	`); err != nil {
		return nil, fmt.Errorf("link physical recovery probe states: %w", err)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, `
		WITH memberships AS (
			SELECT p.id AS physical_id, p.account_id, p.model_key,
				MIN(s.next_probe_at) AS due_at,
				MIN(g.recovery_probe_idle_threshold_seconds) AS idle_threshold_seconds,
				COUNT(DISTINCT g.id) AS beneficiary_group_count
			FROM group_recovery_probe_physical_states p
			JOIN group_recovery_probe_states s ON s.physical_state_id = p.id
			JOIN groups g ON g.id = s.group_id
			JOIN account_groups ag ON ag.group_id = s.group_id AND ag.account_id = s.account_id
			WHERE g.recovery_probe_enabled = TRUE AND g.status = 'active'
			GROUP BY p.id, p.account_id, p.model_key
		), due AS (
			SELECT p.id, membership.*,
				owner.owner_group_id, owner.previous_status, owner.mode, owner.interval_seconds,
				owner.attempts_per_round, owner.backoff_cap_seconds
			FROM group_recovery_probe_physical_states p
			JOIN memberships membership ON membership.physical_id = p.id
			JOIN LATERAL (
				SELECT s.group_id AS owner_group_id, s.status AS previous_status,
					g.recovery_probe_mode AS mode,
					g.recovery_probe_interval_seconds AS interval_seconds,
					g.recovery_probe_attempts_per_round AS attempts_per_round,
					g.recovery_probe_backoff_cap_seconds AS backoff_cap_seconds
				FROM group_recovery_probe_states s
				JOIN groups g ON g.id = s.group_id
				JOIN account_groups ag ON ag.group_id = s.group_id AND ag.account_id = s.account_id
				WHERE s.physical_state_id = p.id
					AND g.recovery_probe_enabled = TRUE
					AND g.status = 'active'
				ORDER BY s.next_probe_at ASC, s.group_id ASC
				LIMIT 1
			) owner ON TRUE
			JOIN accounts a ON a.id = p.account_id
			WHERE a.status = 'active' AND a.schedulable = TRUE AND a.deleted_at IS NULL
				AND membership.due_at <= $1::timestamptz
				AND (p.status <> 'probing' OR p.updated_at <= $1::timestamptz - INTERVAL '`+groupRecoveryProbeStaleClaimInterval+`')
				AND NOT EXISTS (
					SELECT 1 FROM usage_logs ul
							WHERE ul.account_id = p.account_id
								AND LOWER(BTRIM(COALESCE(NULLIF(BTRIM(ul.requested_model), ''), ul.model))) = p.model_key
							AND ul.actual_cost > 0
							AND ul.request_type <> 6
						AND LOWER(COALESCE(ul.user_agent, '')) NOT LIKE '%sub2api-channel-monitor/%'
						AND ul.created_at >= $1::timestamptz - make_interval(secs => membership.idle_threshold_seconds)
				)
			ORDER BY membership.due_at ASC, p.id ASC
			FOR UPDATE OF p SKIP LOCKED
			LIMIT $2
		), claimed AS (
			UPDATE group_recovery_probe_physical_states p
			SET status = 'probing', owner_group_id = due.owner_group_id,
				last_probe_at = $1::timestamptz, updated_at = $1::timestamptz
			FROM due
			WHERE p.id = due.id
			RETURNING p.id, p.account_id, p.model, p.status,
				p.consecutive_successes, p.consecutive_failures, p.last_probe_at,
				p.next_probe_at, p.last_success_at, p.last_failure_at,
				p.last_error_class, p.last_error, p.latency_ms, p.probe_count, p.updated_at,
				due.previous_status, due.owner_group_id, due.mode, due.interval_seconds,
				due.attempts_per_round, due.backoff_cap_seconds, due.beneficiary_group_count
		), projected AS (
			UPDATE group_recovery_probe_states s
			SET status = 'probing', last_probe_at = $1::timestamptz, updated_at = $1::timestamptz
			FROM claimed c
			WHERE s.physical_state_id = c.id AND s.group_id = c.owner_group_id
			RETURNING s.id, s.physical_state_id
		)
		SELECT projected.id, c.id AS physical_state_id, c.owner_group_id, c.account_id, c.model, c.status, c.previous_status,
			c.consecutive_successes, c.consecutive_failures, c.last_probe_at,
			c.next_probe_at, c.last_success_at, c.last_failure_at,
			c.last_error_class, c.last_error, c.latency_ms, c.probe_count, c.updated_at,
			c.mode, c.interval_seconds, c.attempts_per_round, c.backoff_cap_seconds,
			c.beneficiary_group_count
		FROM claimed c
		JOIN projected ON projected.physical_state_id = c.id
	`, now.UTC(), limit)
	if err != nil {
		return nil, fmt.Errorf("claim group recovery probe states: %w", err)
	}
	defer func() { _ = rows.Close() }()

	jobs := make([]service.GroupRecoveryProbeJob, 0, limit)
	for rows.Next() {
		state, job, scanErr := scanGroupRecoveryProbeJob(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		job.State = state
		job.ClaimedAt = now.UTC()
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return jobs, nil
}

func (r *groupRecoveryProbeRepository) Complete(ctx context.Context, completion service.GroupRecoveryProbeCompletion) (bool, error) {
	if r == nil || r.db == nil {
		return false, fmt.Errorf("group recovery probe repository is unavailable")
	}
	physicalStateID := completion.PhysicalStateID
	if physicalStateID <= 0 {
		physicalStateID = completion.StateID
	}
	var rescheduled int64
	err := r.db.QueryRowContext(ctx, `
		WITH target AS (
			SELECT p.*
			FROM group_recovery_probe_physical_states p
			WHERE p.id = $1 AND p.status = 'probing' AND p.last_probe_at = $2
			FOR UPDATE OF p
		), projected AS (
			UPDATE group_recovery_probe_states s
			SET status = CASE
					WHEN $9 = 'permanent' AND g.recovery_probe_mode = 'smart' THEN 'paused'
					WHEN $9 = 'permanent' THEN 'failed'
					ELSE $3
				END,
			consecutive_successes = $4,
			consecutive_failures = $5,
			last_probe_at = $2,
			last_success_at = COALESCE($6, t.last_success_at),
			last_failure_at = COALESCE($7, t.last_failure_at),
			next_probe_at = CASE
				WHEN g.id = t.owner_group_id THEN $8
				WHEN $9 = 'permanent' AND g.recovery_probe_mode = 'smart'
					THEN NOW() + INTERVAL '6 hours'
				WHEN $3 = 'paused' AND g.recovery_probe_mode = 'smart'
					THEN NOW() + INTERVAL '6 hours'
				WHEN g.recovery_probe_mode = 'smart' AND $3 = 'eligible'
					THEN NOW() + make_interval(secs => GREATEST(
						g.recovery_probe_interval_seconds,
						CASE
							WHEN $4 >= 4 THEN 14400
							WHEN $4 >= 3 THEN 7200
							ELSE $13
						END
					))
				WHEN g.recovery_probe_mode = 'smart' AND $3 = 'failed'
					THEN NOW() + LEAST(
						make_interval(secs => g.recovery_probe_backoff_cap_seconds),
						CASE
							WHEN $5 <= 1 THEN INTERVAL '1 minute'
							WHEN $5 = 2 THEN INTERVAL '2 minutes'
							WHEN $5 = 3 THEN INTERVAL '5 minutes'
							WHEN $5 = 4 THEN INTERVAL '10 minutes'
							WHEN $5 = 5 THEN INTERVAL '15 minutes'
							ELSE INTERVAL '30 minutes'
						END
					)
				WHEN g.recovery_probe_mode = 'smart' AND $3 = 'warm' THEN NOW() + INTERVAL '5 minutes'
				ELSE NOW() + make_interval(secs => g.recovery_probe_interval_seconds)
			END,
			last_error_class = $9,
			last_error = $10,
			latency_ms = $11,
			probe_count = t.probe_count + $12,
			updated_at = NOW()
			FROM target t, groups g
			WHERE s.physical_state_id = t.id
				AND g.id = s.group_id
			RETURNING s.physical_state_id, s.next_probe_at
		), projection_due AS (
			SELECT physical_state_id, MIN(next_probe_at) AS next_probe_at
			FROM projected
			GROUP BY physical_state_id
		), completed AS (
			UPDATE group_recovery_probe_physical_states p
			SET status = $3,
				consecutive_successes = $4,
				consecutive_failures = $5,
				last_success_at = COALESCE($6, p.last_success_at),
				last_failure_at = COALESCE($7, p.last_failure_at),
				next_probe_at = COALESCE(due.next_probe_at, $8),
				last_error_class = $9,
				last_error = $10,
				latency_ms = $11,
				probe_count = p.probe_count + $12,
				updated_at = NOW()
			FROM target t
			LEFT JOIN projection_due due ON due.physical_state_id = t.id
			WHERE p.id = t.id
			RETURNING p.id
		)
		SELECT COUNT(*) FROM completed
	`, physicalStateID, completion.ClaimedAt.UTC(), completion.Status,
		completion.ConsecutiveSuccesses, completion.ConsecutiveFailures,
		completion.LastSuccessAt, completion.LastFailureAt, completion.NextProbeAt.UTC(),
		completion.LastErrorClass, completion.LastError, completion.LatencyMs, completion.AttemptCount,
		service.GroupRecoveryProbeSmartEligibleMinIntervalSeconds).Scan(&rescheduled)
	if err != nil {
		return false, fmt.Errorf("complete group recovery probe: %w", err)
	}
	// The timestamp guard prevents an older worker from replacing a newer
	// physical claim. Callers must not audit or bill a rejected completion.
	return rescheduled > 0, nil
}

func (r *groupRecoveryProbeRepository) CreateAudit(ctx context.Context, audit service.GroupRecoveryProbeAudit) error {
	_, err := r.CreateAuditWithID(ctx, audit)
	return err
}

func (r *groupRecoveryProbeRepository) CreateAuditWithID(ctx context.Context, audit service.GroupRecoveryProbeAudit) (int64, error) {
	if r == nil || r.db == nil {
		return 0, fmt.Errorf("group recovery probe repository is unavailable")
	}
	if audit.CostStatus == "" {
		audit.CostStatus = service.GroupRecoveryProbeCostStatusUnavailable
	}
	if audit.SettlementStatus == "" {
		audit.SettlementStatus = service.GroupRecoveryProbeSettlementPending
	}
	var auditID int64
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO group_recovery_probe_audits (
			physical_state_id, beneficiary_group_count,
			group_id, account_id, model, started_at, finished_at, status,
			attempts, success_count, failure_count, latency_ms, error_class,
			sanitized_error, upstream_status_code, actual_cost, cost_status,
			input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens,
			settlement_status, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15,
			$16, $17, $18, $19, $20, $21, $22, NOW())
		RETURNING id
	`, audit.PhysicalStateID, maxInt(audit.BeneficiaryGroups, 1),
		audit.GroupID, audit.AccountID, audit.Model, audit.StartedAt.UTC(), audit.FinishedAt.UTC(),
		audit.Status, audit.Attempts, audit.SuccessCount, audit.FailureCount, audit.LatencyMs,
		audit.ErrorClass, audit.SanitizedError, audit.UpstreamStatusCode, audit.EstimatedCost, audit.CostStatus,
		audit.UsageTokens.InputTokens, audit.UsageTokens.OutputTokens, audit.UsageTokens.CacheCreationTokens,
		audit.UsageTokens.CacheReadTokens, audit.SettlementStatus).Scan(&auditID)
	if err != nil {
		return 0, fmt.Errorf("create group recovery probe audit: %w", err)
	}
	return auditID, nil
}

func (r *groupRecoveryProbeRepository) GetBillingSummary(ctx context.Context, groupID int64, since time.Time) (service.GroupRecoveryProbeBillingSummary, error) {
	var summary service.GroupRecoveryProbeBillingSummary
	if r == nil || r.db == nil {
		return summary, fmt.Errorf("group recovery probe repository is unavailable")
	}
	query := `
		SELECT
			COALESCE(SUM(settled_cost) FILTER (WHERE settlement_status = 'settled'), 0),
			COALESCE(SUM(
				CASE
					WHEN settlement_status = 'budget_blocked' THEN 0
					ELSE COALESCE(settled_cost, actual_cost, 0)
				END
			), 0),
			COALESCE(SUM(attempts), 0),
			COUNT(*) FILTER (WHERE settlement_status = 'settled'),
			COUNT(*) FILTER (WHERE settlement_status = 'unavailable'),
			COUNT(*) FILTER (WHERE settlement_status = 'failed')
		FROM group_recovery_probe_audits
		WHERE created_at >= $1`
	args := []any{since.UTC()}
	if groupID > 0 {
		query += " AND group_id = $2"
		args = append(args, groupID)
	}
	err := r.db.QueryRowContext(ctx, query, args...).Scan(
		&summary.TodaySettledCost,
		&summary.TodayBudgetCost,
		&summary.TodayAttempts,
		&summary.TodaySettled,
		&summary.TodayUnavailable,
		&summary.TodayFailed,
	)
	if err != nil {
		return summary, fmt.Errorf("get group recovery probe billing summary: %w", err)
	}
	return summary, nil
}

func (r *groupRecoveryProbeRepository) UpdateAuditSettlement(ctx context.Context, auditID int64, settlement service.GroupRecoveryProbeAuditSettlement) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("group recovery probe repository is unavailable")
	}
	if auditID <= 0 {
		return fmt.Errorf("group recovery probe audit id is required")
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE group_recovery_probe_audits
		SET settlement_status = $2,
			settled_cost = $3,
			usage_log_id = $4,
			billing_user_id = $5,
			billing_api_key_id = $6,
			settlement_error = $7,
			cost_status = CASE WHEN $8 <> '' THEN $8 ELSE cost_status END
		WHERE id = $1 AND settlement_status = 'pending'
	`, auditID, settlement.Status, settlement.SettledCost, settlement.UsageLogID,
		settlement.BillingUserID, settlement.BillingAPIKeyID, settlement.Error, settlement.CostStatus)
	if err != nil {
		return fmt.Errorf("update group recovery probe audit settlement: %w", err)
	}
	return nil
}

func (r *groupRecoveryProbeRepository) ListStates(ctx context.Context, groupID int64, accountIDs []int64, model string) (map[int64]service.GroupRecoveryProbeState, error) {
	result := make(map[int64]service.GroupRecoveryProbeState, len(accountIDs))
	model = strings.TrimSpace(model)
	if r == nil || r.db == nil || groupID <= 0 || len(accountIDs) == 0 || model == "" {
		return result, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, group_id, account_id, model, status,
			consecutive_successes, consecutive_failures, last_probe_at,
			next_probe_at, last_success_at, last_failure_at,
			last_error_class, last_error, latency_ms, probe_count, updated_at
		FROM group_recovery_probe_states
		WHERE group_id = $1 AND account_id = ANY($2)
			AND LOWER(BTRIM(model)) = LOWER(BTRIM($3))
	`, groupID, pq.Array(accountIDs), model)
	if err != nil {
		return nil, fmt.Errorf("list group recovery probe states: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		state, scanErr := scanGroupRecoveryProbeState(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result[state.AccountID] = state
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *groupRecoveryProbeRepository) ReconcileRealUsage(ctx context.Context, now time.Time) (int64, error) {
	if r == nil || r.db == nil {
		return 0, fmt.Errorf("group recovery probe repository is unavailable")
	}
	var projectionCount int64
	err := r.db.QueryRowContext(ctx, `
		WITH target AS (
			SELECT p.*, real_usage.real_usage_at
			FROM group_recovery_probe_physical_states p
			JOIN LATERAL (
				SELECT MAX(ul.created_at) AS real_usage_at
				FROM usage_logs ul
					WHERE ul.account_id = p.account_id
						AND LOWER(BTRIM(COALESCE(NULLIF(BTRIM(ul.requested_model), ''), ul.model))) = p.model_key
						AND ul.actual_cost > 0
						AND ul.request_type <> 6
						AND LOWER(COALESCE(ul.user_agent, '')) NOT LIKE '%sub2api-channel-monitor/%'
						AND ul.created_at > GREATEST(
							COALESCE(p.last_probe_at, '-infinity'::timestamptz),
							COALESCE(p.last_success_at, '-infinity'::timestamptz)
						)
			) real_usage ON real_usage.real_usage_at > GREATEST(
				COALESCE(p.last_probe_at, '-infinity'::timestamptz),
				COALESCE(p.last_success_at, '-infinity'::timestamptz)
			)
			FOR UPDATE OF p
		), synchronization_target AS (
			SELECT p.*
			FROM group_recovery_probe_physical_states p
			WHERE NOT EXISTS (SELECT 1 FROM target t WHERE t.id = p.id)
				AND EXISTS (
					SELECT 1
					FROM group_recovery_probe_states s
					JOIN groups g ON g.id = s.group_id
					WHERE s.physical_state_id = p.id
						AND (
							(
								p.status <> 'probing'
								AND s.status IS DISTINCT FROM CASE
									WHEN p.last_error_class = 'permanent' AND g.recovery_probe_mode = 'smart' THEN 'paused'
									WHEN p.last_error_class = 'permanent' THEN 'failed'
									ELSE p.status
								END
							)
							OR ROW(
							s.consecutive_successes, s.consecutive_failures,
							s.last_probe_at, s.last_success_at, s.last_failure_at,
							s.last_error_class, s.last_error, s.latency_ms, s.probe_count
							) IS DISTINCT FROM ROW(
							p.consecutive_successes, p.consecutive_failures,
							p.last_probe_at, p.last_success_at, p.last_failure_at,
							p.last_error_class, p.last_error, p.latency_ms, p.probe_count
							)
						)
				)
			FOR UPDATE OF p
		), synchronized AS (
			UPDATE group_recovery_probe_states s
			SET status = CASE
					WHEN p.status = 'probing' THEN s.status
					WHEN p.last_error_class = 'permanent' AND g.recovery_probe_mode = 'smart' THEN 'paused'
					WHEN p.last_error_class = 'permanent' THEN 'failed'
					ELSE p.status
				END,
				consecutive_successes = p.consecutive_successes,
				consecutive_failures = p.consecutive_failures,
				last_probe_at = p.last_probe_at,
				last_success_at = p.last_success_at,
				last_failure_at = p.last_failure_at,
				last_error_class = p.last_error_class,
				last_error = p.last_error,
				latency_ms = p.latency_ms,
				probe_count = p.probe_count,
				updated_at = $1::timestamptz
			FROM synchronization_target p, groups g
			WHERE s.physical_state_id = p.id AND g.id = s.group_id
				AND (
					(
						p.status <> 'probing'
						AND s.status IS DISTINCT FROM CASE
							WHEN p.last_error_class = 'permanent' AND g.recovery_probe_mode = 'smart' THEN 'paused'
							WHEN p.last_error_class = 'permanent' THEN 'failed'
							ELSE p.status
						END
					)
					OR ROW(
						s.consecutive_successes, s.consecutive_failures,
						s.last_probe_at, s.last_success_at, s.last_failure_at,
						s.last_error_class, s.last_error, s.latency_ms, s.probe_count
						) IS DISTINCT FROM ROW(
						p.consecutive_successes, p.consecutive_failures,
						p.last_probe_at, p.last_success_at, p.last_failure_at,
						p.last_error_class, p.last_error, p.latency_ms, p.probe_count
						)
					)
				RETURNING s.id
		), projected AS (
			UPDATE group_recovery_probe_states s
			SET status = 'eligible',
				consecutive_successes = GREATEST(t.consecutive_successes, 2),
				consecutive_failures = 0,
				last_probe_at = t.last_probe_at,
				last_success_at = t.real_usage_at,
				last_failure_at = t.last_failure_at,
				next_probe_at = t.real_usage_at + make_interval(secs => CASE
					WHEN g.recovery_probe_mode = 'smart' THEN GREATEST(
						g.recovery_probe_interval_seconds,
					CASE
						WHEN t.consecutive_successes >= 4 THEN 14400
						WHEN t.consecutive_successes >= 3 THEN 7200
						ELSE $2
					END
				)
				ELSE g.recovery_probe_interval_seconds
				END),
				last_error_class = '',
				last_error = '',
				latency_ms = t.latency_ms,
				probe_count = t.probe_count,
				updated_at = $1::timestamptz
			FROM target t, groups g
			WHERE s.physical_state_id = t.id AND g.id = s.group_id
			RETURNING s.physical_state_id, s.next_probe_at
		), projection_due AS (
			SELECT physical_state_id, MIN(next_probe_at) AS next_probe_at
			FROM projected
			GROUP BY physical_state_id
		), healed AS (
			UPDATE group_recovery_probe_physical_states p
			SET status = 'eligible',
				consecutive_successes = GREATEST(t.consecutive_successes, 2),
				consecutive_failures = 0,
				last_success_at = t.real_usage_at,
				next_probe_at = due.next_probe_at,
				last_error_class = '',
				last_error = '',
				updated_at = $1::timestamptz
			FROM target t
			JOIN projection_due due ON due.physical_state_id = t.id
			WHERE p.id = t.id
			RETURNING p.id
		)
		SELECT COUNT(*) FROM projected
	`, now.UTC(), service.GroupRecoveryProbeSmartEligibleMinIntervalSeconds).Scan(&projectionCount)
	if err != nil {
		return 0, fmt.Errorf("reconcile group recovery probe real usage: %w", err)
	}
	return projectionCount, nil
}

type groupRecoveryProbeScanner interface {
	Scan(dest ...any) error
}

func scanGroupRecoveryProbeState(scanner groupRecoveryProbeScanner) (service.GroupRecoveryProbeState, error) {
	var state service.GroupRecoveryProbeState
	var lastProbeAt, nextProbeAt, lastSuccessAt, lastFailureAt sql.NullTime
	err := scanner.Scan(
		&state.ID, &state.GroupID, &state.AccountID, &state.Model, &state.Status,
		&state.ConsecutiveSuccesses, &state.ConsecutiveFailures, &lastProbeAt,
		&nextProbeAt, &lastSuccessAt, &lastFailureAt,
		&state.LastErrorClass, &state.LastError, &state.LatencyMs, &state.ProbeCount, &state.UpdatedAt,
	)
	if err != nil {
		return state, err
	}
	state.LastProbeAt = nullableTimePointer(lastProbeAt)
	state.NextProbeAt = nullableTimePointer(nextProbeAt)
	state.LastSuccessAt = nullableTimePointer(lastSuccessAt)
	state.LastFailureAt = nullableTimePointer(lastFailureAt)
	return state, nil
}

func scanGroupRecoveryProbeJob(scanner groupRecoveryProbeScanner) (service.GroupRecoveryProbeState, service.GroupRecoveryProbeJob, error) {
	var state service.GroupRecoveryProbeState
	var job service.GroupRecoveryProbeJob
	var lastProbeAt, nextProbeAt, lastSuccessAt, lastFailureAt sql.NullTime
	err := scanner.Scan(
		&state.ID, &job.PhysicalStateID, &state.GroupID, &state.AccountID, &state.Model, &state.Status,
		&job.PreviousStatus,
		&state.ConsecutiveSuccesses, &state.ConsecutiveFailures, &lastProbeAt,
		&nextProbeAt, &lastSuccessAt, &lastFailureAt,
		&state.LastErrorClass, &state.LastError, &state.LatencyMs, &state.ProbeCount, &state.UpdatedAt,
		&job.Mode, &job.IntervalSeconds, &job.AttemptsPerRound, &job.BackoffCapSeconds,
		&job.BeneficiaryGroups,
	)
	if err != nil {
		return state, job, err
	}
	state.LastProbeAt = nullableTimePointer(lastProbeAt)
	state.NextProbeAt = nullableTimePointer(nextProbeAt)
	state.LastSuccessAt = nullableTimePointer(lastSuccessAt)
	state.LastFailureAt = nullableTimePointer(lastFailureAt)
	return state, job, nil
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func nullableTimePointer(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	t := value.Time
	return &t
}
