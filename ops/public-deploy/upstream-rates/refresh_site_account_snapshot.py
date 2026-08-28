#!/usr/bin/env python3
"""Refresh read-only production account multiplier snapshots into the ledger.

This script reads sub2api PostgreSQL account/group metadata and writes only the
independent upstream ledger SQLite database. It does not modify production
accounts, groups, channels, pricing, keys, or credentials.
"""

from __future__ import annotations

import argparse
import json
import re
import shlex
import sqlite3
import subprocess
from dataclasses import dataclass
from datetime import datetime, timezone
from decimal import Decimal, InvalidOperation
from pathlib import Path
from typing import Any
from urllib.parse import urlparse

from discount_profiles import effective_discount_for_site, load_discount_profiles


DEFAULT_DB = "/var/lib/fluterapi-upstream-rates/upstream_rates.sqlite"
DEFAULT_COMPOSE_DIR = "/www/sub2api"
MODIFIED_PREFIX = "（修改）"
JUNCHE_RECHARGE_FACTOR = Decimal("0.925925926")
KNOWN_RECHARGE_BY_HOST: dict[str, tuple[str, Decimal]] = {
    "vip.lcodex.cn": ("充100到账108（成本系数0.925926）", JUNCHE_RECHARGE_FACTOR),
}
OBSERVATION_ONLY_STATUS_MARKERS = (
    "上游观察",
    "未接入",
)
BROWSER_ACCOUNT_OBSERVATION_MAX_AGE_SECONDS = 3600


SCHEMA = """
create table if not exists site_account_snapshots (
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

create index if not exists idx_site_account_snapshots_normalized_name
  on site_account_snapshots(normalized_account_name);

create table if not exists metadata (
  key text primary key,
  value text not null
);
"""


@dataclass(frozen=True)
class GroupSnapshot:
    id: int
    name: str
    status: str
    rate_multiplier: Decimal | None
    priority: int | None
    allow_image_generation: bool
    image_rate_independent: bool
    image_rate_multiplier: Decimal | None
    image_price_1k: Decimal | None
    image_price_2k: Decimal | None
    image_price_4k: Decimal | None


@dataclass(frozen=True)
class AccountSnapshot:
    id: int
    name: str
    platform: str
    base_url: str
    status: str
    schedulable: bool
    rate_multiplier: Decimal | None
    updated_at: str
    groups: list[GroupSnapshot]


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Refresh site account multiplier snapshots")
    parser.add_argument("--db", default=DEFAULT_DB)
    parser.add_argument("--ssh-host", default="fluterapi-prod")
    parser.add_argument("--compose-dir", default=DEFAULT_COMPOSE_DIR)
    parser.add_argument("--local-postgres", action="store_true")
    return parser.parse_args()


def normalize_name(value: str) -> str:
    name = (value or "").strip()
    while name.startswith(MODIFIED_PREFIX):
        name = name[len(MODIFIED_PREFIX) :].strip()
    return name


def account_stem(value: str) -> str:
    name = normalize_name(value)
    name = name.replace("（", "(").replace("）", ")")
    name = re.sub(r"cc\s*max", "max", name, flags=re.IGNORECASE)
    name = re.sub(r"\s+", " ", name).strip()
    suffix_patterns = [
        # Compact labels glued to the numeric suffix: "仅文字0.08",
        # "仅生图3.6分", "ccmax仅客户端1.1".
        r"(?:仅文字|仅生图|文字|生图|仅客户端|不限客户端)\s*\d+(?:\.\d+)?(?:分)?(?:\s*x)?(?:\s*/\s*(?:次|张))?\s*$",
        # Plain trailing multipliers/prices: "0.18", "1.35x", "0.009/次".
        r"\s+(?:倍率\s*)?\d+(?:\.\d+)?(?:\s*x)?(?:\s*/\s*(?:次|张))?\s*$",
        # Calculated suffixes kept in account names: "0.2*0.9=0.18",
        # "0.078*23=1.014", "3.6分*0.93=3.2728分".
        r"\s*\d+(?:\.\d+)?(?:分)?\s*[*×x]\s*\d+(?:\.\d+)?(?:分)?\s*=\s*\d+(?:\.\d+)?(?:分)?\s*$",
        # Text labels that usually introduce the numeric cost suffix.
        r"\s*(?:仅文字|仅生图|文字|生图|仅客户端|不限客户端)\s*[-：:]?\s*$",
    ]
    changed = True
    while changed:
        changed = False
        for pattern in suffix_patterns:
            new_name = re.sub(pattern, "", name, flags=re.IGNORECASE).strip()
            if new_name != name:
                name = new_name
                changed = True
    return name.lower()


