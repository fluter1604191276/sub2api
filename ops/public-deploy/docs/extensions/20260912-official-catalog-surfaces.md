# Official catalog surfaces compatibility

Capability ID: catalog-surfaces
Business purpose: Adopt the official v0.2.4 model-plaza aggregation and
presentation while retaining the site's independent visibility controls and
available-channel workflow.
Backend/frontend files: `backend/internal/service/model_plaza_service.go`,
`backend/internal/handler/model_plaza_handler.go`,
`backend/internal/handler/available_channel_handler.go`,
`backend/internal/handler/catalog_metadata.go`,
`frontend/src/views/ModelPlazaView.vue`,
`frontend/src/components/modelPlaza/`,
`frontend/src/components/channels/`, and
`frontend/src/utils/catalogModelFamily.ts`.
Routes or jobs: Existing `/api/v1/model-plaza` and
`/api/v1/channels/available`; available-channel group links preserve
`/model-plaza?embedded=1&group=<id>`. No new job.
Database migration/data dependency: Existing active channel/group pricing and
public catalog visibility settings; no migration and no data mutation.
Billing impact: Presentation only. Site standard/user prices and official
reference prices are displayed separately; no UI catalog setting changes
channel pricing, user billing, mappings, routing, probes, or scheduling. The
site's displayed USD remains the site's RMB 1:1 purchasing-power convention.
Scheduling impact: None.
Client protocol impact: None; protocol platform remains distinct from the
model-family filter (for example, a DeepSeek model in an OpenAI-compatible
group remains under OpenAI protocol).
Tests/fixtures: Model Plaza, pricing table, available-channel, and model-chip
Vitest suites; handler unit tests; release-bundle tests; typecheck and diff
checks.
First release manifest: Pending candidate manifest.
Rollback note: Revert the application image only; no data rollback is needed.
Owner/status: fluter / candidate, production not switched.

Compatibility decisions:

- `catalog_metadata` explicitly identifies configured-channel aggregation,
  configured-not-live availability, pre-group pricing basis, and user-rate
  resolution status.
- Missing user-rate resolution falls back visibly to site standard price rather
  than claiming an exact personalized price.
- Available channels retain their existing JSON array contract; metadata is
  additive in response headers.
- Official aggregation remains the source of model-plaza data. Local filters
  are presentation-only and never write billing state.
