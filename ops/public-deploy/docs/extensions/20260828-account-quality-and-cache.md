# 二开登记：账号质量与缓存命中率

```text
Capability ID: quality-score; cache-hit-rate
Business purpose: 在账号管理中展示 1h/24h 质量、首字/总耗时、等级和 24h 平均缓存命中率，为调度与成本判断提供可见数据。
Backend/frontend files: backend/internal/pkg/usagestats/account_stats.go; backend/internal/service/account_usage_service.go; backend/internal/handler/admin/account_handler.go; backend/internal/repository/usage_log_repo_stats.go; frontend/src/views/admin/AccountsView.vue; frontend/src/types/index.ts
Routes or jobs: /admin/accounts/today-stats/batch；/admin/accounts/cache-hit-stats/batch。
Database migration/data dependency: usage_logs 聚合；无新增迁移。
Environment-variable/config dependency: none
Billing impact: internal cost
Scheduling impact: score
Client protocol impact: none
Upstream version/base commit: v0.1.149 lineage, current custom release branch
Tests/fixtures: account stats、usage/billing and frontend table preference tests。
Image smoke evidence: 发布后需认证检查账号质量与缓存字段存在，并确认分页/筛选不改变统计窗口。
First release manifest: pending candidate build
Rollback note: 回退应用镜像即可；统计接口不写用户账单。
Owner/status: fluter / ready for candidate build
```
