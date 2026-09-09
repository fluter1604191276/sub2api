# Sub2API 新会话接手协议

这份文档是新 Codex/agents 会话的第一入口。它解决两个高频问题：找错
工作区，以及从旧版本/旧镜像继续二开而丢失生产二开。

## 先建立事实

先读本仓库的 `AGENTS.md`、`ops/public-deploy/docs/RELEASE-LINES.md`、
`RELEASE-BASELINE.md`、`PRODUCTION-EXTENSIONS.md`，再执行下面的只读检查：

```bash
git status --short --branch
git rev-parse --show-toplevel
git rev-parse HEAD
git remote -v
git worktree list
ssh fluterapi-prod 'cat /etc/fluterapi-node-role'
ssh fluterapi-prod 'docker inspect sub2api --format "{{.Config.Image}}|{{.Image}}|{{index .Config.Labels \"org.opencontainers.image.revision\"}}|{{index .Config.Labels \"org.opencontainers.image.source-snapshot\"}}"'
ssh fluterapi-prod 'cd /www/sub2api && docker compose ps'
```

生产 SSH 别名必须是 `fluterapi-prod`，并且角色文件必须精确返回
`production`。连接成功、hostname、IP 或旧别名都不能替代角色校验。旧节点
使用 `fluterapi-legacy`，清理时保留其长期代理服务。

Git remote 要看实际输出：本项目通常把 `fluter` 指向
`fluter1604191276/sub2api`，把 `origin` 指向官方上游 `Wei-Shaw/sub2api`。
不要把 `origin` 自动当成发布仓库，也不要根据仓库名猜生产来源。

实时容器标签、不可变镜像 digest、Git revision、源码快照 hash 和 release
manifest 组成一次发布的完整身份。历史文档、镜像 tag、旧 worktree 或当前
终端路径都只是线索，不能单独证明当前生产版本。

## 找对开发源

如果当前目录不是 Git 工作树，停止在该目录构建。不要从压缩包、生产目录、
临时复制目录或历史版本名 worktree 反向构建。进入包含本仓库的有效 checkout，
从实时生产镜像的 revision 创建下一条开发线：

```bash
cd <有效仓库 checkout>
ops/public-deploy/create-production-derived-worktree.sh <name> <branch>
cd <仓库根目录>/.worktrees/<name>
ops/public-deploy/check-production-baseline.sh
```

这个脚本会先校验生产角色、镜像 digest、revision 和 source snapshot，再创建
工作树。生产切换后，旧工作树自动变成历史证据，不能继续叠加下一轮二开。

## 二开完整性

每项二开必须同时有：源码、路由或后台任务、数据/配置影响、测试、镜像 smoke
证据和回滚说明。登记位置是：

- `ops/public-deploy/docs/PRODUCTION-EXTENSIONS.md`
- `ops/public-deploy/docs/extensions/YYYYMMDD-<name>.md`

重点核对现有生产能力：智能调度及粘性策略、智能探针及预算、账号/分组质量
与缓存命中率、模型同步与筛选、分页 100、渠道/图像/工具计费、错误转换、
长上下文计费、可用渠道、模型广场，以及 Responses 协议兼容边界。清单中任何
一项没有代码和测试证据，都不能用“容器 healthy”代替。

## 构建、推送、切换

保持 clean commit，按路径显式提交。使用 `build-production-image.sh` 构建
`linux/amd64` 镜像，生成 secret-free release manifest，并用
`verify-release-bundle.sh` 验证每项能力。镜像必须记录 commit、source snapshot
和 digest；可读 tag 不能代替 digest。

推送默认使用用户指定的功能分支。用户明确确认发布仓库和 `main` 后，才允许
以普通 push 更新 `main`；禁止 force push。推送后重新核对远端 commit、工作区、
manifest 和镜像 label。

生产切换前确认角色文件、备份数据库/Compose/env/当前镜像和 Caddy 配置。正常
切换只更新应用服务：

```bash
docker compose up -d --no-deps sub2api
```

保留旧镜像和回滚 Compose，切换后逐项验证健康接口、认证边界、核心 API、计费、
二开页面、调度/探针/质量字段和协议 fixture。发现能力缺失、工具被静默丢弃、
计费异常或核心路由缺失时，保留失败证据并按 manifest 回滚。

## 交接包

每轮完成后在最终报告中给出：生产镜像与 digest、Git revision、源码快照、工作
树和分支、release manifest、扩展清单验证结果、测试结果、切换前备份路径、回滚
对象和遗留风险。不要写入任何 key、token、cookie、OAuth 数据、私钥或完整敏感
配置。

这份文档不是实时状态面板。每次新会话开始都必须重新查询生产；完成生产切换后，
更新 `RELEASE-LINES.md` 和 `RELEASE-BASELINE.md` 的当前事实，并保留旧值作为
历史发布记录。
