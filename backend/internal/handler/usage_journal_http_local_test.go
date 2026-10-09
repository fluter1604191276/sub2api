//go:build unit && localjournal

package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type journalHTTPUserRepo struct {
	service.UserRepository
	db *sql.DB
}

func (s journalHTTPUserRepo) GetByID(ctx context.Context, id int64) (*service.User, error) {
	u := &service.User{ID: id, Status: service.StatusActive, Concurrency: 10}
	e := s.db.QueryRowContext(ctx, "SELECT balance FROM users WHERE id=$1", id).Scan(&u.Balance)
	return u, e
}

type journalHTTPUpstream struct {
	body, contentType string
	calls             atomic.Int64
}

func (u *journalHTTPUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.calls.Add(1)
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{u.contentType}}, Body: io.NopCloser(strings.NewReader(u.body))}, nil
}
func (u *journalHTTPUpstream) DoWithTLS(req *http.Request, p string, a int64, n int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, p, a, n)
}
func journalHTTPDB(t *testing.T) (*sql.DB, *config.Config) {
	t.Helper()
	path := os.Getenv("SUB2_JOURNAL_ENV")
	var meta struct {
		Socket  string `json:"socket_dir"`
		Data    string `json:"data_dir"`
		Started bool   `json:"started"`
		TCP     bool   `json:"listen_tcp"`
	}
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(b, &meta); e != nil {
		t.Fatal(e)
	}
	if !meta.Started || meta.TCP || !strings.HasPrefix(meta.Socket, "/tmp/sub2usage-pg18-") {
		t.Fatal("private fixture required")
	}
	schema := fmt.Sprintf("synthetic_journal_http_%d", time.Now().UnixNano())
	dsn := fmt.Sprintf("host='%s' port=54472 user=synthetic_owner dbname=postgres sslmode=disable", meta.Socket)
	admin, e := sql.Open("postgres", dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer admin.Close()
	var data, user, listen string
	if e = admin.QueryRow("SELECT current_setting('data_directory'),current_user,current_setting('listen_addresses')").Scan(&data, &user, &listen); e != nil {
		t.Fatal(e)
	}
	if data != meta.Data || user != "synthetic_owner" || listen != "" {
		t.Fatal("identity mismatch")
	}
	if _, e = admin.Exec("CREATE SCHEMA " + schema); e != nil {
		t.Fatal(e)
	}
	db, e := sql.Open("postgres", dsn+" options='-c search_path="+schema+"'")
	if e != nil {
		t.Fatal(e)
	}
	db.SetMaxOpenConns(6)
	t.Cleanup(func() {
		db.Close()
		a, e := sql.Open("postgres", dsn)
		if e != nil {
			t.Error(e)
			return
		}
		defer a.Close()
		if _, e = a.Exec("DROP SCHEMA " + schema + " CASCADE"); e != nil {
			t.Error(e)
		}
	})
	for _, file := range []string{filepath.Join(filepath.Dir(path), "schema.sql"), "../../../ops/local-development/usage-journal/v1.up.sql"} {
		b, e = os.ReadFile(file)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(string(b)); e != nil {
			t.Fatal(e)
		}
	}
	if e = timezone.Init("UTC"); e != nil {
		t.Fatal(e)
	}
	cfg := &config.Config{RunMode: config.RunModeStandard}
	cfg.Default.RateMultiplier = 1
	cfg.Gateway.UsageJournalDraftEnabled = true
	cfg.Gateway.UsageJournalMaxPending = 100
	cfg.Gateway.UsageJournalMaxPendingAgeSeconds = 60
	cfg.Gateway.UsageJournalMaxRelationBytes = 10485760
	cfg.Gateway.UsageRecord.OverflowPolicy = config.UsageRecordOverflowPolicySync
	return db, cfg
}
func TestUsageJournalHTTPBusinessHandlerForwardAndQueuedBilling(t *testing.T) {
	for _, family := range []string{"claude", "openai"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s_stream_%t", family, stream), func(t *testing.T) {
				db, cfg := journalHTTPDB(t)
				gin.SetMode(gin.TestMode)
				platform := service.PlatformAnthropic
				model := "claude-sonnet-4"
				path := "/v1/messages"
				if family == "openai" {
					platform = service.PlatformOpenAI
					model = "gpt-5.1"
					path = "/v1/responses"
				}
				groupID := int64(1)
				group := &service.Group{ID: 1, Hydrated: true, Platform: platform, Status: service.StatusActive, RateMultiplier: 1}
				user := &service.User{ID: 1, Status: service.StatusActive, Concurrency: 10, Balance: 100}
				api := &service.APIKey{ID: 2, UserID: 1, GroupID: &groupID, Group: group, User: user, Status: service.StatusActive, Quota: 100}
				account := service.Account{ID: 3, Name: "synthetic-account", Platform: platform, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"api_key": "SYNTHETIC-NON-CREDENTIAL", "base_url": "https://synthetic.invalid"}, AccountGroups: []service.AccountGroup{{AccountID: 3, GroupID: 1}}}
				upstream := &journalHTTPUpstream{contentType: "application/json"}
				if family == "claude" {
					upstream.body = `{"id":"msg_synthetic","type":"message","role":"assistant","model":"claude-sonnet-4","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1000,"output_tokens":2000}}`
					if stream {
						upstream.contentType = "text/event-stream"
						upstream.body = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_synthetic\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4\",\"content\":[],\"usage\":{\"input_tokens\":1000,\"output_tokens\":0}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2000}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
					}
				} else {
					account.Extra = map[string]any{"openai_passthrough": true}
					upstream.body = `{"id":"resp_synthetic","object":"response","model":"gpt-5.1","status":"completed","output":[],"usage":{"input_tokens":1000,"output_tokens":2000,"total_tokens":3000}}`
					if stream {
						upstream.contentType = "text/event-stream"
						upstream.body = "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":" + upstream.body + "}\n\n"
					}
				}
				accountRepo := &openAIWSFailoverHandlerAccountRepoStub{accounts: []service.Account{account}}
				billing := repository.NewUsageBillingRepository(nil, db)
				cache := service.NewBillingCacheService(nil, journalHTTPUserRepo{db: db}, nil, nil, nil, nil, cfg, nil)
				defer cache.Stop()
				keys := service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg)
				pool := service.NewUsageRecordWorkerPoolWithOptions(service.UsageRecordWorkerPoolOptions{WorkerCount: 1, QueueSize: 4, TaskTimeout: 5 * time.Second, OverflowPolicy: config.UsageRecordOverflowPolicySync})
				defer pool.Stop()
				concurrency := service.NewConcurrencyService(&fakeConcurrencyCache{})
				router := gin.New()
				router.Use(middleware.RequestLogger(), middleware.ClientRequestID(), func(c *gin.Context) {
					c.Set(string(middleware.ContextKeyAPIKey), api)
					c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1, Concurrency: 10})
					c.Next()
				})
				if family == "claude" {
					snapshot := service.NewSchedulerSnapshotService(&fakeSchedulerCache{accounts: []*service.Account{&account}}, nil, nil, nil, nil)
					gateway := service.NewGatewayService(accountRepo, &fakeGroupRepo{group: group}, nil, billing, nil, nil, nil, nil, cfg, snapshot, nil, service.NewBillingService(cfg, nil), nil, cache, nil, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
					h := NewGatewayHandler(gateway, nil, nil, nil, nil, concurrency, cache, nil, keys, pool, nil, nil, nil, cfg, nil)
					router.POST(path, h.Messages)
				} else {
					gateway := service.NewOpenAIGatewayService(accountRepo, nil, billing, nil, nil, nil, nil, cfg, nil, nil, service.NewBillingService(cfg, nil), nil, cache, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
					h := NewOpenAIGatewayHandler(gateway, concurrency, cache, keys, pool, nil, nil, nil, cfg)
					router.POST(path, h.Responses)
				}
				runtime := service.NewUsageJournalWorkerRuntime(cfg, repository.NewUsageJournalRepository(db))
				if e := runtime.Start(); e != nil {
					t.Fatal(e)
				}
				defer runtime.Stop()
				front := runtime.WrapHTTP(router)
				body := fmt.Sprintf(`{"model":%q,"stream":%t,"max_tokens":100,"messages":[{"role":"user","content":"synthetic"}],"input":"synthetic"}`, model, stream)
				req := httptest.NewRequest("POST", path, strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				front.ServeHTTP(rec, req)
				if rec.Code != 200 || upstream.calls.Load() != 1 {
					t.Fatalf("real handler did not forward: %d calls=%d body=%s", rec.Code, upstream.calls.Load(), rec.Body.String())
				}
				id := rec.Header().Get("X-Client-Request-ID")
				if id == "" {
					t.Fatal("actual middleware ID lost")
				}
				deadline := time.Now().Add(5 * time.Second)
				for {
					var n int
					var state string
					e := db.QueryRow("SELECT state FROM usage_recovery_journal_v1 WHERE request_id=$1", "client:"+id).Scan(&state)
					if e == nil && state == "done" {
						if e = db.QueryRow("SELECT count(*) FROM usage_logs WHERE request_id=$1", "client:"+id).Scan(&n); e != nil {
							t.Fatal(e)
						}
						if n != 1 {
							t.Fatal(n)
						}
						break
					}
					if time.Now().After(deadline) {
						t.Fatalf("queued real RecordUsage not durable: %v %s", e, state)
					}
					time.Sleep(10 * time.Millisecond)
				}
				var balance, quota float64
				var input, output int
				if e := db.QueryRow("SELECT balance FROM users WHERE id=1").Scan(&balance); e != nil {
					t.Fatal(e)
				}
				if e := db.QueryRow("SELECT quota_used FROM api_keys WHERE id=2").Scan(&quota); e != nil {
					t.Fatal(e)
				}
				if e := db.QueryRow("SELECT input_tokens,output_tokens FROM usage_logs").Scan(&input, &output); e != nil {
					t.Fatal(e)
				}
				if balance >= 100 || quota <= 0 || input != 1000 || output != 2000 {
					t.Fatal("real billing not applied", balance, quota, input, output)
				}
				t.Logf("EVIDENCE real HTTP %s handler, actual Forward/parser + queued RecordUsage + PG journal/worker: stream=%t wallet=%f quota=%f auth identity synthetic; transport in-memory", family, stream, balance, quota)
			})
		}
	}
}
