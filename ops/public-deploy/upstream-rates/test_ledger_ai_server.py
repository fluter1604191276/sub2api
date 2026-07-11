#!/usr/bin/env python3
from __future__ import annotations

import importlib.util
import os
import sqlite3
import sys
import tempfile
import unittest
from datetime import datetime, timezone
from pathlib import Path
from unittest import mock


SCRIPT = Path(__file__).with_name("ledger_ai_server.py")


def load_module():
    spec = importlib.util.spec_from_file_location("ledger_ai_server", SCRIPT)
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class LedgerAIServerMetricsTest(unittest.TestCase):
    def setUp(self) -> None:
        self.mod = load_module()

    def test_parse_meminfo_uses_bytes(self):
        parsed = self.mod.parse_meminfo("MemTotal:       1024 kB\nMemAvailable:    256 kB\n")

        self.assertEqual(1024 * 1024, parsed["MemTotal"])
        self.assertEqual(256 * 1024, parsed["MemAvailable"])

    def test_parse_net_dev_sums_non_loopback_interfaces(self):
        text = """
Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 10 0 0 0 0 0 0 0 20 0 0 0 0 0 0 0
  eth0: 1000 0 0 0 0 0 0 0 2000 0 0 0 0 0 0 0
  eth1: 3000 0 0 0 0 0 0 0 4000 0 0 0 0 0 0 0
"""

        parsed = self.mod.parse_net_dev(text)

        self.assertEqual(4000, parsed["rx_bytes"])
        self.assertEqual(6000, parsed["tx_bytes"])
        self.assertEqual("eth1", parsed["primary_interface"])

    def test_parse_docker_ps(self):
        parsed = self.mod.parse_docker_ps(
            "sub2api-backend-1\tUp 2 hours (healthy)\n"
            "sub2api-db-1\tExited (1) 1 minute ago\n"
            "sub2api-cache-1\tUp 2 hours (unhealthy)\n"
        )

        self.assertEqual("sub2api-backend-1", parsed[0]["name"])
        self.assertEqual("ok", parsed[0]["health"])
        self.assertEqual("risk", parsed[1]["health"])
        self.assertEqual("risk", parsed[2]["health"])

    def test_expected_container_inventory_marks_missing_as_risk(self):
        inventory = self.mod.expected_container_inventory(
            self.mod.parse_docker_ps(
                "sub2api\tUp 2 hours (healthy)\n"
                "production-s2a-manager-web-1\tUp 5 minutes\n"
            )
        )

        by_id = {item["id"]: item for item in inventory}
        self.assertEqual("ok", by_id["sub2api"]["health"])
        self.assertEqual("ok", by_id["s2a_web"]["health"])
        self.assertFalse(by_id["redis"]["present"])
        self.assertEqual("risk", by_id["redis"]["health"])

    def test_data_freshness_reads_known_metadata_only(self):
        with tempfile.NamedTemporaryFile(suffix=".sqlite") as tmp:
            conn = sqlite3.connect(tmp.name)
            conn.execute("create table metadata (key text primary key, value text not null)")
            conn.executemany(
                "insert into metadata(key, value) values (?, ?)",
                [
                    ("kbq_pricing_updated_at", "2026-07-12T00:00:00+00:00"),
                    ("last_upstream_hub_imported_at", "2026-07-11T00:00:00+00:00"),
                    ("secret_token", "must-not-leak"),
                ],
            )
            conn.commit()
            conn.close()

            items = self.mod.collect_data_freshness(
                tmp.name,
                "/missing/dashboard.html",
                now=datetime(2026, 7, 12, 3, 0, tzinfo=timezone.utc),
            )

        by_id = {item["id"]: item for item in items}
        self.assertEqual("warn", by_id["kbq_pricing"]["tone"])
        self.assertEqual("risk", by_id["upstream_hub"]["tone"])
        self.assertEqual("risk", by_id["static_dashboard"]["tone"])
        self.assertNotIn("must-not-leak", repr(items))

    def test_data_freshness_warns_when_public_pricing_covers_hub_login_failure(self):
        with tempfile.NamedTemporaryFile(suffix=".sqlite") as tmp:
            conn = sqlite3.connect(tmp.name)
            conn.executescript(
                """
                create table metadata (key text primary key, value text not null);
                create table upstream_hub_channels (last_error text not null);
                create table upstream_adapter_status (status text not null);
                """
            )
            conn.executemany(
                "insert into metadata(key, value) values (?, ?)",
                [
                    ("last_upstream_hub_imported_at", "2026-07-12T02:30:00+00:00"),
                    ("kbq_pricing_updated_at", "2026-07-12T02:30:00+00:00"),
                ],
            )
            conn.execute("insert into upstream_hub_channels(last_error) values ('login expired with private detail')")
            conn.execute("insert into upstream_adapter_status(status) values ('ok')")
            conn.commit()
            conn.close()

            items = self.mod.collect_data_freshness(
                tmp.name,
                "/missing/dashboard.html",
                now=datetime(2026, 7, 12, 3, 0, tzinfo=timezone.utc),
            )

        hub = next(item for item in items if item["id"] == "upstream_hub")
        self.assertEqual("warn", hub["tone"])
        self.assertEqual("public_pricing_fallback", hub["condition"])
        self.assertNotIn("private detail", repr(items))

    def test_parse_systemd_timer_marks_inactive_as_risk(self):
        timer = self.mod.parse_systemd_timer_show(
            "ActiveState=inactive\nSubState=dead\nLastTriggerUSec=Sat 2026-07-11 04:15:00 UTC\n",
            "s2a-manager-backup.timer",
        )

        self.assertEqual("risk", timer["tone"])
        self.assertEqual("inactive", timer["active_state"])

    def test_latest_backup_file_reports_age_and_size(self):
        with tempfile.TemporaryDirectory() as tmp:
            backup = Path(tmp) / "sub2api-backup-20260712T000000Z.tar.gz"
            backup.write_bytes(b"backup")
            timestamp = datetime(2026, 7, 12, 0, 0, tzinfo=timezone.utc).timestamp()
            os.utime(backup, (timestamp, timestamp))

            result = self.mod.latest_backup_file(
                str(Path(tmp) / "sub2api-backup-*.tar.gz"),
                now=datetime(2026, 7, 12, 21, 0, tzinfo=timezone.utc),
            )

        self.assertEqual(6, result["size_bytes"])
        self.assertEqual("warn", result["tone"])
        self.assertEqual(21 * 3600, result["age_seconds"])

    def test_collect_metrics_caches_slow_checks(self):
        slow = {"services": [], "containers": {"available": True, "items": []}, "freshness": [], "backups": []}
        with mock.patch.object(self.mod, "collect_slow_metrics", return_value=slow) as collect:
            first = self.mod.collect_metrics("/tmp/ledger.sqlite", "/tmp/index.html")
            second = self.mod.collect_metrics("/tmp/ledger.sqlite", "/tmp/index.html")

        self.assertEqual(1, collect.call_count)
        self.assertEqual(first["services"], second["services"])

    def test_collect_metrics_shape(self):
        metrics = self.mod.collect_metrics()

        self.assertEqual("ok", metrics["status"])
        self.assertIn("cpu", metrics)
        self.assertIn("memory", metrics)
        self.assertIn("disk", metrics)
        self.assertIn("disks", metrics)
        self.assertIn("www", metrics["disks"])
        self.assertIn("net", metrics)
        self.assertIn("containers", metrics)
        self.assertIn("services", metrics)
        self.assertIn("freshness", metrics)
        self.assertIn("backups", metrics)

    def test_browser_status_is_current_coverage_requires_current_script(self):
        self.assertFalse(
            self.mod.browser_status_is_current_coverage(
                "browser_observed",
                "Chrome Tampermonkey read-only snapshot; account_lines=1; script=0.1.12",
            )
        )
        self.assertTrue(
            self.mod.browser_status_is_current_coverage(
                "browser_observed",
                "Chrome Tampermonkey read-only snapshot; account_lines=1; script=0.1.15; wait_state=stable",
            )
        )

    def test_automation_scope_describes_upstream_hub_not_safari_default(self):
        context = self.mod.automation_scope_context()

        self.assertIn("upstream-hub 脱敏快照导入", context["hourly"])
        self.assertIn("浏览器只读快照仅作为诊断兜底", context["hourly"])
        self.assertNotIn("Safari 已登录页面只读快照", context["hourly"])
        self.assertIn("--create-drafts", context["drafts"])


if __name__ == "__main__":
    unittest.main()
