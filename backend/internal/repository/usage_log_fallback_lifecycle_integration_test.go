//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestUsageLogFallbackDurableKeyKeepsOneRowAndNoFinancialEffects(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := newUsageLogRepositoryWithSQL(client, integrationDB)
	user := mustCreateUser(t, client, &service.User{Email: "synthetic-log-fallback-" + uuid.NewString() + "@example.invalid", Balance: 100})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "synthetic-" + uuid.NewString(), Name: "synthetic"})
	account := mustCreateAccount(t, client, &service.Account{Name: "synthetic-" + uuid.NewString()})
	log := &service.UsageLog{UserID: user.ID, APIKeyID: key.ID, AccountID: account.ID, RequestID: uuid.NewString(), Model: "synthetic", ActualCost: 1.25, CreatedAt: time.Now().UTC()}
	prepared := prepareUsageLogInsert(log)
	// A pre-existing row represents the uncertain original write having won.
	require.NoError(t, execUsageLogInsertNoResult(ctx, integrationDB, prepared))
	bad := *log
	bad.UserID, bad.RequestID = -1, uuid.NewString() // Fail the whole batch FK check.
	a := usageLogBestEffortRequest{prepared: prepared, apiKeyID: key.ID, resultCh: make(chan error, 1)}
	b := usageLogBestEffortRequest{prepared: prepared, apiKeyID: key.ID, resultCh: make(chan error, 1)}
	c := usageLogBestEffortRequest{prepared: prepareUsageLogInsert(&bad), apiKeyID: key.ID, resultCh: make(chan error, 1)}
	repo.flushBestEffortBatch(integrationDB, []usageLogBestEffortRequest{a, b, c})
	require.NoError(t, <-a.resultCh)
	require.NoError(t, <-b.resultCh)
	require.Error(t, <-c.resultCh)
	var count, dedup int
	var balance, quota float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT count(*) FROM usage_logs WHERE request_id=$1 AND api_key_id=$2", log.RequestID, key.ID).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT balance FROM users WHERE id=$1", user.ID).Scan(&balance))
	require.Equal(t, 100.0, balance)
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT quota_used FROM api_keys WHERE id=$1", key.ID).Scan(&quota))
	require.Zero(t, quota)
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT count(*) FROM usage_billing_dedup WHERE api_key_id=$1", key.ID).Scan(&dedup))
	require.Zero(t, dedup)
	_, cached := repo.bestEffortRecent.Get(usageLogBatchKey(bad.RequestID, key.ID))
	require.False(t, cached, "failed fallback must not be cached as persistence success")
}
