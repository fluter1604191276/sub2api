#!/usr/bin/env python3
from __future__ import annotations

import importlib.util
import json
import sys
import tempfile
import unittest
from decimal import Decimal
from pathlib import Path


SCRIPT = Path(__file__).with_name("check_browser_snapshot_account_markers.py")


def load_module():
    spec = importlib.util.spec_from_file_location("check_browser_snapshot_account_markers", SCRIPT)
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class CheckBrowserSnapshotAccountMarkersTest(unittest.TestCase):
    def setUp(self) -> None:
        self.mod = load_module()
        self.tolerance = Decimal("0.000001")

    def check(self, provider: str, site: str, account_name: str, page_rate: float):
        return self.mod.check_account(
            provider,
            site,
            {
                "account_name": account_name,
                "upstream_group": "test",
                "page_rate": page_rate,
            },
            self.tolerance,
        )

    def test_kingdom_expression_compares_page_rate_and_cost_marker(self):
        row = self.check(
            "Kingdom",
            "api.tokenskingdom.com",
            "kingdom codex plus1号 仅文字1.7*0.078=0.1326",
            1.7,
        )

        self.assertEqual("OK", row.status)
        self.assertEqual(Decimal("1.7"), row.name_marker.page_rate_marker)
        self.assertEqual(Decimal("0.078"), row.name_marker.recharge_factor_marker)
        self.assertEqual(Decimal("0.1326"), row.name_marker.cost_marker)

    def test_text_rate_ignores_image_cent_marker(self):
        row = self.check(
            "Meow",
            "api.saki.lat",
            "meow codex 文字0.05 生图(可原可桥) 1/2/4k 5分",
            0.05,
        )

        self.assertEqual("OK", row.status)
        self.assertEqual(Decimal("0.05"), row.name_marker.text_rate_marker)
        self.assertEqual(Decimal("5"), row.name_marker.image_cent_marker)

    def test_image_price_only_is_informational(self):
        row = self.check(
            "Kingdom",
            "api.tokenskingdom.com",
            "kingdom codex 仅生图 5.1分",
            1,
        )

        self.assertEqual("INFO", row.status)
        self.assertEqual("image_price_only", row.name_marker.marker_kind)

    def test_real_page_rate_drift_is_reported(self):
        row = self.check(
            "超超 Mouubox",
            "api.mouubox.com",
            "超超(主站) codex 0.03",
            0.06,
        )

        self.assertEqual("DRIFT", row.status)
        self.assertIn("0.06x", row.reason)
        self.assertIn("0.03x", row.reason)
        self.assertIn("建议：账号名标注 0.03", row.reason)
        self.assertIn("更新为页面值 0.06", row.reason)

    def test_ambiguous_bare_numbers_are_informational(self):
        row = self.check(
            "Example",
            "example.test",
            "codex pro 0.1 不限客户端2",
            0.2,
        )

        self.assertEqual("INFO", row.status)
        self.assertEqual("ambiguous_rate", row.name_marker.marker_kind)
        self.assertIn("账号名含多个数字", row.reason)

    def test_generic_key_names_are_informational(self):
        row = self.check("钧澈", "vip.lcodex.cn", "非自用", 0.18)

        self.assertEqual("INFO", row.status)
        self.assertIn("未按我站账号命名规则", row.reason)

    def test_image_cent_requires_image_context(self):
        self.assertEqual(
            Decimal("5.1"),
            self.mod.extract_image_cent_marker("文字0.05 生图5.1分"),
        )
        self.assertIsNone(self.mod.extract_image_cent_marker("3.6分*0.91=3.2728分"))
        self.assertIsNone(self.mod.extract_image_cent_marker("钧澈 codex 仅生图 3.6分*0.91=3.2728分"))

    def test_image_price_expression_is_informational(self):
        row = self.check(
            "钧澈",
            "vip.lcodex.cn",
            "钧澈 codex 仅生图 3.6分*0.91=3.2728分",
            0.9,
        )

        self.assertEqual("INFO", row.status)
        self.assertEqual("image_price_only", row.name_marker.marker_kind)
        self.assertEqual(Decimal("3.2728"), row.name_marker.image_cent_marker)

    def test_load_rows_skips_legacy_tampermonkey_snapshot(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            snapshot = Path(tmpdir) / "latest.json"
            snapshot.write_text(
                json.dumps(
                    [
                        {
                            "provider": "Kingdom",
                            "site": "api.tokenskingdom.com",
                            "script_version": "0.1.12",
                            "detail": (
                                "Chrome Tampermonkey read-only snapshot; "
                                "account_lines=1; script=0.1.12"
                            ),
                            "detected_accounts": [
                                {
                                    "account_name": "kingdom codex 超级特惠 仅文字1*0.078=0.078",
                                    "upstream_group": "plus 1号池",
                                    "page_rate": 0.93,
                                }
                            ],
                        }
                    ],
                    ensure_ascii=False,
                )
            )

            rows = self.mod.load_rows(snapshot, self.tolerance)

        self.assertEqual([], rows)

    def test_load_rows_skips_preserved_account_snapshot(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            snapshot = Path(tmpdir) / "latest.json"
            snapshot.write_text(
                json.dumps(
                    [
                        {
                            "provider": "钧澈",
                            "site": "vip.lcodex.cn",
                            "script_version": "0.1.15",
                            "detail": (
                                "Chrome Tampermonkey read-only snapshot; "
                                "fresh_account_lines=0; preserved previous account lines "
                                "from 2026-06-12T18:00:00+00:00; script=0.1.15"
                            ),
                            "detected_accounts": [
                                {
                                    "account_name": "钧澈 codex pro 0.15*0.91=0.1365",
                                    "upstream_group": "GPT-PRO",
                                    "page_rate": 0.2,
                                }
                            ],
                        }
                    ],
                    ensure_ascii=False,
                )
            )

            rows = self.mod.load_rows(snapshot, self.tolerance)

        self.assertEqual([], rows)

    def test_load_rows_skips_preserved_non_empty_snapshot(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            snapshot = Path(tmpdir) / "latest.json"
            snapshot.write_text(
                json.dumps(
                    [
                        {
                            "provider": "Kingdom",
                            "site": "api.tokenskingdom.com",
                            "script_version": "0.1.15",
                            "detail": (
                                "latest read was empty; preserved previous non-empty snapshot "
                                "from 2026-06-15T06:14:15+00:00; script=0.1.15"
                            ),
                            "detected_accounts": [
                                {
                                    "account_name": "kingdom codex 超级特惠 仅文字1*0.078=0.078",
                                    "upstream_group": "plus 1号池",
                                    "page_rate": 0.93,
                                }
                            ],
                        }
                    ],
                    ensure_ascii=False,
                )
            )

            rows = self.mod.load_rows(snapshot, self.tolerance)

        self.assertEqual([], rows)


if __name__ == "__main__":
    unittest.main()
