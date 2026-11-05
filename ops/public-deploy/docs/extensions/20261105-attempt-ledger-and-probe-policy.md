# Upstream attempt ledger and protocol-policy probes

## Scope

Tasks/141 and tasks/74 share a development branch rebased onto production
`a5382a523b77ce5a8fd565e0c6cf176bb39e537c`. The previous Responses diagnostic
commits are preserved in `backup/responses-diagnostics-before-rebase-20261105`.
The diagnostic table is migration `313_ops_response_diagnostics.sql`. Its
original development-only migration number was 244 and was never deployed.

The independent `upstream_attempts` table is migration 312. Every observed text
HTTP request and Responses WebSocket attempt has a unique attempt identity.
Retry attempts remain distinct even when the provider reuses a request ID.
The ledger includes success, failure, incomplete and missing-usage outcomes.
Missing usage or unidentified pricing remains unknown and is separate from a
known zero cost. Account costs use account pricing and its multiplier.

## Entry points and data

- `backend/internal/service/upstream_attempt*.go` observes raw protocol metadata.
- `backend/internal/repository/upstream_attempt_repo.go` persists and reconciles.
- `GET /api/v1/admin/ops/upstream-attempts` lists individual attempts.
- `GET /api/v1/admin/ops/upstream-attempts/daily-costs` groups costs by account
  and Beijing calendar day. Both routes use the existing administrator boundary.
- `GET /api/v1/admin/ops/upstream-attempts/queue-status` exposes enqueued,
  persisted, failed, dropped and queued counts to administrators.
- The Ops page exposes inclusive date and account filters and unknown counts.
- Recovery probes mark their source and group for this independent ledger.

HTTP observation follows the transport's existing requirement to close response
bodies. Close parses metadata and enqueues scalar persistence work without
waiting on a database write. The queue holds at most 1024 jobs, has two workers,
and bounds each insert to two seconds. Saturation drops a job and increments the
drop counter; shutdown drains for at most five seconds. Database failures emit
only an attempt identifier and leave the client response unchanged.
Error responses read only a prefix by their caller receive an additional bounded
tail observation (at most 4 MiB total and a 100 ms drain allowance); unobserved or
oversized usage stays unknown. WebSocket prices are frozen from a stable request
context before each actual write. Each actual OAuth plugin dispatch and HTTP
redirect/Grok compatibility hop is observed separately; local plugin routing or
availability failures do not invent sent attempts.
This ledger is observational and does not charge users,
update quotas, retry requests, or change ordinary account selection. A process
crash or database failure can still leave missing observations.

Only whitelisted IDs, model/protocol/status fields and numeric token/cost data
are stored. Request/response bodies, prompts, tool arguments, credentials and
free-form upstream errors are excluded. OpenAI/Gemini input counts are normalized
to non-cache input for the existing account-cost engine. Cache reads and writes,
output, and reasoning remain separate metadata fields.

## Probe policy

`cross_protocol_conversion_disabled` pauses the affected protocol before future
network attempts. The runner recalculates its protocol list between rounds.
Only this marker pauses permanently. Generic 401, model-not-found and balance
errors use ordinary backoff and remain probeable after repair. Group
configuration changes clear only the stale conversion rejection. Changes during
an in-flight probe are checked again on completion. Fully rejected states are
explicitly excluded from due claims until configuration changes.
Maintenance excludes physical rows already in `probing`; configuration changes
cannot reset an in-flight claim and hand it to a second worker.

The account/model physical probe state remains shared across its configured
group memberships. A configuration change in a participating group invalidates
the corresponding shared conversion-policy rejection.

## Verification and release

Targeted service/repository/handler/route tests, race checks, Vue tests,
typechecking and isolated PostgreSQL migration/idempotency tests accompany this
candidate. The evidence directory on MacBook Pro is
`/Users/Admin/Documents/study_project/sub2api/.release-evidence/20261105-three-batch-review-revision`.
Its final report records precise commands, exits and remaining verification gaps.

Image smoke and the first release manifest remain pending the separate release
approval. Migration and deployment each require taoo's approval. An older binary
can run with the additive ledger and diagnostic tables retained. Roll back the
application image using the approved manifest, preserving financial observations
and the prior image. Do not drop the tables or rewrite applied migration checksums.

Production acceptance must sample failed HTTP requests (including usage in the
tail), WebSocket turns and OAuth plugin requests against their upstream records,
then inspect queue drops/failures and table growth. Diagnostics follow existing
Ops retention; the independent upstream ledger currently has no automatic purge.
Measure its growth and approve a retention policy before deleting financial
observations. Unit tests and isolated PostgreSQL checks do not establish this
production completeness or growth evidence. Migrations 312/313 still need
separate approval.

Owner is Codex on MacBook Pro. Status is a local development candidate.
