#!/usr/bin/env python3
"""Render the admin-only Fluter upstream rate dashboard from SQLite."""

from __future__ import annotations

import argparse
import html
import json
import re
import sqlite3
from datetime import datetime, timedelta, timezone
from pathlib import Path

from discount_profiles import (
    compact_decimal,
    effective_discount_for_site,
    load_discount_profiles,
)


DEFAULT_DB = "/var/lib/fluterapi-upstream-rates/upstream_rates.sqlite"
DEFAULT_OUTPUT = "/www/fluterapi-home/admin/upstream-rates/index.html"
BEIJING_TZ_NAME = "Asia/Shanghai"
BEIJING_TZ = timezone(timedelta(hours=8), name="CST")
MIN_PROVIDER_TAMPERMONKEY_VERSION = (0, 1, 15)
PROVIDER_SNAPSHOT_MAX_AGE_SECONDS = 3600
PROVIDER_SNAPSHOT_FUTURE_SKEW_SECONDS = 300
PROVIDER_STATUS_SNAPSHOT_MAX_SKEW_SECONDS = 300
SITE_ALIASES = {
    "api.tokenskingdom.com": ("api.tokenskingdom.com", "tokenskingdom.com", "image.tokenskingdom.com"),
    "tokenskingdom.com": ("tokenskingdom.com", "api.tokenskingdom.com", "image.tokenskingdom.com"),
    "image.tokenskingdom.com": ("image.tokenskingdom.com", "api.tokenskingdom.com", "tokenskingdom.com"),
}


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--db", default=DEFAULT_DB)
    parser.add_argument("--output", default=DEFAULT_OUTPUT)
    return parser.parse_args()


def beijing_tz():
    return BEIJING_TZ


def number_or_none(value):
    return None if value is None else float(value)


def normalize_datetime_text(raw: str) -> str:
    value = raw.replace("Z", "+00:00")
    value = re.sub(
        r"(\d{2}:\d{2}:\d{2}(?:\.\d+)?)([+-]\d{2})(\d{2})$",
        r"\1\2:\3",
        value,
    )
    value = re.sub(
        r"(\d{2}:\d{2}:\d{2}(?:\.\d+)?)([+-]\d{2})$",
        r"\1\2:00",
        value,
    )
    return value


def format_beijing_time(value: object) -> str:
    if value is None:
        return ""
    if isinstance(value, datetime):
        parsed = value
    else:
        raw = str(value or "").strip()
        if not raw:
            return ""
        try:
            parsed = datetime.fromisoformat(normalize_datetime_text(raw))
        except ValueError:
            return raw
    if parsed.tzinfo is None:
        parsed = parsed.replace(tzinfo=timezone.utc)
    return parsed.astimezone(beijing_tz()).strftime("%Y-%m-%d %H:%M:%S 北京时间")


def parse_utc_datetime(value: object) -> datetime | None:
    if value is None:
        return None
    if isinstance(value, datetime):
        parsed = value
    else:
        raw = str(value or "").strip()
        if not raw:
            return None
        try:
            parsed = datetime.fromisoformat(normalize_datetime_text(raw))
        except ValueError:
            return None
    if parsed.tzinfo is None:
        parsed = parsed.replace(tzinfo=timezone.utc)
    return parsed.astimezone(timezone.utc)


def observed_age_seconds(observed_at: object, reference: datetime | None = None) -> float | None:
    observed = parse_utc_datetime(observed_at)
    if observed is None:
        return None
    if reference is None:
        reference = datetime.now(timezone.utc)
    return (reference - observed).total_seconds()


def observation_is_recent(
    observed_at: object,
    *,
    max_age_seconds: int | None = None,
    future_skew_seconds: int | None = None,
    reference: datetime | None = None,
) -> bool:
    if max_age_seconds is None:
        max_age_seconds = PROVIDER_SNAPSHOT_MAX_AGE_SECONDS
    if future_skew_seconds is None:
        future_skew_seconds = PROVIDER_SNAPSHOT_FUTURE_SKEW_SECONDS
    age = observed_age_seconds(observed_at, reference)
    if age is None:
        return False
    return -future_skew_seconds <= age <= max_age_seconds


def observations_are_aligned(left: object, right: object, *, max_skew_seconds: int | None = None) -> bool:
    if max_skew_seconds is None:
        max_skew_seconds = PROVIDER_STATUS_SNAPSHOT_MAX_SKEW_SECONDS
    left_dt = parse_utc_datetime(left)
    right_dt = parse_utc_datetime(right)
    if left_dt is None or right_dt is None:
        return False
    return abs((left_dt - right_dt).total_seconds()) <= max_skew_seconds


def format_metadata_value(key: str, value: object) -> str:
    raw = "" if value is None else str(value)
    if not raw:
        return ""
    if any(word in key.lower() for word in ("time", "updated", "observed", "snapshot", "refresh", "run", "audit", "generated")):
        return redact_sensitive_text(format_beijing_time(raw))
    return redact_sensitive_text(raw)


def site_lookup_keys(site: object) -> tuple[str, ...]:
    normalized = str(site or "").strip().lower()
    return SITE_ALIASES.get(normalized, (normalized,))


def set_site_alias_items(target: dict[str, dict[str, str]], site: object, item: dict[str, str]) -> None:
    for key in site_lookup_keys(site):
        if key:
            target[key] = item


def metadata_key_is_renderable(key: object) -> bool:
    return str(key or "") not in {
        "priority_plan_manual_write_command",
    }


def renderable_metadata(metadata: object) -> dict[str, str]:
    if not isinstance(metadata, dict):
        return {}
    return {
        str(key): format_metadata_value(str(key), value)
        for key, value in metadata.items()
        if metadata_key_is_renderable(key)
    }


def adapter_status_rank(item: dict[str, object]) -> tuple[int, str, str]:
    status = str(item.get("status") or "")
    if status in {"covered_by_upstream_hub", "hub_observed"}:
        return (0, str(item.get("provider") or ""), str(item.get("site") or ""))
    if status == "ok":
        return (1, str(item.get("provider") or ""), str(item.get("site") or ""))
    if status in {"hub_error", "hub_observed_empty"}:
        return (2, str(item.get("provider") or ""), str(item.get("site") or ""))
    if status == "covered_by_browser":
        return (3, str(item.get("provider") or ""), str(item.get("site") or ""))
    if status in {"browser_observed", "browser_observed_empty"}:
        return (4, str(item.get("provider") or ""), str(item.get("site") or ""))
    if status == "failed":
        return (5, str(item.get("provider") or ""), str(item.get("site") or ""))
    if status == "needs_adapter":
        return (6, str(item.get("provider") or ""), str(item.get("site") or ""))
    return (7, str(item.get("provider") or ""), str(item.get("site") or ""))


def load_browser_balance_snapshots(conn: sqlite3.Connection) -> dict[str, dict[str, str]]:
    snapshots = load_api_balance_snapshots(conn)
    has_table = conn.execute(
        "select 1 from sqlite_master where type = 'table' and name = 'browser_adapter_snapshots'"
    ).fetchone()
    if has_table:
        for row in conn.execute(
            """
            select provider, site, detected_balance, observed_at
            from browser_adapter_snapshots
            where detected_balance <> ''
            """
        ):
            item = {
                "provider": row["provider"],
                "site": row["site"],
                "balance": row["detected_balance"],
                "observedAt": format_beijing_time(row["observed_at"]),
            }
            snapshots[row["site"]] = item
            for alias in site_lookup_keys(row["site"]):
                snapshots.setdefault(alias, item)
    load_upstream_hub_balance_snapshots(conn, snapshots)
    return snapshots


def load_api_balance_snapshots(conn: sqlite3.Connection) -> dict[str, dict[str, str]]:
    has_table = conn.execute(
        "select 1 from sqlite_master where type = 'table' and name = 'balance_api_snapshots'"
    ).fetchone()
    if not has_table:
        return {}

    snapshots: dict[str, dict[str, str]] = {}
    for row in conn.execute(
        """
        select provider, site, balance_label, observed_at
        from balance_api_snapshots
        where balance_label <> ''
        order by observed_at desc, account_id asc
        """
    ):
        if row["site"] in snapshots:
            continue
        item = {
            "provider": row["provider"],
            "site": row["site"],
            "balance": row["balance_label"],
            "observedAt": format_beijing_time(row["observed_at"]),
        }
        snapshots[row["site"]] = item
        for alias in site_lookup_keys(row["site"]):
            snapshots.setdefault(alias, item)
    return snapshots


def load_upstream_hub_balance_snapshots(
    conn: sqlite3.Connection,
    snapshots: dict[str, dict[str, str]],
) -> None:
    """Overlay balances collected by upstream-hub.

    upstream-hub is the preferred logged-in collection source after the
    ledger/hub merge.  Older API and browser balances stay useful as fallback
    diagnostics, but a hub balance should win when both are present.
    """

    if not table_exists(conn, "upstream_hub_channels"):
        return

    seen: set[str] = set()
    for row in conn.execute(
        """
        select channel_name, site, last_balance_label, last_balance_at, imported_at
        from upstream_hub_channels
        where last_balance_label <> ''
        order by imported_at desc, channel_id asc
        """
    ):
        site = row["site"]
        if not site or site in seen:
            continue
        observed_at = row["last_balance_at"] or row["imported_at"]
        item = {
            "provider": row["channel_name"],
            "site": site,
            "balance": row["last_balance_label"],
            "observedAt": format_beijing_time(observed_at),
        }
        set_site_alias_items(snapshots, site, item)
        seen.add(site)


def table_exists(conn: sqlite3.Connection, table_name: str) -> bool:
    return bool(
        conn.execute(
            "select 1 from sqlite_master where type = 'table' and name = ?",
            (table_name,),
        ).fetchone()
    )


def json_list(value: object) -> list[object]:
    try:
        data = json.loads(str(value or "[]"))
    except json.JSONDecodeError:
        return []
    return data if isinstance(data, list) else []


def compact_text(value: object, limit: int = 500) -> str:
    return " ".join(str(value or "").split())[:limit]


SENSITIVE_TEXT_REPLACEMENTS = (
    (re.compile(r"sk-[A-Za-z0-9_-]{2,}(?:\.\.\.|\*{3,})[A-Za-z0-9_-]{2,}", re.IGNORECASE), "[redacted-key]"),
    (re.compile(r"sk-[A-Za-z0-9_-]{8,}", re.IGNORECASE), "[redacted-key]"),
    (re.compile(r"Bearer\s+[A-Za-z0-9._~+/=-]+", re.IGNORECASE), "Bearer [redacted-token]"),
    (
        re.compile(
            r"\b(access[_-]?token|api[_-]?key|cookie|password)\s*[:=]\s*[^\s;&\"'<>]+",
            re.IGNORECASE,
        ),
        lambda match: f"{match.group(1)}=[redacted]",
    ),
)


def redact_sensitive_text(value: object, limit: int = 500) -> str:
    text = compact_text(value, limit)
    for pattern, replacement in SENSITIVE_TEXT_REPLACEMENTS:
        text = pattern.sub(replacement, text)
    return text


RATE_VALUE_RE = re.compile(
    r"(?<![0-9.])(?:"
    r"(\d+(?:\.\d+)?)\s*[xX]"
    r"|×\s*(\d+(?:\.\d+)?)"
    r"|(\d+(?:\.\d+)?)\s*倍"
    r")"
)


def snapshot_rate_matches(value: object) -> list[re.Match[str]]:
    return list(RATE_VALUE_RE.finditer(compact_text(value, 500)))


def snapshot_rate_value(match: re.Match[str]) -> float:
    return float(next(group for group in match.groups() if group is not None))


def strip_snapshot_rate_markers(value: object) -> str:
    text = RATE_VALUE_RE.sub("", compact_text(value, 500))
    text = re.sub(r"选择分组.*$", "", text)
    return text.strip(" /:：|｜-")


def looks_like_quota_only_account_line(account_name: str, source_line: str) -> bool:
    account = compact_text(account_name, 160)
    source = compact_text(source_line, 500)
    if not account or not source:
        return False
    if any(
        marker.lower() in account.lower()
        for marker in (
            "codex",
            "claude",
            "gpt",
            "deepseek",
            "gemini",
            "grok",
            "sonnet",
            "opus",
            "haiku",
            "meow",
            "magic",
            "kingdom",
            "kbq",
            "mouubox",
            "超超",
            "钧澈",
            "聪明",
            "生图",
            "仅文字",
            "文字",
            "plus",
            "pro",
            "team",
            "ccmax",
        )
    ):
        return False
    has_quota_pair = bool(re.search(r"[¥￥$]\s*\d+(?:\.\d+)?\s*/\s*[¥￥$]\s*\d+(?:\.\d+)?", source))
    has_key_markers = any(marker in source for marker in ("quota usage", "已启用", "无限额度", "有限额度", "Tag:"))
    has_group_rate = bool(re.search(r"(?<![A-Za-z0-9_.])\d+(?:\.\d+)?\s*[xX](?![A-Za-z0-9_.])", source))
    return has_quota_pair and has_key_markers and has_group_rate


def repair_provider_account_name(provider: str, site: str, account_name: str, source_line: str) -> str:
    name = compact_text(account_name, 160)
    # TokensKingdom's virtualized key table has occasionally dropped the first
    # visible "k" from the first row while the rest of the row is intact.  Repair
    # only this exact, site-bound typo for display/matching hygiene.
    if site == "api.tokenskingdom.com" and name.startswith("ingdom "):
        return "k" + name
    return name


def repair_provider_snapshot_group(account_name: str, upstream_group: str, source_line: str) -> str:
    group = compact_text(upstream_group, 160)
    account_norm = compact_text(account_name, 160)
    source = compact_text(source_line, 500)
    if source and account_norm and source.startswith(account_norm):
        tail = source[len(account_norm) :].strip(" /:：|｜-")
        key_match = re.match(
            r"(sk-[A-Za-z0-9_-]{2,}\.\.\.[A-Za-z0-9_-]{2,}|"
            r"sk-[A-Za-z0-9_-]{3,}\*{3,}[A-Za-z0-9_-]{2,}|"
            r"\.\.\.redacted-long-token\.\.\.)\s+",
            tail,
            re.IGNORECASE,
        )
        if key_match:
            after_key = tail[key_match.end() :]
            first_rate = snapshot_rate_matches(after_key)
            if first_rate:
                candidate = strip_snapshot_rate_markers(after_key[: first_rate[0].start()])
                if candidate:
                    group = candidate
    return group


def repair_provider_snapshot_rate(raw_rate: object, source_line: str) -> float | None:
    try:
        current = float(raw_rate) if raw_rate not in (None, "") else None
    except (TypeError, ValueError):
        current = None
    # Only repair clearly impossible "8x from 1.8x1.8x" style artifacts.
    if current is not None and current >= 2:
        matches = snapshot_rate_matches(source_line)
        decimals = [snapshot_rate_value(match) for match in matches if "." in match.group(0)]
        plausible = [value for value in decimals if value < current]
        if plausible:
            return plausible[-1]
    return current


def browser_names_match(left: object, right: object) -> bool:
    left_text = str(left or "").strip().lower()
    right_text = str(right or "").strip().lower()
    return bool(left_text and right_text and left_text == right_text)


def clean_provider_snapshot_account(
    item: object,
    *,
    provider: str,
    site: str,
) -> dict[str, object] | None:
    if not isinstance(item, dict):
        return None
    source_line = compact_text(item.get("source_line"), 500)
    account_name = repair_provider_account_name(
        provider,
        site,
        compact_text(item.get("account_name") or item.get("name"), 160),
        source_line,
    )
    upstream_group = repair_provider_snapshot_group(
        account_name,
        compact_text(item.get("upstream_group") or item.get("group"), 160),
        source_line,
    )
    if looks_like_quota_only_account_line(account_name, source_line):
        return None
    if not account_name:
        return None
    page_rate = repair_provider_snapshot_rate(item.get("page_rate", item.get("rate")), source_line)
    return {
        "account_name": account_name,
        "upstream_group": upstream_group,
        "page_rate": page_rate,
        "source_line": redact_sensitive_text(source_line or account_name),
    }


def provider_snapshot_accounts_are_current(
    detail: object,
    status: object = "browser_observed",
    status_browser: object = "",
    snapshot_browser: object = "",
    observed_at: object = None,
    snapshot_observed_at: object = None,
) -> bool:
    """Return whether snapshot account rows came from this read.

    Browser adapters may preserve previous non-empty account rows when a SPA
    briefly renders only a balance or control panel.  That fallback is useful
    for audit continuity, but the Provider page is a "what did the upstream page
    show this time?" view, so preserved account rows must stay out of it.
    """

    if str(status or "") != "browser_observed":
        return False
    if observed_at is not None and not observation_is_recent(observed_at):
        return False
    if snapshot_observed_at is not None and not observation_is_recent(snapshot_observed_at):
        return False
    if observed_at is not None and snapshot_observed_at is not None and not observations_are_aligned(
        observed_at,
        snapshot_observed_at,
    ):
        return False
    text = str(detail or "")
    if "preserved previous account lines" in text or "preserved previous non-empty snapshot" in text:
        return False
    if re.search(r"\bfresh_account_lines\s*=\s*0\b", text):
        return False
    if not browser_names_match(status_browser, snapshot_browser):
        return False
    if provider_snapshot_source_state(
        detail,
        status=status,
        status_browser=status_browser,
        snapshot_browser=snapshot_browser,
        observed_at=observed_at,
        snapshot_observed_at=snapshot_observed_at,
    ) != "current":
        return False
    return True


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


def provider_snapshot_script_version(detail: object) -> tuple[int, ...] | None:
    match = re.search(r"\bscript\s*=\s*([0-9]+(?:\.[0-9]+){0,3})\b", str(detail or ""))
    return parse_semver(match.group(1)) if match else None


def format_semver(value: tuple[int, ...] | None) -> str:
    return ".".join(str(part) for part in value) if value else ""


def browser_status_is_current_coverage(status: object, detail: object) -> bool:
    if str(status or "") != "browser_observed":
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
        version = provider_snapshot_script_version(text)
        if version is None or semver_lt(version, MIN_PROVIDER_TAMPERMONKEY_VERSION):
            return False
    return bool(
        re.search(r"\b(?:account_lines|rate_lines)\s*=\s*[1-9]\d*\b", text)
        or re.search(r"\bfresh_(?:account|rate)_lines\s*=\s*[1-9]\d*\b", text)
    )


def provider_snapshot_source_state(
    detail: object,
    status: object = "browser_observed",
    status_browser: object = "",
    snapshot_browser: object = "",
    observed_at: object = None,
    snapshot_observed_at: object = None,
) -> str:
    if str(status or "") != "browser_observed":
        return "capture_incomplete"
    text = str(detail or "")
    if (
        "preserved previous account lines" in text
        or "preserved previous non-empty snapshot" in text
        or re.search(r"\bfresh_account_lines\s*=\s*0\b", text)
    ):
        return "hidden_preserved"
    if not browser_names_match(status_browser, snapshot_browser):
        return "browser_mismatch"
    if "Chrome Tampermonkey read-only snapshot" in text:
        version = provider_snapshot_script_version(text)
        if version is None or semver_lt(version, MIN_PROVIDER_TAMPERMONKEY_VERSION):
            return "legacy_script"
        if re.search(r"\bwait_state\s*=\s*timeout\b", text):
            return "unstable_snapshot"
        if "partial account snapshot" in text:
            return "partial_snapshot"
    if observed_at is not None and not observation_is_recent(observed_at):
        return "stale_snapshot"
    if snapshot_observed_at is not None and not observation_is_recent(snapshot_observed_at):
        return "stale_snapshot"
    if observed_at is not None and snapshot_observed_at is not None and not observations_are_aligned(
        observed_at,
        snapshot_observed_at,
    ):
        return "misaligned_snapshot"
    return "current"


def provider_fresh_account_count(detail: object) -> int | None:
    """Extract the account row count reported by the latest page read."""

    text = str(detail or "")
    for pattern in (r"\bfresh_account_lines\s*=\s*(\d+)\b", r"\baccount_lines\s*=\s*(\d+)\b"):
        match = re.search(pattern, text)
        if match:
            return int(match.group(1))
    return None


def provider_snapshot_has_extraction_loss(detail: object, extracted_account_count: int) -> bool:
    """Return whether the page reported more account rows than we extracted.

    Virtualized upstream key tables can expose a high visible account-line count
    while the Tampermonkey row parser only emits a smaller account JSON list.
    Treat that as a partial capture: it is useful diagnostic evidence, but not a
    complete current provider inventory.
    """

    fresh_account_count = provider_fresh_account_count(detail)
    if "ignored low-signal collector snapshot" in str(detail or ""):
        return False
    if fresh_account_count is None or fresh_account_count <= 0:
        return False
    return 0 <= extracted_account_count < fresh_account_count


def load_provider_diagnostics(conn: sqlite3.Connection) -> list[dict[str, object]]:
    """Summarize the current browser/public provider source state.

    Provider rows are intentionally strict: they only show account rows from the
    current upstream page read.  Diagnostics keep the hidden/preserved state
    visible without letting preserved account rows leak back into the Provider
    table.
    """

    snapshots_by_pair: dict[tuple[str, str], sqlite3.Row] = {}
    if table_exists(conn, "browser_adapter_snapshots"):
        snapshots_by_pair = {
            (row["provider"], row["site"]): row
            for row in conn.execute(
                """
                select provider, site, browser, detected_accounts_json, detected_balance,
                       page_url, page_title, observed_at
                from browser_adapter_snapshots
                """
            )
        }

    status_by_pair: dict[tuple[str, str], sqlite3.Row] = {}
    if table_exists(conn, "browser_adapter_status"):
        status_by_pair = {
            (row["provider"], row["site"]): row
            for row in conn.execute(
                """
                select provider, site, browser, status, detail, observed_at
                from browser_adapter_status
                """
            )
        }

    keys = sorted(
        set(snapshots_by_pair) | set(status_by_pair),
        key=lambda item: (str(item[0]), str(item[1])),
    )
    diagnostics: list[dict[str, object]] = []
    for provider, site in keys:
        snapshot = snapshots_by_pair.get((provider, site))
        status = status_by_pair.get((provider, site))
        detail = status["detail"] if status else ""
        raw_accounts = json_list(snapshot["detected_accounts_json"] if snapshot else "[]")
        cleaned_accounts = [
            cleaned
            for account in raw_accounts
            if (
                cleaned := clean_provider_snapshot_account(
                    account,
                    provider=provider,
                    site=site,
                )
            )
        ]
        fresh_account_count = provider_fresh_account_count(detail)
        script_version = provider_snapshot_script_version(detail)
        explicit_source_state = provider_snapshot_source_state(
            detail,
            status=status["status"] if status else "",
            status_browser=status["browser"] if status else "",
            snapshot_browser=snapshot["browser"] if snapshot else "",
            observed_at=status["observed_at"] if status else snapshot["observed_at"] if snapshot else "",
            snapshot_observed_at=snapshot["observed_at"] if snapshot else None,
        )
        current = explicit_source_state == "current"
        extraction_loss = current and provider_snapshot_has_extraction_loss(detail, len(raw_accounts))
        displayed_count = len(cleaned_accounts) if current and not extraction_loss else 0
        if extraction_loss:
            source_state = "extraction_loss"
        elif not current and explicit_source_state != "current":
            source_state = explicit_source_state
        elif not current:
            source_state = "hidden_preserved"
        elif raw_accounts and not cleaned_accounts:
            source_state = "filtered"
        elif displayed_count:
            source_state = "current"
        else:
            source_state = "empty"
        diagnostics.append(
            {
                "provider": provider,
                "site": site,
                "browser": status["browser"] if status else "",
                "status": status["status"] if status else "",
                "detail": detail,
                "observedAt": format_beijing_time(
                    status["observed_at"] if status else snapshot["observed_at"] if snapshot else ""
                ),
                "snapshotObservedAt": format_beijing_time(snapshot["observed_at"] if snapshot else ""),
                "pageUrl": snapshot["page_url"] if snapshot else "",
                "pageTitle": snapshot["page_title"] if snapshot else "",
                "balanceLabel": snapshot["detected_balance"] if snapshot else "",
                "rawAccountCount": len(raw_accounts),
                "cleanAccountCount": len(cleaned_accounts),
                "freshAccountCount": fresh_account_count,
                "displayedAccountCount": displayed_count,
                "scriptVersion": format_semver(script_version),
                "minimumScriptVersion": format_semver(MIN_PROVIDER_TAMPERMONKEY_VERSION),
                "sourceState": source_state,
            }
        )
    return diagnostics


def load_provider_observations(
    conn: sqlite3.Connection,
    browser_balances: dict[str, dict[str, str]],
) -> list[dict[str, object]]:
    """Load current upstream observations, not historical ledger rows.

    The provider view is meant to answer "what does the latest upstream page
    show now?"  It therefore reads only the latest browser snapshot JSON, not
    cumulative observation tables or upstream_rate_records, both of which
    intentionally retain history.
    """

    observations: list[dict[str, object]] = []
    hub_sites: set[str] = set()

    def append_item(
        *,
        provider: str,
        site: str,
        source_kind: str,
        source_label: str,
        account_name: str = "",
        upstream_group: str = "",
        page_rate: object = None,
        source_line: str = "",
        matched_ledger_rows: object = None,
        observed_at: object = None,
    ) -> None:
        browser_balance = browser_balances.get(site)
        observations.append(
            {
                "provider": provider,
                "site": site,
                "sourceKind": source_kind,
                "sourceLabel": source_label,
                "accountName": account_name,
                "upstreamGroup": upstream_group,
                "pageRate": number_or_none(page_rate),
                "sourceLine": redact_sensitive_text(source_line),
                "matchedLedgerRows": matched_ledger_rows,
                "observedAt": format_beijing_time(observed_at),
                "balanceLabel": browser_balance["balance"] if browser_balance else "",
                "balanceUpdatedAt": browser_balance["observedAt"] if browser_balance else "",
            }
        )

    if table_exists(conn, "upstream_hub_rate_observations"):
        for row in conn.execute(
            """
            select channel_name, site, model_name, description, page_rate,
                   completion_ratio, last_seen_at, imported_at
            from upstream_hub_rate_observations
            order by channel_name, site, model_name
            """
        ):
            hub_sites.update(site_lookup_keys(row["site"]))
            browser_balance = browser_balances.get(row["site"])
            source_line = (
                f"upstream-hub last_seen={format_beijing_time(row['last_seen_at'])}; "
                f"completion_ratio={row['completion_ratio'] if row['completion_ratio'] is not None else '-'}"
            )
            observations.append(
                {
                    "provider": row["channel_name"],
                    "site": row["site"],
                    "sourceKind": "upstream_hub_rate_snapshot",
                    "sourceLabel": "upstream-hub 当前倍率快照",
                    "accountName": row["channel_name"],
                    "upstreamGroup": row["model_name"],
                    "pageRate": number_or_none(row["page_rate"]),
                    "sourceLine": redact_sensitive_text(source_line),
                    "matchedLedgerRows": None,
                    "observedAt": format_beijing_time(row["imported_at"]),
                    "balanceLabel": browser_balance["balance"] if browser_balance else "",
                    "balanceUpdatedAt": browser_balance["observedAt"] if browser_balance else "",
                }
            )

    if table_exists(conn, "browser_adapter_snapshots"):
        browser_status_by_pair = {}
        if table_exists(conn, "browser_adapter_status"):
            browser_status_by_pair = {
                (row["provider"], row["site"]): {
                    "browser": row["browser"],
                    "status": row["status"],
                    "detail": row["detail"],
                    "observed_at": row["observed_at"],
                }
                for row in conn.execute(
                    """
                    select provider, site, browser, status, detail, observed_at
                    from browser_adapter_status
                    """
                )
            }
        for row in conn.execute(
            """
            select provider, site, browser, detected_accounts_json, observed_at
            from browser_adapter_snapshots
            order by provider, site
            """
        ):
            if row["site"] in hub_sites:
                continue
            status_item = browser_status_by_pair.get((row["provider"], row["site"]), {})
            detail = status_item.get("detail", "")
            raw_accounts = json_list(row["detected_accounts_json"])
            if not provider_snapshot_accounts_are_current(
                detail,
                status=status_item.get("status", ""),
                status_browser=status_item.get("browser", ""),
                snapshot_browser=row["browser"],
                observed_at=status_item.get("observed_at", row["observed_at"]),
                snapshot_observed_at=row["observed_at"],
            ):
                continue
            if (
                provider_snapshot_source_state(
                    detail,
                    status=status_item.get("status", ""),
                    status_browser=status_item.get("browser", ""),
                    snapshot_browser=row["browser"],
                    observed_at=status_item.get("observed_at", row["observed_at"]),
                    snapshot_observed_at=row["observed_at"],
                )
                == "current"
                and provider_snapshot_has_extraction_loss(detail, len(raw_accounts))
            ):
                continue
            for account in raw_accounts:
                cleaned = clean_provider_snapshot_account(
                    account,
                    provider=row["provider"],
                    site=row["site"],
                )
                if not cleaned:
                    continue
                append_item(
                    provider=row["provider"],
                    site=row["site"],
                    source_kind="browser_account_snapshot",
                    source_label="浏览器当前账号快照",
                    account_name=cleaned["account_name"],
                    upstream_group=cleaned["upstream_group"],
                    page_rate=cleaned["page_rate"],
                    source_line=cleaned["source_line"],
                    matched_ledger_rows=None,
                    observed_at=row["observed_at"],
                )

    observations.sort(
        key=lambda item: (
            str(item["provider"]),
            str(item["site"]),
            str(item["sourceKind"]),
            str(item["accountName"]),
            str(item["upstreamGroup"]),
        )
    )
    return observations


