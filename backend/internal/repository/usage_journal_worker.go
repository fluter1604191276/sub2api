package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type UsageJournalRepository struct{ db *sql.DB }

func NewUsageJournalRepository(db *sql.DB) *UsageJournalRepository {
	return &UsageJournalRepository{db: db}
}

// ProcessNext never calls billing. The journal row, usage INSERT and completion
// share a transaction; process/connection death releases the claim by rollback.
func (r *UsageJournalRepository) ProcessNext(ctx context.Context) (bool, error) {
	tx, e := r.db.BeginTx(ctx, nil)
	if e != nil {
		return false, e
	}
	// Rollback is cleanup after every path, including successful Commit.
	defer func() { _ = tx.Rollback() }()
	var key, raw, sha, fingerprint string
	var apiKeyID int64
	e = tx.QueryRowContext(ctx, `SELECT request_id,api_key_id,payload_json,snapshot_sha256,request_fingerprint FROM usage_recovery_journal_v1 WHERE state='pending' AND retry_after<=now() ORDER BY retry_after,created_at,request_id,api_key_id FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&key, &apiKeyID, &raw, &sha, &fingerprint)
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	var snapshot service.UsageJournalSnapshot
	log, validationErr := decodeUsageJournalRow(raw, sha, key, apiKeyID, fingerprint, &snapshot)
	if validationErr != nil {
		return true, quarantineUsageJournal(ctx, tx, key, apiKeyID, "invalid_snapshot")
	}
	if _, e = tx.ExecContext(ctx, "SAVEPOINT usage_write"); e != nil {
		return true, e
	}
	// Keep the same lock order as retention: watermark before usage row. This
	// also protects an existing usage row while marking its journal complete.
	var stateID int
	e = tx.QueryRowContext(ctx, `SELECT id FROM usage_group_rollup_state WHERE id=1 FOR KEY SHARE`).Scan(&stateID)
	if e == nil {
		e = execUsageLogInsertNoResult(ctx, tx, prepareUsageLogInsert(log))
	}
	if e == nil {
		var matches bool
		matches, e = usageJournalLogMatches(ctx, tx, log)
		if e == nil && !matches {
			if _, e = tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT usage_write"); e != nil {
				return true, e
			}
			return true, quarantineUsageJournal(ctx, tx, key, apiKeyID, "usage_row_conflict")
		}
	}
	if e != nil {
		_ = tx.Rollback()
		if class := usageJournalPermanentWriteClass(e); class != "" {
			return true, r.quarantineFailedUsageWrite(key, apiKeyID, sha, class)
		}
		r.deferUsageJournal(key, apiKeyID, sha)
		return true, e
	}
	_, e = tx.ExecContext(ctx, `UPDATE usage_recovery_journal_v1 SET state='done',completed_at=now(),last_error_class=NULL WHERE request_id=$1 AND api_key_id=$2 AND state='pending'`, key, apiKeyID)
	if e != nil {
		return true, e
	}
	if e = tx.Commit(); e != nil {
		resolve, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		var done bool
		resolveErr := r.db.QueryRowContext(resolve, `SELECT state='done' AND snapshot_sha256=$3 FROM usage_recovery_journal_v1 WHERE request_id=$1 AND api_key_id=$2`, key, apiKeyID, sha).Scan(&done)
		if resolveErr == nil && done {
			return true, nil
		}
		return true, fmt.Errorf("usage journal commit uncertain: %w", e)
	}
	return true, nil
}

// A frozen snapshot cannot repair a deleted FK target or a rejected immutable
// scalar. Retain it for review instead of occupying the retry queue forever.
// Lock/timeouts, connection loss and schema availability remain retryable.
func usageJournalPermanentWriteClass(err error) string {
	var pgErr *pq.Error
	if !errors.As(err, &pgErr) {
		return ""
	}
	switch string(pgErr.Code) {
	case "23503":
		return "missing_reference"
	case "22001":
		return "field_too_long"
	case "22003":
		return "numeric_range"
	case "23502":
		return "required_field"
	case "23514":
		return "constraint_conflict"
	case "22P02":
		return "invalid_field"
	}
	return ""
}

func (r *UsageJournalRepository) quarantineFailedUsageWrite(key string, apiKeyID int64, sha, class string) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, e := r.db.ExecContext(ctx, `UPDATE usage_recovery_journal_v1 SET state='quarantined',attempts=attempts+1,last_error_class=$4 WHERE request_id=$1 AND api_key_id=$2 AND snapshot_sha256=$3 AND state='pending'`, key, apiKeyID, sha, class)
	return e
}

func decodeUsageJournalRow(raw, sha, key string, apiKeyID int64, fingerprint string, snapshot *service.UsageJournalSnapshot) (*service.UsageLog, error) {
	if len(raw) > 1024*1024 {
		return nil, errors.New("oversized usage snapshot")
	}
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(snapshot); e != nil {
		return nil, e
	}
	if snapshot.SHA256 != sha || snapshot.RequestID != key || snapshot.APIKeyID != apiKeyID || snapshot.RequestFingerprint != fingerprint {
		return nil, errors.New("journal row envelope mismatch")
	}
	var cmd service.UsageBillingCommand
	d = json.NewDecoder(strings.NewReader(string(snapshot.BillingCommandJSON)))
	d.DisallowUnknownFields()
	if e := d.Decode(&cmd); e != nil {
		return nil, e
	}
	if cmd.UsageJournalSnapshot != nil {
		return nil, errors.New("nested snapshot forbidden")
	}
	if e := snapshot.Validate(&cmd); e != nil {
		return nil, e
	}
	return snapshot.DecodeLog()
}

func quarantineUsageJournal(ctx context.Context, tx *sql.Tx, key string, apiKeyID int64, class string) error {
	_, e := tx.ExecContext(ctx, `UPDATE usage_recovery_journal_v1 SET state='quarantined',last_error_class=$3 WHERE request_id=$1 AND api_key_id=$2 AND state='pending'`, key, apiKeyID, class)
	if e != nil {
		return e
	}
	return tx.Commit()
}

func (r *UsageJournalRepository) deferUsageJournal(key string, apiKeyID int64, sha string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	// A concurrent worker may have completed it after rollback. Never overwrite
	// done or its immutable payload, and never store raw payload/error strings.
	_, _ = r.db.ExecContext(ctx, `UPDATE usage_recovery_journal_v1 SET attempts=attempts+1,retry_after=now()+make_interval(secs=>LEAST(60,POWER(2,LEAST(attempts,5)))::double precision),last_error_class='transient_write' WHERE request_id=$1 AND api_key_id=$2 AND snapshot_sha256=$3 AND state='pending'`, key, apiKeyID, sha)
}

var usageJournalSQLType = regexp.MustCompile(`^(bigint|integer|smallint|boolean|text|jsonb|numeric(\([0-9]+,[0-9]+\))?|character varying(\([0-9]+\))?|timestamp(\([0-9]+\))? with time zone)$`)

// Compare every scalar the actual INSERT persists, using the table's actual
// typmods. Numeric rounding, JSON key order and timestamp precision are thus
// PostgreSQL's own representation, rather than float/JSON byte approximations.
func usageJournalLogMatches(ctx context.Context, tx *sql.Tx, log *service.UsageLog) (bool, error) {
	cols := strings.Split(usageLogSelectColumns, ", ")[1:]
	p := prepareUsageLogInsert(log)
	if len(cols) != len(p.args) {
		return false, errors.New("usage scalar shape changed")
	}
	rows, e := tx.QueryContext(ctx, `SELECT attname,format_type(atttypid,atttypmod) FROM pg_attribute WHERE attrelid='usage_logs'::regclass AND attnum>0 AND NOT attisdropped`)
	if e != nil {
		return false, e
	}
	types := map[string]string{}
	for rows.Next() {
		var col, typ string
		if e = rows.Scan(&col, &typ); e != nil {
			_ = rows.Close() // Preserve the original scan failure.
			return false, e
		}
		types[col] = typ
	}
	e = rows.Err()
	if closeErr := rows.Close(); e == nil {
		e = closeErr
	}
	if e != nil {
		return false, e
	}
	casts := make([]string, len(cols))
	for i, col := range cols {
		typ := types[col]
		if !usageJournalSQLType.MatchString(typ) {
			return false, fmt.Errorf("unsupported usage scalar type for %s", col)
		}
		casts[i] = fmt.Sprintf("CAST($%d AS %s)", i+1, typ)
	}
	query := fmt.Sprintf("SELECT ROW(%s) IS NOT DISTINCT FROM ROW(%s) FROM usage_logs WHERE request_id=$%d AND api_key_id=$%d FOR KEY SHARE", strings.Join(cols, ","), strings.Join(casts, ","), len(cols)+1, len(cols)+2)
	args := append(p.args, log.RequestID, log.APIKeyID)
	var matches bool
	e = tx.QueryRowContext(ctx, query, args...).Scan(&matches)
	return matches, e
}

const UsageJournalSchemaVersion = "sub2api.usage-journal.v1.activation-1"

// Bound to the reviewed v1.up.sql catalog, including defaults/checks/function body.
// Updating a schema requires a new reviewed identity, never runtime auto-migration.
const UsageJournalSchemaSignature = "25b9c552876f1dcf691de67d0f0e7aa4ce601e301cb89314b2862a0bbeb7353b"

const usageJournalCatalogSQL = `WITH target AS (
 SELECT c.oid,c.relkind,c.relpersistence,c.relrowsecurity,c.relforcerowsecurity FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname=current_schema() AND c.relname='usage_recovery_journal_v1'
)
SELECT COALESCE((SELECT obj_description(oid,'pg_class') FROM target),''),
jsonb_build_object(
 'kind',(SELECT relkind::text FROM target),
 'persistence',(SELECT relpersistence::text FROM target),
 'row_security',(SELECT relrowsecurity FROM target),
 'force_row_security',(SELECT relforcerowsecurity FROM target),
 'columns',COALESCE((SELECT jsonb_agg(jsonb_build_object('name',a.attname,'type',format_type(a.atttypid,a.atttypmod),'required',a.attnotnull,'default',COALESCE(pg_get_expr(d.adbin,d.adrelid),'')) ORDER BY a.attnum) FROM pg_attribute a JOIN target t ON t.oid=a.attrelid LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum WHERE a.attnum>0 AND NOT a.attisdropped),'[]'::jsonb),
 'constraints',COALESCE((SELECT jsonb_agg(jsonb_build_object('name',c.conname,'definition',pg_get_constraintdef(c.oid),'validated',c.convalidated) ORDER BY c.conname) FROM pg_constraint c JOIN target t ON t.oid=c.conrelid),'[]'::jsonb),
 'indexes',COALESCE((SELECT jsonb_agg(jsonb_build_object('name',c.relname,'definition',replace(pg_get_indexdef(i.indexrelid),quote_ident(current_schema())||'.',''),'valid',i.indisvalid,'ready',i.indisready) ORDER BY c.relname) FROM pg_index i JOIN target t ON t.oid=i.indrelid JOIN pg_class c ON c.oid=i.indexrelid),'[]'::jsonb),
 'triggers',COALESCE((SELECT jsonb_agg(jsonb_build_object('name',x.tgname,'definition',replace(pg_get_triggerdef(x.oid),quote_ident(current_schema())||'.',''),'enabled',x.tgenabled,'function',p.proname,'schema',(n.nspname=current_schema()),'body',p.prosrc,'language',l.lanname,'security_definer',p.prosecdef,'arguments',p.pronargs,'return_type',format_type(p.prorettype,NULL),'config',p.proconfig) ORDER BY x.tgname) FROM pg_trigger x JOIN target t ON t.oid=x.tgrelid JOIN pg_proc p ON p.oid=x.tgfoid JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang WHERE NOT x.tgisinternal),'[]'::jsonb)
)::text`

func normalizeJournalCatalog(v any) any {
	switch x := v.(type) {
	case string:
		return strings.Join(strings.Fields(x), " ")
	case []any:
		for i := range x {
			x[i] = normalizeJournalCatalog(x[i])
		}
		return x
	case map[string]any:
		for k, v := range x {
			x[k] = normalizeJournalCatalog(v)
		}
		return x
	}
	return v
}
func journalCatalogIdentity(ctx context.Context, q usageJournalQuerier) (string, string, error) {
	var version, raw string
	if e := q.QueryRowContext(ctx, usageJournalCatalogSQL).Scan(&version, &raw); e != nil {
		return "", "", e
	}
	var catalog any
	if e := json.Unmarshal([]byte(raw), &catalog); e != nil {
		return "", "", e
	}
	b, e := json.Marshal(normalizeJournalCatalog(catalog))
	if e != nil {
		return "", "", e
	}
	h := sha256.Sum256(b)
	return version, hex.EncodeToString(h[:]), nil
}

// CatalogIdentity exposes only static schema identities, never financial data.
func (r *UsageJournalRepository) CatalogIdentity(ctx context.Context) (string, string, error) {
	return journalCatalogIdentity(ctx, r.db)
}
func (r *UsageJournalRepository) ValidateSchema(ctx context.Context) error {
	tx, e := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if e != nil {
		return e
	}
	defer func() { _ = tx.Rollback() }()
	var trusted bool
	if e = tx.QueryRowContext(ctx, `SELECT bool_and(to_regclass(x.name)=to_regclass(format('%I.%I',current_schema(),x.name))) FROM (VALUES ('usage_recovery_journal_v1'),('usage_logs'),('usage_group_rollup_state'),('usage_billing_dedup'),('usage_billing_dedup_archive')) x(name)`).Scan(&trusted); e != nil {
		return e
	}
	if !trusted {
		return errors.New("usage journal relation resolution differs from trusted current schema")
	}
	version, signature, e := journalCatalogIdentity(ctx, tx)
	if e != nil {
		return e
	}
	if version != UsageJournalSchemaVersion || signature != UsageJournalSchemaSignature {
		return errors.New("usage journal schema version or full catalog signature mismatch")
	}
	var allowed bool
	e = tx.QueryRowContext(ctx, `SELECT has_table_privilege(current_user,'usage_recovery_journal_v1','SELECT') AND has_table_privilege(current_user,'usage_recovery_journal_v1','INSERT') AND has_table_privilege(current_user,'usage_recovery_journal_v1','UPDATE') AND has_table_privilege(current_user,'usage_logs','SELECT') AND has_table_privilege(current_user,'usage_logs','INSERT') AND has_table_privilege(current_user,'usage_logs','UPDATE') AND has_table_privilege(current_user,'usage_group_rollup_state','SELECT') AND has_table_privilege(current_user,'usage_group_rollup_state','UPDATE') AND has_table_privilege(current_user,'usage_billing_dedup','SELECT') AND has_table_privilege(current_user,'usage_billing_dedup_archive','SELECT') AND has_sequence_privilege(current_user,pg_get_serial_sequence('usage_logs','id'),'USAGE')`).Scan(&allowed)
	if e != nil {
		return e
	}
	if !allowed {
		return errors.New("usage journal existing role lacks required permissions; no permissions changed")
	}
	var watermark int
	if e = tx.QueryRowContext(ctx, `SELECT id FROM usage_group_rollup_state WHERE id=1`).Scan(&watermark); e != nil {
		return fmt.Errorf("usage journal watermark unavailable: %w", e)
	}
	return tx.Commit()
}
func (r *UsageJournalRepository) Observe(ctx context.Context) (service.UsageJournalObservation, error) {
	o := service.UsageJournalObservation{ErrorClasses: map[string]int64{}}
	e := r.db.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE state='pending'),count(*) FILTER(WHERE state='quarantined'),GREATEST(0,COALESCE(EXTRACT(EPOCH FROM clock_timestamp()-min(created_at) FILTER(WHERE state='pending')),0)),COALESCE(max(attempts),0),pg_total_relation_size('usage_recovery_journal_v1') FROM usage_recovery_journal_v1 WHERE state<>'done'`).Scan(&o.Pending, &o.Quarantined, &o.OldestPendingSeconds, &o.MaxAttempts, &o.RelationBytes)
	if e != nil {
		return o, e
	}
	rows, e := r.db.QueryContext(ctx, `SELECT COALESCE(last_error_class,'unclassified'),count(*) FROM usage_recovery_journal_v1 WHERE state='quarantined' GROUP BY last_error_class`)
	if e != nil {
		return o, e
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var class string
		var n int64
		if e = rows.Scan(&class, &n); e != nil {
			return o, e
		}
		switch class {
		case "invalid_snapshot", "usage_row_conflict", "missing_reference", "field_too_long", "numeric_range", "required_field", "constraint_conflict", "invalid_field":
		default:
			class = "unclassified"
		}
		o.ErrorClasses[class] += n
	}
	if e = rows.Err(); e != nil {
		return o, e
	}
	o.Known = true
	o.ObservedAt = time.Now().UTC()
	return o, nil
}

