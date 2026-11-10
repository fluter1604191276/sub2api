# Responses completion diagnostics

Capability ID: responses-completion-diagnostics

Business purpose: allow administrators to locate HTTP 200 Responses requests
that terminated incomplete, without treating HTTP success as evidence of a
completed model response. This is diagnostic metadata, separate from billing,
SLA error counters, retries and account scheduling.

Owner: Codex, task https://tasks.fluterlab.top/tasks/74.
Base: rebased with tasks/141 onto production revision `a5382a523b77ce5a8fd565e0c6cf176bb39e537c`.
Status: development candidate; not deployed.

## Contract

- Record native Responses completion status and incomplete reason, correlated
  with server/client/upstream request IDs, response ID, user, group and account.
- Account identity is captured with the outbound attempt; selecting another
  candidate before slot acquisition cannot relabel the previous attempt.
- SSE-to-JSON observation scans frames in order independently of the existing
  client conversion/usage selection. The first authoritative terminal is retained;
  a provisional bare error may be superseded by a formal terminal.
- Preserve three distinct optional output budgets: requested, forwarded and
  upstream-reported. Missing values mean unknown, not zero or a presumed default.
- Preserve upstream input/cache/output/reasoning usage as separate optional
  fields. Input includes cached tokens; cache is a subset, not extra input.
- Terminal outcomes distinguish completed, incomplete, failed, client disconnect
  and missing terminal event. Duplicate terminal frames produce one request row.
- Client disconnection is also a separate boolean: an upstream completed response
  drained after disconnection remains completed with that flag set, without
  implying successful delivery to the client.
- No prompt, tool payload, authorization header or raw upstream error text is
  retained. Protocol strings and numeric fields are bounded/validated.
- Collection covers native HTTP stream/JSON/SSE-to-JSON/passthrough, Responses
  WebSocket turns, OAuth plugin forwarding, Grok and the Anthropic/Chat
  Completions bridges, including the client WebSocket HTTP bridge. Each WebSocket turn publishes its scalar terminal;
  starting another turn flushes any earlier unpublished bare-error or unfinished
  snapshot without changing response bytes or billing.
- Ingestion is best effort on an independent bounded queue. Queue saturation or
  persistence failures may lose diagnostic metadata; this is not a billing
  journal. Drop/failure counters and rate-limited warnings expose those gaps.
- Storage and query are independent of error logs and usage accounting. Metadata
  collection must not alter response bytes, response status, retry decisions,
  scheduling decisions or customer charges.

## Surfaces and data

Backend: service Responses observer and Ops diagnostics methods; handler bounded
ingestion; dedicated repository and migration; administrator Ops list endpoint.
Frontend: administrator Ops dashboard Responses diagnostic entry and details.
Route: `GET /api/v1/admin/ops/response-diagnostics`.
Data dependency: additive `ops_response_diagnostics` table with request dedupe
and time/filter indexes. The undeployed migration 313 now stores an optional
UUID `turn_id` with unique `(request_id, turn_id)`: ordinary HTTP keeps an empty
turn ID and its previous request dedupe, while WebSocket turns cannot collide on
their shared connection request ID. The root request ID and exact search remain
unchanged. Migration 313 must be reviewed and approved with this candidate;
no production migration or applied checksum has been rewritten.
Retention follows the existing Ops error retention
setting and cleanup master switch, including its existing zero-day truncate
semantics. This is distinct from usage-log retention; no retention defaults or
settings are changed by this feature.
Environment/config dependency: existing Ops monitoring and retention settings.
Billing impact: none. Scheduling impact: none. Client protocol impact: observation
only; no automatic failover, replay, response rewriting or inference probes.

## Verification and deployment

Fixtures cover HTTP 200 incomplete, normal completed, failed, disconnected and
missing-terminal cases; absent budgets/usage; cached input; duplicate terminal
frames; secret-free metadata; list filters and pagination; administrator access;
UI unknown/zero distinction and the three output budgets. Actual verification
results belong in the candidate evidence report.

Image smoke evidence: not run; no production image built for this development
task. First release manifest: none. Production deployment remains a separate
operation requiring the existing baseline, backup and release gates.

Rollback: restore the previous application image. The additive diagnostic table
can remain inert; do not drop recorded data as part of application rollback.
Existing usage/error tables and billing data are not migrated by this feature.

## 2026-10-20 returned-review revision

Task 74 review identified native passthrough coverage, failover slot-acquisition
account attribution, and SSE-to-JSON terminal-order gaps. The revision adds
passthrough observers, binds account ID to the actual outbound attempt, and
observes buffered SSE in order without changing client response or usage logic.
Targeted regressions exercise real HTTP passthrough streaming/JSON/SSE-to-JSON,
a later selected candidate that is never sent, and conflicting terminal frames.
Development evidence lives on MacBook Pro under
/Users/Admin/Documents/study_project/sub2api/.release-evidence/20261020-responses-diagnostics-revision/.
Still a development candidate; production deployment is not part of this revision.

## 2026-11-05 client WebSocket HTTP bridge revision

The bridge observes original/forwarded caps, actual outbound account and upstream
HTTP status/request ID, then raw upstream Responses usage and terminals before
client-facing model/tool/error rewriting. It publishes at turn completion so a
last-frame write failure preserves the client-disconnect flag. Bare-error and
missing-terminal turns are flushed without inventing an upstream terminal.
Actual local HTTP plus coder WebSocket tests compare client frames and billing
usage with persistence enabled/disabled. PostgreSQL tests prove two turns with
one root request ID survive, identical keys dedupe and root-ID queries return
both. Evidence belongs to the MacBook Pro directory
`/Users/Admin/Documents/study_project/sub2api/.release-evidence/20261105-ws-http-bridge-diagnostics`.
No production action is included; PR5 retains its separate 24-hour rollout gate.
