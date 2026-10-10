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

The compatible scanner identified 15 x/net HTTP/2 deprecations. Upstream transport now uses public net/http Protocols and HTTP2Config with the same PING intervals, and preserves existing configuration objects. A loopback regression checks two simultaneous streams, cancellation and connection reuse. Manifest GOAWAY retains retry classification through the existing stable error prefix, including wrapped errors and cancellation precedence. This selectively adapts reviewed transport work from2635c91 without importing its candidate chain, pricing or unrelated dependencies.

Six ingress h2c deprecations remain a release BLOCK. The configured HTTP/1 and HTTP/2 idle timeouts can differ; public HTTP2Config has no independent server idle timeout. Replacing the adapter would silently change one timeout/security behavior, while copying an internal adapter or suppressing staticcheck would hide the gap. Keep the existing ingress code and all checks pending a separately reviewed compatibility design; this repair does not authorize that semantic change. A failed lint gate cannot be described as passed even if all other CI jobs succeed.

## Auth projection repair and financial behavior risk

Full real integration on b6b2e13 reached and passed the recovery-probe repository tests but exposed two existing auth projection failures. The SQL/Ent auth query already selects codex_models_manifest_config, force_openai_fast and free_openai_fast; its service.Group mapper dropped all three. Copy those existing stored values into the auth snapshot, retaining both real integration assertions. No schema, configured group value, price formula, settlement code or historical financial row changes.

This is a development source fix with an explicit deployment risk: currently true stored fast force/free values will become effective rather than silently defaulting to false. This shared mapper is also used by group repository Get/List, so review all group read callers, not only the auth path. Force can change selected upstream tier; free can change how configured priority costs are charged. A future release review must inspect affected live groups read-only and separately approve the resulting financial behavior before rollout. Do not backfill, replay, rebill or invoke real upstream requests to validate this change. Codex fixed-account routing also follows its saved configuration. These changes do not authorize production activation or extend release exceptions.

Disabling the usage journal does not isolate this shared-mapper financial behavior. It requires its own release decision regardless of the journal activation flag.

## Noncalled module records

The patched-source govulncheck scan is zero called/zero imported-package findings but initially retained seven required-module-only records. Official minimum fixes x/image0.45.0 and klauspost/compress1.18.7 close six of these (GO-2026-6222/5066/5062/5061/4961 and GO-2026-5841). Use normal Go module resolution and existing SDK, without scan suppression. Remaining GO-2026-5932 concerns unmaintained x/crypto/openpgp with no patch; that package is not imported or called by this source. Preserve the module record and verify the actual final binary separately; source success does not mean zero module findings or approve plugins/runtime use.


## Follow-up: preserve r4 financial flags and narrow HTTP/2 compatibility

The follow-up removes only `ForceOpenAIFast` and `FreeOpenAIFast` mappings added by
1c0f88a. r4 selected these columns but did not materialize them; restoring them
changed cold authentication and shared group reads. The Codex manifest mapping
remains. No billing formula, stored flag, historical record or schema is changed.
The original `TestGetByKeyForAuthCarriesGroupForceOpenAIFast` True assertions stay
unchanged and are expected to fail: its desired contract conflicts with the r4
financial baseline. This is an explicit release blocker, not an accepted waiver.
A new financial activation gate or accepting changed financial behavior is outside
this minimal repair and must not be silently introduced to make CI green.

Four H2 limits move to public `http.Server.HTTP2`; legacy integer conversions
are retained. `ConfigureServer` and legacy `IdleTimeout` remain unsuppressed:
Go1.27.2 public HTTP2Config cannot preserve separate H1/H2 server idle timeouts.
The legacy bridge still registers native graceful shutdown. No new listener
multiplexer, timer, private adapter, nolint or relaxed CI scope is introduced.
Real loopback tests assert wire SETTINGS/windows, independent idle timeouts in
both directions, active streams, graceful Shutdown, Close cancellation and H2C-off.

braces3.0.3 HIGH GHSA-vfj7-8cjw-p6xm remains in eight dev-only dependency paths
from useful TS ESLint7.18 and Tailwind3.4.19 roots. Official advisory has no patch.
Removing those tools, substituting brace-expansion, or forcing chokidar4 changes
contracts and does not safely eliminate all paths. No major upgrade, local fork,
audit exception or deadline extension is included in this repair. Release remains
NO-GO and allow_release=false; production and real paid upstream are untouched.
