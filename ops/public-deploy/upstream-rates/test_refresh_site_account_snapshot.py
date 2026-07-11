#!/usr/bin/env python3
from __future__ import annotations

import importlib.util
import sqlite3
import sys
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("refresh_site_account_snapshot.py")


def load_module():
    spec = importlib.util.spec_from_file_location("refresh_site_account_snapshot", SCRIPT)
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class RefreshSiteAccountSnapshotTest(unittest.TestCase):
    def setUp(self) -> None:
        self.mod = load_module()

    def test_compact_text_suffixes_share_account_stem(self):
        cases = {
            "magic codex pro 0.16": "magic codex pro",
            "magic codex pro 仅文字0.08": "magic codex pro",
            "magic codex 代理快速通道 仅文字0.045": "magic codex 代理快速通道",
            "超超(主站) codex pro 仅文字0.1": "超超(主站) codex pro",
            "meow claude ccmax仅客户端 1.1": "meow claude max",
        }

        for name, expected in cases.items():
            with self.subTest(name=name):
                self.assertEqual(expected, self.mod.account_stem(name))

    def test_semantic_match_can_pair_magic_proxy_fast_rename(self):
        conn = sqlite3.connect(":memory:")
        conn.row_factory = sqlite3.Row
        conn.executescript(
            """
            create table site_account_snapshots (
              account_name text not null,
              account_stem text not null,
              base_host text not null
            );
            create table upstream_rate_records (
              fluter_account_name text not null,
              upstream_group text not null,
              site text not null
            );
            insert into site_account_snapshots values
              ('magic codex pro 仅文字0.08', 'magic codex pro', 'pool.gptstore.club'),
              ('magic codex 代理快速通道 仅文字0.045', 'magic codex 代理快速通道', 'pool.gptstore.club');
            insert into upstream_rate_records values
              ('magic codex 0.04', '代理快速渠道（不能生图）', 'pool.gptstore.club');
            """
        )

        row = conn.execute("select * from upstream_rate_records").fetchone()
        snapshots = conn.execute("select * from site_account_snapshots").fetchall()
        matches = self.mod.semantic_candidates(row, snapshots)

        self.assertEqual(1, len(matches))
        self.assertEqual("magic codex 代理快速通道 仅文字0.045", matches[0]["account_name"])
        conn.close()

    def test_missing_junche_snapshot_row_is_inserted_when_browser_account_confirms_it(self):
        conn = sqlite3.connect(":memory:")
        conn.row_factory = sqlite3.Row
        conn.executescript(
            """
            create table site_account_snapshots (
              account_id integer primary key,
              account_name text not null,
              normalized_account_name text not null,
              account_stem text not null default '',
              platform text not null,
              base_url text not null,
              base_host text not null,
              status text not null,
              schedulable integer not null,
              rate_multiplier real,
              groups_json text not null,
              group_label text not null,
              production_updated_at text not null,
              observed_at text not null
            );
            create table upstream_rate_records (
              id integer primary key autoincrement,
              category text not null,
              kind text not null,
              site text not null,
              fluter_account_name text not null,
              upstream_group text not null,
              page_rate real,
              recharge_ratio_label text not null,
              recharge_factor real not null,
              site_account_multiplier real,
              site_group_multiplier text not null,
              actual_cost_label text not null default '',
              status text not null,
              note text not null default '',
              updated_at text not null default '',
              unique(site, fluter_account_name, upstream_group)
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
              observed_at text not null
            );
            insert into site_account_snapshots values (
              7401,
              '钧澈 codex pro破限 仅文字0.25*0.91=0.22728',
              '钧澈 codex pro破限 仅文字0.25*0.91=0.22728',
              '钧澈 codex pro破限',
              'openai',
              'https://vip.lcodex.cn/v1',
              'vip.lcodex.cn',
              'active',
              1,
              0.22728,
              '[]',
              'codex pro渠道@0.3',
              '2026-06-12T00:00:00+00:00',
              '2026-06-12T20:00:00+00:00'
            );
            insert into browser_adapter_account_observations values (
              '钧澈',
              'vip.lcodex.cn',
              '钧澈 codex pro破限 仅文字0.25*0.91=0.22728',
              '钧澈codexpro破限仅文字0.25*0.91=0.22728',
              'pro破限',
              0.25,
              '钧澈 codex pro破限 仅文字0.25*0.91=0.22728 / pro破限 / 0.25x',
              0,
              '2026-06-12T19:59:00+00:00'
            );
            """
        )

        matched = self.mod.refresh_ledger_rows(conn, "2026-06-12T20:00:00+00:00")

        row = conn.execute(
            """
            select category, kind, site, fluter_account_name, upstream_group,
                   page_rate, recharge_factor, site_account_multiplier,
                   site_group_multiplier, status, note
            from upstream_rate_records
            where fluter_account_name = '钧澈 codex pro破限 仅文字0.25*0.91=0.22728'
            """
        ).fetchone()
        self.assertEqual((0, 0, 0, 0), matched)
        self.assertIsNotNone(row)
        self.assertEqual("钧澈", row["category"])
        self.assertEqual("Codex Pro", row["kind"])
        self.assertEqual("pro破限", row["upstream_group"])
        self.assertEqual(0.25, row["page_rate"])
        self.assertAlmostEqual(0.925925926, row["recharge_factor"])
        self.assertEqual(0.22728, row["site_account_multiplier"])
        self.assertIn("codex pro渠道@0.3", row["site_group_multiplier"])
        self.assertEqual("已确认", row["status"])
        self.assertIn("台账自动补行", row["note"])
        metadata = conn.execute(
            "select value from metadata where key = 'site_account_snapshot_inserted_missing_rows'"
        ).fetchone()
        self.assertIsNotNone(metadata)
        self.assertEqual("1", metadata["value"])
        conn.close()

    def test_missing_snapshot_row_is_not_inserted_from_stale_browser_account_observation(self):
        conn = sqlite3.connect(":memory:")
        conn.row_factory = sqlite3.Row
        conn.executescript(
            """
            create table site_account_snapshots (
              account_id integer primary key,
              account_name text not null,
              normalized_account_name text not null,
              account_stem text not null default '',
              platform text not null,
              base_url text not null,
              base_host text not null,
              status text not null,
              schedulable integer not null,
              rate_multiplier real,
              groups_json text not null,
              group_label text not null,
              production_updated_at text not null,
              observed_at text not null
            );
            create table upstream_rate_records (
              id integer primary key autoincrement,
              category text not null,
              kind text not null,
              site text not null,
              fluter_account_name text not null,
              upstream_group text not null,
              page_rate real,
              recharge_ratio_label text not null,
              recharge_factor real not null,
              site_account_multiplier real,
              site_group_multiplier text not null,
              actual_cost_label text not null default '',
              status text not null,
              note text not null default '',
              updated_at text not null default '',
              unique(site, fluter_account_name, upstream_group)
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
              observed_at text not null
            );
            insert into site_account_snapshots values (
              7401,
              '钧澈 codex pro破限 仅文字0.25*0.91=0.22728',
              '钧澈 codex pro破限 仅文字0.25*0.91=0.22728',
              '钧澈 codex pro破限',
              'openai',
              'https://vip.lcodex.cn/v1',
              'vip.lcodex.cn',
              'active',
              1,
              0.22728,
              '[]',
              'codex pro渠道@0.3',
              '2026-06-12T00:00:00+00:00',
              '2026-06-12T20:00:00+00:00'
            );
            insert into browser_adapter_account_observations values (
              '钧澈',
              'vip.lcodex.cn',
              '钧澈 codex pro破限 仅文字0.25*0.91=0.22728',
              '钧澈codexpro破限仅文字0.25*0.91=0.22728',
              'pro破限',
              0.25,
              '钧澈 codex pro破限 仅文字0.25*0.91=0.22728 / pro破限 / 0.25x',
              0,
              '2026-06-11T20:00:00+00:00'
            );
            """
        )

        matched = self.mod.refresh_ledger_rows(conn, "2026-06-12T20:00:00+00:00")

        count = conn.execute("select count(*) from upstream_rate_records").fetchone()[0]
        self.assertEqual((0, 0, 0, 0), matched)
        self.assertEqual(0, count)
        conn.close()

    def test_observation_only_row_is_not_renamed_by_production_snapshot(self):
        conn = sqlite3.connect(":memory:")
        conn.row_factory = sqlite3.Row
        conn.executescript(
            """
            create table site_account_snapshots (
              account_id integer primary key,
              account_name text not null,
              normalized_account_name text not null,
              account_stem text not null default '',
              platform text not null,
              base_url text not null,
              base_host text not null,
              status text not null,
              schedulable integer not null,
              rate_multiplier real,
              groups_json text not null,
              group_label text not null,
              production_updated_at text not null,
              observed_at text not null
            );
            create table upstream_rate_records (
              id integer primary key autoincrement,
              category text not null,
              kind text not null,
              site text not null,
              fluter_account_name text not null,
              upstream_group text not null,
              page_rate real,
              recharge_ratio_label text not null,
              recharge_factor real not null,
              site_account_multiplier real,
              site_group_multiplier text not null,
              actual_cost_label text not null default '',
              status text not null,
              note text not null default '',
              updated_at text not null default '',
              unique(site, fluter_account_name, upstream_group)
            );
            insert into site_account_snapshots values (
              7399,
              'meow codex pro 仅文字0.25',
              'meow codex pro 仅文字0.25',
              'meow codex pro',
              'openai',
              'https://api.saki.lat/v1',
              'api.saki.lat',
              'active',
              1,
              0.25,
              '[]',
              'codex pro渠道@0.3',
              '2026-06-12T00:00:00+00:00',
              '2026-06-12T20:00:00+00:00'
            );
            insert into upstream_rate_records (
              category, kind, site, fluter_account_name, upstream_group,
              page_rate, recharge_ratio_label, recharge_factor,
              site_account_multiplier, site_group_multiplier, status, note
            ) values (
              'Meow',
              '生图',
              'api.saki.lat',
              'meow codex pro 生图 0.07',
              'codex pro 生图',
              0.07,
              '1:1',
              1,
              null,
              '本站未接入/未调度',
              '上游观察/本站未接入',
              'observation only'
            );
            """
        )

        matched = self.mod.refresh_ledger_rows(conn, "2026-06-12T20:00:00+00:00")

        row = conn.execute("select * from upstream_rate_records").fetchone()
        self.assertEqual((0, 0, 0, 1), matched)
        self.assertEqual("meow codex pro 生图 0.07", row["fluter_account_name"])
        self.assertEqual("codex pro 生图", row["upstream_group"])
        self.assertIsNone(row["site_account_multiplier"])
        self.assertEqual("本站未接入/未调度", row["site_group_multiplier"])
        self.assertEqual("上游观察/本站未接入", row["status"])
        self.assertEqual("observation only", row["note"])
        conn.close()


if __name__ == "__main__":
    unittest.main()
