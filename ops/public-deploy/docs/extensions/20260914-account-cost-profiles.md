# 二开登记：账号级上游成本价卡与覆盖审计

```text
Capability ID: pricing-calibration
Business purpose: 让账号内部成本统计使用经过上游流水核实的账号级价格，避免将 LiteLLM 默认价误当成可达鸭/忘川等折后上游成本；同时审计其它模型是否仍走回退路径。
Backend/frontend files: backend/internal/service/account_stats_pricing.go; group_recovery_probe_billing.go; channel_service.go; backend/internal/repository/channel_repo_account_stats_pricing.go; backend/internal/handler/admin/channel_handler.go; frontend/src/views/admin/ChannelsView.vue; frontend/src/components/admin/channel/PricingEntryCard.vue
Routes or jobs: ops/public-deploy/upstream-rates/audit_account_stats_cost_coverage.py（只读审计）
Database migration/data dependency: 复用现有账号统计定价表；238_account_stats_time_pricing.sql 只增加可空 time_pricing JSONB 列；cache_write_1h_price 复用已部署的 migration 232。
Billing impact: internal cost
Scheduling impact: 不改变选号或粘性算法；更准确的成本记录可能影响成本看板和账号额度统计。探针账本按修正后的账号成本结算。
Client protocol impact: none
Tests/fixtures: account_stats_cost_profile_test.go 的账单复算、裸名 Pro 回退、账户隔离、峰谷与周末边界、探针跨时段和单次倍率；repository/handler 的 1h 缓存及 time_pricing 往返；前端 channel 测试；审计脚本 unittest。
First release manifest: pending candidate manifest
Rollback note: 固定价卡可单独撤回本轮规则，不动其它规则/售价/历史流水。启用分时规则后，回滚旧镜像前必须停用这些分时规则，否则旧代码会忽略时段。新增 nullable 列可保留，禁止为回滚删除已记录的账单。
Owner/status: fluter / candidate code; 2026-09-14 已备份并写入五个账号的八张固定成本价卡；分时价卡尚未启用；没有切换镜像。
```

约束：账号级价格填写“账号倍率应用前”的基准价，最终聚合时只乘一次
`accounts.rate_multiplier`。它不改变用户扣费；用户价格仍由分组/用户专属倍率决定。

本轮可达鸭 DeepSeek 价格依据截图流水换算，具体生产写入记录在运维备份与发布证据中，
不在源码中保存凭证或敏感数据。

## 核价边界

- 输入、缓存读取、缓存写入分别核算；缓存 token 不能再次计入未命中输入。
- NULL `account_stats_cost` 不是零成本，仍用
  `COALESCE(account_stats_cost,total_cost) * COALESCE(account_rate_multiplier,1)`。
- 公开模型广场 `model.pricing` 是分组倍率前价格。价格卡与截图中的折后价格不能混用，
  账号倍率只能乘一次；本站不额外做美元人民币换汇。
- 裸名与日期后缀可以是不同商品，不用通配符把裸名 DeepSeek Pro 改成日期版价格。
- 自定义账号规则优先于 `apply_pricing_to_account_stats` 开关；开关继续为 false，
  不允许为了修成本开启用户定价复制或改分组倍率。
- 账号价卡的空平台表示不限平台，不能在响应中回填为 anthropic。混合平台规则
  保存时保留各条目的原始平台、OR 匹配范围及规则顺序；界面平台分栏只是显示归属，
  不得因打开再保存而缩小范围或改变首个命中规则。
- 分时成本复用现有 ChannelTimePricing，使用请求发生时间，而不是写流水时的时间。
- 默认服务档位的空字符串仍占据参数位置，不得把后续 max 推理强度吞入档位。
  兼容两种调用形态，并保留已有 Fable max 内部成本附加倍率，不改变用户计费。
- 公开价卡是当前报价证据，不等于上游真实扣款证明；只有样本流水或余额差额能够对账。
- 当前配置覆盖审计不能反推历史流水当时使用的定价来源，也不能据此批量改历史账单。
- 审计使用成本解析器当前实际读取的分组平台；composite 不擅自改成账号平台，
  避免把未命中的具体平台规则报告为已覆盖。运行逻辑的 composite 平台处理另行评估。

## 发布与验证

代码仍基于实时核验的生产 revision `c2a32bfcc35f8f7981b0af15258681c052a85858`。
本轮不改变智能调度、模型同步、展示目录、用户分组倍率或余额。
固定价卡兼容当前镜像，不依赖新列；不同 1h 缓存价和分时价依赖本轮新代码。

数据库写前保存了七张渠道/成本定价表的 PostgreSQL custom dump，权限 0600，
用 pg_restore 目录检查通过。生产私有证据和 SQL 留在项目容器 `.release-evidence/`，
不将账号明细、客户账单或生产备份提交到公共仓库。

分时规则上线前必须重新核价、备份，确认 migration 238 和代码均已部署；
忘川 DeepSeek Pro 每日峰时翻倍，Flash 只在工作日峰时翻倍，两者不能合并为同一规则。
候选镜像 smoke 与真实新流水对账未做，不能仅凭单元测试宣布已部署完成。

2026-09-15 收尾验证：账号成本、DeepSeek、分时价、探针结算及仓储/API 往返的
定向 Go 测试通过；前端 channel 61 项测试、typecheck、定向 ESLint 通过；
审计脚本 21 项测试及生产只读 SQL 执行通过；定向 go vet 通过。
默认档位吞掉 max 强度的失败在相同生产基线复现，本轮新增回归并修复。
最新生产只读抽查仍未出现这五个账号在缓存过期后的新流水，不能据此推断端到端已验证。

已知基线测试缺口：`TestAliyunCaptchaVerifier_TransportError` 和
`TestGetModelDefaultPricing_ReturnsFable51CacheTTLs` 在本轮候选与相同生产基线均失败。
前者涉及验证码传输错误分类，后者涉及默认价查询接口的 Fable max 推理倍率字段为空
（该测试的 1h 缓存断言实际已通过）；
不把它们包装为本轮回归，也不宣称整个后端全量测试通过。发布前仍需单独登记处理。

当前生产渠道缓存最多保留 10 分钟，Redis 发布未发现该服务的订阅者。
直接 SQL 配置变更需留出缓存过期时间并验证新流水，不能把发布通知返回 0 当作刷新成功，
更不能为了刷新成本配置重启生产。缓存订阅接线问题另记后续任务，不混入本轮计费修复。
