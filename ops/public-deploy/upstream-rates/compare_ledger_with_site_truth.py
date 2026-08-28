#!/usr/bin/env python3
"""Compare ledger-derived costs with production account multipliers.

This is a diagnostic/training report. It treats current production
accounts.rate_multiplier as the temporary reference for a manual calibration
session, then shows where the ledger's upstream-derived cost disagrees.

It never edits production accounts or the ledger database.
"""

from __future__ import annotations

import argparse
import json
import re
import sqlite3
from dataclasses import dataclass
from datetime import datetime, timezone
from decimal import Decimal, InvalidOperation
from typing import Any


DEFAULT_DB = "/var/lib/fluterapi-upstream-rates/upstream_rates.sqlite"


EXCLUDED_KIND_KEYWORDS = ("生图", "特殊")
EXCLUDED_STATUS_KEYWORDS = ("未接入", "未分配", "未调度", "停用", "无生图权限")
EXCLUDED_NAME_KEYWORDS = ("deepseek", "按次")
MIN_BROWSER_GROUPS_FOR_REMOVAL = 2
MIN_TAMPERMONKEY_SNAPSHOT_VERSION = (0, 1, 15)
RECENT_FUTURE_SKEW_SECONDS = 300
STATUS_OBSERVATION_MAX_SKEW_SECONDS = 300
EXACT_GROUP_MATCH_TOKENS = {"gpt", "pro", "cc", "ag", "max"}


@dataclass(frozen=True)
class SourceInfo:
    label: str
    kind: str
    confidence: str
    observed_at: str
    detail: str = ""


@dataclass(frozen=True)
class CompareRow:
    id: int
    category: str
    kind: str
    site: str
    account_name: str
    upstream_group: str
    script_cost: Decimal | None
    site_account_multiplier: Decimal | None
    status: str
    source: str
    source_kind: str
    confidence: str
    reason: str


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Compare ledger costs with current production account multipliers")
    parser.add_argument("--db", default=DEFAULT_DB)
    parser.add_argument(
        "--threshold",
        default="0.001",
        help="Absolute tolerance when comparing against manual site truth; default accepts small rounding/safety-pad differences.",
    )
    parser.add_argument("--include-excluded", action="store_true")
    parser.add_argument("--json", action="store_true", help="Emit JSON instead of markdown")
    return parser.parse_args()


def decimal_or_none(value) -> Decimal | None:
    if value is None or value == "":
        return None
    try:
        return Decimal(str(value))
    except (InvalidOperation, ValueError):
        return None


def compact(value: Decimal | None) -> str:
    if value is None:
        return "-"
    return format(value.quantize(Decimal("0.000000001")).normalize(), "f")


def parse_dt(value: Any) -> datetime | None:
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


def normalize_match_text(value: str) -> str:
    value = (value or "").lower()
    value = re.sub(r"sk-[a-z0-9._-]+", "", value)
    value = re.sub(r"\.\.\.redacted(?:-long-token)?\.\.\.", "", value)
    value = value.replace("（", "(").replace("）", ")")
    value = re.sub(r"[\s/_:：,，;；|｜·\-+()（）\[\]【】<>《》\"'“”‘’]", "", value)
    # A few providers use tiny wording drift for the same key pool.
    value = value.replace("无限制客户端", "无限客户端").replace("无限刷客户端", "无限客户端")
    return value


def normalize_account_name(value: str) -> str:
    value = normalize_match_text(value)
    while value.startswith("修改"):
        value = value[2:]
    return value


def match_group(left: str, right: str) -> bool:
    left_norm = normalize_match_text(left)
    right_norm = normalize_match_text(right)
    if not left_norm or not right_norm:
        return False
    if left_norm == right_norm:
        return True
    if (
        len(left_norm) <= 3
        or len(right_norm) <= 3
        or left_norm in EXACT_GROUP_MATCH_TOKENS
        or right_norm in EXACT_GROUP_MATCH_TOKENS
    ):
        return False
    return left_norm in right_norm or right_norm in left_norm


