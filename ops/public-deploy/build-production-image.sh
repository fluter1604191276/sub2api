#!/usr/bin/env bash
set -euo pipefail

# Production-only image builder. It binds the image to both the Git revision
# and a complete source snapshot so a descriptive tag cannot hide omissions.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
IMAGE_REF="${1:?usage: build-production-image.sh IMAGE_REF}"

cd "${REPO_ROOT}"

status="$(git status --short)"
if [[ -n "${status}" && "${ALLOW_DIRTY_SOURCE:-0}" != "1" ]]; then
  echo "REFUSED: production image build requires a clean worktree" >&2
  echo "Set ALLOW_DIRTY_SOURCE=1 only for an explicitly reviewed emergency snapshot." >&2
  exit 1
fi
if [[ -n "${status}" ]]; then
  echo "WARNING: building an explicitly acknowledged dirty source snapshot" >&2
fi

COMMIT="$(git rev-parse HEAD)"
SOURCE_SNAPSHOT="$(python3 - "${REPO_ROOT}" <<'PY'
import hashlib
import pathlib
import subprocess
import sys

root = pathlib.Path(sys.argv[1])
paths = subprocess.check_output(
    ["git", "ls-files", "-co", "--exclude-standard", "-z"],
    cwd=root,
).split(b"\0")
digest = hashlib.sha256()
for raw in sorted(path for path in paths if path):
    relative = raw.decode("utf-8")
    path = root / relative
    if not path.is_file():
        continue
    file_digest = hashlib.sha256(path.read_bytes()).hexdigest()
    digest.update(relative.encode("utf-8"))
    digest.update(b"\0")
    digest.update(file_digest.encode("ascii"))
    digest.update(b"\n")
print(digest.hexdigest())
PY
)"
DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
VERSION="${VERSION:-${IMAGE_REF##*:}}"

echo "building ${IMAGE_REF}"
echo "source commit: ${COMMIT}"
echo "source snapshot: ${SOURCE_SNAPSHOT}"

BUILD_ARGS=(
  --build-arg "GOPROXY=${GOPROXY:-https://goproxy.cn,direct}"
  --build-arg "GOSUMDB=${GOSUMDB:-sum.golang.google.cn}"
  --build-arg "VERSION=${VERSION}"
  --build-arg "COMMIT=${COMMIT}"
  --build-arg "DATE=${DATE}"
  --build-arg "SOURCE_SNAPSHOT=${SOURCE_SNAPSHOT}"
)
for base_arg in NODE_IMAGE GOLANG_IMAGE ALPINE_IMAGE POSTGRES_IMAGE; do
  if [[ -n "${!base_arg:-}" ]]; then
    BUILD_ARGS+=(--build-arg "${base_arg}=${!base_arg}")
  fi
done

docker build \
  --platform linux/amd64 \
  "${BUILD_ARGS[@]}" \
  -t "${IMAGE_REF}" \
  -f "${REPO_ROOT}/Dockerfile" \
  "${REPO_ROOT}"

docker image inspect "${IMAGE_REF}" --format \
  'image={{.Id}} arch={{.Architecture}} revision={{index .Config.Labels "org.opencontainers.image.revision"}} snapshot={{index .Config.Labels "org.opencontainers.image.source-snapshot"}}'
