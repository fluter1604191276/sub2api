# Local braces recursion guard proposal

Capability ID: build-tool-security (development proposal; not a production release).
Business purpose: bound braces3.0.3 AST nesting before recursive compile/expand/stringify can exhaust the Node stack (GHSA-vfj7-8cjw-p6xm / CVE-2026-93687).

The reviewed GitHub advisory still lists affected versions <=3.0.3 and no patched version. This is a local patch, not a claimed upstream version upgrade or advisory waiver. The package remains version3.0.3 and the HIGH advisory remains disclosed. The patch introduces a fixed AST depth limit128; the parser accepts at most127 nested brace/parenthesis groups. Deep patterns fail with SyntaxError. Ordinary escaping, quoted text and bracket literals follow the original parser; excessive real AST nesting cannot be allowed through an options override. Direct AST callers are guarded at all three walkers. Other expansion/memory resource risks are outside this targeted fix.

Backend/frontend files: frontend/patches/braces@3.0.3.patch (four upstream lib files), frontend/package.json, frontend/pnpm-lock.yaml, frontend/security/test-braces-depth-guard.mjs, Dockerfile patch COPY before frozen install. No backend, Go lock, billing, schema, pricing, application protocol, HTTP2, or runtime APK change.
Routes or jobs: no application routes/jobs changed. Existing frontend test:run and build execute the dependency guard tests; audit workflow and audit exceptions are unchanged.
Database migration/data dependency: none. Existing migration241 and all financial/history retention contracts remain.
Billing impact: none.
Scheduling impact: none.
Client protocol impact: none; this only rejects excessive depth in development-tool brace patterns.

Tests/fixtures: bounded512KiB stack/64MiB heap guard tests, unpatched RangeError control and ordinary differential corpus; real pnpm9 frozen offline small fixture applies exact patch to micromatch/chokidar chains. Full frontend Node22 typecheck, unfiltered test:run, lint:check and build results are in the separate local evidence package. Full frontend clean offline reinstall is unavailable because cached SheetJS0.20.3 tarball is missing; copied original matching-lock modules with exact patched bytes were used for local frontend regression. These results are not Node24 Docker/hosted CI or a new image acceptance.

First release manifest: none; proposal gets its own source SHA/snapshot if committed. b84/d37 remains the separately sealed candidate.
Rollback note: revert this source patch only on a development branch; do not rewrite b84 sealed artifacts. Any resulting image must have its own identity and verification. No production change/restart/rollback is authorized.
Owner/status: local repair proposal with recorded tests; Security disposition remains pending responsible review. No suppressed audit, dependency alias substitution, removed useful tools, relaxed checks or new security exception.

Primary source: https://github.com/advisories/GHSA-vfj7-8cjw-p6xm ; upstream3.0.3 source: https://github.com/micromatch/braces/tree/3.0.3 .
