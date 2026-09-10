package repository

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"time"
)

func (r *channelMonitorRepository) DailyBills(ctx context.Context, start, end time.Time) ([]service.ChannelMonitorDailyBill, error) {
	if err := r.reconcileMonitorCosts(ctx, start, end); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT to_char(bill_date, 'YYYY-MM-DD'), base_cost_usd,
        checks, unknown_cost_checks, failed_checks, historical_partial
        , costs.cost, COALESCE(costs.costed,0)
        FROM channel_monitor_daily_bills b LEFT JOIN LATERAL (
          SELECT sum(account_cost_usd) AS cost, count(*) AS costed
          FROM channel_monitor_cost_records c
          WHERE c.checked_at >= (b.bill_date::timestamp AT TIME ZONE 'Asia/Shanghai')
            AND c.checked_at < ((b.bill_date+1)::timestamp AT TIME ZONE 'Asia/Shanghai')
            AND c.checked_at <= $3 AND c.reconciled_at IS NOT NULL
        ) costs ON TRUE
        WHERE bill_date >= $1::date AND bill_date <= $2::date
        ORDER BY bill_date DESC`, start.Format("2006-01-02"), end.Format("2006-01-02"), end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]service.ChannelMonitorDailyBill, 0)
	for rows.Next() {
		var row service.ChannelMonitorDailyBill
		if err := rows.Scan(&row.Date, &row.BaseCostUSD, &row.Checks, &row.UnknownCostChecks,
			&row.FailedChecks, &row.HistoricalPartial, &row.AccountCostUSD, &row.CostedChecks); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}
