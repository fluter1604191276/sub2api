#!/usr/bin/env bash
set -euo pipefail

DEPLOY_DIR="${DEPLOY_DIR:-/www/sub2api}"
BACKUP_DIR="${BACKUP_DIR:-${DEPLOY_DIR}/backups}"
NODE_ROLE_FILE="${NODE_ROLE_FILE:-/etc/fluterapi-node-role}"
RETENTION_DAYS="${RETENTION_DAYS:-14}"
MANUAL_SQL_RETENTION_DAYS="${MANUAL_SQL_RETENTION_DAYS:-7}"
BACKUP_CLEANUP_MODE="${BACKUP_CLEANUP_MODE:-delete}"
TIMESTAMP="$(date -u +%Y%m%dT%H%M%SZ)"
WORK_DIR=""
TEMP_ARCHIVE=""
LOCK_DIR="${BACKUP_DIR}/.sub2api-backup.lock"
LOCK_HELD=false
ARCHIVE="${BACKUP_DIR}/sub2api-backup-${TIMESTAMP}.tar.gz"

if [[ ! -f "$NODE_ROLE_FILE" ]] || [[ "$(<"$NODE_ROLE_FILE")" != "production" ]]; then
  echo "refusing backup: ${NODE_ROLE_FILE} must contain exactly production" >&2
  exit 1
fi

cd "$DEPLOY_DIR"

if [[ ! -f .env ]]; then
  echo "missing ${DEPLOY_DIR}/.env" >&2
  exit 1
fi

# Reject invalid retention policy before dumping or publishing anything.
if [[ "$BACKUP_CLEANUP_MODE" != "delete" && "$BACKUP_CLEANUP_MODE" != "print" ]] ||
   [[ ! "$RETENTION_DAYS" =~ ^[0-9]+$ || ! "$MANUAL_SQL_RETENTION_DAYS" =~ ^[0-9]+$ ]]; then
  echo "invalid backup retention policy" >&2
  exit 1
fi

