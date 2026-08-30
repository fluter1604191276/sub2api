#!/usr/bin/env bash
set -euo pipefail

# Create the next development line from the image that is serving production.
# This is the only supported bootstrap path for a new round of二开.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORKTREE_REPO="$(cd "${SCRIPT_DIR}/../.." && pwd)"
PRODUCTION_ALIAS="${PRODUCTION_ALIAS:-fluterapi-prod}"
WORKTREE_NAME="${1:?usage: create-production-derived-worktree.sh WORKTREE_NAME [BRANCH_NAME]}"
BRANCH_NAME="${2:-feature/from-production-${WORKTREE_NAME}}"

if [[ "${WORKTREE_NAME}" == */* ]]; then
  echo "REFUSED: WORKTREE_NAME must not contain '/'" >&2
  exit 1
fi
if [[ -z "${BRANCH_NAME}" ]]; then
  echo "REFUSED: BRANCH_NAME must not be empty" >&2
  exit 1
fi

MAIN_GIT_COMMON_DIR="$(git -C "${WORKTREE_REPO}" rev-parse --path-format=absolute --git-common-dir)"
MAIN_REPO="$(cd "${MAIN_GIT_COMMON_DIR}/.." && pwd)"
TARGET="${MAIN_REPO}/.worktrees/${WORKTREE_NAME}"

if [[ -e "${TARGET}" ]]; then
  echo "REFUSED: target worktree already exists: ${TARGET}" >&2
  exit 1
fi
if git -C "${MAIN_REPO}" show-ref --verify --quiet "refs/heads/${BRANCH_NAME}"; then
  echo "REFUSED: branch already exists: ${BRANCH_NAME}" >&2
  exit 1
fi

live_baseline="$(ssh "${PRODUCTION_ALIAS}" 'role=$(cat /etc/fluterapi-node-role)
docker inspect sub2api --format "${role}|{{.Config.Image}}|{{.Image}}|{{index .Config.Labels \"org.opencontainers.image.revision\"}}|{{index .Config.Labels \"org.opencontainers.image.source-snapshot\"}}"')"
IFS='|' read -r live_role live_image live_digest live_revision live_snapshot <<< "${live_baseline}"

if [[ "${live_role}" != "production" ]]; then
  echo "REFUSED: ${PRODUCTION_ALIAS} is not marked production" >&2
  exit 1
fi
if [[ ! "${live_digest}" =~ ^sha256:[0-9a-f]{64}$ ]]; then
  echo "REFUSED: production image has no valid immutable digest" >&2
  exit 1
fi
if [[ ! "${live_revision}" =~ ^[0-9a-f]{40}$ ]]; then
  echo "REFUSED: production image has no valid Git revision label" >&2
  exit 1
fi
if [[ ! "${live_snapshot}" =~ ^[0-9a-f]{64}$ ]]; then
  echo "REFUSED: production image has no valid source snapshot label" >&2
  exit 1
fi
if ! git -C "${MAIN_REPO}" cat-file -e "${live_revision}^{commit}" 2>/dev/null; then
  echo "REFUSED: production revision ${live_revision} is not available locally" >&2
  exit 1
fi

mkdir -p "$(dirname "${TARGET}")"
git -C "${MAIN_REPO}" worktree add -b "${BRANCH_NAME}" "${TARGET}" "${live_revision}"

printf 'production-derived worktree created:\n  image=%s\n  digest=%s\n  revision=%s\n  source_snapshot=%s\n  branch=%s\n  worktree=%s\n' \
  "${live_image}" "${live_digest}" "${live_revision}" "${live_snapshot}" "${BRANCH_NAME}" "${TARGET}"
