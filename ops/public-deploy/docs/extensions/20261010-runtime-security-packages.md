# r4 journal candidate runtime security package repair

Development-only follow-up to source bfbbfe3c09bef1fa7a1ed9c366acccf556b140f3. The old amd64 runtime manifest sha256:356f78a31a3da7c6969b34777245e8d9115ad3b4c1ecb79402cab96d56b7fc31 remains an unpatched historical artifact. This change does not restore the cancelled production switch or set allow_release=true.

## Minimal package scope

Keep Alpine3.21 and the existing Go/frontend/PostgreSQL builder versions. Require libcrypto3 and libssl3 >=3.3.7-r2, and zlib >=1.3.2-r1 in the final runtime stage. Its explicit name `runtime` permits a fresh package-install layer with `--no-cache-filter runtime`; no broad APK upgrade or OS major-version migration is introduced. Normal APK repository/package signature verification remains enabled. Record the exact resolved package versions and all transitive changes from the new actual image before accepting it.

The official Alpine3.21 main secdb and x86_64 APKINDEX checked on2026-10-10 list these three fixed packages. OpenSSL3.3.7-r2 covers package-level candidates CVE-2026-35189, CVE-2026-54872, CVE-2026-54875, CVE-2026-72897, CVE-2026-75804, CVE-2026-75805, CVE-2026-75806, CVE-2026-77696, CVE-2026-84782 and CVE-2026-84784. CVE-2026-54875 is specific to ARM64/RISC-V and does not apply to this amd64 artifact. zlib1.3.2-r1 covers CVE-2026-85091. These are distribution package thresholds, not proof that every affected operation is reachable in Sub2. Do not invent an upstream OpenSSL3.3.8 fix.

Sources: https://secdb.alpinelinux.org/v3.21/main.json and https://dl-cdn.alpinelinux.org/alpine/v3.21/main/x86_64/APKINDEX.tar.gz. Alpine3.21 main support is currently listed through2026-11-01 at https://alpinelinux.org/releases/; a future OS branch migration needs separate compatibility work.

## Preserved behavior and evidence limits

No business, billing, journal, financial-history, schema, frontend, Go dependency, HTTP/2 compatibility exclusion, CI/Security policy, entrypoint, user, port, persistence or service configuration changes. Keep pg_dump/psql/copied libpq18.6 and verify their actual versions/linkage in the new image; APK libpq17.11 and copied /usr/local/lib/libpq.so.5 are different files. The clients dynamically link the system OpenSSL/zlib libraries, so Go CGO=0 alone does not eliminate these package risks.

GO-2026-5932 remains a module-only x/crypto/OpenPGP record. The prior exact-source linux/amd64/CGO0/embed analysis found0called/0imported-package findings and actual ELF PCLN found no OpenPGP functions, but stripped binary govulncheck exit3 was retained. Recheck the new artifact and preserve raw scan exits; do not suppress the record. Known braces3.0.3 development HIGH GHSA-vfj7-8cjw-p6xm / CVE-2026-93687 remains disclosed and unwaived.

## Candidate acceptance and rollback

Commit this development repair and use its own clean SHA, full source snapshot, version tag, OCI index/runtime/config digests and archive/binary hashes. Complete that exact commit's full existing CI/Security and new actual-image APK/CVE/ELF/source binding checks. A successful source workflow does not prove the final runtime packages were upgraded. Regenerate the guarded MacBook local-interactive command for the new SHA/snapshot; never reuse or relax the old identity guards. Do not retry SSH Keychain unlocks or alter credential helpers.

Remaining release gates include actual image startup/entrypoint and synthetic PG/Redis backup/restore, native amd64 load, journal persistence/recovery, historical financial-record risk and upstream fee boundaries. Original legacy acceptance node identity/resources must be freshly verified after its SSH timeout is resolved; no production/alternate-node substitution. No production migration, paid upstream request, personal account, financial replay or rebilling is authorized by this repair.

Development rollback is reverting this Dockerfile change on the repair branch and making a separately identified artifact. The old manifest is retained as evidence, not promoted as a safe release. Future production rollback still requires the verified pre-switch production digest/Compose/backup plan; none is activated here.
