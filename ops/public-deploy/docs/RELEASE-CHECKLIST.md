# Production Release Checklist

This checklist is the stop condition for a Sub2API image switch. It exists because process state, not operator memory, must carry the site's二开.

## Before Build

- [ ] Confirm the worktree is the public deployment worktree.
- [ ] Confirm all intended二开 are listed in PRODUCTION-EXTENSIONS.md.
- [ ] Compare the candidate branch with the production baseline commit/tag.
- [ ] Require a clean worktree; if an emergency dirty snapshot is used, record the explicit exception and source snapshot hash.
- [ ] Ensure no user change is being overwritten or reset.
- [ ] Ensure every new/changed二开 has a dated registration under the release records directory.
- [ ] Record upstream version, migration impact, environment-variable impact, and Docker context.
- [ ] Confirm .dockerignore excludes operator snapshots, reports, credentials, and backup material from the build context.
- [ ] Confirm local_shell, custom, and other protocol-sensitive fixtures have an explicit route decision.

## Build And Verify

- [ ] Build for linux/amd64.
- [ ] Use ops/public-deploy/build-production-image.sh so the image carries the source revision and snapshot labels.
- [ ] Generate a manifest from the exact build worktree.
- [ ] Record image digest; do not use a descriptive tag as the only identity.
- [ ] Confirm image revision and source-snapshot labels match the manifest; a tag-only or unlabeled image is blocked.
- [ ] Verify every required capability in the extension inventory.
- [ ] Run targeted backend tests for changed behavior.
- [ ] Run frontend typecheck/build when frontend files or embedded assets changed.
- [ ] Run git diff --check.
- [ ] Run image-level smoke checks for health, authentication boundary, admin routes, scheduler/probe fields, quality/cache fields, and protocol fixtures.
- [ ] Stop if any result is unknown, partial without an explicit compatibility decision, or based only on process health.

## Before Switch

- [ ] Verify ssh fluterapi-prod 'cat /etc/fluterapi-node-role' returns exactly production.
- [ ] Back up database, Compose, env, current image identity, and relevant Caddy configuration.
- [ ] Confirm old image and rollback Compose remain available.
- [ ] Confirm disk headroom for the new image and backup.
- [ ] Review manifest decision and have the release archive ready.

## Switch

Use only the application service update:

~~~bash
docker compose up -d --no-deps sub2api
~~~

Do not run docker compose down for a normal image switch. Do not restart PostgreSQL, Redis, or Caddy unless the change explicitly requires it. Do not delete the previous image before post-release verification.

## After Switch

- [ ] Container is running and healthy.
- [ ] /health returns the expected JSON.
- [ ] Unauthenticated admin routes still return 401/403.
- [ ] Core admin route existence checks pass.
- [ ] Scheduler/probe/quality/cache fields are present in authenticated fixture responses.
- [ ] Minimal text and streaming requests pass.
- [ ] Responses tool fixtures do not silently lose required tools; unsupported route behavior is explicit.
- [ ] Billing and usage records remain within expected boundaries.
- [ ] Observe before declaring the release complete.

## Automatic Rollback Trigger

Rollback when a required capability is absent, a protocol fixture is silently dropped, the app is unhealthy, a core route is missing, or billing/session behavior is wrong. Restore the recorded Compose/image pair, keep the failed image for analysis, and attach the manifest to the incident record.
