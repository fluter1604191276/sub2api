# Sub2API v0.2.7 Upgrade Preflight

Date: 2026-09-19 (Asia/Shanghai)

Status: preparation complete; merge, image build, database migration and
production switch have not started.

## Decision Summary

- Latest official stable release: [`v0.2.7`](https://github.com/Wei-Shaw/sub2api/releases/tag/v0.2.7), published at `2026-09-19T04:28:46Z`.
- There is no official `v0.2.6` release. The release path is `v0.2.4 -> v0.2.5 -> v0.2.7`.
- The upgrade is useful, especially for OpenAI WebSocket stability, Responses
  bridges, Kimi quota recovery, Codex-to-Anthropic tool schemas, Antigravity
  streaming, OpenCode and Seedance.
- It is not a drop-in image replacement. The official and Fluter branches both
  changed scheduling, billing, monitoring, catalog UI and model pricing.
- Build from the live production revision and merge the pinned upstream
  revisions. Do not deploy the official image directly and do not build from an
  old worktree.

## Pinned Identities

### Official

```text
v0.2.4:      5de5e2bed035d43591a2e10e51f420ef6a84eb98
v0.2.5:      86f93c28ee34cc74b629dafb748bd5ac5ca8c5ea
v0.2.7 tag:  aea725f2ea644d5592d0bbb1d63b607efa7e200a
VERSION fix: 1a9d49e16f7a22c432b428fce4af8d731f1fa364
```

The `v0.2.7` tag still contains `backend/cmd/server/VERSION=0.2.5`. Official
release workflows injected the release version while building, and the next
official commit changed the source file to `0.2.7`. A Fluter source build must
therefore absorb both the `v0.2.7` tag and pinned commit `1a9d49e16`; otherwise
the application can still display `0.2.5` after a nominal `v0.2.7` upgrade.

### Live Fluter Production

Verified read-only on 2026-09-19:

```text
SSH alias: fluterapi-prod
Role: production
Directory: /www/sub2api
Image: fluter/sub2api:fluter-0.2.4-deepseek-fixed-peak-20260918-r1
Image ID: sha256:c8b2ad2879da22cc76f1b33026035ffcc361119c8c6b86af7bea857567233c09
Revision: 39abf42abcce5d0d50e05673050704265da9c8c7
Source snapshot: 6972b4a33283ea71a7f0fb328522ddeb13640b50962b2f868de2ff059ca08c62
Exact source: project/.worktrees/production-39abf42-exact
Manifest: .release-evidence/20260918-deepseek-fixed-peak/release-manifest.json
Latest automatic archive: /www/sub2api/backups/sub2api-backup-20260919T034415Z.tar.gz
```

The application, PostgreSQL and Redis were healthy, and
`https://api.fluterapi.top/health` returned `{"status":"ok"}`. This preflight
made no production changes.

### Prepared Upgrade Line

```text
Branch: prep/v0.2.7-from-production-20260919
Worktree: project/.worktrees/upgrade-v0.2.7-20260919
Base revision: 39abf42abcce5d0d50e05673050704265da9c8c7
```

`check-production-baseline.sh` passed. A reversible trial merge was used only
to enumerate conflicts and then aborted; the worktree returned to a clean
production-derived baseline.

## Official Changes Worth Taking

### v0.2.5

- OpenCode Zen/GO accounts and protocol routing.
- OpenAI WebSocket pool capacity, retry, ping handling and execution-scope fixes.
- Responses bridge fixes for system messages, cache control, text recovery and
  namespaced calls.
- DeepSeek V4.1 Flash validation, routing and official pricing fixes.
- Scheduler duration and sticky-hit statistics corrections.
- Channel monitor provider, path, refresh and UTC-bucket corrections.
- Bulk subscription, API key and user administration improvements.
- More precise usage-cost display and TTFT in request details.

### v0.2.7

- Native Seedance Ark asynchronous video task API.
- Plugin host KV/account-directory services and a read-only plugin status channel.
- Codex root-level union tool schemas converted correctly on
  `/v1/responses -> /v1/messages`.
- DeepSeek Chat fallback restores `reasoning_content`.
- Kimi Coding Plan 403 quota exhaustion becomes a timed pause instead of a
  permanent disable.
- Antigravity Gemini bare-model routing and SSE heartbeat fixes.
- Model Plaza remains reachable from the mobile portrait header.

## Divergence And Conflict Surface

Using `v0.2.4` as the common ancestor, official `v0.2.7` is 268 commits ahead
and the live Fluter revision is 90 commits ahead. Both sides changed 94 files.
The trial merge produced these 13 direct conflicts:

```text
backend/cmd/server/VERSION
backend/cmd/server/wire_gen.go
backend/internal/service/account_stats_pricing_test.go
backend/internal/service/billing_service.go
backend/internal/service/billing_service_test.go
backend/internal/service/deepseek_pricing_test.go
backend/internal/service/gateway_record_usage_test.go
backend/internal/service/openai_account_scheduler.go
backend/internal/service/openai_gateway_record_usage_test.go
backend/resources/model-pricing/model_prices_and_context_window.json
frontend/src/components/layout/AppSidebar.vue
frontend/src/components/layout/__tests__/AppSidebar.spec.ts
frontend/src/views/admin/__tests__/ChannelMonitorView.grok.spec.ts
```

Required resolutions:

- `VERSION`: resolve to `0.2.7` and test the compiled value, not just the file.
- `wire_gen.go`: merge `wire.go`, then regenerate with Wire. Never hand-edit the
  generated file as the final resolution.
- Billing, DeepSeek and pricing JSON: preserve Fluter's fixed DeepSeek peak-base
  policy, CNY-as-site-unit semantics, account cost accounting, long-context
  tiers and tool/search charges; selectively absorb official model aliases and
  routing fixes. Do not restore official dynamic peak/off-peak repricing.
- Scheduler: preserve smart scheduling, exploration, sticky escape, quality
  thresholds, warm standby and model-capability fail-open; absorb official quota
  window, metric and media-slot corrections.
- Sidebar: preserve Canvas, external recharge/tutorial links, catalog management,
  Model Plaza and Available Channels; integrate the official site-billing-mode UI.
- Monitor tests: assert provider-registry behavior after the merge. Do not resolve
  the conflict by freezing an obsolete hard-coded provider count.

## Database Migration Gate

Official adds two migrations:

```text
238_opencode_go_platform.sql
238_purge_unlimited_user_platform_quotas.sql
```

Fluter already has:

```text
238_account_stats_time_pricing.sql
```

The migration runner records and sorts full filenames, so duplicate numeric
prefixes can coexist. The risk is semantic, not filename collision.

Production currently has 944 rows in `user_platform_quotas`; all 944 have
`daily_limit_usd`, `weekly_limit_usd` and `monthly_limit_usd` set to NULL. The
official purge migration will delete all of them. Although these rows represent
unlimited quota, they contain historical usage/window state. Before release:

1. Create a fresh full database archive.
2. Export `user_platform_quotas` separately with row count and checksum evidence.
3. Restore the production backup into an isolated database.
4. Run the complete merged migration set there.
5. Verify quota creation, reads, usage-window display and the quota flusher.
6. Compare expected deletion and post-migration constraints before allowing a
   production migration.

Never use the live production database as the migration test environment.

## Default Configuration Changes

Production does not explicitly override the following keys, so the new defaults
would take effect after upgrade:

```text
gateway.openai_compact_model: gpt-5.4 -> gpt-5.5
gateway.openai_ws.oauth_max_conns_factor: 1.0 -> 5.0
gateway.openai_ws.apikey_max_conns_factor: 1.0 -> 5.0
```

Acceptance must cover compact fallback reselecting the platform/account, 1013
busy behavior, WebSocket memory/connection counts and hard concurrency limits.
The default SSRF allowlist also adds `opencode.ai`; no manual production config
change is currently required because production has no custom `allowed_hosts`.

## Upstream Issue Watchlist

`v0.2.7` was released only hours before this preflight. Official CI and security
scans passed, but the following open issues overlap Fluter's critical paths:

- [#7330](https://github.com/Wei-Shaw/sub2api/issues/7330): compact fallback can
  switch model without reselecting platform/account.
- [#7336](https://github.com/Wei-Shaw/sub2api/issues/7336): DeepSeek V4.1 Flash
  can report zero tokens and later disconnect.
- [#7285](https://github.com/Wei-Shaw/sub2api/issues/7285): DeepSeek native
  Responses silently ignores `additional_tools`.
- [#7241](https://github.com/Wei-Shaw/sub2api/issues/7241): Chat fallback can
  lose namespaced custom exec calls.
- [#7222](https://github.com/Wei-Shaw/sub2api/issues/7222): an empty leading SSE
  chunk can prevent overloaded failover.
- [#7205](https://github.com/Wei-Shaw/sub2api/issues/7205): sticky-session account
  failover has a cache/input-token billing risk.
- [#7209](https://github.com/Wei-Shaw/sub2api/issues/7209): V1 channel-monitor
  schedules may not resume after upgrade.
- [#7292](https://github.com/Wei-Shaw/sub2api/issues/7292): priority, load factor,
  concurrency and sticky spillover may be applied inconsistently.
- [#7348](https://github.com/Wei-Shaw/sub2api/issues/7348): an invalid Codex UA
  can silently fall back to an old version rejected by Astra.

The v0.2.7 Anthropic union-schema fix does not resolve the DeepSeek tools issues.

## Implementation Order

1. Start from this production-derived line and fetch pinned official revisions.
2. Merge `v0.2.7`, resolve conflicts by the rules above, then absorb only the
   pinned VERSION sync commit.
3. Register any compatibility decisions in `PRODUCTION-EXTENSIONS.md` and a
   dated extension record.
4. Regenerate Wire and generated assets from their source definitions.
5. Run the isolated restored-database migration rehearsal.
6. Run targeted billing, scheduler, probe, monitor, catalog, Responses and
   migration tests; then full backend tests, frontend typecheck/tests/build and
   `git diff --check`.
7. Exercise protocol fixtures for native Responses tools, Anthropic bridging,
   DeepSeek Chat/Responses and streaming overload failover.
8. Build a `linux/amd64` candidate from a clean commit, generate the secret-free
   manifest and run the full production-extension capability gate.
9. Keep the candidate isolated for review. A production switch is a separate,
   explicitly authorized operation with fresh backups and rollback evidence.

## Release Acceptance Gate

Do not approve the candidate unless all of these are evidenced:

- Compiled UI/API version reports `0.2.7`.
- Every capability in `PRODUCTION-EXTENSIONS.md` remains present, including smart
  scheduling/probes, quality/cache telemetry, model sync/filter, pagination 100,
  catalog controls, fixed DeepSeek peak pricing and tool/long-context billing.
- The restored-database migration rehearsal passes and the quota purge result is
  explicitly accepted.
- Billing snapshots remain finite and correct for cache, long-context, tool,
  image/video and account-cost paths.
- Model capability errors, generic upstream failures, overloaded streams and
  sticky escape select another valid account where appropriate.
- Native Responses terminal/custom tools are retained end to end; bridge routes
  remain explicitly marked partial where they cannot preserve native tools.
- V1/V2 monitor schedules, high-frequency probes, budget ledgers and automatic
  recovery continue after restart.
- Model Plaza and Available Channels preserve Fluter visibility controls and do
  not mutate backend channel pricing.
- Candidate manifest records commit, source snapshot, immutable image ID,
  architecture, prior production image and all test results.

## Current Stop Point

Preparation is complete. No merge commit, candidate image or release manifest
for v0.2.7 exists yet. Production remains on the verified v0.2.4-derived image.
