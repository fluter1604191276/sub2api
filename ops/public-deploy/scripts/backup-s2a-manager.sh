#!/usr/bin/env bash
set -euo pipefail

DEPLOY_DIR="${DEPLOY_DIR:-/www/s2a-manager}"
BACKUP_DIR="${BACKUP_DIR:-${DEPLOY_DIR}/backups}"
NODE_ROLE_FILE="${NODE_ROLE_FILE:-/etc/fluterapi-node-role}"
RETENTION_DAYS="${RETENTION_DAYS:-1}"
BACKUP_DIRECTORY_RETENTION_DAYS="${BACKUP_DIRECTORY_RETENTION_DAYS:-1}"
BACKUP_CLEANUP_MODE="${BACKUP_CLEANUP_MODE:-delete}"
TIMESTAMP="$(date -u +%Y%m%dT%H%M%SZ)"
WORK_DIR="${BACKUP_DIR}/.tmp-${TIMESTAMP}"
ARCHIVE="${BACKUP_DIR}/s2a-manager-backup-${TIMESTAMP}.tar.gz"

if [[ ! -f "$NODE_ROLE_FILE" ]] || [[ "$(<"$NODE_ROLE_FILE")" != "production" ]]; then
  echo "refusing backup: ${NODE_ROLE_FILE} must contain exactly production" >&2
  exit 1
fi

cd "$DEPLOY_DIR"

if [[ ! -f .env ]]; then
  echo "missing ${DEPLOY_DIR}/.env" >&2
  exit 1
fi

mkdir -p "$BACKUP_DIR" "$WORK_DIR"
chmod 700 "$BACKUP_DIR" "$WORK_DIR"

cleanup() {
  rm -rf "$WORK_DIR"
}
trap cleanup EXIT

set -a
# shellcheck disable=SC1091
. "${DEPLOY_DIR}/.env"
set +a

cleanup_old_backups() {
  local action="-print"

  if [[ "$BACKUP_CLEANUP_MODE" == "delete" ]]; then
    action="-delete"
  elif [[ "$BACKUP_CLEANUP_MODE" != "print" ]]; then
    echo "invalid BACKUP_CLEANUP_MODE: ${BACKUP_CLEANUP_MODE}; expected print or delete" >&2
    exit 1
  fi

  echo "daily archives are pruned only by the verified local retention sync"
}

cleanup_old_backup_directories() {
  local backup_dir

  echo "cleaning old release/rollback backup directories older than ${BACKUP_DIRECTORY_RETENTION_DAYS} days (${BACKUP_CLEANUP_MODE})..."
  while IFS= read -r -d '' backup_dir; do
    if [[ "$BACKUP_CLEANUP_MODE" == "delete" ]]; then
      rm -rf -- "$backup_dir"
    else
      printf '%s\n' "$backup_dir"
    fi
  done < <(
    find "$BACKUP_DIR" -mindepth 1 -maxdepth 1 -type d -mmin "+$((BACKUP_DIRECTORY_RETENTION_DAYS * 1440))" \
      \( -name 'pre-*' -o -name 'rollback-*' -o -name 'release-*' -o -name 'releases' \) \
      -print0
  )
}

cleanup_old_backup_misc() {
  local backup_item backup_name

  echo "cleaning old non-daily backup artifacts older than ${BACKUP_DIRECTORY_RETENTION_DAYS} days (${BACKUP_CLEANUP_MODE})..."
  while IFS= read -r -d '' backup_item; do
    backup_name="${backup_item##*/}"
    case "$backup_name" in
      s2a-manager-backup-*.tar.gz|s2a-manager-backup-*.tar.gz.sha256)
        continue
        ;;
    esac
    if [[ "$BACKUP_CLEANUP_MODE" == "delete" ]]; then
      rm -rf -- "$backup_item"
    else
      printf '%s\n' "$backup_item"
    fi
  done < <(
    find "$BACKUP_DIR" -mindepth 1 -maxdepth 1 -mmin "+$((BACKUP_DIRECTORY_RETENTION_DAYS * 1440))" -print0
  )
}

echo "dumping postgres database..."
docker compose -f "${DEPLOY_DIR}/docker-compose.yml" exec -T s2a-manager-postgres \
  pg_dump -U s2amanager -d s2amanager --clean --if-exists --no-owner --no-privileges \
  > "${WORK_DIR}/postgres.sql"

echo "copying deployment state..."
cp "${DEPLOY_DIR}/.env" "${WORK_DIR}/env"
cp "${DEPLOY_DIR}/docker-compose.yml" "${WORK_DIR}/docker-compose.yml"

if [[ -f "${DEPLOY_DIR}/logs/settings.json" ]]; then
  mkdir -p "${WORK_DIR}/logs"
  cp "${DEPLOY_DIR}/logs/settings.json" "${WORK_DIR}/logs/settings.json"
fi

if [[ -d "${DEPLOY_DIR}/source" ]]; then
  tar -C "$DEPLOY_DIR" \
    --exclude='backups' \
    --exclude='source/node_modules' \
    --exclude='source/.next' \
    -cf - source | tar -C "$WORK_DIR" -xf -
fi

cat > "${WORK_DIR}/README.txt" <<EOF
S2A Manager backup
Created UTC: ${TIMESTAMP}
Deploy dir: ${DEPLOY_DIR}

Contents:
- postgres.sql: PostgreSQL logical dump
- env: production environment file with secrets
- docker-compose.yml: runtime compose file
- logs/settings.json: app settings snapshot, when present
- source/: runtime source tree

Keep this archive private. It contains secrets.
EOF

echo "creating archive..."
tar -C "$WORK_DIR" -czf "$ARCHIVE" .
sha256sum "$ARCHIVE" > "${ARCHIVE}.sha256"
chmod 600 "$ARCHIVE" "${ARCHIVE}.sha256"

cleanup_old_backups
cleanup_old_backup_directories
cleanup_old_backup_misc

echo "backup created: $ARCHIVE"
ls -lh "$ARCHIVE" "${ARCHIVE}.sha256"
