#!/usr/bin/env python3
"""Refresh API-based upstream balance snapshots into the Fluter ledger.

This script reads production account credentials into process memory only long
enough to call known read-only balance endpoints. It writes only sanitized
balance summaries and adapter status into the independent ledger SQLite DB.

It never stores API keys, Bearer tokens, cookies, passwords, or raw upstream
responses, and it never modifies production sub2api accounts/groups/channels.
"""

from __future__ import annotations

import argparse
import json
import re
import shlex
import sqlite3
import subprocess
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass
from datetime import datetime, timezone
from decimal import Decimal, InvalidOperation
from pathlib import Path
from typing import Any


DEFAULT_DB = "/var/lib/fluterapi-upstream-rates/upstream_rates.sqlite"
DEFAULT_COMPOSE_DIR = "/www/sub2api"
DEFAULT_ACCOUNT_LIMIT = 250


SCHEMA = """
create table if not exists balance_api_adapter_status (
  provider text not null,
  site text not null,
  adapter_kind text not null,
  status text not null,
  detail text not null,
  observed_at text not null,
  unique(provider, site)
);

create table if not exists balance_api_snapshots (
  provider text not null,
  site text not null,
  account_id integer not null,
  account_name text not null,
  endpoint text not null,
  remaining real,
  used real,
  total real,
  unit text not null,
  balance_label text not null,
  source text not null,
  observed_at text not null,
  unique(provider, site, account_id, endpoint)
);

create table if not exists metadata (
  key text primary key,
  value text not null
);
"""


UPSERT_STATUS = """
insert into balance_api_adapter_status (
  provider, site, adapter_kind, status, detail, observed_at
) values (?, ?, ?, ?, ?, ?)
on conflict(provider, site) do update set
  adapter_kind = excluded.adapter_kind,
  status = excluded.status,
  detail = excluded.detail,
  observed_at = excluded.observed_at;
"""


UPSERT_SNAPSHOT = """
insert into balance_api_snapshots (
  provider, site, account_id, account_name, endpoint, remaining, used, total,
  unit, balance_label, source, observed_at
) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
on conflict(provider, site, account_id, endpoint) do update set
  account_name = excluded.account_name,
  remaining = excluded.remaining,
  used = excluded.used,
  total = excluded.total,
  unit = excluded.unit,
  balance_label = excluded.balance_label,
  source = excluded.source,
  observed_at = excluded.observed_at;
"""


@dataclass(frozen=True)
class Account:
    id: int
    name: str
    platform: str
    base_url: str
    api_key: str
    status: str
    schedulable: bool

    @property
    def host(self) -> str:
        return base_host(self.base_url)


@dataclass(frozen=True)
class BalanceSnapshot:
    provider: str
    site: str
    account_id: int
    account_name: str
    endpoint: str
    remaining: float | None
    used: float | None
    total: float | None
    unit: str
    balance_label: str
    source: str


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Refresh API-based upstream balance snapshots")
    parser.add_argument("--db", default=DEFAULT_DB)
    parser.add_argument("--ssh-host", default="fluterapi-prod")
    parser.add_argument("--compose-dir", default=DEFAULT_COMPOSE_DIR)
    parser.add_argument("--local-postgres", action="store_true")
    parser.add_argument("--timeout", type=int, default=5)
    parser.add_argument("--account-limit", type=int, default=DEFAULT_ACCOUNT_LIMIT)
    parser.add_argument(
        "--max-accounts-per-site",
        type=int,
        default=2,
        help="Probe at most this many representative accounts per provider/site.",
    )
    parser.add_argument(
        "--include-unschedulable",
        action="store_true",
        help="Also try accounts with schedulable=false. Default keeps probes to routed accounts.",
    )
    return parser.parse_args()


def run_remote_or_local(
    args: argparse.Namespace,
    command: str,
    input_text: str | None = None,
) -> subprocess.CompletedProcess[str]:
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
    return extract_json_payload(proc.stdout)


def extract_json_payload(value: str) -> str:
    text = (value or "").strip()
    if not text:
        return "[]"
    starts = [index for index in (text.find("["), text.find("{")) if index >= 0]
    if not starts:
        return text
    start = min(starts)
    return text[start:].strip()


