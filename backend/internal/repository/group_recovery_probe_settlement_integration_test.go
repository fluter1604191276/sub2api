//go:build integration

package repository

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type probeSettlementFixture struct {
	groupID   int64
	accountID int64
	userID    int64
	apiKeyID  int64
	auditIDs  []int64
}

func newProbeSettlementFixture(t *testing.T, auditCount int) probeSettlementFixture {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()
	fixture := probeSettlementFixture{}
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		INSERT INTO groups (name, platform, status)
		VALUES ($1, 'openai', 'active')
		RETURNING id
	`, "probe-settlement-"+suffix).Scan(&fixture.groupID))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		INSERT INTO accounts (name, platform, type, status, schedulable, credentials, extra)
		VALUES ($1, 'openai', 'api_key', 'active', TRUE, '{}'::jsonb, '{}'::jsonb)
		RETURNING id
	`, "probe-settlement-"+suffix).Scan(&fixture.accountID))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		INSERT INTO users (email, password_hash, role, balance, status)
		VALUES ($1, 'hash', 'admin', 10, 'active')
		RETURNING id
	`, "probe-settlement-"+suffix+"@example.com").Scan(&fixture.userID))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		INSERT INTO api_keys (user_id, key, name, status)
		VALUES ($1, $2, 'probe settlement', 'active')
		RETURNING id
	`, fixture.userID, "sk-probe-settlement-"+suffix).Scan(&fixture.apiKeyID))

	startedAt := time.Now().UTC().Add(-time.Second)
	for i := 0; i < auditCount; i++ {
		var auditID int64
		require.NoError(t, integrationDB.QueryRowContext(ctx, `
			INSERT INTO group_recovery_probe_audits (
				group_id, account_id, model, started_at, finished_at, status,
				attempts, success_count, failure_count, latency_ms, error_class,
				cost_status, settlement_status, input_tokens, output_tokens
			) VALUES ($1, $2, 'gpt-5.6-sol', $3, $4, 'eligible', 1, 1, 0, 1000, '',
				'estimated', 'pending', 100, 10)
			RETURNING id
		`, fixture.groupID, fixture.accountID, startedAt, startedAt.Add(time.Second)).Scan(&auditID))
		fixture.auditIDs = append(fixture.auditIDs, auditID)
	}

	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM group_recovery_probe_audits WHERE group_id = $1", fixture.groupID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM usage_logs WHERE api_key_id = $1", fixture.apiKeyID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM usage_billing_dedup WHERE api_key_id = $1", fixture.apiKeyID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM usage_billing_dedup_archive WHERE api_key_id = $1", fixture.apiKeyID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM api_keys WHERE id = $1", fixture.apiKeyID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM users WHERE id = $1", fixture.userID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM accounts WHERE id = $1", fixture.accountID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM groups WHERE id = $1", fixture.groupID)
	})
	return fixture
}

func probeAtomicSettlementCommand(fixture probeSettlementFixture, auditID int64, cost float64) *service.GroupRecoveryProbeAtomicSettlementCommand {
	requestID := fmt.Sprintf("probe:%d", auditID)
	groupID := fixture.groupID
	billingMode := string(service.BillingModeToken)
	durationMs := 1000
	accountMultiplier := 1.0
	return &service.GroupRecoveryProbeAtomicSettlementCommand{
		AuditID:        auditID,
		ReservationUSD: 0.01,
		DailyBudgetUSD: 1,
		BudgetSince:    time.Now().UTC().Add(-time.Hour),
		SettledCostUSD: cost,
		BillingCommand: &service.UsageBillingCommand{
			RequestID:        requestID,
			APIKeyID:         fixture.apiKeyID,
			UserID:           fixture.userID,
			AccountID:        fixture.accountID,
			AccountType:      service.AccountTypeAPIKey,
			Model:            "gpt-5.6-sol",
			BillingType:      service.BillingTypeBalance,
			InputTokens:      100,
			OutputTokens:     10,
			BalanceCost:      cost,
			AccountQuotaCost: cost,
		},
		UsageLog: &service.UsageLog{
			UserID:                fixture.userID,
			APIKeyID:              fixture.apiKeyID,
			AccountID:             fixture.accountID,
			RequestID:             requestID,
			Model:                 "gpt-5.6-sol",
			RequestedModel:        "gpt-5.6-sol",
			GroupID:               &groupID,
			InputTokens:           100,
			OutputTokens:          10,
			InputCost:             cost,
			TotalCost:             cost,
			ActualCost:            cost,
			RateMultiplier:        1,
			AccountRateMultiplier: &accountMultiplier,
			BillingType:           service.BillingTypeBalance,
			BillingMode:           &billingMode,
			RequestType:           service.RequestTypeProbe,
			DurationMs:            &durationMs,
			CreatedAt:             time.Now().UTC(),
		},
	}
}

