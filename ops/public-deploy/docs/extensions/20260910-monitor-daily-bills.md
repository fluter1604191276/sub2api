# Channel Monitor Daily Bills

Base: live revision `12e3171f263d5bbe93f471c677883e6129377917`, image
`fluter/sub2api:fluter-0.1.183-smart-primary-score-20260910-r2`.
This change has not been deployed.

## Contract

- Admin-only `GET /api/v1/admin/channel-monitors/bills?days=30`, range 1-365 days.
- Calendar dates always use Asia/Shanghai, independently of app/server timezone.
- Today represents completed persisted checks since local midnight; its as-of
  timestamp is returned. In-flight reservations are not spending.
- Numeric amounts remain explicitly USD base-price usage estimates, NOT supplier
  debits. There is no reliable request-ID/cost linkage in the existing checker.
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

2026-09-10 validation: disposable PostgreSQL test passed; two Vue component
tests passed; targeted ESLint and git diff --check passed. Full-project Go
test compilation and vue-tsc did not produce a completed result under local
memory pressure and were interrupted. These are NOT passed checks. Browser
layout verification and image construction have not been completed. Do not
release until these gates are completed.

Actual per-day procurement cost requires trustworthy correlation with billed
requests, or supplier bill reconciliation, not a static current account multiplier.
The current UI marks supplier debit unverified. This implementation must not be
represented as completing actual-cost reconciliation.
