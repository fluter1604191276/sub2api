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
Upstream version/base commit: v0.1.149 lineage, current custom release branch
Tests/fixtures: upstream-rates Python tests and sanitization fixtures。
Image smoke evidence: not part of runtime image; release verifier confirms ops files are excluded by .dockerignore。
First release manifest: pending candidate build
Rollback note: 台账工具可独立回退；不得用旧/partial 快照替代实时证据。
Owner/status: fluter / ready for candidate build
```
