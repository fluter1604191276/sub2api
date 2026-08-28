#!/usr/bin/env python3
"""Audit KBQ true upstream costs from production usage logs.

This script is read-only for sub2api PostgreSQL. It reads recent usage_logs,
fetches KBQ public /api/pricing, recomputes the real upstream cost, and writes
only the independent upstream-rates SQLite ledger.

No API keys, Bearer tokens, cookies, or request bodies are selected.
"""

from __future__ import annotations

import argparse
import json
import sqlite3
import subprocess
import urllib.request
from dataclasses import dataclass
from datetime import datetime, timezone
from decimal import Decimal
from pathlib import Path
from typing import Any


DEFAULT_DB = "/var/lib/fluterapi-upstream-rates/upstream_rates.sqlite"
DEFAULT_KBQ_PRICING_URL = "https://xn--vduyey89e.com/api/pricing"
DEFAULT_KBQ_RECHARGE_FACTOR = Decimal("0.9")


SCHEMA = """
create table if not exists kbq_true_cost_audit_runs (
  id integer primary key autoincrement,
  observed_at text not null,
  hours integer not null,
  pricing_version text not null,
  request_count integer not null,
  bucket_count integer not null,
  user_billed_cost real not null,
  comparable_user_billed_cost real not null default 0,
  unpriced_user_billed_cost real not null default 0,
  true_upstream_cost real not null,
  margin real not null,
  margin_percent real,
  real_loss_bucket_count integer not null,
  display_drift_bucket_count integer not null,
  missing_price_bucket_count integer not null,
  tool_fee_unknown_bucket_count integer not null default 0,
  cache_creation_1h_tokens integer not null,
  source text not null,
  note text not null
);

create table if not exists kbq_true_cost_audit_buckets (
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
  service_tier text not null default 'default',
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
  kbq_group_key text not null default '',
  kbq_group_ratio real,
  group_ratio_source text not null default '',
  tool_fee_unknown integer not null default 0,
  note text not null,
  foreign key(run_id) references kbq_true_cost_audit_runs(id)
);

create table if not exists metadata (
  key text primary key,
  value text not null
);
"""


def has_column(conn: sqlite3.Connection, table_name: str, column_name: str) -> bool:
    return any(row[1] == column_name for row in conn.execute(f"pragma table_info({table_name})"))


def ensure_ledger_schema(conn: sqlite3.Connection) -> None:
    conn.executescript(SCHEMA)
    additions = {
        "kbq_true_cost_audit_runs": [
            ("tool_fee_unknown_bucket_count", "integer not null default 0"),
            ("comparable_user_billed_cost", "real not null default 0"),
            ("unpriced_user_billed_cost", "real not null default 0"),
        ],
        "kbq_true_cost_audit_buckets": [
            ("kbq_group_key", "text not null default ''"),
            ("kbq_group_ratio", "real"),
            ("group_ratio_source", "text not null default ''"),
            ("tool_fee_unknown", "integer not null default 0"),
            ("service_tier", "text not null default 'default'"),
        ],
    }
    for table_name, columns in additions.items():
        for column_name, definition in columns:
            if not has_column(conn, table_name, column_name):
                conn.execute(f"alter table {table_name} add column {column_name} {definition}")


@dataclass
class TokenPrices:
    input: Decimal | None = None
    output: Decimal | None = None
    cache_read: Decimal | None = None
    cache_write: Decimal | None = None


@dataclass
class UsageBucket:
    account_id: int
    account_name: str
    base_url: str
    channel_id: int | None
    channel_name: str
    group_id: int | None
    group_name: str
    model: str
    upstream_model: str
    service_tier: str
    request_count: int
    input_tokens: int
    output_tokens: int
    cache_creation_tokens: int
    cache_creation_5m_tokens: int
    cache_creation_1h_tokens: int
    cache_read_tokens: int
    user_billed_cost: Decimal
    displayed_account_cost: Decimal
    explicit_kbq_group: str


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Audit KBQ true upstream cost against user billing")
    parser.add_argument("--db", default=DEFAULT_DB)
    parser.add_argument("--ssh-host", default="fluterapi-prod")
    parser.add_argument("--hours", type=int, default=720)
    parser.add_argument("--pricing-url", default=DEFAULT_KBQ_PRICING_URL)
    parser.add_argument("--timeout", type=int, default=30)
    parser.add_argument(
        "--recharge-factor",
        default=str(DEFAULT_KBQ_RECHARGE_FACTOR),
        help="Effective purchase-cost factor for KBQ balance, e.g. 0.9 for 10%% off recharge",
    )
    parser.add_argument(
        "--local-postgres",
        action="store_true",
        help="Run docker exec locally instead of through ssh; useful when this script is executed on the VPS",
    )
    parser.add_argument(
        "--fail-on-loss",
        action="store_true",
        help="Exit with code 2 if any REAL_LOSS bucket is found",
    )
    return parser.parse_args(argv)


