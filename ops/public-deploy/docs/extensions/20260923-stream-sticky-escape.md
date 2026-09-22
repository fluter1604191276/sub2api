# 二开登记：已提交流断流后的粘性逃逸

```text
Capability ID: scheduler
Business purpose: 用户已经看到部分有效输出后，上游流在终止事件前断开时，避免下一次“继续”请求再次粘性命中同一故障账号。
Backend/frontend files: backend/internal/service/openai_gateway_passthrough.go; backend/internal/service/openai_gateway_response_handling.go; backend/internal/service/openai_gateway_chat_completions.go; backend/internal/service/openai_gateway_chat_completions_raw.go; backend/internal/service/openai_gateway_messages.go; backend/internal/service/openai_sticky_compat.go; backend/internal/service/openai_account_runtime_block_fastpath.go; backend/internal/service/openai_stream_sticky_escape_test.go
Routes or jobs: OpenAI Responses 原生流、Responses -> Chat Completions、Responses -> Anthropic Messages、Responses 透传、原生 Chat Completions 流的异常收尾路径；无新增后台任务或管理路由。
Database migration/data dependency: 无 schema 变更；使用现有账号+模型临时冷却状态、当前分组/会话 sticky 缓存和现有请求上下文。
Environment-variable/config dependency: 使用现有智能调度/粘性配置和 `openaiCommittedStreamFailureCooldown`；无新增环境变量。
Billing impact: none
Scheduling impact: account selection
Client protocol impact: OpenAI Responses | Chat Completions | Anthropic
Upstream version/base commit: current production-derived candidate base `3b43110cedabce5c6bcb0966f5f082dae624e054`; candidate branch `feature/stream-sticky-escape-20260922`。
Tests/fixtures: `go test ./internal/service -count=1` passed; `go test ./...` passed; targeted semantic-output, idempotency, cancellation exemption, Responses bridge, Anthropic bridge, and raw Chat truncation fixtures passed; release verifier tests 29/29 passed; `git diff --check` passed。
Image smoke evidence: candidate `fluter/sub2api:fluter-0.2.4-stream-sticky-escape-20260923-r2` was inspected as linux/amd64; container marker smoke passed for scheduler, including `openai_stream_sticky_escape`; container `--version` reported the candidate commit. Full authenticated production smoke and live traffic validation remain pending because production was not switched。
First release manifest: `/Users/fluter_claw/Documents/study_project/sub2api/.release-evidence/20260923-stream-sticky-escape/release-manifest.json` (secret-free; allow_release=true)。
Rollback note: restore the previous application image only; no database rollback is required. Keep the previous image and Compose/configuration backup until post-switch protocol and routing smoke passes。
Owner/status: fluter / candidate verified, production not switched。
```

## Behavior boundary

The helper acts only after semantic text, reasoning, refusal, or tool-call data
has been observed and the stream then fails without a terminal event. It cools
the account for the existing committed-stream failure interval, scoped by
canonical upstream model. It deletes both the current xxhash sticky key and the
legacy SHA-256 key so a compatibility fallback cannot resurrect the account.
The operation is idempotent within a request.

It deliberately does not fail over after semantic output by splicing another
account into the same response. The current request returns the existing typed
stream error; the next request is allowed to select another candidate. Client
cancellation, context cancellation, normal terminal completion, and scanner
size-limit errors are excluded from account health changes.
