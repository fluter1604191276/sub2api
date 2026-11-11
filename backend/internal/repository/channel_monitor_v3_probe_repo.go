package repository

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

const channelMonitorV3RealTrafficSQL = `SELECT EXISTS(
 SELECT 1 FROM usage_logs u WHERE u.group_id=$1 AND u.created_at >= $2 AND u.request_type<>6
 AND LOWER(COALESCE(u.user_agent,'')) NOT LIKE '%sub2api-channel-monitor/%'
 AND u.user_id NOT IN (SELECT user_id FROM channel_monitor_v3_identity WHERE singleton=TRUE)
 AND u.user_id NOT IN (SELECT user_id FROM openai_iq_identity WHERE singleton=TRUE)
 UNION ALL
 SELECT 1 FROM upstream_attempts a WHERE a.group_id=$1 AND a.created_at >= $2 AND a.source='request'
 AND COALESCE(a.user_id,0) NOT IN (SELECT user_id FROM channel_monitor_v3_identity WHERE singleton=TRUE)
 AND COALESCE(a.user_id,0) NOT IN (SELECT user_id FROM openai_iq_identity WHERE singleton=TRUE)
 AND NOT EXISTS (SELECT 1 FROM usage_logs u WHERE u.group_id=a.group_id AND (u.request_id=a.request_id OR u.request_id='client:'||a.client_request_id) AND LOWER(COALESCE(u.user_agent,'')) LIKE '%sub2api-channel-monitor/%')
)`

const channelMonitorV3BudgetUsedSQL = `SELECT COALESCE(SUM(CASE WHEN status='settled' THEN actual_cost_usd ELSE GREATEST(reserved_usd,COALESCE(actual_cost_usd,0)) END),0) FROM channel_monitor_v3_probe_dispatches WHERE budget_day=$1`

func (r *channelMonitorV3Repository) ListPendingProbes(ctx context.Context) ([]service.ChannelMonitorV3ProbeJob, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT dispatch_id::text,client_request_id,group_id,model,platform,budget_day::text,reserved_usd,created_at,updated_at FROM channel_monitor_v3_probe_dispatches WHERE status='completed' ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []service.ChannelMonitorV3ProbeJob
	for rows.Next() {
		var j service.ChannelMonitorV3ProbeJob
		if err := rows.Scan(&j.DispatchID, &j.ClientRequestID, &j.GroupID, &j.Model, &j.Platform, &j.BudgetDay, &j.ReservedUSD, &j.CreatedAt, &j.CompletedAt); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func (r *channelMonitorV3Repository) TryClaimProbe(ctx context.Context, group service.ChannelMonitorV3GroupConfig, now time.Time, limit float64) (*service.ChannelMonitorV3ProbeJob, error) {
	if !group.Visible || group.Status != service.StatusActive || group.ProbeIntervalSeconds == 0 || group.TestModel == "" || !finiteProbeMoney(limit) {
		return nil, nil
	}
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return nil, err
	}
	day := now.In(loc).Format("2006-01-02")
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(315000001)`); err != nil {
		return nil, err
	}
	var enabled bool
	var currentLimit float64
	if err = tx.QueryRowContext(ctx, `SELECT enabled,daily_budget_usd FROM channel_monitor_v3_settings WHERE singleton=TRUE FOR UPDATE`).Scan(&enabled, &currentLimit); err != nil || !enabled {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if currentLimit < limit {
		limit = currentLimit
	}
	var last *time.Time
	var active, visible bool
	var interval int
	var model, platform string
	if err = tx.QueryRowContext(ctx, `SELECT g.status='active',c.visible,c.probe_interval_seconds,c.last_probe_at,c.test_model,g.platform FROM groups g JOIN channel_monitor_v3_groups c ON c.group_id=g.id WHERE g.id=$1 AND g.deleted_at IS NULL FOR UPDATE`, group.GroupID).Scan(&active, &visible, &interval, &last, &model, &platform); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if !active || !visible || interval != group.ProbeIntervalSeconds || model != group.TestModel || platform != group.Platform || (last != nil && now.Sub(*last) < time.Duration(interval)*time.Second) {
		return nil, nil
	}
	var real bool
	if err = tx.QueryRowContext(ctx, channelMonitorV3RealTrafficSQL, group.GroupID, now.Add(-time.Duration(interval)*time.Second)).Scan(&real); err != nil {
		return nil, err
	}
	if real {
		return nil, nil
	}
	var reserved float64
	if err = tx.QueryRowContext(ctx, channelMonitorV3BudgetUsedSQL, day).Scan(&reserved); err != nil {
		return nil, err
	}
	if reserved >= limit {
		return nil, nil
	}
	id := uuid.NewString()
	client := "cmv3-" + id
	hold := limit - reserved
	if _, err = tx.ExecContext(ctx, `INSERT INTO channel_monitor_v3_probe_dispatches(dispatch_id,group_id,model,platform,budget_day,reserved_usd,client_request_id,status) VALUES($1,$2,$3,$4,$5,$6,$7,'claimed')`, id, group.GroupID, group.TestModel, group.Platform, day, hold, client); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE channel_monitor_v3_groups SET last_probe_at=$2,updated_at=NOW() WHERE group_id=$1`, group.GroupID, now); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &service.ChannelMonitorV3ProbeJob{DispatchID: id, ClientRequestID: client, GroupID: group.GroupID, Model: group.TestModel, Platform: group.Platform, BudgetDay: day, ReservedUSD: hold, CreatedAt: now}, nil
}

