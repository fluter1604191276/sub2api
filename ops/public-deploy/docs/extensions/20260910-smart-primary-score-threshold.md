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

Release blockers inherited from model synchronization commits: omitted apply mode
now defaults to add instead of the previous sync behavior; mapping and discovered
model snapshot writes are separate and can partially succeed; the batch UI mode
and removal preview need final consistency review. Candidate image construction
does not approve deployment while these issues remain unresolved.
