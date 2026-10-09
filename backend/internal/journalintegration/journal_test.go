//go:build localjournal

package journalintegration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	_ "github.com/lib/pq"
)

var counter atomic.Int64

type fixture struct {
	db             *sql.DB
	schema, socket string
	cfg            *config.Config
}

func check(t *testing.T, e error) {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
}
func note(t *testing.T, v any) { b, _ := json.Marshal(v); t.Log("EVIDENCE " + string(b)) }
func connect(t *testing.T, schema, socket string, port int) *sql.DB {
	t.Helper()
	db, e := sql.Open("postgres", fmt.Sprintf("host='%s' port=%d dbname=postgres user=synthetic_owner sslmode=disable options='-c search_path=%s'", socket, port, schema))
	check(t, e)
	db.SetMaxOpenConns(6)
	return db
}
func newFixture(t *testing.T) *fixture {
	t.Helper()
	path := os.Getenv("SUB2_JOURNAL_ENV")
	if path == "" {
		t.Fatal("private synthetic PG fixture required")
	}
	var cfg struct {
		Socket  string `json:"socket_dir"`
		Data    string `json:"data_dir"`
		Started bool   `json:"started"`
		TCP     bool   `json:"listen_tcp"`
	}
	b, e := os.ReadFile(path)
	check(t, e)
	check(t, json.Unmarshal(b, &cfg))
	if !cfg.Started || cfg.TCP || !strings.HasPrefix(filepath.Base(cfg.Socket), "sub2usage-pg18-") {
		t.Fatal("non-private fixture")
	}
	check(t, timezone.Init("UTC"))
	schema := fmt.Sprintf("synthetic_journal_%d", counter.Add(1))
	admin := connect(t, "public", cfg.Socket, 54472)
	var data, user, listen string
	check(t, admin.QueryRow(`SELECT current_setting('data_directory'),current_user,current_setting('listen_addresses')`).Scan(&data, &user, &listen))
	if data != cfg.Data || user != "synthetic_owner" || listen != "" {
		t.Fatal("identity mismatch")
	}
	_, e = admin.Exec(`CREATE SCHEMA ` + schema)
	check(t, e)
	admin.Close()
	db := connect(t, schema, cfg.Socket, 54472)
	b, e = os.ReadFile(filepath.Join(filepath.Dir(path), "schema.sql"))
	check(t, e)
	_, e = db.Exec(string(b))
	check(t, e)
	b, e = os.ReadFile("../../../ops/local-development/usage-journal/v1.up.sql")
	check(t, e)
	_, e = db.Exec(string(b))
	check(t, e)
	c := &config.Config{}
	c.Default.RateMultiplier = 1
	c.Gateway.UsageJournalDraftEnabled = true
	c.Gateway.UsageJournalMaxPending = 100000
	c.Gateway.UsageJournalMaxPendingAgeSeconds = 3600
	c.Gateway.UsageJournalMaxRelationBytes = 1073741824
	f := &fixture{db, schema, cfg.Socket, c}
	t.Cleanup(func() {
		db.Close()
		admin := connect(t, "public", cfg.Socket, 54472)
		defer admin.Close()
		_, e := admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		if e != nil {
			t.Error(e)
		}
	})
	return f
}

// Marker dependency enables the real atomic key-quota SQL. Calls to legacy
// quota mutation fail the test; no billing/log/journal query is mocked.
type rejectLegacyQuota struct{}

func (rejectLegacyQuota) UpdateQuotaUsed(context.Context, int64, float64) error {
	return errors.New("legacy quota path forbidden")
}
func (rejectLegacyQuota) UpdateRateLimitUsage(context.Context, int64, float64) error {
	return errors.New("legacy rate-limit path forbidden")
}

