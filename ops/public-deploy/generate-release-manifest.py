#!/usr/bin/env python3
"""Generate a secret-free release manifest from the exact source tree used to build.

The manifest is evidence, not a release approval.  It defaults to
allow_release=false and records hashes instead of source contents.
"""

from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import subprocess
from pathlib import Path
from typing import Iterable


CAPABILITY_IDS = (
    "scheduler",
    "scheduled-probe",
    "quality-score",
    "cache-hit-rate",
    "image-cost",
    "pricing-calibration",
    "model-sync-filter",
    "error-passthrough",
    "model-capability-failover",
    "generic-400-failover",
    "responses-tools",
    "upstream-ledger",
    "ops-baseline",
    "catalog-surfaces",
)


def run_git(repo_root: Path, *args: str) -> str:
    result = subprocess.run(
        ["git", "-C", str(repo_root), *args],
        check=True,
        capture_output=True,
        text=True,
    )
    return result.stdout


def git_status(repo_root: Path) -> list[str]:
    return [line for line in run_git(repo_root, "status", "--short").splitlines() if line]


def source_files(repo_root: Path) -> list[str]:
    raw = run_git(repo_root, "ls-files", "-co", "--exclude-standard", "-z")
    return sorted(item for item in raw.split("\0") if item)


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def snapshot_hash(repo_root: Path, files: Iterable[str]) -> tuple[str, dict[str, str]]:
    file_hashes: dict[str, str] = {}
    snapshot = hashlib.sha256()
    for relative in files:
        path = repo_root / relative
        if not path.is_file():
            continue
        file_hash = sha256_file(path)
        file_hashes[relative] = file_hash
        snapshot.update(relative.encode("utf-8"))
        snapshot.update(b"\0")
        snapshot.update(file_hash.encode("ascii"))
        snapshot.update(b"\n")
    return snapshot.hexdigest(), file_hashes


def changed_paths(status_lines: Iterable[str]) -> list[str]:
    paths: list[str] = []
    for line in status_lines:
        if len(line) < 4:
            continue
        value = line[3:]
        if " -> " in value:
            value = value.rsplit(" -> ", 1)[1]
        paths.append(value)
    return sorted(set(paths))


def inspect_image(image: str) -> dict[str, object]:
    result = subprocess.run(
        ["docker", "image", "inspect", image],
        check=True,
        capture_output=True,
        text=True,
    )
    payload = json.loads(result.stdout)
    if not payload:
        raise ValueError(f"docker returned no metadata for {image}")
    item = payload[0]
    repo_digests = item.get("RepoDigests") or []
    digest = repo_digests[0] if repo_digests else item.get("Id", "")
    if "@" in digest:
        digest = digest.rsplit("@", 1)[1]
    labels = item.get("Config", {}).get("Labels") or {}
    return {
        "image_ref": image,
        "image_digest": digest,
        "architecture": item.get("Architecture", ""),
        "revision_label": labels.get("org.opencontainers.image.revision", ""),
        "version_label": labels.get("org.opencontainers.image.version", ""),
        "source_snapshot_label": labels.get("org.opencontainers.image.source-snapshot", ""),
    }


def build_manifest(
    repo_root: Path,
    image_ref: str,
    image_digest: str,
    architecture: str,
    previous_image_digest: str,
    tests: dict[str, str],
    dirty_acknowledged: bool,
    allow_release: bool,
    image_metadata: dict[str, object] | None = None,
) -> dict[str, object]:
    status_lines = git_status(repo_root)
    files = source_files(repo_root)
    snapshot, file_hashes = snapshot_hash(repo_root, files)
    changed = changed_paths(status_lines)
    changed_hashes = {path: file_hashes[path] for path in changed if path in file_hashes}
    source = {
        "repo_root": str(repo_root),
        "head": run_git(repo_root, "rev-parse", "HEAD").strip(),
        "branch": run_git(repo_root, "branch", "--show-current").strip(),
        "dirty": bool(status_lines),
        "dirty_acknowledged": dirty_acknowledged,
        "status_count": len(status_lines),
        "status_sha256": hashlib.sha256("\n".join(status_lines).encode("utf-8")).hexdigest(),
        "snapshot_sha256": snapshot,
        "changed_file_hashes": changed_hashes,
    }
    release = {
        "image_ref": image_ref,
        "image_digest": image_digest,
        "architecture": architecture,
        "previous_image_digest": previous_image_digest,
        "allow_release": allow_release,
    }
    if image_metadata:
        release["image_inspection"] = image_metadata

    capabilities = {
        capability_id: {
            "status": "unverified",
            "evidence": [],
            "compatibility_decision": "",
        }
        for capability_id in CAPABILITY_IDS
    }
    return {
        "schema_version": 1,
        "generated_at_utc": dt.datetime.now(dt.timezone.utc).replace(microsecond=0).isoformat(),
        "source": source,
        "release": release,
        "capabilities": capabilities,
        "tests": tests,
        "notes": "Secret-free evidence manifest. This file does not approve a release by itself.",
    }