func (r *channelMonitorV3Repository) ReleaseUndispatchedProbe(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE channel_monitor_v3_probe_dispatches SET status='undispatched',reserved_usd=0,updated_at=NOW() WHERE dispatch_id=$1::uuid AND status='claimed'`, id)
	return err
}
func (r *channelMonitorV3Repository) MarkProbeDispatched(ctx context.Context, id string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(315000001)`); err != nil {
		return err
	}
	var enabled bool
	var limit float64
	if err = tx.QueryRowContext(ctx, `SELECT enabled,daily_budget_usd FROM channel_monitor_v3_settings WHERE singleton=TRUE FOR UPDATE`).Scan(&enabled, &limit); err != nil {
		return err
	}
	var day, status string
	if err = tx.QueryRowContext(ctx, `SELECT budget_day::text,status FROM channel_monitor_v3_probe_dispatches WHERE dispatch_id=$1::uuid FOR UPDATE`, id).Scan(&day, &status); err != nil {
		return err
	}
	if status != "claimed" {
		return fmt.Errorf("probe dispatch claim is no longer available")
	}
	var occupied float64
	if err = tx.QueryRowContext(ctx, channelMonitorV3BudgetUsedSQL, day).Scan(&occupied); err != nil {
		return err
	}
	// This transaction is the send-admission point. Serialize it with settings,
	// claims and settlements, and release an obsolete, proven-unsent claim.
	if !enabled || !finiteProbeMoney(limit) || occupied > limit || day != time.Now().In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("2006-01-02") {
		if _, err = tx.ExecContext(ctx, `UPDATE channel_monitor_v3_probe_dispatches SET status='undispatched',reserved_usd=0,updated_at=NOW() WHERE dispatch_id=$1::uuid`, id); err != nil {
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		return fmt.Errorf("probe dispatch settings or budget changed before admission")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE channel_monitor_v3_probe_dispatches SET status='sent',sent_at=NOW(),updated_at=NOW() WHERE dispatch_id=$1::uuid`, id); err != nil {
		return err
	}
	return tx.Commit()
}
func (r *channelMonitorV3Repository) FinishProbe(ctx context.Context, id string, completed bool) error {
	status := "uncertain"
	if completed {
		status = "completed"
	}
	_, err := r.db.ExecContext(ctx, `UPDATE channel_monitor_v3_probe_dispatches SET status=$2,updated_at=NOW() WHERE dispatch_id=$1::uuid AND status='sent'`, id, status)
	return err
}
func (r *channelMonitorV3Repository) SettleProbe(ctx context.Context, id string, c service.UpstreamAttemptCostSummary) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(315000001)`); err != nil {
		return err
	}
	var reserved float64
	var status string
	if err = tx.QueryRowContext(ctx, `SELECT reserved_usd,status FROM channel_monitor_v3_probe_dispatches WHERE dispatch_id=$1::uuid FOR UPDATE`, id).Scan(&reserved, &status); err != nil {
		return err
	}
	if status != "completed" {
		return tx.Commit()
	}
	status = "settled"
	validCost := finiteProbeMoney(c.KnownCostUSD)
	if c.AttemptCount <= 0 || c.UnknownCount > 0 || !validCost || c.KnownCostUSD > reserved {
		status = "uncertain"
	}
	// Keep any known portion even if another attempt has unknown usage. The
	// durable hold is never released by incomplete evidence or an overrun.
	var actual any
	if validCost && (status == "settled" || c.KnownCostUSD > 0) {
		actual = c.KnownCostUSD
	}
	_, err = tx.ExecContext(ctx, `UPDATE channel_monitor_v3_probe_dispatches SET status=$2::text,actual_cost_usd=$3,settled_at=CASE WHEN $2::text='settled' THEN NOW() ELSE NULL END,updated_at=NOW() WHERE dispatch_id=$1::uuid`, id, status, actual)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if validCost && c.KnownCostUSD > reserved {
		slog.Error("channel_monitor_v3_probe_cost_exceeds_reservation", "dispatch_id", id, "actual_usd", c.KnownCostUSD, "reserved_usd", reserved)
	}
	return nil
}
func (r *channelMonitorV3Repository) HasRealTrafficSince(ctx context.Context, gid int64, since time.Time) (bool, error) {
	var yes bool
	err := r.db.QueryRowContext(ctx, channelMonitorV3RealTrafficSQL, gid, since).Scan(&yes)
	return yes, err
}
func (r *channelMonitorV3Repository) ProbeBudget(ctx context.Context, day string, limit float64) (service.ChannelMonitorV3Budget, error) {
	var b service.ChannelMonitorV3Budget
	b.Date = day
	err := r.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(actual_cost_usd),0),COALESCE(SUM(GREATEST(reserved_usd-COALESCE(actual_cost_usd,0),0)) FILTER(WHERE status<>'settled'),0) FROM channel_monitor_v3_probe_dispatches WHERE budget_day=$1`, day).Scan(&b.SpentUSD, &b.ReservedUSD)
	b.RemainingUSD = math.Max(0, limit-b.SpentUSD-b.ReservedUSD)
	return b, err
}
func finiteProbeMoney(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 }

var _ service.ChannelMonitorV3ProbeRepository = (*channelMonitorV3Repository)(nil)
