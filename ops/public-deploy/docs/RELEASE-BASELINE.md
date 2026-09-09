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

These values were read from the live node and original manifest on 2026-09-09.
Recheck before the next production operation:

~~~text
SSH alias: fluterapi-prod
Role marker: production
Production directory: /www/sub2api
Current image: fluter/sub2api:fluter-0.1.183-full-custom-20260909-budget-r2
Current image digest (local image ID): sha256:321eb362242d996a2941d3296323ba85c6a1679b2f49a0f7abd20c11156e0c08
Current image revision: 7a5709b8fdb7ec240b4acd500fdbe7c1bfd873aa
Current source snapshot: a68e698e9f4e3519cac7da7c3b3cf9aa611dafa05a1fda368d53bd340019611f
Previous image: fluter/sub2api:fluter-0.1.183-full-custom-20260908-astra-r1
Previous production digest: sha256:d3303a7ab530c7f53c81c861f3f2cdc50c8adfc4fd6e48df7321a5f34ddc0bde
Previous revision: 3be658745bc28c252ee4b0357c8d98f75fcefa88
Original manifest: /www/sub2api-builds/release-manifests/20260909-budget-r2.json
Pre-switch database/archive backup: /www/sub2api/backups/sub2api-backup-20260909T014645Z.tar.gz
Pre-switch image record: /www/sub2api/backups/image-before-switch-20260909T014645Z.txt
Compose/env: retained within the pre-switch full archive
Caddy: no configuration change in this release; no new separate snapshot claimed
Post-switch verification record: 2026-09-09; container healthy, public endpoints 200, admin boundary 403
Live recheck: 2026-09-09T04:15:48Z; production role, running/healthy, revision and snapshot match manifest
~~~

The digest above is Docker's local image ID, not a registry RepoDigest. Verify
the live container against the immutable original manifest. Generate a new
manifest for a new build; never rewrite the old manifest to match newer docs.
Backup paths are dated recovery evidence; check retention/moved archives before
assuming any historical path still exists.

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
# Run in the verified checkout discovered via NEW-SESSION-HANDOFF.md.
git status --short --branch
git rev-parse HEAD
ssh fluterapi-prod 'test "$(cat /etc/fluterapi-node-role)" = production'
ssh fluterapi-prod 'cd /www/sub2api && docker compose ps'
ssh fluterapi-prod 'docker inspect sub2api --format "{{.Config.Image}} {{.Image}}"'
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
