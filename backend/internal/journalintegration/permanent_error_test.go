//go:build localjournal

package journalintegration

import (
	"context"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/lib/pq"
	"testing"
)

// Deliberate structural constraints apply only to an empty private synthetic
// usage table. They inject actual PostgreSQL states, not mocked Error values.
func TestJournalPermanentWriteStatesPreserveReceiptWithoutRebilling(t *testing.T) {
	cases := []struct{ code, class, ddl, probe string }{
		{"22001", "field_too_long", "ALTER TABLE usage_logs ALTER COLUMN model TYPE varchar(1)", "INSERT INTO usage_logs(model) VALUES('synthetic')"},
		{"22003", "numeric_range", "ALTER TABLE usage_logs ALTER COLUMN input_tokens TYPE smallint", "INSERT INTO usage_logs(input_tokens) VALUES(1000000)"},
		{"23502", "required_field", "ALTER TABLE usage_logs ADD COLUMN synthetic_required text NOT NULL", "INSERT INTO usage_logs(model) VALUES('synthetic')"},
		{"23514", "constraint_conflict", "ALTER TABLE usage_logs ADD CONSTRAINT synthetic_failure CHECK(false)", "INSERT INTO usage_logs(model) VALUES('synthetic')"},
		{"22P02", "invalid_field", "ALTER TABLE usage_logs ALTER COLUMN model TYPE integer USING 0", "INSERT INTO usage_logs(model) VALUES('synthetic')"},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			f := newFixture(t)
			key := "synthetic-permanent-" + tc.code
			check(t, f.gateway(t, key, false, nil))
			var payload, receipt, token, balance, quota string
			check(t, f.db.QueryRow(`SELECT payload_json,billing_result_json,commit_token FROM usage_recovery_journal_v1`).Scan(&payload, &receipt, &token))
			check(t, f.db.QueryRow(`SELECT balance::text FROM users WHERE id=1`).Scan(&balance))
			check(t, f.db.QueryRow(`SELECT quota_used::text FROM api_keys WHERE id=2`).Scan(&quota))
			_, e := f.db.Exec(tc.ddl)
			check(t, e)
			_, e = f.db.Exec(tc.probe)
			var pgerr *pq.Error
			if !errors.As(e, &pgerr) || string(pgerr.Code) != tc.code {
				t.Fatalf("actual PostgreSQL fault %s: %v", tc.code, e)
			}
			repo := repository.NewUsageJournalRepository(f.db)
			worked, e := repo.ProcessNext(context.Background())
			check(t, e)
			if !worked {
				t.Fatal("not claimed")
			}
			var state, class, gotPayload, gotReceipt, gotToken, gotBalance, gotQuota string
			var attempts, rows int
			check(t, f.db.QueryRow(`SELECT state,last_error_class,payload_json,billing_result_json,commit_token,attempts FROM usage_recovery_journal_v1`).Scan(&state, &class, &gotPayload, &gotReceipt, &gotToken, &attempts))
			check(t, f.db.QueryRow(`SELECT balance::text FROM users WHERE id=1`).Scan(&gotBalance))
			check(t, f.db.QueryRow(`SELECT quota_used::text FROM api_keys WHERE id=2`).Scan(&gotQuota))
			check(t, f.db.QueryRow(`SELECT count(*) FROM usage_logs`).Scan(&rows))
			if state != "quarantined" || class != tc.class || gotPayload != payload || gotReceipt != receipt || gotToken != token || gotBalance != balance || gotQuota != quota || attempts != 1 || rows != 0 {
				t.Fatal("permanent failure mutated durable billing or failed quarantine", state, class, attempts, rows)
			}
			worked, e = repo.ProcessNext(context.Background())
			check(t, e)
			if worked {
				t.Fatal("quarantine was retried")
			}
			note(t, map[string]any{"actual_pg_SQLSTATE": tc.code, "class": class, "snapshot_receipt_token_unchanged": true, "financial_replay": false, "second_worker_idle": true, "private_usage_shape_injection_only": true})
		})
	}
}
