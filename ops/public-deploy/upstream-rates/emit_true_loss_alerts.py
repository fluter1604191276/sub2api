#!/usr/bin/env python3
"""Emit explicit KBQ true-loss alerts from the upstream ledger.

This script reads only the independent upstream-rates SQLite database. It does
not recalculate costs, read production credentials, or modify sub2api
production data. The source of truth is the latest audit written by
audit_kbq_true_costs.py.

By default it is a dry-run reporter. It sends a POST only when an explicit
loopback endpoint is provided and the latest audit has REAL_LOSS buckets.
"""

from __future__ import annotations

import argparse
import json
import sqlite3
import sys
import urllib.error
import urllib.parse
import urllib.request
from datetime import datetime, timezone
from pathlib import Path
from typing import Any


DEFAULT_DB = "/var/lib/fluterapi-upstream-rates/upstream_rates.sqlite"
EVENT_TYPE = "fluter.kbq_true_loss"
SENT_RUN_ID_KEY = "kbq_true_loss_alert_sent_run_id"
SENT_AT_KEY = "kbq_true_loss_alert_sent_at"


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Emit dry-run or local OpenClaw alerts for KBQ REAL_LOSS buckets")
    parser.add_argument("--db", default=DEFAULT_DB)
    parser.add_argument(
        "--endpoint",
        default="",
        help="Explicit local OpenClaw endpoint, for example http://127.0.0.1:8765/alerts",
    )
    parser.add_argument("--timeout", type=int, default=10)
    parser.add_argument("--limit", type=int, default=10, help="Maximum REAL_LOSS buckets included in the alert")
    parser.add_argument("--json", action="store_true", help="Print the alert payload as JSON")
    parser.add_argument("--dry-run", action="store_true", help="Never POST, even when --endpoint is provided")
    parser.add_argument("--force", action="store_true", help="Re-send even if this audit run was already alerted")
    parser.add_argument(
        "--fail-soft",
        action="store_true",
        help="Return 0 on endpoint delivery errors after printing a warning",
    )
    return parser.parse_args(argv)


def table_exists(conn: sqlite3.Connection, table_name: str) -> bool:
    row = conn.execute(
        "select 1 from sqlite_master where type = 'table' and name = ?",
        (table_name,),
    ).fetchone()
    return row is not None


def number_or_none(value: Any) -> float | None:
    if value is None or value == "":
        return None
    try:
        return float(value)
    except (TypeError, ValueError):
        return None


def money(value: Any) -> str:
    number = number_or_none(value)
    if number is None:
        return "-"
    return f"${number:.8f}".rstrip("0").rstrip(".")


def loss_amount(row: dict[str, Any]) -> float:
    true_cost = number_or_none(row.get("true_upstream_cost")) or 0.0
    user_billed = number_or_none(row.get("user_billed_cost")) or 0.0
    return max(0.0, true_cost - user_billed)


def load_latest_audit(conn: sqlite3.Connection, limit: int) -> tuple[dict[str, Any] | None, list[dict[str, Any]]]:
    if not table_exists(conn, "kbq_true_cost_audit_runs") or not table_exists(conn, "kbq_true_cost_audit_buckets"):
        return None, []
    run = conn.execute(
        "select * from kbq_true_cost_audit_runs order by id desc limit 1"
    ).fetchone()
    if not run:
        return None, []
    rows = conn.execute(
        """
        select *
        from kbq_true_cost_audit_buckets
        where run_id = ?
          and status = 'REAL_LOSS'
        order by
          (coalesce(true_upstream_cost, 0) - coalesce(user_billed_cost, 0)) desc,
          margin asc
        limit ?
        """,
        (run["id"], int(limit)),
    ).fetchall()
    return dict(run), [dict(row) for row in rows]


def build_alert_payload(run: dict[str, Any] | None, losses: list[dict[str, Any]]) -> dict[str, Any]:
    observed_at = datetime.now(timezone.utc).isoformat(timespec="seconds")
    if run is None:
        return {
            "event_type": EVENT_TYPE,
            "severity": "info",
            "status": "NO_AUDIT",
            "observed_at": observed_at,
            "title": "KBQ 真实成本审计暂无记录",
            "summary": "未找到 kbq_true_cost_audit_runs；需要先运行 audit_kbq_true_costs.py。",
            "audit": None,
            "losses": [],
        }

    total_loss = sum(loss_amount(row) for row in losses)
    real_loss_count = int(run.get("real_loss_bucket_count") or 0)
    status = "REAL_LOSS" if real_loss_count > 0 else "OK"
    severity = "critical" if status == "REAL_LOSS" else "info"
    title = (
        f"KBQ 真实成本真倒挂：{real_loss_count} 个桶，亏损约 {money(total_loss)}"
        if status == "REAL_LOSS"
        else "KBQ 真实成本审计正常：未发现真倒挂"
    )
    return {
        "event_type": EVENT_TYPE,
        "severity": severity,
        "status": status,
        "observed_at": observed_at,
        "title": title,
        "summary": (
            f"审计 run #{run['id']}，窗口 {run['hours']}h，"
            f"用户扣费 {money(run['user_billed_cost'])}，真实成本 {money(run['true_upstream_cost'])}，"
            f"利润 {money(run['margin'])}，REAL_LOSS {real_loss_count}。"
        ),
        "audit": {
            "run_id": run["id"],
            "audit_observed_at": run["observed_at"],
            "hours": run["hours"],
            "pricing_version": run["pricing_version"],
            "request_count": run["request_count"],
            "bucket_count": run["bucket_count"],
            "user_billed_cost": number_or_none(run["user_billed_cost"]),
            "true_upstream_cost": number_or_none(run["true_upstream_cost"]),
            "margin": number_or_none(run["margin"]),
            "margin_percent": number_or_none(run["margin_percent"]),
            "real_loss_bucket_count": real_loss_count,
            "display_drift_bucket_count": int(run.get("display_drift_bucket_count") or 0),
            "missing_price_bucket_count": int(run.get("missing_price_bucket_count") or 0),
            "source": run["source"],
        },
        "losses": [
            {
                "account_id": row["account_id"],
                "account_name": row["account_name"],
                "channel_id": row["channel_id"],
                "channel_name": row["channel_name"],
                "group_id": row["group_id"],
                "group_name": row["group_name"],
                "model": row["model"],
                "upstream_model": row["upstream_model"],
                "request_count": row["request_count"],
                "user_billed_cost": number_or_none(row["user_billed_cost"]),
                "true_upstream_cost": number_or_none(row["true_upstream_cost"]),
                "margin": number_or_none(row["margin"]),
                "loss_amount": loss_amount(row),
                "note": row["note"],
            }
            for row in losses
        ],
    }


