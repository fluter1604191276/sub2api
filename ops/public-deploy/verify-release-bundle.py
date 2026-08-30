#!/usr/bin/env python3
"""Verify a Sub2API release manifest and its source capability evidence.

This is a pre-switch gate.  It is intentionally conservative: missing evidence
is an error, and a healthy container is not treated as capability evidence.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import re
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Iterable


REQUIRED_CAPABILITIES = (
    "scheduler",
    "scheduled-probe",
    "quality-score",
    "cache-hit-rate",
    "image-cost",
    "pricing-calibration",
    "model-sync-filter",
    "error-passthrough",
    "responses-tools",
    "upstream-ledger",
    "ops-baseline",
)

CAPABILITY_FILES = {
    "scheduler": (
        "backend/internal/service/gateway_scheduling.go",
        "backend/internal/service/openai_account_scheduler.go",
        "backend/internal/service/smart_scheduler_routing.go",
        "backend/internal/service/smart_scheduler_preview.go",
        "backend/internal/service/smart_scheduler_routing_integration_test.go",
        "frontend/src/views/admin/AccountsView.vue",
        "frontend/src/views/admin/GroupsView.vue",
    ),
    "scheduled-probe": (
        "backend/internal/service/scheduled_test_runner_service.go",
        "backend/internal/service/scheduled_test_service.go",
        "backend/internal/handler/admin/scheduled_test_handler.go",
        "frontend/src/api/admin/scheduledTests.ts",
    ),
    "quality-score": (
        "backend/internal/pkg/usagestats/account_stats.go",
        "backend/internal/handler/admin/account_handler.go",
        "frontend/src/views/admin/AccountsView.vue",
    ),
    "cache-hit-rate": (
        "backend/internal/pkg/usagestats/account_stats.go",
        "backend/internal/repository/usage_log_repo_stats.go",
        "backend/internal/service/account_usage_service.go",
        "backend/internal/handler/admin/account_handler.go",
        "backend/internal/server/routes/admin.go",
        "frontend/src/api/admin/accounts.ts",
        "frontend/src/types/index.ts",
        "frontend/src/views/admin/AccountsView.vue",
        "frontend/src/views/admin/__tests__/AccountsView.cacheHitRate.spec.ts",
    ),
    "image-cost": (
        "backend/internal/service/account_stats_image_pricing.go",
        "backend/internal/service/account_stats_image_pricing_test.go",
        "frontend/src/components/admin/channel/accountStatsImageCost.ts",
    ),
    "pricing-calibration": (
        "backend/internal/service/channel_model_calibration.go",
        "backend/internal/repository/channel_repo_pricing.go",
        "backend/internal/server/routes/admin.go",
    ),
    "model-sync-filter": (
        "backend/internal/service/account_model_sync.go",
        "backend/internal/server/routes/admin.go",
        "frontend/src/api/admin/accounts.ts",
        "frontend/src/components/admin/account/AccountTableFilters.vue",
    ),
    "error-passthrough": (
        "backend/internal/service/error_passthrough_service.go",
        "backend/internal/service/error_passthrough_runtime.go",
        "backend/internal/handler/admin/error_passthrough_handler.go",
        "backend/internal/server/routes/admin.go",
    ),
    "responses-tools": (
        "backend/internal/pkg/apicompat/responses_to_anthropic_request.go",
        "backend/internal/pkg/apicompat/responses_to_anthropic.go",
        "backend/internal/pkg/apicompat/chatcompletions_responses_bridge.go",
        "backend/internal/pkg/apicompat/responses_to_anthropic_tools_test.go",
        "backend/internal/pkg/apicompat/responses_to_chatcompletions_codex_events_test.go",
    ),
    "upstream-ledger": (
        "ops/public-deploy/upstream-rates/README.md",
        "ops/public-deploy/upstream-rates/refresh_upstream_ledger.py",
        "ops/public-deploy/upstream-rates/audit_kbq_true_costs.py",
        "ops/public-deploy/upstream-rates/test_refresh_upstream_ledger.py",
    ),
    "ops-baseline": (
        "ops/public-deploy/README.md",
        "ops/public-deploy/backup-sub2api.sh",
        "ops/public-deploy/build-production-image.sh",
        "ops/public-deploy/docs/PRODUCTION-EXTENSIONS.md",
        "ops/public-deploy/docs/RELEASE-BASELINE.md",
    ),
}

REQUIRED_ROUTES = (
    "accounts.POST(\"/sync/models\"",
    "accounts.POST(\"/quality-stats/batch\"",
    "accounts.POST(\"/cache-hit-stats/batch\"",
    "plans.POST(\"\"",
    "channels.GET(\"/model-calibration/preview\"",
    "channels.POST(\"/model-calibration/apply\"",
)

REQUIRED_TESTS = (
    "backend",
    "frontend",
    "diff_check",
    "protocol_fixtures",
    "image_smoke",
)

# These markers are checked in the final image, not just in the source tree.
# They are deliberately small, stable runtime symbols/log labels rather than
# version strings, so a stale or partially merged image cannot pass on its tag.
IMAGE_CAPABILITY_MARKERS = {
    "scheduler": ("smart_scheduler", "sticky.smart_scheduler_switched"),
    "scheduled-probe": ("scheduled-test-plans", "recovery_probe"),
    "quality-score": ("quality_score", "quality_grade"),
    "cache-hit-rate": ("cache_hit_rate",),
    "image-cost": ("account_stats_image_pricing", "image_generation_call"),
    "pricing-calibration": ("model_calibration",),
    "model-sync-filter": ("sync/models",),
    "error-passthrough": ("error_passthrough",),
    "responses-tools": ("custom_tool_call", "response.custom_tool_call_input.delta"),
}


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def run_git(repo_root: Path, *args: str) -> str:
    result = subprocess.run(
        ["git", "-C", str(repo_root), *args],
        check=True,
        capture_output=True,
        text=True,
    )
    return result.stdout


def source_files(repo_root: Path) -> list[str]:
    raw = run_git(repo_root, "ls-files", "-co", "--exclude-standard", "-z")
    return sorted(item for item in raw.split("\0") if item)


def current_snapshot_hash(repo_root: Path) -> str:
    digest = hashlib.sha256()
    for relative in source_files(repo_root):
        path = repo_root / relative
        if not path.is_file():
            continue
        digest.update(relative.encode("utf-8"))
        digest.update(b"\0")
        digest.update(sha256_file(path).encode("ascii"))
        digest.update(b"\n")
    return digest.hexdigest()


def file_text(repo_root: Path, relative: str) -> str:
    path = repo_root / relative
    try:
        return path.read_text(encoding="utf-8")
    except (OSError, UnicodeDecodeError):
        return ""


def validate_manifest_structure(manifest: dict[str, object]) -> list[str]:
    errors: list[str] = []
    if manifest.get("schema_version") != 1:
        errors.append("manifest schema_version must be 1")
    release = manifest.get("release")
    if not isinstance(release, dict):
        return errors + ["manifest release object is missing"]
    digest = str(release.get("image_digest", ""))
    if not digest.startswith("sha256:") or len(digest) != len("sha256:") + 64:
        errors.append("release.image_digest must be an immutable sha256 digest")
    previous = str(release.get("previous_image_digest", ""))
    if not previous.startswith("sha256:") or len(previous) != len("sha256:") + 64:
        errors.append("release.previous_image_digest must be an immutable sha256 digest")
    if release.get("architecture") != "linux/amd64":
        errors.append("release.architecture must be linux/amd64")
    if release.get("allow_release") is not True:
        errors.append("release.allow_release is not true")
    image_inspection = release.get("image_inspection")
    if not isinstance(image_inspection, dict):
        errors.append("release.image_inspection is missing; inspect the candidate image")
    else:
        for key in ("image_digest", "architecture", "revision_label", "source_snapshot_label"):
            if not str(image_inspection.get(key, "")).strip():
                errors.append(f"release.image_inspection.{key} is missing")
    source = manifest.get("source")
    if not isinstance(source, dict):
        errors.append("manifest source object is missing")
    tests = manifest.get("tests")
    if not isinstance(tests, dict):
        errors.append("manifest tests object is missing")
    else:
        for key in REQUIRED_TESTS:
            if tests.get(key) != "passed":
                errors.append(f"test {key} is not marked passed")
    capabilities = manifest.get("capabilities")
    if not isinstance(capabilities, dict):
        errors.append("manifest capabilities object is missing")
    else:
        for capability_id in REQUIRED_CAPABILITIES:
            item = capabilities.get(capability_id)
            if not isinstance(item, dict):
                errors.append(f"capability {capability_id} is missing")
                continue
            status = item.get("status")
            evidence = item.get("evidence")
            if status not in {"present", "partial"}:
                errors.append(f"capability {capability_id} status is {status!r}")
            if not isinstance(evidence, list) or not evidence:
                errors.append(f"capability {capability_id} has no evidence")
            if status == "partial" and not str(item.get("compatibility_decision", "")).strip():
                errors.append(f"capability {capability_id} is partial without compatibility_decision")
    return errors


def validate_source_capabilities(repo_root: Path) -> list[str]:
    errors: list[str] = []
    for capability_id, files in CAPABILITY_FILES.items():
        missing = [relative for relative in files if not (repo_root / relative).is_file()]
        if missing:
            errors.append(f"capability {capability_id} missing source files: {', '.join(missing)}")
    routes = file_text(repo_root, "backend/internal/server/routes/admin.go")
    for marker in REQUIRED_ROUTES:
        if marker not in routes:
            errors.append(f"required admin route marker missing: {marker}")
    responses = "\n".join(
        file_text(repo_root, relative)
        for relative in CAPABILITY_FILES["responses-tools"]
    )
    for marker in ("local_shell", "custom_tool_call", "response.custom_tool_call_input.delta"):
        if marker not in responses:
            errors.append(f"responses tool marker missing: {marker}")
    return errors


def validate_source_identity(repo_root: Path, manifest: dict[str, object]) -> list[str]:
    errors: list[str] = []
    source = manifest.get("source")
    if not isinstance(source, dict):
        return errors
    try:
        current_head = run_git(repo_root, "rev-parse", "HEAD").strip()
        current_status = run_git(repo_root, "status", "--short").splitlines()
    except (subprocess.CalledProcessError, OSError) as exc:
        return [f"cannot inspect git source identity: {exc}"]
    if source.get("head") != current_head:
        errors.append("manifest source.head does not match current HEAD")
    if bool(source.get("dirty")) != bool(current_status):
        errors.append("manifest source.dirty does not match current worktree")
    if bool(source.get("dirty")) and source.get("dirty_acknowledged") is not True:
        errors.append("dirty source requires source.dirty_acknowledged=true")
    expected_snapshot = source.get("snapshot_sha256")
    if expected_snapshot != current_snapshot_hash(repo_root):
        errors.append("manifest source.snapshot_sha256 does not match current source")
    return errors


def validate_manifest(
    repo_root: Path,
    manifest: dict[str, object],
    *,
    acknowledge_dirty: bool = False,
    image_metadata: dict[str, object] | None = None,
) -> list[str]:
    errors = validate_manifest_structure(manifest)
    errors.extend(validate_source_identity(repo_root, manifest))
    errors.extend(validate_source_capabilities(repo_root))
    source = manifest.get("source")
    if isinstance(source, dict) and source.get("dirty") and not acknowledge_dirty:
        errors.append("current source is dirty; pass --acknowledge-dirty only for an explicitly reviewed exception")
    if image_metadata:
        release = manifest.get("release") or {}
        if image_metadata.get("architecture") not in {"amd64", "linux/amd64"}:
            errors.append("inspected image architecture is not amd64")
        inspected_digest = str(image_metadata.get("image_digest", ""))
        if inspected_digest and inspected_digest != release.get("image_digest"):
            errors.append("manifest image digest does not match inspected image")
        source = manifest.get("source") or {}
        if image_metadata.get("revision_label") != source.get("head"):
            errors.append("inspected image revision label does not match manifest source.head")
        if image_metadata.get("source_snapshot_label") != source.get("snapshot_sha256"):
            errors.append("inspected image source snapshot label does not match manifest source.snapshot_sha256")
        image_capabilities = image_metadata.get("capability_markers")
        if not isinstance(image_capabilities, dict):
            errors.append("image capability smoke evidence is missing")
        else:
            for capability_id in IMAGE_CAPABILITY_MARKERS:
                result = image_capabilities.get(capability_id)
                if not isinstance(result, dict) or result.get("status") != "present":
                    errors.append(f"image capability smoke failed: {capability_id}")
    return errors


def inspect_image(image: str) -> dict[str, object]:
    result = subprocess.run(
        ["docker", "image", "inspect", image],
        check=True,
        capture_output=True,
        text=True,
    )
    payload = json.loads(result.stdout)
    item = payload[0]
    repo_digests = item.get("RepoDigests") or []
    digest = repo_digests[0] if repo_digests else item.get("Id", "")
    if "@" in digest:
        digest = digest.rsplit("@", 1)[1]
    labels = item.get("Config", {}).get("Labels") or {}
    return {
        "image_digest": digest,
        "architecture": item.get("Architecture", ""),
        "revision_label": labels.get("org.opencontainers.image.revision", ""),
        "source_snapshot_label": labels.get("org.opencontainers.image.source-snapshot", ""),
    }


def inspect_binary_capabilities(payload: bytes) -> dict[str, dict[str, object]]:
    printable = b"\0".join(re.findall(rb"[\x20-\x7e]{4,}", payload))
    results: dict[str, dict[str, object]] = {}
    for capability_id, markers in IMAGE_CAPABILITY_MARKERS.items():
        matched = [marker for marker in markers if marker.encode("ascii") in printable]
        results[capability_id] = {
            "status": "present" if len(matched) == len(markers) else "missing",
            "matched": matched,
            "required": list(markers),
        }
    return results


def inspect_image_capabilities(image: str) -> dict[str, dict[str, object]]:
    """Inspect the compiled application binary without starting the service."""
    result = subprocess.run(
        ["docker", "create", "--entrypoint", "/bin/true", image],
        check=True,
        capture_output=True,
        text=True,
    )
    container_id = result.stdout.strip()
    if not container_id:
        raise ValueError(f"docker returned no container for {image}")
    try:
        with tempfile.TemporaryDirectory(prefix="sub2api-image-smoke-") as directory:
            binary = Path(directory) / "sub2api"
            subprocess.run(
                ["docker", "cp", f"{container_id}:/app/sub2api", str(binary)],
                check=True,
                capture_output=True,
                text=True,
            )
            return inspect_binary_capabilities(binary.read_bytes())
    finally:
        subprocess.run(
            ["docker", "rm", "-f", container_id],
            check=False,
            capture_output=True,
            text=True,
        )


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo-root", type=Path, default=Path(__file__).resolve().parents[2])
    parser.add_argument("--manifest", type=Path, required=True)
    parser.add_argument("--image", default="", help="Inspect this local image with Docker")
    parser.add_argument("--acknowledge-dirty", action="store_true")
    args = parser.parse_args()

    try:
        manifest = json.loads(args.manifest.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        print(f"RELEASE BLOCKED: cannot read manifest: {exc}", file=sys.stderr)
        return 2
    image_metadata = None
    if args.image:
        try:
            image_metadata = inspect_image(args.image)
            image_metadata["capability_markers"] = inspect_image_capabilities(args.image)
        except (OSError, ValueError, subprocess.CalledProcessError, json.JSONDecodeError, IndexError) as exc:
            print(f"RELEASE BLOCKED: cannot inspect image {args.image}: {exc}", file=sys.stderr)
            return 2
    errors = validate_manifest(
        args.repo_root.resolve(),
        manifest,
        acknowledge_dirty=args.acknowledge_dirty,
        image_metadata=image_metadata,
    )
    if errors:
        print("RELEASE BLOCKED")
        for error in errors:
            print(f"- {error}")
        return 1
    print("RELEASE VERIFIED: manifest, source identity, capability matrix, and test evidence passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
