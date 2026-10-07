# Release Lines

This file is the short index for Sub2API release sources. It prevents a
historical worktree or image tag from being mistaken for the current production
source.

The entries below are point-in-time records, not permanent development
locations. After every production switch, query the live container, replace the
production baseline block, and create the next line from that new revision.

## Prepared Candidate (2026-10-08; not deployed)

User requested prepare-only today. Production remains r4 / 06d2674d660a65abb8f2bb0a63b6e032eb35c937; this entry does not change the production baseline.

- Release repository/branch: fluter1604191276/sub2api, candidate/security-20261008.
- Exact candidate commit: 9acf41b5a0f60522a28f0b5b76a83f996f4b373d.
- Source snapshot: efe57920e84e11c95e776c7f6176428b65f095a2dfdcf880f1c54cc4253f2686.
- Native linux/amd64 image: fluter/sub2api:candidate-0.2.13-9acf41b5a0f6-amd64.
- Local Docker immutable image digest: sha256:40e2f55c3f2a89c6ea31d72c5df1d50e858f9bc626b2f13732d9448b5b3d42a8. No registry publication was performed.
- Candidate image archive SHA256: 93964e268d294dc42f9dedf73c5fbafbb910fa3f4d488a662af347a5f6404a08.
- VPS evidence/manifest/archive: /www/sub2api/release-evidence/20261008-security-9acf41b5a/; release-manifest.candidate.json has allow_release=false.
- Local source: project/.worktrees/security-20261008. Clean source was pushed before building; VPS source came from an independent Git clone of that branch.
- Local handoff: .release-evidence/20261008-switch-handoff/CANDIDATE.md and vps-originals/. The public image archive and sanitized metadata are copied there; real production data/configuration/admin records/keys stay on the VPS.
- Vue family 3.5.42 and source-map-js 1.2.2 address the two newly reported high advisories. Security Scan 37657517757 passed. Fresh frontend suite: 2701 tests; image artifact checks: 229 passed; all 14 OCI blob hashes verified.
- Production dependency audit: 0 high/critical, 13 moderate/4 low. Full development audit still has 33 high/3 critical; no new exceptions added.
- CI 37657517744 still fails integration and Go lint, with the same 41 failing test nodes and 20 lint diagnostics as the old 1c candidate CI. Historical R16 default/unit/mock results do not cover the complete integration-tag suite. These failures remain uncorrected.
- Current source has 309 SQL files; a fresh synthetic DB matched their exact normalized checksums. The production upgrade ledger is expected to retain historical 173_account_stats_image_operation.sql and total 310 after the nine pending migrations. No production migration ran here.
- Previous 1c2ba1ca candidate is superseded by this patched source and image. Do not select it or the earlier bf candidate by a readable tag.
- Current r4 image remains the future same-database rollback object: sha256:4d4cc10e1720c3f789129c5ec3f4d122bd8f46904684e0c5a21e62d99fdad8c1. R17/R18 backup evidence remains on the production host; refresh the coordinated recovery point before any future switch.

A future switch needs a new user instruction, fresh live checks, resolution of CI failures, safe admission freeze/natural long-stream drain, verified independent recovery within 600 seconds, and a post-backup transaction preservation plan. The local switch-controller.py draft failed safety review and is hard-disabled; it was never uploaded or run on production. No maintenance, Caddy reload, Redis ACL change or Cloudflare rule change occurred.

## Production Baseline (2026-09-23)

Current image: fluter/sub2api:fluter-0.2.4-stream-sticky-escape-20260923-r4.
Current revision: 06d2674d660a65abb8f2bb0a63b6e032eb35c937.
Current image ID: sha256:4d4cc10e1720c3f789129c5ec3f4d122bd8f46904684e0c5a21e62d99fdad8c1.
Source snapshot: 96be23952093ee5181bb05e2b180f8b92de0c18acc646b9365379f5cdb13b0bc.
Live rechecked from MacBook Pro on 2026-10-01 Asia/Shanghai: production role and image labels match; app, PostgreSQL and Redis healthy.
Exact preserved source on MacBook Pro: /Users/Admin/Documents/study_project/sub2api/.worktrees/production-06d2674d6 (clean, detached, locked, snapshot matches original manifest).
Mac mini project/.worktrees/stream-sticky-escape-20260922 is now at 02a1a6777, with documentation changes after production; its current files are not the deployed snapshot.
Evidence: .release-evidence/20260923-stream-sticky-escape/release-manifest-r4.json.
Rollback image: fluter/sub2api:fluter-0.2.4-account-groups-20260922-r1 with image ID sha256:941d399a182f57105851591b10768981ebb11c960bf9d0b96e1049f520b53da7.
Rollback Compose remains at /www/sub2api/backups/docker-compose.pre-sticky-escape-20260923.yml.
Pre-switch archive moved to Mac mini: /Users/fluter_claw/Backups/fluterapi-sub2api/vps-archive-20260924T041013Z/sub2api-backup-20260923T034236Z.tar.gz (path observed 2026-10-01; re-locate and verify before restore).
Latest full archive observed on VPS: /www/sub2api/backups/sub2api-backup-20260930T033122Z.tar.gz.
Local entry and verification limits: LOCAL-MAC-HANDOFF.md. Original manifest unchanged; this is not a new release.
The stream sticky escape group control is available under Admin -> Groups -> Smart Scheduler.

