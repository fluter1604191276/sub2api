#!/usr/bin/env python3
"""Mark stale upstream ledger rows whose upstream groups disappeared.

This script writes only the independent upstream ledger SQLite database. It
never edits sub2api production accounts, groups, channels, pricing, keys, or
credentials.
"""

from __future__ import annotations

import argparse
import json
import re
import sqlite3
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Any


DEFAULT_DB = "/var/lib/fluterapi-upstream-rates/upstream_rates.sqlite"
REMOVED_STATUS = "上游分组已消失/待重映射"
REMOVED_LABEL = "旧上游分组未在本轮刷新页面/接口出现；以当前站内账号记录为本轮校准参考，等待人工映射到新上游分组"
SKIP_KIND_KEYWORDS = ("生图", "特殊")
SKIP_STATUS_KEYWORDS = ("未接入", "未分配", "未调度", "停用")
SKIP_CATEGORIES = ("KBQ",)
MIN_BROWSER_GROUPS_FOR_REMOVAL = 2
MIN_TAMPERMONKEY_SNAPSHOT_VERSION = (0, 1, 15)
RECENT_FUTURE_SKEW_SECONDS = 300
STATUS_OBSERVATION_MAX_SKEW_SECONDS = 300
EXACT_GROUP_MATCH_TOKENS = {"gpt", "pro", "cc", "ag", "max"}
SITE_ALIASES = {
    "api.tokenskingdom.com": ("api.tokenskingdom.com", "tokenskingdom.com", "image.tokenskingdom.com"),
    "tokenskingdom.com": ("tokenskingdom.com", "api.tokenskingdom.com", "image.tokenskingdom.com"),
    "image.tokenskingdom.com": ("image.tokenskingdom.com", "api.tokenskingdom.com", "tokenskingdom.com"),
}


def site_lookup_keys(site: str) -> tuple[str, ...]:
    normalized = str(site or "").strip().lower()
    return SITE_ALIASES.get(normalized, (normalized,))


@dataclass(frozen=True)
class ObservedGroups:
    source: str
    observed_at: str
    groups: list[str]
    supports_removal: bool


@dataclass(frozen=True)
class CleanupCandidate:
    id: int
    category: str
    kind: str
    site: str
    account_name: str
    upstream_group: str
    status: str
    source: str
    observed_at: str
    seen_groups: list[str]
    needs_update: bool


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Mark removed upstream groups in the independent ledger")
    parser.add_argument("--db", default=DEFAULT_DB)
    parser.add_argument("--apply", action="store_true", help="Write status/note updates to the ledger DB")
    parser.add_argument("--json", action="store_true")
    parser.add_argument(
        "--max-age-hours",
        type=float,
        default=36,
        help="Only trust browser/public observations newer than this many hours relative to the latest orchestrated refresh",
    )
    return parser.parse_args()


def table_exists(conn: sqlite3.Connection, name: str) -> bool:
    return bool(conn.execute("select 1 from sqlite_master where type='table' and name=?", (name,)).fetchone())


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


def latest_metadata_dt(conn: sqlite3.Connection, key: str) -> datetime | None:
    if not table_exists(conn, "metadata"):
        return None
    record = conn.execute("select value from metadata where key = ?", (key,)).fetchone()
    return parse_dt(record["value"]) if record else None


def is_recent_enough(observed_at: str, reference: datetime | None, max_age_hours: float) -> bool:
    observed = parse_dt(observed_at)
    if observed is None:
        return False
    if reference is None:
        reference = datetime.now(timezone.utc)
    age = (reference - observed).total_seconds()
    return -RECENT_FUTURE_SKEW_SECONDS <= age <= max_age_hours * 3600


def observations_are_aligned(left: Any, right: Any, max_skew_seconds: int = STATUS_OBSERVATION_MAX_SKEW_SECONDS) -> bool:
    left_dt = parse_dt(left)
    right_dt = parse_dt(right)
    if left_dt is None or right_dt is None:
        return False
    return abs((left_dt - right_dt).total_seconds()) <= max_skew_seconds


def browser_observation_is_current(
    observed_at: Any,
    status: sqlite3.Row | None,
    reference: datetime | None,
    max_age_hours: float,
) -> bool:
    if not status:
        return False
    return is_recent_enough(observed_at, reference, max_age_hours) and observations_are_aligned(
        status["observed_at"],
        observed_at,
    )


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


