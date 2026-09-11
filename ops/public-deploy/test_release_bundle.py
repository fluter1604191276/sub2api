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
    def test_new_smart_operations_are_required_by_both_manifest_tools(self):
        self.assertEqual(set(verify.REQUIRED_CAPABILITIES), set(manifest_generator.CAPABILITY_IDS))
        for capability in ("channel-monitor-bulk-interval", "channel-monitor-budget", "smart-probe-modes", "account-model-sync-preview"):
            self.assertIn(capability, verify.IMAGE_CAPABILITY_MARKERS)
            manifest = valid_manifest()
            del manifest["capabilities"][capability]
            self.assertIn(f"capability {capability} is missing", verify.validate_manifest_structure(manifest))

    def test_official_024_compatibility_requires_authentic_version_evidence(self):
        self.assertIn("official-024-compatibility", verify.REQUIRED_CAPABILITIES)
        self.assertIn("official-024-compatibility", manifest_generator.CAPABILITY_IDS)
        self.assertIn(
            verify.OFFICIAL_VERSION_FILE,
            verify.CAPABILITY_FILES["official-024-compatibility"],
        )
        repo_root = SCRIPT_DIR.parents[1]
        self.assertEqual(
            verify.OFFICIAL_VERSION,
            (repo_root / verify.OFFICIAL_VERSION_FILE).read_text(encoding="utf-8").strip(),
        )

        original_file_text = verify.file_text
        try:
            verify.file_text = lambda root, relative: (
                "candidate-tag-only"
                if relative == verify.OFFICIAL_VERSION_FILE
                else original_file_text(root, relative)
            )
            errors = verify.validate_source_capabilities(repo_root)
        finally:
            verify.file_text = original_file_text
        self.assertIn(
            "official compatibility VERSION is 'candidate-tag-only'; expected '0.2.4'",
            errors,
        )

    def test_operational_release_files_require_live_production_baseline(self):
        repo_root = SCRIPT_DIR.parents[1]
        files = (
            "ops/public-deploy/README.md",
            "ops/public-deploy/docs/RELEASE-BASELINE.md",
            "ops/public-deploy/docs/RELEASE-LINES.md",
            "ops/public-deploy/docs/RELEASE-PROCEDURE.md",
            "ops/public-deploy/docs/RELEASE-CHECKLIST.md",
            "ops/public-deploy/check-production-baseline.sh",
            "ops/public-deploy/create-production-derived-worktree.sh",
            "ops/public-deploy/upstream-rates/run_upstream_hub_ledger_sync.sh",
            "ops/public-deploy/upstream-rates/launchd/com.fluter.upstream-hub-ledger-sync.plist",
            "ops/public-deploy/upstream-rates/launchd/com.fluter.upstream-chrome-collector.plist",
        )
        for relative in files:
            content = (repo_root / relative).read_text(encoding="utf-8")
            if not relative.endswith(".md"):
                self.assertNotIn(".worktrees/public-deploy", content, relative)
            if relative in {
                "ops/public-deploy/README.md",
                "ops/public-deploy/docs/RELEASE-BASELINE.md",
                "ops/public-deploy/docs/RELEASE-LINES.md",
                "ops/public-deploy/docs/RELEASE-PROCEDURE.md",
                "ops/public-deploy/docs/RELEASE-CHECKLIST.md",
            }:
                self.assertIn("live production", content.lower(), relative)
                self.assertIn("every", content.lower(), relative)
            elif relative == "ops/public-deploy/create-production-derived-worktree.sh":
                self.assertIn("docker inspect sub2api", content, relative)
                self.assertIn("git -C \"${MAIN_REPO}\" worktree add -b", content, relative)
            elif relative == "ops/public-deploy/check-production-baseline.sh":
                self.assertIn('PRODUCTION_ALIAS="${PRODUCTION_ALIAS:-fluterapi-prod}"', content, relative)
                self.assertIn("git merge-base --is-ancestor", content, relative)
            elif relative.endswith("run_upstream_hub_ledger_sync.sh"):
                self.assertIn('SCRIPT_DIR="${0:A:h}"', content, relative)
                self.assertIn('WORKDIR="$(cd "${SCRIPT_DIR}/../../.." && pwd)"', content, relative)
            elif relative.endswith("com.fluter.upstream-hub-ledger-sync.plist"):
                self.assertIn("/Users/fluter_claw/Library/Application Support/Fluter/upstream-ledger-sync", content, relative)
            elif relative.endswith("com.fluter.upstream-chrome-collector.plist"):
                # This collector is a diagnostic rollback tool, not a release
                # input; it must not make the active release line canonical.
                self.assertIn("chrome_readonly_collector.py", content, relative)
            else:
                self.assertNotIn("public-0.1.183-full-custom-20260830", content, relative)

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

    def test_binary_capability_smoke_requires_runtime_markers(self):
        payload = b"smart_scheduler sticky.smart_scheduler_switched recovery_probe scheduled-test-plans"
        results = verify.inspect_binary_capabilities(payload)
        self.assertEqual("present", results["scheduler"]["status"])
        self.assertEqual("present", results["scheduled-probe"]["status"])
        self.assertEqual("missing", results["quality-score"]["status"])

    def test_binary_marker_matcher_finds_markers_across_chunk_boundaries(self):
        matched = verify.match_binary_markers(
            (b"prefix smart_sched", b"uler suffix"),
            ("smart_scheduler", "missing-marker"),
        )
        self.assertEqual({"smart_scheduler"}, matched)

    def test_image_capability_smoke_streams_from_amd64_container(self):
        original_run = verify.subprocess.run
        original_stream = verify.stream_container_markers
        calls = []
        try:
            def fake_run(args, **kwargs):
                calls.append((args, kwargs))
                return type("Completed", (), {"stdout": "container-id\n"})()

            verify.subprocess.run = fake_run
            verify.stream_container_markers = lambda container, markers, timeout: {
                "smart_scheduler",
                "sticky.smart_scheduler_switched",
            }
            results = verify.inspect_image_capabilities("example/image:test")
        finally:
            verify.subprocess.run = original_run
            verify.stream_container_markers = original_stream

        create_args, create_kwargs = calls[0]
        cleanup_args, cleanup_kwargs = calls[-1]
        self.assertEqual("docker", create_args[0])
        self.assertIn("create", create_args)
        self.assertIn("/bin/cat", create_args)
        self.assertIn("linux/amd64", create_args)
        self.assertNotIn("cp", create_args)
        self.assertEqual(verify.IMAGE_INSPECTION_TIMEOUT_SECONDS, create_kwargs["timeout"])
        self.assertEqual(["docker", "rm", "-f"], cleanup_args[:3])
        self.assertEqual(verify.CONTAINER_CLEANUP_TIMEOUT_SECONDS, cleanup_kwargs["timeout"])
        self.assertEqual("present", results["scheduler"]["status"])
        self.assertEqual("missing", results["scheduled-probe"]["status"])

    def test_image_capability_timeout_always_removes_container(self):
        original_run = verify.subprocess.run
        original_stream = verify.stream_container_markers
        calls = []
        try:
            def fake_run(args, **kwargs):
                calls.append((args, kwargs))
                return type("Completed", (), {"stdout": "container-id\n"})()

            def timeout(*args, **kwargs):
                raise subprocess.TimeoutExpired(["docker", "start"], 120)

            verify.subprocess.run = fake_run
            verify.stream_container_markers = timeout
            with self.assertRaises(subprocess.TimeoutExpired):
                verify.inspect_image_capabilities("example/image:test")
        finally:
            verify.subprocess.run = original_run
            verify.stream_container_markers = original_stream

        self.assertEqual(["docker", "rm", "-f"], calls[-1][0][:3])

    def test_container_stream_failure_is_rejected(self):
        original_popen = verify.subprocess.Popen
        try:
            verify.subprocess.Popen = lambda command, **kwargs: original_popen(
                ["/bin/sh", "-c", "printf partial-marker; exit 23"],
                **kwargs,
            )
            with self.assertRaises(subprocess.CalledProcessError) as raised:
                verify.stream_container_markers("container-id", ("missing-marker",), 5)
        finally:
            verify.subprocess.Popen = original_popen
        self.assertEqual(23, raised.exception.returncode)

    def test_container_stream_timeout_kills_attach_process(self):
        original_popen = verify.subprocess.Popen
        processes = []
        try:
            def sleeping_popen(command, **kwargs):
                process = original_popen(["/bin/sh", "-c", "exec sleep 10"], **kwargs)
                processes.append(process)
                return process

            verify.subprocess.Popen = sleeping_popen
            with self.assertRaises(subprocess.TimeoutExpired):
                verify.stream_container_markers("container-id", ("missing-marker",), 0.05)
        finally:
            verify.subprocess.Popen = original_popen
            for process in processes:
                if process.poll() is None:
                    process.kill()
                    process.wait()
        self.assertTrue(processes)
        self.assertIsNotNone(processes[0].poll())

    def test_official_024_image_smoke_requires_version_and_route_markers(self):
        complete = verify.inspect_binary_capabilities(b"0.2.4 model-allowlist-candidates")
        self.assertEqual("present", complete["official-024-compatibility"]["status"])

        tag_only = verify.inspect_binary_capabilities(b"official-024-candidate")
        self.assertEqual("missing", tag_only["official-024-compatibility"]["status"])

    def test_public_catalog_contract_is_required_with_source_and_image_evidence(self):
        capability = "public-catalog-contract"
        self.assertIn(capability, verify.REQUIRED_CAPABILITIES)
        self.assertIn(capability, manifest_generator.CAPABILITY_IDS)
        repo_root = SCRIPT_DIR.parents[1]
        self.assertTrue(
            all((repo_root / relative).is_file() for relative in verify.CAPABILITY_FILES[capability])
        )

        complete = verify.inspect_binary_capabilities(
            b"configured_not_live before_group_multiplier unavailable_fallback_to_group"
        )
        self.assertEqual("present", complete[capability]["status"])

        incomplete = verify.inspect_binary_capabilities(
            b"configured_not_live before_group_multiplier"
        )
        self.assertEqual("missing", incomplete[capability]["status"])

    def test_scheduler_source_requires_complete_routing_implementation(self):
        self.assertIn(
            "backend/internal/service/smart_scheduler_routing.go",
            verify.CAPABILITY_FILES["scheduler"],
        )
        self.assertIn(
            "backend/internal/service/smart_scheduler_preview.go",
            verify.CAPABILITY_FILES["scheduler"],
        )

    def test_catalog_surfaces_are_required_and_all_source_evidence_exists(self):
        self.assertIn("catalog-surfaces", verify.REQUIRED_CAPABILITIES)
        self.assertIn("catalog-surfaces", verify.IMAGE_CAPABILITY_MARKERS)
        repo_root = SCRIPT_DIR.parents[1]
        self.assertTrue(
            all(
                (repo_root / relative).is_file()
                for relative in verify.CAPABILITY_FILES["catalog-surfaces"]
            )
        )
        self.assertEqual(
            [],
            verify.validate_source_capabilities(repo_root),
        )

    def test_model_capability_failover_is_required_and_has_runtime_markers(self):
        self.assertIn("model-capability-failover", verify.REQUIRED_CAPABILITIES)
        self.assertIn("model-capability-failover", verify.IMAGE_CAPABILITY_MARKERS)
        repo_root = SCRIPT_DIR.parents[1]
        self.assertTrue(
            all(
                (repo_root / relative).is_file()
                for relative in verify.CAPABILITY_FILES["model-capability-failover"]
            )
        )
        complete = b"unknown provider for model smart_capability"
        results = verify.inspect_binary_capabilities(complete)
        self.assertEqual("present", results["model-capability-failover"]["status"])

    def test_catalog_surface_image_smoke_requires_all_runtime_markers(self):
        complete = b"public_catalog_visibility model_plaza available_channels"
        results = verify.inspect_binary_capabilities(complete)
        self.assertEqual("present", results["catalog-surfaces"]["status"])

        incomplete = b"public_catalog_visibility model_plaza"
        results = verify.inspect_binary_capabilities(incomplete)
        self.assertEqual("missing", results["catalog-surfaces"]["status"])

    def test_maintained_upstream_docs_do_not_reference_removed_script_directory(self):
        repo_root = SCRIPT_DIR.parents[1]
        for relative in (
            "ops/public-deploy/README.md",
            "ops/public-deploy/docs/extensions/20260828-upstream-ledger.md",
        ):
            content = (repo_root / relative).read_text(encoding="utf-8")
            self.assertNotIn("ops/public-deploy/scripts/", content, relative)

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
