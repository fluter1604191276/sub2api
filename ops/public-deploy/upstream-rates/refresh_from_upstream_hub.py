#!/usr/bin/env python3
"""Import upstream-hub observations into the Fluter upstream ledger.

This adapter is intentionally read-only with respect to upstream-hub and
sub2api. It reads upstream-hub's local Postgres tables and writes only the
independent Fluter upstream ledger SQLite database. It does not store upstream
credentials, cookies, API keys, or raw HTML.
"""

from __future__ import annotations

import argparse
import os
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


DEFAULT_DB = "/var/lib/fluterapi-upstream-rates/upstream_rates.sqlite"
DEFAULT_HUB_COMPOSE_DIR = "/Users/fluter_claw/Desktop/study_project/upstream-hub"
DEFAULT_HUB_CONTAINER = "upstreamhub-postgres"
DEFAULT_HUB_SERVICE = "postgres"
DEFAULT_HUB_DB = "upstreamhub"
DEFAULT_HUB_USER = "upstreamhub"
DEFAULT_HUB_HOST = "127.0.0.1"
DEFAULT_HUB_PORT = "54329"
DEFAULT_COLIMA_DOCKER_SOCK = Path.home() / ".colima" / "default" / "docker.sock"
SITE_ALIASES = {
    "api.tokenskingdom.com": ("api.tokenskingdom.com", "tokenskingdom.com", "image.tokenskingdom.com"),
    "tokenskingdom.com": ("tokenskingdom.com", "api.tokenskingdom.com", "image.tokenskingdom.com"),
    "image.tokenskingdom.com": ("image.tokenskingdom.com", "api.tokenskingdom.com", "tokenskingdom.com"),
}


def site_lookup_keys(site: str) -> tuple[str, ...]:
    normalized = str(site or "").strip().lower()
    return SITE_ALIASES.get(normalized, (normalized,))


@dataclass(frozen=True)
class HubChannel:
    id: int
    name: str
    type: str
    site_url: str
    site: str
    monitor_enabled: bool
    last_balance: Decimal | None
    last_balance_at: str
    last_error: str
    updated_at: str


@dataclass(frozen=True)
class HubRateSnapshot:
    channel_id: int
    model_name: str
    description: str
    ratio: Decimal
    completion_ratio: Decimal | None
    first_seen_at: str
    last_seen_at: str


@dataclass(frozen=True)
class HubBalanceSnapshot:
    channel_id: int
    balance: Decimal
    sampled_at: str


@dataclass(frozen=True)
class HubRateChange:
    channel_id: int
    model_name: str
    old_ratio: Decimal | None
    new_ratio: Decimal
    old_completion_ratio: Decimal | None
    new_completion_ratio: Decimal | None
    changed_at: str


SCHEMA = """
create table if not exists upstream_hub_channels (
  channel_id integer primary key,
  channel_name text not null,
  channel_type text not null,
  site text not null,
  site_url text not null,
  monitor_enabled integer not null,
  last_balance real,
  last_balance_label text not null default '',
  last_balance_at text not null default '',
  last_error text not null default '',
  hub_updated_at text not null default '',
  imported_at text not null
);

create table if not exists upstream_hub_rate_observations (
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

create table if not exists upstream_hub_balance_observations (
  channel_id integer not null,
  channel_name text not null,
  site text not null,
  balance real not null,
  balance_label text not null,
  sampled_at text not null,
  imported_at text not null,
  unique(channel_id, sampled_at)
);

create table if not exists upstream_hub_rate_change_observations (
  channel_id integer not null,
  channel_name text not null,
  site text not null,
  model_name text not null,
  old_ratio real,
  new_ratio real not null,
  old_completion_ratio real,
  new_completion_ratio real,
  changed_at text not null,
  imported_at text not null,
  unique(channel_id, model_name, changed_at)
);

create table if not exists upstream_adapter_status (
  provider text not null,
  site text not null,
  adapter_kind text not null,
  status text not null,
  detail text not null,
  observed_at text not null,
  unique(provider, site)
);

create table if not exists metadata (
  key text primary key,
  value text not null
);
"""