def parse_test_values(values: list[str]) -> dict[str, str]:
    tests = {
        "backend": "unknown",
        "frontend": "unknown",
        "diff_check": "unknown",
        "protocol_fixtures": "unknown",
        "image_smoke": "unknown",
    }
    for value in values:
        if "=" not in value:
            raise ValueError(f"test must use NAME=STATUS: {value}")
        name, status = value.split("=", 1)
        if name not in tests:
            raise ValueError(f"unknown test name: {name}")
        if status not in {"passed", "failed", "unknown", "skipped"}:
            raise ValueError(f"invalid test status: {status}")
        tests[name] = status
    return tests


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo-root", type=Path, default=Path(__file__).resolve().parents[2])
    parser.add_argument("--image", required=True, help="Candidate image reference")
    parser.add_argument("--image-digest", default="", help="Optional expected digest; must match inspected image")
    parser.add_argument("--architecture", default="linux/amd64")
    parser.add_argument("--previous-image-digest", required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--test", action="append", default=[], metavar="NAME=STATUS")
    parser.add_argument(
        "--capability",
        action="append",
        default=[],
        metavar="ID=STATUS:EVIDENCE",
        help=(
            "Record capability evidence. Repeat per capability; STATUS is "
            "present, partial, or unverified. Use | to separate evidence items."
        ),
    )
    parser.add_argument(
        "--capability-decision",
        action="append",
        default=[],
        metavar="ID=DECISION",
        help="Record the compatibility decision for a partial capability.",
    )
    parser.add_argument("--acknowledge-dirty", action="store_true")
    parser.add_argument("--allow-release", action="store_true")
    args = parser.parse_args()

    repo_root = args.repo_root.resolve()
    # Always inspect the candidate. A manually supplied digest without image
    # labels would allow a tag/digest to be detached from its source snapshot.
    image_metadata = inspect_image(args.image)
    inspected_digest = str(image_metadata["image_digest"])
    if args.image_digest and args.image_digest != inspected_digest:
        raise ValueError(
            f"supplied image digest {args.image_digest} does not match inspected image {inspected_digest}"
        )
    image_digest = inspected_digest
    if image_metadata.get("architecture"):
        args.architecture = f"linux/{image_metadata['architecture']}"
    manifest = build_manifest(
        repo_root=repo_root,
        image_ref=args.image,
        image_digest=image_digest,
        architecture=args.architecture,
        previous_image_digest=args.previous_image_digest,
        tests=parse_test_values(args.test),
        dirty_acknowledged=args.acknowledge_dirty,
        allow_release=args.allow_release,
        image_metadata=image_metadata,
    )
    apply_capability_evidence(manifest, args.capability, args.capability_decision)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(manifest, indent=2, ensure_ascii=True) + "\n", encoding="utf-8")
    print(f"wrote secret-free release manifest: {args.output}")
    print(f"source head: {manifest['source']['head']}")
    print(f"source dirty: {manifest['source']['dirty']}")
    print(f"source snapshot: {manifest['source']['snapshot_sha256']}")
    print(f"image digest: {manifest['release']['image_digest']}")
    print(f"allow_release: {manifest['release']['allow_release']}")
    return 0


def parse_capability_values(values: list[str]) -> dict[str, dict[str, object]]:
    parsed: dict[str, dict[str, object]] = {}
    for value in values:
        if "=" not in value or ":" not in value:
            raise ValueError(f"capability must use ID=STATUS:EVIDENCE: {value}")
        capability_id, payload = value.split("=", 1)
        status, evidence_text = payload.split(":", 1)
        capability_id = capability_id.strip()
        status = status.strip().lower()
        evidence = [item.strip() for item in evidence_text.split("|") if item.strip()]
        if capability_id not in CAPABILITY_IDS:
            raise ValueError(f"unknown capability: {capability_id}")
        if status not in {"present", "partial", "unverified"}:
            raise ValueError(f"invalid capability status: {status}")
        if status != "unverified" and not evidence:
            raise ValueError(f"capability evidence is required for {capability_id}")
        if capability_id in parsed:
            raise ValueError(f"capability specified more than once: {capability_id}")
        parsed[capability_id] = {"status": status, "evidence": evidence}
    return parsed


def parse_capability_decisions(values: list[str]) -> dict[str, str]:
    parsed: dict[str, str] = {}
    for value in values:
        if "=" not in value:
            raise ValueError(f"capability decision must use ID=DECISION: {value}")
        capability_id, decision = value.split("=", 1)
        capability_id = capability_id.strip()
        decision = decision.strip()
        if capability_id not in CAPABILITY_IDS:
            raise ValueError(f"unknown capability: {capability_id}")
        if not decision:
            raise ValueError(f"capability decision is empty: {capability_id}")
        if capability_id in parsed:
            raise ValueError(f"capability decision specified more than once: {capability_id}")
        parsed[capability_id] = decision
    return parsed


def apply_capability_evidence(
    manifest: dict[str, object], values: list[str], decisions: list[str]
) -> None:
    capabilities = manifest["capabilities"]
    assert isinstance(capabilities, dict)
    evidence = parse_capability_values(values)
    compatibility_decisions = parse_capability_decisions(decisions)
    for capability_id, item in evidence.items():
        target = capabilities[capability_id]
        assert isinstance(target, dict)
        target.update(item)
    for capability_id, decision in compatibility_decisions.items():
        target = capabilities[capability_id]
        assert isinstance(target, dict)
        target["compatibility_decision"] = decision
    if manifest["release"]["allow_release"] is True:
        missing = [
            capability_id
            for capability_id, item in capabilities.items()
            if item["status"] not in {"present", "partial"} or not item["evidence"]
        ]
        if missing:
            raise ValueError(
                "--allow-release requires evidence for every capability: "
                + ", ".join(missing)
            )


if __name__ == "__main__":
    raise SystemExit(main())
