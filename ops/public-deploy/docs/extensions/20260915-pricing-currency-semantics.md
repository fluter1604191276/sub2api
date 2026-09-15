# 二开登记：官方币种语义与显式渠道定价边界

```text
Capability ID: pricing-calibration
Business purpose: 按国内/国外模型保留官方价格币种语义，并允许已配置完整 token 价的渠道模型在官方目录暂未收录时安全计费。
Backend/frontend files: backend/internal/service/billing_service.go; backend/internal/service/billing_service_test.go; backend/internal/service/pricing_service.go; backend/internal/service/model_plaza_service.go; backend/internal/handler/model_plaza_handler.go; frontend pricing/catalog files from the parent pricing-display-currency extension
Routes or jobs: 现有模型定价、可用渠道和模型广场接口；无新增任务。
Database migration/data dependency: 无迁移；渠道模型定价沿用现有字段，显式渠道价需同时提供 input_price 与 output_price。
Billing impact: internal cost and official reference display metadata; currency never changes settlement arithmetic.
Scheduling impact: none
Client protocol impact: none
Tests/fixtures: GLM CNY fallback tests; unknown-model explicit channel pricing tests; image-only/partial-price fail-closed tests; currency metadata arithmetic invariance test; model-plaza handler/service tests.
First release manifest: pending candidate manifest
Rollback note: 回退应用镜像即可；无数据库回滚要求。
Owner/status: fluter / candidate, production not switched
```

## Contract

- 国产模型价卡使用中国区官方人民币口径并标记 `CNY`；海外模型使用官方美元口径并标记 `USD`。
- 站内余额仍是无币种的统一计费单位，`Currency` 与 `PriceBasis` 只用于展示、审计和追溯，不参与结算换算。
- 未核实的 GLM 旧型号不再猜测 fallback 价格，避免把错误官方价带入成本计算。
- 若模型不在动态目录或受控 fallback 中，渠道必须同时提供明确的输入和输出 token 价格，才能形成基础价卡；仅图片价、仅缓存价或只填一侧 token 价均 fail-closed。
- 对未知模型的显式渠道价，平台为国内平台标记 `CNY`，其余标记 `USD`；这只是来源币种元数据，不改变数值结算。

本登记不包含任何 key、cookie、Bearer token、密码或原始请求体。
