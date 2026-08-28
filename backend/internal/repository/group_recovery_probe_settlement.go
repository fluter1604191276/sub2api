package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const groupRecoveryProbeBillingBudgetLockKey int64 = 0x4752504255444745

func (r *groupRecoveryProbeRepository) SettleProbe(
	ctx context.Context,
	command *service.GroupRecoveryProbeAtomicSettlementCommand,
) (_ *service.GroupRecoveryProbeAtomicSettlementResult, err error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("group recovery probe repository is unavailable")
	}
	if err := validateGroupRecoveryProbeSettlementCommand(command); err != nil {
		return nil, err
	}
	command.BillingCommand.Normalize()

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	// One global probe budget is configured for the installation. Serializing
	// settlements makes the read-check-write budget decision race-free across
	// workers while keeping ordinary request billing fully concurrent.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, groupRecoveryProbeBillingBudgetLockKey); err != nil {
		return nil, fmt.Errorf("lock group recovery probe budget: %w", err)
	}

	currentStatus, usageLogID, err := lockGroupRecoveryProbeAuditSettlement(ctx, tx, command.AuditID)
	if err != nil {
		return nil, err
	}
	if currentStatus != service.GroupRecoveryProbeSettlementPending {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		tx = nil
		return &service.GroupRecoveryProbeAtomicSettlementResult{
			Status:     currentStatus,
			UsageLogID: usageLogID,
		}, nil
	}

	blockedReason, err := groupRecoveryProbeBudgetBlockReason(ctx, tx, command)
	if err != nil {
		return nil, err
	}
	if blockedReason != "" {
		if err := markGroupRecoveryProbeBudgetBlocked(ctx, tx, command.AuditID, blockedReason); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		tx = nil
		return &service.GroupRecoveryProbeAtomicSettlementResult{
			Status: service.GroupRecoveryProbeSettlementBudgetBlocked,
		}, nil
	}

	billingRepo := &usageBillingRepository{db: r.db}
	applied, err := billingRepo.claimUsageBillingKey(ctx, tx, command.BillingCommand)
	if err != nil {
		return nil, err
	}
	if !applied {
		existingUsageLogID, findErr := findProbeUsageLogID(ctx, tx, command.BillingCommand.RequestID, command.BillingCommand.APIKeyID)
		if findErr != nil {
			return nil, findErr
		}
		if existingUsageLogID == nil {
			return nil, errors.New("probe billing dedup exists without a usage log")
		}
		if err := settleGroupRecoveryProbeAudit(ctx, tx, command, *existingUsageLogID); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		tx = nil
		return &service.GroupRecoveryProbeAtomicSettlementResult{
			Status:     service.GroupRecoveryProbeSettlementSettled,
			UsageLogID: existingUsageLogID,
		}, nil
	}

	applyResult := &service.UsageBillingApplyResult{Applied: true}
	if err := billingRepo.applyUsageBillingEffects(ctx, tx, command.BillingCommand, applyResult); err != nil {
		return nil, err
	}
	usageRepo := newUsageLogRepositoryWithSQL(nil, tx)
	inserted, err := usageRepo.createSingle(ctx, tx, command.UsageLog)
	if err != nil {
		return nil, fmt.Errorf("create probe usage log: %w", err)
	}
	if !inserted || command.UsageLog.ID <= 0 {
		return nil, errors.New("probe usage log already exists without a billing dedup claim")
	}
	if err := settleGroupRecoveryProbeAudit(ctx, tx, command, command.UsageLog.ID); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	tx = nil
	usageLogID = &command.UsageLog.ID
	return &service.GroupRecoveryProbeAtomicSettlementResult{
		Status:         service.GroupRecoveryProbeSettlementSettled,
		BillingApplied: true,
		UsageLogID:     usageLogID,
	}, nil
}

func validateGroupRecoveryProbeSettlementCommand(command *service.GroupRecoveryProbeAtomicSettlementCommand) error {
	const epsilon = 0.000000000001
	if command == nil {
		return errors.New("group recovery probe settlement command is required")
	}
	if command.AuditID <= 0 {
		return errors.New("group recovery probe audit id is required")
	}
	if command.BillingCommand == nil || command.UsageLog == nil {
		return errors.New("group recovery probe billing command and usage log are required")
	}
	if command.ReservationUSD <= 0 || command.DailyBudgetUSD <= 0 || command.SettledCostUSD <= 0 {
		return errors.New("group recovery probe settlement limits and cost must be positive")
	}
	if command.BudgetSince.IsZero() {
		return errors.New("group recovery probe budget start is required")
	}
	if strings.TrimSpace(command.BillingCommand.RequestID) == "" {
		return service.ErrUsageBillingRequestIDRequired
	}
	if command.UsageLog.RequestID != command.BillingCommand.RequestID || command.UsageLog.APIKeyID != command.BillingCommand.APIKeyID {
		return errors.New("group recovery probe usage log identity does not match billing command")
	}
	if command.UsageLog.RequestType != service.RequestTypeProbe {
		return errors.New("group recovery probe usage log must use probe request type")
	}
	if command.BillingCommand.BillingType != service.BillingTypeBalance || command.UsageLog.BillingType != service.BillingTypeBalance {
		return errors.New("group recovery probe settlement must use balance billing")
	}
	if command.BillingCommand.UserID != command.UsageLog.UserID || command.BillingCommand.AccountID != command.UsageLog.AccountID {
		return errors.New("group recovery probe usage log owner does not match billing command")
	}
	if absProbeSettlementDifference(command.BillingCommand.BalanceCost, command.SettledCostUSD) > epsilon ||
		absProbeSettlementDifference(command.BillingCommand.AccountQuotaCost, command.SettledCostUSD) > epsilon ||
		absProbeSettlementDifference(command.UsageLog.ActualCost, command.SettledCostUSD) > epsilon {
		return errors.New("group recovery probe charged cost does not match budgeted settlement cost")
	}
	return nil
}

