# Production Release Baseline

## Baseline Policy

For every future round of二开, query the live production image and
create a new development line from its labeled Git revision. The version, tag,
branch, and worktree recorded below are only the current point-in-time facts;
they must be replaced after every verified production switch. Never continue
development from this document's old path after production has changed.

Use `ops/public-deploy/create-production-derived-worktree.sh` to bootstrap the
next line and `ops/public-deploy/check-production-baseline.sh` to verify it.

## Current Production Baseline

These values are the last verified post-rollback facts and must be rechecked before the next production operation:

~~~text
SSH alias: fluterapi-prod
Role marker: production
Production directory: /www/sub2api
Current image: fluter/sub2api:fluter-0.1.183-full-custom-20260905-generic-400-failover-r2
Current image digest: sha256:325ffd47738eb8e2d1aff5440f28dd19fd352c9b06a64f339eb19ff220cbc458
Current image revision: afc912e2d6d11293b155e69c3e76d2683212e34a
Current source snapshot: 56e4486c227d867351e4fa97f3a5a8c1eaac18284e9a2c0ce29c6189f88661dc
Previous production digest: sha256:3c5a393bc801008e88a846f90d4e927f2ce4335b0b1b0f90dead659e2bb60ffa
Pre-switch database/archive backup: /www/sub2api/backups/sub2api-backup-20260905T182550Z.tar.gz
Pre-switch Compose backup: /www/sub2api/backups/docker-compose-before-switch-20260905T184624Z.yml
Pre-switch Caddy backup: /www/sub2api/backups/caddy-before-switch-20260905T184502Z
Post-switch verification: 2026-09-06; container healthy, public health endpoints 200, admin boundary 401
~~~

The digest above is a recorded incident-review value. The next operator must query the running container and update the manifest; this document is not a substitute for live verification.

The next development worktree must be created from the live revision recorded
above. The main checkout, old version-specific release worktrees, and legacy
`public-deploy` worktree are not release inputs for the next change.

## Immutable Release Contract

The version-line index is maintained in `RELEASE-LINES.md`. The current
candidate must come from its active candidate line; the production baseline and
historical worktrees are evidence or rollback sources only.

Every candidate must have:

1. An exact Git commit, or an explicitly acknowledged dirty source snapshot with a complete content hash.
2. An image reference and immutable image digest.
3. linux/amd64 target architecture for the production VPS.
4. A manifest generated from the exact source used for the image.
5. A capability result for every entry in PRODUCTION-EXTENSIONS.md.
6. Backend/frontend/test results and git diff --check evidence.
7. The production baseline image digest and rollback artifacts.
8. A decision field that says allow_release: true; absence or false means stop.

Dirty worktrees are not automatically forbidden for local experimentation, but a dirty manifest is rejected by the production verifier unless the operator explicitly acknowledges it. For routine production, the policy is clean commit only.

## Baseline Verification

Read-only preflight:

~~~bash
cd /Users/fluter_claw/Documents/study_project/sub2api/.worktrees/public-from-production-20260830
git status --short --branch
git rev-parse HEAD
ssh fluterapi-prod 'test "$(cat /etc/fluterapi-node-role)" = production'
ssh fluterapi-prod 'cd /www/sub2api && docker compose ps'
ssh fluterapi-prod 'docker inspect sub2api --format "{{.Config.Image}} {{.Id}}"'
ssh fluterapi-prod 'df -h / /www'
~~~

The role marker is mandatory. Hostname, IP, and a successful SSH connection cannot replace it.

## Release Artifacts

Keep the following together outside the secret-bearing production directory or in an access-controlled release archive:

~~~text
release-manifest.json
image digest / registry inspection output
capability smoke-test output
backend test output
frontend typecheck/build output
git diff --check output
pre-switch Compose hash and database backup path
post-switch health and route checks
~~~

The manifest and test outputs must never contain credentials or full request bodies.
