#!/usr/bin/env python3
"""Plan safe account priority bucket cleanup for Fluter sub2api.

Default mode is read-only. The script learns Fluter's production account
priority convention, prints a plan, and can write that dry-run plan into the
independent ledger SQLite DB for dashboard rendering. Production note writes
were retired after upstream-hub became the collection source; this script never
changes priority, groups, status, schedulable, notes, model mappings,
credentials, or multipliers.
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
from pathlib import Path


DEFAULT_COMPOSE_DIR = "/www/sub2api"
DEFAULT_BACKUP_DIR = "/www/sub2api/backups"
DEFAULT_LEDGER_DB = "/var/lib/fluterapi-upstream-rates/upstream_rates.sqlite"
MODIFIED_PREFIX = "（修改）"
WRITE_NOTES_DEPRECATED_MESSAGE = (
    "--write-notes has been retired. This script is read-only and may only "
    "write the independent preview DB via --preview-db."
)
BEIJING_TZ_NAME = "Asia/Shanghai"
BEIJING_TZ = timezone(timedelta(hours=8), name="CST")


@dataclass(frozen=True)
class GroupLink:
    name: str
    rate_multiplier: Decimal | None
    link_priority: int | None


@dataclass(frozen=True)
class AccountRow:
    id: int
    name: str
    priority: int | None
    status: str
    schedulable: bool
    rate_multiplier: Decimal | None
    notes: str
    groups: tuple[GroupLink, ...]

    @property
    def group_label(self) -> str:
        if not self.groups:
            return "-"
        labels = []
        for group in self.groups:
            rate = decimal_label(group.rate_multiplier)
            labels.append(f"{group.name}@{rate}")
        return ", ".join(labels)

    @property
    def is_draft(self) -> bool:
        return self.name.startswith(MODIFIED_PREFIX) or "台账草案" in self.notes

    @property
    def is_bucket_anchor(self) -> bool:
        return "占位" in self.name or self.name.startswith("-----")


@dataclass(frozen=True)
class Bucket:
    code: str
    label: str
    start: int
    end: int
    family: str

    def contains(self, value: int | None) -> bool:
        return value is not None and self.start <= value <= self.end


@dataclass(frozen=True)
class ClassifiedAccount:
    account: AccountRow
    bucket: Bucket
    reason: str


@dataclass(frozen=True)
class PlannedPriority:
    account: AccountRow
    bucket: Bucket
    target_priority: int
    reason: str
    mode: str


BUCKETS: dict[str, Bucket] = {
    "protected": Bucket("protected", "0-9 手工保护区", 0, 9, "manual"),
    "codex_ultra_low": Bucket("codex_ultra_low", "10-19 codex 超低价", 10, 19, "codex"),
    "codex_low": Bucket("codex_low", "20-29 codex 低价", 20, 29, "codex"),
    "codex_value": Bucket("codex_value", "30-39 codex 高性价比", 30, 39, "codex"),
    "codex_pro_low": Bucket("codex_pro_low", "40-49 codex pro 王炸低价", 40, 49, "codex"),
    "codex_pro_fast": Bucket("codex_pro_fast", "50-59 codex pro 王炸高速/应急", 50, 59, "codex"),
    "codex_pro_limit": Bucket("codex_pro_limit", "60-69 codex pro 破限", 60, 69, "codex"),
    "codex_pro_fallback": Bucket("codex_pro_fallback", "70-79 codex pro 兜底稳定", 70, 79, "codex"),
    "image": Bucket("image", "80-89 生图/生图+文字", 80, 89, "image"),
    "deepseek": Bucket("deepseek", "90-99 deepseek/其它专项", 90, 99, "deepseek"),
    "claude_low_cache_ultra": Bucket("claude_low_cache_ultra", "100-109 claude 超低价低缓存", 100, 109, "claude"),
    "claude_low_cache": Bucket("claude_low_cache", "110-119 claude 低价低缓存", 110, 119, "claude"),
    "claude_high_cache": Bucket("claude_high_cache", "120-129 claude 低价高缓存", 120, 129, "claude"),
    "claude_value": Bucket("claude_value", "130-139 claude 高性价比", 130, 139, "claude"),
    "claude_stable": Bucket("claude_stable", "140-149 claude 超稳定", 140, 149, "claude"),
    "claude_fallback": Bucket("claude_fallback", "150-159 claude 备用兜底", 150, 159, "claude"),
    "claude_ccmax_client": Bucket("claude_ccmax_client", "160-169 claude ccmax 仅客户端", 160, 169, "claude"),
    "claude_ccmax_open": Bucket("claude_ccmax_open", "170-179 claude ccmax 不限客户端", 170, 179, "claude"),
    "claude_per_call": Bucket("claude_per_call", "200-209 claude 按次", 200, 209, "claude"),
}


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Dry-run Fluter account priority buckets")
    parser.add_argument("--ssh-host", default="us-api-vps")
    parser.add_argument("--compose-dir", default=DEFAULT_COMPOSE_DIR)
    parser.add_argument("--backup-dir", default=DEFAULT_BACKUP_DIR)
    parser.add_argument("--local-postgres", action="store_true")
    parser.add_argument("--family", choices=["all", "codex", "claude", "image", "deepseek"], default="all")
    parser.add_argument("--include-protected", action="store_true", help="Allow planning accounts currently in 0-9")
    parser.add_argument("--include-drafts", action="store_true", help="Include （修改）/台账草案 accounts")
    parser.add_argument("--include-inactive", action="store_true")
    parser.add_argument(
        "--strict-order",
        action="store_true",
        help="Also reorder accounts already inside the right bucket by cost/name. Default only fixes out-of-bucket accounts.",
    )
    parser.add_argument(
        "--write-all-notes",
        action="store_true",
        help="Include all classified accounts in the read-only preview, not only out-of-bucket accounts.",
    )
    parser.add_argument("--write-notes", action="store_true", help="Deprecated safety stop: production note writes are retired")
    parser.add_argument(
        "--preview-db",
        default="",
        help=(
            "Write the dry-run plan into the independent upstream ledger SQLite DB "
            "for read-only dashboard rendering. This never writes production accounts."
        ),
    )
    parser.add_argument("--apply", action="store_true", help="Deprecated safety stop: priority writes are disabled")
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


def compact_text(value: str, limit: int = 90) -> str:
    text = re.sub(r"\s+", " ", value or "").strip().replace("|", "\\|")
    if len(text) <= limit:
        return text
    return text[: max(1, limit - 1)].rstrip() + "…"


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
    command = (
        f"cd {shlex.quote(args.compose_dir)} && "
        "docker compose exec -T postgres psql -U sub2api -d sub2api -At"
    )
    proc = run_remote_or_local(args, command, sql)
    if proc.returncode != 0:
        raise SystemExit(f"PostgreSQL command failed:\n{proc.stderr.strip()}")
    return proc.stdout


def load_accounts(args: argparse.Namespace) -> list[AccountRow]:
    sql = """
