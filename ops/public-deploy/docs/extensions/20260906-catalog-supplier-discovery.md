# 二开登记：目录供应商识别与协议平台隔离

```text
Capability ID: catalog-surfaces
Business purpose: 让用户侧可用渠道和模型广场按模型供应商识别 OpenAI-compatible 的 Kimi、DeepSeek 及其他已支持国产模型，不再因上游协议相同而被合并到 OpenAI 视图中。
Backend/frontend files: frontend/src/utils/modelProvider.ts; frontend/src/utils/availableChannelsCatalog.ts; frontend/src/api/channels.ts; frontend/src/views/user/AvailableChannelsView.vue; frontend/src/components/channels/AvailableChannelsTable.vue; frontend/src/components/modelPlaza/ModelPlazaContent.vue; frontend/src/components/modelPlaza/PlazaFilterBar.vue
Routes or jobs: /available-channels; /model-plaza; no backend route or scheduled job change
Database migration/data dependency: no migration; requires an active channel attached to a user-visible group. Inactive DeepSeek channels remain hidden by design.
Billing impact: none
Scheduling impact: none
Client protocol impact: none
Tests/fixtures: modelProvider.spec.ts; availableChannelsCatalog.spec.ts; frontend ESLint; frontend build; git diff --check
First release manifest: pending candidate build
Rollback note: revert this frontend commit or restore the previous application image; no database rollback is required.
Owner/status: fluter / implemented locally, pending candidate verification
```

Implementation note: `platform` remains the request/routing protocol from the
backend. `display_platform` is a frontend-only field used for badges and group
colors after a mixed protocol section is split by inferred supplier.
