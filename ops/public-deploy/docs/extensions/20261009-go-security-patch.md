# 2026-10-09 Go security patch candidate

Capability ID: ops-baseline / official compatibility.
Business purpose: fix the published Go standard-library and x/net security
findings using official patch releases, without vulnerability suppressions.
Base: 3e1e838067c62fb036db11d3dbe6e455aed8f83e, quota-history policy B.

Backend/frontend files: backend/go.mod and go.sum; root, deploy and backend
Dockerfiles; backend-ci, security-scan and release workflow version checks.
Go 1.27.2 and x/net v0.60.0 are pinned consistently. Only the required Go
module closure is upgraded; frontend dependencies and audit exceptions stay
unchanged. Official archive checksum, closure diff and module verification
are retained with local evidence.

Routes/jobs: no application route or job changes. Library patches can affect
HTTP framing, connection reuse and cancellation, so protocol and financial
regressions must run on the new exact SHA; old tests are not inherited.
Database migration/data dependency: none added or modified. Migration 238,
quota policy B (soft-delete old unlimited rows, preserve history, start newly
limited quota at zero), balances, prices and financial idempotency are intact.
Billing/scheduling impact: no intended business-semantic change.

Tests/fixtures: fresh default/unit/integration/lint, policy B real-PG race,
keepalive repetition, protocol/cancellation/billing regressions, full frontend,
release helpers, source govulncheck JSON/verbose including linux/amd64/embed,
and built binary metadata/security/image checks. Exact pass/fail/skip and
coverage limits belong to the final evidence, not this planned checklist.
First release manifest: local candidate evidence under
.release-evidence/20261009-quota-history-security/; allow_release=false until
all gates and explicit switch scope are satisfied.
Rollback: retain the original 3e source, old candidate images and production
r4. No production database, network, configuration or container changes here.
Owner/status: isolated local development; no push/merge/deploy authorization.

Official sources: https://go.dev/doc/devel/release#go1.27.2 and
https://github.com/golang/net/releases/tag/v0.60.0. Static called findings do
not prove actual exploitability; source scans do not replace binary scans.

Fresh Go 1.27.2 verification exposed an Ent schema importer incompatibility:
x/tools v0.49.0 cannot decode export-data V5. Official v0.50.0 is the minimum
compatible importer and the focused schema test passes after this upgrade.
CI golangci-lint moves from v2.13 to v2.14 for the same importer support; no
linter is disabled. govulncheck stays v1.8.0 and is rebuilt with Go 1.27.2.
The failed first validation is retained, and the final SHA is revalidated.