UPSERT_CHANNEL = """
insert into upstream_hub_channels (
  channel_id, channel_name, channel_type, site, site_url, monitor_enabled,
  last_balance, last_balance_label, last_balance_at, last_error, hub_updated_at, imported_at
) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
on conflict(channel_id) do update set
  channel_name = excluded.channel_name,
  channel_type = excluded.channel_type,
  site = excluded.site,
  site_url = excluded.site_url,
  monitor_enabled = excluded.monitor_enabled,
  last_balance = excluded.last_balance,
  last_balance_label = excluded.last_balance_label,
  last_balance_at = excluded.last_balance_at,
  last_error = excluded.last_error,
  hub_updated_at = excluded.hub_updated_at,
  imported_at = excluded.imported_at;
"""

UPSERT_RATE = """
insert into upstream_hub_rate_observations (
  channel_id, channel_name, site, model_name, description, page_rate,
  completion_ratio, first_seen_at, last_seen_at, imported_at
) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
on conflict(channel_id, model_name) do update set
  channel_name = excluded.channel_name,
  site = excluded.site,
  description = excluded.description,
  page_rate = excluded.page_rate,
  completion_ratio = excluded.completion_ratio,
  first_seen_at = excluded.first_seen_at,
  last_seen_at = excluded.last_seen_at,
  imported_at = excluded.imported_at;
"""

UPSERT_BALANCE = """
insert into upstream_hub_balance_observations (
  channel_id, channel_name, site, balance, balance_label, sampled_at, imported_at
) values (?, ?, ?, ?, ?, ?, ?)
on conflict(channel_id, sampled_at) do update set
  channel_name = excluded.channel_name,
  site = excluded.site,
  balance = excluded.balance,
  balance_label = excluded.balance_label,
  imported_at = excluded.imported_at;
"""

