#!/usr/bin/env python3
"""Unit tests for the release evidence gate."""

from __future__ import annotations

import importlib.util
import json
import subprocess
import unittest
from pathlib import Path


SCRIPT_DIR = Path(__file__).resolve().parent


def load_module(filename: str, name: str):
    spec = importlib.util.spec_from_file_location(name, SCRIPT_DIR / filename)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load {filename}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


verify = load_module("verify-release-bundle.py", "verify_release_bundle")
manifest_generator = load_module("generate-release-manifest.py", "generate_release_manifest")


def valid_manifest() -> dict:
    capabilities = {
        capability_id: {
            "status": "present",
            "evidence": ["source-and-tests"],
            "compatibility_decision": "",
        }
        for capability_id in verify.REQUIRED_CAPABILITIES
    }
    return {
        "schema_version": 1,
        "source": {
            "head": "not-used-by-structure-test",
            "dirty": False,
            "dirty_acknowledged": False,
            "snapshot_sha256": "not-used-by-structure-test",
        },
        "release": {
            "image_ref": "example/image:release",
            "image_digest": "sha256:" + "a" * 64,
            "previous_image_digest": "sha256:" + "b" * 64,
            "architecture": "linux/amd64",
            "allow_release": True,
            "image_inspection": {
                "image_digest": "sha256:" + "a" * 64,
                "architecture": "amd64",
                "revision_label": "not-used-by-structure-test",
                "source_snapshot_label": "not-used-by-structure-test",
            },
        },
        "capabilities": capabilities,
        "tests": {key: "passed" for key in verify.REQUIRED_TESTS},
    }


