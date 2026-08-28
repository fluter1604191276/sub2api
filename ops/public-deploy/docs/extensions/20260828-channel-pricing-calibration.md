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
Upstream version/base commit: v0.1.171
Official base commit: f0e7a9c7a23a7d02fb159b62fa809621eb0475a6
Custom source branch: release/v0.1.171-fluter-full-custom-20260829
Tests/fixtures: channel calibration, image pricing, gateway usage and frontend pricing tests。
Image smoke evidence: 候选镜像 smoke 证据记录在发布 manifest；覆盖校准预览、模型应用和图片成本上下文解析。
First release manifest: sub2api-release-20260829-r5.json
Rollback note: 数据写入前备份；回退镜像并按记录恢复 pricing migration/data。
Owner/status: fluter / ready for candidate build
```
