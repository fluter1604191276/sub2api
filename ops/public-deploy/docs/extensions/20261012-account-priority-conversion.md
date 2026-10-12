# Manual account multiplier to priority conversion (#14)

Capability ID: account-priority-conversion.

Business purpose: administrators select accounts in Account Management -> More
actions -> Convert multiplier to priority, preview every account's multiplier,
current priority and calculated priority, then explicitly confirm an overwrite.
This implements the user-approved v8/comment48 rules as reaffirmed by comment95;
source text SHA256 `2efe6636e32b15944c953e8c7617edea3464a0dc2f6739087bc76ac2e7274c7c`.

Backend files: service/account_priority_conversion.go,
repository/account_priority_conversion.go, handler/admin/account_priority_conversion.go.
Frontend: AccountPriorityConversionDialog.vue, AccountsView.vue, admin/accounts.ts
and English/Chinese account translations.

Routes: POST /api/v1/admin/accounts/priority-conversion/preview and /apply.
Both retain AdminAuthMiddleware and independently check the authenticated admin
role. No non-admin UI entry. Preview reads only id/name/priority/rate_multiplier
from live accounts; credentials, shadow inheritance and billing multiplier
fallbacks are not used. The decimal database value is read as text, multiplied
by 1000 and rounded up using the existing shopspring/decimal dependency. Zero
maps to 1. Missing, invalid, negative and out-of-range values remain unchanged
with a per-account reason. The PostgreSQL priority INT range is checked before
conversion. `2.0070` yields 2007. Duplicate priorities are allowed.

Apply recomputes on the server. Each confirmed account snapshot is checked
against its current decimal multiplier and old priority; a conditional SQL
UPDATE also checks those values and deleted_at to close the read/write race.
Only priority and updated_at are written, with the existing scheduler outbox
event committed in the same transaction. Concurrently changed or deleted
accounts are skipped and displayed. A failed/ambiguous acknowledgement is
reported without automatic replay; the UI requires a fresh preview.

Database dependency: existing accounts and scheduler_outbox tables only.
No migration, default-priority change, refund/debit/replay, reversal or extra
audit feature. Default priority remains 50 in schema/import/CRS/UI. Previously
manual priorities are overwritten only on explicit confirmation. Cancellation
and preview perform zero writes. The accepted mix with unchanged priority50
accounts is retained. No automatic/scheduled conversion.

Billing impact: none; account multipliers, user/group prices and quota counters
are untouched. Scheduling impact: writes the existing priority field and emits
the existing cache-refresh event; no algorithm or ranking direction changes.
Client protocol impact: none; no journal or global tools/media/WS 503 gate.

Tests: service decimal/partial failure/stale snapshots; repository exact-column
SQL, transactional outbox rollback and deleted/stale accounts; handler role and
request boundaries; dialog preview/cancel/confirmation/errors/late responses;
API per-account snapshots; optional localpriority real PostgreSQL fixture on a
disposable Unix socket. Full evidence and actual outcomes live outside source.

First release manifest: pending new candidate image and full release evidence.
This source change is local development, not a production release.
Rollback: reverting the application feature removes the button/routes but does
not undo priorities already explicitly confirmed. No undo operation is promised;
administrators may edit the existing field normally. Preserve new transactions
and production state; do not restore an old database dump over them.

Owner/status: Mini task3 isolated development; existing Lumen requirement author
record retained. Production deployment requires separate authorization.
