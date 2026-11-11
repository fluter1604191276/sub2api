//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strings"
	"testing"

	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

// TestChannelMonitorV3MigrationsSyntheticForwardBackupRestore exercises the
// reviewed recovery procedure in one transaction on the disposable PostgreSQL
// created by the integration harness. It does not model a production rollback:
// production must use a separately approved backup and recovery plan.
func TestChannelMonitorV3MigrationsSyntheticForwardBackupRestore(t *testing.T) {
	ctx := context.Background()
	tx := testTx(t)
	const (
		migration314 = "314_channel_monitor_v3_config.sql"
		migration315 = "315_channel_monitor_v3_probes.sql"
		migration316 = "316_channel_monitor_v3_identity.sql"
		migration317 = "317_openai_iq_manual_runs.sql"
	)
	migrations := []string{migration314, migration315, migration316, migration317}

	// Simulate the exact pre-314 schema in the disposable database. Every DDL
	// statement stays in this transaction and is rolled back by testTx cleanup.
	_, err := tx.ExecContext(ctx, `
		DROP TABLE IF EXISTS channel_monitor_v3_keys;
		DROP TABLE IF EXISTS channel_monitor_v3_identity;
		DROP TABLE IF EXISTS channel_monitor_v3_probe_dispatches;
		DROP TABLE IF EXISTS channel_monitor_v3_groups;
		DROP TABLE IF EXISTS channel_monitor_v3_settings;
		DROP INDEX IF EXISTS idx_openai_iq_runs_manual_started;
		ALTER TABLE openai_iq_runs DROP CONSTRAINT IF EXISTS openai_iq_runs_trigger_check;
		ALTER TABLE openai_iq_runs DROP COLUMN IF EXISTS requested_by;
		ALTER TABLE openai_iq_runs DROP COLUMN IF EXISTS trigger`)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `DELETE FROM schema_migrations WHERE filename = ANY($1)`, pqTextArray(migrations))
	require.NoError(t, err)

	var userID, groupID, keyID int64
	require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO users(email,password_hash) VALUES('cmv3-rollback@example.invalid','fixture') RETURNING id`).Scan(&userID))
	require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO groups(name,platform,status) VALUES('cmv3-rollback','openai','active') RETURNING id`).Scan(&groupID))
	require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO api_keys(user_id,key,name,group_id) VALUES($1,'cmv3-rollback-key','cmv3 rollback',$2) RETURNING id`, userID, groupID).Scan(&keyID))

	for _, name := range []string{migration314, migration315, migration316} {
		applySyntheticMigration(t, tx, name)
	}
	var scheduledRunID int64
	require.NoError(t, tx.QueryRowContext(ctx, `
		INSERT INTO openai_iq_runs(started_at,question,expected_answer,model,settings_snapshot,status)
		VALUES(NOW(),'pre-317 scheduled fixture','15','gpt-6-astra','{}','complete') RETURNING id`).Scan(&scheduledRunID))
	applySyntheticMigration(t, tx, migration317)

	// Forward verification uses synthetic, referentially valid rows in all four
	// migrations and confirms the pre-existing IQ row gained the scheduled default.
	_, err = tx.ExecContext(ctx, `INSERT INTO channel_monitor_v3_settings(singleton,enabled,daily_budget_usd) VALUES(TRUE,TRUE,3.5)`)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `INSERT INTO channel_monitor_v3_groups(group_id,visible,probe_interval_seconds,test_model) VALUES($1,TRUE,600,'gpt-6-astra')`, groupID)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `INSERT INTO channel_monitor_v3_probe_dispatches(dispatch_id,group_id,model,platform,budget_day,reserved_usd,client_request_id,status) VALUES('00000000-0000-0000-0000-000000000317',$1,'gpt-6-astra','openai','2099-01-01',0.1,'cmv3-rollback-317','settled')`, groupID)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `INSERT INTO channel_monitor_v3_identity(singleton,user_id,identity_marker) VALUES(TRUE,$1,'fixture-marker')`, userID)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `INSERT INTO channel_monitor_v3_keys(group_id,key_id,user_id) VALUES($1,$2,$3)`, groupID, keyID, userID)
	require.NoError(t, err)
	var trigger string
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT trigger FROM openai_iq_runs WHERE id=$1`, scheduledRunID).Scan(&trigger))
	require.Equal(t, "scheduled", trigger)
	var manualRunID int64
	require.NoError(t, tx.QueryRowContext(ctx, `
		INSERT INTO openai_iq_runs(started_at,question,expected_answer,model,settings_snapshot,status,trigger,requested_by)
		VALUES(NOW(),'manual fixture','15','gpt-6-astra','{}','complete','manual',$1) RETURNING id`, userID).Scan(&manualRunID))

	// Back up affected synthetic data before the reviewed inverse DDL. This is
	// the minimum recoverable shape; real production recovery needs an approved
	// database backup rather than this in-transaction fixture.
	_, err = tx.ExecContext(ctx, `
		CREATE TEMP TABLE cmv3_backup_settings AS TABLE channel_monitor_v3_settings;
		CREATE TEMP TABLE cmv3_backup_groups AS TABLE channel_monitor_v3_groups;
		CREATE TEMP TABLE cmv3_backup_dispatches AS TABLE channel_monitor_v3_probe_dispatches;
		CREATE TEMP TABLE cmv3_backup_identity AS TABLE channel_monitor_v3_identity;
		CREATE TEMP TABLE cmv3_backup_keys AS TABLE channel_monitor_v3_keys;
		CREATE TEMP TABLE cmv3_backup_iq_trigger AS SELECT id, trigger, requested_by FROM openai_iq_runs WHERE FALSE`)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `INSERT INTO cmv3_backup_iq_trigger SELECT id, trigger, requested_by FROM openai_iq_runs WHERE id IN ($1,$2)`, scheduledRunID, manualRunID)
	require.NoError(t, err)

	// Reviewed inverse: drop only objects introduced by 314-317, and remove the
	// matching forward-only ledger rows so the exact SQL can be applied again.
	_, err = tx.ExecContext(ctx, `
		DROP INDEX IF EXISTS idx_openai_iq_runs_manual_started;
		ALTER TABLE openai_iq_runs DROP CONSTRAINT IF EXISTS openai_iq_runs_trigger_check;
		ALTER TABLE openai_iq_runs DROP COLUMN requested_by;
		ALTER TABLE openai_iq_runs DROP COLUMN trigger;
		DROP TABLE channel_monitor_v3_keys;
		DROP TABLE channel_monitor_v3_identity;
		DROP TABLE channel_monitor_v3_probe_dispatches;
		DROP TABLE channel_monitor_v3_groups;
		DROP TABLE channel_monitor_v3_settings`)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `DELETE FROM schema_migrations WHERE filename = ANY($1)`, pqTextArray(migrations))
	require.NoError(t, err)
	var preservedQuestion string
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT question FROM openai_iq_runs WHERE id=$1`, scheduledRunID).Scan(&preservedQuestion))
	require.Equal(t, "pre-317 scheduled fixture", preservedQuestion, "inverse DDL must not delete pre-existing IQ runs")
	requireNoColumn(t, tx, "openai_iq_runs", "trigger")
	requireNoTable(t, tx, "channel_monitor_v3_settings")
	var ledgerRows int
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE filename = ANY($1)`, pqTextArray(migrations)).Scan(&ledgerRows))
	require.Zero(t, ledgerRows)

	for _, name := range migrations {
		applySyntheticMigration(t, tx, name)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO channel_monitor_v3_settings SELECT * FROM cmv3_backup_settings;
		INSERT INTO channel_monitor_v3_groups SELECT * FROM cmv3_backup_groups;
		INSERT INTO channel_monitor_v3_probe_dispatches SELECT * FROM cmv3_backup_dispatches;
		INSERT INTO channel_monitor_v3_identity SELECT * FROM cmv3_backup_identity;
		INSERT INTO channel_monitor_v3_keys SELECT * FROM cmv3_backup_keys;
		UPDATE openai_iq_runs target SET trigger=backup.trigger, requested_by=backup.requested_by
		FROM cmv3_backup_iq_trigger backup WHERE target.id=backup.id`)
	require.NoError(t, err)
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT trigger FROM openai_iq_runs WHERE id=$1`, scheduledRunID).Scan(&trigger))
	require.Equal(t, "scheduled", trigger)
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT trigger FROM openai_iq_runs WHERE id=$1`, manualRunID).Scan(&trigger))
	require.Equal(t, "manual", trigger)
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM channel_monitor_v3_probe_dispatches`).Scan(&ledgerRows))
	require.Equal(t, 1, ledgerRows)
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE filename = ANY($1)`, pqTextArray(migrations)).Scan(&ledgerRows))
	require.Equal(t, len(migrations), ledgerRows)
}

func applySyntheticMigration(t *testing.T, tx sqlExecer, name string) {
	t.Helper()
	content, err := dbmigrations.FS.ReadFile(name)
	require.NoError(t, err)
	sqlText := strings.TrimSpace(string(content))
	_, err = tx.ExecContext(context.Background(), sqlText)
	require.NoError(t, err)
	sum := sha256.Sum256([]byte(sqlText))
	_, err = tx.ExecContext(context.Background(), `INSERT INTO schema_migrations(filename,checksum) VALUES($1,$2)`, name, hex.EncodeToString(sum[:]))
	require.NoError(t, err)
}

type sqlExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func pqTextArray(items []string) string {
	return "{" + strings.Join(items, ",") + "}"
}

func requireNoColumn(t *testing.T, tx sqlExecer, table, column string) {
	t.Helper()
	rows, err := tx.QueryContext(context.Background(), `
		SELECT column_name FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = $1 AND column_name = $2`, table, column)
	require.NoError(t, err)
	defer rows.Close()
	require.False(t, rows.Next(), "column %s.%s should not exist", table, column)
}

func requireNoTable(t *testing.T, tx sqlExecer, table string) {
	t.Helper()
	rows, err := tx.QueryContext(context.Background(), `
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = current_schema() AND table_name = $1`, table)
	require.NoError(t, err)
	defer rows.Close()
	require.False(t, rows.Next(), "table %s should not exist", table)
}
