//go:build localjournal

package journalintegration

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestJournalShutdownWaitsHTTPAndRealBillingQueue(t *testing.T) {
	f := newFixture(t)
	r := service.NewUsageJournalWorkerRuntime(f.cfg, repository.NewUsageJournalRepository(f.db))
	check(t, r.Start())
	defer r.Stop()
	pool := service.NewUsageRecordWorkerPoolWithOptions(service.UsageRecordWorkerPoolOptions{WorkerCount: 1, QueueSize: 1, TaskTimeout: 5 * time.Second, OverflowPolicy: config.UsageRecordOverflowPolicySync})
	defer pool.Stop()
	occupied, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	pool.Submit(func(context.Context) { close(occupied); <-release })
	<-occupied
	// Occupied worker + one queued item forces the HTTP producer's actual sync fallback.
	queued := make(chan error, 1)
	pool.Submit(func(context.Context) { queued <- f.gateway(t, "synthetic-drain-queued", false, nil) })
	entered, finish, handled := make(chan struct{}), make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-finish:
		default:
			close(finish)
		}
	}()
	mode := make(chan service.UsageRecordSubmitMode, 1)
	front := r.WrapHTTP(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		close(entered)
		<-finish
		mode <- pool.Submit(func(context.Context) { check(t, f.gateway(t, "synthetic-drain-sync", false, nil)) })
		w.WriteHeader(204)
	}))
	go func() {
		defer close(handled)
		front.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"synthetic-text","messages":[]}`)))
	}()
	<-entered
	drained := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { drained <- service.DrainUsageJournal(ctx, &http.Server{}, pool, r) }()
	deadline := time.Now().Add(time.Second)
	for r.Stats().Accepting && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	rec := httptest.NewRecorder()
	front.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"synthetic-text","messages":[]}`)))
	if rec.Code != 503 {
		t.Fatal("new HTTP admitted while draining")
	}
	select {
	case e := <-drained:
		t.Fatal("drain returned before HTTP producer", e)
	case <-time.After(30 * time.Millisecond):
	}
	close(finish)
	<-handled
	if got := <-mode; got != service.UsageRecordSubmitModeSync {
		t.Fatal("producer lost while pool stopped", got)
	}
	select {
	case e := <-drained:
		t.Fatal("drain returned before queued billing", e)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	check(t, <-queued)
	check(t, <-drained)
	if pool.Stats().DroppedPoolStopped != 0 {
		t.Fatal("shutdown lost producer", pool.Stats())
	}
	check(t, r.RollbackReady(context.Background()))
	var balance float64
	var journal int
	check(t, f.db.QueryRow("SELECT balance FROM users WHERE id=1").Scan(&balance))
	check(t, f.db.QueryRow("SELECT count(*) FROM usage_recovery_journal_v1").Scan(&journal))
	if balance != 94 || journal != 2 {
		t.Fatal(balance, journal)
	}
	note(t, map[string]any{"tracked_actual_HTTP_producer": true, "actual_sync_fallback_before_pool_stop": true, "queued_billing_drained": true, "wallet": balance, "durable_journals": journal, "markers_rollback_ready": true, "consumer_stop_after_producers": true, "http_listener_socket_used": false})
}

func TestJournalShutdownTimeoutDoesNotDeclareDrained(t *testing.T) {
	for _, stage := range []string{"http_producer", "billing_queue"} {
		t.Run(stage, func(t *testing.T) {
			f := newFixture(t)
			r := service.NewUsageJournalWorkerRuntime(f.cfg, repository.NewUsageJournalRepository(f.db))
			check(t, r.Start())
			defer r.Stop()
			pool := service.NewUsageRecordWorkerPoolWithOptions(service.UsageRecordWorkerPoolOptions{WorkerCount: 1, QueueSize: 1, TaskTimeout: 5 * time.Second, OverflowPolicy: config.UsageRecordOverflowPolicySync})
			defer pool.Stop()
			entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			if stage == "http_producer" {
				h := r.WrapHTTP(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
					close(entered)
					<-release
					check(t, f.gateway(t, "synthetic-drain-timeout", false, nil))
					close(finished)
				}))
				go h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"synthetic-text","messages":[]}`)))
			} else {
				pool.Submit(func(context.Context) {
					close(entered)
					<-release
					check(t, f.gateway(t, "synthetic-drain-timeout", false, nil))
					close(finished)
				})
			}
			<-entered
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			e := service.DrainUsageJournal(ctx, &http.Server{}, pool, r)
			cancel()
			if e == nil {
				t.Fatal("timeout declared drained")
			}
			if r.Stats().Accepting {
				t.Fatal("timeout reopened ingress")
			}
			check(t, f.db.Ping())
			close(release)
			<-finished
			// The consumer was left available: committed late billing still creates its one log.
			deadline := time.Now().Add(3 * time.Second)
			var n int
			for time.Now().Before(deadline) {
				check(t, f.db.QueryRow("SELECT count(*) FROM usage_logs WHERE request_id='synthetic-drain-timeout'").Scan(&n))
				if n == 1 {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if n != 1 {
				t.Fatal("consumer stopped before unfinished producer")
			}
			ctx, cancel = context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			check(t, service.DrainUsageJournal(ctx, &http.Server{}, pool, r))
			f.assertOnce(t, "synthetic-drain-timeout", true)
			note(t, map[string]any{"timeout_stage": stage, "drain_result": "explicit failure", "DB_left_open": true, "late_commit_consumed_once": true, "retry_drain_completed": true, "process_exit_guaranteed": false})
		})
	}
}
