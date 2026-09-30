# 双端工作与发布交接

这份文档约定 Mac mini（本机）和 MacBook Pro 如何共同维护
`fluter1604191276/sub2api`。它解决的是代码与事实的交接问题，不保存任何密钥、
Cookie、数据库密码或完整敏感配置。

## 一、工作区结构

本机的 `/Users/fluter_claw/Documents/study_project/sub2api` 是项目容器，不是
Git 根目录。不要因为当前终端位于容器目录，就把容器当作可构建源码。

| 位置 | 作用 | 是否可直接开发/构建 |
| --- | --- | --- |
| `project/` | 实际 Git checkout，包含 `backend/`、`frontend/`、`deploy/`、Dockerfile 和官方文档 | 当前 `main` 有未提交及未跟踪改动，未审阅前不可作为发布源 |
| `project/.git` | Git common directory 和 worktree 管理信息 | 不手工复制、移动或修改 |
| `.worktrees/ops-handoff-20260909/` | 持久运维文档 worktree，保存本交接协议、发布流程和二开清单 | 可维护文档；不代表当前生产代码 |
| `.worktrees/production-*` | 精确生产源码保留点，用于追溯或回滚 | 只核对/恢复，不在其中继续开发 |
| `.worktrees/<candidate>` | 从实时生产 revision 派生的候选开发线 | 通过创建脚本生成后才可开发 |
| `.release-evidence/` | 本地脱敏 release manifest、测试和镜像证据 | 保存证据；不是源码输入 |
| `.omx/`、`.omc/`、`.playwright-cli/` | Codex/OMX 会话状态、日志和浏览器自动化状态 | 不作为发布输入，不跨设备复制 |
| `output/`、`outputs/`、`.dev/` | 截图、演示、临时构建和诊断产物 | 仅按证据需要保留，不用于确定生产基线 |

真正的生产运行目录在 VPS 的 `/www/sub2api`，不是本机任何 worktree。生产节点、
镜像、revision、digest 和角色必须以实时查询为准；历史文档中的日期、tag、目录名
都只是线索。

## 二、两台 Mac 的协作边界

1. 两台设备各自拥有独立 clone/checkout、独立 `.git` 和独立 worktree。不要共享
   本机路径、不要把整个 `.git` 目录通过网盘复制、不要把生产数据库或 `.env` 在两台
   设备之间同步。
2. 代码只通过 Git commit、受控远程分支和脱敏 release evidence 交接。不要用 Finder
   拷贝一个“看起来最新”的目录作为交接方式。
3. `fluter` remote 指向发布仓库
   `https://github.com/fluter1604191276/sub2api.git`；`origin` 通常指向官方上游
   `https://github.com/Wei-Shaw/sub2api.git`。每台设备开工都要执行 `git remote -v`
   核对，不能只看 remote 名字。
4. `main` 是代码同步线，不是当前生产指针。不要在两个设备上同时直接修改 `main`，
   也不要把 `main`、版本 tag 或旧 worktree 当成生产基线。业务开发使用有主题和日期的
   分支，例如 `feature/<topic>-20261001-macmini` 或
   `feature/<topic>-20261001-macbook`。
5. 同一个发布分支同一时间只由一台设备负责写入。另一台设备先 `fetch`，确认 commit
   后再继续；禁止 force push。若两端各有未提交改动，先分别保存为可审阅 commit，
   再由一台设备整合并测试。
6. SSH 私钥不复制、不读取、不写进文档。每台设备只需自行配置同名 alias
   `fluterapi-prod`，并单独验证主机、端口、用户、host key 和生产角色。

MacBook Pro 的历史项目路径曾是 `/Users/Admin/Documents/study_project/sub2api`，
但这不是固定约束；新设备以自身 `pwd`、`git rev-parse --show-toplevel` 和
`git worktree list` 的结果为准。

## 三、每台设备的开工检查

下面的检查只读，不会重启服务或写生产数据。将 `<checkout>` 替换为该设备上实际的
Git checkout：

```bash
cd <checkout>
git status --short --branch
git rev-parse --show-toplevel
git rev-parse HEAD
git remote -v
git worktree list --porcelain
sed -n '1,220p' ops/public-deploy/docs/NEW-SESSION-HANDOFF.md
sed -n '1,220p' ops/public-deploy/docs/RELEASE-LINES.md
sed -n '1,240p' ops/public-deploy/docs/RELEASE-BASELINE.md
sed -n '1,260p' ops/public-deploy/docs/PRODUCTION-EXTENSIONS.md
```

需要判断生产现状时，再执行以下只读检查，并要求角色精确返回
`production`：

