# Release Lines

This file is the short index for Sub2API release sources. It prevents a
historical worktree or image tag from being mistaken for the current production
source.

The entries below are point-in-time records, not permanent development
locations. After every production switch, query the live container, replace the
production baseline block, and create the next line from that new revision.

## Production Baseline (2026-09-22)

Current image: `fluter/sub2api:fluter-0.2.4-account-groups-20260922-r1`.
Current revision: `3b43110cedabce5c6bcb0966f5f082dae624e054`.
Current image ID: `sha256:941d399a182f57105851591b10768981ebb11c960bf9d0b96e1049f520b53da7`.
Source snapshot: `6a031974e8b6dcd80cbab85dafe7cde2e179c42a5631967babdf2d0ad0ddc7fb`.
Exact local source: `.worktrees/passive-capability-observation-20260922` (candidate source, preserved).
Evidence: `ops/public-deploy/release-manifests/20260922-account-groups-r1` (ignored local release evidence).
Remote manifest: `/www/sub2api-builds/release-manifests/20260922-account-groups-r1.json`.
Rollback image: `fluter/sub2api:fluter-0.2.4-deepseek-fixed-peak-20260918-r1` with digest `sha256:c8b2ad2879da22cc76f1b33026035ffcc361119c8c6b86af7bea857567233c09`.
Rollback config: `/www/sub2api/backups/pre-switch-account-groups-20260922T093617Z`.
Database archive: `/www/sub2api/backups/sub2api-backup-20260922T093730Z.tar.gz`.

Post-switch checks: production role, container healthy, `/health` 200, API
available channels/model plaza 200, unauthenticated admin boundary 401,
capability tables present, and no recent fatal/migration errors. PostgreSQL,
Redis and Caddy were not restarted. The next development line must derive from
this live-verified revision; documentation commits after it do not change the
deployed image identity.

## Historical Production Baseline (2026-09-18)

Image: `fluter/sub2api:fluter-0.2.4-deepseek-fixed-peak-20260918-r1`.
Revision: `39abf42abcce5d0d50e05673050704265da9c8c7`.
Image ID: `sha256:c8b2ad2879da22cc76f1b33026035ffcc361119c8c6b86af7bea857567233c09`.
Source snapshot: `6972b4a33283ea71a7f0fb328522ddeb13640b50962b2f868de2ff059ca08c62`.
Rollback predecessor: `fluter/sub2api:fluter-0.2.4-pricing-quality-20260917-r1`.

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

The baseline is the image currently serving production. The next development
line must be created from its `Revision`, and this block must be updated after
every verified production switch. It is a reference record, not a substitute
for live verification.

## Historical Production-Derived Development Line

```text
Base image: fluter/sub2api:fluter-0.1.183-probe-budget-fix-20260909-r1
Base digest: sha256:935a1734a9875e92cb500609b5f76ab4b12b270600d397193f6726af6e820945
Base revision: 6f71a42f5d36cfce69f6b1798b81a19f546c04cf
Branch: create the next line from the live revision after this switch
Worktree: create a new production-derived worktree; do not reuse the previous candidate path
```

This is the only approved source for the current round of二开 until production
changes again. It inherits the site-specific scheduler, scheduled probe,
quality and cache telemetry, image cost, pricing calibration, model
sync/filter, error sanitization, Responses compatibility boundary, and release
integrity work from the running image.

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
