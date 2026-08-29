# 二开登记：账号质量与缓存命中率

```text
Capability ID: quality-score; cache-hit-rate
Business purpose: 在账号管理中展示 1h/24h 质量、首字/总耗时、等级和 24h 平均缓存命中率，为调度与成本判断提供可见数据。
Backend/frontend files: backend/internal/pkg/usagestats/account_stats.go; backend/internal/service/account_usage_service.go; backend/internal/handler/admin/account_handler.go; backend/internal/repository/usage_log_repo_stats.go; backend/internal/server/routes/admin.go; frontend/src/api/admin/accounts.ts; frontend/src/views/admin/AccountsView.vue; frontend/src/types/index.ts
Routes or jobs: /admin/accounts/today-stats/batch；/admin/accounts/cache-hit-stats/batch。
Database migration/data dependency: usage_logs 聚合；无新增迁移。
Environment-variable/config dependency: none
Billing impact: internal cost
Scheduling impact: score
Client protocol impact: none
Upstream version/base commit: v0.1.171
Official base commit: f0e7a9c7a23a7d02fb159b62fa809621eb0475a6
Custom source branch: release/v0.1.171-fluter-full-custom-20260829
Tests/fixtures: account stats、usage/billing、cache-hit repository aggregation and AccountsView cache-hit rendering/table preference tests。
Image smoke evidence: 候选镜像 smoke 证据记录在发布 manifest 的 image_smoke 与 capability 字段；覆盖账号质量、缓存字段及分页/筛选窗口。
First valid release manifest: sub2api-release-20260829-r6.json (pending candidate verification)
Invalidated evidence: sub2api-release-20260829-r5.json omitted the cache-hit source chain and must not be used as cache-hit-rate release evidence.
Rollback note: 回退应用镜像即可；统计接口不写用户账单。
Owner/status: fluter / ready for candidate build
```
