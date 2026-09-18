# DeepSeek public pricing uses a fixed peak base

```text
Capability ID: pricing-calibration
Business purpose: 统一 DeepSeek 公共价卡口径，避免同一模型因请求时间落入不同峰谷价而产生不可预期的站内售价。
Backend/frontend files: backend/internal/service/billing_service.go; backend/internal/service/model_plaza_official_pricing.go; backend/resources/model-pricing/model_prices_and_context_window.json; related pricing tests.
Routes or jobs: 现有统一计费、可用渠道和模型广场定价解析；无新增路由或后台任务。
Database migration/data dependency: 无迁移；生产 channel 41 的公共 model_pricing 需要单独校准，account_stats_pricing_rules 保持原样。
Environment-variable/config dependency: 无新增环境变量；校准脚本必须显式使用 PRICING_CHANNEL_IDS=41。
Billing impact: user charge and official reference display; account internal cost cards remain independent.
Scheduling impact: none
Client protocol impact: none
Upstream version/base commit: production revision 3be979bebe408c9732b590f3095d42f2a084a46f.
Tests/fixtures: DeepSeek fixed-price, alias, cache, account-cost priority, gateway usage, model-plaza and JSON pricing tests; full backend service unit suite passed.
Image smoke evidence: pending candidate image build.
First release manifest: pending candidate image build.
Rollback note: restore the previous application image; if channel 41 is calibrated, restore the recorded pricing-before dump or the channel-41 pre-change payload.
Owner/status: fluter / candidate; production image not switched.
```

## Pricing contract

- Flash: `¥2/M` input, `¥8/M` output, `¥0.04/M` cache read.
- Pro: `¥9/M` input, `¥27/M` output, `¥0.30/M` cache read.
- The site treats the displayed CNY values as unified internal billing units; no CNY/USD conversion is applied.
- A user group multiplier is applied once to these peak-base prices. For example, `0.6x` yields Flash `1.2 / 4.8 / 0.024` and Pro `5.4 / 16.2 / 0.18` per million tokens.
- Public DeepSeek cards have `time_pricing = null`. This change does not delete the generic group peak/off-peak feature and does not rewrite account-specific upstream cost rules.
- Legacy and dated aliases are grouped by service tier: `deepseek-flash`, `deepseek-v4-flash`, `deepseek-v4-flash-0731`, `deepseek-v4-flash-vision-exp`, `deepseek-v4.1-flash`, and `deepseek-v4.1-flash-0910` use Flash; `deepseek-v4-pro` and `deepseek-v4-pro-0813` use Pro.

## Verification boundary

The production preview targets only channel 41 and leaves channel identity,
mappings, group membership, and account cost rules outside the public card
replacement. A write must be preceded by the existing `pricing-before.dump`
backup and followed by a readback proving every public card has
`time_pricing = null`.
