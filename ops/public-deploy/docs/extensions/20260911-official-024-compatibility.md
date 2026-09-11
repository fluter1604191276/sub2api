# Official Sub2API v0.2.4 compatibility

Capability ID: official-024-compatibility
Business purpose: 将官方 Sub2API v0.2.4 的功能与修复合并到当前生产基线，同时保持 Fluter 的调度、探针、质量评分、缓存命中率、计费、模型同步、可用渠道和模型广场扩展。
Backend/frontend files: official v0.2.4 changes across backend and frontend; compatibility adjustments in backend/internal/domain/model_allowlist.go, backend/internal/server/routes/admin.go, frontend/src/api/admin/groups.ts, frontend/src/views/admin/GroupsView.vue, frontend/src/composables/useTableLoader.ts, frontend/src/views/admin/AccountsView.vue.
Routes or jobs: 保留旧的 `/:id/models-list-candidates` 管理路由，并兼容官方 `/:id/model-allowlist-candidates` 路由；无新增定时任务。
Database migration/data dependency: 官方 v0.2.4 migrations plus existing production migrations; no destructive data reset. Model allowlist fields accept both legacy and official representations.
Billing impact: none by the compatibility layer; official pricing remains the billing source, while existing image/search/tool/internal-cost boundaries remain site-specific.
Scheduling impact: none to the scheduler policy; account model allowlist and model synchronization remain available to candidate selection.
Client protocol impact: OpenAI Responses, Chat Completions, Anthropic, Gemini, and provider-specific official v0.2.4 paths; Responses-native tool limitations remain explicitly partial on compatibility bridges.
Tests/fixtures: `go test ./...`; `pnpm run typecheck`; `pnpm test:run`; targeted model allowlist, account model sync, smart scheduler, probe, billing, cache-hit, catalog, and protocol tests; `git diff --check`.
First release manifest: pending candidate manifest.
Rollback note: keep the current production image, Compose, environment backup, and database backup; rollback the application image only unless a tested migration rollback is explicitly required.
Owner/status: fluter / candidate, production not switched.

Compatibility decisions:

- Official model allowlist naming is canonical; the legacy candidates endpoint remains as a compatibility alias.
- Account list and ETag refreshes retain compact `lite=1` requests; full account details are fetched separately for edit/test/stat actions.
- Reactive table filters are snapshotted before async requests so post-request UI cleanup cannot mutate request parameters.
- Simple mode continues to reject advanced administrative operations and avoids advanced capacity requests.
- No official model-plaza or catalog presentation data writes channel pricing, user billing, routing, probes, or smart scheduling.
