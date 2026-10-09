// Journal repository hooks are an isolated, disabled draft; not a release migration.
package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func usageJournalReceiptHash(snapshotSHA, resultJSON string, tokens ...string) string {
	token := ""
	if len(tokens) > 0 {
		token = tokens[0]
	}
	h := sha256.Sum256([]byte(snapshotSHA + "|" + token + "|" + resultJSON))
	return hex.EncodeToString(h[:])
}

func insertUsageJournalSnapshot(ctx context.Context, tx *sql.Tx, cmd *service.UsageBillingCommand, result *service.UsageBillingApplyResult, attemptToken string) error {
	b, e := json.Marshal(cmd.UsageJournalSnapshot)
	if e != nil {
		return e
	}
	if len(b) > 1024*1024 {
		return errors.New("usage snapshot exceeds recovery limit")
	}
	receipt, e := json.Marshal(result)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO usage_recovery_journal_v1 (request_id,api_key_id,request_fingerprint,payload_json,snapshot_sha256,billing_result_json,billing_result_sha256,commit_token) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, cmd.RequestID, cmd.APIKeyID, cmd.RequestFingerprint, string(b), cmd.UsageJournalSnapshot.SHA256, string(receipt), usageJournalReceiptHash(cmd.UsageJournalSnapshot.SHA256, string(receipt), attemptToken), attemptToken)
	return e
}

// Existing markers are never replayed to manufacture a journal. A duplicate must
// reference its original immutable snapshot, not a newly timestamped log.
type usageJournalQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func lookupUsageJournal(ctx context.Context, q usageJournalQuerier, cmd *service.UsageBillingCommand, expectedAttempt ...string) (bool, *service.UsageBillingApplyResult, error) {
	var raw, sha, receipt, receiptSHA, token string
	e := q.QueryRowContext(ctx, `SELECT payload_json,snapshot_sha256,billing_result_json,billing_result_sha256,commit_token FROM usage_recovery_journal_v1 WHERE request_id=$1 AND api_key_id=$2`, cmd.RequestID, cmd.APIKeyID).Scan(&raw, &sha, &receipt, &receiptSHA, &token)
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil, nil
	}
	if e != nil {
		return false, nil, e
	}
	var saved service.UsageJournalSnapshot
	if e = json.Unmarshal([]byte(raw), &saved); e != nil {
		return false, nil, e
	}
	if sha != saved.SHA256 {
		return false, nil, errors.New("usage journal hash column mismatch")
	}
	if e = saved.Validate(cmd); e != nil {
		return false, nil, e
	}
	if len(token) != 32 || receiptSHA != usageJournalReceiptHash(sha, receipt, token) {
		return false, nil, errors.New("usage journal receipt hash mismatch")
	}
	var result service.UsageBillingApplyResult
	if e = json.Unmarshal([]byte(receipt), &result); e != nil {
		return false, nil, e
	}
	if !result.Applied {
		return false, nil, errors.New("usage journal receipt must describe original applied transaction")
	}
	if len(expectedAttempt) > 0 && expectedAttempt[0] != token {
		result.Applied = false
	}
	return true, &result, nil
}

func verifyExistingUsageJournal(ctx context.Context, tx *sql.Tx, cmd *service.UsageBillingCommand) error {
	found, _, e := lookupUsageJournal(ctx, tx, cmd)
	if e != nil {
		return e
	}
	if !found {
		return service.ErrUsageJournalLegacyMarker
	}
	return nil
}
