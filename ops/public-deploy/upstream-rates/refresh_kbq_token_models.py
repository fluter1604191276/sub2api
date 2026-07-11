#!/usr/bin/env python3
"""Refresh KBQ token-priced and Claude per-call model costs into SQLite.

This script reads KBQ's public /api/pricing data. It does not use or store API
keys. It stores token-priced Claude/Codex models and Claude quota_type=1
per-call models separately so the dashboard does not mix token multipliers with
per-call prices.
"""

from __future__ import annotations

import argparse
import json
import sqlite3
import urllib.request
from datetime import datetime, timezone
from decimal import Decimal, InvalidOperation
from pathlib import Path
from typing import Any


DEFAULT_DB = "/var/lib/fluterapi-upstream-rates/upstream_rates.sqlite"
DEFAULT_URL = "https://xn--vduyey89e.com/api/pricing"
DEFAULT_RECHARGE_FACTOR = 0.9


SCHEMA = """
create table if not exists kbq_token_model_records (
  id integer primary key autoincrement,
  category text not null,
  model_name text not null,
  base_model text not null,
  cost_multiplier real not null,
  endpoints text not null,
  input_usd_per_1m real,
  output_usd_per_1m real,
  cache_read_usd_per_1m real,
  cache_write_usd_per_1m real,
  raw_model_ratio real,
  official_input_usd_per_1m real,
  official_output_usd_per_1m real,
  official_cache_read_usd_per_1m real,
  official_cache_write_usd_per_1m real,
  official_label text not null,
  pricing_version text not null,
  source_url text not null,
  note text not null,
  updated_at text not null,
  unique(category, model_name)
);

create table if not exists kbq_per_call_model_records (
  id integer primary key autoincrement,
  category text not null,
  model_name text not null,
  base_model text not null,
  per_call_price real not null,
  effective_per_call_cost real not null,
  recharge_factor real not null,
  endpoints text not null,
  tags text not null,
  description text not null,
  pricing_version text not null,
  source_url text not null,
  note text not null,
  updated_at text not null,
  unique(category, model_name)
);

create table if not exists metadata (
  key text primary key,
  value text not null
);
"""


UPSERT = """
insert into kbq_token_model_records (
  category, model_name, base_model, cost_multiplier, endpoints,
  input_usd_per_1m, output_usd_per_1m, cache_read_usd_per_1m,
  cache_write_usd_per_1m, raw_model_ratio,
  official_input_usd_per_1m, official_output_usd_per_1m,
  official_cache_read_usd_per_1m, official_cache_write_usd_per_1m,
  official_label, pricing_version, source_url, note, updated_at
) values (
  :category, :model_name, :base_model, :cost_multiplier, :endpoints,
  :input_usd_per_1m, :output_usd_per_1m, :cache_read_usd_per_1m,
  :cache_write_usd_per_1m, :raw_model_ratio,
  :official_input_usd_per_1m, :official_output_usd_per_1m,
  :official_cache_read_usd_per_1m, :official_cache_write_usd_per_1m,
  :official_label, :pricing_version, :source_url, :note, :updated_at
)
on conflict(category, model_name) do update set
  base_model = excluded.base_model,
  cost_multiplier = excluded.cost_multiplier,
  endpoints = excluded.endpoints,
  input_usd_per_1m = excluded.input_usd_per_1m,
  output_usd_per_1m = excluded.output_usd_per_1m,
  cache_read_usd_per_1m = excluded.cache_read_usd_per_1m,
  cache_write_usd_per_1m = excluded.cache_write_usd_per_1m,
  raw_model_ratio = excluded.raw_model_ratio,
  official_input_usd_per_1m = excluded.official_input_usd_per_1m,
  official_output_usd_per_1m = excluded.official_output_usd_per_1m,
  official_cache_read_usd_per_1m = excluded.official_cache_read_usd_per_1m,
  official_cache_write_usd_per_1m = excluded.official_cache_write_usd_per_1m,
  official_label = excluded.official_label,
  pricing_version = excluded.pricing_version,
  source_url = excluded.source_url,
  note = excluded.note,
  updated_at = excluded.updated_at;
"""


