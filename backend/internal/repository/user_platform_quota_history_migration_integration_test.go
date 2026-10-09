//go:build integration

package repository

import (
	"context"
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"
	"github.com/Wei-Shaw/sub2api/ent/userplatformquota"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

// Exercise the embedded migration and real repository in one rolled-back PG
// transaction. No committed fixtures or historical rows escape the test.
func TestUserPlatformQuotaHistoryMigration_ArchiveAndFreshLimit(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	ctx = dbent.NewTxContext(ctx, tx)
	client := tx.Client()
	userID := mustCreateUserForQuota(t, client)
	_, err := client.ExecContext(ctx, `INSERT INTO user_platform_quotas
 (user_id,platform,daily_limit_usd,weekly_limit_usd,monthly_limit_usd,
 daily_usage_usd,weekly_usage_usd,monthly_usage_usd,
 daily_window_start,weekly_window_start,monthly_window_start,created_at,updated_at,deleted_at)
 SELECT $1,platform,d,w,m,du,wu,mu,ds,ws,ms,
 '2026-09-01T00:00:00Z'::timestamptz,'2026-09-02T00:00:00Z'::timestamptz,del
 FROM (VALUES
 ('openai',NULL::numeric,NULL::numeric,NULL::numeric,0.125,0.5,0.75,now(),now(),now(),NULL::timestamptz),
 ('anthropic',NULL,NULL,NULL,0,0,0,NULL,NULL,NULL,NULL),
 ('antigravity',NULL,NULL,NULL,1,2,3,now()-interval '40 days',now()-interval '40 days',now()-interval '40 days',NULL),
 ('gemini',0,NULL,NULL,4,5,6,now(),now(),now(),NULL),
 ('grok',NULL,10,NULL,7,8,9,now(),now(),now(),NULL),
 ('kimi',NULL,NULL,20,10,11,12,now(),now(),now(),NULL),
 ('zhipu',NULL,NULL,NULL,13,14,15,now(),now(),now(),'2026-09-03T00:00:00Z'::timestamptz)
 ) AS fixture(platform,d,w,m,du,wu,mu,ds,ws,ms,del)`, userID)
	require.NoError(t, err)

	// Compare every persisted column except the one intentionally changed, using
	// PG JSON numeric/timestamp serialization rather than lossy Go float copies.
	snapshot := func(includeDeleted bool) string {
		t.Helper()
		expression := "to_jsonb(q)-'deleted_at'"
		if includeDeleted {
			expression = "to_jsonb(q)"
		}
		rows, queryErr := client.QueryContext(ctx, "SELECT jsonb_agg("+expression+" ORDER BY id)::text FROM user_platform_quotas q WHERE user_id=$1", userID)
		require.NoError(t, queryErr)
		defer rows.Close()
		require.True(t, rows.Next())
		var value string
		require.NoError(t, rows.Scan(&value))
		require.NoError(t, rows.Err())
		return value
	}
	before := snapshot(false)
	migration, err := migrations.FS.ReadFile("238_purge_unlimited_user_platform_quotas.sql")
	require.NoError(t, err)
	_, err = client.ExecContext(ctx, string(migration))
	require.NoError(t, err)
	require.JSONEq(t, before, snapshot(false), "all original columns except deleted_at must survive")
	require.Equal(t, 7, querySingleInt(t, ctx, client, "SELECT count(*) FROM user_platform_quotas WHERE user_id=$1", userID))
	require.Equal(t, 4, querySingleInt(t, ctx, client, "SELECT count(*) FROM user_platform_quotas WHERE user_id=$1 AND deleted_at IS NOT NULL", userID))
	require.Equal(t, 1, querySingleInt(t, ctx, client, "SELECT count(*) FROM user_platform_quotas WHERE user_id=$1 AND platform='zhipu' AND deleted_at='2026-09-03T00:00:00Z'::timestamptz", userID))
	require.Equal(t, 3, querySingleInt(t, ctx, client, "SELECT count(*) FROM user_platform_quotas WHERE user_id=$1 AND (daily_limit_usd IS NOT NULL OR weekly_limit_usd IS NOT NULL OR monthly_limit_usd IS NOT NULL) AND deleted_at IS NULL", userID))
	once := snapshot(true)
	res, err := client.ExecContext(ctx, string(migration))
	require.NoError(t, err)
	affected, err := res.RowsAffected()
	require.NoError(t, err)
	require.Zero(t, affected, "repeat migration must change no rows")
	require.JSONEq(t, once, snapshot(true))

	repo := NewUserPlatformQuotaRepository(client)
	active, err := repo.ListByUser(ctx, userID)
	require.NoError(t, err)
	require.Len(t, active, 3, "default view hides archived state, preserving configured limits")
	record, err := repo.GetByUserPlatform(ctx, userID, "openai")
	require.NoError(t, err)
	require.Nil(t, record)
	all, err := client.UserPlatformQuota.Query().Where(userplatformquota.UserIDEQ(userID)).All(mixins.SkipSoftDelete(ctx))
	require.NoError(t, err)
	require.Len(t, all, 7)
	zero, weekly, monthly, first := 0.0, 10.0, 20.0, 0.5
	require.NoError(t, repo.UpsertForUser(ctx, userID, []UserPlatformQuotaRecord{
		{Platform: "gemini", DailyLimitUSD: &zero},
		{Platform: "grok", WeeklyLimitUSD: &weekly},
		{Platform: "kimi", MonthlyLimitUSD: &monthly},
		{Platform: "openai", MonthlyLimitUSD: &first},
	}))
	record, err = repo.GetByUserPlatform(ctx, userID, "openai")
	require.NoError(t, err)
	require.NotNil(t, record)
	require.Zero(t, record.DailyUsageUSD)
	require.Zero(t, record.WeeklyUsageUSD)
	require.Zero(t, record.MonthlyUsageUSD)
	require.Nil(t, record.DailyWindowStart)
	require.Nil(t, record.WeeklyWindowStart)
	require.Nil(t, record.MonthlyWindowStart)
	require.Equal(t, 8, querySingleInt(t, ctx, client, "SELECT count(*) FROM user_platform_quotas WHERE user_id=$1", userID))
	require.Equal(t, 1, querySingleInt(t, ctx, client, "SELECT count(*) FROM user_platform_quotas WHERE user_id=$1 AND platform='openai' AND deleted_at IS NOT NULL AND monthly_usage_usd=0.75", userID))
	preserved, err := repo.GetByUserPlatform(ctx, userID, "grok")
	require.NoError(t, err)
	require.InDelta(t, 8.0, preserved.WeeklyUsageUSD, 1e-10, "existing configured usage remains")
}
