# Account and Group Quality Telemetry

This branch adds display-only account and group quality telemetry to Sub2API.
It is based on the official `v0.1.163` tag and is intentionally limited to the
quality feature. It does not change account routing, group priority, billing,
or scheduler decisions.

## Features

- Account page: `1h Quality` and `24h Quality` columns.
- Group page: `1h Quality` and `24h Quality` columns.
- Latest `10` and `100` timed requests for each account or group.
- Average time to first token (TTFT) and average total duration.
- Numeric score plus letter grade: `S+`, `S`, `S-`, `A+`, `A`, `A-`, `B+`,
  `B`, `B-`, or `C`.
- 1-hour activity state: `active`, `low_sample`, `degraded`, `failing`, or
  `idle`.
- Display-side snapshot caching and ETag support for batch requests.

The quality columns are opt-in through the existing admin table column
settings. They are intended for manual scheduling review. They never modify
account priority, schedulability, group routing, or sticky-session behavior.

## Data Scope

Quality samples are read from existing usage and error records. No database
migration is required.

Successful samples must satisfy all of the following:

- `usage_logs.actual_cost > 0`
- `usage_logs.stream = TRUE`
- `usage_logs.duration_ms IS NOT NULL`
- The record is within the last 24 hours.

The latest 10/100 requests are ranked per account or group. The 1-hour view
uses the same ranked data and limits the displayed evidence to the last hour.
Failure counts come from streaming entries in `ops_error_logs` during the
last hour. This keeps probe and non-streaming records out of the quality
display.

## Scoring Policy

The scoring implementation is in
`backend/internal/service/account_quality.go`.

- Minimum valid sample count: `3`.
- TTFT weight: `85%`.
- Total duration weight: `15%`.
- TTFT requires at least `3` TTFT samples.
- If TTFT evidence is insufficient, duration-only scoring is capped at `69`.
- A window without usable timing data remains unscored.

The score uses piecewise-linear latency curves calibrated from observed
interactive traffic. The current curve points are:

| Metric | Curve points `(latency, score)` |
|---|---|
| TTFT | `0.8s/100`, `2s/95`, `4s/85`, `8s/72`, `12s/62`, `20s/48`, `30s/35`, `45s/20`, `60s/10`, `90s/0` |
| Total duration | `5s/100`, `10s/90`, `20s/75`, `40s/55`, `60s/40`, `90s/25`, `120s/12`, `180s/0` |

Letter grades map to the numeric score as follows:

| Score | Grade |
|---:|:---|
| 95-100 | S+ |
| 90-94 | S |
| 85-89 | S- |
| 80-84 | A+ |
| 75-79 | A |
| 70-74 | A- |
| 65-69 | B+ |
| 60-64 | B |
| 50-59 | B- |
| 0-49 | C |

Activity classification uses successful and failed streaming attempts from the
last hour:

- `active`: at least 3 successful requests.
- `degraded`: at least 5 total attempts and failure ratio at least 20%.
- `failing`: no successful request and at least 3 failures.
- `low_sample`: some activity, but not enough evidence for the states above.
- `idle`: no successful or failed request in the last hour.

An account or group may have a good 24-hour historical score while being
`idle` in the last hour. The UI mutes that historical score so it is not
mistaken for current live evidence.

## Admin API

Both endpoints require the existing admin authentication middleware.

```http
POST /api/v1/admin/accounts/quality-stats/batch
Content-Type: application/json

{"account_ids":[1,2,3]}
```

```http
POST /api/v1/admin/groups/quality-stats/batch
Content-Type: application/json

{"group_ids":[10,20]}
```

The response has the shape:

```json
{
  "stats": {
    "1": {
      "last_10": {},
      "last_100": {},
      "recent_1h": {},
      "activity": {},
      "window_hours": 24,
      "score_version": 2
    }
  }
}
```

The in-process snapshot cache is short-lived and only reduces repeated admin
table requests. It does not persist quality data or replace the database.

## Validation

From the repository root:

```bash
cd backend
go test ./internal/service ./internal/repository ./internal/handler/...

cd ../frontend
pnpm run typecheck
pnpm exec vitest run \
  src/components/account/__tests__/AccountQualityCell.spec.ts \
  src/views/admin/__tests__/AccountsView.bulkEdit.spec.ts \
  src/views/admin/__tests__/AccountsView.schedulerScore.spec.ts \
  src/views/admin/__tests__/AccountsView.sparkShadow.spec.ts \
  src/views/admin/__tests__/AccountsView.usageWindowsHint.spec.ts \
  src/views/admin/__tests__/GroupsView.columnSettings.spec.ts \
  src/api/__tests__/admin.groups.duplicate.spec.ts
pnpm run lint:check
pnpm run build
```

## Integration Notes

The feature expects the existing `usage_logs` and `ops_error_logs` timing
fields to be populated. Deployments that do not record streaming duration or
TTFT will correctly show missing samples instead of fabricating a score.

This branch is a feature-focused sharing branch, not a production image. Keep
local credentials, account exports, upstream URLs, and environment files out
of any public fork or release artifact.