def browser_account_key(value: str) -> str:
    value = normalize_name(value).lower()
    value = re.sub(r"sk-[a-z0-9._-]+", "", value)
    value = re.sub(r"\.\.\.redacted(?:-long-token)?\.\.\.", "", value)
    value = value.replace("（", "(").replace("）", ")")
    value = re.sub(r"[\s/_:：,，;；|｜·\-+()（）\[\]【】<>《》\"'“”‘’]", "", value)
    return value


def parse_observed_datetime(value: Any) -> datetime | None:
    raw = str(value or "").strip()
    if not raw:
        return None
    try:
        parsed = datetime.fromisoformat(raw.replace("Z", "+00:00"))
    except ValueError:
        return None
    if parsed.tzinfo is None:
        parsed = parsed.replace(tzinfo=timezone.utc)
    return parsed.astimezone(timezone.utc)


def observed_within(
    observed_at: Any,
    reference_at: Any,
    max_age_seconds: int = BROWSER_ACCOUNT_OBSERVATION_MAX_AGE_SECONDS,
) -> bool:
    observed = parse_observed_datetime(observed_at)
    reference = parse_observed_datetime(reference_at)
    if observed is None or reference is None:
        return False
    return abs((reference - observed).total_seconds()) <= max_age_seconds


def semantic_match_tokens(account_name: str, upstream_group: str) -> tuple[str, ...]:
    text = f"{account_name} {upstream_group}".lower()
    tokens: list[str] = []
    if "代理快速" in text:
        tokens.append("代理快速")
    if "pro" in text:
        tokens.append("pro")
    if "plus" in text:
        tokens.append("plus")
    if "claude" in text and "max" in text:
        tokens.append("claude-max")
    return tuple(tokens)


def token_matches_snapshot(token: str, snapshot: sqlite3.Row) -> bool:
    text = f"{snapshot['account_name']} {snapshot['account_stem']}".lower()
    if token == "代理快速":
        return "代理快速" in text
    if token == "pro":
        return "pro" in text
    if token == "plus":
        return "plus" in text
    if token == "claude-max":
        return "claude" in text and "max" in text
    return False


def semantic_candidates(row: sqlite3.Row, snapshots: list[sqlite3.Row]) -> list[sqlite3.Row]:
    if sqlite_host(row["site"]) == "vip.lcodex.cn":
        return []
    tokens = semantic_match_tokens(row["fluter_account_name"], row["upstream_group"])
    if not tokens:
        return []
    host = sqlite_host(row["site"])
    candidates = [snapshot for snapshot in snapshots if snapshot["base_host"] == host]
    for token in tokens:
        narrowed = [snapshot for snapshot in candidates if token_matches_snapshot(token, snapshot)]
        if narrowed:
            candidates = narrowed
    return candidates


def decimal_or_none(value: Any) -> Decimal | None:
    if value is None or value == "":
        return None
    try:
        return Decimal(str(value))
    except (InvalidOperation, ValueError):
        return None


def decimal_label(value: Decimal | None) -> str:
    if value is None:
        return "-"
    return format(value.normalize(), "f")


def sqlite_number(value: Decimal | None) -> float | None:
    return None if value is None else float(value)


def base_host(base_url: str) -> str:
    parsed = urlparse(base_url or "")
    host = parsed.netloc or parsed.path
    return host.split("@")[-1].split(":")[0].lower()


def sqlite_host(value: str) -> str:
    return base_host(value if "://" in (value or "") else f"https://{value or ''}")


def is_observation_only_status(status: str) -> bool:
    """Rows that document upstream-only keys must not be renamed by site snapshots."""

    return any(marker in (status or "") for marker in OBSERVATION_ONLY_STATUS_MARKERS)


def table_exists(conn: sqlite3.Connection, table_name: str) -> bool:
    return bool(
        conn.execute(
            "select 1 from sqlite_master where type='table' and name=?",
            (table_name,),
        ).fetchone()
    )


