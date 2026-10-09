# Candidate contract corrections

Capability IDs: error-passthrough, channel-monitor-bulk-interval,
channel-monitor-budget, image-cost, pricing-calibration,
official-0214-compatibility, ops-baseline.

Base: candidate 7ae12eff07cf421a254659a55a87bca96dcc5a8f, retaining official
v0.2.14 and the complete existing extension inventory. This is a locally
authorized successor; production reads, writes, switching and new-SHA push are
not authorized in this phase. Existing production-derived ancestry is preserved;
no production query is made to create this worktree.

## Error responses

The runtime rule path and OpenAI, Anthropic and Gemini handler rule paths share
ErrorPassthroughClientMessage. Upstream-derived error text uses the existing
client-visible sanitizer; URL query/fragment and upstream locations are removed
while ordinary diagnostic text remains. Rule matching still uses the original
body, configured custom messages remain operator-controlled, and response codes,
skip-monitoring policy and failover decisions are unchanged. Ops sanitization is
unchanged. Tests cover URL query/fragment, percent encoding, nested errors,
top-level message/detail and all four emission paths.

## Task cancellation and budget cleanup

Scheduled monitor context is propagated through fire/pool submission to the
existing bounded RunCheck timeout. Unschedule and Stop cancel cooperative checks;
Stop still waits for checks that do not return immediately. No new concurrency,
cadence, distributed execution or fee policy is introduced.

The existing known-complete finite budget settlement is preserved after task
cancellation using the existing 15-second detachedBillingContext. Original-day,
reservation and actual-cost values are unchanged, with one settlement and no
retry. Unknown/nonfinite/incomplete cost retains its conservative reservation.
History persistence still uses its original context. Crash-safe reservation
identity, settlement idempotency and history-failure scheduling policy remain
explicit scope decisions, not guarantees added by this patch.

## Optional rule scopes

The shared repository insertion helper normalizes nil group_ids/account_ids to
empty PostgreSQL arrays. The API already permits account-only or group-only
rules. Supplied IDs are preserved; an empty group scope does not inherit a
channel's groups or match every group. Create, Update and batch replacement use
the same helper. No migration, backfill, new configuration or business-group
inference occurs. Ordinary user zero-price policy is unchanged.

## Validation, identity and rollback

Source and regression tests are recorded in
.release-evidence/20261009-contract-fixes/ outside the source tree. Candidate
commit/image/snapshot identities, actual pass/fail/not-run results, full required
checks, independent review and isolated restoration are bound there after the
source is committed. This record is not evidence that an image already contains
the changes or that release gates passed.

Rollback is the previous immutable candidate 7ae12eff and its image, without a
new database migration. Production rollback remains the separately recorded r4
object; this development record does not authorize production action. All22
formal gates, allow_release=false, V1/tool scope decisions and the old unknown
startup cause remain unchanged until explicit evidence/decisions are registered.
October10 03:00 conditional review/switch automation still exists and must not
switch while NO-GO; this phase does not modify it.
