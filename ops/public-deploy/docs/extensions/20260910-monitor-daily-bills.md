# Channel Monitor Daily Bills

Base: live revision `12e3171f263d5bbe93f471c677883e6129377917`, image
`fluter/sub2api:fluter-0.1.183-smart-primary-score-20260910-r2`.
This change has not been deployed.

## Contract

- Admin-only `GET /api/v1/admin/channel-monitors/bills?days=30`, range 1-365 days.
- Calendar dates always use Asia/Shanghai, independently of app/server timezone.
- Today represents completed persisted checks since local midnight; its as-of
  timestamp is returned. In-flight reservations are not spending.
- Account cost uses the smart-probe formula: gateway usage snapshot
  `COALESCE(account_stats_cost,total_cost) * COALESCE(account_rate_multiplier,1)`,
  rounded to 8 decimal places. This is configured procurement cost, not a
  supplier-invoice reconciliation. Base-price estimates remain a separate column.
- New checks capture the response X-Client-Request-ID and resolve the configured
  monitor credential to a local API-key ID in memory. Reconciliation requires
  both to match a unique, time-bounded usage log with the fixed monitor user-agent.
  All 13 production monitors used the site's own gateway at implementation time.
  External endpoints without corresponding local usage remain pending.
- Cost snapshots retain account ID, usage-log ID, cost base, rate and amount.
  Repeated reconciliation cannot double count or reprice settled records.
  Reconciliation occurs after checks (last 30 days) and on bill retrieval;
  delayed usage is retried without extra upstream calls or balance/quota writes.
- Missing usage/pricing is counted as unknown, not free. Pure quota checks are
  zero-cost. Historical zero estimates conservatively remain unknown.
- Historical backfill is partial because old history may have been purged.
  Missing days are not fabricated as zero-cost days.
- Daily aggregates survive monitor deletion and history cleanup. A transaction
  trigger makes history persistence and daily recording atomic, including on
  rollback. Existing admission-budget behavior is unchanged.
- Migration 234 adds a table and trigger without changing existing columns.
  Backfill locks history inserts briefly; assess history size before deployment.

## Verification

`ops/public-deploy/test-monitor-daily-bills.py` uses an isolated disposable
PostgreSQL container and tests midnight attribution, numeric/unknown estimates,
quota zero cost, NaN protection, migration reruns, deletion retention and rollback.
Service tests exercise midnight and invalid ranges. Repository tests assert the
budget ledger is not used as spending. Vue tests assert budget reservations do
not appear as cost and network failures do not masquerade as zero cost.

## Remaining Work

2026-09-10 validation (supersedes the earlier interrupted run): disposable
PostgreSQL tests passed; two Vue component tests passed; targeted ESLint,
frontend typecheck and git diff --check passed. Targeted Go service/repository
bill tests passed, including transaction rollback. Browser layout verification
and image construction have not been completed. Do not release until remaining
release gates are completed.

Historical checks without correlation IDs cannot be reliably repriced. Direct
external monitors, if added later, require a separately verified account binding
and pricing scope. They must not silently default to 1x cost. Supplier invoice
verification remains separate from the account-rate cost implemented here.

Migration 235 adds nullable correlation columns and a durable cost-record table;
history insertion and cost-record capture share a database transaction. Existing
production images ignore the added columns. No production migration was run.

`test-monitor-account-cost.py` passed on isolated PostgreSQL: custom cost base,
rate snapshot, zero multiplier, API-key mismatch isolation, delayed records,
idempotency and retention. Vue component tests and targeted ESLint passed.
