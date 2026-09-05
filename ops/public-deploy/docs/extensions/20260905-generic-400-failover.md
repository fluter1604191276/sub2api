# 二开登记：账号级通用上游 400 切换

```text
Capability ID: generic-400-failover
Business purpose: 某些 OpenAI-compatible 上游在选中的账号无法完成请求时，返回 HTTP 400 且消息为“Upstream request failed”。该响应不代表用户请求参数错误；需要脱离当前账号/会话粘性，尝试一个备用账号，避免用户反复点击继续仍命中同一故障账号。
Backend/frontend files: backend/internal/service/openai_gateway_upstream_errors.go; backend/internal/service/openai_account_runtime_block_fastpath.go; backend/internal/service/openai_gateway_passthrough.go; backend/internal/service/openai_gateway_cc_pipeline.go; backend/internal/service/openai_gateway_forward.go; backend/internal/handler/openai_gateway_handler.go; related tests
Routes or jobs: OpenAI Responses、Messages/兼容转发、Chat Completions/forward failover paths；Responses 和 Messages 每次请求最多为该特定错误切换一个备用账号。
Database migration/data dependency: no migration; reuse existing account-model transient runtime state and sticky-session cache。
Environment-variable/config dependency: none; cooldown is currently 90 seconds and is intentionally scoped to account+model。
Billing impact: none
Scheduling impact: account selection; the failed account-model pair is temporarily cooled, the whole account remains available for other models, and the current group/session sticky binding is cleared in both current and legacy key formats。
Client protocol impact: OpenAI Responses; Chat Completions; compatible OpenAI routes。流式响应已经输出语义内容后仍禁止拼接式切换。
Upstream version/base commit: production-derived r4 line, base commit 30d34f051848c72e8c49392d2406c1e20f311d9b
Tests/fixtures: openai_generic_upstream_failure_test.go; openai_account_runtime_transient_test.go; openai_gateway_first_output_timeout_test.go; openai_sticky_compat_test.go; `go test ./internal/service -run 'Test(ClearStickyAfterOpenAIFailover|OpenAIGenericUpstreamFailure|HandleOpenAIGenericUpstreamFailure)' -count=1`
Image smoke evidence: pending candidate image build
First release manifest: pending candidate release manifest
Rollback note: restore the previous production image and Compose; no schema rollback is required. Existing 90-second runtime cooldowns expire naturally。
Owner/status: fluter / candidate implementation, not released to production
```

Implementation boundary:

- Only an HTTP 400 whose known message field or plain-text body contains
  `Upstream request failed` is classified. Ordinary invalid-request 400s,
  including a user body that merely echoes that phrase, do not fail over.
- The account-model cooldown is 90 seconds. It does not disable the whole
  account and does not affect unrelated models.
- After classification, the current group/session sticky binding is removed in
  both the current and legacy hash formats. The group ID is taken from the
  authenticated API key, so the same account can remain bound elsewhere.
- One alternate-account attempt is allowed per request for this reason. This
  prevents a group-wide outage from multiplying upstream cost and latency.

This record contains no keys, cookies, bearer tokens, passwords, or raw request
bodies.
