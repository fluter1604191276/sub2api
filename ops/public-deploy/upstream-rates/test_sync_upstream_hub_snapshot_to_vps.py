#!/usr/bin/env python3
from __future__ import annotations

import importlib.util
import io
import sys
import tempfile
import unittest
from contextlib import redirect_stdout
from pathlib import Path


SCRIPT = Path(__file__).with_name("sync_upstream_hub_snapshot_to_vps.py")


def load_module():
    spec = importlib.util.spec_from_file_location("sync_upstream_hub_snapshot_to_vps", SCRIPT)
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class SyncUpstreamHubSnapshotToVpsTest(unittest.TestCase):
    def setUp(self) -> None:
        self.mod = load_module()

    def test_assert_snapshot_is_sanitized_requires_schema(self):
        with tempfile.NamedTemporaryFile("w", suffix=".json", delete=False) as tmp:
            tmp.write("{}")
            path = Path(tmp.name)
        try:
            with self.assertRaises(SystemExit):
                self.mod.assert_snapshot_is_sanitized(path)
        finally:
            path.unlink(missing_ok=True)

    def test_assert_snapshot_is_sanitized_rejects_secret_markers(self):
        with tempfile.NamedTemporaryFile("w", suffix=".json", delete=False) as tmp:
            tmp.write('{"schema":"fluter-upstream-hub-snapshot/v1","bad":"Bearer abc"}')
            path = Path(tmp.name)
        try:
            with self.assertRaises(SystemExit):
                self.mod.assert_snapshot_is_sanitized(path)
        finally:
            path.unlink(missing_ok=True)

    def test_snapshot_summary_counts_sanitized_payload(self):
        with tempfile.NamedTemporaryFile("w", suffix=".json", delete=False) as tmp:
            tmp.write(
                """
                {
                  "schema": "fluter-upstream-hub-snapshot/v1",
                  "exported_at": "2026-06-16T00:00:00+00:00",
                  "channels": [{}, {}],
                  "rates": [{}, {}, {}],
                  "balances": [{}],
                  "rate_changes": [{}, {}]
                }
                """
            )
            path = Path(tmp.name)
        try:
            self.mod.assert_snapshot_is_sanitized(path)
            summary = self.mod.snapshot_summary(path)
        finally:
            path.unlink(missing_ok=True)

        self.assertEqual(
            {
                "channels": 2,
                "rates": 3,
                "balances": 1,
                "rate_changes": 2,
                "exported_at": "2026-06-16T00:00:00+00:00",
            },
            summary,
        )

    def test_refresh_remote_ledger_uses_snapshot_argument(self):
        calls: list[tuple[str, str, str]] = []
        original = self.mod.run_remote
        self.mod.run_remote = lambda host, command, name: calls.append((host, command, name))
        try:
            args = self.mod.parse_args(
                [
                    "--ssh-host",
                    "vps",
                    "--remote-snapshot",
                    "/var/lib/fluterapi-upstream-rates/upstream-hub-snapshot.json",
                    "--true-loss-alert-endpoint",
                    "http://127.0.0.1:8752/alerts",
                ]
            )
            self.mod.refresh_remote_ledger(args)
        finally:
            self.mod.run_remote = original

        self.assertEqual(1, len(calls))
        host, command, name = calls[0]
        self.assertEqual("vps", host)
        self.assertEqual("refresh VPS ledger from sanitized snapshot", name)
        self.assertIn("--upstream-hub-snapshot-json", command)
        self.assertIn("/var/lib/fluterapi-upstream-rates/upstream-hub-snapshot.json", command)
        self.assertIn("--true-loss-alert-endpoint", command)
        self.assertIn("http://127.0.0.1:8752/alerts", command)

    def test_export_only_does_not_copy_or_refresh(self):
        calls: list[tuple[str, list[str]]] = []
        remote_calls: list[tuple[str, str, str]] = []
        with tempfile.NamedTemporaryFile("w", suffix=".json", delete=False) as tmp:
            snapshot = Path(tmp.name)

        def fake_export(args, snapshot_path, script_dir, python):
            snapshot_path.write_text(
                """
                {
                  "schema": "fluter-upstream-hub-snapshot/v1",
                  "exported_at": "2026-06-16T00:00:00+00:00",
                  "channels": [{}],
                  "rates": [{}, {}],
                  "balances": [],
                  "rate_changes": [{}]
                }
                """
            )

        original_export = self.mod.export_snapshot
        original_run = self.mod.run_command
        original_remote = self.mod.run_remote
        output = io.StringIO()
        self.mod.export_snapshot = fake_export
        self.mod.run_command = lambda command, name: calls.append((name, command))
        self.mod.run_remote = lambda host, command, name: remote_calls.append((host, command, name))
        try:
            with redirect_stdout(output):
                result = self.mod.main(["--local-snapshot", str(snapshot), "--export-only"])
        finally:
            self.mod.export_snapshot = original_export
            self.mod.run_command = original_run
            self.mod.run_remote = original_remote
            snapshot.unlink(missing_ok=True)

        self.assertEqual(0, result)
        self.assertEqual([], calls)
        self.assertEqual([], remote_calls)
        self.assertIn("snapshot_summary channels=1 rates=2 balances=0 rate_changes=1", output.getvalue())


if __name__ == "__main__":
    unittest.main()
