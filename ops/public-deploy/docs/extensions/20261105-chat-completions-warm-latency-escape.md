# 二开登记：慢温备 Chat Completions 粘性逃逸

```text
Capability ID: scheduler
Business purpose: 当 chat_completions 会话粘住温备池唯一最低分且首字 p90 超过阈值的账号时，重新选择其他可用账号，避免普通调度在清除粘性后立即选回该慢号。
Backend/frontend files: backend/internal/service/smart_scheduler_routing.go; backend/internal/service/openai_account_scheduler.go; backend/internal/service/chat_completions_sticky_escape_test.go
Routes or jobs: OpenAI Chat Completions 的既有账号选择路径，无新增路由或后台任务。
Database migration/data dependency: 无迁移。读取既有精确 model+endpoint 质量窗口与会话粘性缓存。
Environment-variable/config dependency: 复用每组 Smart Sticky Policy 的 chat_completions_warm_latency_escape_ms，默认 30000ms；高级调度总开关关闭时仍执行同一粘性复核。
Billing impact: none
Scheduling impact: account selection。任何已确认的 Smart Sticky 切换都会解除本次会话粘性；仅 chat_completions 慢温备 latency 逃逸会在本次选择中排除原账号，其他切换保留既有健康账号 fallback 语义。实际选择的非预期挑战者才会结算切换 cooldown 与 escape budget，未绑定选择的 trace 不会被后续请求消费。
Client protocol impact: Chat Completions
Upstream version/base commit: production a5382a523b77ce5a8fd565e0c6cf176bb39e537c
Tests/fixtures: backend/internal/service/chat_completions_sticky_escape_test.go 覆盖延迟逃逸、better_quality、primary_over_warm、cheaper_within_tolerance，以及高级调度挑战者满并发 fallback；smart_scheduler_routing_integration_test.go 覆盖 trace 绑定与 fallback cooldown。
Image smoke evidence: pending candidate image build; production has not been switched for this change.
First release manifest: pending.
Rollback note: 将每组阈值设为高于观测延迟或回滚候选镜像，可恢复此前粘性行为。
Owner/status: bot-codex / candidate implementation, local targeted verification complete; PR, image smoke, deployment, and 24-hour production observation pending.
```