copy (
  select coalesce(jsonb_agg(row_to_json(t) order by priority nulls last, lower(name), id), '[]'::jsonb)
  from (
    select
      a.id,
      a.name,
      a.priority,
      coalesce(a.status, '') as status,
      coalesce(a.schedulable, false) as schedulable,
      a.rate_multiplier::text as rate_multiplier,
      coalesce(a.notes, '') as notes,
      coalesce(
        jsonb_agg(
          jsonb_build_object(
            'name', g.name,
            'rate_multiplier', g.rate_multiplier::text,
            'link_priority', ag.priority
          )
          order by ag.priority nulls last, g.name
        ) filter (where g.id is not null),
        '[]'::jsonb
      ) as groups
    from accounts a
    left join account_groups ag on ag.account_id = a.id
    left join groups g on g.id = ag.group_id and g.deleted_at is null
    where a.deleted_at is null
    group by a.id, a.name, a.priority, a.status, a.schedulable, a.rate_multiplier, a.notes
  ) t
) to stdout;
"""
    payload = run_psql(args, sql).strip() or "[]"
    raw_rows = json.loads(payload)
    accounts: list[AccountRow] = []
    for row in raw_rows:
        groups = tuple(
            GroupLink(
                name=group.get("name") or "",
                rate_multiplier=decimal_or_none(group.get("rate_multiplier")),
                link_priority=int(group["link_priority"]) if group.get("link_priority") is not None else None,
            )
            for group in row.get("groups", [])
        )
        accounts.append(
            AccountRow(
                id=int(row["id"]),
                name=row.get("name") or "",
                priority=int(row["priority"]) if row.get("priority") is not None else None,
                status=row.get("status") or "",
                schedulable=bool(row.get("schedulable")),
                rate_multiplier=decimal_or_none(row.get("rate_multiplier")),
                notes=row.get("notes") or "",
                groups=groups,
            )
        )
    return accounts


def account_text(account: AccountRow) -> str:
    group_names = " ".join(group.name for group in account.groups)
    return f"{account.name} {group_names}".lower()


def group_text(account: AccountRow) -> str:
    return " ".join(group.name for group in account.groups).lower()


def name_text(account: AccountRow) -> str:
    return account.name.lower()


def has_any(text: str, keywords: tuple[str, ...]) -> bool:
    return any(keyword.lower() in text for keyword in keywords)


def codex_cost_bucket(account: AccountRow) -> tuple[Bucket | None, str]:
    text = account_text(account)
    groups = group_text(account)
    # 用户手动分组是调度意图；账号名里可能包含上游营销词，所以有分组时优先看分组。
    if "王炸低价pro" in groups:
        return BUCKETS["codex_pro_low"], "命中 codex 王炸低价pro 分组"
    if "王炸高速pro" in groups:
        return BUCKETS["codex_pro_fast"], "命中 codex 王炸高速pro 分组"
    if "破除限制pro" in groups:
        return BUCKETS["codex_pro_limit"], "命中 codex pro 破限"
    if "兜底稳定pro" in groups:
        return BUCKETS["codex_pro_fallback"], "命中 codex 兜底稳定pro 分组"
    if "福利超低价" in groups:
        return BUCKETS["codex_ultra_low"], "福利超低价不自动写入 0-9，按超低价候选处理"
    if "超低价渠道" in groups:
        return BUCKETS["codex_ultra_low"], "命中 codex 超低价分组"
    if "低价渠道" in groups:
        return BUCKETS["codex_low"], "命中 codex 低价分组"
    if "高性价比渠道" in groups:
        return BUCKETS["codex_value"], "命中 codex 高性价比分组"
    if "pro破限" in text:
        return BUCKETS["codex_pro_limit"], "账号名命中 codex pro 破限"

    rate = account.rate_multiplier
    if rate is None:
        return None, "缺少账号成本倍率，无法按成本入档"
    if rate < Decimal("0.06"):
        return BUCKETS["codex_ultra_low"], "按账号成本倍率 <0.06 入超低价档"
    if rate < Decimal("0.08"):
        return BUCKETS["codex_low"], "按账号成本倍率 0.06-0.08 入低价档"
    if rate < Decimal("0.10"):
        return BUCKETS["codex_value"], "按账号成本倍率 0.08-0.10 入高性价比档"
    if rate < Decimal("0.12"):
        return BUCKETS["codex_pro_low"], "按账号成本倍率 0.10-0.12 入 pro 王炸低价档"
    if rate < Decimal("0.18"):
        return BUCKETS["codex_pro_fast"], "按账号成本倍率 0.12-0.18 入 pro 王炸高速/应急档"
    if rate < Decimal("0.30"):
        return BUCKETS["codex_pro_fallback"], "按账号成本倍率 0.18-0.30 入 pro 兜底稳定档"
    return BUCKETS["codex_pro_fallback"], "按账号成本倍率 >=0.30 入 pro 兜底稳定档"


def classify_account(account: AccountRow) -> tuple[Bucket | None, str]:
    text = account_text(account)
    groups = group_text(account)
    name = name_text(account)
    if "claude" in text and "按次" in text:
        return BUCKETS["claude_per_call"], "命中 claude 按次账号"
    if has_any(text, ("仅生图", "生图+文字", "生图(可原可桥)", " codex 生图", " gpt-image")):
        return BUCKETS["image"], "命中生图/生图+文字账号"
    if "deepseek" in text:
        return BUCKETS["deepseek"], "命中 deepseek 专项"
    if "claude" in text:
        if "超低价低缓存" in groups:
            return BUCKETS["claude_low_cache_ultra"], "命中 claude 超低价低缓存分组"
        if "低价低缓存" in groups:
            return BUCKETS["claude_low_cache"], "命中 claude 低价低缓存分组"
        if "低价高缓存" in groups:
            return BUCKETS["claude_high_cache"], "命中 claude 低价高缓存分组"
        if "高性价比" in groups:
            return BUCKETS["claude_value"], "命中 claude 高性价比分组"
        if "超稳定" in groups:
            return BUCKETS["claude_stable"], "命中 claude 超稳定分组"
        if "备用兜底" in groups:
            return BUCKETS["claude_fallback"], "命中 claude 备用兜底/Azure"
        if "仅客户端" in groups:
            return BUCKETS["claude_ccmax_client"], "命中 claude ccmax 仅客户端"
        if "不限客户端" in groups:
            return BUCKETS["claude_ccmax_open"], "命中 claude ccmax 不限客户端"
        if "低缓" in name:
            return BUCKETS["claude_low_cache"], "账号名命中 claude 低缓存"
        if "高缓" in name:
            return BUCKETS["claude_high_cache"], "账号名命中 claude 高缓存"
        if "anti稳定" in name:
            return BUCKETS["claude_stable"], "账号名命中 claude 稳定"
        if "azure" in name:
            return BUCKETS["claude_fallback"], "账号名命中 claude Azure 备用"
        if "仅客户端" in name:
            return BUCKETS["claude_ccmax_client"], "账号名命中 claude ccmax 仅客户端"
        if "ccmax" in name or "cc max" in name:
            return BUCKETS["claude_ccmax_open"], "账号名命中 claude ccmax"
        return None, "claude 账号但缺少可识别分组语义，先人工确认"
    if has_any(text, ("codex", "gpt-", "gpt5", "gpt-5", "plus", "pro")):
        return codex_cost_bucket(account)
    return None, "非 codex/claude/deepseek/生图账号，跳过"


def family_matches(bucket: Bucket, family: str) -> bool:
    return family == "all" or bucket.family == family


def rank_target_priorities(classified: list[ClassifiedAccount]) -> dict[int, int]:
    targets: dict[int, int] = {}
    by_bucket: dict[str, list[ClassifiedAccount]] = {}
    for item in classified:
        by_bucket.setdefault(item.bucket.code, []).append(item)

    for items in by_bucket.values():
        bucket = items[0].bucket
        rate_order: dict[str, int] = {}
        next_offset = 0
        sorted_items = sorted(
            items,
            key=lambda item: (
                item.account.rate_multiplier is None,
                item.account.rate_multiplier or Decimal("999999"),
                item.account.priority is None,
                item.account.priority or 999999,
                item.account.name,
                item.account.id,
            ),
        )
        for item in sorted_items:
            rate_key = decimal_label(item.account.rate_multiplier)
            if rate_key not in rate_order:
                rate_order[rate_key] = next_offset
                next_offset += 1
            target = min(bucket.start + 1 + rate_order[rate_key], bucket.end)
            targets[item.account.id] = target
    return targets


def plan(args: argparse.Namespace) -> tuple[list[PlannedPriority], list[ClassifiedAccount], list[str]]:
    accounts = load_accounts(args)
    classified: list[ClassifiedAccount] = []
    skipped: list[str] = []
    for account in accounts:
        if not args.include_inactive and account.status != "active":
            skipped.append(f"{account.name}: 状态 {account.status}，默认跳过")
            continue
        if not args.include_drafts and account.is_draft:
            skipped.append(f"{account.name}: 草案账号默认跳过")
            continue
        if account.is_bucket_anchor:
            skipped.append(f"{account.name}: 档位占位锚点账号默认跳过")
            continue
        if not args.include_protected and BUCKETS["protected"].contains(account.priority):
            skipped.append(f"{account.name}: 当前 priority={account.priority} 在 0-9 手工保护区，默认不动")
            continue
        bucket, reason = classify_account(account)
        if bucket is None:
            skipped.append(f"{account.name}: {reason}")
            continue
        if not family_matches(bucket, args.family):
            skipped.append(f"{account.name}: family={bucket.family}，本轮筛选 family={args.family}")
            continue
        classified.append(ClassifiedAccount(account=account, bucket=bucket, reason=reason))

    strict_targets = rank_target_priorities(classified)
    changes: list[PlannedPriority] = []
    for item in classified:
        account = item.account
        if args.write_all_notes:
            target = strict_targets[account.id]
            mode = "all_notes"
        elif args.strict_order:
            target = strict_targets[account.id]
            mode = "strict_order"
        else:
            if item.bucket.contains(account.priority):
                continue
            target = strict_targets[account.id]
            mode = "bucket_fix"
        if args.write_all_notes or account.priority != target:
            changes.append(
                PlannedPriority(
                    account=account,
                    bucket=item.bucket,
                    target_priority=target,
                    reason=item.reason,
                    mode=mode,
                )
            )
    return changes, classified, skipped


def print_report(changes: list[PlannedPriority], classified: list[ClassifiedAccount], skipped: list[str], args: argparse.Namespace) -> None:
    print("# Account priority bucket plan")
    print()
    print("mode=WRITE-NOTES" if args.write_notes else "mode=DRY-RUN")
    print(f"family={args.family}")
    print(f"strict_order={str(args.strict_order).lower()}")
    print(f"write_all_notes={str(args.write_all_notes).lower()}")
    print(f"classified={len(classified)}")
    print(f"planned_note_updates={len(changes)}")
    print(f"skipped={len(skipped)}")
    print()
    print("## Bucket Rules")
    for key in (
        "protected",
        "codex_ultra_low",
        "codex_low",
        "codex_value",
        "codex_pro_low",
        "codex_pro_fast",
        "codex_pro_limit",
        "codex_pro_fallback",
        "image",
        "deepseek",
        "claude_low_cache_ultra",
        "claude_low_cache",
        "claude_high_cache",
        "claude_value",
        "claude_stable",
        "claude_fallback",
        "claude_ccmax_client",
        "claude_ccmax_open",
        "claude_per_call",
    ):
        bucket = BUCKETS[key]
        print(f"- {bucket.label}")
    print()
    if changes:
        print("## Planned Updates")
        print("| id | account | current | target | rate | bucket | groups | reason |")
        print("|---:|---|---:|---:|---:|---|---|---|")
        for change in changes:
            account = change.account
            print(
                "| {id} | {name} | {current} | {target} | {rate} | {bucket} | {groups} | {reason} |".format(
                    id=account.id,
                    name=compact_text(account.name),
                    current=account.priority if account.priority is not None else "-",
                    target=change.target_priority,
                    rate=decimal_label(account.rate_multiplier),
                    bucket=compact_text(change.bucket.label, 44),
                    groups=compact_text(account.group_label, 72),
                    reason=compact_text(change.reason, 80),
                )
            )
        print()
    if classified:
        ok_count = len(
            [
                item
                for item in classified
                if item.bucket.contains(item.account.priority)
                and not any(change.account.id == item.account.id for change in changes)
            ]
        )
        print(f"already_in_expected_bucket={ok_count}")
        by_bucket: dict[str, int] = {}
        for item in classified:
            by_bucket[item.bucket.label] = by_bucket.get(item.bucket.label, 0) + 1
        for label, count in sorted(by_bucket.items()):
            print(f"- {label}: {count}")
        print()
    if skipped:
        print("## Skipped")
        for item in skipped[:80]:
            print(f"- {compact_text(item, 160)}")
        if len(skipped) > 80:
            print(f"- ... {len(skipped) - 80} more")
        print()
    print("No production changes were made. --write-notes is retired and exits before any production write.")


def create_preview_schema(conn: sqlite3.Connection) -> None:
    conn.executescript(
        """
