# 二开登记：模型定价解析兼容与 DeepSeek v4.1 Flash 计费

```text
Capability ID: pricing-calibration
Business purpose: 修复已确认的 DeepSeek v4.1 Flash 未命中本站兜底价格问题，并兼容官方模型名带已知 OpenAI/Codex 变体后缀时的渠道定价查找，避免实际扣费回落到错误价格。
Backend/frontend files: backend/internal/service/billing_service.go; backend/internal/service/billing_service_test.go; backend/internal/service/model_pricing_resolver.go; backend/internal/service/model_pricing_resolver_test.go
Routes or jobs: 现有 OpenAI/兼容协议计费路径；无新增路由或后台任务。
Database migration/data dependency: 无迁移；继续使用现有渠道模型定价、分组价格和长上下文计费开关。
Billing impact: user charge and internal cost; only known model aliases are affected.
Scheduling impact: none
Client protocol impact: OpenAI/Codex and compatible token billing paths
Tests/fixtures: DeepSeek v4.1 Flash 两种别名价格回归测试；OpenAI/Codex 渠道基础模型命中、精确变体优先、不相关模型不误命中测试；后端单测、前端类型检查/构建、镜像能力门禁。
First release manifest: pending candidate manifest
Rollback note: 保留当前生产镜像和配置，异常时只回退应用镜像；无数据库回滚要求。
Owner/status: fluter / candidate implementation, not released to production
```

Pricing boundary:

- `deepseek-v4.1-flash` and `deepseek-v4-1-flash` use the confirmed site
  billing rates: input `¥2/M`, output `¥10/M`, cache read `¥0.041/M`.
- The site keeps its existing USD/RMB purchasing-power convention of `1:1`.
- Channel lookup tries the literal request model first. Only a known
  OpenAI/Codex model alias may then fall back to its canonical base model;
  explicit variant pricing always wins.
- Unknown OpenAI, DeepSeek, or other provider identifiers do not receive a
  newly inferred price from this change.

This record contains no keys, cookies, bearer tokens, passwords, or raw request
bodies.
