#!/usr/bin/env python3
from __future__ import annotations

import importlib.util
import sys
import unittest
from pathlib import Path


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
        parsed = self.mod.parse_docker_ps("sub2api-backend-1\tUp 2 hours (healthy)\nsub2api-db-1\tExited (1) 1 minute ago\n")

        self.assertEqual("sub2api-backend-1", parsed[0]["name"])
        self.assertEqual("ok", parsed[0]["health"])
        self.assertEqual("warn", parsed[1]["health"])

    def test_collect_metrics_shape(self):
        metrics = self.mod.collect_metrics()

        self.assertEqual("ok", metrics["status"])
        self.assertIn("cpu", metrics)
        self.assertIn("memory", metrics)
        self.assertIn("disk", metrics)
        self.assertIn("net", metrics)
        self.assertIn("containers", metrics)

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
