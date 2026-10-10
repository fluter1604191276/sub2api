# Usage journal activation — independent r4 repair branch

Capability ID: usage-recovery-journal
Business purpose: commit financial effects and immutable usage snapshot atomically; persist missing usage rows without repeating charges.
Backend/frontend files: backend/internal/{config,handler,repository,service}/usage_journal*.go, usage_billing_repo.go, gateway_usage_billing.go, openai_gateway_{service,usage}.go, usage_billing.go, cmd/server/wire{,_gen}.go; no frontend behavior change.
Routes or jobs: ordinary text gateway routes and aliases; journal consumer and sampled admission monitor. Enabled mode rejects excluded media/tools/search/async/WebSocket work before forwarding.
Database migration/data dependency: 241_usage_recovery_journal_v1.sql registered only on r4 base whose migration head is 240. One additive table, indexes, immutable trigger/function and version comment. Standard checksum ledger; no historical backfill, grants or down. Existing object collisions fail closed. A newer branch requires migration-number review and revalidation.
Environment-variable/config dependency: usage_journal_draft_enabled defaults false. True requires standard mode, sync overflow, explicit positive pending count/age/relation-byte limits, Codex image bridge disabled and exact schema/permissions validation.
Billing impact: user charge transaction gains journal/receipt, no financial replay or price change. Post-commit caches/notifications remain non-atomic.
Scheduling impact: none; enabled admission can close while the consumer drains.
Client protocol impact: OpenAI Responses / Chat Completions / Anthropic / Gemini ordinary text only; enabled compatibility restrictions require separate production approval.
Upstream version/base commit: r4 06d2674d660a65abb8f2bb0a63b6e032eb35c937; activation parent 148075a1b00363499fb0f97be5f95a00e42e2332. This branch does not replace the formal production candidate.
Tests/fixtures: private Unix-socket PG18 synthetic identities; journal transactions, consumer crashes/quarantine, unknown commit, HTTP handler/Wire activation, shutdown, capacity and backup/rollback tests; actual migration runner registration/checksum/rollback/collision tests. No real upstream or credentials.
Image smoke evidence: pending new source-bound image and same-digest native amd64 acceptance. Cross compilation and previous images cannot fulfill this gate.
First release manifest: none; CI/build/native acceptance authorized, production allow_release=false.
Rollback note: stop admission and drain tracked producers; retain journal and financial rows, verify every journal key has matching hot/archive dedup coverage. Binary rollback only; do not restore an older DB over live debits, drop schema or replay charges. See ops/local-development/usage-journal/ROLLBACK.md.
Owner/status: Fluter; nonproduction validation only. Pre-financial-COMMIT upstream completion loss, sampled capacity overshoot, disk/WAL growth and compatibility restrictions remain explicit rollout risks.