def browser_status_supports_group_removal(status: sqlite3.Row) -> bool:
    """Return whether browser-observed groups are complete enough for removal.

    Hidden preserved rows and partial/unstable Tampermonkey reads can still be
    useful diagnostics, but they must not mark missing groups as removed.
    """

    if not status or status["status"] != "browser_observed":
        return False
    detail = str(status["detail"] or "")
    if "rate_lines=0" in detail or re.search(r"\bfresh_rate_lines\s*=\s*0\b", detail):
        return False
    if (
        "preserved previous rate lines" in detail
        or "preserved previous account lines" in detail
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


def browser_status_supports_account_confirmation(status: sqlite3.Row | None) -> bool:
    """Return whether account rows are current enough to confirm a ledger row."""

    if not status or status["status"] != "browser_observed":
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


def normalize_match_text(value: str) -> str:
    value = (value or "").lower()
    value = re.sub(r"sk-[a-z0-9._-]+", "", value)
    value = re.sub(r"\.\.\.redacted(?:-long-token)?\.\.\.", "", value)
    value = value.replace("（", "(").replace("）", ")")
    value = re.sub(r"[\s/_:：,，;；|｜·\-+()（）\[\]【】<>《》\"'“”‘’]", "", value)
    value = value.replace("无限制客户端", "无限客户端").replace("无限刷客户端", "无限客户端")
    return value


def group_matches(left: str, right: str) -> bool:
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


def account_observation_confirms_row(
    conn: sqlite3.Connection,
    row: sqlite3.Row,
    reference: datetime | None,
    max_age_hours: float,
) -> bool:
    """A fresh key-page account row is stronger than a public group omission.

    Some NewAPI public pricing pages list only coarse/new group names while the
    logged-in key page still shows the exact account row. If the current
    account name is visible in a fresh browser snapshot, do not mark its old
    group as removed solely because `/api/pricing` omitted that group.
    """

    if not table_exists(conn, "browser_adapter_account_observations"):
        return False
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
            (row["site"],),
        ).fetchone()
    if not browser_status_supports_account_confirmation(status):
        return False
    normalized = normalize_match_text(str(row["fluter_account_name"] or ""))
    if not normalized:
        return False
    for observed in conn.execute(
        """
        select normalized_account_name, account_name, observed_at
        from browser_adapter_account_observations
        where site = ?
        """,
        (row["site"],),
    ):
        if not browser_observation_is_current(observed["observed_at"], status, reference, max_age_hours):
            continue
        observed_names = (
            normalize_match_text(str(observed["normalized_account_name"] or "")),
            normalize_match_text(str(observed["account_name"] or "")),
        )
        if normalized in observed_names:
            return True
    return False


def skip_row(row: sqlite3.Row) -> bool:
    category = str(row["category"] or "")
    kind = str(row["kind"] or "")
    status = str(row["status"] or "")
    if category in SKIP_CATEGORIES:
        return True
    if any(keyword in kind for keyword in SKIP_KIND_KEYWORDS):
        return True
    if any(keyword in status for keyword in SKIP_STATUS_KEYWORDS):
        return True
    return False


def latest_public_status_ok(conn: sqlite3.Connection, site: str) -> sqlite3.Row | None:
    if not table_exists(conn, "upstream_adapter_status"):
        return None
    return conn.execute(
        """
        select provider, status, detail, observed_at
        from upstream_adapter_status
        where site = ?
          and adapter_kind = 'public_pricing'
        order by observed_at desc
        limit 1
        """,
        (site,),
    ).fetchone()


def browser_status_has_rates(conn: sqlite3.Connection, site: str) -> sqlite3.Row | None:
    if not table_exists(conn, "browser_adapter_status"):
        return None
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
    if not browser_status_supports_group_removal(status):
        return None
    return status