def is_excluded(row: sqlite3.Row) -> str | None:
    kind = row["kind"] or ""
    status = row["status"] or ""
    name = (row["fluter_account_name"] or "").lower()
    category = row["category"] or ""
    if any(keyword in kind for keyword in EXCLUDED_KIND_KEYWORDS):
        return f"排除类型：{kind}"
    if any(keyword in status for keyword in EXCLUDED_STATUS_KEYWORDS):
        return f"排除状态：{status}"
    if any(keyword in name for keyword in EXCLUDED_NAME_KEYWORDS):
        return "排除：DeepSeek/按次账号需单独公式"
    if category == "KBQ" and "按次" in status:
        return "排除：KBQ 按次 Claude 需单独按次售价"
    return None


def table_exists(conn: sqlite3.Connection, name: str) -> bool:
    return bool(conn.execute("select 1 from sqlite_master where type='table' and name=?", (name,)).fetchone())


def latest_metadata_dt(conn: sqlite3.Connection, key: str) -> datetime | None:
    if not table_exists(conn, "metadata"):
        return None
    record = conn.execute("select value from metadata where key = ?", (key,)).fetchone()
    return parse_dt(record["value"]) if record else None


def latest_public_status(conn: sqlite3.Connection, site: str) -> sqlite3.Row | None:
    if not table_exists(conn, "upstream_adapter_status"):
        return None
    return conn.execute(
        """
        select provider, adapter_kind, status, detail, observed_at
        from upstream_adapter_status
        where site = ?
        order by observed_at desc
        limit 1
        """,
        (site,),
    ).fetchone()


def is_recent_enough(observed_at: str, reference: datetime | None, max_age_seconds: int = 36 * 3600) -> bool:
    observed = parse_dt(observed_at)
    if observed is None:
        return False
    if reference is None:
        reference = datetime.now(timezone.utc)
    age = (reference - observed).total_seconds()
    return -RECENT_FUTURE_SKEW_SECONDS <= age <= max_age_seconds


def observations_are_aligned(left: Any, right: Any, max_skew_seconds: int = STATUS_OBSERVATION_MAX_SKEW_SECONDS) -> bool:
    left_dt = parse_dt(left)
    right_dt = parse_dt(right)
    if left_dt is None or right_dt is None:
        return False
    return abs((left_dt - right_dt).total_seconds()) <= max_skew_seconds


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


def browser_status_source_kind(status: sqlite3.Row | None, reference: datetime | None = None) -> str:
    if not status or status["status"] != "browser_observed":
        return "browser_capture_incomplete"
    detail = str(status["detail"] or "")
    if "rate_lines=0" in detail or re.search(r"\bfresh_rate_lines\s*=\s*0\b", detail):
        return "browser_capture_incomplete"
    if (
        "preserved previous rate lines" in detail
        or "preserved previous account lines" in detail
        or "preserved previous non-empty snapshot" in detail
    ):
        return "browser_preserved_snapshot"
    if "partial account snapshot" in detail:
        return "browser_partial_snapshot"
    if re.search(r"\bwait_state\s*=\s*timeout\b", detail):
        return "browser_unstable_snapshot"
    if "Chrome Tampermonkey read-only snapshot" in detail:
        version = detail_script_version(detail)
        if version is None or semver_lt(version, MIN_TAMPERMONKEY_SNAPSHOT_VERSION):
            return "browser_legacy_script"
    if reference is not None and not is_recent_enough(status["observed_at"], reference):
        return "browser_stale_snapshot"
    return "browser_observed_current"


