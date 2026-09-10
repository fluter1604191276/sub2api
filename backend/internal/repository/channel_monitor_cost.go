package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (r *channelMonitorRepository) MonitorBillingKeyID(ctx context.Context, key string) (int64, error) {
	if key == "" {
		return 0, nil
	}
	var id int64
	err := r.db.QueryRowContext(ctx, `SELECT id FROM api_keys WHERE key=$1 AND deleted_at IS NULL`, key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// Resolve from immutable gateway usage snapshots, not current account settings
// or user-facing actual_cost. Advisory lock serializes concurrent bill refreshes.
func (r *channelMonitorRepository) reconcileMonitorCosts(ctx context.Context, start, end time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(724193521)`); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `WITH candidates AS (
        SELECT c.history_id, u.id AS usage_id, u.account_id,
            COALESCE(u.account_stats_cost,u.total_cost) AS base,
            COALESCE(u.account_rate_multiplier,1) AS rate,
            count(*) OVER (PARTITION BY c.history_id) AS matches,
            count(*) OVER (PARTITION BY u.id) AS owners
        FROM channel_monitor_cost_records c JOIN usage_logs u
          ON u.request_id=c.request_id AND u.api_key_id=c.api_key_id
        WHERE c.reconciled_at IS NULL AND c.checked_at >= $1 AND c.checked_at <= $2
          AND u.created_at >= c.checked_at - interval '5 minutes'
          AND u.created_at <= c.checked_at + interval '1 hour'
          AND u.user_agent LIKE '%sub2api-channel-monitor/1%'
          AND NOT EXISTS (SELECT 1 FROM channel_monitor_cost_records used WHERE used.usage_log_id=u.id)
    ) UPDATE channel_monitor_cost_records c SET usage_log_id=x.usage_id,
        account_id=x.account_id, account_base_cost_usd=x.base, account_rate_multiplier=x.rate,
        account_cost_usd=round((x.base*x.rate)::numeric,8), reconciled_at=NOW()
      FROM candidates x WHERE c.history_id=x.history_id AND x.matches=1 AND x.owners=1
        AND x.base >= 0 AND x.base < 'Infinity'::numeric
        AND x.rate >= 0 AND x.rate < 'Infinity'::numeric
        AND x.base*x.rate < 'Infinity'::numeric`, start, end)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (r *channelMonitorRepository) ReconcileMonitorCosts(ctx context.Context) error {
	now := time.Now()
	return r.reconcileMonitorCosts(ctx, now.AddDate(0, 0, -30), now)
}
