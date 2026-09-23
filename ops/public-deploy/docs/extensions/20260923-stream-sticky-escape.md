# 二开登记：流式断流后的分组级粘性逃逸

```text
Capability ID: scheduler
Business purpose: 上游流异常中断或缺失终止事件后，避免下一次重试/“继续”仍命中同一故障账号；管理员可按分组关闭该行为。
Backend/frontend files: backend/ent/schema/group.go; backend/internal/service/openai_gateway_passthrough.go; backend/internal/service/openai_gateway_chat_completions_raw.go; backend/internal/service/openai_raw_stream_truncation.go; backend/internal/service/api_key_auth_cache*.go; backend/internal/service/admin_group.go; backend/internal/repository/group_repo.go; backend/internal/handler/admin/group_handler.go; frontend/src/views/admin/GroupsView.vue; frontend/src/types/index.ts; frontend/src/i18n/locales/{zh,en}/admin/overview.ts
Routes or jobs: 复用 PUT /api/v1/admin/groups/:id；OpenAI Responses 原生流、Responses -> Chat Completions、Responses -> Anthropic Messages、Responses 透传和原生 Chat Completions 流的异常收尾路径；无新增后台任务。
Database migration/data dependency: backend/migrations/240_group_stream_sticky_escape.sql；groups.stream_sticky_escape_enabled BOOLEAN NOT NULL DEFAULT TRUE。
Environment-variable/config dependency: gateway.openai_scheduler.sticky_escape_enabled 是部署级总闸；只有总闸和分组 stream_sticky_escape_enabled 同时开启才执行断流逃逸。
Billing impact: none
Scheduling impact: account+model temporary cooldown, current group/session sticky deletion, and same-account retry avoidance for qualifying stream failures.
Client protocol impact: OpenAI Responses | Chat Completions | Anthropic bridge
Tests/fixtures: backend/internal/service/openai_stream_sticky_escape_test.go; backend/internal/service/api_key_auth_cache_smart_scheduler_test.go; backend/internal/service/admin_service_group_test.go; backend/internal/handler/admin/group_handler_smart_scheduler_test.go; frontend/src/views/admin/__tests__/GroupsView.smartSchedulerPreview.spec.ts
Image smoke evidence: pending candidate image build; production has not been switched for this change.
Rollback note: switch the group setting off to restore legacy sticky/same-account retry behavior. Full code rollback may restore the previous application image; migration 240 may remain because its defaulted column is backward compatible.
Owner/status: fluter / candidate implementation, verification pending.
```

## Behavior

The switch is exposed under **Admin -> Groups -> Smart Scheduler**. New groups
default to enabled. Frontends talking to an older API response also treat a
missing field as enabled, while the runtime intentionally fails closed if the
authenticated request has no materialized group snapshot.

When both gates are enabled, an abnormal upstream disconnect or missing terminal
event cools only the current account+canonical-model pair, deletes the current
and legacy sticky keys for the current group/session, and marks pre-commit
failover errors to avoid retrying the same account. The current partially emitted
response is never spliced with another account; recovery applies to a transparent
pre-commit retry or the next client request.

When the group switch is disabled, the gateway preserves the previous sticky and
same-account retry behavior. Client cancellation, request-context cancellation,
normal terminal completion, and local scanner size limits never trigger escape.