def load_accounts(args: argparse.Namespace) -> list[Account]:
    schedulable_filter = "" if args.include_unschedulable else "and coalesce(a.schedulable, false) = true"
    sql = f"""
copy (
  select coalesce(jsonb_agg(row_to_json(t) order by lower(name), id), '[]'::jsonb)
  from (
    select
      a.id,
      a.name,
      coalesce(a.platform, '') as platform,
      coalesce(a.credentials->>'base_url', '') as base_url,
      coalesce(a.credentials->>'api_key', '') as api_key,
      coalesce(a.status, '') as status,
      coalesce(a.schedulable, false) as schedulable
    from accounts a
    where a.deleted_at is null
      and a.status = 'active'
      {schedulable_filter}
      and coalesce(a.credentials->>'api_key', '') <> ''
      and coalesce(a.credentials->>'base_url', '') <> ''
      and (
        lower(a.name) like '%kbq%'
        or lower(a.name) like '%kingdom%'
        or lower(a.name) like '%magic%'
        or lower(a.name) like '%meow%'
        or a.name like '%钧澈%'
        or a.name like '%超超%'
        or a.name like '%聪明%'
        or lower(a.credentials->>'base_url') like '%congmingai.com%'
      )
    order by lower(a.name), a.id
    limit {int(args.account_limit)}
  ) t
) to stdout;
"""
    payload = run_psql(args, sql).strip() or "[]"
    raw = json.loads(payload)
    return [
        Account(
            id=int(row["id"]),
            name=row.get("name") or "",
            platform=row.get("platform") or "",
            base_url=row.get("base_url") or "",
            api_key=row.get("api_key") or "",
            status=row.get("status") or "",
            schedulable=bool(row.get("schedulable")),
        )
        for row in raw
    ]


def account_probe_priority(account: Account) -> tuple[int, int, str]:
    name = account.name.lower()
    if "仅生图" in account.name or "生图" in account.name:
        category = 4
    elif "按次" in account.name:
        category = 3
    elif "kbq" in name:
        category = 0
    elif account.schedulable:
        category = 1
    else:
        category = 2
    return category, account.id, account.name


def select_probe_accounts(accounts: list[Account], max_per_site: int) -> list[Account]:
    if max_per_site <= 0:
        return accounts
    grouped: dict[tuple[str, str], list[Account]] = {}
    for account in accounts:
        grouped.setdefault((provider_name(account), account.host), []).append(account)
    selected: list[Account] = []
    for items in grouped.values():
        selected.extend(sorted(items, key=account_probe_priority)[:max_per_site])
    return sorted(selected, key=lambda item: (provider_name(item), item.host, account_probe_priority(item)))


def base_host(value: str) -> str:
    raw = (value or "").strip()
    parsed = urllib.parse.urlparse(raw if "://" in raw else f"https://{raw}")
    host = parsed.netloc or parsed.path
    return host.split("@")[-1].split(":")[0].lower()


def provider_name(account: Account) -> str:
    lowered = account.name.lower()
    if "kbq" in lowered or "xn--vduyey89e.com" in account.host:
        return "KBQ"
    if "kingdom" in lowered or "tokenskingdom.com" in account.host:
        return "Kingdom"
    if "magic" in lowered or "gptstore.club" in account.host:
        return "Magic"
    if "meow" in lowered or "saki.lat" in account.host:
        return "Meow"
    if "钧澈" in account.name or "lcodex.cn" in account.host:
        return "钧澈"
    if "超超" in account.name or "mouubox.com" in account.host:
        return "超超 Mouubox"
    if "聪明" in account.name or "congmingai.com" in account.host:
        return "聪明AI"
    return account.host or "unknown"


def normalize_base_url(value: str) -> str:
    return (value or "").strip().rstrip("/")


def root_base_url(value: str) -> str:
    parsed = urllib.parse.urlparse(normalize_base_url(value))
    if not parsed.scheme or not parsed.netloc:
        return normalize_base_url(value)
    return urllib.parse.urlunparse((parsed.scheme, parsed.netloc, "", "", "", "")).rstrip("/")


def decimal_or_none(value: Any) -> Decimal | None:
    if value is None or value == "":
        return None
    try:
        return Decimal(str(value))
    except (InvalidOperation, ValueError):
        return None


def float_or_none(value: Any) -> float | None:
    number = decimal_or_none(value)
    return None if number is None else float(number)


def compact_number(value: float | None) -> str:
    if value is None:
        return ""
    text = f"{value:.6f}".rstrip("0").rstrip(".")
    return text or "0"


