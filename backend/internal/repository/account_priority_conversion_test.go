package repository

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountPriorityConditionalWriteAndOutboxAreAtomic(t *testing.T) {
	for _, test := range []struct {
		name        string
		rows        int64
		outboxError bool
		commitError bool
	}{
		{"confirmed", 1, false, false}, {"changed or deleted", 0, false, false}, {"outbox failure", 1, true, false}, {"commit acknowledgement lost", 1, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			repo := newAccountRepositoryWithSQL(client, db, nil)
			mock.ExpectBegin()
			mock.ExpectExec(regexp.QuoteMeta("UPDATE accounts SET priority = $1, updated_at = NOW()")+`\s+`+regexp.QuoteMeta("WHERE id = $2 AND deleted_at IS NULL AND rate_multiplier = $3::numeric AND priority = $4")).WithArgs(2007, int64(17), "2.0070", 50).WillReturnResult(sqlmock.NewResult(0, test.rows))
			if test.rows > 0 {
				x := mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox")).WithArgs(service.SchedulerOutboxEventAccountChanged, int64(17), nil, nil, sqlmock.AnyArg())
				if test.outboxError {
					x.WillReturnError(errors.New("outbox unavailable"))
				} else {
					x.WillReturnResult(sqlmock.NewResult(1, 1))
				}
			}
			if test.rows == 0 || test.outboxError {
				mock.ExpectRollback()
			} else if test.commitError {
				mock.ExpectCommit().WillReturnError(errors.New("ack lost"))
			} else {
				mock.ExpectCommit()
			}
			ok, err := repo.UpdatePriorityIfUnchanged(context.Background(), 17, "2.0070", 50, 2007)
			if test.outboxError || test.commitError {
				require.Error(t, err)
				require.False(t, ok)
			} else {
				require.NoError(t, err)
				require.Equal(t, test.rows > 0, ok)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestAccountPriorityPreviewReadsOwnDecimalWithoutCredentials(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := newAccountRepositoryWithSQL(nil, db, nil)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, name, priority, rate_multiplier::text") + `\s+` + regexp.QuoteMeta("FROM accounts WHERE id = ANY($1) AND deleted_at IS NULL")).WithArgs("{17,19}").WillReturnRows(sqlmock.NewRows([]string{"id", "name", "priority", "rate_multiplier"}).AddRow(17, "synthetic shadow", 0, "2.0070").AddRow(19, "invalid legacy", 50, nil))
	rows, err := repo.GetPriorityConversionAccounts(context.Background(), []int64{17, 19})
	require.NoError(t, err)
	require.Equal(t, 0, rows[0].OldPriority)
	require.Equal(t, "2.0070", *rows[0].RateMultiplier)
	require.Nil(t, rows[1].RateMultiplier)
	require.NoError(t, mock.ExpectationsWereMet())
}