def browser_status_has_current_account_rows(status: sqlite3.Row | None, reference: datetime | None = None) -> bool:
    """Return whether account rows were freshly read from the current page."""

    if not status or status["status"] != "browser_observed":
        return False
    if reference is not None and not is_recent_enough(status["observed_at"], reference):
        return False
    detail = str(status["detail"] or "")
    if re.search(r"\bfresh_account_lines\s*=\s*0\b", detail) or "account_lines=0" in detail:
        return False
    if (
        "preserved previous account lines" in detail
        or "preserved previous rate lines" in detail
        or "preserved previous non-empty snapshot" in detail
    ):
        return False
    if "partial account snapshot" in detail:
        return False
    if re.search(r"\bwait_state\s*=\s*timeout\b", detail):
        return False
    if "Chrome Tampermonkey read-only snapshot" in detail:
        version = detail_script_version(detail)
        if version is None or semver_lt(version, MIN_TAMPERMONKEY_SNAPSHOT_VERSION):
            return False
    return True


def browser_observation_is_current(
    observed_at: Any,
    status: sqlite3.Row | None,
    reference: datetime | None,
) -> bool:
    if not status:
        return False
    return is_recent_enough(observed_at, reference) and observations_are_aligned(status["observed_at"], observed_at)


def observed_groups_for_site(
    conn: sqlite3.Connection,
    site: str,
    reference: datetime | None = None,
) -> list[str]:
    groups: list[str] = []
    if table_exists(conn, "upstream_hub_rate_observations"):
        groups.extend(
            row["model_name"]
            for row in conn.execute(
                "select model_name, imported_at from upstream_hub_rate_observations where site = ? order by model_name",
                (site,),
            )
            if is_recent_enough(row["imported_at"], reference)
        )
    if table_exists(conn, "provider_group_ratio_records"):
        groups.extend(
            row["group_name"]
            for row in conn.execute(
                "select group_name, updated_at from provider_group_ratio_records where site = ? order by group_name",
                (site,),
            )
            if is_recent_enough(row["updated_at"], reference)
        )
    if table_exists(conn, "browser_adapter_rate_observations"):
        browser_status = None
        if table_exists(conn, "browser_adapter_status"):
            browser_status = conn.execute(
                """
                select provider, status, detail, observed_at
                from browser_adapter_status
                where site = ?
                order by observed_at desc
                limit 1
                """,
                (site,),
            ).fetchone()
        if browser_status_source_kind(browser_status, reference) == "browser_observed_current":
            groups.extend(
                row["upstream_group"]
                for row in conn.execute(
                    "select upstream_group, observed_at from browser_adapter_rate_observations where site = ? order by upstream_group",
                    (site,),
                )
                if browser_observation_is_current(row["observed_at"], browser_status, reference)
            )
    return groups


def observed_hub_groups_for_site(
    conn: sqlite3.Connection,
    site: str,
    reference: datetime | None = None,
) -> list[str]:
    if not table_exists(conn, "upstream_hub_rate_observations"):
        return []
    return [
        row["model_name"]
        for row in conn.execute(
            "select model_name, imported_at from upstream_hub_rate_observations where site = ? order by model_name",
            (site,),
        )
        if is_recent_enough(row["imported_at"], reference)
    ]


def observed_browser_groups_for_site(
    conn: sqlite3.Connection,
    site: str,
    reference: datetime | None = None,
) -> list[str]:
    if not table_exists(conn, "browser_adapter_rate_observations"):
        return []
    status = None
    if table_exists(conn, "browser_adapter_status"):
        status = conn.execute(
            """
            select provider, status, detail, observed_at
            from browser_adapter_status
            where site = ?
            order by observed_at desc
            limit 1
            """,
            (site,),
        ).fetchone()
    if browser_status_source_kind(status, reference) != "browser_observed_current":
        return []
    return [
        row["upstream_group"]
        for row in conn.execute(
            "select upstream_group, observed_at from browser_adapter_rate_observations where site = ? order by upstream_group",
            (site,),
        )
        if browser_observation_is_current(row["observed_at"], status, reference)
    ]


