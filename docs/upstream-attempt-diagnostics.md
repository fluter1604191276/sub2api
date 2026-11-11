# 上游尝试账本与 Responses 诊断

这组观测仅记录每一次实际上游尝试的白名单标量，不参与用户计费、价格结算或调度决策。

## 账本边界

- HTTP、插件转发与 OpenAI Responses WebSocket 的每次真实发送各有独立 attempt，包括 HTTP 的重定向和 Grok transparent fallback 内部 hop。恢复探针带 `recovery_probe` 来源；插件本地不可用、未实际 dispatch 的请求不会产生虚假 attempt。
- 记录由有界异步队列写入。队列状态和丢弃计数应作为运行健康信号：队列满、进程中断、数据库故障，或 HTTP 调用者不关闭 body 时，允许丢失观测记录，不能据此推断请求没有发生。
- 价格和倍率在发起时快照，仅作影子成本统计；已知零成本与未知成本分开。不会改动账单、余额或选号。
- WebSocket passthrough 按每个 `response.create` 回合建账；连接上的前一回合不会被后一回合终态覆盖。

## Responses 诊断边界

- 原生 OpenAI、Grok、Anthropic/Claude 与 chat-fallback bridge，以及 JSON、SSE、SSE-to-JSON、插件和 WebSocket 路径，都只保存响应 ID、账号、终态、token 标量和请求 cap 的标量快照。
- 不保存 prompt、工具参数、响应正文或密钥。上游错误尾部是长度受限、脱敏后的有限 error-tail，不能作为完整错误正文或审计证据。
- 每次 WebSocket 回合在第一个正式 terminal 时投递不可变快照；中间件仅对尚未投递的请求做 fallback，避免重复持久化。

## 上线核验

上线前确认迁移已应用、异步队列未持续满载/丢弃、诊断和 attempt 的账号与上游请求 ID 可关联，并对 HTTP、插件、各 bridge 和 WS 多回合做抽样验证。观察记录是 best-effort：历史缺失不会回填，也不应用于拒绝用户请求。部署、生产迁移和真实流量检查仍须单独批准。
