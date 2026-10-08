//go:build integration

package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// A real isolated PostgreSQL row lock blocks the quota update after balance
// deduction. Cancellation must roll back all effects and the dedup claim. No
// automatic retry or real transaction/account is involved in this fixture.
func TestUsageBillingLockContract_CancellationRollsBackBalanceAndQuota(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewUsageBillingRepository(client, integrationDB)
	user := mustCreateUser(t, client, &service.User{Email: "synthetic-lock-" + uuid.NewString() + "@example.invalid", PasswordHash: "synthetic-hash", Balance: 100})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "synthetic-lock-" + uuid.NewString(), Name: "synthetic-lock", Quota: 50})

	lockTx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		rollbackErr := lockTx.Rollback()
		if !errors.Is(rollbackErr, sql.ErrTxDone) {
			require.NoError(t, rollbackErr)
		}
	})
	var lockedID int64
	require.NoError(t, lockTx.QueryRowContext(ctx, "SELECT id FROM api_keys WHERE id = $1 FOR UPDATE", key.ID).Scan(&lockedID))
	require.Equal(t, key.ID, lockedID)

	requestID := uuid.NewString()
	cmd := &service.UsageBillingCommand{RequestID: requestID, APIKeyID: key.ID, UserID: user.ID, BalanceCost: 1.25, APIKeyQuotaCost: 1.25}
	type outcome struct {
		result *service.UsageBillingApplyResult
		err    error
	}
	done := make(chan outcome, 1)
	billingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	go func() {
		result, applyErr := repo.Apply(billingCtx, cmd)
		done <- outcome{result: result, err: applyErr}
	}()

	// Observe an actual blocked PostgreSQL UPDATE, rather than assuming a
	// timeout was caused by lock contention. This database is test-only.
	require.Eventually(t, func() bool {
		var waiting int
		queryErr := integrationDB.QueryRowContext(ctx, "SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE '%UPDATE api_keys%'").Scan(&waiting)
		return queryErr == nil && waiting > 0
	}, 2*time.Second, 20*time.Millisecond)

	var got outcome
	select {
	case got = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("billing Apply did not return after its context deadline")
	}
	require.Nil(t, got.result)
	require.Error(t, got.err)
	require.ErrorIs(t, billingCtx.Err(), context.DeadlineExceeded)
	var postgresErr *pq.Error
	if errors.As(got.err, &postgresErr) {
		require.Equal(t, pq.ErrorCode("57014"), postgresErr.Code)
		t.Log("observed PostgreSQL query_canceled SQLSTATE 57014 under real row-lock contention")
	} else {
		require.ErrorIs(t, got.err, context.DeadlineExceeded)
	}
	require.NoError(t, lockTx.Rollback())

	assertUnchanged := func() {
		t.Helper()
		var balance, quota float64
		var dedup int
		require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT balance FROM users WHERE id = $1", user.ID).Scan(&balance))
		require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT quota_used FROM api_keys WHERE id = $1", key.ID).Scan(&quota))
		require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT count(*) FROM usage_billing_dedup WHERE request_id = $1 AND api_key_id = $2", requestID, key.ID).Scan(&dedup))
		require.Equal(t, 100.0, balance, "earlier balance update must roll back")
		require.Zero(t, quota)
		require.Zero(t, dedup, "dedup claim must not survive failed billing")
	}
	assertUnchanged()
	// Merely releasing the blocker does not replay the failed financial call.
	time.Sleep(100 * time.Millisecond)
	assertUnchanged()
}