def newapi_quota_to_display(value: float | None) -> float | None:
    if value is None:
        return None
    # NewAPI stores quota in token-like units where 500000 equals 1 display unit.
    if abs(value) >= 10000:
        return value / 500000
    return value


def format_balance_label(remaining: float | None, unit: str) -> str:
    if remaining is None:
        return "余额接口未返回数值"
    prefix = "¥" if unit == "CNY" else "$" if unit == "USD" else ""
    return f"{prefix}{compact_number(remaining)}"


def build_auth_headers(
    api_key: str,
    auth_style: str = "bearer",
    extra_headers: dict[str, str] | None = None,
) -> dict[str, str]:
    if auth_style == "bearer":
        authorization = f"Bearer {api_key}"
    elif auth_style == "raw":
        authorization = api_key
    else:
        raise ValueError(f"unsupported auth_style: {auth_style}")
    headers = {
        "Accept": "application/json",
        "Authorization": authorization,
        "User-Agent": "fluter-balance-api-adapter/1.0",
    }
    if extra_headers:
        headers.update(extra_headers)
    return headers


def fetch_json(
    url: str,
    api_key: str,
    extra_headers: dict[str, str] | None,
    timeout: int,
    auth_style: str = "bearer",
) -> dict[str, Any]:
    headers = build_auth_headers(api_key, auth_style, extra_headers)
    request = urllib.request.Request(url, headers=headers)
    with urllib.request.urlopen(request, timeout=timeout) as response:
        raw = response.read(262144).decode("utf-8", errors="replace")
    data = json.loads(raw)
    if not isinstance(data, dict):
        raise ValueError("JSON root is not an object")
    return data


def parse_newapi_user_self(account: Account, data: dict[str, Any], endpoint: str) -> BalanceSnapshot | None:
    if data.get("success") is False:
        raise ValueError(str(data.get("message") or "success=false"))
    payload = data.get("data")
    if not isinstance(payload, dict):
        return None

    quota = float_or_none(payload.get("quota"))
    used_quota = float_or_none(payload.get("used_quota"))
    remaining = newapi_quota_to_display(quota)
    used = newapi_quota_to_display(used_quota)
    total = None
    if remaining is not None and used is not None:
        total = remaining + used

    if remaining is None and used is None:
        return None

    unit = "CNY" if account.host.endswith(".cn") or "vduyey" in account.host else "USD"
    return BalanceSnapshot(
        provider=provider_name(account),
        site=account.host,
        account_id=account.id,
        account_name=account.name,
        endpoint=endpoint,
        remaining=remaining,
        used=used,
        total=total,
        unit=unit,
        balance_label=format_balance_label(remaining, unit),
        source="newapi_user_self",
    )


def parse_newapi_dashboard(account: Account, data: dict[str, Any], endpoint: str) -> BalanceSnapshot | None:
    payload = data.get("data") if isinstance(data.get("data"), dict) else data
    if not isinstance(payload, dict):
        return None
    possible_remaining_keys = (
        "quota",
        "balance",
        "remaining",
        "remain_quota",
        "available_quota",
    )
    possible_used_keys = ("used_quota", "used", "used_amount", "quota_consumption")
    remaining = None
    used = None
    for key in possible_remaining_keys:
        remaining = float_or_none(payload.get(key))
        if remaining is not None:
            break
    for key in possible_used_keys:
        used = float_or_none(payload.get(key))
        if used is not None:
            break
    remaining = newapi_quota_to_display(remaining)
    used = newapi_quota_to_display(used)
    if remaining is None and used is None:
        return None
    unit = "CNY" if account.host.endswith(".cn") or "vduyey" in account.host else "USD"
    total = remaining + used if remaining is not None and used is not None else None
    return BalanceSnapshot(
        provider=provider_name(account),
        site=account.host,
        account_id=account.id,
        account_name=account.name,
        endpoint=endpoint,
        remaining=remaining,
        used=used,
        total=total,
        unit=unit,
        balance_label=format_balance_label(remaining, unit),
        source="newapi_dashboard",
    )


