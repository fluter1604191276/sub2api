package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroupRecoveryProbeRealUsageIndexMigration(t *testing.T) {
	content, err := FS.ReadFile("201_group_recovery_probe_usage_index_notx.sql")
	require.NoError(t, err)
	sql := strings.ToLower(string(content))
	require.Contains(t, sql, "create index concurrently if not exists idx_usage_logs_recovery_probe_real_usage")
	require.Contains(t, sql, "account_id")
	require.Contains(t, sql, "lower(btrim(coalesce(nullif(btrim(requested_model), ''), model)))")
	require.Contains(t, sql, "created_at desc")
	require.Contains(t, sql, "actual_cost > 0")
	require.Contains(t, sql, "request_type <> 6")
	require.Contains(t, sql, "sub2api-channel-monitor")
}