PER_CALL_UPSERT = """
insert into kbq_per_call_model_records (
  category, model_name, base_model, per_call_price, effective_per_call_cost,
  recharge_factor, endpoints, tags, description, pricing_version, source_url,
  note, updated_at
) values (
  :category, :model_name, :base_model, :per_call_price, :effective_per_call_cost,
  :recharge_factor, :endpoints, :tags, :description, :pricing_version,
  :source_url, :note, :updated_at
)
on conflict(category, model_name) do update set
  base_model = excluded.base_model,
  per_call_price = excluded.per_call_price,
  effective_per_call_cost = excluded.effective_per_call_cost,
  recharge_factor = excluded.recharge_factor,
  endpoints = excluded.endpoints,
  tags = excluded.tags,
  description = excluded.description,
  pricing_version = excluded.pricing_version,
  source_url = excluded.source_url,
  note = excluded.note,
  updated_at = excluded.updated_at;
"""


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--db", default=DEFAULT_DB)
    parser.add_argument("--url", default=DEFAULT_URL)
    parser.add_argument("--input-json", help="Use a saved /api/pricing JSON file")
    parser.add_argument("--timeout", type=int, default=90)
    parser.add_argument(
        "--recharge-factor",
        type=float,
        default=DEFAULT_RECHARGE_FACTOR,
        help="Effective purchase-cost factor for KBQ balance, e.g. 0.9 for 10%% off recharge",
    )
    return parser.parse_args()


def number_or_none(value: Any) -> float | None:
    if value is None or value == "":
        return None
    try:
        return float(value)
    except (TypeError, ValueError):
        return None


def decimal_or_none(value: Any) -> Decimal | None:
    if value is None or value == "":
        return None
    try:
        return Decimal(str(value))
    except (InvalidOperation, ValueError):
        return None


def compact_decimal(value: Decimal | float | int | None) -> str:
    if value is None:
        return "-"
    decimal = value if isinstance(value, Decimal) else Decimal(str(value))
    return format(decimal.quantize(Decimal("0.000000001")).normalize(), "f")


def model_cost_label(row: dict[str, Any]) -> str:
    """Return a compact per-model cost label for KBQ account notes."""

    cost = compact_decimal(row.get("cost_multiplier"))
    return f"{row['model_name']}={cost}x"


def compact_model_costs_label(rows: list[dict[str, Any]], *, limit: int = 10) -> str:
    """Summarize model-by-model costs without losing mixed-cost account pools.

    Some curated KBQ account pools intentionally include cheaper models to fill
    capability gaps in a higher priced pool, e.g. Azure haiku in a stable or
    high-cache Claude pool. Listing each mapped model's current cost keeps that
    visible instead of collapsing the account note to only the max cost.
    """

    labels = [model_cost_label(row) for row in rows[:limit]]
    if len(rows) > limit:
        labels.append(f"等{len(rows)}个模型")
    return " / ".join(labels)


def strip_prefix(model_name: str) -> str:
    if "]" in model_name:
        return model_name.split("]", 1)[1]
    return model_name


def bracket_prefix(model_name: str) -> str:
    if model_name.startswith("[") and "]" in model_name:
        return model_name.split("]", 1)[0] + "]"
    return ""


def per_call_category(model_name: str) -> str:
    prefix = bracket_prefix(model_name)
    mapping = {
        "[notion]": "Claude 按次 notion",
        "[特特价次kiro]": "Claude 按次 特特价 KIRO",
        "[特价次kiro]": "Claude 按次 特价 KIRO",
        "[特价次AG]": "Claude 按次 特价 AG",
        "[次AG]": "Claude 按次 AG",
    }
    return mapping.get(prefix, "Claude 按次其它")


