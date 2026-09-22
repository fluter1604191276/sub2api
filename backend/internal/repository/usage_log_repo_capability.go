package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

func (r *usageLogRepository) RecordCapabilityObservation(ctx context.Context, observation *service.AccountCapabilityObservation) error {
	if observation == nil || observation.AccountID <= 0 {
		return nil
	}
	if err := validateCapabilityObservation(observation); err != nil {
		return err
	}
	createdAt := observation.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	_, err := r.sql.ExecContext(ctx, `
		WITH inserted AS (
			INSERT INTO account_capability_observations
				(account_id, upstream_model, protocol, capability_type, outcome, reason, request_id, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			RETURNING account_id, upstream_model, protocol, capability_type, outcome, reason, created_at
		), aggregated AS (
			SELECT
				i.account_id, i.upstream_model, i.protocol, i.capability_type,
				CASE WHEN i.outcome = 'success' THEN 1 ELSE 0 END AS success_delta,
				CASE WHEN i.outcome = 'failure' THEN 1 ELSE 0 END AS failure_delta,
				i.outcome, i.reason, i.created_at
			FROM inserted i
		)
		INSERT INTO account_capability_states
			(account_id, upstream_model, protocol, capability_type, state, sample_count,
			 success_count, failure_count, last_success_at, last_failure_at, last_reason, updated_at)
		SELECT
			a.account_id, a.upstream_model, a.protocol, a.capability_type,
			'unknown',
			1, a.success_delta, a.failure_delta,
			CASE WHEN a.outcome = 'success' THEN a.created_at END,
			CASE WHEN a.outcome = 'failure' THEN a.created_at END,
			a.reason, a.created_at
		FROM aggregated a
		ON CONFLICT (account_id, upstream_model, protocol, capability_type)
		DO UPDATE SET
			sample_count = account_capability_states.sample_count + EXCLUDED.sample_count,
			success_count = account_capability_states.success_count + EXCLUDED.success_count,
			failure_count = account_capability_states.failure_count + EXCLUDED.failure_count,
			last_success_at = CASE WHEN EXCLUDED.success_count > 0 THEN EXCLUDED.last_success_at ELSE account_capability_states.last_success_at END,
			last_failure_at = CASE WHEN EXCLUDED.failure_count > 0 THEN EXCLUDED.last_failure_at ELSE account_capability_states.last_failure_at END,
			last_reason = CASE WHEN EXCLUDED.failure_count > 0 THEN EXCLUDED.last_reason ELSE account_capability_states.last_reason END,
			updated_at = EXCLUDED.updated_at,
			state = CASE
				WHEN account_capability_states.sample_count + EXCLUDED.sample_count <= 0 THEN 'unknown'
				WHEN account_capability_states.failure_count + EXCLUDED.failure_count >= 2
					AND account_capability_states.success_count + EXCLUDED.success_count = 0 THEN 'unsupported'
				WHEN account_capability_states.success_count + EXCLUDED.success_count >= 3
					AND (
						COALESCE(EXCLUDED.last_failure_at, account_capability_states.last_failure_at) IS NULL
						OR COALESCE(EXCLUDED.last_failure_at, account_capability_states.last_failure_at) <= EXCLUDED.updated_at - INTERVAL '30 minutes'
					) THEN 'capable'
				ELSE 'degraded'
			END
	`, observation.AccountID, strings.TrimSpace(observation.UpstreamModel), strings.TrimSpace(observation.Protocol),
		string(observation.CapabilityType), string(observation.Outcome), strings.TrimSpace(observation.Reason),
		strings.TrimSpace(observation.RequestID), createdAt)
	return err
}

func validateCapabilityObservation(observation *service.AccountCapabilityObservation) error {
	if observation == nil {
		return nil
	}
	if err := service.ValidateCapabilityTypeForRepository(observation.CapabilityType); err != nil {
		return err
	}
	if observation.Outcome != service.CapabilitySuccess && observation.Outcome != service.CapabilityFailure && observation.Outcome != service.CapabilityExcluded {
		return fmt.Errorf("unknown capability outcome %q", observation.Outcome)
	}
	return nil
}

func (r *usageLogRepository) GetAccountCapabilitySummaryBatch(ctx context.Context, accountIDs []int64) (map[int64]service.AccountCapabilitySummary, error) {
	result := make(map[int64]service.AccountCapabilitySummary, len(accountIDs))
	if len(accountIDs) == 0 {
		return result, nil
	}
	for _, accountID := range accountIDs {
		result[accountID] = service.UnknownAccountCapabilitySummary()
	}
	rows, err := r.sql.QueryContext(ctx, `
		SELECT account_id, capability_type,
		       SUM(sample_count) AS sample_count,
		       SUM(success_count) AS success_count,
		       SUM(failure_count) AS failure_count,
		       MAX(last_success_at) AS last_success_at,
		       MAX(last_failure_at) AS last_failure_at,
		       (ARRAY_AGG(last_reason ORDER BY updated_at DESC))[1] AS last_reason,
		       MAX(updated_at) AS updated_at
		FROM account_capability_states
		WHERE account_id = ANY($1)
		GROUP BY account_id, capability_type
	`, pq.Array(accountIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var accountID int64
		var typ, lastReason string
		var sample, success, failure int64
		var lastSuccess, lastFailure, updated sql.NullTime
		if err := rows.Scan(&accountID, &typ, &sample, &success, &failure, &lastSuccess, &lastFailure, &lastReason, &updated); err != nil {
			return nil, err
		}
		status := service.AccountCapabilityStatusForRepository("", sample, success, failure,
			lastSuccess.Time, lastFailure.Time, updated.Time, lastReason,
			lastSuccess.Valid, lastFailure.Valid, updated.Valid)
		summary := result[accountID]
		switch service.CapabilityType(typ) {
		case service.CapabilityFunctionTool:
			summary.FunctionTool = status
		case service.CapabilityToolRoundtrip:
			summary.ToolRoundtrip = status
		case service.CapabilityClientTool:
			summary.ClientTool = status
		case service.CapabilityTerminalContract:
			summary.TerminalContract = status
		}
		result[accountID] = summary
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
