#!/usr/bin/env python3
from __future__ import annotations

import importlib.util
import sqlite3
import sys
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("refresh_public_pricing_adapters.py")


def load_module():
    spec = importlib.util.spec_from_file_location("refresh_public_pricing_adapters", SCRIPT)
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class RefreshPublicPricingAdaptersTest(unittest.TestCase):
    def setUp(self) -> None:
        self.mod = load_module()

    def connection(self) -> sqlite3.Connection:
        conn = sqlite3.connect(":memory:")
        conn.row_factory = sqlite3.Row
        conn.executescript(
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
              site_account_multiplier real,
              site_group_multiplier text not null default '',
              actual_cost_label text not null default '',
              status text not null,
              note text not null,
              updated_at text not null
            );
            """
        )
        return conn

    def test_public_pricing_updates_matching_text_ledger_row(self):
        conn = self.connection()
        provider = self.mod.Provider("钧澈", "vip.lcodex.cn", "https://vip.lcodex.cn/api/pricing", "public")
        conn.execute(
            """
            insert into upstream_rate_records (
              category, kind, site, fluter_account_name, upstream_group,
              page_rate, recharge_factor, site_account_multiplier, status, note, updated_at
            ) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                "钧澈",
                "Codex Pro",
                "vip.lcodex.cn",
                "钧澈 codex pro/plus 仅文字0.08*0.93=0.0728",
                "GPT-PLUS号池",
                0.045,
                100 / 108,
                0.075,
                "偏保守",
                "",
                "old",
            ),
        )

        changed = self.mod.update_matching_ledger_groups(
            conn,
            provider,
            {"GPT-PLUS号池": 0.08},
            "now",
        )

        self.assertEqual(1, changed)
        row = conn.execute("select * from upstream_rate_records").fetchone()
        self.assertAlmostEqual(0.08, row["page_rate"])
        self.assertIn("实际成本倍率 0.074074074x", row["actual_cost_label"])
        self.assertIn("公开价格接口同步", row["note"])
        self.assertEqual("已确认", row["status"])

    def test_public_pricing_does_not_update_image_or_special_rows(self):
        conn = self.connection()
        provider = self.mod.Provider("钧澈", "vip.lcodex.cn", "https://vip.lcodex.cn/api/pricing", "public")
        for kind in ("生图", "特殊"):
            conn.execute(
                """
                insert into upstream_rate_records (
                  category, kind, site, fluter_account_name, upstream_group,
                  page_rate, recharge_factor, site_account_multiplier, status, note, updated_at
                ) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                """,
                (
                    "钧澈",
                    kind,
                    "vip.lcodex.cn",
                    f"row {kind}",
                    "生图专用分组",
                    0.06,
                    100 / 108,
                    0.033,
                    "未调度",
                    "",
                    "old",
                ),
            )

        changed = self.mod.update_matching_ledger_groups(
            conn,
            provider,
            {"生图专用分组": 1},
            "now",
        )

        self.assertEqual(0, changed)
        self.assertEqual(
            [0.06, 0.06],
            [row["page_rate"] for row in conn.execute("select page_rate from upstream_rate_records order by id")],
        )

    def test_congmingai_is_registered_as_browser_required_provider(self):
        provider = next(provider for provider in self.mod.PROVIDERS if provider.site == "sub2.congmingai.com")

        self.assertEqual("聪明AI", provider.name)
        self.assertIsNone(provider.pricing_url)
        self.assertIn("browser/API-key adapter", provider.note)

    def test_qiaoran_is_registered_as_browser_required_provider(self):
        provider = next(provider for provider in self.mod.PROVIDERS if provider.site == "mdkj.lol")

        self.assertEqual("乔燃", provider.name)
        self.assertIsNone(provider.pricing_url)
        self.assertIn("browser/API-key adapter", provider.note)

    def test_browser_required_provider_is_not_covered_by_legacy_script(self):
        conn = self.connection()
        conn.executescript(
            """
            create table browser_adapter_status (
              provider text not null,
              site text not null,
              status text not null,
              detail text not null,
              observed_at text not null
            );
            create table upstream_adapter_status (
              provider text not null,
              site text not null,
              adapter_kind text not null,
              status text not null,
              detail text not null,
              observed_at text not null,
              unique(provider, site)
            );
            insert into browser_adapter_status values (
              '聪明AI', 'sub2.congmingai.com', 'browser_observed',
              'Chrome Tampermonkey read-only snapshot; account_lines=1; script=0.1.12',
              '2026-06-15T09:00:00+00:00'
            );
            """
        )
        provider = self.mod.Provider("聪明AI", "sub2.congmingai.com", None, "browser/API-key adapter needed")

        name, message = self.mod.refresh_provider(conn, provider, 30, "now", False)

        self.assertEqual("聪明AI", name)
        self.assertEqual("needs browser/API-key adapter", message)
        status = conn.execute("select status, detail from upstream_adapter_status").fetchone()
        self.assertEqual("needs_adapter", status["status"])
        self.assertNotIn("covered", status["detail"].lower())

    def test_browser_required_provider_is_covered_by_current_script(self):
        conn = self.connection()
        conn.executescript(
            """
            create table browser_adapter_status (
              provider text not null,
              site text not null,
              status text not null,
              detail text not null,
              observed_at text not null
            );
            create table upstream_adapter_status (
              provider text not null,
              site text not null,
              adapter_kind text not null,
              status text not null,
              detail text not null,
              observed_at text not null,
              unique(provider, site)
            );
            insert into browser_adapter_status values (
              '聪明AI', 'sub2.congmingai.com', 'browser_observed',
              'Chrome Tampermonkey read-only snapshot; account_lines=1; script=0.1.15; wait_state=stable',
              '2026-06-15T09:00:00+00:00'
            );
            """
        )
        provider = self.mod.Provider("聪明AI", "sub2.congmingai.com", None, "browser/API-key adapter needed")

        name, message = self.mod.refresh_provider(conn, provider, 30, "now", False)

        self.assertEqual("聪明AI", name)
        self.assertEqual("covered by browser adapter", message)
        status = conn.execute("select status, detail from upstream_adapter_status").fetchone()
        self.assertEqual("covered_by_browser", status["status"])
        self.assertIn("current coverage", status["detail"])

    def test_browser_required_provider_prefers_upstream_hub_coverage(self):
        conn = self.connection()
        conn.executescript(
            """
            create table upstream_hub_channels (
              channel_id integer primary key,
              channel_name text not null,
              channel_type text not null,
              site text not null,
              site_url text not null,
              monitor_enabled integer not null,
              last_balance real,
              last_balance_label text not null default '',
              last_balance_at text not null default '',
              last_error text not null default '',
              hub_updated_at text not null default '',
              imported_at text not null
            );
            create table upstream_hub_rate_observations (
              channel_id integer not null,
              channel_name text not null,
              site text not null,
              model_name text not null,
              description text not null default '',
              page_rate real not null,
              completion_ratio real,
              first_seen_at text not null,
              last_seen_at text not null,
              imported_at text not null,
              unique(channel_id, model_name)
            );
            create table upstream_adapter_status (
              provider text not null,
              site text not null,
              adapter_kind text not null,
              status text not null,
              detail text not null,
              observed_at text not null,
              unique(provider, site)
            );
            insert into upstream_hub_channels values (
              1, '聪明AI', 'newapi', 'sub2.congmingai.com',
              'https://sub2.congmingai.com', 1, 40.44, '$40.44',
              '2026-06-16 10:00:00+08', '', '2026-06-16 10:01:00+08',
              '2026-06-16T02:02:00+00:00'
            );
            insert into upstream_hub_rate_observations values (
              1, '聪明AI', 'sub2.congmingai.com', '中转站对接分组',
              '', 0.05, 1, '2026-06-16 09:00:00+08',
              '2026-06-16 10:00:00+08', '2026-06-16T02:02:00+00:00'
            );
            """
        )
        provider = self.mod.Provider("聪明AI", "sub2.congmingai.com", None, "browser/API-key adapter needed")

        name, message = self.mod.refresh_provider(conn, provider, 30, "now", False)

        self.assertEqual("聪明AI", name)
        self.assertEqual("covered by upstream-hub", message)
        status = conn.execute("select status, adapter_kind, detail from upstream_adapter_status").fetchone()
        self.assertEqual("covered_by_upstream_hub", status["status"])
        self.assertEqual("upstream_hub", status["adapter_kind"])
        self.assertIn("rates=1", status["detail"])

    def test_browser_required_provider_reports_hub_error_when_snapshot_failed(self):
        conn = self.connection()
        conn.executescript(
            """
            create table upstream_hub_channels (
              channel_id integer primary key,
              channel_name text not null,
              channel_type text not null,
              site text not null,
              site_url text not null,
              monitor_enabled integer not null,
              last_balance real,
              last_balance_label text not null default '',
              last_balance_at text not null default '',
              last_error text not null default '',
              hub_updated_at text not null default '',
              imported_at text not null
            );
            create table upstream_hub_rate_observations (
              channel_id integer not null,
              channel_name text not null,
              site text not null,
              model_name text not null,
              description text not null default '',
              page_rate real not null,
              completion_ratio real,
              first_seen_at text not null,
              last_seen_at text not null,
              imported_at text not null,
              unique(channel_id, model_name)
            );
            create table upstream_adapter_status (
              provider text not null,
              site text not null,
              adapter_kind text not null,
              status text not null,
              detail text not null,
              observed_at text not null,
              unique(provider, site)
            );
            insert into upstream_hub_channels values (
              1, '聪明AI', 'newapi', 'sub2.congmingai.com',
              'https://sub2.congmingai.com', 1, null, '',
              '', 'token invalid', '2026-06-16 10:01:00+08',
              '2026-06-16T02:02:00+00:00'
            );
            """
        )
        provider = self.mod.Provider("聪明AI", "sub2.congmingai.com", None, "browser/API-key adapter needed")

        name, message = self.mod.refresh_provider(conn, provider, 30, "now", False)

        self.assertEqual("聪明AI", name)
        self.assertEqual("upstream-hub error", message)
        status = conn.execute("select status, adapter_kind, detail from upstream_adapter_status").fetchone()
        self.assertEqual("hub_error", status["status"])
        self.assertEqual("upstream_hub", status["adapter_kind"])
        self.assertIn("rates=0", status["detail"])
        self.assertIn("last_error=token invalid", status["detail"])

    def test_browser_required_provider_reports_empty_hub_when_no_rates_were_captured(self):
        conn = self.connection()
        conn.executescript(
            """
            create table upstream_hub_channels (
              channel_id integer primary key,
              channel_name text not null,
              channel_type text not null,
              site text not null,
              site_url text not null,
              monitor_enabled integer not null,
              last_balance real,
              last_balance_label text not null default '',
              last_balance_at text not null default '',
              last_error text not null default '',
              hub_updated_at text not null default '',
              imported_at text not null
            );
            create table upstream_hub_rate_observations (
              channel_id integer not null,
              channel_name text not null,
              site text not null,
              model_name text not null,
              description text not null default '',
              page_rate real not null,
              completion_ratio real,
              first_seen_at text not null,
              last_seen_at text not null,
              imported_at text not null,
              unique(channel_id, model_name)
            );
            create table upstream_adapter_status (
              provider text not null,
              site text not null,
              adapter_kind text not null,
              status text not null,
              detail text not null,
              observed_at text not null,
              unique(provider, site)
            );
            insert into upstream_hub_channels values (
              1, '聪明AI', 'newapi', 'sub2.congmingai.com',
              'https://sub2.congmingai.com', 1, 40.44, '$40.44',
              '2026-06-16 10:00:00+08', '', '2026-06-16 10:01:00+08',
              '2026-06-16T02:02:00+00:00'
            );
            """
        )
        provider = self.mod.Provider("聪明AI", "sub2.congmingai.com", None, "browser/API-key adapter needed")

        name, message = self.mod.refresh_provider(conn, provider, 30, "now", False)

        self.assertEqual("聪明AI", name)
        self.assertEqual("upstream-hub observed but no rates", message)
        status = conn.execute("select status, adapter_kind, detail from upstream_adapter_status").fetchone()
        self.assertEqual("hub_observed_empty", status["status"])
        self.assertEqual("upstream_hub", status["adapter_kind"])
        self.assertIn("rates=0", status["detail"])

    def test_kingdom_provider_uses_tokenskingdom_hub_alias(self):
        conn = self.connection()
        conn.executescript(
            """
            create table upstream_hub_channels (
              channel_id integer primary key,
              channel_name text not null,
              channel_type text not null,
              site text not null,
              site_url text not null,
              monitor_enabled integer not null,
              last_balance real,
              last_balance_label text not null default '',
              last_balance_at text not null default '',
              last_error text not null default '',
              hub_updated_at text not null default '',
              imported_at text not null
            );
            create table upstream_hub_rate_observations (
              channel_id integer not null,
              channel_name text not null,
              site text not null,
              model_name text not null,
              description text not null default '',
              page_rate real not null,
              completion_ratio real,
              first_seen_at text not null,
              last_seen_at text not null,
              imported_at text not null,
              unique(channel_id, model_name)
            );
            create table upstream_adapter_status (
              provider text not null,
              site text not null,
              adapter_kind text not null,
              status text not null,
              detail text not null,
              observed_at text not null,
              unique(provider, site)
            );
            insert into upstream_hub_channels values (
              14, 'kingdom', 'newapi', 'tokenskingdom.com',
              'https://tokenskingdom.com', 1, 1807.81, '$1807.81',
              '2026-06-16 10:00:00+08', '', '2026-06-16 10:01:00+08',
              '2026-06-16T02:02:00+00:00'
            );
            insert into upstream_hub_rate_observations values (
              14, 'kingdom', 'tokenskingdom.com', 'Plus 2号池',
              '', 1.1, 1, '2026-06-16 09:00:00+08',
              '2026-06-16 10:00:00+08', '2026-06-16T02:02:00+00:00'
            );
            """
        )
        provider = next(provider for provider in self.mod.PROVIDERS if provider.name == "Kingdom")

        name, message = self.mod.refresh_provider(conn, provider, 30, "now", False)

        self.assertEqual("Kingdom", name)
        self.assertEqual("covered by upstream-hub", message)
        status = conn.execute("select status, adapter_kind, detail from upstream_adapter_status").fetchone()
        self.assertEqual("covered_by_upstream_hub", status["status"])
        self.assertEqual("upstream_hub", status["adapter_kind"])
        self.assertIn("rates=1", status["detail"])
        self.assertIn("matched_site=tokenskingdom.com", status["detail"])

    def test_public_pricing_provider_keeps_hub_as_primary_status_when_available(self):
        conn = self.connection()
        conn.executescript(
            """
            create table upstream_hub_channels (
              channel_id integer primary key,
              channel_name text not null,
              channel_type text not null,
              site text not null,
              site_url text not null,
              monitor_enabled integer not null,
              last_balance real,
              last_balance_label text not null default '',
              last_balance_at text not null default '',
              last_error text not null default '',
              hub_updated_at text not null default '',
              imported_at text not null
            );
            create table upstream_hub_rate_observations (
              channel_id integer not null,
              channel_name text not null,
              site text not null,
              model_name text not null,
              description text not null default '',
              page_rate real not null,
              completion_ratio real,
              first_seen_at text not null,
              last_seen_at text not null,
              imported_at text not null,
              unique(channel_id, model_name)
            );
            create table upstream_adapter_status (
              provider text not null,
              site text not null,
              adapter_kind text not null,
              status text not null,
              detail text not null,
              observed_at text not null,
              unique(provider, site)
            );
            create table provider_group_ratio_records (
              provider text not null,
              site text not null,
              group_name text not null,
              page_rate real not null,
              pricing_version text not null,
              source_url text not null,
              updated_at text not null,
              unique(provider, site, group_name)
            );
            create table provider_model_pricing_records (
              provider text not null,
              site text not null,
              model_name text not null,
              quota_type integer,
              model_ratio real,
              completion_ratio real,
              cache_ratio real,
              create_cache_ratio real,
              model_price real,
              supported_endpoints text not null,
              pricing_version text not null,
              source_url text not null,
              updated_at text not null,
              unique(provider, site, model_name)
            );
            insert into upstream_hub_channels values (
              12, '钧澈', 'newapi', 'vip.lcodex.cn',
              'https://vip.lcodex.cn', 1, 557.04, '$557.04',
              '2026-06-16 10:00:00+08', '', '2026-06-16 10:01:00+08',
              '2026-06-16T02:02:00+00:00'
            );
            insert into upstream_hub_rate_observations values (
              12, '钧澈', 'vip.lcodex.cn', '生图专用分组',
              '', 0.9, 1, '2026-06-16 09:00:00+08',
              '2026-06-16 10:00:00+08', '2026-06-16T02:02:00+00:00'
            );
            """
        )
        provider = self.mod.Provider("钧澈", "vip.lcodex.cn", "https://vip.lcodex.cn/api/pricing", "public")
        original_fetch_json = self.mod.fetch_json
        self.mod.fetch_json = lambda _url, _timeout: {
            "success": True,
            "pricing_version": "test",
            "group_ratio": {"GPT-PLUS号池": 0.08},
            "data": [{"model_name": "gpt-5.5"}],
        }
        try:
            name, message = self.mod.refresh_provider(conn, provider, 30, "now", False)
        finally:
            self.mod.fetch_json = original_fetch_json

        self.assertEqual("钧澈", name)
        self.assertIn("covered by upstream-hub", message)
        status = conn.execute("select status, adapter_kind, detail from upstream_adapter_status").fetchone()
        self.assertEqual("covered_by_upstream_hub", status["status"])
        self.assertEqual("upstream_hub", status["adapter_kind"])
        self.assertIn("rates=1", status["detail"])
        self.assertIn("public_pricing groups=1 models=1", status["detail"])

    def test_public_pricing_provider_does_not_hide_hub_error(self):
        conn = self.connection()
        conn.executescript(
            """
            create table upstream_hub_channels (
              channel_id integer primary key,
              channel_name text not null,
              channel_type text not null,
              site text not null,
              site_url text not null,
              monitor_enabled integer not null,
              last_balance real,
              last_balance_label text not null default '',
              last_balance_at text not null default '',
              last_error text not null default '',
              hub_updated_at text not null default '',
              imported_at text not null
            );
            create table upstream_hub_rate_observations (
              channel_id integer not null,
              channel_name text not null,
              site text not null,
              model_name text not null,
              description text not null default '',
              page_rate real not null,
              completion_ratio real,
              first_seen_at text not null,
              last_seen_at text not null,
              imported_at text not null,
              unique(channel_id, model_name)
            );
            create table upstream_adapter_status (
              provider text not null,
              site text not null,
              adapter_kind text not null,
              status text not null,
              detail text not null,
              observed_at text not null,
              unique(provider, site)
            );
            create table provider_group_ratio_records (
              provider text not null,
              site text not null,
              group_name text not null,
              page_rate real not null,
              pricing_version text not null,
              source_url text not null,
              updated_at text not null,
              unique(provider, site, group_name)
            );
            create table provider_model_pricing_records (
              provider text not null,
              site text not null,
              model_name text not null,
              quota_type integer,
              model_ratio real,
              completion_ratio real,
              cache_ratio real,
              create_cache_ratio real,
              model_price real,
              supported_endpoints text not null,
              pricing_version text not null,
              source_url text not null,
              updated_at text not null,
              unique(provider, site, model_name)
            );
            insert into upstream_hub_channels values (
              88, 'KBQ', 'newapi', 'xn--vduyey89e.com',
              'https://xn--vduyey89e.com', 1, null, '',
              '', 'token invalid', '2026-06-16 10:01:00+08',
              '2026-06-16T02:02:00+00:00'
            );
            """
        )
        provider = self.mod.Provider("KBQ", "xn--vduyey89e.com", "https://xn--vduyey89e.com/api/pricing", "public")
        original_fetch_json = self.mod.fetch_json
        self.mod.fetch_json = lambda _url, _timeout: {
            "success": True,
            "pricing_version": "test",
            "group_ratio": {"default": 1},
            "data": [{"model_name": "gpt-5.5"}],
        }
        try:
            name, message = self.mod.refresh_provider(conn, provider, 30, "now", False)
        finally:
            self.mod.fetch_json = original_fetch_json

        self.assertEqual("KBQ", name)
        self.assertIn("upstream-hub error", message)
        self.assertIn("public_pricing groups=1 models=1", message)
        status = conn.execute("select status, adapter_kind, detail from upstream_adapter_status").fetchone()
        self.assertEqual("hub_error", status["status"])
        self.assertEqual("upstream_hub", status["adapter_kind"])
        self.assertIn("last_error=token invalid", status["detail"])
        self.assertIn("public_pricing groups=1 models=1", status["detail"])


if __name__ == "__main__":
    unittest.main()