def source_for_row(conn: sqlite3.Connection, row: sqlite3.Row) -> SourceInfo:
    reference = latest_metadata_dt(conn, "last_orchestrated_refresh_at")
    if row["category"] == "KBQ":
        observed_at = ""
        if table_exists(conn, "metadata"):
            record = conn.execute("select value from metadata where key = 'kbq_pricing_updated_at'").fetchone()
            observed_at = record["value"] if record else ""
        return SourceInfo("KBQ /api/pricing 模型价", "kbq_pricing", "high", observed_at)
    if table_exists(conn, "upstream_hub_rate_observations"):
        hub_rows = conn.execute(
            """
            select channel_name, model_name, page_rate, imported_at
            from upstream_hub_rate_observations
            where site = ?
            order by imported_at desc
            """,
            (row["site"],),
        ).fetchall()
        hub_groups = [
            hub
            for hub in hub_rows
            if is_recent_enough(hub["imported_at"], reference)
        ]
        for hub in hub_groups:
            if match_group(hub["model_name"], row["upstream_group"]):
                return SourceInfo(
                    f"{hub['channel_name']} upstream-hub {hub['imported_at']}",
                    "upstream_hub",
                    "high",
                    hub["imported_at"],
                    detail=f"matched upstream group: {hub['model_name']}",
                )
        unique_hub_groups = sorted({hub["model_name"] for hub in hub_groups if hub["model_name"]})
        if len(unique_hub_groups) >= MIN_BROWSER_GROUPS_FOR_REMOVAL:
            newest = max(hub_groups, key=lambda hub: hub["imported_at"])
            return SourceInfo(
                f"{newest['channel_name']} upstream-hub 未找到该上游分组 {newest['imported_at']}",
                "upstream_hub_group_missing",
                "high",
                newest["imported_at"],
                detail=f"groups={len(unique_hub_groups)}",
            )
    if table_exists(conn, "provider_group_ratio_records"):
        provider = conn.execute(
            """
            select provider, updated_at
            from provider_group_ratio_records
            where site = ?
              and group_name = ?
            order by updated_at desc
            limit 1
            """,
            (row["site"], row["upstream_group"]),
        ).fetchone()
        if provider:
            status = latest_public_status(conn, row["site"])
            status_observed = parse_dt(status["observed_at"]) if status else None
            provider_observed = parse_dt(provider["updated_at"])
            if (
                status
                and status["adapter_kind"] == "public_pricing"
                and status["status"] != "ok"
                and status_observed
                and provider_observed
                and status_observed > provider_observed
            ):
                return SourceInfo(
                    (
                        f"{provider['provider']} /api/pricing 上次成功 {provider['updated_at']}；"
                        f"本轮失败 {status['observed_at']}"
                    ),
                    "public_pricing_stale_after_failure",
                    "low",
                    provider["updated_at"],
                    detail=status["detail"],
                )
            return SourceInfo(
                f"{provider['provider']} /api/pricing {provider['updated_at']}",
                "public_pricing",
                "high",
                provider["updated_at"],
            )
    if table_exists(conn, "browser_adapter_account_observations"):
        browser_status = None
        if table_exists(conn, "browser_adapter_status"):
            browser_status = conn.execute(
                """
                select provider, status, detail, observed_at
                from browser_adapter_status
                where site = ?
                order by observed_at desc
                limit 1
                """,
                (row["site"],),
            ).fetchone()
        browser_status_kind = browser_status_source_kind(browser_status, reference)
        account = conn.execute(
            """
            select provider, account_name, upstream_group, page_rate, matched_ledger_rows, observed_at
            from browser_adapter_account_observations
            where site = ?
              and normalized_account_name = ?
            order by observed_at desc
            limit 1
            """,
            (row["site"], normalize_account_name(row["fluter_account_name"])),
        ).fetchone()
        if (
            account
            and browser_status_has_current_account_rows(browser_status, reference)
            and browser_observation_is_current(account["observed_at"], browser_status, reference)
        ):
            confidence = "medium"
            try:
                if int(account["matched_ledger_rows"] or 0) > 0:
                    confidence = "high"
            except (TypeError, ValueError):
                pass
            return SourceInfo(
                f"{account['provider']} 浏览器账号名快照 {account['observed_at']}",
                "browser_account_snapshot",
                confidence,
                account["observed_at"],
                detail=f"matched upstream account: {account['account_name']}; group: {account['upstream_group']}",
            )
    if table_exists(conn, "browser_adapter_rate_observations"):
        browser_rows = conn.execute(
            """
            select provider, upstream_group, page_rate, matched_ledger_rows, observed_at
            from browser_adapter_rate_observations
            where site = ?
            order by observed_at desc
            """,
            (row["site"],),
        ).fetchall()
        browser_status = None
        if table_exists(conn, "browser_adapter_status"):
            browser_status = conn.execute(
                """
                select provider, status, detail, observed_at
                from browser_adapter_status
                where site = ?
                order by observed_at desc
                limit 1
                """,
                (row["site"],),
            ).fetchone()
        browser_status_kind = browser_status_source_kind(browser_status, reference)
        if browser_status_kind != "browser_observed_current":
            if browser_status:
                return SourceInfo(
                    f"{browser_status['provider']} 浏览器快照不可作为倍率依据 {browser_status['observed_at']}",
                    browser_status_kind,
                    "low",
                    browser_status["observed_at"],
                    detail=browser_status["detail"],
                )
            return SourceInfo("浏览器快照不完整", "browser_capture_incomplete", "low", "")
        for browser in browser_rows:
            if not browser_observation_is_current(browser["observed_at"], browser_status, reference):
                continue
            if match_group(browser["upstream_group"], row["upstream_group"]):
                confidence = "medium"
                try:
                    if int(browser["matched_ledger_rows"] or 0) > 0:
                        confidence = "high"
                except (TypeError, ValueError):
                    pass
                return SourceInfo(
                    f"{browser['provider']} 浏览器快照 {browser['observed_at']}",
                    "browser_snapshot",
                    confidence,
                    browser["observed_at"],
                    detail=f"matched upstream group: {browser['upstream_group']}",
                )
    if table_exists(conn, "upstream_adapter_status"):
        public_status = latest_public_status(conn, row["site"])
        if public_status and public_status["status"] == "ok":
            return SourceInfo(
                f"{public_status['provider']} /api/pricing 未找到该上游分组 {public_status['observed_at']}",
                "public_group_missing",
                "low",
                public_status["observed_at"],
                detail=public_status["detail"],
            )
    if table_exists(conn, "browser_adapter_status"):
        browser_status = conn.execute(
            """
            select provider, status, detail, observed_at
            from browser_adapter_status
            where site = ?
            order by observed_at desc
            limit 1
            """,
            (row["site"],),
        ).fetchone()
        browser_status_kind = browser_status_source_kind(browser_status, reference)
        if browser_status and browser_status_kind != "browser_observed_current":
            return SourceInfo(
                f"{browser_status['provider']} 浏览器快照不可作为倍率依据 {browser_status['observed_at']}",
                browser_status_kind,
                "low",
                browser_status["observed_at"],
                detail=browser_status["detail"],
            )
        if browser_status and browser_status["status"] == "browser_observed":
            browser_groups = set(observed_browser_groups_for_site(conn, row["site"], reference))
            if len(browser_groups) < MIN_BROWSER_GROUPS_FOR_REMOVAL:
                return SourceInfo(
                    f"{browser_status['provider']} 浏览器快照只抓到 {len(browser_groups)} 个倍率分组 {browser_status['observed_at']}",
                    "browser_capture_incomplete",
                    "low",
                    browser_status["observed_at"],
                    detail=browser_status["detail"],
                )
            return SourceInfo(
                f"{browser_status['provider']} 浏览器快照未找到该上游分组 {browser_status['observed_at']}",
                "browser_group_missing",
                "low",
                browser_status["observed_at"],
                detail=browser_status["detail"],
            )
    return SourceInfo("手工种子/未知来源", "manual_seed", "low", "")