def as_decimal(value: Any) -> Decimal:
    return Decimal(str(value or "0"))


def decimal_or_none(value: Any) -> Decimal | None:
    if value is None or value == "":
        return None
    return Decimal(str(value))


def normalize_service_tier(value: Any) -> str:
    """Normalize the production tier label; fast is the priority alias."""
    tier = str(value or "").strip().lower()
    if tier == "fast":
        return "priority"
    return tier or "default"


def run_psql(sql: str, args: argparse.Namespace) -> str:
    psql = "docker exec -i sub2api-postgres psql -U sub2api -d sub2api -At"
    command = psql if args.local_postgres else f"ssh {args.ssh_host} {json.dumps(psql)}"
    proc = subprocess.run(
        command,
        input=sql,
        text=True,
        capture_output=True,
        shell=True,
        check=False,
    )
    if proc.returncode != 0:
        raise SystemExit(f"PostgreSQL read failed:\n{proc.stderr.strip()}")
    return proc.stdout


def load_usage_buckets(args: argparse.Namespace) -> list[UsageBucket]:
    sql = f"""
copy (
  select coalesce(jsonb_agg(row_to_json(t) order by account_id, channel_id, group_id, model, upstream_model), '[]'::jsonb)
  from (
    select
      ul.account_id,
      a.name as account_name,
      credentials->>'base_url' as base_url,
      coalesce(extra->>'kbq_group_key', extra->>'kbq_group', '') as explicit_kbq_group,
      ul.channel_id,
      coalesce(c.name, '') as channel_name,
      ul.group_id,
      coalesce(g.name, '') as group_name,
      coalesce(ul.model, '') as model,
      coalesce(ul.upstream_model, ul.model, '') as upstream_model,
      case lower(trim(coalesce(ul.service_tier, '')))
        when 'fast' then 'priority'
        when '' then 'default'
        else lower(trim(ul.service_tier))
      end as service_tier,
      count(*)::int as request_count,
      coalesce(sum(ul.input_tokens), 0)::bigint as input_tokens,
      coalesce(sum(ul.output_tokens), 0)::bigint as output_tokens,
      coalesce(sum(ul.cache_creation_tokens), 0)::bigint as cache_creation_tokens,
      coalesce(sum(ul.cache_creation_5m_tokens), 0)::bigint as cache_creation_5m_tokens,
      coalesce(sum(ul.cache_creation_1h_tokens), 0)::bigint as cache_creation_1h_tokens,
      coalesce(sum(ul.cache_read_tokens), 0)::bigint as cache_read_tokens,
      coalesce(sum(ul.actual_cost), 0)::text as user_billed_cost,
      coalesce(sum(coalesce(ul.account_stats_cost, ul.total_cost) * coalesce(ul.account_rate_multiplier, 1)), 0)::text as displayed_account_cost
    from usage_logs ul
    join accounts a on a.id = ul.account_id
    left join channels c on c.id = ul.channel_id
    left join groups g on g.id = ul.group_id
    where ul.created_at >= now() - interval '{int(args.hours)} hours'
      and a.deleted_at is null
      and credentials->>'base_url' ilike '%xn--vduyey89e%'
    group by
      ul.account_id, a.name, credentials->>'base_url',
      coalesce(extra->>'kbq_group_key', extra->>'kbq_group', ''),
      ul.channel_id, c.name, ul.group_id, g.name,
      coalesce(ul.model, ''), coalesce(ul.upstream_model, ul.model, ''),
      case lower(trim(coalesce(ul.service_tier, '')))
        when 'fast' then 'priority'
        when '' then 'default'
        else lower(trim(ul.service_tier))
      end
  ) t
) to stdout;
"""
    rows = json.loads(run_psql(sql, args).strip() or "[]")
    return [
        UsageBucket(
            account_id=int(row["account_id"]),
            account_name=row["account_name"],
            base_url=row.get("base_url") or "",
            channel_id=int(row["channel_id"]) if row.get("channel_id") is not None else None,
            channel_name=row.get("channel_name") or "",
            group_id=int(row["group_id"]) if row.get("group_id") is not None else None,
            group_name=row.get("group_name") or "",
            model=row.get("model") or "",
            upstream_model=row.get("upstream_model") or "",
            service_tier=normalize_service_tier(row.get("service_tier")),
            request_count=int(row["request_count"] or 0),
            input_tokens=int(row["input_tokens"] or 0),
            output_tokens=int(row["output_tokens"] or 0),
            cache_creation_tokens=int(row["cache_creation_tokens"] or 0),
            cache_creation_5m_tokens=int(row["cache_creation_5m_tokens"] or 0),
            cache_creation_1h_tokens=int(row["cache_creation_1h_tokens"] or 0),
            cache_read_tokens=int(row["cache_read_tokens"] or 0),
            user_billed_cost=as_decimal(row["user_billed_cost"]),
            displayed_account_cost=as_decimal(row["displayed_account_cost"]),
            explicit_kbq_group=row.get("explicit_kbq_group") or "",
        )
        for row in rows
    ]