func absProbeSettlementDifference(left, right float64) float64 {
	difference := left - right
	if difference < 0 {
		return -difference
	}
	return difference
}

func lockGroupRecoveryProbeAuditSettlement(ctx context.Context, tx *sql.Tx, auditID int64) (string, *int64, error) {
	var status string
	var usageLogID *int64
	err := tx.QueryRowContext(ctx, `
		SELECT settlement_status, usage_log_id
		FROM group_recovery_probe_audits
		WHERE id = $1
		FOR UPDATE
	`, auditID).Scan(&status, &usageLogID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, fmt.Errorf("group recovery probe audit %d not found", auditID)
	}
	if err != nil {
		return "", nil, fmt.Errorf("lock group recovery probe audit settlement: %w", err)
	}
	return status, usageLogID, nil
}

func groupRecoveryProbeBudgetBlockReason(
	ctx context.Context,
	tx *sql.Tx,
	command *service.GroupRecoveryProbeAtomicSettlementCommand,
) (string, error) {
	const epsilon = 0.000000000001
	if command.SettledCostUSD > command.ReservationUSD+epsilon {
		return "actual probe cost exceeded the reserved per-attempt limit", nil
	}
	var todaySettled float64
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(settled_cost), 0)
		FROM group_recovery_probe_audits
		WHERE settlement_status = 'settled'
			AND created_at >= $1
	`, command.BudgetSince.UTC()).Scan(&todaySettled); err != nil {
		return "", fmt.Errorf("read group recovery probe daily budget: %w", err)
	}
	if todaySettled+command.SettledCostUSD > command.DailyBudgetUSD+epsilon {
		return "actual probe cost exceeded the remaining daily budget", nil
	}
	return "", nil
}

func markGroupRecoveryProbeBudgetBlocked(ctx context.Context, tx *sql.Tx, auditID int64, reason string) error {
	result, err := tx.ExecContext(ctx, `
		UPDATE group_recovery_probe_audits
		SET settlement_status = 'budget_blocked',
			settled_cost = NULL,
			usage_log_id = NULL,
			billing_user_id = NULL,
			billing_api_key_id = NULL,
			settlement_error = $2,
			cost_status = 'unavailable'
		WHERE id = $1 AND settlement_status = 'pending'
	`, auditID, reason)
	if err != nil {
		return fmt.Errorf("mark group recovery probe budget blocked: %w", err)
	}
	return requireOneProbeAuditUpdate(result, "mark group recovery probe budget blocked")
}

func settleGroupRecoveryProbeAudit(
	ctx context.Context,
	tx *sql.Tx,
	command *service.GroupRecoveryProbeAtomicSettlementCommand,
	usageLogID int64,
) error {
	result, err := tx.ExecContext(ctx, `
		UPDATE group_recovery_probe_audits
		SET settlement_status = 'settled',
			settled_cost = $2,
			usage_log_id = $3,
			billing_user_id = $4,
			billing_api_key_id = $5,
			settlement_error = '',
			cost_status = 'actual'
		WHERE id = $1 AND settlement_status = 'pending'
	`, command.AuditID, command.SettledCostUSD, usageLogID,
		command.BillingCommand.UserID, command.BillingCommand.APIKeyID)
	if err != nil {
		return fmt.Errorf("settle group recovery probe audit: %w", err)
	}
	return requireOneProbeAuditUpdate(result, "settle group recovery probe audit")
}

func requireOneProbeAuditUpdate(result sql.Result, operation string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("%s affected %d rows", operation, affected)
	}
	return nil
}

func findProbeUsageLogID(ctx context.Context, tx *sql.Tx, requestID string, apiKeyID int64) (*int64, error) {
	var usageLogID int64
	err := tx.QueryRowContext(ctx, `
		SELECT id FROM usage_logs WHERE request_id = $1 AND api_key_id = $2
	`, requestID, apiKeyID).Scan(&usageLogID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find probe usage log: %w", err)
	}
	return &usageLogID, nil
}