def reliable_source(source: SourceInfo, reference: datetime | None) -> bool:
    if source.kind in ("kbq_pricing", "public_pricing", "upstream_hub"):
        return True
    if source.kind in ("browser_snapshot", "browser_account_snapshot"):
        return is_recent_enough(source.observed_at, reference) and source.confidence in ("medium", "high")
    return False


def reliable_missing_source(source: SourceInfo, reference: datetime | None) -> bool:
    """Missing groups are actionable only when the missing proof is fresh."""

    if source.kind in ("browser_group_missing", "upstream_hub_group_missing"):
        return is_recent_enough(source.observed_at, reference)
    return False


def verdict(
    script_cost: Decimal | None,
    site_account: Decimal | None,
    threshold: Decimal,
    source: SourceInfo,
    reference: datetime | None,
) -> str:
    if script_cost is None or site_account is None:
        return "NO_COMPARE"
    if source.kind == "browser_capture_incomplete":
        return "BROWSER_CAPTURE_INCOMPLETE"
    if source.kind in ("browser_group_missing", "upstream_hub_group_missing"):
        return "UPSTREAM_GROUP_REMOVED" if reliable_missing_source(source, reference) else "NEEDS_FRESH_SOURCE"
    if source.kind == "public_group_missing":
        return "NEEDS_FRESH_SOURCE"
    if source.kind == "public_pricing_stale_after_failure":
        return "PUBLIC_PRICING_REFRESH_FAILED"
    delta = script_cost - site_account
    if abs(delta) <= threshold:
        return "MATCH" if reliable_source(source, reference) else "MATCH_LOW_CONFIDENCE"
    if not reliable_source(source, reference):
        return "NEEDS_FRESH_SOURCE"
    if delta > 0:
        return "LEDGER_COST_ABOVE_SITE"
    return "SITE_CONSERVATIVE_REVIEW"


