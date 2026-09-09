# Sub2API 新会话接手协议

这份文档是新 Codex/agents 会话的第一入口。它解决两个高频问题：找错
工作区，以及从旧版本/旧镜像继续二开而丢失生产二开。

本机具体路径与 SSH 已知节点见运维 skill 的 `references/new-session-handoff.md`。
新 clone 没有 skill 时，先完成本地 Git 检查，再发现已有 SSH 配置；缺少凭证只阻断
远端操作，不阻断本地阅读、测试或文档维护。不要安装整个生产栈才能开始阅读项目。

## 先建立事实

先读本仓库的 `AGENTS.md`、`ops/public-deploy/docs/RELEASE-LINES.md`、
`RELEASE-BASELINE.md`、`PRODUCTION-EXTENSIONS.md`，再执行下面的只读检查：

```bash
git status --short --branch
git rev-parse --show-toplevel
git rev-parse HEAD
git remote -v  # 只查看无内嵌凭证的 URL；发现凭证须脱敏，不向聊天转贴
git worktree list
ssh fluterapi-prod 'cat /etc/fluterapi-node-role'
ssh -o BatchMode=yes -o ConnectTimeout=8 fluterapi-prod 'docker inspect sub2api --format "{{.Config.Image}}|{{.Image}}|{{index .Config.Labels "org.opencontainers.image.revision"}}|{{index .Config.Labels "org.opencontainers.image.source-snapshot"}}"'
ssh fluterapi-prod 'cd /www/sub2api && docker compose ps'
```

生产 SSH 默认别名是 `fluterapi-prod`，角色文件必须精确返回
`production`。连接成功、hostname、IP 或旧别名都不能替代角色校验。旧节点
使用 `fluterapi-legacy`，清理时保留其长期代理服务。

别名解析失败时用 `ssh -G` 检查 hostname/port/user/identityfile，按现有配置与
skill 定位兼容别名；明确指定并核验节点后，可用 `PRODUCTION_ALIAS` 覆盖现有脚本。
不凭空新增节点、不扫描 IP 段、不复制私钥、不关闭主机密钥校验。已经位于生产 VPS
的会话直接执行只读命令，构建门禁用 `ON_PRODUCTION_HOST=1`，无需 SSH 回自身。

Git remote 要看实际输出：本项目通常把 `fluter` 指向
`fluter1604191276/sub2api`，把 `origin` 指向官方上游 `Wei-Shaw/sub2api`。
不要把 `origin` 自动当成发布仓库，也不要根据仓库名猜生产来源。

实时容器标签、不可变镜像 digest、Git revision、源码快照 hash 和 release
manifest 组成一次发布的完整身份。历史文档、镜像 tag、旧 worktree 或当前
终端路径都只是线索，不能单独证明当前生产版本。

## 找对开发源

如果当前目录不是 Git 工作树，检查其 `project/` 及 Git worktree 登记。
不要从压缩包、生产运行数据、无 Git 身份的复制目录或仅凭历史版本名构建。
注册过的临时 worktree 仍要校验提交，且交接前必须有持久源码与证据副本。
进入包含本仓库的有效 checkout，
从实时生产镜像的 revision 创建下一条开发线：

```bash
cd <有效仓库 checkout>
ops/public-deploy/create-production-derived-worktree.sh <name> <branch>
cd <脚本输出的实际 worktree 路径>
ops/public-deploy/check-production-baseline.sh
```

这个脚本会先校验生产角色、镜像 digest、revision 和 source snapshot，再创建
工作树。实际路径按 Git common dir 计算，可能在 `project/.worktrees/`；不要猜路径。
缺少 revision 时从已核验的发布 remote fetch，再验证 commit 存在。生产切换后，
下一轮业务开发重新派生；已经在做的分支先比较新基线，保留用户改动并审查整合。

治理文档可以单独从已验证的生产后继提交维护。它们不改变生产镜像身份，不必为了
更新本页重新构建镜像。新业务分支从生产提交创建后，应审查并带入已发布的文档
更新，避免把新接手规范再次丢掉。`main` 是代码同步线，不是“当前已部署”指针。

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

目标授权在当前任务链内有效，无需反复确认已经明确的仓库和分支。本项目在
2026-09-09 已确认 `fluter1604191276/sub2api:main`，仍需核对 URL 和普通 fast-forward。
GitHub HTTP/2 framing 错误可用单次 `git -c http.version=HTTP/1.1 ...` 重试；先收齐
命令退出码/后台 session 结果，空输出或超时不表示分支不存在。最终用 `ls-remote`
核对 SHA；拒绝覆盖时先检查历史，不能 force push。

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

每次切换完成后同时留下持久源码保留点（精确 revision 的 detached worktree 或
可恢复的 Git bundle）、原始 manifest、镜像身份与前一版恢复点。源码副本与 manifest
互相核验；后续文档提交不得重写已发布 manifest 的 revision/snapshot。镜像 `Image`
是本机内容 ID，registry `RepoDigest` 是仓库 manifest 摘要，二者分别记录，不能互换。

## 接手完成标准

新会话应能独立回答：现在运行在哪台节点、如何验证身份、运行哪个提交、从哪里
派生开发、哪些能力必须保留、如何找到备份与回滚镜像。只报告已核验的事实及日期；
列出本地缺少的凭证/工具时要说明它影响哪一步，不把本机 Docker 缺失等同于无法
远端运维。执行一次短超时只读接手检查即可，不为纯文档更新做收费探针或服务切换。

这份文档不是实时状态面板。每次新会话开始都必须重新查询生产；完成生产切换后，
更新 `RELEASE-LINES.md` 和 `RELEASE-BASELINE.md` 的当前事实，并保留旧值作为
历史发布记录。
