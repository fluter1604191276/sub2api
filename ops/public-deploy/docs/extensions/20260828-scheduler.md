# 二开登记：智能调度

```text
Capability ID: scheduler
Business purpose: 根据账号质量、成本、可用性、探索流量和粘性策略选择上游账号，并支持弱粘性逃逸与更优账号切换。
Backend/frontend files: backend/internal/service/gateway_scheduling.go; backend/internal/service/openai_account_scheduler.go; scheduler-related admin handlers/views/tests
Routes or jobs: 生产网关账号选择；账号/分组调度配置与质量展示。
Database migration/data dependency: account/group scheduler settings and usage statistics。
Environment-variable/config dependency: scheduler feature settings stored in application configuration/data。
Billing impact: none directly; selected account affects internal cost.
Scheduling impact: account selection
Client protocol impact: none
First release upstream version/base commit: v0.1.171
First release official base commit: f0e7a9c7a23a7d02fb159b62fa809621eb0475a6
First release custom source branch: release/v0.1.171-fluter-full-custom-20260829
Current candidate upstream version/base commit: v0.1.183 / e8cb019fabf8b55199436229044cbf9aa7a82564
Current candidate custom source branch: release/v0.1.183-fluter-full-custom-20260830
Tests/fixtures: scheduler service and gateway routing tests；发布后需做账号选择、粘性逃逸和无数据账号探索 smoke。
Image smoke evidence: 候选镜像 smoke 证据记录在发布 manifest；覆盖账号选择、粘性逃逸和无数据账号探索。
First release manifest: sub2api-release-20260829-r5.json
Rollback note: 回退应用镜像；调度开关和原有账号配置保留，必要时关闭智能调度。
Owner/status: fluter / ready for candidate build
```
