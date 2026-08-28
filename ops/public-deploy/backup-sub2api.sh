#!/usr/bin/env bash
set -euo pipefail

DEPLOY_DIR="${DEPLOY_DIR:-/www/sub2api}"
BACKUP_DIR="${BACKUP_DIR:-${DEPLOY_DIR}/backups}"
NODE_ROLE_FILE="${NODE_ROLE_FILE:-/etc/fluterapi-node-role}"
RETENTION_DAYS="${RETENTION_DAYS:-14}"
MANUAL_SQL_RETENTION_DAYS="${MANUAL_SQL_RETENTION_DAYS:-7}"
BACKUP_CLEANUP_MODE="${BACKUP_CLEANUP_MODE:-delete}"
TIMESTAMP="$(date -u +%Y%m%dT%H%M%SZ)"
WORK_DIR="${BACKUP_DIR}/.tmp-${TIMESTAMP}"
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

mkdir -p "$WORK_DIR"
chmod 700 "$WORK_DIR"

cleanup() {
  rm -rf "$WORK_DIR"
}
trap cleanup EXIT

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

set -a
# shellcheck disable=SC1091
. "${DEPLOY_DIR}/.env"
set +a

POSTGRES_USER="${POSTGRES_USER:-sub2api}"
POSTGRES_DB="${POSTGRES_DB:-sub2api}"

echo "creating database dump..."
docker compose -f "${DEPLOY_DIR}/docker-compose.yml" exec -T postgres \
  pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" --clean --if-exists --no-owner --no-privileges \
  > "${WORK_DIR}/postgres.sql"

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
tar -C "$WORK_DIR" -czf "$ARCHIVE" .
chmod 600 "$ARCHIVE"

cleanup_old_backups

echo "backup created: $ARCHIVE"
ls -lh "$ARCHIVE"