def fetch_kbq_pricing(args: argparse.Namespace) -> dict[str, Any]:
    request = urllib.request.Request(
        args.pricing_url,
        headers={
            "Accept": "application/json",
            "User-Agent": "fluter-kbq-true-cost-audit/1.0",
        },
    )
    with urllib.request.urlopen(request, timeout=args.timeout) as response:
        return json.loads(response.read().decode("utf-8"))


def resolve_group_ratio(
    pricing: dict[str, Any],
    item: dict[str, Any] | None,
    *,
    account_name: str = "",
    explicit_group: str = "",
) -> tuple[Decimal | None, str, str]:
    """Resolve the KBQ price group without silently guessing a cheaper tier.

    Account names are deliberately not a pricing authority. A name can be
    stale, abbreviated, or describe our internal pool rather than KBQ's group.
    An explicit account metadata key wins; otherwise only a single enabled
    pricing group is safe to infer. Multiple enabled groups are ambiguous.
    """
    del account_name
    ratios = pricing.get("group_ratio") or {}
    enabled = [
        str(key)
        for key in (item or {}).get("enable_groups") or []
        if decimal_or_none(ratios.get(str(key))) is not None
    ]
    explicit = str(explicit_group or "").strip()
    if explicit:
        value = decimal_or_none(ratios.get(explicit))
        if value is not None and (not enabled or explicit in enabled):
            return value, explicit, "account_metadata"
        return None, "", "explicit_group_not_enabled"
    if len(enabled) == 1:
        key = enabled[0]
        return decimal_or_none(ratios.get(key)), key, "single_enabled_group"
    return None, "", "ambiguous"


def tool_fee_is_unknown(model_name: str) -> bool:
    """KBQ may charge DeepSeek tools separately, but usage_logs lack tool counts."""
    return "deepseek" in str(model_name or "").lower()


def upstream_price_signature(item: dict[str, Any]) -> tuple[str, ...]:
    return tuple(
        str(item.get(field))
        for field in (
            "quota_type",
            "model_ratio",
            "completion_ratio",
            "cache_ratio",
            "create_cache_ratio",
        )
    )


def select_upstream_item(
    candidates: list[dict[str, Any]],
    pricing: dict[str, Any],
    explicit_group: str,
) -> tuple[dict[str, Any] | None, Decimal | None, str, str]:
    selected = None
    ratio = None
    group_key = ""
    ratio_source = ""
    price_signature: tuple[str, ...] | None = None
    for candidate in candidates:
        candidate_ratio, candidate_key, candidate_source = resolve_group_ratio(
            pricing,
            candidate,
            explicit_group=explicit_group,
        )
        if candidate_ratio is None:
            continue
        candidate_signature = upstream_price_signature(candidate)
        if ratio is not None and (
            candidate_key != group_key
            or candidate_ratio != ratio
            or candidate_signature != price_signature
        ):
            return None, None, "", "ambiguous_model_variant"
        selected = candidate
        ratio = candidate_ratio
        group_key = candidate_key
        ratio_source = candidate_source
        price_signature = candidate_signature
    return selected, ratio, group_key, ratio_source


