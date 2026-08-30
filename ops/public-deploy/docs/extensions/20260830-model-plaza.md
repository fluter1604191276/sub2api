# 二开登记：模型广场

```text
Capability ID: model-plaza
Business purpose: 将当前用户可访问的模型按模型名与协议平台聚合展示，提供分类筛选、搜索、模型名复制，以及渠道/分组/用户侧价格详情。
Backend/frontend files: frontend/src/views/user/ModelPlazaView.vue; frontend/src/utils/modelPlaza.ts; frontend/src/utils/availableChannels.ts; frontend/src/api/channels.ts; frontend/src/components/layout/AppSidebar.vue; frontend/src/router/index.ts; frontend/src/i18n/locales/zh/dashboard.ts; frontend/src/i18n/locales/en/dashboard.ts
Routes or jobs: GET /api/v1/channels/available；前端 /model-plaza；侧边栏模型广场入口。
Database migration/data dependency: 复用现有用户可用渠道 DTO；无新增表、无写库任务。
Environment-variable/config dependency: none
Billing impact: none; 仅展示用户已经可见的分组倍率与模型定价，不展示账号成本、上游地址或凭证。
Scheduling impact: none
Client protocol impact: none
Tests/fixtures: frontend/src/utils/__tests__/modelPlaza.spec.ts；availableChannels 回归测试；pnpm exec vue-tsc -b；pnpm run lint:check；pnpm run build。
Image smoke evidence: candidate build required; 生产切换前需验证认证用户可访问 /model-plaza、搜索/分类/展开详情/复制模型名正常，未登录请求仍由路由守卫转到登录页。
First release manifest: pending candidate build
Rollback note: 回退前端应用镜像即可；不涉及数据库迁移或生产数据写入。
Owner/status: fluter / ready for candidate build
```

本登记不包含密钥、Cookie、Bearer token、数据库密码、上游 URL、内部账号名或原始流水。
