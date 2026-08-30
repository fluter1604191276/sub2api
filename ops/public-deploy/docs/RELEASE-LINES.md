# Release Lines

This file is the short index for Sub2API release sources. It prevents a
historical worktree or image tag from being mistaken for the current production
source.

## Production Baseline

```text
Role: production
SSH alias: fluterapi-prod
Directory: /www/sub2api
Image: fluter/sub2api:fluter-0.1.171-cache-hit-rate-20260826-r3
Digest: sha256:ee5b428161ce0eb4f99f2ac26ffcfd7d9da3acc0ad8a6fbca2de50980dbc6c2a
```

The baseline is the rollback reference until a later release is explicitly
switched and verified. It is not a source checkout.

## Active Candidate Line

```text
Official baseline: v0.1.183
Official base commit: e8cb019fabf8b55199436229044cbf9aa7a82564
Branch: release/v0.1.183-fluter-full-custom-20260830
Worktree: /Users/fluter_claw/Documents/study_project/sub2api/.worktrees/public-0.1.183-full-custom-20260830
```

This is the only approved source for the current full-custom candidate. It
contains the site-specific scheduler, scheduled probe, quality and cache
telemetry, image cost, pricing calibration, model sync/filter, error
sanitization, Responses compatibility boundary, and release-integrity work.

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

1. Make changes only on the active candidate branch.
2. Keep the worktree clean before building.
3. Register each production extension in the inventory and a dated record.
4. Build only through `build-production-image.sh`.
5. Verify the manifest and immutable image identity before any production
   switch.
6. Keep the previous production image, Compose file, and backups until the new
   image passes post-switch verification.