def endpoint_candidates(account: Account) -> list[tuple[str, str, dict[str, str] | None, str]]:
    root = root_base_url(account.base_url)
    if not root:
        return []
    endpoints = [
        ("newapi_user_self", f"{root}/api/user/self", None),
        ("newapi_dashboard", f"{root}/api/user/dashboard", None),
    ]
    candidates = [
        (kind, url, headers, auth_style)
        for kind, url, headers in endpoints
        for auth_style in ("bearer", "raw")
    ]
    # Deduplicate by URL + header set + auth style.
    seen: set[tuple[str, tuple[tuple[str, str], ...], str]] = set()
    unique: list[tuple[str, str, dict[str, str] | None, str]] = []
    for kind, url, headers, auth_style in candidates:
        key = (url, tuple(sorted((headers or {}).items())), auth_style)
        if key in seen:
            continue
        seen.add(key)
        unique.append((kind, url, headers, auth_style))
    return unique


def try_fetch_balance(account: Account, timeout: int) -> tuple[BalanceSnapshot | None, str]:
    errors: list[str] = []
    for kind, url, headers, auth_style in endpoint_candidates(account):
        endpoint = urllib.parse.urlparse(url).path or url
        try:
            data = fetch_json(url, account.api_key, headers, timeout, auth_style=auth_style)
            if kind == "newapi_dashboard":
                snapshot = parse_newapi_dashboard(account, data, endpoint)
            else:
                snapshot = parse_newapi_user_self(account, data, endpoint)
            if snapshot is not None:
                return snapshot, f"ok via {endpoint} auth={auth_style}"
            errors.append(f"{endpoint} auth={auth_style}: no balance fields")
        except urllib.error.HTTPError as exc:
            body = exc.read(512).decode("utf-8", errors="replace")
            detail = sanitize_error_body(body, secrets=[account.api_key])
            errors.append(f"{endpoint} auth={auth_style}: HTTP {exc.code}{(' ' + detail) if detail else ''}")
        except (OSError, urllib.error.URLError, TimeoutError, json.JSONDecodeError, ValueError) as exc:
            detail = sanitize_error_body(str(exc), secrets=[account.api_key])
            errors.append(f"{endpoint} auth={auth_style}: {type(exc).__name__} {detail[:120]}")
    return None, "; ".join(errors)[:900] if errors else "no supported endpoint"


def sanitize_error_body(value: str, secrets: list[str] | None = None) -> str:
    text = value or ""
    for secret in secrets or []:
        if secret and len(secret) >= 8:
            text = re.sub(re.escape(secret), "***", text)
    text = re.sub(r"sk-[A-Za-z0-9_-]+", "sk-***", text)
    text = re.sub(r"Bearer\s+[A-Za-z0-9._-]+", "Bearer ***", text, flags=re.IGNORECASE)
    text = re.sub(
        r"(?i)(authorization|access[_-]?token|api[_-]?key)([\"'\s:=]+)([A-Za-z0-9._-]{12,})",
        r"\1\2***",
        text,
    )
    text = re.sub(r"\s+", " ", text).strip()
    return text[:180]


def choose_best_snapshots(snapshots: list[BalanceSnapshot]) -> list[BalanceSnapshot]:
    best_by_site: dict[str, BalanceSnapshot] = {}
    for snapshot in snapshots:
        previous = best_by_site.get(snapshot.site)
        if previous is None:
            best_by_site[snapshot.site] = snapshot
            continue
        previous_has_remaining = previous.remaining is not None
        current_has_remaining = snapshot.remaining is not None
        if current_has_remaining and not previous_has_remaining:
            best_by_site[snapshot.site] = snapshot
            continue
        if current_has_remaining == previous_has_remaining and snapshot.account_id < previous.account_id:
            best_by_site[snapshot.site] = snapshot
    return list(best_by_site.values())


def apply_balances_to_ledger(conn: sqlite3.Connection, snapshots: list[BalanceSnapshot], now: str) -> list[str]:
    messages: list[str] = []
    for snapshot in choose_best_snapshots(snapshots):
        before = conn.execute(
            """
            select count(*) as total,
                   sum(case when coalesce(balance_label, '') <> ? then 1 else 0 end) as changed
            from upstream_rate_records
            where site = ?
            """,
            (snapshot.balance_label, snapshot.site),
        ).fetchone()
        if before is None or before["total"] == 0:
            continue
        conn.execute(
            """
            update upstream_rate_records
            set balance_label = ?,
                balance_updated_at = ?,
                updated_at = ?
            where site = ?
              and coalesce(balance_label, '') <> ?
            """,
            (snapshot.balance_label, now, now, snapshot.site, snapshot.balance_label),
        )
        changed = int(before["changed"] or 0)
        messages.append(f"{snapshot.provider}: balance -> {snapshot.balance_label} ({changed} rows)")
    return messages