func (f *fixture) gateway(t *testing.T, key string, openAI bool, billing service.UsageBillingRepository) error {
	t.Helper()
	group := int64(1)
	api := &service.APIKey{ID: 2, UserID: 1, GroupID: &group, Group: &service.Group{ID: 1, RateMultiplier: 1, Platform: service.PlatformAnthropic}, Quota: 100}
	user := &service.User{ID: 1}
	account := &service.Account{ID: 3, Type: service.AccountTypeOAuth, Platform: service.PlatformAnthropic}
	if billing == nil {
		billing = repository.NewUsageBillingRepository(nil, f.db)
	}
	if openAI {
		account.Platform = service.PlatformOpenAI
		api.Group.Platform = service.PlatformOpenAI
		return service.LocalJournalOpenAI(f.cfg, billing).RecordUsage(context.Background(), &service.OpenAIRecordUsageInput{Result: &service.OpenAIForwardResult{RequestID: key, Model: "gpt-5.1", Usage: service.OpenAIUsage{InputTokens: 1000000}}, APIKey: api, User: user, Account: account, APIKeyService: rejectLegacyQuota{}})
	}
	return service.LocalJournalGateway(f.cfg, billing).RecordUsage(context.Background(), &service.RecordUsageInput{Result: &service.ForwardResult{RequestID: key, Model: "claude-sonnet-4", Usage: service.ClaudeUsage{InputTokens: 1000000}}, APIKey: api, User: user, Account: account, APIKeyService: rejectLegacyQuota{}})
}

func (f *fixture) assertOnce(t *testing.T, key string, delivered bool) {
	t.Helper()
	var raw, state, balance, quota string
	var rows int
	check(t, f.db.QueryRow(`SELECT payload_json,state FROM usage_recovery_journal_v1 WHERE request_id=$1 AND api_key_id=2`, key).Scan(&raw, &state))
	var snapshot service.UsageJournalSnapshot
	check(t, json.Unmarshal([]byte(raw), &snapshot))
	cost, e := strconv.ParseFloat(snapshot.MonetaryParameters["balance_cost"], 64)
	check(t, e)
	if cost <= 0 {
		t.Fatal("fixture must actually debit")
	}
	check(t, f.db.QueryRow(`SELECT balance::text FROM users WHERE id=1`).Scan(&balance))
	check(t, f.db.QueryRow(`SELECT quota_used::text FROM api_keys WHERE id=2`).Scan(&quota))
	b, _ := strconv.ParseFloat(balance, 64)
	q, _ := strconv.ParseFloat(quota, 64)
	if b != 100-cost || q != cost {
		t.Fatalf("money effects balance=%s quota=%s cost=%v", balance, quota, cost)
	}
	check(t, f.db.QueryRow(`SELECT count(*) FROM usage_logs WHERE request_id=$1 AND api_key_id=2`, key).Scan(&rows))
	want := 0
	if delivered {
		want = 1
		if state != "done" {
			t.Fatal(state)
		}
	} else if state != "pending" {
		t.Fatal(state)
	}
	if rows != want {
		t.Fatalf("usage rows=%d want=%d", rows, want)
	}
	note(t, map[string]any{"key": key, "actual_gateway_record_usage": true, "wallet": balance, "key_quota": quota, "journal_state": state, "usage_rows": rows})
}

func TestJournalActualPublicGatewaysAndWorker(t *testing.T) {
	for _, openAI := range []bool{false, true} {
		t.Run(fmt.Sprint(openAI), func(t *testing.T) {
			f := newFixture(t)
			key := "synthetic-gateway"
			check(t, f.gateway(t, key, openAI, nil))
			f.assertOnce(t, key, false)
			check(t, f.gateway(t, key, openAI, nil))
			f.assertOnce(t, key, false)
			w := service.NewUsageJournalWorkerRuntime(f.cfg, repository.NewUsageJournalRepository(f.db))
			check(t, w.Start())
			defer w.Stop()
			deadline := time.Now().Add(5 * time.Second)
			for {
				var state string
				check(t, f.db.QueryRow(`SELECT state FROM usage_recovery_journal_v1 WHERE request_id=$1`, key).Scan(&state))
				if state == "done" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("real runtime failed to deliver")
				}
				time.Sleep(10 * time.Millisecond)
			}
			f.assertOnce(t, key, true)
			check(t, f.gateway(t, key, openAI, nil))
			f.assertOnce(t, key, true)
		})
	}
}

type historicalClock struct {
	service.UsageBillingRepository
	at time.Time
}

func (h historicalClock) Apply(ctx context.Context, cmd *service.UsageBillingCommand) (*service.UsageBillingApplyResult, error) {
	log, e := cmd.UsageJournalSnapshot.DecodeLog()
	if e != nil {
		return nil, e
	}
	log.CreatedAt = h.at
	cmd.UsageJournalSnapshot, e = service.NewUsageJournalSnapshot(cmd, log)
	if e != nil {
		return nil, e
	}
	return h.UsageBillingRepository.Apply(ctx, cmd)
}

