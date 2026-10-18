package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestResponseDiagnosticsIndependentInsertAndIdempotency(t *testing.T) {
	db, mock := newSQLMock(t)
	r := &opsRepository{db: db}
	// Only the diagnostic table is written. A repeated server request ID is ignored by PostgreSQL.
	mock.ExpectExec(`INSERT INTO ops_response_diagnostics .* ON CONFLICT \(request_id\) DO NOTHING`).WithArgs(sqlmock.AnyArg(), "server", "", "", "", "", "completed", "", nil, nil, nil, false, true, 200, nil, nil, nil, nil, nil, nil, nil, nil).WillReturnResult(sqlmock.NewResult(1, 1))
	require.NoError(t, r.InsertResponseDiagnostic(context.Background(), &service.OpsResponseDiagnostic{RequestID: "server", Status: "completed", ClientDisconnected: true, HTTPStatus: 200}))
	require.NoError(t, mock.ExpectationsWereMet())
}
func TestResponseDiagnosticsFiltersPaginationAndNullableUsage(t *testing.T) {
	db, mock := newSQLMock(t)
	r := &opsRepository{db: db}
	start := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	uid, aid := int64(331), int64(7900)
	f := &service.OpsResponseDiagnosticFilter{StartTime: &start, EndTime: &end, Status: "incomplete", Reason: "max_output_tokens", UserID: &uid, AccountID: &aid, RequestID: "client", Page: 2, PageSize: 200}
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM ops_response_diagnostics WHERE .*status = \$3.*reason = \$4.*user_id = \$5.*account_id = \$6.*request_id=\$7 OR client_request_id=\$7 OR upstream_request_id=\$7 OR response_id=\$7`).WithArgs(start, end, "incomplete", "max_output_tokens", uid, aid, "client").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(101))
	columns := []string{"id", "created_at", "request_id", "client_request_id", "upstream_request_id", "response_id", "model", "status", "reason", "user_id", "account_id", "group_id", "stream", "client_disconnected", "http_status", "requested_max_output_tokens", "forwarded_max_output_tokens", "upstream_max_output_tokens", "input_tokens", "cache_read_tokens", "cache_creation_tokens", "output_tokens", "reasoning_tokens"}
	mock.ExpectQuery(`SELECT id,created_at.*FROM ops_response_diagnostics.*ORDER BY created_at DESC,id DESC LIMIT \$8 OFFSET \$9`).WithArgs(start, end, "incomplete", "max_output_tokens", uid, aid, "client", 100, 100).WillReturnRows(sqlmock.NewRows(columns).AddRow(1, start, "server", "client", "upstream", "resp-1", "gpt-6.1-sol", "incomplete", "max_output_tokens", uid, aid, nil, true, true, 200, nil, 100, 100, 0, 43228, nil, 1, nil))
	out, err := r.ListResponseDiagnostics(context.Background(), f)
	require.NoError(t, err)
	require.Equal(t, 100, out.PageSize)
	require.EqualValues(t, 101, out.Total)
	require.Len(t, out.Items, 1)
	row := out.Items[0]
	require.Nil(t, row.RequestedMaxOutputTokens)
	require.Nil(t, row.ReasoningTokens)
	require.EqualValues(t, 43228, *row.CacheReadTokens)
	require.EqualValues(t, 0, *row.InputTokens)
	require.Equal(t, 200, row.HTTPStatus)
	require.True(t, row.ClientDisconnected)
	require.NoError(t, mock.ExpectationsWereMet())
}
