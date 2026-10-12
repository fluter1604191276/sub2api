//go:build localpriority

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountPriorityRealPostgres(t *testing.T) {
	dsn := os.Getenv("SUB2_PRIORITY_TEST_DSN")
	// This fixture intentionally accepts only our local disposable Unix socket.
	if !strings.Contains(dsn, "host=/tmp/sub2-priority-") {
		t.Skip("private local priority fixture not supplied")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	schema := fmt.Sprintf("priority_fixture_%d", time.Now().UnixNano())
	_, err = db.ExecContext(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	defer func() { _, _ = db.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE") }()
	_, err = db.ExecContext(ctx, "SET search_path TO "+schema)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `CREATE TABLE accounts(id bigint PRIMARY KEY,name text,priority integer DEFAULT 50,rate_multiplier numeric(10,4),deleted_at timestamptz,updated_at timestamptz,credentials jsonb,quota_used numeric);
	 CREATE TABLE scheduler_outbox(event_type text,account_id bigint,group_id bigint,payload jsonb,dedup_key text);
	 CREATE UNIQUE INDEX outbox_dedup ON scheduler_outbox(dedup_key) WHERE dedup_key IS NOT NULL;
	 INSERT INTO accounts VALUES(1,'synthetic manual',7,2.007,NULL,'2020-01-01','{"sentinel":true}',17.25),(2,'synthetic zero',0,0,NULL,'2020-01-01','{"sentinel":true}',19.25),(3,'synthetic invalid',50,-1,NULL,'2020-01-01','{"sentinel":true}',21.25);`)
	require.NoError(t, err)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	repo := newAccountRepositoryWithSQL(client, db, nil)
	svc := service.NewAdminService(nil, nil, nil, repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, client, nil, nil, nil, nil, nil, nil, nil, nil, nil).(service.AccountPriorityConversionAdminService)

	t.Run("decimal preview no write and per-account confirmed overwrite", func(t *testing.T) {
		preview, err := svc.PreviewAccountPriorityConversion(ctx, []int64{1, 2, 3})
		require.NoError(t, err)
		require.Equal(t, "2.0070", *preview.Rows[0].RateMultiplier)
		require.Equal(t, 2007, *preview.Rows[0].NewPriority)
		require.Equal(t, 1, *preview.Rows[1].NewPriority)
		require.Equal(t, "negative_multiplier", preview.Rows[2].Reason)
		var priority int
		require.NoError(t, db.QueryRowContext(ctx, "SELECT priority FROM accounts WHERE id=1").Scan(&priority))
		require.Equal(t, 7, priority)
		result, err := svc.ApplyAccountPriorityConversion(ctx, []service.AccountPriorityConfirmation{{AccountID: 1, RateMultiplier: "2.0070", OldPriority: 7}, {AccountID: 2, RateMultiplier: "0.0000", OldPriority: 0}, {AccountID: 3, RateMultiplier: "-1.0000", OldPriority: 50}})
		require.NoError(t, err)
		require.Equal(t, 2, result.Updated)
		var unchanged bool
		require.NoError(t, db.QueryRowContext(ctx, `SELECT bool_and(credentials='{"sentinel":true}'::jsonb AND quota_used=CASE id WHEN 1 THEN 17.25 WHEN 2 THEN 19.25 ELSE 21.25 END AND rate_multiplier=CASE id WHEN 1 THEN 2.007 WHEN 2 THEN 0 ELSE -1 END) FROM accounts`).Scan(&unchanged))
		require.True(t, unchanged)
		require.NoError(t, db.QueryRowContext(ctx, "SELECT priority FROM accounts WHERE id=3").Scan(&priority))
		require.Equal(t, 50, priority)
	})
	t.Run("stale conditional write makes no change", func(t *testing.T) {
		ok, err := repo.UpdatePriorityIfUnchanged(ctx, 1, "2.0070", 7, 2008)
		require.NoError(t, err)
		require.False(t, ok)
		_, err = db.ExecContext(ctx, "UPDATE accounts SET rate_multiplier=3 WHERE id=1")
		require.NoError(t, err)
		ok, err = repo.UpdatePriorityIfUnchanged(ctx, 1, "2.0070", 2007, 2008)
		require.NoError(t, err)
		require.False(t, ok)
	})
	t.Run("scheduler outbox failure rolls back priority", func(t *testing.T) {
		_, err = db.ExecContext(ctx, "ALTER TABLE scheduler_outbox RENAME TO hidden_outbox")
		require.NoError(t, err)
		ok, err := repo.UpdatePriorityIfUnchanged(ctx, 1, "3.0000", 2007, 3000)
		require.Error(t, err)
		require.False(t, ok)
		var priority int
		require.NoError(t, db.QueryRowContext(ctx, "SELECT priority FROM accounts WHERE id=1").Scan(&priority))
		require.Equal(t, 2007, priority)
		_, err = db.ExecContext(ctx, "ALTER TABLE hidden_outbox RENAME TO scheduler_outbox")
		require.NoError(t, err)
	})
	t.Run("deleted account cannot be updated", func(t *testing.T) {
		_, err = db.ExecContext(ctx, "UPDATE accounts SET deleted_at=NOW() WHERE id=1")
		require.NoError(t, err)
		ok, err := repo.UpdatePriorityIfUnchanged(ctx, 1, "3.0000", 2007, 3000)
		require.NoError(t, err)
		require.False(t, ok)
	})
}
