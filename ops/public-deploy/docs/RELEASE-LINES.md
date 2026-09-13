# Release Lines

This file is the short index for Sub2API release sources. It prevents a
historical worktree or image tag from being mistaken for the current production
source.

The entries below are point-in-time records, not permanent development
locations. After every production switch, query the live container, replace the
production baseline block, and create the next line from that new revision.

## Production Baseline (2026-09-13)

Current image: `fluter/sub2api:fluter-0.2.4-currency-20260913-r1`.
Current revision: `5d4f620ba397ebe9d6c65b8919771f9e07219183`.
Current image ID: `sha256:13daa766e72ce58b14574143a27b8c7915e56a68ee75905422dfc8a87fc01775`.
Source snapshot: `c827f70b90bf70f04f63ed8b90c0640d0cee0bfcb42ce14c0be385d8b7ad13d2`.
Exact local source: `.worktrees/production-currency-20260913` (detached, preserved).
Evidence: `.release-evidence/20260913-pricing-display-currency-r1`.
Remote manifest: `/www/sub2api-builds/release-manifests/20260913-pricing-display-currency-r1/release-manifest.json`.
See RELEASE-BASELINE.md for remote manifest and rollback paths.

The next development line must derive from this live-verified revision, not
the historical line below. Documentation commits after this revision do not
change the deployed image identity.

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