UPSERT_CHANGE = """
insert into upstream_hub_rate_change_observations (
  channel_id, channel_name, site, model_name, old_ratio, new_ratio,
  old_completion_ratio, new_completion_ratio, changed_at, imported_at
) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
on conflict(channel_id, model_name, changed_at) do update set
  channel_name = excluded.channel_name,
  site = excluded.site,
  old_ratio = excluded.old_ratio,
  new_ratio = excluded.new_ratio,
  old_completion_ratio = excluded.old_completion_ratio,
  new_completion_ratio = excluded.new_completion_ratio,
  imported_at = excluded.imported_at;
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


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Import read-only upstream-hub observations into Fluter ledger")
    parser.add_argument("--db", default=DEFAULT_DB)
    parser.add_argument("--hub-compose-dir", default=DEFAULT_HUB_COMPOSE_DIR)
    parser.add_argument("--hub-container", default=DEFAULT_HUB_CONTAINER)
    parser.add_argument("--hub-service", default=DEFAULT_HUB_SERVICE)
    parser.add_argument("--hub-db", default=DEFAULT_HUB_DB)
    parser.add_argument("--hub-user", default=DEFAULT_HUB_USER)
    parser.add_argument("--hub-host", default=DEFAULT_HUB_HOST)
    parser.add_argument("--hub-port", default=DEFAULT_HUB_PORT)
    parser.add_argument(
        "--docker-host",
        default="",
        help="Optional Docker endpoint for docker compose. If omitted, Colima's default socket is auto-detected.",
    )
    parser.add_argument(
        "--hub-password-env",
        default="POSTGRES_PASSWORD",
        help="Environment variable name used with --hub-connection tcp. The value is never printed.",
    )
    parser.add_argument(
        "--hub-env-file",
        default="",
        help="Optional upstream-hub .env file used only to read --hub-password-env for local TCP psql.",
    )
    parser.add_argument(
        "--hub-connection",
        choices=("docker", "tcp", "auto"),
        default="auto",
        help="How to query upstream-hub Postgres. auto tries local TCP first when a password is available, then Docker compose.",
    )
    parser.add_argument("--hub-query-timeout", type=int, default=15)
    parser.add_argument(
        "--hub-psql-command",
        default="",
        help="Override command used to run psql against upstream-hub. It must read SQL from stdin and output JSON.",
    )
    parser.add_argument(
        "--update-ledger-page-rates",
        action="store_true",
        help="Update matching upstream_rate_records rows by exact site + upstream_group match.",
    )
    parser.add_argument(
        "--export-json",
        default="",
        help="Write a sanitized upstream-hub snapshot JSON file after reading hub Postgres.",
    )
    parser.add_argument(
        "--export-only",
        action="store_true",
        help="Only write --export-json and skip SQLite import. Requires --export-json.",
    )
    parser.add_argument(
        "--import-json",
        default="",
        help="Import a sanitized upstream-hub snapshot JSON file instead of connecting to hub Postgres.",
    )
    return parser.parse_args(argv)


def read_env_file_value(path: str, key: str) -> str:
    if not path:
        return ""
    env_path = Path(path)
    if not env_path.exists():
        return ""
    for raw_line in env_path.read_text(errors="replace").splitlines():
        line = raw_line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        name, value = line.split("=", 1)
        if name.strip() != key:
            continue
        value = value.strip()
        if (value.startswith('"') and value.endswith('"')) or (value.startswith("'") and value.endswith("'")):
            value = value[1:-1]
        return value
    return ""


def hub_password(args: argparse.Namespace) -> str:
    return os.environ.get(args.hub_password_env) or read_env_file_value(
        args.hub_env_file or str(Path(args.hub_compose_dir) / ".env"),
        args.hub_password_env,
    )


def decimal_or_none(value: Any) -> Decimal | None:
    if value is None or value == "":
        return None
    try:
        return Decimal(str(value))
    except (InvalidOperation, ValueError):
        return None


def decimal_float(value: Decimal | None) -> float | None:
    return float(value) if value is not None else None


def compact_decimal(value: Decimal | float | int | None) -> str:
    if value is None:
        return "-"
    decimal = Decimal(str(value)).quantize(Decimal("0.000000001")).normalize()
    return format(decimal, "f")


def normalize_site(value: str) -> str:
    raw = str(value or "").strip()
    if not raw:
        return ""
    parsed = urlparse(raw if re.match(r"^[a-zA-Z][a-zA-Z0-9+.-]*://", raw) else f"https://{raw}")
    host = (parsed.hostname or raw).lower().strip()
    if host.startswith("www."):
        host = host[4:]
    return host


def provider_label(channel: HubChannel) -> str:
    return channel.name or channel.site


def format_balance_label(value: Decimal | None) -> str:
    if value is None:
        return ""
    return f"${compact_decimal(value)}"


def actual_cost_label(page_rate: Decimal, recharge_factor: Decimal, source: str = "upstream-hub") -> str:
    actual = page_rate * recharge_factor
    return (
        f"实际成本倍率 {compact_decimal(actual)}x"
        f"（{source} 页面倍率 {compact_decimal(page_rate)} × 充值系数 {compact_decimal(recharge_factor)}）"
    )


def table_exists(conn: sqlite3.Connection, table_name: str) -> bool:
    return bool(
        conn.execute(
            "select 1 from sqlite_master where type = 'table' and name = ?",
            (table_name,),
        ).fetchone()
    )


def has_column(conn: sqlite3.Connection, table_name: str, column: str) -> bool:
    return any(row[1] == column for row in conn.execute(f"pragma table_info({table_name})"))


def run_psql_command(
    command: str,
    wrapper_sql: str,
    timeout: int,
    env: dict[str, str] | None = None,
) -> list[dict[str, Any]]:
    proc = subprocess.run(
        command,
        input=wrapper_sql,
        text=True,
        shell=True,
        capture_output=True,
        check=False,
        env=env,
        timeout=timeout,
    )
    if proc.returncode != 0:
        raise RuntimeError(proc.stderr.strip() or f"psql command exited {proc.returncode}")
    payload = proc.stdout.strip() or "[]"
    return json.loads(payload)


def docker_command_env(args: argparse.Namespace) -> dict[str, str] | None:
    docker_host = args.docker_host or os.environ.get("DOCKER_HOST", "")
    if not docker_host and DEFAULT_COLIMA_DOCKER_SOCK.exists():
        docker_host = f"unix://{DEFAULT_COLIMA_DOCKER_SOCK}"
    if not docker_host:
        return None
    env = dict(os.environ)
    env["DOCKER_HOST"] = docker_host
    return env


def hub_query_commands(args: argparse.Namespace) -> list[tuple[str, str, dict[str, str] | None]]:
    if args.hub_psql_command:
        return [("custom", args.hub_psql_command, None)]

    commands: list[tuple[str, str, dict[str, str] | None]] = []
    password = hub_password(args)
    if args.hub_connection in {"auto", "tcp"} and password:
        env = dict(os.environ)
        env["PGPASSWORD"] = password
        env["PGCONNECT_TIMEOUT"] = str(max(1, min(int(args.hub_query_timeout), 10)))
        commands.append(
            (
                "tcp",
                (
                    "psql "
                    f"-h {shlex.quote(str(args.hub_host))} "
                    f"-p {shlex.quote(str(args.hub_port))} "
                    f"-U {shlex.quote(args.hub_user)} "
                    f"-d {shlex.quote(args.hub_db)} -At"
                ),
                env,
            )
        )
    if args.hub_connection in {"auto", "docker"}:
        docker_env = docker_command_env(args)
        commands.append(
            (
                "docker_compose",
                (
                    f"cd {shlex.quote(args.hub_compose_dir)} && "
                    f"docker compose exec -T {shlex.quote(args.hub_service)} "
                    f"psql -U {shlex.quote(args.hub_user)} -d {shlex.quote(args.hub_db)} -At"
                ),
                docker_env,
            )
        )
        commands.append(
            (
                "docker_exec",
                (
                    f"docker exec -i {shlex.quote(args.hub_container)} "
                    f"psql -U {shlex.quote(args.hub_user)} -d {shlex.quote(args.hub_db)} -At"
                ),
                docker_env,
            )
        )
    return commands


def run_hub_query(args: argparse.Namespace, sql: str) -> list[dict[str, Any]]:
    wrapper_sql = f"""