def load_hub_groups_for_site(
    conn: sqlite3.Connection,
    site: str,
    reference: datetime | None,
    max_age_hours: float,
) -> ObservedGroups | None:
    if not table_exists(conn, "upstream_hub_rate_observations"):
        return None
    sites = site_lookup_keys(site)
    placeholders = ",".join("?" for _ in sites)
    rows = [
        row
        for row in conn.execute(
            f"""
            select channel_name, model_name, imported_at
            from upstream_hub_rate_observations
            where site in ({placeholders})
            order by model_name
            """,
            sites,
        )
        if is_recent_enough(row["imported_at"], reference, max_age_hours)
    ]
    if not rows:
        return None
    groups = sorted({row["model_name"] for row in rows if row["model_name"]})
    if not groups:
        return None
    newest = max(rows, key=lambda row: row["imported_at"])
    return ObservedGroups(
        source=f"{newest['channel_name']} upstream_hub",
        observed_at=newest["imported_at"],
        groups=groups,
        supports_removal=len(groups) >= MIN_BROWSER_GROUPS_FOR_REMOVAL,
    )


def observed_groups_for_site(conn: sqlite3.Connection, site: str, reference: datetime | None, max_age_hours: float) -> ObservedGroups:
    hub_groups = load_hub_groups_for_site(conn, site, reference, max_age_hours)
    if hub_groups and hub_groups.supports_removal:
        return hub_groups

    rows: list[tuple[str, str, str, str, bool]] = []
    public_status = latest_public_status_ok(conn, site)
    # Public /api/pricing rows are useful price references, but they are not a
    # complete account/key-page inventory.  Do not let an omitted public group
    # mark a logged-in upstream account group as removed; only current browser
    # snapshots may support removal below.
    public_supports_removal = False
    if table_exists(conn, "provider_group_ratio_records"):
        for row in conn.execute(
            """
            select provider, group_name, updated_at
            from provider_group_ratio_records
            where site = ?
            order by group_name
            """,
            (site,),
        ):
            if is_recent_enough(row["updated_at"], reference, max_age_hours):
                rows.append(
                    (
                        "public_pricing",
                        row["provider"],
                        row["updated_at"],
                        row["group_name"],
                        public_supports_removal,
                    )
                )

    browser_status = browser_status_has_rates(conn, site)
    if table_exists(conn, "browser_adapter_rate_observations") and browser_status:
        browser_observed = str(browser_status["observed_at"] or "")
        if is_recent_enough(browser_observed, reference, max_age_hours):
            browser_rows = [
                row
                for row in conn.execute(
                    """
                    select provider, upstream_group, observed_at
                    from browser_adapter_rate_observations
                    where site = ?
                    order by upstream_group
                    """,
                    (site,),
                )
                if browser_observation_is_current(row["observed_at"], browser_status, reference, max_age_hours)
            ]
            browser_supports_removal = len({row["upstream_group"] for row in browser_rows}) >= MIN_BROWSER_GROUPS_FOR_REMOVAL
            for row in conn.execute(
                """
                select provider, upstream_group, observed_at
                from browser_adapter_rate_observations
                where site = ?
                order by upstream_group
                """,
                (site,),
            ):
                if browser_observation_is_current(row["observed_at"], browser_status, reference, max_age_hours):
                    rows.append(
                        (
                            "browser_snapshot",
                            row["provider"],
                            row["observed_at"],
                            row["upstream_group"],
                            browser_supports_removal,
                        )
                    )

    if not rows:
        return hub_groups or ObservedGroups("", "", [], False)

    removal_rows = [row for row in rows if row[4]]
    source_kind, provider, observed_at, _, _ = max(removal_rows or rows, key=lambda item: item[2])
    groups = sorted({group for _, _, _, group, _ in rows if group} | set(hub_groups.groups if hub_groups else []))
    return ObservedGroups(
        source=f"{provider} {source_kind}",
        observed_at=observed_at,
        groups=groups,
        supports_removal=bool(removal_rows),
    )


