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
| `PUT /api/v1/admin/channel-monitor-v3/settings` | Global probe enable and daily upstream reservation budget |
| `PUT /api/v1/admin/channel-monitor-v3/groups/:id` | Per-group visibility, interval and test model |
| V3 runner | Every 15 seconds, one process-local cycle at a time, durable cross-process claims |

## Data and configuration

Migration 314 adds settings and per-group controls. Migration 315 adds durable
probe dispatches and budget reservations. Migration 316 adds the dedicated
identity and per-group loopback-only keys. Migration 312 from the second batch
provides per-attempt actual upstream costs. The loopback endpoint uses the configured HTTP server port and existing
environment configuration.

Fresh settings disable supplemental probes and hide every group, with a $2
Beijing-calendar daily upstream reservation budget. Administrators explicitly select visible
groups and enable probes. Frequency zero or an empty model disables that group's
probe. Visibility remains usable independently of probe enablement.

Quality takes the latest valid 10/100 observations across all history, including
zero-charge probes and monitors. Client-attributed Ops errors are filtered before
the window limit, matching direct, `client:<client_request_id>` and
`local:<request_id>` usage identities within the same group. Observable upstream-failure samples remain eligible. Existing
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

The runner reuses the attempt-ledger frozen price snapshot and account multiplier.
Its upper bound covers reachable price intervals, 5-minute/1-hour cache writes,
cache reads, reasoning levels and the largest configured time multiplier.
The same calculation runs again at the actual selected HTTP transport. A V3
dispatch permits one HTTP attempt, an explicit maximum of 128 output tokens and
at most 32 KiB of request data; paid tools and multiple outputs are rejected.
Gateway retries share the atomic guard. Unknown prices, an expanded or missing
output limit, an insufficient reservation or a Beijing date change fail closed.
Responses WebSocket and OAuth plugin dispatches are temporarily blocked for V3
before upstream I/O; ordinary customer requests retain those transports.
Probe failures do not write production account/model cooldowns, account error
state or transport circuit breakers; their attempts remain in the probe ledger.

A claim reserves the remaining global allowance. Settlement locks the dispatch
row and releases the hold only when all costs are known and
`actual_cost_usd <= reserved_usd`. A known overrun is retained as `uncertain`,
keeps its hold, counts at least its actual cost against subsequent reservations,
and emits `channel_monitor_v3_probe_cost_exceeds_reservation` for alerting.
Any known portion of an incomplete result is preserved. The runner allows one
minute after completion for the bounded asynchronous attempt ledger to flush.
Claiming, final send admission and settlement share the global budget lock.
Final send admission also locks the saved settings and rechecks the current cap,
enable flag and Beijing date; an obsolete unsent reservation is released.
Changes saved after this admission point do not cancel an already admitted
in-flight request, whose reservation and eventual cost remain accounted for.
Unknown or missing cost, ambiguous network outcomes and interrupted dispatches retain their reservation
for that budget day. Such cases can stop further probes for the day. Dispatches
are never automatically replayed after a crash. A lower cap or a Beijing date
change releases a proven-unsent claim before dispatch.

Billing impact is internal inference cost and discounted charges to the dedicated
monitoring user. Reservations use upstream cost before the user discount. This
is an admission budget against frozen configured prices and provider token
limits, not a guarantee about an external invoice when a provider violates its
limits or reports extra usage. Such an overrun must remain uncertain and alert.
Existing customer billing formulas and ordinary account-selection logic have no
difference from the second-batch base. Protocols follow the group's native
Responses, Chat Completions, Anthropic or Gemini gateway entry point.

## Verification and release

Tests cover historical windows, client error exclusion with upstream sample
retention, small samples, settings validation, public DTO and route boundaries,
identity idempotence, 0.001 rates, durable claims, duplicate dispatch rejection,
unknown reservations, idle traffic checks, budget/date pre-send guards, trusted
marking, blocked WebSocket/plugin retries, oversized requests/output limits,
cache/tier price ceilings, over-reservation settlement and crash recovery.
Real PostgreSQL integration uses the normal
migration runner, and re-runs it to check idempotence. Frontend component/API/route
and locale tests, type checks, lint, builds and Go race/static checks accompany
the candidate.

Evidence is produced on MacBook Pro at
`/Users/Admin/Documents/study_project/sub2api/.release-evidence/20261105-three-batch-review-revision`.
`REPORT.md` lists exact final commands, exits and verification gaps. Candidate
image smoke evidence and the first release manifest are pending release approval.

Before rollout, obtain separate approvals for migrations 314–316, creation of
the probe identity, global dispatch enablement and public group visibility.
The first controlled settlement must verify `actual_cost_usd <= reserved_usd`,
no production cooldown changes after probe failures, and attempt completeness
and queue drops. Local tests do not replace those production observations.

Owner is Codex. Status is a PR candidate awaiting renewed review. PR
review belongs to Relay. Merge, production migration and deployment each require
taoo approval. Disabling the 14 existing V1 monitors requires a separate approval
after V3 rollout. Application rollback uses the approved previous image and
retains additive tables, identities and financial history.