def likely_reason(
    mark: str,
    row: sqlite3.Row,
    source: SourceInfo,
    script_cost: Decimal | None,
    site_account: Decimal | None,
    conn: sqlite3.Connection,
) -> str:
    if mark == "MATCH":
        return "脚本与本轮人工真值一致，且来源是公开接口或新鲜浏览器快照"
    if mark == "MATCH_LOW_CONFIDENCE":
        return "数值与本轮人工真值一致，但来源是旧种子、旧公开接口或低置信快照；不能证明脚本已会自动抓准"
    if mark == "NO_COMPARE":
        return "缺少脚本成本或站内账号倍率"
    if mark == "BROWSER_CAPTURE_INCOMPLETE":
        return "页面已刷新/观察到，但没有抓到任何倍率行；需要改油猴/打开更具体的 key 或分组页"
    if mark == "UPSTREAM_GROUP_REMOVED":
        candidates = observed_groups_for_site(conn, row["site"], latest_metadata_dt(conn, "last_orchestrated_refresh_at"))
        sample = "、".join(candidates[:8])
        suffix = f"；本轮看到的分组示例：{sample}" if sample else ""
        return f"本轮刷新后的上游页面/接口没有出现这个旧分组；按当前人工校准规则，视为上游分组已消失，台账保留历史但不再拿它反驳站内真值{suffix}"
    if mark == "PUBLIC_PRICING_REFRESH_FAILED":
        return "本轮公开价格接口刷新失败，当前数值来自上一次成功记录；先不要用它反驳人工真值，等接口恢复后再校准"
    if mark == "NEEDS_FRESH_SOURCE":
        if source.kind == "public_group_missing":
            return "公开价格接口没有列出该组，但公开 pricing 不是登录账号页完整清单；需要新鲜浏览器账号/倍率快照后才能判断是否改名或下架"
        return "脚本成本来自旧种子或低置信来源，不能用来反驳本轮人工真值；需要刷新页面、补 adapter 或确认分组映射"
    if mark == "LEDGER_COST_ABOVE_SITE":
        return "可靠上游来源显示成本高于站内账号倍率：这是需要优先复核的低估风险"
    if mark == "SITE_CONSERVATIVE_REVIEW":
        return "可靠上游来源显示成本低于站内账号倍率：通常是安全垫/保守记录，不亏本，但本轮训练要确认是否故意保守"
    return "需要人工查看来源"


