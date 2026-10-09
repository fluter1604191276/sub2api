//go:build localjournal

package journalintegration

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestJournalReviewedSchemaIdentity(t *testing.T) {
	f := newFixture(t)
	v, s, e := repository.NewUsageJournalRepository(f.db).CatalogIdentity(context.Background())
	check(t, e)
	note(t, map[string]any{"reviewed_schema_version": v, "reviewed_catalog_sha256": s})
	if v != repository.UsageJournalSchemaVersion {
		t.Fatal(v)
	}
}

func TestJournalStartupFullSchemaFailClosed(t *testing.T) {
	cases := []struct{ name, sql string }{
		{"missing_table", "DROP TABLE usage_recovery_journal_v1"},
		{"wrong_version", "COMMENT ON TABLE usage_recovery_journal_v1 IS 'unreviewed-v2'"},
		{"wrong_default", "ALTER TABLE usage_recovery_journal_v1 ALTER COLUMN payload_version SET DEFAULT 2"},
		{"missing_check", "ALTER TABLE usage_recovery_journal_v1 DROP CONSTRAINT usage_recovery_journal_v1_payload_json_check"},
		{"noop_function", "CREATE OR REPLACE FUNCTION usage_journal_v1_immutable() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NEW; END; $$"},
		{"disabled_trigger", "ALTER TABLE usage_recovery_journal_v1 DISABLE TRIGGER usage_journal_v1_immutable"},
		{"missing_index", "DROP INDEX usage_recovery_journal_v1_pending_idx"},
		{"wrong_column_type", "ALTER TABLE usage_recovery_journal_v1 ALTER COLUMN attempts TYPE bigint"},
		{"extra_trigger", "CREATE TRIGGER synthetic_other BEFORE INSERT ON usage_recovery_journal_v1 FOR EACH ROW EXECUTE FUNCTION usage_journal_v1_immutable()"},
		{"unlogged_table", "ALTER TABLE usage_recovery_journal_v1 SET UNLOGGED"},
		{"row_security", "ALTER TABLE usage_recovery_journal_v1 ENABLE ROW LEVEL SECURITY"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			_, e := f.db.Exec(tc.sql)
			check(t, e)
			runtime := service.NewUsageJournalWorkerRuntime(f.cfg, repository.NewUsageJournalRepository(f.db))
			if e = runtime.Start(); e == nil {
				runtime.Stop()
				t.Fatal("bad schema started")
			}
			if runtime.Stats().Known {
				t.Fatal("failed startup looked ready")
			}
			calls := 0
			h := runtime.WrapHTTP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"synthetic-text","messages":[]}`)))
			if rec.Code != 503 || calls != 0 {
				t.Fatal("failed runtime forwarded")
			}
			runtime.Stop()
			note(t, map[string]any{"schema_negative": tc.name, "startup_blocked": true, "worker_not_started": true, "forward_calls": calls})
		})
	}
	t.Run("temp_relation_shadow", func(t *testing.T) {
		f := newFixture(t)
		f.db.SetMaxOpenConns(1)
		_, e := f.db.Exec("CREATE TEMP TABLE usage_logs(id bigint)")
		check(t, e)
		r := service.NewUsageJournalWorkerRuntime(f.cfg, repository.NewUsageJournalRepository(f.db))
		if e = r.Start(); e == nil {
			r.Stop()
			t.Fatal("temp shadow accepted")
		}
		note(t, map[string]any{"temp_shadow_read_only_rejected": true})
	})
	t.Run("valid_exact_schema", func(t *testing.T) {
		f := newFixture(t)
		runtime := service.NewUsageJournalWorkerRuntime(f.cfg, repository.NewUsageJournalRepository(f.db))
		check(t, runtime.Start())
		defer runtime.Stop()
		if !runtime.Stats().Known || !runtime.Stats().Healthy {
			t.Fatal(runtime.Stats())
		}
	})
	t.Run("default_false_without_schema", func(t *testing.T) {
		f := newFixture(t)
		_, e := f.db.Exec("DROP TABLE usage_recovery_journal_v1")
		check(t, e)
		f.cfg.Gateway.UsageJournalDraftEnabled = false
		r := service.NewUsageJournalWorkerRuntime(f.cfg, repository.NewUsageJournalRepository(f.db))
		check(t, r.Start())
		r.Stop()
	})
}

func TestJournalCapacityAdmissionAndObservationContract(t *testing.T) {
	f := newFixture(t)
	repo := repository.NewUsageJournalRepository(f.db)
	r := service.NewUsageJournalWorkerRuntime(f.cfg, repo)
	check(t, r.Start())
	defer r.Stop()
	var calls atomic.Int64
	h := r.WrapHTTP(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { calls.Add(1); w.WriteHeader(204) }))
	for _, path := range []string{"/v1/messages", "/v1/responses", "/v1/chat/completions", "/responses", "/chat/completions", "/backend-api/codex/responses", "/v1beta/models/a:generateContent", "/antigravity/v1/messages"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("POST", path, strings.NewReader(`{"model":"synthetic-text","messages":[]}`)))
		if rec.Code != 204 {
			t.Fatal(path, rec.Code)
		}
	}
	for _, path := range []string{"/v1/images/generations", "/v1/embeddings", "/v1/videos", "/v1/web_search", "/v1/audio/speech", "/v1/realtime", "/v1/batches", "/embeddings", "/images/generations", "/images/edits", "/images/generations/async", "/images/edits/async", "/images/tasks/a", "/video/generations", "/videos", "/videos/generations", "/videos/edits", "/videos/extensions", "/videos/a/content", "/tts", "/stt", "/custom-voices", "/custom-voices/a/audio", "/realtime", "/web_search", "/x_search", "/alpha/search", "/messages/count_tokens"} {
		rec := httptest.NewRecorder()
		before := calls.Load()
		h.ServeHTTP(rec, httptest.NewRequest("POST", path, strings.NewReader(`{"model":"synthetic-text","messages":[]}`)))
		if rec.Code != 503 || calls.Load() != before {
			t.Fatal("unsupported entry forwarded", path)
		}
	}
	req := httptest.NewRequest("GET", "/v1/responses", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 503 {
		t.Fatal("gateway WS admitted")
	}
	r.Stop() // Commit the quarantined fixture before a fresh consumer can see it.
	check(t, f.gateway(t, "synthetic-quarantine-alert", false, nil))
	_, e := f.db.Exec("UPDATE usage_recovery_journal_v1 SET state='quarantined',last_error_class='missing_reference'")
	check(t, e)
	r = service.NewUsageJournalWorkerRuntime(f.cfg, repo)
	check(t, r.Start())
	defer r.Stop()
	h = r.WrapHTTP(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { calls.Add(1); w.WriteHeader(204) }))
	check(t, r.Sample(context.Background(), repo))
	status := r.Stats()
	if status.Healthy || status.Quarantined != 1 || status.ErrorClasses["missing_reference"] != 1 {
		t.Fatal(status)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"synthetic-text","messages":[]}`)))
	if rec.Code != 503 {
		t.Fatal("quarantine not gated")
	}
	_, e = f.db.Exec("UPDATE usage_recovery_journal_v1 SET state='pending',last_error_class=NULL")
	check(t, e)
	// A cancelled observation is UNKNOWN, retaining last values, never an empty success.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e = r.Sample(ctx, repo); e == nil {
		t.Fatal("cancelled observation succeeded")
	}
	if r.Stats().Known || r.Stats().Healthy {
		t.Fatal("unknown became ready")
	}
	check(t, r.Sample(context.Background(), repo))
	r.StopEnrollment()
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"synthetic-text","messages":[]}`)))
	if rec.Code != 503 {
		t.Fatal("draining admitted")
	}
	note(t, map[string]any{"supported_HTTP_only": true, "unsupported_and_WS_pre_forward_rejected": true, "quarantine_alarm_and_admission_gate": true, "observation_failure": "UNKNOWN", "consumer_remains_running_to_drain": true})
}

func TestJournalPayloadRestrictionBeforeForward(t *testing.T) {
	f := newFixture(t)
	r := service.NewUsageJournalWorkerRuntime(f.cfg, repository.NewUsageJournalRepository(f.db))
	check(t, r.Start())
	defer r.Stop()
	calls := 0
	h := r.WrapHTTP(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) { calls++; w.WriteHeader(204) }))
	cases := []string{`{"tools":[{"type":"web_search"}]}`, `{"tools":[{"type":"function","name":"synthetic"}]}`, `{"functions":[{"name":"f"}]}`, `{"audio":{}}`, `{"modalities":["audio"]}`, `{"model":"gpt-image-1"}`, `{"generationConfig":{"responseModalities":["IMAGE"]}}`, `{"search_parameters":{"mode":"auto"}}`, `{"tools":[{"type":"image_generation"}],"tools":[]}`, `not-json`}
	for i, body := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/responses", strings.NewReader(body)))
		if rec.Code != 503 || calls != 0 {
			t.Fatal("unsupported payload forwarded", i, rec.Code, calls)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"gpt-5.1","tools":[],"modalities":["text"],"input":"synthetic"}`)))
	if rec.Code != 204 || calls != 1 {
		t.Fatal("text not accepted", rec.Code, calls)
	}
	for _, path := range []string{"/v1beta/models/gemini-image:generateContent", "/antigravity/v1beta/models/gemini-audio:streamGenerateContent"} {
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("POST", path, strings.NewReader(`{"contents":[]}`)))
		if rec.Code != 503 || calls != 1 {
			t.Fatal("URL media model forwarded", path)
		}
	}
	f.cfg.Gateway.TextMaxBodySize = 10
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"input":"larger than explicit limit"}`)))
	if rec.Code != 503 || calls != 1 {
		t.Fatal("oversized payload forwarded")
	}
	note(t, map[string]any{"payload_negative_cases": len(cases) + 3, "ordinary_text_pass": true, "all_rejected_before_forward": true, "body_not_logged": true})
}

func TestJournalCapacityLimitsGateActualPendingAgeBytes(t *testing.T) {
	for _, limit := range []string{"pending", "age", "bytes"} {
		t.Run(limit, func(t *testing.T) {
			f := newFixture(t)
			repo := repository.NewUsageJournalRepository(f.db)
			// No runtime consumer yet: committed backlog makes each limit reproducible.
			check(t, f.gateway(t, "synthetic-cap-a", false, nil))
			check(t, f.gateway(t, "synthetic-cap-b", false, nil))
			switch limit {
			case "pending":
				f.cfg.Gateway.UsageJournalMaxPending = 1
			case "age":
				f.cfg.Gateway.UsageJournalMaxPendingAgeSeconds = 1
				time.Sleep(1100 * time.Millisecond)
			case "bytes":
				f.cfg.Gateway.UsageJournalMaxRelationBytes = 1
			}
			// Hold the actual watermark to keep the consumer busy while sampling independently.
			lock, e := f.db.Begin()
			check(t, e)
			_, e = lock.Exec("SELECT id FROM usage_group_rollup_state WHERE id=1 FOR UPDATE")
			check(t, e)
			defer lock.Rollback()
			r := service.NewUsageJournalWorkerRuntime(f.cfg, repo)
			check(t, r.Start())
			defer r.Stop()
			if r.Stats().Healthy {
				t.Fatal("limit breach ready", limit, r.Stats())
			}
			h := r.WrapHTTP(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) { t.Fatal("breach forwarded") }))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"synthetic-text","messages":[]}`)))
			if rec.Code != 503 {
				t.Fatal(rec.Code)
			}
			first := r.Stats().ObservedAt
			deadline := time.Now().Add(7 * time.Second)
			for !r.Stats().ObservedAt.After(first) && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if !r.Stats().ObservedAt.After(first) {
				t.Fatal("busy worker stopped monitoring")
			}
			check(t, lock.Rollback())
			note(t, map[string]any{"limit": limit, "blocked_before_upstream": true, "busy_consumer_observation_tick": true})
		})
	}
}
