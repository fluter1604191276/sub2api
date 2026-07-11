#!/usr/bin/env python3
from __future__ import annotations

import importlib.util
import sys
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("refresh_upstream_ledger.py")


def load_module():
    spec = importlib.util.spec_from_file_location("refresh_upstream_ledger", SCRIPT)
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class RefreshUpstreamLedgerTest(unittest.TestCase):
    def setUp(self) -> None:
        self.mod = load_module()
        self.old_argv = sys.argv[:]

    def tearDown(self) -> None:
        sys.argv = self.old_argv

    def run_main_with_args(self, *args: str):
        steps: list[tuple[str, list[str]]] = []
        metadata: dict[str, str] = {}

        def fake_run_step(name: str, command: list[str]) -> None:
            steps.append((name, command))

        def fake_write_metadata(_db_path: str, key: str, value: str) -> None:
            metadata[key] = value

        self.mod.run_step = fake_run_step
        self.mod.write_metadata = fake_write_metadata
        sys.argv = [str(SCRIPT), *args]

        result = self.mod.main()

        return result, steps, metadata

    def test_default_pipeline_imports_upstream_hub_and_skips_legacy_balance_api(self):
        with tempfile.TemporaryDirectory() as hub_compose_dir:
            result, steps, metadata = self.run_main_with_args(
                "--db",
                "/tmp/upstream-rates-test.sqlite",
                "--output",
                "/tmp/upstream-rates-test.html",
                "--local-postgres",
                "--hub-compose-dir",
                hub_compose_dir,
            )

        self.assertEqual(0, result)
        command_text = "\n".join(" ".join(command) for _name, command in steps)
        step_names = [name for name, _command in steps]

        self.assertIn("import read-only upstream-hub observations", step_names)
        self.assertIn("refresh_from_upstream_hub.py", command_text)
        self.assertIn("--update-ledger-page-rates", command_text)
        self.assertIn("--hub-compose-dir", command_text)
        self.assertIn("emit_true_loss_alerts.py", command_text)
        self.assertIn("--fail-soft", command_text)
        self.assertNotIn("refresh_balance_api_adapters.py", command_text)
        self.assertIn("true-loss alert", metadata["last_orchestrated_refresh_note"])
        self.assertIn("legacy API balance adapters skipped", metadata["last_orchestrated_refresh_note"])

    def test_default_true_loss_alert_is_dry_run_without_endpoint(self):
        with tempfile.TemporaryDirectory() as hub_compose_dir:
            _result, steps, _metadata = self.run_main_with_args(
                "--db",
                "/tmp/upstream-rates-test.sqlite",
                "--output",
                "/tmp/upstream-rates-test.html",
                "--local-postgres",
                "--hub-compose-dir",
                hub_compose_dir,
            )

        alert_steps = [command for name, command in steps if name == "emit optional KBQ true-loss alert"]

        self.assertEqual(1, len(alert_steps))
        self.assertIn("emit_true_loss_alerts.py", " ".join(alert_steps[0]))
        self.assertIn("--fail-soft", alert_steps[0])
        self.assertNotIn("--endpoint", alert_steps[0])

    def test_upstream_hub_snapshot_import_uses_sanitized_json(self):
        with tempfile.NamedTemporaryFile(suffix=".json", delete=False) as tmp:
            snapshot = tmp.name
        try:
            result, steps, _metadata = self.run_main_with_args(
                "--db",
                "/tmp/upstream-rates-test.sqlite",
                "--output",
                "/tmp/upstream-rates-test.html",
                "--local-postgres",
                "--upstream-hub-snapshot-json",
                snapshot,
            )

            self.assertEqual(0, result)
            command_text = "\n".join(" ".join(command) for _name, command in steps)

            self.assertIn("refresh_from_upstream_hub.py", command_text)
            self.assertIn("--import-json", command_text)
            self.assertIn(snapshot, command_text)
            self.assertNotIn("--hub-compose-dir", command_text)
        finally:
            Path(snapshot).unlink(missing_ok=True)

    def test_missing_upstream_hub_snapshot_skips_only_hub_step(self):
        result, steps, _metadata = self.run_main_with_args(
            "--db",
            "/tmp/upstream-rates-test.sqlite",
            "--output",
            "/tmp/upstream-rates-test.html",
            "--local-postgres",
            "--upstream-hub-snapshot-json",
            "/tmp/definitely-missing-upstream-hub-snapshot.json",
        )

        self.assertEqual(0, result)
        step_names = [name for name, _command in steps]
        command_text = "\n".join(" ".join(command) for _name, command in steps)

        self.assertNotIn("import read-only upstream-hub observations", step_names)
        self.assertNotIn("refresh_from_upstream_hub.py", command_text)
        self.assertIn("refresh KBQ token model pricing", step_names)

    def test_missing_default_hub_compose_dir_skips_only_hub_step(self):
        result, steps, _metadata = self.run_main_with_args(
            "--db",
            "/tmp/upstream-rates-test.sqlite",
            "--output",
            "/tmp/upstream-rates-test.html",
            "--local-postgres",
            "--hub-compose-dir",
            "/tmp/definitely-missing-upstream-hub-compose-dir",
        )

        self.assertEqual(0, result)
        step_names = [name for name, _command in steps]
        command_text = "\n".join(" ".join(command) for _name, command in steps)

        self.assertNotIn("import read-only upstream-hub observations", step_names)
        self.assertNotIn("refresh_from_upstream_hub.py", command_text)
        self.assertIn("refresh KBQ token model pricing", step_names)

    def test_legacy_balance_api_runs_only_when_explicitly_included(self):
        _result, steps, _metadata = self.run_main_with_args(
            "--db",
            "/tmp/upstream-rates-test.sqlite",
            "--output",
            "/tmp/upstream-rates-test.html",
            "--local-postgres",
            "--include-balance-api-adapters",
        )

        command_text = "\n".join(" ".join(command) for _name, command in steps)

        self.assertIn("refresh_balance_api_adapters.py", command_text)

    def test_skip_upstream_hub_omits_hub_import(self):
        _result, steps, _metadata = self.run_main_with_args(
            "--db",
            "/tmp/upstream-rates-test.sqlite",
            "--output",
            "/tmp/upstream-rates-test.html",
            "--local-postgres",
            "--skip-upstream-hub",
        )

        command_text = "\n".join(" ".join(command) for _name, command in steps)
        step_names = [name for name, _command in steps]

        self.assertNotIn("import read-only upstream-hub observations", step_names)
        self.assertNotIn("refresh_from_upstream_hub.py", command_text)

    def test_true_loss_alert_endpoint_is_explicit_loopback_argument(self):
        _result, steps, _metadata = self.run_main_with_args(
            "--db",
            "/tmp/upstream-rates-test.sqlite",
            "--output",
            "/tmp/upstream-rates-test.html",
            "--local-postgres",
            "--true-loss-alert-endpoint",
            "http://127.0.0.1:8752/alerts",
        )

        alert_steps = [command for name, command in steps if name == "emit optional KBQ true-loss alert"]

        self.assertEqual(1, len(alert_steps))
        self.assertIn("--endpoint", alert_steps[0])
        self.assertIn("http://127.0.0.1:8752/alerts", alert_steps[0])

    def test_true_loss_alert_skipped_when_kbq_audit_is_skipped(self):
        _result, steps, _metadata = self.run_main_with_args(
            "--db",
            "/tmp/upstream-rates-test.sqlite",
            "--output",
            "/tmp/upstream-rates-test.html",
            "--local-postgres",
            "--skip-kbq-audit",
        )

        step_names = [name for name, _command in steps]
        command_text = "\n".join(" ".join(command) for _name, command in steps)

        self.assertNotIn("audit KBQ true upstream cost", step_names)
        self.assertNotIn("emit optional KBQ true-loss alert", step_names)
        self.assertNotIn("emit_true_loss_alerts.py", command_text)


if __name__ == "__main__":
    unittest.main()