def balance_snapshot_rows(browser_balances: dict[str, dict[str, str]]) -> list[dict[str, str]]:
    """Return one current balance row per upstream site.

    Balance radar is a wallet view, so it should include upstream-hub balances
    even before a provider has curated account rows in upstream_rate_records.
    """

    rows: list[dict[str, str]] = []
    seen: set[tuple[str, str]] = set()
    for key in sorted(browser_balances):
        item = browser_balances[key]
        site = str(item.get("site") or key)
        balance = str(item.get("balance") or "")
        if not balance:
            continue
        identity = (site, balance)
        if identity in seen:
            continue
        seen.add(identity)
        rows.append(
            {
                "provider": str(item.get("provider") or ""),
                "site": site,
                "balanceLabel": balance,
                "balanceUpdatedAt": str(item.get("observedAt") or ""),
            }
        )
    rows.sort(key=lambda row: (row["provider"], row["site"], row["balanceLabel"]))
    return rows


def load_rows(db_path: str):
    conn = sqlite3.connect(db_path)
    conn.row_factory = sqlite3.Row
    browser_balances = load_browser_balance_snapshots(conn)
    balance_snapshots = balance_snapshot_rows(browser_balances)
    discount_profiles = load_discount_profiles(conn)
    provider_observations = load_provider_observations(conn, browser_balances)
    provider_diagnostics = load_provider_diagnostics(conn)
    rows = []
    for row in conn.execute(
        """
        select *
        from upstream_rate_records
        order by
          case status
            when '需核对' then 0
            when '接近成本/需谨慎' then 1
            when '未确认' then 2
            when '需按单张流水核对' then 3
            when '需按模型价换算' then 4
            when '偏保守' then 5
            else 6
          end,
          category,
          site,
          fluter_account_name
        """
    ):
        browser_balance = browser_balances.get(row["site"])
        balance_label = row["balance_label"]
        balance_updated_at = row["balance_updated_at"]
        if browser_balance:
            balance_label = browser_balance["balance"]
            balance_updated_at = f"只读观察 {browser_balance['observedAt']}"
        page_rate = number_or_none(row["page_rate"])
        discount = effective_discount_for_site(
            discount_profiles,
            row["site"],
            row["recharge_factor"],
            row["recharge_ratio_label"],
        )
        recharge_factor = float(discount.recharge_factor)
        actual_cost_multiplier = None
        if page_rate is not None:
            actual_cost_multiplier = page_rate * recharge_factor
        site_account_multiplier = number_or_none(row["site_account_multiplier"])
        cost_record_ratio = None
        if actual_cost_multiplier and site_account_multiplier is not None:
            cost_record_ratio = site_account_multiplier / actual_cost_multiplier
        stored_actual_cost_label = row["actual_cost_label"] if "actual_cost_label" in row.keys() else ""
        if (
            stored_actual_cost_label
            and ("生图" in row["kind"] or "/张" in stored_actual_cost_label or "每张" in stored_actual_cost_label)
        ):
            actual_cost_label = stored_actual_cost_label
        elif page_rate is not None:
            actual_cost_label = (
                f"实际成本倍率 {compact_decimal(actual_cost_multiplier)}x"
                f"（页面倍率 {compact_decimal(page_rate)} × 充值系数 {compact_decimal(discount.recharge_factor)}）"
            )
        else:
            actual_cost_label = stored_actual_cost_label
        rows.append(
            {
                "category": row["category"],
                "kind": row["kind"],
                "site": row["site"],
                "fluterAccountName": row["fluter_account_name"],
                "upstreamGroup": row["upstream_group"],
                "pageRate": page_rate,
                "rechargeRatioLabel": discount.recharge_ratio_label,
                "rechargeFactor": recharge_factor,
                "discountSource": discount.source,
                "discountStatus": discount.status,
                "discountConfidence": discount.confidence,
                "discountNote": discount.note,
                "actualCostMultiplier": actual_cost_multiplier,
                "siteAccountMultiplier": site_account_multiplier,
                "siteGroupMultiplier": row["site_group_multiplier"],
                "actualCostLabel": actual_cost_label,
                "balanceLabel": balance_label,
                "balanceUpdatedAt": format_beijing_time(balance_updated_at),
                "status": row["status"],
                "note": row["note"],
                "updatedAt": format_beijing_time(row["updated_at"]),
                "costRecordRatio": cost_record_ratio,
            }
        )
    metadata = renderable_metadata(
        {
            row["key"]: row["value"]
            for row in conn.execute("select key, value from metadata order by key")
        }
    )
    kbq_rows = []
    has_kbq_table = conn.execute(
        "select 1 from sqlite_master where type = 'table' and name = 'kbq_token_model_records'"
    ).fetchone()
    if has_kbq_table:
        for row in conn.execute(
            """
            select *
            from kbq_token_model_records
            order by category, cost_multiplier, model_name
            """
        ):
            kbq_rows.append(
                {
                    "category": row["category"],
                    "modelName": row["model_name"],
                    "baseModel": row["base_model"],
                    "costMultiplier": number_or_none(row["cost_multiplier"]),
                    "endpoints": row["endpoints"],
                    "inputUsdPer1M": number_or_none(row["input_usd_per_1m"]),
                    "outputUsdPer1M": number_or_none(row["output_usd_per_1m"]),
                    "cacheReadUsdPer1M": number_or_none(row["cache_read_usd_per_1m"]),
                    "cacheWriteUsdPer1M": number_or_none(row["cache_write_usd_per_1m"]),
                    "rawModelRatio": number_or_none(row["raw_model_ratio"]),
                    "officialInputUsdPer1M": number_or_none(row["official_input_usd_per_1m"]),
                    "officialOutputUsdPer1M": number_or_none(row["official_output_usd_per_1m"]),
                    "officialCacheReadUsdPer1M": number_or_none(row["official_cache_read_usd_per_1m"]),
                    "officialCacheWriteUsdPer1M": number_or_none(row["official_cache_write_usd_per_1m"]),
                    "officialLabel": row["official_label"],
                    "pricingVersion": row["pricing_version"],
                    "sourceUrl": row["source_url"],
                    "note": row["note"],
                    "updatedAt": format_beijing_time(row["updated_at"]),
                }
            )
    kbq_per_call_rows = []
    has_kbq_per_call_table = conn.execute(
        "select 1 from sqlite_master where type = 'table' and name = 'kbq_per_call_model_records'"
    ).fetchone()
    if has_kbq_per_call_table:
        for row in conn.execute(
            """
            select *
            from kbq_per_call_model_records
            order by category, effective_per_call_cost, model_name
            """
        ):
            kbq_per_call_rows.append(
                {
                    "category": row["category"],
                    "modelName": row["model_name"],
                    "baseModel": row["base_model"],
                    "perCallPrice": number_or_none(row["per_call_price"]),
                    "effectivePerCallCost": number_or_none(row["effective_per_call_cost"]),
                    "rechargeFactor": number_or_none(row["recharge_factor"]),
                    "endpoints": row["endpoints"],
                    "tags": row["tags"],
                    "description": row["description"],
                    "pricingVersion": row["pricing_version"],
                    "sourceUrl": row["source_url"],
                    "note": row["note"],
                    "updatedAt": format_beijing_time(row["updated_at"]),
                }
            )
    audit_summary = None
    audit_buckets = []
    has_audit_table = conn.execute(
        "select 1 from sqlite_master where type = 'table' and name = 'kbq_true_cost_audit_runs'"
    ).fetchone()
    if has_audit_table:
        audit_run = conn.execute(
            """
            select *
            from kbq_true_cost_audit_runs
            order by id desc
            limit 1
            """
        ).fetchone()
        if audit_run:
            audit_summary = {
                "id": audit_run["id"],
                "observedAt": format_beijing_time(audit_run["observed_at"]),
                "hours": audit_run["hours"],
                "pricingVersion": audit_run["pricing_version"],
                "requestCount": audit_run["request_count"],
                "bucketCount": audit_run["bucket_count"],
                "userBilledCost": number_or_none(audit_run["user_billed_cost"]),
                "trueUpstreamCost": number_or_none(audit_run["true_upstream_cost"]),
                "margin": number_or_none(audit_run["margin"]),
                "marginPercent": number_or_none(audit_run["margin_percent"]),
                "realLossBucketCount": audit_run["real_loss_bucket_count"],
                "displayDriftBucketCount": audit_run["display_drift_bucket_count"],
                "missingPriceBucketCount": audit_run["missing_price_bucket_count"],
                "cacheCreation1hTokens": audit_run["cache_creation_1h_tokens"],
                "source": audit_run["source"],
                "note": audit_run["note"],
            }
            for row in conn.execute(
                """
                select *
                from kbq_true_cost_audit_buckets
                where run_id = ?
                order by
                  case status
                    when 'REAL_LOSS' then 0
                    when 'NO_PRICE' then 1
                    else 2
                  end,
                  case display_status
                    when 'DISPLAY_DRIFT' then 0
                    else 1
                  end,
                  margin asc
                limit 80
                """,
                (audit_run["id"],),
            ):
                audit_buckets.append(
                    {
                        "status": row["status"],
                        "displayStatus": row["display_status"],
                        "accountId": row["account_id"],
                        "accountName": row["account_name"],
                        "channelId": row["channel_id"],
                        "channelName": row["channel_name"],
                        "groupId": row["group_id"],
                        "groupName": row["group_name"],
                        "model": row["model"],
                        "upstreamModel": row["upstream_model"],
                        "requestCount": row["request_count"],
                        "inputTokens": row["input_tokens"],
                        "outputTokens": row["output_tokens"],
                        "cacheReadTokens": row["cache_read_tokens"],
                        "cacheWriteTokens": row["cache_write_tokens"],
                        "cacheCreation1hTokens": row["cache_creation_1h_tokens"],
                        "userBilledCost": number_or_none(row["user_billed_cost"]),
                        "trueUpstreamCost": number_or_none(row["true_upstream_cost"]),
                        "margin": number_or_none(row["margin"]),
                        "displayedAccountCost": number_or_none(row["displayed_account_cost"]),
                        "note": row["note"],
                    }
                )
    adapter_status = []
    browser_status_by_pair = {}
    has_browser_adapter_table = conn.execute(
        "select 1 from sqlite_master where type = 'table' and name = 'browser_adapter_status'"
    ).fetchone()
    if has_browser_adapter_table:
        for row in conn.execute(
            """
            select provider, site, browser, status, detail, observed_at
            from browser_adapter_status
            order by
              case status
                when 'browser_observed' then 0
                when 'browser_observed_empty' then 1
                when 'needs_browser_tab' then 2
                else 3
              end,
              provider,
              site
            """
        ):
            item = {
                "provider": row["provider"],
                "site": row["site"],
                "adapterKind": f"{row['browser']} browser_readonly",
                "status": row["status"],
                "detail": row["detail"],
                "observedAt": format_beijing_time(row["observed_at"]),
                "currentCoverage": browser_status_is_current_coverage(row["status"], row["detail"]),
            }
            browser_status_by_pair[(row["provider"], row["site"])] = item
            adapter_status.append(item)

    priority_plan = []
    has_priority_plan_table = conn.execute(
        "select 1 from sqlite_master where type = 'table' and name = 'account_priority_plan_rows'"
    ).fetchone()
    if has_priority_plan_table:
        latest_run = conn.execute(
            """
            select run_id
            from account_priority_plan_rows
            order by observed_at desc, id desc
            limit 1
            """
        ).fetchone()
        if latest_run:
            for row in conn.execute(
                """
                select *
                from account_priority_plan_rows
                where run_id = ?
                order by target_priority asc, account_name asc, id asc
                """,
                (latest_run["run_id"],),
            ):
                priority_plan.append(
                    {
                        "runId": row["run_id"],
                        "accountId": row["account_id"],
                        "accountName": row["account_name"],
                        "currentPriority": row["current_priority"],
                        "targetPriority": row["target_priority"],
                        "rateMultiplier": row["rate_multiplier"],
                        "bucket": row["bucket"],
                        "groups": row["groups"],
                        "reason": row["reason"],
                        "mode": row["mode"],
                        "observedAt": format_beijing_time(row["observed_at"]),
                    }
                )

    has_adapter_table = conn.execute(
        "select 1 from sqlite_master where type = 'table' and name = 'upstream_adapter_status'"
    ).fetchone()
    if has_adapter_table:
        for row in conn.execute(
            """
            select provider, site, adapter_kind, status, detail, observed_at
            from upstream_adapter_status
            order by
              case status
                when 'failed' then 0
                when 'needs_adapter' then 1
                when 'ok' then 2
                else 3
              end,
              provider,
              site
            """
        ):
            public_status = row["status"]
            public_detail = row["detail"]
            browser_item = browser_status_by_pair.get((row["provider"], row["site"]))
            if (
                public_status == "needs_adapter"
                and browser_item
                and browser_status_is_current_coverage(browser_item["status"], browser_item["detail"])
            ):
                public_status = "covered_by_browser"
                public_detail = (
                    f"{public_detail}; browser read-only adapter has current coverage for this provider: "
                    f"{browser_item['detail']}"
                )
            adapter_status.append(
                {
                    "provider": row["provider"],
                    "site": row["site"],
                    "adapterKind": row["adapter_kind"],
                    "status": public_status,
                    "detail": public_detail,
                    "observedAt": format_beijing_time(row["observed_at"]),
                    "currentCoverage": public_status in ("ok", "covered_by_browser"),
                }
            )
    has_balance_api_adapter_table = conn.execute(
        "select 1 from sqlite_master where type = 'table' and name = 'balance_api_adapter_status'"
    ).fetchone()
    if has_balance_api_adapter_table:
        for row in conn.execute(
            """
            select provider, site, adapter_kind, status, detail, observed_at
            from balance_api_adapter_status
            order by
              case status
                when 'ok' then 0
                when 'unsupported_or_failed' then 1
                else 2
              end,
              provider,
              site
            """
        ):
            adapter_status.append(
                {
                    "provider": row["provider"],
                    "site": row["site"],
                    "adapterKind": row["adapter_kind"],
                    "status": row["status"],
                    "detail": row["detail"],
                    "observedAt": format_beijing_time(row["observed_at"]),
                    "currentCoverage": row["status"] == "ok",
                }
            )
    adapter_status.sort(key=adapter_status_rank)
    conn.close()
    return (
        rows,
        kbq_rows,
        kbq_per_call_rows,
        audit_summary,
        audit_buckets,
        adapter_status,
        metadata,
        priority_plan,
        provider_observations,
        provider_diagnostics,
        balance_snapshots,
    )


def build_dashboard_context(
    rows,
    kbq_rows,
    kbq_per_call_rows,
    audit_summary,
    audit_buckets,
    adapter_status,
    metadata,
    priority_plan,
    provider_observations=None,
    provider_diagnostics=None,
    balance_snapshots=None,
) -> dict[str, str]:
    generated_at = format_beijing_time(datetime.now(BEIJING_TZ))
    return {
        "generated_at": generated_at,
        "data_json": json.dumps(rows, ensure_ascii=False),
        "provider_observations_json": json.dumps(provider_observations or [], ensure_ascii=False),
        "provider_diagnostics_json": json.dumps(provider_diagnostics or [], ensure_ascii=False),
        "balance_snapshots_json": json.dumps(balance_snapshots or [], ensure_ascii=False),
        "kbq_json": json.dumps(kbq_rows, ensure_ascii=False),
        "kbq_per_call_json": json.dumps(kbq_per_call_rows, ensure_ascii=False),
        "audit_json": json.dumps(audit_summary, ensure_ascii=False),
        "audit_buckets_json": json.dumps(audit_buckets, ensure_ascii=False),
        "adapter_status_json": json.dumps(adapter_status, ensure_ascii=False),
        "metadata_json": json.dumps(renderable_metadata(metadata), ensure_ascii=False),
        "priority_plan_json": json.dumps(priority_plan, ensure_ascii=False),
    }


