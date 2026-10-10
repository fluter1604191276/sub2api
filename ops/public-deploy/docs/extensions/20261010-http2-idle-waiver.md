# HTTP2-IDLE-20261010 temporary exception

Status: user-approved temporary lint exception, development scope only. Parent conversation explicitly approved exactly the two verified compatibility warnings. No security, test, image/native acceptance or production-release gate is waived. Release remains CANCELLED and allow_release=false; passing all checks does not restore the canceled switch.

## Exact scope and rationale

The only excluded diagnostics are staticcheck SA1019 for `http2.ConfigureServer(server, &http2.Server{` and the immediately associated `IdleTimeout: time.Duration(h2cConfig.IdleTimeout) * time.Second,` in `backend/internal/server/http.go`. Each .golangci.yml rule combines an anchored relative path, exact source line, exact SA1019 message and staticcheck linter. This intentionally avoids nolint:staticcheck (which would cover other staticcheck diagnostics on that line), global -SA1019, broad directory/file exclusions or changes to enabled linters. An unrelated SA1019 or any other diagnostic remains visible even in this file.

Reviewed versions: Go1.27.2, golang.org/x/net v0.60.0, golangci-lint v2.14.0. Official docs advise Server.Protocols and Server.IdleTimeout; the exact installed SDK has no independent HTTP2Config idle field. Four HTTP/2 stream/frame/flow-control limits already use the public API. The compatibility bridge supplies independent H2 idle while preserving native graceful shutdown on one Server. Defaults remain H1=120s/H2=75s, and other configured values still apply independently. Removing the bridge or using one common timeout changes behavior; PING/request timeouts and hard Close timers are not equivalent to idle GOAWAY. This warning is real deprecation, not a claim that the analyzer is wrong.

Current normal Go1.27 builds use x/net's native wrapping implementation, without http2legacy. The reviewed Security result has no called/imported-package vulnerability finding attributable to these calls. Deprecation is not itself proof of a security flaw. Keep security scans running normally; this exception does not waive a future reachable vulnerability.

## Review triggers and removal

Repository maintainers must review this exception on any Go SDK, x/net, golangci-lint version change; public HTTP2Config capability change; edits to these source expressions, H1/H2 idle settings, protocol support, ingress limits, Wire lifecycle or shutdown/drain logic; new relevant security advisory; or before a future production-release decision. Scheduled policy review is due 2026-11-10 (documentation deadline only, no automation created). Do not broaden the regex to regain green CI after a changed diagnostic. A newly available supported independent H2 idle API should replace the bridge and both rules together after regression acceptance. If a security issue is identified, reassess/remove the exception and keep release blocked until repaired.

## Follow-up task: supported HTTP/2 migration

Scope approval is still required for a larger migration. Preserve single-address HTTP1/H2C behavior, independent idle, limits and lifecycle. Prefer a supported future per-protocol idle API; otherwise separately design protocol dispatch plus two native Servers behind a unified application lifecycle. Review ProvideHTTPServer, Application.Server and Wire providers/generated code, main startup/Shutdown, DrainUsageJournal/cleanup, listener/connection ownership, bounded preface reads and queues. Avoid reflection/private registration hooks or copied internal adapters.

Acceptance must include real SETTINGS/windows; H1-before-H2 and H2-before-H1 idle; PING not extending H2 idle; active-stream survival and connection reuse; graceful active drain; immediate Close cancellation; H2C-off; slow/malformed prefaces and header/body limits; startup failures and concurrent/repeated shutdown; no leaked goroutines/FDs or unbounded queues. Compare throughput, latency and resource use with the approved baseline, with thresholds set before implementation. A future change needs a new SHA, source snapshot, complete CI/Security, real final image and native acceptance; old build results are not equivalent.

The existing `http2_compatibility_test.go` and `http_ingress_test.go` remain intact and must pass. Whole unit/integration, frontend, shell and security workflow scope remains unchanged. No production changes, migrations, paid upstream calls, billing replay or credential/helper adjustments are authorized by this exception.

References: https://pkg.go.dev/golang.org/x/net/http2#ConfigureServer ; https://pkg.go.dev/net/http#HTTP2Config ; https://golangci-lint.run/docs/linters/false-positives/ . Golangci-lint v2.14 base_rule.go requires all configured dimensions to match, supporting the exact two exclusions.
