# 二开登记：容器内镜像能力检查

```text
Capability ID: ops-baseline
Business purpose: 让发布门禁在本机 arm64 Docker Desktop 检查 linux/amd64 候选镜像时保持可用，避免 docker cp 提取临时容器文件卡住而无法验收。
Backend/frontend files: ops/public-deploy/verify-release-bundle.py; ops/public-deploy/test_release_bundle.py; ops/public-deploy/docs/PRODUCTION-EXTENSIONS.md
Routes or jobs: 发布前本地镜像 smoke；不新增运行时路由或定时任务。
Database migration/data dependency: none
Environment-variable/config dependency: Docker CLI with linux/amd64 execution support；候选镜像内提供 /bin/sh、strings、grep。
Billing impact: none
Scheduling impact: none
Client protocol impact: none
Tests/fixtures: release bundle unit tests 20/20；容器内 amd64 marker smoke；Python compile；git diff --check。
Image smoke evidence: verifier 在候选容器内读取 /app/sub2api 的稳定能力标记，不再通过 docker cp 将二进制复制到宿主机；命令设置 120 秒超时。
First release manifest: sub2api-20260905-generic-400-failover-r1-release-manifest.json
Rollback note: 只回退发布工具对应 Git 提交；不涉及生产运行时和生产数据库。
Owner/status: fluter / ready for candidate build
```

本登记不包含密钥、Cookie、Bearer token、数据库密码、报告或原始流水。
