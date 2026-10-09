//go:build localjournal

package journalintegration

import (
	"context"
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func financialState(t *testing.T, f *fixture) string {
	t.Helper()
	var state string
	check(t, f.db.QueryRow(`SELECT jsonb_build_object('users',(SELECT jsonb_agg(to_jsonb(u)) FROM users u),'keys',(SELECT jsonb_agg(to_jsonb(k)) FROM api_keys k),'subs',(SELECT jsonb_agg(to_jsonb(s)) FROM user_subscriptions s),'accounts',(SELECT jsonb_agg(to_jsonb(a)) FROM accounts a),'dedup',(SELECT jsonb_agg(to_jsonb(d)) FROM usage_billing_dedup d),'usage',(SELECT jsonb_agg(to_jsonb(l)) FROM usage_logs l),'watermark',(SELECT jsonb_agg(to_jsonb(w)) FROM usage_group_rollup_state w))::text`).Scan(&state))
	return state
}
func execMigration(t *testing.T, f *fixture, script string) error {
	t.Helper()
	conn, e := f.db.Conn(context.Background())
	check(t, e)
	defer conn.Close()
	_, e = conn.ExecContext(context.Background(), script)
	if e != nil {
		_, rollback := conn.ExecContext(context.Background(), "ROLLBACK")
		check(t, rollback)
	}
	return e
}
func TestJournalMigrationAtomicForwardAndFailure(t *testing.T) {
	f := newFixture(t)
	_, e := f.db.Exec(`DROP TABLE usage_recovery_journal_v1;DROP FUNCTION usage_journal_v1_immutable();INSERT INTO usage_logs(user_id,api_key_id,account_id,request_id,model,created_at) VALUES(1,2,3,'synthetic-legacy-history','synthetic',now()-interval '7 days')`)
	check(t, e)
	before := financialState(t, f)
	b, e := os.ReadFile("../../../ops/local-development/usage-journal/v1.up.sql")
	check(t, e)
	script := string(b)
	bad := strings.Replace(script, "CREATE FUNCTION usage_journal_v1_immutable()", "SELECT 1/0;\nCREATE FUNCTION usage_journal_v1_immutable()", 1)
	if execMigration(t, f, bad) == nil {
		t.Fatal("DDL failure did not fail")
	}
	var tableExists, functionExists bool
	check(t, f.db.QueryRow(`SELECT to_regclass('usage_recovery_journal_v1') IS NOT NULL,to_regprocedure('usage_journal_v1_immutable()') IS NOT NULL`).Scan(&tableExists, &functionExists))
	if tableExists || functionExists || financialState(t, f) != before {
		t.Fatal("failed DDL not atomic")
	}
	check(t, execMigration(t, f, script))
	if financialState(t, f) != before {
		t.Fatal("additive up changed historical/financial rows")
	}
	var indexes, triggers int
	check(t, f.db.QueryRow(`SELECT count(*) FROM pg_indexes WHERE schemaname=current_schema() AND tablename='usage_recovery_journal_v1'`).Scan(&indexes))
	check(t, f.db.QueryRow(`SELECT count(*) FROM pg_trigger WHERE tgrelid='usage_recovery_journal_v1'::regclass AND NOT tgisinternal`).Scan(&triggers))
	if indexes != 3 || triggers != 1 {
		t.Fatal(indexes, triggers)
	}
	if execMigration(t, f, script) == nil {
		t.Fatal("second unregistered up unexpectedly accepted")
	}
	if financialState(t, f) != before {
		t.Fatal("duplicate up damaged history")
	}
	note(t, map[string]any{"actual_review_SQL_up": true, "injected_DDL_failure_full_rollback": true, "preexisting_historical_financial_rows_byte_stable": true, "new_indexes": indexes, "immutable_triggers": triggers, "duplicate_up": "fails safely; formal migration ledger still required", "production_migration_registered": false})
}
func TestJournalAdditiveSchemaActualR4RollbackAndPendingRecovery(t *testing.T) {
	f := newFixture(t)
	key := "synthetic-rollback-pending"
	check(t, f.gateway(t, key, false, nil))
	var raw string
	check(t, f.db.QueryRow(`SELECT payload_json FROM usage_recovery_journal_v1 WHERE request_id=$1`, key).Scan(&raw))
	var snapshot service.UsageJournalSnapshot
	check(t, json.Unmarshal([]byte(raw), &snapshot))
	binary := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(os.Getenv("SUB2_JOURNAL_ENV")))), "usage-journal-readiness", "actual-r4-compat.test")
	if _, e := os.Stat(binary); e != nil {
		t.Fatal("actual r4 compatibility binary required", e)
	}
	cmd := exec.Command(binary, "-test.run=^TestActualR4RepositoryAgainstAdditiveJournal$", "-test.v")
	cmd.Env = append(os.Environ(), "SUB2_COMPAT_SCHEMA="+f.schema, "SUB2_COMPAT_PENDING_COMMAND="+string(snapshot.BillingCommandJSON))
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("actual old r4: %v %s", e, out)
	}
	t.Log(string(out))
	var saved, state, balance, quota string
	check(t, f.db.QueryRow(`SELECT payload_json,state FROM usage_recovery_journal_v1 WHERE request_id=$1`, key).Scan(&saved, &state))
	check(t, f.db.QueryRow(`SELECT balance::text FROM users WHERE id=1`).Scan(&balance))
	check(t, f.db.QueryRow(`SELECT quota_used::text FROM api_keys WHERE id=2`).Scan(&quota))
	if saved != raw || state != "pending" || balance != "95.00000000" || quota != "5.00000000" {
		t.Fatal("binary rollback damaged pending or double billed", state, balance, quota)
	}
	worked, e := repository.NewUsageJournalRepository(f.db).ProcessNext(context.Background())
	check(t, e)
	if !worked {
		t.Fatal("pending not recoverable on return to journal code")
	}
	check(t, f.db.QueryRow(`SELECT state FROM usage_recovery_journal_v1 WHERE request_id=$1`, key).Scan(&state))
	if state != "done" {
		t.Fatal(state)
	}
	check(t, f.db.QueryRow(`SELECT balance::text FROM users WHERE id=1`).Scan(&balance))
	if balance != "95.00000000" {
		t.Fatal("worker rebilled")
	}
	note(t, map[string]any{"actual_unmodified_r4_repository_child": true, "additive_schema_retained": true, "pending_payload_unchanged_during_rollback": true, "old_new_request_charged_once": true, "pending_recovered_log_only_on_new_code": true, "wallet": balance, "r4_full_server_startup_not_tested": true, "destructive_down_tested": false})
}