```bash
ssh -G fluterapi-prod | awk '$1 ~ /^(hostname|port|user|identityfile|identitiesonly|proxyjump)$/ {print}'
ssh -o BatchMode=yes -o ConnectTimeout=8 fluterapi-prod 'cat /etc/fluterapi-node-role; hostname'
ssh -o BatchMode=yes -o ConnectTimeout=8 fluterapi-prod \
  'docker inspect sub2api --format "{{.Config.Image}}|{{.Image}}|{{index .Config.Labels \"org.opencontainers.image.revision\"}}|{{index .Config.Labels \"org.opencontainers.image.source-snapshot\"}}"'
ssh -o BatchMode=yes -o ConnectTimeout=8 fluterapi-prod \
  'cd /www/sub2api && docker compose ps'
```

如果 alias 不存在，按 `fluter-sub2api-public-ops` skill 的已知配置发现流程检查现有
`~/.ssh/config` 和 Include；不要扫描 IP 段、猜新节点、关闭 host key 校验或把
DNS/SSH 失败直接解释成服务故障。缺少 SSH 凭证只阻断远端操作，不阻断本地阅读和测试。

## 四、正确开始一轮二开

二开必须从**实时生产容器的 Git revision**派生，而不是从 `main`、最新 tag、旧
生产快照或另一台 Mac 的工作目录派生：

```bash
cd <已核验的 Git checkout>
git fetch fluter --prune
ops/public-deploy/create-production-derived-worktree.sh <name> <branch>
cd <脚本输出的实际 worktree>
ops/public-deploy/check-production-baseline.sh
```

脚本输出的路径才是后续开发路径。不要手工猜 `.worktrees` 的归属；Git common dir
可能位于 `project/.worktrees/`，也可能位于容器之外。若脚本不存在或基线检查失败，
先读交接文档并修复 Git/生产事实，不要绕过检查从历史线构建。

每项二开都要同时登记在：

- `ops/public-deploy/docs/PRODUCTION-EXTENSIONS.md`
- `ops/public-deploy/docs/extensions/YYYYMMDD-<name>.md`

登记要覆盖源码、路由或后台任务、数据/配置影响、计费/调度/协议影响、测试、镜像
smoke 和回滚方式。智能调度、智能探针、账号质量/缓存命中率、模型同步筛选、渠道
定价、可用渠道/模型广场和 Responses 能力不能只靠“容器 healthy”证明存在。

## 五、双端交接包

一台设备把工作交给另一台设备时，必须交接一个已提交且可定位的 commit。交接消息
至少包含以下内容：

```text
设备：Mac mini | MacBook Pro
工作分支：<branch>
工作树：<absolute path>
当前 commit：<sha>
派生自生产 revision：<sha>
改动范围：<short summary>
二开登记：<extension record paths>
测试证据：<commands and pass/fail>
工作区状态：clean | dirty（若 dirty，列出路径和原因）
候选镜像/manifest：<none or exact paths>
生产操作：未执行 | 仅核验 | 已授权切换（附备份和回滚对象）
下一步：<one concrete action>
```

交接前执行：

```bash
git status --short --branch
git diff --check
git show --stat --oneline HEAD
git log -1 --format='%H%n%s'
```

若需要另一台设备继续开发，先把分支推到用户已确认的远程，再在另一台设备上
`git fetch fluter`、核对 commit 和 `git switch --track`。不要把未审阅的脏目录、
本地数据库、浏览器状态、`node_modules`、备份或凭证作为交接内容。

## 六、候选构建与生产切换边界

标准顺序固定为：

```text
实时生产核验
  -> 从生产 revision 创建 worktree
  -> 开发、测试、登记二开
  -> clean commit
  -> 推送并核对远端 SHA
  -> 构建 linux/amd64 候选镜像
  -> 生成并验证 secret-free manifest
  -> 保存数据库/Compose/env/当前镜像备份
  -> 获得切换授权
  -> 仅更新 sub2api 应用服务
  -> 能力级 smoke test
  -> 保存精确生产源码、manifest、镜像 digest 和回滚对象
  -> 更新 RELEASE-LINES.md 与 RELEASE-BASELINE.md
```

文档提交可以单独维护，不等于镜像已上线。一次发布的完整身份必须同时包含：

```text
branch + Git commit + source snapshot hash + image digest + release manifest
```

镜像 tag、GitHub `main`、生产容器 healthy 或 `/health` 200 都不能单独证明二开完整。
生产切换只使用发布流程规定的应用服务更新；保留旧镜像和回滚 Compose，失败时按
manifest 恢复，不删除失败镜像或历史证据。

## 七、当前本机特别注意事项

截至本次交接核对：

- 当前容器的 `project/main` 有账号管理、缓存命中率、模型同步等未提交/未跟踪改动，
  不能直接构建、推送或切换生产。
- 本机存在多个历史生产快照、候选 worktree 和发布证据；目录名与日期不能替代
  revision/digest/manifest 核对。
- `fluter` 与 `origin` 的语义不同；发布仓库不是官方上游，推送前必须确认 URL 和
  分支，且只允许普通 fast-forward push。
- 本文档和其他交接文档不会自动刷新生产状态。每次新会话、每次生产切换前都要重新
  查询生产容器标签、digest、revision、source snapshot、角色和备份。
