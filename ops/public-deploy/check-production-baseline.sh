#!/usr/bin/env bash
set -euo pipefail

# Verify that the current source line descends from the image actually serving
# production. This is safe to run before development and before every build.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
PRODUCTION_ALIAS="${PRODUCTION_ALIAS:-fluterapi-prod}"

cd "${REPO_ROOT}"
COMMIT="$(git rev-parse HEAD)"

if [[ "${ALLOW_NON_PRODUCTION_BASELINE:-0}" == "1" ]]; then
  if [[ -z "${PRODUCTION_BASELINE_OVERRIDE_REASON:-}" ]]; then
    echo "REFUSED: PRODUCTION_BASELINE_OVERRIDE_REASON is required when bypassing the live baseline check" >&2
    exit 1
  fi
  echo "WARNING: live production baseline check bypassed: ${PRODUCTION_BASELINE_OVERRIDE_REASON}" >&2
  exit 0
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
if ! git cat-file -e "${live_revision}^{commit}" 2>/dev/null; then
  echo "REFUSED: production revision ${live_revision} is not available in this repository" >&2
  exit 1
fi
if ! git merge-base --is-ancestor "${live_revision}" "${COMMIT}"; then
  echo "REFUSED: HEAD ${COMMIT} does not descend from live production revision ${live_revision}" >&2
  echo "Create the next worktree from the live revision before developing or building." >&2
  exit 1
fi

printf 'production baseline verified: image=%s digest=%s revision=%s\n' \
  "${live_image}" "${live_digest}" "${live_revision}"
