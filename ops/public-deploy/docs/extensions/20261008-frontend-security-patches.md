# Frontend security patches — 2026-10-08

Capability ID: official-0213-compatibility / ops-baseline

Business purpose: replace the two vulnerable dependencies found by the
2026-10-08 00:42 Shanghai security scan of candidate `1c2ba1ca279f`.
The old candidate image does not contain these fixes and is superseded.

Backend/frontend files: `frontend/package.json`, `frontend/pnpm-lock.yaml`,
`tools/check_frontend_security_patches.cjs`.

Vue and its compiler/runtime/shared/server-renderer family are locked to
3.5.42; source-map-js is locked to 1.2.2. The existing SheetJS 0.20.3 tarball
and integrity pin are preserved. Vue's updated compiler adds its required
Babel helpers/parser/types and entities patch dependency.

Advisories and upstream patches:

- https://github.com/advisories/GHSA-68fv-2mgg-jv7q
- https://github.com/7rulnik/source-map-js/pull/79
- https://github.com/advisories/GHSA-g2v6-rqmx-r4w6
- https://github.com/vuejs/core/pull/15266

Applicability: the application uses Vue client-side `createApp()` and a
normal Vite browser build, embedded into the Go executable. No application
SSR or indexed-source-map consumer entry point was found. This is source
reachability evidence, not a vulnerability exception: both dependencies
are patched and must be checked in the actual built image.

Routes/jobs: no changes. Database migrations/data dependencies: no changes.
Billing impact: none. Scheduling impact: none. Client protocol impact: none.
Backend, SQL, legal documents, and application frontend source are unchanged
from the previously validated candidate; only dependency and audit records change.

Tests/fixtures: frozen pnpm 9 install; full 355-file / 2701-test frontend
suite; typecheck; lint:check; production build; bounded installed-package
regressions for invalid/excessive/nested source-map offsets and SSR attribute
names (`node tools/check_frontend_security_patches.cjs`). Production audit
has 0 high / 0 critical, with 13 moderate / 4 low still recorded. The full
development dependency audit retains 33 high / 3 critical and other lower
severity findings; this record does not claim a clean full dependency audit.
No new audit exception is added. The repository CI gate audits production
dependencies and validates exceptions for applicable high/critical findings.

First release manifest/image smoke: the new commit must receive its own
linux/amd64 image, source snapshot, digest, manifest, compiled capability
verification and isolated image smoke. Prior image results may be reused
only with their explicit unchanged-backend scope; they are not new-image tests.

Rollback note: production remains r4 (`06d2674d660a`). Preserve its immutable
image and coordinated backups. A later switch requires a fresh production
review; preparation is not deployment approval. No database rollback or
acceptance of administrator terms is performed here.

Owner/status: local MacBook Pro release owner; prepare-only at user request.
