# 二开登记：国内/国外模型价格显示币种语义

```text
Capability ID: pricing-calibration
Business purpose: 让国内模型价格显示为人民币符号、国外模型价格显示为美元符号，同时明确两者都只是统一站内计费单位的展示语义。
Backend/frontend files: backend/internal/service/domain_constants.go; backend/internal/handler/available_channel_handler.go; backend/internal/handler/model_plaza_handler.go; backend/internal/handler/admin/channel_handler.go; frontend/src/api/channels.ts; frontend/src/api/modelPlaza.ts; frontend/src/api/admin/channels.ts; frontend/src/utils/pricing.ts; frontend/src/components/channels/PricingRow.vue; frontend/src/components/channels/SupportedModelChip.vue; frontend/src/components/modelPlaza/PlazaModelPricingTable.vue; frontend/src/views/user/ModelPlazaView.vue
Routes or jobs: 现有 /channels/available、/model-plaza、/admin/channels API；无新增任务。
Database migration/data dependency: 无迁移；货币由定价条目的 platform 推导，避免新增可漂移的币种配置。
Billing impact: none; currency only changes display symbols and never enters settlement math.
Scheduling impact: none
Client protocol impact: none
Tests/fixtures: PricingCurrencyForPlatform unit test；现有可用渠道/模型广场与前端构建验证。
First release manifest: pending candidate manifest
Rollback note: 回退应用镜像即可；无数据库回滚要求。
Owner/status: fluter / candidate implementation, not released to production
```

## Contract

- `deepseek`、`zhipu`、`kimi`、`minimax`、`qwen` 显示 `¥`。
- `openai`、`anthropic`、`gemini`、`grok` 及未知平台显示 `$`。
- 官方参考价仍按官方目录的美元语义显示；它与本站实收价格是两列不同的数据。
- 价格数值、分组倍率、用户倍率、缓存价、长上下文区间价、按次价和图片价均不改变。
- 余额仍是一种无货币属性的站内计费单位；不存在美元余额与人民币余额的换算。

本登记不包含任何 key、cookie、Bearer token、密码或原始请求体。
