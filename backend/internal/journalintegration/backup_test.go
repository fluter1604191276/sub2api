//go:build localjournal

package journalintegration

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestJournalPrivateBackupRestoreAndRollbackGate(t *testing.T) {
	f := newFixture(t)
	key := "synthetic-backup-pending"
	check(t, f.gateway(t, key, false, nil))
	repo := repository.NewUsageJournalRepository(f.db)
	check(t, repo.RollbackReady(context.Background()))
	before := financialState(t, f)
	var raw string
	check(t, f.db.QueryRow("SELECT payload_json::text FROM usage_recovery_journal_v1").Scan(&raw))
	dump := filepath.Join(t.TempDir(), "owned-synthetic-schema.dump")
	cmd := exec.Command("/opt/homebrew/opt/postgresql@18/bin/pg_dump", "-h", f.socket, "-p", "54472", "-U", "synthetic_owner", "-d", "postgres", "-n", f.schema, "--no-owner", "--no-privileges", "-Fc", "-f", dump)
	cmd.Env = append(os.Environ(), "PGPASSFILE=/dev/null")
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("private dump: %v %s", e, out)
	}
	b, e := os.ReadFile(dump)
	check(t, e)
	hash := fmt.Sprintf("%x", sha256.Sum256(b))
	_, e = f.db.Exec("DROP SCHEMA " + f.schema + " CASCADE")
	check(t, e)
	cmd = exec.Command("/opt/homebrew/opt/postgresql@18/bin/pg_restore", "-h", f.socket, "-p", "54472", "-U", "synthetic_owner", "-d", "postgres", "--no-owner", "--no-privileges", "--exit-on-error", dump)
	cmd.Env = append(os.Environ(), "PGPASSFILE=/dev/null")
	out, e = cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("private restore: %v %s", e, out)
	}
	if got := financialState(t, f); got != before {
		t.Fatal("restored financial records differ")
	}
	var restored string
	check(t, f.db.QueryRow("SELECT payload_json::text FROM usage_recovery_journal_v1").Scan(&restored))
	if restored != raw {
		t.Fatal("immutable pending lost")
	}
	check(t, repo.ValidateSchema(context.Background()))
	check(t, repo.RollbackReady(context.Background()))
	worked, e := repo.ProcessNext(context.Background())
	check(t, e)
	if !worked {
		t.Fatal("restored pending not processed")
	}
	f.assertOnce(t, key, true)
	note(t, map[string]any{"actual_pg_dump_restore_owned_synthetic_schema": true, "dump_sha256": hash, "financial_snapshot_equal": true, "pending_payload_equal": true, "restored_schema_signature_valid": true, "restored_pending_log_only": true, "production_backup_taken": false})
}

func TestJournalRollbackGateAllStatesAndMarkerConflicts(t *testing.T) {
	for _, state := range []string{"pending", "done", "quarantined"} {
		for _, marker := range []string{"hot", "archive", "missing", "hot_conflict", "archive_conflict"} {
			t.Run(state+"_"+marker, func(t *testing.T) {
				f := newFixture(t)
				key := "synthetic-rollback-marker"
				check(t, f.gateway(t, key, false, nil))
				repo := repository.NewUsageJournalRepository(f.db)
				if state == "done" {
					_, e := repo.ProcessNext(context.Background())
					check(t, e)
				} else if state == "quarantined" {
					_, e := f.db.Exec("UPDATE usage_recovery_journal_v1 SET state='quarantined',last_error_class='synthetic_fixture'")
					check(t, e)
				}
				switch marker {
				case "archive":
					_, e := f.db.Exec("INSERT INTO usage_billing_dedup_archive(request_id,api_key_id,request_fingerprint,created_at) SELECT request_id,api_key_id,request_fingerprint,created_at FROM usage_billing_dedup;DELETE FROM usage_billing_dedup")
					check(t, e)
				case "missing":
					_, e := f.db.Exec("DELETE FROM usage_billing_dedup")
					check(t, e)
				case "hot_conflict":
					_, e := f.db.Exec("UPDATE usage_billing_dedup SET request_fingerprint='synthetic-conflict'")
					check(t, e)
				case "archive_conflict":
					_, e := f.db.Exec("INSERT INTO usage_billing_dedup_archive(request_id,api_key_id,request_fingerprint,created_at) SELECT request_id,api_key_id,request_fingerprint,created_at FROM usage_billing_dedup;UPDATE usage_billing_dedup_archive SET request_fingerprint='synthetic-conflict'")
					check(t, e)
				}
				before := financialState(t, f)
				e := repo.RollbackReady(context.Background())
				want := marker == "hot" || marker == "archive"
				if (e == nil) != want {
					t.Fatal("bad readiness", state, marker, e)
				}
				if financialState(t, f) != before {
					t.Fatal("rollback audit mutated money")
				}
				note(t, map[string]any{"journal_state": state, "marker_case": marker, "ready": want, "audit_read_only": true, "financial_replay": false})
			})
		}
	}
}