create table if not exists account_priority_plan_rows (
  id integer primary key autoincrement,
  run_id text not null,
  account_id integer not null,
  account_name text not null,
  current_priority integer,
  target_priority integer not null,
  rate_multiplier text not null,
  bucket text not null,
  groups text not null,
  reason text not null,
  mode text not null,
  observed_at text not null
);

create table if not exists metadata (
  key text primary key,
  value text not null
);
"""
    )


def write_priority_preview_db(args: argparse.Namespace, changes: list[PlannedPriority]) -> str | None:
    if not args.preview_db:
        return None
    db_path = Path(args.preview_db)
    db_path.parent.mkdir(parents=True, exist_ok=True)
    observed_at = datetime.now(beijing_tz()).isoformat(timespec="seconds")
    run_id = observed_at
    conn = sqlite3.connect(db_path)
    with conn:
        create_preview_schema(conn)
        conn.execute("delete from account_priority_plan_rows")
        for change in changes:
            account = change.account
            conn.execute(
                """
                insert into account_priority_plan_rows (
                  run_id, account_id, account_name, current_priority, target_priority,
                  rate_multiplier, bucket, groups, reason, mode, observed_at
                ) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                """,
                (
                    run_id,
                    account.id,
                    account.name,
                    account.priority,
                    change.target_priority,
                    decimal_label(account.rate_multiplier),
                    change.bucket.label,
                    account.group_label,
                    change.reason,
                    change.mode,
                    observed_at,
                ),
            )
        conn.execute(
            "insert or replace into metadata(key, value) values (?, ?)",
            ("last_priority_plan_preview_observed_at", observed_at),
        )
        conn.execute(
            "insert or replace into metadata(key, value) values (?, ?)",
            ("last_priority_plan_preview_count", str(len(changes))),
        )
        conn.execute(
            "insert or replace into metadata(key, value) values (?, ?)",
            (
                "priority_plan_preview_note",
                "read-only dry-run preview only; production notes write path retired after upstream-hub adoption",
            ),
        )
    conn.close()
    return str(db_path)


def main() -> int:
    args = parse_args()
    if args.apply:
        raise SystemExit("--apply has been disabled. This script is read-only.")
    if args.write_notes:
        raise SystemExit(WRITE_NOTES_DEPRECATED_MESSAGE)
    changes, classified, skipped = plan(args)
    print_report(changes, classified, skipped, args)
    preview_path = write_priority_preview_db(args, changes)
    if preview_path:
        print()
        print(f"preview_db={preview_path}")
        print(f"preview_rows={len(changes)}")
        print("Preview DB is read-only dashboard data; no production accounts were changed.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
