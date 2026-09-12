# Production Release Baseline

## Baseline Policy

For every future round of二开, query the live production image and
create a new development line from its labeled Git revision. The version, tag,
branch, and worktree recorded below are only the current point-in-time facts;
they must be replaced after every verified production switch. Never continue
development from this document's old path after production has changed.

Use `ops/public-deploy/create-production-derived-worktree.sh` to bootstrap the
next line and `ops/public-deploy/check-production-baseline.sh` to verify it.

## Current Production Baseline (2026-09-10)

Verified switch completed at approximately 15:15 UTC. This block supersedes
the historical 2026-09-09 baseline below.

~~~text
SSH alias: fluterapi-prod
Role: production
Directory: /www/sub2api
Image: fluter/sub2api:fluter-0.1.183-probe-daily-bills-20260910-r1
Image ID: sha256:c69d71603f5cdb806085b28aa9f05ae80a3895572945e1697b5550896ff4646f
Revision: 09ede4f7880a40b3aa4a0794e5d5c439fbd891fb
Source snapshot: 89bff5ed0f9bdb1d4cddcda1f2c5ea25aa090d98bfc6bd3510e45c713879cdbf
Manifest: /www/sub2api-builds/release-manifests/20260910-probe-daily-bills-r1/release-manifest.json
Rollback config: /www/sub2api/backups/pre-switch-daily-bills-20260910T151428Z
Database archive: /www/sub2api/backups/sub2api-backup-20260910T141330Z.tar.gz
Previous image: fluter/sub2api:fluter-0.1.183-smart-primary-score-20260910-r2
Local exact source: .worktrees/production-09ede4f78
Local evidence: .release-evidence/20260910-probe-daily-bills-r1
~~~

Native image capability gate, isolated authenticated smoke, public application
pages and admin authentication boundary passed. No paid upstream end-to-end
request was executed. Other service containers were not restarted.

Daily bill migrations 234/235 and actual usage correlation were verified in an
isolated full application. Account-rate costs use immutable usage snapshots;
historical uncorrelated spending remains unverified. Budget reservations and
user billing are unchanged. Prior image and configuration are retained.

In the previous smart-primary-score release, the first switch automatically rolled back because the smoke script incorrectly
assumed the static homepage domain exposed /health, and urllib received 403.
Verified existing routes use api.fluterapi.top/health (GET 200), while
fluterapi.top/health returns 404. Corrected curl checks passed on the retry.
Internal Docker smoke networks must be tested using their internal address;
published host ports were unreachable despite a healthy application.

## Historical Production Baseline (2026-09-09)

These values were read from the live node after the 2026-09-09 probe-budget
switch. Recheck before the next production operation:

~~~text
SSH alias: fluterapi-prod
Role marker: production
Production directory: /www/sub2api
Current image: fluter/sub2api:fluter-0.1.183-probe-budget-fix-20260909-r1
Current image digest (local image ID): sha256:935a1734a9875e92cb500609b5f76ab4b12b270600d397193f6726af6e820945
Current image revision: 6f71a42f5d36cfce69f6b1798b81a19f546c04cf
Current source snapshot: 86033da24f23daccbf6d24d51632b1063be0d772be3afea97b2781551bb6c87c
Previous image: fluter/sub2api:fluter-0.1.183-full-custom-20260909-budget-r2
Previous production digest: sha256:321eb362242d996a2941d3296323ba85c6a1679b2f49a0f7abd20c11156e0c08
Previous revision: 7a5709b8fdb7ec240b4acd500fdbe7c1bfd873aa
Original manifest: /www/sub2api-builds/release-manifests/20260909-probe-budget-fix-r1.json
Pre-switch database/archive backup: /www/sub2api/backups/sub2api-backup-20260909T073423Z.tar.gz
Pre-switch configuration backup: /www/sub2api/backups/pre-switch-20260909T0745Z
Compose/env: retained within the pre-switch full archive and configuration backup
Caddy: retained in the pre-switch configuration backup; no runtime change
Post-switch verification record: 2026-09-09; healthy, /health 200, public endpoints 200, admin boundary 401
Live recheck: 2026-09-09; production role, running/healthy, revision and snapshot match manifest
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