func probeSettlementState(t *testing.T, fixture probeSettlementFixture, auditID int64) (string, *int64, float64, float64, int, int) {
	t.Helper()
	ctx := context.Background()
	var status string
	var usageLogID *int64
	var balance, accountQuota float64
	var usageCount, dedupCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT settlement_status, usage_log_id FROM group_recovery_probe_audits WHERE id = $1
	`, auditID).Scan(&status, &usageLogID))
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT balance FROM users WHERE id = $1", fixture.userID).Scan(&balance))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT COALESCE((extra->>'quota_used')::numeric, 0) FROM accounts WHERE id = $1
	`, fixture.accountID).Scan(&accountQuota))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM usage_logs WHERE api_key_id = $1
	`, fixture.apiKeyID).Scan(&usageCount))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM usage_billing_dedup WHERE api_key_id = $1
	`, fixture.apiKeyID).Scan(&dedupCount))
	return status, usageLogID, balance, accountQuota, usageCount, dedupCount
}

func TestGroupRecoveryProbeAtomicSettlementBlocksCostAboveReservation(t *testing.T) {
	fixture := newProbeSettlementFixture(t, 1)
	repo := NewGroupRecoveryProbeRepository(integrationDB)
	cmd := probeAtomicSettlementCommand(fixture, fixture.auditIDs[0], 0.011)

	result, err := repo.SettleProbe(context.Background(), cmd)
	require.NoError(t, err)
	require.Equal(t, service.GroupRecoveryProbeSettlementBudgetBlocked, result.Status)

	status, usageLogID, balance, accountQuota, usageCount, dedupCount := probeSettlementState(t, fixture, fixture.auditIDs[0])
	require.Equal(t, service.GroupRecoveryProbeSettlementBudgetBlocked, status)
	require.Nil(t, usageLogID)
	require.InDelta(t, 10, balance, 1e-10)
	require.Zero(t, accountQuota)
	require.Zero(t, usageCount)
	require.Zero(t, dedupCount)
}

func TestGroupRecoveryProbeAtomicSettlementRejectsChargeCostMismatch(t *testing.T) {
	fixture := newProbeSettlementFixture(t, 1)
	repo := NewGroupRecoveryProbeRepository(integrationDB)
	cmd := probeAtomicSettlementCommand(fixture, fixture.auditIDs[0], 0.004)
	cmd.BillingCommand.BalanceCost = 0.009

	_, err := repo.SettleProbe(context.Background(), cmd)
	require.ErrorContains(t, err, "charged cost does not match")

	status, usageLogID, balance, accountQuota, usageCount, dedupCount := probeSettlementState(t, fixture, fixture.auditIDs[0])
	require.Equal(t, service.GroupRecoveryProbeSettlementPending, status)
	require.Nil(t, usageLogID)
	require.InDelta(t, 10, balance, 1e-10)
	require.Zero(t, accountQuota)
	require.Zero(t, usageCount)
	require.Zero(t, dedupCount)
}

