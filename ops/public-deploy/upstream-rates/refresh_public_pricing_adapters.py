#!/usr/bin/env python3
"""Refresh public upstream pricing adapters into the Fluter ledger.

This script only reads public pricing endpoints and writes independent pricing
snapshots into the ledger SQLite database. It does not read API keys and does
not modify sub2api production accounts, groups, channels, or pricing.

By default it also does not overwrite the manually curated final ledger rates in
upstream_rate_records. Public NewAPI group_ratio values are often only one part
of Fluter's final cost formula, so direct overwrite is opt-in.
"""

from __future__ import annotations

import argparse
import json
import re
import sqlite3
import urllib.error
import urllib.request
from dataclasses import dataclass
from datetime import datetime, timezone
from decimal import Decimal
from pathlib import Path
from typing import Any, NamedTuple


DEFAULT_DB = "/var/lib/fluterapi-upstream-rates/upstream_rates.sqlite"
MIN_TAMPERMONKEY_SNAPSHOT_VERSION = (0, 1, 15)


@dataclass(frozen=True)
class Provider:
    name: str
    site: str
    pricing_url: str | None
    note: str


class HubCoverage(NamedTuple):
    detail: str
    status: str
    message: str


SITE_ALIASES = {
    "api.tokenskingdom.com": ("api.tokenskingdom.com", "tokenskingdom.com", "image.tokenskingdom.com"),
    "tokenskingdom.com": ("tokenskingdom.com", "api.tokenskingdom.com", "image.tokenskingdom.com"),
    "image.tokenskingdom.com": ("image.tokenskingdom.com", "api.tokenskingdom.com", "tokenskingdom.com"),
}


def site_lookup_keys(site: str) -> tuple[str, ...]:
    normalized = str(site or "").strip().lower()
    return SITE_ALIASES.get(normalized, (normalized,))


PROVIDERS = [
    Provider("KBQ", "xn--vduyey89e.com", "https://xn--vduyey89e.com/api/pricing", "public newapi pricing"),
    Provider("钧澈", "vip.lcodex.cn", "https://vip.lcodex.cn/api/pricing", "public newapi pricing"),
    Provider("Meow", "api.saki.lat", None, "no public /api/pricing; browser/API-key adapter needed"),
    Provider("Magic", "pool.gptstore.club", None, "no public /api/pricing; browser/API-key adapter needed"),
    Provider(
        "Kingdom",
        "api.tokenskingdom.com",
        None,
        "no public /api/pricing; browser/API-key adapter needed; image.tokenskingdom.com is an API fast subdomain, not a separate dashboard",
    ),
    Provider("超超 Mouubox", "api.mouubox.com", None, "no public /api/pricing; browser/API-key adapter needed"),
    Provider("超超 Mouubox 副站", "sub2api.mouubox.com", None, "no public /api/pricing; browser/API-key adapter needed"),
    Provider("聪明AI", "sub2.congmingai.com", None, "no public /api/pricing; browser/API-key adapter needed"),
    Provider("乔燃", "mdkj.lol", None, "no public /api/pricing; browser/API-key adapter needed"),
]


SCHEMA = """
create table if not exists upstream_adapter_status (
  provider text not null,
  site text not null,
  adapter_kind text not null,
  status text not null,
  detail text not null,
  observed_at text not null,
  unique(provider, site)
);

create table if not exists provider_group_ratio_records (
  provider text not null,
  site text not null,
  group_name text not null,
  page_rate real not null,
  pricing_version text not null,
  source_url text not null,
  updated_at text not null,
  unique(provider, site, group_name)
);

create table if not exists provider_model_pricing_records (
  provider text not null,
  site text not null,
  model_name text not null,
  quota_type integer,
  model_ratio real,
  completion_ratio real,
  cache_ratio real,
  create_cache_ratio real,
  model_price real,
  supported_endpoints text not null,
  pricing_version text not null,
  source_url text not null,
  updated_at text not null,
  unique(provider, site, model_name)
);

create table if not exists metadata (
  key text primary key,
  value text not null
);
"""


UPSERT_STATUS = """
insert into upstream_adapter_status (
  provider, site, adapter_kind, status, detail, observed_at
) values (?, ?, ?, ?, ?, ?)
on conflict(provider, site) do update set
  adapter_kind = excluded.adapter_kind,
  status = excluded.status,
  detail = excluded.detail,
  observed_at = excluded.observed_at;
"""


