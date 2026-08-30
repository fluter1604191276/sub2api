# Release Lines

This file is the short index for Sub2API release sources. It prevents a
historical worktree or image tag from being mistaken for the current production
source.

The entries below are point-in-time records, not permanent development
locations. After every production switch, query the live container, replace the
production baseline block, and create the next line from that new revision.

## Production Baseline

```text
Role: production
SSH alias: fluterapi-prod
Directory: /www/sub2api
Image: fluter/sub2api:fluter-0.1.183-full-custom-20260830-r1
Digest: sha256:3c5a393bc801008e88a846f90d4e927f2ce4335b0b1b0f90dead659e2bb60ffa
Revision: 224f53ce5ca93933cf7a0fabd700422e52fd0eeb
```

The baseline is the image currently serving production. The next development
line must be created from its `Revision`, and this block must be updated after
every verified production switch. It is a reference record, not a substitute
for live verification.

## Production-Derived Development Line

```text
Base image: fluter/sub2api:fluter-0.1.183-full-custom-20260830-r1
Base digest: sha256:3c5a393bc801008e88a846f90d4e927f2ce4335b0b1b0f90dead659e2bb60ffa
Base revision: 224f53ce5ca93933cf7a0fabd700422e52fd0eeb
Branch: feature/post-production-20260830
Worktree: /Users/fluter_claw/Documents/study_project/sub2api/.worktrees/public-from-production-20260830
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
