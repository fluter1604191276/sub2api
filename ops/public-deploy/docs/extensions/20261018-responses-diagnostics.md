# Responses completion diagnostics

Capability ID: responses-completion-diagnostics

Business purpose: allow administrators to locate HTTP 200 Responses requests
that terminated incomplete, without treating HTTP success as evidence of a
completed model response. This is diagnostic metadata, separate from billing,
SLA error counters, retries and account scheduling.

Owner: Codex, task https://tasks.fluterlab.top/tasks/74.
Base: production revision `550fc80eb71746f07879eb3c01bfe5a5fdb50de0`.
Status: development candidate; not deployed.

## Contract

- Record native Responses completion status and incomplete reason, correlated
  with server/client/upstream request IDs, response ID, user, group and account.
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
- Collection covers the native HTTP Responses stream and JSON handlers, including
  SSE-to-JSON responses. Uninstrumented protocol bridges and WebSocket paths must
  not be falsely classified as missing-terminal native responses.
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
and time/filter indexes. Retention follows the existing Ops error retention
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
