# Smart scheduler primary score threshold

Capability ID: smart-probe-modes
Business purpose: Demote primary accounts whose confidence-adjusted final score is below the configured threshold to warm, preserving existing exploration and fallback. This does not promote unmeasured accounts or bypass hard isolation.
Backend/frontend files: `backend/internal/service/smart_sticky_policy.go`, `backend/internal/service/smart_scheduler_preview.go`, `frontend/src/views/admin/GroupsView.vue`, `frontend/src/api/admin/groups.ts`, localized admin overview strings.
Routes or jobs: Existing group smart-scheduler policy GET/PUT routes; no new job.
Database migration/data dependency: Existing settings repository, per-group JSON policy; no migration.
Billing impact: none.
Scheduling impact: score; accounts below `primary_min_score` remain warm and remain eligible for exploration/fallback.
Client protocol impact: none.
Tests/fixtures: `go test ./internal/service -run 'TestPrimaryThreshold|TestSmartStickyPolicy|TestSmartScheduler' -count=1` passed, including preview/routing consistency, threshold cache invalidation, disabled/invalid values and sticky escape to a qualified primary. Frontend typecheck and Vite build passed; final image verification is separate.
First release manifest: pending.
Rollback note: Set the group threshold to 0 or restore the previous application image.
Owner/status: 2026-09-10 candidate; not deployed.

Compatibility: Default 0 preserves prior pools. Changing only this threshold retains
existing sticky review timing, cooldown and escape budgets. It does not guarantee
an immediate switch or replay a partially delivered stream.

The r1 candidate was blocked by model synchronization atomicity and mode
consistency defects. The follow-up uses a single conditional SQL update for
credentials plus snapshot, in the existing outbox transaction; restores omitted
mode to sync; freezes mode during a batch; and excludes removals in add previews.
SQL-mock tests cover successful commit, version conflict and outbox rollback.
Frontend tests cover add-only selection and locking. See the model-sync extension
record for details. Deployment still requires image-level release verification.