select coalesce(jsonb_agg(row_to_json(t)), '[]'::jsonb)::text
from (
{sql}
) t;
"""
    errors: list[str] = []
    commands = hub_query_commands(args)
    if not commands:
        raise SystemExit(
            "upstream-hub query failed:\n"
            f"no psql command available for hub_connection={args.hub_connection}; "
            f"set {args.hub_password_env} or use --hub-connection docker"
        )
    for label, command, env in commands:
        try:
            return run_psql_command(command, wrapper_sql, args.hub_query_timeout, env)
        except subprocess.TimeoutExpired as exc:
            errors.append(f"{label}: query timed out after {exc.timeout}s")
            if args.hub_connection != "auto":
                break
        except Exception as exc:
            errors.append(f"{label}: {exc}")
            if args.hub_connection != "auto":
                break
    raise SystemExit("upstream-hub query failed:\n" + "\n".join(errors))


def load_hub_channels(args: argparse.Namespace) -> list[HubChannel]:
    rows = run_hub_query(
        args,
        """
    select id, name, type, site_url, coalesce(monitor_enabled, false) as monitor_enabled,
           last_balance, last_balance_at::text as last_balance_at,
           coalesce(last_error, '') as last_error,
           updated_at::text as updated_at
    from channels
    where deleted_at is null
    order by id
""",
    )
    channels: list[HubChannel] = []
    for row in rows:
        site_url = row.get("site_url") or ""
        channels.append(
            HubChannel(
                id=int(row["id"]),
                name=row.get("name") or "",
                type=row.get("type") or "",
                site_url=site_url,
                site=normalize_site(site_url),
                monitor_enabled=bool(row.get("monitor_enabled")),
                last_balance=decimal_or_none(row.get("last_balance")),
                last_balance_at=row.get("last_balance_at") or "",
                last_error=row.get("last_error") or "",
                updated_at=row.get("updated_at") or "",
            )
        )
    return channels


def load_hub_rates(args: argparse.Namespace) -> list[HubRateSnapshot]:
    rows = run_hub_query(
        args,
        """
    select channel_id, model_name, coalesce(description, '') as description,
           ratio, completion_ratio, first_seen_at::text as first_seen_at,
           last_seen_at::text as last_seen_at
    from rate_snapshots
    order by channel_id, model_name
