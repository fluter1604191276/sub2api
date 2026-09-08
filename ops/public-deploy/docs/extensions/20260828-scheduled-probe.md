# 二开登记：智能探针

```text
Capability ID: scheduled-probe
Business purpose: 对一段时间未使用的账号按分组计划做受控探针，记录归属、预算、结果并反馈调度可用性，避免无数据账号永久失去机会。
Backend/frontend files: backend/internal/service/scheduled_test_runner_service.go; backend/internal/service/scheduled_test_service.go; backend/internal/service/group_recovery_probe.go; backend/internal/service/group_recovery_probe_billing.go; backend/internal/handler/admin/scheduled_test_handler.go; backend/internal/handler/admin/account_upstream_billing_probe.go; backend/internal/repository/scheduled_test_repo.go; backend/internal/repository/group_recovery_probe_repo.go; backend/internal/repository/group_recovery_probe_settlement.go; backend/internal/service/group_recovery_probe_test.go; backend/internal/service/group_recovery_probe_billing_test.go; backend/internal/repository/group_recovery_probe_repo_test.go; frontend/src/api/admin/scheduledTests.ts; frontend/src/views/admin/GroupsView.vue; frontend/src/views/admin/__tests__/GroupsView.smartSchedulerPreview.spec.ts
Routes or jobs: scheduled test plan/result admin routes；scheduled test runner service。
Database migration/data dependency: scheduled test plans/results and usage attribution records。
Environment-variable/config dependency: per-group probe plan, budget and test model settings。
Billing impact: internal cost; probe usage must remain attributable and budgeted, not user traffic.
Scheduling impact: account availability
Client protocol impact: provider-specific probe request paths。
First release upstream version/base commit: v0.1.171
First release official base commit: f0e7a9c7a23a7d02fb159b62fa809621eb0475a6
First release custom source branch: release/v0.1.171-fluter-full-custom-20260829
Current candidate upstream version/base commit: v0.1.183 / e8cb019fabf8b55199436229044cbf9aa7a82564
Current candidate custom source branch: release/v0.1.183-fluter-full-custom-20260830
Tests/fixtures: backend/internal/service/group_recovery_probe_test.go; backend/internal/service/group_recovery_probe_billing_test.go; backend/internal/repository/group_recovery_probe_repo_test.go; frontend/src/api/admin/scheduledTests.ts; frontend/src/views/admin/__tests__/GroupsView.smartSchedulerPreview.spec.ts；发布后需验证关闭默认值、预算边界与成功/失败回写。
Image smoke evidence: 候选镜像 smoke 证据记录在发布 manifest；覆盖探针默认关闭、预算边界及成功/失败回写。
First release manifest: sub2api-release-20260829-r5.json
Rollback note: 关闭探针计划或回退应用镜像；保留探针结果和成本记录用于审计。
Owner/status: fluter / ready for candidate build
```