// Required with ingress/producer quiescence and marker cleanup frozen throughout
// a legacy rollback. This read-only gate is necessary, not a backup or repair.
func (r *UsageJournalRepository) RollbackReady(ctx context.Context) error {
	var missing, hot, archive int64
	e := r.db.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE NOT EXISTS(SELECT 1 FROM usage_billing_dedup d WHERE d.request_id=j.request_id AND d.api_key_id=j.api_key_id AND d.request_fingerprint=j.request_fingerprint) AND NOT EXISTS(SELECT 1 FROM usage_billing_dedup_archive a WHERE a.request_id=j.request_id AND a.api_key_id=j.api_key_id AND a.request_fingerprint=j.request_fingerprint)),count(*) FILTER(WHERE EXISTS(SELECT 1 FROM usage_billing_dedup d WHERE d.request_id=j.request_id AND d.api_key_id=j.api_key_id AND d.request_fingerprint IS DISTINCT FROM j.request_fingerprint)),count(*) FILTER(WHERE EXISTS(SELECT 1 FROM usage_billing_dedup_archive a WHERE a.request_id=j.request_id AND a.api_key_id=j.api_key_id AND a.request_fingerprint IS DISTINCT FROM j.request_fingerprint)) FROM usage_recovery_journal_v1 j`).Scan(&missing, &hot, &archive)
	if e != nil {
		return e
	}
	if missing != 0 || hot != 0 || archive != 0 {
		return fmt.Errorf("legacy rollback blocked: missing=%d hot_conflicts=%d archive_conflicts=%d", missing, hot, archive)
	}
	return nil
}
