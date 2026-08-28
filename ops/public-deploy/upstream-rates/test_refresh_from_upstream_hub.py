#!/usr/bin/env python3
from __future__ import annotations

import importlib.util
import os
import sqlite3
import sys
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("refresh_from_upstream_hub.py")


def load_module():
    spec = importlib.util.spec_from_file_location("refresh_from_upstream_hub", SCRIPT)
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class RefreshFromUpstreamHubTest(unittest.TestCase):
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
              balance_label text not null default '',
              balance_updated_at text not null default '',
              status text not null,
              note text not null,
              updated_at text not null
            );
            """
        )
        self.mod.ensure_schema(conn)
        return conn

    def test_normalize_site_handles_urls_and_www(self):
        self.assertEqual("api.saki.lat", self.mod.normalize_site("https://api.saki.lat/v1"))
        self.assertEqual("mdkj.lol", self.mod.normalize_site("www.mdkj.lol"))

    def test_read_env_file_value_handles_quotes(self):
        with tempfile.NamedTemporaryFile("w", delete=False) as tmp:
            tmp.write("POSTGRES_PASSWORD='secret value'\nOTHER=x\n")
            path = tmp.name
        try:
            self.assertEqual("secret value", self.mod.read_env_file_value(path, "POSTGRES_PASSWORD"))
            self.assertEqual("", self.mod.read_env_file_value(path, "MISSING"))
        finally:
            Path(path).unlink(missing_ok=True)

    def test_auto_hub_query_prefers_tcp_when_env_password_exists(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            Path(tmpdir, ".env").write_text("POSTGRES_PASSWORD=secret\n")
            args = self.mod.parse_args(
                [
                    "--hub-compose-dir",
                    tmpdir,
                    "--hub-host",
                    "127.0.0.1",
                    "--hub-port",
                    "54329",
                ]
            )

            commands = self.mod.hub_query_commands(args)

        self.assertEqual("tcp", commands[0][0])
        self.assertIn("-h 127.0.0.1", commands[0][1])
        self.assertEqual("secret", commands[0][2]["PGPASSWORD"])
        self.assertEqual("docker_compose", commands[1][0])
        self.assertEqual("docker_exec", commands[2][0])

    def test_tcp_hub_query_without_password_has_no_command(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            args = self.mod.parse_args(
                [
                    "--hub-compose-dir",
                    tmpdir,
                    "--hub-connection",
                    "tcp",
                ]
            )

            commands = self.mod.hub_query_commands(args)

        self.assertEqual([], commands)

    def test_docker_hub_query_uses_explicit_docker_host(self):
        args = self.mod.parse_args(
            [
                "--hub-connection",
                "docker",
                "--docker-host",
                "unix:///tmp/test-docker.sock",
            ]
        )

        commands = self.mod.hub_query_commands(args)

        self.assertEqual("docker_compose", commands[0][0])
        self.assertEqual("unix:///tmp/test-docker.sock", commands[0][2]["DOCKER_HOST"])
        self.assertEqual("docker_exec", commands[1][0])

    def test_hub_query_wrapper_uses_plain_select_to_preserve_json_escaping(self):
        args = self.mod.parse_args(["--hub-psql-command", "unused"])
        calls = []
        original_hub_query_commands = self.mod.hub_query_commands
        original_run_psql_command = self.mod.run_psql_command

        def fake_hub_query_commands(_args):
            return [("custom", "unused", None)]

        def fake_run_psql_command(command, wrapper_sql, timeout, env):
            calls.append((command, wrapper_sql, timeout, env))
            return [{"last_error": 'Get "https://example.test/api": no such host'}]

        self.mod.hub_query_commands = fake_hub_query_commands
        self.mod.run_psql_command = fake_run_psql_command
        self.addCleanup(lambda: setattr(self.mod, "hub_query_commands", original_hub_query_commands))
        self.addCleanup(lambda: setattr(self.mod, "run_psql_command", original_run_psql_command))

        rows = self.mod.run_hub_query(args, "select 1 as id")

        self.assertEqual('Get "https://example.test/api": no such host', rows[0]["last_error"])
        wrapper_sql = calls[0][1]
        self.assertIn("select coalesce(jsonb_agg(row_to_json(t)), '[]'::jsonb)::text", wrapper_sql)
        self.assertNotIn("copy (", wrapper_sql.lower())
        self.assertNotIn("to stdout", wrapper_sql.lower())

    def test_snapshot_json_round_trips_sanitized_observations(self):
        channel = self.mod.HubChannel(
            id=8,
            name="聪明AI",
            type="newapi",
            site_url="https://sub2.congmingai.com",
            site="sub2.congmingai.com",
            monitor_enabled=True,
            last_balance=self.mod.Decimal("40.44"),
            last_balance_at="2026-06-16 10:00:00+08",
            last_error="",
            updated_at="2026-06-16 10:01:00+08",
        )
        rate = self.mod.HubRateSnapshot(
            channel_id=8,
            model_name="仅文字0.05",
            description="",
            ratio=self.mod.Decimal("0.05"),
            completion_ratio=self.mod.Decimal("1"),
            first_seen_at="2026-06-16 09:00:00+08",
            last_seen_at="2026-06-16 10:00:00+08",
        )
        balance = self.mod.HubBalanceSnapshot(
            channel_id=8,
            balance=self.mod.Decimal("40.40"),
            sampled_at="2026-06-16 10:00:00+08",
        )
        change = self.mod.HubRateChange(
            channel_id=8,
            model_name="仅文字0.05",
            old_ratio=self.mod.Decimal("0.04"),
            new_ratio=self.mod.Decimal("0.05"),
            old_completion_ratio=None,
            new_completion_ratio=self.mod.Decimal("1"),
            changed_at="2026-06-16 10:00:00+08",
        )
        with tempfile.NamedTemporaryFile(suffix=".json", delete=False) as tmp:
            path = tmp.name
        try:
            self.mod.write_snapshot_json(
                path,
                [channel],
                [rate],
                [balance],
                [change],
                "2026-06-16T02:00:00+00:00",
            )
            text = Path(path).read_text()
            self.assertIn("fluter-upstream-hub-snapshot/v1", text)
            for forbidden in ("password", "cookie", "Bearer", "access_token", "sk-"):
                self.assertNotIn(forbidden, text)

            channels, rates, balances, changes = self.mod.load_snapshot_json(path)

            self.assertEqual("sub2.congmingai.com", channels[0].site)
            self.assertEqual(self.mod.Decimal("0.05"), rates[0].ratio)
            self.assertEqual(self.mod.Decimal("40.40"), balances[0].balance)
            self.assertEqual(self.mod.Decimal("0.04"), changes[0].old_ratio)
        finally:
            Path(path).unlink(missing_ok=True)

    def test_main_rejects_conflicting_snapshot_modes(self):
        with self.assertRaises(SystemExit):
            self.mod.main(["--export-json", "/tmp/a.json", "--import-json", "/tmp/b.json"])

    def test_export_only_requires_export_path(self):
        with self.assertRaises(SystemExit):
            self.mod.main(["--export-only"])

    def test_hub_observations_update_exact_matching_ledger_rows_only(self):
        conn = self.connection()
        conn.execute(
            """
            insert into upstream_rate_records (
              category, kind, site, fluter_account_name, upstream_group,
              page_rate, recharge_factor, site_account_multiplier, status, note, updated_at
            ) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                "聪明AI",
                "Codex",
                "sub2.congmingai.com",
                "聪明ai codex 对接 仅文字0.05",
                "中转站对接分组",
                0.05,
                1,
                0.05,
                "已确认",
                "old note",
                "old",
            ),
        )
        conn.execute(
            """
            insert into upstream_rate_records (
              category, kind, site, fluter_account_name, upstream_group,
              page_rate, recharge_factor, site_account_multiplier, status, note, updated_at
            ) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                "聪明AI",
                "Codex",
                "sub2.congmingai.com",
                "unmatched",
                "旧分组",
                0.02,
                1,
                0.02,
                "已确认",
                "old note",
                "old",
            ),
        )
        channel = self.mod.HubChannel(
            id=1,
            name="聪明AI",
            type="newapi",
            site_url="https://sub2.congmingai.com",
            site="sub2.congmingai.com",
            monitor_enabled=True,
            last_balance=self.mod.Decimal("40.44"),
            last_balance_at="2026-06-16 10:00:00+08",
            last_error="",
            updated_at="2026-06-16 10:01:00+08",
        )
        rate = self.mod.HubRateSnapshot(
            channel_id=1,
            model_name="中转站对接分组",
            description="",
            ratio=self.mod.Decimal("0.06"),
            completion_ratio=self.mod.Decimal("1"),
            first_seen_at="2026-06-16 09:00:00+08",
            last_seen_at="2026-06-16 10:00:00+08",
        )

        result = self.mod.write_observations(
            conn,
            [channel],
            [rate],
            [],
            [],
            "2026-06-16T02:00:00+00:00",
            True,
        )

        self.assertEqual((1, 1, 0, 1, 2), result)
        matched = conn.execute(
            "select page_rate, actual_cost_label, balance_label, note from upstream_rate_records where upstream_group = '中转站对接分组'"
        ).fetchone()
        unmatched = conn.execute(
            "select page_rate, balance_label from upstream_rate_records where upstream_group = '旧分组'"
        ).fetchone()
        observation = conn.execute("select * from upstream_hub_rate_observations").fetchone()
        status = conn.execute("select status, adapter_kind from upstream_adapter_status").fetchone()

        self.assertAlmostEqual(0.06, matched["page_rate"])
        self.assertIn("实际成本倍率 0.06x", matched["actual_cost_label"])
        self.assertIn("upstream-hub 同步", matched["note"])
        self.assertAlmostEqual(0.02, unmatched["page_rate"])
        self.assertEqual("$40.44", matched["balance_label"])
        self.assertEqual("$40.44", unmatched["balance_label"])
        self.assertEqual("中转站对接分组", observation["model_name"])
        self.assertEqual("hub_observed", status["status"])
        self.assertEqual("upstream_hub", status["adapter_kind"])

    def test_hub_import_does_not_update_image_rows_as_text_rates(self):
        conn = self.connection()
        conn.execute(
            """
            insert into upstream_rate_records (
              category, kind, site, fluter_account_name, upstream_group,
              page_rate, recharge_factor, site_account_multiplier, status, note, updated_at
            ) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                "Meow",
                "生图",
                "api.saki.lat",
                "meow codex 生图 0.05",
                "Image-2 无限刷客户端 无上限并发",
                0.05,
                1,
                0.5,
                "生图来源/已确认",
                "old note",
                "old",
            ),
        )
        channel = self.mod.HubChannel(
            id=2,
            name="Meow",
            type="newapi",
            site_url="https://api.saki.lat",
            site="api.saki.lat",
            monitor_enabled=True,
            last_balance=None,
            last_balance_at="",
            last_error="",
            updated_at="now",
        )
        rate = self.mod.HubRateSnapshot(
            channel_id=2,
            model_name="Image-2 无限刷客户端 无上限并发",
            description="",
            ratio=self.mod.Decimal("0.08"),
            completion_ratio=None,
            first_seen_at="now",
            last_seen_at="now",
        )

        result = self.mod.write_observations(conn, [channel], [rate], [], [], "now", True)

        self.assertEqual((1, 1, 0, 0, 0), result)
        row = conn.execute("select page_rate, actual_cost_label from upstream_rate_records").fetchone()
        self.assertAlmostEqual(0.05, row["page_rate"])
        self.assertEqual("", row["actual_cost_label"])

    def test_tokenskingdom_hub_site_updates_api_tokenskingdom_ledger_alias(self):
        conn = self.connection()
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
                "kingdom codex plus2号 仅文字1.1*0.078=0.0858",
                "Plus 2号池",
                0.95,
                0.07444,
                0.0741,
                "已确认",
                "old note",
                "old",
            ),
        )
        channel = self.mod.HubChannel(
            id=14,
            name="kingdom",
            type="newapi",
            site_url="https://tokenskingdom.com",
            site="tokenskingdom.com",
            monitor_enabled=True,
            last_balance=self.mod.Decimal("1807.81"),
            last_balance_at="2026-06-16 10:00:00+08",
            last_error="",
            updated_at="2026-06-16 10:01:00+08",
        )
        rate = self.mod.HubRateSnapshot(
            channel_id=14,
            model_name="Plus 2号池",
            description="",
            ratio=self.mod.Decimal("1.1"),
            completion_ratio=self.mod.Decimal("1"),
            first_seen_at="2026-06-16 09:00:00+08",
            last_seen_at="2026-06-16 10:00:00+08",
        )

        result = self.mod.write_observations(
            conn,
            [channel],
            [rate],
            [],
            [],
            "2026-06-16T02:00:00+00:00",
            True,
        )

        self.assertEqual((1, 1, 0, 1, 1), result)
        row = conn.execute("select page_rate, actual_cost_label, balance_label, note from upstream_rate_records").fetchone()
        self.assertAlmostEqual(1.1, row["page_rate"])
        self.assertIn("实际成本倍率 0.081884x", row["actual_cost_label"])
        self.assertEqual("$1807.81", row["balance_label"])
        self.assertIn("upstream-hub 同步", row["note"])


if __name__ == "__main__":
    unittest.main()
