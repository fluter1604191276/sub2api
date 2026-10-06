//go:build integration

// Package authintegration exercises the real repository and Redis auth cache.
// Run only against an explicitly supplied disposable, migrated database.
package authintegration_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestOperationalGroupThroughRealAuthAndRedis(t *testing.T) {
	dsn, addr := os.Getenv("SUB2API_AUTH_TEST_DSN"), os.Getenv("SUB2API_AUTH_TEST_REDIS")
	if dsn == "" || addr == "" {
		t.Skip("requires disposable SUB2API_AUTH_TEST_DSN and SUB2API_AUTH_TEST_REDIS")
	}
	ctx := context.Background()
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer db.Close()
	var database string
	require.NoError(t, db.QueryRow("SELECT current_database()").Scan(&database))
	require.True(t, strings.HasPrefix(database, "synthetic_"), "refuse non-synthetic database")
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	defer rdb.Close()
	require.NoError(t, rdb.Ping(ctx).Err())
	repo := repository.NewAPIKeyRepository(client, db)
	groups := repository.NewGroupRepository(client, db)
	cfg := &config.Config{APIKeyAuth: config.APIKeyAuthCacheConfig{L1Size: 100, L1TTLSeconds: 60, L2TTLSeconds: 300}}
	newService := func() *service.APIKeyService {
		return service.NewAPIKeyService(repo, nil, nil, nil, nil, repository.NewAPIKeyCache(rdb), cfg)
	}
	first, second := newService(), newService()
	first.StartAuthCacheInvalidationSubscriber(ctx)
	second.StartAuthCacheInvalidationSubscriber(ctx)
	defer first.StopAuthCacheInvalidationSubscriber()
	defer second.StopAuthCacheInvalidationSubscriber()
	require.Eventually(t, func() bool {
		return first.AuthCacheInvalidationSubscriberHealth().Connected && second.AuthCacheInvalidationSubscriberHealth().Connected
	}, 3*time.Second, 10*time.Millisecond)
	for _, state := range []string{"unset", "enabled", "disabled"} {
		t.Run(state, func(t *testing.T) {
			suffix := fmt.Sprintf("auth-chain-%s-%d", state, time.Now().UnixNano())
			create := client.Group.Create().SetName(suffix).SetPlatform("openai").SetRateMultiplier(1)
			if state != "unset" {
				create.SetStreamStickyEscapeEnabled(state == "enabled").SetSmartSchedulerEnabled(state == "enabled")
			}
			group, err := create.Save(ctx)
			require.NoError(t, err)
			user, err := client.User.Create().SetEmail(suffix + "@example.invalid").SetPasswordHash("synthetic-only").SetBalance(100).Save(ctx)
			require.NoError(t, err)
			key := &service.APIKey{Key: suffix, Name: suffix, UserID: user.ID, GroupID: &group.ID, Status: service.StatusActive}
			require.NoError(t, repo.Create(ctx, key))
			hash := sha256.Sum256([]byte(key.Key))
			cacheKey := "apikey:auth:" + hex.EncodeToString(hash[:])
			t.Cleanup(func() {
				first.InvalidateAuthCacheByKey(ctx, key.Key)
				_, _ = db.Exec("DELETE FROM api_keys WHERE id=$1", key.ID)
				_, _ = db.Exec("DELETE FROM users WHERE id=$1", user.ID)
				_, _ = db.Exec("DELETE FROM groups WHERE id=$1", group.ID)
				_, _ = db.Exec("DELETE FROM auth_cache_invalidation_outbox WHERE cache_key=$1", hex.EncodeToString(hash[:]))
				_ = rdb.Del(ctx, cacheKey).Err()
			})
			_, err = db.Exec(`UPDATE groups SET model_routing='{"synthetic-model":[11]}', model_routing_enabled=true, supported_model_scopes='["gemini_text"]', force_openai_fast=true, free_openai_fast=true, max_reasoning_effort='high', max_reasoning_effort_over_limit='deny', codex_models_manifest_config='{"enabled":true,"account_ids":[11,22],"fallback_to_scheduler":true}', require_oauth_only=true, require_privacy_set=true, batch_image_discount_multiplier=0.7, batch_image_hold_multiplier=1.3, pool_mode_enabled=true, pool_mode_retry_count=4, pool_mode_retry_status_codes='[429,503]', custom_error_codes_enabled=true, custom_error_codes='[418]', recovery_probe_enabled=true, recovery_probe_mode='high_frequency', recovery_probe_model='synthetic-model', recovery_probe_interval_seconds=91, recovery_probe_attempts_per_round=2, recovery_probe_idle_threshold_seconds=3600, recovery_probe_backoff_cap_seconds=900 WHERE id=$1`, group.ID)
			require.NoError(t, err)
			expectedEscape := state != "disabled"
			expectedSmart := state == "enabled"
			assertGroup := func(g *service.Group) {
				require.NotNil(t, g)
				require.Equal(t, expectedEscape, g.StreamStickyEscapeEnabled)
				require.Equal(t, expectedSmart, g.SmartSchedulerEnabled)
				require.True(t, g.ForceOpenAIFast)
				require.True(t, g.FreeOpenAIFast)
				require.Equal(t, "deny", g.MaxReasoningEffortOverLimit)
				require.True(t, g.CodexModelsManifestConfig.Enabled)
				require.Equal(t, []int64{11, 22}, g.CodexModelsManifestConfig.AccountIDs)
				require.True(t, g.RequireOAuthOnly)
				require.True(t, g.RequirePrivacySet)
				require.Equal(t, 0.7, g.BatchImageDiscountMultiplier)
				require.Equal(t, 1.3, g.BatchImageHoldMultiplier)
				require.NotNil(t, g.PoolModeEnabled)
				require.True(t, *g.PoolModeEnabled)
				require.Equal(t, 4, *g.PoolModeRetryCount)
				require.Equal(t, []int{429, 503}, *g.PoolModeRetryStatusCodes)
				require.True(t, *g.CustomErrorCodesEnabled)
				require.Equal(t, []int{418}, *g.CustomErrorCodes)
				require.True(t, g.RecoveryProbeEnabled)
				require.Equal(t, "high_frequency", g.RecoveryProbeMode)
				require.Equal(t, "synthetic-model", g.RecoveryProbeModel)
				require.Equal(t, 91, g.RecoveryProbeIntervalSeconds)
				require.Equal(t, 2, g.RecoveryProbeAttemptsPerRound)
				require.Equal(t, 3600, g.RecoveryProbeIdleThresholdSeconds)
				require.Equal(t, 900, g.RecoveryProbeBackoffCapSeconds)
			}
			direct, err := repo.GetByKeyForAuth(ctx, key.Key)
			require.NoError(t, err)
			assertGroup(direct.Group)
			cold, err := first.GetByKey(ctx, key.Key)
			require.NoError(t, err)
			assertGroup(cold.Group)
			raw, err := rdb.Get(ctx, cacheKey).Bytes()
			require.NoError(t, err)
			var entry service.APIKeyAuthCacheEntry
			require.NoError(t, json.Unmarshal(raw, &entry))
			require.NotNil(t, entry.Snapshot)
			require.Equal(t, 26, entry.Snapshot.Version)
			warm, err := second.GetByKey(ctx, key.Key)
			require.NoError(t, err)
			assertGroup(warm.Group)
			full, err := groups.GetByID(ctx, group.ID)
			require.NoError(t, err)
			// Every operational Group field must survive the real auth chain. Metadata
			// is intentionally excluded; additions require a conscious scope decision.
			excluded := map[string]bool{"ID": true, "Name": true, "Description": true, "DefaultValidityDays": true, "DuplicateOperationID": true, "SortOrder": true, "CreatedAt": true, "UpdatedAt": true, "AccountGroups": true, "AccountCount": true, "ActiveAccountCount": true, "RateLimitedAccountCount": true}
			a, b := reflect.ValueOf(*full), reflect.ValueOf(*warm.Group)
			for i := 0; i < a.NumField(); i++ {
				n := a.Type().Field(i).Name
				if !excluded[n] {
					require.Equal(t, a.Field(i).Interface(), b.Field(i).Interface(), n)
				}
			}
			// Rolling upgrade: a serialized v25 snapshot must be reloaded, not reused.
			first.InvalidateAuthCacheByKey(ctx, key.Key)
			entry.Snapshot.Version = 25
			entry.Snapshot.Group.StreamStickyEscapeEnabled = !expectedEscape
			stale, err := json.Marshal(entry)
			require.NoError(t, err)
			require.NoError(t, rdb.Set(ctx, cacheKey, stale, time.Minute).Err())
			fresh, err := newService().GetByKey(ctx, key.Key)
			require.NoError(t, err)
			assertGroup(fresh.Group)
			// Changed persisted settings invalidate both Redis and subscriber L1 caches.
			_, err = db.Exec("DELETE FROM auth_cache_invalidation_outbox WHERE cache_key=$1", hex.EncodeToString(hash[:]))
			require.NoError(t, err)
			_, err = db.Exec("UPDATE groups SET stream_sticky_escape_enabled=NOT stream_sticky_escape_enabled, smart_scheduler_enabled=NOT smart_scheduler_enabled WHERE id=$1", group.ID)
			require.NoError(t, err)
			var queued int
			require.NoError(t, db.QueryRow("SELECT count(*) FROM auth_cache_invalidation_outbox WHERE cache_key=$1", hex.EncodeToString(hash[:])).Scan(&queued))
			require.Positive(t, queued)
			expectedEscape = !expectedEscape
			expectedSmart = !expectedSmart
			first.InvalidateAuthCacheByGroupID(ctx, group.ID)
			require.Eventually(t, func() bool {
				v, e := second.GetByKey(ctx, key.Key)
				return e == nil && v.Group.StreamStickyEscapeEnabled == expectedEscape && v.Group.SmartSchedulerEnabled == expectedSmart
			}, 3*time.Second, 10*time.Millisecond)
			updated, err := second.GetByKey(ctx, key.Key)
			require.NoError(t, err)
			assertGroup(updated.Group)
			t.Log("real PostgreSQL projection, Redis round trip, v25 rejection, durable outbox and subscriber invalidation verified")
		})
	}
}
