#!/usr/bin/env python3
from __future__ import annotations

import importlib.util
import sqlite3
import sys
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("cleanup_removed_upstream_groups.py")


def load_module():
    spec = importlib.util.spec_from_file_location("cleanup_removed_upstream_groups", SCRIPT)
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class CleanupRemovedUpstreamGroupsTest(unittest.TestCase):
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
              actual_cost_label text not null default '',
              status text not null,
              note text not null default '',
              updated_at text not null default ''
            );

            create table metadata (
              key text primary key,
              value text not null
            );

            insert into metadata(key, value) values
              ('last_orchestrated_refresh_at', '2026-06-12T20:00:00+00:00');
            """
        )
        return conn

    def insert_ledger(self, conn: sqlite3.Connection, upstream_group: str = "old group", *, kind: str = "Codex", status: str = "已确认") -> None:
        conn.execute(
            """
            insert into upstream_rate_records (
              category, kind, site, fluter_account_name, upstream_group,
              page_rate, recharge_factor, site_account_multiplier, status
            ) values (?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            ("Provider", kind, "example.com", "example account", upstream_group, 0.01, 1, 0.01, status),
        )

    def write_db(self, conn: sqlite3.Connection, path: Path) -> None:
        conn.commit()
        disk = sqlite3.connect(path)
        with disk:
            conn.backup(disk)
        disk.close()

    def test_group_matches_requires_exact_for_short_ambiguous_tokens(self):
        self.assertFalse(self.mod.group_matches("gpt", "gpt-pro"))
        self.assertFalse(self.mod.group_matches("pro", "pro破限"))
        self.assertFalse(self.mod.group_matches("ag", "稳定AG量"))
        self.assertFalse(self.mod.group_matches("cc", "ccmax"))
        self.assertTrue(self.mod.group_matches("代理快速渠道", "代理快速渠道（不能生图）"))

    def test_marks_fresh_missing_browser_group_as_removed(self):
        conn = self.connection()
        self.insert_ledger(conn, "old group")
        conn.executescript(
            """
            create table browser_adapter_status (
              provider text not null,
              site text not null,
              status text not null,
              detail text not null,
              observed_at text not null
            );
            create table browser_adapter_rate_observations (
              provider text not null,
              site text not null,
              upstream_group text not null,
              page_rate real not null,
              source_line text not null,
              matched_ledger_rows integer not null,
              observed_at text not null
            );
            insert into browser_adapter_status values (
              'Provider', 'example.com', 'browser_observed', 'rate_lines=2',
              '2026-06-12T19:59:00+00:00'
            );
            insert into browser_adapter_rate_observations values (
              'Provider', 'example.com', 'new group', 0.02, 'key / new group / 0.02x', 0,
              '2026-06-12T19:59:00+00:00'
            );
            insert into browser_adapter_rate_observations values (
              'Provider', 'example.com', 'another group', 0.03, 'key / another group / 0.03x', 0,
              '2026-06-12T19:59:00+00:00'
            );
            """
        )

        path = Path("/tmp/fluter-cleanup-test.sqlite")
        try:
            if path.exists():
                path.unlink()
            self.write_db(conn, path)
            candidates = self.mod.load_candidates(str(path), 36)
            self.assertEqual(1, len(candidates))
            self.assertEqual("old group", candidates[0].upstream_group)

            self.mod.apply_cleanup(str(path), candidates)
            check = sqlite3.connect(path)
            check.row_factory = sqlite3.Row
            row = check.execute("select status, actual_cost_label, note from upstream_rate_records").fetchone()
            self.assertEqual(self.mod.REMOVED_STATUS, row["status"])
            self.assertIn("未在本轮刷新页面/接口出现", row["actual_cost_label"])
            self.assertIn("old group", row["note"])
            check.close()
        finally:
            if path.exists():
                path.unlink()

    def test_misaligned_browser_rate_rows_are_not_enough_to_mark_removed(self):
        conn = self.connection()
        self.insert_ledger(conn, "old group")
        conn.executescript(
            """
            create table browser_adapter_status (
              provider text not null,
              site text not null,
              status text not null,
              detail text not null,
              observed_at text not null
            );
            create table browser_adapter_rate_observations (
              provider text not null,
              site text not null,
              upstream_group text not null,
              page_rate real not null,
              source_line text not null,
              matched_ledger_rows integer not null,
              observed_at text not null
            );
            insert into browser_adapter_status values (
              'Provider', 'example.com', 'browser_observed', 'rate_lines=2',
              '2026-06-12T19:59:00+00:00'
            );
            insert into browser_adapter_rate_observations values
              ('Provider', 'example.com', 'new group', 0.02, 'key / new group / 0.02x', 0, '2026-06-12T19:40:00+00:00'),
              ('Provider', 'example.com', 'another group', 0.03, 'key / another group / 0.03x', 0, '2026-06-12T19:40:00+00:00');
            """
        )

        path = Path("/tmp/fluter-cleanup-test.sqlite")
        try:
            if path.exists():
                path.unlink()
            self.write_db(conn, path)
            self.assertEqual([], self.mod.load_candidates(str(path), 36))
        finally:
            if path.exists():
                path.unlink()

    def test_rate_lines_zero_is_not_removed_group(self):
        conn = self.connection()
        self.insert_ledger(conn, "old group")
        conn.executescript(
            """
            create table browser_adapter_status (
              provider text not null,
              site text not null,
              status text not null,
              detail text not null,
              observed_at text not null
            );
            create table browser_adapter_rate_observations (
              provider text not null,
              site text not null,
              upstream_group text not null,
              page_rate real not null,
              source_line text not null,
              matched_ledger_rows integer not null,
              observed_at text not null
            );
            insert into browser_adapter_status values (
              'Provider', 'example.com', 'browser_observed', 'rate_lines=0',
              '2026-06-12T19:59:00+00:00'
            );
            """
        )
        path = Path("/tmp/fluter-cleanup-test.sqlite")
        try:
            if path.exists():
                path.unlink()
            self.write_db(conn, path)
            self.assertEqual([], self.mod.load_candidates(str(path), 36))
        finally:
            if path.exists():
                path.unlink()

    def test_tokenskingdom_hub_alias_prevents_removed_group_false_positive(self):
        conn = self.connection()
        conn.execute(
            """
            insert into upstream_rate_records (
              category, kind, site, fluter_account_name, upstream_group,
              page_rate, recharge_factor, site_account_multiplier, status
            ) values (?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                "Kingdom",
                "Codex",
                "api.tokenskingdom.com",
                "kingdom codex plus2号 仅文字1.1*0.078=0.0858",
                "Plus 2号池",
                1.1,
                0.07444,
                0.085536,
                "已确认",
            ),
        )
        conn.executescript(
            """
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
            insert into upstream_hub_rate_observations values
              (14, 'kingdom', 'tokenskingdom.com', 'Plus 2号池', '', 1.1, 1,
               '2026-06-12T19:00:00+00:00', '2026-06-12T19:59:00+00:00',
               '2026-06-12T19:59:00+00:00'),
              (14, 'kingdom', 'tokenskingdom.com', 'CC Max 1号池', '', 15.5, 1,
               '2026-06-12T19:00:00+00:00', '2026-06-12T19:59:00+00:00',
               '2026-06-12T19:59:00+00:00');
            """
        )
        path = Path("/tmp/fluter-cleanup-test.sqlite")
        try:
            if path.exists():
                path.unlink()
            self.write_db(conn, path)
            self.assertEqual([], self.mod.load_candidates(str(path), 36))
        finally:
            if path.exists():
                path.unlink()

    def test_skip_image_and_stopped_rows(self):
        conn = self.connection()
        self.insert_ledger(conn, "old image group", kind="生图")
        self.insert_ledger(conn, "old stopped group", status="停用")
        conn.executescript(
            """
            create table browser_adapter_status (
              provider text not null,
              site text not null,
              status text not null,
              detail text not null,
              observed_at text not null
            );
            create table browser_adapter_rate_observations (
              provider text not null,
              site text not null,
              upstream_group text not null,
              page_rate real not null,
              source_line text not null,
              matched_ledger_rows integer not null,
              observed_at text not null
            );
            insert into browser_adapter_status values (
              'Provider', 'example.com', 'browser_observed', 'rate_lines=1',
              '2026-06-12T19:59:00+00:00'
            );
            insert into browser_adapter_rate_observations values (
              'Provider', 'example.com', 'new group', 0.02, 'key / new group / 0.02x', 0,
              '2026-06-12T19:59:00+00:00'
            );
            """
        )
        path = Path("/tmp/fluter-cleanup-test.sqlite")
        try:
            if path.exists():
                path.unlink()
            self.write_db(conn, path)
            self.assertEqual([], self.mod.load_candidates(str(path), 36))
        finally:
            if path.exists():
                path.unlink()

    def test_kbq_rows_are_not_cleaned_by_plain_group_page(self):
        conn = self.connection()
        self.insert_ledger(conn, "[plus]gpt-5.4 / [plus]gpt-5.5")
        conn.execute("update upstream_rate_records set category = 'KBQ', site = 'xn--vduyey89e.com'")
        conn.executescript(
            """
            create table upstream_adapter_status (
              provider text not null,
              site text not null,
              adapter_kind text not null,
              status text not null,
              detail text not null,
              observed_at text not null
            );
            create table provider_group_ratio_records (
              provider text not null,
              site text not null,
              group_name text not null,
              page_rate real not null,
              updated_at text not null
            );
            insert into upstream_adapter_status values (
              'KBQ', 'xn--vduyey89e.com', 'public_pricing', 'ok',
              'groups=6 models=94', '2026-06-12T19:59:00+00:00'
            );
            insert into provider_group_ratio_records values
              ('KBQ', 'xn--vduyey89e.com', 'GPT-plus', 0.08, '2026-06-12T19:59:00+00:00'),
              ('KBQ', 'xn--vduyey89e.com', 'GPT-pro', 0.15, '2026-06-12T19:59:00+00:00');
            """
        )
        path = Path("/tmp/fluter-cleanup-test.sqlite")
        try:
            if path.exists():
                path.unlink()
            self.write_db(conn, path)
            self.assertEqual([], self.mod.load_candidates(str(path), 36))
        finally:
            if path.exists():
                path.unlink()

    def test_single_browser_group_is_not_enough_to_mark_removed(self):
        conn = self.connection()
        self.insert_ledger(conn, "old group")
        conn.executescript(
            """
            create table browser_adapter_status (
              provider text not null,
              site text not null,
              status text not null,
              detail text not null,
              observed_at text not null
            );
            create table browser_adapter_rate_observations (
              provider text not null,
              site text not null,
              upstream_group text not null,
              page_rate real not null,
              source_line text not null,
              matched_ledger_rows integer not null,
              observed_at text not null
            );
            insert into browser_adapter_status values (
              'Provider', 'example.com', 'browser_observed', 'rate_lines=1',
              '2026-06-12T19:59:00+00:00'
            );
            insert into browser_adapter_rate_observations values (
              'Provider', 'example.com', 'only visible group', 0.02, 'key / only visible group / 0.02x', 0,
              '2026-06-12T19:59:00+00:00'
            );
            """
        )
        path = Path("/tmp/fluter-cleanup-test.sqlite")
        try:
            if path.exists():
                path.unlink()
            self.write_db(conn, path)
            self.assertEqual([], self.mod.load_candidates(str(path), 36))
        finally:
            if path.exists():
                path.unlink()

    def test_hub_groups_can_mark_removed_when_inventory_is_complete(self):
        conn = self.connection()
        self.insert_ledger(conn, "old group")
        conn.executescript(
            """
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
              imported_at text not null
            );
            insert into upstream_hub_rate_observations values
              (1, 'Provider', 'example.com', 'new group', '', 0.02, 1, '2026-06-12T19:00:00+00:00', '2026-06-12T19:59:00+00:00', '2026-06-12T19:59:00+00:00'),
              (1, 'Provider', 'example.com', 'another group', '', 0.03, 1, '2026-06-12T19:00:00+00:00', '2026-06-12T19:59:00+00:00', '2026-06-12T19:59:00+00:00');
            """
        )
        path = Path("/tmp/fluter-cleanup-test.sqlite")
        try:
            if path.exists():
                path.unlink()
            self.write_db(conn, path)
            candidates = self.mod.load_candidates(str(path), 36)
            self.assertEqual(1, len(candidates))
            self.assertEqual("Provider upstream_hub", candidates[0].source)
            self.assertEqual("old group", candidates[0].upstream_group)
        finally:
            if path.exists():
                path.unlink()

    def test_single_hub_group_is_not_enough_to_mark_removed(self):
        conn = self.connection()
        self.insert_ledger(conn, "old group")
        conn.executescript(
            """
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
              imported_at text not null
            );
            insert into upstream_hub_rate_observations values
              (1, 'Provider', 'example.com', 'only visible group', '', 0.02, 1, '2026-06-12T19:00:00+00:00', '2026-06-12T19:59:00+00:00', '2026-06-12T19:59:00+00:00');
            """
        )
        path = Path("/tmp/fluter-cleanup-test.sqlite")
        try:
            if path.exists():
                path.unlink()
            self.write_db(conn, path)
            self.assertEqual([], self.mod.load_candidates(str(path), 36))
        finally:
            if path.exists():
                path.unlink()

    def test_legacy_tampermonkey_status_is_not_enough_to_mark_removed(self):
        conn = self.connection()
        self.insert_ledger(conn, "old group")
        conn.executescript(
            """
            create table browser_adapter_status (
              provider text not null,
              site text not null,
              status text not null,
              detail text not null,
              observed_at text not null
            );
            create table browser_adapter_rate_observations (
              provider text not null,
              site text not null,
              upstream_group text not null,
              page_rate real not null,
              source_line text not null,
              matched_ledger_rows integer not null,
              observed_at text not null
            );
            insert into browser_adapter_status values (
              'Provider', 'example.com', 'browser_observed',
              'Chrome Tampermonkey read-only snapshot; rate_lines=2; script=0.1.12',
              '2026-06-12T19:59:00+00:00'
            );
            insert into browser_adapter_rate_observations values
              ('Provider', 'example.com', 'new group', 0.02, 'key / new group / 0.02x', 0, '2026-06-12T19:59:00+00:00'),
              ('Provider', 'example.com', 'another group', 0.03, 'key / another group / 0.03x', 0, '2026-06-12T19:59:00+00:00');
            """
        )
        path = Path("/tmp/fluter-cleanup-test.sqlite")
        try:
            if path.exists():
                path.unlink()
            self.write_db(conn, path)
            self.assertEqual([], self.mod.load_candidates(str(path), 36))
        finally:
            if path.exists():
                path.unlink()

    def test_preserved_rate_lines_are_not_enough_to_mark_removed(self):
        conn = self.connection()
        self.insert_ledger(conn, "old group")
        conn.executescript(
            """
            create table browser_adapter_status (
              provider text not null,
              site text not null,
              status text not null,
              detail text not null,
              observed_at text not null
            );
            create table browser_adapter_rate_observations (
              provider text not null,
              site text not null,
              upstream_group text not null,
              page_rate real not null,
              source_line text not null,
              matched_ledger_rows integer not null,
              observed_at text not null
            );
            insert into browser_adapter_status values (
              'Provider', 'example.com', 'browser_observed',
              'Chrome Tampermonkey read-only snapshot; fresh_rate_lines=0; preserved previous rate lines from 2026-06-12T18:00:00+00:00; script=0.1.15',
              '2026-06-12T19:59:00+00:00'
            );
            insert into browser_adapter_rate_observations values
              ('Provider', 'example.com', 'new group', 0.02, 'key / new group / 0.02x', 0, '2026-06-12T18:00:00+00:00'),
              ('Provider', 'example.com', 'another group', 0.03, 'key / another group / 0.03x', 0, '2026-06-12T18:00:00+00:00');
            """
        )
        path = Path("/tmp/fluter-cleanup-test.sqlite")
        try:
            if path.exists():
                path.unlink()
            self.write_db(conn, path)
            self.assertEqual([], self.mod.load_candidates(str(path), 36))
        finally:
            if path.exists():
                path.unlink()

    def test_preserved_non_empty_snapshot_is_not_enough_to_mark_removed(self):
        conn = self.connection()
        self.insert_ledger(conn, "old group")
        conn.executescript(
            """
            create table browser_adapter_status (
              provider text not null,
              site text not null,
              status text not null,
              detail text not null,
              observed_at text not null
            );
            create table browser_adapter_rate_observations (
              provider text not null,
              site text not null,
              upstream_group text not null,
              page_rate real not null,
              source_line text not null,
              matched_ledger_rows integer not null,
              observed_at text not null
            );
            insert into browser_adapter_status values (
              'Provider', 'example.com', 'browser_observed',
              'latest read was empty; preserved previous non-empty snapshot from 2026-06-12T18:00:00+00:00; script=0.1.15',
              '2026-06-12T19:59:00+00:00'
            );
            insert into browser_adapter_rate_observations values
              ('Provider', 'example.com', 'new group', 0.02, 'key / new group / 0.02x', 0, '2026-06-12T18:00:00+00:00'),
              ('Provider', 'example.com', 'another group', 0.03, 'key / another group / 0.03x', 0, '2026-06-12T18:00:00+00:00');
            """
        )
        path = Path("/tmp/fluter-cleanup-test.sqlite")
        try:
            if path.exists():
                path.unlink()
            self.write_db(conn, path)
            self.assertEqual([], self.mod.load_candidates(str(path), 36))
        finally:
            if path.exists():
                path.unlink()

    def test_partial_account_snapshot_is_not_enough_to_mark_removed(self):
        conn = self.connection()
        self.insert_ledger(conn, "old group")
        conn.executescript(
            """
            create table browser_adapter_status (
              provider text not null,
              site text not null,
              status text not null,
              detail text not null,
              observed_at text not null
            );
            create table browser_adapter_rate_observations (
              provider text not null,
              site text not null,
              upstream_group text not null,
              page_rate real not null,
              source_line text not null,
              matched_ledger_rows integer not null,
              observed_at text not null
            );
            insert into browser_adapter_status values (
              'Provider', 'example.com', 'browser_observed',
              'Chrome Tampermonkey read-only snapshot; account_lines=7; rate_lines=2; script=0.1.15; partial account snapshot 7/9 compared with previous 2026-06-12T18:00:00+00:00',
              '2026-06-12T19:59:00+00:00'
            );
            insert into browser_adapter_rate_observations values
              ('Provider', 'example.com', 'new group', 0.02, 'key / new group / 0.02x', 0, '2026-06-12T19:59:00+00:00'),
              ('Provider', 'example.com', 'another group', 0.03, 'key / another group / 0.03x', 0, '2026-06-12T19:59:00+00:00');
            """
        )
        path = Path("/tmp/fluter-cleanup-test.sqlite")
        try:
            if path.exists():
                path.unlink()
            self.write_db(conn, path)
            self.assertEqual([], self.mod.load_candidates(str(path), 36))
        finally:
            if path.exists():
                path.unlink()

    def test_fresh_browser_account_row_prevents_public_pricing_removed_false_positive(self):
        conn = self.connection()
        self.insert_ledger(conn, "old api-only group")
        conn.executescript(
            """
            create table upstream_adapter_status (
              provider text not null,
              site text not null,
              adapter_kind text not null,
              status text not null,
              detail text not null,
              observed_at text not null
            );
            create table provider_group_ratio_records (
              provider text not null,
              site text not null,
              group_name text not null,
              page_rate real not null,
              updated_at text not null
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
            insert into upstream_adapter_status values (
              'Provider', 'example.com', 'public_pricing', 'ok',
              'groups=2', '2026-06-12T19:59:00+00:00'
            );
            insert into provider_group_ratio_records values
              ('Provider', 'example.com', 'new group', 0.02, '2026-06-12T19:59:00+00:00'),
              ('Provider', 'example.com', 'another group', 0.03, '2026-06-12T19:59:00+00:00');
            insert into browser_adapter_account_observations values (
              'Provider', 'example.com', 'example account', 'exampleaccount',
              'old api-only group', 0.01, 'example account / old api-only group / 0.01x',
              1, '2026-06-12T19:59:00+00:00'
            );
            """
        )
        path = Path("/tmp/fluter-cleanup-test.sqlite")
        try:
            if path.exists():
                path.unlink()
            self.write_db(conn, path)
            self.assertEqual([], self.mod.load_candidates(str(path), 36))
        finally:
            if path.exists():
                path.unlink()

    def test_misaligned_browser_account_row_does_not_confirm_old_group(self):
        conn = self.connection()
        self.insert_ledger(conn, "old api-only group")
        conn.executescript(
            """
            create table browser_adapter_status (
              provider text not null,
              site text not null,
              status text not null,
              detail text not null,
              observed_at text not null
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
            create table browser_adapter_rate_observations (
              provider text not null,
              site text not null,
              upstream_group text not null,
              page_rate real not null,
              source_line text not null,
              matched_ledger_rows integer not null,
              observed_at text not null
            );
            insert into browser_adapter_status values (
              'Provider', 'example.com', 'browser_observed',
              'Chrome Tampermonkey read-only snapshot; account_lines=1; rate_lines=2; script=0.1.15; wait_state=stable',
              '2026-06-12T19:59:00+00:00'
            );
            insert into browser_adapter_account_observations values (
              'Provider', 'example.com', 'example account', 'exampleaccount',
              'old api-only group', 0.01, 'example account / old api-only group / 0.01x',
              1, '2026-06-12T19:40:00+00:00'
            );
            insert into browser_adapter_rate_observations values
              ('Provider', 'example.com', 'new group', 0.02, 'key / new group / 0.02x', 0, '2026-06-12T19:59:00+00:00'),
              ('Provider', 'example.com', 'another group', 0.03, 'key / another group / 0.03x', 0, '2026-06-12T19:59:00+00:00');
            """
        )
        path = Path("/tmp/fluter-cleanup-test.sqlite")
        try:
            if path.exists():
                path.unlink()
            self.write_db(conn, path)
            candidates = self.mod.load_candidates(str(path), 36)
            self.assertEqual(1, len(candidates))
            self.assertEqual("old api-only group", candidates[0].upstream_group)
        finally:
            if path.exists():
                path.unlink()

    def test_public_pricing_alone_does_not_mark_account_group_removed(self):
        conn = self.connection()
        self.insert_ledger(conn, "old api-only group")
        conn.executescript(
            """
            create table upstream_adapter_status (
              provider text not null,
              site text not null,
              adapter_kind text not null,
              status text not null,
              detail text not null,
              observed_at text not null
            );
            create table provider_group_ratio_records (
              provider text not null,
              site text not null,
              group_name text not null,
              page_rate real not null,
              updated_at text not null
            );
            insert into upstream_adapter_status values (
              'Provider', 'example.com', 'public_pricing', 'ok',
              'groups=2', '2026-06-12T19:59:00+00:00'
            );
            insert into provider_group_ratio_records values
              ('Provider', 'example.com', 'new public group', 0.02, '2026-06-12T19:59:00+00:00'),
              ('Provider', 'example.com', 'another public group', 0.03, '2026-06-12T19:59:00+00:00');
            """
        )
        path = Path("/tmp/fluter-cleanup-test.sqlite")
        try:
            if path.exists():
                path.unlink()
            self.write_db(conn, path)
            self.assertEqual([], self.mod.load_candidates(str(path), 36))
        finally:
            if path.exists():
                path.unlink()

    def test_already_removed_row_is_reported_but_not_rewritten(self):
        conn = self.connection()
        self.insert_ledger(conn, "old group", status=self.mod.REMOVED_STATUS)
        conn.executescript(
            """
            create table browser_adapter_status (
              provider text not null,
              site text not null,
              status text not null,
              detail text not null,
              observed_at text not null
            );
            create table browser_adapter_rate_observations (
              provider text not null,
              site text not null,
              upstream_group text not null,
              page_rate real not null,
              source_line text not null,
              matched_ledger_rows integer not null,
              observed_at text not null
            );
            insert into browser_adapter_status values (
              'Provider', 'example.com', 'browser_observed', 'rate_lines=2',
              '2026-06-12T19:59:00+00:00'
            );
            insert into browser_adapter_rate_observations values
              ('Provider', 'example.com', 'new group', 0.02, 'key / new group / 0.02x', 0, '2026-06-12T19:59:00+00:00'),
              ('Provider', 'example.com', 'another group', 0.03, 'key / another group / 0.03x', 0, '2026-06-12T19:59:00+00:00');
            """
        )
        path = Path("/tmp/fluter-cleanup-test.sqlite")
        try:
            if path.exists():
                path.unlink()
            self.write_db(conn, path)
            candidates = self.mod.load_candidates(str(path), 36)
            self.assertEqual(1, len(candidates))
            self.assertFalse(candidates[0].needs_update)
            self.mod.apply_cleanup(str(path), candidates)
            check = sqlite3.connect(path)
            check.row_factory = sqlite3.Row
            cleaned = check.execute("select value from metadata where key = 'removed_upstream_groups_cleaned_count'").fetchone()
            reported = check.execute("select value from metadata where key = 'removed_upstream_groups_reported_count'").fetchone()
            self.assertEqual("0", cleaned["value"])
            self.assertEqual("1", reported["value"])
            check.close()
        finally:
            if path.exists():
                path.unlink()

    def test_short_group_token_does_not_hide_removed_candidate(self):
        conn = self.connection()
        self.insert_ledger(conn, "pro")
        conn.executescript(
            """
            create table browser_adapter_status (
              provider text not null,
              site text not null,
              status text not null,
              detail text not null,
              observed_at text not null
            );
            create table browser_adapter_rate_observations (
              provider text not null,
              site text not null,
              upstream_group text not null,
              page_rate real not null,
              source_line text not null,
              matched_ledger_rows integer not null,
              observed_at text not null
            );
            insert into browser_adapter_status values (
              'Provider', 'example.com', 'browser_observed',
              'Chrome Tampermonkey read-only snapshot; account_lines=2; rate_lines=2; script=0.1.15; wait_state=stable',
              '2026-06-12T19:59:00+00:00'
            );
            insert into browser_adapter_rate_observations values
              ('Provider', 'example.com', 'pro破限', 0.02, 'key / pro破限 / 0.02x', 0, '2026-06-12T19:59:00+00:00'),
              ('Provider', 'example.com', 'gpt-pro', 0.03, 'key / gpt-pro / 0.03x', 0, '2026-06-12T19:59:00+00:00');
            """
        )
        path = Path("/tmp/fluter-cleanup-test.sqlite")
        try:
            if path.exists():
                path.unlink()
            self.write_db(conn, path)
            candidates = self.mod.load_candidates(str(path), 36)
            self.assertEqual(1, len(candidates))
            self.assertEqual("pro", candidates[0].upstream_group)
        finally:
            if path.exists():
                path.unlink()


if __name__ == "__main__":
    unittest.main()
