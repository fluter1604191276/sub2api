# Public Deploy Ops

Operational helper scripts for the independent public VPS deployment.

## Release integrity

### Permanent baseline rule

Every new or modified二开 must start from the Git revision of the image that
is actually serving production at that time. This rule applies after every
production switch; it is not tied to `0.1.183`, a particular image tag, branch,
or local worktree name. Bootstrap the next line with
`create-production-derived-worktree.sh`, then let
`check-production-baseline.sh` recheck the live node before every build.

Production image switches require the capability inventory and evidence gate:

- docs/PRODUCTION-EXTENSIONS.md is the source-of-truth list of site-specific二开.
- docs/RELEASE-LINES.md is the source-of-truth index for active, production,
  and historical version lines.
- docs/RELEASE-BASELINE.md records the current production image and rollback contract.
- docs/RELEASE-CHECKLIST.md is the operator stop condition.
- docs/INCIDENT-20260828-production-switch.md records the recent release failure and
  the protocol compatibility boundary that health checks did not catch.
- generate-release-manifest.py creates a secret-free manifest for the exact source
  snapshot used to build an image.
- check-production-baseline.sh binds the next development line to the image
  that is actually serving production.
- create-production-derived-worktree.sh creates each new development line
  from the live production revision and records the immutable image identity.
- verify-release-bundle.sh blocks a candidate when source identity, required
  capabilities, immutable image identity, compiled-image markers, or test
  evidence is missing.
- build-production-image.sh binds the image to the source revision and snapshot
  labels, and refuses dirty worktrees unless an emergency exception is explicit.

The verifier is intentionally conservative. A healthy container is not capability
evidence, and a dirty worktree is not a release baseline. Do not mark
allow_release=true until the listed tests and image-level smoke checks have
actually passed.

## Git and worktree discipline

The production candidate is the exact clean Git commit named by its release
manifest. Before a release, inspect the branch, base, staged/unstaged changes,
and remote explicitly. Review files one by one and stage only the approved
paths. Do not use `git add .`, `git commit -a`, `git reset --hard`, `git clean`,
or `git checkout --` to resolve mixed state; those commands can hide or erase a
site-specific extension.

Each site-specific extension must have both a row in
`docs/PRODUCTION-EXTENSIONS.md` and a dated record under `docs/extensions/`.
The record must name its source files, routes/jobs, data and billing impact,
tests, image smoke evidence, and rollback path. Reports, databases, browser
snapshots, credentials, and local release evidence stay ignored or outside the
repository.

The normal sequence is:

1. Review and test the complete diff.
2. Make explicit Lore-protocol commits.
3. Push only the named feature branch; never force-push `main`.
4. Confirm the remote commit and a clean worktree.
5. Build the image from that clean commit with `build-production-image.sh`.
6. Generate and verify the secret-free manifest against the image labels and
   immutable digest.

If any capability, source identity, image label, test, or smoke result is
unknown, stop the release. A healthy container is necessary but does not prove
that the site's二开 survived.

## Backup

`backup-sub2api.sh` creates a private archive under `/www/sub2api/backups`.

The archive includes:

- PostgreSQL logical dump from the running `postgres` container.
- Production `.env` file.
- `docker-compose.yml`.
- `data/` and `redis_data/`, when present.

The archive contains secrets. Keep it private.

Run on the VPS:

```bash
/www/sub2api/scripts/backup-sub2api.sh
```

Retention defaults to 14 days. Override with:

```bash
RETENTION_DAYS=30 /www/sub2api/scripts/backup-sub2api.sh
```

## S2A Manager backup

`backup-s2a-manager.sh` creates a private archive under `/www/s2a-manager/backups`.
It includes:

- PostgreSQL logical dump from the running `s2a-manager-postgres` container.
- Production `.env` file.
- `docker-compose.yml`.
- `logs/settings.json`, when present.
- The runtime `source/` tree.

Run on the VPS:

```bash
/www/s2a-manager/scripts/backup-s2a-manager.sh
```

Retention defaults to 7 days.

Install the systemd units on the VPS:

```bash
sudo cp /path/to/s2a-manager-backup.service /etc/systemd/system/
sudo cp /path/to/s2a-manager-backup.timer /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now s2a-manager-backup.timer
```

## Upstream pricing and cost audit

The maintained upstream ledger tools live under
`ops/public-deploy/upstream-rates/`. The old Node.js examples are retired
and must not be used for release or pricing decisions.

The safe refresh entrypoint reads allowlisted upstream observations and
production metadata, writes only the independent ledger SQLite database, and
renders the read-only dashboard. It does not edit Sub2API accounts, groups,
channels, pricing, or user data:

```bash
sudo python3 /var/lib/fluterapi-upstream-rates/refresh_upstream_ledger.py \
  --local-postgres
```

The refresh includes public KBQ pricing, supported public pricing adapters,
read-only account/group snapshots, mapping preflight, the historical KBQ
true-cost audit, and the static dashboard render. Use `--fail-on-loss` as a
release or pricing-change gate. Use `--skip-kbq-audit` and related skip flags
only for a documented diagnostic run.

The individual maintained tools are:

- `refresh_upstream_ledger.py`: orchestrates the safe read-only refresh.
- `audit_kbq_configuration.py`: checks current mappings, reachable billing
  tiers, missing prices, and uncovered tool fees.
- `audit_kbq_true_costs.py`: reconstructs historical KBQ token-cost lower
  bounds from usage records and identifies confirmed loss buckets.
- `compare_ledger_with_site_truth.py`: compares ledger observations with the
  current site snapshot without changing production.
- `refresh_from_upstream_hub.py` and
  `sync_upstream_hub_snapshot_to_vps.py`: import a sanitized upstream-hub
  snapshot without copying credentials or raw upstream data.

For local development, run the tools from the repository root and provide
credentials only through the process environment when a diagnostic requires
them. Never commit or store upstream API keys, cookies, bearer tokens, or
database passwords in this repository.
