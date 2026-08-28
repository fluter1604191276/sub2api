#!/usr/bin/env python3
from __future__ import annotations

import importlib.util
import json
import sqlite3
import sys
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("refresh_browser_readonly_adapters.py")


def load_module():
    spec = importlib.util.spec_from_file_location("refresh_browser_readonly_adapters", SCRIPT)
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class RefreshBrowserReadonlyAdaptersTest(unittest.TestCase):
    def setUp(self) -> None:
        self.mod = load_module()

    def connection(self) -> sqlite3.Connection:
        conn = sqlite3.connect(":memory:")
        conn.row_factory = sqlite3.Row
        conn.executescript(self.mod.SCHEMA)
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
            insert into upstream_rate_records (
              category, kind, site, fluter_account_name, upstream_group,
              page_rate, recharge_factor, site_account_multiplier, status, note, updated_at
            ) values (
              '超超 Mouubox', 'Codex', 'api.mouubox.com', '超超(主站) codex 0.03',
              'gpt', 0.008, 1, 0.03, '偏保守', '', 'old'
            );
            """
        )
        return conn

    def observation(self, observed_at: str) -> dict:
        return {
            "provider": "超超 Mouubox",
            "site": "api.mouubox.com",
            "observed_at": observed_at,
            "detected_rates": ["sk-442...0130 / gpt / 0.03x / 选择分组"],
        }

    def test_stale_browser_rate_observation_does_not_update_ledger(self):
        conn = self.connection()

        updates = self.mod.apply_browser_rates_to_ledger(
            conn,
            [self.observation("2026-06-12T18:00:00+00:00")],
            "2026-06-12T20:00:00+00:00",
        )

        self.assertEqual([], updates)
        row = conn.execute("select page_rate, actual_cost_label, updated_at from upstream_rate_records").fetchone()
        self.assertAlmostEqual(0.008, row["page_rate"])
        self.assertEqual("", row["actual_cost_label"])
        self.assertEqual("old", row["updated_at"])

    def test_fresh_browser_rate_observation_updates_ledger(self):
        conn = self.connection()

        updates = self.mod.apply_browser_rates_to_ledger(
            conn,
            [self.observation("2026-06-12T19:59:00+00:00")],
            "2026-06-12T20:00:00+00:00",
        )

        self.assertEqual(["超超 Mouubox 超超(主站) codex 0.03: 0.008x -> 0.03x"], updates)
        row = conn.execute("select page_rate, actual_cost_label, updated_at from upstream_rate_records").fetchone()
        self.assertAlmostEqual(0.03, row["page_rate"])
        self.assertIn("实际成本倍率 0.03x", row["actual_cost_label"])
        self.assertEqual("2026-06-12T20:00:00+00:00", row["updated_at"])

    def test_balance_detection_does_not_treat_group_multiplier_as_balance(self):
        text = """
        名称 状态 剩余额度/总额度 分组 密钥 可用模型
        无限额度
        对接倍率
        0.05x
        无限制
        """

        self.assertEqual("", self.mod.detect_balance(text, page_url="https://vip.lcodex.cn/console/token"))

    def test_key_quota_table_is_not_wallet_balance(self):
        text = (
            "令牌管理 名称 状态 剩余额度/总额度 分组 密钥 可用模型 "
            "非自用 已启用 ¥154.41 / ¥200.00 GPT-PRO纯享号池 0.18x"
        )

        self.assertEqual(
            "",
            self.mod.detect_balance(text, page_url="https://vip.lcodex.cn/console/token"),
        )

    def test_concatenated_dashboard_balance_is_trimmed_to_label_and_amount(self):
        text = "空悲切 API首页控制台账户数据当前余额¥139.75充值历史消耗¥406.85"

        self.assertEqual(
            "当前余额 ¥139.75",
            self.mod.detect_balance(text, page_url="https://xn--vduyey89e.com/console"),
        )

    def test_dashboard_balance_without_nearby_amount_is_ignored(self):
        text = (
            "👋晚上好，fluter账户数据当前余额充值历史消耗使用统计请求次数统计次数"
            "资源消耗统计额度统计Tokens性能指标平均RPM平均TPM模型数据分析"
            "API信息暂无API信息请联系管理员在系统设置中配置"
        )

        self.assertEqual(
            "",
            self.mod.detect_balance(text, page_url="https://vip.lcodex.cn/console"),
        )

    def test_dashboard_money_without_balance_label_is_detected_for_logged_in_home(self):
        text = (
            "聪明AI仪表盘API 密钥使用记录渠道状态我的订阅充值/订阅我的订单兑换个人资料"
            "openai服务器状态浅色模式收起$40.99FLfluter1604191276user 创建密钥"
        )

        self.assertEqual(
            "余额 $40.99",
            self.mod.detect_balance(text, page_url="https://sub2.congmingai.com/keys"),
        )

    def test_dashboard_money_fallback_ignores_usage_amounts(self):
        text = (
            "API 密钥管理 名称 分组 用量 速率限制 "
            "聪明ai codex 对接 今日: $7.8016 近30天: $7.8016 活跃"
        )

        self.assertEqual(
            "",
            self.mod.detect_balance(text, page_url="https://sub2.congmingai.com/keys"),
        )

    def test_usage_page_money_without_balance_label_is_not_balance(self):
        text = "使用记录 请求路径 /v1/responses 扣费 $0.0312 状态 成功"

        self.assertEqual(
            "",
            self.mod.detect_balance(text, page_url="https://sub2.congmingai.com/usage"),
        )

    def test_probable_balance_rejects_long_dashboard_noise(self):
        text = (
            "👋晚上好，fluter账户数据当前余额充值历史消耗使用统计请求次数统计次数"
            "资源消耗统计额度统计Tokens性能指标平均RPM平均TPM模型数据分析"
            "API信息暂无API信息请联系管理员在系统设置中配置"
        )

        self.assertFalse(self.mod.is_probable_balance_label(text))
        self.assertTrue(self.mod.is_probable_balance_label("当前余额 ¥120.30"))

    def test_amount_from_line_ignores_plain_multiplier(self):
        self.assertEqual("", self.mod.amount_from_line("对接倍率 0.05x 无限制"))
        self.assertEqual("¥0.05", self.mod.amount_from_line("余额 ¥0.05"))

    def test_magic_concatenated_rates_use_last_active_rate(self):
        self.assertEqual(
            ("Pro（可用非流式传输）", 0.08),
            self.mod.parse_browser_rate_line("sk-29b...f630 / Pro（可用非流式传输） / 0.13x0.08x / 选择分组"),
        )
        self.assertEqual(
            ("代理快速渠道", 0.045),
            self.mod.parse_browser_rate_line("sk-0ae...e8e3 / 代理快速渠道 / 0.06x0.045x / 选择分组"),
        )
        self.assertEqual(
            ("Plus 兜底 保稳", 1.8),
            self.mod.parse_browser_rate_line("sk-dea...5dfb / Plus 兜底 保稳1.8x1.8x / 选择分组"),
        )
        self.assertEqual(
            ("Plus 兜底 保稳", 1.8),
            self.mod.parse_browser_rate_line("sk-dea...5dfb / Plus 兜底 保稳1.8倍1.8倍 / 选择分组"),
        )

    def test_quota_only_browser_snapshot_account_line_is_rejected(self):
        cleaned = self.mod.clean_detected_account(
            {
                "account_name": "非自用",
                "upstream_group": "¥585.03 GPT-PRO纯享号池",
                "page_rate": 0.15,
                "source_line": "非自用 已启用 ¥196.63 / ¥585.03 GPT-PRO纯享号池 0.15x 无限制 无限制",
            }
        )
        self.assertIsNone(cleaned)

    def test_magic_browser_groups_match_current_site_account_names(self):
        conn = self.connection()
        conn.execute("delete from upstream_rate_records")
        rows = [
            ("magic codex pro 仅文字0.08", "Pro号池（生图请选择生图分组）"),
            ("magic codex 代理快速通道 仅文字0.045", "代理快速渠道（不能生图）"),
        ]
        for account_name, upstream_group in rows:
            conn.execute(
                """
                insert into upstream_rate_records (
                  category, kind, site, fluter_account_name, upstream_group,
                  page_rate, recharge_factor, site_account_multiplier, status, note, updated_at
                ) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                """,
                (
                    "Magic",
                    "Codex",
                    "pool.gptstore.club",
                    account_name,
                    upstream_group,
                    1,
                    1,
                    1,
                    "已确认",
                    "",
                    "old",
                ),
            )
        updates = self.mod.apply_browser_rates_to_ledger(
            conn,
            [
                {
                    "provider": "Magic",
                    "site": "pool.gptstore.club",
                    "observed_at": "2026-06-12T19:59:00+00:00",
                    "detected_rates": [
                        "sk-29b...f630 / Pro（可用非流式传输） / 0.13x0.08x / 选择分组",
                        "sk-0ae...e8e3 / 代理快速渠道 / 0.06x0.045x / 选择分组",
                    ],
                }
            ],
            "2026-06-12T20:00:00+00:00",
        )

        self.assertEqual(2, len(updates))
        rates = {
            row["fluter_account_name"]: row["page_rate"]
            for row in conn.execute("select fluter_account_name, page_rate from upstream_rate_records")
        }
        self.assertAlmostEqual(0.08, rates["magic codex pro 仅文字0.08"])
        self.assertAlmostEqual(0.045, rates["magic codex 代理快速通道 仅文字0.045"])

    def test_short_group_name_does_not_match_longer_related_group(self):
        conn = self.connection()

        updates = self.mod.apply_browser_rates_to_ledger(
            conn,
            [
                {
                    "provider": "超超 Mouubox",
                    "site": "api.mouubox.com",
                    "observed_at": "2026-06-12T19:59:00+00:00",
                    "detected_rates": [
                        "sk-a22...86f1 / gpt-pro / 0.1x / 选择分组",
                        "sk-442...0130 / gpt / 0.03x / 选择分组",
                    ],
                }
            ],
            "2026-06-12T20:00:00+00:00",
        )

        self.assertEqual(["超超 Mouubox 超超(主站) codex 0.03: 0.008x -> 0.03x"], updates)
        row = conn.execute("select page_rate from upstream_rate_records").fetchone()
        self.assertAlmostEqual(0.03, row["page_rate"])
        observations = conn.execute(
            """
            select upstream_group, matched_ledger_rows
            from browser_adapter_rate_observations
            order by upstream_group
            """
        ).fetchall()
        self.assertEqual(
            [("gpt", 1), ("gpt-pro", 0)],
            [(row["upstream_group"], row["matched_ledger_rows"]) for row in observations],
        )

    def test_legacy_tampermonkey_rate_snapshot_does_not_update_ledger_or_observations(self):
        conn = self.connection()

        updates = self.mod.apply_browser_rates_to_ledger(
            conn,
            [
                {
                    "provider": "超超 Mouubox",
                    "site": "api.mouubox.com",
                    "observed_at": "2026-06-12T19:59:00+00:00",
                    "detail": "Chrome Tampermonkey read-only snapshot; rate_lines=1; script=0.1.12",
                    "detected_rates": ["sk-442...0130 / gpt / 0.03x / 选择分组"],
                }
            ],
            "2026-06-12T20:00:00+00:00",
        )

        self.assertEqual([], updates)
        row = conn.execute("select page_rate, updated_at from upstream_rate_records").fetchone()
        self.assertAlmostEqual(0.008, row["page_rate"])
        self.assertEqual("old", row["updated_at"])
        count = conn.execute("select count(*) from browser_adapter_rate_observations").fetchone()[0]
        self.assertEqual(0, count)

    def test_preserved_rate_lines_do_not_update_ledger_or_observations(self):
        conn = self.connection()

        updates = self.mod.apply_browser_rates_to_ledger(
            conn,
            [
                {
                    "provider": "超超 Mouubox",
                    "site": "api.mouubox.com",
                    "observed_at": "2026-06-12T19:59:00+00:00",
                    "detail": (
                        "Chrome Tampermonkey read-only snapshot; fresh_rate_lines=0; "
                        "preserved previous rate lines from 2026-06-12T18:00:00+00:00; script=0.1.15"
                    ),
                    "detected_rates": ["sk-442...0130 / gpt / 0.03x / 选择分组"],
                }
            ],
            "2026-06-12T20:00:00+00:00",
        )

        self.assertEqual([], updates)
        row = conn.execute("select page_rate, updated_at from upstream_rate_records").fetchone()
        self.assertAlmostEqual(0.008, row["page_rate"])
        self.assertEqual("old", row["updated_at"])
        count = conn.execute("select count(*) from browser_adapter_rate_observations").fetchone()[0]
        self.assertEqual(0, count)

    def test_preserved_non_empty_snapshot_does_not_update_rates_or_observations(self):
        conn = self.connection()

        updates = self.mod.apply_browser_rates_to_ledger(
            conn,
            [
                {
                    "provider": "超超 Mouubox",
                    "site": "api.mouubox.com",
                    "observed_at": "2026-06-12T19:59:00+00:00",
                    "detail": (
                        "latest read was empty; preserved previous non-empty snapshot "
                        "from 2026-06-12T18:00:00+00:00; script=0.1.15"
                    ),
                    "detected_rates": ["sk-442...0130 / gpt / 0.03x / 选择分组"],
                }
            ],
            "2026-06-12T20:00:00+00:00",
        )

        self.assertEqual([], updates)
        row = conn.execute("select page_rate, updated_at from upstream_rate_records").fetchone()
        self.assertAlmostEqual(0.008, row["page_rate"])
        self.assertEqual("old", row["updated_at"])
        count = conn.execute("select count(*) from browser_adapter_rate_observations").fetchone()[0]
        self.assertEqual(0, count)

    def test_timeout_rate_snapshot_does_not_update_ledger_or_observations(self):
        conn = self.connection()

        updates = self.mod.apply_browser_rates_to_ledger(
            conn,
            [
                {
                    "provider": "超超 Mouubox",
                    "site": "api.mouubox.com",
                    "observed_at": "2026-06-12T19:59:00+00:00",
                    "detail": "Chrome Tampermonkey read-only snapshot; rate_lines=1; script=0.1.15; wait_state=timeout",
                    "detected_rates": ["sk-442...0130 / gpt / 0.03x / 选择分组"],
                }
            ],
            "2026-06-12T20:00:00+00:00",
        )

        self.assertEqual([], updates)
        row = conn.execute("select page_rate, updated_at from upstream_rate_records").fetchone()
        self.assertAlmostEqual(0.008, row["page_rate"])
        self.assertEqual("old", row["updated_at"])
        count = conn.execute("select count(*) from browser_adapter_rate_observations").fetchone()[0]
        self.assertEqual(0, count)

    def test_empty_rate_refresh_preserves_previous_snapshot_rates(self):
        old_rate = "sk-29b...f630 / Pro（可用非流式传输） / 0.13x0.08x / 选择分组"
        with tempfile.TemporaryDirectory() as tmpdir:
            db_path = str(Path(tmpdir) / "ledger.sqlite")
            conn = sqlite3.connect(db_path)
            conn.row_factory = sqlite3.Row
            conn.executescript(self.mod.SCHEMA)
            conn.execute(
                """
                insert into browser_adapter_snapshots (
                  provider, site, browser, page_url, page_title, detected_balance,
                  detected_rates_json, sanitized_excerpt, observed_at
                ) values (?, ?, ?, ?, ?, ?, ?, ?, ?)
                """,
                (
                    "Magic",
                    "pool.gptstore.club",
                    "Chrome",
                    "https://pool.gptstore.club/token",
                    "Magic token",
                    "余额 ¥100",
                    json.dumps([old_rate], ensure_ascii=False),
                    "token page with rate",
                    "2026-06-12T20:00:00+00:00",
                ),
            )
            conn.execute(
                """
                insert into browser_adapter_rate_observations (
                  provider, site, upstream_group, page_rate, source_line,
                  matched_ledger_rows, observed_at
                ) values (?, ?, ?, ?, ?, ?, ?)
                """,
                (
                    "Magic",
                    "pool.gptstore.club",
                    "Pro（可用非流式传输）",
                    0.08,
                    old_rate,
                    1,
                    "2026-06-12T20:00:00+00:00",
                ),
            )
            conn.commit()
            conn.close()

            self.mod.write_observations(
                db_path,
                [
                    {
                        "provider": "Magic",
                        "site": "pool.gptstore.club",
                        "browser": "Safari",
                        "status": "browser_observed",
                        "detail": "tabs=1; rate_lines=0",
                        "observed_at": "2026-06-12T20:10:00+00:00",
                        "page_url": "https://pool.gptstore.club/dashboard",
                        "page_title": "Magic dashboard",
                        "detected_balance": "",
                        "detected_rates": [],
                        "sanitized_excerpt": "控制台页面正常，但这一页没有 key 分组倍率",
                    }
                ],
            )

            conn = sqlite3.connect(db_path)
            conn.row_factory = sqlite3.Row
            snapshot = conn.execute(
                """
                select browser, detected_rates_json, sanitized_excerpt, observed_at
                from browser_adapter_snapshots
                where provider = 'Magic' and site = 'pool.gptstore.club'
                """
            ).fetchone()
            status = conn.execute(
                """
                select detail
                from browser_adapter_status
                where provider = 'Magic' and site = 'pool.gptstore.club'
                """
            ).fetchone()
            rate_count = conn.execute(
                """
                select count(*) as count
                from browser_adapter_rate_observations
                where provider = 'Magic' and site = 'pool.gptstore.club'
                """
            ).fetchone()["count"]
            conn.close()

        self.assertEqual("Safari", snapshot["browser"])
        self.assertEqual([old_rate], json.loads(snapshot["detected_rates_json"]))
        self.assertIn("这一页没有 key 分组倍率", snapshot["sanitized_excerpt"])
        self.assertEqual("2026-06-12T20:10:00+00:00", snapshot["observed_at"])
        self.assertIn("preserved previous rate lines", status["detail"])
        self.assertEqual(1, rate_count)

    def test_browser_account_name_observation_updates_matching_ledger_row(self):
        conn = self.connection()
        conn.execute("delete from upstream_rate_records")
        conn.execute(
            """
            insert into upstream_rate_records (
              category, kind, site, fluter_account_name, upstream_group,
              page_rate, recharge_factor, site_account_multiplier, status, note, updated_at
            ) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                "Magic",
                "Codex",
                "pool.gptstore.club",
                "magic codex pro 仅文字0.08",
                "旧Pro号池",
                0.13,
                1,
                0.08,
                "需核对/倍率漂移",
                "",
                "old",
            ),
        )

        updates = self.mod.apply_browser_accounts_to_ledger(
            conn,
            [
                {
                    "provider": "Magic",
                    "site": "pool.gptstore.club",
                    "observed_at": "2026-06-12T19:59:00+00:00",
                    "detected_accounts": [
                        {
                            "account_name": "magic codex pro 仅文字0.08",
                            "upstream_group": "Pro号池",
                            "page_rate": 0.08,
                            "source_line": "magic codex pro 仅文字0.08 / Pro号池 / 0.08x / 选择分组",
                        }
                    ],
                }
            ],
            "2026-06-12T20:00:00+00:00",
        )

        self.assertEqual(["Magic magic codex pro 仅文字0.08: 0.13x -> 0.08x"], updates)
        row = conn.execute(
            "select page_rate, upstream_group, status, actual_cost_label from upstream_rate_records"
        ).fetchone()
        self.assertAlmostEqual(0.08, row["page_rate"])
        self.assertEqual("Pro号池", row["upstream_group"])
        self.assertEqual("已确认", row["status"])
        self.assertIn("实际成本倍率 0.08x", row["actual_cost_label"])
        observed = conn.execute(
            """
            select account_name, matched_ledger_rows
            from browser_adapter_account_observations
            where site = 'pool.gptstore.club'
            """
        ).fetchone()
        self.assertEqual("magic codex pro 仅文字0.08", observed["account_name"])
        self.assertEqual(1, observed["matched_ledger_rows"])

    def test_legacy_tampermonkey_account_snapshot_does_not_update_ledger_or_observations(self):
        conn = self.connection()
        conn.execute("delete from upstream_rate_records")
        conn.execute(
            """
            insert into upstream_rate_records (
              category, kind, site, fluter_account_name, upstream_group,
              page_rate, recharge_factor, site_account_multiplier, status, note, updated_at
            ) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                "Kingdom",
                "Codex",
                "api.tokenskingdom.com",
                "kingdom codex plus2号 仅文字1.2*0.078=0.0936",
                "Plus 2号池",
                0.93,
                0.07444,
                0.0936,
                "需核对/倍率漂移",
                "",
                "old",
            ),
        )

        updates = self.mod.apply_browser_accounts_to_ledger(
            conn,
            [
                {
                    "provider": "Kingdom",
                    "site": "api.tokenskingdom.com",
                    "observed_at": "2026-06-12T19:59:00+00:00",
                    "detail": "Chrome Tampermonkey read-only snapshot; balance=yes; account_lines=7; rate_lines=0; script=0.1.12",
                    "detected_accounts": [
                        {
                            "account_name": "kingdom codex plus2号 仅文字1.2*0.078=0.0936",
                            "upstream_group": "Plus 2号池",
                            "page_rate": 1.2,
                            "source_line": "kingdom codex plus2号 仅文字1.2*0.078=0.0936 sk-aaa...bbbb Plus 2号池 1.2x",
                        }
                    ],
                }
            ],
            "2026-06-12T20:00:00+00:00",
        )

        self.assertEqual([], updates)
        row = conn.execute(
            "select page_rate, status, updated_at from upstream_rate_records"
        ).fetchone()
        self.assertAlmostEqual(0.93, row["page_rate"])
        self.assertEqual("需核对/倍率漂移", row["status"])
        self.assertEqual("old", row["updated_at"])
        count = conn.execute("select count(*) from browser_adapter_account_observations").fetchone()[0]
        self.assertEqual(0, count)

    def test_preserved_non_empty_snapshot_accounts_do_not_update_ledger_or_observations(self):
        conn = self.connection()
        conn.execute("delete from upstream_rate_records")
        conn.execute(
            """
            insert into upstream_rate_records (
              category, kind, site, fluter_account_name, upstream_group,
              page_rate, recharge_factor, site_account_multiplier, status, note, updated_at
            ) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                "Kingdom",
                "Codex",
                "api.tokenskingdom.com",
                "kingdom codex plus2号 仅文字1.2*0.078=0.0936",
                "Plus 2号池",
                0.93,
                0.07444,
                0.0936,
                "需核对/倍率漂移",
                "",
                "old",
            ),
        )

        updates = self.mod.apply_browser_accounts_to_ledger(
            conn,
            [
                {
                    "provider": "Kingdom",
                    "site": "api.tokenskingdom.com",
                    "observed_at": "2026-06-12T19:59:00+00:00",
                    "detail": (
                        "latest read was empty; preserved previous non-empty snapshot "
                        "from 2026-06-12T18:00:00+00:00; script=0.1.15"
                    ),
                    "detected_accounts": [
                        {
                            "account_name": "kingdom codex plus2号 仅文字1.2*0.078=0.0936",
                            "upstream_group": "Plus 2号池",
                            "page_rate": 1.2,
                            "source_line": "kingdom codex plus2号 仅文字1.2*0.078=0.0936 sk-aaa...bbbb Plus 2号池 1.2x",
                        }
                    ],
                }
            ],
            "2026-06-12T20:00:00+00:00",
        )

        self.assertEqual([], updates)
        row = conn.execute("select page_rate, status, updated_at from upstream_rate_records").fetchone()
        self.assertAlmostEqual(0.93, row["page_rate"])
        self.assertEqual("需核对/倍率漂移", row["status"])
        self.assertEqual("old", row["updated_at"])
        count = conn.execute("select count(*) from browser_adapter_account_observations").fetchone()[0]
        self.assertEqual(0, count)

    def test_partial_account_snapshot_does_not_update_ledger_or_observations(self):
        conn = self.connection()
        conn.execute("delete from upstream_rate_records")
        conn.execute(
            """
            insert into upstream_rate_records (
              category, kind, site, fluter_account_name, upstream_group,
              page_rate, recharge_factor, site_account_multiplier, status, note, updated_at
            ) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                "钧澈",
                "Codex",
                "vip.lcodex.cn",
                "钧澈 codex 对接倍率仅文字0.09*0.93=0.0819",
                "对接倍率",
                0.05,
                0.925926,
                0.0819,
                "需核对/倍率漂移",
                "",
                "old",
            ),
        )

        updates = self.mod.apply_browser_accounts_to_ledger(
            conn,
            [
                {
                    "provider": "钧澈",
                    "site": "vip.lcodex.cn",
                    "observed_at": "2026-06-12T19:59:00+00:00",
                    "detail": "Chrome Tampermonkey read-only snapshot; account_lines=1; script=0.1.15; wait_state=stable; partial account snapshot 1/7 compared with previous 2026-06-12T19:00:00+00:00",
                    "detected_accounts": [
                        {
                            "account_name": "钧澈 codex 对接倍率仅文字0.09*0.93=0.0819",
                            "upstream_group": "对接倍率",
                            "page_rate": 0.09,
                            "source_line": "钧澈 codex 对接倍率仅文字0.09*0.93=0.0819 sk-aaa...bbbb 对接倍率 0.09x",
                        }
                    ],
                }
            ],
            "2026-06-12T20:00:00+00:00",
        )

        self.assertEqual([], updates)
        row = conn.execute(
            "select page_rate, status, updated_at from upstream_rate_records"
        ).fetchone()
        self.assertAlmostEqual(0.05, row["page_rate"])
        self.assertEqual("需核对/倍率漂移", row["status"])
        self.assertEqual("old", row["updated_at"])
        count = conn.execute("select count(*) from browser_adapter_account_observations").fetchone()[0]
        self.assertEqual(0, count)

    def test_extraction_loss_account_snapshot_does_not_update_ledger_or_observations(self):
        conn = self.connection()
        conn.execute("delete from upstream_rate_records")
        conn.execute(
            """
            insert into upstream_rate_records (
              category, kind, site, fluter_account_name, upstream_group,
              page_rate, recharge_factor, site_account_multiplier, status, note, updated_at
            ) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                "钧澈",
                "Codex",
                "vip.lcodex.cn",
                "钧澈 codex 对接倍率仅文字0.06*0.93=0.0546",
                "对接倍率",
                0.05,
                0.925926,
                0.0546,
                "需核对/倍率漂移",
                "",
                "old",
            ),
        )
        accounts = [
            {
                "account_name": f"钧澈 codex 对接倍率仅文字0.0{i}",
                "upstream_group": "对接倍率",
                "page_rate": 0.06,
                "source_line": f"钧澈 codex 对接倍率仅文字0.0{i} sk-aaa...bbbb 对接倍率 0.06x",
            }
            for i in range(7)
        ]

        updates = self.mod.apply_browser_accounts_to_ledger(
            conn,
            [
                {
                    "provider": "钧澈",
                    "site": "vip.lcodex.cn",
                    "observed_at": "2026-06-12T19:59:00+00:00",
                    "detail": "Chrome Tampermonkey read-only snapshot; account_lines=22; rate_lines=1; script=0.1.15; wait_state=stable",
                    "detected_accounts": accounts,
                }
            ],
            "2026-06-12T20:00:00+00:00",
        )

        self.assertEqual([], updates)
        row = conn.execute(
            "select page_rate, status, updated_at from upstream_rate_records"
        ).fetchone()
        self.assertAlmostEqual(0.05, row["page_rate"])
        self.assertEqual("需核对/倍率漂移", row["status"])
        self.assertEqual("old", row["updated_at"])
        count = conn.execute("select count(*) from browser_adapter_account_observations").fetchone()[0]
        self.assertEqual(0, count)

    def test_stale_self_confirming_account_name_observation_does_not_update_ledger(self):
        conn = self.connection()
        conn.execute("delete from upstream_rate_records")
        conn.execute(
            """
            insert into upstream_rate_records (
              category, kind, site, fluter_account_name, upstream_group,
              page_rate, recharge_factor, site_account_multiplier, status, note, updated_at
            ) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                "Meow",
                "Claude",
                "api.saki.lat",
                "meow claude ccmax仅客户端 0.75",
                "CC Max 限制客户端 无上限并发",
                1.1,
                1,
                0.75,
                "需核对/倍率漂移",
                "",
                "old",
            ),
        )

        updates = self.mod.apply_browser_accounts_to_ledger(
            conn,
            [
                {
                    "provider": "Meow",
                    "site": "api.saki.lat",
                    "observed_at": "2026-06-12T02:00:00+00:00",
                    "detected_accounts": [
                        {
                            "account_name": "meow claude ccmax仅客户端 0.75",
                            "upstream_group": "CC Max 限制客户端 无上限并发",
                            "page_rate": 0.75,
                            "source_line": (
                                "meow claude ccmax仅客户端 0.75 sk-aaa...bbbb "
                                "CC Max 限制客户端 无上限并发 0.75x 选择分组"
                            ),
                        }
                    ],
                }
            ],
            "2026-06-13T01:59:00+00:00",
        )

        self.assertEqual([], updates)
        row = conn.execute(
            "select page_rate, status, actual_cost_label, updated_at from upstream_rate_records"
        ).fetchone()
        self.assertAlmostEqual(1.1, row["page_rate"])
        self.assertEqual("需核对/倍率漂移", row["status"])
        self.assertEqual("", row["actual_cost_label"])
        self.assertEqual("old", row["updated_at"])
        count = conn.execute("select count(*) from browser_adapter_account_observations").fetchone()[0]
        self.assertEqual(0, count)

    def test_stale_ambiguous_account_name_observation_does_not_update_ledger(self):
        conn = self.connection()
        conn.execute("delete from upstream_rate_records")
        conn.execute(
            """
            insert into upstream_rate_records (
              category, kind, site, fluter_account_name, upstream_group,
              page_rate, recharge_factor, site_account_multiplier, status, note, updated_at
            ) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                "Meow",
                "Codex",
                "api.saki.lat",
                "meow codex pro 0.1 不限客户端2",
                "Pro",
                0.2,
                1,
                0.1,
                "需核对/倍率漂移",
                "",
                "old",
            ),
        )

        updates = self.mod.apply_browser_accounts_to_ledger(
            conn,
            [
                {
                    "provider": "Meow",
                    "site": "api.saki.lat",
                    "observed_at": "2026-06-12T02:00:00+00:00",
                    "detected_accounts": [
                        {
                            "account_name": "meow codex pro 0.1 不限客户端2",
                            "upstream_group": "Pro",
                            "page_rate": 0.1,
                            "source_line": (
                                "meow codex pro 0.1 不限客户端2 sk-aaa...bbbb "
                                "Pro 0.1x 选择分组"
                            ),
                        }
                    ],
                }
            ],
            "2026-06-13T01:59:00+00:00",
        )

        self.assertEqual([], updates)
        row = conn.execute("select page_rate, actual_cost_label, updated_at from upstream_rate_records").fetchone()
        self.assertAlmostEqual(0.2, row["page_rate"])
        self.assertEqual("", row["actual_cost_label"])
        self.assertEqual("old", row["updated_at"])

    def test_snapshot_preserves_detected_accounts_json(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            db_path = str(Path(tmpdir) / "ledger.sqlite")
            self.mod.write_observations(
                db_path,
                [
                    {
                        "provider": "Magic",
                        "site": "pool.gptstore.club",
                        "browser": "Chrome",
                        "status": "browser_observed",
                        "detail": "account_lines=1; rate_lines=1",
                        "observed_at": "2026-06-12T20:00:00+00:00",
                        "page_url": "https://pool.gptstore.club/token",
                        "page_title": "Magic token",
                        "detected_balance": "",
                        "detected_accounts": [
                            {
                                "account_name": "magic codex pro 仅文字0.08",
                                "upstream_group": "Pro号池",
                                "page_rate": 0.08,
                                "source_line": "magic codex pro 仅文字0.08 / Pro号池 / 0.08x",
                            }
                        ],
                        "detected_rates": ["magic codex pro 仅文字0.08 / Pro号池 / 0.08x"],
                        "sanitized_excerpt": "magic codex pro 仅文字0.08 / Pro号池 / 0.08x",
                    }
                ],
            )
            conn = sqlite3.connect(db_path)
            conn.row_factory = sqlite3.Row
            row = conn.execute(
                """
                select detected_accounts_json
                from browser_adapter_snapshots
                where site = 'pool.gptstore.club'
                """
            ).fetchone()
            conn.close()

        accounts = json.loads(row["detected_accounts_json"])
        self.assertEqual("magic codex pro 仅文字0.08", accounts[0]["account_name"])

    def test_empty_safari_read_does_not_overwrite_fresh_chrome_account_snapshot(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            db_path = str(Path(tmpdir) / "ledger.sqlite")
            self.mod.write_observations(
                db_path,
                [
                    {
                        "provider": "Kingdom",
                        "site": "api.tokenskingdom.com",
                        "browser": "Chrome",
                        "status": "browser_observed",
                        "detail": "Chrome Tampermonkey read-only snapshot; balance=yes; account_lines=1; rate_lines=1; script=0.1.15",
                        "observed_at": "2026-06-12T20:00:00+00:00",
                        "page_url": "https://tokenskingdom.com/keys",
                        "page_title": "API 密钥",
                        "detected_balance": "余额 $587.05",
                        "detected_accounts": [
                            {
                                "account_name": "kingdom current account 1.2",
                                "upstream_group": "Plus 2号池",
                                "page_rate": 1.2,
                                "source_line": "kingdom current account 1.2 sk-abc...def Plus 2号池 1.2x",
                            }
                        ],
                        "detected_rates": ["kingdom current account 1.2 sk-abc...def Plus 2号池 1.2x"],
                        "sanitized_excerpt": "current chrome snapshot",
                    }
                ],
            )
            self.mod.write_observations(
                db_path,
                [
                    {
                        "provider": "Kingdom",
                        "site": "api.tokenskingdom.com",
                        "browser": "Safari",
                        "status": "browser_observed",
                        "detail": "latest read was empty; preserved previous non-empty snapshot from 2026-06-12T20:00:00+00:00; no open logged-in tab for api.tokenskingdom.com",
                        "observed_at": "2026-06-12T20:05:00+00:00",
                        "page_url": "https://tokenskingdom.com/keys",
                        "page_title": "API 密钥",
                        "detected_balance": "",
                        "detected_accounts": [],
                        "detected_rates": [],
                        "sanitized_excerpt": "Safari empty fallback",
                    }
                ],
            )
            conn = sqlite3.connect(db_path)
            conn.row_factory = sqlite3.Row
            snapshot = conn.execute(
                """
                select browser, page_title, detected_balance, detected_accounts_json, observed_at
                from browser_adapter_snapshots
                where provider = 'Kingdom' and site = 'api.tokenskingdom.com'
                """
            ).fetchone()
            status = conn.execute(
                """
                select browser, detail, observed_at
                from browser_adapter_status
                where provider = 'Kingdom' and site = 'api.tokenskingdom.com'
                """
            ).fetchone()
            conn.close()

        self.assertEqual("Chrome", snapshot["browser"])
        self.assertEqual("API 密钥", snapshot["page_title"])
        self.assertEqual("余额 $587.05", snapshot["detected_balance"])
        self.assertEqual(
            "Chrome Tampermonkey read-only snapshot; balance=yes; account_lines=1; rate_lines=1; script=0.1.15",
            status["detail"],
        )
        self.assertEqual("Chrome", status["browser"])
        self.assertEqual("2026-06-12T20:00:00+00:00", status["observed_at"])

    def test_single_site_import_does_not_delete_other_provider_snapshots(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            db_path = str(Path(tmpdir) / "ledger.sqlite")
            self.mod.write_observations(
                db_path,
                [
                    {
                        "provider": "Magic",
                        "site": "pool.gptstore.club",
                        "browser": "Chrome",
                        "status": "browser_observed",
                        "detail": "Chrome Tampermonkey read-only snapshot; account_lines=1; rate_lines=1; script=0.1.15",
                        "observed_at": "2026-06-12T20:00:00+00:00",
                        "page_url": "https://pool.gptstore.club/keys",
                        "page_title": "API 密钥",
                        "detected_balance": "余额 $28.90",
                        "detected_accounts": [
                            {
                                "account_name": "magic codex pro 仅文字0.08",
                                "upstream_group": "Pro号池",
                                "page_rate": 0.08,
                                "source_line": "magic codex pro 仅文字0.08 sk-abc...def Pro号池 0.08x",
                            }
                        ],
                        "detected_rates": ["magic codex pro 仅文字0.08 sk-abc...def Pro号池 0.08x"],
                        "sanitized_excerpt": "magic key page",
                    },
                    {
                        "provider": "Kingdom",
                        "site": "api.tokenskingdom.com",
                        "browser": "Chrome",
                        "status": "browser_observed",
                        "detail": "Chrome Tampermonkey read-only snapshot; account_lines=1; rate_lines=1; script=0.1.15",
                        "observed_at": "2026-06-12T20:00:00+00:00",
                        "page_url": "https://tokenskingdom.com/keys",
                        "page_title": "API 密钥",
                        "detected_balance": "余额 $587.05",
                        "detected_accounts": [
                            {
                                "account_name": "kingdom current account 1.2",
                                "upstream_group": "Plus 2号池",
                                "page_rate": 1.2,
                                "source_line": "kingdom current account 1.2 sk-abc...def Plus 2号池 1.2x",
                            }
                        ],
                        "detected_rates": ["kingdom current account 1.2 sk-abc...def Plus 2号池 1.2x"],
                        "sanitized_excerpt": "kingdom key page",
                    },
                ],
            )
            self.mod.write_observations(
                db_path,
                [
                    {
                        "provider": "Kingdom",
                        "site": "api.tokenskingdom.com",
                        "browser": "Chrome",
                        "status": "browser_observed",
                        "detail": "Chrome Tampermonkey read-only snapshot; account_lines=1; rate_lines=1; script=0.1.15",
                        "observed_at": "2026-06-12T20:05:00+00:00",
                        "page_url": "https://tokenskingdom.com/keys",
                        "page_title": "API 密钥",
                        "detected_balance": "余额 $590.00",
                        "detected_accounts": [
                            {
                                "account_name": "kingdom current account 1.2",
                                "upstream_group": "Plus 2号池",
                                "page_rate": 1.2,
                                "source_line": "kingdom current account 1.2 sk-abc...def Plus 2号池 1.2x",
                            }
                        ],
                        "detected_rates": ["kingdom current account 1.2 sk-abc...def Plus 2号池 1.2x"],
                        "sanitized_excerpt": "kingdom key page refreshed",
                    }
                ],
            )
            conn = sqlite3.connect(db_path)
            conn.row_factory = sqlite3.Row
            rows = conn.execute(
                """
                select provider, site, detected_balance
                from browser_adapter_snapshots
                order by provider, site
                """
            ).fetchall()
            conn.close()

        self.assertEqual(
            [
                ("Kingdom", "api.tokenskingdom.com", "余额 $590.00"),
                ("Magic", "pool.gptstore.club", "余额 $28.90"),
            ],
            [(row["provider"], row["site"], row["detected_balance"]) for row in rows],
        )

    def test_collector_panel_accounts_are_ignored_when_importing_snapshot(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            db_path = str(Path(tmpdir) / "ledger.sqlite")
            self.mod.write_observations(
                db_path,
                [
                    {
                        "provider": "Magic",
                        "site": "pool.gptstore.club",
                        "browser": "Chrome",
                        "status": "browser_observed",
                        "detail": "Chrome Tampermonkey read-only snapshot",
                        "observed_at": "2026-06-12T20:00:00+00:00",
                        "page_url": "https://pool.gptstore.club/keys",
                        "page_title": "Magic keys",
                        "detected_balance": "本页识别：余额 有，账号行 5，倍率行 0",
                        "detected_accounts": [
                            {
                                "account_name": "脚本：0.1.7；token：已配置；collector：collector 可用",
                                "source_line": "脚本：0.1.7；collector：collector 可用 / 自动发送快照",
                            }
                        ],
                        "detected_rates": [],
                        "sanitized_excerpt": "Fluter 上游采集 / 自动发送快照",
                    }
                ],
            )
            conn = sqlite3.connect(db_path)
            conn.row_factory = sqlite3.Row
            row = conn.execute(
                """
                select detected_accounts_json
                from browser_adapter_snapshots
                where site = 'pool.gptstore.club'
                """
            ).fetchone()
            conn.close()

        self.assertEqual([], json.loads(row["detected_accounts_json"]))

    def test_source_line_repairs_magic_account_name_and_rate(self):
        cleaned = self.mod.clean_detected_account(
            {
                "account_name": "ClaudeCode Max201x选择分组",
                "upstream_group": "",
                "page_rate": None,
                "source_line": (
                    "magic claude ccmax 1 sk-f64...761f ClaudeCode Max20 1x 选择分组 "
                    "今日: $0.0000 近30天: $0.0000"
                ),
            }
        )

        self.assertIsNotNone(cleaned)
        self.assertEqual("magic claude ccmax 1", cleaned["account_name"])
        self.assertEqual("ClaudeCode Max20", cleaned["upstream_group"])
        self.assertEqual(1.0, cleaned["page_rate"])

    def test_clean_detected_account_repairs_kingdom_first_row_typo(self):
        cleaned = self.mod.clean_detected_account(
            {
                "site": "api.tokenskingdom.com",
                "account_name": "ingdom codex 超级特惠 仅文字0.95*0.078=0.0741",
                "upstream_group": "plus 1号池",
                "page_rate": 1,
                "source_line": (
                    "ingdom codex 超级特惠 仅文字0.95*0.078=0.0741 "
                    "sk-1d2...b582 plus 1号池 1x 选择分组"
                ),
            }
        )

        self.assertIsNotNone(cleaned)
        self.assertEqual(
            "kingdom codex 超级特惠 仅文字0.95*0.078=0.0741",
            cleaned["account_name"],
        )

    def test_source_line_ignores_post_select_group_duplicate_rate(self):
        cleaned = self.mod.clean_detected_account(
            {
                "account_name": "ClaudeCode Max201x选择分组",
                "upstream_group": "",
                "page_rate": None,
                "source_line": (
                    "magic claude ccmax 1 sk-f64...761f ClaudeCode Max20 1x 选择分组 "
                    "今日: $0.0000 近30天: $0.0000 / 复制到剪贴板 / 点击更换分组 / "
                    "ClaudeCode Max201x选择分组 / 使用密钥 / 禁用 / 编辑 / 删除"
                ),
            }
        )

        self.assertIsNotNone(cleaned)
        self.assertEqual("magic claude ccmax 1", cleaned["account_name"])
        self.assertEqual("ClaudeCode Max20", cleaned["upstream_group"])
        self.assertEqual(1.0, cleaned["page_rate"])

    def test_rate_line_ignores_post_select_group_summary_rate(self):
        parsed = self.mod.parse_browser_rate_line(
            "magic claude ccmax 1 sk-f64...761f ClaudeCode Max20 1x 选择分组 "
            "今日: $0.0000 / 点击更换分组 / ClaudeCode Max201x选择分组"
        )

        self.assertEqual(("magic claude ccmax 1 sk-f64...761f ClaudeCode Max20", 1.0), parsed)

    def test_rate_line_rejects_whole_page_summary_noise(self):
        parsed = self.mod.parse_browser_rate_line(
            "Tokens 王国仪表盘API 密钥使用记录渠道状态我的订阅兑换邀请返利个人资料"
            "API 密钥管理您的 API 密钥和访问令牌 创建密钥全部分组全部状态"
            "API 端点默认 https://api.tokenskingdom.com/v1 OpenAI 兼容接口"
        )

        self.assertIsNone(parsed)

    def test_source_line_preserves_meow_image_account_name_with_slashes(self):
        cleaned = self.mod.clean_detected_account(
            {
                "account_name": "meow codex 文字0.05 生图(可原可桥) 1",
                "upstream_group": "",
                "page_rate": None,
                "source_line": (
                    "meow codex 文字0.05 生图(可原可桥) 1/2/4k 5分 "
                    "sk-907...58f9 Image-2 无限制客户端 无上限并发 0.05x 选择分组"
                ),
            }
        )

        self.assertIsNotNone(cleaned)
        self.assertEqual("meow codex 文字0.05 生图(可原可桥) 1/2/4k 5分", cleaned["account_name"])
        self.assertEqual("Image-2 无限制客户端 无上限并发", cleaned["upstream_group"])
        self.assertEqual(0.05, cleaned["page_rate"])

    def test_source_line_repairs_mouubox_short_group_rate(self):
        cleaned = self.mod.clean_detected_account(
            {
                "account_name": "超超(主站) codex 0.03",
                "upstream_group": "gpt0.",
                "page_rate": 6.0,
                "source_line": (
                    "超超(主站) codex 0.03 sk-442...0130 gpt 0.06x 选择分组 "
                    "今日: $9.1932 近30天: $32.3659"
                ),
            }
        )

        self.assertIsNotNone(cleaned)
        self.assertEqual("超超(主站) codex 0.03", cleaned["account_name"])
        self.assertEqual("gpt", cleaned["upstream_group"])
        self.assertEqual(0.06, cleaned["page_rate"])

    def test_source_line_trims_junche_status_prefix_from_group(self):
        cleaned = self.mod.clean_detected_account(
            {
                "account_name": "钧澈 codex team狂欢 仅文字0.002",
                "upstream_group": "钧澈 codex team狂欢 仅文字0.002 已启用 无限额度 team狂欢",
                "page_rate": 0.002,
                "source_line": (
                    "钧澈 codex team狂欢 仅文字0.002 已启用 无限额度 "
                    "team狂欢 0.002x 无限制 sk-rBg8**********grKe"
                ),
            }
        )

        self.assertIsNotNone(cleaned)
        self.assertEqual("钧澈 codex team狂欢 仅文字0.002", cleaned["account_name"])
        self.assertEqual("team狂欢", cleaned["upstream_group"])
        self.assertEqual(0.002, cleaned["page_rate"])

    def test_quota_only_junche_row_is_not_treated_as_account_observation(self):
        cleaned = self.mod.clean_detected_account(
            {
                "account_name": "非自用",
                "upstream_group": "¥585.03 GPT-PRO纯享号池",
                "page_rate": 0.15,
                "source_line": (
                    "非自用 已启用 ¥227.52 / ¥585.03 GPT-PRO纯享号池 0.15x "
                    "无限制 无限制 2026-06-13 20:44:49 永不过期 聊天 禁用 编辑 删除 "
                    "/ Select this row / on / 非自用 / Tag: 已启用 / 已启用 / ¥227.52 / ¥585.03 "
                    "/ quota usage / Tag: GPT-PRO纯享号池 / GPT-PRO纯享号池 / 0.15x / sk-YQuI**********UhHU"
                ),
            }
        )

        self.assertIsNone(cleaned)

    def test_congmingai_provider_is_observed_from_logged_in_tab(self):
        now = "2026-06-13T20:00:00+00:00"
        provider = next(provider for provider in self.mod.PROVIDERS if provider.site == "sub2.congmingai.com")
        row = self.mod.observation_for_provider(
            provider,
            [
                {
                    "url": "https://sub2.congmingai.com/keys",
                    "title": "聪明AI - API Keys",
                    "text": "聪明AI API 密钥管理 当前余额 ¥12.34 codex 0.05x",
                }
            ],
            "Safari",
            now,
        )

        self.assertEqual("聪明AI", row["provider"])
        self.assertEqual("sub2.congmingai.com", row["site"])
        self.assertEqual("browser_observed", row["status"])
        self.assertEqual("当前余额 ¥12.34", row["detected_balance"])
        self.assertEqual(now, row["observed_at"])

    def test_qiaoran_provider_is_observed_from_logged_in_tab(self):
        now = "2026-06-13T20:00:00+00:00"
        provider = next(provider for provider in self.mod.PROVIDERS if provider.site == "mdkj.lol")
        row = self.mod.observation_for_provider(
            provider,
            [
                {
                    "url": "https://mdkj.lol/keys",
                    "title": "乔燃 - API Keys",
                    "text": "乔燃 API 密钥管理 当前余额 ¥56.78 codex 0.05x",
                }
            ],
            "Safari",
            now,
        )

        self.assertEqual("乔燃", row["provider"])
        self.assertEqual("mdkj.lol", row["site"])
        self.assertEqual("browser_observed", row["status"])
        self.assertEqual("当前余额 ¥56.78", row["detected_balance"])
        self.assertEqual(now, row["observed_at"])

    def test_parse_group_and_rate_accepts_multiply_sign_times_and_uppercase_x(self):
        self.assertEqual(("分组", 0.06), self.mod.parse_group_and_rate("分组 ×0.06"))
        self.assertEqual(("分组", 0.06), self.mod.parse_group_and_rate("分组 0.06倍"))
        self.assertEqual(("分组", 0.06), self.mod.parse_group_and_rate("分组 0.06X"))

    def test_filters_quota_status_as_account_name(self):
        self.assertTrue(self.mod.is_noise_account_name("非自用 已启用 ¥161.28"))


if __name__ == "__main__":
    unittest.main()