""",
    )
    rates: list[HubRateSnapshot] = []
    for row in rows:
        ratio = decimal_or_none(row.get("ratio"))
        if ratio is None:
            continue
        rates.append(
            HubRateSnapshot(
                channel_id=int(row["channel_id"]),
                model_name=row.get("model_name") or "",
                description=row.get("description") or "",
                ratio=ratio,
                completion_ratio=decimal_or_none(row.get("completion_ratio")),
                first_seen_at=row.get("first_seen_at") or "",
                last_seen_at=row.get("last_seen_at") or "",
            )
        )
    return rates


def load_hub_balances(args: argparse.Namespace, limit: int = 200) -> list[HubBalanceSnapshot]:
    rows = run_hub_query(
        args,
        f"""
    select channel_id, balance, sampled_at::text as sampled_at
    from balance_snapshots
    order by sampled_at desc, id desc
    limit {int(limit)}
""",
    )
    balances: list[HubBalanceSnapshot] = []
    for row in rows:
        balance = decimal_or_none(row.get("balance"))
        if balance is None:
            continue
        balances.append(
            HubBalanceSnapshot(
                channel_id=int(row["channel_id"]),
                balance=balance,
                sampled_at=row.get("sampled_at") or "",
            )
        )
    return balances


def load_hub_rate_changes(args: argparse.Namespace, limit: int = 200) -> list[HubRateChange]:
    rows = run_hub_query(
        args,
        f"""
    select channel_id, model_name, old_ratio, new_ratio,
           old_completion_ratio, new_completion_ratio,
           changed_at::text as changed_at
    from rate_change_logs
    order by changed_at desc, id desc
    limit {int(limit)}
