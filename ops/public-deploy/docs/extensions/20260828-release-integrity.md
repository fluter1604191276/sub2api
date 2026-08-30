# 二开登记：发布完整性与能力证据门禁

```text
Capability ID: ops-baseline
Business purpose: 把生产镜像、源码快照、二开能力、测试证据和回滚身份绑定，避免健康检查通过但二开缺失。
Backend/frontend files: ops/public-deploy/build-production-image.sh; ops/public-deploy/generate-release-manifest.py; ops/public-deploy/verify-release-bundle.py; ops/public-deploy/test_release_bundle.py
Routes or jobs: 发布前本地门禁；不新增运行时路由或定时任务。
Database migration/data dependency: none
Environment-variable/config dependency: Docker CLI；镜像 OCI labels。
Billing impact: none
Scheduling impact: none
Client protocol impact: none
First release upstream version/base commit: v0.1.171
First release official base commit: f0e7a9c7a23a7d02fb159b62fa809621eb0475a6
First release custom source branch: release/v0.1.171-fluter-full-custom-20260829
Current candidate upstream version/base commit: v0.1.183 / e8cb019fabf8b55199436229044cbf9aa7a82564
Current candidate custom source branch: release/v0.1.183-fluter-full-custom-20260830
Tests/fixtures: release manifest unit tests 10/10；Python compile；shell syntax；git diff --check。
Image smoke evidence: 候选镜像必须携带 revision/source-snapshot 标签，最终 smoke 结果记录在发布 manifest。
First release manifest: sub2api-release-20260829-r5.json
Rollback note: 保留旧生产 digest、Compose 和备份；失败时只恢复已记录的应用镜像对。
Owner/status: fluter / ready for candidate build
```

本登记不包含密钥、Cookie、Bearer token、数据库密码、报告或原始流水。
