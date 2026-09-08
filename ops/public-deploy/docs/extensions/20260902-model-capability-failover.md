# 二开登记：确定性模型能力错误的账号级切换

```text
Capability ID: model-capability-failover
Business purpose: 将上游返回的“unknown provider for model”等确定性模型能力错误识别为账号-模型不可用，立即切换到其他候选账号，避免流式请求断流后因会话粘性反复命中同一错误账号。
Backend/frontend files: backend/internal/service/model_not_found_error.go; backend/internal/service/ratelimit_service.go; backend/internal/service/openai_account_runtime_block_fastpath.go; backend/internal/service/openai_gateway_upstream_errors.go; backend/internal/service/openai_gateway_passthrough.go; backend/internal/service/model_not_found_error_test.go; backend/internal/service/ratelimit_service_model_not_found_test.go; backend/internal/service/openai_access_state_failover_test.go
Routes or jobs: OpenAI Responses、Chat Completions、兼容转发及相关 failover 路径；复用现有模型能力冷却键和调度排除逻辑。
Database migration/data dependency: no migration; writes the existing account model-rate-limit state with endpoint-scoped capability keys。
Billing impact: none
Scheduling impact: account selection; only the affected account-model-endpoint pair is temporarily excluded, the whole account remains available for other models。
Client protocol impact: OpenAI Responses; Chat Completions; compatible OpenAI routes。流式响应尚未写出数据时允许切换，已写出数据后仍禁止拼接式切换。
Tests/fixtures: model_not_found_error_test.go; ratelimit_service_model_not_found_test.go; openai_access_state_failover_test.go; release smoke must verify unknown-provider classification, model-scoped exclusion, context-model fallback, failover, and sticky-session escape。
First release manifest: fluter-0.1.183-full-custom-20260902-r4-release-manifest.json; developed from the live production-derived 0.1.183 line。
Rollback note: restore the prior production image; the existing model-rate-limit rows may expire naturally, and no schema rollback is required。
Owner/status: fluter / candidate implementation, not released to production
```

Incident note: `unknown provider for model gpt-5.6-sol` is an upstream capability
rejection, not a client network disconnect. Before this change it remained a
normal active/schedulable account state, so a sticky session could retry the same
account indefinitely. The classifier deliberately requires the specific phrase
`unknown provider for model`; a generic provider validation error is not enough
to quarantine an account-model pair.

This record contains no keys, cookies, bearer tokens, passwords, or raw request
bodies.
