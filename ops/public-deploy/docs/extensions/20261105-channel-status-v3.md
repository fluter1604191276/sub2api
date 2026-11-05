# Channel status V3

## Purpose and baseline

Capability ID `channel-monitor-v3` covers tasks/145. It adds a public group
status page and administrator controls, with idle-only supplemental inference.
Development starts from production `a5382a523b77ce5a8fd565e0c6cf176bb39e537c`
and is stacked on the second-batch attempt-ledger candidate `710865b83`.

## Source and entry points

Backend files are `internal/service/channel_monitor_v3*.go`,
`internal/repository/channel_monitor_v3*.go`, the V3 handlers, route registration,
and generated dependency wiring. `account_quality.go` exposes a parameterized
sample minimum while existing quality callers keep their established minimum.
Frontend files include the admin V3 API and view, public V3 API and view, router,
home navigation, sidebar and locale entries.

| Entry | Access and purpose |
| --- | --- |
| `/channel-status-v3` | Anonymous page with group names, latest-10/latest-100 grades and sample counts |
| `GET /api/v1/channel-monitor-v3` | Public-IP rate limited minimal DTO |
| `/admin/channel-monitors-v3` | Existing administrator navigation and authorization |
| `GET /api/v1/admin/channel-monitor-v3` | Settings, all groups and today's budget |
| `PUT /api/v1/admin/channel-monitor-v3/settings` | Global probe enable and actual upstream daily cap |
| `PUT /api/v1/admin/channel-monitor-v3/groups/:id` | Per-group visibility, interval and test model |
| V3 runner | Every 15 seconds, one process-local cycle at a time, durable cross-process claims |

## Data and configuration

Migration 314 adds settings and per-group controls. Migration 315 adds durable
probe dispatches and budget reservations. Migration 316 adds the dedicated
identity and per-group loopback-only keys. Migration 312 from the second batch
provides per-attempt actual upstream costs. The loopback endpoint uses the configured HTTP server port and existing
environment configuration.

Fresh settings disable supplemental probes and hide every group, with a $2
Beijing-calendar daily upstream cap. Administrators explicitly select visible
groups and enable probes. Frequency zero or an empty model disables that group's
probe. Visibility remains usable independently of probe enablement.

Quality takes the latest valid 10/100 observations across all history, including
zero-charge probes and monitors. Client-attributed Ops errors are filtered before
the window limit. Observable upstream-failure samples remain eligible. Existing
quality curves produce grades with a minimum of one valid sample. Raw timing,
prices, accounts, models and probe settings stay outside the public DTO.

## Identity, cost and scheduling

Enabling probes transactionally creates a separate non-login monitoring user.
It receives an initial internal balance of $1 and a per-group user multiplier
of 0.001. This development choice keeps ordinary gateway balance validation
usable. There is no automatic balance replenishment. Subscription groups require
the monitoring user's own active subscription. All requests pass through ordinary
authentication, group model admission, selection and billing. Internal V3
monitoring spend is governed by its reserved upstream budget, so the trusted
monitor request omits the customer-revenue profit gate. Ordinary requests keep
the existing profit policy.

Only groups without real traffic in their configured interval can be claimed.
The claim and the final pre-send check both inspect traffic. Legacy monitors,
recovery probes, IQ requests and V3 requests are separated from real traffic.
A server-private loopback token marks V3 cost attribution and is stripped from
inbound headers before forwarding. It grants no additional gateway privileges.
The existing monitor user-agent family keeps supplemental requests outside
ordinary scheduler quality statistics. V3 historical quality includes them.

The runner reuses the attempt-ledger price snapshot and account multiplier,
including conservative input/output/cache bounds, before dispatch. Each V3
dispatch permits one actual upstream attempt. The transport boundary rechecks
the selected account's frozen price, actual request byte allowance, explicit
128-token output ceiling and Beijing date before sending. Gateway retries share
the same atomic guard. Requests with an expanded or missing output limit fail
closed before reaching the provider.
A claim reserves the remaining global allowance. Known actual costs settle the
reservation after the loopback response has fully ended. Unknown or missing cost,
ambiguous network outcomes and interrupted dispatches retain their reservation
for that budget day. Such cases can stop further probes for the day. Dispatches
are never automatically replayed after a crash. A lower cap or a Beijing date
change releases a proven-unsent claim before dispatch.

Billing impact is internal inference cost and discounted charges to the dedicated
monitoring user. The daily cap uses actual upstream cost before the user discount.
Existing customer billing formulas and ordinary account-selection logic have no
difference from the second-batch base. Protocols follow the group's native
Responses, Chat Completions, Anthropic or Gemini gateway entry point.

## Verification and release

Tests cover historical windows, client error exclusion with upstream sample
retention, small samples, settings validation, public DTO and route boundaries,
identity idempotence, 0.001 rates, durable claims, duplicate dispatch rejection,
unknown reservations, idle traffic checks, budget/date pre-send guards, trusted
marking and native request payloads. Real PostgreSQL integration uses the normal
migration runner, and re-runs it to check idempotence. Frontend component/API/route
and locale tests, type checks, lint, builds and Go race/static checks accompany
the candidate.

Evidence is produced on MacBook Pro at
`/Users/Admin/Documents/study_project/sub2api/.release-evidence/20261105-batch3-channel-status`.
`REPORT.md` lists exact final commands, exits and verification gaps. Candidate
image smoke evidence and the first release manifest are pending release approval.

Owner is Codex. Status is a local development candidate awaiting review. PR
review belongs to Relay. Merge, production migration and deployment each require
taoo approval. Disabling the 14 existing V1 monitors requires a separate approval
after V3 rollout. Application rollback uses the approved previous image and
retains additive tables, identities and financial history.
