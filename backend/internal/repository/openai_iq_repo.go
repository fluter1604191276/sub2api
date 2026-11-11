package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type openAIIQRepository struct {
	db *sql.DB
}

func NewOpenAIIQRepository(db *sql.DB) service.OpenAIIQRepository {
	return &openAIIQRepository{db: db}
}

func (r *openAIIQRepository) Latest(ctx context.Context, groupIDs []int64) (*service.IQView, error) {
	if r == nil || r.db == nil {
		return &service.IQView{Results: []service.IQResult{}}, nil
	}
	if len(groupIDs) == 0 {
		return &service.IQView{Results: []service.IQResult{}}, nil
	}
	var runID int64
	var started time.Time
	var question string
	var expectedAnswer string
	var model string
	err := r.db.QueryRowContext(ctx, `
		SELECT id, started_at, question, expected_answer, model
		FROM openai_iq_runs
		WHERE status IN ('running', 'complete', 'error')
		  AND EXISTS (
			  SELECT 1
			  FROM openai_iq_results visible_result
			  WHERE visible_result.run_id = openai_iq_runs.id
			    AND visible_result.group_id = ANY($1)
		  )
		ORDER BY started_at DESC
		LIMIT 1`, pq.Array(groupIDs)).Scan(&runID, &started, &question, &expectedAnswer, &model)
	if err == sql.ErrNoRows {
		return &service.IQView{Results: []service.IQResult{}}, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT group_id,group_name,rate_multiplier,model,response_model,expected_answer,model_answer,output,status,checked_at,error FROM openai_iq_results WHERE run_id=$1 AND group_id=ANY($2) ORDER BY group_name`, runID, pq.Array(groupIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	view := &service.IQView{
		Model:          model,
		Question:       question,
		ExpectedAnswer: expectedAnswer,
		RoundStartedAt: &started,
		Results:        []service.IQResult{},
	}
	for rows.Next() {
		var x service.IQResult
		if err := rows.Scan(&x.GroupID, &x.GroupName, &x.RateMultiplier, &x.Model, &x.ResponseModel, &x.ExpectedAnswer, &x.ModelAnswer, &x.Output, &x.Status, &x.CheckedAt, &x.Error); err != nil {
			return nil, err
		}
		view.Results = append(view.Results, x)
	}
	return view, rows.Err()
}
func (r *openAIIQRepository) Budget(ctx context.Context, limit float64) (service.IQBudget, error) {
	out := service.IQBudget{Date: timezone.Today().Format("2006-01-02")}
	if r == nil || r.db == nil {
		out.RemainingUSD = limit
		return out, nil
	}
	if err := r.ReconcileBudget(ctx); err != nil {
		return out, err
	}
	// A pending result in a live run can still be using its allowance. Once
	// that result or run is terminal, its unmatched hold is an unknown expense,
	// not a request that the coordinator should wait for indefinitely.
	err := r.db.QueryRowContext(ctx, `WITH reservations AS (
		SELECT budget.actual_cost_usd, budget.reserved_usd,
		       budget.actual_cost_usd IS NULL AND NOT COALESCE(
		           runs.status = 'running' AND runs.lease_expires_at >= NOW()
		           AND results.status = 'pending', FALSE) AS unconfirmed,
		       COALESCE(results.checked_at, runs.finished_at,
		           CASE WHEN runs.lease_expires_at < NOW() THEN runs.lease_expires_at END,
		           budget.created_at) AS unconfirmed_since
		FROM openai_iq_budget_reservations budget
		LEFT JOIN openai_iq_runs runs ON runs.id = budget.run_id
		LEFT JOIN openai_iq_results results
		    ON results.run_id = budget.run_id AND results.group_id = budget.group_id
		WHERE budget.budget_date = $1
	) SELECT COALESCE(SUM(actual_cost_usd), 0),
		COALESCE(SUM(reserved_usd) FILTER (WHERE actual_cost_usd IS NULL), 0),
		COALESCE(SUM(reserved_usd) FILTER (WHERE unconfirmed), 0),
		MAX(unconfirmed_since) FILTER (WHERE unconfirmed)
	FROM reservations`, out.Date).Scan(&out.SpentUSD, &out.ReservedUSD, &out.UnconfirmedUSD, &out.UnconfirmedSince)
	if err != nil {
		return out, fmt.Errorf("read OpenAI IQ budget: %w", err)
	}
	out.RemainingUSD = limit - out.SpentUSD - out.ReservedUSD
	if out.RemainingUSD < 0 {
		out.RemainingUSD = 0
	}
	return out, nil
}

// ClaimDue creates one durable snapshot only when the configured interval has
// elapsed. The transaction advisory lock makes the decision safe across server
// instances. An expired lease is never replayed: it is terminally marked error.
func (r *openAIIQRepository) ClaimDue(ctx context.Context, settings service.IQSettings) (*service.IQRun, error) {
	if r == nil || r.db == nil || !settings.Enabled || len(settings.GroupIDs) == 0 {
		return nil, nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(14020261104)`); err != nil {
		return nil, err
	}
	claimNow, err := iqClaimTimestamp(ctx, tx)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		WITH expired_runs AS (
			UPDATE openai_iq_runs
			SET status = 'error', finished_at = $1
			WHERE status = 'running' AND lease_expires_at IS NOT NULL AND lease_expires_at < $1
			RETURNING id
		)
		UPDATE openai_iq_results
		SET status = 'error', checked_at = $1, error = '执行进程未在租约内完成，本轮已终止，费用预留继续保留'
		WHERE status = 'pending' AND run_id IN (SELECT id FROM expired_runs)`, claimNow); err != nil {
		return nil, err
	}
	var activeRunID int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM openai_iq_runs WHERE status = 'running' AND lease_expires_at >= $1 ORDER BY started_at DESC LIMIT 1`, claimNow).Scan(&activeRunID)
	if err == nil {
		return nil, tx.Commit()
	}
	if err != sql.ErrNoRows {
		return nil, err
	}

	var due bool
	err = tx.QueryRowContext(ctx, `
		SELECT started_at <= $1::timestamptz - ($2::double precision * INTERVAL '1 second')
		FROM openai_iq_runs WHERE trigger = $3 ORDER BY started_at DESC LIMIT 1`, claimNow, settings.IntervalSeconds, service.IQTriggerScheduled).Scan(&due)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if err == nil && !due {
		return nil, tx.Commit()
	}

	run := &service.IQRun{
		Question: settings.Question,
		Answer:   settings.ExpectedAnswer,
		Model:    settings.Model,
		Trigger:  service.IQTriggerScheduled,
	}
	snapshot, err := json.Marshal(settings)
	if err != nil {
		return nil, err
	}
	err = tx.QueryRowContext(ctx, `
		INSERT INTO openai_iq_runs (started_at, question, expected_answer, model, settings_snapshot, lease_expires_at, trigger)
		VALUES ($1, $2, $3, $4, $5::jsonb, $1::timestamptz + INTERVAL '10 minutes', $6)
		RETURNING id, started_at`, claimNow, run.Question, run.Answer, run.Model, string(snapshot), run.Trigger).Scan(&run.ID, &run.StartedAt)
	if err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO openai_iq_results (run_id, group_id, group_name, rate_multiplier, model, expected_answer, status)
		SELECT $1, id, name, rate_multiplier, $2, $3, 'pending'
		FROM groups
		WHERE id = ANY($4) AND platform = $5 AND status = 'active' AND deleted_at IS NULL`,
		run.ID, run.Model, run.Answer, pq.Array(settings.GroupIDs), service.PlatformOpenAI)
	if err != nil {
		return nil, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if count != int64(len(settings.GroupIDs)) {
		return nil, fmt.Errorf("OpenAI IQ group snapshot changed while claiming run")
	}
	if err := refreshIQRunLease(ctx, tx, run); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return run, nil
}

// ClaimManual creates one administrator-requested, single-group run. The
// transaction uses the same advisory lock as ClaimDue so it cannot overlap a
// scheduled run or another manual request on a different server instance.
func (r *openAIIQRepository) ClaimManual(ctx context.Context, settings service.IQSettings, groupID, requestedBy int64) (*service.IQRun, error) {
	if r == nil || r.db == nil || groupID <= 0 || requestedBy <= 0 || !containsIQGroup(settings.GroupIDs, groupID) {
		return nil, service.ErrIQGroupInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(14020261104)`); err != nil {
		return nil, err
	}
	claimNow, err := iqClaimTimestamp(ctx, tx)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		WITH expired_runs AS (
			UPDATE openai_iq_runs
			SET status = 'error', finished_at = $1
			WHERE status = 'running' AND lease_expires_at IS NOT NULL AND lease_expires_at < $1
			RETURNING id
		)
		UPDATE openai_iq_results
		SET status = 'error', checked_at = $1, error = '执行进程未在租约内完成，本轮已终止，费用预留继续保留'
		WHERE status = 'pending' AND run_id IN (SELECT id FROM expired_runs)`, claimNow); err != nil {
		return nil, err
	}
	var activeRunID int64
	err = tx.QueryRowContext(ctx, `
		SELECT id FROM openai_iq_runs
		WHERE status = 'running' AND lease_expires_at >= $1
		ORDER BY started_at DESC LIMIT 1`, claimNow).Scan(&activeRunID)
	if err == nil {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return nil, service.ErrIQRunActive
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	var cooling bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM openai_iq_runs runs
			JOIN openai_iq_results results ON results.run_id = runs.id
			WHERE runs.trigger = $1 AND results.group_id = $2
			  AND runs.started_at > $3::timestamptz - INTERVAL '5 minutes'
		)`, service.IQTriggerManual, groupID, claimNow).Scan(&cooling); err != nil {
		return nil, err
	}
	if cooling {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return nil, service.ErrIQManualCooldown
	}
	run := &service.IQRun{Question: settings.Question, Answer: settings.ExpectedAnswer, Model: settings.Model, Trigger: service.IQTriggerManual}
	snapshot, err := json.Marshal(settings)
	if err != nil {
		return nil, err
	}
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO openai_iq_runs (started_at, question, expected_answer, model, settings_snapshot, lease_expires_at, trigger, requested_by)
		VALUES ($1, $2, $3, $4, $5::jsonb, $1::timestamptz + INTERVAL '10 minutes', $6, $7)
		RETURNING id, started_at`, claimNow, run.Question, run.Answer, run.Model, string(snapshot), run.Trigger, requestedBy).Scan(&run.ID, &run.StartedAt); err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO openai_iq_results (run_id, group_id, group_name, rate_multiplier, model, expected_answer, status)
		SELECT $1, id, name, rate_multiplier, $2, $3, 'pending'
		FROM groups
		WHERE id = $4 AND platform = $5 AND status = 'active' AND deleted_at IS NULL`,
		run.ID, run.Model, run.Answer, groupID, service.PlatformOpenAI)
	if err != nil {
		return nil, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if count != 1 {
		return nil, service.ErrIQGroupInvalid
	}
	if err := refreshIQRunLease(ctx, tx, run); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return run, nil
}

