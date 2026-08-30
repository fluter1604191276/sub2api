# 二开登记：可用渠道与模型广场目录体验

```text
Capability ID: catalog-surfaces
Business purpose: 让用户在可用渠道与模型广场之间快速切换，并按平台、访问范围、分组、倍率和模型名定位可用能力，同时显示当前结果摘要。
Backend/frontend files: frontend/src/components/catalog/CatalogSurfaceNav.vue; frontend/src/views/user/AvailableChannelsView.vue; frontend/src/utils/availableChannelsCatalog.ts; frontend/src/components/modelPlaza/ModelPlazaContent.vue; frontend/src/components/modelPlaza/PlazaFilterBar.vue; frontend/src/components/modelPlaza/PlazaGroupSection.vue; frontend/src/i18n/locales/zh/common.ts; frontend/src/i18n/locales/en/common.ts; frontend/src/i18n/locales/zh/dashboard.ts; frontend/src/i18n/locales/en/dashboard.ts
Routes or jobs: /available-channels; /model-plaza?embedded=1; no new backend route or scheduled job.
Database migration/data dependency: none; consumes existing /channels/available and /model-plaza response contracts.
Billing impact: none
Scheduling impact: none
Client protocol impact: none
Tests/fixtures: availableChannelsCatalog.spec.ts; CatalogSurfaceNav.spec.ts; PlazaFilterBar.spec.ts; PlazaGroupSection.spec.ts; AvailableChannelsTable.spec.ts; frontend typecheck; targeted ESLint; git diff --check.
First release manifest: pending candidate build; developed from the live production-derived 0.1.183 line.
Rollback note: revert the catalog-surface commit or restore the prior application image; no database or runtime configuration rollback is required.
Owner/status: fluter / development complete, not released to production
```

Implementation notes: available channels preserve the channel/platform/group/model
context while filtering; the model plaza keeps its existing price matrix,
exclusive-rate, peak-rate, image-rate, and long-context presentation. Access
facets are frontend-only visibility filters and do not change authorization or
billing. This record contains no keys, cookies, bearer tokens, passwords, or raw
request bodies.
