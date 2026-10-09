//go:build localjournal

package journalintegration

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"testing"
	"time"
)

// These wrappers observe/change synthetic input at the actual Apply boundary;
// all effects, journal writes and log delivery still use the real repositories.
type observeCommand struct {
	service.UsageBillingRepository
	before func(*service.UsageBillingCommand)
}

func (o observeCommand) Apply(ctx context.Context, c *service.UsageBillingCommand) (*service.UsageBillingApplyResult, error) {
	o.before(c)
	return o.UsageBillingRepository.Apply(ctx, c)
}
func (f *fixture) unchanged(t *testing.T) {
	t.Helper()
	var ok bool
	check(t, f.db.QueryRow(`SELECT (SELECT balance=100 FROM users WHERE id=1) AND (SELECT quota_used=0 FROM api_keys WHERE id=2) AND NOT EXISTS(SELECT 1 FROM usage_logs) AND NOT EXISTS(SELECT 1 FROM usage_recovery_journal_v1)`).Scan(&ok))
	if !ok {
		t.Fatal("negative case changed financial or log state")
	}
}
func TestJournalLegacyMarkerRejectsWithoutReplay(t *testing.T) {
	f := newFixture(t)
	b := observeCommand{repository.NewUsageBillingRepository(nil, f.db), func(c *service.UsageBillingCommand) {
		_, e := f.db.Exec(`INSERT INTO usage_billing_dedup(request_id,api_key_id,request_fingerprint) VALUES($1,$2,$3)`, c.RequestID, c.APIKeyID, c.RequestFingerprint)
		check(t, e)
	}}
	e := f.gateway(t, "synthetic-legacy", false, b)
	if !errors.Is(e, service.ErrUsageJournalLegacyMarker) {
		t.Fatal(e)
	}
	f.unchanged(t)
	note(t, map[string]any{"actual_public_gateway": true, "legacy_marker_no_journal": "reject", "financial_replay": false})
}
func TestJournalInsertFailureRollsBackAllSQL(t *testing.T) {
	f := newFixture(t)
	_, e := f.db.Exec(`CREATE FUNCTION synthetic_reject_journal() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic journal failure'; END $$; CREATE TRIGGER synthetic_reject BEFORE INSERT ON usage_recovery_journal_v1 FOR EACH ROW EXECUTE FUNCTION synthetic_reject_journal()`)
	check(t, e)
	if f.gateway(t, "synthetic-journal-failure", false, nil) == nil {
		t.Fatal("journal failure ignored")
	}
	f.unchanged(t)
	var n int
	check(t, f.db.QueryRow(`SELECT count(*) FROM usage_billing_dedup`).Scan(&n))
	if n != 0 {
		t.Fatal("claim not rolled back")
	}
	note(t, map[string]any{"actual_journal_insert_exception": true, "dedup_balance_quota_rollback": true})
}
func TestJournalFingerprintConflictNoSecondDebit(t *testing.T) {
	f := newFixture(t)
	key := "synthetic-fingerprint"
	check(t, f.gateway(t, key, false, nil))
	b := observeCommand{repository.NewUsageBillingRepository(nil, f.db), func(c *service.UsageBillingCommand) {
		l, e := c.UsageJournalSnapshot.DecodeLog()
		check(t, e)
		c.InputTokens++
		l.InputTokens++
		c.RequestFingerprint = ""
		c.Normalize()
		c.UsageJournalSnapshot, e = service.NewUsageJournalSnapshot(c, l)
		check(t, e)
	}}
	e := f.gateway(t, key, false, b)
	if !errors.Is(e, service.ErrUsageBillingRequestConflict) {
		t.Fatal(e)
	}
	f.assertOnce(t, key, false)
}
func TestJournalScalarConflictQuarantinesWithoutOverwrite(t *testing.T) {
	f := newFixture(t)
	key := "synthetic-scalar-conflict"
	check(t, f.gateway(t, key, false, nil))
	_, e := f.db.Exec(`INSERT INTO usage_logs(request_id,api_key_id,model,actual_cost,group_id,created_at) VALUES($1,2,'wrong-model',3,1,now()-interval '5 days')`, key)
	check(t, e)
	var before string
	check(t, f.db.QueryRow(`SELECT closed_before::text FROM usage_group_rollup_state`).Scan(&before))
	worked, e := repository.NewUsageJournalRepository(f.db).ProcessNext(context.Background())
	check(t, e)
	if !worked {
		t.Fatal("not claimed")
	}
	var state, model, after string
	check(t, f.db.QueryRow(`SELECT state FROM usage_recovery_journal_v1 WHERE request_id=$1`, key).Scan(&state))
	check(t, f.db.QueryRow(`SELECT model FROM usage_logs WHERE request_id=$1`, key).Scan(&model))
	check(t, f.db.QueryRow(`SELECT closed_before::text FROM usage_group_rollup_state`).Scan(&after))
	if state != "quarantined" || model != "wrong-model" || before != after {
		t.Fatal(state, model, before, after)
	}
	note(t, map[string]any{"scalar_conflict": "quarantined", "existing_usage_unchanged": true, "savepoint_watermark_unchanged": true})
}
func TestJournalInvalidRowEnvelopeQuarantines(t *testing.T) {
	f := newFixture(t)
	check(t, f.gateway(t, "synthetic-valid-source", false, nil))
	var raw, sha, fp, result, resultSHA, token string
	check(t, f.db.QueryRow(`SELECT payload_json,snapshot_sha256,request_fingerprint,billing_result_json,billing_result_sha256,commit_token FROM usage_recovery_journal_v1`).Scan(&raw, &sha, &fp, &result, &resultSHA, &token))
	_, e := f.db.Exec(`UPDATE usage_recovery_journal_v1 SET payload_json='{}'`)
	if e == nil {
		t.Fatal("immutable trigger did not reject")
	}
	_, e = f.db.Exec(`INSERT INTO usage_recovery_journal_v1(request_id,api_key_id,request_fingerprint,payload_json,snapshot_sha256,billing_result_json,billing_result_sha256,commit_token,created_at,retry_after) VALUES('synthetic-forged-key',2,$1,$2,$3,$4,$5,$6,now()-interval '1 day',now()-interval '1 day')`, fp, raw, sha, result, resultSHA, token)
	check(t, e)
	_, e = repository.NewUsageJournalRepository(f.db).ProcessNext(context.Background())
	check(t, e)
	var state string
	check(t, f.db.QueryRow(`SELECT state FROM usage_recovery_journal_v1 WHERE request_id='synthetic-forged-key'`).Scan(&state))
	if state != "quarantined" {
		t.Fatal(state)
	}
	var n int
	check(t, f.db.QueryRow(`SELECT count(*) FROM usage_logs`).Scan(&n))
	if n != 0 {
		t.Fatal("invalid row delivered")
	}
	f.assertOnce(t, "synthetic-valid-source", false)
	note(t, map[string]any{"immutable_payload_trigger": "reject", "forged_envelope": "quarantined", "billing_replayed": false})
}
func TestJournalHalfRoundingJSONNullAndTimestamp(t *testing.T) {
	f := newFixture(t)
	group := int64(1)
	api := &service.APIKey{ID: 2, UserID: 1, GroupID: &group, Group: &service.Group{ID: 1, RateMultiplier: 1.25, Platform: service.PlatformOpenAI}, Quota: 100}
	user := &service.User{ID: 1}
	account := &service.Account{ID: 3, Type: service.AccountTypeOAuth, Platform: service.PlatformOpenAI}
	var snapshot *service.UsageJournalSnapshot
	b := observeCommand{repository.NewUsageBillingRepository(nil, f.db), func(c *service.UsageBillingCommand) {
		l, e := c.UsageJournalSnapshot.DecodeLog()
		check(t, e)
		l.ImageSizeBreakdown = map[string]int{"z": 2, "a": 1}
		l.AccountStatsCost = nil
		l.CreatedAt = time.Date(2026, 10, 9, 1, 2, 3, 123456789, time.UTC)
		c.UsageJournalSnapshot, e = service.NewUsageJournalSnapshot(c, l)
		check(t, e)
		snapshot = c.UsageJournalSnapshot
	}}
	check(t, service.LocalJournalOpenAI(f.cfg, b).RecordUsage(context.Background(), &service.OpenAIRecordUsageInput{Result: &service.OpenAIForwardResult{RequestID: "synthetic-precision", Model: "gpt-5", Usage: service.OpenAIUsage{InputTokens: 25, OutputTokens: 0}}, APIKey: api, User: user, Account: account, APIKeyService: rejectLegacyQuota{}}))
	_, e := repository.NewUsageJournalRepository(f.db).ProcessNext(context.Background())
	check(t, e)
	var delta, quota, cost, state string
	var null, correctTime, correctJSON bool
	check(t, f.db.QueryRow(`SELECT (100-balance)::text FROM users WHERE id=1`).Scan(&delta))
	check(t, f.db.QueryRow(`SELECT quota_used::text FROM api_keys WHERE id=2`).Scan(&quota))
	check(t, f.db.QueryRow(`SELECT actual_cost::text,account_stats_cost IS NULL,created_at=CAST($1 AS timestamptz),image_size_breakdown='{"a":1,"z":2}'::jsonb FROM usage_logs WHERE request_id='synthetic-precision'`, time.Date(2026, 10, 9, 1, 2, 3, 123456789, time.UTC)).Scan(&cost, &null, &correctTime, &correctJSON))
	check(t, f.db.QueryRow(`SELECT state FROM usage_recovery_journal_v1`).Scan(&state))
	if delta != "0.00007813" || quota != delta || cost != "0.0000781250" || state != "done" || !null || !correctTime || !correctJSON {
		t.Fatal(delta, quota, cost, state, null, correctTime, correctJSON)
	}
	original, _ := json.Marshal(snapshot)
	check(t, f.gatewayPrecisionReplay(t, b, api, user, account))
	var nowRaw string
	check(t, f.db.QueryRow(`SELECT payload_json FROM usage_recovery_journal_v1`).Scan(&nowRaw))
	if string(original) != nowRaw {
		t.Fatal("retry replaced original snapshot")
	}
	note(t, map[string]any{"actual_public_openai_pricing": true, "billing_8_decimals": delta, "usage_10_decimals": cost, "nullable_jsonb_microsecond_comparison": true})
}
func (f *fixture) gatewayPrecisionReplay(t *testing.T, b service.UsageBillingRepository, api *service.APIKey, user *service.User, account *service.Account) error {
	return service.LocalJournalOpenAI(f.cfg, b).RecordUsage(context.Background(), &service.OpenAIRecordUsageInput{Result: &service.OpenAIForwardResult{RequestID: "synthetic-precision", Model: "gpt-5", Usage: service.OpenAIUsage{InputTokens: 25, OutputTokens: 0}}, APIKey: api, User: user, Account: account, APIKeyService: rejectLegacyQuota{}})
}