def official_prices(base_model: str) -> dict[str, Any] | None:
    model = base_model.lower()
    if "gpt-5.5" in model:
        return {
            "input": 5,
            "output": 30,
            "cache_read": 0.5,
            "cache_write": None,
            "label": "OpenAI gpt-5.5: input $5, output $30, cached input $0.5 / 1M",
        }
    if "gpt-5.4-mini" in model:
        return {
            "input": 0.75,
            "output": 4.5,
            "cache_read": 0.075,
            "cache_write": None,
            "label": "OpenAI gpt-5.4-mini: input $0.75, output $4.5, cached input $0.075 / 1M",
        }
    if "gpt-5.4" in model:
        return {
            "input": 2.5,
            "output": 15,
            "cache_read": 0.25,
            "cache_write": None,
            "label": "OpenAI gpt-5.4: input $2.5, output $15, cached input $0.25 / 1M",
        }
    if "claude" in model:
        if "fable-5" in model or "mythos-5" in model:
            return {
                "input": 10,
                "output": 50,
                "cache_read": 1,
                "cache_write": 12.5,
                "label": "Anthropic Fable/Mythos 5: input $10, output $50, cache read $1, 5m cache write $12.5 / 1M",
            }
        if "haiku-4-5" in model:
            return {
                "input": 1,
                "output": 5,
                "cache_read": 0.1,
                "cache_write": 1.25,
                "label": "Anthropic Haiku 4.5: input $1, output $5, cache read $0.1, cache write $1.25 / 1M",
            }
        if "sonnet-4" in model:
            return {
                "input": 3,
                "output": 15,
                "cache_read": 0.3,
                "cache_write": 3.75,
                "label": "Anthropic Sonnet 4.x: input $3, output $15, cache read $0.3, cache write $3.75 / 1M",
            }
        if "opus-4-1" in model or "opus-4-20250514" in model:
            return {
                "input": 15,
                "output": 75,
                "cache_read": 1.5,
                "cache_write": 18.75,
                "label": "Anthropic legacy Opus 4/4.1: input $15, output $75, cache read $1.5, cache write $18.75 / 1M",
            }
        if "opus-4" in model:
            return {
                "input": 5,
                "output": 25,
                "cache_read": 0.5,
                "cache_write": 6.25,
                "label": "Anthropic Opus 4.5+ style: input $5, output $25, cache read $0.5, cache write $6.25 / 1M",
            }
    return None


def load_pricing(args: argparse.Namespace) -> dict[str, Any]:
    if args.input_json:
        return json.loads(Path(args.input_json).read_text(encoding="utf-8"))
    request = urllib.request.Request(
        args.url,
        headers={
            "Accept": "application/json",
            "User-Agent": "fluter-upstream-rates/1.0",
        },
    )
    with urllib.request.urlopen(request, timeout=args.timeout) as response:
        return json.loads(response.read().decode("utf-8"))


def live_prices(
    item: dict[str, Any],
    default_group_ratio: float,
    recharge_factor: float,
) -> dict[str, float | None]:
    model_ratio = number_or_none(item.get("model_ratio")) or 0
    completion_ratio = number_or_none(item.get("completion_ratio")) or 1
    input_price = model_ratio * default_group_ratio * 2 * recharge_factor
    cache_ratio = number_or_none(item.get("cache_ratio"))
    cache_write_ratio = number_or_none(item.get("create_cache_ratio"))
    return {
        "input": input_price,
        "output": input_price * completion_ratio,
        "cache_read": None if cache_ratio is None else input_price * cache_ratio,
        "cache_write": None if cache_write_ratio is None else input_price * cache_write_ratio,
        "raw_model_ratio": model_ratio,
    }


def max_cost_multiplier(
    live: dict[str, float | None], official: dict[str, Any]
) -> float | None:
    ratios = []
    for key in ("input", "output", "cache_read", "cache_write"):
        live_value = live.get(key)
        official_value = official.get(key)
        if live_value is not None and official_value:
            ratios.append(live_value / official_value)
    if not ratios:
        return None
    return max(ratios)


