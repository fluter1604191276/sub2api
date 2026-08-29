#!/usr/bin/env bash
set -euo pipefail

DEPLOY_DIR="${DEPLOY_DIR:-/www/sub2api}"
BACKUP_DIR="${BACKUP_DIR:-${DEPLOY_DIR}/backups}"
NODE_ROLE_FILE="${NODE_ROLE_FILE:-/etc/fluterapi-node-role}"
RETENTION_DAYS="${RETENTION_DAYS:-1}"
MANUAL_SQL_RETENTION_DAYS="${MANUAL_SQL_RETENTION_DAYS:-1}"
BACKUP_DIRECTORY_RETENTION_DAYS="${BACKUP_DIRECTORY_RETENTION_DAYS:-1}"
BACKUP_CLEANUP_MODE="${BACKUP_CLEANUP_MODE:-delete}"
COMPRESS_LARGE_SQL_MB="${COMPRESS_LARGE_SQL_MB:-1024}"
MIN_BACKUP_FREE_GB="${MIN_BACKUP_FREE_GB:-12}"
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

  echo "daily archives are pruned only by the verified local retention sync"

  echo "cleaning manual SQL backups older than ${MANUAL_SQL_RETENTION_DAYS} days (${BACKUP_CLEANUP_MODE})..."
  find "$BACKUP_DIR" -maxdepth 1 -type f -name '*.sql' -mmin "+$((MANUAL_SQL_RETENTION_DAYS * 1440))" "$action"
  find "$BACKUP_DIR" -maxdepth 1 -type f -name '*.sql.gz' -mmin "+$((MANUAL_SQL_RETENTION_DAYS * 1440))" "$action"
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
      sub2api-backup-*.tar.gz|sub2api-backup-*.tar.gz.sha256)
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

compress_large_manual_sql() {
  if ! command -v gzip >/dev/null 2>&1; then
    echo "gzip not found; skip large manual SQL compression" >&2
    return 0
  fi

  echo "compressing large manual SQL backups over ${COMPRESS_LARGE_SQL_MB} MB before backup..."
  find "$BACKUP_DIR" -maxdepth 1 -type f -name '*.sql' -size +"${COMPRESS_LARGE_SQL_MB}"M -print0 |
    while IFS= read -r -d '' sql_file; do
      if [[ -f "${sql_file}.gz" ]]; then
        echo "skip existing compressed copy: ${sql_file}.gz"
        continue
      fi
      local tmp_file="${sql_file}.gz.tmp"
      echo "compressing ${sql_file}"
      gzip -1 -c "$sql_file" > "$tmp_file"
      gzip -t "$tmp_file"
      rm -f "$sql_file"
      mv "$tmp_file" "${sql_file}.gz"
    done
}

ensure_backup_free_space() {
  local free_kb required_kb
  free_kb="$(df -Pk "$BACKUP_DIR" | awk 'NR==2 {print $4}')"
  required_kb=$((MIN_BACKUP_FREE_GB * 1024 * 1024))
  if (( free_kb < required_kb )); then
    echo "not enough free space in ${BACKUP_DIR}: free $((free_kb / 1024 / 1024))G, require ${MIN_BACKUP_FREE_GB}G" >&2
    exit 1
  fi
}

compress_large_manual_sql
ensure_backup_free_space

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
cleanup_old_backup_directories
cleanup_old_backup_misc

echo "backup created: $ARCHIVE"
ls -lh "$ARCHIVE"
