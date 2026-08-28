#!/bin/zsh
set -eu

WORKDIR="/Users/fluter_claw/Desktop/study_project/sub2api/.worktrees/public-deploy"
SCRIPT="ops/public-deploy/upstream-rates/sync_upstream_hub_snapshot_to_vps.py"
LOCKDIR="/tmp/fluter-upstream-hub-ledger-sync.lock"
PYTHON="/opt/homebrew/bin/python3"

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
cd "${WORKDIR}"
"${PYTHON}" "${SCRIPT}"
echo "$(timestamp) finish upstream-hub ledger sync"
