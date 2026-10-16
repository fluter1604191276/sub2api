# Account-derived model catalogue — 2026-10-16

## Purpose and baseline

Model-plaza and available-channel membership previously depended on hand-written
channel price/mapping rows. This omitted models already configured on supplying
accounts. Derive display membership from eligible bound accounts instead, while
keeping persisted billing rules separate.

Development baseline: live production revision
`c7b483fc7895be0745f8fa1a86f0fe3401f6c5e1`, verified on MacBook Pro through the
existing `fluterapi-prod` alias with node role exactly `production`.
Branch: `feature/account-catalog-20261016`.

## Source, routes and data

- `backend/internal/service/account_model_catalog*.go`: batch-load persistent
  account eligibility and derive finite public names from exact account mapping
  keys and saved `extra.available_models` snapshots. Wildcard patterns are not
  emitted as model names. Passthrough accounts use snapshots, ignoring stale
  mapping keys. Account bindings and provider/group boundaries are preserved.
- `backend/internal/service/channel_available.go` and
  `model_plaza_service.go`: shared read-only projection applies existing channel
  aliases, restrictions, group allowlists and pricing lookup rules. Composite
  groups preserve their routing rules and concrete provider attribution.
- `backend/internal/service/catalog_display_pricing.go`: distinguish absent
  prices from intentionally configured zero prices. Unknown prices remain
  unavailable; a synthesized zero fallback must not become a free price card.
- `backend/internal/handler/available_channel_handler.go`: public sections use
  each visible group's own model pool; combine groups only when their model and
  display-price sets agree. Existing public visibility policy remains in force.
- `GET /api/v1/admin/channels/model-catalog`: existing administrator authentication;
  read-only response contains channel IDs, public model names and platforms only.
- `frontend/src/views/admin/ChannelsView.vue` and admin channel API: separate
  account model count/overview, loading/failure states and refresh. Reading the
  overview never adds price rows or changes an edit form's persisted payload.
- Public catalogue metadata reports `configured_accounts` and
  `configured_not_live`. Existing response structures remain compatible.

Account snapshots are the output of the existing account model sync. This change
automatically follows saved account changes on the next catalogue request; it
does not add an upstream polling job or call providers during public page reads.
An upstream model absent from both an allowed exact alias and a saved snapshot
cannot be discovered by this projection alone. Refresh the account snapshot
using the existing account-sync flow first. A failed sync retains its existing
known-good snapshot, following existing sync behavior.

## Impact boundaries

No database migration, environment-variable change, timer, schema change or new
dependency. No writes to accounts, channel price cards, mappings, group prices,
rates or routes. Real request billing, scheduling and protocol forwarding remain
unchanged. A configured catalogue entry is not a live availability guarantee and
does not prove every endpoint/tool type works on every supplying account.

## Verification and release gate

Relevant unit coverage includes account add/remove and disabled/schedulable
membership, finite wildcard/passthrough evidence, aliases, group isolation,
restrictions, price immutability, display-price fallback, public field whitelists
and frontend read-only/failure behavior. Complete backend default tests and
frontend tests/lint/typecheck/build are required before source delivery, together
with the deployment-script test/syntax floor and `git diff --check`.

Raw local verification evidence belongs outside the build context:
`/Users/Admin/Documents/study_project/sub2api/.release-evidence/20261016-account-catalog/`.
The local delivery report records exact commands, outcomes and source identity.

Image smoke: pending a separately prepared clean-commit amd64 candidate. Before
release verify anonymous/admin authentication boundaries, group-specific public
responses, account updates reflected in both views, unknown-price rendering and
that channel/account configuration is unchanged. Health alone is insufficient.
No production switch or paid business request is part of this source delivery.

## Rollback

This feature stores no new business data and needs no database downgrade. Revert
the feature commit or restore the recorded previous application image using the
normal release checklist; preserve the current database and all newer business
transactions. Do not restore an old database snapshot for this display change.
Current live image identity and source remain in the external delivery evidence;
do not treat this dated record as a production version pointer.
