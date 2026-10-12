# Runtime security package patches (candidate only)

Capability ID: ops-baseline / official-0215-compatibility.
Routes/jobs: existing application and PostgreSQL client commands only.
Data/migrations/billing/scheduling impact: none.

This minimal Dockerfile change stays on Alpine 3.21 and names the final stage
`runtime`. It explicitly requires Alpine-maintained libcrypto3/libssl3 >=3.3.7-r2
and zlib >=1.3.2-r1, addressing 11 unique component CVEs detected in the e69
image by actual installed versions and official Alpine secdb. It does not change
Go/frontend dependencies, billing, quotas, migrations, PostgreSQL client major
version, or any production configuration. Existing embedded PostgreSQL 18.6
clients must be rechecked against the updated libraries in the final image.

Build the new clean commit with `--no-cache-filter runtime` through the original
interactive MBP Docker path. Do not reuse the e69 build script, tag, source
snapshot, or image digest as evidence for this new candidate. Never replace the
original credential helper, unlock the Keychain from automation, or silently
retry a denied build. No push or deployment is included in this change.

Validation must record the exact source SHA/snapshot, actual new image labels and
linux/amd64 digest, APK package versions/signature verification and fresh secdb
comparison, Go module and binary inventory, offline version/dynamic linkage,
non-root entrypoint, and synthetic restore/persistence tests when authorized.
Source-only checks or downloaded patched APKs are not a rebuilt-image pass.
The braces development advisory and remaining native/load/migration/financial
checks retain their separate evidence and disposition. No exemption is granted.

Rollback: preserve e69 and its existing evidence for development comparison.
A production binary rollback requires separate schema compatibility, current
backup and post-cutover transaction preservation review. No production action
or migration is authorized here. Release remains cancelled / allow_release=false.
