package repository

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountModelSyncCredentialsAreConditionalAndAtomic(t *testing.T) {
	for _, test := range []struct {
		name      string
		rows      int64
		outboxErr error
		wantErr   error
	}{
		{name: "matching preview", rows: 1},
		{name: "concurrent edit", rows: 0, wantErr: service.ErrAccountModelSyncConflict},
		{name: "outbox failure", rows: 1, outboxErr: errors.New("outbox unavailable"), wantErr: errors.New("outbox unavailable")},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			repo := newAccountRepositoryWithSQL(client, db, nil)
			expected := time.Date(2026, 9, 9, 0, 0, 0, 123000, time.UTC)
			mock.ExpectBegin()
			mock.ExpectExec(`(?s)`+regexp.QuoteMeta("UPDATE accounts")+`.*`+regexp.QuoteMeta("extra = COALESCE(CASE")+`.*`+regexp.QuoteMeta("END, '{}'::jsonb) || $4::jsonb")+`.*`+regexp.QuoteMeta("WHERE id = $2 AND deleted_at IS NULL")+`.*`+regexp.QuoteMeta("AND updated_at = $3")).
				WithArgs(`{"model_mapping":{"model-b":"model-b"}}`, int64(17), expected, `{"available_models":["model-b"]}`).
				WillReturnResult(sqlmock.NewResult(0, test.rows))
			if test.rows > 0 {
				outbox := mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox")).
					WithArgs(service.SchedulerOutboxEventAccountChanged, int64(17), nil, nil, sqlmock.AnyArg())
				if test.outboxErr != nil {
					outbox.WillReturnError(test.outboxErr)
				} else {
					outbox.WillReturnResult(sqlmock.NewResult(1, 1))
				}
			}
			if test.wantErr != nil {
				mock.ExpectRollback()
			} else {
				mock.ExpectCommit()
			}
			err = repo.UpdateModelMappingIfUnchanged(context.Background(), 17, map[string]any{"model_mapping": map[string]string{"model-b": "model-b"}}, map[string]any{"available_models": []string{"model-b"}}, expected)
			if test.wantErr != nil {
				require.ErrorContains(t, err, test.wantErr.Error())
			} else {
				require.NoError(t, err)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