def group_label(groups: list[GroupSnapshot]) -> str:
    if not groups:
        return "未分配/当前不调度"
    labels: list[str] = []
    for group in sorted(groups, key=lambda item: (item.priority is None, item.priority or 0, item.name)):
        label = f"{group.name}@{decimal_label(group.rate_multiplier)}"
        extras: list[str] = []
        if group.status and group.status != "active":
            extras.append(group.status)
        if group.allow_image_generation:
            if group.image_rate_independent:
                price_parts = []
                for size, price in (
                    ("1K", group.image_price_1k),
                    ("2K", group.image_price_2k),
                    ("4K", group.image_price_4k),
                ):
                    if price is not None:
                        price_parts.append(f"{size}={decimal_label(price)}")
                if price_parts:
                    extras.append("生图独立价 " + "/".join(price_parts))
                elif group.image_rate_multiplier is not None:
                    extras.append(f"生图倍率 {decimal_label(group.image_rate_multiplier)}")
                else:
                    extras.append("允许生图")
            elif group.image_rate_multiplier is not None:
                extras.append(f"生图倍率 {decimal_label(group.image_rate_multiplier)}")
            else:
                extras.append("允许生图")
        if extras:
            label += "（" + "，".join(extras) + "）"
        labels.append(label)
    return "; ".join(labels)


def run_remote_or_local(args: argparse.Namespace, command: str, input_text: str | None = None) -> subprocess.CompletedProcess[str]:
    if args.local_postgres:
        return subprocess.run(
            command,
            input=input_text,
            text=True,
            shell=True,
            capture_output=True,
            check=False,
        )
    return subprocess.run(
        ["ssh", "-T", args.ssh_host, command],
        input=input_text,
        text=True,
        capture_output=True,
        check=False,
    )


def run_psql(args: argparse.Namespace, sql: str) -> str:
    command = (
        f"cd {shlex.quote(args.compose_dir)} && "
        "docker compose exec -T postgres psql -U sub2api -d sub2api -At"
    )
    proc = run_remote_or_local(args, command, sql)
    if proc.returncode != 0:
        raise SystemExit(f"PostgreSQL command failed:\n{proc.stderr.strip()}")
    return proc.stdout


def load_accounts(args: argparse.Namespace) -> list[AccountSnapshot]:
    sql = """
copy (
  select coalesce(jsonb_agg(row_to_json(t) order by name, id), '[]'::jsonb)
  from (
    select
      a.id,
      a.name,
      coalesce(a.platform, '') as platform,
      coalesce(a.credentials->>'base_url', '') as base_url,
      coalesce(a.status, '') as status,
      coalesce(a.schedulable, false) as schedulable,
      a.rate_multiplier::text as rate_multiplier,
      coalesce(a.updated_at::text, '') as updated_at,
      coalesce(
        jsonb_agg(
          jsonb_build_object(
            'id', g.id,
            'name', g.name,
            'status', coalesce(g.status, ''),
            'rate_multiplier', g.rate_multiplier::text,
            'priority', ag.priority,
            'allow_image_generation', coalesce(g.allow_image_generation, false),
            'image_rate_independent', coalesce(g.image_rate_independent, false),
            'image_rate_multiplier', g.image_rate_multiplier::text,
            'image_price_1k', g.image_price_1k::text,
            'image_price_2k', g.image_price_2k::text,
            'image_price_4k', g.image_price_4k::text
          )
          order by ag.priority nulls last, g.name
        ) filter (where g.id is not null),
        '[]'::jsonb
      ) as groups
    from accounts a
    left join account_groups ag on ag.account_id = a.id
    left join groups g on g.id = ag.group_id and g.deleted_at is null
    where a.deleted_at is null
    group by a.id
  ) t
) to stdout;
"""
    payload = run_psql(args, sql).strip() or "[]"
    raw_accounts = json.loads(payload)
    accounts: list[AccountSnapshot] = []
    for row in raw_accounts:
        groups = [
            GroupSnapshot(
                id=int(group["id"]),
                name=group.get("name") or "",
                status=group.get("status") or "",
                rate_multiplier=decimal_or_none(group.get("rate_multiplier")),
                priority=int(group["priority"]) if group.get("priority") is not None else None,
                allow_image_generation=bool(group.get("allow_image_generation")),
                image_rate_independent=bool(group.get("image_rate_independent")),
                image_rate_multiplier=decimal_or_none(group.get("image_rate_multiplier")),
                image_price_1k=decimal_or_none(group.get("image_price_1k")),
                image_price_2k=decimal_or_none(group.get("image_price_2k")),
                image_price_4k=decimal_or_none(group.get("image_price_4k")),
            )
            for group in row.get("groups") or []
        ]
        accounts.append(
            AccountSnapshot(
                id=int(row["id"]),
                name=row.get("name") or "",
                platform=row.get("platform") or "",
                base_url=row.get("base_url") or "",
                status=row.get("status") or "",
                schedulable=bool(row.get("schedulable")),
                rate_multiplier=decimal_or_none(row.get("rate_multiplier")),
                updated_at=row.get("updated_at") or "",
                groups=groups,
            )
        )
    return accounts