def build_rows(
    pricing: dict[str, Any],
    source_url: str,
    recharge_factor: float,
) -> list[dict[str, Any]]:
    default_group_ratio = float(pricing.get("group_ratio", {}).get("default") or 1)
    pricing_version = pricing.get("pricing_version") or ""
    rows = []
    for item in pricing.get("data", []):
        model_name = item.get("model_name") or ""
        low = model_name.lower()
        if item.get("quota_type") != 0:
            continue
        if "claude" in low:
            category = "Claude"
        elif "gpt" in low or "codex" in low:
            category = "Codex/OpenAI"
        else:
            continue

        base_model = strip_prefix(model_name)
        official = official_prices(base_model)
        if official is None:
            continue
        live = live_prices(item, default_group_ratio, recharge_factor)
        cost_multiplier = max_cost_multiplier(live, official)
        if cost_multiplier is None:
            continue
        endpoints = ", ".join(item.get("supported_endpoint_types") or [])
        rows.append(
            {
                "category": category,
                "model_name": model_name,
                "base_model": base_model,
                "cost_multiplier": cost_multiplier,
                "endpoints": endpoints,
                "input_usd_per_1m": live["input"],
                "output_usd_per_1m": live["output"],
                "cache_read_usd_per_1m": live["cache_read"],
                "cache_write_usd_per_1m": live["cache_write"],
                "raw_model_ratio": live["raw_model_ratio"],
                "official_input_usd_per_1m": official["input"],
                "official_output_usd_per_1m": official["output"],
                "official_cache_read_usd_per_1m": official["cache_read"],
                "official_cache_write_usd_per_1m": official["cache_write"],
                "official_label": official["label"],
                "pricing_version": pricing_version,
                "source_url": source_url,
                "note": (
                    "只含 KBQ quota_type=0 按 token 计费；成本倍率=上游实际价/官方基准价；"
                    f"已计入 KBQ 充值折扣系数 {recharge_factor:g}；已排除按次、DeepSeek、Gemini、Grok。"
                ),
            }
        )
    return sorted(rows, key=lambda row: (row["category"], row["cost_multiplier"], row["model_name"]))


def table_exists(conn: sqlite3.Connection, table_name: str) -> bool:
    return bool(
        conn.execute(
            "select 1 from sqlite_master where type = 'table' and name = ?",
            (table_name,),
        ).fetchone()
    )


def has_column(conn: sqlite3.Connection, table_name: str, column_name: str) -> bool:
    return any(
        row[1] == column_name
        for row in conn.execute(f"pragma table_info({table_name})")
    )


def models_for_upstream_group(
    upstream_group: str, rows: list[dict[str, Any]]
) -> list[dict[str, Any]]:
    """Map curated KBQ account rows to live /api/pricing model records."""

    group = (upstream_group or "").lower()
    matched: list[dict[str, Any]] = []

    def add_prefix(
        prefix: str,
        *,
        require: tuple[str, ...] = (),
        exclude: tuple[str, ...] = (),
    ) -> None:
        for row in rows:
            model_name = str(row["model_name"])
            base_model = str(row["base_model"]).lower()
            if not model_name.startswith(prefix):
                continue
            if require and not any(token in base_model for token in require):
                continue
            if exclude and any(token in base_model for token in exclude):
                continue
            if row not in matched:
                matched.append(row)

    if "[plus]" in group:
        add_prefix("[plus]")
    if "[pro]" in group:
        add_prefix("[pro]")
    if "[kiro量低缓]" in group:
        add_prefix("[kiro量低缓]")
    if "[kiro量高缓]" in group:
        add_prefix("[kiro量高缓]")
    if "[ag量]" in group:
        add_prefix("[AG量]")
    if "[稳定ag量]" in group:
        add_prefix("[稳定AG量]")
    if "[max-cc]" in group:
        add_prefix("[MAX-CC]")
    if "[azure量]" in group:
        if "haiku" in group and "opus" not in group and "sonnet" not in group:
            add_prefix("[Azure量]", require=("haiku",))
        elif "opus" in group or "sonnet" in group:
            add_prefix("[Azure量]", require=("opus", "sonnet"))
        else:
            add_prefix("[Azure量]")

    return matched


