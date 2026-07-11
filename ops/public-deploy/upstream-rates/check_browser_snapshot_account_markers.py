#!/usr/bin/env python3
"""Check Chrome/Tampermonkey account rows against rate markers in account names.

This is a local read-only diagnostic. It reads the sanitized Chrome collector
snapshot and compares visible upstream page rates with the rate markers Fluter
keeps in upstream account names. It never contacts upstream sites and never
edits the production sub2api database.
"""

from __future__ import annotations

import argparse
import json
import re
from dataclasses import asdict, dataclass
from decimal import Decimal, InvalidOperation
from pathlib import Path
from typing import Any


DEFAULT_SNAPSHOT = Path(__file__).resolve().parent / "local-snapshots/chrome/latest.json"
DEFAULT_TOLERANCE = Decimal("0.000001")
MIN_TAMPERMONKEY_SNAPSHOT_VERSION = (0, 1, 15)


@dataclass(frozen=True)
class NameMarker:
    marker_kind: str
    page_rate_marker: Decimal | None = None
    recharge_factor_marker: Decimal | None = None
    cost_marker: Decimal | None = None
    text_rate_marker: Decimal | None = None
    image_cent_marker: Decimal | None = None


@dataclass(frozen=True)
class CheckRow:
    provider: str
    site: str
    account_name: str
    upstream_group: str
    page_rate: Decimal | None
    status: str
    reason: str
    name_marker: NameMarker


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Check browser snapshot account-name rate markers")
    parser.add_argument("--snapshot", type=Path, default=DEFAULT_SNAPSHOT)
    parser.add_argument("--json", action="store_true")
    parser.add_argument("--tolerance", default=str(DEFAULT_TOLERANCE))
    return parser.parse_args()


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


def snapshot_script_version(observation: dict[str, Any]) -> tuple[int, ...] | None:
    direct = parse_semver(str(observation.get("script_version") or ""))
    if direct:
        return direct
    match = re.search(r"\bscript\s*=\s*([0-9]+(?:\.[0-9]+){0,3})\b", str(observation.get("detail") or ""))
    return parse_semver(match.group(1)) if match else None


def snapshot_skip_reason(observation: dict[str, Any]) -> str | None:
    """Return why this snapshot should not train current drift conclusions."""

    detail = str(observation.get("detail") or "")
    if (
        "preserved previous account lines" in detail
        or "preserved previous non-empty snapshot" in detail
        or re.search(r"\bfresh_account_lines\s*=\s*0\b", detail)
        or "account_lines=0" in detail
    ):
        return "本轮没有抓到新的账号行，旧账号快照只保留作诊断，不参与倍率漂移判断"
    if "partial account snapshot" in detail:
        return "本轮疑似只抓到局部账号行，不参与倍率漂移判断"
    if re.search(r"\bwait_state\s*=\s*timeout\b", detail):
        return "本轮页面等待超时，不参与倍率漂移判断"
    if "Chrome Tampermonkey read-only snapshot" in detail:
        version = snapshot_script_version(observation)
        if version is None or semver_lt(version, MIN_TAMPERMONKEY_SNAPSHOT_VERSION):
            return "旧版油猴脚本快照，不参与倍率漂移判断；请更新脚本后重抓"
    return None


def decimal_or_none(value: Any) -> Decimal | None:
    if value in (None, ""):
        return None
    try:
        return Decimal(str(value))
    except (InvalidOperation, ValueError):
        return None


def compact(value: Decimal | None) -> str:
    if value is None:
        return "-"
    return format(value.quantize(Decimal("0.000000001")).normalize(), "f")


def close(left: Decimal | None, right: Decimal | None, tolerance: Decimal) -> bool:
    if left is None or right is None:
        return False
    return abs(left - right) <= tolerance