def write_snapshots(db_path: str, accounts: list[AccountSnapshot], observed_at: str) -> int:
    path = Path(db_path)
    path.parent.mkdir(parents=True, exist_ok=True)
    conn = sqlite3.connect(path)
    conn.row_factory = sqlite3.Row
    with conn:
        conn.executescript(SCHEMA)
        snapshot_columns = {
            row["name"] for row in conn.execute("pragma table_info(site_account_snapshots)")
        }
        if "account_stem" not in snapshot_columns:
            conn.execute(
                "alter table site_account_snapshots add column account_stem text not null default ''"
            )
        conn.execute(
            """
            create index if not exists idx_site_account_snapshots_host_stem
              on site_account_snapshots(base_host, account_stem)
            """
        )
        conn.execute("delete from site_account_snapshots")
        for account in accounts:
            group_payload = [
                {
                    "id": group.id,
                    "name": group.name,
                    "status": group.status,
                    "rate_multiplier": sqlite_number(group.rate_multiplier),
                    "priority": group.priority,
                    "allow_image_generation": group.allow_image_generation,
                    "image_rate_independent": group.image_rate_independent,
                    "image_rate_multiplier": sqlite_number(group.image_rate_multiplier),
                    "image_price_1k": sqlite_number(group.image_price_1k),
                    "image_price_2k": sqlite_number(group.image_price_2k),
                    "image_price_4k": sqlite_number(group.image_price_4k),
                }
                for group in account.groups
            ]
            conn.execute(
                """
                insert into site_account_snapshots (
                  account_id, account_name, normalized_account_name, account_stem, platform,
                  base_url, base_host, status, schedulable, rate_multiplier,
                  groups_json, group_label, production_updated_at, observed_at
                ) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                """,
                (
                    account.id,
                    account.name,
                    normalize_name(account.name),
                    account_stem(account.name),
                    account.platform,
                    account.base_url,
                    base_host(account.base_url),
                    account.status,
                    1 if account.schedulable else 0,
                    sqlite_number(account.rate_multiplier),
                    json.dumps(group_payload, ensure_ascii=False),
                    group_label(account.groups),
                    account.updated_at,
                    observed_at,
                ),
            )

        matched, exact_matches, stem_matches, ambiguous_matches = refresh_ledger_rows(conn, observed_at)
        conn.execute(
            "insert or replace into metadata(key, value) values (?, ?)",
            ("site_account_snapshot_refreshed_at", observed_at),
        )
        conn.execute(
            "insert or replace into metadata(key, value) values (?, ?)",
            (
                "site_account_snapshot_note",
                "Read-only production accounts/groups snapshot. It refreshes ledger display columns only and never edits production accounts.",
            ),
        )
        conn.execute(
            "insert or replace into metadata(key, value) values (?, ?)",
            (
                "site_account_snapshot_match_summary",
                json.dumps(
                    {
                        "matched": matched,
                        "exact_name": exact_matches,
                        "host_stem": stem_matches,
                        "ambiguous_or_missing": ambiguous_matches,
                    },
                    ensure_ascii=False,
                ),
            ),
        )
    conn.close()
    return matched


def decimal_from_sqlite(value: Any) -> Decimal | None:
    if value is None:
        return None
    return decimal_or_none(value)


def compact_decimal(value: Decimal | None) -> str:
    if value is None:
        return "-"
    return format(value.quantize(Decimal("0.000000001")).normalize(), "f")


def same_decimal(left: Any, right: Any) -> bool:
    left_decimal = decimal_from_sqlite(left)
    right_decimal = decimal_from_sqlite(right)
    if left_decimal is None or right_decimal is None:
        return left_decimal is right_decimal
    return abs(left_decimal - right_decimal) < Decimal("0.000000001")


