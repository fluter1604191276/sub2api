#!/usr/bin/env python3
from __future__ import annotations

import importlib.util
import sqlite3
import sys
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("compare_ledger_with_site_truth.py")


def load_module():
    spec = importlib.util.spec_from_file_location("compare_ledger_with_site_truth", SCRIPT)
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class CompareLedgerWithSiteTruthTest(unittest.TestCase):
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
              status text not null
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

    def insert_ledger(self, conn: sqlite3.Connection, **overrides) -> None:
        values = {
            "category": "Provider",
            "kind": "Codex",
            "site": "example.com",
            "fluter_account_name": "example codex",
            "upstream_group": "gpt",
            "page_rate": 0.008,
            "recharge_factor": 1,
            "site_account_multiplier": 0.03,
            "status": "已确认",
        }
        values.update(overrides)
        conn.execute(
            """
            insert into upstream_rate_records (
              category, kind, site, fluter_account_name, upstream_group,
              page_rate, recharge_factor, site_account_multiplier, status
            ) values (
              :category, :kind, :site, :fluter_account_name, :upstream_group,
              :page_rate, :recharge_factor, :site_account_multiplier, :status
            )
            """,
            values,
        )

    def test_seeded_lower_value_needs_fresh_source(self):
        conn = self.connection()
        self.insert_ledger(conn)
        row = conn.execute("select * from upstream_rate_records").fetchone()
        source = self.mod.source_for_row(conn, row)
        mark = self.mod.verdict(
            self.mod.Decimal("0.008"),
            self.mod.Decimal("0.03"),
            self.mod.Decimal("0.001"),
            source,
            self.mod.latest_metadata_dt(conn, "last_orchestrated_refresh_at"),
        )

        self.assertEqual("NEEDS_FRESH_SOURCE", mark)

    def test_reliable_lower_value_is_conservative_review(self):
        conn = self.connection()
        self.insert_ledger(conn)
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
              'Chrome Tampermonkey read-only snapshot; rate_lines=1; script=0.1.15; wait_state=stable',
              '2026-06-12T19:59:00+00:00'
            );
            insert into browser_adapter_rate_observations values (
              'Provider', 'example.com', 'gpt', 0.008, 'key / gpt / 0.008x', 1,
              '2026-06-12T19:59:00+00:00'
            );
            """
        )
        row = conn.execute("select * from upstream_rate_records").fetchone()
        source = self.mod.source_for_row(conn, row)
        mark = self.mod.verdict(
            self.mod.Decimal("0.008"),
            self.mod.Decimal("0.03"),
            self.mod.Decimal("0.001"),
            source,
            self.mod.latest_metadata_dt(conn, "last_orchestrated_refresh_at"),
        )

        self.assertEqual("SITE_CONSERVATIVE_REVIEW", mark)

    def test_upstream_hub_group_match_is_high_confidence_source(self):
        conn = self.connection()
        self.insert_ledger(conn, upstream_group="gpt", page_rate=0.008, site_account_multiplier=0.03)
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
            insert into upstream_hub_rate_observations values (
              1, 'Provider', 'example.com', 'gpt', '', 0.008, 1,
              '2026-06-12T19:00:00+00:00',
              '2026-06-12T19:59:00+00:00',
              '2026-06-12T19:59:00+00:00'
            );
            """
        )
        row = conn.execute("select * from upstream_rate_records").fetchone()
        source = self.mod.source_for_row(conn, row)
        mark = self.mod.verdict(
            self.mod.Decimal("0.008"),
            self.mod.Decimal("0.03"),
            self.mod.Decimal("0.001"),
            source,
            self.mod.latest_metadata_dt(conn, "last_orchestrated_refresh_at"),
        )

        self.assertEqual("upstream_hub", source.kind)
        self.assertEqual("high", source.confidence)
        self.assertEqual("SITE_CONSERVATIVE_REVIEW", mark)

    def test_upstream_hub_missing_group_is_removed_when_inventory_is_complete(self):
        conn = self.connection()
        self.insert_ledger(conn, upstream_group="old group", page_rate=0.008, site_account_multiplier=0.03)
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
        row = conn.execute("select * from upstream_rate_records").fetchone()
        source = self.mod.source_for_row(conn, row)
        mark = self.mod.verdict(
            self.mod.Decimal("0.008"),
            self.mod.Decimal("0.03"),
            self.mod.Decimal("0.001"),
            source,
            self.mod.latest_metadata_dt(conn, "last_orchestrated_refresh_at"),
        )

        self.assertEqual("upstream_hub_group_missing", source.kind)
        self.assertEqual("UPSTREAM_GROUP_REMOVED", mark)

    def test_short_group_tokens_require_exact_match(self):
        self.assertTrue(self.mod.match_group("gpt", "GPT"))
        self.assertTrue(self.mod.match_group("pro", "PRO"))
        self.assertFalse(self.mod.match_group("gpt", "gpt-pro"))
        self.assertFalse(self.mod.match_group("pro", "pro破限"))
        self.assertFalse(self.mod.match_group("ag", "稳定AG量"))

    def test_long_group_names_still_allow_containment_match(self):
        self.assertTrue(self.mod.match_group("GPT-PRO纯享号池", "GPT PRO 纯享"))
        self.assertTrue(self.mod.match_group("CC Max 限制客户端 无上限并发", "ccmax限制客户端"))

    def test_stale_browser_rate_observation_is_not_reliable_even_when_status_is_current(self):
        conn = self.connection()
        self.insert_ledger(conn)
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
              'Chrome Tampermonkey read-only snapshot; rate_lines=1; script=0.1.15; wait_state=stable',
              '2026-06-12T19:59:00+00:00'
            );
            insert into browser_adapter_rate_observations values (
              'Provider', 'example.com', 'gpt', 0.008, 'key / gpt / 0.008x', 1,
              '2026-06-10T19:59:00+00:00'
            );
            """
        )
        row = conn.execute("select * from upstream_rate_records").fetchone()
        source = self.mod.source_for_row(conn, row)
        mark = self.mod.verdict(
            self.mod.Decimal("0.008"),
            self.mod.Decimal("0.03"),
            self.mod.Decimal("0.001"),
            source,
            self.mod.latest_metadata_dt(conn, "last_orchestrated_refresh_at"),
        )

        self.assertEqual("browser_capture_incomplete", source.kind)
        self.assertEqual("BROWSER_CAPTURE_INCOMPLETE", mark)

    def test_misaligned_browser_rate_observation_is_not_reliable_even_when_both_are_recent(self):
        conn = self.connection()
        self.insert_ledger(conn)
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
              'Chrome Tampermonkey read-only snapshot; rate_lines=1; script=0.1.15; wait_state=stable',
              '2026-06-12T19:59:00+00:00'
            );
            insert into browser_adapter_rate_observations values (
              'Provider', 'example.com', 'gpt', 0.008, 'key / gpt / 0.008x', 1,
              '2026-06-12T19:40:00+00:00'
            );
            """
        )
        row = conn.execute("select * from upstream_rate_records").fetchone()
        source = self.mod.source_for_row(conn, row)
        mark = self.mod.verdict(
            self.mod.Decimal("0.008"),
            self.mod.Decimal("0.03"),
            self.mod.Decimal("0.001"),
            source,
            self.mod.latest_metadata_dt(conn, "last_orchestrated_refresh_at"),
        )

        self.assertEqual("browser_capture_incomplete", source.kind)
        self.assertEqual("BROWSER_CAPTURE_INCOMPLETE", mark)

    def test_stale_browser_account_observation_is_not_reliable_even_when_status_is_current(self):
        conn = self.connection()
        self.insert_ledger(
            conn,
            fluter_account_name="example codex 0.008",
            page_rate=0.008,
            site_account_multiplier=0.03,
        )
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
            insert into browser_adapter_status values (
              'Provider', 'example.com', 'browser_observed',
              'Chrome Tampermonkey read-only snapshot; account_lines=1; script=0.1.15; wait_state=stable',
              '2026-06-12T19:59:00+00:00'
            );
            insert into browser_adapter_account_observations values (
              'Provider', 'example.com', 'example codex 0.008', 'examplecodex0.008',
              'gpt', 0.008, 'example codex 0.008 / gpt / 0.008x', 1,
              '2026-06-10T19:59:00+00:00'
            );
            """
        )
        row = conn.execute("select * from upstream_rate_records").fetchone()
        source = self.mod.source_for_row(conn, row)
        mark = self.mod.verdict(
            self.mod.Decimal("0.008"),
            self.mod.Decimal("0.03"),
            self.mod.Decimal("0.001"),
            source,
            self.mod.latest_metadata_dt(conn, "last_orchestrated_refresh_at"),
        )

        self.assertEqual("browser_capture_incomplete", source.kind)
        self.assertEqual("BROWSER_CAPTURE_INCOMPLETE", mark)

    def test_misaligned_browser_account_observation_is_not_reliable_even_when_both_are_recent(self):
        conn = self.connection()
        self.insert_ledger(
            conn,
            fluter_account_name="example codex 0.008",
            page_rate=0.008,
            site_account_multiplier=0.03,
        )
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
            insert into browser_adapter_status values (
              'Provider', 'example.com', 'browser_observed',
              'Chrome Tampermonkey read-only snapshot; account_lines=1; script=0.1.15; wait_state=stable',
              '2026-06-12T19:59:00+00:00'
            );
            insert into browser_adapter_account_observations values (
              'Provider', 'example.com', 'example codex 0.008', 'examplecodex0.008',
              'gpt', 0.008, 'example codex 0.008 / gpt / 0.008x', 1,
              '2026-06-12T19:40:00+00:00'
            );
            """
        )
        row = conn.execute("select * from upstream_rate_records").fetchone()
        source = self.mod.source_for_row(conn, row)
        mark = self.mod.verdict(
            self.mod.Decimal("0.008"),
            self.mod.Decimal("0.03"),
            self.mod.Decimal("0.001"),
            source,
            self.mod.latest_metadata_dt(conn, "last_orchestrated_refresh_at"),
        )

        self.assertEqual("browser_capture_incomplete", source.kind)
        self.assertEqual("BROWSER_CAPTURE_INCOMPLETE", mark)

    def test_missing_group_in_fresh_browser_snapshot_is_treated_as_removed_group(self):
        conn = self.connection()
        self.insert_ledger(conn, upstream_group="old group")
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
        row = conn.execute("select * from upstream_rate_records").fetchone()
        source = self.mod.source_for_row(conn, row)
        mark = self.mod.verdict(
            self.mod.Decimal("0.008"),
            self.mod.Decimal("0.03"),
            self.mod.Decimal("0.001"),
            source,
            self.mod.latest_metadata_dt(conn, "last_orchestrated_refresh_at"),
        )

        self.assertEqual("UPSTREAM_GROUP_REMOVED", mark)

    def test_missing_group_in_misaligned_browser_rows_needs_fresh_source(self):
        conn = self.connection()
        self.insert_ledger(conn, upstream_group="old group")
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
        row = conn.execute("select * from upstream_rate_records").fetchone()
        source = self.mod.source_for_row(conn, row)
        mark = self.mod.verdict(
            self.mod.Decimal("0.008"),
            self.mod.Decimal("0.03"),
            self.mod.Decimal("0.001"),
            source,
            self.mod.latest_metadata_dt(conn, "last_orchestrated_refresh_at"),
        )

        self.assertEqual("BROWSER_CAPTURE_INCOMPLETE", mark)

    def test_missing_group_in_stale_browser_snapshot_needs_fresh_source(self):
        conn = self.connection()
        self.insert_ledger(conn, upstream_group="old group")
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
              '2026-06-10T19:59:00+00:00'
            );
            insert into browser_adapter_rate_observations values
              ('Provider', 'example.com', 'new group', 0.02, 'key / new group / 0.02x', 0, '2026-06-10T19:59:00+00:00'),
              ('Provider', 'example.com', 'another group', 0.03, 'key / another group / 0.03x', 0, '2026-06-10T19:59:00+00:00');
            """
        )
        row = conn.execute("select * from upstream_rate_records").fetchone()
        source = self.mod.source_for_row(conn, row)
        mark = self.mod.verdict(
            self.mod.Decimal("0.008"),
            self.mod.Decimal("0.03"),
            self.mod.Decimal("0.001"),
            source,
            self.mod.latest_metadata_dt(conn, "last_orchestrated_refresh_at"),
        )

        self.assertEqual("browser_stale_snapshot", source.kind)
        self.assertEqual("NEEDS_FRESH_SOURCE", mark)

    def test_preserved_snapshot_reason_wins_over_stale_timing(self):
        conn = self.connection()
        self.insert_ledger(conn, upstream_group="old group")
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
              'latest read was empty; preserved previous non-empty snapshot from 2026-06-10T19:00:00+00:00; script=0.1.15',
              '2026-06-10T19:59:00+00:00'
            );
            insert into browser_adapter_rate_observations values
              ('Provider', 'example.com', 'new group', 0.02, 'key / new group / 0.02x', 0, '2026-06-10T19:59:00+00:00'),
              ('Provider', 'example.com', 'another group', 0.03, 'key / another group / 0.03x', 0, '2026-06-10T19:59:00+00:00');
            """
        )
        row = conn.execute("select * from upstream_rate_records").fetchone()
        source = self.mod.source_for_row(conn, row)
        mark = self.mod.verdict(
            self.mod.Decimal("0.008"),
            self.mod.Decimal("0.03"),
            self.mod.Decimal("0.001"),
            source,
            self.mod.latest_metadata_dt(conn, "last_orchestrated_refresh_at"),
        )

        self.assertEqual("browser_preserved_snapshot", source.kind)
        self.assertEqual("NEEDS_FRESH_SOURCE", mark)

    def test_single_browser_group_is_capture_incomplete_not_removed(self):
        conn = self.connection()
        self.insert_ledger(conn, upstream_group="old group")
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
        row = conn.execute("select * from upstream_rate_records").fetchone()
        source = self.mod.source_for_row(conn, row)
        mark = self.mod.verdict(
            self.mod.Decimal("0.008"),
            self.mod.Decimal("0.03"),
            self.mod.Decimal("0.001"),
            source,
            self.mod.latest_metadata_dt(conn, "last_orchestrated_refresh_at"),
        )

        self.assertEqual("BROWSER_CAPTURE_INCOMPLETE", mark)

    def test_public_pricing_previous_success_is_marked_refresh_failed_after_latest_failure(self):
        conn = self.connection()
        self.insert_ledger(conn, site="pricing.example.com", upstream_group="plus", page_rate=0.08, site_account_multiplier=0.08)
        conn.executescript(
            """
            create table provider_group_ratio_records (
              provider text not null,
              site text not null,
              group_name text not null,
              page_rate real not null,
              updated_at text not null
            );
            create table upstream_adapter_status (
              provider text not null,
              site text not null,
              adapter_kind text not null,
              status text not null,
              detail text not null,
              observed_at text not null
            );
            insert into provider_group_ratio_records values (
              'Provider', 'pricing.example.com', 'plus', 0.08, '2026-06-12T18:00:00+00:00'
            );
            insert into upstream_adapter_status values (
              'Provider', 'pricing.example.com', 'public_pricing', 'failed',
              'HTTP Error 522', '2026-06-12T20:00:00+00:00'
            );
            """
        )
        row = conn.execute("select * from upstream_rate_records").fetchone()
        source = self.mod.source_for_row(conn, row)
        mark = self.mod.verdict(
            self.mod.Decimal("0.08"),
            self.mod.Decimal("0.08"),
            self.mod.Decimal("0.001"),
            source,
            self.mod.latest_metadata_dt(conn, "last_orchestrated_refresh_at"),
        )

        self.assertEqual("PUBLIC_PRICING_REFRESH_FAILED", mark)
        self.assertEqual("public_pricing_stale_after_failure", source.kind)

    def test_public_pricing_missing_group_needs_browser_confirmation(self):
        conn = self.connection()
        self.insert_ledger(
            conn,
            site="pricing.example.com",
            upstream_group="old account-only group",
            page_rate=0.08,
            site_account_multiplier=0.08,
        )
        conn.executescript(
            """
            create table provider_group_ratio_records (
              provider text not null,
              site text not null,
              group_name text not null,
              page_rate real not null,
              updated_at text not null
            );
            create table upstream_adapter_status (
              provider text not null,
              site text not null,
              adapter_kind text not null,
              status text not null,
              detail text not null,
              observed_at text not null
            );
            insert into provider_group_ratio_records values
              ('Provider', 'pricing.example.com', 'new public group', 0.08, '2026-06-12T19:59:00+00:00'),
              ('Provider', 'pricing.example.com', 'another public group', 0.10, '2026-06-12T19:59:00+00:00');
            insert into upstream_adapter_status values (
              'Provider', 'pricing.example.com', 'public_pricing', 'ok',
              'groups=2', '2026-06-12T19:59:00+00:00'
            );
            """
        )
        row = conn.execute("select * from upstream_rate_records").fetchone()
        source = self.mod.source_for_row(conn, row)
        mark = self.mod.verdict(
            self.mod.Decimal("0.08"),
            self.mod.Decimal("0.08"),
            self.mod.Decimal("0.001"),
            source,
            self.mod.latest_metadata_dt(conn, "last_orchestrated_refresh_at"),
        )

        self.assertEqual("public_group_missing", source.kind)
        self.assertEqual("NEEDS_FRESH_SOURCE", mark)

    def test_legacy_tampermonkey_snapshot_is_not_reliable_source(self):
        conn = self.connection()
        self.insert_ledger(conn, upstream_group="old group")
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
        row = conn.execute("select * from upstream_rate_records").fetchone()
        source = self.mod.source_for_row(conn, row)
        mark = self.mod.verdict(
            self.mod.Decimal("0.008"),
            self.mod.Decimal("0.03"),
            self.mod.Decimal("0.001"),
            source,
            self.mod.latest_metadata_dt(conn, "last_orchestrated_refresh_at"),
        )

        self.assertEqual("browser_legacy_script", source.kind)
        self.assertEqual("NEEDS_FRESH_SOURCE", mark)

    def test_preserved_rate_lines_are_not_reliable_source(self):
        conn = self.connection()
        self.insert_ledger(conn, upstream_group="old group")
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
        row = conn.execute("select * from upstream_rate_records").fetchone()
        source = self.mod.source_for_row(conn, row)
        mark = self.mod.verdict(
            self.mod.Decimal("0.008"),
            self.mod.Decimal("0.03"),
            self.mod.Decimal("0.001"),
            source,
            self.mod.latest_metadata_dt(conn, "last_orchestrated_refresh_at"),
        )

        self.assertEqual("browser_capture_incomplete", source.kind)
        self.assertEqual("BROWSER_CAPTURE_INCOMPLETE", mark)

    def test_preserved_non_empty_snapshot_is_not_reliable_source(self):
        conn = self.connection()
        self.insert_ledger(conn, upstream_group="old group")
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
        row = conn.execute("select * from upstream_rate_records").fetchone()
        source = self.mod.source_for_row(conn, row)
        mark = self.mod.verdict(
            self.mod.Decimal("0.008"),
            self.mod.Decimal("0.03"),
            self.mod.Decimal("0.001"),
            source,
            self.mod.latest_metadata_dt(conn, "last_orchestrated_refresh_at"),
        )

        self.assertEqual("browser_preserved_snapshot", source.kind)
        self.assertEqual("NEEDS_FRESH_SOURCE", mark)


if __name__ == "__main__":
    unittest.main()