# All secret-bearing temporary files are private from their creation.
umask 077
mkdir -p "$BACKUP_DIR"
cleanup() {
  local status=$? cleanup_failed=false
  if [[ -n "$TEMP_ARCHIVE" ]] && ! rm -f -- "$TEMP_ARCHIVE"; then
    cleanup_failed=true
  fi
  if [[ -n "$WORK_DIR" ]] && ! rm -rf -- "$WORK_DIR"; then
    cleanup_failed=true
  fi
  if [[ "$LOCK_HELD" == true ]] && ! rmdir -- "$LOCK_DIR"; then
    cleanup_failed=true
  fi
  if [[ "$cleanup_failed" == true ]]; then
    echo "backup temporary cleanup failed; inspect owned temporary paths before retry" >&2
    [[ "$status" -ne 0 ]] || status=1
  fi
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# Serialize this script's dump/snapshot/retention operations. Never remove an
# unowned lock, including one left by SIGKILL or a previous host crash.
if ! mkdir "$LOCK_DIR"; then
  echo "refusing backup: another backup holds ${LOCK_DIR}" >&2
  exit 1
fi
LOCK_HELD=true
if [[ -e "$ARCHIVE" || -L "$ARCHIVE" ]]; then
  echo "refusing backup: archive already exists: ${ARCHIVE}" >&2
  exit 1
fi
WORK_DIR="$(mktemp -d "${BACKUP_DIR}/.tmp-${TIMESTAMP}.XXXXXX")"
TEMP_ARCHIVE="$(mktemp "${BACKUP_DIR}/.archive-${TIMESTAMP}.XXXXXX")"

cleanup_old_backups() {
  local action="-print"

  if [[ "$BACKUP_CLEANUP_MODE" == "delete" ]]; then
    action="-delete"
  elif [[ "$BACKUP_CLEANUP_MODE" != "print" ]]; then
    echo "invalid BACKUP_CLEANUP_MODE: ${BACKUP_CLEANUP_MODE}; expected print or delete" >&2
    exit 1
  fi

  echo "cleaning old daily archives older than ${RETENTION_DAYS} days (${BACKUP_CLEANUP_MODE})..."
  find "$BACKUP_DIR" -maxdepth 1 -type f -name 'sub2api-backup-*.tar.gz' -mtime "+${RETENTION_DAYS}" "$action"

  echo "cleaning manual SQL backups older than ${MANUAL_SQL_RETENTION_DAYS} days (${BACKUP_CLEANUP_MODE})..."
  find "$BACKUP_DIR" -maxdepth 1 -type f -name '*.sql' -mtime "+${MANUAL_SQL_RETENTION_DAYS}" "$action"
  find "$BACKUP_DIR" -maxdepth 1 -type f -name '*.sql.gz' -mtime "+${MANUAL_SQL_RETENTION_DAYS}" "$action"
}

echo "creating database dump..."
# Load application credentials only for pg_dump. Deployment .env cannot mutate
# the parent's retention policy or owned temporary/lock paths.
(
  readonly DEPLOY_DIR BACKUP_DIR NODE_ROLE_FILE RETENTION_DAYS MANUAL_SQL_RETENTION_DAYS
  readonly BACKUP_CLEANUP_MODE TIMESTAMP WORK_DIR TEMP_ARCHIVE LOCK_DIR LOCK_HELD ARCHIVE
  set -a
  # shellcheck disable=SC1091
  . "${DEPLOY_DIR}/.env"
  set +a
  docker compose -f "${DEPLOY_DIR}/docker-compose.yml" exec -T postgres \
    pg_dump -U "${POSTGRES_USER:-sub2api}" -d "${POSTGRES_DB:-sub2api}" \
    --clean --if-exists --no-owner --no-privileges
) > "${WORK_DIR}/postgres.sql"

if [[ ! -s "${WORK_DIR}/postgres.sql" ]]; then
  echo "refusing backup: empty database dump" >&2
  exit 1
fi

echo "copying deployment files..."
cp "${DEPLOY_DIR}/.env" "${WORK_DIR}/env"
cp "${DEPLOY_DIR}/docker-compose.yml" "${WORK_DIR}/docker-compose.yml"

if [[ -f "${DEPLOY_DIR}/.env.example" ]]; then
  cp "${DEPLOY_DIR}/.env.example" "${WORK_DIR}/env.example"
fi

if [[ -d "${DEPLOY_DIR}/data" ]]; then
  tar -C "$DEPLOY_DIR" --exclude='data/logs/*' -czf "${WORK_DIR}/data.tar.gz" data
fi

if [[ -d "${DEPLOY_DIR}/redis_data" ]]; then
  tar -C "$DEPLOY_DIR" -czf "${WORK_DIR}/redis_data.tar.gz" redis_data
fi

cat > "${WORK_DIR}/README.txt" <<EOF
Sub2API public deployment backup
Created UTC: ${TIMESTAMP}
Deploy dir: ${DEPLOY_DIR}

Contents:
- postgres.sql: PostgreSQL logical dump
- env: production environment file with secrets
- docker-compose.yml: runtime compose file
- data.tar.gz: application data directory, if present
- redis_data.tar.gz: Redis data directory, if present

Keep this archive private. It contains secrets.
EOF

echo "creating archive..."
# Capture the expected members before creating validation metadata. Validate
# nested snapshots too; a valid outer gzip alone cannot prove they are readable.
expected_files=("$WORK_DIR"/*)
for nested in data.tar.gz redis_data.tar.gz; do
  if [[ -f "${WORK_DIR}/${nested}" ]]; then
    gzip -t "${WORK_DIR}/${nested}"
    tar -tzf "${WORK_DIR}/${nested}" > /dev/null
  fi
done

tar -C "$WORK_DIR" -czf "$TEMP_ARCHIVE" .
gzip -t "$TEMP_ARCHIVE"
tar -tzf "$TEMP_ARCHIVE" | LC_ALL=C sort > "${WORK_DIR}/.archive-members"
{
  printf './\n'
  for member in "${expected_files[@]}"; do
    printf './%s\n' "${member##*/}"
  done
} | LC_ALL=C sort > "${WORK_DIR}/.expected-members"
cmp "${WORK_DIR}/.expected-members" "${WORK_DIR}/.archive-members"
# Stream each expected member without extracting another full backup onto disk.
# The member list rejects extras/duplicates; cmp rejects altered member bytes.
for member in "${expected_files[@]}"; do
  tar -xOzf "$TEMP_ARCHIVE" "./${member##*/}" | cmp - "$member"
done
chmod 600 "$TEMP_ARCHIVE"
# Linux sync with a file operand reports flush errors before publication.
sync "$TEMP_ARCHIVE"

# Same-directory hard-link publication is atomic and refuses any existing name.
# Unlike mv -n it cannot report success while silently retaining a collision,
# nor overwrite a valid older backup in a check-then-rename race.
ln -T -- "$TEMP_ARCHIVE" "$ARCHIVE"
rm -- "$TEMP_ARCHIVE"
TEMP_ARCHIVE=""

cleanup_old_backups

echo "backup created: $ARCHIVE"
ls -lh "$ARCHIVE"
