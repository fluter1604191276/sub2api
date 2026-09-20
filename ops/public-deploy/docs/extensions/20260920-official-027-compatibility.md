# Official Sub2API v0.2.7 compatibility

Capability ID: official-027-compatibility
Business purpose: Upgrade the production-derived custom line to the official v0.2.7 feature baseline without losing Fluter's scheduling, probe, pricing, catalog, quality and billing behavior.
Backend/frontend files: Official v0.2.7 merge across backend and frontend; generated Wire regenerated from `backend/cmd/server/wire.go`; pricing and scheduler conflicts manually reconciled.
Routes or jobs: Official Responses/WebSocket/DeepSeek/Antigravity/OpenCode/Seedance/plugin paths; existing Fluter scheduler, probe, monitor and public-catalog routes retained.
Database migration/data dependency: Official `238_opencode_go_platform.sql` and `238_purge_unlimited_user_platform_quotas.sql` require isolated restored-database rehearsal before release; production was not migrated.
Billing impact: internal cost and user charge boundaries preserved; DeepSeek remains on the site's fixed peak-price base and explicit channel pricing remains authoritative.
Scheduling impact: score-aware smart scheduling, exploration, sticky escape and model-capability handling retained; official quota-window and scheduler metric fixes absorbed. Official scheduling remains the path when smart scheduling is disabled.
Client protocol impact: OpenAI Responses, Chat Completions, Anthropic, Gemini, WebSocket and plugin compatibility updates; bridge routes remain subject to the documented native-tool boundary.
Tests/fixtures: `go test ./...`; targeted billing/DeepSeek/service tests; frontend typecheck; AppSidebar and ChannelMonitor tests; frontend production build.
First release manifest: pending candidate image and immutable digest; no production release yet.
Rollback note: keep production image `fluter/sub2api:fluter-0.2.4-deepseek-fixed-peak-20260918-r1` and the 20260919 production backup set; do not migrate or switch until the restored-database rehearsal and capability smoke pass.
Owner/status: Fluter / candidate prepared locally, production unchanged.
