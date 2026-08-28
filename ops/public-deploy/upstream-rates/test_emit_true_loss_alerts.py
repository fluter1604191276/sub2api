#!/usr/bin/env python3
from __future__ import annotations

import importlib.util
import json
import sqlite3
import sys
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("emit_true_loss_alerts.py")


def load_module():
    spec = importlib.util.spec_from_file_location("emit_true_loss_alerts", SCRIPT)
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class EmitTrueLossAlertsTest(unittest.TestCase):
    def setUp(self) -> None:
        self.mod = load_module()
        tmp = tempfile.NamedTemporaryFile(suffix=".sqlite", delete=False)
        tmp.close()
        self.db_path = tmp.name
        self.conn = sqlite3.connect(self.db_path)
        self.conn.row_factory = sqlite3.Row

    def tearDown(self) -> None:
        self.conn.close()
        Path(self.db_path).unlink(missing_ok=True)

    def create_schema(self) -> None:
        self.conn.executescript(
            """
            create table kbq_true_cost_audit_runs (
              id integer primary key autoincrement,
              observed_at text not null,
              hours integer not null,
              pricing_version text not null,
              request_count integer not null,
              bucket_count integer not null,
              user_billed_cost real not null,
              true_upstream_cost real not null,
              margin real not null,
              margin_percent real,
              real_loss_bucket_count integer not null,
              display_drift_bucket_count integer not null,
              missing_price_bucket_count integer not null,
              cache_creation_1h_tokens integer not null,
              source text not null,
              note text not null
            );
            create table kbq_true_cost_audit_buckets (
              id integer primary key autoincrement,
              run_id integer not null,
              status text not null,
              display_status text not null,
              account_id integer not null,
              account_name text not null,
              channel_id integer,
              channel_name text not null,
              group_id integer,
              group_name text not null,
              model text not null,
              upstream_model text not null,
              request_count integer not null,
              input_tokens integer not null,
              output_tokens integer not null,
              cache_read_tokens integer not null,
              cache_write_tokens integer not null,
              cache_creation_1h_tokens integer not null,
              user_billed_cost real not null,
              true_upstream_cost real,
              margin real,
              displayed_account_cost real not null,
              note text not null
            );
            create table metadata (key text primary key, value text not null);
            """
        )
        self.conn.commit()

    def insert_run(self, real_loss_bucket_count: int = 0) -> int:
        cur = self.conn.execute(
            """
            insert into kbq_true_cost_audit_runs (
              observed_at, hours, pricing_version, request_count, bucket_count,
              user_billed_cost, true_upstream_cost, margin, margin_percent,
              real_loss_bucket_count, display_drift_bucket_count,
              missing_price_bucket_count, cache_creation_1h_tokens, source, note
            ) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                "2026-06-16T01:00:00+00:00",
                24,
                "test",
                3,
                2,
                1.0,
                1.2 if real_loss_bucket_count else 0.8,
                -0.2 if real_loss_bucket_count else 0.2,
                -16.6 if real_loss_bucket_count else 25.0,
                real_loss_bucket_count,
                1,
                0,
                0,
                "https://xn--vduyey89e.com/api/pricing",
                "read-only audit",
            ),
        )
        self.conn.commit()
        return int(cur.lastrowid)

    def insert_loss_bucket(self, run_id: int, margin: float = -0.2) -> None:
        self.conn.execute(
            """
            insert into kbq_true_cost_audit_buckets (
              run_id, status, display_status, account_id, account_name,
              channel_id, channel_name, group_id, group_name, model,
              upstream_model, request_count, input_tokens, output_tokens,
              cache_read_tokens, cache_write_tokens, cache_creation_1h_tokens,
              user_billed_cost, true_upstream_cost, margin,
              displayed_account_cost, note
            ) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                run_id,
                "REAL_LOSS",
                "OK",
                7399,
                "KBQ claude kiro/anti高缓 0.36",
                12,
                "claude 高性价比渠道",
                34,
                "claude 高性价比",
                "claude-opus-4-8",
                "[kiro量高缓]claude-opus-4-8",
                3,
                100,
                20,
                10,
                5,
                0,
                1.0,
                1.2,
                margin,
                1.1,
                "test true loss",
            ),
        )
        self.conn.commit()

    def test_no_audit_is_info_payload(self):
        run, losses = self.mod.load_latest_audit(self.conn, 10)
        payload = self.mod.build_alert_payload(run, losses)

        self.assertEqual("NO_AUDIT", payload["status"])
        self.assertEqual([], payload["losses"])

    def test_ok_audit_does_not_send_even_with_endpoint(self):
        self.create_schema()
        self.insert_run(real_loss_bucket_count=0)
        run, losses = self.mod.load_latest_audit(self.conn, 10)
        payload = self.mod.build_alert_payload(run, losses)

        args = self.mod.parse_args(["--db", self.db_path, "--endpoint", "http://127.0.0.1:9999/alerts"])
        result = self.mod.maybe_send(self.conn, payload, args)

        self.assertEqual("OK", payload["status"])
        self.assertEqual("skipped_no_real_loss", result)

    def test_real_loss_payload_contains_only_real_loss_rows(self):
        self.create_schema()
        run_id = self.insert_run(real_loss_bucket_count=1)
        self.insert_loss_bucket(run_id)
        self.conn.execute(
            """
            insert into kbq_true_cost_audit_buckets (
              run_id, status, display_status, account_id, account_name,
              channel_id, channel_name, group_id, group_name, model,
              upstream_model, request_count, input_tokens, output_tokens,
              cache_read_tokens, cache_write_tokens, cache_creation_1h_tokens,
              user_billed_cost, true_upstream_cost, margin,
              displayed_account_cost, note
            ) values (?, 'OK', 'DISPLAY_DRIFT', 1, 'display-only', null, '', null, '',
                      'm', 'm', 1, 0, 0, 0, 0, 0, 1.0, 0.5, 0.5, 10, 'not a loss')
            """,
            (run_id,),
        )
        self.conn.commit()

        run, losses = self.mod.load_latest_audit(self.conn, 10)
        payload = self.mod.build_alert_payload(run, losses)

        self.assertEqual("REAL_LOSS", payload["status"])
        self.assertEqual(1, len(payload["losses"]))
        self.assertEqual("[kiro量高缓]claude-opus-4-8", payload["losses"][0]["upstream_model"])
        self.assertAlmostEqual(0.2, payload["losses"][0]["loss_amount"])
        self.assertIn("DISPLAY_DRIFT", self.mod.render_markdown(payload))

    def test_duplicate_run_is_not_sent_twice(self):
        self.create_schema()
        run_id = self.insert_run(real_loss_bucket_count=1)
        self.insert_loss_bucket(run_id)
        self.conn.execute(
            "insert into metadata(key, value) values (?, ?)",
            (self.mod.SENT_RUN_ID_KEY, str(run_id)),
        )
        self.conn.commit()
        run, losses = self.mod.load_latest_audit(self.conn, 10)
        payload = self.mod.build_alert_payload(run, losses)

        args = self.mod.parse_args(["--db", self.db_path, "--endpoint", "http://127.0.0.1:9999/alerts"])
        result = self.mod.maybe_send(self.conn, payload, args)

        self.assertEqual("skipped_duplicate_run", result)

    def test_endpoint_must_be_loopback(self):
        with self.assertRaises(ValueError):
            self.mod.validate_loopback_endpoint("https://example.com/alerts")
        with self.assertRaises(ValueError):
            self.mod.validate_loopback_endpoint("http://user:pass@127.0.0.1/alerts")
        self.assertEqual(
            "http://127.0.0.1:8752/alerts",
            self.mod.validate_loopback_endpoint("http://127.0.0.1:8752/alerts"),
        )

    def test_main_json_output_is_parseable(self):
        self.create_schema()
        run_id = self.insert_run(real_loss_bucket_count=1)
        self.insert_loss_bucket(run_id)
        old_stdout = sys.stdout
        try:
            from io import StringIO

            buf = StringIO()
            sys.stdout = buf
            rc = self.mod.main(["--db", self.db_path, "--json", "--dry-run"])
            output = buf.getvalue()
        finally:
            sys.stdout = old_stdout

        self.assertEqual(0, rc)
        json_text = output.split("\nalert_result=", 1)[0]
        parsed = json.loads(json_text)
        self.assertEqual("REAL_LOSS", parsed["status"])


if __name__ == "__main__":
    unittest.main()
