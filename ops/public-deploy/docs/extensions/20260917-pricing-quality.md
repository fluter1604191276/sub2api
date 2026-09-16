# Catalog price provenance and robust streaming quality

```text
Capability ID: pricing-calibration, catalog-surfaces, quality-score, scheduler
Business purpose: distinguish reference, configured and effective prices; keep isolated latency outliers from dominating quality while recognizing slow generation.
Backend/frontend files: model_plaza_official_pricing.go, model_plaza_service.go, model_plaza_handler.go, account_quality.go, smart_scheduler_preview.go, usage_log_repo_stats.go, usage_log_repo_smart_scheduler_quality.go, frontend catalog pricing helpers/components and quality descriptions.
Routes or jobs: existing model plaza, available channels, account/group quality and smart scheduling endpoints.
Database migration/data dependency: none; existing usage_logs, ops_error_logs and configured channel/group/account prices.
Billing impact: display only; no live price write, currency conversion or billing calculation change.
Scheduling impact: quality scores and candidate ordering in already enabled smart scheduling groups.
Client protocol impact: none.
Tests/fixtures: domestic reference and currency boundary tests; effective price, zero-price, image and tier tests; robust quality, fallback and repository scan/filter tests; existing scheduling and protocol regressions.
First release manifest: pending candidate build and verification.
Rollback note: restore previous application image; no data rollback required.
Owner/status: fluter / candidate; not deployed.
```

## Price boundaries

- Site balances remain internal units. A currency label never converts numbers.
- Available Channels distinguishes configured channel base prices from rate-adjusted references. Its existing DTO cannot resolve group-specific overrides, image rates or disabled long-context tiers; those cases must not claim an inferred actual price. Model Plaza retains the billing schedule resolver and effective user/group rate.
- Official references are independent of customer billing and account cost cards. An upstream-observed price is not an official reference. A currency mismatch must not be repaired by changing only its symbol.
- Verified China-region references take precedence over the historical billing catalog. Exact model IDs are required; unverified dated/reseller aliases do not inherit a reference by substring.
- Customer price and account cost are separate. Each applies its own multiplier once. Calibrating live prices requires a separately reviewed configuration change and backup; this patch does not carry such a mutation.

Verified 2026-09-17, all values CNY per million tokens, input/output/cache hit:

| Model | Reference | Conditions |
| --- | --- | --- |
| GLM-5.2 / GLM-5.3 | 8 / 28 / 2 | China-region official pricing |
| GLM-5.3-Flash | 0.8 / 2.8 / 0.23 | Regular price; page also lists a promotion labelled through 09-09, not assumed currently effective |
| GLM-5.1 | 6 / 24 / 1.3 | Input <32K; 32K+ reference is 8 / 28 / 2; no unverified numeric token boundary added |
| DeepSeek V4.1 Flash | 1 / 4 / 0.02 | Off-peak; peak is twice this price |
| DeepSeek V4 Pro 0813 | 4.5 / 13.5 / 0.15 | Off-peak; peak is twice this price |

Sources: https://open.bigmodel.cn/pricing and
https://api-docs.deepseek.com/zh-cn/quick_start/pricing/ .
DeepSeek peak windows: weekdays 09:00-12:00 and 14:00-18:00 Asia/Shanghai.
These official windows are explanatory metadata, not the site's customer time-pricing configuration.

## Quality v3 / preview-v6

- Keep actual average first-token and duration metrics. Do not relabel medians as averages.
- Score TTFT at 70% and generation throughput at 30%. Throughput normalizes post-first-token duration by output tokens so longer answers are not automatically worse.
- Convert each median/tail metric to a bounded score first, then combine median 80% / tail 20%. This limits one extreme value without dropping repeated slow requests.
- Use only charged streaming usage evidence, excluding scheduled probes and channel-monitor traffic. A charged usage row does not prove a complete successful response; this change does not infer error ownership from latency or silently discard client cancellations.
- Throughput evidence requires at least 32 output tokens and at least 1,000ms after the first token. At least three valid samples are required for each component. Partial evidence remains explicitly identified and capped.
- Account/group display and smart scheduling use the same scoring function. Their scopes differ: routing remains model/endpoint-specific, whereas the account summary is broader.
- Preserve exploration, recovery probes, sticky escape, circuit exclusions, configured primary-score thresholds and fallback selection. Existing enabled groups will receive new quality scores after deployment; replay scores are not a full prediction of final routing shares.

## Known configuration risks

The production audit identified a shared GLM pricing row across differently priced
models, potential customer-price/account-cost inversions, and historical DeepSeek
billing defaults. The private audit stays outside Git. None is silently corrected
by changing public reference labels. A candidate image does not certify live
account cost cards against fresh upstream bills.

## Regression reconciliation

The full unit suite also exposed a pre-existing creation-path omission: disabled
Codex manifest account selections were normalized for validation but not copied
to the newly created group. The candidate persists the normalized disabled
selection in `admin_group.go`; creation still rejects enabling the feature before
group membership can be validated. Existing create/update regression tests cover
this repair. There is no schema change or effect on existing groups.

API contract fixtures now include the already-existing monitor daily budget
field. Two dated-model fallback expectations now match the existing CNY price
cards, and the dashboard expectation follows quality version 3. No billing
implementation was changed to satisfy these fixtures.

## Candidate validation

- `go test -tags unit ./...`: passed, including service, repository, handler,
  API contracts, protocol bridges and existing scheduling/probe regressions.
- Frontend full suite: 285 files / 2,070 tests passed. Production build,
  type checking, translation completeness and changed-file ESLint passed.
- Release-tool unit suite: 28 tests passed; shell syntax, Python compile and
  diff whitespace checks passed.
- Exact candidate account/group/routing statistics queries executed successfully
  against production PostgreSQL in read-only transactions. No live data changed.
- Independent scoring review found no remaining scoped runtime issue. Sampled
  24-hour replay is retained privately; it is not a claim about future traffic shares.
- Image identity, binary capability checks and runtime smoke are recorded in
  the release evidence after the clean source commit, not inferred from tests.
