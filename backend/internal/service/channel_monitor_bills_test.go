package service

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

type dailyBillsStub struct {
	ChannelMonitorRepository
	start, end time.Time
	err        error
}

func (r *dailyBillsStub) DailyBills(_ context.Context, start, end time.Time) ([]ChannelMonitorDailyBill, error) {
	r.start, r.end = start, end
	return []ChannelMonitorDailyBill{}, r.err
}
func TestMonitorBillsBeijingMidnight(t *testing.T) {
	repo := &dailyBillsStub{}
	s := &ChannelMonitorService{repo: repo}
	for _, tc := range []struct{ utc, date string }{
		{"2026-09-10T15:59:59Z", "2026-09-10"},
		{"2026-09-10T16:00:00Z", "2026-09-11"},
	} {
		now, err := time.Parse(time.RFC3339, tc.utc)
		require.NoError(t, err)
		bill, err := s.dailyBillsAt(context.Background(), 1, now)
		require.NoError(t, err)
		require.Equal(t, tc.date+"T00:00:00+08:00", repo.start.Format(time.RFC3339))
		require.True(t, repo.end.Equal(now))
		require.Equal(t, "Asia/Shanghai", bill.Timezone)
	}
	_, err := s.dailyBillsAt(context.Background(), 366, time.Now())
	require.Error(t, err)
	repo.err = errors.New("database unavailable")
	_, err = s.DailyBills(context.Background(), 30)
	require.ErrorContains(t, err, "database unavailable")
}
