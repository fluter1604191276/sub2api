package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestUserPlatformQuotasPurgeUnlimitedMigration restricts policy B to archiving
// active unlimited rows without changing counters, windows or audit timestamps.
func TestUserPlatformQuotasPurgeUnlimitedMigration(t *testing.T) {
	content, err := FS.ReadFile("238_purge_unlimited_user_platform_quotas.sql")
	require.NoError(t, err)

	// Strip comments; allow only this narrowly scoped UPDATE.
	var stmts []string
	for _, line := range strings.Split(string(content), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			continue
		}
		stmts = append(stmts, trimmed)
	}
	sql := strings.Join(stmts, " ")
	require.Equal(t, "UPDATE user_platform_quotas SET deleted_at = CURRENT_TIMESTAMP WHERE deleted_at IS NULL AND daily_limit_usd IS NULL AND weekly_limit_usd IS NULL AND monthly_limit_usd IS NULL;", sql)
}
