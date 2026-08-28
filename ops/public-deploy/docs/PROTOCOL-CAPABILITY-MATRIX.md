# 协议能力矩阵

这份矩阵用于解释 2026-08-28 事故中的终端问题，并作为发布验收的固定项。

| 入站请求 | 上游路径 | `function` 工具 | `local_shell` / `custom` 等 Responses 原生工具 | 结论 |
| --- | --- | --- | --- | --- |
| OpenAI Responses | 原生 `/v1/responses` | 保留 | 由上游是否支持决定，必须实测 | 可用性按上游能力确认 |
| OpenAI Responses | Chat Completions 兼容桥 | 可转换 | Chat Completions 没有等价原生表达，不能宣称终端能力 | 必须显式标记不支持/拒绝，禁止静默丢弃 |
| Anthropic Messages | Chat Completions 兼容桥 | 可转换 | 普通工具映射不等于终端执行语义 | 必须显式标记边界，禁止当作终端直通 |

验收必须包含三类 fixture：工具定义、工具调用输出、流式事件。只检查 HTTP 200、容器
healthy 或普通文字回复是不够的。若桥接路径仍保持兼容过滤行为，也必须让发布清单把该项
标为 `partial` 并写明“终端工具不支持，需原生 Responses 上游”这一兼容决策。
