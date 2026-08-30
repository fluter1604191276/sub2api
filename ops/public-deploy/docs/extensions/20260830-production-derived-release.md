# 二开登记：从实时生产版本派生与最终镜像能力门禁

```text
Capability ID: ops-baseline
Business purpose: 每次二开从正在运行的生产镜像 revision 派生，并验证最终镜像二进制确实包含登记的关键能力，避免工作树、镜像标签和实际运行内容分叉。
Backend/frontend files: ops/public-deploy/check-production-baseline.sh; ops/public-deploy/build-production-image.sh; ops/public-deploy/verify-release-bundle.py; ops/public-deploy/test_release_bundle.py
Routes or jobs: 发布前实时生产 revision 检查；最终镜像编译能力 smoke。
Database migration/data dependency: none
Billing impact: none
Scheduling impact: protects scheduler and probe extensions from omission
Client protocol impact: verifies Responses tool markers; does not claim unsupported bridge capability
Tests/fixtures: release manifest unit tests; binary marker fixture; shell syntax check
First release manifest: 20260830 production rollback review
Rollback note: keep the current production image digest and rollback backup before each application-only switch
Owner/status: fluter / ready for next development line
```

The live production query is mandatory. The readable image tag, a stale
version-named worktree, source-only tests, and `/health` alone are not release
evidence. This record contains no keys, cookies, bearer tokens, passwords, or
raw request bodies.
