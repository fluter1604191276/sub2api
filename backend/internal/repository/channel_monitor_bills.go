package repository

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"time"
)

func (r *channelMonitorRepository) DailyBills(ctx context.Context, start, end time.Time) ([]service.ChannelMonitorDailyBill, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT to_char(bill_date, 'YYYY-MM-DD'), base_cost_usd,
        checks, unknown_cost_checks, failed_checks, historical_partial
        FROM channel_monitor_daily_bills WHERE bill_date >= $1::date AND bill_date <= $2::date
        ORDER BY bill_date DESC`, start.Format("2006-01-02"), end.Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]service.ChannelMonitorDailyBill, 0)
	for rows.Next() {
		var row service.ChannelMonitorDailyBill
		if err := rows.Scan(&row.Date, &row.BaseCostUSD, &row.Checks, &row.UnknownCostChecks,
			&row.FailedChecks, &row.HistoricalPartial); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}
