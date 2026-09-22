# Passive account capability observation

```text
Capability ID: passive-capability-observation
Business purpose: 根据真实用户请求中的工具往返和明确的上游工具错误，统一汇总账号的工具、客户端工具和终端契约能力，帮助管理员发现工具不可用账号。
Backend/frontend files: backend/internal/service/account_capability.go; backend/internal/repository/usage_log_repo_capability.go; backend/internal/service/account_usage_service.go; backend/internal/service/gateway_forward.go; backend/internal/service/openai_gateway_forward.go; backend/internal/handler/admin/account_handler.go; backend/internal/server/routes/admin.go; frontend/src/views/admin/AccountsView.vue; frontend/src/api/admin/accounts.ts; frontend/src/types/index.ts; frontend/src/i18n/locales/zh/admin/accounts.ts; frontend/src/i18n/locales/en/admin/accounts.ts.
Routes or jobs: POST /api/v1/admin/accounts/capability-stats/batch; no scheduled job.
Database migration/data dependency: backend/migrations/239_account_capability_observations.sql; observations retain redacted metadata only, while account_capability_states stores account-level aggregates across groups, models, and protocols.
Billing impact: none
Scheduling impact: none
Client protocol impact: OpenAI Responses | Chat Completions | Anthropic; observation only.
Tests/fixtures: backend/internal/service/account_capability_test.go; service compilation; frontend typecheck/build pending dependency installation; no raw request body or credentials stored.
First release manifest: pending candidate image build.
Rollback note: restore the previous application image. If the migration has already run, leaving the additive capability tables is safe; removing them requires a separately backed-up database migration and is not part of application rollback.
Owner/status: fluter / candidate; production image not switched.
```

## Behavior boundary

Only real gateway traffic is observed. Probe traffic is not included. Capacity,
transport, client cancellation, and balance errors are excluded from capability
failure evidence. A failover request attributes each capability failure to the
account recorded in its upstream error event. A success is recorded only when
the request contains tool-roundtrip evidence, so a request that merely declares
tools and returns ordinary text does not falsely mark an account capable.

The account management column is opt-in and display-only. It shows tool and
terminal states with sample counts and hover details. Missing tables during a
rolling upgrade produce an unavailable/unknown result without breaking the
account page.

## State policy

`unknown` means no evidence. `degraded` covers sparse or mixed evidence.
`unsupported` requires at least two failures and no success. `capable` requires
at least three successes and no failure in the previous 30 minutes. Aggregation
is account-wide, so membership in multiple groups does not duplicate the
displayed state.
