#!/usr/bin/env python3
from __future__ import annotations

import importlib.util
import json
import re
import sqlite3
import sys
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("render_upstream_dashboard.py")


def load_module(path: Path, name: str):
    spec = importlib.util.spec_from_file_location(name, path)
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


def extract_js_const_json(html: str, name: str):
    match = re.search(rf"^\s*const {re.escape(name)} = (.*);$", html, re.MULTILINE)
    assert match is not None
    return json.loads(match.group(1))


class RenderUpstreamDashboardTest(unittest.TestCase):
    def setUp(self) -> None:
        self.render_mod = load_module(SCRIPT, "render_upstream_dashboard_under_test")
        self.render_mod.PROVIDER_SNAPSHOT_MAX_AGE_SECONDS = 10**12

    def create_minimal_db(self) -> str:
        tmp = tempfile.NamedTemporaryFile(suffix=".sqlite", delete=False)
        tmp.close()
        conn = sqlite3.connect(tmp.name)
        conn.executescript(
            """
            create table metadata (key text primary key, value text not null);
            create table upstream_discount_profiles (
              id integer primary key autoincrement,
              site text not null unique,
              provider_name text not null,
              discount_name text not null,
              recharge_factor real not null,
              paid_amount real,
              credited_amount real,
              currency text not null,
              effective_from text not null default '',
              effective_to text not null default '',
              status text not null,
              confidence text not null,
              note text not null,
              updated_at text not null
            );
            create table upstream_rate_records (
              id integer primary key,
              category text not null,
              kind text not null,
              site text not null,
              fluter_account_name text not null,
              upstream_group text not null,
              page_rate real,
              recharge_ratio_label text not null,
              recharge_factor real,
              site_account_multiplier real,
              site_group_multiplier text not null,
              actual_cost_label text not null default '',
              balance_label text not null,
              balance_updated_at text not null,
              status text not null,
              note text not null,
              updated_at text not null
            );
            create table browser_adapter_snapshots (
              provider text not null,
              site text not null,
              browser text not null,
              page_url text not null,
              page_title text not null,
              detected_balance text not null,
              detected_accounts_json text not null default '[]',
              detected_rates_json text not null,
              sanitized_excerpt text not null,
              observed_at text not null,
              unique(provider, site)
            );
            create table browser_adapter_status (
              provider text not null,
              site text not null,
              browser text not null,
              status text not null,
              detail text not null,
              observed_at text not null,
              unique(provider, site)
            );
            create table browser_adapter_account_observations (
              provider text not null,
              site text not null,
              account_name text not null,
              normalized_account_name text not null,
              upstream_group text not null,
              page_rate real,
              source_line text not null,
              matched_ledger_rows integer not null,
              observed_at text not null,
              unique(provider, site, normalized_account_name)
            );
            create table browser_adapter_rate_observations (
              provider text not null,
              site text not null,
              upstream_group text not null,
              page_rate real not null,
              source_line text not null,
              matched_ledger_rows integer not null,
              observed_at text not null,
              unique(provider, site, upstream_group)
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
            """
        )
        conn.execute(
            """
            insert into upstream_rate_records (
              category, kind, site, fluter_account_name, upstream_group,
              page_rate, recharge_ratio_label, recharge_factor,
              site_account_multiplier, site_group_multiplier, balance_label,
              balance_updated_at, status, note, updated_at
            ) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                "Kingdom",
                "Codex",
                "api.tokenskingdom.com",
                "stale kingdom old account",
                "过期旧组别",
                0.75,
                "148.88=2000刀",
                0.07444,
                0.05832,
                "codex 旧售价@0.1",
                "$999",
                "2026-06-01T00:00:00+00:00",
                "上游分组已消失/待重映射",
                "old seed row",
                "2026-06-01T00:00:00+00:00",
            ),
        )
        conn.execute(
            """
            insert into upstream_discount_profiles (
              site, provider_name, discount_name, recharge_factor, paid_amount,
              credited_amount, currency, effective_from, effective_to, status,
              confidence, note, updated_at
            ) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                "api.tokenskingdom.com",
                "Kingdom",
                "148.88 RMB = 2000 USD",
                0.07444,
                148.88, 2000,
                "CNY/USD",
                "2026-06-08",
                "",
                "已确认",
                "manual",
                "Kingdom 折扣档案",
                "2026-06-12T00:00:00+00:00",
            ),
        )
        conn.execute(
            """
            insert into browser_adapter_snapshots values (
              'Kingdom', 'api.tokenskingdom.com', 'chrome',
              'https://tokenskingdom.com/keys', 'API 密钥',
              '余额 $397.04',
              '[{"account_name":"kingdom current account 1.2","upstream_group":"Plus 2号池","page_rate":1.2,"source_line":"kingdom current account 1.2 sk-abc...def Plus 2号池 1.2x"}]',
              '[]', 'sanitized',
              '2026-06-12T19:59:00+00:00'
            )
            """
        )
        conn.execute(
            """
            insert into browser_adapter_account_observations values (
              'Kingdom', 'api.tokenskingdom.com',
              'kingdom current account 1.2',
              'kingdomcurrentaccount12',
              'Plus 2号池', 1.2,
              'kingdom current account 1.2 sk-abc...def Plus 2号池 1.2x',
              1,
              '2026-06-12T19:59:00+00:00'
            )
            """
        )
        conn.execute(
            """
            insert into browser_adapter_status values (
              'Kingdom', 'api.tokenskingdom.com', 'chrome',
              'browser_observed', 'Chrome Tampermonkey read-only snapshot; account_lines=1; script=0.1.15',
              '2026-06-12T19:59:00+00:00'
            )
            """
        )
        conn.execute(
            """
            insert into provider_group_ratio_records values (
              '钧澈', 'vip.lcodex.cn', 'GPT-PLUS号池', 0.08,
              'test-version', 'https://vip.lcodex.cn/api/pricing',
              '2026-06-14T18:46:24+00:00'
            )
            """
        )
        conn.commit()
        conn.close()
        return tmp.name

    def test_server_dashboard_has_risk_groups_and_safe_external_links(self):
        html = self.render_mod.render(
            rows=[],
            kbq_rows=[],
            kbq_per_call_rows=[],
            audit_summary={},
            audit_buckets=[],
            adapter_status=[],
            metadata={},
            priority_plan=[],
        )

        for section_id in (
            "serverServices",
            "serverMetricCards",
            "serverContainers",
            "serverFreshness",
            "serverBackups",
        ):
            self.assertIn(f'id="{section_id}"', html)
        self.assertIn('href="/admin/s2a-manager"', html)
        self.assertIn('href="https://codexradar.com/"', html)
        self.assertGreaterEqual(html.count('target="_blank" rel="noopener"'), 2)
        self.assertIn("外部参考不参与基础设施评分", html)
        self.assertIn("ageSeconds > 24 * 3600", html)
        self.assertIn("ageSeconds > 2 * 3600", html)
        self.assertIn("worstInfrastructureTone(item.tone, infrastructureFreshnessTone", html)
        self.assertIn('item.health === "risk"', html)

    def test_dashboard_navigation_only_exposes_retained_read_only_modules(self):
        html = self.render_mod.render(
            rows=[],
            kbq_rows=[],
            kbq_per_call_rows=[],
            audit_summary={},
            audit_buckets=[],
            adapter_status=[],
            metadata={},
            priority_plan=[],
        )

        nav_routes = re.findall(
            r'class="nav-link" href="#([^"]+)" data-route="([^"]+)"',
            html,
        )
        self.assertEqual(
            [
                ("overview", "overview"),
                ("server", "server"),
                ("kbq", "kbq"),
                ("risk", "risk"),
                ("log", "log"),
            ],
            nav_routes,
        )
        for removed_route in (
            "assistant",
            "automation",
            "providers",
            "balance",
            "accounts",
            "image",
        ):
            self.assertNotRegex(
                html,
                rf'<(?:a|section)[^>]+data-route="{removed_route}"',
            )
        self.assertNotIn('/admin/upstream-rates/ai', html)
        for removed_artifact in (
            "const DATA =",
            "const PROVIDER_OBSERVATIONS =",
            "const PROVIDER_DIAGNOSTICS =",
            "const BALANCE_SNAPSHOTS =",
            "const ADAPTER_STATUS =",
            "const PRIORITY_PLAN =",
            "function renderProviderObservations",
            "function renderBalanceStrip",
            "function renderImageCosts",
            "function renderPriorityPlan",
            "优先级预览",
            "余额雷达",
            "生图成本",
        ):
            with self.subTest(removed_artifact=removed_artifact):
                self.assertNotIn(removed_artifact, html)

    def test_format_beijing_time_accepts_postgres_short_utc_suffix(self):
        self.assertEqual(
            "2026-06-16 14:45:57 北京时间",
            self.render_mod.format_beijing_time("2026-06-16 06:45:57.663875+00"),
        )
        self.assertEqual(
            "2026-06-16 14:45:57 北京时间",
            self.render_mod.format_beijing_time("2026-06-16 06:45:57.663875+0000"),
        )

    def test_provider_observations_do_not_include_historical_ledger_rows(self):
        db_path = self.create_minimal_db()
        try:
            (
                rows,
                _kbq_rows,
                _kbq_per_call_rows,
                _audit_summary,
                _audit_buckets,
                _adapter_status,
                _metadata,
                _priority_plan,
                provider_observations,
                provider_diagnostics,
                _balance_snapshots,
            ) = self.render_mod.load_rows(db_path)
            self.assertEqual(["stale kingdom old account"], [row["fluterAccountName"] for row in rows])

            observed_names = [row["accountName"] for row in provider_observations]
            observed_groups = [row["upstreamGroup"] for row in provider_observations]
            self.assertIn("kingdom current account 1.2", observed_names)
            self.assertNotIn("GPT-PLUS号池", observed_groups)
            self.assertNotIn("stale kingdom old account", observed_names)
            self.assertNotIn("过期旧组别", observed_groups)

            diagnostic_pairs = {(row["provider"], row["site"]) for row in provider_diagnostics}
            self.assertIn(("Kingdom", "api.tokenskingdom.com"), diagnostic_pairs)

            html = self.render_mod.render(
                rows,
                [],
                [],
                None,
                [],
                [],
                {},
                [],
                provider_observations,
                provider_diagnostics,
            )
            self.assertNotIn("const PROVIDER_OBSERVATIONS =", html)
            self.assertNotIn("const PROVIDER_DIAGNOSTICS =", html)
            self.assertNotIn("kingdom current account 1.2", html)
            self.assertNotIn("stale kingdom old account", html)
            self.assertNotIn("[redacted-key]", html)
        finally:
            Path(db_path).unlink(missing_ok=True)

    def test_load_rows_uses_site_discount_profile_over_stale_row_factor(self):
        db_path = self.create_minimal_db()
        try:
            rows, *_rest = self.render_mod.load_rows(db_path)
            row = rows[0]

            self.assertEqual("discount_profile", row["discountSource"])
            self.assertEqual("已确认", row["discountStatus"])
            self.assertAlmostEqual(0.07444, row["rechargeFactor"])
            self.assertAlmostEqual(0.75 * 0.07444, row["actualCostMultiplier"])
            self.assertIn("充值系数 0.07444", row["actualCostLabel"])
        finally:
            Path(db_path).unlink(missing_ok=True)

    def test_load_rows_preserves_image_per_shot_cost_label(self):
        db_path = self.create_minimal_db()
        conn = sqlite3.connect(db_path)
        try:
            conn.execute(
                """
                insert into upstream_rate_records (
                  category, kind, site, fluter_account_name, upstream_group,
                  page_rate, recharge_ratio_label, recharge_factor,
                  site_account_multiplier, site_group_multiplier, actual_cost_label,
                  balance_label, balance_updated_at, status, note, updated_at
                ) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                """,
                (
                    "Kingdom",
                    "生图",
                    "image.tokenskingdom.com",
                    "kingdom image",
                    "Image",
                    0.65,
                    "148.88=2000刀",
                    0.07444,
                    1,
                    "生图 ¥0.10/张",
                    "$0.65/张 = ¥0.048386/张",
                    "$999",
                    "2026-06-01T00:00:00+00:00",
                    "已按流水确认",
                    "image cost",
                    "2026-06-01T00:00:00+00:00",
                ),
            )
            conn.commit()
            rows, *_rest = self.render_mod.load_rows(db_path)
            image = next(row for row in rows if row["kind"] == "生图")

            self.assertEqual("$0.65/张 = ¥0.048386/张", image["actualCostLabel"])
            self.assertAlmostEqual(0.65 * 0.07444, image["actualCostMultiplier"])
        finally:
            conn.close()
            Path(db_path).unlink(missing_ok=True)

    def test_balance_snapshots_include_hub_balance_without_curated_ledger_row(self):
        conn = sqlite3.connect(":memory:")
        conn.row_factory = sqlite3.Row
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
            """
        )
        conn.execute(
            "insert into upstream_hub_channels values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
            (
                41,
                "乔燃",
                "newapi",
                "mdkj.lol",
                "https://mdkj.lol",
                1,
                149.95122272,
                "$149.95122272",
                "2026-06-16 06:45:57.663875+00",
                "",
                "2026-06-16T06:46:00+00:00",
                "2026-06-16T07:29:09+00:00",
            ),
        )

        balances = self.render_mod.load_browser_balance_snapshots(conn)
        snapshots = self.render_mod.balance_snapshot_rows(balances)
        html = self.render_mod.render(
            rows=[],
            kbq_rows=[],
            kbq_per_call_rows=[],
            audit_summary={},
            audit_buckets=[],
            adapter_status=[],
            metadata={},
            priority_plan=[],
            provider_observations=[],
            provider_diagnostics=[],
            balance_snapshots=snapshots,
        )
        self.assertEqual(1, len(snapshots))
        self.assertNotIn("const BALANCE_SNAPSHOTS =", html)
        self.assertNotIn("乔燃", html)
        self.assertNotIn("$149.95122272", html)

    def test_render_only_exposes_retained_kbq_metadata(self):
        html = self.render_mod.render(
            rows=[],
            kbq_rows=[],
            kbq_per_call_rows=[],
            audit_summary={},
            audit_buckets=[],
            adapter_status=[],
            metadata={
                "priority_plan_manual_write_command": (
                    'ssh us-api-vps "sudo python3 '
                    '/var/lib/fluterapi-upstream-rates/plan_account_priority_buckets.py --write-notes"'
                ),
                "safe_note": "ok",
                "last_upstream_hub_imported_at": "2026-06-16T06:46:00+00:00",
                "kbq_pricing_version": "2026-06-16",
            },
            priority_plan=[],
            provider_observations=[],
            provider_diagnostics=[],
        )
        metadata_payload = extract_js_const_json(html, "META")
        self.assertNotIn("priority_plan_manual_write_command", metadata_payload)
        self.assertNotIn("safe_note", metadata_payload)
        self.assertNotIn("last_upstream_hub_imported_at", metadata_payload)
        self.assertEqual("2026-06-16", metadata_payload["kbq_pricing_version"])
        self.assertNotIn("--write-notes", html)

    def test_sensitive_text_redaction_covers_key_like_shapes(self):
        raw = (
            "sk-abc...def sk-123********xyz sk-1234567890abcdef "
            "Bearer abc.def access_token=secret cookie=session password=pw"
        )
        redacted = self.render_mod.redact_sensitive_text(raw)
        self.assertNotIn("sk-", redacted)
        self.assertNotIn("abc.def", redacted)
        self.assertNotIn("secret", redacted)
        self.assertNotIn("session", redacted)
        self.assertNotIn("password=pw", redacted)
        self.assertIn("[redacted-key]", redacted)

    def test_provider_observations_include_upstream_hub_current_rates_and_balance(self):
        conn = sqlite3.connect(":memory:")
        conn.row_factory = sqlite3.Row
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
            """
        )
        conn.execute(
            "insert into upstream_hub_channels values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
            (
                1,
                "聪明AI",
                "newapi",
                "sub2.congmingai.com",
                "https://sub2.congmingai.com",
                1,
                40.44,
                "$40.44",
                "2026-06-16T02:00:00+00:00",
                "",
                "2026-06-16T02:01:00+00:00",
                "2026-06-16T02:02:00+00:00",
            ),
        )
        conn.execute(
            "insert into upstream_hub_rate_observations values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
            (
                1,
                "聪明AI",
                "sub2.congmingai.com",
                "中转站对接分组",
                "",
                0.05,
                1,
                "2026-06-16T01:00:00+00:00",
                "2026-06-16T02:00:00+00:00",
                "2026-06-16T02:02:00+00:00",
            ),
        )

        balances = self.render_mod.load_browser_balance_snapshots(conn)
        rows = self.render_mod.load_provider_observations(conn, balances)

        self.assertEqual("$40.44", balances["sub2.congmingai.com"]["balance"])
        self.assertEqual(1, len(rows))
        row = rows[0]
        self.assertEqual("upstream_hub_rate_snapshot", row["sourceKind"])
        self.assertEqual("upstream-hub 当前倍率快照", row["sourceLabel"])
        self.assertEqual("聪明AI", row["provider"])
        self.assertEqual("聪明AI", row["accountName"])
        self.assertEqual("中转站对接分组", row["upstreamGroup"])
        self.assertEqual(0.05, row["pageRate"])
        self.assertEqual("$40.44", row["balanceLabel"])
        self.assertIn("completion_ratio=1.0", row["sourceLine"])

    def test_provider_observations_prefer_hub_and_skip_browser_rows_for_same_site(self):
        conn = sqlite3.connect(":memory:")
        conn.row_factory = sqlite3.Row
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
            create table browser_adapter_status (
              provider text not null,
              site text not null,
              browser text not null,
              status text not null,
              detail text not null,
              observed_at text not null,
              unique(provider, site)
            );
            create table browser_adapter_snapshots (
              provider text not null,
              site text not null,
              browser text not null,
              page_url text not null,
              page_title text not null,
              detected_balance text not null,
              detected_accounts_json text not null default '[]',
              detected_rates_json text not null,
              sanitized_excerpt text not null,
              observed_at text not null,
              unique(provider, site)
            );
            """
        )
        conn.execute(
            "insert into upstream_hub_channels values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
            (
                1,
                "钧澈",
                "newapi",
                "vip.lcodex.cn",
                "https://vip.lcodex.cn",
                1,
                684.62,
                "¥684.62",
                "2026-06-16T02:00:00+00:00",
                "",
                "2026-06-16T02:01:00+00:00",
                "2026-06-16T02:02:00+00:00",
            ),
        )
        for idx, name in enumerate(
            [
                "Claude-MAX号池",
                "Claude-MAX外接版",
                "GPT-PLUS号池",
                "GPT-PRO纯享号池",
                "pro破限",
                "team狂欢",
                "专享福利",
                "对接倍率",
                "生图专用分组",
            ],
            start=1,
        ):
            conn.execute(
                "insert into upstream_hub_rate_observations values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
                (
                    1,
                    "钧澈",
                    "vip.lcodex.cn",
                    name,
                    "",
                    idx / 100,
                    None,
                    "2026-06-16T01:00:00+00:00",
                    "2026-06-16T02:00:00+00:00",
                    "2026-06-16T02:02:00+00:00",
                ),
            )
        conn.execute(
            "insert into browser_adapter_status values (?, ?, ?, ?, ?, ?)",
            (
                "钧澈",
                "vip.lcodex.cn",
                "chrome",
                "browser_observed",
                "Chrome Tampermonkey read-only snapshot; account_lines=1; script=0.1.15",
                "2026-06-16T02:02:00+00:00",
            ),
        )
        conn.execute(
            "insert into browser_adapter_snapshots values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
            (
                "钧澈",
                "vip.lcodex.cn",
                "chrome",
                "https://vip.lcodex.cn/token",
                "令牌管理",
                "当前余额 ¥684.62",
                json.dumps(
                    [
                        {
                            "account_name": "旧浏览器账号不该进主表",
                            "upstream_group": "旧组",
                            "page_rate": 0.99,
                            "source_line": "旧浏览器账号不该进主表 sk-abc...def 旧组 0.99x",
                        }
                    ],
                    ensure_ascii=False,
                ),
                "[]",
                "",
                "2026-06-16T02:02:00+00:00",
            ),
        )

        balances = self.render_mod.load_browser_balance_snapshots(conn)
        rows = self.render_mod.load_provider_observations(conn, balances)

        self.assertEqual(9, len(rows))
        self.assertTrue(all(row["sourceKind"] == "upstream_hub_rate_snapshot" for row in rows))
        self.assertNotIn("旧浏览器账号不该进主表", [row["accountName"] for row in rows])
        self.assertEqual(
            [
                "Claude-MAX号池",
                "Claude-MAX外接版",
                "GPT-PLUS号池",
                "GPT-PRO纯享号池",
                "pro破限",
                "team狂欢",
                "专享福利",
                "对接倍率",
                "生图专用分组",
            ],
            [row["upstreamGroup"] for row in rows],
        )

    def test_kingdom_hub_balance_is_available_through_site_aliases(self):
        conn = sqlite3.connect(":memory:")
        conn.row_factory = sqlite3.Row
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
            """
        )
        conn.execute(
            "insert into upstream_hub_channels values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
            (
                14,
                "kingdom",
                "newapi",
                "tokenskingdom.com",
                "https://tokenskingdom.com",
                1,
                1807.81,
                "$1807.81",
                "2026-06-16T02:00:00+00:00",
                "",
                "2026-06-16T02:01:00+00:00",
                "2026-06-16T02:02:00+00:00",
            ),
        )

        balances = self.render_mod.load_browser_balance_snapshots(conn)

        self.assertEqual("$1807.81", balances["tokenskingdom.com"]["balance"])
        self.assertEqual("$1807.81", balances["api.tokenskingdom.com"]["balance"])
        self.assertEqual("$1807.81", balances["image.tokenskingdom.com"]["balance"])

    def test_adapter_status_sort_prefers_upstream_hub_over_public_summary(self):
        hub = {
            "provider": "钧澈",
            "site": "vip.lcodex.cn",
            "adapterKind": "upstream_hub",
            "status": "covered_by_upstream_hub",
        }
        public = {
            "provider": "钧澈",
            "site": "vip.lcodex.cn",
            "adapterKind": "public_pricing",
            "status": "ok",
        }
        browser = {
            "provider": "钧澈",
            "site": "vip.lcodex.cn",
            "adapterKind": "chrome browser_readonly",
            "status": "browser_observed",
        }

        ordered = sorted([public, browser, hub], key=self.render_mod.adapter_status_rank)

        self.assertEqual([hub, public, browser], ordered)

    def test_adapter_status_sort_does_not_treat_hub_error_as_coverage(self):
        hub_error = {
            "provider": "聪明AI",
            "site": "sub2.congmingai.com",
            "adapterKind": "upstream_hub",
            "status": "hub_error",
        }
        public = {
            "provider": "聪明AI",
            "site": "sub2.congmingai.com",
            "adapterKind": "public_pricing",
            "status": "ok",
        }
        browser = {
            "provider": "聪明AI",
            "site": "sub2.congmingai.com",
            "adapterKind": "chrome browser_readonly",
            "status": "browser_observed",
        }

        ordered = sorted([browser, hub_error, public], key=self.render_mod.adapter_status_rank)

        self.assertEqual([public, hub_error, browser], ordered)

    def test_provider_diagnostics_show_hidden_preserved_rows_without_displaying_them(self):
        conn = sqlite3.connect(":memory:")
        conn.row_factory = sqlite3.Row
        conn.executescript(
            """
            create table browser_adapter_snapshots (
              provider text not null,
              site text not null,
              browser text not null,
              page_url text not null,
              page_title text not null,
              detected_balance text not null,
              detected_accounts_json text not null default '[]',
              detected_rates_json text not null,
              sanitized_excerpt text not null,
              observed_at text not null,
              unique(provider, site)
            );
            create table browser_adapter_status (
              provider text not null,
              site text not null,
              browser text not null,
              status text not null,
              detail text not null,
              observed_at text not null,
              unique(provider, site)
            );
            """
        )
        conn.execute(
            "insert into browser_adapter_status values (?, ?, ?, ?, ?, ?)",
            (
                "钧澈",
                "vip.lcodex.cn",
                "chrome",
                "browser_observed",
                "Chrome Tampermonkey read-only snapshot; balance=yes; account_lines=0; preserved previous account lines from 2026-06-15T03:29:15+00:00",
                "2026-06-12T19:59:00+00:00",
            ),
        )
        conn.execute(
            "insert into browser_adapter_snapshots values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
            (
                "钧澈",
                "vip.lcodex.cn",
                "chrome",
                "https://vip.lcodex.cn/console",
                "控制台",
                "当前余额 ¥684.62",
                json.dumps(
                    [
                        {
                            "account_name": "钧澈 codex 对接倍率仅文字0.09*0.93=0.0819",
                            "upstream_group": "对接倍率",
                            "page_rate": 0.09,
                            "source_line": "钧澈 codex 对接倍率仅文字0.09*0.93=0.0819 sk-abc...def 对接倍率 0.09x",
                        }
                    ],
                    ensure_ascii=False,
                ),
                "[]",
                "",
                "2026-06-12T19:59:00+00:00",
            ),
        )

        rows = self.render_mod.load_provider_observations(conn, {})
        diagnostics = self.render_mod.load_provider_diagnostics(conn)
        self.assertEqual([], rows)
        self.assertEqual(1, len(diagnostics))
        self.assertEqual("hidden_preserved", diagnostics[0]["sourceState"])
        self.assertEqual(1, diagnostics[0]["rawAccountCount"])
        self.assertEqual(1, diagnostics[0]["cleanAccountCount"])
        self.assertEqual(0, diagnostics[0]["displayedAccountCount"])

    def test_provider_hides_legacy_script_rows_without_displaying_them(self):
        conn = sqlite3.connect(":memory:")
        conn.row_factory = sqlite3.Row
        conn.executescript(
            """
            create table browser_adapter_snapshots (
              provider text not null,
              site text not null,
              browser text not null,
              page_url text not null,
              page_title text not null,
              detected_balance text not null,
              detected_accounts_json text not null default '[]',
              detected_rates_json text not null,
              sanitized_excerpt text not null,
              observed_at text not null,
              unique(provider, site)
            );
            create table browser_adapter_status (
              provider text not null,
              site text not null,
              browser text not null,
              status text not null,
              detail text not null,
              observed_at text not null,
              unique(provider, site)
            );
            """
        )
        conn.execute(
            "insert into browser_adapter_status values (?, ?, ?, ?, ?, ?)",
            (
                "Kingdom",
                "api.tokenskingdom.com",
                "chrome",
                "browser_observed",
                "Chrome Tampermonkey read-only snapshot; account_lines=7; rate_lines=1; script=0.1.13",
                "2026-06-12T19:59:00+00:00",
            ),
        )
        conn.execute(
            "insert into browser_adapter_snapshots values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
            (
                "Kingdom",
                "api.tokenskingdom.com",
                "chrome",
                "https://tokenskingdom.com/keys",
                "API 密钥",
                "余额 $727.17",
                json.dumps(
                    [
                        {
                            "account_name": "kingdom codex plus2号 仅文字1.2*0.078=0.0936",
                            "upstream_group": "Plus 2号池",
                            "page_rate": 1.2,
                            "source_line": "kingdom codex plus2号 仅文字1.2*0.078=0.0936 sk-abc...def Plus 2号池 1.2x",
                        }
                    ],
                    ensure_ascii=False,
                ),
                "[]",
                "",
                "2026-06-12T19:59:00+00:00",
            ),
        )

        rows = self.render_mod.load_provider_observations(conn, {})
        diagnostics = self.render_mod.load_provider_diagnostics(conn)
        self.assertEqual([], rows)
        self.assertEqual("legacy_script", diagnostics[0]["sourceState"])
        self.assertEqual(1, diagnostics[0]["rawAccountCount"])
        self.assertEqual(0, diagnostics[0]["displayedAccountCount"])

    def test_provider_diagnostics_show_filtered_noise_rows(self):
        conn = sqlite3.connect(":memory:")
        conn.row_factory = sqlite3.Row
        conn.executescript(
            """
            create table browser_adapter_snapshots (
              provider text not null,
              site text not null,
              browser text not null,
              page_url text not null,
              page_title text not null,
              detected_balance text not null,
              detected_accounts_json text not null default '[]',
              detected_rates_json text not null,
              sanitized_excerpt text not null,
              observed_at text not null,
              unique(provider, site)
            );
            create table browser_adapter_status (
              provider text not null,
              site text not null,
              browser text not null,
              status text not null,
              detail text not null,
              observed_at text not null,
              unique(provider, site)
            );
            """
        )
        conn.execute(
            "insert into browser_adapter_status values (?, ?, ?, ?, ?, ?)",
            (
                "钧澈",
                "vip.lcodex.cn",
                "chrome",
                "browser_observed",
                "Chrome Tampermonkey read-only snapshot; account_lines=1; script=0.1.15",
                "2026-06-12T19:59:00+00:00",
            ),
        )
        conn.execute(
            "insert into browser_adapter_snapshots values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
            (
                "钧澈",
                "vip.lcodex.cn",
                "chrome",
                "https://vip.lcodex.cn/token",
                "令牌管理",
                "当前余额 ¥684.62",
                json.dumps(
                    [
                        {
                            "account_name": "非自用",
                            "upstream_group": "¥585.03 GPT-PRO纯享号池",
                            "page_rate": 0.15,
                            "source_line": "非自用 已启用 ¥196.63 / ¥585.03 GPT-PRO纯享号池 0.15x 无限制",
                        }
                    ],
                    ensure_ascii=False,
                ),
                "[]",
                "",
                "2026-06-12T19:59:00+00:00",
            ),
        )

        rows = self.render_mod.load_provider_observations(conn, {})
        diagnostics = self.render_mod.load_provider_diagnostics(conn)
        self.assertEqual([], rows)
        self.assertEqual("filtered", diagnostics[0]["sourceState"])
        self.assertEqual(1, diagnostics[0]["rawAccountCount"])
        self.assertEqual(0, diagnostics[0]["cleanAccountCount"])
        self.assertEqual(0, diagnostics[0]["displayedAccountCount"])

    def test_provider_hides_extraction_loss_when_page_reports_more_rows_than_json(self):
        conn = sqlite3.connect(":memory:")
        conn.row_factory = sqlite3.Row
        conn.executescript(
            """
            create table browser_adapter_snapshots (
              provider text not null,
              site text not null,
              browser text not null,
              page_url text not null,
              page_title text not null,
              detected_balance text not null,
              detected_accounts_json text not null default '[]',
              detected_rates_json text not null,
              sanitized_excerpt text not null,
              observed_at text not null,
              unique(provider, site)
            );
            create table browser_adapter_status (
              provider text not null,
              site text not null,
              browser text not null,
              status text not null,
              detail text not null,
              observed_at text not null,
              unique(provider, site)
            );
            """
        )
        accounts = [
            {
                "account_name": f"钧澈 codex 对接倍率仅文字0.0{i}",
                "upstream_group": "对接倍率",
                "page_rate": 0.06,
                "source_line": f"钧澈 codex 对接倍率仅文字0.0{i} sk-abc...def 对接倍率 0.06x",
            }
            for i in range(7)
        ]
        conn.execute(
            "insert into browser_adapter_status values (?, ?, ?, ?, ?, ?)",
            (
                "钧澈",
                "vip.lcodex.cn",
                "chrome",
                "browser_observed",
                "Chrome Tampermonkey read-only snapshot; balance=no; account_lines=22; rate_lines=1; script=0.1.15; wait_state=stable",
                "2026-06-12T19:59:00+00:00",
            ),
        )
        conn.execute(
            "insert into browser_adapter_snapshots values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
            (
                "钧澈",
                "vip.lcodex.cn",
                "chrome",
                "https://vip.lcodex.cn/token",
                "令牌管理",
                "",
                json.dumps(accounts, ensure_ascii=False),
                "[]",
                "",
                "2026-06-12T19:59:00+00:00",
            ),
        )

        rows = self.render_mod.load_provider_observations(conn, {})
        diagnostics = self.render_mod.load_provider_diagnostics(conn)
        self.assertEqual([], rows)
        self.assertEqual("extraction_loss", diagnostics[0]["sourceState"])
        self.assertEqual(22, diagnostics[0]["freshAccountCount"])
        self.assertEqual(7, diagnostics[0]["rawAccountCount"])
        self.assertEqual(7, diagnostics[0]["cleanAccountCount"])
        self.assertEqual(0, diagnostics[0]["displayedAccountCount"])

    def test_provider_snapshot_filters_quota_only_rows_and_repairs_kingdom_typo(self):
        conn = sqlite3.connect(":memory:")
        conn.row_factory = sqlite3.Row
        conn.executescript(
            """
            create table browser_adapter_snapshots (
              provider text not null,
              site text not null,
              browser text not null,
              page_url text not null,
              page_title text not null,
              detected_balance text not null,
              detected_accounts_json text not null default '[]',
              detected_rates_json text not null,
              sanitized_excerpt text not null,
              observed_at text not null,
              unique(provider, site)
            );
            create table browser_adapter_status (
              provider text not null,
              site text not null,
              browser text not null,
              status text not null,
              detail text not null,
              observed_at text not null,
              unique(provider, site)
            );
            """
        )
        conn.execute(
            """
            insert into browser_adapter_status values (?, ?, ?, ?, ?, ?)
            """,
            (
                "钧澈",
                "vip.lcodex.cn",
                "chrome",
                "browser_observed",
                "Chrome Tampermonkey read-only snapshot; account_lines=2; script=0.1.15",
                "2026-06-12T19:59:00+00:00",
            ),
        )
        conn.execute(
            """
            insert into browser_adapter_status values (?, ?, ?, ?, ?, ?)
            """,
            (
                "Kingdom",
                "api.tokenskingdom.com",
                "chrome",
                "browser_observed",
                "Chrome Tampermonkey read-only snapshot; account_lines=2; script=0.1.15",
                "2026-06-12T19:59:00+00:00",
            ),
        )
        conn.execute(
            """
            insert into browser_adapter_snapshots values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                "钧澈",
                "vip.lcodex.cn",
                "chrome",
                "https://vip.lcodex.cn/token",
                "令牌管理",
                "",
                json.dumps(
                    [
                        {
                            "account_name": "非自用",
                            "upstream_group": "¥585.03 GPT-PRO纯享号池",
                            "page_rate": 0.15,
                            "source_line": "非自用 已启用 ¥196.63 / ¥585.03 GPT-PRO纯享号池 0.15x 无限制",
                        },
                        {
                            "account_name": "钧澈 codex 对接倍率仅文字0.05*0.93=0.0455",
                            "upstream_group": "对接倍率",
                            "page_rate": 0.05,
                            "source_line": "钧澈 codex 对接倍率仅文字0.05*0.93=0.0455 sk-abc...def 对接倍率 0.05x",
                        },
                    ],
                    ensure_ascii=False,
                ),
                "[]",
                "",
                "2026-06-12T19:59:00+00:00",
            ),
        )
        conn.execute(
            """
            insert into browser_adapter_snapshots values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                "Kingdom",
                "api.tokenskingdom.com",
                "chrome",
                "https://tokenskingdom.com/keys",
                "API 密钥",
                "",
                json.dumps(
                    [
                        {
                            "account_name": "ingdom codex 超级特惠 仅文字0.95*0.078=0.0741",
                            "upstream_group": "plus 1号池",
                            "page_rate": 0.95,
                            "source_line": "ingdom codex 超级特惠 仅文字0.95*0.078=0.0741 sk-abc...def plus 1号池 0.95x",
                        },
                        {
                            "account_name": "kingdom codex plus1号 仅文字1.7*0.078=0.1326",
                            "upstream_group": "Plus 兜底 保稳1.",
                            "page_rate": 8,
                            "source_line": "kingdom codex plus1号 仅文字1.7*0.078=0.1326 sk-def...abc Plus 兜底 保稳1.8x1.8x选择分组",
                        }
                    ],
                    ensure_ascii=False,
                ),
                "[]",
                "",
                "2026-06-12T19:59:00+00:00",
            ),
        )

        rows = self.render_mod.load_provider_observations(conn, {})
        names = [row["accountName"] for row in rows]
        self.assertNotIn("非自用", names)
        self.assertIn("钧澈 codex 对接倍率仅文字0.05*0.93=0.0455", names)
        self.assertIn("kingdom codex 超级特惠 仅文字0.95*0.078=0.0741", names)
        kingdom = next(row for row in rows if row["site"] == "api.tokenskingdom.com" and row["accountName"].startswith("kingdom codex plus1号"))
        self.assertAlmostEqual(1.8, kingdom["pageRate"])
        self.assertEqual("Plus 兜底 保稳", kingdom["upstreamGroup"])

    def test_provider_snapshot_skips_preserved_previous_account_lines(self):
        conn = sqlite3.connect(":memory:")
        conn.row_factory = sqlite3.Row
        conn.executescript(
            """
            create table browser_adapter_snapshots (
              provider text not null,
              site text not null,
              browser text not null,
              page_url text not null,
              page_title text not null,
              detected_balance text not null,
              detected_accounts_json text not null default '[]',
              detected_rates_json text not null,
              sanitized_excerpt text not null,
              observed_at text not null,
              unique(provider, site)
            );
            create table browser_adapter_status (
              provider text not null,
              site text not null,
              browser text not null,
              status text not null,
              detail text not null,
              observed_at text not null,
              unique(provider, site)
            );
            """
        )
        conn.execute(
            """
            insert into browser_adapter_status values (?, ?, ?, ?, ?, ?)
            """,
            (
                "Magic",
                "pool.gptstore.club",
                "chrome",
                "browser_observed",
                "Chrome Tampermonkey read-only snapshot; fresh_account_lines=0; preserved previous account lines from 2026-06-13T00:00:00+00:00",
                "2026-06-12T19:59:00+00:00",
            ),
        )
        conn.execute(
            """
            insert into browser_adapter_snapshots values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                "Magic",
                "pool.gptstore.club",
                "chrome",
                "https://pool.gptstore.club/keys",
                "API 密钥",
                "",
                json.dumps(
                    [
                        {
                            "account_name": "magic stale account 0.04",
                            "upstream_group": "旧组",
                            "page_rate": 0.04,
                            "source_line": "magic stale account 0.04 sk-abc...def 旧组 0.04x",
                        }
                    ],
                    ensure_ascii=False,
                ),
                "[]",
                "",
                "2026-06-12T19:59:00+00:00",
            ),
        )

        rows = self.render_mod.load_provider_observations(conn, {})
        self.assertEqual([], rows)

    def test_provider_snapshot_skips_preserved_previous_non_empty_snapshot(self):
        conn = sqlite3.connect(":memory:")
        conn.row_factory = sqlite3.Row
        conn.executescript(
            """
            create table browser_adapter_snapshots (
              provider text not null,
              site text not null,
              browser text not null,
              page_url text not null,
              page_title text not null,
              detected_balance text not null,
              detected_accounts_json text not null default '[]',
              detected_rates_json text not null,
              sanitized_excerpt text not null,
              observed_at text not null,
              unique(provider, site)
            );
            create table browser_adapter_status (
              provider text not null,
              site text not null,
              browser text not null,
              status text not null,
              detail text not null,
              observed_at text not null,
              unique(provider, site)
            );
            """
        )
        conn.execute(
            """
            insert into browser_adapter_status values (?, ?, ?, ?, ?, ?)
            """,
            (
                "Kingdom",
                "api.tokenskingdom.com",
                "Safari",
                "browser_observed",
                "latest read was empty; preserved previous non-empty snapshot from 2026-06-12T19:59:00+00:00; no open logged-in tab for api.tokenskingdom.com",
                "2026-06-12T19:59:00+00:00",
            ),
        )
        conn.execute(
            """
            insert into browser_adapter_snapshots values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                "Kingdom",
                "api.tokenskingdom.com",
                "chrome",
                "https://api.tokenskingdom.com/keys",
                "API 密钥",
                "余额 $587.05",
                json.dumps(
                    [
                        {
                            "account_name": "kingdom stale account 0.88",
                            "upstream_group": "旧 Plus 组",
                            "page_rate": 0.88,
                            "source_line": "kingdom stale account 0.88 sk-abc...def 旧 Plus 组 0.88x",
                        }
                    ],
                    ensure_ascii=False,
                ),
                "[]",
                "",
                "2026-06-12T19:59:00+00:00",
            ),
        )

        rows = self.render_mod.load_provider_observations(conn, {})
        diagnostics = self.render_mod.load_provider_diagnostics(conn)
        self.assertEqual([], rows)
        self.assertEqual("hidden_preserved", diagnostics[0]["sourceState"])
        self.assertEqual(1, diagnostics[0]["rawAccountCount"])
        self.assertEqual(0, diagnostics[0]["displayedAccountCount"])

    def test_provider_snapshot_hides_browser_mismatch_rows(self):
        conn = sqlite3.connect(":memory:")
        conn.row_factory = sqlite3.Row
        conn.executescript(
            """
            create table browser_adapter_snapshots (
              provider text not null,
              site text not null,
              browser text not null,
              page_url text not null,
              page_title text not null,
              detected_balance text not null,
              detected_accounts_json text not null default '[]',
              detected_rates_json text not null,
              sanitized_excerpt text not null,
              observed_at text not null,
              unique(provider, site)
            );
            create table browser_adapter_status (
              provider text not null,
              site text not null,
              browser text not null,
              status text not null,
              detail text not null,
              observed_at text not null,
              unique(provider, site)
            );
            """
        )
        conn.execute(
            "insert into browser_adapter_status values (?, ?, ?, ?, ?, ?)",
            (
                "Kingdom",
                "api.tokenskingdom.com",
                "Safari",
                "browser_observed",
                "tabs=1; rate_lines=0",
                "2026-06-12T19:59:00+00:00",
            ),
        )
        conn.execute(
            "insert into browser_adapter_snapshots values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
            (
                "Kingdom",
                "api.tokenskingdom.com",
                "chrome",
                "https://api.tokenskingdom.com/keys",
                "API 密钥",
                "余额 $587.05",
                json.dumps(
                    [
                        {
                            "account_name": "kingdom old chrome account 0.95",
                            "upstream_group": "CC Max 1号池",
                            "page_rate": 0.95,
                            "source_line": "kingdom old chrome account 0.95 sk-abc...def CC Max 1号池 0.95x",
                        }
                    ],
                    ensure_ascii=False,
                ),
                "[]",
                "",
                "2026-06-12T19:59:00+00:00",
            ),
        )

        rows = self.render_mod.load_provider_observations(conn, {})
        diagnostics = self.render_mod.load_provider_diagnostics(conn)
        self.assertEqual([], rows)
        self.assertEqual("browser_mismatch", diagnostics[0]["sourceState"])
        self.assertEqual(1, diagnostics[0]["rawAccountCount"])
        self.assertEqual(0, diagnostics[0]["displayedAccountCount"])

    def test_provider_diagnostics_treats_legacy_script_as_non_current_coverage(self):
        tmp = tempfile.NamedTemporaryFile(suffix=".sqlite", delete=False)
        tmp.close()
        conn = sqlite3.connect(tmp.name)
        conn.row_factory = sqlite3.Row
        conn.executescript(
            """
            create table metadata (key text primary key, value text not null);
            create table upstream_rate_records (
              id integer primary key,
              category text not null,
              kind text not null,
              site text not null,
              fluter_account_name text not null,
              upstream_group text not null,
              page_rate real,
              recharge_ratio_label text not null,
              recharge_factor real,
              site_account_multiplier real,
              site_group_multiplier text not null,
              actual_cost_label text not null default '',
              balance_label text not null,
              balance_updated_at text not null,
              status text not null,
              note text not null,
              updated_at text not null
            );
            create table browser_adapter_snapshots (
              provider text not null,
              site text not null,
              browser text not null,
              page_url text not null,
              page_title text not null,
              detected_balance text not null,
              detected_accounts_json text not null default '[]',
              detected_rates_json text not null,
              sanitized_excerpt text not null,
              observed_at text not null,
              unique(provider, site)
            );
            create table browser_adapter_status (
              provider text not null,
              site text not null,
              browser text not null,
              status text not null,
              detail text not null,
              observed_at text not null,
              unique(provider, site)
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
            """
        )
        conn.execute(
            "insert into browser_adapter_status values (?, ?, ?, ?, ?, ?)",
            (
                "聪明AI",
                "sub2.congmingai.com",
                "chrome",
                "browser_observed",
                "Chrome Tampermonkey read-only snapshot; account_lines=1; script=0.1.12",
                "2026-06-15T09:00:00+00:00",
            ),
        )
        conn.execute(
            "insert into browser_adapter_snapshots values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
            (
                "聪明AI",
                "sub2.congmingai.com",
                "chrome",
                "https://sub2.congmingai.com/keys",
                "API 密钥",
                "余额 $40.44",
                json.dumps(
                    [
                        {
                            "account_name": "聪明ai codex 对接 仅文字0.05",
                            "upstream_group": "当前组",
                            "page_rate": 0.05,
                            "source_line": "聪明ai codex 对接 仅文字0.05 sk-abc...def 当前组 0.05x",
                        }
                    ],
                    ensure_ascii=False,
                ),
                "[]",
                "",
                "2026-06-15T09:00:00+00:00",
            ),
        )
        conn.execute(
            "insert into upstream_adapter_status values (?, ?, ?, ?, ?, ?)",
            (
                "聪明AI",
                "sub2.congmingai.com",
                "browser_or_key_required",
                "needs_adapter",
                "browser/API-key adapter needed",
                "2026-06-15T09:00:00+00:00",
            ),
        )
        conn.commit()
        conn.close()
        try:
            (
                _rows,
                _kbq_rows,
                _kbq_per_call_rows,
                _audit_summary,
                _audit_buckets,
                adapter_status,
                _metadata,
                _priority_plan,
                provider_observations,
                provider_diagnostics,
                _balance_snapshots,
            ) = self.render_mod.load_rows(tmp.name)
        finally:
            Path(tmp.name).unlink(missing_ok=True)

        public_item = next(
            item
            for item in adapter_status
            if item["site"] == "sub2.congmingai.com" and item["adapterKind"] == "browser_or_key_required"
        )
        self.assertEqual("needs_adapter", public_item["status"])
        self.assertEqual([], provider_observations)
        self.assertEqual("legacy_script", provider_diagnostics[0]["sourceState"])

    def test_provider_snapshot_skips_preserved_marker_after_long_detail_prefix(self):
        conn = sqlite3.connect(":memory:")
        conn.row_factory = sqlite3.Row
        conn.executescript(
            """
            create table browser_adapter_snapshots (
              provider text not null,
              site text not null,
              browser text not null,
              page_url text not null,
              page_title text not null,
              detected_balance text not null,
              detected_accounts_json text not null default '[]',
              detected_rates_json text not null,
              sanitized_excerpt text not null,
              observed_at text not null,
              unique(provider, site)
            );
            create table browser_adapter_status (
              provider text not null,
              site text not null,
              browser text not null,
              status text not null,
              detail text not null,
              observed_at text not null,
              unique(provider, site)
            );
            """
        )
        long_prefix = "; ".join(
            f"ignored low-signal collector snapshot from 2026-06-14T00:{minute:02d}:00+00:00"
            for minute in range(12)
        )
        conn.execute(
            "insert into browser_adapter_status values (?, ?, ?, ?, ?, ?)",
            (
                "钧澈",
                "vip.lcodex.cn",
                "chrome",
                "browser_observed",
                f"Chrome Tampermonkey read-only snapshot; {long_prefix}; fresh_account_lines=0; preserved previous account lines from 2026-06-13T00:00:00+00:00",
                "2026-06-12T19:59:00+00:00",
            ),
        )
        conn.execute(
            "insert into browser_adapter_snapshots values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
            (
                "钧澈",
                "vip.lcodex.cn",
                "chrome",
                "https://vip.lcodex.cn/console/token",
                "令牌管理",
                "",
                json.dumps(
                    [
                        {
                            "account_name": "钧澈 stale account 0.05",
                            "upstream_group": "旧组",
                            "page_rate": 0.05,
                            "source_line": "钧澈 stale account 0.05 sk-abc...def 旧组 0.05x",
                        }
                    ],
                    ensure_ascii=False,
                ),
                "[]",
                "",
                "2026-06-12T19:59:00+00:00",
            ),
        )

        rows = self.render_mod.load_provider_observations(conn, {})
        self.assertEqual([], rows)

    def test_provider_snapshot_keeps_current_accounts_with_ignored_low_signal_history(self):
        conn = sqlite3.connect(":memory:")
        conn.row_factory = sqlite3.Row
        conn.executescript(
            """
            create table browser_adapter_snapshots (
              provider text not null,
              site text not null,
              browser text not null,
              page_url text not null,
              page_title text not null,
              detected_balance text not null,
              detected_accounts_json text not null default '[]',
              detected_rates_json text not null,
              sanitized_excerpt text not null,
              observed_at text not null,
              unique(provider, site)
            );
            create table browser_adapter_status (
              provider text not null,
              site text not null,
              browser text not null,
              status text not null,
              detail text not null,
              observed_at text not null,
              unique(provider, site)
            );
            """
        )
        conn.execute(
            "insert into browser_adapter_status values (?, ?, ?, ?, ?, ?)",
            (
                "Meow",
                "api.saki.lat",
                "chrome",
                "browser_observed",
                "Chrome Tampermonkey read-only snapshot; balance=yes; account_lines=6; rate_lines=1; script=0.1.15; ignored low-signal collector snapshot from 2026-06-14T00:00:00+00:00",
                "2026-06-12T19:59:00+00:00",
            ),
        )
        conn.execute(
            "insert into browser_adapter_snapshots values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
            (
                "Meow",
                "api.saki.lat",
                "chrome",
                "https://api.saki.lat/keys",
                "API 密钥",
                "",
                json.dumps(
                    [
                        {
                            "account_name": "meow codex 当前账号 0.05",
                            "upstream_group": "当前组",
                            "page_rate": 0.05,
                            "source_line": "meow codex 当前账号 0.05 sk-abc...def 当前组 0.05x",
                        }
                    ],
                    ensure_ascii=False,
                ),
                "[]",
                "",
                "2026-06-12T19:59:00+00:00",
            ),
        )

        rows = self.render_mod.load_provider_observations(conn, {})
        self.assertEqual(["meow codex 当前账号 0.05"], [row["accountName"] for row in rows])

    def test_provider_snapshot_hides_legacy_tampermonkey_rows(self):
        conn = sqlite3.connect(":memory:")
        conn.row_factory = sqlite3.Row
        conn.executescript(
            """
            create table browser_adapter_snapshots (
              provider text not null,
              site text not null,
              browser text not null,
              page_url text not null,
              page_title text not null,
              detected_balance text not null,
              detected_accounts_json text not null default '[]',
              detected_rates_json text not null,
              sanitized_excerpt text not null,
              observed_at text not null,
              unique(provider, site)
            );
            create table browser_adapter_status (
              provider text not null,
              site text not null,
              browser text not null,
              status text not null,
              detail text not null,
              observed_at text not null,
              unique(provider, site)
            );
            """
        )
        conn.execute(
            "insert into browser_adapter_status values (?, ?, ?, ?, ?, ?)",
            (
                "Kingdom",
                "api.tokenskingdom.com",
                "chrome",
                "browser_observed",
                "Chrome Tampermonkey read-only snapshot; balance=yes; account_lines=7; rate_lines=0; script=0.1.12",
                "2026-06-12T19:59:00+00:00",
            ),
        )
        conn.execute(
            "insert into browser_adapter_snapshots values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
            (
                "Kingdom",
                "api.tokenskingdom.com",
                "chrome",
                "https://tokenskingdom.com/keys",
                "API 密钥",
                "余额 $587.05",
                json.dumps(
                    [
                        {
                            "account_name": "kingdom codex plus2号 仅文字1.2*0.078=0.0936",
                            "upstream_group": "Plus 2号池",
                            "page_rate": 1.2,
                            "source_line": "kingdom codex plus2号 仅文字1.2*0.078=0.0936 sk-abc...def Plus 2号池 1.2x",
                        }
                    ],
                    ensure_ascii=False,
                ),
                "[]",
                "",
                "2026-06-12T19:59:00+00:00",
            ),
        )

        rows = self.render_mod.load_provider_observations(conn, {})
        diagnostics = self.render_mod.load_provider_diagnostics(conn)
        self.assertEqual([], rows)
        self.assertEqual("legacy_script", diagnostics[0]["sourceState"])
        self.assertEqual(1, diagnostics[0]["rawAccountCount"])
        self.assertEqual(0, diagnostics[0]["displayedAccountCount"])

    def test_provider_snapshot_hides_timeout_rows(self):
        conn = sqlite3.connect(":memory:")
        conn.row_factory = sqlite3.Row
        conn.executescript(
            """
            create table browser_adapter_snapshots (
              provider text not null,
              site text not null,
              browser text not null,
              page_url text not null,
              page_title text not null,
              detected_balance text not null,
              detected_accounts_json text not null default '[]',
              detected_rates_json text not null,
              sanitized_excerpt text not null,
              observed_at text not null,
              unique(provider, site)
            );
            create table browser_adapter_status (
              provider text not null,
              site text not null,
              browser text not null,
              status text not null,
              detail text not null,
              observed_at text not null,
              unique(provider, site)
            );
            """
        )
        conn.execute(
            "insert into browser_adapter_status values (?, ?, ?, ?, ?, ?)",
            (
                "钧澈",
                "vip.lcodex.cn",
                "chrome",
                "browser_observed",
                "Chrome Tampermonkey read-only snapshot; balance=yes; account_lines=7; rate_lines=1; script=0.1.15; wait_state=timeout; best_account_lines=7",
                "2026-06-12T19:59:00+00:00",
            ),
        )
        conn.execute(
            "insert into browser_adapter_snapshots values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
            (
                "钧澈",
                "vip.lcodex.cn",
                "chrome",
                "https://vip.lcodex.cn/console/token",
                "钧澈API",
                "当前余额 ¥765.71",
                json.dumps(
                    [
                        {
                            "account_name": "钧澈 codex 对接倍率仅文字0.09*0.93=0.0819",
                            "upstream_group": "对接倍率",
                            "page_rate": 0.09,
                            "source_line": "钧澈 codex 对接倍率仅文字0.09*0.93=0.0819 sk-abc...def 对接倍率 0.09x",
                        }
                    ],
                    ensure_ascii=False,
                ),
                "[]",
                "",
                "2026-06-12T19:59:00+00:00",
            ),
        )

        rows = self.render_mod.load_provider_observations(conn, {})
        diagnostics = self.render_mod.load_provider_diagnostics(conn)
        self.assertEqual([], rows)
        self.assertEqual("unstable_snapshot", diagnostics[0]["sourceState"])
        self.assertEqual(1, diagnostics[0]["rawAccountCount"])
        self.assertEqual(0, diagnostics[0]["displayedAccountCount"])

    def test_provider_snapshot_hides_stale_rows_with_default_freshness_window(self):
        self.render_mod.PROVIDER_SNAPSHOT_MAX_AGE_SECONDS = 3600
        conn = sqlite3.connect(":memory:")
        conn.row_factory = sqlite3.Row
        conn.executescript(
            """
            create table browser_adapter_snapshots (
              provider text not null,
              site text not null,
              browser text not null,
              page_url text not null,
              page_title text not null,
              detected_balance text not null,
              detected_accounts_json text not null default '[]',
              detected_rates_json text not null,
              sanitized_excerpt text not null,
              observed_at text not null,
              unique(provider, site)
            );
            create table browser_adapter_status (
              provider text not null,
              site text not null,
              browser text not null,
              status text not null,
              detail text not null,
              observed_at text not null,
              unique(provider, site)
            );
            """
        )
        conn.execute(
            "insert into browser_adapter_status values (?, ?, ?, ?, ?, ?)",
            (
                "Kingdom",
                "api.tokenskingdom.com",
                "chrome",
                "browser_observed",
                "Chrome Tampermonkey read-only snapshot; account_lines=1; script=0.1.15",
                "2026-06-12T19:59:00+00:00",
            ),
        )
        conn.execute(
            "insert into browser_adapter_snapshots values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
            (
                "Kingdom",
                "api.tokenskingdom.com",
                "chrome",
                "https://tokenskingdom.com/keys",
                "API 密钥",
                "余额 $727.17",
                json.dumps(
                    [
                        {
                            "account_name": "kingdom stale current-looking account",
                            "upstream_group": "Plus 2号池",
                            "page_rate": 1.2,
                            "source_line": "kingdom stale current-looking account sk-abc...def Plus 2号池 1.2x",
                        }
                    ],
                    ensure_ascii=False,
                ),
                "[]",
                "",
                "2026-06-12T19:59:00+00:00",
            ),
        )

        rows = self.render_mod.load_provider_observations(conn, {})
        diagnostics = self.render_mod.load_provider_diagnostics(conn)
        self.assertEqual([], rows)
        self.assertEqual("stale_snapshot", diagnostics[0]["sourceState"])
        self.assertEqual(1, diagnostics[0]["rawAccountCount"])
        self.assertEqual(0, diagnostics[0]["displayedAccountCount"])

    def test_provider_snapshot_hides_misaligned_status_and_snapshot_times(self):
        conn = sqlite3.connect(":memory:")
        conn.row_factory = sqlite3.Row
        conn.executescript(
            """
            create table browser_adapter_snapshots (
              provider text not null,
              site text not null,
              browser text not null,
              page_url text not null,
              page_title text not null,
              detected_balance text not null,
              detected_accounts_json text not null default '[]',
              detected_rates_json text not null,
              sanitized_excerpt text not null,
              observed_at text not null,
              unique(provider, site)
            );
            create table browser_adapter_status (
              provider text not null,
              site text not null,
              browser text not null,
              status text not null,
              detail text not null,
              observed_at text not null,
              unique(provider, site)
            );
            """
        )
        conn.execute(
            "insert into browser_adapter_status values (?, ?, ?, ?, ?, ?)",
            (
                "Kingdom",
                "api.tokenskingdom.com",
                "chrome",
                "browser_observed",
                "Chrome Tampermonkey read-only snapshot; account_lines=1; script=0.1.15",
                "2026-06-12T19:59:00+00:00",
            ),
        )
        conn.execute(
            "insert into browser_adapter_snapshots values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
            (
                "Kingdom",
                "api.tokenskingdom.com",
                "chrome",
                "https://tokenskingdom.com/keys",
                "API 密钥",
                "余额 $727.17",
                json.dumps(
                    [
                        {
                            "account_name": "kingdom stale current-looking account",
                            "upstream_group": "Plus 2号池",
                            "page_rate": 1.2,
                            "source_line": "kingdom stale current-looking account sk-abc...def Plus 2号池 1.2x",
                        }
                    ],
                    ensure_ascii=False,
                ),
                "[]",
                "",
                "2026-06-12T20:20:00+00:00",
            ),
        )

        rows = self.render_mod.load_provider_observations(conn, {})
        diagnostics = self.render_mod.load_provider_diagnostics(conn)
        self.assertEqual([], rows)
        self.assertEqual("misaligned_snapshot", diagnostics[0]["sourceState"])
        self.assertEqual(1, diagnostics[0]["rawAccountCount"])
        self.assertEqual(0, diagnostics[0]["displayedAccountCount"])

    def test_dashboard_does_not_render_provider_matrix_or_diagnostics(self):
        html = self.render_mod.render(
            rows=[],
            kbq_rows=[],
            kbq_per_call_rows=[],
            audit_summary={},
            audit_buckets=[],
            adapter_status=[
                {
                    "provider": "Kingdom",
                    "site": "api.tokenskingdom.com",
                    "status": "browser_observed",
                    "detail": "Chrome Tampermonkey read-only snapshot; account_lines=6; script=0.1.12",
                }
            ],
            metadata={},
            priority_plan=[],
            provider_observations=[],
            provider_diagnostics=[
                {
                    "provider": "Kingdom",
                    "site": "api.tokenskingdom.com",
                    "sourceState": "legacy_script",
                    "rawAccountCount": 6,
                    "displayedAccountCount": 0,
                }
            ],
        )
        self.assertNotIn("function groupProviderObservationsBySite", html)
        self.assertNotIn("ADAPTER_STATUS", html)
        self.assertNotIn("采集诊断", html)
        self.assertNotIn("非上游账号清单", html)


if __name__ == "__main__":
    unittest.main()
