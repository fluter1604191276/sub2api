# 二开登记：公开目录图片模型展示策略

```text
Capability ID: catalog-surfaces
Business purpose: 在生图/视频模型完成接口可用性测试和定价校准前，公开“可用渠道”和“模型广场”默认仅展示已确认的 GPT 图片模型，并允许管理员通过独立页面逐项覆盖展示状态。
Backend/frontend files: backend/internal/service/public_catalog_visibility.go; backend/internal/handler/admin/public_catalog_handler.go; frontend/src/views/admin/PublicCatalogView.vue; frontend/src/api/admin/publicCatalog.ts; frontend/src/utils/availableChannelsCatalog.ts; frontend/src/views/user/AvailableChannelsView.vue; frontend/src/components/modelPlaza/ModelPlazaContent.vue; backend/internal/service/public_catalog_visibility_test.go; frontend/src/views/admin/__tests__/PublicCatalogView.spec.ts; frontend/src/utils/__tests__/availableChannelsCatalog.spec.ts
Routes or jobs: /available-channels; /model-plaza; /admin/channels/catalog; GET/PUT /api/v1/admin/public-catalog/visibility; no scheduled job
Database migration/data dependency: no migration; uses the independent `public_catalog_visibility` setting and filters the existing public response data
Billing impact: none; hidden models remain unchanged in internal pricing and billing
Scheduling impact: none; visibility is not an account or group eligibility signal
Client protocol impact: none
Tests/fixtures: public_catalog_visibility_test.go; available_channel_handler_test.go; model_plaza_handler_test.go; PublicCatalogView.spec.ts; availableChannelsCatalog.spec.ts; frontend typecheck; ESLint; frontend build; git diff --check
First release manifest: pending candidate build; release smoke remains pending
Rollback note: restore the prior frontend application image or revert this display-only commit; no database rollback is required
Owner/status: fluter / candidate implementation, not released to production
```

Policy boundary: non-media text models remain visible. Media models are recognized
by their billing mode or common image/video family names, and the default allowlist
contains only the exact `gpt-image` name or names beginning with `gpt-image-`.
Administrators may change the default media policy or an explicit `platform:model`
override from `/admin/channels/catalog`. This filter must not be reused for account
model whitelists, routing, scheduler decisions, upstream probes, monitoring, or
billing.

This record contains no keys, cookies, bearer tokens, passwords, upstream URLs, or
raw request bodies.