def write_results(
    db_path: str,
    accounts: list[Account],
    snapshots: list[BalanceSnapshot],
    failures: dict[tuple[str, str], list[str]],
    now: str,
) -> None:
    path = Path(db_path)
    path.parent.mkdir(parents=True, exist_ok=True)
    conn = sqlite3.connect(path)
    conn.row_factory = sqlite3.Row
    with conn:
        conn.executescript(SCHEMA)
        active_pairs = {(provider_name(account), account.host) for account in accounts}
        if active_pairs:
            placeholders = ",".join(["(?, ?)"] * len(active_pairs))
            flat_values = [value for pair in active_pairs for value in pair]
            conn.execute(
                f"delete from balance_api_adapter_status where (provider, site) not in ({placeholders})",
                flat_values,
            )
        for snapshot in snapshots:
            conn.execute(
                UPSERT_SNAPSHOT,
                (
                    snapshot.provider,
                    snapshot.site,
                    snapshot.account_id,
                    snapshot.account_name,
                    snapshot.endpoint,
                    snapshot.remaining,
                    snapshot.used,
                    snapshot.total,
                    snapshot.unit,
                    snapshot.balance_label,
                    snapshot.source,
                    now,
                ),
            )

        snapshots_by_pair: dict[tuple[str, str], list[BalanceSnapshot]] = {}
        for snapshot in snapshots:
            snapshots_by_pair.setdefault((snapshot.provider, snapshot.site), []).append(snapshot)

        for account in accounts:
            pair = (provider_name(account), account.host)
            if pair in snapshots_by_pair:
                sample = snapshots_by_pair[pair][0]
                detail = (
                    f"balance={sample.balance_label}; accounts_ok={len(snapshots_by_pair[pair])}; "
                    f"source={sample.source}; account_sample={sample.account_name}"
                )
                conn.execute(
                    UPSERT_STATUS,
                    (pair[0], pair[1], "api_balance", "ok", detail[:900], now),
                )
            else:
                details = failures.get(pair) or ["no supported balance endpoint returned data"]
                conn.execute(
                    UPSERT_STATUS,
                    (
                        pair[0],
                        pair[1],
                        "api_balance",
                        "unsupported_or_failed",
                        "; ".join(details)[:900],
                        now,
                    ),
                )

        ledger_updates = apply_balances_to_ledger(conn, snapshots, now)
        conn.execute(
            "insert or replace into metadata(key, value) values (?, ?)",
            ("balance_api_adapters_refreshed_at", now),
        )
        conn.execute(
            "insert or replace into metadata(key, value) values (?, ?)",
            (
                "balance_api_adapters_note",
                "API balance adapters read production account credentials into memory only, call known read-only balance endpoints, store sanitized balance summaries only, and never edit production accounts.",
            ),
        )
        conn.execute(
            "insert or replace into metadata(key, value) values (?, ?)",
            ("balance_api_adapters_ledger_updates", json.dumps(ledger_updates, ensure_ascii=False)),
        )
    conn.close()


def main() -> int:
    args = parse_args()
    now = datetime.now(timezone.utc).isoformat(timespec="seconds")
    accounts = load_accounts(args)
    snapshots: list[BalanceSnapshot] = []
    failures: dict[tuple[str, str], list[str]] = {}

    probe_accounts = select_probe_accounts(accounts, args.max_accounts_per_site)

    for account in probe_accounts:
        snapshot, detail = try_fetch_balance(account, args.timeout)
        pair = (provider_name(account), account.host)
        if snapshot is None:
            failures.setdefault(pair, []).append(f"{account.name}: {detail}")
            continue
        snapshots.append(snapshot)

    write_results(args.db, probe_accounts, snapshots, failures, now)
    sites_ok = len({(snapshot.provider, snapshot.site) for snapshot in snapshots})
    sites_seen = len({(provider_name(account), account.host) for account in probe_accounts})
    print(
        "balance_api_adapters: "
        f"accounts_available={len(accounts)} accounts_checked={len(probe_accounts)} sites_seen={sites_seen} "
        f"snapshots={len(snapshots)} sites_ok={sites_ok} observed_at={now}"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