func iqClaimTimestamp(ctx context.Context, tx *sql.Tx) (time.Time, error) {
	var now time.Time
	err := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now)
	return now, err
}

// refreshIQRunLease makes the persistent lease start immediately before the
// claim commits, so lock waits and snapshot writes do not consume it.
func refreshIQRunLease(ctx context.Context, tx *sql.Tx, run *service.IQRun) error {
	now, err := iqClaimTimestamp(ctx, tx)
	if err != nil {
		return err
	}
	return tx.QueryRowContext(ctx, `
		UPDATE openai_iq_runs
		SET started_at = $2, lease_expires_at = $2::timestamptz + INTERVAL '10 minutes'
		WHERE id = $1
		RETURNING started_at`, run.ID, now).Scan(&run.StartedAt)
}

func containsIQGroup(groupIDs []int64, target int64) bool {
	for _, groupID := range groupIDs {
		if groupID == target {
			return true
		}
	}
	return false
}

// ReserveBudget records the worst-case request cost before dispatch. The
// daily lock covers reservations from all currently outstanding runs.
func (r *openAIIQRepository) ReserveBudget(ctx context.Context, runID, groupID int64, date time.Time, amount, dailyLimit float64) (bool, error) {
	if !isFiniteNonNegative(amount) || !isFiniteNonNegative(dailyLimit) || amount <= 0 || dailyLimit <= 0 || amount > dailyLimit {
		return false, fmt.Errorf("invalid OpenAI IQ budget reservation")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(14020261105)`); err != nil {
		return false, err
	}
	var allowed bool
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(COALESCE(actual_cost_usd, reserved_usd)), 0) + CEIL($2::numeric * 10000000000) / 10000000000 <= $3::numeric
		FROM openai_iq_budget_reservations
		WHERE budget_date = $1`, date.Format("2006-01-02"), amount, dailyLimit).Scan(&allowed); err != nil {
		return false, err
	}
	if !allowed {
		return false, tx.Commit()
	}
	var pending bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM openai_iq_results results
			JOIN openai_iq_runs runs ON runs.id = results.run_id
			WHERE results.run_id = $1 AND results.group_id = $2 AND results.status = 'pending'
			  AND runs.status = 'running' AND runs.lease_expires_at >= NOW())`, runID, groupID).Scan(&pending); err != nil {
		return false, err
	}
	if !pending {
		return false, tx.Commit()
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO openai_iq_budget_reservations (budget_date, run_id, group_id, reserved_usd)
		VALUES ($1, $2, $3, CEIL($4::numeric * 10000000000) / 10000000000)
		ON CONFLICT (run_id, group_id) DO NOTHING`, date.Format("2006-01-02"), runID, groupID, amount)
	if err != nil {
		return false, err
	}
	created, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if created != 1 {
		return false, tx.Commit()
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// BindBudgetRequest makes a durable reservation specific to exactly one
// dedicated IQ key and gateway request before any upstream bytes are sent.
func (r *openAIIQRepository) BindBudgetRequest(ctx context.Context, runID, groupID, keyID int64, requestID string) error {
	if r == nil || r.db == nil || requestID == "" || runID <= 0 || groupID <= 0 || keyID <= 0 {
		return fmt.Errorf("invalid OpenAI IQ budget binding")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(14020261105)`); err != nil {
		return err
	}
	var existingKey sql.NullInt64
	var existingRequest sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT key_id, request_id FROM openai_iq_budget_reservations WHERE run_id=$1 AND group_id=$2 FOR UPDATE`, runID, groupID).Scan(&existingKey, &existingRequest)
	if err != nil {
		return err
	}
	if existingRequest.Valid {
		if existingKey.Valid && existingKey.Int64 == keyID && existingRequest.String == requestID {
			return tx.Commit()
		}
		return fmt.Errorf("OpenAI IQ reservation is already bound")
	}
	result, err := tx.ExecContext(ctx, `UPDATE openai_iq_budget_reservations reservation
		SET key_id=$3, user_id=identity.user_id, request_id=$4
		FROM openai_iq_identity identity, openai_iq_keys keys, api_keys key
		WHERE reservation.run_id=$1 AND reservation.group_id=$2 AND reservation.request_id IS NULL
		  AND keys.group_id=reservation.group_id AND keys.key_id=$3 AND keys.user_id=identity.user_id
		  AND key.id=keys.key_id AND key.user_id=identity.user_id AND key.group_id=reservation.group_id`, runID, groupID, keyID, requestID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("OpenAI IQ key does not own this reservation")
	}
	return tx.Commit()
}

// ReconcileBudget replaces an outstanding hold only when one authoritative IQ
// usage row uniquely proves its immutable key, user, group and correlation.
func (r *openAIIQRepository) ReconcileBudget(ctx context.Context) error {
	if r == nil || r.db == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(14020261105)`); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `WITH candidates AS (
		SELECT reservation.id, usage.id AS usage_id, usage.account_id,
			COALESCE(usage.account_stats_cost, usage.total_cost) AS base,
			COALESCE(usage.account_rate_multiplier, 1) AS rate,
			COUNT(*) OVER (PARTITION BY reservation.id) AS matches,
			COUNT(*) OVER (PARTITION BY usage.id) AS owners
		FROM openai_iq_budget_reservations reservation
		JOIN usage_logs usage ON usage.request_id = reservation.request_id
			AND usage.api_key_id = reservation.key_id AND usage.user_id = reservation.user_id AND usage.group_id = reservation.group_id
		WHERE reservation.request_id IS NOT NULL AND reservation.actual_cost_usd IS NULL
			AND usage.user_agent = 'sub2api-openai-iq'
			AND NOT EXISTS (SELECT 1 FROM openai_iq_budget_reservations used WHERE used.usage_log_id = usage.id)
	) UPDATE openai_iq_budget_reservations reservation
		SET actual_cost_usd=CEIL((candidate.base*candidate.rate)::numeric*10000000000)/10000000000,
			usage_log_id=candidate.usage_id, account_id=candidate.account_id,
			account_base_cost_usd=candidate.base, account_rate_multiplier=candidate.rate, settled_at=NOW()
		FROM candidates candidate WHERE reservation.id=candidate.id AND candidate.matches=1 AND candidate.owners=1
			AND candidate.base >= 0 AND candidate.base < 'Infinity'::numeric
			AND candidate.rate >= 0 AND candidate.rate < 'Infinity'::numeric
			AND candidate.base*candidate.rate >= 0 AND candidate.base*candidate.rate < 'Infinity'::numeric`)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func isFiniteNonNegative(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}
