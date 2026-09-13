# 二开登记：公开目录文字模型白名单策略

```text
Capability ID: catalog-surfaces
Business purpose: 防止新增或历史内部文字模型自动出现在用户侧可用渠道和模型广场，允许管理员用默认隐藏加显式白名单控制展示。
Backend/frontend files: backend/internal/service/public_catalog_visibility.go; backend/internal/service/setting_parse.go; frontend/src/api/admin/publicCatalog.ts; frontend/src/views/admin/PublicCatalogView.vue; frontend/src/i18n/locales/zh/admin/publicCatalog.ts; frontend/src/i18n/locales/en/admin/publicCatalog.ts
Routes or jobs: GET/PUT /api/v1/admin/public-catalog/visibility；现有 /channels/available、/model-plaza 读取同一展示策略；无新增任务。
Database migration/data dependency: 无迁移；扩展 settings.public_catalog_visibility JSON，旧数据缺少字段时保持原有“文字默认展示”兼容行为。
Billing impact: none; 仅用户侧展示，不改变渠道基础价、分组倍率、模型映射或结算。
Scheduling impact: none
Client protocol impact: none
Tests/fixtures: public_catalog_visibility service tests；PublicCatalogView UI tests；available-channel/model-plaza filtering tests。
First release manifest: pending candidate manifest
Rollback note: 回退应用镜像即可；需要将 public_catalog_visibility 恢复为切换前备份值时再执行 settings 单项恢复。
Owner/status: fluter / candidate; production default to be set to hidden with an explicit reviewed allowlist
```

## Contract

- 文字和媒体模型分别拥有默认展示策略。
- 显式 `platform:model` 覆盖优先于默认策略，键按小写规范化。
- GPT 图片模型仍按既有兼容规则默认展示；其他媒体模型默认隐藏。
- 该配置只控制用户侧目录，不影响后台渠道、账号模型、分组限制、计费、探针或智能调度。
