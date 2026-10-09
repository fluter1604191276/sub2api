# Unlimited user-platform quota history: policy B (2026-10-09)

The production upgrade previously planned to delete all unlimited quota rows.
Policy B retains that history by setting only `deleted_at` on active rows whose
three limits are NULL. IDs, user/platform references, every counter, window
start, `created_at`, and `updated_at` are retained. Any configured limit,
including an explicit zero, and all previously archived rows are unchanged.
The change does not modify balances, prices, subscriptions, API Key quotas,
billing deduplication, usage logs, or billing replay.

## Future limits and audit visibility

Ordinary repository/Ent views hide archived rows. A first subsequently
configured limit uses the existing `UpsertForUser` insertion path: one fresh
active row, zero usage counters, NULL window starts. Historical unlimited
usage therefore does not consume the newly enabled limit. Updating an already
configured active limit retains its usage. Clearing and later reconfiguring
limits follows the existing soft-delete/new-row semantics.

Authorized audit readers can retrieve archived state using a read-only database
query with `deleted_at IS NOT NULL`, or Ent `mixins.SkipSoftDelete(ctx)` scoped
to the intended user/platform. This is a visibility mechanism, not an instruction
to change production or disclose user-level data. No new public/admin history UI
is added. This migration itself never physically deletes history; existing
independent parent-user deletion/retention workflows are outside its scope.

Restoring historical state as an active quota is a separate policy decision:
it may collide with the partial unique index or inherit old usage and reject
requests. Do not automatically clear `deleted_at`, merge counters, replay billing,
or restore a pre-upgrade database over new transactions. Preserve archived data
for audit; any requested operational restoration requires a reviewed transaction
and conflict handling. Migration rollback does not automatically reactivate rows.

## Migration identity and production boundary

The filename remains `238_purge_unlimited_user_platform_quotas.sql` because the
exact migration is **not applied** on the verified target production as of the
2026-10-09 read-only snapshot. Production may have other successful filenames
starting with 238; those are unrelated and must not be rewritten.

Original raw file SHA256: `2ea9aea4b152531184b14559dc413ad2c33eadc9985b49ff90c579b3e1f1592e`.
Original runner checksum (trimmed content):
`052756d1b1ac951f002034bf0434f8a6ea943ec0a2a34541ca4374ba6278cce7`.
New runner checksum (trimmed content): `137533d4d8fed4b660204f3a78d636dcb751697bf28d0a40d188e86514b6cb72`.
Original SQL is preserved in `20261009-quota-history-original-238.sql.txt`,
outside embedded migration files, and in the base Git commit.

Do not edit any successful database checksum or whitelist this mismatch.
An environment that already applied the original DELETE must fail closed and
be reviewed separately: this migration cannot recover physically deleted data.
Before deployment, recheck the exact filename is still pending and limits/counts
are current. The archive UPDATE runs in the normal migration transaction at app
startup; no schema change or production pre-migration is authorized now.

## Release validation

This patch belongs to `official-0214-compatibility` and `ops-baseline` capabilities.
Unit contract/checksum tests and real PostgreSQL regression tests cover preserved
rows, timestamps/windows, explicit zero/positive limits, pre-archived rows,
idempotence, hidden defaults and a zero-initialized future active row.
The flusher skips snapshots whose three limit pointers are all nil, so a late
old unlimited snapshot cannot overwrite a newly configured row. Explicit zero
and positive limit snapshots are still written. Real PG/Redis interleaving
regression captures a snapshot before archive/new-limit/cache invalidation and
flushes it afterwards. This guard does not solve the existing finite-limit
clear/recreate or reset/flusher races, nor cache invalidation failure; keep those
separate concurrency/enforcement gates.

Full candidate tests, independent code/architecture reviews and a new image are
required. The prior e0 SHA/digest and test results cannot identify or approve this
patch. Formal `allow_release` stays false until all gates close, including native
amd64 image load/drain, persistence, provider behavior, historical financial
outcomes, dependency security, and backup/rollback evidence. No push is authorized.