def render_markdown(payload: dict[str, Any]) -> str:
    lines = [
        f"# {payload['title']}",
        "",
        f"- 状态：`{payload['status']}`",
        f"- 说明：{payload['summary']}",
    ]
    audit = payload.get("audit")
    if audit:
        lines.extend(
            [
                f"- 审计来源：{audit['source']}",
                f"- DISPLAY_DRIFT：{audit['display_drift_bucket_count']}（展示口径漂移，不等于真亏）",
                f"- NO_PRICE：{audit['missing_price_bucket_count']}（需要人工核对价格表）",
            ]
        )
    if payload["losses"]:
        lines.extend(["", "## REAL_LOSS 明细"])
        for index, row in enumerate(payload["losses"], start=1):
            lines.append(
                "{idx}. #{account_id} {account_name} / {group_name} / {upstream_model}: "
                "用户扣费 {user_billed}，真实成本 {true_cost}，差额 {loss}，请求 {requests}".format(
                    idx=index,
                    account_id=row["account_id"],
                    account_name=row["account_name"],
                    group_name=row["group_name"] or "-",
                    upstream_model=row["upstream_model"] or row["model"] or "-",
                    user_billed=money(row["user_billed_cost"]),
                    true_cost=money(row["true_upstream_cost"]),
                    loss=money(row["loss_amount"]),
                    requests=row["request_count"],
                )
            )
    return "\n".join(lines)


def validate_loopback_endpoint(endpoint: str) -> str:
    parsed = urllib.parse.urlparse(endpoint)
    if parsed.scheme not in {"http", "https"}:
        raise ValueError("alert endpoint must be http(s)")
    if parsed.hostname not in {"127.0.0.1", "localhost", "::1"}:
        raise ValueError("alert endpoint must be loopback-only")
    if parsed.username or parsed.password or parsed.query or parsed.fragment:
        raise ValueError("alert endpoint must not contain credentials, query, or fragment")
    return endpoint


def metadata_value(conn: sqlite3.Connection, key: str) -> str | None:
    if not table_exists(conn, "metadata"):
        return None
    row = conn.execute("select value from metadata where key = ?", (key,)).fetchone()
    return str(row["value"]) if row else None


def mark_alert_sent(conn: sqlite3.Connection, run_id: int, observed_at: str) -> None:
    with conn:
        conn.execute("create table if not exists metadata (key text primary key, value text not null)")
        conn.execute(
            "insert or replace into metadata(key, value) values (?, ?)",
            (SENT_RUN_ID_KEY, str(run_id)),
        )
        conn.execute(
            "insert or replace into metadata(key, value) values (?, ?)",
            (SENT_AT_KEY, observed_at),
        )


def post_json(endpoint: str, payload: dict[str, Any], timeout: int) -> int:
    data = json.dumps(payload, ensure_ascii=False).encode("utf-8")
    request = urllib.request.Request(
        endpoint,
        data=data,
        headers={
            "Content-Type": "application/json; charset=utf-8",
            "User-Agent": "fluter-upstream-rates-true-loss-alert/1.0",
        },
        method="POST",
    )
    with urllib.request.urlopen(request, timeout=timeout) as response:
        response.read(4096)
        return int(response.status)


def maybe_send(conn: sqlite3.Connection, payload: dict[str, Any], args: argparse.Namespace) -> str:
    if not args.endpoint:
        return "dry_run_no_endpoint"
    endpoint = validate_loopback_endpoint(args.endpoint)
    if args.dry_run:
        return "dry_run_requested"
    if payload["status"] != "REAL_LOSS":
        return "skipped_no_real_loss"

    run_id = int(payload["audit"]["run_id"])
    if not args.force and metadata_value(conn, SENT_RUN_ID_KEY) == str(run_id):
        return "skipped_duplicate_run"

    status_code = post_json(endpoint, payload, args.timeout)
    mark_alert_sent(conn, run_id, payload["observed_at"])
    return f"sent_http_{status_code}"


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)
    path = Path(args.db)
    conn = sqlite3.connect(path)
    conn.row_factory = sqlite3.Row
    try:
        run, losses = load_latest_audit(conn, args.limit)
        payload = build_alert_payload(run, losses)
        markdown = render_markdown(payload)
        if args.json:
            print(json.dumps(payload, ensure_ascii=False, indent=2))
        else:
            print(markdown)

        try:
            result = maybe_send(conn, payload, args)
        except (OSError, ValueError, urllib.error.URLError, RuntimeError) as exc:
            print(f"alert delivery failed: {exc}", file=sys.stderr)
            return 0 if args.fail_soft else 1
        print(f"alert_result={result}")
        return 0
    finally:
        conn.close()


if __name__ == "__main__":
    raise SystemExit(main())