func TestJournalActualSubscriptionRateAndAccountQuotaOnce(t *testing.T) {
	f := newFixture(t)
	group := int64(1)
	api := &service.APIKey{ID: 2, UserID: 1, GroupID: &group, Group: &service.Group{ID: 1, RateMultiplier: 1, Platform: service.PlatformAnthropic, SubscriptionType: service.SubscriptionTypeSubscription}, Quota: 100, RateLimit5h: 100, RateLimit1d: 100, RateLimit7d: 100}
	account := &service.Account{ID: 3, Type: service.AccountTypeAPIKey, Platform: service.PlatformAnthropic, Extra: map[string]any{"quota_limit": 100.0, "quota_daily_limit": 100.0, "quota_weekly_limit": 100.0}}
	in := &service.RecordUsageInput{Result: &service.ForwardResult{RequestID: "synthetic-subscription", Model: "claude-sonnet-4", Usage: service.ClaudeUsage{InputTokens: 1000000}}, APIKey: api, User: &service.User{ID: 1}, Account: account, Subscription: &service.UserSubscription{ID: 1, GroupID: 1}, APIKeyService: rejectLegacyQuota{}}
	g := service.LocalJournalGateway(f.cfg, repository.NewUsageBillingRepository(nil, f.db))
	check(t, g.RecordUsage(context.Background(), in))
	check(t, g.RecordUsage(context.Background(), in))
	_, e := repository.NewUsageJournalRepository(f.db).ProcessNext(context.Background())
	check(t, e)
	check(t, g.RecordUsage(context.Background(), in))
	var ok bool
	check(t, f.db.QueryRow(`SELECT (SELECT balance=100 FROM users WHERE id=1) AND (SELECT daily_usage_usd=3 AND weekly_usage_usd=3 AND monthly_usage_usd=3 FROM user_subscriptions WHERE id=1) AND (SELECT quota_used=3 AND usage_5h=3 AND usage_1d=3 AND usage_7d=3 FROM api_keys WHERE id=2) AND (SELECT (extra->>'quota_used')::numeric=3 AND (extra->>'quota_daily_used')::numeric=3 AND (extra->>'quota_weekly_used')::numeric=3 FROM accounts WHERE id=3) AND (SELECT count(*)=1 AND bool_and(subscription_id=1) FROM usage_logs) AND (SELECT state='done' FROM usage_recovery_journal_v1)`).Scan(&ok))
	if !ok {
		t.Fatal("actual billing effect matrix was not exactly once")
	}
	note(t, map[string]any{"actual_public_gateway_subscription": true, "wallet_unchanged": true, "subscription_daily_weekly_monthly": 3, "key_quota_and_three_windows": 3, "account_total_daily_weekly": 3, "duplicate_before_after_worker": true})
}