def live_prices(
    item: dict[str, Any] | None,
    default_group_ratio: Decimal | None,
    recharge_factor: Decimal,
) -> TokenPrices:
    if not item or item.get("quota_type") == 1 or default_group_ratio is None:
        return TokenPrices()
    model_ratio = decimal_or_none(item.get("model_ratio"))
    if model_ratio is None:
        return TokenPrices()
    completion_ratio = decimal_or_none(item.get("completion_ratio")) or Decimal("1")
    input_price = model_ratio * default_group_ratio * Decimal("2") * recharge_factor
    cache_read_ratio = decimal_or_none(item.get("cache_ratio"))
    cache_write_ratio = decimal_or_none(item.get("create_cache_ratio"))
    return TokenPrices(
        input=input_price,
        output=input_price * completion_ratio,
        cache_read=input_price * cache_read_ratio if cache_read_ratio is not None else None,
        cache_write=input_price * cache_write_ratio if cache_write_ratio is not None else None,
    )


def token_cost(tokens: int, price_per_1m: Decimal | None) -> Decimal | None:
    if price_per_1m is None:
        return None
    return Decimal(tokens) * price_per_1m / Decimal("1000000")


def true_cost(bucket: UsageBucket, prices: TokenPrices) -> tuple[Decimal | None, str]:
    parts = [
        token_cost(bucket.input_tokens, prices.input),
        token_cost(bucket.output_tokens, prices.output),
        token_cost(bucket.cache_read_tokens, prices.cache_read),
    ]
    if any(part is None for part in parts):
        return None, "missing input/output/cache-read price"

    if bucket.cache_creation_5m_tokens or bucket.cache_creation_1h_tokens:
        cache_write_tokens = bucket.cache_creation_5m_tokens + bucket.cache_creation_1h_tokens
        note = "KBQ create_cache_ratio applied to recorded cache-write tokens"
    else:
        cache_write_tokens = bucket.cache_creation_tokens
        note = "KBQ create_cache_ratio applied to legacy cache_creation_tokens"

    cache_part = token_cost(cache_write_tokens, prices.cache_write)
    if cache_part is None and cache_write_tokens:
        return None, "missing cache-write price"
    if bucket.cache_creation_1h_tokens:
        note += "; 1h tokens present, KBQ exposes one create_cache_ratio"
    return sum(part for part in parts if part is not None) + (cache_part or Decimal("0")), note


def status_for(user_billed: Decimal, cost: Decimal | None) -> str:
    if cost is None:
        return "NO_PRICE"
    tolerance = max(Decimal("0.000001"), cost * Decimal("0.005"))
    return "REAL_LOSS" if cost > user_billed + tolerance else "OK"


def display_status_for(displayed: Decimal, cost: Decimal | None) -> str:
    if cost is None:
        return "-"
    tolerance = max(Decimal("0.000001"), cost * Decimal("0.05"))
    return "DISPLAY_DRIFT" if abs(displayed - cost) > tolerance else "OK"


def to_float(value: Decimal | None) -> float | None:
    return None if value is None else float(value)


def summarize_rows(
    args: argparse.Namespace,
    pricing: dict[str, Any],
    recharge_factor: Decimal,
    rows: list[dict[str, Any]],
) -> dict[str, Any]:
    user_billed_cost = sum((row["user_billed_cost"] for row in rows), Decimal("0"))
    comparable_user_billed_cost = sum(
        (row["user_billed_cost"] for row in rows if row["true_upstream_cost"] is not None),
        Decimal("0"),
    )
    unpriced_user_billed_cost = user_billed_cost - comparable_user_billed_cost
    true_upstream_cost = sum(
        (row["true_upstream_cost"] for row in rows if row["true_upstream_cost"] is not None),
        Decimal("0"),
    )
    margin = comparable_user_billed_cost - true_upstream_cost
    margin_percent = margin / true_upstream_cost * Decimal("100") if true_upstream_cost else None
    return {
        "observed_at": datetime.now(timezone.utc).isoformat(timespec="seconds"),
        "hours": args.hours,
        "pricing_version": pricing.get("pricing_version") or "",
        "request_count": sum(row["request_count"] for row in rows),
        "bucket_count": len(rows),
        "user_billed_cost": user_billed_cost,
        "comparable_user_billed_cost": comparable_user_billed_cost,
        "unpriced_user_billed_cost": unpriced_user_billed_cost,
        "true_upstream_cost": true_upstream_cost,
        "margin": margin,
        "margin_percent": margin_percent,
        "real_loss_bucket_count": sum(1 for row in rows if row["status"] == "REAL_LOSS"),
        "display_drift_bucket_count": sum(1 for row in rows if row["display_status"] == "DISPLAY_DRIFT"),
        "missing_price_bucket_count": sum(1 for row in rows if row["status"] == "NO_PRICE"),
        "tool_fee_unknown_bucket_count": sum(1 for row in rows if row["tool_fee_unknown"]),
        "service_tier_counts": {
            tier: sum(1 for row in rows if row.get("service_tier", "default") == tier)
            for tier in sorted({row.get("service_tier", "default") for row in rows})
        },
        "cache_creation_1h_tokens": sum(row["cache_creation_1h_tokens"] for row in rows),
        "source": args.pricing_url,
        "note": f"Loss check uses KBQ /api/pricing plus production usage_logs token counts; account-specific KBQ group ratios and recharge factor {recharge_factor} are applied; margin excludes buckets without a current upstream price and is only a token-margin upper bound when tool fees are unknown; site balance and KBQ CNY prices use the same numeric accounting unit; sub2api database is read-only.",
    }


