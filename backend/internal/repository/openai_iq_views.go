package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// LatestValid returns one user-safe snapshot for every model/group pair. A
// later failed or skipped attempt cannot hide a completed answer.
func (r *openAIIQRepository) LatestValid(ctx context.Context, allowed, configured []int64, currentModel, currentQuestion, currentAnswer string) ([]service.IQPublicResult, error) {
	if r == nil || r.db == nil || len(allowed) == 0 {
		return []service.IQPublicResult{}, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		WITH visible_groups AS (
			SELECT id, name, rate_multiplier FROM groups
			WHERE id = ANY($1) AND platform = 'openai' AND status = 'active' AND deleted_at IS NULL
		), pairs AS (
			SELECT DISTINCT result.group_id, result.model
			FROM openai_iq_results result JOIN visible_groups group_row ON group_row.id = result.group_id
		), latest AS (
			SELECT DISTINCT ON (result.group_id, result.model)
				result.group_id, result.model, run.question, result.expected_answer, result.model_answer, result.output, result.status, result.checked_at
			FROM openai_iq_results result JOIN openai_iq_runs run ON run.id = result.run_id
			JOIN visible_groups group_row ON group_row.id = result.group_id
			WHERE result.status IN ('correct', 'incorrect') AND result.checked_at IS NOT NULL
			ORDER BY result.group_id, result.model, result.checked_at DESC, result.id DESC
		)
		SELECT pairs.group_id, group_row.name, group_row.rate_multiplier, pairs.model,
			COALESCE(latest.question, ''), COALESCE(latest.expected_answer, ''), COALESCE(latest.model_answer, ''), COALESCE(latest.output, ''),
			COALESCE(latest.status, 'no_results'), latest.checked_at
		FROM pairs JOIN visible_groups group_row ON group_row.id = pairs.group_id
		LEFT JOIN latest ON latest.group_id = pairs.group_id AND latest.model = pairs.model
		UNION ALL
		SELECT group_row.id, group_row.name, group_row.rate_multiplier, $3::text, $4::text, $5::text, '', '', 'no_results', NULL
		FROM visible_groups group_row JOIN (SELECT unnest($2::bigint[]) AS id) configured_group ON configured_group.id=group_row.id
		WHERE NOT EXISTS (SELECT 1 FROM pairs WHERE pairs.group_id = group_row.id AND pairs.model = $3::text)
		ORDER BY 2, 4`, pq.Array(allowed), pq.Array(configured), currentModel, currentQuestion, currentAnswer)
	if err != nil {
		return nil, fmt.Errorf("read latest OpenAI IQ results: %w", err)
	}
	defer rows.Close()
	out := make([]service.IQPublicResult, 0)
	for rows.Next() {
		var item service.IQPublicResult
		if err := rows.Scan(&item.GroupID, &item.GroupName, &item.RateMultiplier, &item.Model, &item.Question, &item.ExpectedAnswer, &item.ModelAnswer, &item.Output, &item.Status, &item.CheckedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *openAIIQRepository) Diagnostics(ctx context.Context, page, pageSize int) (*service.IQDiagnosticsPage, error) {
	if page < 1 || page > service.MaxIQDiagnosticsPage || pageSize < 1 || pageSize > service.MaxIQDiagnosticsPageSize {
		return nil, fmt.Errorf("invalid OpenAI IQ diagnostics pagination")
	}
	out := &service.IQDiagnosticsPage{Runs: []service.IQDiagnosticRun{}, Page: page, PageSize: pageSize}
	if r == nil || r.db == nil {
		return out, nil
	}
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM openai_iq_runs`).Scan(&out.Total); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, started_at, finished_at, question, expected_answer, model, status, trigger, requested_by,
		       status = 'running' AND lease_expires_at IS NOT NULL AND lease_expires_at >= NOW() AS lease_active
		FROM openai_iq_runs
		ORDER BY started_at DESC, id DESC LIMIT $1 OFFSET $2`, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var run service.IQDiagnosticRun
		if err := rows.Scan(&run.ID, &run.StartedAt, &run.FinishedAt, &run.Question, &run.ExpectedAnswer, &run.Model, &run.Status, &run.Trigger, &run.RequestedBy, &run.LeaseActive); err != nil {
			return nil, err
		}
		run.Results = []service.IQDiagnosticResult{}
		out.Runs = append(out.Runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out.Runs {
		rows, err := r.db.QueryContext(ctx, `SELECT result.id, result.run_id, result.group_id, result.group_name, result.rate_multiplier, result.model, result.response_model, result.expected_answer, result.model_answer, result.output, result.status, result.checked_at, result.error, reservation.reserved_usd, reservation.actual_cost_usd, reservation.request_id, reservation.usage_log_id, reservation.account_id, reservation.settled_at FROM openai_iq_results result LEFT JOIN openai_iq_budget_reservations reservation ON reservation.run_id=result.run_id AND reservation.group_id=result.group_id WHERE result.run_id=$1 ORDER BY result.id`, out.Runs[i].ID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var item service.IQDiagnosticResult
			var reservation service.IQDiagnosticReservation
			var reserved sql.NullFloat64
			if err := rows.Scan(&item.ID, &item.RunID, &item.GroupID, &item.GroupName, &item.RateMultiplier, &item.Model, &item.ResponseModel, &item.ExpectedAnswer, &item.ModelAnswer, &item.Output, &item.Status, &item.CheckedAt, &item.Error, &reserved, &reservation.ActualCostUSD, &reservation.RequestID, &reservation.UsageLogID, &reservation.AccountID, &reservation.SettledAt); err != nil {
				_ = rows.Close()
				return nil, err
			}
			if reserved.Valid {
				reservation.ReservedUSD = reserved.Float64
				item.Reservation = &reservation
			}
			out.Runs[i].Results = append(out.Runs[i].Results, item)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	return out, nil
}