def reconciled_status(
    current_status: str,
    page_rate: Any,
    recharge_factor: Any,
    site_multiplier: Any,
) -> str:
    if current_status not in ("已确认", "已覆盖", "偏保守", "需核对", "需核对/倍率漂移", "上游分组已消失/待重映射"):
        return current_status
    page = decimal_from_sqlite(page_rate)
    multiplier = decimal_from_sqlite(site_multiplier)
    if page is None or multiplier is None or page <= 0:
        return "需核对" if current_status == "上游分组已消失/待重映射" else current_status
    recharge = decimal_from_sqlite(recharge_factor) or Decimal("1")
    actual = page * recharge
    if actual <= 0:
        return current_status
    coverage = multiplier / actual
    if coverage < Decimal("0.999"):
        return "需核对/倍率漂移"
    if coverage > Decimal("1.05"):
        return "偏保守"
    if current_status in ("需核对", "需核对/倍率漂移", "偏保守"):
        return "已确认"
    if current_status == "上游分组已消失/待重映射":
        return "已确认"
    return current_status


def append_change_note(note: str, observed_at: str, old_name: str, snapshot: sqlite3.Row, match_type: str) -> str:
    rate = compact_decimal(decimal_from_sqlite(snapshot["rate_multiplier"]))
    if old_name == snapshot["account_name"]:
        line = f"[{observed_at}] 生产账号快照：同步账号成本倍率 {rate}x；匹配方式 {match_type}。"
    else:
        line = (
            f"[{observed_at}] 生产账号快照：{old_name} -> {snapshot['account_name']}，"
            f"账号成本倍率 {rate}x；匹配方式 {match_type}。"
        )
    return (line + "\n" + (note or ""))[:1800]


def infer_category_kind(account_name: str, groups: str) -> tuple[str, str]:
    text = f"{account_name} {groups}".lower()
    if "钧澈" in account_name:
        category = "钧澈"
    else:
        category = "未分类"
    if "生图" in account_name:
        kind = "生图"
    elif "claude" in text:
        kind = "Claude"
    elif "pro" in text:
        kind = "Codex Pro"
    elif "codex" in text:
        kind = "Codex"
    else:
        kind = "未分类"
    return category, kind


def browser_account_lookup(conn: sqlite3.Connection, reference_at: str) -> dict[tuple[str, str], sqlite3.Row]:
    if not table_exists(conn, "browser_adapter_account_observations"):
        return {}
    rows = conn.execute(
        """
        select *
        from browser_adapter_account_observations
        order by observed_at asc
        """
    ).fetchall()
    lookup: dict[tuple[str, str], sqlite3.Row] = {}
    for row in rows:
        if not observed_within(row["observed_at"], reference_at):
            continue
        lookup[(row["site"], row["normalized_account_name"])] = row
    return lookup


