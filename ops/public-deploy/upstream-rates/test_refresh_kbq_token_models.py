#!/usr/bin/env python3
from __future__ import annotations

import importlib.util
import sqlite3
import sys
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("refresh_kbq_token_models.py")


def load_module():
    spec = importlib.util.spec_from_file_location("refresh_kbq_token_models", SCRIPT)
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class RefreshKbqTokenModelsTest(unittest.TestCase):
    def setUp(self) -> None:
        self.mod = load_module()

    def pricing(self):
        return {
            "success": True,
            "pricing_version": "test-version",
            "group_ratio": {"default": 1, "GPT-plus": 0.6, "GPT-pro": 1},
            "data": [
                {
                    "model_name": "[plus]gpt-5.5",
                    "quota_type": 0,
                    "model_ratio": 0.3,
                    "completion_ratio": 6,
                    "cache_ratio": 0.1,
                    "create_cache_ratio": 1.25,
                    "enable_groups": ["GPT-plus", "default"],
                    "supported_endpoint_types": ["openai"],
                },
                {
                    "model_name": "[plus]gpt-5.4",
                    "quota_type": 0,
                    "model_ratio": 0.15,
                    "completion_ratio": 6,
                    "cache_ratio": 0.1,
                    "create_cache_ratio": 1.25,
                    "enable_groups": ["GPT-plus", "default"],
                    "supported_endpoint_types": ["openai"],
                },
                {
                    "model_name": "[特价]deepseek-v4-flash",
                    "quota_type": 0,
                    "model_ratio": 0.1,
                    "completion_ratio": 2,
                    "cache_ratio": 0.02,
                    "create_cache_ratio": None,
                    "enable_groups": ["default"],
                    "supported_endpoint_types": ["openai"],
                },
                {
                    "model_name": "vendor-model-without-official-baseline",
                    "quota_type": 0,
                    "model_ratio": 0.25,
                    "completion_ratio": 3,
                    "cache_ratio": 0.1,
                    "create_cache_ratio": None,
                    "enable_groups": ["default"],
                    "supported_endpoint_types": ["openai"],
                },
            ],
        }

    def mixed_claude_pricing(self):
        return {
            "success": True,
            "pricing_version": "test-version",
            "group_ratio": {"default": 1},
            "data": [
                {
                    "model_name": "[稳定AG量]claude-sonnet-4-6",
                    "quota_type": 0,
                    "model_ratio": 0.75,
                    "completion_ratio": 5,
                    "cache_ratio": 0.1,
                    "create_cache_ratio": 1.25,
                    "supported_endpoint_types": ["anthropic", "openai"],
                },
                {
                    "model_name": "[Azure量]claude-haiku-4-5",
                    "quota_type": 0,
                    "model_ratio": 0.2,
                    "completion_ratio": 5,
                    "cache_ratio": 0.1,
                    "create_cache_ratio": 1.25,
                    "supported_endpoint_types": ["anthropic", "openai"],
                },
            ],
        }

    def test_kbq_codex_plus_cost_uses_live_model_ratio_after_recharge(self):
        rows = self.mod.build_rows(self.pricing(), "https://example.test/api/pricing", 0.9)
        by_name = {row["model_name"]: row for row in rows}

        self.assertAlmostEqual(0.108, by_name["[plus]gpt-5.5"]["cost_multiplier"])
        self.assertAlmostEqual(0.108, by_name["[plus]gpt-5.4"]["cost_multiplier"])
        self.assertAlmostEqual(0.54, by_name["[plus]gpt-5.5"]["input_usd_per_1m"])
        self.assertAlmostEqual(3.24, by_name["[plus]gpt-5.5"]["output_usd_per_1m"])
        self.assertEqual("model_variant", by_name["[plus]gpt-5.5"]["kbq_group_key"])

    def test_deepseek_is_included_with_live_absolute_prices(self):
        rows = self.mod.build_rows(self.pricing(), "https://example.test/api/pricing", 0.9)
        by_name = {row["model_name"]: row for row in rows}

        row = by_name["[特价]deepseek-v4-flash"]
        self.assertEqual("DeepSeek", row["category"])
        self.assertEqual("NO_OFFICIAL_BASELINE", row["pricing_status"])
        self.assertAlmostEqual(0.18, row["input_usd_per_1m"])
        self.assertAlmostEqual(0.36, row["output_usd_per_1m"])
        self.assertAlmostEqual(0.0036, row["cache_read_usd_per_1m"])
        self.assertIsNone(row["cost_multiplier"])

    def test_bare_multi_group_model_keeps_each_group_variant(self):
        pricing = self.pricing()
        pricing["data"].append(
            {
                "model_name": "gpt-5.5",
                "quota_type": 0,
                "model_ratio": 0.5,
                "completion_ratio": 6,
                "cache_ratio": 0.1,
                "create_cache_ratio": 1.25,
                "enable_groups": ["GPT-plus", "GPT-pro"],
                "supported_endpoint_types": ["openai"],
            }
        )

        rows = self.mod.build_rows(pricing, "https://example.test/api/pricing", 0.9)
        variants = {
            row["kbq_group_key"]: row
            for row in rows
            if row["model_name"] == "gpt-5.5"
        }

        self.assertAlmostEqual(0.108, variants["GPT-plus"]["cost_multiplier"])
        self.assertAlmostEqual(0.18, variants["GPT-pro"]["cost_multiplier"])

    def test_schema_upgrade_rebuilds_old_unique_index(self):
        conn = sqlite3.connect(":memory:")
        conn.executescript(
            """
            create table kbq_token_model_records (
              id integer primary key autoincrement,
              category text not null,
              model_name text not null,
              base_model text not null,
              kbq_group_key text not null default '',
              pricing_status text not null default 'OK',
              cost_multiplier real,
              unique(category, model_name)
            );
            """
        )

        self.mod.ensure_token_table_schema(conn)

        self.assertTrue(self.mod.token_table_has_current_unique_index(conn))
        conn.close()

    def test_unknown_official_baseline_is_visible_instead_of_dropped(self):
        rows = self.mod.build_rows(self.pricing(), "https://example.test/api/pricing", 0.9)
        by_name = {row["model_name"]: row for row in rows}

        row = by_name["vendor-model-without-official-baseline"]
        self.assertEqual("NO_OFFICIAL_BASELINE", row["pricing_status"])
        self.assertIsNone(row["cost_multiplier"])
        self.assertAlmostEqual(0.45, row["input_usd_per_1m"])

    def test_curated_kbq_plus_ledger_row_is_reconciled_from_model_records(self):
        conn = sqlite3.connect(":memory:")
        conn.row_factory = sqlite3.Row
        conn.execute(
            """
            create table upstream_rate_records (
              id integer primary key autoincrement,
              category text not null,
              kind text not null,
              site text not null,
              fluter_account_name text not null,
              upstream_group text not null,
              page_rate real,
              recharge_factor real not null,
              actual_cost_label text not null default '',
              note text not null,
              updated_at text not null
            )
            """
        )
        conn.execute(
            """
            insert into upstream_rate_records (
              category, kind, site, fluter_account_name, upstream_group,
              page_rate, recharge_factor, actual_cost_label, note, updated_at
            ) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                "KBQ",
                "Codex",
                "xn--vduyey89e.com",
                "KBQ codex plus 仅文字0.12*0.9=0.108",
                "[plus]gpt-5.4 / [plus]gpt-5.5",
                0.08,
                0.9,
                "实际成本倍率 0.072x（页面倍率 0.08 × 充值系数 0.9）",
                "",
                "old",
            ),
        )
        rows = self.mod.build_rows(self.pricing(), "https://example.test/api/pricing", 0.9)

        changed = self.mod.refresh_curated_kbq_ledger_rows(conn, rows, 0.9, "now")

        self.assertEqual(1, changed)
        row = conn.execute("select * from upstream_rate_records").fetchone()
        self.assertAlmostEqual(0.12, row["page_rate"])
        self.assertIn("实际成本倍率 0.108x", row["actual_cost_label"])
        self.assertIn("KBQ价格同步", row["note"])
        conn.close()

    def test_curated_kbq_notes_list_each_mapped_model_cost(self):
        conn = sqlite3.connect(":memory:")
        conn.row_factory = sqlite3.Row
        conn.execute(
            """
            create table upstream_rate_records (
              id integer primary key autoincrement,
              category text not null,
              kind text not null,
              site text not null,
              fluter_account_name text not null,
              upstream_group text not null,
              page_rate real,
              recharge_factor real not null,
              actual_cost_label text not null default '',
              note text not null,
              updated_at text not null
            )
            """
        )
        conn.execute(
            """
            insert into upstream_rate_records (
              category, kind, site, fluter_account_name, upstream_group,
              page_rate, recharge_factor, actual_cost_label, note, updated_at
            ) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                "KBQ",
                "Claude",
                "xn--vduyey89e.com",
                "KBQ claude anti稳定 0.5*0.9=0.45",
                "[稳定AG量] Claude + [Azure量]haiku",
                0.5,
                0.9,
                "",
                "",
                "old",
            ),
        )
        rows = self.mod.build_rows(
            self.mixed_claude_pricing(),
            "https://example.test/api/pricing",
            0.9,
        )

        changed = self.mod.refresh_curated_kbq_ledger_rows(conn, rows, 0.9, "now")

        self.assertEqual(1, changed)
        row = conn.execute("select * from upstream_rate_records").fetchone()
        self.assertAlmostEqual(0.5, row["page_rate"])
        self.assertIn("[稳定AG量]claude-sonnet-4-6=0.45x", row["note"])
        self.assertIn("[Azure量]claude-haiku-4-5=0.36x", row["note"])
        self.assertIn("本账号池最高当前真实成本 0.45x", row["note"])
        conn.close()


if __name__ == "__main__":
    unittest.main()
