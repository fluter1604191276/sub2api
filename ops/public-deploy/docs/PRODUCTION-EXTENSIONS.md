# Production Extension Inventory

This is the authoritative inventory of site-specific Sub2API behavior that must survive an upstream update. It is capability-oriented: a file list alone is not enough because Docker can omit source files, generated frontend assets can be stale, and a route can exist while its runtime behavior is broken.

Every release manifest must record the status and evidence for each capability below. New二开 must be added here before it is eligible for production.

每次新增或修改二开，还必须复制 `EXTENSION-RECORD-TEMPLATE.md` 到 `extensions/`，把源码、路由/任务、数据依赖、测试、镜像 smoke 和回滚信息写清楚。清单是发布门禁的输入，不是发布完成后的回忆录。

## Capability Matrix

| ID | Capability | Required evidence | Risk surface | Current status |
| --- | --- | --- | --- | --- |
| scheduler | Smart account scheduling, sticky-session escape, exploration, score-aware selection | gateway_scheduling.go, scheduler tests, admin settings/routes | Routing, availability, session affinity | Required |
| scheduled-probe | Scheduled probe plans, budget/attribution, result handling, scheduler linkage | scheduled_test_runner_service.go, scheduled_test_service.go, scheduled_test_handler.go, probe tests | Upstream cost, account availability | Required |
| quality-score | Account/group 1h and 24h quality score, grade, first-token and duration metrics | account stats service/repository, AccountsView.vue, relevant API/routes/tests | Scheduling decisions, operator visibility | Required |
| cache-hit-rate | Rolling 24h cache-hit statistics in account management | GetBatchCacheHitStats, account_usage_service.go, account API/UI/tests | Cost analysis, pricing decisions | Required |
| image-cost | Separate image upstream cost from user billing and size/operation context | account_stats_image_pricing.go, pricing tests, channel UI/tests | Billing and margin | Required |
| pricing-calibration | Channel/model pricing calibration and explicit billing boundaries | channel calibration service/repository/tests, pricing routes | User charges, loss risk | Required |
| catalog-surfaces | User-facing available-channel catalog and model-plaza navigation, filtering, summaries, readable group names, plus administrator-controlled public model visibility | backend/internal/service/public_catalog_visibility.go, backend/internal/handler/admin/public_catalog_handler.go, frontend/src/views/admin/PublicCatalogView.vue, frontend/src/api/admin/publicCatalog.ts, public catalog and model-plaza tests | Catalog discoverability only; no billing, mapping, routing, probe, or scheduler effect | Required |
| model-sync-filter | Sync upstream-supported models, model filtering, page-size behavior | account model sync service, account routes/UI/tests | Availability and mapping | Required |
| error-passthrough | Configurable error rewriting without leaking upstream URLs | error passthrough handler/service/routes/tests | Security and client retry behavior | Required |
| model-capability-failover | Deterministic upstream model-capability rejection isolation and account failover | model_not_found_error.go, ratelimit_service.go, OpenAI failover handlers, classifier/rate-limit tests | Routing, model availability, sticky-session escape | Required |
| generic-400-failover | Account-scoped failover for the narrow `HTTP 400 Upstream request failed` gateway response | openai_gateway_upstream_errors.go, openai_account_runtime_block_fastpath.go, OpenAI gateway handlers/forwarders, failover and sticky tests | Routing, retry budget, sticky-session escape | Required |
| responses-tools | Responses tool parsing, streaming custom tool events, bridge behavior | apicompat converters and fixtures | Client protocol, terminal capability | Partial by design; release blocker unless route is explicit |
| upstream-ledger | Upstream pricing, account-cost and mapping audit tools | ops/public-deploy/upstream-rates, sanitized snapshot/ledger tests | Cost audit, mapping decisions | Required |
| ops-baseline | Backups, role marker, release evidence, upstream-rate maintenance | ops/public-deploy, release manifest, backup tests | Recovery and auditability | Required |

## Status Semantics

- Required: must be present and tested for a production image.
- Partial: the feature has a known protocol or upstream limitation. The release must state the supported route and reject or report unsupported requests; it cannot silently claim end-to-end support.
- Retired: kept only as historical evidence and must not be included in the production acceptance set.

## Change Record Fields

For every new or changed extension, add a short record containing:

~~~text
Capability ID:
Business purpose:
Backend/frontend files:
Routes or jobs:
Database migration/data dependency:
Billing impact: none | internal cost | user charge
Scheduling impact: none | score | account selection
Client protocol impact: none | OpenAI Responses | Chat Completions | Anthropic | other
Tests/fixtures:
First release manifest:
Rollback note:
Owner/status:
~~~

Do not put API keys, cookies, Bearer tokens, database passwords, or raw upstream request bodies in this inventory.

## Source Of Truth

The source of truth is the tuple:

~~~text
production image digest + release-manifest.json + source snapshot hash + this inventory + test evidence
~~~

Git branch names, image tag names, local folder names, and memory are labels only.

## Catalog Visibility Boundary

The `public_catalog_visibility` setting is an independent presentation policy. The
administrator page is `/admin/channels/catalog`, backed by:

~~~text
GET /api/v1/admin/public-catalog/visibility
PUT /api/v1/admin/public-catalog/visibility
~~~

The policy supports a default media visibility and explicit `platform:model`
overrides. Text models remain visible by default; `gpt-image` and
`gpt-image-*` remain visible by default; other media models remain hidden until
explicitly enabled. Historical overrides are retained when a model temporarily
disappears from the active-channel candidate list.

This setting must not be reused as a source for channel pricing, model mappings,
group routing, user billing, upstream cost accounting, probes, monitoring, or
smart scheduling.

## Required Release Records

发布记录至少要能回答四个问题：这次镜像由哪个源码快照构建、包含哪些二开、每项二开如何验证、失败时恢复哪个镜像和 Compose。登记文件、release manifest 和测试输出必须随候选版本保存；不能只依赖 Codex 会话上下文或个人记忆。