func TestGroupRecoveryProbeAtomicSettlementBlocksDailyBudgetOverflow(t *testing.T) {
	fixture := newProbeSettlementFixture(t, 2)
	ctx := context.Background()
	_, err := integrationDB.ExecContext(ctx, `
		UPDATE group_recovery_probe_audits
		SET settlement_status = 'settled', settled_cost = 0.009, cost_status = 'actual'
		WHERE id = $1
	`, fixture.auditIDs[0])
	require.NoError(t, err)
	repo := NewGroupRecoveryProbeRepository(integrationDB)
	cmd := probeAtomicSettlementCommand(fixture, fixture.auditIDs[1], 0.002)
	cmd.DailyBudgetUSD = 0.01

	result, err := repo.SettleProbe(ctx, cmd)
	require.NoError(t, err)
	require.Equal(t, service.GroupRecoveryProbeSettlementBudgetBlocked, result.Status)

	status, usageLogID, balance, accountQuota, usageCount, dedupCount := probeSettlementState(t, fixture, fixture.auditIDs[1])
	require.Equal(t, service.GroupRecoveryProbeSettlementBudgetBlocked, status)
	require.Nil(t, usageLogID)
	require.InDelta(t, 10, balance, 1e-10)
	require.Zero(t, accountQuota)
	require.Zero(t, usageCount)
	require.Zero(t, dedupCount)
}

func TestGroupRecoveryProbeAtomicSettlementRollsBackWhenUsageLogInsertFails(t *testing.T) {
	fixture := newProbeSettlementFixture(t, 1)
	repo := NewGroupRecoveryProbeRepository(integrationDB)
	cmd := probeAtomicSettlementCommand(fixture, fixture.auditIDs[0], 0.004)
	cmd.UsageLog.Model = strings.Repeat("x", 101)

	_, err := repo.SettleProbe(context.Background(), cmd)
	require.Error(t, err)

	status, usageLogID, balance, accountQuota, usageCount, dedupCount := probeSettlementState(t, fixture, fixture.auditIDs[0])
	require.Equal(t, service.GroupRecoveryProbeSettlementPending, status)
	require.Nil(t, usageLogID)
	require.InDelta(t, 10, balance, 1e-10)
	require.Zero(t, accountQuota)
	require.Zero(t, usageCount)
	require.Zero(t, dedupCount)
}

