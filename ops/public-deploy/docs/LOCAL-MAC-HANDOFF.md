# MacBook Pro 临时接手生产工作

核验日期：2026-10-01，Asia/Shanghai。此次建立本机入口并只读核验生产，没有镜像构建、
服务切换、数据库/配置写入、收费探针或 GitHub 推送。下一项任务重新查询生产。

## 本机工作区

项目容器：`/Users/Admin/Documents/study_project/sub2api`，不是 Git 根目录。

| 位置（相对容器） | 用途 |
| --- | --- |
| `project/` | 独立 clone，Git common dir 是本机的 `project/.git` |
| `project/.worktrees/ops-handoff-20261001/` | 文档分支 `docs/local-handoff-20261001-macbook` |
| `.worktrees/production-06d2674d6/` | 精确生产源码，detached、locked，仅核对和恢复 |
| `.release-evidence/20260923-stream-sticky-escape/release-manifest-r4.json` | 原始脱敏 manifest，字节不变 |
| `.release-evidence/20261001-local-handoff/` | 本轮核验结果 |
| `macmini-public-deploy/` | 原有旧 Python 快照，保留原样，不作为发布源 |

文档工作树先从实时生产提交创建，再带入主 Mac 的文档后继提交
`02a1a67770e5f72962e32cb379f3d13ac362c54a` 和交接协议提交
`1d84e53e4a5dc5ec5f4461668f68a44900b866dd`。当前文档 SHA 用 `git rev-parse HEAD`
核验；它不等于生产 revision。

## Git 与 SSH

| remote | URL | 职责 |
| --- | --- | --- |
| `fluter` | `https://github.com/fluter1604191276/sub2api.git` | 发布仓库 |
| `origin` | `https://github.com/Wei-Shaw/sub2api.git` | 官方上游 |
| `macmini-handoff` | `ssh://mac-mini/Users/fluter_claw/Documents/study_project/sub2api/project` | 只读获取已提交分支，push URL 禁用 |

本轮通过 Git/SSH 获取提交，没有复制主 Mac 的工作目录、`.git`、未提交改动、数据库、
私钥或浏览器状态。主 Mac 的脏 `project/main` 和历史工作树均保留原样。
本机生成的 `project/.worktrees/` 通过本机 `.git/info/exclude` 排除，不混入主 checkout
的源码提交；工作树清单仍由 Git 登记。两份本机 AGENTS 入口按项目忽略规则保留在本机。

`fluter/main` 当前是 `b641b032486eae4b57858bb9f144a3321aa36073`，属于 0.2.7 准备线，
与生产不同。GitHub 尚无 `docs/sub2api-handoff-20260909` 和
`feature/stream-sticky-escape-20260922` 分支。Mac mini 文档分支在 `1d84e53e4` 且干净；
流式修复分支在 `02a1a6777`，比生产提交多出的改动只更新三份发布文档。本机已保存相关
Git 历史；本轮未推送 GitHub。之后按具体任务授权推送 `fluter` 的明确分支，不自动更新 main。

本机 `fluterapi-prod` 已加入既有 `us-api-vps-new` host block：

```text
Endpoint: root@166.0.27.152:15729
Hostname: fluterapi-us-gport-primary-01
Role: /etc/fluterapi-node-role = production
Directory: /www/sub2api
IdentityFile: ~/.ssh/id_ed25519（本机已有身份；未读取或复制私钥）
SSH config backup: ~/.ssh/config.before-sub2api-handoff-20261001
Mac mini alias: mac-mini
```

只添加 alias，保留原有连接和 host key 校验设置。本机直连与经 Mac mini 核验的身份一致。

## 实时生产与原始证据

```text
Image: fluter/sub2api:fluter-0.2.4-stream-sticky-escape-20260923-r4
Image ID: sha256:4d4cc10e1720c3f789129c5ec3f4d122bd8f46904684e0c5a21e62d99fdad8c1
RepoDigests reported by Docker: fluter/sub2api@sha256:4d4cc10e1720c3f789129c5ec3f4d122bd8f46904684e0c5a21e62d99fdad8c1
Git revision: 06d2674d660a65abb8f2bb0a63b6e032eb35c937
Source snapshot: 96be23952093ee5181bb05e2b180f8b92de0c18acc646b9365379f5cdb13b0bc
Source branch in original manifest: feature/stream-sticky-escape-20260922
Architecture in original manifest: linux/amd64
Manifest SHA-256: 1948b5191d6d5e5543a6ea96db51e0d142ebe7192071ae8132afa007185259f3
State: sub2api / PostgreSQL / Redis running and healthy
```

这次镜像历史上经 SSH 导入生产，Docker Hub push 超时。RepoDigests 是本轮 Docker
元数据读数；本轮没有验证 registry 可下载此镜像，不承诺 `docker pull` 可用。
主 Mac 的旧 `ops-handoff-20260909` 文档基线停在 9 月 15 日；本机已带入流式修复分支
上的 9 月 23 日记录并重查生产，原始 manifest 未改写。

## 下一项任务入口

只读核验生产：

