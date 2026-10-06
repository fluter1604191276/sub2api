package migrations

import (
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestFluterReasoningDefaultMetadataDoesNotRewritePrices(t *testing.T) {
	b, err := FS.ReadFile("242_fluter_reasoning_defaults_metadata.sql")
	require.NoError(t, err)
	sql := string(b)
	require.Contains(t, sql, "Fable 5.1 max=3, others=1")
	require.Contains(t, sql, "explicit max=1 disables surcharge")
	var statements []string
	for _, line := range strings.Split(sql, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "--") {
			statements = append(statements, line)
		}
	}
	executable := strings.ToUpper(strings.Join(statements, " "))
	require.NotContains(t, executable, "UPDATE ")
	require.NotContains(t, executable, "DELETE ")
	require.NotContains(t, executable, "INSERT ")
	require.Equal(t, 2, strings.Count(executable, "COMMENT ON COLUMN"))
}
