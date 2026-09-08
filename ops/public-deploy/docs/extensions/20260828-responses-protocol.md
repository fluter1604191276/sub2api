# 二开登记：Responses 工具协议边界

```text
Capability ID: responses-tools
Business purpose: 固定 Responses 原生工具、流式事件与兼容桥的能力边界，避免把桥接成功误报为终端能力完整。
Backend/frontend files: backend/internal/pkg/apicompat/responses_client_tools.go; backend/internal/pkg/apicompat/responses_client_tools_item_id_helper_test.go; backend/internal/pkg/apicompat/responses_client_tools_item_id_test.go; backend/internal/pkg/apicompat/responses_client_tools_test.go; backend/internal/pkg/apicompat/responses_stream_event_wire.go; backend/internal/pkg/apicompat/responses_stream_event_wire_test.go; backend/internal/pkg/apicompat/responses_to_anthropic_request.go; backend/internal/pkg/apicompat/responses_to_anthropic.go; backend/internal/pkg/apicompat/responses_to_anthropic_tools_test.go; backend/internal/pkg/apicompat/responses_to_chatcompletions.go; backend/internal/pkg/apicompat/responses_to_chatcompletions_codex_events_test.go; backend/internal/pkg/apicompat/chatcompletions_responses_bridge.go; backend/internal/pkg/apicompat/chatcompletions_responses_bridge_custom_tools_test.go; backend/internal/service/openai_gateway_responses_client_tools_test.go; backend/internal/service/openai_responses_input_compat_test.go; backend/internal/service/openai_responses_item_id_test.go
Routes or jobs: OpenAI Responses gateway paths and provider compatibility bridges。
Database migration/data dependency: none
Environment-variable/config dependency: upstream protocol capability。
Billing impact: none
Scheduling impact: none
Client protocol impact: OpenAI Responses; Chat Completions; Anthropic。
First release upstream version/base commit: v0.1.171
First release official base commit: f0e7a9c7a23a7d02fb159b62fa809621eb0475a6
First release custom source branch: release/v0.1.171-fluter-full-custom-20260829
Current candidate upstream version/base commit: v0.1.183 / e8cb019fabf8b55199436229044cbf9aa7a82564
Current candidate custom source branch: release/v0.1.183-fluter-full-custom-20260830
Tests/fixtures: backend/internal/pkg/apicompat/responses_client_tools_test.go; backend/internal/pkg/apicompat/responses_stream_event_wire_test.go; backend/internal/pkg/apicompat/responses_to_anthropic_tools_test.go; backend/internal/pkg/apicompat/responses_to_chatcompletions_codex_events_test.go; backend/internal/pkg/apicompat/chatcompletions_responses_bridge_custom_tools_test.go; backend/internal/service/openai_gateway_responses_client_tools_test.go; backend/internal/service/openai_responses_input_compat_test.go; backend/internal/service/openai_responses_item_id_test.go。
Image smoke evidence: 候选镜像 smoke 证据记录在发布 manifest；分别覆盖原生 Responses、Chat Completions 桥和 Anthropic 桥，不支持的工具显式标记。
First release manifest: sub2api-release-20260829-r5.json
Rollback note: 协议 smoke 失败立即回退旧镜像，不以 HTTP 200 或 healthy 作为替代证据。
Owner/status: fluter / partial by design; explicit compatibility decision required
```
