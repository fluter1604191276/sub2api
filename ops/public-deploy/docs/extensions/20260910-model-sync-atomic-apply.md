# Atomic model synchronization

Capability ID: account-model-sync-preview
Purpose: prevent partial mapping/snapshot writes and inconsistent batch modes.
Source: backend/internal/service/account_model_sync.go;
backend/internal/repository/account_repo.go;
frontend/src/components/admin/account/AccountModelSyncDialog.vue.
Routes: existing model-mapping preview/apply admin endpoints only.
Data: existing accounts credentials/extra; no schema migration.
Behavior: missing mode preserves full sync; explicit add preserves stale identity
entries. Aliases and wildcard mappings survive both modes. Discovery snapshots
describe upstream support, not the union of local aliases. Mapping, snapshot and
scheduler outbox commit or roll back together, guarded by preview updated_at.
UI: fixed mode for all batches, add-only preview omits removals.
Tests: account_model_sync_apply_test.go; account_repo_model_sync_cas_test.go;
AccountModelSyncDialog.spec.ts.
Billing: no billing changes; mapping changes affect future model eligibility.
Rollback: restore prior application image. Applied mappings are user-approved
data changes and require a pre-apply database backup to undo.
Status: candidate pending release verification; manifest outside Git worktree.
