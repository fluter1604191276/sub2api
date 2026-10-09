//go:build unit

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"testing"
	"time"
)

func draftJournalCommand(t *testing.T) *service.UsageBillingCommand {
	t.Helper()
	cmd := &service.UsageBillingCommand{RequestID: "synthetic-journal", APIKeyID: 2, UserID: 1, AccountID: 3, Model: "synthetic-model", BalanceCost: 2.5}
	cmd.Normalize()
	log := &service.UsageLog{RequestID: cmd.RequestID, APIKeyID: 2, UserID: 1, AccountID: 3, Model: cmd.Model, ActualCost: 2.5, CreatedAt: time.Date(2026, 10, 10, 1, 0, 0, 0, time.UTC)}
	s, e := service.NewUsageJournalSnapshot(cmd, log)
	if e != nil {
		t.Fatal(e)
	}
	cmd.UsageJournalSnapshot = s
	return cmd
}
func expectDraftNewBilling(mock sqlmock.Sqlmock, cmd *service.UsageBillingCommand) {
	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO usage_billing_dedup").WithArgs(cmd.RequestID, cmd.APIKeyID, cmd.RequestFingerprint).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
	mock.ExpectQuery("SELECT request_fingerprint.*FROM usage_billing_dedup_archive").WillReturnRows(sqlmock.NewRows([]string{"request_fingerprint"}))
	if cmd.UsageJournalSnapshot != nil {
		mock.ExpectQuery("SELECT payload_json,snapshot_sha256").WillReturnRows(sqlmock.NewRows([]string{"payload_json", "snapshot_sha256", "billing_result_json", "billing_result_sha256", "commit_token"}))
	}
	mock.ExpectQuery(conditionalBalanceDeductSQL).WithArgs(cmd.BalanceCost, cmd.UserID).WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(97.5))
}
func TestUsageJournalDraftApplyCommitsSnapshotWithBilling(t *testing.T) {
	db, m, e := sqlmock.New()
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	cmd := draftJournalCommand(t)
	expectDraftNewBilling(m, cmd)
	raw, _ := json.Marshal(cmd.UsageJournalSnapshot)
	m.ExpectExec("INSERT INTO usage_recovery_journal_v1").WithArgs(cmd.RequestID, cmd.APIKeyID, cmd.RequestFingerprint, string(raw), cmd.UsageJournalSnapshot.SHA256, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectCommit()
	res, e := (&usageBillingRepository{db: db}).Apply(context.Background(), cmd)
	if e != nil || res == nil || !res.Applied {
		t.Fatalf("Apply: %v", e)
	}
	if e = m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestUsageJournalDraftInsertFailureRollsBackBilling(t *testing.T) {
	db, m, e := sqlmock.New()
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	cmd := draftJournalCommand(t)
	expectDraftNewBilling(m, cmd)
	m.ExpectExec("INSERT INTO usage_recovery_journal_v1").WillReturnError(errors.New("synthetic journal write failure"))
	m.ExpectRollback()
	if _, e = (&usageBillingRepository{db: db}).Apply(context.Background(), cmd); e == nil {
		t.Fatal("journal failure ignored")
	}
	if e = m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestUsageJournalDraftLegacyMarkerNeverReplaysEffects(t *testing.T) {
	db, m, e := sqlmock.New()
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	cmd := draftJournalCommand(t)
	m.ExpectBegin()
	m.ExpectQuery("INSERT INTO usage_billing_dedup").WillReturnError(sql.ErrNoRows)
	m.ExpectQuery("SELECT request_fingerprint").WillReturnRows(sqlmock.NewRows([]string{"request_fingerprint"}).AddRow(cmd.RequestFingerprint))
	m.ExpectQuery("SELECT payload_json,snapshot_sha256").WillReturnError(sql.ErrNoRows)
	m.ExpectRollback()
	_, e = (&usageBillingRepository{db: db}).Apply(context.Background(), cmd)
	if !errors.Is(e, service.ErrUsageJournalLegacyMarker) {
		t.Fatal(e)
	}
	if e = m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}

func TestUsageJournalDraftDuplicateUsesOriginalSnapshot(t *testing.T) {
	db, m, e := sqlmock.New()
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	cmd := draftJournalCommand(t)
	original, _ := json.Marshal(cmd.UsageJournalSnapshot)
	originalSHA := cmd.UsageJournalSnapshot.SHA256
	log, _ := cmd.UsageJournalSnapshot.DecodeLog()
	log.CreatedAt = log.CreatedAt.Add(time.Minute)
	fresh, e := service.NewUsageJournalSnapshot(cmd, log)
	if e != nil {
		t.Fatal(e)
	}
	cmd.UsageJournalSnapshot = fresh
	m.ExpectBegin()
	m.ExpectQuery("INSERT INTO usage_billing_dedup").WillReturnError(sql.ErrNoRows)
	m.ExpectQuery("SELECT request_fingerprint").WillReturnRows(sqlmock.NewRows([]string{"request_fingerprint"}).AddRow(cmd.RequestFingerprint))
	receipt := `{"Applied":true}`
	m.ExpectQuery("SELECT payload_json,snapshot_sha256").WillReturnRows(sqlmock.NewRows([]string{"payload_json", "snapshot_sha256", "billing_result_json", "billing_result_sha256", "commit_token"}).AddRow(string(original), originalSHA, receipt, usageJournalReceiptHash(originalSHA, receipt, "0123456789abcdef0123456789abcdef"), "0123456789abcdef0123456789abcdef"))
	m.ExpectRollback()
	res, e := (&usageBillingRepository{db: db}).Apply(context.Background(), cmd)
	if e != nil || res == nil || res.Applied {
		t.Fatalf("duplicate: %v", e)
	}
	if e = m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestUsageJournalDraftNilSnapshotPreservesR4Transaction(t *testing.T) {
	db, m, e := sqlmock.New()
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	cmd := draftJournalCommand(t)
	cmd.UsageJournalSnapshot = nil
	expectDraftNewBilling(m, cmd)
	m.ExpectCommit()
	res, e := (&usageBillingRepository{db: db}).Apply(context.Background(), cmd)
	if e != nil || res == nil || !res.Applied {
		t.Fatalf("default path: %v", e)
	}
	if e = m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