```bash
ssh -o BatchMode=yes -o ConnectTimeout=8 fluterapi-prod \
  'test "$(cat /etc/fluterapi-node-role)" = production && hostname && docker inspect sub2api --format "{{.Config.Image}}|{{.Image}}|{{index .Config.Labels \"org.opencontainers.image.revision\"}}|{{index .Config.Labels \"org.opencontainers.image.source-snapshot\"}}"'
ssh -o BatchMode=yes -o ConnectTimeout=8 fluterapi-prod \
  'cd /www/sub2api && docker compose ps'
```

下一轮业务二开必须从当时的实时 revision 派生。下面的 `<topic>`、`<date>` 是实际任务
名与日期，创建后进入脚本输出路径，不在精确生产保留点或 main 中开发：

```bash
cd /Users/Admin/Documents/study_project/sub2api/project/.worktrees/ops-handoff-20261001
git status --short --branch
git remote -v
git fetch fluter --prune
ops/public-deploy/create-production-derived-worktree.sh <topic>-<date>-macbook feature/<topic>-<date>-macbook
cd <脚本输出的实际工作树>
ops/public-deploy/check-production-baseline.sh
```

若实时 revision 在本机缺失，先从已核验的交接 Git 源获取提交，不猜历史版本。例如：

```bash
git fetch --no-tags macmini-handoff \
  refs/heads/docs/sub2api-handoff-20260909:refs/remotes/macmini-handoff/docs/sub2api-handoff-20260909 \
  refs/heads/feature/stream-sticky-escape-20260922:refs/remotes/macmini-handoff/feature/stream-sticky-escape-20260922
```

新分支带入需要的最新交接规范，保留完整二开清单。发布时重新执行测试、构建、manifest、
容量检查、备份与授权流程。本页记录不授予服务切换或生产数据写入权限。

## 备份与回滚

本轮仅定位和读取元数据，没有下载或解压数据库备份，没有触发备份或恢复：

```text
Rollback image: fluter/sub2api:fluter-0.2.4-account-groups-20260922-r1
Rollback image ID: sha256:941d399a182f57105851591b10768981ebb11c960bf9d0b96e1049f520b53da7
Rollback Git revision: 3b43110cedabce5c6bcb0966f5f082dae624e054
Rollback source snapshot: 6a031974e8b6dcd80cbab85dafe7cde2e179c42a5631967babdf2d0ad0ddc7fb
Rollback Compose on VPS: /www/sub2api/backups/docker-compose.pre-sticky-escape-20260923.yml
Rollback Compose SHA-256: f1ed4017a5b1f08dc308b3b072a375e22305352af3523714aa63852e9473817c
Historical pre-switch archive: /www/sub2api/backups/sub2api-backup-20260923T034236Z.tar.gz（已迁离）
Retained pre-switch archive on Mac mini: /Users/fluter_claw/Backups/fluterapi-sub2api/vps-archive-20260924T041013Z/sub2api-backup-20260923T034236Z.tar.gz
Latest full archive observed on VPS: /www/sub2api/backups/sub2api-backup-20260930T033122Z.tar.gz（3,091,801,392 bytes）
Latest observed Mac mini retention manifest: /Users/fluter_claw/Backups/fluterapi-sub2api/vps-archive-20260930T041009Z/manifest.json
That manifest records the 20260929T033231Z archive transferred and verified, SHA-256: 82bc6b0920f6e596c6161e15b99e788a20c67fec12bf285037693c0a388575e2
```

回滚镜像和 Compose 已在 VPS 实时确认；切换前归档在 Mac mini 确认了路径存在，未重读
3 GB 归档计算校验和或做恢复演练。保留任务会移动备份，恢复前重新定位。回滚应用与恢复
数据库分别评估，不自动用旧归档覆盖后续生产数据。

备份同步/保留继续由 Mac mini 现有 LaunchAgent 负责，本机不启动竞争任务或删除归档。
2026-09-23 曾因两个约 13 GiB 的手工未压缩 dump 占满 `/www` 并导致数据库故障；
不要重做无容量门禁的全量明文 dump。本轮 `/www` 使用 24%、根盘使用 89%，实际备份/
构建前核对目标文件系统与峰值所需空间。复盘仍在主 Mac 的
`.release-evidence/20260923-disk-full-incident/INCIDENT.md`。

## 验证范围与工具限制

本轮已核验：本机直连角色和标签、三项容器状态、回滚对象、本机精确源码 clean、
4,127 个文件的 snapshot 与原 manifest 一致，以及全部 22 项必需能力的源码登记。
已运行无镜像参数的 release bundle 核验和实时生产基线检查。

原 manifest 的 backend/frontend/diff/protocol/image smoke 为历史通过记录。本轮没有
重跑这些测试、构建镜像或做完整镜像 smoke；多数能力证据只保存名称，未附原始日志和
image capability markers。新发布需收集新证据。Responses 工具为 Partial，原生与桥接
路径的限制继续有效。
现有 gate 的 `public-catalog-contract` 强制项在 manifest 中存在，但库存矩阵没有独立行；
此为既有文档差异，不能据此删去该能力或绕过 gate。

本机已有 Git、SSH、gh、Node、pnpm、Python、Docker/Compose/buildx CLI。Docker 服务
未运行，Go 不在 PATH，frontend 依赖未安装；生产源 `backend/go.mod` 要求 Go 1.27.0。
这影响本机测试/构建。本机现已能阅读/编辑源码并经 SSH 处理生产只读运维；后续按实际
任务准备构建环境，不将本机构建环境缺失误判为无生产访问能力。