def actual_cost_label(actual_cost: Decimal, page_rate: Decimal, recharge_factor: Decimal) -> str:
    return (
        f"实际成本倍率 {compact_decimal(actual_cost)}x"
        f"（KBQ /api/pricing 实时模型价 {compact_decimal(page_rate)} × "
        f"充值系数 {compact_decimal(recharge_factor)}）"
    )


def prepend_note_once(note: str, line: str) -> str:
    if line in (note or ""):
        return note or ""
    return (line + "\n" + (note or ""))[:1800]


def refresh_curated_kbq_ledger_rows(
    conn: sqlite3.Connection,
    rows: list[dict[str, Any]],
    recharge_factor: float,
    observed_at: str,
) -> int:
    """Sync curated KBQ ledger account rows from live model pricing.

    This updates only the independent ledger SQLite database, never the
    production sub2api account table. It keeps the dashboard's account summary
    rows from showing stale manual seed rates after KBQ changes /api/pricing.
    """

    if not table_exists(conn, "upstream_rate_records"):
        return 0
    for column in ("actual_cost_label", "note", "updated_at"):
        if not has_column(conn, "upstream_rate_records", column):
            return 0

    recharge = decimal_or_none(recharge_factor) or Decimal("1")
    if recharge <= 0:
        return 0

    ledger_rows = conn.execute(
        """
        select id, upstream_group, page_rate, recharge_factor, actual_cost_label, note
        from upstream_rate_records
        where site = ?
          and category = 'KBQ'
          and kind not like '%生图%'
        """,
        ("xn--vduyey89e.com",),
    ).fetchall()

    changed = 0
    for ledger_row in ledger_rows:
        matched_models = models_for_upstream_group(ledger_row["upstream_group"], rows)
        if not matched_models:
            continue
        actual = max(
            decimal_or_none(model["cost_multiplier"]) or Decimal("0")
            for model in matched_models
        )
        if actual <= 0:
            continue
        page_rate = actual / recharge
        label = actual_cost_label(actual, page_rate, recharge)
        old_page_rate = decimal_or_none(ledger_row["page_rate"])
        old_recharge = decimal_or_none(ledger_row["recharge_factor"]) or Decimal("1")
        models_label = compact_model_costs_label(matched_models)
        note_line = (
            f"[{observed_at}] KBQ价格同步：按 /api/pricing 的 {models_label} "
            f"计算本账号池最高当前真实成本 {compact_decimal(actual)}x；"
            f"台账页面成本 {compact_decimal(page_rate)}x × 充值系数 {compact_decimal(recharge)}。"
        )
        needs_update = (
            old_page_rate is None
            or abs(old_page_rate - page_rate) > Decimal("0.000000001")
            or abs(old_recharge - recharge) > Decimal("0.000000001")
            or (ledger_row["actual_cost_label"] or "") != label
        )
        if not needs_update:
            continue
        conn.execute(
            """
            update upstream_rate_records
            set page_rate = ?,
                recharge_factor = ?,
                actual_cost_label = ?,
                note = ?,
                updated_at = ?
            where id = ?
            """,
            (
                float(page_rate),
                float(recharge),
                label,
                prepend_note_once(ledger_row["note"], note_line),
                observed_at,
                ledger_row["id"],
            ),
        )
        changed += 1
    return changed


