# 二开登记：可用渠道分类目录

```text
Capability ID: available-channels-catalog
Business purpose: 按模型类别、协议平台、渠道分组和支持模型展示用户可用渠道，降低长列表中模型归属不清的问题。
Backend/frontend files: frontend/src/components/channels/AvailableChannelsTable.vue; frontend/src/views/user/AvailableChannelsView.vue; frontend/src/utils/availableChannels.ts; frontend/src/i18n/locales/zh/dashboard.ts; frontend/src/i18n/locales/en/dashboard.ts
Routes or jobs: GET /api/v1/channels/available；前端 /available-channels
Database migration/data dependency: 复用 channels、channel_groups、groups、channel_model_pricing 现有用户可用渠道 DTO；无新增表
Environment-variable/config dependency: none
Billing impact: none; 仅展示既有分组倍率和渠道定价
Scheduling impact: none
Client protocol impact: none
Upstream version/base commit: v0.1.183 lineage, current custom release branch
Tests/fixtures: frontend/src/utils/__tests__/availableChannels.spec.ts; pnpm run build; pnpm run lint:check
Image smoke evidence: pending candidate build; 生产切换前需验证用户可用渠道接口与页面类别/价格浮层
First release manifest: pending candidate build
Rollback note: 回退前端镜像即可；数据库渠道、分组和定价数据不依赖本功能，已通过生产备份保护
Owner/status: fluter / ready for candidate build
```
