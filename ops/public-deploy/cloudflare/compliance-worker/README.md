# Fluter Region Compliance Worker

Cloudflare Worker for edge-level region compliance routing.

## Current status

- Worker name: `fluter-region-compliance`
- Dashboard active version observed after deploy: `8c4dbe6a`
- Routes are attached in Cloudflare Dashboard for the three hosts below.
- The origin Caddy compliance rules are intentionally kept as fallback.

## Routes

- `fluterapi.top/*`
- `api.fluterapi.top/*`
- `img-api.fluterapi.top/*`

## Behavior

- Restricted country set currently contains `CN` only.
- `fluterapi.top/*`
  - `CN` visitors receive the main blocked page with HTTP `403`.
  - `/admin/upstream-rates/*` passes through for the Basic Auth ledger.
- `api.fluterapi.top/*`
  - `/health`, `/v1/*`, and `/responses` pass through.
  - Other restricted browser-style paths show the risk notice unless `fluter_region_ack=1`.
- `img-api.fluterapi.top/*`
  - `/health`, `/responses`, `/v1/responses`, `/v1/images/generations`, and `/v1/images/edits` pass through.
  - Other restricted browser-style paths show the risk notice unless `fluter_region_ack=1`.

The origin Caddy region rules can remain as a fallback during initial rollout.

## Verify Locally

```bash
npm install
npm run verify
npx wrangler deploy --dry-run
```

## Deploy

Preferred deployment is Wrangler after Cloudflare auth is available:

```bash
npx wrangler login
npx wrangler deploy
```

If using the Cloudflare dashboard, create a Worker named
`fluter-region-compliance`, paste `src/index.js`, and attach the three routes
listed above.

## Production smoke checks

Run these after route changes:

```bash
curl -fsS https://api.fluterapi.top/health
curl -fsS https://img-api.fluterapi.top/health
curl -sS -D - https://fluterapi.top/admin/upstream-rates/ -o /dev/null
curl -sS -D - https://api.fluterapi.top/v1/models -o -
curl -sS -D - -X POST https://img-api.fluterapi.top/v1/images/generations \
  -H 'content-type: application/json' \
  --data '{"model":"gpt-image-2","prompt":"health probe"}'
```

Expected:

- Health endpoints return `200` JSON.
- Admin ledger returns `401` with Basic Auth when unauthenticated.
- Machine API paths return JSON application errors when unauthenticated, not
  compliance HTML.

## Rollback

Disable the Worker routes or delete the Worker from Cloudflare. The existing
Caddy fallback still serves the same compliance pages at the origin.