def build_per_call_rows(
    pricing: dict[str, Any],
    source_url: str,
    recharge_factor: float,
) -> list[dict[str, Any]]:
    pricing_version = pricing.get("pricing_version") or ""
    rows = []
    for item in pricing.get("data", []):
        model_name = item.get("model_name") or ""
        low = model_name.lower()
        if item.get("quota_type") != 1 or "claude" not in low:
            continue
        per_call_price = number_or_none(item.get("model_price"))
        if per_call_price is None:
            continue
        effective_cost = per_call_price * recharge_factor
        endpoints = ", ".join(item.get("supported_endpoint_types") or [])
        tags = item.get("tags") or ""
        description = item.get("description") or ""
        category = per_call_category(model_name)
        rows.append(
            {
                "category": category,
                "model_name": model_name,
                "base_model": strip_prefix(model_name),
                "per_call_price": per_call_price,
                "effective_per_call_cost": effective_cost,
                "recharge_factor": recharge_factor,
                "endpoints": endpoints,
                "tags": tags,
                "description": description,
                "pricing_version": pricing_version,
                "source_url": source_url,
                "note": (
                    "KBQ quota_type=1 按次 Claude 模型；effective_per_call_cost="
                    "model_price × KBQ 充值折扣系数。按次价格不能和 token 成本倍率混用。"
                ),
            }
        )
    return sorted(rows, key=lambda row: (row["category"], row["effective_per_call_cost"], row["model_name"]))


def main() -> None:
    args = parse_args()
    db_path = Path(args.db)
    db_path.parent.mkdir(parents=True, exist_ok=True)
    pricing = load_pricing(args)
    if not pricing.get("success") or not isinstance(pricing.get("data"), list):
        raise SystemExit("Unexpected KBQ /api/pricing response")
    rows = build_rows(pricing, args.url, args.recharge_factor)
    per_call_rows = build_per_call_rows(pricing, args.url, args.recharge_factor)
    now = datetime.now(timezone.utc).isoformat(timespec="seconds")
    conn = sqlite3.connect(db_path)
    conn.row_factory = sqlite3.Row
    with conn:
        conn.executescript(SCHEMA)
        conn.execute("delete from kbq_token_model_records")
        conn.execute("delete from kbq_per_call_model_records")
        for row in rows:
            payload = dict(row)
            payload["updated_at"] = now
            conn.execute(UPSERT, payload)
        synced_ledger_rows = refresh_curated_kbq_ledger_rows(
            conn, rows, args.recharge_factor, now
        )
        for row in per_call_rows:
            payload = dict(row)
            payload["updated_at"] = now
            conn.execute(PER_CALL_UPSERT, payload)
        conn.execute(
            "insert or replace into metadata(key, value) values (?, ?)",
            ("kbq_pricing_updated_at", now),
        )
        conn.execute(
            "insert or replace into metadata(key, value) values (?, ?)",
            ("kbq_pricing_version", pricing.get("pricing_version") or ""),
        )
        conn.execute(
            "insert or replace into metadata(key, value) values (?, ?)",
            ("kbq_pricing_source", args.url),
        )
        conn.execute(
            "insert or replace into metadata(key, value) values (?, ?)",
            ("kbq_recharge_factor", f"{args.recharge_factor:g}"),
        )
        conn.execute(
            "insert or replace into metadata(key, value) values (?, ?)",
            (
                "kbq_recharge_note",
                "KBQ token and per-call model costs include the current effective recharge factor; curated KBQ account ledger rows are reconciled from /api/pricing model records when present.",
            ),
        )
        conn.execute(
            "insert or replace into metadata(key, value) values (?, ?)",
            ("kbq_curated_ledger_rows_synced", str(synced_ledger_rows)),
        )
        conn.execute(
            "insert or replace into metadata(key, value) values (?, ?)",
            ("kbq_per_call_pricing_updated_at", now),
        )
        conn.execute(
            "insert or replace into metadata(key, value) values (?, ?)",
            ("kbq_per_call_model_count", str(len(per_call_rows))),
        )
    print(
        f"Refreshed {len(rows)} KBQ token model records and "
        f"{len(per_call_rows)} Claude per-call model records into {db_path}; "
        f"synced {synced_ledger_rows} curated KBQ ledger rows"
    )


if __name__ == "__main__":
    main()
