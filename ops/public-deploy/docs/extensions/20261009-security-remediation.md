# 2026-10-09 Security remediation follow-up

Capability: official compatibility / upstream transport / ops-baseline.
Base: 2d0db11b9aa1bcf90c1e36a0fe87f73d40754470, preserving quota policy B.
No migration, balance, price, idempotency or financial write-path change.

Fixable module-only security records use official minimum patched versions:
golang.org/x/image v0.45.0 and github.com/klauspost/compress v1.18.7.
Transport configuration migrates to public net/http Protocols and HTTP2Config,
preserving the OpenAI and long-stream PING intervals. Cancellation, active
streams and connection reuse receive loopback HTTP/2 regression coverage.
GOAWAY retry classification uses the existing net/http error handling, with
wrapped GOAWAY and cancellation-precedence regression cases.

Release builds retain the Go/ELF symbol table by removing only linker -s.
DWARF remains omitted with -w. This permits the unchanged govulncheck binary
scanner to inspect the actual artifact rather than fall back to module
metadata for a stripped executable. The binary is larger; its size and
actual symbol-table presence must be measured. This does not suppress a
finding or change scanner rules. Both the older stripped scan and same-source
symbol-bearing companion evidence are retained.

The ingress HTTP/2 server compatibility API remains unchanged pending a
reviewed design: public HTTP2Config does not support independently configured
HTTP/1 and HTTP/2 idle timeouts. Copying the unexported x/net/net/http adapter
protocol is not an official stable public API migration. No nolint, library
downgrade or silent timeout change is acceptable. Its remaining lint failures
remain explicit release blockers.

Evidence: .release-evidence/20261009-security-remediation-v2/.
Exact SHA tests, source and actual-binary scans, image smoke and synthetic E2E
must be rerun; earlier checks are not inherited. allow_release remains false.
No push, merge, deployment, production write or real-provider probes here.

Native amd64 validation proposal uses a separately verified nonproduction
legacy node only after capacity and resource isolation checks. Production is
never a load target. Current MacBook image execution is ARM64 emulation.

Official evidence:
https://pkg.go.dev/vuln/GO-2026-6222
https://pkg.go.dev/vuln/GO-2026-5841
https://github.com/golang/vuln/blob/v1.8.0/internal/vulncheck/binary.go
