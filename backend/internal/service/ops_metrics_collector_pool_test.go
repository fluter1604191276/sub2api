package service

import (
	"database/sql"
	"github.com/stretchr/testify/require"
	"sync"
	"testing"
	"time"
)

func TestOpsDBPoolWaitSampleBoundaries(t *testing.T) {
	var s opsDBPoolWaitSampler
	pool := new(sql.DB)
	at := time.Unix(100, 0)
	stats := sql.DBStats{WaitCount: 20, WaitDuration: 3 * time.Second}
	require.Equal(t, "initial", s.observeLocked(pool, &stats, at).status)
	stats.WaitCount += 2
	stats.WaitDuration += 500 * time.Millisecond
	got := s.observeLocked(pool, &stats, at.Add(time.Minute))
	require.Equal(t, "interval", got.status)
	require.Equal(t, time.Minute, got.interval)
	require.EqualValues(t, 2, got.waitCountDelta)
	require.Equal(t, 500*time.Millisecond, got.waitDurationDelta)
	got = s.observeLocked(pool, &stats, at.Add(2*time.Minute))
	require.Equal(t, "interval", got.status)
	require.Zero(t, got.waitCountDelta)
	require.Zero(t, got.waitDurationDelta)
	// Wait duration completes independently of when acquisitions begin waiting.
	stats.WaitDuration += time.Second
	got = s.observeLocked(pool, &stats, at.Add(3*time.Minute))
	require.Zero(t, got.waitCountDelta)
	require.Equal(t, time.Second, got.waitDurationDelta)
}

func TestOpsDBPoolWaitSampleResetMissingInvalid(t *testing.T) {
	for _, kind := range []string{"count", "duration", "pool", "missing", "invalid"} {
		t.Run(kind, func(t *testing.T) {
			var s opsDBPoolWaitSampler
			pool := new(sql.DB)
			at := time.Unix(100, 0)
			stats := sql.DBStats{WaitCount: 20, WaitDuration: 3 * time.Second}
			s.observeLocked(pool, &stats, at)
			want := "counter_reset"
			switch kind {
			case "count":
				stats.WaitCount = 1
			case "duration":
				stats.WaitDuration = time.Second
			case "pool":
				pool = new(sql.DB)
			case "missing":
				require.Equal(t, "unavailable", s.observeLocked(nil, nil, at.Add(time.Second)).status)
				want = "initial"
			case "invalid":
				bad := sql.DBStats{WaitCount: -1}
				require.Equal(t, "invalid_counter", s.observeLocked(pool, &bad, at.Add(time.Second)).status)
				want = "initial"
			}
			got := s.observeLocked(pool, &stats, at.Add(time.Minute))
			require.Equal(t, want, got.status)
			require.Zero(t, got.interval)
			require.Zero(t, got.waitCountDelta)
			require.Zero(t, got.waitDurationDelta)
			stats.WaitCount++
			stats.WaitDuration += time.Millisecond
			got = s.observeLocked(pool, &stats, at.Add(2*time.Minute))
			require.Equal(t, "interval", got.status)
			require.EqualValues(t, 1, got.waitCountDelta)
			require.Equal(t, time.Millisecond, got.waitDurationDelta)
		})
	}
}

func TestOpsDBPoolWaitSampleRejectsStaleTime(t *testing.T) {
	var s opsDBPoolWaitSampler
	pool := new(sql.DB)
	at := time.Unix(100, 0)
	stats := sql.DBStats{WaitCount: 2}
	s.observeLocked(pool, &stats, at)
	stale := sql.DBStats{WaitCount: 50}
	for _, timeAt := range []time.Time{at, at.Add(-time.Second)} {
		require.Equal(t, "non_increasing_time", s.observeLocked(pool, &stale, timeAt).status)
	}
	stats.WaitCount = 4
	got := s.observeLocked(pool, &stats, at.Add(time.Second))
	require.EqualValues(t, 2, got.waitCountDelta)
	require.Equal(t, time.Second, got.interval)
}

func TestOpsDBPoolWaitMissingCollector(t *testing.T) {
	var c *OpsMetricsCollector
	a, i := c.dbPoolStats()
	require.Nil(t, a)
	require.Nil(t, i)
	c = &OpsMetricsCollector{}
	a, i = c.dbPoolStats()
	require.Nil(t, a)
	require.Nil(t, i)
}

func TestOpsDBPoolWaitConcurrentSampling(t *testing.T) {
	// Zero-valued sql.DB offers real zero statistics without a driver or IO.
	c := &OpsMetricsCollector{db: new(sql.DB)}
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			for range 20 {
				a, i := c.dbPoolStats()
				if a == nil || i == nil {
					t.Error("available pool counters missing")
				}
			}
		})
	}
	wg.Wait()
	require.NotNil(t, c.dbPoolWaits.previous)
	require.Zero(t, c.dbPoolWaits.previous.WaitCount)
}