def extract_name_marker(account_name: str) -> NameMarker:
    name = " ".join(str(account_name or "").split())

    image_price_expression = re.search(
        r"(?P<price>\d+(?:\.\d+)?)\s*分\s*\*\s*(?P<factor>\d+(?:\.\d+)?)\s*=\s*(?P<cost>\d+(?:\.\d+)?)\s*分",
        name,
    )
    if image_price_expression and any(marker in name for marker in ("生图", "图", "张")):
        return NameMarker(marker_kind="image_price_only", image_cent_marker=Decimal(image_price_expression.group("cost")))

    image_cent = extract_image_cent_marker(name)

    # Keep this expression path independent from provider-wide recharge factors.
    # Kingdom account names intentionally carry all three values
    # (`page_rate * recharge_factor = cost_multiplier`) so this checker can
    # catch either a changed upstream page rate or a changed recharge discount.
    expression = re.search(
        r"(?P<page>\d+(?:\.\d+)?)\s*\*\s*(?P<factor>\d+(?:\.\d+)?)\s*=\s*(?P<cost>\d+(?:\.\d+)?)",
        name,
    )
    if expression:
        return NameMarker(
            marker_kind="page_times_recharge_cost",
            page_rate_marker=Decimal(expression.group("page")),
            recharge_factor_marker=Decimal(expression.group("factor")),
            cost_marker=Decimal(expression.group("cost")),
            image_cent_marker=image_cent,
        )

    # Prefer text-rate markers such as "仅文字0.13" or "文字0.05" and avoid
    # treating image price markers like "5分" or size labels like "1/2/4k" as
    # upstream text multipliers.
    labelled_rate = re.search(r"(?:仅文字|文字|文本|倍率|成本)\s*(?P<rate>\d+(?:\.\d+)?)", name)
    if labelled_rate:
        return NameMarker(
            marker_kind="text_rate",
            text_rate_marker=Decimal(labelled_rate.group("rate")),
            image_cent_marker=image_cent,
        )

    if image_cent is not None:
        return NameMarker(marker_kind="image_price_only", image_cent_marker=image_cent)

    candidates: list[Decimal] = []
    for match in re.finditer(r"(?<![\d/])(?P<rate>\d+(?:\.\d+)?)(?![\d.kK分/号])", name):
        tail = name[match.end() : match.end() + 4]
        if re.match(r"\s*分", tail):
            continue
        candidates.append(Decimal(match.group("rate")))
    if len(candidates) == 1:
        return NameMarker(
            marker_kind="bare_rate",
            text_rate_marker=candidates[0],
            image_cent_marker=image_cent,
        )
    if len(candidates) > 1:
        return NameMarker(marker_kind="ambiguous_rate", image_cent_marker=image_cent)

    return NameMarker(marker_kind="missing")


def extract_image_cent_marker(name: str) -> Decimal | None:
    for match in re.finditer(r"(?P<cents>\d+(?:\.\d+)?)\s*分", name):
        start, end = match.span()
        if re.match(r"\s*\*", name[end : end + 4]):
            continue
        if "=" in name[max(0, start - 12) : start]:
            continue
        before = name[max(0, start - 28) : start]
        after = name[end : min(len(name), end + 8)]
        context = before + after
        if any(marker in context for marker in ("生图", "图", "张")):
            return Decimal(match.group("cents"))
    return None


def update_marker_suggestion(old: Decimal | None, new: Decimal | None) -> str:
    return f"；建议：账号名标注 {compact(old)} → 更新为页面值 {compact(new)}"


def is_noise_account_name(account_name: str) -> bool:
    text = " ".join(str(account_name or "").split())
    if not text:
        return True
    if re.search(r"已启用|未启用|已禁用", text) and re.search(r"[¥￥$]\s*\d", text):
        return True
    return False


def is_generic_unmapped_key_name(account_name: str) -> bool:
    text = " ".join(str(account_name or "").split())
    return text in {"自用", "非自用"}