func TestJournalHistoricalLongLockAndRollupExactlyOnce(t *testing.T) {
	f := newFixture(t)
	key := "synthetic-historical"
	today := service.GroupUsageTodayStart(time.Now())
	billing := historicalClock{repository.NewUsageBillingRepository(nil, f.db), today.AddDate(0, 0, -5).Add(time.Hour)}
	check(t, f.gateway(t, key, false, billing))
	f.assertOnce(t, key, false)
	lock, e := f.db.Begin()
	check(t, e)
	defer lock.Rollback()
	_, e = lock.Exec(`SELECT id FROM usage_group_rollup_state WHERE id=1 FOR UPDATE`)
	check(t, e)
	store := repository.NewUsageJournalRepository(f.db)
	for i := 1; i <= 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		start := time.Now()
		worked, e := store.ProcessNext(ctx)
		cancel()
		if !worked || e == nil || time.Since(start) < 9*time.Second {
			t.Fatalf("long lock did not exhaust real budget: %v", e)
		}
		f.assertOnce(t, key, false)
		_, e = f.db.Exec(`UPDATE usage_recovery_journal_v1 SET retry_after=now() WHERE request_id=$1`, key)
		check(t, e)
		note(t, map[string]any{"real_worker_timeout_window": i, "elapsed_ms": time.Since(start).Milliseconds(), "durable_pending": true})
	}
	check(t, lock.Rollback())
	worked, e := store.ProcessNext(context.Background())
	check(t, e)
	if !worked {
		t.Fatal("not reclaimed")
	}
	f.assertOnce(t, key, true)
	rollup, ok := repository.NewDashboardAggregationRepository(f.db).(interface {
		SyncGroupUsageRollups(context.Context, time.Time) error
	})
	if !ok {
		t.Fatal("actual rollup capability missing")
	}
	for i := 0; i < 3; i++ {
		check(t, rollup.SyncGroupUsageRollups(context.Background(), today))
		_, e = store.ProcessNext(context.Background())
		check(t, e)
	}
	var raw, aggregate string
	check(t, f.db.QueryRow(`SELECT actual_cost::text FROM usage_logs WHERE request_id=$1`, key).Scan(&raw))
	check(t, f.db.QueryRow(`SELECT actual_cost::text FROM usage_group_daily_rollups WHERE bucket_date=$1::date AND group_id=1`, today.AddDate(0, 0, -5)).Scan(&aggregate))
	if raw != aggregate {
		t.Fatalf("rollup %s != %s", aggregate, raw)
	}
	note(t, map[string]any{"historical_fixture_clock_at_billing_boundary": true, "actual_rollup_function": true, "raw_cost": raw, "rollup_cost": aggregate, "repeated_syncs": 3})
}

func TestJournalConcurrentBillingWorkersAndRetentionKeys(t *testing.T) {
	f := newFixture(t)
	key := "synthetic-concurrent"
	var wg sync.WaitGroup
	errs := make(chan error, 24)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- f.gateway(t, key, false, nil) }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		check(t, e)
	}
	f.assertOnce(t, key, false)
	store := repository.NewUsageJournalRepository(f.db)
	errs = make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := store.ProcessNext(context.Background()); errs <- e }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		check(t, e)
	}
	f.assertOnce(t, key, true)
	_, e := f.db.Exec(`DELETE FROM usage_billing_dedup; DELETE FROM usage_billing_dedup_archive`)
	check(t, e)
	check(t, f.gateway(t, key, false, nil))
	f.assertOnce(t, key, true)
	_, e = f.db.Exec(`DELETE FROM usage_logs WHERE request_id=$1`, key)
	check(t, e)
	worked, e := store.ProcessNext(context.Background())
	check(t, e)
	if worked {
		t.Fatal("completed usage resurrected after retention")
	}
	var count int
	check(t, f.db.QueryRow(`SELECT count(*) FROM usage_logs WHERE request_id=$1`, key).Scan(&count))
	if count != 0 {
		t.Fatal("retention resurrected")
	}
	note(t, map[string]any{"billing_callers": 24, "worker_callers": 8, "debit_once": true, "journal_survives_dedup_retention": true, "completed_usage_not_resurrected": true})
}