def insert_missing_snapshot_rows(
    conn: sqlite3.Connection,
    observed_at: str,
    snapshots: list[sqlite3.Row],
) -> int:
    """Add current production accounts to the independent ledger when observed.

    This is deliberately conservative: it requires a fresh-ish browser account
    observation for the same site/name so old production rows are not promoted
    into the cost ledger without an upstream key-page cross-check.
    """

    browser_accounts = browser_account_lookup(conn, observed_at)
    discount_profiles = load_discount_profiles(conn)
    inserted = 0
    for snapshot in snapshots:
        host = snapshot["base_host"]
        if host not in KNOWN_RECHARGE_BY_HOST and host not in discount_profiles:
            continue
        key = (host, browser_account_key(snapshot["account_name"]))
        browser = browser_accounts.get(key)
        if not browser:
            continue
        existing = conn.execute(
            """
            select 1
            from upstream_rate_records
            where site = ? and fluter_account_name = ?
            limit 1
            """,
            (host, snapshot["account_name"]),
        ).fetchone()
        if existing:
            continue
        page_rate = browser["page_rate"]
        if page_rate is None:
            page_rate = snapshot["rate_multiplier"]
        fallback_label, fallback_factor = KNOWN_RECHARGE_BY_HOST.get(host, ("1:1", Decimal("1")))
        discount = effective_discount_for_site(discount_profiles, host, fallback_factor, fallback_label)
        category, kind = infer_category_kind(snapshot["account_name"], snapshot["group_label"])
        note = (
            f"[{observed_at}] 台账自动补行：生产账号快照和浏览器只读账号行均看到该账号；"
            "仅写入独立台账主表，未改生产账号。"
        )
        conn.execute(
            """
            insert into upstream_rate_records (
              category, kind, site, fluter_account_name, upstream_group, page_rate,
              recharge_ratio_label, recharge_factor, site_account_multiplier,
              site_group_multiplier, actual_cost_label, status, note, updated_at
            ) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                category,
                kind,
                host,
                snapshot["account_name"],
                browser["upstream_group"] or "待映射",
                page_rate,
                discount.recharge_ratio_label,
                float(discount.recharge_factor),
                snapshot["rate_multiplier"],
                snapshot["group_label"] or "未分配/当前不调度",
                "",
                "已确认",
                note,
                observed_at,
            ),
        )
        inserted += 1
    return inserted


def refresh_ledger_rows(conn: sqlite3.Connection, observed_at: str) -> tuple[int, int, int, int]:
    snapshots = conn.execute("select * from site_account_snapshots").fetchall()
    by_name: dict[str, list[sqlite3.Row]] = {}
    by_host_stem: dict[tuple[str, str], list[sqlite3.Row]] = {}
    for snapshot in snapshots:
        by_name.setdefault(snapshot["normalized_account_name"], []).append(snapshot)
        by_host_stem.setdefault((snapshot["base_host"], snapshot["account_stem"]), []).append(snapshot)

    rows = conn.execute(
        """
        select id, site, fluter_account_name, upstream_group, page_rate,
               recharge_factor, site_account_multiplier, site_group_multiplier, status, note
        from upstream_rate_records
        """
    ).fetchall()

    matched = 0
    exact_matches = 0
    stem_matches = 0
    ambiguous_matches = 0
    for row in rows:
        if is_observation_only_status(row["status"]):
            ambiguous_matches += 1
            continue
        normalized = normalize_name(row["fluter_account_name"])
        match_type = "exact_name"
        candidates = by_name.get(normalized, [])
        if len(candidates) != 1:
            match_type = "host_stem"
            candidates = by_host_stem.get((sqlite_host(row["site"]), account_stem(normalized)), [])
        if len(candidates) != 1:
            match_type = "semantic_host"
            candidates = semantic_candidates(row, snapshots)
        if len(candidates) != 1:
            ambiguous_matches += 1
            continue

        snapshot = candidates[0]
        if match_type == "exact_name":
            exact_matches += 1
        else:
            stem_matches += 1
        old_name = row["fluter_account_name"]
        new_name = snapshot["account_name"]
        update_name = new_name
        conflict = conn.execute(
            """
            select 1
            from upstream_rate_records
            where site = ? and fluter_account_name = ? and upstream_group = ? and id <> ?
            limit 1
            """,
            (row["site"], new_name, row["upstream_group"], row["id"]),
        ).fetchone()
        if conflict:
            update_name = old_name

        changed = (
            update_name != old_name
            or not same_decimal(row["site_account_multiplier"], snapshot["rate_multiplier"])
            or (snapshot["group_label"] and snapshot["group_label"] != row["site_group_multiplier"])
        )
        note = row["note"]
        if changed:
            note = append_change_note(note, observed_at, old_name, snapshot, match_type)

        conn.execute(
            """
            update upstream_rate_records
            set fluter_account_name = ?,
                site_account_multiplier = ?,
                site_group_multiplier = coalesce(nullif(?, ''), site_group_multiplier),
                status = ?,
                note = ?,
                updated_at = ?
            where id = ?
            """,
            (
                update_name,
                snapshot["rate_multiplier"],
                snapshot["group_label"],
                reconciled_status(
                    row["status"],
                    row["page_rate"],
                    row["recharge_factor"],
                    snapshot["rate_multiplier"],
                ),
                note,
                observed_at,
                row["id"],
            ),
        )
        matched += 1
    inserted = insert_missing_snapshot_rows(conn, observed_at, snapshots)
    if inserted:
        conn.execute(
            "create table if not exists metadata (key text primary key, value text not null)"
        )
        conn.execute(
            "insert or replace into metadata(key, value) values (?, ?)",
            ("site_account_snapshot_inserted_missing_rows", str(inserted)),
        )
    return matched, exact_matches, stem_matches, ambiguous_matches


def main() -> int:
    args = parse_args()
    observed_at = datetime.now(timezone.utc).isoformat(timespec="seconds")
    accounts = load_accounts(args)
    matched = write_snapshots(args.db, accounts, observed_at)
    active = sum(1 for account in accounts if account.status == "active")
    schedulable = sum(1 for account in accounts if account.schedulable)
    print(
        "site_account_snapshot: "
        f"accounts={len(accounts)} active={active} schedulable={schedulable} "
        f"ledger_rows_matched={matched} observed_at={observed_at}"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