""",
    )
    changes: list[HubRateChange] = []
    for row in rows:
        new_ratio = decimal_or_none(row.get("new_ratio"))
        if new_ratio is None:
            continue
        changes.append(
            HubRateChange(
                channel_id=int(row["channel_id"]),
                model_name=row.get("model_name") or "",
                old_ratio=decimal_or_none(row.get("old_ratio")),
                new_ratio=new_ratio,
                old_completion_ratio=decimal_or_none(row.get("old_completion_ratio")),
                new_completion_ratio=decimal_or_none(row.get("new_completion_ratio")),
                changed_at=row.get("changed_at") or "",
            )
        )
    return changes


def snapshot_payload(
    channels: list[HubChannel],
    rates: list[HubRateSnapshot],
    balances: list[HubBalanceSnapshot],
    changes: list[HubRateChange],
    exported_at: str,
) -> dict[str, Any]:
    """Build a sanitized snapshot that contains no credentials or raw HTML."""
    return {
        "schema": "fluter-upstream-hub-snapshot/v1",
        "exported_at": exported_at,
        "note": "sanitized upstream-hub observations only; no secret material or raw HTML",
        "channels": [
            {
                "id": channel.id,
                "name": channel.name,
                "type": channel.type,
                "site_url": channel.site_url,
                "site": channel.site,
                "monitor_enabled": channel.monitor_enabled,
                "last_balance": compact_decimal(channel.last_balance),
                "last_balance_at": channel.last_balance_at,
                "last_error": channel.last_error,
                "updated_at": channel.updated_at,
            }
            for channel in channels
        ],
        "rates": [
            {
                "channel_id": rate.channel_id,
                "model_name": rate.model_name,
                "description": rate.description,
                "ratio": compact_decimal(rate.ratio),
                "completion_ratio": compact_decimal(rate.completion_ratio) if rate.completion_ratio is not None else "",
                "first_seen_at": rate.first_seen_at,
                "last_seen_at": rate.last_seen_at,
            }
            for rate in rates
        ],
        "balances": [
            {
                "channel_id": balance.channel_id,
                "balance": compact_decimal(balance.balance),
                "sampled_at": balance.sampled_at,
            }
            for balance in balances
        ],
        "rate_changes": [
            {
                "channel_id": change.channel_id,
                "model_name": change.model_name,
                "old_ratio": compact_decimal(change.old_ratio) if change.old_ratio is not None else "",
                "new_ratio": compact_decimal(change.new_ratio),
                "old_completion_ratio": compact_decimal(change.old_completion_ratio)
                if change.old_completion_ratio is not None
                else "",
                "new_completion_ratio": compact_decimal(change.new_completion_ratio)
                if change.new_completion_ratio is not None
                else "",
                "changed_at": change.changed_at,
            }
            for change in changes
        ],
    }


def write_snapshot_json(
    path: str,
    channels: list[HubChannel],
    rates: list[HubRateSnapshot],
    balances: list[HubBalanceSnapshot],
    changes: list[HubRateChange],
    exported_at: str,
) -> None:
    output = Path(path)
    output.parent.mkdir(parents=True, exist_ok=True)
    payload = snapshot_payload(channels, rates, balances, changes, exported_at)
    output.write_text(json.dumps(payload, ensure_ascii=False, indent=2) + "\n")


def load_snapshot_json(path: str) -> tuple[list[HubChannel], list[HubRateSnapshot], list[HubBalanceSnapshot], list[HubRateChange]]:
    payload = json.loads(Path(path).read_text())
    if payload.get("schema") != "fluter-upstream-hub-snapshot/v1":
        raise SystemExit("unsupported upstream-hub snapshot schema")

    channels = [
        HubChannel(
            id=int(row["id"]),
            name=row.get("name") or "",
            type=row.get("type") or "",
            site_url=row.get("site_url") or "",
            site=normalize_site(row.get("site") or row.get("site_url") or ""),
            monitor_enabled=bool(row.get("monitor_enabled")),
            last_balance=decimal_or_none(row.get("last_balance")),
            last_balance_at=row.get("last_balance_at") or "",
            last_error=row.get("last_error") or "",
            updated_at=row.get("updated_at") or "",
        )
        for row in payload.get("channels", [])
    ]
    rates = []
    for row in payload.get("rates", []):
        ratio = decimal_or_none(row.get("ratio"))
        if ratio is None:
            continue
        rates.append(
            HubRateSnapshot(
                channel_id=int(row["channel_id"]),
                model_name=row.get("model_name") or "",
                description=row.get("description") or "",
                ratio=ratio,
                completion_ratio=decimal_or_none(row.get("completion_ratio")),
                first_seen_at=row.get("first_seen_at") or "",
                last_seen_at=row.get("last_seen_at") or "",
            )
        )
    balances = []
    for row in payload.get("balances", []):
        balance = decimal_or_none(row.get("balance"))
        if balance is None:
            continue
        balances.append(
            HubBalanceSnapshot(
                channel_id=int(row["channel_id"]),
                balance=balance,
                sampled_at=row.get("sampled_at") or "",
            )
        )
    changes = []
    for row in payload.get("rate_changes", []):
        new_ratio = decimal_or_none(row.get("new_ratio"))
        if new_ratio is None:
            continue
        changes.append(
            HubRateChange(
                channel_id=int(row["channel_id"]),
                model_name=row.get("model_name") or "",
                old_ratio=decimal_or_none(row.get("old_ratio")),
                new_ratio=new_ratio,
                old_completion_ratio=decimal_or_none(row.get("old_completion_ratio")),
                new_completion_ratio=decimal_or_none(row.get("new_completion_ratio")),
                changed_at=row.get("changed_at") or "",
            )
        )
    return channels, rates, balances, changes


def ensure_schema(conn: sqlite3.Connection) -> None:
    conn.executescript(SCHEMA)
    if table_exists(conn, "upstream_rate_records"):
        columns = {row[1] for row in conn.execute("pragma table_info(upstream_rate_records)")}
        if "actual_cost_label" not in columns:
            conn.execute("alter table upstream_rate_records add column actual_cost_label text not null default ''")
        if "balance_label" not in columns:
            conn.execute("alter table upstream_rate_records add column balance_label text not null default ''")
        if "balance_updated_at" not in columns:
            conn.execute("alter table upstream_rate_records add column balance_updated_at text not null default ''")


def update_matching_ledger_rates(
    conn: sqlite3.Connection,
    channels_by_id: dict[int, HubChannel],
    rates: list[HubRateSnapshot],
    imported_at: str,
) -> int:
    if not table_exists(conn, "upstream_rate_records"):
        return 0
    required = {"actual_cost_label", "note", "updated_at", "recharge_factor", "status"}
    if not all(has_column(conn, "upstream_rate_records", column) for column in required):
        return 0
    changed = 0
    for rate in rates:
        channel = channels_by_id.get(rate.channel_id)
        if not channel or not channel.site or not rate.model_name:
            continue
        sites = site_lookup_keys(channel.site)
        placeholders = ",".join("?" for _ in sites)
        rows = conn.execute(
            f"""
            select id, page_rate, recharge_factor, actual_cost_label, note
            from upstream_rate_records
            where site in ({placeholders})
              and upstream_group = ?
              and instr(kind, '生图') = 0
              and instr(kind, '特殊') = 0
            """,
            (*sites, rate.model_name),
        ).fetchall()
        for row in rows:
            recharge_factor = Decimal(str(row["recharge_factor"] or 1))
            label = actual_cost_label(rate.ratio, recharge_factor)
            old_page_rate = decimal_or_none(row["page_rate"])
            if old_page_rate == rate.ratio and row["actual_cost_label"] == label:
                continue
            note_line = (
                f"[{imported_at}] upstream-hub 同步：{provider_label(channel)} / {rate.model_name} "
                f"页面倍率 {compact_decimal(rate.ratio)}x。"
            )
            note = str(row["note"] or "")
            if note_line not in note:
                note = (note_line + "\n" + note)[:1800]
            conn.execute(
                """
                update upstream_rate_records
                set page_rate = ?,
                    actual_cost_label = ?,
                    note = ?,
                    updated_at = ?
                where id = ?
                """,
                (float(rate.ratio), label, note, imported_at, row["id"]),
            )
            changed += 1
    return changed


def update_matching_ledger_balances(
    conn: sqlite3.Connection,
    channels: list[HubChannel],
    imported_at: str,
) -> int:
    if not table_exists(conn, "upstream_rate_records"):
        return 0
    if not all(has_column(conn, "upstream_rate_records", column) for column in ("balance_label", "balance_updated_at")):
        return 0
    changed = 0
    for channel in channels:
        if not channel.site or channel.last_balance is None:
            continue
        label = format_balance_label(channel.last_balance)
        updated = f"upstream-hub 只读观察 {channel.last_balance_at or imported_at}"
        sites = site_lookup_keys(channel.site)
        placeholders = ",".join("?" for _ in sites)
        cur = conn.execute(
            f"""
            update upstream_rate_records
            set balance_label = ?,
                balance_updated_at = ?
            where site in ({placeholders})
              and coalesce(balance_label, '') <> ?
            """,
            (label, updated, *sites, label),
        )
        changed += cur.rowcount if cur.rowcount is not None else 0
    return changed


def write_observations(
    conn: sqlite3.Connection,
    channels: list[HubChannel],
    rates: list[HubRateSnapshot],
    balances: list[HubBalanceSnapshot],
    changes: list[HubRateChange],
    imported_at: str,
    update_ledger_page_rates: bool,
) -> tuple[int, int, int, int, int]:
    channels_by_id = {channel.id: channel for channel in channels}
    for channel in channels:
        balance_label = format_balance_label(channel.last_balance)
        conn.execute(
            UPSERT_CHANNEL,
            (
                channel.id,
                channel.name,
                channel.type,
                channel.site,
                channel.site_url,
                1 if channel.monitor_enabled else 0,
                decimal_float(channel.last_balance),
                balance_label,
                channel.last_balance_at,
                channel.last_error,
                channel.updated_at,
                imported_at,
            ),
        )
        conn.execute(
            UPSERT_STATUS,
            (
                provider_label(channel),
                channel.site or channel.name,
                "upstream_hub",
                "hub_observed" if not channel.last_error else "hub_error",
                (
                    f"channel_id={channel.id}; monitor_enabled={channel.monitor_enabled}; "
                    f"rates={sum(1 for rate in rates if rate.channel_id == channel.id)}; "
                    f"balance={balance_label or '-'}; last_error={channel.last_error[:160]}"
                ),
                imported_at,
            ),
        )
    for rate in rates:
        channel = channels_by_id.get(rate.channel_id)
        if not channel:
            continue
        conn.execute(
            UPSERT_RATE,
            (
                rate.channel_id,
                channel.name,
                channel.site,
                rate.model_name,
                rate.description,
                float(rate.ratio),
                decimal_float(rate.completion_ratio),
                rate.first_seen_at,
                rate.last_seen_at,
                imported_at,
            ),
        )
    for balance in balances:
        channel = channels_by_id.get(balance.channel_id)
        if not channel:
            continue
        conn.execute(
            UPSERT_BALANCE,
            (
                balance.channel_id,
                channel.name,
                channel.site,
                float(balance.balance),
                format_balance_label(balance.balance),
                balance.sampled_at,
                imported_at,
            ),
        )
    for change in changes:
        channel = channels_by_id.get(change.channel_id)
        if not channel:
            continue
        conn.execute(
            UPSERT_CHANGE,
            (
                change.channel_id,
                channel.name,
                channel.site,
                change.model_name,
                decimal_float(change.old_ratio),
                float(change.new_ratio),
                decimal_float(change.old_completion_ratio),
                decimal_float(change.new_completion_ratio),
                change.changed_at,
                imported_at,
            ),
        )
    ledger_rate_updates = update_matching_ledger_rates(conn, channels_by_id, rates, imported_at) if update_ledger_page_rates else 0
    ledger_balance_updates = update_matching_ledger_balances(conn, channels, imported_at)
    return len(channels), len(rates), len(balances), ledger_rate_updates, ledger_balance_updates


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)
    if args.export_only and not args.export_json:
        raise SystemExit("--export-only requires --export-json")
    if args.export_json and args.import_json:
        raise SystemExit("--export-json and --import-json are mutually exclusive")

    imported_at = datetime.now(timezone.utc).isoformat(timespec="seconds")
    source = "upstream-hub Postgres"
    if args.import_json:
        channels, rates, balances, changes = load_snapshot_json(args.import_json)
        source = f"sanitized upstream-hub snapshot {args.import_json}"
    else:
        channels = load_hub_channels(args)
        rates = load_hub_rates(args)
        balances = load_hub_balances(args)
        changes = load_hub_rate_changes(args)
        if args.export_json:
            write_snapshot_json(args.export_json, channels, rates, balances, changes, imported_at)
        if args.export_only:
            print(
                "upstream_hub_export "
                f"channels={len(channels)} rates={len(rates)} balances={len(balances)} "
                f"rate_changes={len(changes)} output={args.export_json}"
            )
            return 0

    db_path = Path(args.db)
    db_path.parent.mkdir(parents=True, exist_ok=True)
    conn = sqlite3.connect(db_path)
    conn.row_factory = sqlite3.Row
    with conn:
        ensure_schema(conn)
        channel_count, rate_count, balance_count, ledger_rate_updates, ledger_balance_updates = write_observations(
            conn,
            channels,
            rates,
            balances,
            changes,
            imported_at,
            args.update_ledger_page_rates,
        )
        for key, value in (
            (
                "source",
                "upstream-hub 登录态只读采集；生产库 accounts.rate_multiplier 只读查询",
            ),
            ("last_upstream_hub_imported_at", imported_at),
            ("last_upstream_hub_channel_count", str(channel_count)),
            ("last_upstream_hub_rate_count", str(rate_count)),
            ("last_upstream_hub_balance_snapshot_count", str(balance_count)),
            ("last_upstream_hub_ledger_rate_updates", str(ledger_rate_updates)),
            ("last_upstream_hub_ledger_balance_updates", str(ledger_balance_updates)),
            ("last_upstream_hub_note", f"read-only import from {source}; no production writes"),
        ):
            conn.execute("insert or replace into metadata(key, value) values (?, ?)", (key, value))
    conn.close()
    print(
        "upstream_hub_import "
        f"channels={len(channels)} rates={len(rates)} balances={len(balances)} "
        f"rate_changes={len(changes)} ledger_rate_updates={ledger_rate_updates} "
        f"ledger_balance_updates={ledger_balance_updates}"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