def render_dashboard_document(context: dict[str, str]) -> str:
    generated_at = context["generated_at"]
    data_json = context["data_json"]
    provider_observations_json = context["provider_observations_json"]
    provider_diagnostics_json = context["provider_diagnostics_json"]
    balance_snapshots_json = context["balance_snapshots_json"]
    kbq_json = context["kbq_json"]
    kbq_per_call_json = context["kbq_per_call_json"]
    audit_json = context["audit_json"]
    audit_buckets_json = context["audit_buckets_json"]
    adapter_status_json = context["adapter_status_json"]
    metadata_json = context["metadata_json"]
    priority_plan_json = context["priority_plan_json"]
    return f"""<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <meta name="robots" content="noindex,nofollow" />
  <meta name="theme-color" content="#f6f7f9" />
  <title>Fluter 上游成本倍率台账</title>
  <style>
    :root {{
      color-scheme: light;
      --bg: #eef4f3;
      --panel: #ffffff;
      --panel-soft: #f7fbfb;
      --panel-glass: rgba(255, 255, 255, 0.88);
      --line: #cfdee2;
      --line-strong: #9fb8c0;
      --text: #12202b;
      --muted: #617180;
      --rail: #06131a;
      --rail-soft: #0d2530;
      --rail-muted: #9fb4bd;
      --teal: #0f766e;
      --cyan: #0891b2;
      --blue: #1d4ed8;
      --amber: #b45309;
      --red: #b91c1c;
      --green: #047857;
      --shadow: 0 1px 2px rgba(15, 23, 42, 0.07), 0 12px 34px rgba(15, 23, 42, 0.055);
      --glow: 0 0 0 1px rgba(8, 145, 178, 0.17), 0 16px 42px rgba(15, 23, 42, 0.09);
      --focus: rgba(20, 184, 166, 0.32);
      --circuit: rgba(8, 145, 178, 0.10);
      --angle: linear-gradient(135deg, transparent 0 12px, rgba(8, 145, 178, 0.12) 12px 13px, transparent 13px);
    }}
    * {{ box-sizing: border-box; }}
    html {{ -webkit-tap-highlight-color: rgba(15, 118, 110, 0.16); scroll-behavior: smooth; }}
    body {{
      margin: 0;
      background:
        linear-gradient(rgba(8, 145, 178, 0.055) 1px, transparent 1px),
        linear-gradient(90deg, rgba(8, 145, 178, 0.045) 1px, transparent 1px),
        linear-gradient(135deg, rgba(20, 184, 166, 0.07), transparent 34%),
        linear-gradient(315deg, rgba(180, 83, 9, 0.055), transparent 28%),
        var(--bg);
      background-size: 40px 40px;
      background-attachment: fixed;
      color: var(--text);
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif;
      line-height: 1.55;
    }}
    body::before {{
      content: "";
      position: fixed;
      inset: 0;
      pointer-events: none;
      z-index: -1;
      background:
        linear-gradient(115deg, transparent 0 18%, rgba(8, 145, 178, 0.08) 18.1%, transparent 18.8% 60%, rgba(20, 184, 166, 0.06) 60.1%, transparent 61%),
        repeating-linear-gradient(90deg, transparent 0 96px, rgba(8, 145, 178, 0.045) 96px 97px);
      opacity: 0.72;
    }}
    .skip-link {{
      position: fixed;
      left: 18px;
      top: 12px;
      z-index: 100;
      transform: translateY(-150%);
      background: var(--text);
      color: white;
      border-radius: 8px;
      padding: 9px 12px;
      text-decoration: none;
    }}
    .skip-link:focus-visible {{ transform: translateY(0); outline: 3px solid var(--focus); outline-offset: 2px; }}
    .sr-only {{
      position: absolute;
      width: 1px;
      height: 1px;
      padding: 0;
      margin: -1px;
      overflow: hidden;
      clip: rect(0, 0, 0, 0);
      white-space: nowrap;
      border: 0;
    }}
    .app-shell {{
      display: grid;
      grid-template-columns: 268px minmax(0, 1fr);
      min-height: 100vh;
    }}
    .side-nav {{
      position: sticky;
      top: 0;
      height: 100vh;
      background:
        linear-gradient(180deg, rgba(20, 184, 166, 0.16), rgba(20, 184, 166, 0) 260px),
        linear-gradient(135deg, rgba(125, 211, 252, 0.10), transparent 44%),
        repeating-linear-gradient(180deg, transparent 0 42px, rgba(255, 255, 255, 0.028) 42px 43px),
        var(--rail);
      color: white;
      padding: 20px 16px;
      overflow-y: auto;
      border-right: 1px solid rgba(255, 255, 255, 0.08);
      box-shadow: inset -1px 0 0 rgba(45, 212, 191, 0.12), 10px 0 34px rgba(6, 19, 26, 0.13);
    }}
    .brand {{
      display: grid;
      grid-template-columns: 40px minmax(0, 1fr);
      gap: 10px;
      align-items: center;
      margin-bottom: 22px;
    }}
    .brand-mark {{
      display: grid;
      place-items: center;
      width: 40px;
      height: 40px;
      border: 1px solid rgba(125, 211, 252, 0.36);
      border-radius: 8px;
      background:
        linear-gradient(135deg, rgba(45, 212, 191, 0.16), transparent 54%),
        #0b2633;
      box-shadow: inset 0 0 18px rgba(45, 212, 191, 0.18), 0 0 22px rgba(8, 145, 178, 0.16);
      font-weight: 780;
      letter-spacing: 0;
    }}
    .brand-title {{ font-size: 15px; font-weight: 760; line-height: 1.25; }}
    .brand-sub {{ color: var(--rail-muted); font-size: 12px; margin-top: 2px; }}
    .nav-group {{ margin-top: 18px; }}
    .nav-label {{
      color: var(--rail-muted);
      font-size: 11px;
      font-weight: 720;
      letter-spacing: 0.08em;
      text-transform: uppercase;
      margin: 0 8px 8px;
    }}
    .nav-link, .category-nav-item {{
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 10px;
      width: 100%;
      min-height: 38px;
      border: 1px solid transparent;
      border-radius: 8px;
      padding: 8px 10px;
      color: #e8eef3;
      background: transparent;
      text-decoration: none;
      font-size: 13px;
      font-weight: 650;
      cursor: pointer;
      touch-action: manipulation;
      position: relative;
    }}
    .nav-link:hover, .category-nav-item:hover, .nav-link:focus-visible, .category-nav-item:focus-visible, .nav-link.active {{
      background:
        linear-gradient(90deg, rgba(45, 212, 191, 0.14), rgba(14, 165, 233, 0.05)),
        var(--rail-soft);
      border-color: rgba(125, 211, 252, 0.16);
      color: white;
    }}
    .nav-link.active, .category-nav-item.active {{
      border-left-color: rgba(45, 212, 191, 0.85);
      box-shadow: inset 2px 0 0 rgba(45, 212, 191, 0.85), inset 0 0 22px rgba(45, 212, 191, 0.07);
    }}
    .nav-link span:last-child, .category-nav-item span:last-child {{
      color: var(--rail-muted);
      font-size: 12px;
      font-weight: 620;
    }}
    .category-nav-item.active {{ background: rgba(15, 118, 110, 0.34); border-color: rgba(45, 212, 191, 0.34); color: white; }}
    .side-note {{
      margin-top: 18px;
      border: 1px solid rgba(255, 255, 255, 0.1);
      border-radius: 8px;
      padding: 12px;
      background:
        linear-gradient(135deg, rgba(45, 212, 191, 0.10), rgba(255, 255, 255, 0.03)),
        rgba(255, 255, 255, 0.04);
      color: var(--rail-muted);
      font-size: 12px;
    }}
    .content-shell {{
      min-width: 0;
      padding: 22px 28px 32px;
    }}
    .module-view[hidden] {{ display: none; }}
    .priority-plan-command {{
      white-space: pre-wrap;
      word-break: break-all;
    }}
    .console-hero {{
      scroll-margin-top: 20px;
      border: 1px solid rgba(8, 145, 178, 0.22);
      border-radius: 8px;
      background:
        linear-gradient(135deg, rgba(8, 145, 178, 0.10), transparent 36%),
        linear-gradient(315deg, rgba(180, 83, 9, 0.08), transparent 28%),
        var(--panel-glass);
      box-shadow: var(--glow);
      padding: 18px;
      position: relative;
      overflow: hidden;
      backdrop-filter: blur(10px);
    }}
    .console-hero::before {{
      content: "";
      position: absolute;
      inset: 0 0 auto 0;
      height: 3px;
      background: linear-gradient(90deg, var(--teal), var(--cyan), var(--amber));
      opacity: 0.86;
      pointer-events: none;
    }}
    .console-hero::after {{
      content: "";
      position: absolute;
      right: -44px;
      bottom: -64px;
      width: 220px;
      height: 180px;
      pointer-events: none;
      background:
        linear-gradient(135deg, transparent 0 36%, rgba(8, 145, 178, 0.12) 36.3% 36.9%, transparent 37.2%),
        linear-gradient(45deg, transparent 0 50%, rgba(20, 184, 166, 0.10) 50.2% 50.8%, transparent 51%),
        repeating-linear-gradient(90deg, transparent 0 18px, rgba(8, 145, 178, 0.08) 18px 19px);
      opacity: 0.72;
    }}
    .topbar {{ display: flex; align-items: flex-start; justify-content: space-between; gap: 18px; }}
    .topbar > div:first-child {{ min-width: 0; }}
    h1 {{ margin: 0; font-size: 21px; letter-spacing: 0; text-wrap: balance; }}
    .sub {{ color: var(--muted); font-size: 13px; margin-top: 4px; }}
    .route-sub {{
      color: var(--muted);
      font-size: 13px;
      margin-top: 4px;
      max-width: 760px;
    }}
    .actions {{ display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }}
    .pill {{
      border: 1px solid var(--line);
      background: rgba(255, 255, 255, 0.82);
      border-radius: 999px;
      padding: 7px 12px;
      color: var(--muted);
      font-size: 13px;
      white-space: nowrap;
      box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.72);
    }}
    .mobile-nav {{
      display: none;
      gap: 8px;
      margin-top: 14px;
      overflow-x: auto;
      padding-bottom: 2px;
    }}
    .mobile-nav a {{
      border: 1px solid var(--line);
      background: var(--panel-soft);
      color: var(--text);
      border-radius: 8px;
      padding: 8px 10px;
      text-decoration: none;
      font-size: 13px;
      font-weight: 680;
      white-space: nowrap;
    }}
    .mobile-nav a.active {{ background: var(--teal); border-color: var(--teal); color: white; }}
    .action-button {{
      border: 1px solid var(--line);
      background:
        linear-gradient(180deg, rgba(255, 255, 255, 0.96), rgba(247, 251, 251, 0.96));
      color: var(--text);
      border-radius: 8px;
      padding: 8px 12px;
      font-size: 13px;
      font-weight: 680;
      cursor: pointer;
      touch-action: manipulation;
      box-shadow: 0 1px 0 rgba(255, 255, 255, 0.72);
    }}
    .action-button:hover {{ border-color: var(--teal); background: #edfafa; box-shadow: 0 0 0 3px rgba(20, 184, 166, 0.10); }}
    .action-button:active, .category-card:active, .bucket-card:active, .segmented button:active {{ transform: translateY(1px); }}
    :where(button, input, select, a):focus-visible {{
      outline: 3px solid var(--focus);
      outline-offset: 2px;
    }}
    .section-block {{
      scroll-margin-top: 24px;
      margin-top: 18px;
    }}
    .module-head {{
      display: flex;
      align-items: end;
      justify-content: space-between;
      gap: 12px;
      margin-bottom: 12px;
    }}
    .module-head h2 {{ margin: 0; font-size: 18px; }}
    .freshness-strip {{
      display: grid;
      grid-template-columns: repeat(3, minmax(0, 1fr));
      gap: 10px;
      margin-top: 14px;
    }}
    .freshness-item {{
      border: 1px solid var(--line);
      border-radius: 8px;
      background: #fbfdfd;
      padding: 10px 12px;
      min-height: 72px;
    }}
    .freshness-item span {{
      display: block;
      color: var(--muted);
      font-size: 12px;
      font-weight: 700;
    }}
    .freshness-item strong {{
      display: block;
      margin-top: 4px;
      font-size: 14px;
      overflow-wrap: anywhere;
    }}
    .section-kicker {{
      color: var(--muted);
      font-size: 11px;
      font-weight: 760;
      letter-spacing: 0.08em;
      text-transform: uppercase;
      margin: 18px 0 8px;
    }}
    .section-panel {{
      background: rgba(255, 255, 255, 0.94);
      border: 1px solid var(--line);
      border-radius: 8px;
      box-shadow: var(--shadow);
      padding: 14px;
    }}
    .health-grid {{
      display: grid;
      grid-template-columns: repeat(4, minmax(0, 1fr));
      gap: 10px;
      margin-top: 16px;
    }}
    .health-card {{
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 12px;
      border: 1px solid var(--line);
      border-left: 4px solid var(--green);
      border-radius: 8px;
      background: var(--panel-soft);
      padding: 11px 12px;
      min-height: 74px;
      color: var(--text);
      text-align: left;
      cursor: pointer;
      touch-action: manipulation;
    }}
    .health-card:hover {{ border-color: var(--teal); border-left-color: var(--teal); background: #f0f8f7; }}
    .health-card:active {{ transform: translateY(1px); }}
    .health-card strong {{ display: block; font-size: 24px; line-height: 1; font-variant-numeric: tabular-nums; }}
    .health-label {{ display: block; color: var(--text); font-size: 13px; font-weight: 720; margin-top: 7px; }}
    .health-hint {{ display: block; color: var(--muted); font-size: 12px; margin-top: 1px; }}
    .health-dot {{
      display: none;
    }}
    .health-card.warn {{ border-left-color: var(--amber); }}
    .health-card.risk {{ border-left-color: var(--red); }}
    .overview-command-center {{
      display: grid;
      grid-template-columns: minmax(0, 1.25fr) minmax(280px, 0.75fr);
      gap: 12px;
      margin-top: 14px;
    }}
    .overview-panel {{
      border: 1px solid var(--line);
      border-radius: 8px;
      background: rgba(255, 255, 255, 0.96);
      padding: 12px;
      min-width: 0;
      box-shadow: var(--shadow);
    }}
    .overview-panel-head {{
      display: flex;
      justify-content: space-between;
      gap: 10px;
      align-items: start;
      margin-bottom: 10px;
    }}
    .overview-panel h3 {{
      margin: 0;
      font-size: 15px;
      text-wrap: balance;
    }}
    .overview-list, .overview-source-list {{
      display: grid;
      gap: 8px;
    }}
    .overview-item {{
      display: grid;
      gap: 4px;
      width: 100%;
      min-height: 64px;
      border: 1px solid var(--line);
      border-left: 4px solid var(--green);
      border-radius: 8px;
      background: var(--panel-soft);
      padding: 9px 10px;
      color: var(--text);
      text-align: left;
      touch-action: manipulation;
    }}
    button.overview-item {{
      cursor: pointer;
    }}
    button.overview-item:hover {{
      border-color: var(--teal);
      box-shadow: 0 0 0 3px rgba(20, 184, 166, 0.10);
    }}
    .overview-item.warn {{ border-left-color: var(--amber); background: #fffbeb; }}
    .overview-item.risk {{ border-left-color: var(--red); background: #fef2f2; }}
    .overview-item.info {{ border-left-color: var(--blue); background: #eff6ff; }}
    .overview-item strong {{
      min-width: 0;
      font-size: 13px;
      overflow-wrap: anywhere;
    }}
    .overview-item span {{
      min-width: 0;
      color: var(--muted);
      font-size: 12px;
      line-height: 1.45;
      overflow-wrap: anywhere;
    }}
    .grid {{
      display: grid;
      grid-template-columns: repeat(5, minmax(0, 1fr));
      gap: 14px;
      margin-top: 18px;
    }}
    .card {{
      background: var(--panel);
      border: 1px solid var(--line);
      border-radius: 8px;
      box-shadow: var(--shadow);
      padding: 16px;
      min-height: 106px;
    }}
    .card .label {{ color: var(--muted); font-size: 13px; }}
    .card .value {{ font-size: 27px; font-weight: 760; margin-top: 8px; font-variant-numeric: tabular-nums; }}
    .card .hint {{ color: var(--muted); font-size: 12px; margin-top: 4px; }}
    .filter-panel {{ margin-top: 14px; }}
    .toolbar {{
      display: grid;
      grid-template-columns: 1.4fr repeat(3, minmax(140px, 0.4fr));
      gap: 10px;
    }}
    input, select {{
      width: 100%;
      border: 1px solid var(--line);
      border-radius: 8px;
      background: var(--panel);
      color: var(--text);
      padding: 11px 12px;
      font-size: 14px;
    }}
    input:focus, select:focus {{ border-color: var(--teal); box-shadow: 0 0 0 3px rgba(15, 118, 110, 0.14); }}
    .quick-filters {{
      display: flex;
      flex-wrap: wrap;
      gap: 8px;
      margin-top: 10px;
    }}
    #quickFilters .action-button {{
      border-radius: 999px;
      padding: 6px 13px;
      background: var(--panel-soft);
      color: var(--muted);
      font-size: 12px;
      font-weight: 720;
    }}
    #quickFilters .action-button:hover {{ color: var(--text); }}
    .quick-filters button.active {{ background: var(--teal); color: white; border-color: var(--teal); }}
    .section-title {{ display: flex; justify-content: space-between; align-items: end; gap: 12px; margin: 18px 0 10px; }}
    .section-panel > .section-title:first-child, .console-hero .section-title:first-child {{ margin-top: 0; }}
    h2 {{ margin: 0; font-size: 17px; }}
    .summary-line {{ color: var(--muted); font-size: 13px; }}
    .category-grid {{ display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 12px; }}
    .category-card {{
      background: var(--panel);
      border: 1px solid var(--line);
      border-radius: 8px;
      padding: 14px;
      cursor: pointer;
      text-align: left;
      touch-action: manipulation;
    }}
    .category-card:hover, .category-card.active {{ border-color: var(--teal); background: var(--panel-soft); }}
    .category-card strong {{ display: block; font-size: 15px; margin-bottom: 8px; }}
    .category-card span {{ color: var(--muted); font-size: 13px; }}
    .cat-meta {{
      display: flex;
      flex-wrap: wrap;
      gap: 6px;
      margin-top: 8px;
    }}
    .cat-meta span {{
      border: 1px solid var(--line);
      border-radius: 999px;
      background: var(--panel-soft);
      padding: 3px 8px;
      color: var(--muted);
      font-size: 12px;
      font-weight: 650;
      white-space: nowrap;
    }}
    .cat-meta span.risk-chip {{ color: var(--red); background: #fef2f2; border-color: #fecaca; }}
    .cat-meta span.ok-chip {{ color: var(--green); background: #ecfdf5; border-color: #bbf7d0; }}
    .discount-meta {{
      display: flex;
      flex-wrap: wrap;
      gap: 5px;
      align-items: center;
    }}
    .discount-pill {{
      display: inline-flex;
      align-items: center;
      max-width: 100%;
      border: 1px solid var(--line);
      border-radius: 999px;
      padding: 2px 7px;
      background: var(--panel-soft);
      color: var(--muted);
      font-size: 11px;
      font-weight: 680;
      line-height: 1.4;
      white-space: nowrap;
    }}
    .discount-pill.ok {{ color: var(--green); background: #ecfdf5; border-color: #bbf7d0; }}
    .discount-pill.warn {{ color: #92400e; background: #fffbeb; border-color: #fde68a; }}
    .category-detail[hidden] {{ display: none; }}
    .balance-strip {{
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(190px, 1fr));
      gap: 10px;
    }}
    .balance-chip {{
      background: var(--panel);
      border: 1px solid var(--line);
      border-radius: 8px;
      padding: 10px 12px;
      min-height: 72px;
    }}
    .balance-chip.balance-ok {{ border-color: #bbf7d0; background: #ecfdf5; }}
    .balance-chip.balance-watch {{ border-color: #fde68a; background: #fffbeb; }}
    .balance-chip.balance-low {{ border-color: #fecaca; background: #fef2f2; }}
    .balance-chip strong {{
      display: block;
      color: var(--muted);
      font-size: 11px;
      font-weight: 720;
      letter-spacing: 0.06em;
      text-transform: uppercase;
      margin-bottom: 3px;
    }}
    .balance-chip .balance-value {{ font-size: 22px; font-weight: 780; font-variant-numeric: tabular-nums; }}
    .kbq-wrap {{
      background: rgba(255, 255, 255, 0.94);
      border: 1px solid var(--line);
      border-radius: 8px;
      box-shadow: var(--shadow);
      padding: 14px;
    }}
    .site-matrix {{
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
      gap: 10px;
      margin-top: 10px;
    }}
    .site-node {{
      display: grid;
      gap: 8px;
      border: 1px solid var(--line);
      border-left: 4px solid var(--teal);
      border-radius: 8px;
      background: rgba(255, 255, 255, 0.96);
      padding: 12px;
      text-align: left;
      color: var(--text);
      cursor: pointer;
      touch-action: manipulation;
      min-height: 132px;
    }}
    .site-node:hover {{ border-color: var(--teal); box-shadow: var(--glow); }}
    .site-node:active {{ transform: translateY(1px); }}
    .site-node.warn {{ border-left-color: var(--amber); }}
    .site-node.risk {{ border-left-color: var(--red); }}
    .site-node-head {{
      display: flex;
      justify-content: space-between;
      gap: 10px;
      align-items: start;
    }}
    .site-node strong {{
      display: block;
      min-width: 0;
      font-size: 14px;
      overflow-wrap: anywhere;
    }}
    .site-node small {{
      color: var(--muted);
      font-size: 12px;
      overflow-wrap: anywhere;
    }}
    .site-node-metrics {{
      display: grid;
      grid-template-columns: repeat(3, minmax(0, 1fr));
      gap: 6px;
    }}
    .site-node-metrics span {{
      border: 1px solid var(--line);
      border-radius: 8px;
      background: var(--panel-soft);
      color: var(--muted);
      padding: 5px 6px;
      font-size: 12px;
      font-variant-numeric: tabular-nums;
      overflow-wrap: anywhere;
    }}
    .site-node-foot {{
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 8px;
      flex-wrap: wrap;
      color: var(--muted);
      font-size: 12px;
      overflow-wrap: anywhere;
    }}
    .site-node-foot-note {{
      min-width: 0;
      overflow-wrap: anywhere;
    }}
    .provider-toolbar {{
      display: grid;
      grid-template-columns: minmax(240px, 1fr) auto;
      gap: 10px;
      align-items: center;
      margin-bottom: 12px;
    }}
    .provider-notices {{
      display: grid;
      gap: 8px;
      margin: 0 0 12px;
    }}
    .provider-diagnostics {{
      border: 1px solid var(--line);
      border-radius: 8px;
      background: var(--panel);
      margin: 0 0 12px;
      overflow: hidden;
    }}
    .provider-diagnostics details {{
      display: block;
    }}
    .provider-diagnostics summary {{
      cursor: pointer;
      list-style: none;
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 12px;
      padding: 12px 14px;
      color: var(--text);
      font-size: 13px;
      font-weight: 700;
      letter-spacing: 0;
    }}
    .provider-diagnostics summary::-webkit-details-marker {{
      display: none;
    }}
    .provider-diagnostics-summary-note {{
      color: var(--muted);
      font-size: 12px;
      font-weight: 500;
    }}
    .provider-diagnostics-grid {{
      border-top: 1px solid var(--line);
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
      gap: 10px;
      padding: 12px;
    }}
    .provider-diagnostic {{
      border: 1px solid var(--line);
      border-radius: 8px;
      background: var(--panel-soft);
      padding: 10px;
      display: grid;
      gap: 7px;
      min-width: 0;
    }}
    .provider-diagnostic.warn {{
      border-color: #fde68a;
      background: #fffbeb;
    }}
    .provider-diagnostic.risk {{
      border-color: #fecaca;
      background: #fff1f2;
    }}
    .provider-diagnostic-head {{
      display: flex;
      align-items: flex-start;
      justify-content: space-between;
      gap: 8px;
    }}
    .provider-diagnostic strong {{
      color: var(--text);
      font-size: 14px;
      overflow-wrap: anywhere;
    }}
    .provider-diagnostic small,
    .provider-diagnostic div {{
      color: var(--muted);
      font-size: 12px;
      line-height: 1.45;
      overflow-wrap: anywhere;
    }}
    .provider-notice {{
      border: 1px solid #fde68a;
      border-left: 4px solid var(--amber);
      border-radius: 8px;
      background: #fffbeb;
      color: #92400e;
      padding: 9px 11px;
      font-size: 13px;
      line-height: 1.5;
      overflow-wrap: anywhere;
    }}
    .provider-notice strong {{
      color: #78350f;
      margin-right: 6px;
    }}
    .freshness-pill {{
      display: inline-flex;
      align-items: center;
      justify-content: center;
      border: 1px solid transparent;
      border-radius: 999px;
      padding: 4px 8px;
      font-size: 12px;
      font-weight: 720;
      font-variant-numeric: tabular-nums;
      white-space: nowrap;
    }}
    .freshness-pill.ok {{ color: var(--green); background: #ecfdf5; border-color: #bbf7d0; }}
    .freshness-pill.warn {{ color: var(--amber); background: #fffbeb; border-color: #fde68a; }}
    .freshness-pill.risk {{ color: var(--red); background: #fef2f2; border-color: #fecaca; }}
    .inspector-age {{
      display: flex;
      align-items: center;
      justify-content: flex-start;
      gap: 6px;
      margin-top: 2px;
    }}
    .account-inspector {{
      background: rgba(255, 255, 255, 0.97);
      border: 1px solid rgba(8, 145, 178, 0.24);
      border-radius: 8px;
      box-shadow: var(--glow);
      padding: 14px;
    }}
    .inspector-grid {{
      display: grid;
      gap: 10px;
      margin-top: 12px;
    }}
    .inspector-row {{
      display: grid;
      gap: 4px;
      border: 1px solid var(--line);
      border-radius: 8px;
      background: var(--panel-soft);
      padding: 10px;
    }}
    .inspector-row span {{
      color: var(--muted);
      font-size: 11px;
      font-weight: 760;
      letter-spacing: 0.06em;
      text-transform: uppercase;
    }}
    .inspector-row strong, .inspector-row div {{
      min-width: 0;
      overflow-wrap: anywhere;
      font-size: 13px;
    }}
    .kbq-toolbar {{
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 12px;
      flex-wrap: wrap;
      margin-bottom: 12px;
    }}
    .segmented {{
      display: inline-flex;
      border: 1px solid var(--line);
      border-radius: 8px;
      overflow: hidden;
      background: #f8fafc;
    }}
    .segmented button {{
      border: 0;
      background: transparent;
      padding: 9px 12px;
      cursor: pointer;
      color: var(--muted);
      font-weight: 680;
    }}
    .segmented button.active {{ background: var(--teal); color: white; }}
    .kbq-mode-tabs {{ margin-bottom: 12px; }}
    .kbq-tab-panel[hidden] {{ display: none; }}
    .bucket-grid {{
      display: grid;
      grid-template-columns: repeat(6, minmax(0, 1fr));
      gap: 10px;
      margin-bottom: 12px;
    }}
    .bucket-card {{
      border: 1px solid var(--line);
      border-radius: 8px;
      background: #fbfdfd;
      padding: 10px;
      cursor: pointer;
      min-height: 76px;
      text-align: left;
      touch-action: manipulation;
    }}
    .bucket-card:hover, .bucket-card.active {{ border-color: var(--teal); background: var(--panel-soft); }}
    .bucket-card strong {{ display: block; font-size: 18px; }}
    .bucket-card span {{ color: var(--muted); font-size: 12px; }}
    .kbq-note {{
      background: #fff7ed;
      border: 1px solid #fed7aa;
      color: #7c2d12;
      border-radius: 8px;
      padding: 10px 12px;
      font-size: 13px;
      margin-bottom: 12px;
    }}
    .assistant-panel {{
      background: var(--panel);
      border: 1px solid var(--line);
      border-radius: 8px;
      box-shadow: var(--shadow);
      padding: 14px;
    }}
    .assistant-form {{
      display: grid;
      grid-template-columns: 1fr auto;
      gap: 10px;
      margin-top: 10px;
    }}
    .assistant-answer {{
      border: 1px solid var(--line);
      border-radius: 8px;
      background: #fbfdfd;
      min-height: 86px;
      padding: 12px;
      margin-top: 10px;
      white-space: pre-wrap;
      font-size: 13px;
    }}
    .assistant-examples {{
      display: flex;
      flex-wrap: wrap;
      gap: 8px;
      margin-top: 10px;
    }}
    .assistant-examples button {{
      border: 1px solid var(--line);
      background: #f8fafc;
      color: var(--text);
      border-radius: 999px;
      padding: 7px 10px;
      font-size: 12px;
      cursor: pointer;
    }}
    .assistant-examples button:hover {{ border-color: var(--teal); background: var(--panel-soft); }}
    .audit-panel {{
      background: var(--panel);
      border: 1px solid var(--line);
      border-radius: 8px;
      box-shadow: var(--shadow);
      padding: 14px;
    }}
    .adapter-grid {{
      display: grid;
      grid-template-columns: repeat(3, minmax(0, 1fr));
      gap: 10px;
    }}
    .adapter-chip {{
      border: 1px solid var(--line);
      border-radius: 8px;
      background: #fbfdfd;
      padding: 12px;
      min-height: 112px;
    }}
    .adapter-head {{ display: flex; align-items: center; justify-content: space-between; gap: 8px; margin-bottom: 6px; }}
    .adapter-head strong {{ display: block; min-width: 0; font-size: 14px; }}
    .adapter-chip.ok {{ background: #ecfdf5; border-color: #bbf7d0; }}
    .adapter-chip.needs {{ background: #eff6ff; border-color: #bfdbfe; }}
    .adapter-chip.failed {{ background: #fef2f2; border-color: #fecaca; }}
    .plan-grid {{
      display: grid;
      grid-template-columns: repeat(4, minmax(0, 1fr));
      gap: 10px;
      margin-top: 12px;
    }}
    .plan-card {{
      border: 1px solid var(--line);
      border-radius: 8px;
      background: #fbfdfd;
      padding: 12px;
      min-height: 104px;
    }}
    .plan-card span {{
      display: block;
      color: var(--muted);
      font-size: 12px;
      font-weight: 700;
    }}
    .plan-card strong {{
      display: block;
      margin-top: 6px;
      font-size: 20px;
      font-variant-numeric: tabular-nums;
      overflow-wrap: anywhere;
    }}
    .plan-card small {{
      display: block;
      margin-top: 6px;
      color: var(--muted);
      font-size: 12px;
      overflow-wrap: anywhere;
    }}
    .plan-command {{
      margin-top: 12px;
      border: 1px solid var(--line);
      border-radius: 8px;
      background: #0f172a;
      color: #e2e8f0;
      padding: 12px;
      overflow-x: auto;
      font: 12px/1.5 ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", monospace;
      white-space: pre-wrap;
      overflow-wrap: anywhere;
    }}
    .plan-table-wrap {{ margin-top: 12px; }}
    .plan-table {{ min-width: 1360px; }}
    .plan-table th:nth-child(1), .plan-table td:nth-child(1) {{ width: 96px; }}
    .plan-table th:nth-child(2), .plan-table td:nth-child(2) {{ width: 240px; }}
    .plan-table th:nth-child(3), .plan-table td:nth-child(3) {{ width: 90px; }}
    .plan-table th:nth-child(4), .plan-table td:nth-child(4) {{ width: 90px; }}
    .plan-table th:nth-child(5), .plan-table td:nth-child(5) {{ width: 110px; }}
    .plan-table th:nth-child(6), .plan-table td:nth-child(6) {{ width: 180px; }}
    .plan-table th:nth-child(7), .plan-table td:nth-child(7) {{ width: 220px; }}
    .plan-table th:nth-child(8), .plan-table td:nth-child(8) {{ width: 260px; }}
    .plan-table th:nth-child(9), .plan-table td:nth-child(9) {{ width: 96px; }}
    .server-grid {{
      display: grid;
      grid-template-columns: repeat(4, minmax(0, 1fr));
      gap: 10px;
      margin-top: 12px;
    }}
    .server-metric-card {{
      border: 1px solid var(--line);
      border-left: 4px solid var(--green);
      border-radius: 8px;
      background: #fbfdfd;
      padding: 12px;
      min-height: 102px;
    }}
    .server-metric-card.warn {{ border-left-color: var(--amber); background: #fffbeb; }}
    .server-metric-card.risk {{ border-left-color: var(--red); background: #fef2f2; }}
    .server-metric-card span {{ display: block; color: var(--muted); font-size: 12px; font-weight: 700; }}
    .server-metric-card strong {{ display: block; margin-top: 6px; font-size: 24px; line-height: 1.1; font-variant-numeric: tabular-nums; overflow-wrap: anywhere; }}
    .server-metric-card small {{ display: block; margin-top: 6px; color: var(--muted); font-size: 12px; }}
    .server-split {{
      display: grid;
      grid-template-columns: minmax(0, 0.9fr) minmax(0, 1.1fr);
      gap: 12px;
      margin-top: 12px;
    }}
    .server-sparkline {{
      width: 100%;
      height: 118px;
      border: 1px solid var(--line);
      border-radius: 8px;
      background: #fbfdfd;
      padding: 10px;
    }}
    .server-sparkline svg {{ display: block; width: 100%; height: 100%; overflow: visible; }}
    .container-list {{
      display: grid;
      gap: 8px;
      margin-top: 10px;
      max-height: 360px;
      overflow: auto;
    }}
    .container-row {{
      display: grid;
      grid-template-columns: minmax(0, 0.8fr) minmax(0, 1.2fr) auto;
      gap: 10px;
      align-items: center;
      border: 1px solid var(--line);
      border-radius: 8px;
      background: #fbfdfd;
      padding: 9px 10px;
      font-size: 13px;
    }}
    .container-row strong, .container-row span {{ overflow-wrap: anywhere; }}
    .audit-summary {{
      display: grid;
      grid-template-columns: repeat(6, minmax(0, 1fr));
      gap: 10px;
      margin-bottom: 12px;
    }}
    .audit-metric {{
      border: 1px solid var(--line);
      border-radius: 8px;
      background: #fbfdfd;
      padding: 12px;
      min-height: 82px;
    }}
    .audit-metric span {{ display: block; color: var(--muted); font-size: 12px; }}
    .audit-metric strong {{ display: block; font-size: 20px; margin-top: 4px; }}
    .audit-metric.risk {{ background: #fef2f2; border-color: #fecaca; }}
    .audit-metric.ok {{ background: #ecfdf5; border-color: #bbf7d0; }}
    .table-wrap {{
      background: var(--panel);
      border: 1px solid var(--line);
      border-radius: 8px;
      box-shadow: var(--shadow);
      overflow: auto;
    }}
    caption {{
      color: var(--muted);
      font-size: 12px;
      padding: 10px 12px;
      text-align: left;
    }}
    table {{ width: 100%; border-collapse: separate; border-spacing: 0; min-width: 1180px; table-layout: fixed; }}
    th, td {{ padding: 8px 10px; border-bottom: 1px solid var(--line); vertical-align: top; text-align: left; }}
    th {{ position: sticky; top: 0; z-index: 5; background: var(--panel-soft); color: #264653; font-size: 12px; border-bottom: 0; box-shadow: 0 1px 0 var(--line); }}
    td {{ font-size: 13px; line-height: 1.38; }}
    tr:hover td {{ background: #fbfdfd; }}
    .accounts-table {{ min-width: 1740px; }}
    .kbq-table {{ min-width: 1420px; }}
    .kbq-per-call-table {{ min-width: 1380px; }}
    .audit-table {{ min-width: 1540px; }}
    .image-table {{ min-width: 1260px; }}
    .provider-table {{ min-width: 1320px; }}
    .accounts-table th:nth-child(1), .accounts-table td:nth-child(1) {{ width: 132px; }}
    .accounts-table th:nth-child(2), .accounts-table td:nth-child(2) {{ width: 220px; }}
    .accounts-table th:nth-child(3), .accounts-table td:nth-child(3) {{ width: 150px; }}
    .accounts-table th:nth-child(4), .accounts-table td:nth-child(4) {{ width: 106px; }}
    .accounts-table th:nth-child(5), .accounts-table td:nth-child(5) {{ width: 170px; }}
    .accounts-table th:nth-child(6), .accounts-table td:nth-child(6) {{ width: 96px; }}
    .accounts-table th:nth-child(7), .accounts-table td:nth-child(7) {{ width: 122px; }}
    .accounts-table th:nth-child(8), .accounts-table td:nth-child(8) {{ width: 154px; }}
    .accounts-table th:nth-child(9), .accounts-table td:nth-child(9) {{ width: 124px; }}
    .accounts-table th:nth-child(10), .accounts-table td:nth-child(10) {{ width: 126px; }}
    .accounts-table th:nth-child(11), .accounts-table td:nth-child(11) {{ width: 178px; }}
    .accounts-table th:nth-child(12), .accounts-table td:nth-child(12) {{ width: 108px; }}
    .accounts-table th:nth-child(13), .accounts-table td:nth-child(13) {{ width: 280px; }}
    .kbq-table th:nth-child(1), .kbq-table td:nth-child(1),
    .kbq-per-call-table th:nth-child(1), .kbq-per-call-table td:nth-child(1) {{ width: 112px; }}
    .kbq-table th:nth-child(2), .kbq-table td:nth-child(2) {{ width: 300px; }}
    .kbq-per-call-table th:nth-child(2), .kbq-per-call-table td:nth-child(2) {{ width: 320px; }}
    .kbq-table th:nth-child(7), .kbq-table td:nth-child(7),
    .kbq-per-call-table th:nth-child(7), .kbq-per-call-table td:nth-child(7) {{ width: 220px; }}
    .audit-table th:nth-child(2), .audit-table td:nth-child(2) {{ width: 270px; }}
    .audit-table th:nth-child(3), .audit-table td:nth-child(3) {{ width: 250px; }}
    .audit-table th:nth-child(10), .audit-table td:nth-child(10) {{ width: 280px; }}
    .image-table th:nth-child(2), .image-table td:nth-child(2) {{ width: 260px; }}
    .image-table th:nth-child(8), .image-table td:nth-child(8) {{ width: 320px; }}
    .provider-table th:nth-child(1), .provider-table td:nth-child(1) {{ width: 150px; }}
    .provider-table th:nth-child(2), .provider-table td:nth-child(2) {{ width: 170px; }}
    .provider-table th:nth-child(3), .provider-table td:nth-child(3) {{ width: 260px; }}
    .provider-table th:nth-child(4), .provider-table td:nth-child(4) {{ width: 220px; }}
    .provider-table th:nth-child(8), .provider-table td:nth-child(8) {{ width: 280px; }}
    td, .category-card span, .bucket-card span {{ overflow-wrap: anywhere; }}
    .name {{ font-weight: 680; }}
    .table-wrap .name, .table-wrap .muted, .text-clip {{
      display: -webkit-box;
      -webkit-box-orient: vertical;
      overflow: hidden;
      text-overflow: ellipsis;
      overflow-wrap: anywhere;
    }}
    .table-wrap .name, .text-clip.one {{ -webkit-line-clamp: 1; }}
    .text-clip.two {{ -webkit-line-clamp: 2; }}
    .text-clip.three {{ -webkit-line-clamp: 3; }}
    .table-wrap .muted {{ -webkit-line-clamp: 1; font-size: 12px; }}
    .cell-stack {{
      display: grid;
      gap: 2px;
      min-width: 0;
    }}
    .detail-cell {{
      display: grid;
      grid-template-columns: minmax(0, 1fr) auto;
      align-items: start;
      gap: 8px;
      min-width: 0;
    }}
    .detail-button {{
      border: 1px solid var(--line);
      border-radius: 999px;
      background: var(--panel-soft);
      color: var(--muted);
      cursor: pointer;
      font-size: 12px;
      font-weight: 700;
      line-height: 1;
      padding: 5px 7px;
      touch-action: manipulation;
      white-space: nowrap;
    }}
    .detail-button:hover {{ border-color: var(--teal); color: var(--teal); background: #eefcf9; }}
    .inspect-mini {{
      display: inline-flex;
      align-items: center;
      justify-content: center;
      margin-top: 6px;
      border: 1px solid var(--line);
      border-radius: 999px;
      background: var(--panel);
      color: var(--muted);
      cursor: pointer;
      font-size: 12px;
      font-weight: 720;
      padding: 4px 8px;
      touch-action: manipulation;
      white-space: nowrap;
    }}
    .inspect-mini:hover {{ border-color: var(--teal); color: var(--teal); background: #eefcf9; }}
    .detail-dialog {{
      width: min(760px, calc(100vw - 32px));
      max-height: min(720px, calc(100vh - 32px));
      border: 1px solid var(--line);
      border-radius: 8px;
      padding: 0;
      box-shadow: 0 18px 60px rgba(15, 23, 42, 0.22);
    }}
    .detail-dialog::backdrop {{ background: rgba(15, 23, 42, 0.34); }}
    .detail-dialog form {{
      display: grid;
      grid-template-rows: auto minmax(0, 1fr) auto;
      max-height: min(720px, calc(100vh - 32px));
    }}
    .dialog-head, .dialog-actions {{
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 10px;
      padding: 12px 14px;
      border-bottom: 1px solid var(--line);
      background: var(--panel-soft);
    }}
    .dialog-actions {{ border-bottom: 0; border-top: 1px solid var(--line); justify-content: flex-end; }}
    .dialog-head strong {{ font-size: 14px; }}
    .dialog-body {{
      margin: 0;
      padding: 14px;
      overflow: auto;
      color: #243241;
      background: var(--panel);
      font: 13px/1.55 ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", monospace;
      white-space: pre-wrap;
      overflow-wrap: anywhere;
    }}
    .icon-close {{
      width: 32px;
      height: 32px;
      border: 1px solid var(--line);
      border-radius: 8px;
      background: var(--panel);
      color: var(--text);
      cursor: pointer;
      font-size: 18px;
      line-height: 1;
    }}
    .muted {{ color: var(--muted); }}
    .rate {{ font-variant-numeric: tabular-nums; font-weight: 720; }}
    th.cost-real, td.cost-real {{ background: #eff6ff; }}
    th.cost-internal, td.cost-internal {{ background: #f0fdfa; }}
    th.cost-sell, td.cost-sell {{ background: #fff7ed; }}
    th.profit-col, td.profit-col {{ background: #fbfdfd; }}
    .profit-signal {{
      display: inline-flex;
      align-items: center;
      border-radius: 999px;
      padding: 4px 8px;
      font-size: 12px;
      font-weight: 700;
      border: 1px solid transparent;
      white-space: nowrap;
    }}
    .profit-signal.ok {{ color: var(--green); background: #ecfdf5; border-color: #bbf7d0; }}
    .profit-signal.warn {{ color: var(--amber); background: #fffbeb; border-color: #fde68a; }}
    .profit-signal.risk {{ color: var(--red); background: #fef2f2; border-color: #fecaca; }}
    .profit-signal.info {{ color: var(--blue); background: #eff6ff; border-color: #bfdbfe; }}
    .hint-icon {{
      display: inline-flex;
      align-items: center;
      justify-content: center;
      width: 16px;
      height: 16px;
      margin-left: 4px;
      border-radius: 999px;
      background: #e2e8f0;
      color: #334155;
      font-size: 11px;
      font-weight: 800;
    }}
    .image-cost-grid {{
      display: grid;
      grid-template-columns: repeat(3, minmax(0, 1fr));
      gap: 10px;
      margin-bottom: 12px;
    }}
    .image-cost-card {{
      border: 1px solid var(--line);
      border-radius: 8px;
      background: #fbfdfd;
      padding: 12px;
    }}
    .image-cost-card span {{ display: block; color: var(--muted); font-size: 12px; }}
    .image-cost-card strong {{ display: block; font-size: 22px; margin-top: 4px; font-variant-numeric: tabular-nums; }}
    .log-list {{
      display: grid;
      gap: 10px;
    }}
    .log-item {{
      border: 1px solid var(--line);
      border-radius: 8px;
      background: #fbfdfd;
      padding: 12px;
    }}
    .log-item strong {{ display: block; font-size: 14px; }}
    .log-item div {{ color: var(--muted); font-size: 13px; margin-top: 3px; overflow-wrap: anywhere; }}
    .badge {{
      display: inline-flex;
      align-items: center;
      justify-content: center;
      max-width: 100%;
      border-radius: 999px;
      padding: 4px 8px;
      font-size: 12px;
      font-weight: 650;
      border: 1px solid transparent;
      line-height: 1.2;
      text-align: center;
    }}
    .ok {{ color: var(--green); background: #ecfdf5; border-color: #bbf7d0; }}
    .warn {{ color: var(--amber); background: #fffbeb; border-color: #fde68a; }}
    .risk {{ color: var(--red); background: #fef2f2; border-color: #fecaca; }}
    .info {{ color: var(--blue); background: #eff6ff; border-color: #bfdbfe; }}
    .note {{ max-width: 380px; color: #374151; }}
    .empty-row td {{ color: var(--muted); padding: 20px 12px; text-align: center; }}
    .footer-note {{ color: var(--muted); font-size: 12px; margin: 16px 0 28px; }}

    /* Stage 3 visual skin: operational sci-fi without changing data layout. */
    .section-panel, .assistant-panel, .audit-panel, .kbq-wrap, .account-inspector {{
      background: var(--panel-glass);
      backdrop-filter: blur(8px);
    }}
    .card, .category-card, .site-node, .bucket-card, .adapter-chip, .plan-card,
    .server-metric-card, .audit-metric, .image-cost-card, .log-item,
    .freshness-item, .balance-chip, .container-row {{
      background:
        linear-gradient(180deg, rgba(255, 255, 255, 0.985), rgba(248, 251, 251, 0.985));
      box-shadow: var(--shadow);
    }}
    .card, .site-node, .health-card, .category-card, .bucket-card {{
      position: relative;
      overflow: hidden;
    }}
    .card::after, .site-node::after, .health-card::after, .category-card::after, .bucket-card::after {{
      content: "";
      position: absolute;
      inset: auto 0 0 0;
      height: 3px;
      background: var(--angle);
      opacity: 0.66;
      pointer-events: none;
    }}
    .site-node:hover, .category-card:hover, .bucket-card:hover, .health-card:hover,
    .action-button:hover, .inspect-mini:hover, .detail-button:hover {{
      box-shadow: 0 0 0 3px rgba(20, 184, 166, 0.10), var(--shadow);
    }}
    .site-node.warn::after, .health-card.warn::after {{
      background: linear-gradient(90deg, rgba(180, 83, 9, 0.36), transparent);
    }}
    .site-node.risk::after, .health-card.risk::after {{
      background: linear-gradient(90deg, rgba(185, 28, 28, 0.38), transparent);
    }}
    .site-node-metrics span, .cat-meta span {{
      background: linear-gradient(180deg, rgba(247, 251, 251, 0.98), rgba(241, 247, 248, 0.98));
    }}
    .inspector-row, .assistant-answer {{
      background:
        linear-gradient(180deg, rgba(248, 251, 251, 0.98), rgba(242, 247, 248, 0.98));
    }}
    .segmented {{
      background: rgba(247, 251, 251, 0.92);
      box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.72);
    }}
    .segmented button.active {{
      background: linear-gradient(135deg, var(--teal), var(--cyan));
      box-shadow: inset 0 0 18px rgba(255, 255, 255, 0.12);
    }}
    .plan-command {{
      background:
        linear-gradient(135deg, rgba(8, 145, 178, 0.18), transparent 42%),
        #0f172a;
      border-color: rgba(125, 211, 252, 0.25);
      box-shadow: inset 0 0 24px rgba(8, 145, 178, 0.10);
    }}
    .table-wrap {{
      background: var(--panel);
      border-color: var(--line-strong);
    }}
    th {{
      background: linear-gradient(180deg, rgba(242, 247, 248, 0.99), rgba(235, 242, 244, 0.99));
      color: #213b47;
    }}
    td {{
      background-clip: padding-box;
    }}
    tr:hover td {{
      background: #f8fbfb;
    }}
    .mobile-nav a.active, .quick-filters button.active {{
      background: linear-gradient(135deg, var(--teal), var(--cyan));
      border-color: transparent;
    }}
    @media (prefers-reduced-motion: reduce) {{
      *, *::before, *::after {{ scroll-behavior: auto !important; }}
      .action-button:active, .category-card:active, .bucket-card:active, .segmented button:active {{ transform: none; }}
    }}
    @media (min-width: 1500px) {{
      body[data-route="accounts"] .content-shell {{ padding-right: 366px; }}
      #accountInspector {{
        position: fixed;
        top: 22px;
        right: 28px;
        width: 318px;
        max-height: calc(100vh - 44px);
        overflow: auto;
        z-index: 20;
      }}
    }}
    @media (max-width: 1180px) {{
      .app-shell {{ grid-template-columns: 228px minmax(0, 1fr); }}
      .side-nav {{ padding: 18px 12px; }}
      .content-shell {{ padding: 18px; }}
      .health-grid {{ grid-template-columns: repeat(2, minmax(0, 1fr)); }}
    }}
    @media (max-width: 960px) {{
      .app-shell {{ display: block; }}
      .side-nav {{ display: none; }}
      .content-shell {{ padding: 16px; }}
      .mobile-nav {{ display: flex; }}
      .grid {{ grid-template-columns: repeat(2, minmax(0, 1fr)); }}
      .toolbar {{ grid-template-columns: 1fr; }}
      .provider-toolbar {{ grid-template-columns: 1fr; }}
      .assistant-form {{ grid-template-columns: 1fr; }}
      .category-grid {{ grid-template-columns: 1fr; }}
      .balance-strip {{ grid-template-columns: repeat(2, minmax(0, 1fr)); }}
      .bucket-grid {{ grid-template-columns: repeat(2, minmax(0, 1fr)); }}
      .adapter-grid {{ grid-template-columns: repeat(2, minmax(0, 1fr)); }}
      .server-grid {{ grid-template-columns: repeat(2, minmax(0, 1fr)); }}
      .server-split {{ grid-template-columns: 1fr; }}
      .audit-summary {{ grid-template-columns: repeat(2, minmax(0, 1fr)); }}
      .freshness-strip, .image-cost-grid {{ grid-template-columns: 1fr; }}
      .site-matrix {{ grid-template-columns: 1fr; }}
      .overview-command-center {{ grid-template-columns: 1fr; }}
    }}
    @media (max-width: 640px) {{
      .content-shell {{ padding-left: 12px; padding-right: 12px; }}
      .topbar {{ align-items: flex-start; flex-direction: column; }}
      .actions {{ width: 100%; }}
      .grid, .health-grid, .balance-strip, .bucket-grid, .adapter-grid, .audit-summary {{ grid-template-columns: 1fr; }}
      .server-grid, .server-split {{ grid-template-columns: 1fr; }}
      .section-title {{ align-items: flex-start; flex-direction: column; }}
      table {{ min-width: 920px; }}
    }}
  </style>
</head>
<body>
  <a class="skip-link" href="#main">跳到内容</a>
  <div class="app-shell">
    <aside class="side-nav" aria-label="台账导航">
      <div class="brand">
        <div class="brand-mark" aria-hidden="true">F</div>
        <div>
          <div class="brand-title">Fluter 台账总控</div>
          <div class="brand-sub">倍率 · 余额 · 审计</div>
        </div>
      </div>
      <nav class="nav-group" aria-label="主导航">
        <div class="nav-label">Console</div>
        <a class="nav-link" href="#overview" data-route="overview"><span>总览</span><span>Overview</span></a>
        <a class="nav-link" href="#server" data-route="server"><span>服务器状态</span><span>Server</span></a>
        <a class="nav-link" href="#assistant" data-route="assistant"><span>问台账 AI</span><span>Ask</span></a>
        <a class="nav-link" href="#automation" data-route="automation"><span>自动化状态</span><span>Auto</span></a>
        <a class="nav-link" href="#providers" data-route="providers"><span>上游抓取</span><span>Providers</span></a>
      </nav>
      <nav class="nav-group" aria-label="成本与倍率导航">
        <div class="nav-label">Cost</div>
        <a class="nav-link" href="#balance" data-route="balance"><span>余额雷达</span><span>Balance</span></a>
        <a class="nav-link" href="#accounts" data-route="accounts"><span>账号倍率</span><span>Accounts</span></a>
        <a class="nav-link" href="#kbq" data-route="kbq"><span>KBQ 成本</span><span>KBQ</span></a>
        <a class="nav-link" href="#image" data-route="image"><span>生图成本</span><span>Image</span></a>
      </nav>
      <nav class="nav-group" aria-label="审计与风险导航">
        <div class="nav-label">Audit</div>
        <a class="nav-link" href="#risk" data-route="risk"><span>风险 / 倒挂</span><span>Risk</span></a>
        <a class="nav-link" href="#log" data-route="log"><span>操作记录</span><span>Log</span></a>
      </nav>
      <div class="nav-group" aria-label="站点分类筛选">
        <div class="nav-label">Account Categories</div>
        <div id="categoryNav"></div>
      </div>
      <div class="side-note">
        <div>生成：{html.escape(generated_at)}</div>
        <div id="sideVisibleCount" aria-live="polite">等待渲染…</div>
      </div>
    </aside>
    <main class="content-shell" id="main">
      <header class="console-hero" aria-label="台账顶部">
        <div class="topbar">
          <div>
            <h1 id="routeTitle">总览</h1>
            <div class="route-sub" id="routeSubtitle">管理员只读总控台。数据来自服务器 SQLite 台账；这里只展示、筛选和导出，不保存密钥。</div>
          </div>
          <div class="actions">
            <span class="pill">生成：{html.escape(generated_at)}</span>
            <span class="pill" id="visibleCount" aria-live="polite">-</span>
            <button class="action-button" type="button" id="reloadPage">刷新</button>
            <button class="action-button" type="button" id="exportCsv">导出 CSV</button>
          </div>
        </div>
        <nav class="mobile-nav" aria-label="移动端导航">
          <a href="#overview" data-route="overview">总览</a>
          <a href="#server" data-route="server">服务器</a>
          <a href="#assistant" data-route="assistant">问台账</a>
          <a href="#automation" data-route="automation">自动化</a>
          <a href="#providers" data-route="providers">上游</a>
          <a href="#balance" data-route="balance">余额</a>
          <a href="#accounts" data-route="accounts">账号</a>
          <a href="#kbq" data-route="kbq">KBQ</a>
          <a href="#image" data-route="image">生图</a>
          <a href="#risk" data-route="risk">风险</a>
          <a href="#log" data-route="log">记录</a>
        </nav>
      </header>
      <section class="module-view section-block section-panel" id="overview" data-route="overview" aria-labelledby="overviewTitle">
        <div class="module-head">
          <div>
            <h2 id="overviewTitle">总览</h2>
            <div class="summary-line">先看是否亏本、余额是否危险、台账是否新鲜，再决定要不要进入明细。</div>
          </div>
        </div>
        <div class="health-grid" id="healthCards" aria-label="运营健康灯"></div>
        <div class="overview-command-center" aria-label="运营摘要">
          <div class="overview-panel">
            <div class="overview-panel-head">
              <h3>本轮要看</h3>
              <span class="pill" id="overviewFocusCount">等待渲染…</span>
            </div>
            <div class="overview-list" id="overviewFocusList"></div>
          </div>
          <div class="overview-panel">
            <div class="overview-panel-head">
              <h3>数据来源</h3>
              <span class="pill">只读</span>
            </div>
            <div class="overview-source-list" id="overviewSourceList"></div>
          </div>
        </div>
      </section>
      <section class="module-view section-block" data-route="overview" aria-label="台账统计">
        <div class="section-kicker">Ledger Stats</div>
        <div class="grid" id="cards"></div>
        <div class="freshness-strip" id="freshnessStrip" aria-label="数据新鲜度"></div>
      </section>
      <section class="module-view section-block section-panel" data-route="providers" aria-labelledby="siteMatrixTitle" hidden>
        <div class="section-title">
          <div>
            <h2 id="siteMatrixTitle">上游抓取矩阵</h2>
            <div class="summary-line">只看 upstream-hub 当前观察到的上游账号和组别；不混入主台账历史行、公开价格行、保留快照或旧脚本快照。</div>
          </div>
        </div>
        <div class="site-matrix" id="siteMatrix" aria-label="上游站点矩阵"></div>
      </section>
      <section class="module-view section-block section-panel" id="providers" data-route="providers" aria-labelledby="providersTitle" hidden>
        <div class="section-title">
          <div>
            <h2 id="providersTitle">上游页面抓取</h2>
            <div class="summary-line">这里仅展示当前可信油猴/浏览器账号快照里的账号、组别和页面倍率；旧 seed、历史台账、公开价格、生产快照、保留快照不进入本页。若某站显示 0 条，请先看采集诊断。</div>
          </div>
          <span class="pill" id="providerObservationCount">等待渲染…</span>
        </div>
        <div class="provider-toolbar">
          <label class="sr-only" for="providerSearch">搜索上游抓取行</label>
          <input id="providerSearch" type="search" autocomplete="off" placeholder="搜索上游站点、账号名、组别、来源…" />
          <span class="pill">来源：当前账号快照，不含历史</span>
        </div>
        <div class="provider-diagnostics" id="providerDiagnostics" aria-label="上游抓取诊断"></div>
        <div class="provider-notices" id="providerNotices" aria-live="polite"></div>
        <div class="table-wrap">
          <table class="provider-table">
            <caption>当前上游观察，不代表历史台账；旧脚本、保留快照、公开价格接口和历史 seed 都不会作为这里的账号清单。</caption>
            <thead>
              <tr>
                <th scope="col">上游</th>
                <th scope="col">站点</th>
                <th scope="col">账号名</th>
                <th scope="col">组别</th>
                <th scope="col">页面倍率</th>
                <th scope="col">来源</th>
                <th scope="col">匹配</th>
                <th scope="col">余额/时间</th>
                <th scope="col">抓取证据</th>
              </tr>
            </thead>
            <tbody id="providerRows"></tbody>
          </table>
        </div>
      </section>
      <section class="module-view section-block section-panel" id="server" data-route="server" aria-labelledby="serverTitle" hidden>
        <div class="section-title">
          <div>
            <h2 id="serverTitle">服务器状态</h2>
            <div class="summary-line">管理员专属半实时监控。指标接口在 Basic Auth 后面，只读系统状态，不读取密钥或生产数据库。</div>
          </div>
          <span class="pill" id="serverUpdatedAt">等待指标…</span>
        </div>
        <div class="server-grid" id="serverMetricCards" aria-label="服务器核心指标"></div>
        <div class="server-split">
          <div class="section-panel">
            <div class="section-title">
              <h2>流量趋势</h2>
              <div class="summary-line">前端用两次累计字节差计算，最近 60 个点只存在浏览器内存。</div>
            </div>
            <div class="server-sparkline" id="serverSparkline" aria-label="实时流量曲线"></div>
          </div>
          <div class="section-panel">
            <div class="section-title">
              <h2>容器健康</h2>
              <div class="summary-line">来自只读 docker ps，异常时只展示不可用原因。</div>
            </div>
            <div class="container-list" id="serverContainers" aria-label="容器健康表"></div>
          </div>
        </div>
      </section>
      <section class="module-view section-block section-panel" id="balance" data-route="balance" aria-labelledby="balanceTitle" hidden>
        <div class="section-title">
          <h2 id="balanceTitle">余额雷达</h2>
          <div class="summary-line">低余额会被标黄或标红，方便先补最容易影响服务的上游。</div>
        </div>
        <div class="balance-strip" id="balanceStrip" aria-label="上游余额"></div>
      </section>
      <section class="module-view assistant-panel section-block" id="assistant" data-route="assistant" aria-labelledby="assistantTitle" hidden>
      <div class="section-title">
        <h2 id="assistantTitle">问台账</h2>
        <div class="summary-line">优先调用服务端台账 AI；失败时自动退回页面本地查询</div>
      </div>
      <form class="assistant-form" id="ledgerAssistantForm">
        <label class="sr-only" for="ledgerAssistantInput">输入关于成本、余额或账号的问题</label>
        <input id="ledgerAssistantInput" type="search" autocomplete="off" placeholder="例如：哪些账号要关注？meow 生图成本？KBQ 是否倒挂？kingdom 余额？" />
        <button class="action-button" type="submit">查询</button>
      </form>
      <div class="assistant-examples" id="ledgerAssistantExamples" aria-label="常用问题">
        <button type="button" data-question="哪些账号需要关注">哪些账号需要关注</button>
        <button type="button" data-question="对比账号成本倍率、真实成本和用户分组倍率">倍率对比</button>
        <button type="button" data-question="倍率漂移巡检">倍率漂移巡检</button>
        <button type="button" data-question="KBQ 是否倒挂">KBQ 是否倒挂</button>
        <button type="button" data-question="生图成本">生图成本</button>
        <button type="button" data-question="余额">余额</button>
      </div>
      <div class="assistant-answer" id="ledgerAssistantAnswer" aria-live="polite">输入问题后，我会读取当前台账并请台账 AI 整理答案。复杂业务判断仍以人工复核为准。</div>
    </section>
    <section class="module-view section-block section-panel" id="automation" data-route="automation" aria-labelledby="automationTitle" hidden>
      <div class="section-title">
        <h2 id="automationTitle">自动化覆盖状态</h2>
        <div class="summary-line">upstream-hub 优先，公开价格与生产只读快照补充；单站失败不会阻塞整套刷新</div>
      </div>
      <div class="adapter-grid" id="adapterStatusCards" aria-label="上游自动化覆盖状态"></div>
    </section>
    <section class="module-view section-block section-panel" id="priorityPlan" data-route="accounts" aria-labelledby="priorityPlanTitle" hidden>
      <div class="section-title">
        <h2 id="priorityPlanTitle">优先级预览</h2>
        <div class="summary-line">只读预览卡片。建议变化仅供人工核对；生产备注写入路径已废弃。</div>
      </div>
      <div class="plan-grid" id="priorityPlanCards" aria-label="优先级预览摘要"></div>
      <div class="plan-command" id="priorityPlanCommand">此模块仅展示 dry-run 结果，不提供写生产命令。</div>
      <div class="table-wrap plan-table-wrap">
        <table class="plan-table">
          <caption>最近一次 dry-run 建议：当前优先级 vs 建议优先级</caption>
          <thead>
            <tr>
              <th scope="col">建议序号</th>
              <th scope="col">账号</th>
              <th scope="col">当前</th>
              <th scope="col">建议</th>
              <th scope="col">倍率</th>
              <th scope="col">档位</th>
              <th scope="col">分组</th>
              <th scope="col">原因</th>
              <th scope="col">更新时间</th>
            </tr>
          </thead>
          <tbody id="priorityPlanRows"></tbody>
        </table>
      </div>
    </section>
    <section class="module-view account-inspector section-block" id="accountInspector" data-route="accounts" aria-labelledby="accountInspectorTitle" hidden>
      <div class="section-title">
        <div>
          <h2 id="accountInspectorTitle">账号 Inspector</h2>
          <div class="summary-line">默认展示当前筛选下最需要看的账号；点明细表里的“检查”可锁定单个账号。</div>
        </div>
      </div>
      <div id="accountInspectorBody" class="inspector-grid"></div>
    </section>
    <section class="module-view section-block section-panel filter-panel" data-route="accounts" aria-label="筛选台账" hidden>
      <div class="toolbar">
        <label class="sr-only" for="search">搜索账号、网站、分组或备注</label>
        <input id="search" name="upstream-search" type="search" autocomplete="off" placeholder="搜索账号、网站、分组、备注…" />
        <label class="sr-only" for="categoryFilter">站点分类</label>
        <select id="categoryFilter" name="category-filter"><option value="">全部站点分类</option></select>
        <label class="sr-only" for="kindFilter">类型</label>
        <select id="kindFilter" name="kind-filter"><option value="">全部类型</option></select>
        <label class="sr-only" for="statusFilter">状态</label>
        <select id="statusFilter" name="status-filter"><option value="">全部状态</option></select>
      </div>
      <div class="quick-filters" id="quickFilters" aria-label="快捷筛选">
        <button class="action-button active" type="button" data-quick="" aria-pressed="true">全部</button>
        <button class="action-button" type="button" data-quick="attention" aria-pressed="false">只看需关注</button>
        <button class="action-button" type="button" data-quick="lowBalance" aria-pressed="false">只看低余额</button>
        <button class="action-button" type="button" data-quick="drift" aria-pressed="false">只看倍率漂移</button>
        <button class="action-button" type="button" data-quick="kbq" aria-pressed="false">只看 KBQ</button>
        <button class="action-button" type="button" data-quick="image" aria-pressed="false">只看生图</button>
      </div>
    </section>
    <section class="module-view section-block section-panel" id="categories" data-route="accounts" aria-labelledby="categoriesTitle" hidden>
      <div class="section-title">
        <h2 id="categoriesTitle">账号倍率</h2>
        <div class="summary-line">点击分类卡片可快速筛选；下方明细表把真实成本、内部成本记录和用户售价分开显示。</div>
      </div>
      <div class="category-grid" id="categoryCards"></div>
    </section>
    <section class="module-view category-detail section-block" id="categoryDetail" data-route="kbq" hidden>
      <div class="section-title">
        <h2>KBQ 站点分类详情</h2>
        <div class="summary-line">上方明细台账看账号成本记录和用户售价；这里看 KBQ 公开价格接口的按 token 模型和 Claude 按次模型</div>
      </div>
      <div class="kbq-wrap">
        <div class="segmented kbq-mode-tabs" id="kbqModeTabs" aria-label="KBQ 子模块">
          <button class="active" type="button" data-kbq-mode="token" aria-pressed="true">按 Token</button>
          <button type="button" data-kbq-mode="perCall" aria-pressed="false">按次 Claude</button>
          <button type="button" data-kbq-mode="audit" aria-pressed="false">审计说明</button>
        </div>
        <div class="kbq-tab-panel" data-kbq-panel="token">
        <div class="kbq-toolbar">
          <div class="segmented" id="kbqCategoryTabs" aria-label="KBQ 模型分类"></div>
          <span class="pill" id="kbqMeta" aria-live="polite">-</span>
        </div>
        <div class="kbq-note">成本倍率 = KBQ 上游实际单价 / 官方基准单价。账号成本倍率是内部成本记录，越贴近真实成本越好；用户分组倍率/售价才是卖给用户的口径，判断利润要看它和真实成本的差。</div>
        <div class="bucket-grid" id="kbqBuckets"></div>
        <div class="table-wrap">
          <table class="kbq-table">
            <caption>KBQ Claude 与 Codex/OpenAI 按 token 计费模型成本分档</caption>
            <thead>
              <tr>
                <th scope="col">成本档位</th>
                <th scope="col">KBQ 上游模型</th>
                <th scope="col">接口</th>
                <th scope="col">上游价/1M</th>
                <th scope="col">官方基准/1M</th>
                <th scope="col">raw ratio</th>
                <th scope="col">更新时间</th>
              </tr>
            </thead>
            <tbody id="kbqRows"></tbody>
          </table>
        </div>
        </div>
        <div class="kbq-tab-panel" data-kbq-panel="perCall" hidden>
        <div class="kbq-note">Claude 按次模型单独监控：真实单次成本 = KBQ 接口按次价 × 当前充值折扣系数。这里的“每次”不是 token 倍率，开放前要单独设计售价和分组。</div>
        <div class="table-wrap">
          <table class="kbq-per-call-table">
            <caption>KBQ Claude 按次计费模型价格监控</caption>
            <thead>
              <tr>
                <th scope="col">按次档位</th>
                <th scope="col">KBQ 上游模型</th>
                <th scope="col">接口/标签</th>
                <th scope="col">接口价</th>
                <th scope="col">折扣后成本</th>
                <th scope="col">说明</th>
                <th scope="col">更新时间</th>
              </tr>
            </thead>
            <tbody id="kbqPerCallRows"></tbody>
          </table>
        </div>
        </div>
        <div class="kbq-tab-panel" data-kbq-panel="audit" hidden>
          <div class="kbq-note">KBQ 的账号总览行必须跟实时模型明细对齐；按 token 模型用 /api/pricing 相对官方价换算，按次模型按“每次价 × 充值系数”独立看。DISPLAY_DRIFT 是后台展示口径漂移，不能当成真实亏损。</div>
          <div class="freshness-strip" id="kbqGuideCards" aria-label="KBQ 口径说明"></div>
        </div>
      </div>
    </section>
    <section class="module-view section-block" id="kbqAuditSection" data-route="risk" hidden>
      <div class="section-title">
        <h2>KBQ 真实成本审计</h2>
        <div class="summary-line">用最近 usage_logs 重新计算 KBQ 上游真实成本；判断是否真的倒挂</div>
      </div>
      <div class="audit-panel">
        <div id="kbqAuditSummary" class="audit-summary"></div>
        <div class="kbq-note">判断亏本只看“真实上游成本是否大于用户扣费”。A成本属于后台展示口径，旧默认价格可能偏高，所以单独标记为 DISPLAY_DRIFT。</div>
        <div class="table-wrap">
          <table class="audit-table">
            <caption>最近一次 KBQ 真实成本审计桶</caption>
            <thead>
              <tr>
                <th scope="col">状态</th>
                <th scope="col">账号/渠道/分组</th>
                <th scope="col">模型</th>
                <th scope="col">请求</th>
                <th scope="col">用户扣费</th>
                <th scope="col">真实成本</th>
                <th scope="col">利润</th>
                <th scope="col">A成本展示</th>
                <th scope="col">缓存</th>
                <th scope="col">备注</th>
              </tr>
            </thead>
            <tbody id="kbqAuditRows"></tbody>
          </table>
        </div>
      </div>
    </section>
    <section class="module-view section-block" id="imageSection" data-route="image" hidden>
      <div class="section-title">
        <h2>生图成本</h2>
        <div class="summary-line">生图按张计费，重点看单张真实扣费和单张售价，不和文字 token 倍率混算。</div>
      </div>
      <div class="kbq-wrap">
        <div class="image-cost-grid" id="imageCostCards"></div>
        <div class="kbq-note">如果单张真实成本来自上游流水，以备注为准；如果只看到文字倍率，需要重新跑小样本核价后再定价。</div>
        <div class="table-wrap">
          <table class="image-table">
            <caption>生图账号、桥接/原生能力与单张成本记录</caption>
            <thead>
              <tr>
                <th scope="col">状态</th>
                <th scope="col">我站账号</th>
                <th scope="col">上游</th>
                <th scope="col">上游分组</th>
                <th scope="col">每张上游成本</th>
                <th scope="col">每张售价/分组</th>
                <th scope="col">利润信号</th>
                <th scope="col">备注</th>
              </tr>
            </thead>
            <tbody id="imageCostRows"></tbody>
          </table>
        </div>
      </div>
    </section>
    <section class="module-view section-block" id="ledgerSection" data-route="accounts" hidden>
      <div class="section-title">
        <h2>明细台账</h2>
        <div class="summary-line">文字模型看实际成本倍率；生图等按次项目优先看单张成本说明</div>
      </div>
      <div class="table-wrap">
        <table class="accounts-table">
          <caption>上游账号、页面倍率、余额、内部成本记录和用户售价明细</caption>
          <thead>
            <tr>
              <th scope="col">状态</th>
              <th scope="col">我站账号命名</th>
              <th scope="col">上游</th>
              <th scope="col">类型</th>
              <th scope="col">上游分组</th>
              <th scope="col">页面倍率</th>
              <th scope="col">充值比例</th>
              <th scope="col">余额</th>
              <th scope="col" class="cost-real">真实成本<span class="hint-icon" title="上游实际收我们的成本；文字模型通常按倍率，生图优先按单张流水。">i</span></th>
              <th scope="col" class="cost-internal">账号成本倍率<span class="hint-icon" title="我站账号里的内部成本记录，目标是贴近真实上游成本，不是卖给用户的售价。">i</span></th>
              <th scope="col" class="cost-sell">用户售价<span class="hint-icon" title="用户分组倍率或单张售价，判断利润要看它是否覆盖真实成本。">i</span></th>
              <th scope="col" class="profit-col">利润信号</th>
              <th scope="col">备注</th>
            </tr>
          </thead>
          <tbody id="rows"></tbody>
        </table>
      </div>
    </section>
    <section class="module-view section-block section-panel" id="operationLog" data-route="log" hidden>
      <div class="section-title">
        <h2>操作记录</h2>
        <div class="summary-line">这里展示本次静态页渲染、台账元数据和自动化只读快照摘要。</div>
      </div>
      <div class="log-list" id="operationLogList"></div>
    </section>
    <p class="footer-note">安全说明：本页面不包含完整 API key、密码、Cookie 或 Bearer token。外层由 Caddy Basic Auth 保护。</p>
  </main>
  </div>
  <dialog class="detail-dialog" id="detailDialog" aria-labelledby="detailDialogTitle">
    <form method="dialog">
      <div class="dialog-head">
        <strong id="detailDialogTitle">完整内容</strong>
        <button class="icon-close" type="submit" aria-label="关闭详情">×</button>
      </div>
      <pre class="dialog-body" id="detailDialogBody"></pre>
      <div class="dialog-actions">
        <button class="action-button" type="submit">关闭</button>
      </div>
    </form>
  </dialog>
  <script>
    const DATA = {data_json};
    const PROVIDER_OBSERVATIONS = {provider_observations_json};
    const PROVIDER_DIAGNOSTICS = {provider_diagnostics_json};
    const BALANCE_SNAPSHOTS = {balance_snapshots_json};
    const KBQ_MODELS = {kbq_json};
    const KBQ_PER_CALL_MODELS = {kbq_per_call_json};
    const KBQ_AUDIT = {audit_json};
    const KBQ_AUDIT_BUCKETS = {audit_buckets_json};
    const ADAPTER_STATUS = {adapter_status_json};
    const META = {metadata_json};
    const PRIORITY_PLAN = {priority_plan_json};
    const GENERATED_AT = {json.dumps(generated_at)};

    const fmtRate = (value) => value === null || value === undefined || Number.isNaN(value)
      ? "未确认"
      : `${{Number(value.toFixed(6)).toString()}}x`;
    const fmtMoney = (value) => value === null || value === undefined || Number.isNaN(value)
      ? "-"
      : `$${{Number(value.toFixed(6)).toString()}}`;
    const fmtNumber = (value) => value === null || value === undefined || Number.isNaN(value)
      ? "-"
      : Number(value.toFixed(6)).toString();
    const esc = (value) => String(value ?? "").replace(/[&<>"']/g, ch => ({{"&":"&amp;","<":"&lt;",">":"&gt;","\\"":"&quot;","'":"&#039;"}}[ch]));
    const uniq = (arr) => [...new Set(arr.filter(Boolean))].sort((a, b) => a.localeCompare(b, "zh-Hans-CN"));
    let detailItems = [];

    function detailCell(value, options = {{}}) {{
      const text = String(value ?? "").trim();
      const empty = options.empty || "-";
      const label = options.label || "内容";
      const lines = options.lines || 2;
      const threshold = options.threshold || 46;
      const className = `text-clip ${{lines === 1 ? "one" : lines === 3 ? "three" : "two"}}`;
      if (!text) return `<span class="${{className}} muted">${{esc(empty)}}</span>`;
      const needsDetail = text.length > threshold || /\\n/.test(text);
      if (!needsDetail) return `<span class="${{className}}" title="${{esc(text)}}">${{esc(text)}}</span>`;
      const id = detailItems.push({{ title: label, text }}) - 1;
      return `<div class="detail-cell"><span class="${{className}}" title="${{esc(text)}}">${{esc(text)}}</span><button class="detail-button" type="button" data-detail="${{id}}" aria-label="查看完整${{esc(label)}}">展开</button></div>`;
    }}

    function stackCell(items) {{
      return `<div class="cell-stack">${{items.filter(Boolean).join("")}}</div>`;
    }}

    function discountSourceLabel(row) {{
      return row.discountSource === "discount_profile" ? "站点折扣档案" : "行内兜底";
    }}

    function discountTone(row) {{
      if (row.discountStatus === "已确认" || row.discountSource === "discount_profile") return "ok";
      return "warn";
    }}

    function discountCell(row) {{
      const factor = Number(row.rechargeFactor.toFixed(6)).toString();
      return stackCell([
        detailCell(row.rechargeRatioLabel, {{ label: "充值比例", lines: 1, threshold: 24 }}),
        `<div class="discount-meta"><span class="discount-pill ${{discountTone(row)}}">${{esc(discountSourceLabel(row))}}</span><span class="discount-pill" title="${{esc(row.discountNote || "")}}">系数 ${{esc(factor)}} · ${{esc(row.discountStatus || "待核对")}}</span></div>`,
      ]);
    }}

    const search = document.querySelector("#search");
    const categoryFilter = document.querySelector("#categoryFilter");
    const kindFilter = document.querySelector("#kindFilter");
    const statusFilter = document.querySelector("#statusFilter");
    const tbody = document.querySelector("#rows");
    const categoryDetail = document.querySelector("#categoryDetail");
    const ledgerSection = document.querySelector("#ledgerSection");
    const categoryNav = document.querySelector("#categoryNav");
    const healthCards = document.querySelector("#healthCards");
    const sideVisibleCount = document.querySelector("#sideVisibleCount");
    const assistantInput = document.querySelector("#ledgerAssistantInput");
    const assistantAnswer = document.querySelector("#ledgerAssistantAnswer");
    const routeTitle = document.querySelector("#routeTitle");
    const routeSubtitle = document.querySelector("#routeSubtitle");
    const detailDialog = document.querySelector("#detailDialog");
    const detailDialogTitle = document.querySelector("#detailDialogTitle");
    const detailDialogBody = document.querySelector("#detailDialogBody");
    const serverMetricCards = document.querySelector("#serverMetricCards");
    const serverUpdatedAt = document.querySelector("#serverUpdatedAt");
    const serverContainers = document.querySelector("#serverContainers");
    const serverSparkline = document.querySelector("#serverSparkline");
    const siteMatrix = document.querySelector("#siteMatrix");
    const providerRows = document.querySelector("#providerRows");
    const providerSearch = document.querySelector("#providerSearch");
    const providerObservationCount = document.querySelector("#providerObservationCount");
    const providerDiagnostics = document.querySelector("#providerDiagnostics");
    const providerNotices = document.querySelector("#providerNotices");
    const accountInspectorBody = document.querySelector("#accountInspectorBody");
    const overviewFocusList = document.querySelector("#overviewFocusList");
    const overviewFocusCount = document.querySelector("#overviewFocusCount");
    const overviewSourceList = document.querySelector("#overviewSourceList");
    const moduleViews = [...document.querySelectorAll(".module-view[data-route]")];
    const kbqState = {{ category: "Claude", bucket: "" }};
    let kbqMode = "token";
    let inspectorRowKey = "";
    const serverState = {{ last: null, points: [] }};
    const ROUTE_META = {{
      overview: {{
        title: "总览",
        subtitle: "先看风险、余额、漂移和自动化状态，再进入具体模块处理。"
      }},
      server: {{
        title: "服务器状态",
        subtitle: "半实时查看 VPS 负载、内存、磁盘、流量和容器状态；指标接口在 Basic Auth 后面。"
      }},
      assistant: {{
        title: "问台账 AI",
        subtitle: "用自然语言问成本、余额、倍率和接入情况；服务不可用时会走本页本地查询兜底。"
      }},
      automation: {{
        title: "自动化状态",
        subtitle: "查看上游页面 adapter、浏览器只读快照和刷新覆盖情况。"
      }},
      providers: {{
        title: "上游抓取",
        subtitle: "优先展示 upstream-hub 当前观察到的账号/组别；诊断浏览器快照只作 hub 缺口兜底，公开价格、seed 和历史台账只进诊断或明细。"
      }},
      balance: {{
        title: "余额雷达",
        subtitle: "按上游余额组查看余额，低余额优先处理。"
      }},
      accounts: {{
        title: "账号倍率",
        subtitle: "核心台账：分清真实成本、账号成本倍率和用户分组售价。"
      }},
      kbq: {{
        title: "KBQ 成本",
      subtitle: "KBQ 公开价格接口的模型成本分档，只做参考价表，不直接判断利润。"
      }},
      image: {{
        title: "生图成本",
        subtitle: "图片按单张核价，重点看真实单张扣费、单张售价和是否支持原生/桥接/图生图。"
      }},
      risk: {{
        title: "风险 / 倒挂",
        subtitle: "重点看真实上游成本是否超过用户扣费，DISPLAY_DRIFT 只代表展示口径漂移。"
      }},
      log: {{
        title: "操作记录",
        subtitle: "查看本次渲染、元数据和 upstream-hub / public pricing / 诊断 adapter 最近状态。"
      }},
    }};
    const HASH_ALIASES = {{
      "": "overview",
      overview: "overview",
      server: "server",
      assistant: "assistant",
      automation: "automation",
      providers: "providers",
      provider: "providers",
      balances: "balance",
      balance: "balance",
      categories: "accounts",
      ledgerSection: "accounts",
      accounts: "accounts",
      categoryDetail: "kbq",
      kbq: "kbq",
      imageSection: "image",
      image: "image",
      kbqAuditSection: "risk",
      risk: "risk",
      operationLog: "log",
      log: "log",
    }};
    let quickMode = "";

    function scrollToTarget(target, offset = 24) {{
      if (!target) return;
      const reduceMotion = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
      window.scrollTo({{
        top: Math.max(target.offsetTop - offset, 0),
        behavior: reduceMotion ? "auto" : "smooth",
      }});
    }}

    function routeFromName(name) {{
      const normalized = HASH_ALIASES[String(name || "").replace(/^#/, "")] || "overview";
      return ROUTE_META[normalized] ? normalized : "overview";
    }}

    function showRoute(name, options = {{}}) {{
      const route = routeFromName(name);
      for (const view of moduleViews) {{
        view.hidden = view.dataset.route !== route;
      }}
      for (const item of document.querySelectorAll(".nav-link[data-route], .mobile-nav a[data-route]")) {{
        const active = item.dataset.route === route;
        item.classList.toggle("active", active);
        if (active) item.setAttribute("aria-current", "page");
        else item.removeAttribute("aria-current");
      }}
      const meta = ROUTE_META[route] || ROUTE_META.overview;
      if (routeTitle) routeTitle.textContent = meta.title;
      if (routeSubtitle) routeSubtitle.textContent = meta.subtitle;
      document.body.dataset.route = route;
      if (options.updateHash !== false && location.hash.slice(1) !== route) {{
        history.pushState(null, "", `#${{route}}`);
      }}
      if (route === "kbq") renderKbqModels();
      if (route === "risk") renderKbqAudit();
      if (route === "image") renderImageCosts();
      if (route === "log") renderOperationLog();
      if (route === "providers") renderProviderObservations();
      if (route === "server") refreshServerMetrics();
      if (options.resetScroll !== false) {{
        const reduceMotion = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
        window.scrollTo({{ top: 0, behavior: reduceMotion ? "auto" : "smooth" }});
      }}
    }}

    function routeFromHash() {{
      showRoute(location.hash.slice(1), {{ updateHash: false, resetScroll: false }});
    }}

    function badgeClass(status) {{
      if (status.includes("真倒挂") || status.includes("倒挂") || status.includes("错误") || status.includes("失效") || status.includes("失败")) return "risk";
      if (status.includes("消失") || status.includes("待重映射")) return "warn";
      if (status.includes("尺寸") || status.includes("缩水")) return "warn";
      if (status.includes("核对") || status.includes("谨慎") || status.includes("未确认") || status.includes("单张") || status.includes("模型价") || status.includes("漂移") || status.includes("需观察")) return "warn";
      if (status.includes("未分配") || status.includes("未调度") || status.includes("停用") || status.includes("未接入") || status.includes("文字调度") || status.includes("非生图") || status.includes("无生图权限")) return "info";
      if (status.includes("覆盖") || status.includes("确认") || status.includes("保守")) return "ok";
      return "info";
    }}

    function parseBalanceNumber(label) {{
      const match = String(label || "").replace(/,/g, "").match(/[0-9]+(?:\\.[0-9]+)?/);
      return match ? Number(match[0]) : null;
    }}

    function hasUsableBalance(label) {{
      const text = String(label || "");
      return Boolean(text) && !text.includes("未显示") && !text.includes("未记录");
    }}

    function parseTimestampLike(value) {{
      const raw = String(value || "").trim();
      if (!raw) return null;
      const iso = raw.match(/\\d{{4}}-\\d{{2}}-\\d{{2}}T\\d{{2}}:\\d{{2}}:\\d{{2}}(?:\\.\\d+)?(?:Z|[+\\-]\\d{{2}}:?\\d{{2}})?/);
      if (iso) {{
        const parsed = new Date(iso[0]);
        return Number.isNaN(parsed.getTime()) ? null : parsed;
      }}
      const bj = raw.match(/\\d{{4}}-\\d{{2}}-\\d{{2}}[ T]\\d{{2}}:\\d{{2}}:\\d{{2}}/);
      if (bj) {{
        const parsed = new Date(`${{bj[0].replace(" ", "T")}}+08:00`);
        return Number.isNaN(parsed.getTime()) ? null : parsed;
      }}
      return null;
    }}

    function toneScore(tone) {{
      return {{ risk: 0, warn: 1, ok: 2, info: 3, "": 4 }}[tone] ?? 4;
    }}

    function worstTone(...tones) {{
      return tones.filter(Boolean).sort((a, b) => toneScore(a) - toneScore(b))[0] || "";
    }}

    function freshnessSignal(values) {{
      const dates = (Array.isArray(values) ? values : [values])
        .map(parseTimestampLike)
        .filter(Boolean);
      if (!dates.length) return {{ label: "未记录", tone: "warn", ageHours: null }};
      const latest = dates.reduce((a, b) => (a > b ? a : b));
      const ageHours = Math.max(0, (Date.now() - latest.getTime()) / 3600000);
      const label = ageHours < 1
        ? `距今 ${{Math.max(1, Math.round(ageHours * 60))}} 分钟`
        : `距今 ${{ageHours < 10 ? ageHours.toFixed(1) : Math.round(ageHours)}} 小时`;
      const tone = ageHours > 24 ? "risk" : ageHours > 2 ? "warn" : "ok";
      return {{ label, tone, ageHours }};
    }}

    function fmtBytes(value) {{
      if (value === null || value === undefined || Number.isNaN(Number(value))) return "-";
      const units = ["B", "KB", "MB", "GB", "TB"];
      let size = Number(value);
      let unit = 0;
      while (Math.abs(size) >= 1024 && unit < units.length - 1) {{
        size /= 1024;
        unit += 1;
      }}
      return `${{size >= 10 || unit === 0 ? size.toFixed(0) : size.toFixed(1)}} ${{units[unit]}}`;
    }}

    function fmtMbps(value) {{
      if (value === null || value === undefined || Number.isNaN(Number(value))) return "-";
      return `${{Number(value).toFixed(Number(value) >= 10 ? 1 : 2)}} Mbps`;
    }}

    function fmtPercent(value) {{
      if (value === null || value === undefined || Number.isNaN(Number(value))) return "-";
      return `${{Number(value).toFixed(Number(value) >= 10 ? 1 : 2)}}%`;
    }}

    function fmtUptime(seconds) {{
      if (seconds === null || seconds === undefined || Number.isNaN(Number(seconds))) return "-";
      let remaining = Math.max(0, Math.floor(Number(seconds)));
      const days = Math.floor(remaining / 86400);
      remaining -= days * 86400;
      const hours = Math.floor(remaining / 3600);
      remaining -= hours * 3600;
      const minutes = Math.floor(remaining / 60);
      if (days > 0) return `${{days}}天 ${{hours}}小时`;
      if (hours > 0) return `${{hours}}小时 ${{minutes}}分钟`;
      return `${{minutes}}分钟`;
    }}

    function metricTone(percentValue, warnAt = 70, riskAt = 88) {{
      const value = Number(percentValue);
      if (Number.isNaN(value)) return "";
      if (value >= riskAt) return "risk";
      if (value >= warnAt) return "warn";
      return "";
    }}

    function loadTone(loadPercent) {{
      return metricTone(loadPercent, 70, 100);
    }}

    function updateServerMetricCards(metrics, rates) {{
      if (!serverMetricCards) return;
      const cpu = metrics.cpu || {{}};
      const memory = metrics.memory || {{}};
      const disk = metrics.disk || {{}};
      const net = metrics.net || {{}};
      const containers = metrics.containers || {{}};
      const containerItems = containers.items || [];
      const unhealthy = containerItems.filter(item => item.health !== "ok").length;
      const cards = [
        {{
          label: "CPU 负载",
          value: fmtPercent(cpu.load1_per_core_percent),
          hint: `1m ${{fmtNumber(cpu.load1)}} / cores ${{cpu.cores ?? "-"}}`,
          tone: loadTone(cpu.load1_per_core_percent),
        }},
        {{
          label: "内存",
          value: fmtPercent(memory.used_percent),
          hint: `${{fmtBytes(memory.used_bytes)}} / ${{fmtBytes(memory.total_bytes)}}`,
          tone: metricTone(memory.used_percent, 75, 90),
        }},
        {{
          label: "磁盘 /",
          value: fmtPercent(disk.used_percent),
          hint: `${{fmtBytes(disk.used_bytes)}} / ${{fmtBytes(disk.total_bytes)}}`,
          tone: metricTone(disk.used_percent, 75, 90),
        }},
        {{
          label: "实时流量",
          value: fmtMbps((rates.rx_mbps || 0) + (rates.tx_mbps || 0)),
          hint: `↓ ${{fmtMbps(rates.rx_mbps)}} · ↑ ${{fmtMbps(rates.tx_mbps)}} · ${{net.primary_interface || "-"}}`,
          tone: "",
        }},
        {{
          label: "运行时间",
          value: fmtUptime(metrics.uptime_sec),
          hint: metrics.ts || "未记录",
          tone: "",
        }},
        {{
          label: "容器",
          value: `${{containerItems.length || 0}}`,
          hint: containers.available ? `${{unhealthy}} 个异常/非 Up` : (containers.error || "docker 不可用"),
          tone: containers.available ? (unhealthy ? "warn" : "") : "warn",
        }},
      ];
      serverMetricCards.innerHTML = cards.map(card => `
        <div class="server-metric-card ${{card.tone}}">
          <span>${{esc(card.label)}}</span>
          <strong>${{esc(card.value)}}</strong>
          <small>${{esc(card.hint)}}</small>
        </div>
      `).join("");
    }}

    function updateServerContainers(metrics) {{
      if (!serverContainers) return;
      const containers = metrics.containers || {{}};
      if (!containers.available) {{
        serverContainers.innerHTML = `<div class="container-row"><strong>docker ps 不可用</strong><span>${{esc(containers.error || "未返回容器信息")}}</span><span class="badge warn">WARN</span></div>`;
        return;
      }}
      const items = containers.items || [];
      if (!items.length) {{
        serverContainers.innerHTML = `<div class="container-row"><strong>无容器</strong><span>docker ps 未返回运行容器</span><span class="badge info">INFO</span></div>`;
        return;
      }}
      serverContainers.innerHTML = items.map(item => `
        <div class="container-row">
          <strong>${{esc(item.name)}}</strong>
          <span>${{esc(item.status)}}</span>
          <span class="badge ${{item.health === "ok" ? "ok" : "warn"}}">${{item.health === "ok" ? "OK" : "CHECK"}}</span>
        </div>
      `).join("");
    }}

    function renderServerSparkline() {{
      if (!serverSparkline) return;
      const points = serverState.points.slice(-60);
      if (points.length < 2) {{
        serverSparkline.innerHTML = `<div class="muted">等待第二次采样后显示趋势…</div>`;
        return;
      }}
      const width = 520;
      const height = 94;
      const maxValue = Math.max(1, ...points.map(point => Math.max(point.rx, point.tx)));
      const xFor = index => points.length === 1 ? 0 : (index / (points.length - 1)) * width;
      const yFor = value => height - (Math.max(0, value) / maxValue) * height;
      const line = key => points.map((point, index) => `${{xFor(index).toFixed(2)}},${{yFor(point[key]).toFixed(2)}}`).join(" ");
      serverSparkline.innerHTML = `
        <svg viewBox="0 0 ${{width}} ${{height}}" role="img" aria-label="最近实时流量 Mbps 曲线">
          <polyline points="${{line("rx")}}" fill="none" stroke="#2563eb" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"></polyline>
          <polyline points="${{line("tx")}}" fill="none" stroke="#0f766e" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"></polyline>
        </svg>
        <div class="summary-line">蓝色下载，绿色上传；峰值 ${{fmtMbps(maxValue)}}</div>
      `;
    }}

    function applyServerMetrics(metrics) {{
      const nowMs = Date.now();
      const net = metrics.net || {{}};
      let rates = {{ rx_mbps: null, tx_mbps: null }};
      if (serverState.last && Number.isFinite(net.rx_bytes) && Number.isFinite(net.tx_bytes)) {{
        const seconds = Math.max((nowMs - serverState.last.t) / 1000, 1);
        rates = {{
          rx_mbps: Math.max(0, (net.rx_bytes - serverState.last.rx) * 8 / seconds / 1000000),
          tx_mbps: Math.max(0, (net.tx_bytes - serverState.last.tx) * 8 / seconds / 1000000),
        }};
        serverState.points.push({{ rx: rates.rx_mbps, tx: rates.tx_mbps }});
        if (serverState.points.length > 60) serverState.points.shift();
      }}
      if (Number.isFinite(net.rx_bytes) && Number.isFinite(net.tx_bytes)) {{
        serverState.last = {{ t: nowMs, rx: net.rx_bytes, tx: net.tx_bytes }};
      }}
      if (serverUpdatedAt) serverUpdatedAt.textContent = metrics.ts ? `指标：${{metrics.ts}}` : "指标已更新";
      updateServerMetricCards(metrics, rates);
      updateServerContainers(metrics);
      renderServerSparkline();
    }}

    async function refreshServerMetrics() {{
      if (!serverMetricCards) return;
      try {{
        const response = await fetch("/admin/upstream-rates/metrics", {{ cache: "no-store" }});
        const payload = await response.json();
        if (!response.ok || !payload || payload.status !== "ok") throw new Error(payload && payload.error ? payload.error : `HTTP ${{response.status}}`);
        applyServerMetrics(payload);
      }} catch (error) {{
        if (serverUpdatedAt) serverUpdatedAt.textContent = "指标暂不可用";
        serverMetricCards.innerHTML = `
          <div class="server-metric-card warn">
            <span>指标暂不可用</span>
            <strong>CHECK</strong>
            <small>${{esc(error.message || String(error))}}</small>
          </div>
        `;
      }}
    }}

    function balanceGroupKey(row) {{
      return `${{row.category || row.provider || row.site}}:${{row.balanceLabel || ""}}`;
    }}

    function isCostRecordDrift(row) {{
      if (row.actualCostLabel) return false;
      if (typeof row.costRecordRatio !== "number") return false;
      return row.costRecordRatio < 0.95 || row.costRecordRatio > 1.2;
    }}

    function balanceClass(item) {{
      const value = parseBalanceNumber(item.balanceLabel);
      if (value === null) return "";
      if (value < 20) return "balance-low";
      if (value < 100) return "balance-watch";
      return "balance-ok";
    }}

    function rowKey(row) {{
      return [row.site, row.fluterAccountName, row.upstreamGroup, row.kind].join("||");
    }}

    function groupProviderObservationsBySite() {{
      const grouped = new Map();
      for (const row of PROVIDER_OBSERVATIONS) {{
        if (!grouped.has(row.site)) grouped.set(row.site, []);
        grouped.get(row.site).push(row);
      }}
      return grouped;
    }}

    function providerDiagnosticForSite(site) {{
      return PROVIDER_DIAGNOSTICS.find(item => item.site === site) || null;
    }}

    function providerSiteTone(rows) {{
      const freshness = freshnessSignal(rows.flatMap(row => [row.observedAt, row.balanceUpdatedAt]));
      const unmatched = rows.some(row => row.matchedLedgerRows === 0);
      return worstTone(unmatched ? "warn" : "", freshness.tone);
    }}

    function adapterHasCurrentCoverage(item) {{
      return !!item && (
        item.status === "ok"
        || item.status === "covered_by_upstream_hub"
        || item.status === "hub_observed"
        || item.status === "covered_by_browser"
        || (item.status === "browser_observed" && item.currentCoverage)
        || (item.status === "browser_observed_empty" && item.currentCoverage)
      );
    }}

    function providerAdapterForSite(site) {{
      const items = ADAPTER_STATUS.filter(item => item.site === site);
      return items.find(item => item.status === "covered_by_upstream_hub" || item.status === "hub_observed")
        || items.find(item => item.status === "ok")
        || items.find(item => item.status === "hub_error" || item.status === "hub_observed_empty")
        || items.find(item => item.status === "covered_by_browser")
        || items.find(item => item.status === "browser_observed" || item.status === "browser_observed_empty")
        || items[0]
        || null;
    }}

    function providerStatusIssue(item) {{
      if (!item) return "";
      const detail = String(item.detail || "");
      if (/preserved previous account lines|preserved previous non-empty snapshot|fresh_account_lines\\s*=\\s*0/.test(detail)) {{
        return "本轮页面没有抓到新的账号行，导入层只保留了上一轮账号行作审计兜底；Provider 当前页不会展示这些旧行。";
      }}
      if (/script\\s*=\\s*0\\.1\\.(?:0|1|2|3|4|5|6|7|8|9)|script\\s*=\\s*0\\.1\\.1[0-3]/.test(detail)) {{
        return "本轮快照来自旧版油猴脚本，可能只抓到局部账号行；请先更新脚本后重新刷新页面。";
      }}
      if (/wait_state\\s*=\\s*timeout/.test(detail)) {{
        return "本轮快照等待超时，可能只抓到页面局部内容；需要重新刷新页面并等待稳定后再看。";
      }}
      if (/partial account snapshot/.test(detail)) {{
        return "本轮账号行少于上一轮，疑似虚拟表格/分页只抓到局部；Provider 当前页不把它当完整清单。";
      }}
      const reported = Number(item && item.freshAccountCount || 0);
      const extracted = Number(item && item.rawAccountCount || 0);
      if (reported > extracted) {{
        return `页面报告 ${{reported}} 行，采集器只提取 ${{extracted}} 条有效账号对象；本轮不作为完整上游清单。`;
      }}
      if (/account_lines\\s*=\\s*0/.test(detail)) {{
        return "本轮页面账号行为 0，通常是页面未打开到密钥页、表格仍在加载、登录态失效或虚拟表格未渲染当前行。";
      }}
      return "";
    }}

    function providerDiagnosticMeta(item) {{
      if (!item) return {{ label: "未记录", tone: "warn", note: "没有 upstream-hub/public/诊断 adapter 状态。" }};
      if (item.sourceState === "legacy_script") {{
        return {{
          label: "旧脚本",
          tone: "warn",
          note: `本轮采集来自旧版油猴脚本（当前 ${{
            item.scriptVersion || "未知"
          }}，最低要求 ${{
            item.minimumScriptVersion || "0.1.15"
          }}），可能只抓到局部/过期行；请先更新脚本再重抓。`,
        }};
      }}
      if (item.sourceState === "browser_mismatch") {{
        return {{
          label: "浏览器错配",
          tone: "warn",
          note: "状态记录和快照记录来自不同浏览器或旧浏览器残留，当前 Provider 表只认同浏览器同源的最新快照。",
        }};
      }}
      if (item.sourceState === "unstable_snapshot") {{
        return {{
          label: "不稳定",
          tone: "warn",
          note: "本轮快照等待未稳定完成，可能只反映虚拟表格可见区；不作为完整上游清单。",
        }};
      }}
      if (item.sourceState === "partial_snapshot") {{
        return {{
          label: "局部快照",
          tone: "warn",
          note: `本轮账号行少于上一轮（本轮 ${{item.freshAccountCount ?? "未知"}} / 展示 ${{item.displayedAccountCount ?? 0}}），疑似只抓到分页/虚拟表格可见区；不作为完整上游清单。`,
        }};
      }}
      if (item.sourceState === "extraction_loss") {{
        return {{
          label: "采集损耗",
          tone: "warn",
          note: `页面报告 ${{item.freshAccountCount ?? "未知"}} 行，采集器只提取 ${{item.rawAccountCount ?? 0}} 条账号对象；这说明本轮解析不完整，只进诊断，不参与 Provider 当前表或分组消失判断。`,
        }};
      }}
      if (item.sourceState === "hidden_preserved") {{
        return {{
          label: "隐藏旧行",
          tone: "warn",
          note: "本轮没有抓到新的账号行，旧账号快照只保留作审计兜底，不在 Provider 表展示。",
        }};
      }}
      if (item.sourceState === "filtered") {{
        return {{
          label: "已过滤",
          tone: "warn",
          note: "快照里有原始行，但清洗后判断为额度/状态/噪声行，不作为上游账号展示。",
        }};
      }}
      if (item.sourceState === "stale_snapshot") {{
        return {{
          label: "已过期",
          tone: "warn",
          note: "本轮快照时间已超出可用窗口，不能当作当前上游清单。",
        }};
      }}
      if (item.sourceState === "misaligned_snapshot") {{
        return {{
          label: "时间不齐",
          tone: "warn",
          note: "状态时间和快照时间不一致，怀疑抓取/同步错位；不能作为当前清单。",
        }};
      }}
      if (item.sourceState === "current") {{
        return {{
          label: "当前快照",
          tone: "ok",
          note: "本轮上游页面抓到了可展示账号行。",
        }};
      }}
      return {{
        label: "页面空",
        tone: "warn",
        note: "本轮没有识别到账号行，通常需要打开密钥页、刷新页面或等待表格加载。",
      }};
    }}

    function providerDiagnosticMatchesSearch(item, q) {{
      if (!q) return true;
      return [
        item.provider, item.site, item.status, item.detail, item.pageUrl,
        item.pageTitle, item.balanceLabel, item.sourceState
      ].join(" ").toLowerCase().includes(q);
    }}

    function renderProviderDiagnostics() {{
      if (!providerDiagnostics) return;
      const q = (providerSearch && providerSearch.value || "").trim().toLowerCase();
      const items = PROVIDER_DIAGNOSTICS
        .filter(item => providerDiagnosticMatchesSearch(item, q))
        .sort((a, b) => {{
          const am = providerDiagnosticMeta(a);
          const bm = providerDiagnosticMeta(b);
          return toneScore(am.tone) - toneScore(bm.tone)
            || String(a.provider || "").localeCompare(String(b.provider || ""), "zh-Hans-CN")
            || String(a.site || "").localeCompare(String(b.site || ""), "zh-Hans-CN");
        }});
      const currentCount = items.filter(item => item.sourceState === "current" && Number(item.displayedAccountCount || 0) > 0).length;
      const hiddenCount = items.length - currentCount;
      const shouldOpen = q ? " open" : "";
      const header = `
        <details${{shouldOpen}}>
          <summary>
            <span>未入表采集诊断 <span class="provider-diagnostics-summary-note">默认折叠；非上游账号清单，只解释旧脚本/局部/保留快照为什么没有进入下方当前表格</span></span>
            <span class="badge info">诊断 ${{items.length}} · 隐藏 ${{hiddenCount}}</span>
          </summary>
          <div class="provider-diagnostics-grid">
      `;
      providerDiagnostics.innerHTML = items.length ? header + items.map(item => {{
        const meta = providerDiagnosticMeta(item);
        const freshness = freshnessSignal([item.observedAt, item.snapshotObservedAt]);
        const freshCount = item.freshAccountCount === null || item.freshAccountCount === undefined ? "未知" : Number(item.freshAccountCount || 0);
        const counts = `页面报告 ${{freshCount}} / 提取 ${{Number(item.rawAccountCount || 0)}} / 清洗 ${{Number(item.cleanAccountCount || 0)}} / 展示 ${{Number(item.displayedAccountCount || 0)}}`;
        const page = [item.pageTitle, item.pageUrl].filter(Boolean).join(" · ");
        return `
          <div class="provider-diagnostic ${{meta.tone}}">
            <div class="provider-diagnostic-head">
              <div>
                <strong>${{esc(item.provider || "未知上游")}}</strong>
                <small>${{esc(item.site || "-")}}</small>
              </div>
              <span class="badge ${{meta.tone === "ok" ? "ok" : "warn"}}">${{esc(meta.label)}}</span>
            </div>
            <div>${{esc(counts)}} · ${{esc(freshness.label)}}</div>
            <div>${{esc(item.balanceLabel || "未记录余额")}}</div>
            <div>${{esc(meta.note)}}</div>
            <small>诊断信息，不入 Provider 当前账号清单 · ${{esc(page || item.detail || "")}}</small>
          </div>
        `;
      }}).join("") + `</div></details>` : `<details${{shouldOpen}} class="provider-diagnostics"><summary><span>未入表采集诊断</span><span class="provider-diagnostics-summary-note">没有匹配项</span></summary><div class="provider-diagnostics-grid"><div class="provider-diagnostic warn"><strong>没有匹配的采集诊断</strong><div>清空搜索词后可查看所有上游站点本轮抓取状态。</div></div></div></details>`;
    }}

    function renderProviderNotices(rows) {{
      if (!providerNotices) return;
      const q = (providerSearch && providerSearch.value || "").trim().toLowerCase();
      const rowSites = new Set(rows.map(row => row.site));
      const items = ADAPTER_STATUS
        .filter(item => item.status === "browser_observed" || item.status === "browser_observed_empty")
        .filter(item => !q || [item.provider, item.site, item.detail].join(" ").toLowerCase().includes(q) || rowSites.has(item.site))
        .map(item => {{
          const issue = providerStatusIssue(item);
          if (!issue) return null;
          return `<div class="provider-notice"><strong>${{esc(item.provider)}} / ${{esc(item.site)}}</strong>${{esc(issue)}} <span class="muted">${{esc(item.observedAt || "")}}</span></div>`;
        }})
        .filter(Boolean);
      providerNotices.innerHTML = items.join("");
    }}

    function renderSiteMatrix() {{
      if (!siteMatrix) return;
      const grouped = groupProviderObservationsBySite();
      const nodes = [...grouped.entries()].map(([site, rows]) => {{
        const adapterItem = providerAdapterForSite(site);
        const diagnostic = providerDiagnosticForSite(site);
        const diagnosticMeta = providerDiagnosticMeta(diagnostic);
        const adapterIssue = providerStatusIssue(adapterItem);
        const tone = providerSiteTone(rows);
        const freshness = freshnessSignal([
          ...rows.flatMap(row => [row.observedAt, row.balanceUpdatedAt]),
          diagnostic && diagnostic.observedAt,
          diagnostic && diagnostic.snapshotObservedAt,
        ]);
        const providers = uniq([
          ...rows.map(row => row.provider),
          diagnostic && diagnostic.provider,
          adapterItem && adapterItem.provider
        ].filter(Boolean)).slice(0, 3).join(" / ");
        const balances = uniq([
          ...rows.map(row => row.balanceLabel),
          diagnostic && diagnostic.balanceLabel,
        ].filter(hasUsableBalance));
        const rates = rows.map(row => row.pageRate).filter(value => typeof value === "number" && value > 0);
        const minRate = rates.length ? Math.min(...rates) : null;
        const maxRate = rates.length ? Math.max(...rates) : null;
        const lowBalance = rows.some(row => {{
          const value = parseBalanceNumber(row.balanceLabel);
          return value !== null && value < 100;
        }});
        const accountCount = rows.filter(row => String(row.sourceKind || "").startsWith("browser_account")).length;
        const rawAccountCount = diagnostic ? Number(diagnostic.rawAccountCount || 0) : accountCount;
        const displayedAccountCount = diagnostic ? Number(diagnostic.displayedAccountCount || 0) : accountCount;
        const freshAccountCount = diagnostic && diagnostic.freshAccountCount !== null && diagnostic.freshAccountCount !== undefined
          ? Number(diagnostic.freshAccountCount || 0)
          : rawAccountCount;
        const groupCount = uniq(rows.map(row => row.upstreamGroup).filter(Boolean)).length;
        const unmatched = rows.filter(row => row.matchedLedgerRows === 0).length;
        return {{
          site,
          rows,
          tone: worstTone(adapterIssue ? "warn" : "", diagnosticMeta.tone === "ok" ? "" : diagnosticMeta.tone, tone),
          providers,
          balance: balances[0] || "未记录余额",
          rateRange: minRate === null ? "未识别倍率" : minRate === maxRate ? fmtRate(minRate) : `${{fmtRate(minRate)}} - ${{fmtRate(maxRate)}}`,
          accountCount,
          rawAccountCount,
          displayedAccountCount,
          freshAccountCount,
          groupCount,
          attention: unmatched + (adapterIssue ? 1 : 0) + (diagnosticMeta.tone === "warn" ? 1 : 0),
          lowBalance,
          freshness,
          adapterIssue,
          diagnostic,
          diagnosticMeta,
        }};
      }}).sort((a, b) => {{
        const order = {{ risk: 0, warn: 1, "": 2 }};
        return order[a.tone] - order[b.tone] || b.attention - a.attention || a.site.localeCompare(b.site, "zh-Hans-CN");
      }});
      siteMatrix.innerHTML = nodes.length ? nodes.map((node, index) => `
        <button class="site-node ${{node.tone}}" type="button" data-site-index="${{index}}" aria-label="查看 ${{esc(node.site)}}，${{node.rows.length}} 条，关注 ${{node.attention}} 条">
          <div class="site-node-head">
            <div>
              <strong>${{esc(node.site)}}</strong>
              <small>${{esc(node.providers || "未知上游")}}</small>
            </div>
            <span class="badge ${{node.tone === "risk" ? "risk" : node.tone === "warn" ? "warn" : "ok"}}">${{node.tone === "risk" ? "风险" : node.tone === "warn" ? "观察" : "稳定"}}</span>
          </div>
          <div class="site-node-metrics">
            <span>展示 ${{node.displayedAccountCount}} / 提取 ${{node.rawAccountCount}} / 页面 ${{node.freshAccountCount}}</span>
            <span>${{node.groupCount}} 组别</span>
            <span>${{esc(node.rateRange)}}</span>
          </div>
          <div class="site-node-foot">
            <span class="freshness-pill ${{node.freshness.tone}}">${{esc(node.freshness.label)}}</span>
            <span class="site-node-foot-note">${{esc(node.diagnosticMeta.label)}} · ${{esc(node.balance)}}${{node.lowBalance ? " · 低余额" : ""}}</span>
          </div>
        </button>
      `).join("") : `<div class="site-node"><strong>暂无当前上游观察</strong><div class="site-node-foot">运行 upstream-hub 同步或公开 pricing 刷新后显示。</div></div>`;
      for (const button of siteMatrix.querySelectorAll(".site-node[data-site-index]")) {{
        const node = nodes[Number(button.dataset.siteIndex)];
        button.addEventListener("click", () => {{
          if (providerSearch) providerSearch.value = node.site;
          renderProviderObservations();
          showRoute("providers");
        }});
      }}
    }}

    function fillSelect(select, values) {{
      for (const value of values) {{
        const option = document.createElement("option");
        option.value = value;
        option.textContent = value;
        select.appendChild(option);
      }}
    }}

    fillSelect(categoryFilter, uniq(DATA.map(row => row.category)));
    fillSelect(kindFilter, uniq(DATA.map(row => row.kind)));
    fillSelect(statusFilter, uniq(DATA.map(row => row.status)));

    function currentRows() {{
      const q = search.value.trim().toLowerCase();
      return DATA.filter(row => {{
        const haystack = [
          row.category, row.kind, row.site, row.fluterAccountName,
          row.upstreamGroup, row.status, row.note, row.siteGroupMultiplier
          , row.balanceLabel, row.actualCostLabel
        ].join(" ").toLowerCase();
        const balanceValue = parseBalanceNumber(row.balanceLabel);
        const quickMatched =
          !quickMode
          || (quickMode === "attention" && badgeClass(row.status) === "risk")
          || (quickMode === "lowBalance" && balanceValue !== null && balanceValue < 100)
          || (quickMode === "drift" && isCostRecordDrift(row))
          || (quickMode === "kbq" && row.category === "KBQ")
          || (quickMode === "image" && row.kind.includes("生图"));
        return (!q || haystack.includes(q))
          && (!categoryFilter.value || row.category === categoryFilter.value)
          && (!kindFilter.value || row.kind === kindFilter.value)
          && (!statusFilter.value || row.status === statusFilter.value)
          && quickMatched;
      }});
    }}

    function renderCards(rows) {{
      const confirmed = rows.filter(row => row.status === "已确认" || row.status === "已覆盖" || row.status === "偏保守").length;
      const needsAttention = rows.filter(row => badgeClass(row.status) === "risk").length;
      const kingdom = rows.find(row => row.site.includes("tokenskingdom") && row.rechargeFactor !== 1);
      const balanceCount = new Set(BALANCE_SNAPSHOTS.filter(row => hasUsableBalance(row.balanceLabel)).map(balanceGroupKey)).size;
      const minCost = rows
        .map(row => row.actualCostMultiplier)
        .filter(value => typeof value === "number" && value > 0)
        .sort((a, b) => a - b)[0];
      const cards = [
        ["记录数", rows.length, "当前筛选下的台账条目"],
        ["已确认/覆盖", confirmed, "页面倍率与我站记录大体一致"],
        ["需关注", needsAttention, "成本接近、记录不一致或未确认"],
        ["余额记录", balanceCount, "只记录页面明确显示的余额"],
        ["最低成本", fmtRate(minCost), kingdom ? "Kingdom 按充值折算已计入" : "按当前筛选计算"],
      ];
      document.querySelector("#cards").innerHTML = cards.map(([label, value, hint]) => `
        <div class="card"><div class="label">${{esc(label)}}</div><div class="value">${{esc(value)}}</div><div class="hint">${{esc(hint)}}</div></div>
      `).join("");
      document.querySelector("#visibleCount").textContent = `显示 ${{rows.length}} / ${{DATA.length}}`;
      if (sideVisibleCount) sideVisibleCount.textContent = `当前显示 ${{rows.length}} / ${{DATA.length}} 条`;
    }}

    function renderFreshnessStrip() {{
      const box = document.querySelector("#freshnessStrip");
      if (!box) return;
      const metaEntries = Object.entries(META || {{}})
        .filter(([key, value]) => value && /(updated|observed|snapshot|pricing|refresh|run|audit)/i.test(key))
        .slice(0, 6);
      const latestAdapter = ADAPTER_STATUS
        .map(item => item.observedAt)
        .filter(Boolean)
        .sort()
        .at(-1);
      const items = [
        ["页面生成", GENERATED_AT, "静态 HTML 渲染时间"],
        ["KBQ 价格版本", META.kbq_pricing_version || "未记录", "公开 pricing adapter"],
        ["最近采集快照", latestAdapter || "未记录", "upstream-hub / public pricing / 诊断 adapter"],
        ...metaEntries.slice(0, 3).map(([key, value]) => [key, value, "metadata"]),
      ];
      box.innerHTML = items.map(([label, value, hint]) => `
        <div class="freshness-item">
          <span>${{esc(label)}}</span>
          <strong>${{esc(value)}}</strong>
          <span>${{esc(hint)}}</span>
        </div>
      `).join("");
    }}

    function automationIssueCount() {{
      return ADAPTER_STATUS.filter(item => !adapterHasCurrentCoverage(item)).length;
    }}

    function kbqLossCount() {{
      return KBQ_AUDIT ? Number(KBQ_AUDIT.realLossBucketCount || 0) : 0;
    }}

    function healthTone(value, warnAt = 1) {{
      if (value >= warnAt) return "risk";
      return "";
    }}

    function routeForQuickMode(mode) {{
      if (mode === "lowBalance") return "balance";
      if (mode === "kbq") return "risk";
      if (mode === "image") return "image";
      return "accounts";
    }}

    function applyQuickFilter(mode, route = routeForQuickMode(mode), clearCategory = true) {{
      setQuickMode(mode);
      if (clearCategory || mode === "kbq") categoryFilter.value = "";
      render();
      showRoute(route);
    }}

    function applyCategoryFilter(category) {{
      setQuickMode("");
      categoryFilter.value = category || "";
      render();
      showRoute("accounts");
    }}

    function renderHealthCards(rows) {{
      if (!healthCards) return;
      const lowBalance = new Set(BALANCE_SNAPSHOTS.filter(row => {{
        const value = parseBalanceNumber(row.balanceLabel);
        return value !== null && value < 100;
      }}).map(balanceGroupKey)).size;
      const attention = DATA.filter(row => badgeClass(row.status) === "risk").length;
      const drift = DATA.filter(isCostRecordDrift).length;
      const loss = kbqLossCount();
      const autoIssues = automationIssueCount();
      const items = [
        {{
          label: "KBQ 真倒挂",
          value: loss,
          hint: "真实成本审计结果",
          tone: healthTone(loss),
          onClick: () => applyQuickFilter("kbq", "risk"),
        }},
        {{
          label: "低余额",
          value: lowBalance,
          hint: "余额低于 100 的上游余额组",
          tone: healthTone(lowBalance),
          onClick: () => applyQuickFilter("lowBalance", "balance"),
        }},
        {{
          label: "倍率漂移",
          value: drift,
          hint: "账号成本记录与真实成本偏离",
          tone: drift ? "warn" : "",
          onClick: () => applyQuickFilter("drift", "accounts"),
        }},
        {{
          label: "自动化异常",
          value: autoIssues,
          hint: "待补 adapter 或刷新失败",
          tone: autoIssues ? "warn" : "",
          onClick: () => showRoute("automation"),
        }},
      ];
      healthCards.innerHTML = items.map((item, index) => `
        <button class="health-card ${{item.tone}}" type="button" data-health="${{index}}" aria-label="${{esc(item.label)}}：${{esc(item.value)}}，${{esc(item.hint)}}">
          <div>
            <strong>${{esc(item.value)}}</strong>
            <span class="health-label">${{esc(item.label)}}</span>
            <span class="health-hint">${{esc(item.hint)}}</span>
          </div>
          <i class="health-dot" aria-hidden="true"></i>
        </button>
      `).join("");
      for (const button of healthCards.querySelectorAll(".health-card")) {{
        const item = items[Number(button.dataset.health)];
        button.addEventListener("click", item.onClick);
      }}
    }}

    function currentAdapterCoverage() {{
      const covered = ADAPTER_STATUS.filter(adapterHasCurrentCoverage).length;
      return {{
        covered,
        total: ADAPTER_STATUS.length,
        issues: Math.max(ADAPTER_STATUS.length - covered, 0),
      }};
    }}

    function providerObservationSummary() {{
      const grouped = groupProviderObservationsBySite();
      const sites = grouped.size;
      const rates = PROVIDER_OBSERVATIONS.filter(row => typeof row.pageRate === "number" && row.pageRate > 0).length;
      return {{ sites, rates }};
    }}

    function overviewItemTemplate(item, index) {{
      const tag = item.route ? "button" : "div";
      const routeAttrs = item.route ? ` type="button" data-overview-route="${{esc(item.route)}}" data-overview-mode="${{esc(item.mode || "")}}"` : "";
      return `
        <${{tag}} class="overview-item ${{item.tone || ""}}"${{routeAttrs}}>
          <strong>${{esc(item.title)}}</strong>
          <span>${{esc(item.detail)}}</span>
        </${{tag}}>
      `;
    }}

    function renderOverviewFocus(rows) {{
      if (!overviewFocusList) return;
      const lowBalanceRows = DATA.filter(row => {{
        const value = parseBalanceNumber(row.balanceLabel);
        return value !== null && value < 100;
      }});
      const driftRows = DATA.filter(isCostRecordDrift);
      const adapterCoverage = currentAdapterCoverage();
      const providerSummary = providerObservationSummary();
      const loss = kbqLossCount();
      const latestAudit = KBQ_AUDIT
        ? `KBQ 最近 ${{KBQ_AUDIT.hours}}h：请求 ${{KBQ_AUDIT.requestCount}}，利润 ${{fmtNumber(KBQ_AUDIT.margin)}}，REAL_LOSS ${{KBQ_AUDIT.realLossBucketCount}}`
        : "KBQ 审计未生成";
      const items = [
        {{
          title: loss ? "KBQ 有真实倒挂" : "KBQ 未见真实倒挂",
          detail: latestAudit,
          tone: loss ? "risk" : "",
          route: "risk",
          mode: "kbq",
          weight: loss ? 0 : 30,
        }},
        {{
          title: lowBalanceRows.length ? `低余额 ${{new Set(lowBalanceRows.map(balanceGroupKey)).size}} 组` : "余额正常",
          detail: lowBalanceRows.length
            ? `${{new Set(lowBalanceRows.map(balanceGroupKey)).size}} 个余额组低于 100，先看余额雷达`
            : "当前未发现余额低于 100 的上游余额组",
          tone: lowBalanceRows.length ? "warn" : "",
          route: "balance",
          mode: lowBalanceRows.length ? "lowBalance" : "",
          weight: lowBalanceRows.length ? 4 : 34,
        }},
        {{
          title: driftRows.length ? "账号成本记录需复核" : "账号成本记录平稳",
          detail: driftRows.length
            ? `${{driftRows.length}} 条账号成本记录与真实成本偏离，适合进入账号倍率页核对`
            : "当前没有明显账号成本倍率漂移",
          tone: driftRows.length ? "warn" : "",
          route: "accounts",
          mode: driftRows.length ? "drift" : "",
          weight: driftRows.length ? 6 : 36,
        }},
        {{
          title: adapterCoverage.issues ? "采集源有缺口" : "采集源覆盖正常",
          detail: `${{adapterCoverage.covered}} / ${{adapterCoverage.total}} 个 adapter 当前覆盖；upstream-hub 观察 ${{providerSummary.sites}} 个站点、${{providerSummary.rates}} 条倍率`,
          tone: adapterCoverage.issues ? "warn" : "",
          route: "automation",
          weight: adapterCoverage.issues ? 8 : 38,
        }},
      ].sort((a, b) => a.weight - b.weight).slice(0, 5);
      if (overviewFocusCount) {{
        const urgent = items.filter(item => item.tone === "risk" || item.tone === "warn").length;
        overviewFocusCount.textContent = urgent ? `关注 ${{urgent}}` : "无急项";
      }}
      overviewFocusList.innerHTML = items.map(overviewItemTemplate).join("");
      for (const button of overviewFocusList.querySelectorAll("[data-overview-route]")) {{
        button.addEventListener("click", () => {{
          const mode = button.dataset.overviewMode || "";
          if (mode) applyQuickFilter(mode, button.dataset.overviewRoute || routeForQuickMode(mode));
          else showRoute(button.dataset.overviewRoute || "overview");
        }});
      }}
    }}

    function renderOverviewSources() {{
      if (!overviewSourceList) return;
      const adapterCoverage = currentAdapterCoverage();
      const providerSummary = providerObservationSummary();
      const sourceItems = [
        {{
          title: "upstream-hub",
          detail: `登录态采集余额和倍率：${{providerSummary.sites}} 个站点，${{providerSummary.rates}} 条倍率。KBQ/聪明AI 若 hub 失效，可手动看或用 KBQ 公开接口兜底。`,
          tone: adapterCoverage.issues ? "warn" : "",
        }},
        {{
          title: "KBQ /api/pricing",
          detail: `脚本 refresh_kbq_token_models.py 定时拉公开价格接口；AI 只读取 SQLite 结果，不是临时看网页。`,
          tone: "",
        }},
        {{
          title: "生产站只读快照",
          detail: "VPS 每小时只读本站账号倍率、分组售价和 usage_logs，用于判断真实成本是否覆盖用户扣费。",
          tone: "info",
        }},
      ];
      overviewSourceList.innerHTML = sourceItems.map(overviewItemTemplate).join("");
    }}

    function renderCategoryNav() {{
      if (!categoryNav) return;
      const grouped = new Map();
      for (const row of DATA) {{
        if (!grouped.has(row.category)) grouped.set(row.category, []);
        grouped.get(row.category).push(row);
      }}
      const items = [...grouped.entries()].sort((a, b) => a[0].localeCompare(b[0], "zh-Hans-CN"));
      categoryNav.innerHTML = items.map(([category, rows]) => {{
        const active = categoryFilter.value === category || (quickMode === "kbq" && category === "KBQ");
        return `
          <button class="category-nav-item ${{active ? "active" : ""}}" type="button" data-category="${{esc(category)}}" aria-pressed="${{active ? "true" : "false"}}" aria-label="筛选 ${{esc(category)}}，${{rows.length}} 条">
            <span>${{esc(category)}}</span>
            <span>${{rows.length}}</span>
          </button>
        `;
      }}).join("");
      for (const button of categoryNav.querySelectorAll(".category-nav-item")) {{
        button.addEventListener("click", () => applyCategoryFilter(button.dataset.category));
      }}
    }}

    function renderAccountInspector(rows) {{
      if (!accountInspectorBody) return;
      const focusRows = rows.slice().sort((a, b) => {{
        const aFreshness = freshnessSignal([a.updatedAt, a.balanceUpdatedAt]);
        const bFreshness = freshnessSignal([b.updatedAt, b.balanceUpdatedAt]);
        const aTone = Math.min(toneScore(badgeClass(a.status)), toneScore(aFreshness.tone));
        const bTone = Math.min(toneScore(badgeClass(b.status)), toneScore(bFreshness.tone));
        const aDrift = isCostRecordDrift(a) ? 1 : 0;
        const bDrift = isCostRecordDrift(b) ? 1 : 0;
        const aBalance = parseBalanceNumber(a.balanceLabel);
        const bBalance = parseBalanceNumber(b.balanceLabel);
        const aBalanceScore = aBalance === null ? 9999 : aBalance;
        const bBalanceScore = bBalance === null ? 9999 : bBalance;
        return aTone - bTone || bDrift - aDrift || aBalanceScore - bBalanceScore || a.fluterAccountName.localeCompare(b.fluterAccountName, "zh-Hans-CN");
      }}).slice(0, 5);
      accountInspectorBody.innerHTML = focusRows.length ? focusRows.map((row, index) => {{
        const key = rowKey(row);
        const selected = inspectorRowKey === key || index === 0 && !inspectorRowKey;
        const freshness = freshnessSignal([row.updatedAt, row.balanceUpdatedAt]);
        return `
          <div class="inspector-row">
            <span>${{esc(selected ? "当前焦点" : "建议关注")}}</span>
            <strong>${{esc(row.fluterAccountName)}}</strong>
            <div>${{esc(row.site)}} · ${{esc(row.upstreamGroup || "-")}} · ${{esc(row.kind)}}</div>
            <div>真实成本 ${{esc(row.actualCostLabel || fmtRate(row.actualCostMultiplier))}} · 账号成本 ${{esc(fmtRate(row.siteAccountMultiplier))}} · 售价 ${{esc(row.siteGroupMultiplier || "-")}}</div>
            <div>余额 ${{esc(row.balanceLabel || "未记录")}} · 状态 ${{esc(row.status)}} · ${{esc(row.note || "无备注")}}</div>
            <div class="inspector-age"><span class="freshness-pill ${{freshness.tone}}">新鲜度 ${{esc(freshness.label)}}</span></div>
            <button class="inspect-mini" type="button" data-inspect="${{esc(key)}}">锁定此条</button>
          </div>
        `;
      }}).join("") : `<div class="inspector-row"><span>暂无可视条目</span><strong>未记录</strong><div>调整筛选后会显示 5 条最需要看的账号。</div></div>`;
      for (const button of accountInspectorBody.querySelectorAll("[data-inspect]")) {{
        button.addEventListener("click", () => {{
          inspectorRowKey = button.dataset.inspect || "";
          renderAccountInspector(rows);
        }});
      }}
    }}

    function renderBalanceStrip() {{
      const grouped = new Map();
      for (const snapshot of BALANCE_SNAPSHOTS) {{
        if (!hasUsableBalance(snapshot.balanceLabel)) continue;
        const balanceValue = parseBalanceNumber(snapshot.balanceLabel);
        if (quickMode === "lowBalance" && (balanceValue === null || balanceValue >= 100)) continue;
        const item = {{
          category: snapshot.provider || snapshot.site,
          site: snapshot.site,
          balanceLabel: snapshot.balanceLabel,
          balanceUpdatedAt: snapshot.balanceUpdatedAt,
        }};
        grouped.set(balanceGroupKey(item), item);
      }}
      for (const row of DATA) {{
        if (!hasUsableBalance(row.balanceLabel)) continue;
        const balanceValue = parseBalanceNumber(row.balanceLabel);
        if (quickMode === "lowBalance" && (balanceValue === null || balanceValue >= 100)) continue;
        const key = balanceGroupKey(row);
        if (!grouped.has(key)) grouped.set(key, {{
          category: row.category,
          site: row.site,
          balanceLabel: row.balanceLabel,
          balanceUpdatedAt: row.balanceUpdatedAt,
        }});
        else {{
          const item = grouped.get(key);
          if (item.site !== row.site && !item.site.includes(row.site)) item.site = `${{item.site}} / ${{row.site}}`;
        }}
      }}
      const items = [...grouped.values()].sort((a, b) => {{
        const av = parseBalanceNumber(a.balanceLabel);
        const bv = parseBalanceNumber(b.balanceLabel);
        if (av === null && bv === null) return a.category.localeCompare(b.category, "zh-Hans-CN");
        if (av === null) return 1;
        if (bv === null) return -1;
        return av - bv;
      }});
      document.querySelector("#balanceStrip").innerHTML = items.length ? items.map(item => `
        <div class="balance-chip ${{balanceClass(item)}}">
          <strong>${{esc(item.category)}}</strong>
          <div class="balance-value">${{esc(item.balanceLabel)}}</div>
          <div class="muted">${{esc(item.site)}} · ${{esc(item.balanceUpdatedAt)}}</div>
        </div>
      `).join("") : `<div class="balance-chip"><strong>暂无余额</strong><div class="balance-value">未记录</div><div class="muted">刷新 upstream-hub 后显示。</div></div>`;
    }}

    function adapterClass(item) {{
      if (!item) return "needs";
      if (adapterHasCurrentCoverage(item)) return "ok";
      if (item.status === "failed" || item.status === "hub_error") return "failed";
      return "needs";
    }}

    function adapterLabel(item) {{
      if (!item) return "未知";
      if (item.status === "ok") return "已自动化";
      if (item.status === "covered_by_upstream_hub" || item.status === "hub_observed") return "hub 已覆盖";
      if (item.status === "hub_error") return "hub 异常";
      if (item.status === "hub_observed_empty") return "hub 无倍率";
      if (item.status === "failed") return "刷新失败";
      if (item.status === "covered_by_browser") return "诊断快照已覆盖";
      if (item.status === "needs_adapter") return "待补 adapter";
      if (item.status === "browser_observed") return item.currentCoverage ? "诊断页已读" : "旧诊断快照";
      if (item.status === "browser_observed_empty") return item.currentCoverage ? "诊断页无正文" : "旧空诊断页";
      if (item.status === "needs_browser_tab") return "待诊断页面";
      return item.status || "未知";
    }}

    function renderAdapterStatus() {{
      const box = document.querySelector("#adapterStatusCards");
      if (!box) return;
      box.innerHTML = ADAPTER_STATUS.length ? ADAPTER_STATUS.map(item => `
        <div class="adapter-chip ${{adapterClass(item)}}">
          <div class="adapter-head">
            <strong>${{esc(item.provider)}}</strong>
            <span class="badge ${{adapterClass(item) === "ok" ? "ok" : adapterClass(item) === "failed" ? "risk" : "info"}}">${{esc(adapterLabel(item))}}</span>
          </div>
          <div>${{esc(item.site)}}</div>
          <div class="muted">${{esc(item.adapterKind)}} · ${{esc(item.observedAt || "-")}}</div>
          <div class="muted">${{esc(item.detail || "")}}</div>
        </div>
      `).join("") : `<div class="adapter-chip"><strong>暂无状态</strong><div class="muted">运行 upstream-hub 同步或 public adapter 刷新后显示。</div></div>`;
    }}

    function providerSourceLabel(row) {{
      return row.sourceLabel || row.sourceKind || "只读观察";
    }}

    function providerMatchLabel(row) {{
      if (String(row.sourceKind || "").startsWith("browser_account") && (row.matchedLedgerRows === null || row.matchedLedgerRows === undefined)) return "当前快照";
      if (row.matchedLedgerRows === null || row.matchedLedgerRows === undefined) return "未匹配";
      if (row.matchedLedgerRows === 0) return "台账未接入/待映射";
      return `命中 ${{row.matchedLedgerRows}} 条`;
    }}

    function providerMatchBadge(row) {{
      if (row.matchedLedgerRows === 0) return "warn";
      if (row.matchedLedgerRows > 0) return "ok";
      return "info";
    }}

    function currentProviderObservations() {{
      const q = (providerSearch && providerSearch.value || "").trim().toLowerCase();
      return PROVIDER_OBSERVATIONS.filter(row => {{
        const haystack = [
          row.provider, row.site, row.accountName, row.upstreamGroup,
          row.sourceLabel, row.sourceKind, row.sourceLine, row.balanceLabel,
          row.observedAt, row.scriptVersion, row.minimumScriptVersion
        ].join(" ").toLowerCase();
        return !q || haystack.includes(q);
      }});
    }}

    function renderProviderObservations() {{
      if (!providerRows) return;
      const rows = currentProviderObservations();
      if (providerObservationCount) {{
        providerObservationCount.textContent = `显示 ${{rows.length}} / ${{PROVIDER_OBSERVATIONS.length}} 条`;
      }}
      renderProviderDiagnostics();
      renderProviderNotices(rows);
      const hiddenDiagnosticCount = PROVIDER_DIAGNOSTICS.filter(item => item.sourceState !== "current" || Number(item.displayedAccountCount || 0) === 0).length;
      providerRows.innerHTML = rows.length ? rows.map(row => {{
        const freshness = freshnessSignal([row.observedAt, row.balanceUpdatedAt]);
        const balanceLine = [row.balanceLabel, row.balanceUpdatedAt].filter(Boolean).join(" · ") || "未记录余额";
        const scriptBadge = row.scriptVersion && row.scriptVersion !== row.minimumScriptVersion
          ? `<span class="badge warn">脚本 ${{esc(row.scriptVersion)}} / 要求 ${{esc(row.minimumScriptVersion || "0.1.15")}}</span>`
          : `<span class="badge ${{freshness.tone || "info"}}">${{esc(freshness.label)}}</span>`;
        return `
          <tr>
            <td>${{stackCell([detailCell(row.provider, {{ label: "上游名称", lines: 1, threshold: 28 }}), scriptBadge])}}</td>
            <td>${{detailCell(row.site, {{ label: "上游站点", lines: 1, threshold: 32 }})}}</td>
            <td>${{detailCell(row.accountName || "未识别账号名", {{ label: "上游账号名", lines: 2, threshold: 42 }})}}</td>
            <td>${{detailCell(row.upstreamGroup || "-", {{ label: "上游组别", lines: 2, threshold: 38 }})}}</td>
            <td class="rate">${{esc(fmtRate(row.pageRate))}}</td>
            <td>${{detailCell(providerSourceLabel(row), {{ label: "观察来源", lines: 1, threshold: 30 }})}}</td>
            <td><span class="badge ${{providerMatchBadge(row)}}">${{esc(providerMatchLabel(row))}}</span></td>
            <td>${{detailCell(balanceLine, {{ label: "余额和抓取时间", lines: 2, threshold: 44 }})}}</td>
            <td class="note">${{detailCell(row.sourceLine || "无来源摘要", {{ label: "抓取证据", lines: 2, threshold: 62 }})}}</td>
          </tr>
        `;
      }}).join("") : `<tr class="empty-row"><td colspan="9">没有当前可信上游账号快照。旧脚本、保留快照、局部快照和历史台账不会回退显示在这里；${{hiddenDiagnosticCount ? `已有 ${{hiddenDiagnosticCount}} 条被折叠到“未入表采集诊断”。` : "请先运行新版油猴脚本并刷新目标上游密钥页。"}}</td></tr>`;
    }}

    function setQuickMode(mode) {{
      quickMode = mode || "";
      for (const item of document.querySelectorAll("#quickFilters button")) {{
        const active = (item.dataset.quick || "") === quickMode;
        item.classList.toggle("active", active);
        item.setAttribute("aria-pressed", active ? "true" : "false");
      }}
    }}

    function renderCategoryCards(rows) {{
      const grouped = new Map();
      for (const row of DATA) {{
        if (!grouped.has(row.category)) grouped.set(row.category, []);
        grouped.get(row.category).push(row);
      }}
      document.querySelector("#categoryCards").innerHTML = [...grouped.entries()].map(([category, items]) => {{
        const risky = items.filter(item => badgeClass(item.status) === "risk").length;
        const balances = uniq(items.map(item => item.balanceLabel).filter(value => value && !value.includes("未显示")));
        const actuals = items.map(item => item.actualCostMultiplier).filter(value => typeof value === "number");
        const min = actuals.length ? fmtRate(Math.min(...actuals)) : "需人工";
        const active = categoryFilter.value === category || (quickMode === "kbq" && category === "KBQ");
        const balanceText = balances.join(" / ") || "余额未看到";
        return `<button class="category-card ${{active ? "active" : ""}}" type="button" data-category="${{esc(category)}}" aria-pressed="${{active ? "true" : "false"}}" aria-label="筛选 ${{esc(category)}}，${{items.length}} 条记录"><strong>${{esc(category)}}</strong><span>${{esc(balanceText)}}</span><div class="cat-meta"><span>${{items.length}} 条</span><span class="${{risky ? "risk-chip" : "ok-chip"}}">关注 ${{risky}}</span><span>最低 ${{esc(min)}}</span></div></button>`;
      }}).join("");
      for (const button of document.querySelectorAll(".category-card")) {{
        button.addEventListener("click", () => {{
          setQuickMode("");
          categoryFilter.value = button.dataset.category;
          render();
          showRoute("accounts", {{ resetScroll: false }});
        }});
      }}
    }}

    function renderRows(rows) {{
      tbody.innerHTML = rows.length ? rows.map(row => `
        <tr>
          <td><span class="badge ${{badgeClass(row.status)}}">${{esc(row.status)}}</span></td>
          <td>${{stackCell([detailCell(row.fluterAccountName, {{ label: "我站账号命名", lines: 2, threshold: 34 }}), `<span class="muted">${{esc(row.category)}}</span>`])}}</td>
          <td>${{detailCell(row.site, {{ label: "上游站点", lines: 1, threshold: 32 }})}}</td>
          <td>${{detailCell(row.kind, {{ label: "账号类型", lines: 1, threshold: 28 }})}}</td>
          <td>${{detailCell(row.upstreamGroup, {{ label: "上游分组", lines: 2, threshold: 34 }})}}</td>
          <td class="rate">${{esc(fmtRate(row.pageRate))}}</td>
          <td>${{discountCell(row)}}</td>
          <td>${{stackCell([`<span class="rate">${{esc(row.balanceLabel || "未记录")}}</span>`, detailCell(row.balanceUpdatedAt || "", {{ label: "余额更新时间", lines: 1, threshold: 30, empty: "" }})])}}</td>
          <td class="cost-real">${{detailCell(row.actualCostLabel || fmtRate(row.actualCostMultiplier), {{ label: "真实成本", lines: 2, threshold: 30 }})}}</td>
          <td class="rate cost-internal">${{esc(fmtRate(row.siteAccountMultiplier))}}</td>
          <td class="cost-sell">${{detailCell(row.siteGroupMultiplier, {{ label: "用户售价", lines: 2, threshold: 36 }})}}</td>
          <td class="profit-col">${{profitSignalForRow(row)}}</td>
          <td class="note">${{detailCell(row.note, {{ label: "备注", lines: 2, threshold: 58, empty: "无备注" }})}}</td>
        </tr>
      `).join("") : `<tr class="empty-row"><td colspan="13">没有符合当前筛选条件的上游记录。</td></tr>`;
    }}

    function bucketOf(row) {{
      return fmtRate(row.costMultiplier);
    }}

    function renderKbqModels() {{
      if (!categoryDetail) return;
      const panels = [...document.querySelectorAll('[data-kbq-panel]')];
      for (const panel of panels) panel.hidden = panel.dataset.kbqPanel !== kbqMode;
      const categories = uniq(KBQ_MODELS.map(row => row.category));
      if (!categories.includes(kbqState.category)) kbqState.category = categories[0] || "";
      document.querySelector("#kbqCategoryTabs").innerHTML = categories.map(category => `
        <button class="${{category === kbqState.category ? "active" : ""}}" type="button" data-category="${{esc(category)}}" aria-pressed="${{category === kbqState.category ? "true" : "false"}}">${{esc(category)}}</button>
      `).join("");
      for (const button of document.querySelectorAll("#kbqCategoryTabs button")) {{
        button.addEventListener("click", () => {{
          kbqState.category = button.dataset.category;
          kbqState.bucket = "";
          renderKbqModels();
        }});
      }}

      const categoryRows = KBQ_MODELS.filter(row => row.category === kbqState.category);
      const grouped = new Map();
      for (const row of categoryRows) {{
        const bucket = bucketOf(row);
        if (!grouped.has(bucket)) grouped.set(bucket, []);
        grouped.get(bucket).push(row);
      }}
      const buckets = [...grouped.entries()].sort((a, b) => (a[1][0].costMultiplier || 0) - (b[1][0].costMultiplier || 0));
      document.querySelector("#kbqBuckets").innerHTML = buckets.map(([bucket, items]) => {{
        const example = items.slice(0, 2).map(item => item.modelName).join(" / ");
        return `<button class="bucket-card ${{bucket === kbqState.bucket ? "active" : ""}}" type="button" data-bucket="${{esc(bucket)}}" aria-pressed="${{bucket === kbqState.bucket ? "true" : "false"}}" aria-label="筛选 KBQ 成本档位 ${{esc(bucket)}}，${{items.length}} 个模型"><strong>${{esc(bucket)}}</strong><span>${{items.length}} 个模型<br>${{esc(example)}}</span></button>`;
      }}).join("") || `<div class="kbq-note">当前没有 KBQ 按量模型记录。</div>`;
      for (const button of document.querySelectorAll("#kbqBuckets .bucket-card")) {{
        button.addEventListener("click", () => {{
          kbqState.bucket = kbqState.bucket === button.dataset.bucket ? "" : button.dataset.bucket;
          renderKbqModels();
        }});
      }}

      const visibleRows = categoryRows.filter(row => !kbqState.bucket || bucketOf(row) === kbqState.bucket);
      document.querySelector("#kbqMeta").textContent = `${{kbqState.category || "KBQ"}}：${{visibleRows.length}} / ${{categoryRows.length}} 个模型 · version ${{META.kbq_pricing_version || "-"}}`;
      document.querySelector("#kbqRows").innerHTML = visibleRows.length ? visibleRows.map(row => `
        <tr>
          <td><span class="badge ${{row.costMultiplier <= 0.2 ? "ok" : row.costMultiplier <= 0.8 ? "warn" : "risk"}}">${{esc(bucketOf(row))}}</span></td>
          <td>${{stackCell([detailCell(row.modelName, {{ label: "KBQ 上游模型", lines: 2, threshold: 46 }}), detailCell(`基准模型：${{row.baseModel || "-"}}`, {{ label: "官方基准模型", lines: 1, threshold: 42 }})])}}</td>
          <td>${{detailCell(row.endpoints || "-", {{ label: "支持接口", lines: 1, threshold: 34 }})}}</td>
          <td>
            <div>输入 ${{esc(fmtMoney(row.inputUsdPer1M))}} / 输出 ${{esc(fmtMoney(row.outputUsdPer1M))}}</div>
            <div class="muted">缓存读 ${{esc(fmtMoney(row.cacheReadUsdPer1M))}} / 写 ${{esc(fmtMoney(row.cacheWriteUsdPer1M))}}</div>
          </td>
          <td>
            <div>输入 ${{esc(fmtMoney(row.officialInputUsdPer1M))}} / 输出 ${{esc(fmtMoney(row.officialOutputUsdPer1M))}}</div>
            <div class="muted">缓存读 ${{esc(fmtMoney(row.officialCacheReadUsdPer1M))}} / 写 ${{esc(fmtMoney(row.officialCacheWriteUsdPer1M))}}</div>
          </td>
          <td class="rate">${{esc(Number(row.rawModelRatio.toFixed(6)).toString())}}</td>
          <td>${{stackCell([detailCell(row.updatedAt, {{ label: "更新时间", lines: 1, threshold: 28 }}), detailCell(row.sourceUrl, {{ label: "来源 URL", lines: 1, threshold: 34 }})])}}</td>
        </tr>
      `).join("") : `<tr class="empty-row"><td colspan="7">没有符合当前 KBQ 筛选条件的模型记录。</td></tr>`;

      document.querySelector("#kbqPerCallRows").innerHTML = KBQ_PER_CALL_MODELS.length ? KBQ_PER_CALL_MODELS.map(row => `
        <tr>
          <td><span class="badge ${{row.effectivePerCallCost <= 0.03 ? "ok" : row.effectivePerCallCost <= 0.12 ? "warn" : "risk"}}">${{esc(row.category)}}</span></td>
          <td>${{stackCell([detailCell(row.modelName, {{ label: "KBQ 按次模型", lines: 2, threshold: 48 }}), detailCell(`公开模型：${{row.baseModel || "-"}}`, {{ label: "公开模型名", lines: 1, threshold: 42 }})])}}</td>
          <td>${{stackCell([detailCell(row.endpoints || "anthropic/openai", {{ label: "接口", lines: 1, threshold: 34 }}), detailCell(row.tags || "-", {{ label: "标签", lines: 1, threshold: 34 }})])}}</td>
          <td class="rate">${{esc(fmtMoney(row.perCallPrice))}} / 次</td>
          <td><div class="rate">${{esc(fmtMoney(row.effectivePerCallCost))}} / 次</div><div class="muted">充值系数 ${{esc(Number(row.rechargeFactor.toFixed(6)).toString())}}</div></td>
          <td class="note">${{detailCell(row.description || row.note, {{ label: "按次模型说明", lines: 2, threshold: 58, empty: "无说明" }})}}</td>
          <td>${{stackCell([detailCell(row.updatedAt, {{ label: "更新时间", lines: 1, threshold: 28 }}), detailCell(row.sourceUrl, {{ label: "来源 URL", lines: 1, threshold: 34 }})])}}</td>
        </tr>
      `).join("") : `<tr class="empty-row"><td colspan="7">还没有 KBQ Claude 按次模型记录。</td></tr>`;
    }}

    function renderKbqGuide() {{
      const box = document.querySelector("#kbqGuideCards");
      if (!box) return;
      const items = [
        ["账号成本倍率", "内部成本记录", "贴近真实上游成本，不是售价"],
        ["用户售价", "分组倍率 / 单张售价", "判断利润只看它是否覆盖真实成本"],
        ["按次模型", "单次价 × 充值系数", "不要和 token 倍率混用"],
      ];
      box.innerHTML = items.map(([label, value, hint]) => `
        <div class="freshness-item">
          <span>${{esc(label)}}</span>
          <strong>${{esc(value)}}</strong>
          <span>${{esc(hint)}}</span>
        </div>
      `).join("");
    }}

    function auditBadgeClass(status) {{
      if (status === "REAL_LOSS") return "risk";
      if (status === "NO_PRICE" || status === "DISPLAY_DRIFT") return "warn";
      return "ok";
    }}

    function isKbqView() {{
      return categoryFilter.value === "KBQ" || quickMode === "kbq";
    }}

    function renderKbqAudit() {{
      const auditSection = document.querySelector("#kbqAuditSection");
      if (!auditSection) return;
      const summaryEl = document.querySelector("#kbqAuditSummary");
      const rowsEl = document.querySelector("#kbqAuditRows");
      if (!KBQ_AUDIT) {{
        summaryEl.innerHTML = `
          <div class="audit-metric"><span>审计状态</span><strong>暂无记录</strong></div>
          <div class="audit-metric"><span>下一步</span><strong>运行审计脚本</strong></div>
        `;
        rowsEl.innerHTML = `<tr class="empty-row"><td colspan="10">还没有 KBQ 真实成本审计记录。运行 audit_kbq_true_costs.py 后会显示最近一次结果。</td></tr>`;
        return;
      }}
      const lossClass = KBQ_AUDIT.realLossBucketCount > 0 ? "risk" : "ok";
      const marginLabel = `${{fmtNumber(KBQ_AUDIT.margin)}}${{KBQ_AUDIT.marginPercent === null || KBQ_AUDIT.marginPercent === undefined ? "" : ` / ${{fmtNumber(KBQ_AUDIT.marginPercent)}}%`}}`;
      const metrics = [
        ["审计窗口", `${{KBQ_AUDIT.hours}}h`, `version ${{KBQ_AUDIT.pricingVersion || "-"}}`],
        ["请求数", KBQ_AUDIT.requestCount, `${{KBQ_AUDIT.bucketCount}} 个桶`],
        ["用户扣费", fmtNumber(KBQ_AUDIT.userBilledCost), "usage_logs.actual_cost"],
        ["真实成本", fmtNumber(KBQ_AUDIT.trueUpstreamCost), "KBQ pricing 反算"],
        ["利润空间", marginLabel, "用户扣费 - 真实成本"],
        ["真倒挂", KBQ_AUDIT.realLossBucketCount, `展示漂移 ${{KBQ_AUDIT.displayDriftBucketCount}}`],
      ];
      summaryEl.innerHTML = metrics.map(([label, value, hint], index) => `
        <div class="audit-metric ${{index === 5 ? lossClass : ""}}">
          <span>${{esc(label)}}</span>
          <strong>${{esc(value)}}</strong>
          <span>${{esc(hint)}}</span>
        </div>
      `).join("");

      rowsEl.innerHTML = KBQ_AUDIT_BUCKETS.length ? KBQ_AUDIT_BUCKETS.map(row => {{
        const account = `#${{row.accountId}} ${{row.accountName}}`;
        const channel = row.channelId ? `#${{row.channelId}} ${{row.channelName}}` : "-";
        const group = row.groupId ? `#${{row.groupId}} ${{row.groupName}}` : "-";
        return `
          <tr>
            <td>
              <span class="badge ${{auditBadgeClass(row.status)}}">${{esc(row.status)}}</span>
              <div class="muted">${{esc(row.displayStatus)}}</div>
            </td>
            <td>${{stackCell([detailCell(account, {{ label: "审计账号", lines: 1, threshold: 42 }}), detailCell(channel, {{ label: "审计渠道", lines: 1, threshold: 42 }}), detailCell(group, {{ label: "审计分组", lines: 1, threshold: 42 }})])}}</td>
            <td>${{stackCell([detailCell(row.model, {{ label: "请求模型", lines: 1, threshold: 42 }}), detailCell(row.upstreamModel, {{ label: "上游模型", lines: 1, threshold: 42 }})])}}</td>
            <td class="rate">${{esc(row.requestCount)}}</td>
            <td class="rate">${{esc(fmtNumber(row.userBilledCost))}}</td>
            <td class="rate">${{esc(fmtNumber(row.trueUpstreamCost))}}</td>
            <td class="rate">${{esc(fmtNumber(row.margin))}}</td>
            <td class="rate">${{esc(fmtNumber(row.displayedAccountCost))}}</td>
            <td>
              <div>读 ${{esc(row.cacheReadTokens)}}</div>
              <div class="muted">写 ${{esc(row.cacheWriteTokens)}} / 1h ${{esc(row.cacheCreation1hTokens)}}</div>
            </td>
            <td class="note">${{detailCell(row.note, {{ label: "审计备注", lines: 2, threshold: 58, empty: "无备注" }})}}</td>
          </tr>
        `;
      }}).join("") : `<tr class="empty-row"><td colspan="10">最近一次审计没有 KBQ 使用桶。</td></tr>`;
    }}

    function renderCategoryDetail() {{
      renderKbqModels();
    }}

    function lineForRow(row) {{
      const cost = row.actualCostLabel || fmtRate(row.actualCostMultiplier);
      const balance = row.balanceLabel ? `，余额 ${{row.balanceLabel}}` : "";
      return `- ${{row.fluterAccountName}}：${{row.site}} / ${{row.upstreamGroup}}，成本 ${{cost}}，账号成本倍率 ${{fmtRate(row.siteAccountMultiplier)}}，状态 ${{row.status}}${{balance}}`;
    }}

    function extractGroupRates(label) {{
      const rates = [];
      const re = /@([0-9]+(?:\\.[0-9]+)?)/g;
      let match;
      while ((match = re.exec(String(label || ""))) !== null) {{
        const value = Number(match[1]);
        if (Number.isFinite(value)) rates.push(value);
      }}
      return rates;
    }}

    function lowestSellRate(row) {{
      const rates = extractGroupRates(row.siteGroupMultiplier);
      return rates.length ? Math.min(...rates) : null;
    }}

    function parseFirstNumber(label) {{
      const match = String(label || "").replace(/,/g, "").match(/[0-9]+(?:\\.[0-9]+)?/);
      return match ? Number(match[0]) : null;
    }}

    function signalHtml(label, tone) {{
      return `<span class="profit-signal ${{esc(tone)}}">${{esc(label)}}</span>`;
    }}

    function profitSignalForRow(row) {{
      const signal = profitSignalText(row);
      return signalHtml(signal.label, signal.tone);
    }}

    function profitSignalText(row) {{
      if (row.actualCostLabel) {{
        return {{ label: "按单张核对", tone: "info" }};
      }}
      if (!row.actualCostMultiplier) {{
        return {{ label: "成本未确认", tone: "info" }};
      }}
      const sellRate = lowestSellRate(row);
      if (sellRate === null) {{
        return {{ label: "售价未知", tone: "info" }};
      }}
      const coverage = sellRate / row.actualCostMultiplier;
      if (coverage <= 1) return {{ label: "倒挂", tone: "risk" }};
      if (coverage < 1.15) return {{ label: "薄利", tone: "warn" }};
      return {{ label: "覆盖", tone: "ok" }};
    }}

    function imageProfitSignal(row) {{
      const costValue = parseFirstNumber(row.actualCostLabel);
      const sellValue = parseFirstNumber(row.siteGroupMultiplier);
      if (costValue === null || sellValue === null) return signalHtml("需流水核对", "info");
      if (sellValue <= costValue) return signalHtml("倒挂", "risk");
      if (sellValue / costValue < 1.15) return signalHtml("薄利", "warn");
      return signalHtml("覆盖", "ok");
    }}

    function renderImageCosts() {{
      const rows = DATA.filter(row => row.kind.includes("生图"));
      const cards = document.querySelector("#imageCostCards");
      const rowsEl = document.querySelector("#imageCostRows");
      if (!cards || !rowsEl) return;
      const confirmed = rows.filter(row => row.actualCostLabel || row.status.includes("确认") || row.status.includes("覆盖")).length;
      const needsFlow = rows.filter(row => row.status.includes("单张") || row.note.includes("流水") || !row.actualCostLabel).length;
      const usable = rows.filter(row => !row.status.includes("停用") && !row.status.includes("未接入")).length;
      const metrics = [
        ["生图记录", rows.length, "台账中的生图相关账号"],
        ["已核价/可用", confirmed, "有单张成本或明确可用记录"],
        ["仍需流水核对", needsFlow, "需要按实际生成后扣费确认"],
      ];
      cards.innerHTML = metrics.map(([label, value, hint]) => `
        <div class="image-cost-card">
          <span>${{esc(label)}}</span>
          <strong>${{esc(value)}}</strong>
          <span>${{esc(hint)}}</span>
        </div>
      `).join("");
      rowsEl.innerHTML = rows.length ? rows.map(row => `
        <tr>
          <td><span class="badge ${{badgeClass(row.status)}}">${{esc(row.status)}}</span></td>
          <td>${{stackCell([detailCell(row.fluterAccountName, {{ label: "生图账号", lines: 2, threshold: 42 }}), detailCell(row.kind, {{ label: "生图账号类型", lines: 1, threshold: 34 }})])}}</td>
          <td>${{detailCell(row.site, {{ label: "上游站点", lines: 1, threshold: 32 }})}}</td>
          <td>${{detailCell(row.upstreamGroup, {{ label: "上游分组", lines: 2, threshold: 38 }})}}</td>
          <td class="cost-real">${{detailCell(row.actualCostLabel || fmtRate(row.actualCostMultiplier), {{ label: "生图真实成本", lines: 2, threshold: 34 }})}}</td>
          <td class="cost-sell">${{detailCell(row.siteGroupMultiplier || "未记录", {{ label: "生图用户售价", lines: 2, threshold: 36 }})}}</td>
          <td class="profit-col">${{imageProfitSignal(row)}}</td>
          <td class="note">${{detailCell(row.note, {{ label: "生图备注", lines: 2, threshold: 58, empty: "无备注" }})}}</td>
        </tr>
      `).join("") : `<tr class="empty-row"><td colspan="8">当前台账没有生图记录。补充上游账号或运行生图核价后会显示。</td></tr>`;
    }}

    function renderOperationLog() {{
      const box = document.querySelector("#operationLogList");
      if (!box) return;
      const metaItems = Object.entries(META || {{}}).map(([key, value]) => ({{
        title: `metadata：${{key}}`,
        detail: String(value ?? "-"),
      }}));
      const adapterItems = ADAPTER_STATUS.slice(0, 12).map(item => ({{
        title: `${{item.provider}} / ${{adapterLabel(item.status)}}`,
        detail: `${{item.site}} · ${{item.adapterKind}} · ${{item.observedAt || "-"}} · ${{item.detail || ""}}`,
      }}));
      const items = [
        {{ title: "本页生成", detail: GENERATED_AT }},
        ...metaItems,
        ...adapterItems,
      ];
      box.innerHTML = items.length ? items.map(item => `
        <div class="log-item">
          <strong>${{esc(item.title)}}</strong>
          <div>${{esc(item.detail)}}</div>
        </div>
      `).join("") : `<div class="log-item"><strong>暂无记录</strong><div>运行上游刷新脚本后会显示。</div></div>`;
    }}

    function renderPriorityPlan() {{
      const cards = document.querySelector("#priorityPlanCards");
      const command = document.querySelector("#priorityPlanCommand");
      const rowsEl = document.querySelector("#priorityPlanRows");
      if (!cards || !command || !rowsEl) return;
      const previewAt = META.last_priority_plan_preview_observed_at || (PRIORITY_PLAN[0] && PRIORITY_PLAN[0].observedAt) || "未生成";
      const buckets = uniq(PRIORITY_PLAN.map(row => row.bucket));
      const movedCount = PRIORITY_PLAN.filter(row => row.currentPriority !== row.targetPriority).length;
      const minTarget = PRIORITY_PLAN.length ? Math.min(...PRIORITY_PLAN.map(row => Number(row.targetPriority))) : null;
      const metrics = [
        ["待批准变更", PRIORITY_PLAN.length, "dry-run 计划条目"],
        ["当前→建议不同", movedCount, "只读建议，不写生产"],
        ["涉及档位", buckets.length, buckets.slice(0, 2).join(" / ") || "暂无"],
        ["最小建议序号", minTarget === null ? "-" : String(minTarget).padStart(3, "0"), previewAt],
      ];
      cards.innerHTML = metrics.map(([label, value, hint]) => `
        <div class="plan-card">
          <span>${{esc(label)}}</span>
          <strong>${{esc(value)}}</strong>
          <small>${{esc(hint)}}</small>
        </div>
      `).join("");
      command.textContent = [
        "此处仅展示建议；生产备注写入路径已废弃。",
        "台账不会从网页或脚本自动修改 priority、notes、分组、状态或倍率。",
        "如需调整账号排序，请在后台人工核对后手动处理。",
      ].join("\\n");
      rowsEl.innerHTML = PRIORITY_PLAN.length ? PRIORITY_PLAN.map(row => `
        <tr>
          <td class="rate">${{esc(String(row.targetPriority).padStart(3, "0"))}}</td>
          <td>${{stackCell([detailCell(`#${{row.accountId}} ${{row.accountName}}`, {{ label: "优先级账号", lines: 2, threshold: 42 }}), `<span class="muted">${{esc(row.mode || "dry-run")}}</span>`])}}</td>
          <td class="rate">${{esc(row.currentPriority ?? "-")}}</td>
          <td class="rate">${{esc(row.targetPriority)}}</td>
          <td class="rate">${{esc(row.rateMultiplier || "-")}}</td>
          <td>${{detailCell(row.bucket, {{ label: "建议档位", lines: 1, threshold: 34 }})}}</td>
          <td>${{detailCell(row.groups, {{ label: "当前分组", lines: 2, threshold: 42, empty: "-" }})}}</td>
          <td>${{detailCell(row.reason, {{ label: "建议原因", lines: 2, threshold: 54, empty: "-" }})}}</td>
          <td>${{detailCell(row.observedAt, {{ label: "预览时间", lines: 1, threshold: 30, empty: "-" }})}}</td>
        </tr>
      `).join("") : `<tr class="empty-row"><td colspan="9">暂无优先级 dry-run 预览。运行安全刷新或 plan_account_priority_buckets.py --preview-db 后会显示。</td></tr>`;
    }}

    function fmtCoverage(value) {{
      return value === null || value === undefined || Number.isNaN(value)
        ? "未确认"
        : `${{Number(value.toFixed(3)).toString()}}x`;
    }}

    function compareLineForRow(row) {{
      const cost = row.actualCostLabel || fmtRate(row.actualCostMultiplier);
      const costRecordRatio = row.costRecordRatio;
      const sellRate = lowestSellRate(row);
      const sellCoverage = row.actualCostMultiplier && sellRate !== null ? sellRate / row.actualCostMultiplier : null;
      const costRecordVerdict = row.actualCostLabel
        ? "按备注看单张成本"
        : costRecordRatio === null || costRecordRatio === undefined
          ? "成本记录缺少可比倍率"
          : costRecordRatio < 0.95
            ? "账号成本记录偏低，需核对"
            : costRecordRatio > 1.2
              ? "账号成本记录偏保守"
              : "账号成本记录贴近真实成本";
      const sellVerdict = row.actualCostLabel
        ? "售价看单张备注"
        : sellCoverage === null || sellCoverage === undefined
          ? "未看到用户分组倍率"
          : sellCoverage < 1
            ? "售价可能倒挂"
            : sellCoverage < 1.15
              ? "售价贴近成本"
              : "售价有覆盖";
      return `| ${{row.fluterAccountName}} | ${{row.site}} / ${{row.upstreamGroup}} | ${{cost}} | ${{fmtRate(row.siteAccountMultiplier)}} | ${{row.siteGroupMultiplier || "-"}} | ${{fmtCoverage(costRecordRatio)}} | ${{fmtCoverage(sellCoverage)}} | ${{costRecordVerdict}}；${{sellVerdict}} |`;
    }}

    function rowsByQuery(query) {{
      const q = query.toLowerCase();
      return DATA.filter(row => {{
        const haystack = [
          row.category, row.kind, row.site, row.fluterAccountName,
          row.upstreamGroup, row.status, row.note, row.siteGroupMultiplier,
          row.balanceLabel, row.actualCostLabel
        ].join(" ").toLowerCase();
        return haystack.includes(q);
      }});
    }}

    function answerLedgerQuestion(question) {{
      const q = question.trim().toLowerCase();
      if (!q) return "先输入一个问题，比如“哪些账号需要关注”“KBQ 是否倒挂”“meow 生图成本”。";

      if (q.includes("倒挂") || q.includes("kbq") && (q.includes("亏") || q.includes("真实成本") || q.includes("审计"))) {{
        if (!KBQ_AUDIT) return "KBQ 真实成本审计还没有记录。需要先运行 audit_kbq_true_costs.py，再刷新看板。";
        const status = KBQ_AUDIT.realLossBucketCount > 0 ? "有真倒挂，需要马上看审计桶" : "没有发现真实倒挂";
        return [
          `KBQ 最近 ${{KBQ_AUDIT.hours}} 小时：${{status}}。`,
          `请求数：${{KBQ_AUDIT.requestCount}}，用户扣费：${{fmtNumber(KBQ_AUDIT.userBilledCost)}}，真实上游成本：${{fmtNumber(KBQ_AUDIT.trueUpstreamCost)}}，利润空间：${{fmtNumber(KBQ_AUDIT.margin)}}。`,
          `REAL_LOSS：${{KBQ_AUDIT.realLossBucketCount}}，DISPLAY_DRIFT：${{KBQ_AUDIT.displayDriftBucketCount}}。DISPLAY_DRIFT 只是后台 A成本展示漂移，不等于真亏。`,
        ].join("\\n");
      }}

      if (q.includes("关注") || q.includes("风险") || q.includes("亏") || q.includes("漂移")) {{
        const risky = DATA.filter(row => badgeClass(row.status) === "risk" || badgeClass(row.status) === "warn");
        if (!risky.length) return "当前台账没有明显需关注记录。仍建议看 KBQ 真实成本审计和余额是否太低。";
        return ["当前需关注/观察的记录：", ...risky.slice(0, 12).map(lineForRow), risky.length > 12 ? `- 还有 ${{risky.length - 12}} 条，建议用“只看需关注”筛选。` : ""].filter(Boolean).join("\\n");
      }}

      if (q.includes("倍率") || q.includes("对比") || q.includes("我站")) {{
        const comparable = DATA.filter(row => row.siteAccountMultiplier !== null && row.siteAccountMultiplier !== undefined);
        const risky = comparable.filter(row => {{
          if (row.actualCostLabel || !row.actualCostMultiplier) return false;
          const costRecordRisk = typeof row.costRecordRatio === "number" && row.costRecordRatio < 0.95;
          const sellRate = lowestSellRate(row);
          const sellRisk = sellRate !== null && sellRate / row.actualCostMultiplier < 1.15;
          return costRecordRisk || sellRisk;
        }});
        const rows = (risky.length ? risky : comparable).slice(0, 14);
        return [
          "总体结论：账号成本倍率是内部成本记录，用户分组倍率/售价才是卖给用户的口径。判断利润要看用户分组倍率或实际用户扣费是否覆盖真实成本，不要把账号成本倍率当售价。",
          "",
          "| 我站账号 | 上游/分组 | 上游真实成本 | 账号成本倍率（内部） | 用户分组倍率/售价 | 成本记录覆盖 | 售价覆盖 | 判断 |",
          "|---|---|---:|---:|---|---:|---:|---|",
          ...rows.map(compareLineForRow),
          "",
          risky.length ? `风险和下一步：当前有 ${{risky.length}} 条成本记录偏低或售价贴近成本，建议先看台账备注和 dry-run；需要写入时创建禁用草案账号，不直接改老账号。` : "风险和下一步：当前可比账号未看到明显售价倒挂；生图账号仍以单张流水为准。",
        ].join("\\n");
      }}

      if (q.includes("余额")) {{
        const rows = BALANCE_SNAPSHOTS.filter(row => row.balanceLabel && !row.balanceLabel.includes("未显示"));
        return ["当前记录到的余额：", ...rows.map(row => `- ${{row.provider || row.site}} / ${{row.site}}：${{row.balanceLabel}}（${{row.balanceUpdatedAt || "未记录时间"}}）`)].join("\\n");
      }}

      if (q.includes("生图") || q.includes("图片") || q.includes("1k") || q.includes("2k") || q.includes("4k")) {{
        const rows = DATA.filter(row => row.kind.includes("生图"));
        return ["生图账号和单张成本记录：", ...rows.map(lineForRow)].join("\\n");
      }}

      const candidates = rowsByQuery(q);
      if (candidates.length) {{
        return [`找到 ${{candidates.length}} 条相关记录：`, ...candidates.slice(0, 12).map(lineForRow), candidates.length > 12 ? `- 还有 ${{candidates.length - 12}} 条，请把问题再缩小一点。` : ""].filter(Boolean).join("\\n");
      }}

      return "我没有在台账里找到直接匹配项。可以试试输入上游名、账号名、分组名，比如 KBQ、meow、kingdom、claude、生图、余额。";
    }}

    async function askLedgerAI(question) {{
      const response = await fetch("/admin/upstream-rates/ai", {{
        method: "POST",
        headers: {{ "Content-Type": "application/json" }},
        body: JSON.stringify({{ question }}),
      }});
      let payload = null;
      try {{
        payload = await response.json();
      }} catch (error) {{
        payload = null;
      }}
      if (!response.ok || !payload || !payload.ok) {{
        const reason = payload && payload.error ? payload.error : `HTTP ${{response.status}}`;
        throw new Error(reason);
      }}
      const totalTokens = payload.usage && (payload.usage.total_tokens ?? payload.usage.totalTokens);
      const usageLine = payload.usage ? `\\n\\n模型：${{payload.model || "-"}} · tokens：${{totalTokens ?? "-"}}` : "";
      return `${{payload.answer}}${{usageLine}}`;
    }}

    function render() {{
      detailItems = [];
      const rows = currentRows();
      renderCards(rows);
      renderFreshnessStrip();
      renderSiteMatrix();
      renderProviderObservations();
      renderHealthCards(rows);
      renderOverviewFocus(rows);
      renderOverviewSources();
      renderCategoryNav();
      renderBalanceStrip();
      renderAdapterStatus();
      renderPriorityPlan();
      renderAccountInspector(rows);
      renderCategoryCards(rows);
      renderCategoryDetail();
      renderKbqAudit();
      renderKbqGuide();
      renderImageCosts();
      renderOperationLog();
      renderRows(rows);
    }}
    document.addEventListener("click", event => {{
      const button = event.target.closest("[data-detail]");
      if (!button) return;
      const item = detailItems[Number(button.dataset.detail)];
      if (!item) return;
      detailDialogTitle.textContent = item.title || "完整内容";
      detailDialogBody.textContent = item.text || "";
      if (typeof detailDialog.showModal === "function") {{
        detailDialog.showModal();
      }} else {{
        alert(`${{item.title || "完整内容"}}\\n\\n${{item.text || ""}}`);
      }}
    }});
    for (const el of [search, kindFilter, statusFilter]) {{
      el.addEventListener("input", render);
      el.addEventListener("change", render);
    }}
    if (providerSearch) {{
      providerSearch.addEventListener("input", renderProviderObservations);
      providerSearch.addEventListener("change", renderProviderObservations);
    }}
    categoryFilter.addEventListener("input", () => {{
      setQuickMode("");
      render();
    }});
    categoryFilter.addEventListener("change", () => {{
      setQuickMode("");
      render();
    }});
    for (const button of document.querySelectorAll("#quickFilters button")) {{
      button.addEventListener("click", () => {{
        const mode = button.dataset.quick || "";
        applyQuickFilter(mode);
      }});
    }}
    for (const link of document.querySelectorAll(".nav-link[data-route], .mobile-nav a[data-route]")) {{
      link.addEventListener("click", event => {{
        event.preventDefault();
        showRoute(link.dataset.route);
      }});
    }}
    for (const button of document.querySelectorAll("#kbqModeTabs button")) {{
      button.addEventListener("click", () => {{
        kbqMode = button.dataset.kbqMode || "token";
        for (const tab of document.querySelectorAll("#kbqModeTabs button")) {{
          const active = tab.dataset.kbqMode === kbqMode;
          tab.classList.toggle("active", active);
          tab.setAttribute("aria-pressed", active ? "true" : "false");
        }}
        renderKbqModels();
      }});
    }}
    window.addEventListener("hashchange", routeFromHash);
    window.addEventListener("popstate", routeFromHash);
    document.querySelector("#ledgerAssistantForm").addEventListener("submit", async event => {{
      event.preventDefault();
      const question = assistantInput.value;
      assistantAnswer.textContent = "正在读取台账并请台账 AI 分析…";
      try {{
        assistantAnswer.textContent = await askLedgerAI(question);
      }} catch (error) {{
        assistantAnswer.textContent = `台账 AI 暂时不可用（${{error.message}}）。先用本地查询兜底：\\n\\n${{answerLedgerQuestion(question)}}`;
      }}
    }});
    for (const button of document.querySelectorAll("#ledgerAssistantExamples button")) {{
      button.addEventListener("click", async () => {{
        assistantInput.value = button.dataset.question || "";
        const question = assistantInput.value;
        assistantAnswer.textContent = "正在读取台账并请台账 AI 分析…";
        try {{
          assistantAnswer.textContent = await askLedgerAI(question);
        }} catch (error) {{
          assistantAnswer.textContent = `台账 AI 暂时不可用（${{error.message}}）。先用本地查询兜底：\\n\\n${{answerLedgerQuestion(question)}}`;
        }}
      }});
    }}
    document.querySelector("#reloadPage").addEventListener("click", () => location.reload());
    document.querySelector("#exportCsv").addEventListener("click", () => {{
      const rows = currentRows();
      const header = ["状态", "我站账号命名", "上游", "类型", "上游分组", "页面倍率", "充值比例", "余额", "实际成本/单张成本", "账号成本倍率（内部）", "用户分组倍率/售价", "利润信号", "备注"];
      const csvRows = [header, ...rows.map(row => [
        row.status,
        row.fluterAccountName,
        row.site,
        row.kind,
        row.upstreamGroup,
        fmtRate(row.pageRate),
        row.rechargeRatioLabel,
        row.balanceLabel || "",
        row.actualCostLabel || fmtRate(row.actualCostMultiplier),
        fmtRate(row.siteAccountMultiplier),
        row.siteGroupMultiplier,
        profitSignalText(row).label,
        row.note,
      ])];
      const csv = csvRows.map(row => row.map(value => `"${{String(value ?? "").replace(/"/g, '""')}}"`).join(",")).join("\\n");
      const blob = new Blob(["\\uFEFF" + csv], {{ type: "text/csv;charset=utf-8;" }});
      const link = document.createElement("a");
      link.href = URL.createObjectURL(blob);
      link.download = `fluter-upstream-rates-${{new Date().toISOString().slice(0, 19).replace(/[:T]/g, "-")}}.csv`;
      link.click();
      URL.revokeObjectURL(link.href);
    }});
    render();
    routeFromHash();
    refreshServerMetrics();
    window.setInterval(refreshServerMetrics, 8000);
  </script>
</body>
</html>"""