func TestGroupRecoveryProbeAtomicSettlementRollsBackWhenAuditUpdateFails(t *testing.T) {
	fixture := newProbeSettlementFixture(t, 1)
	ctx := context.Background()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	functionName := "fail_probe_audit_update_" + suffix
	triggerName := "fail_probe_audit_update_" + suffix
	_, err := integrationDB.ExecContext(ctx, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.id = %d AND NEW.settlement_status = 'settled' THEN
				RAISE EXCEPTION 'forced probe audit update failure';
			END IF;
			RETURN NEW;
		END $$;
		CREATE TRIGGER %s BEFORE UPDATE ON group_recovery_probe_audits
		FOR EACH ROW EXECUTE FUNCTION %s();
	`, functionName, fixture.auditIDs[0], triggerName, functionName))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON group_recovery_probe_audits", triggerName))
		_, _ = integrationDB.ExecContext(context.Background(), fmt.Sprintf("DROP FUNCTION IF EXISTS %s()", functionName))
	})

	repo := NewGroupRecoveryProbeRepository(integrationDB)
	cmd := probeAtomicSettlementCommand(fixture, fixture.auditIDs[0], 0.004)
	_, err = repo.SettleProbe(ctx, cmd)
	require.Error(t, err)

	status, usageLogID, balance, accountQuota, usageCount, dedupCount := probeSettlementState(t, fixture, fixture.auditIDs[0])
	require.Equal(t, service.GroupRecoveryProbeSettlementPending, status)
	require.Nil(t, usageLogID)
	require.InDelta(t, 10, balance, 1e-10)
	require.Zero(t, accountQuota)
	require.Zero(t, usageCount)
	require.Zero(t, dedupCount)
}

func TestGroupRecoveryProbeAtomicSettlementIsIdempotent(t *testing.T) {
	fixture := newProbeSettlementFixture(t, 1)
	repo := NewGroupRecoveryProbeRepository(integrationDB)
	cmd := probeAtomicSettlementCommand(fixture, fixture.auditIDs[0], 0.004)

	first, err := repo.SettleProbe(context.Background(), cmd)
	require.NoError(t, err)
	require.Equal(t, service.GroupRecoveryProbeSettlementSettled, first.Status)
	require.True(t, first.BillingApplied)
	second, err := repo.SettleProbe(context.Background(), cmd)
	require.NoError(t, err)
	require.Equal(t, service.GroupRecoveryProbeSettlementSettled, second.Status)
	require.False(t, second.BillingApplied)

	status, usageLogID, balance, accountQuota, usageCount, dedupCount := probeSettlementState(t, fixture, fixture.auditIDs[0])
	require.Equal(t, service.GroupRecoveryProbeSettlementSettled, status)
	require.NotNil(t, usageLogID)
	require.InDelta(t, 9.996, balance, 1e-10)
	require.InDelta(t, 0.004, accountQuota, 1e-10)
	require.Equal(t, 1, usageCount)
	require.Equal(t, 1, dedupCount)
}

func TestGroupRecoveryProbeAtomicSettlementCannotBeOverwrittenByLateFailure(t *testing.T) {
	fixture := newProbeSettlementFixture(t, 1)
	repo := NewGroupRecoveryProbeRepository(integrationDB)
	cmd := probeAtomicSettlementCommand(fixture, fixture.auditIDs[0], 0.004)

	result, err := repo.SettleProbe(context.Background(), cmd)
	require.NoError(t, err)
	require.Equal(t, service.GroupRecoveryProbeSettlementSettled, result.Status)
	require.NoError(t, repo.UpdateAuditSettlement(context.Background(), fixture.auditIDs[0], service.GroupRecoveryProbeAuditSettlement{
		Status: service.GroupRecoveryProbeSettlementFailed,
		Error:  "late ambiguous settlement failure",
	}))

	status, usageLogID, balance, accountQuota, usageCount, dedupCount := probeSettlementState(t, fixture, fixture.auditIDs[0])
	require.Equal(t, service.GroupRecoveryProbeSettlementSettled, status)
	require.NotNil(t, usageLogID)
	require.Equal(t, result.UsageLogID, usageLogID)
	require.InDelta(t, 9.996, balance, 1e-10)
	require.InDelta(t, 0.004, accountQuota, 1e-10)
	require.Equal(t, 1, usageCount)
	require.Equal(t, 1, dedupCount)
}

func TestGroupRecoveryProbeAtomicSettlementSerializesDailyBudgetCompetition(t *testing.T) {
	fixture := newProbeSettlementFixture(t, 2)
	repo := NewGroupRecoveryProbeRepository(integrationDB)
	commands := []*service.GroupRecoveryProbeAtomicSettlementCommand{
		probeAtomicSettlementCommand(fixture, fixture.auditIDs[0], 0.006),
		probeAtomicSettlementCommand(fixture, fixture.auditIDs[1], 0.006),
	}
	for _, cmd := range commands {
		cmd.DailyBudgetUSD = 0.01
	}

	results := make([]*service.GroupRecoveryProbeAtomicSettlementResult, len(commands))
	errs := make([]error, len(commands))
	var wg sync.WaitGroup
	for i := range commands {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = repo.SettleProbe(context.Background(), commands[i])
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
	statuses := []string{results[0].Status, results[1].Status}
	require.ElementsMatch(t, []string{
		service.GroupRecoveryProbeSettlementSettled,
		service.GroupRecoveryProbeSettlementBudgetBlocked,
	}, statuses)

	var settledCount, blockedCount, usageCount, dedupCount int
	var balance, accountQuota float64
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), `
		SELECT
			COUNT(*) FILTER (WHERE settlement_status = 'settled'),
			COUNT(*) FILTER (WHERE settlement_status = 'budget_blocked')
		FROM group_recovery_probe_audits WHERE group_id = $1
	`, fixture.groupID).Scan(&settledCount, &blockedCount))
	require.Equal(t, 1, settledCount)
	require.Equal(t, 1, blockedCount)
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), "SELECT balance FROM users WHERE id = $1", fixture.userID).Scan(&balance))
	require.InDelta(t, 9.994, balance, 1e-10)
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), `
		SELECT COALESCE((extra->>'quota_used')::numeric, 0) FROM accounts WHERE id = $1
	`, fixture.accountID).Scan(&accountQuota))
	require.InDelta(t, 0.006, accountQuota, 1e-10)
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM usage_logs WHERE api_key_id = $1", fixture.apiKeyID).Scan(&usageCount))
	require.Equal(t, 1, usageCount)
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM usage_billing_dedup WHERE api_key_id = $1", fixture.apiKeyID).Scan(&dedupCount))
	require.Equal(t, 1, dedupCount)
}
