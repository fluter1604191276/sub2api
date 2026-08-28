#!/usr/bin/env python3
from __future__ import annotations

import argparse
import importlib.util
import sys
import unittest
from decimal import Decimal
from pathlib import Path


SCRIPT = Path(__file__).with_name("audit_kbq_true_costs.py")


def load_module():
    spec = importlib.util.spec_from_file_location("audit_kbq_true_costs", SCRIPT)
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class AuditKBQTrueCostsTest(unittest.TestCase):
    def setUp(self) -> None:
        self.mod = load_module()

    def test_kbq_cny_cost_uses_same_numeric_unit_as_site_balance(self):
        user_billed = Decimal("0.00366548")
        upstream_cost = Decimal("0.01532331")

        self.assertEqual("REAL_LOSS", self.mod.status_for(user_billed, upstream_cost))
        self.assertEqual(Decimal("-0.01165783"), user_billed - upstream_cost)

    def test_usage_query_falls_back_to_total_cost_for_account_display(self):
        captured: dict[str, str] = {}
        original = self.mod.run_psql

        def fake_run_psql(sql, _args):
            captured["sql"] = sql
            return "[]"

        self.mod.run_psql = fake_run_psql
        self.addCleanup(lambda: setattr(self.mod, "run_psql", original))

        rows = self.mod.load_usage_buckets(argparse.Namespace(hours=24))

        self.assertEqual([], rows)
        normalized_sql = " ".join(captured["sql"].split())
        self.assertIn(
            "coalesce(ul.account_stats_cost, ul.total_cost)",
            normalized_sql,
        )
        self.assertIn("service_tier", normalized_sql)

    def test_fast_service_tier_is_normalized_to_priority(self):
        self.assertEqual("priority", self.mod.normalize_service_tier(" fast "))
        self.assertEqual("priority", self.mod.normalize_service_tier("priority"))
        self.assertEqual("default", self.mod.normalize_service_tier(None))

    def test_default_window_keeps_30_day_losses_visible(self):
        args = self.mod.parse_args([])

        self.assertEqual(720, args.hours)

    def test_group_ratio_prefers_explicit_account_metadata(self):
        pricing = {"group_ratio": {"default": 1, "GPT-plus": 0.6}}
        item = {"enable_groups": ["default", "GPT-plus"]}

        ratio, group_key, source = self.mod.resolve_group_ratio(
            pricing,
            item,
            account_name="ambiguous account",
            explicit_group="GPT-plus",
        )

        self.assertEqual(Decimal("0.6"), ratio)
        self.assertEqual("GPT-plus", group_key)
        self.assertEqual("account_metadata", source)

    def test_group_ratio_uses_single_enabled_group_without_name_guessing(self):
        pricing = {"group_ratio": {"default": 1, "GPT-plus": 0.6}}
        item = {"enable_groups": ["default"]}

        ratio, group_key, source = self.mod.resolve_group_ratio(
            pricing,
            item,
            account_name="KBQ DeepSeek",
            explicit_group="",
        )

        self.assertEqual(Decimal("1"), ratio)
        self.assertEqual("default", group_key)
        self.assertEqual("single_enabled_group", source)

    def test_ambiguous_group_ratio_fails_closed(self):
        pricing = {"group_ratio": {"default": 1, "GPT-plus": 0.6}}
        item = {"enable_groups": ["default", "GPT-plus"]}

        ratio, group_key, source = self.mod.resolve_group_ratio(
            pricing,
            item,
            account_name="ambiguous account",
            explicit_group="",
        )

        self.assertIsNone(ratio)
        self.assertEqual("", group_key)
        self.assertEqual("ambiguous", source)

    def test_deepseek_without_recorded_tool_usage_is_explicitly_unknown(self):
        self.assertTrue(self.mod.tool_fee_is_unknown("[特价]deepseek-v4-flash"))
        self.assertFalse(self.mod.tool_fee_is_unknown("gpt-5.5"))

    def test_duplicate_model_variants_with_different_prices_fail_closed(self):
        candidates = [
            {
                "quota_type": 0,
                "model_ratio": 0.1,
                "completion_ratio": 2,
                "cache_ratio": 0.02,
                "enable_groups": ["default"],
            },
            {
                "quota_type": 0,
                "model_ratio": 0.2,
                "completion_ratio": 2,
                "cache_ratio": 0.02,
                "enable_groups": ["default"],
            },
        ]

        item, ratio, group_key, source = self.mod.select_upstream_item(
            candidates,
            {"group_ratio": {"default": 1}},
            "default",
        )

        self.assertIsNone(item)
        self.assertIsNone(ratio)
        self.assertEqual("", group_key)
        self.assertEqual("ambiguous_model_variant", source)

    def test_missing_model_ratio_produces_missing_prices(self):
        prices = self.mod.live_prices(
            {"quota_type": 0, "completion_ratio": 2},
            Decimal("1"),
            Decimal("0.9"),
        )

        self.assertIsNone(prices.input)
        self.assertIsNone(prices.output)

    def test_unpriced_revenue_is_excluded_from_margin(self):
        rows = [
            {
                "request_count": 1,
                "user_billed_cost": Decimal("1.2"),
                "true_upstream_cost": Decimal("1.0"),
                "status": "OK",
                "display_status": "OK",
                "tool_fee_unknown": False,
                "cache_creation_1h_tokens": 0,
            },
            {
                "request_count": 1,
                "user_billed_cost": Decimal("0.8"),
                "true_upstream_cost": None,
                "status": "NO_PRICE",
                "display_status": "-",
                "tool_fee_unknown": False,
                "cache_creation_1h_tokens": 0,
            },
        ]
        args = argparse.Namespace(hours=720, pricing_url="https://example.test/api/pricing")

        summary = self.mod.summarize_rows(args, {"pricing_version": "v1"}, Decimal("0.9"), rows)

        self.assertEqual(Decimal("2.0"), summary["user_billed_cost"])
        self.assertEqual(Decimal("1.2"), summary["comparable_user_billed_cost"])
        self.assertEqual(Decimal("0.8"), summary["unpriced_user_billed_cost"])
        self.assertEqual(Decimal("0.2"), summary["margin"])


if __name__ == "__main__":
    unittest.main()
