# 二开登记：错误重写与上游地址隔离

```text
Capability ID: error-passthrough
Business purpose: 对可重试过载等错误做受控重写，并阻止上游 URL、密钥和内部连接细节透传给用户。
Backend/frontend files: backend/internal/service/error_passthrough_service.go; backend/internal/service/error_passthrough_runtime.go; backend/internal/handler/admin/error_passthrough_handler.go; error passthrough routes/tests
Routes or jobs: error passthrough admin configuration routes；gateway error handling runtime。
Database migration/data dependency: error passthrough rules and account/group scope settings。
Environment-variable/config dependency: none
Billing impact: none
Scheduling impact: retry behavior can affect account availability and request recycling。
Client protocol impact: OpenAI; Anthropic; compatible error envelopes。
First release upstream version/base commit: v0.1.171
First release official base commit: f0e7a9c7a23a7d02fb159b62fa809621eb0475a6
First release custom source branch: release/v0.1.171-fluter-full-custom-20260829
Current candidate upstream version/base commit: v0.1.183 / e8cb019fabf8b55199436229044cbf9aa7a82564
Current candidate custom source branch: release/v0.1.183-fluter-full-custom-20260830
Tests/fixtures: sanitization, rewrite precedence and upstream URL redaction tests；发布后需验证用户响应不含 upstream_url。
Image smoke evidence: 候选镜像 smoke 证据记录在发布 manifest；覆盖错误规则路由和上游 URL 脱敏边界。
First release manifest: sub2api-release-20260829-r5.json
Rollback note: 删除/停用规则或回退应用镜像；保留原始内部日志仅在受控服务端审计范围内。
Owner/status: fluter / ready for candidate build
```
