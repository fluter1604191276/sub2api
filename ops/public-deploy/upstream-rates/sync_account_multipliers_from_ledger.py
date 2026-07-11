#!/usr/bin/env python3
"""Preview account multiplier drift and optionally create disabled draft accounts.

Default mode is dry-run. The safe write path does not edit existing production
accounts. It creates a new disabled draft account copied from the old account:
active + schedulable=false + no account_groups. Operators can review the draft
in the admin UI before manually assigning groups or replacing the old account.
"""

from __future__ import annotations

import argparse
import json
import re
import shlex
import sqlite3
import subprocess
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from decimal import Decimal, InvalidOperation
from urllib.parse import urlparse


DEFAULT_DB = "/var/lib/fluterapi-upstream-rates/upstream_rates.sqlite"
DEFAULT_COMPOSE_DIR = "/www/sub2api"
DEFAULT_BACKUP_DIR = "/www/sub2api/backups"
MODIFIED_PREFIX = "（修改）"
MAX_ACCOUNT_NAME_LEN = 100
BEIJING_TZ_NAME = "Asia/Shanghai"
BEIJING_TZ = timezone(timedelta(hours=8), name="CST")

SKIP_KIND_KEYWORDS = ("生图", "特殊")
SKIP_STATUS_KEYWORDS = (
    "未接入",
    "未分配",
    "未调度",
    "停用",
    "特殊",
    "尺寸",
    "缩水",
    "无生图权限",
    "非生图",
    "文字调度",
    "未确认",
    "需核对",
)


@dataclass(frozen=True)
class LedgerRow:
    id: int
    category: str
    kind: str
    site: str
    account_name: str
    upstream_group: str
    page_rate: Decimal | None
    recharge_factor: Decimal
    site_account_multiplier: Decimal | None
    status: str
    note: str

    @property
    def actual_cost_multiplier(self) -> Decimal | None:
        if self.page_rate is None:
            return None
        return self.page_rate * self.recharge_factor


@dataclass(frozen=True)
class AccountRow:
    id: int
    name: str
    base_url: str
    base_host: str
    stem: str
    status: str
    schedulable: bool
    rate_multiplier: Decimal | None
    notes: str

    @property
    def is_draft(self) -> bool:
        return self.name.startswith(MODIFIED_PREFIX) or draft_source_id(self.notes) is not None


@dataclass(frozen=True)
class PlannedChange:
    ledger: LedgerRow
    account: AccountRow
    match_type: str
    target_multiplier: Decimal
    new_name: str
    note_line: str


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Dry-run account multiplier drift or create disabled draft accounts from upstream ledger"
    )
    parser.add_argument("--db", default=DEFAULT_DB)
    parser.add_argument("--ssh-host", default="us-api-vps")
    parser.add_argument("--compose-dir", default=DEFAULT_COMPOSE_DIR)
    parser.add_argument("--backup-dir", default=DEFAULT_BACKUP_DIR)
    parser.add_argument("--local-postgres", action="store_true")
    parser.add_argument(
        "--create-drafts",
        action="store_true",
        help="Create disabled draft accounts instead of editing existing accounts",
    )
    parser.add_argument(
        "--apply",
        action="store_true",
        help="Deprecated safety stop: use --create-drafts instead",
    )
    parser.add_argument("--include-inactive", action="store_true")
    parser.add_argument("--include-image", action="store_true")
    parser.add_argument(
        "--include-conservative",
        action="store_true",
        help="Also plan drafts when the current account multiplier is higher than ledger cost",
    )
    parser.add_argument("--threshold", default="0.000001")
    parser.add_argument("--max-changes", type=int, default=50)
    return parser.parse_args()


def decimal_or_none(value) -> Decimal | None:
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
        # "0.078*23=1.014", "3.6分*0.91=3.2728分".
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


def token_matches_account(token: str, account: AccountRow) -> bool:
    text = f"{account.name} {account.stem}".lower()
    if token == "代理快速":
        return "代理快速" in text
    if token == "pro":
        return "pro" in text
    if token == "plus":
        return "plus" in text
    if token == "claude-max":
        return "claude" in text and "max" in text
    return False


def semantic_account_matches(row: LedgerRow, accounts: list[AccountRow]) -> list[AccountRow]:
    tokens = semantic_match_tokens(row.account_name, row.upstream_group)
    if not tokens:
        return []
    host = base_host(row.site)
    candidates = [account for account in accounts if account.base_host == host and not account.is_draft]
    for token in tokens:
        narrowed = [account for account in candidates if token_matches_account(token, account)]
        if narrowed:
            candidates = narrowed
    return candidates


