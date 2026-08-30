# 二开登记：上游倍率台账维护

```text
Capability ID: upstream-ledger
Business purpose: 维护上游倍率、真实成本、网页搜索费和账号映射的只读审计工具；KBQ 专用逻辑已退出生产请求热路径。
Backend/frontend files: ops/public-deploy/upstream-rates/*.py; ops/public-deploy/scripts/*
Routes or jobs: upstream ledger refresh/preview scripts；不新增生产 API 路由。
Database migration/data dependency: local SQLite snapshots and production read-only exports; local artifacts are ignored。
Environment-variable/config dependency: operator-supplied upstream credentials only via environment, never repository files。
Billing impact: internal cost audit
Scheduling impact: account selection planning only
Client protocol impact: none
First release upstream version/base commit: v0.1.171
First release official base commit: f0e7a9c7a23a7d02fb159b62fa809621eb0475a6
First release custom source branch: release/v0.1.171-fluter-full-custom-20260829
Current candidate upstream version/base commit: v0.1.183 / e8cb019fabf8b55199436229044cbf9aa7a82564
Current candidate custom source branch: release/v0.1.183-fluter-full-custom-20260830
Tests/fixtures: upstream-rates Python tests and sanitization fixtures。
Image smoke evidence: not part of runtime image; release verifier confirms ops files are excluded by .dockerignore and records the source capability。
First release manifest: sub2api-release-20260829-r5.json
Rollback note: 台账工具可独立回退；不得用旧/partial 快照替代实时证据。
Owner/status: fluter / ready for candidate build
```
