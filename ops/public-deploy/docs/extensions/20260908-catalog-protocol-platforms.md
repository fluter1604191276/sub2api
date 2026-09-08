# 二开登记：目录统一使用协议平台

```text
Capability ID: catalog-surfaces
Business purpose: 让“可用渠道”和“模型广场”的平台筛选、徽章与计数统一使用分组管理中的请求协议平台，避免把模型供应商误显示为分组平台。
Backend/frontend files: frontend/src/constants/platforms.ts; frontend/src/utils/availableChannelsCatalog.ts; frontend/src/api/channels.ts; frontend/src/components/channels/AvailableChannelsTable.vue; frontend/src/components/modelPlaza/ModelPlazaContent.vue; frontend/src/components/modelPlaza/PlazaFilterBar.vue
Routes or jobs: /available-channels; /model-plaza; no backend route or scheduled job change
Database migration/data dependency: none; the existing group/section platform returned by the backend is authoritative
Billing impact: none
Scheduling impact: none
Client protocol impact: none
Tests/fixtures: platforms.spec.ts; availableChannelsCatalog.spec.ts; AvailableChannelsTable.spec.ts; ModelPlazaContent.spec.ts; PlazaFilterBar.spec.ts; frontend typecheck; frontend ESLint; frontend build; git diff --check
First release manifest: pending candidate build
Rollback note: revert this frontend change or restore the previous application image; no database rollback is required.
Owner/status: fluter / implemented locally, pending candidate verification
```

Implementation note: only platforms present in the current visible dataset are
shown, but their identity and ordering come from the same protocol-platform
catalog used by group management. OpenAI-compatible DeepSeek, GLM, Kimi, or
other models therefore remain under OpenAI when their group protocol is OpenAI.