def check_account(provider: str, site: str, account: dict[str, Any], tolerance: Decimal) -> CheckRow:
    page_rate = decimal_or_none(account.get("page_rate"))
    name = str(account.get("account_name") or "")
    group = str(account.get("upstream_group") or "")
    marker = extract_name_marker(name)

    if site == "xn--vduyey89e.com" or provider == "KBQ":
        return CheckRow(provider, site, name, group, page_rate, "INFO", "KBQ uses /api/pricing, not browser account rows", marker)

    if is_noise_account_name(name):
        return CheckRow(provider, site, name, group, page_rate, "INFO", "疑似页面状态/额度行，不参与账号名倍率判断", marker)

    if is_generic_unmapped_key_name(name):
        return CheckRow(provider, site, name, group, page_rate, "INFO", "上游 key 名未按我站账号命名规则标注，需人工映射或忽略", marker)

    if marker.marker_kind == "page_times_recharge_cost":
        if not close(page_rate, marker.page_rate_marker, tolerance):
            return CheckRow(
                provider,
                site,
                name,
                group,
                page_rate,
                "DRIFT",
                (
                    f"页面倍率 {compact(page_rate)}x != 名字里的上游倍率 {compact(marker.page_rate_marker)}x"
                    f"{update_marker_suggestion(marker.page_rate_marker, page_rate)}"
                ),
                marker,
            )
        computed = page_rate * marker.recharge_factor_marker if page_rate is not None and marker.recharge_factor_marker is not None else None
        if marker.cost_marker is not None and not close(computed, marker.cost_marker, Decimal("0.001")):
            return CheckRow(
                provider,
                site,
                name,
                group,
                page_rate,
                "DRIFT",
                (
                    f"页面倍率×名字充值系数 = {compact(computed)}x，但名字成本标注是 {compact(marker.cost_marker)}x"
                    f"；建议：账号名成本标注 {compact(marker.cost_marker)} → 更新为 {compact(computed)}"
                ),
                marker,
            )
        return CheckRow(
            provider,
            site,
            name,
            group,
            page_rate,
            "OK",
            f"页面倍率 {compact(page_rate)}x；名字成本标注 {compact(marker.cost_marker)}x",
            marker,
        )

    if marker.marker_kind in {"text_rate", "bare_rate"}:
        if close(page_rate, marker.text_rate_marker, tolerance):
            extra = f"；另有生图单张 {compact(marker.image_cent_marker)}分" if marker.image_cent_marker is not None else ""
            return CheckRow(provider, site, name, group, page_rate, "OK", f"页面倍率和账号名文字倍率都是 {compact(page_rate)}x{extra}", marker)
        return CheckRow(
            provider,
            site,
            name,
            group,
            page_rate,
            "DRIFT",
            (
                f"页面倍率 {compact(page_rate)}x != 账号名标注 {compact(marker.text_rate_marker)}x"
                f"{update_marker_suggestion(marker.text_rate_marker, page_rate)}"
            ),
            marker,
        )

    if marker.marker_kind == "ambiguous_rate":
        return CheckRow(
            provider,
            site,
            name,
            group,
            page_rate,
            "INFO",
            "账号名含多个数字，无法可靠判定文字倍率",
            marker,
        )

    if marker.marker_kind == "image_price_only":
        return CheckRow(
            provider,
            site,
            name,
            group,
            page_rate,
            "INFO",
            f"账号名只标了生图单张 {compact(marker.image_cent_marker)}分；页面分组倍率 {compact(page_rate)}x 不直接比较",
            marker,
        )

    return CheckRow(provider, site, name, group, page_rate, "INFO", "账号名没有可比较的文字倍率标注", marker)


def load_rows(snapshot: Path, tolerance: Decimal) -> list[CheckRow]:
    data = json.loads(snapshot.read_text(encoding="utf-8"))
    observations = data if isinstance(data, list) else data.get("snapshots", [])
    rows: list[CheckRow] = []
    for observation in observations:
        provider = str(observation.get("provider") or "")
        site = str(observation.get("site") or "")
        skip_reason = snapshot_skip_reason(observation)
        if skip_reason:
            continue
        for account in observation.get("detected_accounts") or []:
            if isinstance(account, dict):
                rows.append(check_account(provider, site, account, tolerance))
    return rows


def render_markdown(rows: list[CheckRow]) -> str:
    counts = {status: sum(1 for row in rows if row.status == status) for status in ("OK", "DRIFT", "INFO")}
    lines = [
        "# Chrome Account Marker Check",
        "",
        f"- OK: {counts['OK']}",
        f"- DRIFT: {counts['DRIFT']}",
        f"- INFO: {counts['INFO']}",
        "",
        "| 状态 | 上游 | 账号名 | 页面倍率 | 分组 | 说明 |",
        "|---|---|---|---:|---|---|",
    ]
    for row in rows:
        account = row.account_name.replace("|", "\\|")
        group = row.upstream_group.replace("|", "\\|")
        reason = row.reason.replace("|", "\\|")
        lines.append(
            f"| {row.status} | {row.provider} | {account} | {compact(row.page_rate)}x | {group} | {reason} |"
        )
    return "\n".join(lines) + "\n"


def main() -> None:
    args = parse_args()
    rows = load_rows(args.snapshot, Decimal(str(args.tolerance)))
    if args.json:
        print(json.dumps([asdict(row) for row in rows], ensure_ascii=False, indent=2, default=str))
    else:
        print(render_markdown(rows))


if __name__ == "__main__":
    main()
