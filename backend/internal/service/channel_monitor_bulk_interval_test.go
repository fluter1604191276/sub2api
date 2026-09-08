//go:build unit

package service

import (
	"context"
	"reflect"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type bulkIntervalRepoStub struct {
	ChannelMonitorRepository
	ids      []int64
	interval int
	updated  []*ChannelMonitor
	err      error
}

func (r *bulkIntervalRepoStub) BulkUpdateInterval(_ context.Context, ids []int64, intervalSeconds int) ([]*ChannelMonitor, error) {
	r.ids = append([]int64(nil), ids...)
	r.interval = intervalSeconds
	return r.updated, r.err
}

type bulkIntervalSchedulerStub struct {
	monitors []*ChannelMonitor
}

func (s *bulkIntervalSchedulerStub) Schedule(m *ChannelMonitor) {
	s.monitors = append(s.monitors, m)
}

func (s *bulkIntervalSchedulerStub) Unschedule(int64) {}

type bulkIntervalEncryptorStub struct{}

func (bulkIntervalEncryptorStub) Encrypt(value string) (string, error) { return "enc:" + value, nil }
func (bulkIntervalEncryptorStub) Decrypt(value string) (string, error) { return "plain:" + value, nil }

func TestBulkUpdateIntervalDeduplicatesIDsAndReschedulesMonitors(t *testing.T) {
	repo := &bulkIntervalRepoStub{
		updated: []*ChannelMonitor{
			{ID: 7, APIKey: "cipher-7", Enabled: true},
			{ID: 3, APIKey: "cipher-3", Enabled: false},
		},
	}
	scheduler := &bulkIntervalSchedulerStub{}
	svc := NewChannelMonitorService(repo, bulkIntervalEncryptorStub{})
	svc.SetScheduler(scheduler)

	affected, err := svc.BulkUpdateInterval(context.Background(), []int64{7, 7, 0, -3, 3}, 15)
	if err != nil {
		t.Fatalf("BulkUpdateInterval returned error: %v", err)
	}
	if affected != 2 {
		t.Fatalf("expected 2 affected monitors, got %d", affected)
	}
	if !reflect.DeepEqual(repo.ids, []int64{7, 3}) {
		t.Fatalf("expected de-duplicated IDs [7 3], got %v", repo.ids)
	}
	if repo.interval != 15 {
		t.Fatalf("expected interval 15, got %d", repo.interval)
	}
	if len(scheduler.monitors) != 2 {
		t.Fatalf("expected 2 scheduler updates, got %d", len(scheduler.monitors))
	}
	if scheduler.monitors[0].APIKey != "plain:cipher-7" || scheduler.monitors[1].APIKey != "plain:cipher-3" {
		t.Fatalf("expected decrypted API keys before scheduling, got %#v", scheduler.monitors)
	}
}

func TestBulkUpdateIntervalRejectsInvalidBatchBeforeRepository(t *testing.T) {
	tests := []struct {
		name string
		ids  []int64
		want string
	}{
		{name: "empty", ids: []int64{0, -1}, want: "CHANNEL_MONITOR_BATCH_EMPTY"},
		{name: "too large", ids: makeIDs(maxChannelMonitorBulkUpdateIDs + 1), want: "CHANNEL_MONITOR_BATCH_TOO_LARGE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &bulkIntervalRepoStub{}
			svc := NewChannelMonitorService(repo, bulkIntervalEncryptorStub{})
			_, err := svc.BulkUpdateInterval(context.Background(), tt.ids, 15)
			if err == nil || !containsErrorCode(err, tt.want) {
				t.Fatalf("expected error code %s, got %v", tt.want, err)
			}
			if repo.ids != nil {
				t.Fatalf("repository should not be called for invalid batch, got %v", repo.ids)
			}
		})
	}
}

func TestBulkUpdateIntervalRejectsInvalidIntervalBeforeRepository(t *testing.T) {
	repo := &bulkIntervalRepoStub{}
	svc := NewChannelMonitorService(repo, bulkIntervalEncryptorStub{})
	if _, err := svc.BulkUpdateInterval(context.Background(), []int64{1}, 14); err == nil {
		t.Fatal("expected invalid interval error")
	}
	if repo.ids != nil {
		t.Fatalf("repository should not be called for invalid interval, got %v", repo.ids)
	}
}

func makeIDs(count int) []int64 {
	ids := make([]int64, count)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	return ids
}

func containsErrorCode(err error, code string) bool {
	return err != nil && infraerrors.Reason(err) == code
}