func TestJournalCurrentTimestampBlockedThenRecovers(t *testing.T) {
	f := newFixture(t)
	key := "synthetic-current-lock"
	check(t, f.gateway(t, key, false, nil))
	tx, e := f.db.Begin()
	check(t, e)
	defer tx.Rollback()
	_, e = tx.Exec(`SELECT id FROM usage_group_rollup_state WHERE id=1 FOR UPDATE`)
	check(t, e)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	worked, e := repository.NewUsageJournalRepository(f.db).ProcessNext(ctx)
	cancel()
	if !worked || e == nil {
		t.Fatal("current INSERT bypassed watermark lock")
	}
	f.assertOnce(t, key, false)
	check(t, tx.Rollback())
	_, e = f.db.Exec(`UPDATE usage_recovery_journal_v1 SET retry_after=now()`)
	check(t, e)
	_, e = repository.NewUsageJournalRepository(f.db).ProcessNext(context.Background())
	check(t, e)
	f.assertOnce(t, key, true)
	note(t, map[string]any{"real_current_timestamp_watermark_lock": true, "durable_after_timeout": true, "same_snapshot_recovered": true})
}

func TestJournalDefaultOffRepositoryNeedsNoNewSchema(t *testing.T) {
	f := newFixture(t)
	_, e := f.db.Exec(`DROP TABLE usage_recovery_journal_v1`)
	check(t, e)
	cmd := &service.UsageBillingCommand{RequestID: "synthetic-default-off", APIKeyID: 2, UserID: 1, AccountID: 3, Model: "synthetic", BalanceCost: 2.5, APIKeyQuotaCost: 2.5}
	b := repository.NewUsageBillingRepository(nil, f.db)
	r, e := b.Apply(context.Background(), cmd)
	check(t, e)
	if !r.Applied {
		t.Fatal("legacy default path failed")
	}
	r, e = b.Apply(context.Background(), cmd)
	check(t, e)
	if r.Applied {
		t.Fatal("legacy dedup changed")
	}
	var ok bool
	check(t, f.db.QueryRow(`SELECT (SELECT balance=97.5 FROM users WHERE id=1) AND (SELECT quota_used=2.5 FROM api_keys WHERE id=2)`).Scan(&ok))
	if !ok {
		t.Fatal("default billing effects changed")
	}
	note(t, map[string]any{"nil_snapshot_actual_repository": true, "new_table_absent": true, "r4_billing_dedup_retained": true})
}
