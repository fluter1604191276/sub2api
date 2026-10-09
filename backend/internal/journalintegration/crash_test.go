//go:build localjournal

package journalintegration

import (
	"context"
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Invoked only as a child test binary, not included in the parent run pattern.
func TestLocalWorkerChild(t *testing.T) {
	if os.Getenv("SUB2_JOURNAL_CHILD") != "worker" {
		return
	}
	var cfg struct {
		Socket  string `json:"socket_dir"`
		Data    string `json:"data_dir"`
		Started bool   `json:"started"`
		TCP     bool   `json:"listen_tcp"`
	}
	b, e := os.ReadFile(os.Getenv("SUB2_JOURNAL_ENV"))
	check(t, e)
	check(t, json.Unmarshal(b, &cfg))
	if !cfg.Started || cfg.TCP {
		t.Fatal("private PG required")
	}
	schema := os.Getenv("SUB2_JOURNAL_CHILD_SCHEMA")
	if !strings.HasPrefix(schema, "synthetic_journal_") {
		t.Fatal("synthetic schema required")
	}
	db := connect(t, schema, cfg.Socket, 54472)
	defer db.Close()
	db.SetMaxOpenConns(1)
	var data, user, listen string
	check(t, db.QueryRow(`SELECT current_setting('data_directory'),current_user,current_setting('listen_addresses')`).Scan(&data, &user, &listen))
	if data != cfg.Data || user != "synthetic_owner" || listen != "" {
		t.Fatal("identity mismatch")
	}
	_, e = db.Exec(`SET application_name='journal-crash-child'`)
	check(t, e)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	worked, e := repository.NewUsageJournalRepository(db).ProcessNext(ctx)
	check(t, e)
	if !worked {
		t.Fatal("no durable work")
	}
}

func TestJournalRealWorkerProcessCrashAndRestart(t *testing.T) {
	f := newFixture(t)
	key := "synthetic-worker-crash"
	check(t, f.gateway(t, key, false, nil))
	f.assertOnce(t, key, false)
	lock, e := f.db.Begin()
	check(t, e)
	defer lock.Rollback()
	_, e = lock.Exec(`SELECT id FROM usage_group_rollup_state WHERE id=1 FOR UPDATE`)
	check(t, e)
	child := func() *exec.Cmd {
		c := exec.Command(os.Args[0], "-test.run=^TestLocalWorkerChild$", "-test.v")
		c.Env = append(os.Environ(), "SUB2_JOURNAL_CHILD=worker", "SUB2_JOURNAL_CHILD_SCHEMA="+f.schema)
		return c
	}
	c := child()
	check(t, c.Start())
	t.Cleanup(func() {
		if c.Process != nil {
			c.Process.Kill()
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		var blocked int
		check(t, f.db.QueryRow(`SELECT count(*) FROM pg_stat_activity WHERE application_name='journal-crash-child' AND wait_event_type='Lock' AND query LIKE '%usage_group_rollup_state%FOR KEY SHARE%'`).Scan(&blocked))
		if blocked == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("actual child did not claim and block")
		}
		time.Sleep(10 * time.Millisecond)
	}
	check(t, c.Process.Kill())
	if e = c.Wait(); e == nil {
		t.Fatal("expected real process crash")
	}
	check(t, lock.Rollback())
	f.assertOnce(t, key, false)
	restart := child()
	out, e := restart.CombinedOutput()
	if e != nil {
		t.Fatalf("worker restart: %v %s", e, out)
	}
	f.assertOnce(t, key, true)
	note(t, map[string]any{"actual_worker_child_SIGKILL": true, "claim_in_transaction": true, "journal_pending_after_crash": true, "new_process_recovered": true, "debit_once": true, "usage_rows": 1})
}