UPSERT_GROUP = """
insert into provider_group_ratio_records (
  provider, site, group_name, page_rate, pricing_version, source_url, updated_at
) values (?, ?, ?, ?, ?, ?, ?)
on conflict(provider, site, group_name) do update set
  page_rate = excluded.page_rate,
  pricing_version = excluded.pricing_version,
  source_url = excluded.source_url,
  updated_at = excluded.updated_at;
"""


UPSERT_MODEL = """
insert into provider_model_pricing_records (
  provider, site, model_name, quota_type, model_ratio, completion_ratio,
  cache_ratio, create_cache_ratio, model_price, supported_endpoints,
  pricing_version, source_url, updated_at
) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
on conflict(provider, site, model_name) do update set
  quota_type = excluded.quota_type,
  model_ratio = excluded.model_ratio,
  completion_ratio = excluded.completion_ratio,
  cache_ratio = excluded.cache_ratio,
  create_cache_ratio = excluded.create_cache_ratio,
  model_price = excluded.model_price,
  supported_endpoints = excluded.supported_endpoints,
  pricing_version = excluded.pricing_version,
  source_url = excluded.source_url,
  updated_at = excluded.updated_at;
"""


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Refresh public upstream pricing adapters")
    parser.add_argument("--db", default=DEFAULT_DB)
    parser.add_argument("--timeout", type=int, default=30)
    parser.add_argument(
        "--update-ledger-page-rates",
        action="store_true",
        help=(
            "Opt-in: update matching upstream_rate_records.page_rate by exact "
            "site + upstream_group match. Default is safer snapshot-only mode."
        ),
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
    except Exception:
        return None


def compact_rate(value: float | Decimal | None) -> str:
    if value is None:
        return "-"
    decimal = Decimal(str(value)).quantize(Decimal("0.000000001")).normalize()
    return format(decimal, "f")


def public_actual_cost_label(provider: Provider, page_rate: float, recharge_factor: float) -> str:
    actual = Decimal(str(page_rate)) * Decimal(str(recharge_factor))
    return (
        f"实际成本倍率 {compact_rate(actual)}x"
        f"（{provider.name} /api/pricing 当前分组倍率 {compact_rate(page_rate)} × "
        f"充值系数 {compact_rate(recharge_factor)}）"
    )


def status_after_public_rate(row: sqlite3.Row, new_page_rate: float) -> str:
    status = str(row["status"])
    if status not in ("已确认", "已覆盖", "偏保守", "需核对/倍率漂移"):
        return status
    site_multiplier = row["site_account_multiplier"]
    if site_multiplier is None:
        return status
    actual = Decimal(str(new_page_rate)) * Decimal(str(row["recharge_factor"] or 1))
    current = Decimal(str(site_multiplier))
    if actual <= 0:
        return status
    coverage = current / actual
    if coverage < Decimal("0.999"):
        return "需核对/倍率漂移"
    if coverage > Decimal("1.05"):
        return "偏保守"
    if status in ("需核对/倍率漂移", "偏保守"):
        return "已确认"
    return status


def prepend_note_once(note: str, line: str) -> str:
    if line in (note or ""):
        return note or ""
    return (line + "\n" + (note or ""))[:1800]


def fetch_json(url: str, timeout: int) -> dict[str, Any]:
    request = urllib.request.Request(
        url,
        headers={
            "Accept": "application/json",
            "User-Agent": "fluter-public-pricing-adapter/1.0",
        },
    )
    with urllib.request.urlopen(request, timeout=timeout) as response:
        content_type = response.headers.get("content-type", "")
        raw = response.read().decode("utf-8")
    if "json" not in content_type.lower() and not raw.lstrip().startswith("{"):
        raise ValueError(f"Non-JSON response from {url}: {content_type}")
    data = json.loads(raw)
    if not data.get("success") or not isinstance(data.get("data"), list):
        raise ValueError(f"Unexpected pricing response from {url}")
    return data


def table_exists(conn: sqlite3.Connection, table_name: str) -> bool:
    return bool(
        conn.execute(
            "select 1 from sqlite_master where type = 'table' and name = ?",
            (table_name,),
        ).fetchone()
    )


def has_column(conn: sqlite3.Connection, table_name: str, column: str) -> bool:
    return any(row[1] == column for row in conn.execute(f"pragma table_info({table_name})"))


def parse_semver(value: str) -> tuple[int, ...] | None:
    raw = str(value or "").strip()
    if not raw:
        return None
    parts = raw.split(".")
    if not all(part.isdigit() for part in parts):
        return None
    return tuple(int(part) for part in parts)


def semver_lt(left: tuple[int, ...], right: tuple[int, ...]) -> bool:
    size = max(len(left), len(right))
    return left + (0,) * (size - len(left)) < right + (0,) * (size - len(right))


def detail_script_version(detail: str) -> tuple[int, ...] | None:
    match = re.search(r"\bscript\s*=\s*([0-9]+(?:\.[0-9]+){0,3})\b", str(detail or ""))
    return parse_semver(match.group(1)) if match else None


def browser_status_is_current_coverage(status: str, detail: str) -> bool:
    if status != "browser_observed":
        return False
    text = str(detail or "")
    if (
        "preserved previous rate lines" in text
        or "preserved previous account lines" in text
        or "preserved previous non-empty snapshot" in text
        or re.search(r"\bfresh_(?:rate|account)_lines\s*=\s*0\b", text)
    ):
        return False
    if "partial account snapshot" in text or re.search(r"\bwait_state\s*=\s*timeout\b", text):
        return False
    if "Chrome Tampermonkey read-only snapshot" in text:
        version = detail_script_version(text)
        if version is None or semver_lt(version, MIN_TAMPERMONKEY_SNAPSHOT_VERSION):
            return False
    has_lines = bool(
        re.search(r"\b(?:account_lines|rate_lines)\s*=\s*[1-9]\d*\b", text)
        or re.search(r"\bfresh_(?:account|rate)_lines\s*=\s*[1-9]\d*\b", text)
    )
    return has_lines


def browser_coverage_detail(conn: sqlite3.Connection, provider: Provider) -> str | None:
    if not table_exists(conn, "browser_adapter_status"):
        return None
    row = conn.execute(
        """
        select status, detail, observed_at
        from browser_adapter_status
        where provider = ?
          and site = ?
        """,
        (provider.name, provider.site),
    ).fetchone()
    if not row or not browser_status_is_current_coverage(row[0], row[1]):
        return None
    return f"browser_readonly current coverage at {row[2]}: {row[1]}"


def upstream_hub_coverage(conn: sqlite3.Connection, provider: Provider) -> HubCoverage | None:
    if not table_exists(conn, "upstream_hub_channels"):
        return None
    sites = site_lookup_keys(provider.site)
    placeholders = ",".join("?" for _ in sites)
    row = conn.execute(
        f"""
        select channel_id, channel_name, site, last_balance_label, last_balance_at,
               last_error, imported_at
        from upstream_hub_channels
        where site in ({placeholders})
        order by imported_at desc, channel_id asc
        limit 1
        """,
        sites,
    ).fetchone()
    if not row:
        return None
    rate_count = 0
    if table_exists(conn, "upstream_hub_rate_observations"):
        rate_count = conn.execute(
            f"select count(*) from upstream_hub_rate_observations where site in ({placeholders})",
            sites,
        ).fetchone()[0]
    alias_note = "" if row["site"] == provider.site else f"; matched_site={row['site']}"
    detail = (
        f"upstream_hub channel_id={row['channel_id']}; channel={row['channel_name']}; "
        f"rates={rate_count}; balance={row['last_balance_label'] or '-'}; imported_at={row['imported_at']}"
        f"{alias_note}"
    )
    if row["last_error"]:
        detail += f"; last_error={str(row['last_error'])[:160]}"
    if row["last_error"]:
        return HubCoverage(detail, "hub_error", "upstream-hub error")
    if rate_count <= 0:
        return HubCoverage(detail, "hub_observed_empty", "upstream-hub observed but no rates")
    return HubCoverage(detail, "covered_by_upstream_hub", "covered by upstream-hub")


def update_matching_ledger_groups(
    conn: sqlite3.Connection,
    provider: Provider,
    group_ratios: dict[str, Any],
    now: str,
) -> int:
    if not table_exists(conn, "upstream_rate_records"):
        return 0
    for column in ("actual_cost_label", "note", "updated_at"):
        if not has_column(conn, "upstream_rate_records", column):
            return 0

    changed = 0
    for group_name, ratio in group_ratios.items():
        page_rate = number_or_none(ratio)
        if page_rate is None:
            continue
        rows = conn.execute(
            """
            select id, fluter_account_name, upstream_group, page_rate,
                   recharge_factor, site_account_multiplier, actual_cost_label,
                   status, note
            from upstream_rate_records
            where site = ?
              and upstream_group = ?
              and instr(kind, '生图') = 0
              and instr(kind, '特殊') = 0
            """,
            (provider.site, group_name),
        ).fetchall()
        for row in rows:
            old_page_rate = row["page_rate"]
            recharge_factor = float(row["recharge_factor"] or 1)
            label = public_actual_cost_label(provider, page_rate, recharge_factor)
            needs_update = (
                old_page_rate is None
                or abs(float(old_page_rate) - page_rate) > 0.0000001
                or row["actual_cost_label"] != label
            )
            if not needs_update:
                continue
            note_line = (
                f"[{now}] 公开价格接口同步：{provider.name} /api/pricing "
                f"将分组 {group_name} 的页面倍率同步为 {compact_rate(page_rate)}x。"
            )
            conn.execute(
                """
                update upstream_rate_records
                set page_rate = ?,
                    actual_cost_label = ?,
                    status = ?,
                    note = ?,
                    updated_at = ?
                where id = ?
                """,
                (
                    page_rate,
                    label,
                    status_after_public_rate(row, page_rate),
                    prepend_note_once(row["note"], note_line),
                    now,
                    row["id"],
                ),
            )
            changed += 1
    return changed


def delete_stale_provider_pricing(
    conn: sqlite3.Connection,
    provider: Provider,
    group_names: set[str],
    model_names: set[str],
) -> None:
    if group_names:
        placeholders = ",".join("?" for _ in group_names)
        conn.execute(
            f"""
            delete from provider_group_ratio_records
            where provider = ?
              and site = ?
              and group_name not in ({placeholders})
            """,
            (provider.name, provider.site, *sorted(group_names)),
        )
    else:
        conn.execute(
            "delete from provider_group_ratio_records where provider = ? and site = ?",
            (provider.name, provider.site),
        )

    if model_names:
        placeholders = ",".join("?" for _ in model_names)
        conn.execute(
            f"""
            delete from provider_model_pricing_records
            where provider = ?
              and site = ?
              and model_name not in ({placeholders})
            """,
            (provider.name, provider.site, *sorted(model_names)),
        )
    else:
        conn.execute(
            "delete from provider_model_pricing_records where provider = ? and site = ?",
            (provider.name, provider.site),
        )


def refresh_provider(
    conn: sqlite3.Connection,
    provider: Provider,
    timeout: int,
    now: str,
    update_ledger_page_rates: bool,
) -> tuple[str, str]:
    if provider.pricing_url is None:
        hub_coverage = upstream_hub_coverage(conn, provider)
        if hub_coverage:
            conn.execute(
                UPSERT_STATUS,
                (
                    provider.name,
                    provider.site,
                    "upstream_hub",
                    hub_coverage.status,
                    f"{provider.note}; {hub_coverage.detail}",
                    now,
                ),
            )
            return provider.name, hub_coverage.message

        browser_detail = browser_coverage_detail(conn, provider)
        if browser_detail:
            conn.execute(
                UPSERT_STATUS,
                (
                    provider.name,
                    provider.site,
                    "browser_readonly",
                    "covered_by_browser",
                    f"{provider.note}; {browser_detail}",
                    now,
                ),
            )
            return provider.name, "covered by browser adapter"

        conn.execute(
            UPSERT_STATUS,
            (
                provider.name,
                provider.site,
                "browser_or_key_required",
                "needs_adapter",
                provider.note,
                now,
            ),
        )
        return provider.name, "needs browser/API-key adapter"

    try:
        data = fetch_json(provider.pricing_url, timeout)
    except (urllib.error.URLError, TimeoutError, ValueError, json.JSONDecodeError) as exc:
        conn.execute(
            UPSERT_STATUS,
            (
                provider.name,
                provider.site,
                "public_pricing",
                "failed",
                str(exc)[:500],
                now,
            ),
        )
        return provider.name, f"failed: {exc}"

    pricing_version = data.get("pricing_version") or ""
    group_ratios = data.get("group_ratio") or {}
    model_names = {
        item.get("model_name")
        for item in data.get("data") or []
        if item.get("model_name")
    }
    delete_stale_provider_pricing(conn, provider, set(group_ratios), set(model_names))
    for group_name, ratio in group_ratios.items():
        page_rate = number_or_none(ratio)
        if page_rate is None:
            continue
        conn.execute(
            UPSERT_GROUP,
            (
                provider.name,
                provider.site,
                group_name,
                page_rate,
                pricing_version,
                provider.pricing_url,
                now,
            ),
        )

    model_count = 0
    for item in data.get("data") or []:
        model_name = item.get("model_name") or ""
        if not model_name:
            continue
        endpoints = ", ".join(item.get("supported_endpoint_types") or [])
        conn.execute(
            UPSERT_MODEL,
            (
                provider.name,
                provider.site,
                model_name,
                item.get("quota_type"),
                number_or_none(item.get("model_ratio")),
                number_or_none(item.get("completion_ratio")),
                number_or_none(item.get("cache_ratio")),
                number_or_none(item.get("create_cache_ratio")),
                number_or_none(item.get("model_price")),
                endpoints,
                pricing_version,
                provider.pricing_url,
                now,
            ),
        )
        model_count += 1

    group_updates: int | None = None
    if update_ledger_page_rates:
        group_updates = update_matching_ledger_groups(conn, provider, group_ratios, now)
    update_label = group_updates if group_updates is not None else "disabled"
    hub_coverage = upstream_hub_coverage(conn, provider)
    if hub_coverage:
        conn.execute(
            UPSERT_STATUS,
            (
                provider.name,
                provider.site,
                "upstream_hub",
                hub_coverage.status,
                (
                    f"{provider.note}; {hub_coverage.detail}; public_pricing groups={len(group_ratios)} "
                    f"models={model_count} ledger_group_updates={update_label}"
                ),
                now,
            ),
        )
        return (
            provider.name,
            (
                f"{hub_coverage.message}; public_pricing groups={len(group_ratios)} "
                f"models={model_count} ledger_group_updates={update_label}"
            ),
        )
    conn.execute(
        UPSERT_STATUS,
        (
            provider.name,
            provider.site,
            "public_pricing",
            "ok",
            f"groups={len(group_ratios)} models={model_count} ledger_group_updates={update_label}",
            now,
        ),
    )
    return provider.name, f"ok groups={len(group_ratios)} models={model_count} ledger_group_updates={update_label}"


def main() -> int:
    args = parse_args()
    db_path = Path(args.db)
    db_path.parent.mkdir(parents=True, exist_ok=True)
    now = datetime.now(timezone.utc).isoformat(timespec="seconds")
    conn = sqlite3.connect(db_path)
    conn.row_factory = sqlite3.Row
    with conn:
        conn.executescript(SCHEMA)
        active_pairs = {(provider.name, provider.site) for provider in PROVIDERS}
        if active_pairs:
            placeholders = ",".join(["(?, ?)"] * len(active_pairs))
            flat_values = [value for pair in active_pairs for value in pair]
            conn.execute(
                f"delete from upstream_adapter_status where (provider, site) not in ({placeholders})",
                flat_values,
            )
        results = [
            refresh_provider(conn, provider, args.timeout, now, args.update_ledger_page_rates)
            for provider in PROVIDERS
        ]
        conn.execute(
            "insert or replace into metadata(key, value) values (?, ?)",
            ("public_pricing_adapters_refreshed_at", now),
        )
        conn.execute(
            "insert or replace into metadata(key, value) values (?, ?)",
            (
                "public_pricing_adapters_note",
                "Public /api/pricing adapters snapshot group/model pricing; curated ledger page_rate updates are opt-in; browser/API-key providers are marked needs_adapter.",
            ),
        )
    conn.close()
    for provider, result in results:
        print(f"{provider}: {result}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
