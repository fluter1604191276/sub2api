# 二开登记：可用渠道与模型广场目录体验

```text
Capability ID: catalog-surfaces
Business purpose: 让用户在可用渠道与模型广场之间快速切换，并由管理员独立控制哪些模型可以出现在用户侧目录中。
Backend/frontend files: backend/internal/service/public_catalog_visibility.go; backend/internal/handler/admin/public_catalog_handler.go; backend/internal/server/routes/admin.go; frontend/src/views/admin/PublicCatalogView.vue; frontend/src/api/admin/publicCatalog.ts; frontend/src/components/catalog/CatalogSurfaceNav.vue; frontend/src/views/user/AvailableChannelsView.vue; frontend/src/utils/availableChannelsCatalog.ts; frontend/src/components/modelPlaza/ModelPlazaContent.vue; frontend/src/components/modelPlaza/PlazaFilterBar.vue; frontend/src/components/modelPlaza/PlazaGroupSection.vue; frontend/src/i18n/locales/zh/common.ts; frontend/src/i18n/locales/en/common.ts; frontend/src/i18n/locales/zh/dashboard.ts; frontend/src/i18n/locales/en/dashboard.ts
Routes or jobs: /available-channels; /model-plaza?embedded=1; /admin/channels/catalog; GET/PUT /api/v1/admin/public-catalog/visibility; no scheduled job.
Database migration/data dependency: no migration; persists the independent SettingKey `public_catalog_visibility` and consumes existing available-channel/model-plaza response contracts.
Billing impact: none
Scheduling impact: none
Client protocol impact: none
Tests/fixtures: public_catalog_visibility_test.go; available_channel_handler_test.go; model_plaza_handler_test.go; PublicCatalogView.spec.ts; availableChannelsCatalog.spec.ts; CatalogSurfaceNav.spec.ts; PlazaFilterBar.spec.ts; PlazaGroupSection.spec.ts; AvailableChannelsTable.spec.ts; frontend typecheck; ESLint; frontend build; git diff --check.
First release manifest: pending candidate build; developed from the live production-derived 0.1.183 line. The exact source commit and snapshot hash must be captured by the release manifest.
Rollback note: revert the catalog-surface commit or restore the prior application image; no database or runtime configuration rollback is required.
Owner/status: fluter / development complete, not released to production
```

Implementation notes: available channels preserve the channel/platform/group/model
context while filtering; the model plaza keeps its existing price matrix,
exclusive-rate, peak-rate, image-rate, and long-context presentation. The admin
visibility page changes only the public response presentation: it does not change
authorization, channel pricing, model mappings, group routing, billing, probes,
monitoring, or smart scheduling. Text models remain visible by default; only
`gpt-image`/`gpt-image-*` media models are exposed by default until other media
families pass compatibility and pricing review. Explicit `platform:model`
overrides are normalized case-insensitively and retained when a candidate is
temporarily absent. This record contains no keys, cookies, bearer tokens,
passwords, or raw request bodies.