def load_candidates(db_path: str, max_age_hours: float) -> list[CleanupCandidate]:
    conn = sqlite3.connect(db_path)
    conn.row_factory = sqlite3.Row
    reference = latest_metadata_dt(conn, "last_orchestrated_refresh_at")
    candidates: list[CleanupCandidate] = []
    for row in conn.execute(
        """
        select id, category, kind, site, fluter_account_name, upstream_group, status
        from upstream_rate_records
        order by category, site, fluter_account_name
        """
    ):
        if skip_row(row):
            continue
        if account_observation_confirms_row(conn, row, reference, max_age_hours):
            continue
        observed = observed_groups_for_site(conn, row["site"], reference, max_age_hours)
        if not observed.groups or not observed.supports_removal:
            continue
        if any(group_matches(row["upstream_group"], group) for group in observed.groups):
            continue
        candidates.append(
            CleanupCandidate(
                id=int(row["id"]),
                category=row["category"],
                kind=row["kind"],
                site=row["site"],
                account_name=row["fluter_account_name"],
                upstream_group=row["upstream_group"],
                status=row["status"],
                source=observed.source,
                observed_at=observed.observed_at,
                seen_groups=observed.groups[:12],
                needs_update=row["status"] != REMOVED_STATUS,
            )
        )
    conn.close()
    return candidates


def apply_cleanup(db_path: str, candidates: list[CleanupCandidate]) -> None:
    conn = sqlite3.connect(db_path)
    conn.row_factory = sqlite3.Row
    now = datetime.now(timezone.utc).isoformat(timespec="seconds")
    with conn:
        changed_count = 0
        for candidate in candidates:
            if not candidate.needs_update:
                continue
            row = conn.execute(
                "select note from upstream_rate_records where id = ?",
                (candidate.id,),
            ).fetchone()
            old_note = str(row["note"] or "") if row else ""
            seen = "、".join(candidate.seen_groups[:8])
            note_line = (
                f"[{now}] 台账清洗：刷新后的 {candidate.source} 未出现旧上游分组 "
                f"「{candidate.upstream_group}」；按当前规则视为已消失，等待人工映射。"
                f"本轮看到：{seen}"
            )
            new_note = (note_line + "\n" + old_note)[:2200]
            conn.execute(
                """
                update upstream_rate_records
                set status = ?,
                    actual_cost_label = ?,
                    note = ?,
                    updated_at = ?
                where id = ?
                """,
                (REMOVED_STATUS, REMOVED_LABEL, new_note, now, candidate.id),
            )
            changed_count += 1
        conn.execute(
            "insert or replace into metadata(key, value) values (?, ?)",
            ("removed_upstream_groups_cleaned_at", now),
        )
        conn.execute(
            "insert or replace into metadata(key, value) values (?, ?)",
            ("removed_upstream_groups_cleaned_count", str(changed_count)),
        )
        conn.execute(
            "insert or replace into metadata(key, value) values (?, ?)",
            ("removed_upstream_groups_candidate_count", str(len(candidates))),
        )
        conn.execute(
            "insert or replace into metadata(key, value) values (?, ?)",
            ("removed_upstream_groups_reported_count", str(len(candidates))),
        )
    conn.close()


def candidate_to_dict(candidate: CleanupCandidate) -> dict[str, Any]:
    return {
        "id": candidate.id,
        "category": candidate.category,
        "kind": candidate.kind,
        "site": candidate.site,
        "account_name": candidate.account_name,
        "upstream_group": candidate.upstream_group,
        "status": candidate.status,
        "source": candidate.source,
        "observed_at": candidate.observed_at,
        "seen_groups": candidate.seen_groups,
        "needs_update": candidate.needs_update,
    }


def main() -> int:
    args = parse_args()
    db_path = Path(args.db)
    if not db_path.exists():
        raise SystemExit(f"DB not found: {db_path}")
    candidates = load_candidates(args.db, args.max_age_hours)
    if args.apply:
        apply_cleanup(args.db, candidates)

    payload = {
        "applied": bool(args.apply),
        "count": len(candidates),
        "rows": [candidate_to_dict(candidate) for candidate in candidates],
    }
    if args.json:
        print(json.dumps(payload, ensure_ascii=False, indent=2))
    else:
        action = "已写入独立台账" if args.apply else "dry-run 未写入"
        print(f"# Removed Upstream Group Cleanup ({action})")
        print()
        print(f"候选数量：{len(candidates)}")
        if candidates:
            print()
            print("| id | 站点 | 账号 | 旧上游分组 | 来源 | 本轮看到的分组 |")
            print("|---:|---|---|---|---|---|")
            for candidate in candidates:
                seen = "、".join(candidate.seen_groups[:8])
                print(
                    f"| {candidate.id} | {candidate.site} | {candidate.account_name} | "
                    f"{candidate.upstream_group} | {candidate.source} | {seen} |"
                )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
