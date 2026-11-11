package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type channelMonitorV3Repository struct{ db *sql.DB }

func NewChannelMonitorV3Repository(db *sql.DB) service.ChannelMonitorV3Repository {
	return &channelMonitorV3Repository{db: db}
}

func (r *channelMonitorV3Repository) Settings(ctx context.Context) (service.ChannelMonitorV3Settings, error) {
	out := service.DefaultChannelMonitorV3Settings()
	err := r.db.QueryRowContext(ctx, `SELECT enabled,daily_budget_usd FROM channel_monitor_v3_settings WHERE singleton=TRUE`).Scan(&out.Enabled, &out.DailyBudgetUSD)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	return out, err
}

func (r *channelMonitorV3Repository) SaveSettings(ctx context.Context, in service.ChannelMonitorV3Settings) (service.ChannelMonitorV3Settings, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return in, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, channelMonitorV3IdentityLock); err != nil {
		return in, err
	}
	if in.Enabled {
		if _, err = ensureChannelMonitorV3Identity(ctx, tx); err != nil {
			return in, err
		}
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO channel_monitor_v3_settings(singleton,enabled,daily_budget_usd,updated_at)
		VALUES(TRUE,$1,$2,NOW()) ON CONFLICT(singleton) DO UPDATE
		SET enabled=EXCLUDED.enabled,daily_budget_usd=EXCLUDED.daily_budget_usd,updated_at=NOW()
		RETURNING enabled,daily_budget_usd`, in.Enabled, in.DailyBudgetUSD).Scan(&in.Enabled, &in.DailyBudgetUSD)
	if err != nil {
		return in, err
	}
	if err = tx.Commit(); err != nil {
		return in, err
	}
	return in, nil
}

func (r *channelMonitorV3Repository) ListGroups(ctx context.Context) ([]service.ChannelMonitorV3GroupConfig, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT g.id,g.name,g.platform,g.status,
		COALESCE(c.visible,FALSE),COALESCE(c.probe_interval_seconds,600),COALESCE(c.test_model,'')
		FROM groups g LEFT JOIN channel_monitor_v3_groups c ON c.group_id=g.id
		WHERE g.deleted_at IS NULL ORDER BY g.sort_order,g.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []service.ChannelMonitorV3GroupConfig{}
	for rows.Next() {
		var group service.ChannelMonitorV3GroupConfig
		if err := rows.Scan(&group.GroupID, &group.Name, &group.Platform, &group.Status, &group.Visible, &group.ProbeIntervalSeconds, &group.TestModel); err != nil {
			return nil, err
		}
		out = append(out, group)
	}
	return out, rows.Err()
}

func (r *channelMonitorV3Repository) SaveGroup(ctx context.Context, in service.ChannelMonitorV3GroupConfig) (*service.ChannelMonitorV3GroupConfig, error) {
	var out service.ChannelMonitorV3GroupConfig
	err := r.db.QueryRowContext(ctx, `WITH saved AS (
		INSERT INTO channel_monitor_v3_groups(group_id,visible,probe_interval_seconds,test_model,updated_at)
		SELECT id,$2,$3,$4,NOW() FROM groups WHERE id=$1 AND deleted_at IS NULL
		ON CONFLICT(group_id) DO UPDATE SET visible=EXCLUDED.visible,
		probe_interval_seconds=EXCLUDED.probe_interval_seconds,test_model=EXCLUDED.test_model,updated_at=NOW()
		RETURNING group_id,visible,probe_interval_seconds,test_model
	) SELECT g.id,g.name,g.platform,g.status,s.visible,s.probe_interval_seconds,s.test_model
	FROM saved s JOIN groups g ON g.id=s.group_id`, in.GroupID, in.Visible, in.ProbeIntervalSeconds, in.TestModel).
		Scan(&out.GroupID, &out.Name, &out.Platform, &out.Status, &out.Visible, &out.ProbeIntervalSeconds, &out.TestModel)
	return &out, err
}
