package repository

import (
	"context"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAIIQDiagnosticsRejectsInvalidPaginationBeforeDatabaseAccess(t *testing.T) {
	repo := NewOpenAIIQRepository(nil)
	for _, tc := range []struct{ page, size int }{
		{math.MaxInt, 50}, {1000001, 20}, {0, 20}, {1, 0}, {1, 51},
	} {
		_, err := repo.Diagnostics(context.Background(), tc.page, tc.size)
		require.ErrorContains(t, err, "invalid OpenAI IQ diagnostics pagination")
	}
}
