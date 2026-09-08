//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestUsageLogRepositorySmartSchedulerDeduplicatesRequestErrors(t *testing.T) {
	ctx := context.Background()
	_, err := integrationDB.ExecContext(ctx, "TRUNCATE ops_error_logs RESTART IDENTITY CASCADE")
	require.NoError(t, err)

	end := time.Now().UTC().Truncate(time.Second)
	accountID := int64(71001)
	_, err = integrationDB.ExecContext(ctx, `
		INSERT INTO ops_error_logs (
			account_id, request_id, error_phase, error_type, error_owner,
			severity, status_code, upstream_status_code, is_business_limited,
			stream, requested_model, inbound_endpoint, user_agent, created_at
		) VALUES
			($1, 'client-wins', 'upstream', 'upstream_error', 'provider', 'error', 502, 502, FALSE, TRUE, 'gpt-5', 'responses', 'codex-cli/1', $2),
			($1, 'client-wins', 'request', 'insufficient_balance', 'client', 'warning', 400, NULL, TRUE, TRUE, 'gpt-5', 'responses', 'codex-cli/1', $3),
			($1, 'client-cancelled', 'upstream', 'upstream_error', 'provider', 'warning', 499, NULL, FALSE, TRUE, 'gpt-5', 'responses', 'codex-cli/1', $3),
			($1, 'client-cancelled-with-upstream', 'upstream', 'upstream_error', 'provider', 'warning', 499, 502, FALSE, TRUE, 'gpt-5', 'responses', 'codex-cli/1', $3),
			($1, 'transient-wins', 'upstream', 'upstream_error', 'provider', 'error', 401, 401, FALSE, TRUE, 'gpt-5', 'responses', 'codex-cli/1', $2),
			($1, 'transient-wins', 'upstream', 'upstream_error', 'provider', 'error', 502, 502, FALSE, TRUE, 'gpt-5', 'responses', 'codex-cli/1', $3),
			($1, 'monitor-transient', 'upstream', 'upstream_error', 'provider', 'error', 502, 502, FALSE, TRUE, 'gpt-5', 'responses', 'sub2api-channel-monitor/1', $3)
	`, accountID, end.Add(-10*time.Minute), end.Add(-time.Minute))
	require.NoError(t, err)

	repo := newUsageLogRepositoryWithSQL(nil, integrationDB)
	statsByAccount, err := repo.GetSmartSchedulerErrorStatsBatch(
		ctx,
		[]int64{accountID},
		end.Add(-24*time.Hour),
		end,
		"gpt-5",
		"responses",
	)
	require.NoError(t, err)

	stats := statsByAccount[accountID]
	require.Equal(t, int64(3), stats.ClientExcludedCount)
	require.Equal(t, int64(1), stats.ProviderTransientFailureCount)
	require.Zero(t, stats.ProviderFailureCount)
	require.Equal(t, int64(1), stats.RecentProviderTransientCount)
	require.Equal(t, int64(1), stats.ImmediateProviderTransientCount)
}
