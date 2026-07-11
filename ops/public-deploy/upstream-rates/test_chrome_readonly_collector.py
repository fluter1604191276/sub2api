#!/usr/bin/env python3
from __future__ import annotations

import importlib.util
import http.client
import sys
import tempfile
import unittest
from datetime import timedelta
from pathlib import Path
from threading import Thread
from unittest import mock


SCRIPT = Path(__file__).with_name("chrome_readonly_collector.py")


def load_module():
    spec = importlib.util.spec_from_file_location("chrome_readonly_collector", SCRIPT)
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class ChromeReadonlyCollectorTest(unittest.TestCase):
    def setUp(self) -> None:
        self.mod = load_module()

    def test_balance_detection_does_not_treat_group_multiplier_as_balance(self):
        text = """
        名称 状态 剩余额度/总额度 分组 密钥 可用模型
        无限额度
        对接倍率
        0.05x
        无限制
        """

        self.assertEqual("", self.mod.detect_balance(text))

    def test_key_quota_table_is_not_wallet_balance(self):
        text = (
            "令牌管理 名称 状态 剩余额度/总额度 分组 密钥 可用模型 "
            "非自用 已启用 ¥154.41 / ¥200.00 GPT-PRO纯享号池 0.18x"
        )

        self.assertEqual(
            "",
            self.mod.detect_balance(text, page_url="https://vip.lcodex.cn/console/token"),
        )

    def test_pricing_page_price_is_not_balance(self):
        text = """
        可用令牌分组
        模型名称 计费类型 模型价格
        claude-opus 按次 模型价格 ¥0.080 / 次
        """

        self.assertEqual("", self.mod.detect_balance(text, page_url="https://xn--vduyey89e.com/pricing"))

    def test_normal_balance_page_still_detects_balance(self):
        self.assertEqual(
            "账户余额 $16.25",
            self.mod.detect_balance("账户余额 $16.25", page_url="https://xn--vduyey89e.com/console"),
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

    def test_raw_long_balance_field_is_not_preserved_as_fallback(self):
        row = self.mod.normalize_observation(
            {
                "provider": "钧澈",
                "site": "vip.lcodex.cn",
                "url": "https://vip.lcodex.cn/console",
                "detected_balance": (
                    "👋晚上好，fluter账户数据当前余额充值历史消耗使用统计请求次数统计次数"
                    "资源消耗统计额度统计Tokens性能指标平均RPM平均TPM模型数据分析"
                    "API信息暂无API信息请联系管理员在系统设置中配置"
                ),
                "text": "令牌管理 名称 状态 剩余额度/总额度 分组 密钥 可用模型",
            }
        )

        self.assertIsNotNone(row)
        self.assertEqual("", row["detected_balance"])

    def test_short_money_balance_field_is_still_preserved(self):
        row = self.mod.normalize_observation(
            {
                "provider": "Magic",
                "site": "pool.gptstore.club",
                "url": "https://pool.gptstore.club/dashboard",
                "detected_balance": "$39.25",
                "text": "API token dashboard",
            }
        )

        self.assertIsNotNone(row)
        self.assertEqual("$39.25", row["detected_balance"])

    def test_amount_from_line_ignores_plain_multiplier(self):
        self.assertEqual("", self.mod.amount_from_line("对接倍率 0.05x 无限制"))
        self.assertEqual("$16.25", self.mod.amount_from_line("账户余额 $16.25"))

    def test_provider_for_command_target_accepts_name_site_and_url(self):
        self.assertEqual("KBQ", self.mod.provider_for_command_target("KBQ").name)
        self.assertEqual("KBQ", self.mod.provider_for_command_target("xn--vduyey89e.com").name)
        self.assertEqual("Magic", self.mod.provider_for_command_target("https://pool.gptstore.club/token").name)
        self.assertEqual("聪明AI", self.mod.provider_for_command_target("sub2.congmingai.com").name)
        self.assertEqual("聪明AI", self.mod.provider_for_command_target("https://sub2.congmingai.com/keys").name)
        self.assertEqual("乔燃", self.mod.provider_for_command_target("mdkj.lol").name)
        self.assertEqual("乔燃", self.mod.provider_for_command_target("https://mdkj.lol/keys").name)
        self.assertIsNone(self.mod.provider_for_command_target("example.com"))

    def test_request_source_allows_tampermonkey_source_header(self):
        self.assertTrue(
            self.mod.request_source_allowed(
                {
                    "X-Collector-Source": "api.mouubox.com",
                }
            )
        )
        self.assertTrue(
            self.mod.request_source_allowed(
                {
                    "X-Collector-Source": "sub2.congmingai.com",
                }
            )
        )
        self.assertTrue(
            self.mod.request_source_allowed(
                {
                    "X-Collector-Source": "mdkj.lol",
                }
            )
        )
        self.assertFalse(
            self.mod.request_source_allowed(
                {
                    "X-Collector-Source": "evil.example.com",
                }
            )
        )

    def test_request_source_rejects_bad_origin_even_with_good_source_header(self):
        self.assertFalse(
            self.mod.request_source_allowed(
                {
                    "Origin": "https://evil.example.com",
                    "X-Collector-Source": "api.mouubox.com",
                }
            )
        )

    def test_scrub_sensitive_environment_removes_only_secret_like_names(self):
        env = {
            "PATH": "/usr/bin",
            "SSH_AUTH_SOCK": "/tmp/ssh.sock",
            "MIMO_API_KEY": "should-go",
            "MY_TOKEN": "should-go",
            "APP_SECRET": "should-go",
            "COOKIE": "should-go",
            "Authorization": "should-go",
        }

        removed = self.mod.scrub_sensitive_environment(env)

        self.assertIn("MIMO_API_KEY", removed)
        self.assertIn("MY_TOKEN", removed)
        self.assertIn("APP_SECRET", removed)
        self.assertIn("COOKIE", removed)
        self.assertIn("Authorization", removed)
        self.assertEqual("/usr/bin", env["PATH"])
        self.assertEqual("/tmp/ssh.sock", env["SSH_AUTH_SOCK"])
        self.assertNotIn("MIMO_API_KEY", env)
        self.assertNotIn("MY_TOKEN", env)
        self.assertNotIn("APP_SECRET", env)
        self.assertNotIn("COOKIE", env)
        self.assertNotIn("Authorization", env)

    def test_normalizes_congmingai_observation(self):
        row = self.mod.normalize_observation(
            {
                "provider": "聪明AI",
                "site": "sub2.congmingai.com",
                "url": "https://sub2.congmingai.com/keys",
                "text": "聪明AI API 密钥管理 当前余额 ¥12.34 codex 0.05x",
                "detected_accounts": [
                    {
                        "account_name": "聪明AI codex 0.05",
                        "upstream_group": "codex",
                        "page_rate": 0.05,
                        "source_line": "聪明AI codex 0.05 / codex / 0.05x / sk-12345678...redacted",
                    }
                ],
            }
        )

        self.assertIsNotNone(row)
        self.assertEqual("聪明AI", row["provider"])
        self.assertEqual("sub2.congmingai.com", row["site"])
        self.assertEqual("当前余额 ¥12.34", row["detected_balance"])
        self.assertEqual("聪明AI codex 0.05", row["detected_accounts"][0]["account_name"])

    def test_command_lifecycle_claim_and_ack(self):
        with tempfile.TemporaryDirectory() as tmp:
            command_dir = Path(tmp)
            command = self.mod.create_command(
                command_dir,
                "KBQ",
                action="refresh_then_send",
                ttl_seconds=60,
                reason="unit test",
            )

            self.assertEqual("KBQ", command["provider"])
            self.assertEqual("pending", command["status"])

            claimed = self.mod.claim_commands(command_dir, "xn--vduyey89e.com")
            self.assertEqual(1, len(claimed))
            self.assertEqual(command["id"], claimed[0]["id"])
            self.assertEqual("refresh_then_send", claimed[0]["action"])

            self.assertEqual([], self.mod.claim_commands(command_dir, "xn--vduyey89e.com"))
            self.assertTrue(self.mod.acknowledge_command(command_dir, command["id"], status="done", detail="ok"))
            stored = self.mod.load_commands(command_dir)
            self.assertEqual("done", stored[0]["status"])
            self.assertEqual("ok", stored[0]["detail"])

    def test_prune_snapshots_keeps_latest_and_non_snapshot_files(self):
        with tempfile.TemporaryDirectory() as tmp:
            snapshot_dir = Path(tmp)
            for index in range(5):
                path = snapshot_dir / f"snapshot-20260615-12000{index}-000000Z.json"
                path.write_text("[]", encoding="utf-8")
                path.touch()
            (snapshot_dir / "latest.json").write_text("[]", encoding="utf-8")
            (snapshot_dir / "notes.json").write_text("{}", encoding="utf-8")
            command_dir = snapshot_dir / "commands"
            command_dir.mkdir()
            (command_dir / "commands.json").write_text("[]", encoding="utf-8")

            self.mod.prune_snapshots(snapshot_dir, keep=2)

            remaining = sorted(path.name for path in snapshot_dir.glob("snapshot-*.json"))
            self.assertEqual(
                [
                    "snapshot-20260615-120003-000000Z.json",
                    "snapshot-20260615-120004-000000Z.json",
                ],
                remaining,
            )
            self.assertTrue((snapshot_dir / "latest.json").exists())
            self.assertTrue((snapshot_dir / "notes.json").exists())
            self.assertTrue((command_dir / "commands.json").exists())

    def test_write_snapshots_prunes_archive_after_new_snapshot(self):
        with tempfile.TemporaryDirectory() as tmp:
            snapshot_dir = Path(tmp)
            for index in range(3):
                (snapshot_dir / f"snapshot-20260615-12000{index}-000000Z.json").write_text("[]", encoding="utf-8")

            with mock.patch.object(self.mod, "MAX_STORED_SNAPSHOTS", 2):
                latest_path, count = self.mod.write_snapshots(
                    snapshot_dir,
                    [
                        self.mod.normalize_observation(
                            {
                                "provider": "KBQ",
                                "site": "xn--vduyey89e.com",
                                "url": "https://xn--vduyey89e.com/pricing",
                                "text": "账户余额 ¥16.25",
                            }
                        )
                    ],
                )

            self.assertEqual(snapshot_dir / "latest.json", latest_path)
            self.assertEqual(1, count)
            self.assertEqual(2, len(list(snapshot_dir.glob("snapshot-*.json"))))
            self.assertTrue(latest_path.exists())

    def test_expired_command_is_not_claimed(self):
        with tempfile.TemporaryDirectory() as tmp:
            command_dir = Path(tmp)
            command = self.mod.create_command(command_dir, "Magic", ttl_seconds=60)
            command["expires_at"] = self.mod.isoformat_utc(self.mod.now_utc() - timedelta(seconds=1))
            self.mod.save_commands(command_dir, [command])

            self.assertEqual([], self.mod.claim_commands(command_dir, "pool.gptstore.club"))
            stored = self.mod.load_commands(command_dir)
            self.assertEqual("expired", stored[0]["status"])

    def test_create_command_rejects_unknown_provider(self):
        with tempfile.TemporaryDirectory() as tmp:
            with self.assertRaises(ValueError):
                self.mod.create_command(Path(tmp), "not-a-provider")

    def test_userscript_endpoint_supports_get_and_head(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            token_file = root / "token"
            token_file.write_text("test-token\n", encoding="utf-8")
            userscript = root / "collector.user.js"
            userscript.write_text("// ==UserScript==\n// @version 9.9.9\n// ==/UserScript==\n", encoding="utf-8")
            server = self.mod.CollectorServer(
                ("127.0.0.1", 0),
                self.mod.CollectorHandler,
                token="test-token",
                snapshot_dir=root / "snapshots",
                command_dir=root / "commands",
                max_body_bytes=4096,
                userscript_path=userscript,
            )
            thread = Thread(target=server.serve_forever, daemon=True)
            thread.start()
            try:
                host, port = server.server_address
                conn = http.client.HTTPConnection(host, port, timeout=5)
                conn.request("HEAD", "/userscript/fluter-upstream-readonly-collector.user.js")
                response = conn.getresponse()
                self.assertEqual(200, response.status)
                self.assertEqual("0", str(len(response.read())))
                conn.close()

                conn = http.client.HTTPConnection(host, port, timeout=5)
                conn.request("GET", "/userscript")
                response = conn.getresponse()
                body = response.read().decode("utf-8")
                self.assertEqual(200, response.status)
                self.assertIn("@version 9.9.9", body)
                conn.close()

                conn = http.client.HTTPConnection(host, port, timeout=5)
                conn.request("GET", "/install")
                response = conn.getresponse()
                body = response.read().decode("utf-8")
                self.assertEqual(200, response.status)
                self.assertIn("安装 / 更新 userscript", body)
                conn.close()
            finally:
                server.shutdown()
                server.server_close()

    def test_normalizes_detected_account_rows(self):
        row = self.mod.normalize_observation(
            {
                "provider": "Magic",
                "site": "pool.gptstore.club",
                "url": "https://pool.gptstore.club/token",
                "text": "magic codex pro 仅文字0.08 / Pro号池 / 0.08x / sk-12345678901234567890",
                "detected_accounts": [
                    {
                        "account_name": "magic codex pro 仅文字0.08",
                        "upstream_group": "Pro号池",
                        "page_rate": 0.08,
                        "source_line": "magic codex pro 仅文字0.08 / Pro号池 / 0.08x / sk-12345678901234567890",
                    }
                ],
            }
        )

        self.assertIsNotNone(row)
        self.assertEqual(1, len(row["detected_accounts"]))
        account = row["detected_accounts"][0]
        self.assertEqual("magic codex pro 仅文字0.08", account["account_name"])
        self.assertEqual("Pro号池", account["upstream_group"])
        self.assertEqual(0.08, account["page_rate"])
        self.assertNotIn("12345678901234567890", account["source_line"])

    def test_junche_source_line_trims_status_prefix_from_group(self):
        row = self.mod.normalize_observation(
            {
                "provider": "钧澈",
                "site": "vip.lcodex.cn",
                "url": "https://vip.lcodex.cn/console/token",
                "detected_accounts": [
                    {
                        "account_name": "钧澈 codex team狂欢 仅文字0.002",
                        "upstream_group": "钧澈 codex team狂欢 仅文字0.002 已启用 无限额度 team狂欢",
                        "page_rate": 0.002,
                        "source_line": (
                            "钧澈 codex team狂欢 仅文字0.002 已启用 无限额度 "
                            "team狂欢 0.002x 无限制 sk-rBg8**********grKe"
                        ),
                    }
                ],
            }
        )

        self.assertIsNotNone(row)
        account = row["detected_accounts"][0]
        self.assertEqual("钧澈 codex team狂欢 仅文字0.002", account["account_name"])
        self.assertEqual("team狂欢", account["upstream_group"])
        self.assertEqual(0.002, account["page_rate"])

    def test_kingdom_first_row_account_name_typo_is_repaired(self):
        row = self.mod.normalize_observation(
            {
                "provider": "Kingdom",
                "site": "api.tokenskingdom.com",
                "url": "https://api.tokenskingdom.com/keys",
                "detected_accounts": [
                    {
                        "account_name": "ingdom codex 超级特惠 仅文字0.95*0.078=0.0741",
                        "upstream_group": "plus 1号池",
                        "page_rate": 1,
                        "source_line": (
                            "ingdom codex 超级特惠 仅文字0.95*0.078=0.0741 "
                            "sk-1d2...b582 plus 1号池 1x 选择分组"
                        ),
                    }
                ],
            }
        )

        self.assertIsNotNone(row)
        account = row["detected_accounts"][0]
        self.assertEqual(
            "kingdom codex 超级特惠 仅文字0.95*0.078=0.0741",
            account["account_name"],
        )

    def test_parse_group_and_rate_accepts_multiply_sign_times_and_uppercase_x(self):
        self.assertEqual(("分组", 0.06), self.mod.parse_group_and_rate("分组 ×0.06"))
        self.assertEqual(("分组", 0.06), self.mod.parse_group_and_rate("分组 0.06倍"))
        self.assertEqual(("分组", 0.06), self.mod.parse_group_and_rate("分组 0.06X"))

    def test_filters_collector_panel_noise_from_detected_accounts_and_balance(self):
        row = self.mod.normalize_observation(
            {
                "provider": "Magic",
                "site": "pool.gptstore.club",
                "url": "https://pool.gptstore.club/keys",
                "detected_balance": "本页识别：余额 有，账号行 5，倍率行 0",
                "text": "Fluter 上游采集\n脚本：0.1.7；collector：collector 可用\n最近结果：等待发送",
                "detected_accounts": [
                    {
                        "account_name": "脚本：0.1.7；token：已配置；collector：collector 可用",
                        "source_line": "脚本：0.1.7；token：已配置；collector：collector 可用 / 自动发送快照",
                    }
                ],
            }
        )

        self.assertIsNotNone(row)
        self.assertEqual("", row["detected_balance"])
        self.assertEqual([], row["detected_accounts"])

    def test_filters_quota_status_as_account_name(self):
        self.assertTrue(self.mod.is_noise_account_name("非自用 已启用 ¥161.28"))

    def test_filters_quota_only_detected_account_rows(self):
        row = self.mod.normalize_observation(
            {
                "provider": "钧澈",
                "site": "vip.lcodex.cn",
                "url": "https://vip.lcodex.cn/console/token",
                "detected_accounts": [
                    {
                        "account_name": "非自用",
                        "upstream_group": "¥585.03 GPT-PRO纯享号池",
                        "page_rate": 0.15,
                        "source_line": (
                            "非自用 已启用 ¥179.96 / ¥585.03 GPT-PRO纯享号池 0.15x "
                            "无限制 无限制 sk-YQuI**********UhHU / quota usage"
                        ),
                    },
                    {
                        "account_name": "钧澈 codex pro纯享应急 0.15*0.91=0.1365",
                        "upstream_group": "GPT-PRO纯享号池",
                        "page_rate": 0.15,
                        "source_line": (
                            "钧澈 codex pro纯享应急 0.15*0.91=0.1365 已启用 无限额度 "
                            "GPT-PRO纯享号池 0.15x sk-test...token"
                        ),
                    },
                ],
            }
        )

        self.assertIsNotNone(row)
        self.assertEqual(1, len(row["detected_accounts"]))
        self.assertEqual(
            "钧澈 codex pro纯享应急 0.15*0.91=0.1365",
            row["detected_accounts"][0]["account_name"],
        )

    def test_merge_latest_preserves_previous_signal_when_incoming_is_panel_only(self):
        previous = [
            self.mod.normalize_observation(
                {
                    "provider": "Magic",
                    "site": "pool.gptstore.club",
                    "url": "https://pool.gptstore.club/keys",
                    "observed_at": "2026-06-13T10:00:00+00:00",
                    "detected_accounts": [
                        {
                            "account_name": "magic codex pro 仅文字0.13",
                            "upstream_group": "Pro（可用非流式传输）",
                            "page_rate": 0.13,
                            "source_line": "magic codex pro 仅文字0.13 / Pro（可用非流式传输） / 0.13x",
                        }
                    ],
                    "detected_rates": [
                        "sk-29b...f630 / Pro（可用非流式传输） / 0.13x / 选择分组"
                    ],
                }
            )
        ]
        incoming = [
            self.mod.normalize_observation(
                {
                    "provider": "Magic",
                    "site": "pool.gptstore.club",
                    "url": "https://pool.gptstore.club/keys",
                    "observed_at": "2026-06-13T10:05:00+00:00",
                    "detected_balance": "本页识别：余额 有，账号行 5，倍率行 0",
                    "text": "Fluter 上游采集\n最近结果：等待发送",
                    "detected_accounts": [
                        {
                            "account_name": "Fluter 上游采集",
                            "source_line": "Fluter 上游采集 / 自动发送快照",
                        }
                    ],
                }
            )
        ]

        merged = self.mod.merge_latest(previous, incoming)

        self.assertEqual(1, len(merged))
        self.assertEqual("2026-06-13T10:05:00+00:00", merged[0]["observed_at"])
        self.assertEqual("magic codex pro 仅文字0.13", merged[0]["detected_accounts"][0]["account_name"])
        self.assertEqual(1, len(merged[0]["detected_rates"]))
        self.assertIn("ignored low-signal collector snapshot", merged[0]["detail"])

    def test_merge_latest_marks_partial_account_snapshot_without_backfilling_old_rows(self):
        previous = [
            self.mod.normalize_observation(
                {
                    "provider": "Kingdom",
                    "site": "api.tokenskingdom.com",
                    "url": "https://tokenskingdom.com/keys",
                    "observed_at": "2026-06-13T10:00:00+00:00",
                    "detected_accounts": [
                        {
                            "account_name": "kingdom codex plus1号 仅文字1.8*0.078=0.1404",
                            "upstream_group": "Plus 兜底",
                            "page_rate": 1.8,
                            "source_line": "kingdom codex plus1号 仅文字1.8*0.078=0.1404 sk-aaa...bbbb Plus 兜底 1.8x",
                        },
                        {
                            "account_name": "kingdom codex plus2号 仅文字1.2*0.078=0.0936",
                            "upstream_group": "Plus 2号池",
                            "page_rate": 1.2,
                            "source_line": "kingdom codex plus2号 仅文字1.2*0.078=0.0936 sk-bbb...cccc Plus 2号池 1.2x",
                        },
                        {
                            "account_name": "kingdom codex pro1号 仅文字4*0.078=0.312",
                            "upstream_group": "Pro池",
                            "page_rate": 4,
                            "source_line": "kingdom codex pro1号 仅文字4*0.078=0.312 sk-ccc...dddd Pro池 4x",
                        },
                    ],
                }
            )
        ]
        incoming = [
            self.mod.normalize_observation(
                {
                    "provider": "Kingdom",
                    "site": "api.tokenskingdom.com",
                    "url": "https://tokenskingdom.com/keys",
                    "observed_at": "2026-06-13T10:05:00+00:00",
                    "detected_accounts": [
                        {
                            "account_name": "kingdom codex plus2号 仅文字1.2*0.078=0.0936",
                            "upstream_group": "Plus 2号池",
                            "page_rate": 1.2,
                            "source_line": "kingdom codex plus2号 仅文字1.2*0.078=0.0936 sk-bbb...cccc Plus 2号池 1.2x",
                        }
                    ],
                }
            )
        ]

        merged = self.mod.merge_latest(previous, incoming)

        self.assertEqual(1, len(merged))
        self.assertEqual(1, len(merged[0]["detected_accounts"]))
        self.assertEqual(
            "kingdom codex plus2号 仅文字1.2*0.078=0.0936",
            merged[0]["detected_accounts"][0]["account_name"],
        )
        self.assertIn("partial account snapshot 1/3", merged[0]["detail"])


if __name__ == "__main__":
    unittest.main()