## Historical Production Baseline (2026-09-18)

Historical image: `fluter/sub2api:fluter-0.2.4-pricing-quality-20260917-r1`.
Historical revision: `3be979bebe408c9732b590f3095d42f2a084a46f`.
Historical image ID: `sha256:1176651ce3e5b3356e8406caae82da0ecde3a04fc703d8f13b8fa26893965ac4`.
Source snapshot: `b25d7c0dcf766f16aed6f85b46c10c2a9d032db0115992543d2116961c4ea5ca`.
Exact local source: `.worktrees/pricing-quality-20260917` (clean, preserved).
Evidence: `.release-evidence/20260917-pricing-quality`.
Remote manifest: `/tmp/release-manifest-authorized-20260917.json` (verified on VPS; original manifest retained locally).
Rollback image: `fluter/sub2api:fluter-0.2.4-currency-20260915-r3` with digest `sha256:a281f7d661d19558b318637e682590e6bb81cca2bf490fb8e32135da3bb01f5d`.
See RELEASE-BASELINE.md for remote manifest and rollback paths.

This was the development baseline after the 2026-09-18 release. It is retained
for history and recovery; new work uses the revision verified from live
production. Documentation commits do not change deployed image identity.

## Historical Production Baseline (2026-09-09)

```text
Role: production
SSH alias: fluterapi-prod
Directory: /www/sub2api
Image: fluter/sub2api:fluter-0.1.183-probe-budget-fix-20260909-r1
Digest (local image ID): sha256:935a1734a9875e92cb500609b5f76ab4b12b270600d397193f6726af6e820945
Revision: 6f71a42f5d36cfce69f6b1798b81a19f546c04cf
Source snapshot: 86033da24f23daccbf6d24d51632b1063be0d772be3afea97b2781551bb6c87c
Manifest: /www/sub2api-builds/release-manifests/20260909-probe-budget-fix-r1.json
Switched: 2026-09-09 Asia/Shanghai; post-switch health and auth-boundary checks passed
Rechecked: 2026-09-09T04:15:48Z; production role, running/healthy, image labels match manifest
```

This block records the 2026-09-09 image and is historical evidence. It is not
the current development base. Use the live production revision and keep the
current baseline block above updated after every switch.

## Historical Production-Derived Development Line

```text
Base image: fluter/sub2api:fluter-0.1.183-probe-budget-fix-20260909-r1
Base digest: sha256:935a1734a9875e92cb500609b5f76ab4b12b270600d397193f6726af6e820945
Base revision: 6f71a42f5d36cfce69f6b1798b81a19f546c04cf
Branch: historical instruction to create a line after the 2026-09-09 switch
Worktree: historical development-line record; do not reuse as a current base
```

That historical line inherited scheduler, probe, quality/cache telemetry,
image cost, pricing, model filtering, error handling and protocol extensions.
Their current inventory is PRODUCTION-EXTENSIONS.md; the base for new work is
always the live production revision, not this historical line.

The release identity is always the complete tuple:

```text
branch + Git commit + source snapshot hash + image digest + release manifest
```

The readable image tag is not sufficient identity.

## Handoff Locations

Verified on the primary Mac on 2026-09-09:

- Project container (not a Git root): /Users/fluter_claw/Documents/study_project/sub2api
- Git common directory: project/.git under that container; its primary checkout is historical.
- Preserved exact production source: .worktrees/production-7a5709b8f (detached and worktree-locked).
- Persistent operations documents: .worktrees/ops-handoff-20260909, branch docs/sub2api-handoff-20260909.
- Original manifest: .release-evidence/20260909-budget-r2/release-manifest.json.

These paths are local discovery hints. Other devices use their own checkout
paths. Read NEW-SESSION-HANDOFF.md before selecting a development base.
Documentation commits on the release remote may be ahead of the running
revision. They do not represent a new deployed image.

## Historical Or Non-Release Sources

The following are retained for investigation, comparison, or rollback only:

```text
/Users/fluter_claw/Desktop/study_project/sub2api/project
/Users/fluter_claw/Desktop/study_project/sub2api/.worktrees/public-deploy
/Users/fluter_claw/Desktop/study_project/sub2api/.worktrees/public-0.1.144-fluter-merge
/Users/fluter_claw/Desktop/study_project/sub2api/.worktrees/public-0.1.146-fluter-prep
/Users/fluter_claw/Desktop/study_project/sub2api/.worktrees/public-update-20260623
```

The main checkout contains user changes. The other worktrees represent older
or partial preparation lines. Do not build or switch from them. Older image
tags such as `0.1.149`, `0.1.161`, and prior `0.1.171-*` tags remain rollback or
forensic artifacts unless a manifest explicitly identifies them as the chosen
baseline.

## Release Rule

1. Query the live production image and create the next branch from its revision.
2. Make changes only on that production-derived branch.
3. Keep the worktree clean before building.
4. Register each production extension in the inventory and a dated record.
5. Build only through `build-production-image.sh`; its live-baseline check is
   mandatory unless an emergency override has a written reason.
6. Verify the manifest, immutable image identity, and compiled-image capability
   smoke before any production switch.
7. Keep the previous production image, Compose file, and backups until the new
   image passes post-switch verification.
