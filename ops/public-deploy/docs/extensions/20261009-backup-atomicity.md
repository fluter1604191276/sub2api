# Backup archive publication failure atomicity

Capability ID: ops-baseline

A partial archive write previously left a corrupt `sub2api-backup-*.tar.gz` file
that the dashboard's name/mtime check could mistake for a fresh backup. The
script now writes to a private unique temporary file, validates nested and outer
archives and exact expected content, flushes, then atomically creates the final
name without replacement. A role/config/retention guard and an owned directory
lock precede the dump. Retention runs after publication only; all cleanup paths
are attempted and errors return nonzero. Deployment dotenv values cannot mutate
owned paths or retention controls.

Source: `ops/public-deploy/backup-sub2api.sh`.
CI: the existing release-helpers job runs backup and full operations regressions.
Job: existing invocation only; no timer/service or production change.
Data dependency: pg_dump, deployment files and optional data/Redis snapshots.
Billing/scheduling/client protocol impact: none.
Runtime dependencies: Linux Bash, tar/gzip/cmp/mktemp and GNU-compatible ln -T,
file-operand sync. Publication uses a same-filesystem hard link instead of rename
so an existing name can never be replaced. There is no migration or new config.

Regression: `ops/public-deploy/test_backup_sub2api.py` tests real Linux archive,
validation and publication with synthetic Docker output and injected partial,
late close, flush, permission, collision, directory/symlink publication, cleanup
and retention failures. Independent isolated real PostgreSQL and stopped Redis
RDB/AOF recovery evidence is outside the source tree in the candidate evidence.

First release manifest: none; local candidate only, allow_release=false.
Rollback: reverting this commit restores the original partial-final-name risk;
do not use it to roll back production data. Source/image/CI identity must be
revalidated for any subsequent release. Capacity, power-loss durability, live
snapshot consistency and complete production recovery are not claimed. Old
unattributed startup failure remains unknown.