class ReleaseManifestStructureTests(unittest.TestCase):
    def test_operational_release_files_use_canonical_candidate_worktree(self):
        repo_root = SCRIPT_DIR.parents[1]
        canonical = ".worktrees/public-0.1.183-full-custom-20260830"
        files = (
            "ops/public-deploy/docs/RELEASE-BASELINE.md",
            "ops/public-deploy/docs/RELEASE-PROCEDURE.md",
            "ops/public-deploy/upstream-rates/run_upstream_hub_ledger_sync.sh",
            "ops/public-deploy/upstream-rates/launchd/com.fluter.upstream-hub-ledger-sync.plist",
            "ops/public-deploy/upstream-rates/launchd/com.fluter.upstream-chrome-collector.plist",
        )
        for relative in files:
            content = (repo_root / relative).read_text(encoding="utf-8")
            self.assertNotIn(".worktrees/public-deploy", content, relative)
            if relative.endswith("run_upstream_hub_ledger_sync.sh"):
                self.assertIn('SCRIPT_DIR="${0:A:h}"', content, relative)
                self.assertIn('WORKDIR="$(cd "${SCRIPT_DIR}/../../.." && pwd)"', content, relative)
            elif relative.endswith("com.fluter.upstream-hub-ledger-sync.plist"):
                self.assertIn("/Users/fluter_claw/Library/Application Support/Fluter/upstream-ledger-sync", content, relative)
            else:
                self.assertIn(canonical, content, relative)

    def test_tag_only_release_is_rejected(self):
        manifest = valid_manifest()
        manifest["release"]["image_digest"] = "example/image:release"
        errors = verify.validate_manifest_structure(manifest)
        self.assertTrue(any("immutable sha256" in error for error in errors))

    def test_missing_test_evidence_is_rejected(self):
        manifest = valid_manifest()
        manifest["tests"]["protocol_fixtures"] = "unknown"
        errors = verify.validate_manifest_structure(manifest)
        self.assertIn("test protocol_fixtures is not marked passed", errors)

    def test_missing_image_inspection_is_rejected(self):
        manifest = valid_manifest()
        del manifest["release"]["image_inspection"]
        errors = verify.validate_manifest_structure(manifest)
        self.assertIn("release.image_inspection is missing; inspect the candidate image", errors)

    def test_partial_capability_requires_explicit_decision(self):
        manifest = valid_manifest()
        manifest["capabilities"]["responses-tools"] = {
            "status": "partial",
            "evidence": ["converter-tests"],
            "compatibility_decision": "",
        }
        errors = verify.validate_manifest_structure(manifest)
        self.assertIn(
            "capability responses-tools is partial without compatibility_decision",
            errors,
        )

    def test_missing_capability_is_rejected(self):
        manifest = valid_manifest()
        del manifest["capabilities"]["scheduler"]
        errors = verify.validate_manifest_structure(manifest)
        self.assertIn("capability scheduler is missing", errors)

    def test_valid_structure_has_no_structure_errors(self):
        self.assertEqual([], verify.validate_manifest_structure(valid_manifest()))

    def test_current_source_capability_markers_match_registered_routes(self):
        repo_root = SCRIPT_DIR.parents[1]
        self.assertEqual([], verify.validate_source_capabilities(repo_root))

    def test_inspect_image_reads_identity_labels(self):
        payload = [{
            "Id": "sha256:" + "c" * 64,
            "Architecture": "amd64",
            "Config": {
                "Labels": {
                    "org.opencontainers.image.revision": "commit-1",
                    "org.opencontainers.image.source-snapshot": "snapshot-1",
                }
            },
        }]
        original_run = verify.subprocess.run
        try:
            verify.subprocess.run = lambda *args, **kwargs: type(
                "Completed", (), {"stdout": json.dumps(payload)}
            )()
            inspected = verify.inspect_image("example/image:test")
        finally:
            verify.subprocess.run = original_run

        self.assertEqual("amd64", inspected["architecture"])
        self.assertEqual("commit-1", inspected["revision_label"])
        self.assertEqual("snapshot-1", inspected["source_snapshot_label"])

    def test_build_manifest_preserves_image_source_snapshot_evidence(self):
        repo_root = SCRIPT_DIR.parents[1]
        release_manifest = manifest_generator.build_manifest(
            repo_root=repo_root,
            image_ref="example/image:candidate",
            image_digest="sha256:" + "a" * 64,
            architecture="linux/amd64",
            previous_image_digest="sha256:" + "b" * 64,
            tests={key: "passed" for key in verify.REQUIRED_TESTS},
            dirty_acknowledged=True,
            allow_release=False,
            image_metadata={
                "image_ref": "example/image:candidate",
                "image_digest": "sha256:" + "a" * 64,
                "architecture": "amd64",
                "revision_label": "commit-1",
                "version_label": "candidate",
                "source_snapshot_label": "snapshot-1",
            },
        )
        self.assertEqual(
            "snapshot-1",
            release_manifest["release"]["image_inspection"]["source_snapshot_label"],
        )

    def test_production_dockerfiles_persist_build_identity_labels(self):
        repo_root = SCRIPT_DIR.parents[1]
        for dockerfile_name in ("Dockerfile", "deploy/Dockerfile"):
            dockerfile = (repo_root / dockerfile_name).read_text(encoding="utf-8")
            self.assertIn("ARG SOURCE_SNAPSHOT", dockerfile, dockerfile_name)
            self.assertIn('org.opencontainers.image.version="${VERSION}"', dockerfile, dockerfile_name)
            self.assertIn('org.opencontainers.image.revision="${COMMIT}"', dockerfile, dockerfile_name)
            self.assertIn('org.opencontainers.image.created="${DATE}"', dockerfile, dockerfile_name)
            self.assertIn('org.opencontainers.image.source-snapshot="${SOURCE_SNAPSHOT}"', dockerfile, dockerfile_name)

    def test_capability_evidence_is_recorded_without_editing_json(self):
        release_manifest = valid_manifest()
        release_manifest["release"]["allow_release"] = False
        release_manifest["capabilities"] = {
            capability_id: {
                "status": "unverified",
                "evidence": [],
                "compatibility_decision": "",
            }
            for capability_id in manifest_generator.CAPABILITY_IDS
        }
        manifest_generator.apply_capability_evidence(
            release_manifest,
            ["scheduler=present:unit-tests|admin-smoke", "responses-tools=partial:fixture"],
            ["responses-tools=bridge does not provide native terminal execution"],
        )
        self.assertEqual("present", release_manifest["capabilities"]["scheduler"]["status"])
        self.assertEqual(["unit-tests", "admin-smoke"], release_manifest["capabilities"]["scheduler"]["evidence"])
        self.assertEqual(
            "bridge does not provide native terminal execution",
            release_manifest["capabilities"]["responses-tools"]["compatibility_decision"],
        )

    def test_allow_release_requires_all_capabilities(self):
        release_manifest = valid_manifest()
        release_manifest["capabilities"]["scheduler"] = {
            "status": "unverified",
            "evidence": [],
            "compatibility_decision": "",
        }
        with self.assertRaises(ValueError):
            manifest_generator.apply_capability_evidence(release_manifest, [], [])


if __name__ == "__main__":
    unittest.main()
