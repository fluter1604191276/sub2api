//go:build localjournal

package journalintegration

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestJournalCapacityDurableBacklogCrashAndDrain(t *testing.T) {
	for _, workers := range []int{1, 8} {
		t.Run(fmt.Sprint(workers), func(t *testing.T) {
			f := newFixture(t)
			var syncCommit, fsync, fullPage, walLevel, lsn string
			check(t, f.db.QueryRow(`SELECT current_setting('synchronous_commit'),current_setting('fsync'),current_setting('full_page_writes'),current_setting('wal_level'),pg_current_wal_insert_lsn()::text`).Scan(&syncCommit, &fsync, &fullPage, &walLevel, &lsn))
			if syncCommit != "on" || fsync != "on" || fullPage != "on" {
				t.Fatal("durability disabled")
			}
			tx, e := f.db.Begin()
			check(t, e)
			defer tx.Rollback()
			_, e = tx.Exec(`SELECT id FROM usage_group_rollup_state WHERE id=1 FOR UPDATE`)
			check(t, e)
			group := int64(1)
			api := &service.APIKey{ID: 2, UserID: 1, GroupID: &group, Group: &service.Group{ID: 1, RateMultiplier: 1, Platform: service.PlatformAnthropic}, Quota: 100}
			user := &service.User{ID: 1}
			account := &service.Account{ID: 3, Type: service.AccountTypeOAuth, Platform: service.PlatformAnthropic}
			g := service.LocalJournalGateway(f.cfg, repository.NewUsageBillingRepository(nil, f.db))
			const small = 2000
			const wide = 128
			const total = small + wide
			var next atomic.Int64
			var wg sync.WaitGroup
			latencies := make(chan int64, total)
			errs := make(chan error, total)
			start := time.Now()
			for w := 0; w < 8; w++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for {
						i := int(next.Add(1) - 1)
						if i >= total {
							return
						}
						ua := "synthetic-capacity"
						if i >= small {
							b := make([]byte, 24576)
							if _, e := rand.Read(b); e != nil {
								errs <- e
								return
							}
							ua = base64.StdEncoding.EncodeToString(b)
						}
						at := time.Now()
						e := g.RecordUsage(context.Background(), &service.RecordUsageInput{Result: &service.ForwardResult{RequestID: fmt.Sprintf("synthetic-capacity-%d", i), Model: "claude-sonnet-4", Usage: service.ClaudeUsage{InputTokens: 10}}, APIKey: api, User: user, Account: account, APIKeyService: rejectLegacyQuota{}, UserAgent: ua})
						latencies <- time.Since(at).Microseconds()
						if e != nil {
							errs <- e
						}
					}
				}()
			}
			wg.Wait()
			ingest := time.Since(start)
			close(errs)
			close(latencies)
			for e := range errs {
				check(t, e)
			}
			lat := []int64{}
			for v := range latencies {
				lat = append(lat, v)
			}
			sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
			percentile := func(p float64) float64 { return float64(lat[int(float64(len(lat)-1)*p)]) / 1000 }
			var pending, rows int
			var payloadBytes, relationBytes, walBytes int64
			check(t, f.db.QueryRow(`SELECT count(*),sum(octet_length(payload_json)),pg_total_relation_size('usage_recovery_journal_v1') FROM usage_recovery_journal_v1 WHERE state='pending'`).Scan(&pending, &payloadBytes, &relationBytes))
			check(t, f.db.QueryRow(`SELECT count(*) FROM usage_logs`).Scan(&rows))
			check(t, f.db.QueryRow(`SELECT pg_wal_lsn_diff(pg_current_wal_insert_lsn(),$1::pg_lsn)::bigint`, lsn).Scan(&walBytes))
			if pending != total || rows != 0 {
				t.Fatal(pending, rows)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			worked, e := repository.NewUsageJournalRepository(f.db).ProcessNext(ctx)
			cancel()
			if !worked || e == nil {
				t.Fatal("backlog not protected by real watermark lock")
			}
			check(t, tx.Rollback())
			_, e = f.db.Exec(`UPDATE usage_recovery_journal_v1 SET retry_after=now()`)
			check(t, e)
			var env struct {
				Data   string `json:"data_dir"`
				Socket string `json:"socket_dir"`
			}
			b, e := os.ReadFile(os.Getenv("SUB2_JOURNAL_ENV"))
			check(t, e)
			check(t, json.Unmarshal(b, &env))
			if !strings.HasPrefix(env.Socket, "/tmp/sub2usage-pg18-") || env.Socket != f.socket || env.Data != filepath.Join(filepath.Dir(os.Getenv("SUB2_JOURNAL_ENV")), "pgdata") {
				t.Fatal("not owned fixture")
			}
			// Actual synthetic PG crash (not merely a clean restart). Committed journal
			// rows must survive WAL recovery; no real service/data is reachable here.
			pg := "/opt/homebrew/opt/postgresql@18/bin/pg_ctl"
			out, e := exec.Command(pg, "-D", env.Data, "-m", "immediate", "-w", "-t", "15", "stop").CombinedOutput()
			if e != nil {
				t.Fatalf("owned stop: %v %s", e, out)
			}
			out, e = exec.Command(pg, "-D", env.Data, "-l", filepath.Join(filepath.Dir(env.Data), "capacity-recovery.log"), "-w", "-t", "15", "start").CombinedOutput()
			if e != nil {
				t.Fatalf("owned restart: %v %s", e, out)
			}
			// Restart the application-side pool too. An old lib/pq idle connection can
			// report ErrBadConn on the first Ping after an immediate server crash.
			oldPoolCloseErr := f.db.Close() // expected stale transport may report broken pipe
			f.db = connect(t, f.schema, f.socket, 54472)
			check(t, f.db.Ping())
			check(t, f.db.QueryRow(`SELECT count(*) FROM usage_recovery_journal_v1 WHERE state='pending'`).Scan(&pending))
			if pending != total {
				t.Fatal("committed backlog lost after PG crash", pending)
			}
			start = time.Now()
			var delivered atomic.Int64
			errs = make(chan error, 8)
			for w := 0; w < workers; w++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					store := repository.NewUsageJournalRepository(f.db)
					for {
						worked, e := store.ProcessNext(context.Background())
						if e != nil {
							errs <- e
							return
						}
						if !worked {
							return
						}
						delivered.Add(1)
					}
				}()
			}
			wg.Wait()
			drain := time.Since(start)
			close(errs)
			for e := range errs {
				check(t, e)
			}
			var done int
			var balance, quota string
			check(t, f.db.QueryRow(`SELECT count(*) FROM usage_recovery_journal_v1 WHERE state='done'`).Scan(&done))
			check(t, f.db.QueryRow(`SELECT count(*) FROM usage_logs`).Scan(&rows))
			check(t, f.db.QueryRow(`SELECT (100-balance)::text FROM users WHERE id=1`).Scan(&balance))
			check(t, f.db.QueryRow(`SELECT quota_used::text FROM api_keys WHERE id=2`).Scan(&quota))
			if done != total || rows != total || delivered.Load() != total || balance != "0.06384000" || quota != balance {
				t.Fatal(done, rows, delivered.Load(), balance, quota)
			}
			var smallMean, wideMean float64
			var finalJournalBytes, usageRelationBytes, totalWalBytes int64
			check(t, f.db.QueryRow(`SELECT avg(octet_length(payload_json)) FILTER (WHERE octet_length(payload_json)<10000),avg(octet_length(payload_json)) FILTER (WHERE octet_length(payload_json)>=10000),pg_total_relation_size('usage_recovery_journal_v1'),pg_total_relation_size('usage_logs') FROM usage_recovery_journal_v1`).Scan(&smallMean, &wideMean, &finalJournalBytes, &usageRelationBytes))
			check(t, f.db.QueryRow(`SELECT pg_wal_lsn_diff(pg_current_wal_insert_lsn(),$1::pg_lsn)::bigint`, lsn).Scan(&totalWalBytes))
			note(t, map[string]any{"typical_mean_payload_bytes": smallMean, "wide_mean_payload_bytes": wideMean, "journal_bytes_after_done_before_vacuum": finalJournalBytes, "usage_relation_bytes": usageRelationBytes, "total_ingest_recovery_drain_WAL_bytes": totalWalBytes, "capacity_input_model": "bytes × arrival_rate × retention; replica/full production WAL not inferred"})
			note(t, map[string]any{"records": total, "small_records": small, "random_32KiB_UA_records": wide, "billing_concurrency": 8, "worker_concurrency": workers, "db_pool_max": 6, "ingest_seconds": ingest.Seconds(), "ingest_rps": float64(total) / ingest.Seconds(), "billing_p50_ms": percentile(.50), "billing_p95_ms": percentile(.95), "billing_p99_ms": percentile(.99), "payload_bytes": payloadBytes, "journal_relation_bytes": relationBytes, "whole_cluster_wal_delta_bytes": walBytes, "drain_seconds": drain.Seconds(), "drain_rps": float64(total) / drain.Seconds(), "fsync": fsync, "synchronous_commit": syncCommit, "full_page_writes": fullPage, "wal_level": walLevel, "real_immediate_PG_crash_recovery": true, "fresh_application_pool": true, "old_pool_close_had_transport_error": oldPoolCloseErr != nil, "done_rows": done, "usage_rows": rows, "money_delta": balance, "scope": "local ARM PG18 minimal synthetic schema; not production SLO or replica WAL estimate"})
		})
	}
}