def render(
    rows,
    kbq_rows,
    kbq_per_call_rows,
    audit_summary,
    audit_buckets,
    adapter_status,
    metadata,
    priority_plan,
    provider_observations=None,
    provider_diagnostics=None,
    balance_snapshots=None,
) -> str:
    return render_dashboard_document(
        build_dashboard_context(
            rows,
            kbq_rows,
            kbq_per_call_rows,
            audit_summary,
            audit_buckets,
            adapter_status,
            metadata,
            priority_plan,
            provider_observations,
            provider_diagnostics,
            balance_snapshots,
        )
    )


def main() -> None:
    args = parse_args()
    (
        rows,
        kbq_rows,
        kbq_per_call_rows,
        audit_summary,
        audit_buckets,
        adapter_status,
        metadata,
        priority_plan,
        provider_observations,
        provider_diagnostics,
        balance_snapshots,
    ) = load_rows(args.db)
    output = Path(args.output)
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(
        render(
            rows,
            kbq_rows,
            kbq_per_call_rows,
            audit_summary,
            audit_buckets,
            adapter_status,
            metadata,
            priority_plan,
            provider_observations,
            provider_diagnostics,
            balance_snapshots,
        ),
        encoding="utf-8",
    )
    print(f"Rendered {len(rows)} records to {output}")


if __name__ == "__main__":
    main()
