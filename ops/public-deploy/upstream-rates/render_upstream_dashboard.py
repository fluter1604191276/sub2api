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
    normalized = str(key or "")
    return normalized in {
        "kbq_pricing_updated_at",
        "kbq_pricing_version",
        "kbq_pricing_source",
        "kbq_recharge_factor",
        "kbq_recharge_note",
        "kbq_per_call_pricing_updated_at",
        "kbq_per_call_model_count",
        "kbq_true_cost_audit_updated_at",
        "kbq_true_cost_audit_hours",
        "kbq_true_loss_alert_sent_run_id",
        "kbq_true_loss_alert_sent_at",
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
        "kbq_json": json.dumps(kbq_rows, ensure_ascii=False),
        "kbq_per_call_json": json.dumps(kbq_per_call_rows, ensure_ascii=False),
        "audit_json": json.dumps(audit_summary, ensure_ascii=False),
        "audit_buckets_json": json.dumps(audit_buckets, ensure_ascii=False),
        "metadata_json": json.dumps(renderable_metadata(metadata), ensure_ascii=False),
    }


def render_dashboard_document(context: dict[str, str]) -> str:
    generated_at = context["generated_at"]
    kbq_json = context["kbq_json"]
    kbq_per_call_json = context["kbq_per_call_json"]
    audit_json = context["audit_json"]
    audit_buckets_json = context["audit_buckets_json"]
    metadata_json = context["metadata_json"]
    return f"""<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <meta name="robots" content="noindex,nofollow" />
  <meta name="theme-color" content="#f6f7f9" />
  <title>Fluter Upstream Rates</title>
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
    .server-section {{ margin-top: 16px; }}
    .server-section-head {{
      display: flex;
      align-items: end;
      justify-content: space-between;
      gap: 12px;
      margin-bottom: 10px;
    }}
    .server-section-head h3 {{ margin: 0; font-size: 15px; }}
    .server-entry-grid {{
      display: grid;
      grid-template-columns: repeat(3, minmax(0, 1fr));
      gap: 10px;
    }}
    .server-entry {{
      display: grid;
      grid-template-columns: minmax(0, 1fr) auto;
      gap: 8px;
      align-items: center;
      min-height: 76px;
      border: 1px solid var(--line);
      border-left: 4px solid var(--green);
      border-radius: 8px;
      background: #fbfdfd;
      padding: 10px 12px;
      color: var(--text);
      text-decoration: none;
    }}
    .server-entry:hover {{ border-color: var(--teal); border-left-color: var(--teal); background: #f0f8f7; }}
    .server-entry.warn {{ border-left-color: var(--amber); background: #fffbeb; }}
    .server-entry.risk {{ border-left-color: var(--red); background: #fef2f2; }}
    .server-entry.info {{ border-left-color: var(--blue); background: #eff6ff; }}
    .server-entry strong, .server-entry span {{ display: block; overflow-wrap: anywhere; }}
    .server-entry span {{ color: var(--muted); font-size: 12px; margin-top: 3px; }}
    .server-policy {{
      margin-top: 10px;
      border-left: 3px solid var(--blue);
      padding: 8px 10px;
      color: var(--muted);
      background: #eff6ff;
      font-size: 12px;
    }}
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
      .server-entry-grid {{ grid-template-columns: repeat(2, minmax(0, 1fr)); }}
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
      .server-entry-grid {{ grid-template-columns: 1fr; }}
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
          <div class="brand-sub">基础设施 · KBQ · 审计</div>
        </div>
      </div>
      <nav class="nav-group" aria-label="主导航">
        <div class="nav-label">Console</div>
        <a class="nav-link" href="#overview" data-route="overview"><span>总览</span><span>Overview</span></a>
        <a class="nav-link" href="#server" data-route="server"><span>服务器状态</span><span>Server</span></a>
      </nav>
      <nav class="nav-group" aria-label="定价导航">
        <div class="nav-label">Pricing</div>
        <a class="nav-link" href="#kbq" data-route="kbq"><span>KBQ 成本</span><span>KBQ</span></a>
      </nav>
      <nav class="nav-group" aria-label="审计与风险导航">
        <div class="nav-label">Audit</div>
        <a class="nav-link" href="#risk" data-route="risk"><span>风险 / 倒挂</span><span>Risk</span></a>
        <a class="nav-link" href="#log" data-route="log"><span>操作记录</span><span>Log</span></a>
      </nav>
      <div class="side-note">
        <div>生成：{html.escape(generated_at)}</div>
        <div>管理员只读</div>
      </div>
    </aside>
    <main class="content-shell" id="main">
      <header class="console-hero" aria-label="台账顶部">
        <div class="topbar">
          <div>
            <h1 id="routeTitle">总览</h1>
            <div class="route-sub" id="routeSubtitle">管理员只读总控台。聚合基础设施、KBQ 定价与真实成本审计。</div>
          </div>
          <div class="actions">
            <span class="pill">生成：{html.escape(generated_at)}</span>
            <button class="action-button" type="button" id="reloadPage">刷新</button>
          </div>
        </div>
        <nav class="mobile-nav" aria-label="移动端导航">
          <a href="#overview" data-route="overview">总览</a>
          <a href="#server" data-route="server">服务器</a>
          <a href="#kbq" data-route="kbq">KBQ</a>
          <a href="#risk" data-route="risk">风险</a>
          <a href="#log" data-route="log">记录</a>
        </nav>
      </header>
      <section class="module-view section-block" id="overview" data-route="overview" aria-labelledby="overviewTitle">
        <div class="module-head">
          <div>
            <h2 id="overviewTitle">Upstream Rates</h2>
            <div class="summary-line">基础设施与 KBQ 成本审计。采集配置、账号状态和运行历史统一进入 S2A Manager。</div>
          </div>
        </div>
        <div class="server-entry-grid" aria-label="保留模块">
          <a class="server-entry info dashboard-route-link" href="#server" data-route="server"><div><strong>基础设施</strong><span>服务、资源、容器、新鲜度与备份</span></div><span class="badge info">查看</span></a>
          <a class="server-entry info dashboard-route-link" href="#kbq" data-route="kbq"><div><strong>KBQ 定价</strong><span>Token 与按次模型公开成本</span></div><span class="badge info">查看</span></a>
          <a class="server-entry info dashboard-route-link" href="#risk" data-route="risk"><div><strong>真实成本审计</strong><span>REAL_LOSS、缺价与展示漂移</span></div><span class="badge info">查看</span></a>
          <a class="server-entry info" href="/admin/s2a-manager" target="_blank" rel="noopener"><div><strong>S2A Manager</strong><span>采集配置与运行历史</span></div><span class="badge info">进入</span></a>
        </div>
      </section>
      <section class="module-view section-block section-panel" id="server" data-route="server" aria-labelledby="serverTitle" hidden>
        <div class="section-title">
          <div>
            <h2 id="serverTitle">基础设施与数据链路</h2>
            <div class="summary-line">主生产节点 us-api-vps-new · 管理员只读风险总览</div>
          </div>
          <div class="actions">
            <a class="action-button" href="/admin/s2a-manager" target="_blank" rel="noopener">S2A 运行历史</a>
            <span class="pill" id="serverUpdatedAt">等待指标…</span>
          </div>
        </div>
        <div class="server-section">
          <div class="server-section-head">
            <h3>服务入口</h3>
            <span class="summary-line">health 失败为红；受保护入口返回鉴权状态视为可达</span>
          </div>
          <div class="server-entry-grid" id="serverServices" aria-label="服务入口健康"></div>
          <div class="server-policy">Codex Radar 仅作外部参考，不抓取或镜像数据；外部参考不参与基础设施评分。</div>
        </div>
        <div class="server-section">
          <div class="server-section-head"><h3>资源状态</h3><span class="summary-line">CPU/load、内存、根盘、/www、流量与 uptime</span></div>
          <div class="server-grid" id="serverMetricCards" aria-label="服务器核心指标"></div>
        </div>
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
        <div class="server-section">
          <div class="server-section-head"><h3>数据新鲜度</h3><span class="summary-line">超过 2 小时琥珀，超过 24 小时红</span></div>
          <div class="server-entry-grid" id="serverFreshness" aria-label="数据新鲜度"></div>
        </div>
        <div class="server-section">
          <div class="server-section-head"><h3>备份与保留</h3><span class="summary-line">timer inactive 或严重过期为红</span></div>
          <div class="server-entry-grid" id="serverBackups" aria-label="备份状态"></div>
        </div>
      </section>
    <section class="module-view category-detail section-block" id="categoryDetail" data-route="kbq" hidden>
      <div class="section-title">
        <h2>KBQ 成本</h2>
        <div class="summary-line">KBQ 公开价格接口的按 token 模型与 Claude 按次模型</div>
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
        <div class="kbq-note">成本倍率 = KBQ 上游实际单价 / 官方基准单价。这里保留公开定价参考；采集与账号管理统一进入 S2A Manager。</div>
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
    <section class="module-view section-block section-panel" id="operationLog" data-route="log" hidden>
      <div class="section-title">
        <h2>操作记录</h2>
        <div class="summary-line">本次静态页渲染、KBQ 定价与真实成本审计摘要。</div>
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
    const KBQ_MODELS = {kbq_json};
    const KBQ_PER_CALL_MODELS = {kbq_per_call_json};
    const KBQ_AUDIT = {audit_json};
    const KBQ_AUDIT_BUCKETS = {audit_buckets_json};
    const META = {metadata_json};
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

    const routeTitle = document.querySelector("#routeTitle");
    const routeSubtitle = document.querySelector("#routeSubtitle");
    const detailDialog = document.querySelector("#detailDialog");
    const detailDialogTitle = document.querySelector("#detailDialogTitle");
    const detailDialogBody = document.querySelector("#detailDialogBody");
    const serverMetricCards = document.querySelector("#serverMetricCards");
    const serverUpdatedAt = document.querySelector("#serverUpdatedAt");
    const serverServices = document.querySelector("#serverServices");
    const serverContainers = document.querySelector("#serverContainers");
    const serverSparkline = document.querySelector("#serverSparkline");
    const serverFreshness = document.querySelector("#serverFreshness");
    const serverBackups = document.querySelector("#serverBackups");
    const moduleViews = [...document.querySelectorAll(".module-view[data-route]")];
    const kbqState = {{ category: "Claude", bucket: "" }};
    let kbqMode = "token";
    const serverState = {{ last: null, points: [] }};
    const ROUTE_META = {{
      overview: {{
        title: "总览",
        subtitle: "基础设施、KBQ 公开定价与真实成本审计。"
      }},
      server: {{
        title: "基础设施与数据链路",
        subtitle: "聚合服务、资源、容器、新鲜度与备份风险；采集运行细节进入 S2A manager。"
      }},
      kbq: {{
        title: "KBQ 成本",
        subtitle: "KBQ 公开价格接口的模型成本分档，只做参考价表，不直接判断利润。"
      }},
      risk: {{
        title: "风险 / 倒挂",
        subtitle: "重点看真实上游成本是否超过用户扣费，DISPLAY_DRIFT 只代表展示口径漂移。"
      }},
      log: {{
        title: "操作记录",
        subtitle: "查看本次渲染、KBQ 定价与真实成本审计元数据。"
      }},
    }};
    const HASH_ALIASES = {{
      "": "overview",
      overview: "overview",
      server: "server",
      categoryDetail: "kbq",
      kbq: "kbq",
      kbqAuditSection: "risk",
      risk: "risk",
      operationLog: "log",
      log: "log",
    }};
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
      if (route === "log") renderOperationLog();
      if (route === "server") refreshServerMetrics();
      if (options.resetScroll !== false) {{
        const reduceMotion = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
        window.scrollTo({{ top: 0, behavior: reduceMotion ? "auto" : "smooth" }});
      }}
    }}

    function routeFromHash() {{
      showRoute(location.hash.slice(1), {{ updateHash: false, resetScroll: false }});
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

    function fmtAge(ageSeconds) {{
      if (ageSeconds === null || ageSeconds === undefined || Number.isNaN(Number(ageSeconds))) return "未记录";
      const seconds = Math.max(0, Number(ageSeconds));
      if (seconds >= 86400) return `${{(seconds / 86400).toFixed(1)}} 天前`;
      if (seconds >= 3600) return `${{(seconds / 3600).toFixed(1)}} 小时前`;
      return `${{Math.max(0, Math.floor(seconds / 60))}} 分钟前`;
    }}

    function infrastructureFreshnessTone(ageSeconds) {{
      if (ageSeconds === null || ageSeconds === undefined || Number.isNaN(Number(ageSeconds))) return "risk";
      if (ageSeconds > 24 * 3600) return "risk";
      if (ageSeconds > 2 * 3600) return "warn";
      return "ok";
    }}

    function worstInfrastructureTone(...tones) {{
      if (tones.includes("risk")) return "risk";
      if (tones.includes("warn")) return "warn";
      return "ok";
    }}

    function toneBadge(tone) {{
      if (tone === "risk") return '<span class="badge risk">RISK</span>';
      if (tone === "warn") return '<span class="badge warn">WARN</span>';
      return '<span class="badge ok">OK</span>';
    }}

    function updateServerServices(metrics) {{
      if (!serverServices) return;
      const services = Array.isArray(metrics.services) ? metrics.services : [];
      const cards = services.map(item => `
        <a class="server-entry ${{item.tone || "risk"}}" href="${{esc(item.url || "#")}}" target="_blank" rel="noopener">
          <div><strong>${{esc(item.label || item.id || "服务")}}</strong><span>${{item.status_code ? `HTTP ${{esc(item.status_code)}}` : esc(item.error || "检查失败")}} · ${{esc(item.latency_ms ?? "-")}} ms</span></div>
          ${{toneBadge(item.tone)}}
        </a>
      `);
      cards.push(`
        <a class="server-entry info" href="https://codexradar.com/" target="_blank" rel="noopener">
          <div><strong>Codex Radar</strong><span>外部模型与生态参考</span></div>
          <span class="badge info">参考</span>
        </a>
      `);
      serverServices.innerHTML = cards.join("");
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
      const disks = metrics.disks || {{ root: metrics.disk || {{}}, www: {{}} }};
      const rootDisk = disks.root || metrics.disk || {{}};
      const wwwDisk = disks.www || {{}};
      const net = metrics.net || {{}};
      const containers = metrics.containers || {{}};
      const containerItems = containers.items || [];
      const unhealthy = containerItems.filter(item => item.health !== "ok").length;
      const cards = [
        {{
          label: "CPU / Load",
          value: fmtPercent(cpu.load1_per_core_percent),
          hint: `1m ${{fmtNumber(cpu.load1)}} · 5m ${{fmtNumber(cpu.load5)}} · 15m ${{fmtNumber(cpu.load15)}} · ${{cpu.cores ?? "-"}} cores`,
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
          value: fmtPercent(rootDisk.used_percent),
          hint: `${{fmtBytes(rootDisk.used_bytes)}} / ${{fmtBytes(rootDisk.total_bytes)}}`,
          tone: metricTone(rootDisk.used_percent, 80, 90),
        }},
        {{
          label: "数据盘 /www",
          value: fmtPercent(wwwDisk.used_percent),
          hint: `${{fmtBytes(wwwDisk.used_bytes)}} / ${{fmtBytes(wwwDisk.total_bytes)}}`,
          tone: metricTone(wwwDisk.used_percent, 80, 90),
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
          tone: containers.available ? (unhealthy ? "risk" : "") : "risk",
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
        serverContainers.innerHTML = `<div class="container-row"><strong>docker ps 不可用</strong><span>${{esc(containers.error || "未返回容器信息")}}</span><span class="badge risk">RISK</span></div>`;
        return;
      }}
      const items = containers.items || [];
      if (!items.length) {{
        serverContainers.innerHTML = `<div class="container-row"><strong>无容器</strong><span>docker ps 未返回运行容器</span><span class="badge info">INFO</span></div>`;
        return;
      }}
      serverContainers.innerHTML = items.map(item => `
        <div class="container-row">
          <strong>${{esc(item.label || item.name)}}</strong>
          <span>${{esc(item.status)}}</span>
          <span class="badge ${{item.health === "risk" ? "risk" : "ok"}}">${{item.health === "risk" ? "RISK" : "OK"}}</span>
        </div>
      `).join("");
    }}

    function updateServerFreshness(metrics) {{
      if (!serverFreshness) return;
      const items = Array.isArray(metrics.freshness) ? metrics.freshness : [];
      serverFreshness.innerHTML = items.map(item => {{
        const tone = worstInfrastructureTone(item.tone, infrastructureFreshnessTone(item.age_seconds));
        return `
          <div class="server-entry ${{tone}}">
            <div><strong>${{esc(item.label || item.id)}}</strong><span>${{esc(fmtAge(item.age_seconds))}} · ${{esc(item.updated_at || "未记录")}}</span>${{item.summary ? `<span>${{esc(item.summary)}}</span>` : ""}}</div>
            ${{toneBadge(tone)}}
          </div>
        `;
      }}).join("");
    }}

    function updateServerBackups(metrics) {{
      if (!serverBackups) return;
      const items = Array.isArray(metrics.backups) ? metrics.backups : [];
      serverBackups.innerHTML = items.map(item => {{
        const timer = item.timer || {{}};
        const latest = item.latest || {{}};
        const tone = timer.active_state !== "active" || latest.tone === "risk" ? "risk" : (latest.tone === "warn" ? "warn" : "ok");
        return `
          <div class="server-entry ${{tone}}">
            <div>
              <strong>${{esc(item.label || item.id)}}</strong>
              <span>${{esc(timer.unit || "timer")}}: ${{esc(timer.active_state || "unknown")}} · 最近备份 ${{esc(fmtAge(latest.age_seconds))}} · ${{esc(fmtBytes(latest.size_bytes))}}</span>
              <span>${{esc(latest.path || "未找到备份")}} · ${{esc(item.retention || "")}}</span>
            </div>
            ${{toneBadge(tone)}}
          </div>
        `;
      }}).join("");
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
      updateServerServices(metrics);
      updateServerMetricCards(metrics, rates);
      updateServerContainers(metrics);
      updateServerFreshness(metrics);
      updateServerBackups(metrics);
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

    function bucketOf(row) {{
      return fmtRate(row.costMultiplier);
    }}

    function renderKbqModels() {{
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
        ["Token 模型", "相对官方基准价", "输入、输出与缓存价格分别核对"],
        ["按次模型", "单次价 × 充值系数", "不要和 token 倍率混用"],
        ["真实成本", "usage logs 审计", "REAL_LOSS 才代表真实倒挂"],
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

    function renderOperationLog() {{
      const box = document.querySelector("#operationLogList");
      if (!box) return;
      const metaItems = Object.entries(META || {{}}).map(([key, value]) => ({{
        title: `metadata：${{key}}`,
        detail: String(value ?? "-"),
      }}));
      const items = [
        {{ title: "本页生成", detail: GENERATED_AT }},
        ...metaItems,
      ];
      box.innerHTML = items.length ? items.map(item => `
        <div class="log-item">
          <strong>${{esc(item.title)}}</strong>
          <div>${{esc(item.detail)}}</div>
        </div>
      `).join("") : `<div class="log-item"><strong>暂无记录</strong><div>重新渲染静态页面后会显示。</div></div>`;
    }}

    function render() {{
      detailItems = [];
      renderKbqModels();
      renderKbqAudit();
      renderKbqGuide();
      renderOperationLog();
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
    for (const link of document.querySelectorAll(".nav-link[data-route], .mobile-nav a[data-route], .dashboard-route-link[data-route]")) {{
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
    document.querySelector("#reloadPage").addEventListener("click", () => location.reload());
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
    print(
        f"Rendered {len(kbq_rows)} KBQ token models and "
        f"{len(kbq_per_call_rows)} per-call models to {output}"
    )


if __name__ == "__main__":
    main()
