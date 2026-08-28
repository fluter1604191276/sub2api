#!/usr/bin/env python3
from __future__ import annotations

import importlib.util
import argparse
import contextlib
import io
import json
import sqlite3
import sys
import tempfile
import unittest
from decimal import Decimal
from pathlib import Path


SCRIPT = Path(__file__).with_name("audit_kbq_configuration.py")


def load_module():
    spec = importlib.util.spec_from_file_location("audit_kbq_configuration", SCRIPT)
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class AuditKBQConfigurationTest(unittest.TestCase):
    def setUp(self) -> None:
        self.mod = load_module()

    def test_deepseek_half_price_group_is_detected_before_usage_exists(self):
        row = self.mod.evaluate_mapping(
            account={
                "account_id": 7788,
                "account_name": "KBQ openai DeepSeek 特价",
                "group_id": 13,
                "group_name": "deepseek",
                "group_rate_multiplier": Decimal("0.5"),
                "minimum_user_multiplier": Decimal("0.5"),
                "channel_status": "inactive",
                "group_web_search_price_per_call": None,
                "explicit_kbq_group": "",
            },
            requested_model="deepseek-v4-flash",
            upstream_item={
                "model_name": "[特价]deepseek-v4-flash",
                "model_ratio": 0.1,
                "completion_ratio": 2,
                "cache_ratio": 0.02,
                "create_cache_ratio": None,
                "enable_groups": ["default"],
            },
            pricing={"group_ratio": {"default": 1}},
            recharge_factor=Decimal("0.9"),
            local_prices=self.mod.BasePricingTiers(
                default=self.mod.TokenPrices(
                    input=Decimal("0.14"),
                    output=Decimal("0.28"),
                    cache_read=Decimal("0.0028"),
                    cache_write=None,
                ),
            ),
        )

        self.assertEqual("REAL_LOSS", row["status"])
        self.assertAlmostEqual(0.18, float(row["upstream_input_price"]))
        self.assertAlmostEqual(0.07, float(row["site_input_price"]))
        self.assertGreater(float(row["minimum_break_even_multiplier"]), 1.28)
        self.assertTrue(row["tool_fee_unknown"])

    def test_safe_non_deepseek_mapping_reports_ok(self):
        row = self.mod.evaluate_mapping(
            account={
                "account_id": 1,
                "account_name": "KBQ safe",
                "group_id": 2,
                "group_name": "safe",
                "group_rate_multiplier": Decimal("1.5"),
                "minimum_user_multiplier": Decimal("1.5"),
                "channel_status": "active",
                "group_web_search_price_per_call": Decimal("0.01"),
                "explicit_kbq_group": "default",
            },
            requested_model="gpt-safe",
            upstream_item={
                "model_name": "gpt-safe",
                "model_ratio": 0.5,
                "completion_ratio": 4,
                "cache_ratio": 0.1,
                "create_cache_ratio": None,
                "enable_groups": ["default"],
            },
            pricing={"group_ratio": {"default": 1}},
            recharge_factor=Decimal("0.9"),
            local_prices=self.mod.BasePricingTiers(
                default=self.mod.TokenPrices(
                    input=Decimal("1"),
                    output=Decimal("4"),
                    cache_read=Decimal("0.1"),
                    cache_write=None,
                ),
            ),
        )

        self.assertEqual("OK", row["status"])
        self.assertEqual("default", row["checked_service_tiers"])
        self.assertFalse(row["tool_fee_unknown"])

    def test_pool_safety_uses_highest_routable_account_cost(self):
        rows = [
            {
                "account_id": 1,
                "account_name": "cheap",
                "account_status": "active",
                "schedulable": True,
                "group_id": 13,
                "group_status": "active",
                "requested_model": "deepseek-v4-flash",
                "minimum_user_multiplier": Decimal("2"),
                "minimum_safe_user_multiplier": Decimal("1.2"),
                "status": "OK",
                "note": "cheap account is individually safe",
            },
            {
                "account_id": 2,
                "account_name": "expensive",
                "account_status": "active",
                "schedulable": True,
                "group_id": 13,
                "group_status": "active",
                "requested_model": "deepseek-v4-flash",
                "minimum_user_multiplier": Decimal("2"),
                "minimum_safe_user_multiplier": Decimal("3.8"),
                "status": "REAL_LOSS",
                "note": "expensive account loses",
            },
        ]

        self.mod.apply_pool_safety(rows)

        self.assertEqual("POOL_PRICE_UNDERCUT", rows[0]["status"])
        self.assertEqual("POOL_PRICE_UNDERCUT", rows[0]["pool_status"])
        self.assertEqual(Decimal("3.8"), rows[0]["pool_minimum_safe_user_multiplier"])
        self.assertEqual(2, rows[0]["pool_price_source_account_id"])
        self.assertEqual(2, rows[0]["pool_account_count"])
        self.assertEqual("REAL_LOSS", rows[1]["status"])
        self.assertEqual("POOL_PRICE_UNDERCUT", rows[1]["pool_status"])

    def test_pool_safety_excludes_dormant_draft_accounts(self):
        rows = [
            {
                "account_id": 1,
                "account_name": "live",
                "account_status": "active",
                "schedulable": True,
                "group_id": 13,
                "group_status": "active",
                "requested_model": "deepseek-v4-flash",
                "minimum_user_multiplier": Decimal("2"),
                "minimum_safe_user_multiplier": Decimal("1.2"),
                "status": "OK",
                "note": "",
            },
            {
                "account_id": 2,
                "account_name": "disabled draft",
                "account_status": "active",
                "schedulable": False,
                "group_id": 13,
                "group_status": "active",
                "requested_model": "deepseek-v4-flash",
                "minimum_user_multiplier": Decimal("2"),
                "minimum_safe_user_multiplier": Decimal("9"),
                "status": "REAL_LOSS",
                "note": "",
            },
        ]

        self.mod.apply_pool_safety(rows)

        self.assertEqual("OK", rows[0]["status"])
        self.assertEqual(Decimal("1.2"), rows[0]["pool_minimum_safe_user_multiplier"])
        self.assertEqual(1, rows[0]["pool_account_count"])
        self.assertEqual("NOT_EVALUATED", rows[1].get("pool_status", "NOT_EVALUATED"))

    def test_deepseek_half_price_group_also_exposes_tool_fee_loss(self):
        row = self.mod.evaluate_mapping(
            account={
                "account_id": 7788,
                "account_name": "KBQ DeepSeek",
                "group_id": 13,
                "group_name": "deepseek",
                "group_rate_multiplier": Decimal("0.5"),
                "minimum_user_multiplier": Decimal("0.5"),
                "channel_status": "inactive",
                "group_web_search_price_per_call": None,
            },
            requested_model="deepseek-v4-flash",
            upstream_item={
                "model_name": "[特价]deepseek-v4-flash",
                "model_ratio": 0.1,
                "completion_ratio": 2,
                "cache_ratio": 0.02,
                "enable_groups": ["default"],
            },
            pricing={"group_ratio": {"default": 1}},
            recharge_factor=Decimal("0.9"),
            local_prices=self.mod.BasePricingTiers(
                default=self.mod.TokenPrices(
                    input=Decimal("0.14"), output=Decimal("0.28"), cache_read=Decimal("0.0028")
                ),
            ),
        )

        self.assertEqual("REAL_LOSS", row["status"])
        self.assertEqual("TOOL_FEE_UNCOVERED_LOSS", row["tool_fee_status"])
        self.assertEqual(Decimal("0.009"), row["upstream_web_search_price_per_call"])
        self.assertEqual(Decimal("0"), row["site_web_search_price_per_call"])
        self.assertIn("configured post-multiplier price would be 0.005", row["note"])

    def test_inactive_channel_is_not_used_as_local_price(self):
        account = {
            "group_id": 13,
            "group_platform": "openai",
            "channel_status": "inactive",
        }
        pricing_rows = [{
            "group_id": 13,
            "platform": "openai",
            "models": ["deepseek-v4-flash"],
            "input_price": "9",
        }]

        self.assertIsNone(
            self.mod.matching_channel_pricing(
                account, "deepseek-v4-flash", pricing_rows
            )
        )

    def test_active_channel_price_overrides_fallback(self):
        base = self.mod.BasePricingTiers(
            default=self.mod.TokenPrices(
                input=Decimal("0.14"),
                output=Decimal("0.28"),
                cache_read=Decimal("0.0028"),
            ),
        )
        resolved, source = self.mod.apply_channel_pricing(
            base,
            {
                "billing_mode": "token",
                "input_price": "0.4",
                "output_price": "0.8",
                "cache_read_price": "0.04",
                "cache_write_price": None,
                "intervals": [],
            },
        )

        self.assertEqual("channel_flat", source)
        self.assertEqual(Decimal("0.4"), resolved.default.input)
        self.assertEqual(Decimal("0.8"), resolved.default.output)
        self.assertEqual(Decimal("0.04"), resolved.default.cache_read)
        priority = self.mod.effective_priority_prices(resolved)
        self.assertEqual(Decimal("0.4"), priority.input)
        self.assertEqual(Decimal("0.8"), priority.output)
        self.assertEqual(Decimal("0.04"), priority.cache_read)

    def test_partial_channel_override_preserves_existing_priority_dimensions(self):
        base = self.mod.BasePricingTiers(
            default=self.mod.TokenPrices(
                input=Decimal("5"),
                output=Decimal("25"),
                cache_read=Decimal("0.5"),
            ),
            priority_explicit=self.mod.TokenPrices(
                input=Decimal("10"),
                output=Decimal("40"),
                cache_read=Decimal("1"),
            ),
        )
        resolved, _ = self.mod.apply_channel_pricing(
            base,
            {
                "billing_mode": "token",
                "input_price": "6",
                "output_price": None,
                "cache_read_price": None,
                "cache_write_price": None,
                "intervals": [],
            },
        )

        priority = self.mod.effective_priority_prices(resolved)
        self.assertEqual(Decimal("6"), priority.input)
        self.assertEqual(Decimal("40"), priority.output)
        self.assertEqual(Decimal("1"), priority.cache_read)

    def test_single_candidate_still_fails_closed_when_group_is_ambiguous(self):
        selected = self.mod.select_upstream_item(
            [{
                "model_name": "same-model",
                "quota_type": 0,
                "model_ratio": 0.1,
                "completion_ratio": 2,
                "enable_groups": ["default", "GPT-plus"],
            }],
            {"group_ratio": {"default": 1, "GPT-plus": 0.6}},
            "",
        )

        self.assertIsNotNone(selected)
        row = self.mod.evaluate_mapping(
            account={"account_id": 1},
            requested_model="model",
            upstream_item=selected,
            pricing={"group_ratio": {"default": 1, "GPT-plus": 0.6}},
            recharge_factor=Decimal("0.9"),
            local_prices=self.mod.BasePricingTiers(
                default=self.mod.TokenPrices(input=Decimal("1"), output=Decimal("2")),
            ),
        )
        self.assertEqual("AMBIGUOUS_GROUP_RATIO", row["status"])

    def test_conflicting_active_channel_prices_fail_closed(self):
        account = {
            "group_id": 13,
            "group_platform": "openai",
            "channel_status": "active",
        }
        rows = [
            {"group_id": 13, "platform": "openai", "models": ["deepseek-v4-flash"], "input_price": "0.14"},
            {"group_id": 13, "platform": "openai", "models": ["deepseek-v4-flash"], "input_price": "0.36"},
        ]

        row, status = self.mod.resolve_channel_pricing(account, "deepseek-v4-flash", rows)
        self.assertIsNone(row)
        self.assertEqual("ambiguous", status)

    def test_identical_duplicate_active_channel_prices_are_safe(self):
        account = {
            "group_id": 13,
            "group_platform": "openai",
            "channel_status": "active",
        }
        rows = [
            {"group_id": 13, "platform": "openai", "models": ["deepseek-v4-flash"], "input_price": "0.14"},
            {"group_id": 13, "platform": "openai", "models": ["deepseek-v4-flash"], "input_price": "0.14"},
        ]

        row, status = self.mod.resolve_channel_pricing(account, "deepseek-v4-flash", rows)
        self.assertIsNotNone(row)
        self.assertEqual("matched", status)

    def test_sparse_interval_uses_zero_for_omitted_dimensions(self):
        base = self.mod.BasePricingTiers(
            default=self.mod.TokenPrices(
                input=Decimal("1"),
                output=Decimal("4"),
                cache_read=Decimal("0.1"),
                cache_write=Decimal("1.25"),
            ),
        )
        resolved, source = self.mod.apply_channel_pricing(
            base,
            {
                "billing_mode": "token",
                "input_price": None,
                "output_price": None,
                "cache_read_price": None,
                "cache_write_price": None,
                "intervals": [{"min_tokens": 0, "max_tokens": 1000, "input_price": "2"}],
            },
        )

        self.assertEqual("channel_intervals_conservative", source)
        self.assertEqual(Decimal("1"), resolved.default.input)
        self.assertEqual(Decimal("0"), resolved.default.output)
        self.assertEqual(Decimal("0"), resolved.default.cache_read)
        self.assertEqual(Decimal("0"), resolved.default.cache_write)
        priority = self.mod.effective_priority_prices(resolved)
        self.assertEqual(Decimal("2"), priority.input)
        self.assertEqual(Decimal("0"), priority.output)
        self.assertEqual(Decimal("0"), priority.cache_read)
        self.assertEqual(Decimal("0"), priority.cache_write)

    def test_channel_binding_ambiguity_fails_closed(self):
        context = self.mod.resolve_billing_model_context(
            {
                "channel_binding_count": 2,
                "channel_status": "active",
                "group_platform": "openai",
            },
            "alias",
            "upstream-model",
        )

        self.assertEqual("CHANNEL_BINDING_AMBIGUOUS", context.error_status)

    def test_billing_model_source_resolves_requested_channel_and_upstream(self):
        base = {
            "channel_binding_count": 1,
            "channel_status": "active",
            "group_platform": "openai",
            "channel_model_mapping": {"openai": {"alias": "channel-model"}},
        }
        expected = {
            "requested": "alias",
            "channel_mapped": "channel-model",
            "upstream": "upstream-model",
        }

        for source, billing_model in expected.items():
            with self.subTest(source=source):
                context = self.mod.resolve_billing_model_context(
                    {**base, "billing_model_source": source},
                    "alias",
                    "upstream-model",
                )
                self.assertEqual("channel-model", context.channel_mapped_model)
                self.assertEqual(billing_model, context.billing_model)
                self.assertEqual(source, context.billing_model_source)
                self.assertEqual("", context.error_status)

    def test_channel_mapping_uses_exact_before_wildcard(self):
        mapping = {
            "openai": {
                "deepseek-v4-*": "wild-target",
                "deepseek-v4-flash": "exact-target",
            }
        }

        mapped, error = self.mod.resolve_channel_mapped_model(
            mapping,
            "openai",
            "deepseek-v4-flash",
        )

        self.assertEqual("exact-target", mapped)
        self.assertEqual("", error)

    def test_channel_mapping_supports_single_wildcard(self):
        mapped, error = self.mod.resolve_channel_mapped_model(
            {"openai": {"deepseek-*": "deepseek-v4-pro"}},
            "openai",
            "deepseek-chat",
        )

        self.assertEqual("deepseek-v4-pro", mapped)
        self.assertEqual("", error)

    def test_invalid_billing_model_source_fails_closed(self):
        context = self.mod.resolve_billing_model_context(
            {
                "channel_binding_count": 1,
                "channel_status": "active",
                "billing_model_source": "cheapest",
                "group_platform": "openai",
            },
            "alias",
            "upstream-model",
        )

        self.assertEqual("INVALID_BILLING_MODEL_SOURCE", context.error_status)

    def test_break_even_is_computed_for_default_priority_and_flex(self):
        row = self.mod.evaluate_mapping(
            account={
                "account_id": 1,
                "group_rate_multiplier": Decimal("2"),
                "minimum_user_multiplier": Decimal("2"),
            },
            requested_model="model",
            billing_model="model",
            billing_model_source="requested",
            channel_mapped_model="model",
            upstream_item={
                "model_name": "model",
                "model_ratio": 0.5,
                "completion_ratio": 4,
                "enable_groups": ["default"],
            },
            pricing={"group_ratio": {"default": 1}},
            recharge_factor=Decimal("1"),
            local_prices=self.mod.BasePricingTiers(
                default=self.mod.TokenPrices(
                    input=Decimal("1"),
                    output=Decimal("4"),
                ),
            ),
        )

        self.assertEqual(Decimal("1"), row["minimum_break_even_multiplier"])
        self.assertEqual(Decimal("0.5"), row["minimum_break_even_multiplier_priority"])
        self.assertEqual(Decimal("2"), row["minimum_break_even_multiplier_flex"])
        self.assertEqual(Decimal("1"), row["minimum_safe_user_multiplier"])
        self.assertEqual("OK", row["status"])

    def test_flex_loss_blocks_even_when_default_is_safe(self):
        row = self.mod.evaluate_mapping(
            account={
                "account_id": 1,
                "group_platform": "openai",
                "group_rate_multiplier": Decimal("1"),
                "minimum_user_multiplier": Decimal("1"),
            },
            requested_model="model",
            billing_model="model",
            billing_model_source="requested",
            channel_mapped_model="model",
            upstream_item={
                "model_name": "model",
                "model_ratio": 0.5,
                "completion_ratio": 4,
                "enable_groups": ["default"],
            },
            pricing={"group_ratio": {"default": 1}},
            recharge_factor=Decimal("1"),
            local_prices=self.mod.BasePricingTiers(
                default=self.mod.TokenPrices(
                    input=Decimal("1"),
                    output=Decimal("4"),
                ),
            ),
        )

        self.assertEqual("REAL_LOSS", row["status"])
        self.assertEqual("default,priority,flex", row["checked_service_tiers"])
        self.assertEqual(Decimal("2"), row["minimum_safe_user_multiplier"])
        self.assertIn("flex", row["note"])

    def test_filtering_flex_uses_only_default_and_priority_break_even(self):
        row = self.mod.evaluate_mapping(
            account={
                "account_id": 1,
                "account_platform": "openai",
                "account_type": "api_key",
                "group_platform": "openai",
                "group_rate_multiplier": Decimal("1"),
                "minimum_user_multiplier": Decimal("1"),
            },
            requested_model="deepseek-v4-flash",
            mapped_upstream_model="[special]deepseek-v4-flash",
            upstream_item={
                "model_name": "[special]deepseek-v4-flash",
                "model_ratio": 0.5,
                "completion_ratio": 4,
                "enable_groups": ["default"],
            },
            pricing={"group_ratio": {"default": 1}},
            recharge_factor=Decimal("1"),
            local_prices=self.mod.BasePricingTiers(
                default=self.mod.TokenPrices(input=Decimal("1"), output=Decimal("4")),
            ),
            fast_policy_settings={
                "rules": [{
                    "service_tier": "flex",
                    "action": "filter",
                    "scope": "apikey",
                    "model_whitelist": ["[special]deepseek-*"],
                }],
            },
        )

        self.assertEqual("default,priority", row["checked_service_tiers"])
        self.assertEqual(Decimal("1"), row["minimum_safe_user_multiplier"])

    def test_force_priority_makes_flex_share_priority_break_even(self):
        tiers = self.mod.reachable_service_tiers(
            {
                "account_platform": "openai",
                "account_type": "api_key",
                "group_platform": "openai",
            },
            "deepseek-v4-flash",
            {
                "rules": [{
                    "service_tier": "flex",
                    "action": "force_priority",
                    "scope": "all",
                }],
            },
        )

        self.assertEqual(("default", "priority"), tiers)

    def test_oauth_scoped_flex_block_does_not_hide_api_key_risk(self):
        tiers = self.mod.reachable_service_tiers(
            {
                "account_platform": "openai",
                "account_type": "api_key",
                "group_platform": "openai",
            },
            "deepseek-v4-flash",
            {
                "rules": [{
                    "service_tier": "flex",
                    "action": "block",
                    "scope": "oauth",
                }],
            },
        )

        self.assertEqual(("default", "priority", "flex"), tiers)

    def test_auto_rule_can_make_priority_reachable_after_direct_tiers_are_blocked(self):
        tiers = self.mod.reachable_service_tiers(
            {
                "account_platform": "openai",
                "account_type": "api_key",
                "group_platform": "openai",
            },
            "deepseek-v4-flash",
            {
                "rules": [
                    {
                        "service_tier": "priority",
                        "action": "block",
                        "scope": "all",
                    },
                    {
                        "service_tier": "flex",
                        "action": "block",
                        "scope": "all",
                    },
                    {
                        "service_tier": "auto",
                        "action": "force_priority",
                        "scope": "all",
                    },
                ],
            },
        )

        self.assertEqual(("default", "priority"), tiers)

    def test_anthropic_mapping_does_not_block_on_unreachable_flex_tier(self):
        row = self.mod.evaluate_mapping(
            account={
                "account_id": 1,
                "group_platform": "anthropic",
                "group_rate_multiplier": Decimal("1"),
                "minimum_user_multiplier": Decimal("1"),
            },
            requested_model="claude-model",
            billing_model="claude-model",
            billing_model_source="requested",
            channel_mapped_model="claude-model",
            upstream_item={
                "model_name": "claude-model",
                "model_ratio": 0.5,
                "completion_ratio": 4,
                "enable_groups": ["default"],
            },
            pricing={"group_ratio": {"default": 1}},
            recharge_factor=Decimal("1"),
            local_prices=self.mod.BasePricingTiers(
                default=self.mod.TokenPrices(
                    input=Decimal("1"),
                    output=Decimal("4"),
                ),
            ),
        )

        self.assertEqual("OK", row["status"])
        self.assertEqual("default,priority", row["checked_service_tiers"])
        self.assertEqual(Decimal("1"), row["minimum_safe_user_multiplier"])
        self.assertNotIn("loss tiers", row["note"])

    def test_anthropic_fast_mode_reachable_priority_loss_blocks(self):
        row = self.mod.evaluate_mapping(
            account={
                "account_id": 1,
                "group_platform": "anthropic",
                "group_rate_multiplier": Decimal("1"),
                "minimum_user_multiplier": Decimal("1"),
            },
            requested_model="claude-model",
            billing_model="claude-model",
            billing_model_source="requested",
            channel_mapped_model="claude-model",
            upstream_item={
                "model_name": "claude-model",
                "model_ratio": 0.5,
                "completion_ratio": 4,
                "enable_groups": ["default"],
            },
            pricing={"group_ratio": {"default": 1}},
            recharge_factor=Decimal("1"),
            local_prices=self.mod.BasePricingTiers(
                default=self.mod.TokenPrices(
                    input=Decimal("2"),
                    output=Decimal("8"),
                ),
                priority_explicit=self.mod.TokenPrices(
                    input=Decimal("0.5"),
                    output=Decimal("2"),
                ),
            ),
        )

        self.assertEqual("REAL_LOSS", row["status"])
        self.assertEqual("default,priority", row["checked_service_tiers"])
        self.assertIn("loss tiers: priority", row["note"])

    def test_deepseek_tool_fee_without_group_price_blocks_even_when_tokens_are_safe(self):
        row = self.mod.evaluate_mapping(
            account={
                "account_id": 1,
                "account_name": "KBQ DeepSeek",
                "group_id": 13,
                "group_name": "deepseek",
                "group_rate_multiplier": Decimal("2"),
                "minimum_user_multiplier": Decimal("2"),
                "channel_status": "active",
                "group_web_search_price_per_call": None,
            },
            requested_model="deepseek-v4-flash",
            upstream_item={
                "model_name": "[特价]deepseek-v4-flash",
                "quota_type": 0,
                "model_ratio": 0.1,
                "completion_ratio": 2,
                "cache_ratio": 0.02,
                "enable_groups": ["default"],
            },
            pricing={"group_ratio": {"default": 1}},
            recharge_factor=Decimal("0.9"),
            local_prices=self.mod.BasePricingTiers(
                default=self.mod.TokenPrices(
                    input=Decimal("0.14"),
                    output=Decimal("0.28"),
                    cache_read=Decimal("0.0028"),
                ),
            ),
        )

        self.assertEqual("TOOL_FEE_UNCOVERED_LOSS", row["status"])
        self.assertEqual("TOOL_FEE_UNCOVERED_LOSS", row["tool_fee_status"])
        self.assertEqual(Decimal("0"), row["site_web_search_price_per_call"])

    def test_multiple_enabled_groups_require_explicit_account_metadata(self):
        ratio, key, source = self.mod.resolve_group_ratio(
            {"group_ratio": {"GPT-plus": 0.6, "GPT-pro": 1}},
            {"enable_groups": ["GPT-plus", "GPT-pro"]},
        )

        self.assertIsNone(ratio)
        self.assertEqual("", key)
        self.assertEqual("ambiguous", source)

    def test_duplicate_upstream_variants_with_different_prices_fail_closed(self):
        selected = self.mod.select_upstream_item(
            [
                {
                    "model_name": "same-model",
                    "quota_type": 0,
                    "model_ratio": 0.1,
                    "completion_ratio": 2,
                    "enable_groups": ["default"],
                },
                {
                    "model_name": "same-model",
                    "quota_type": 0,
                    "model_ratio": 0.2,
                    "completion_ratio": 2,
                    "enable_groups": ["default"],
                },
            ],
            {"group_ratio": {"default": 1}},
            "default",
        )

        self.assertIsNone(selected)

    def test_missing_upstream_price_preserves_mapped_model_name(self):
        row = self.mod.evaluate_mapping(
            account={"account_id": 1, "account_name": "KBQ missing"},
            requested_model="alias",
            mapped_upstream_model="removed-upstream-model",
            upstream_item=None,
            pricing={"group_ratio": {}},
            recharge_factor=Decimal("0.9"),
            local_prices=None,
        )

        self.assertEqual("NO_UPSTREAM_PRICE", row["status"])
        self.assertEqual("removed-upstream-model", row["upstream_model"])

    def test_missing_model_ratio_fails_closed(self):
        row = self.mod.evaluate_mapping(
            account={"account_id": 1, "account_name": "KBQ malformed"},
            requested_model="model",
            upstream_item={
                "model_name": "model",
                "completion_ratio": 2,
                "enable_groups": ["default"],
            },
            pricing={"group_ratio": {"default": 1}},
            recharge_factor=Decimal("0.9"),
            local_prices=self.mod.BasePricingTiers(
                default=self.mod.TokenPrices(input=Decimal("1"), output=Decimal("2")),
            ),
        )

        self.assertEqual("NO_UPSTREAM_PRICE", row["status"])

    def test_missing_paid_dimension_blocks_even_when_other_dimensions_are_safe(self):
        row = self.mod.evaluate_mapping(
            account={
                "account_id": 1,
                "account_name": "KBQ sparse local",
                "group_id": 2,
                "group_name": "safe",
                "group_rate_multiplier": Decimal("2"),
                "minimum_user_multiplier": Decimal("2"),
            },
            requested_model="model",
            upstream_item={
                "model_name": "model",
                "model_ratio": 0.5,
                "completion_ratio": 4,
                "cache_ratio": 0.1,
                "enable_groups": ["default"],
            },
            pricing={"group_ratio": {"default": 1}},
            recharge_factor=Decimal("0.9"),
            local_prices=self.mod.BasePricingTiers(
                default=self.mod.TokenPrices(
                    input=Decimal("1"), output=Decimal("4"), cache_read=Decimal("0")
                ),
            ),
        )

        self.assertEqual("NO_LOCAL_PRICE", row["status"])
        self.assertIn("cache_read", row["note"])

    def test_summary_splits_active_and_dormant_blocking_rows(self):
        active = {
            "status": "REAL_LOSS",
            "account_id": 1,
            "account_status": "active",
            "schedulable": True,
            "group_status": "active",
        }
        dormant = {
            "status": "NO_UPSTREAM_PRICE",
            "account_id": 2,
            "account_status": "active",
            "schedulable": False,
            "group_status": "active",
        }

        self.assertTrue(self.mod.mapping_is_currently_routable(active))
        self.assertFalse(self.mod.mapping_is_currently_routable(dormant))

    def test_fail_on_loss_exit_semantics_ignore_dormant_only_findings(self):
        self.assertFalse(
            self.mod.should_fail_on_loss(
                {"active_blocking_count": 0, "dormant_blocking_count": 5},
                True,
            )
        )
        self.assertTrue(
            self.mod.should_fail_on_loss(
                {"active_blocking_count": 1, "dormant_blocking_count": 0},
                True,
            )
        )
        self.assertFalse(
            self.mod.should_fail_on_loss({"active_blocking_count": 1}, False)
        )

    def test_main_returns_exit_two_for_active_blocking_configuration(self):
        args = argparse.Namespace(fail_on_loss=True, db=":memory:")
        summary = {
            "account_count": 1,
            "mapping_count": 1,
            "active_mapping_count": 1,
            "ok_count": 0,
            "blocking_count": 1,
            "active_blocking_count": 1,
            "dormant_blocking_count": 0,
            "real_loss_count": 1,
            "ambiguous_group_ratio_count": 0,
            "missing_upstream_price_count": 0,
            "missing_local_price_count": 0,
            "tool_fee_uncovered_count": 0,
        }
        self.mod.parse_args = lambda: args
        self.mod.audit = lambda _args: (summary, [])
        self.mod.write_ledger = lambda _db, _summary, _rows: 42

        with contextlib.redirect_stdout(io.StringIO()):
            exit_code = self.mod.main()

        self.assertEqual(2, exit_code)

    def test_write_ledger_persists_configuration_audit_without_secrets(self):
        summary = {
            "observed_at": "2026-08-13T00:00:00+00:00",
            "pricing_version": "v1",
            "account_count": 1,
            "mapping_count": 1,
            "active_mapping_count": 1,
            "ok_count": 0,
            "blocking_count": 1,
            "active_blocking_count": 1,
            "dormant_blocking_count": 0,
            "real_loss_count": 1,
            "ambiguous_group_ratio_count": 0,
            "missing_upstream_price_count": 0,
            "missing_local_price_count": 0,
            "missing_user_price_count": 0,
            "tool_fee_uncovered_count": 0,
            "tool_fee_unknown_count": 1,
            "source": "https://example.test/api/pricing",
            "note": "read-only",
        }
        row = self.mod.evaluate_mapping(
            account={
                "account_id": 7788,
                "account_name": "KBQ DeepSeek",
                "account_status": "active",
                "schedulable": True,
                "group_id": 13,
                "group_name": "deepseek",
                "group_status": "active",
                "group_rate_multiplier": Decimal("0.5"),
                "minimum_user_multiplier": Decimal("0.5"),
                "channel_id": 16,
                "channel_name": "deepseek",
                "channel_status": "inactive",
                "group_web_search_price_per_call": None,
            },
            requested_model="deepseek-v4-flash",
            upstream_item={
                "model_name": "[特价]deepseek-v4-flash",
                "quota_type": 0,
                "model_ratio": 0.1,
                "completion_ratio": 2,
                "cache_ratio": 0.02,
                "enable_groups": ["default"],
            },
            pricing={"group_ratio": {"default": 1}},
            recharge_factor=Decimal("0.9"),
            local_prices=self.mod.BasePricingTiers(
                default=self.mod.TokenPrices(
                    input=Decimal("0.14"), output=Decimal("0.28"), cache_read=Decimal("0.0028")
                ),
            ),
            local_pricing_source="fallback",
        )
        with tempfile.NamedTemporaryFile(suffix=".sqlite") as tmp:
            run_id = self.mod.write_ledger(tmp.name, summary, [row])
            conn = sqlite3.connect(tmp.name)
            stored = conn.execute(
                "select status, account_id, local_pricing_source, checked_service_tiers, upstream_web_search_price_per_call, site_web_search_price_per_call from kbq_configuration_audit_rows where run_id=?",
                (run_id,),
            ).fetchone()
            tables = {value for (value,) in conn.execute("select name from sqlite_master where type='table'")}
            conn.close()

        self.assertEqual(("REAL_LOSS", 7788, "fallback", "default", 0.009, 0.0), stored)
        self.assertIn("kbq_configuration_audit_runs", tables)
        self.assertIn("kbq_configuration_audit_rows", tables)

    def test_write_ledger_migrates_previous_configuration_schema(self):
        with tempfile.NamedTemporaryFile(suffix=".sqlite") as tmp:
            conn = sqlite3.connect(tmp.name)
            conn.executescript(self.mod.SCHEMA)
            conn.execute("alter table kbq_configuration_audit_runs rename to old_runs")
            conn.execute("alter table kbq_configuration_audit_rows rename to old_rows")
            conn.executescript(
                """
                create table kbq_configuration_audit_runs as
                  select id, observed_at, pricing_version, account_count, mapping_count,
                         ok_count, blocking_count, real_loss_count,
                         ambiguous_group_ratio_count, missing_upstream_price_count,
                         missing_local_price_count, missing_user_price_count,
                         tool_fee_uncovered_count, tool_fee_unknown_count, source, note
                  from old_runs where 0;
                create table kbq_configuration_audit_rows as
                  select id, run_id, status, account_id, account_name, account_status,
                         schedulable, group_id, group_name, group_status, requested_model,
                         upstream_model, pricing_status, kbq_group_key, kbq_group_ratio,
                         group_ratio_source, group_rate_multiplier, minimum_user_multiplier,
                         channel_id, channel_name, channel_status, local_pricing_source,
                         upstream_input_price, upstream_output_price,
                         upstream_cache_read_price, upstream_cache_write_price,
                         local_input_price, local_output_price, local_cache_read_price,
                         local_cache_write_price, site_input_price, site_output_price,
                         site_cache_read_price, site_cache_write_price,
                         minimum_break_even_multiplier, group_web_search_price_per_call,
                         tool_fee_status, note
                  from old_rows where 0;
                drop table old_rows;
                drop table old_runs;
                """
            )
            self.mod.ensure_ledger_schema(conn)
            run_columns = {row[1] for row in conn.execute("pragma table_info(kbq_configuration_audit_runs)")}
            row_columns = {row[1] for row in conn.execute("pragma table_info(kbq_configuration_audit_rows)")}
            conn.close()

        self.assertIn("active_mapping_count", run_columns)
        self.assertIn("active_blocking_count", run_columns)
        self.assertIn("dormant_blocking_count", run_columns)
        self.assertIn("upstream_web_search_price_per_call", row_columns)
        self.assertIn("site_web_search_price_per_call", row_columns)
        self.assertIn("minimum_safe_user_multiplier", row_columns)


if __name__ == "__main__":
    unittest.main()
