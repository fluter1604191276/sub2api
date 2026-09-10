package service

import (
	"context"
	"fmt"
	"time"
)

type ChannelMonitorDailyBill struct {
	Date              string  `json:"date"`
	BaseCostUSD       float64 `json:"base_cost_usd"`
	Checks            int64   `json:"checks"`
	UnknownCostChecks int64   `json:"unknown_cost_checks"`
	FailedChecks      int64   `json:"failed_checks"`
	HistoricalPartial bool    `json:"historical_partial"`
}

type ChannelMonitorBills struct {
	Timezone    string                    `json:"timezone"`
	AsOf        time.Time                 `json:"as_of"`
	PeriodStart time.Time                 `json:"period_start"`
	Days        []ChannelMonitorDailyBill `json:"days"`
}

type channelMonitorBillsRepository interface {
	DailyBills(context.Context, time.Time, time.Time) ([]ChannelMonitorDailyBill, error)
}

func (s *ChannelMonitorService) DailyBills(ctx context.Context, days int) (*ChannelMonitorBills, error) {
	return s.dailyBillsAt(ctx, days, time.Now())
}

func (s *ChannelMonitorService) dailyBillsAt(ctx context.Context, days int, now time.Time) (*ChannelMonitorBills, error) {
	if days < 1 || days > 365 {
		return nil, fmt.Errorf("bill range must be between 1 and 365 days")
	}
	zone, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return nil, err
	}
	now = now.In(zone)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, zone)
	start := today.AddDate(0, 0, 1-days)
	repo, ok := s.repo.(channelMonitorBillsRepository)
	if !ok {
		return nil, fmt.Errorf("channel monitor daily billing is unavailable")
	}
	rows, err := repo.DailyBills(ctx, start, now)
	if err != nil {
		return nil, err
	}
	// Missing days mean no retained evidence, not proof of free probes.
	return &ChannelMonitorBills{Timezone: "Asia/Shanghai", AsOf: now, PeriodStart: start, Days: rows}, nil
}