def draft_source_id(notes: str) -> int | None:
    match = re.search(r"原账号\s*id=(\d+)", notes or "")
    if not match:
        return None
    return int(match.group(1))


def make_draft_name(account: AccountRow) -> str:
    suffix = f" #{account.id}"
    raw = account.name.strip()
    draft = f"{MODIFIED_PREFIX}{raw}"
    if len(draft) <= MAX_ACCOUNT_NAME_LEN:
        return draft
    available_with_suffix = MAX_ACCOUNT_NAME_LEN - len(MODIFIED_PREFIX) - len(suffix)
    return f"{MODIFIED_PREFIX}{raw[:max(1, available_with_suffix)].rstrip()}{suffix}"


def base_host(value: str) -> str:
    raw = value or ""
    parsed = urlparse(raw if "://" in raw else f"https://{raw}")
    host = parsed.netloc or parsed.path
    return host.split("@")[-1].split(":")[0].lower()


def sql_quote(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


def beijing_tz():
    return BEIJING_TZ


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
    base = (
        f"cd {shlex.quote(args.compose_dir)} && "
        "docker compose exec -T postgres psql -U sub2api -d sub2api -At"
    )
    proc = run_remote_or_local(args, base, sql)
    if proc.returncode != 0:
        raise SystemExit(f"PostgreSQL command failed:\n{proc.stderr.strip()}")
    return proc.stdout


def create_backup(args: argparse.Namespace) -> str:
    stamp = datetime.now(beijing_tz()).strftime("%Y%m%d-%H%M%S")
    backup_path = f"{args.backup_dir.rstrip('/')}/account-drafts-before-{stamp}.sql"
    command = (
        f"mkdir -p {shlex.quote(args.backup_dir)} && "
        f"cd {shlex.quote(args.compose_dir)} && "
        f"docker compose exec -T postgres pg_dump -U sub2api -d sub2api > {shlex.quote(backup_path)}"
    )
    proc = run_remote_or_local(args, command)
    if proc.returncode != 0:
        raise SystemExit(f"Backup failed:\n{proc.stderr.strip()}")
    return backup_path


def load_ledger_rows(db_path: str) -> list[LedgerRow]:
    conn = sqlite3.connect(db_path)
    conn.row_factory = sqlite3.Row
    rows = []
    for row in conn.execute(
        """
        select id, category, kind, site, fluter_account_name, upstream_group,
               page_rate, recharge_factor, site_account_multiplier, status, note
        from upstream_rate_records
        order by category, fluter_account_name, upstream_group
        """
    ):
        rows.append(
            LedgerRow(
                id=int(row["id"]),
                category=row["category"],
                kind=row["kind"],
                site=row["site"],
                account_name=row["fluter_account_name"],
                upstream_group=row["upstream_group"],
                page_rate=decimal_or_none(row["page_rate"]),
                recharge_factor=decimal_or_none(row["recharge_factor"]) or Decimal("1"),
                site_account_multiplier=decimal_or_none(row["site_account_multiplier"]),
                status=row["status"],
                note=row["note"],
            )
        )
    conn.close()
    return rows


def load_accounts(args: argparse.Namespace) -> list[AccountRow]:
    sql = """
copy (
  select coalesce(jsonb_agg(row_to_json(t) order by id), '[]'::jsonb)
    from (
    select id, name, coalesce(credentials->>'base_url', '') as base_url,
           status, schedulable, rate_multiplier::text as rate_multiplier,
           coalesce(notes, '') as notes
    from accounts
    where deleted_at is null
  ) t
) to stdout;
"""
    payload = run_psql(args, sql).strip() or "[]"
    rows = json.loads(payload)
    return [
        AccountRow(
            id=int(row["id"]),
            name=row["name"],
            base_url=row.get("base_url") or "",
            base_host=base_host(row.get("base_url") or ""),
            stem=account_stem(row["name"]),
            status=row.get("status") or "",
            schedulable=bool(row.get("schedulable")),
            rate_multiplier=decimal_or_none(row.get("rate_multiplier")),
            notes=row.get("notes") or "",
        )
        for row in rows
    ]


def skip_reason(row: LedgerRow, args: argparse.Namespace) -> str | None:
    if not args.include_image and any(keyword in row.kind for keyword in SKIP_KIND_KEYWORDS):
        return f"类型 {row.kind} 默认不按文字倍率同步"
    if row.actual_cost_multiplier is None:
        return "台账缺少 page_rate，无法计算实际成本倍率"
    if not args.include_inactive and any(keyword in row.status for keyword in SKIP_STATUS_KEYWORDS):
        return f"状态 {row.status} 默认跳过"
    return None


def match_account(
    row: LedgerRow,
    accounts_by_name: dict[str, list[AccountRow]],
    accounts_by_host_stem: dict[tuple[str, str], list[AccountRow]],
    accounts: list[AccountRow],
) -> tuple[list[AccountRow], str]:
    normalized = normalize_name(row.account_name)
    exact_matches = accounts_by_name.get(normalized, [])
    if len(exact_matches) == 1:
        return exact_matches, "exact_name"
    if len(exact_matches) > 1:
        same_host = [account for account in exact_matches if account.base_host == base_host(row.site)]
        if len(same_host) == 1:
            return same_host, "exact_name_host"

    stem_matches = accounts_by_host_stem.get((base_host(row.site), account_stem(normalized)), [])
    if stem_matches:
        return stem_matches, "host_stem"
    semantic_matches = semantic_account_matches(row, accounts)
    if semantic_matches:
        return semantic_matches, "semantic_host"
    if exact_matches:
        return exact_matches, "ambiguous_exact_name"
    return [], "missing"


def plan_changes(args: argparse.Namespace) -> tuple[list[PlannedChange], list[str]]:
    threshold = Decimal(str(args.threshold))
    ledger_rows = load_ledger_rows(args.db)
    accounts = load_accounts(args)
    accounts_by_name: dict[str, list[AccountRow]] = {}
    accounts_by_host_stem: dict[tuple[str, str], list[AccountRow]] = {}
    for account in accounts:
        if account.is_draft:
            continue
        accounts_by_name.setdefault(normalize_name(account.name), []).append(account)
        accounts_by_host_stem.setdefault((account.base_host, account.stem), []).append(account)
    existing_draft_source_ids = {
        source_id
        for account in accounts
        for source_id in [draft_source_id(account.notes)]
        if source_id is not None
    }

    changes: list[PlannedChange] = []
    skipped: list[str] = []
    seen_accounts: set[int] = set()
    for row in ledger_rows:
        reason = skip_reason(row, args)
        if reason:
            skipped.append(f"{row.account_name}: {reason}")
            continue
        matches, match_type = match_account(row, accounts_by_name, accounts_by_host_stem, accounts)
        if not matches:
            skipped.append(f"{row.account_name}: 生产库找不到同名账号，也找不到同站点账号骨架匹配")
            continue
        if len(matches) > 1:
            ids = ", ".join(str(match.id) for match in matches)
            skipped.append(f"{row.account_name}: 生产库匹配不唯一 ids={ids}，匹配方式={match_type}")
            continue
        account = matches[0]
        if not args.include_inactive and account.status != "active":
            skipped.append(f"{row.account_name}: 生产账号 id={account.id} 状态为 {account.status}，默认跳过")
            continue
        if account.id in seen_accounts:
            skipped.append(f"{row.account_name}: 同一个生产账号被多条台账记录命中，跳过避免重复修改")
            continue
        target = row.actual_cost_multiplier
        assert target is not None
        current = account.rate_multiplier
        if current is not None and abs(current - target) < threshold:
            continue
        if current is not None and current > target and not args.include_conservative:
            skipped.append(
                f"{row.account_name}: 当前账号倍率 {decimal_label(current)} 高于台账成本 "
                f"{decimal_label(target)}，属于保守记录；默认不创建降倍率草案"
            )
            continue
        seen_accounts.add(account.id)
        new_name = make_draft_name(account)
        if account.id in existing_draft_source_ids:
            skipped.append(f"{row.account_name}: 已存在原账号 id={account.id} 的草案账号，跳过重复创建")
            continue
        now_label = datetime.now(beijing_tz()).strftime("%Y-%m-%d %H:%M CST")
        note_line = (
            f"[{now_label}] 台账草案：原账号 id={account.id}，原账号名={account.name}；"
            f"旧账号倍率={decimal_label(current)}，新台账成本倍率={decimal_label(target)}；"
            f"来源={row.site} / {row.upstream_group}；"
            f"实际成本=页面倍率 {decimal_label(row.page_rate)} × 充值系数 {decimal_label(row.recharge_factor)}；"
            f"匹配方式={match_type}；"
            "状态=草案账号，active + schedulable=false，未分配任何用户组，等待人工核对。"
        )
        changes.append(
            PlannedChange(
                ledger=row,
                account=account,
                match_type=match_type,
                target_multiplier=target,
                new_name=new_name,
                note_line=note_line,
            )
        )
    return changes, skipped


def print_plan(changes: list[PlannedChange], skipped: list[str], mode: str) -> None:
    print(f"# Account multiplier ledger sync {mode}")
    print()
    print(f"planned_drafts={len(changes)}")
    print(f"skipped={len(skipped)}")
    print()
    if changes:
        print("| source_account_id | source_account | draft_account | current | target | status | match | source |")
        print("|---:|---|---|---:|---:|---|---|---|")
        for change in changes:
            print(
                "| {id} | {name} | {draft} | {current} | {target} | {status} | {match} | {source} |".format(
                    id=change.account.id,
                    name=change.account.name.replace("|", "\\|"),
                    draft=change.new_name.replace("|", "\\|"),
                    current=decimal_label(change.account.rate_multiplier),
                    target=decimal_label(change.target_multiplier),
                    status=change.ledger.status.replace("|", "\\|"),
                    match=change.match_type.replace("|", "\\|"),
                    source=f"{change.ledger.site} / {change.ledger.upstream_group}".replace("|", "\\|"),
                )
            )
        print()
    if skipped:
        print("## Skipped")
        for item in skipped[:80]:
            print(f"- {item}")
        if len(skipped) > 80:
            print(f"- ... {len(skipped) - 80} more")


def create_draft_accounts(args: argparse.Namespace, changes: list[PlannedChange]) -> tuple[str | None, list[tuple[int, str]]]:
    if not changes:
        return None, []
    if len(changes) > args.max_changes:
        raise SystemExit(
            f"Refusing to create {len(changes)} draft accounts; raise --max-changes if this is intentional"
        )
    backup_path = create_backup(args)
    created: list[tuple[int, str]] = []
    statements = ["begin;"]
    for change in changes:
        statements.append(
            """
create temporary table if not exists fluter_created_account_drafts (
  source_account_id bigint,
  draft_account_id bigint,
  draft_account_name text
) on commit drop;

with inserted as (
  insert into accounts (
    name,
    platform,
    type,
    credentials,
    extra,
    proxy_id,
    concurrency,
    priority,
    status,
    error_message,
    last_used_at,
    created_at,
    updated_at,
    deleted_at,
    schedulable,
    rate_limited_at,
    rate_limit_reset_at,
    overload_until,
    session_window_start,
    session_window_end,
    session_window_status,
    temp_unschedulable_until,
    temp_unschedulable_reason,
    notes,
    expires_at,
    auto_pause_on_expired,
    rate_multiplier,
    load_factor
  )
  select
    {name},
    platform,
    type,
    credentials,
    extra,
    proxy_id,
    concurrency,
    priority,
    'active',
    null,
    null,
    now(),
    now(),
    null,
    false,
    null,
    null,
    null,
    null,
    null,
    null,
    null,
    null,
    {note},
    expires_at,
    auto_pause_on_expired,
    {rate},
    load_factor
  from accounts
  where id = {id} and deleted_at is null
  returning id, name
)
insert into fluter_created_account_drafts (source_account_id, draft_account_id, draft_account_name)
select {id}, id, name from inserted;
""".format(
                name=sql_quote(change.new_name),
                note=sql_quote(change.note_line),
                rate=decimal_label(change.target_multiplier),
                id=change.account.id,
            )
        )
    statements.append(
        """
select source_account_id || '|' || draft_account_id || '|' || draft_account_name
from fluter_created_account_drafts
order by source_account_id, draft_account_id;
"""
    )
    statements.append("commit;")
    output = run_psql(args, "\n".join(statements))
    for line in output.splitlines():
        parts = line.split("|", 2)
        if len(parts) == 3 and parts[1].isdigit():
            created.append((int(parts[1]), parts[2]))
    return backup_path, created


def main() -> int:
    args = parse_args()
    if args.apply:
        raise SystemExit(
            "--apply has been disabled for safety. Use --create-drafts to create disabled review accounts."
        )
    changes, skipped = plan_changes(args)
    if args.create_drafts:
        mode = "CREATE-DRAFTS"
    else:
        mode = "DRY-RUN"
    print_plan(changes, skipped, mode)
    if args.create_drafts:
        backup_path, created = create_draft_accounts(args, changes)
        print()
        print(f"backup_path={backup_path or '-'}")
        print(f"created_drafts={len(created)}")
        for draft_id, draft_name in created:
            print(f"- draft_account_id={draft_id} name={draft_name}")
    else:
        print()
        print("No production changes were made. Re-run with --create-drafts to create disabled review accounts.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
