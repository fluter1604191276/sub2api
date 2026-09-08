# Smart operations and model plaza release

Capability ID: channel-monitor-budget; smart-probe-modes; account-model-sync-preview; catalog-surfaces
Business purpose: Keep monitoring spend bounded, evaluate healthier accounts more quickly, make sticky escape policy explicit, safely synchronize upstream model support in bulk, and present available channels by protocol platform.
Backend/frontend files: `backend/internal/service/`, `backend/internal/handler/admin/`, `backend/ent/schema/channel_monitor_daily_rollup.go`, `backend/migrations/231_group_recovery_probe_high_frequency.sql`, `backend/migrations/232_channel_monitor_daily_budget.sql`, `frontend/src/components/admin/account/AccountModelSyncDialog.vue`, `frontend/src/views/user/ModelPlazaView.vue`, `frontend/src/components/channels/AvailableChannelsTable.vue`, `frontend/src/utils/{availableChannels,modelPlaza}.ts`.
Routes or jobs: Smart-sticky policy admin routes; channel-monitor budget/list/settings routes; recovery-probe scheduler; account model sync preview/apply routes; user available-channel and model-plaza routes.
Database migration/data dependency: Group recovery probe mode constraint and channel-monitor daily cost/budget fields; normal migration runner applies both migrations.
Billing impact: internal cost (probe estimation and budget guard); no user-charge formula change.
Scheduling impact: score-aware sticky escape and high-frequency probe candidate selection.
Client protocol impact: OpenAI/Anthropic/Gemini-compatible monitoring probes; model-plaza presentation only.
Tests/fixtures: Frontend typecheck, production build, model-plaza and available-channel component tests; backend compile-only package tests and targeted service/handler/routes tests.
First release manifest: `release/smart-ops-model-plaza-20260909`.
Rollback note: Restore the previous image digest and Compose/env snapshot; retain database migrations and disable new modes/budget before rollback if needed.
Owner/status: 2026-09-09 release candidate; pending remote image smoke and production switch.