def load_rows(db_path: str, include_excluded: bool, threshold: Decimal) -> list[CompareRow]:
    conn = sqlite3.connect(db_path)
    conn.row_factory = sqlite3.Row
    rows: list[CompareRow] = []
    reference_time = latest_metadata_dt(conn, "last_orchestrated_refresh_at")
    for row in conn.execute(
        """
        select id, category, kind, site, fluter_account_name, upstream_group,
               page_rate, recharge_factor, site_account_multiplier, status
        from upstream_rate_records
        order by category, fluter_account_name, upstream_group
        """
    ):
        excluded = is_excluded(row)
        if excluded and not include_excluded:
            continue
        page = decimal_or_none(row["page_rate"])
        recharge = decimal_or_none(row["recharge_factor"]) or Decimal("1")
        script_cost = page * recharge if page is not None else None
        site_account = decimal_or_none(row["site_account_multiplier"])
        source = source_for_row(conn, row)
        mark = "EXCLUDED" if excluded else verdict(script_cost, site_account, threshold, source, reference_time)
        rows.append(
            CompareRow(
                id=int(row["id"]),
                category=row["category"],
                kind=row["kind"],
                site=row["site"],
                account_name=row["fluter_account_name"],
                upstream_group=row["upstream_group"],
                script_cost=script_cost,
                site_account_multiplier=site_account,
                status=mark,
                source=source.label,
                source_kind=source.kind,
                confidence=source.confidence,
                reason=excluded or likely_reason(mark, row, source, script_cost, site_account, conn),
            )
        )
    conn.close()
    return rows


def main() -> int:
    args = parse_args()
    threshold = decimal_or_none(args.threshold) or Decimal("0.000001")
    rows = load_rows(args.db, args.include_excluded, threshold)
    summary: dict[str, int] = {}
    for row in rows:
        summary[row.status] = summary.get(row.status, 0) + 1

    if args.json:
        print(
            json.dumps(
                {
                    "summary": summary,
                    "rows": [
                        {
                            "id": row.id,
                            "category": row.category,
                            "kind": row.kind,
                            "site": row.site,
                            "account_name": row.account_name,
                            "upstream_group": row.upstream_group,
                            "script_cost": compact(row.script_cost),
                            "site_account_multiplier": compact(row.site_account_multiplier),
                            "status": row.status,
                            "source": row.source,
                            "source_kind": row.source_kind,
                            "confidence": row.confidence,
                            "reason": row.reason,
                        }
                        for row in rows
                    ],
                },
                ensure_ascii=False,
                indent=2,
            )
        )
        return 0

    print("# Ledger vs Site Manual Truth")
    print()
    print("This report is diagnostic only. It does not edit production or ledger data.")
    print()
    print("SUMMARY")
    print(json.dumps(summary, ensure_ascii=False, sort_keys=True))
    print()
    print("| 状态 | 分类 | 账号 | 上游分组 | 脚本成本 | 站内账号倍率 | 置信度 | 来源 | 判断 |")
    print("|---|---|---|---|---:|---:|---|---|---|")
    for row in rows:
        print(
            "| {status} | {category} | {account} | {group} | {script} | {site} | {confidence} | {source} | {reason} |".format(
                status=row.status,
                category=row.category.replace("|", "\\|"),
                account=row.account_name.replace("|", "\\|"),
                group=row.upstream_group.replace("|", "\\|"),
                script=compact(row.script_cost),
                site=compact(row.site_account_multiplier),
                confidence=row.confidence,
                source=row.source.replace("|", "\\|"),
                reason=row.reason.replace("|", "\\|"),
            )
        )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