def audit(args: argparse.Namespace) -> tuple[dict[str, Any], list[dict[str, Any]]]:
    buckets = load_usage_buckets(args)
    pricing = fetch_kbq_pricing(args)
    models: dict[str, list[dict[str, Any]]] = {}
    for item in pricing.get("data", []):
        if isinstance(item, dict) and item.get("model_name"):
            models.setdefault(str(item["model_name"]), []).append(item)
    recharge_factor = decimal_or_none(args.recharge_factor) or DEFAULT_KBQ_RECHARGE_FACTOR
    rows: list[dict[str, Any]] = []
    for bucket in buckets:
        candidates = models.get(bucket.upstream_model, [])
        item, ratio, group_key, ratio_source = select_upstream_item(
            candidates,
            pricing,
            bucket.explicit_kbq_group,
        )
        if not candidates:
            cost, note = None, "upstream model missing from KBQ pricing"
        elif item is None or ratio is None:
            cost, note = None, "KBQ group/model variant ambiguous; fail closed"
        else:
            cost, note = true_cost(bucket, live_prices(item, ratio, recharge_factor))
            note = f"KBQ group={group_key} ({ratio_source}); " + note
        tool_fee_unknown = tool_fee_is_unknown(bucket.upstream_model)
        if tool_fee_unknown:
            note += "; possible KBQ tool fee is not recorded in usage_logs"
        status = status_for(bucket.user_billed_cost, cost)
        display_status = display_status_for(bucket.displayed_account_cost, cost)
        margin = bucket.user_billed_cost - cost if cost is not None else None
        cache_write_tokens = (
            bucket.cache_creation_5m_tokens + bucket.cache_creation_1h_tokens
            if bucket.cache_creation_5m_tokens or bucket.cache_creation_1h_tokens
            else bucket.cache_creation_tokens
        )
        rows.append(
            {
                "status": status,
                "display_status": display_status,
                "account_id": bucket.account_id,
                "account_name": bucket.account_name,
                "channel_id": bucket.channel_id,
                "channel_name": bucket.channel_name,
                "group_id": bucket.group_id,
                "group_name": bucket.group_name,
                "model": bucket.model,
                "upstream_model": bucket.upstream_model,
                "service_tier": bucket.service_tier,
                "request_count": bucket.request_count,
                "input_tokens": bucket.input_tokens,
                "output_tokens": bucket.output_tokens,
                "cache_read_tokens": bucket.cache_read_tokens,
                "cache_write_tokens": cache_write_tokens,
                "cache_creation_1h_tokens": bucket.cache_creation_1h_tokens,
                "user_billed_cost": bucket.user_billed_cost,
                "true_upstream_cost": cost,
                "margin": margin,
                "displayed_account_cost": bucket.displayed_account_cost,
                "kbq_group_key": group_key,
                "kbq_group_ratio": ratio,
                "group_ratio_source": ratio_source,
                "tool_fee_unknown": tool_fee_unknown,
                "note": note,
            }
        )

    return summarize_rows(args, pricing, recharge_factor, rows), rows


