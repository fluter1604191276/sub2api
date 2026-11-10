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

func TestResponseDiagnosticsPersistsWebSocketTurnsIndependently(t *testing.T) {
	ctx := context.Background()
	repo := &opsRepository{db: integrationDB}
	requestID := "response-diagnostic-" + uuid.NewString()
	turnOne := uuid.NewString()
	turnTwo := uuid.NewString()
	defer func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM ops_response_diagnostics WHERE request_id = $1", requestID)
	}()

	insert := func(turnID string) {
		t.Helper()
		require.NoError(t, repo.InsertResponseDiagnostic(ctx, &service.OpsResponseDiagnostic{
			CreatedAt: time.Now().UTC(), RequestID: requestID, TurnID: turnID, Status: "completed",
		}))
	}
	insert(turnOne)
	insert(turnTwo)
	insert(turnOne) // exactly one row per root request + turn key

	list, err := repo.ListResponseDiagnostics(ctx, &service.OpsResponseDiagnosticFilter{
		StartTime: diagnosticTimePtr(time.Now().Add(-time.Minute)), EndTime: diagnosticTimePtr(time.Now().Add(time.Minute)), RequestID: requestID,
	})
	require.NoError(t, err)
	require.Len(t, list.Items, 2)
	gotTurns := map[string]bool{}
	for _, item := range list.Items {
		gotTurns[item.TurnID] = true
	}
	require.True(t, gotTurns[turnOne])
	require.True(t, gotTurns[turnTwo])

	insert("")
	insert("") // ordinary HTTP remains idempotent
	var count int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM ops_response_diagnostics WHERE request_id = $1", requestID).Scan(&count))
	require.Equal(t, 3, count)
}

func diagnosticTimePtr(v time.Time) *time.Time { return &v }
