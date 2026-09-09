# Probe budget and settlement repair

Capability ID: channel-monitor-budget; smart-probe-modes
Business purpose: Restore scheduled channel monitoring after PostgreSQL rejected the budget reservation query, and make successful recovery probes settle exactly the same amount in the audit row, user balance, account quota, and usage log.
Backend/frontend files: `backend/internal/repository/channel_monitor_repo.go`, `backend/internal/service/group_recovery_probe_billing.go`; regression tests in `backend/internal/repository/channel_monitor_budget_test.go`, `backend/internal/repository/channel_monitor_budget_integration_test.go`, and `backend/internal/service/group_recovery_probe_billing_test.go`.
Routes or jobs: Existing channel-monitor runner and group recovery-probe scheduler; no route or schedule change.
Database migration/data dependency: Existing `channel_monitor_daily_budget_ledger` and `group_recovery_probe_audits` schema; no migration.
Billing impact: internal cost; no user charge formula change. Probe settlement now uses the existing eight-decimal `QuantizeUsageBillingAmount` contract before atomic billing.
Scheduling impact: Restores V1 monitor budget reservations and allows recovery-probe settlements to complete without falsely marking them failed.
Client protocol impact: none.
Tests/fixtures: Real PostgreSQL integration tests for finite/unlimited budgets, unknown-price fail-closed behavior, missing settlement row, and 32 concurrent reservations; service tests for production cost values, rounding boundaries, idempotent retry, and non-finite/zero guards; existing release bundle tests.
First release manifest: pending candidate build from live production revision `7a5709b8fdb7ec240b4acd500fdbe7c1bfd873aa`.
Rollback note: Keep the previous production image and existing migrations; rollback only the application image if needed. No destructive schema rollback is required.
Owner/status: 2026-09-09 repair candidate; not deployed.
