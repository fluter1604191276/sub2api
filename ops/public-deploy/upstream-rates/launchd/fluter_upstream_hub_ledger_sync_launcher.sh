#!/bin/zsh
set -eu

RUN_DIR="/Users/fluter_claw/Library/Application Support/Fluter/upstream-ledger-sync"
PYTHON="/opt/homebrew/bin/python3"
LOCKDIR="/tmp/fluter-upstream-hub-ledger-sync.lock"
export DOCKER_HOST="unix:///Users/fluter_claw/.colima/default/docker.sock"
export PATH="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"

timestamp() {
  date "+%Y-%m-%dT%H:%M:%S%z"
}

if ! mkdir "${LOCKDIR}" 2>/dev/null; then
  echo "$(timestamp) skip: another upstream-hub ledger sync is already running"
  exit 0
fi

cleanup() {
  rmdir "${LOCKDIR}" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

echo "$(timestamp) start upstream-hub ledger sync"
cd "${RUN_DIR}"
"${PYTHON}" "${RUN_DIR}/sync_upstream_hub_snapshot_to_vps.py" \
  --hub-connection auto \
  --hub-psql-command "docker exec -i upstreamhub-postgres psql -U upstreamhub -d upstreamhub -At"
echo "$(timestamp) finish upstream-hub ledger sync"
