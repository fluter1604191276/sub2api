package repository

import (
	"context"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *openAIIQRepository) RunResults(ctx context.Context, runID int64) ([]service.IQResult, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT group_id, group_name, rate_multiplier, model, expected_answer
		FROM openai_iq_results WHERE run_id = $1 AND status = 'pending'
		ORDER BY group_id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results := make([]service.IQResult, 0)
	for rows.Next() {
		var result service.IQResult
		if err := rows.Scan(&result.GroupID, &result.GroupName, &result.RateMultiplier, &result.Model, &result.ExpectedAnswer); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, rows.Err()
}

func (r *openAIIQRepository) CompleteResult(ctx context.Context, runID int64, result service.IQResult) error {
	switch result.Status {
	case "correct", "incorrect", "error", "skipped":
	default:
		return fmt.Errorf("invalid IQ completion status")
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE openai_iq_results results
		SET response_model = $3, model_answer = $4, output = $5, status = $6,
		    checked_at = NOW(), error = $7
		WHERE results.run_id = $1 AND results.group_id = $2 AND results.status = 'pending'
		  AND EXISTS (SELECT 1 FROM openai_iq_runs runs
		              WHERE runs.id = results.run_id AND runs.status = 'running')`,
		runID, result.GroupID, result.ResponseModel, result.ModelAnswer, result.Output, result.Status, result.Error)
	return err
}

func (r *openAIIQRepository) FinishRun(ctx context.Context, runID int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		UPDATE openai_iq_results SET status = 'skipped', checked_at = NOW(),
		    error = '本轮未执行，检测已关闭或执行时间已到上限'
		WHERE run_id = $1 AND status = 'pending'`, runID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE openai_iq_runs SET status = 'complete', finished_at = NOW()
		WHERE id = $1 AND status = 'running'`, runID); err != nil {
		return err
	}
	return tx.Commit()
}
