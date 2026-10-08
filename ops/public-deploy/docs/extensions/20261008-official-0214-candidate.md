# Official v0.2.14 candidate integration (2026-10-08)

Capability IDs: official-0214-compatibility, smart-probe-modes, channel-monitor-budget, account-model-sync-preview, passive-capability-observation, ops-baseline.

Business purpose: retain the complete production-derived customization line while merging official `v0.2.14`, exact upstream commit `0363b8cdba8cec3e2ba4b2dbd49c4481143fa55d`, onto security candidate `9acf41b5a0f60522a28f0b5b76a83f996f4b373d`. The business remains independent CloudCat voucher sale and Sub2 voucher redemption. No external payment integration or affiliate operation is introduced by this task.

Upstream application changes: stronger new-install administrator credential validation, EasyPay notification signature/status hardening, payment resume guard, and Codex API-key model discovery configuration. Existing administrator credentials are not changed. The tag's tracked VERSION file is 0.2.13; this candidate records 0.2.14 explicitly so untagged builds do not silently advertise the previous release.

Security dependency contract: keep SheetJS official CDN 0.20.3 and its original SHA512 integrity; reject upstream npm xlsx 0.18.5 and its two audit exceptions. Vue family moves from 3.5.42 to pinned 3.5.43; source-map-js remains pinned 1.2.2. The existing bounded package security regressions check the updated exact family versions. gRPC remains 1.83.2. No checks are disabled and no new exceptions are added.

Backend files: `internal/repository/group_repo.go` normalizes recovery probe config for Create, shared transactional create/duplicate helper, and Update using the same service function as administrator writes. Zero attempts remains the documented omitted-value default (1, or 5 for high-frequency mode); negative and >5 attempts fail before writes. All other config defaults and validation follow the same contract. Caller state changes only after a successful save. Legacy GroupService and direct repository callers are covered; Ent-only and direct SQL callers retain generated/schema defaults and database constraints. No migration is edited, added, or executed in production. Tests cover ordinary Create, transactional helper, Update, high-frequency defaults, rejected values, unchanged persisted state, and direct SQL constraint rejection.

Go lint cleanup checks previously ignored SQL close and rollback errors, preserves original and rollback errors, handles an optional budget ledger assertion without panic, returns a sanitized response-close error, and removes unused wrappers/obsolete implementations. Unit-tag tests formerly calling wrappers call the equivalent active implementation with the same policy; tests are retained. The live billing formulas, scheduler policy and budget ownership are unchanged.

Routes/jobs: all existing customization routes and tasks retained. Only official setup/payment/model-discovery behavior is integrated. Database dependency: existing migration 196 and subsequent high-frequency recovery probe migration; no new custom migration. Billing impact: error propagation and persistence defaults, no price or settlement formula change. Scheduling impact: omitted recovery-probe config gets the established validated defaults, no enablement change. Client protocol impact: official Codex configuration enables API-key model discovery; streaming/Responses compatibility limits remain explicit.

Tests/fixtures: full default/unit/integration Go suites, golangci-lint v2.13, govulncheck, full frontend suite/typecheck/lint/build, SheetJS and bounded Vue/source-map regressions, deployment shell and release helper tests, existing synthetic protocol/extension checks. Actual outcomes, failures and unrun items are recorded outside source under `.release-evidence/20261008-official-0214/`; this document does not declare them passed.

First release manifest: candidate only, generated after exact clean commit/build when available. Production remains r4; release permission remains false until complete evidence and a separate production-switch instruction exist. No GitHub push, PR or production mutation is authorized here.

Rollback: before any future authorized switch, retain exact r4 image `sha256:4d4cc10e1720c3f789129c5ec3f4d122bd8f46904684e0c5a21e62d99fdad8c1` and production revision `06d2674d660a65abb8f2bb0a63b6e032eb35c937`, use the coordinated backup/transaction-preserving recovery policy. Do not restore an old dump over new transactions. This task performs no switch or rollback.

Owner/status: local candidate development, verification pending, not deployed.

The active official-compatibility gate is advanced to v0.2.14 in both manifest generation and verification, retaining historical records; the expected binary and tracked version are 0.2.14. This replaces the prior active 0.2.13 version check without changing the other 21 required capabilities.

Write-path review also closes the group duplicate configuration gap: recovery probe, pool/custom-error, long-context pricing gate and model pricing are copied with independent pointer/map/slice storage. The duplicate stays inactive, and the recovery job SQL requires an active group. Configuration copying therefore does not start probe traffic. Existing runtime identities, statistics, membership and the intentionally reset Codex pinned-account manifest are not cloned.

The previously blocked group reasoning-pricing integration assertion now reaches actual billing. Its expected value is corrected to preserve the established Fable max=3 model default after clearing or omitting the override; an explicit max=1 case proves opt-out. No billing formula is modified.
