# 二开登记：V1 流式渠道探针兼容

```text
Capability ID: v1-streaming-probe
Business purpose: 让 V1 主动探针使用与真实请求一致的流式协议，避免只支持流式的 OpenAI-compatible 上游因 stream=false 被误标红，同时保留真实上游故障。
Backend/frontend files: backend/internal/service/channel_monitor_checker.go; backend/internal/service/channel_monitor_checker_body_test.go
Routes or jobs: V1 channel monitor probe runner；无新增路由。
Database migration/data dependency: none
Environment-variable/config dependency: none
Billing impact: internal cost
Scheduling impact: score；探针结果影响渠道可用性与智能调度输入
Client protocol impact: OpenAI Chat Completions；OpenAI Responses；OpenAI-compatible upstreams
Tests/fixtures: 默认 Chat/Responses stream=true；Chat SSE delta；Responses response.output_text.delta；replace 模式明确 stream-only 400 的单次重试；普通 400 不重试。
First release manifest: pending
Rollback note: 回退应用镜像即可；监控配置和历史结果保留。
Owner/status: fluter / ready for candidate build
```
