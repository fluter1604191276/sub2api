//go:build localjournal

package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	_ "github.com/lib/pq"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func journalProviderDB(t *testing.T) (*sql.DB, *config.Config) {
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
	schema := fmt.Sprintf("synthetic_journal_provider_%d", time.Now().UnixNano())
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

func TestJournalActualWireProvider(t *testing.T) {
	for _, mode := range []string{"true_valid", "true_bad_schema", "false_no_schema"} {
		t.Run(mode, func(t *testing.T) {
			db, cfg := journalProviderDB(t)
			calls := 0
			original := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(204) })
			srv := &http.Server{Handler: original}
			if mode == "true_bad_schema" {
				_, e := db.Exec("ALTER TABLE usage_recovery_journal_v1 SET UNLOGGED")
				if e != nil {
					t.Fatal(e)
				}
			}
			if mode == "false_no_schema" {
				cfg.Gateway.UsageJournalDraftEnabled = false
				_, e := db.Exec("DROP TABLE usage_recovery_journal_v1")
				if e != nil {
					t.Fatal(e)
				}
			}
			runtime, e := provideUsageJournalRuntime(cfg, db, srv)
			if mode == "true_bad_schema" {
				if e == nil || runtime != nil {
					t.Fatal("bad schema provider accepted")
				}
				if fmt.Sprintf("%p", srv.Handler) != fmt.Sprintf("%p", original) {
					t.Fatal("failed provider modified handler")
				}
				t.Log("EVIDENCE actual generated Wire provider returns schema error before attaching HTTP or starting listener")
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			defer runtime.Stop()
			rec := httptest.NewRecorder()
			srv.Handler.ServeHTTP(rec, httptest.NewRequest("POST", "/images/generations", nil))
			want := 503
			if mode == "false_no_schema" {
				want = 204
			}
			if rec.Code != want {
				t.Fatal(rec.Code, want)
			}
			if mode == "true_valid" && calls != 0 {
				t.Fatal("formal provider did not guard route")
			}
			t.Logf("EVIDENCE generated Wire provider %s handler status=%d; no listener started", mode, rec.Code)
		})
	}
}
