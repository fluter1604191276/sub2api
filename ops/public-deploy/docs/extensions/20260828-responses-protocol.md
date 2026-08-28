# 二开登记：Responses 工具协议边界

```text
Capability ID: responses-tools
Business purpose: 固定 Responses 原生工具、流式事件与兼容桥的能力边界，避免把桥接成功误报为终端能力完整。
Backend/frontend files: backend/internal/pkg/apicompat/responses_to_anthropic.go; backend/internal/pkg/apicompat/responses_to_chatcompletions.go; backend/internal/pkg/apicompat/chatcompletions_responses_bridge.go; protocol fixtures
Routes or jobs: OpenAI Responses gateway paths and provider compatibility bridges。
Database migration/data dependency: none
Environment-variable/config dependency: upstream protocol capability。
Billing impact: none
Scheduling impact: none
Client protocol impact: OpenAI Responses; Chat Completions; Anthropic。
Upstream version/base commit: v0.1.149 lineage, current custom release branch
Tests/fixtures: tool definition、tool call output、streaming event fixtures；apicompat tests。
Image smoke evidence: 必须分别验证原生 Responses、Chat Completions 桥和 Anthropic 桥；桥接不支持的工具必须显式标记。
First release manifest: pending candidate build
Rollback note: 协议 smoke 失败立即回退旧镜像，不以 HTTP 200 或 healthy 作为替代证据。
Owner/status: fluter / partial by design; explicit compatibility decision required
```
