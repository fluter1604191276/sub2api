# Journal r4 baseline security and persistence regression repair

This is an independent development repair on `feature/usage-journal-activation-r4`, based on r4 `06d2674d660a65abb8f2bb0a63b6e032eb35c937` and journal source `f39079250fc8ec4be9224b369db1f82b8b3c281e`. It does not restore the canceled production switch or replace the original e0bf1fee candidate or later canceled612812f image.

## Source and runtime contract

- Upgrade Go/Docker/workflow verification together to1.27.2, x/net0.60.0 and grpc1.83.2. Their required module minimums follow normal Go MVS; x/tools0.50.0 is required for patched toolchain importer compatibility. Standard startup/migrations and journal feature flag semantics are unchanged.
- Pin axios1.20.0, Vue3.5.43, source-map-js1.2.2 and the official SheetJS0.20.3 tarball with pnpm integrity. Remove the two superseded xlsx exceptions; no exception renewal or audit severity reduction. Upgrade vulnerable development dependencies through supported releases, preserve coverage thresholds while adapting Vitest4 configuration, and retain full audit evidence. Braces GHSA-vfj7-8cjw-p6xm has no official patched release and remains an unresolved development risk.
- Group repository Create/shared clone and Update reuse the existing complete recovery-probe normalizer. Zero means the existing mode-specific defaults, disabled stays disabled, and explicitly invalid values fail before SQL. Successful saves synchronize the caller object. Existing schema constraints196/231 remain untouched. No live group writes or probe requests are made by this repair.
- Address inherited lint without reducing scanner scope: cleanup errors are intentionally discarded only after completion; normal row errors remain fail closed. Missing budget settlement interfaces preserve reservations. Production smart-sticky WithPolicy and unified pricing stay active; thin default-policy adapters reside in unit tests, and pricing assertions exercise actual runtime helpers. Unreferenced private definitions are removed.

## Validation and release boundaries

Actual PostgreSQL tests cover Create/Update defaults for disabled/enabled smart and high-frequency modes, explicit values, zero reset, invalid config without rows/outbox writes and direct original CHECK enforcement. Existing transactional clone/outbox rollback tests remain. Test configuration enables no real probe runner/upstream, financial replay, rebilling, or production migration.

Run new exact-SHA CI unit/integration/lint/frontend and Security, plus installed-package source-map/Vue regressions and real SheetJS export/parser contracts. Use existing MacBook patched Go runtime for local verification. The repaired source requires its own clean commit, source manifest and new linux/amd64 digest; old candidates' CI/image results are not inherited. Native same-digest acceptance is separate.

Rollback is binary/source selection under the existing journal compatibility plan. No destructive migration, permission, networking, pricing or production deployment is authorized. Production remains NO-GO/allow_release=false until all necessary gates and separately authorized release conditions are satisfied.

The first patched-Go CI found golangci-lint2.13.2 unable to decode Go1.27.2 export-data version5 (maximum4). Pin the official2.14.0 release, which carries x/tools0.50.0, without changing lint rules, source exclusions or the patched Go version. The scanner compatibility change requires a new exact-SHA CI run; b6b2e13 Security success is retained as prior evidence only.
