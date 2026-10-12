# New Sub2 v0.2.15 / task 14 / runtime security review

Review-only branch. No release, image publication, production deployment,
production migration, paid provider request or historical billing replay is
included. `allow_release=false`; old release schedules remain cancelled.

## Fixed code and upstream baseline

The build and source tests target code commit
`c7b483fc7895be0745f8fa1a86f0fe3401f6c5e1`, source snapshot
`6be45832f73bf266681609ab5cdf91b389ab62f60c029862f8f54422a9dd972b`.
The review branch adds this document on top; its HEAD is a documentation-only
descendant. Do not treat that HEAD as the already-built image revision.
The source is production-derived r4 plus official Wei-Shaw/sub2api v0.2.15,
peeled commit `f2669c8cf62555cd92389b3f55920e9e6e7c6ff2`, then the local task14
change `e69afcc72cc7d34c894bbfda345164e00d29b019` and runtime patch c7.
Official v0.2.15 compatibility record and extension inventory are in this tree.

Local linux/amd64 image (not pushed to a registry):
- OCI index: `sha256:86c3beee7887221cb3d535402735258475e29ffb9ea5d926a97b8355babd9588`.
- Platform manifest: `sha256:c621d525f00a190d0b87f752439eb06ec1398d5b90bffab092785e6f62d52152`.
- Actual ELF: `3fe0947af11e4ba282d2d3ced0e8b967d821d9a424fe8aef8c08af52230bb75a`.
Build/archive exited 0. Archive blobs and layer diff IDs were independently
checked. The application reports 0.2.15 and c7; PG tools remain 18.6 and offline
linkage checks pass. This is a cross-compiled image inspected/emulated on ARM;
it is not native amd64 load/persistence acceptance.

## Task 14 contract to inspect closely

Admin-only selected-account action: preview each stored decimal multiplier,
current priority and proposed `ceil(rate*1000)` priority; exact decimal arithmetic,
0 becomes 1, 2.007 becomes 2007. Bad/missing/negative/out-of-range rows are skipped
with reasons while valid rows proceed. Equal priorities are permitted. Explicit
confirmation overwrites manual values including 0-9; cancellation writes nothing.
Default priority 50, billing multipliers and scheduling algorithms do not change.

Review service/repository/admin handler, the two preview/apply routes and the
Vue dialog. Confirm independent role guards, per-row old multiplier/priority
CAS, priority-only writes plus scheduler outbox in the same transaction, stale
preview behavior, mixed results, selected-ID snapshot and ambiguous-ACK handling.
No automatic retry of an uncertain apply response. There is no automatic
conversion, undo facility or new financial audit subsystem.

## Required customizations

The 23 current capabilities and their source/test contracts remain registered
in `PRODUCTION-EXTENSIONS.md` and the release verifier:

| Area | Capability IDs |
| --- | --- |
| Routing/scheduling | scheduler; error-passthrough; model-capability-failover; generic-400-failover; smart-probe-modes |
| Probe/monitor | scheduled-probe; v1-streaming-probe; channel-monitor-bulk-interval; channel-monitor-budget |
| Account/cost/model | quality-score; cache-hit-rate; image-cost; pricing-calibration; model-sync-filter; account-model-sync-preview; account-priority-conversion |
| Tools/catalog/UI | responses-tools; passive-capability-observation; catalog-surfaces; public-catalog-contract |
| Operations/upstream | upstream-ledger; ops-baseline; official-0215-compatibility |

All 197 declared path references exist; original r4 migration paths are retained.
All 42 verifier markers occur in the actual e69 and c7 binaries. These facts and
passing source tests are not full functional acceptance of every image route.
Operations scripts stay in the repository; their presence does not enable live
ledger maintenance or prove a current backup. Private workspace reports, image
archives, DB data, credentials and live production configuration are not supplied.

Inspect intentional upstream compatibility changes: dynamic platform catalogue
while retaining local group/model filters and pricing labels; reasoning-effort
map with legacy max-field conversion and explicit-null/one behavior; official
inline priority edit/credential-refresh response; custom Markdown race guards.
Existing homepage/Markdown/navigation, account cost and user charge separation,
pricing guards and passive capability UI are covered by source tests; live menu
content and full-page visual behavior still need acceptance. Responses bridges
retain route-specific tool limitations; do not claim universal terminal execution.
Abandoned journal, global tools/media/WebSocket 503 rejection and the paused
custom 500K pricing proposal are intentionally excluded. Do not restore them.

## Runtime/security review

c7 changes only the final Docker stage and two registration documents. Same
Alpine3.21; actual installed libcrypto3/libssl3 3.3.7-r2 and zlib1.3.2-r1 match
official patched APK payloads. Normal signed APK install is used without
allow-untrusted. Fresh Alpine3.21 main/community secdb and actual apk comparison
show zero fixed-version mismatch, closing the previous 11 unique component CVE
matches in this scope. Not a blanket absence-of-vulnerabilities claim.

braces3.0.3 still has HIGH GHSA-vfj7-8cjw-p6xm, no official fixed version. The
final image has no Node/braces paths; actual embedded JS scans and same-source
bundle graph found no braces dependency/function evidence. Exact graph matching
covered 77 actual chunks; 59 nonidentical chunks were not individually attributed.
A bounded local deep-pattern probe still raises RangeError. Risk mainly concerns
Node build/lint/watch accepting malicious patterns, including untrusted PR config.
Conditional deferral with trusted inputs/isolated credential-free CI is proposed;
no audit waiver, rule deletion, threshold change or local fork is included.
Review those boundaries before any formal release.

GO-2026-5932 remains in module/stripped-binary wildcard records for x/crypto.
Actual pclntab contains no OpenPGP function; source Linux/embed scan has no
imported/called affected package finding. Raw findings remain recorded; scanner
JSON exit 0 does not mean all findings were cleared.

## Verification and remaining questions

At exact c7 product source: Go1.27.2 full short unit run: 58 packages PASS,
22,903 pass test events including subtests, 18 conditional test skips and 52
empty/no-test package skips; 0 failures. Golangci-lint: 0 issues. Frontend:
382 files / 2,913 assertions PASS, typecheck/lint/build PASS; release helper:
30 tests PASS. Frontend JSON's nested suite count is not its file count.
Same product source previously passed four task14 real-PG synthetic scenarios
and two quota238 real-PG/Redis top-level tests with no skips; not all-schema
integration. Memory assertion still uses 8MiB and an isolated measured worker;
its deliberate retained-buffer negative control must fail the original assertion.
The older shared-run failure remains evidence, not silently reclassified as noise.

Review migration `238_purge_unlimited_user_platform_quotas.sql`: pending-only
policy B archives active rows with three NULL limits, retaining IDs/counters/
windows/timestamps except deleted_at. First future limit creates a fresh active
zero-count row; stale NULL Redis snapshots cannot refill it. If the official
DELETE form of that exact filename was already applied with another checksum,
fail closed; no checksum rewrite/exemption. Numeric prefix 238 alone is not an
identity. Full migration/restore and old-binary compatibility are pending; never
overwrite post-cutover transactions with an old full dump.

Native amd64 load/continuous persistence, full schema upgrade/restore, current
production identity/config/backup, actual provider/Antigravity behavior and
historical financial correctness remain unverified. Existing usage-log loss/
rollup lock mechanisms are not declared fixed by this upgrade. No real-account
or paid upstream probe and no replay/debit should be requested as an automatic
review step. New remote/resource/financial operations require their own scope.
Please prioritize actionable code findings with file/line and concrete failing
scenario, distinguishing source defects from still-unverified deployment facts.
