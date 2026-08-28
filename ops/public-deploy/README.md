# Public Deploy Ops

Operational helper scripts for the independent public VPS deployment.

## Release integrity

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
- verify-release-bundle.sh blocks a candidate when source identity, required
  capabilities, immutable image identity, or test evidence is missing.
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

## KBQ upstream pricing table

`scripts/generate-kbq-pricing-table.mjs` reads KBQ upstream data and generates
a local price report without changing production config.

It reads:

- `GET /v1/models` to confirm the models visible to the current upstream key.
- `GET /api/pricing` to calculate model pricing and ratios.

Run from this repository root:

```bash
KBQ_API_KEY='sk-...' node ops/public-deploy/scripts/generate-kbq-pricing-table.mjs
```

Generated files:

- `ops/public-deploy/reports/kbq-openai-anthropic-pricing.md`
- `ops/public-deploy/reports/kbq-openai-anthropic-pricing.json`

Generate a token-only report that excludes per-call models:

```bash
KBQ_API_KEY='sk-...' node ops/public-deploy/scripts/generate-kbq-pricing-table.mjs \
  --token-only \
  --output ops/public-deploy/reports/kbq-openai-anthropic-token-pricing.md \
  --json-output ops/public-deploy/reports/kbq-openai-anthropic-token-pricing.json
```

For a pricing-only preview that does not call `/v1/models`:

```bash
node ops/public-deploy/scripts/generate-kbq-pricing-table.mjs --pricing-only
```

Do not commit or store upstream API keys in this repo.

## Upstream cost audit

`scripts/audit-upstream-costs.mjs` creates a read-only report for all active
upstream accounts in the public production database.

It does not select or print API keys. It reads only account names, base URLs,
model mappings, recorded account multipliers, and group bindings.

Run from this repository root:

```bash
node ops/public-deploy/scripts/audit-upstream-costs.mjs
```

Generated files:

- `ops/public-deploy/reports/upstream-cost-audit.md`
- `ops/public-deploy/reports/upstream-cost-audit.json`

What it can do automatically:

- Read production account/group metadata through SSH.
- Probe public NewAPI-style pricing endpoints such as `/api/pricing`.
- Convert supported token prices into Fluter's cost-ratio convention:
  `upstream actual price / official baseline price`.
- Mark rows as `OK`, `WATCH`, `RISK`, `MISSING`, `PER_CALL`, or `NO_BASELINE`.

What it cannot safely do by itself:

- Log into upstream websites.
- Read upstream API keys from production.
- Infer private per-key group multipliers when the upstream only shows them
  inside a logged-in dashboard.

When a host appears under "needs manual confirmation", log into that upstream
dashboard and check the API key group multiplier, model price page, or recent
billing logs. For image-generation-only accounts, use small real calls and
upstream billing records to update the account notes.

Useful options:

```bash
node ops/public-deploy/scripts/audit-upstream-costs.mjs --timeout-ms 15000
node ops/public-deploy/scripts/audit-upstream-costs.mjs --include-inactive
node ops/public-deploy/scripts/audit-upstream-costs.mjs --no-probe
```