def write_ledger(db_path: str, summary: dict[str, Any], rows: list[dict[str, Any]]) -> int:
    path = Path(db_path)
    path.parent.mkdir(parents=True, exist_ok=True)
    conn = sqlite3.connect(path)
    with conn:
        ensure_ledger_schema(conn)
        run_payload = dict(summary)
        run_payload.setdefault("comparable_user_billed_cost", run_payload["user_billed_cost"])
        run_payload.setdefault("unpriced_user_billed_cost", Decimal("0"))
        for key in (
            "user_billed_cost",
            "comparable_user_billed_cost",
            "unpriced_user_billed_cost",
            "true_upstream_cost",
            "margin",
            "margin_percent",
        ):
            run_payload[key] = to_float(run_payload[key])
        cursor = conn.execute(
            """
            insert into kbq_true_cost_audit_runs (
              observed_at, hours, pricing_version, request_count, bucket_count,
              user_billed_cost, comparable_user_billed_cost,
              unpriced_user_billed_cost, true_upstream_cost, margin, margin_percent,
              real_loss_bucket_count, display_drift_bucket_count,
              missing_price_bucket_count, tool_fee_unknown_bucket_count,
              cache_creation_1h_tokens, source, note
            ) values (
              :observed_at, :hours, :pricing_version, :request_count, :bucket_count,
              :user_billed_cost, :comparable_user_billed_cost,
              :unpriced_user_billed_cost, :true_upstream_cost, :margin, :margin_percent,
              :real_loss_bucket_count, :display_drift_bucket_count,
              :missing_price_bucket_count, :tool_fee_unknown_bucket_count,
              :cache_creation_1h_tokens, :source, :note
            )
            """,
            run_payload,
        )
        run_id = int(cursor.lastrowid)
        for row in rows:
            payload = dict(row)
            payload["run_id"] = run_id
            for key in ("user_billed_cost", "true_upstream_cost", "margin", "displayed_account_cost", "kbq_group_ratio"):
                payload[key] = to_float(payload[key])
            payload["tool_fee_unknown"] = int(bool(payload["tool_fee_unknown"]))
            conn.execute(
                """
                insert into kbq_true_cost_audit_buckets (
                  run_id, status, display_status, account_id, account_name,
                  channel_id, channel_name, group_id, group_name, model,
                  upstream_model, service_tier, request_count, input_tokens, output_tokens,
                  cache_read_tokens, cache_write_tokens, cache_creation_1h_tokens,
                  user_billed_cost, true_upstream_cost, margin,
                  displayed_account_cost, kbq_group_key, kbq_group_ratio,
                  group_ratio_source, tool_fee_unknown, note
                ) values (
                  :run_id, :status, :display_status, :account_id, :account_name,
                  :channel_id, :channel_name, :group_id, :group_name, :model,
                  :upstream_model, :service_tier, :request_count, :input_tokens, :output_tokens,
                  :cache_read_tokens, :cache_write_tokens, :cache_creation_1h_tokens,
                  :user_billed_cost, :true_upstream_cost, :margin,
                  :displayed_account_cost, :kbq_group_key, :kbq_group_ratio,
                  :group_ratio_source, :tool_fee_unknown, :note
                )
                """,
                payload,
            )
        conn.execute(
            "insert or replace into metadata(key, value) values (?, ?)",
            ("kbq_true_cost_audit_updated_at", summary["observed_at"]),
        )
        conn.execute(
            "insert or replace into metadata(key, value) values (?, ?)",
            ("kbq_true_cost_audit_hours", str(summary["hours"])),
        )
    return run_id


def main() -> int:
    args = parse_args()
    summary, rows = audit(args)
    run_id = write_ledger(args.db, summary, rows)
    print(
        "KBQ true-cost audit run #{run_id}: requests={requests}, buckets={buckets}, "
        "user_billed={actual:.8f}, true_cost={true:.8f}, margin={margin:.8f}, "
        "real_loss_buckets={losses}, display_drift_buckets={drifts}".format(
            run_id=run_id,
            requests=summary["request_count"],
            buckets=summary["bucket_count"],
            actual=float(summary["user_billed_cost"]),
            true=float(summary["true_upstream_cost"]),
            margin=float(summary["margin"]),
            losses=summary["real_loss_bucket_count"],
            drifts=summary["display_drift_bucket_count"],
        )
    )
    if args.fail_on_loss and summary["real_loss_bucket_count"]:
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
