# 二开登记：智能探针

```text
Capability ID: scheduled-probe
Business purpose: 对一段时间未使用的账号按分组计划做受控探针，记录归属、预算、结果并反馈调度可用性，避免无数据账号永久失去机会。
Backend/frontend files: backend/internal/service/scheduled_test_runner_service.go; backend/internal/service/scheduled_test_service.go; backend/internal/handler/admin/scheduled_test_handler.go; scheduled probe frontend/API/tests
Routes or jobs: scheduled test plan/result admin routes；scheduled test runner service。
Database migration/data dependency: scheduled test plans/results and usage attribution records。
Environment-variable/config dependency: per-group probe plan, budget and test model settings。
Billing impact: internal cost; probe usage must remain attributable and budgeted, not user traffic.
Scheduling impact: account availability
Client protocol impact: provider-specific probe request paths。
Upstream version/base commit: v0.1.171
Official base commit: f0e7a9c7a23a7d02fb159b62fa809621eb0475a6
Custom source branch: release/v0.1.171-fluter-full-custom-20260829
Tests/fixtures: scheduled test service/runner and billing attribution tests；发布后需验证关闭默认值、预算边界与成功/失败回写。
Image smoke evidence: pending candidate build
First release manifest: pending candidate build
Rollback note: 关闭探针计划或回退应用镜像；保留探针结果和成本记录用于审计。
Owner/status: fluter / ready for candidate build
```
