# Official v0.2.15 integration (candidate only)

Capability ID: official-0215-compatibility.

Official repository Wei-Shaw/sub2api, stable tag v0.2.15, peeled commit
f2669c8cf62555cd92389b3f55920e9e6e7c6ff2, published 2026-10-09T03:35:49Z.
The tag's tracked VERSION still says 0.2.14; this candidate records 0.2.15
explicitly, matching the upstream main's version-only follow-up.

The source integrates this fixed upstream commit into the existing r4-derived,
non-journal candidate line. Official platform catalog, protocol/tool/WS/model
and frontend race fixes are retained. Site-specific scheduling, monitoring
budget/bills, pricing/cost/catalog controls, model-sync and passive capability
observations survive. Conflict resolutions retain sanitized account-test errors,
both native model-cache tests, dynamic platform-catalog ordering plus the local
group ordering helper and model filter, and local pricing labels. They do not
change customer pricing. The existing SheetJS official0.20.3 dependency and
locked integrity are preserved; official Go1.27.2/x-net0.60/grpc1.83.2/Node24
updates are adopted, without importing the abandoned journal security branch.
Fresh dependency review also applies official x/image0.45.0 and compress1.18.7
module fixes. No financial/product formula changes or audit exemptions.
The remaining x/crypto OpenPGP required-module advisory and braces development
HIGH remain explicit; final image and release disposition are separate.

No journal files or migration241_usage_recovery_journal_v1.sql are included.
There is no new global rejection of tools/media/WebSocket requests. The paused
custom500k pricing proposal is not imported. Official migration241 files are
unrelated to that abandoned local journal filename and must not be deleted by
numeric prefix. Existing quota-history-preservation migration238 remains a
candidate-only proposal: live migration status and irreversible effects require
their own reviewed production plan. No migration executes during this work.

Tests: exact candidate backend/frontend regressions and #14 contract tests;
source/runtime security scan and final amd64 image evidence must be recorded
separately. Old candidate PASS/digest does not qualify this source. Formal
manifest remains allow_release=false pending required evidence.

Known limitations: upstream usage_log insertion and migration223 rollup lock
mechanisms are unchanged from r4; PR7104 remains unmerged and the previous
isolated PostgreSQL counterexamples remain. Retention365-day configuration is
Grok #26's production operation and observation, not a code fix or a guarantee
against future recurrence. No paid provider tests, production changes or
historical billing replay are authorized by this local development record.

Rollback: preserve the actual previous production image/config and database
history; inspect new schema/financial transactions before any binary rollback.
Never overwrite newer committed transactions with a historical full dump.
