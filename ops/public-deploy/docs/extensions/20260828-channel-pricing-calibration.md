# 二开登记：渠道定价校准与生图成本

```text
Capability ID: pricing-calibration; image-cost
Business purpose: 校准渠道模型映射；将生图上游成本按操作/尺寸独立于用户扣费记录，避免混淆成本与售价。
Backend/frontend files: backend/internal/service/channel_model_calibration.go; backend/internal/repository/channel_repo_pricing.go; backend/internal/service/account_stats_image_pricing.go; backend/internal/service/account_stats_pricing.go; backend/internal/server/routes/admin.go; frontend/src/views/admin/ChannelsView.vue; frontend/src/components/admin/channel/PricingEntryCard.vue
Routes or jobs: GET /admin/channels/model-calibration/preview；POST /admin/channels/model-calibration/apply。
Database migration/data dependency: channel_model_pricing；account stats image operation migration。
Environment-variable/config dependency: none
Billing impact: internal cost; user charge remains separate from account stats pricing.
Scheduling impact: none
Client protocol impact: OpenAI image/Responses request context。
Upstream version/base commit: v0.1.149 lineage, current custom release branch
Tests/fixtures: channel calibration, image pricing, gateway usage and frontend pricing tests。
Image smoke evidence: 发布后需验证预览不写库、应用只改模型列表、图片成本按上下文解析。
First release manifest: pending candidate build
Rollback note: 数据写入前备份；回退镜像并按记录恢复 pricing migration/data。
Owner/status: fluter / ready for candidate build
```
