//go:build localjournal

package journalintegration

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"testing"
)

// Exact FK policies from r4 migrations 001/003, attached only to the synthetic
// usage table. This supplements, rather than claims, full production migration.
func TestJournalRealForeignKeysDeletedTargetQuarantine(t *testing.T) {
	for _, target := range []string{"user", "api_key", "account", "group", "subscription"} {
		t.Run(target, func(t *testing.T) {
			f := newFixture(t)
			_, e := f.db.Exec(`ALTER TABLE usage_logs ADD CONSTRAINT synthetic_user_fk FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE,ADD CONSTRAINT synthetic_key_fk FOREIGN KEY(api_key_id) REFERENCES api_keys(id) ON DELETE CASCADE,ADD CONSTRAINT synthetic_account_fk FOREIGN KEY(account_id) REFERENCES accounts(id) ON DELETE CASCADE,ADD CONSTRAINT synthetic_group_fk FOREIGN KEY(group_id) REFERENCES groups(id) ON DELETE SET NULL,ADD CONSTRAINT synthetic_subscription_fk FOREIGN KEY(subscription_id) REFERENCES user_subscriptions(id) ON DELETE SET NULL`)
			check(t, e)
			key := "synthetic-fk-" + target
			if target == "subscription" {
				group := int64(1)
				api := &service.APIKey{ID: 2, UserID: 1, GroupID: &group, Group: &service.Group{ID: 1, RateMultiplier: 1, Platform: service.PlatformAnthropic, SubscriptionType: service.SubscriptionTypeSubscription}, Quota: 100}
				g := service.LocalJournalGateway(f.cfg, repository.NewUsageBillingRepository(nil, f.db))
				check(t, g.RecordUsage(context.Background(), &service.RecordUsageInput{Result: &service.ForwardResult{RequestID: key, Model: "claude-sonnet-4", Usage: service.ClaudeUsage{InputTokens: 1000000}}, APIKey: api, User: &service.User{ID: 1}, Account: &service.Account{ID: 3, Type: service.AccountTypeOAuth, Platform: service.PlatformAnthropic}, Subscription: &service.UserSubscription{ID: 1, GroupID: 1}, APIKeyService: rejectLegacyQuota{}}))
			} else {
				check(t, f.gateway(t, key, false, nil))
			}
			var before string
			check(t, f.db.QueryRow(`SELECT payload_json FROM usage_recovery_journal_v1`).Scan(&before))
			var del string
			switch target {
			case "user":
				del = "DELETE FROM users WHERE id=1"
			case "api_key":
				del = "DELETE FROM api_keys WHERE id=2"
			case "account":
				del = "DELETE FROM accounts WHERE id=3"
			case "group":
				del = "DELETE FROM groups WHERE id=1"
			case "subscription":
				del = "DELETE FROM user_subscriptions WHERE id=1"
			}
			_, e = f.db.Exec(del)
			check(t, e)
			worked, e := repository.NewUsageJournalRepository(f.db).ProcessNext(context.Background())
			check(t, e)
			if !worked {
				t.Fatal("not claimed")
			}
			var state, payload string
			var class *string
			check(t, f.db.QueryRow(`SELECT state,payload_json,last_error_class FROM usage_recovery_journal_v1`).Scan(&state, &payload, &class))
			want := "quarantined"
			if state != want || payload != before || (want == "quarantined" && (class == nil || *class != "missing_reference")) {
				t.Fatal(target, state, class)
			}
			worked, e = repository.NewUsageJournalRepository(f.db).ProcessNext(context.Background())
			check(t, e)
			if worked {
				t.Fatal("permanent poison was retried")
			}
			note(t, map[string]any{"actual_r4_usage_FK_policy": true, "hard_deleted_synthetic_target": target, "state": state, "snapshot_retained": true, "financial_replay": false, "references_recreated": false})
		})
	}
}

func TestJournalSoftDeletedReferencesRetainOriginalUsage(t *testing.T) {
	f := newFixture(t)
	key := "synthetic-soft-delete"
	check(t, f.gateway(t, key, false, nil))
	_, e := f.db.Exec(`UPDATE users SET deleted_at=now();UPDATE api_keys SET deleted_at=now();UPDATE accounts SET deleted_at=now();UPDATE groups SET deleted_at=now();UPDATE user_subscriptions SET deleted_at=now()`)
	check(t, e)
	_, e = repository.NewUsageJournalRepository(f.db).ProcessNext(context.Background())
	check(t, e)
	f.assertOnce(t, key, true)
	note(t, map[string]any{"soft_deleted_reference_rows_still_exist": true, "original_log_recovered": true, "billing_not_rechecked_or_replayed": true})
}
