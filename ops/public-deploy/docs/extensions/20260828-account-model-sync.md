# 二开登记：账号模型同步与筛选

```text
Capability ID: model-sync-filter
Business purpose: 同步账号上游支持模型，并允许按模型筛选账号，减少模型映射遗漏。
Backend/frontend files: backend/internal/service/account_model_sync.go; backend/internal/handler/admin/account_handler.go; backend/internal/repository/account_repo.go; backend/internal/server/routes/admin.go; frontend/src/api/admin/accounts.ts; frontend/src/components/admin/account/AccountTableFilters.vue; frontend/src/views/admin/AccountsView.vue
Routes or jobs: GET /admin/accounts/models；POST /admin/accounts/sync/models；GET /admin/accounts?model=...
Database migration/data dependency: account.extra.available_models JSON。
Environment-variable/config dependency: none
Billing impact: none
Scheduling impact: account selection
Client protocol impact: none
Upstream version/base commit: v0.1.149 lineage, current custom release branch
Tests/fixtures: account model sync service tests and admin handler/service tests。
Image smoke evidence: 认证检查同步入口、模型筛选和空模型回退行为。
First release manifest: pending candidate build
Rollback note: 回退应用镜像；已持久化的模型快照不影响请求计费。
Owner/status: fluter / ready for candidate build
```
