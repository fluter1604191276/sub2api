# v0.2.13 candidate compatibility and auth projection repair

Capability IDs: official-0213-compatibility, scheduler, smart-probe-modes, public-catalog-contract.
Target: official v0.2.13 (3040209f205472038c1ba745a1bedd2edd9053b1). Candidate only; not a production release.

The real stream acceptance exposed omitted group fields in GetByKeyForAuth. Restore operational group projection, entity mapping and Redis snapshot round trips; bump snapshot version to 26 to reject stale v25 data. Migration 243 expands durable group-change invalidation without changing stored defaults, pricing or scheduling algorithms. Normal admin group updates retain immediate cache invalidation.

Source: api_key_repo.go, api_key_auth_cache.go, api_key_auth_cache_impl.go and 243_group_auth_operational_fields_invalidation.sql.
Regression: internal/authintegration/auth_group_test.go uses actual PostgreSQL/Redis and APIKeyService.GetByKey, plus deployment stream tests. Runtime evidence lives outside committed sources; unverified capabilities remain partial or failed in the release manifest.

Raw Chat Completions now buffers nonsemantic SSE preambles when stream escape is enabled, so role-only EOF can retry before committing client headers. Semantic content, terminal frames and protocol errors release the preamble; disabled escape retains existing behavior. Unit regressions cover role-only truncation and normal semantic/terminal output. The Docker health check uses IPv4 loopback to match isolated IPv4 listeners. Evidence and synthetic backups are excluded from Git source snapshots and Docker contexts.

Round7 discovered a runtime panic in the existing amd64 emulation environment. A standalone Ent SQL builder reproducer also fails there, while the identical 320000-query arm64 run passes. Disabling inlining or asynchronous preemption did not resolve it. No compiler or preemption workaround was adopted. Round8 ran the same amd64 reproducer and final candidate on native x86_64 under resource limits: 320000 queries, six stream scenarios / 41 unchanged assertions, real auth, billing, migration/recovery and a 180-second healthy soak passed. Historical emulation failures remain evidence; production load and long-duration stability are still unverified.

Release tooling now explicitly requires v0.2.13 and the 22-item capability matrix, including public-catalog-contract. Prior 0.2.4 release records remain historical evidence.

Billing impact: no new tariff or default; existing configured fast/free, reasoning access control and batch multipliers now survive auth materialization. Scheduling impact: existing configured smart/escape and group policies reach their consumers. This can change behavior where a configured setting was previously silently ignored; validate before release.

Rollback: restore a coordinated database/config/image recovery point. v26 ignores old auth snapshots; future rollback must likewise prevent incompatible cached snapshots. Migration 243 changes trigger behavior only; previous upgrade migrations may still be destructive. No production approval is granted by this record.

SheetJS security follow-up: replace npm xlsx0.18.5 with the official CE0.20.3 tarball https://cdn.sheetjs.com/xlsx-0.20.3/xlsx-0.20.3.tgz (SHA256 8dc73fc3b00203e72d176e85b50938627c7b086e607c682e8d3c22c02bb99fe8). Pin SHA512 integrity in pnpm-lock.yaml; keep Apache-2.0 licensing and the UsageView dynamic export API. Ignore dependency installation scripts in the Docker frontend stage. Remove only the two repaired xlsx audit exceptions, with no expiry extension. Actual dependency source comparison and real workbook round trips support the fix; a clean npm audit alone is insufficient for a non-registry tarball. Official advisories: https://cdn.sheetjs.com/advisories/CVE-2023-30533 and https://cdn.sheetjs.com/advisories/CVE-2024-22363. npm remains on0.18.5; the official CDN is the maintained distribution. Grafana's merged PR86738 provides a primary-source example of the same migration (https://github.com/grafana/grafana/pull/86738/files).

Monitoring follow-up preserves the real SSRF transport and endpoint restrictions. New transport regression rejects private/metadata targets without replacing the client or resolver. Synthetic monitoring acceptance is split into V1 management/security rejection and V2 passive usage aggregation, with exact scope in the evidence manifest; no localhost bypass is introduced.
