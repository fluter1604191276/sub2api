# 2026-10-08 候选用量日志可靠性第一阶段

独立分支 candidate/usage-reliability-20261008，基于已验收冻结候选 f8a9319273cc0123af4c7a2983611fdbfe9627cc；由实时r4生产基线创建后快进f8。原f8工作树/镜像e43/CI37722339741/Security37722339762全部保留。本阶段不推送、构建镜像、合并或部署，不修改生产。

## 行为

best-effort日志批写保留原10秒窗口；失败后整个单条fallback共享一个新的独立10秒窗口，串行一次每个分组，耗尽不再调用SQL。成功批次不进入fallback。该变化使原先过期context上的无效日志尝试变成可执行尝试，新增最多10秒后台SQL预算，可能与服务层同步日志兜底重叠；两条路径继续使用原(request_id, api_key_id)唯一约束的ON CONFLICT DO NOTHING，不修改为DO UPDATE，不制造新幂等键。

结果不确定且缺少持久键的日志失败闭合：repository不重写；service也不再无条件同步重写，除非明确NotPersisted。Dropped既可能是入队前也可能入队后取消，不能视为未写入证明。此保护可能使无key日志放弃一次原本可成功的兜底，但避免uncertain commit/NULL唯一键导致重复usage；不得伪报为持久化成功。已有成功recent-cache仅在SQL成功/冲突已存在后设置，失败不设置。

计费Apply次数、余额/价格/配额计算、billing幂等cmd、worker重试策略均保持不变。没有自动补扣或财务重放；usage_log补写只在日志表，不能将其解释为真实财务交易已完成。commit不确定的财务结果仍需另有脱敏对账证据与专门授权。

## 观测

新增persistence_stage：固定operation=usage_billing或usage_log、固定phase、outcome、error_class、context_state、elapsed_ms；不记录SQL/错误正文/请求或用户身份、参数、token、金额、DSN。repo.Apply与best-effort/batch/single/sync/skipped阶段区分。默认失败Warn、成功Debug；不能宣称默认配置有全量成功延迟分布。

ops池采样记录WaitCount/WaitDuration累计值和实际区间增量。首样本/重置/池替换/缺失不伪造零增量，读取Stats/时间/更新基线互斥。两个counter生命周期不同，不相除推断平均；DBConnWaiting保留nil，不伪装为当前等待连接数。无schema/配置修改，未在生产启用这些新观测。

## 验证与边界

修复前合成SQL驱动回归在批10秒耗尽后明确deadline失败；修复后针对性race覆盖成功0fallback、重复key1fallback、unknown outcome使用原ON CONFLICT、共享deadline、无键端到端闭合、成功cache/失败不缓存、财务Apply一次、匿名字段及池采样边界。真实PG新增已有row+坏FK批次失败后再fallback的回归，断言一行usage、余额/配额/dedup不变；本轮Docker引擎不可用，仅编译未运行，不声称已验证真实lost-ack。

完整本轮证据位于项目容器 .release-evidence/20261008-official-0214/usage-reliability/，按最终新SHA记录；不得继承f8的已构建镜像或hostedCI为新SHA通过。nativeamd64镜像负载/真实上游/Antigravity及braces开发依赖高危仍未消除，allow_release=false。
