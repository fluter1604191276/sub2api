package repository

import (
	"context"
	"database/sql"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

var _ service.AccountPriorityConversionRepository = (*accountRepository)(nil)

func (r *accountRepository) GetPriorityConversionAccounts(ctx context.Context, ids []int64) ([]service.AccountPriorityConversionRow, error) {
	rows, err := r.sql.QueryContext(ctx, `SELECT id, name, priority, rate_multiplier::text
		FROM accounts WHERE id = ANY($1) AND deleted_at IS NULL`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make([]service.AccountPriorityConversionRow, 0, len(ids))
	for rows.Next() {
		var row service.AccountPriorityConversionRow
		var multiplier sql.NullString
		if err := rows.Scan(&row.AccountID, &row.Name, &row.OldPriority, &multiplier); err != nil {
			return nil, err
		}
		if multiplier.Valid {
			row.RateMultiplier = &multiplier.String
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// UpdatePriorityIfUnchanged changes only priority and its modification time.
// A stale preview or deleted account makes no write. The existing scheduler
// outbox is committed atomically; this does not alter scheduling policy.
func (r *accountRepository) UpdatePriorityIfUnchanged(ctx context.Context, id int64, multiplier string, oldPriority, newPriority int) (bool, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	exec := tx.Client()
	result, err := exec.ExecContext(ctx, `UPDATE accounts SET priority = $1, updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL AND rate_multiplier = $3::numeric AND priority = $4`, newPriority, id, multiplier, oldPriority)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if rows == 0 {
		return false, nil
	}
	if err := enqueueSchedulerOutbox(ctx, exec, service.SchedulerOutboxEventAccountChanged, &id, nil, nil); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
