# Fluter Region Compliance Pages

This folder contains static region-compliance notice pages and the production routing notes.

## Target behavior

- `fluterapi.top`
  - Restricted visits to `/docs/*` without `fluter_docs_ack=1` see `risk-notice.html`.
  - Entering the displayed acknowledgement phrase sets a `/docs`-scoped cookie for 24 hours and then opens the tutorial.
  - Other restricted browser paths continue to see `main-blocked.html`.
  - `/admin/upstream-rates/*` and `/admin/s2a-manager/*` remain exempt and stay behind Basic Auth.
- `api.fluterapi.top`
  - Machine API paths remain proxied: `/v1/*`, `/responses`, `/health`.
  - Restricted browser/page visits without `fluter_region_ack=1` see `risk-notice.html`.
- `img-api.fluterapi.top`
  - Image API paths remain proxied: `/v1/images/generations`, `/v1/images/edits`, `/v1/responses`, `/responses`, `/health`.
  - Restricted browser/page visits without `fluter_region_ack=1` see `risk-notice.html`.

## Cloudflare Worker archive

Worker source and deployment notes live at:

`../cloudflare/compliance-worker/`

The Worker implementation is retained as an archive and rollback reference,
but production no longer attaches Workers Routes to these domains. Caddy is
the active routing layer, avoiding Worker request-quota exhaustion from taking
the website and API offline together.

## Caddy production path

The Caddy rules are the active production implementation. Changes are
reversible with `caddy validate` and `systemctl reload caddy`, and do not
require rebuilding or restarting the sub2api container.

## Test headers for Caddy fallback

Cloudflare supplies `CF-IPCountry`. For origin-level smoke tests, simulate a restricted visitor with:

```bash
curl -sS -H 'CF-IPCountry: CN' -H 'Host: fluterapi.top' https://127.0.0.1/ --resolve fluterapi.top:443:127.0.0.1 -k
```

Tutorial gate checks:

```bash
curl -sS -H 'CF-IPCountry: CN' https://fluterapi.top/docs/ --resolve fluterapi.top:443:127.0.0.1 -k
curl -sS -H 'CF-IPCountry: CN' -H 'Cookie: fluter_docs_ack=1' https://fluterapi.top/docs/ --resolve fluterapi.top:443:127.0.0.1 -k
```

For public tests through Cloudflare, country headers are controlled by Cloudflare and should not be assumed spoofable from a normal browser request.
