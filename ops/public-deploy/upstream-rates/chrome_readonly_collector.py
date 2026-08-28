#!/usr/bin/env python3
"""Local Chrome/Tampermonkey read-only collector for upstream ledger snapshots.

The collector accepts sanitized observations from the Fluter Tampermonkey
userscript, sanitizes them again, keeps only a small field allowlist, and writes
adapter-compatible JSON snapshots for refresh_browser_readonly_adapters.py.

It never stores cookies, passwords, full API keys, Bearer tokens, or raw HTML.
It binds to 127.0.0.1 by default and does not SSH from the request handler.
"""

from __future__ import annotations

import argparse
import hmac
import json
import os
import re
import secrets
import shlex
import socket
import subprocess
import threading
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from http import HTTPStatus
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any
from urllib.parse import parse_qs, urlparse


DEFAULT_HOST = "127.0.0.1"
DEFAULT_PORT = 8799
DEFAULT_MAX_BODY_BYTES = 256 * 1024
DEFAULT_TOKEN_FILE = Path.home() / ".config/fluter-collector/token"
DEFAULT_SNAPSHOT_DIR = Path(__file__).resolve().parent / "local-snapshots/chrome"
DEFAULT_COMMAND_DIR = DEFAULT_SNAPSHOT_DIR / "commands"
DEFAULT_IMPORT_SCRIPT = Path(__file__).resolve().parent / "refresh_browser_readonly_adapters.py"
DEFAULT_USERSCRIPT_PATH = (
    Path(__file__).resolve().parent.parent
    / "browser-userscripts/fluter-upstream-readonly-collector.user.js"
)
COMMANDS_FILE_NAME = "commands.json"
ALLOWED_COMMAND_ACTIONS = {"refresh_then_send", "send_snapshot"}
ALLOWED_COMMAND_ACK_STATUS = {"done", "error"}
DEFAULT_COMMAND_TTL_SECONDS = 5 * 60
MAX_COMMAND_TTL_SECONDS = 30 * 60
MAX_STORED_COMMANDS = 200
MAX_STORED_SNAPSHOTS = 500
SENSITIVE_ENV_NAME_RE = re.compile(
    r"(?:^|_)(?:api[_-]?key|token|secret|password|passwd|cookie|bearer|authorization)(?:$|_)",
    re.IGNORECASE,
)
MASKED_KEY_RE = re.compile(
    r"(sk-[A-Za-z0-9_-]{2,}\.\.\.[A-Za-z0-9_-]{2,}|"
    r"sk-[A-Za-z0-9_-]{8}\.\.\.redacted|"
    r"sk-[A-Za-z0-9_-]{3,}\*{3,}[A-Za-z0-9_-]{2,}|"
    r"\.\.\.redacted-long-token\.\.\.)",
    re.IGNORECASE,
)
RATE_VALUE_RE = re.compile(
    r"(?<![0-9.])(?:"
    r"(\d+(?:\.\d+)?)\s*[xX]"
    r"|×\s*(\d+(?:\.\d+)?)"
    r"|(\d+(?:\.\d+)?)\s*倍"
    r")"
)


@dataclass(frozen=True)
class Provider:
    name: str
    site: str
    aliases: tuple[str, ...]


PROVIDERS = (
    Provider("Meow", "api.saki.lat", ("api.saki.lat", "saki.lat")),
    Provider("Magic", "pool.gptstore.club", ("pool.gptstore.club", "gptstore.club")),
    Provider(
        "Kingdom",
        "api.tokenskingdom.com",
        ("api.tokenskingdom.com", "image.tokenskingdom.com", "tokenskingdom.com"),
    ),
    Provider("超超 Mouubox", "api.mouubox.com", ("api.mouubox.com",)),
    Provider("超超 Mouubox 副站", "sub2api.mouubox.com", ("sub2api.mouubox.com",)),
    Provider("聪明AI", "sub2.congmingai.com", ("sub2.congmingai.com",)),
    Provider("乔燃", "mdkj.lol", ("mdkj.lol",)),
    Provider("KBQ", "xn--vduyey89e.com", ("xn--vduyey89e.com",)),
    Provider("钧澈", "vip.lcodex.cn", ("vip.lcodex.cn", "lcodex.cn")),
)


ALLOWED_STATUS = {
    "browser_observed",
    "browser_observed_empty",
    "needs_review",
    "browser_read_failed",
}
BALANCE_KEYWORDS = (
    "当前余额",
    "账户余额",
    "账号余额",
    "可用余额",
    "剩余余额",
    "余额",
    "剩余额度",
    "可用额度",
    "balance",
    "remaining",
    "quota",
)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Run the local Chrome read-only upstream collector")
    parser.add_argument("--host", default=DEFAULT_HOST)
    parser.add_argument("--port", type=int, default=DEFAULT_PORT)
    parser.add_argument("--token-file", type=Path, default=DEFAULT_TOKEN_FILE)
    parser.add_argument("--snapshot-dir", type=Path, default=DEFAULT_SNAPSHOT_DIR)
    parser.add_argument("--command-dir", type=Path, default=DEFAULT_COMMAND_DIR)
    parser.add_argument("--max-body-bytes", type=int, default=DEFAULT_MAX_BODY_BYTES)
    parser.add_argument("--init-token", action="store_true", help="Create a local collector token file with mode 600")
    parser.add_argument("--sync-latest", action="store_true", help="Import latest.json through refresh_browser_readonly_adapters.py")
    parser.add_argument("--remote-ssh-host", default="fluterapi-prod")
    parser.add_argument("--import-script", type=Path, default=DEFAULT_IMPORT_SCRIPT)
    parser.add_argument("--no-remote-render-dashboard", action="store_true")
    parser.add_argument("--queue-command", metavar="SITE_OR_PROVIDER", help="Queue one local browser command for an opened upstream tab")
    parser.add_argument("--command-action", default="refresh_then_send", choices=sorted(ALLOWED_COMMAND_ACTIONS))
    parser.add_argument("--command-ttl-seconds", type=int, default=DEFAULT_COMMAND_TTL_SECONDS)
    parser.add_argument("--command-reason", default="")
    parser.add_argument("--userscript-path", type=Path, default=DEFAULT_USERSCRIPT_PATH)
    return parser.parse_args()


def all_aliases() -> tuple[str, ...]:
    return tuple(alias for provider in PROVIDERS for alias in provider.aliases)


def scrub_sensitive_environment(env: dict[str, str] | None = None) -> list[str]:
    """Remove unrelated secrets inherited by launchd before serving locally."""

    target = os.environ if env is None else env
    removed: list[str] = []
    for key in list(target):
        if SENSITIVE_ENV_NAME_RE.search(key):
            removed.append(key)
            target.pop(key, None)
    return removed


def host_matches(host: str, alias: str) -> bool:
    host = host.lower().strip(".")
    alias = alias.lower().strip(".")
    return host == alias or host.endswith("." + alias)


def provider_for_site(site: str) -> Provider | None:
    site = site.lower().strip()
    for provider in PROVIDERS:
        if host_matches(site, provider.site) or any(host_matches(site, alias) for alias in provider.aliases):
            return provider
    return None


def provider_for_url(url: str) -> Provider | None:
    host = urlparse(url).netloc.lower().split("@")[-1].split(":")[0]
    if not host:
        return None
    return provider_for_site(host)


def provider_for_command_target(target: str) -> Provider | None:
    target = str(target or "").strip()
    if not target:
        return None
    by_url = provider_for_url(target)
    if by_url:
        return by_url
    lowered = target.lower().strip()
    for provider in PROVIDERS:
        if lowered == provider.name.lower():
            return provider
        if host_matches(lowered, provider.site) or any(host_matches(lowered, alias) for alias in provider.aliases):
            return provider
    return None


def provider_for_payload(item: dict[str, Any]) -> Provider | None:
    provider_name = str(item.get("provider") or "").strip()
    site = str(item.get("site") or "").strip()
    url = str(item.get("page_url") or item.get("url") or "").strip()
    provider = provider_for_site(site) if site else provider_for_url(url)
    if not provider:
        return None
    if provider_name and provider_name != provider.name:
        return None
    return provider


def sanitize(text: str) -> str:
    text = str(text or "").replace("\x00", "")
    text = re.sub(r"(sk-[A-Za-z0-9_-]{8})[A-Za-z0-9_-]{12,}", r"\1...redacted", text)
    text = re.sub(r"(?i)(bearer\s+)[A-Za-z0-9._-]{16,}", r"\1...redacted", text)
    text = re.sub(r"(?i)(api[_ -]?key\s*[:=]\s*)[A-Za-z0-9._-]{12,}", r"\1...redacted", text)
    text = re.sub(r"[A-Za-z0-9_-]{48,}", "...redacted-long-token...", text)
    lines = [line.strip() for line in text.splitlines()]
    return "\n".join(line for line in lines if line)


def source_lines(text: str) -> list[str]:
    return [" ".join(line.split()) for line in sanitize(text).splitlines() if line.strip()]


def compact_excerpt(text: str, limit: int = 1200) -> str:
    interesting: list[str] = []
    for line in source_lines(text):
        if is_collector_panel_noise(line):
            continue
        lower = line.lower()
        if (
            "余额" in line
            or "额度" in line
            or "倍率" in line
            or "分组" in line
            or "号池" in line
            or "使用记录" in line
            or "日志" in line
            or "balance" in lower
            or "quota" in lower
            or "group" in lower
            or re.search(r"\b\d+(?:\.\d+)?x\b", lower)
        ):
            interesting.append(line[:180])
        if len("\n".join(interesting)) >= limit:
            break
    if not interesting:
        interesting = source_lines(text)[:10]
    return "\n".join(interesting)[:limit]


def is_collector_panel_noise(value: str) -> bool:
    text = str(value or "")
    if not text:
        return False
    panel_markers = (
        "Fluter 上游采集",
        "脚本：",
        "collector",
        "本页识别",
        "最近结果",
        "自动发送快照",
        "发送间隔",
        "定时刷新页面后抓取",
        "刷新后发送",
        "设置 token",
        "检测 collector",
    )
    return any(marker in text for marker in panel_markers)


def is_pseudo_balance(value: str) -> bool:
    text = str(value or "").strip()
    if not text:
        return False
    if is_collector_panel_noise(text):
        return True
    if text.startswith(("本页识别：", "脚本：")):
        return True
    return False


def is_noise_account_name(value: str) -> bool:
    text = str(value or "").strip()
    if not text:
        return True
    if is_collector_panel_noise(text):
        return True
    noise_markers = (
        "管理您的 API 密钥和访问令牌",
        "点击可复制此端点",
        "苹果生图工作台",
        "生图工作台",
        "使用记录",
        "渠道状态",
        "可用令牌分组",
        "统计Tokens",
        "toggle token visibility",
        "copy token key",
        "You need to enable JavaScript to run this app.",
    )
    if any(marker in text for marker in noise_markers):
        return True
    if re.search(r"已启用|未启用|已禁用", text) and re.search(r"[¥￥$]\s*\d", text):
        return True
    if re.match(r"^\d{1,2}\s+\d{2}:\d{2}:\d{2}\b", text):
        return True
    if re.match(r"^\d{4}[/-]\d{2}[/-]\d{2}\b", text):
        return True
    return False


def clean_account_candidate(value: str) -> str:
    text = sanitize(value).strip(" /:：|｜-")
    if not text or len(text) < 3 or len(text) > 120:
        return ""
    if MASKED_KEY_RE.search(text) or re.search(r"(?i)bearer|api[_ -]?key|https?://", text):
        return ""
    if re.search(r"\b\d+(?:\.\d+)?x\b", text, re.IGNORECASE):
        return ""
    if is_noise_account_name(text):
        return ""
    if text in {
        "选择分组",
        "点击更换分组",
        "复制到剪贴板",
        "使用密钥",
        "导入到 CCS",
        "禁用",
        "编辑",
        "删除",
        "操作",
        "状态",
        "已启用",
        "无限额度",
        "Select this row",
        "on",
    }:
        return ""
    if text.startswith("Tag:"):
        return ""
    if re.fullmatch(r"[$¥￥]?\s*-?\d+(?:\.\d+)?", text):
        return ""
    return text


def clean_group_candidate(value: str) -> str:
    text = sanitize(value).strip(" /:：|｜-")
    if not text or len(text) > 120:
        return ""
    if MASKED_KEY_RE.search(text) or re.search(r"(?i)bearer|api[_ -]?key|https?://", text):
        return ""
    if re.search(r"\b\d+(?:\.\d+)?x\b", text, re.IGNORECASE):
        return ""
    if is_collector_panel_noise(text):
        return ""
    if text in {"选择分组", "点击更换分组", "编辑", "删除", "复制", "操作", "状态"}:
        return ""
    return text


def semantic_parts(value: str) -> list[str]:
    return [part.strip() for part in re.split(r"\s+/\s+|\t| {2,}", value) if part.strip()]


def compact_account_prefix(value: str) -> str:
    text = sanitize(value).strip(" /:：|｜-")
    for marker in (" 已启用", " 未启用", " 已禁用", " 禁用", " 无限额度", " 有限额度"):
        idx = text.find(marker)
        if idx > 0:
            return clean_account_candidate(text[:idx])
    return ""


def first_account_candidate(parts: list[str], fallback: str) -> str:
    for candidate in parts or [fallback]:
        cleaned = clean_account_candidate(candidate)
        if cleaned:
            return cleaned
        compact = compact_account_prefix(candidate)
        if compact:
            return compact
    compact = compact_account_prefix(fallback)
    if compact:
        return compact
    return ""


def last_group_candidate(parts: list[str], fallback: str) -> str:
    for candidate in reversed(parts or [fallback]):
        cleaned = clean_group_candidate(candidate)
        if cleaned:
            return cleaned
    return ""


def rate_value_matches(value: str) -> list[re.Match[str]]:
    return list(RATE_VALUE_RE.finditer(str(value or "")))


def rate_value_from_match(match: re.Match[str]) -> float:
    return float(next(group for group in match.groups() if group is not None))


def strip_rate_markers(value: str) -> str:
    text = RATE_VALUE_RE.sub("", str(value or ""))
    text = re.sub(r"选择分组.*$", "", text)
    return text.strip(" /:-：|｜")


def trim_source_before_select_group(value: str) -> str:
    text = str(value or "")
    if "选择分组" in text:
        text = text.split("选择分组", 1)[0]
    return text


def parse_group_and_rate(value: str) -> tuple[str, float | None]:
    text = trim_source_before_select_group(value)
    rate_matches = rate_value_matches(text)
    if not rate_matches:
        return "", None
    rate_match = rate_matches[0]
    before_rate = strip_rate_markers(text[: rate_match.start()])
    group = last_group_candidate(semantic_parts(before_rate), before_rate)
    return group, rate_value_from_match(rate_matches[-1])


def trim_group_prefix(group: str, account_name: str) -> str:
    text = sanitize(group).strip(" /:：|｜-")
    account = sanitize(account_name).strip()
    if account and text.startswith(account):
        text = text[len(account) :].strip()
    status_words = (
        "已启用",
        "未启用",
        "已禁用",
        "禁用",
        "无限额度",
        "有限额度",
        "Select this row",
        "on",
        "off",
    )
    changed = True
    while changed:
        changed = False
        for word in status_words:
            if text.startswith(word):
                text = text[len(word) :].strip(" /:：|｜-")
                changed = True
    return clean_group_candidate(text) or clean_group_candidate(group)


def parse_account_from_source_line(source_line: str) -> dict[str, Any] | None:
    source = " ".join(sanitize(source_line).split())
    match = MASKED_KEY_RE.search(source)
    if not match:
        return None
    before_key = source[: match.start()].strip(" /:：|｜-")
    after_key = source[match.end() :].strip()
    before_parts = semantic_parts(before_key)
    account_name = first_account_candidate(before_parts, before_key)
    if not account_name:
        return None

    upstream_group, page_rate = parse_group_and_rate(after_key)
    if page_rate is None:
        before_group, before_rate = parse_group_and_rate(before_key)
        upstream_group = upstream_group or before_group
        page_rate = before_rate
    upstream_group = trim_group_prefix(upstream_group, account_name)
    return {
        "account_name": account_name[:160],
        "upstream_group": upstream_group[:160],
        "page_rate": page_rate,
        "source_line": source[:260],
    }


def amount_from_line(line: str) -> str:
    match = re.search(r"([$¥￥]\s*-?\d+(?:\.\d+)?)", line)
    if match:
        return " ".join(match.group(1).split())
    for match in re.finditer(r"(?<![A-Za-z0-9_.-])-?\d+(?:\.\d+)?(?![A-Za-z0-9_.-])", line):
        suffix = line[match.end() : match.end() + 8]
        if re.match(r"\s*(?:x\b|条|次|tokens?\b|rpm\b|tpm\b|[kKmM]\b)", suffix, re.IGNORECASE):
            continue
        return match.group(0)
    return ""


def has_money_amount(value: str) -> bool:
    return bool(re.search(r"[$¥￥]\s*-?\d+(?:\.\d+)?", str(value or "")))


def is_probable_balance_label(value: str) -> bool:
    text = sanitize(str(value or "")).strip()
    if not text or is_pseudo_balance(text) or is_collector_panel_noise(text):
        return False
    if is_token_quota_context(text):
        return False
    if len(text) > 180:
        return False
    if re.search(r"\b\d+(?:\.\d+)?\s*x\b", text, re.IGNORECASE) and not has_money_amount(text):
        return False
    lower = text.lower()
    if has_money_amount(text):
        return True
    if any(keyword in text or keyword in lower for keyword in BALANCE_KEYWORDS):
        return bool(amount_from_line(text))
    return False


def is_token_quota_context(value: str) -> bool:
    text = sanitize(str(value or ""))
    return bool(
        "剩余额度/总额度" in text
        or ("剩余额度" in text and "总额度" in text)
        or ("剩余额度" in text and any(marker in text for marker in ("密钥", "分组", "已启用", "未启用", "已禁用")))
    )


def is_pricing_like(page_url: str, text: str) -> bool:
    path = urlparse(page_url or "").path.lower()
    if any(part in path for part in ("/pricing", "/price", "/models")):
        return True
    haystack = sanitize(text)
    markers = ("可用令牌分组", "模型倍率", "模型名称", "计费模型", "按Token", "按次", "输入价格", "输出价格", "模型价格")
    return sum(1 for marker in markers if marker in haystack) >= 2


def is_usage_or_billing_page(page_url: str) -> bool:
    path = urlparse(page_url or "").path.lower()
    return any(
        marker in path
        for marker in (
            "/usage",
            "/log",
            "/logs",
            "/record",
            "/records",
            "/billing",
            "/bill",
            "/order",
            "/orders",
            "/payment",
            "/payments",
            "/recharge",
            "/topup",
        )
    )


def is_logged_in_dashboard_page(page_url: str, text: str) -> bool:
    parsed = urlparse(page_url or "")
    path = parsed.path.lower().rstrip("/")
    if is_usage_or_billing_page(page_url) or is_pricing_like(page_url, text):
        return False
    if path in ("", "/", "/dashboard", "/console", "/home", "/keys", "/token", "/tokens", "/apikey", "/apikeys"):
        return True
    haystack = sanitize(text)
    dashboard_markers = ("仪表盘", "控制台", "API 密钥", "创建密钥", "个人资料", "服务器状态")
    return sum(1 for marker in dashboard_markers if marker in haystack) >= 2


def money_context_is_usage_like(context: str) -> bool:
    return any(
        marker in context
        for marker in (
            "今日",
            "近30天",
            "近 30 天",
            "用量",
            "使用记录",
            "日志",
            "请求",
            "扣费",
            "消费",
            "消耗",
            "价格",
            "单价",
            "模型价格",
        )
    )


def isolated_dashboard_money_balance(text: str, page_url: str) -> str:
    if not is_logged_in_dashboard_page(page_url, text):
        return ""
    haystack = sanitize(text)
    money_matches = list(re.finditer(r"[$¥￥]\s*-?\d+(?:\.\d+)?", haystack))
    candidates: list[str] = []
    for match in money_matches:
        context = haystack[max(0, match.start() - 36) : match.end() + 36]
        if money_context_is_usage_like(context):
            continue
        candidates.append(" ".join(match.group(0).split()))
    if len(candidates) == 1:
        return f"余额 {candidates[0]}"[:120]
    return ""


def detect_balance(text: str, page_url: str = "") -> str:
    if is_pricing_like(page_url, text):
        return ""
    if is_token_quota_context(text):
        return ""
    lines = source_lines(text)
    pattern = (
        r"(当前余额|账户余额|账号余额|可用余额|剩余余额|余额|剩余额度|可用额度|"
        r"balance|remaining|quota)[^\n$¥￥0-9-]{0,40}"
        r"([$¥￥]?\s*-?\d+(?:\.\d+)?)"
    )
    match = re.search(pattern, "\n".join(lines), re.IGNORECASE)
    if match:
        label = match.group(1)
        amount = " ".join(match.group(2).split())
        return f"{label} {amount}"[:120]
    for idx, line in enumerate(lines):
        lower = line.lower()
        if not any(keyword in line or keyword in lower for keyword in BALANCE_KEYWORDS):
            continue
        for candidate in lines[idx : min(len(lines), idx + 4)]:
            if len(candidate) > 180 and not has_money_amount(candidate):
                continue
            if (
                re.search(r"\b\d+(?:\.\d+)?\s*x\b", candidate, re.IGNORECASE)
                and not has_money_amount(candidate)
            ):
                continue
            amount = amount_from_line(candidate)
            if amount:
                if len(candidate) <= 120 and amount in candidate:
                    return candidate[:120]
                label = next(
                    (keyword for keyword in BALANCE_KEYWORDS if keyword in line or keyword in lower),
                    "余额",
                )
                return f"{label} {amount}"[:120]
    return isolated_dashboard_money_balance(text, page_url)


def normalize_rate_line(value: Any) -> str:
    if isinstance(value, dict):
        source_line = sanitize(str(value.get("source_line") or ""))
        model = sanitize(str(value.get("model") or value.get("group") or value.get("name") or ""))[:160]
        page_rate = value.get("page_rate", value.get("rate"))
        if source_line:
            return source_line[:220]
        if page_rate is not None and model:
            return f"{model} / {page_rate}x"[:220]
        if model:
            return model[:220]
        return ""
    return sanitize(str(value or ""))[:220]


def rate_line_is_too_noisy(value: str) -> bool:
    text = sanitize(str(value or ""))
    if not text:
        return True
    if len(text) > 320:
        return True
    noisy_markers = (
        "仪表盘",
        "API 密钥管理",
        "创建密钥",
        "全部分组",
        "全部状态",
        "使用记录",
        "渠道状态",
        "我的订阅",
        "个人资料",
        "OpenAI 兼容接口",
    )
    return sum(1 for marker in noisy_markers if marker in text) >= 3


def normalize_rate_lines(value: Any) -> list[str]:
    if not isinstance(value, list):
        return []
    rows: list[str] = []
    seen: set[str] = set()
    for item in value:
        line = normalize_rate_line(item)
        if not line or line in seen or rate_line_is_too_noisy(line):
            continue
        seen.add(line)
        rows.append(line)
        if len(rows) >= 20:
            break
    return rows


def normalize_match_text(value: Any) -> str:
    return re.sub(r"\s+", "", sanitize(str(value or "")).lower())


def repair_detected_account_name(site: str, account_name: str) -> str:
    name = sanitize(str(account_name or "")).strip()
    # TokensKingdom's virtualized table can drop the first visible "k" from
    # the first row.  Keep this repair narrow so other provider names are not
    # guessed from partial text.
    if site == "api.tokenskingdom.com" and name.startswith("ingdom "):
        return "k" + name
    return name


def looks_like_quota_only_account_line(account_name: str, source_line: str) -> bool:
    account = sanitize(str(account_name or "")).strip()
    source = sanitize(str(source_line or ""))
    if not account or not source:
        return False
    if re.search(
        r"codex|claude|gpt|deepseek|gemini|grok|sonnet|opus|haiku|meow|magic|kingdom|kbq|mouubox|超超|钧澈|聪明|生图|仅文字|文字|plus|pro|team|cc\s*max|ccmax",
        account,
        re.IGNORECASE,
    ):
        return False
    has_quota_pair = bool(re.search(r"[¥￥$]\s*\d+(?:\.\d+)?\s*/\s*[¥￥$]\s*\d+(?:\.\d+)?", source))
    has_key_markers = any(marker in source for marker in ("quota usage", "已启用", "无限额度", "有限额度", "Tag:"))
    has_group_rate = bool(RATE_VALUE_RE.search(source))
    return has_quota_pair and has_key_markers and has_group_rate


def normalize_detected_accounts(value: Any, *, site: str = "") -> list[dict[str, Any]]:
    if not isinstance(value, list):
        return []
    rows: list[dict[str, Any]] = []
    seen: set[tuple[str, str, str]] = set()
    for item in value:
        if not isinstance(item, dict):
            continue
        account_name = sanitize(str(item.get("account_name") or item.get("name") or ""))[:160]
        upstream_group = sanitize(str(item.get("upstream_group") or item.get("group") or ""))[:160]
        source_line = sanitize(str(item.get("source_line") or ""))[:260]
        parsed = parse_account_from_source_line(source_line)
        page_rate = item.get("page_rate", item.get("rate"))
        if parsed:
            account_name = parsed["account_name"]
            upstream_group = parsed["upstream_group"] or upstream_group
            page_rate = parsed["page_rate"] if parsed["page_rate"] is not None else page_rate
            source_line = parsed["source_line"]
        account_name = repair_detected_account_name(site, account_name)
        try:
            page_rate_value = float(page_rate) if page_rate not in (None, "") else None
        except (TypeError, ValueError):
            page_rate_value = None
        if not account_name or is_noise_account_name(account_name):
            continue
        if source_line and is_collector_panel_noise(source_line):
            continue
        if looks_like_quota_only_account_line(account_name, source_line):
            continue
        if page_rate_value is None and not upstream_group:
            continue
        if "Tag:" in source_line and normalize_match_text(account_name) == normalize_match_text(upstream_group):
            continue
        key = (account_name, upstream_group, str(page_rate_value))
        if key in seen:
            continue
        seen.add(key)
        rows.append(
            {
                "account_name": account_name,
                "upstream_group": upstream_group,
                "page_rate": page_rate_value,
                "source_line": source_line or account_name,
            }
        )
        if len(rows) >= 40:
            break
    return rows


def clean_observed_at(value: Any) -> str:
    raw = sanitize(str(value or ""))[:80]
    if not raw:
        return datetime.now(timezone.utc).isoformat(timespec="seconds")
    try:
        parsed = datetime.fromisoformat(raw.replace("Z", "+00:00"))
    except ValueError:
        return datetime.now(timezone.utc).isoformat(timespec="seconds")
    if not parsed.tzinfo:
        parsed = parsed.replace(tzinfo=timezone.utc)
    return parsed.astimezone(timezone.utc).isoformat(timespec="seconds")


def normalize_observation(item: Any) -> dict[str, Any] | None:
    if not isinstance(item, dict):
        return None
    provider = provider_for_payload(item)
    if not provider:
        return None

    page_url = sanitize(str(item.get("page_url") or item.get("url") or ""))[:800]
    if page_url and provider_for_url(page_url) != provider:
        return None

    page_title = sanitize(str(item.get("page_title") or item.get("title") or ""))[:240]
    script_version = sanitize(str(item.get("script_version") or ""))[:40]
    text = sanitize(str(item.get("sanitized_excerpt") or item.get("text") or ""))
    detected_rates = normalize_rate_lines(item.get("detected_rates"))
    detected_accounts = normalize_detected_accounts(item.get("detected_accounts"), site=provider.site)
    raw_balance_full = sanitize(str(item.get("detected_balance") or ""))
    if is_pseudo_balance(raw_balance_full):
        raw_balance_full = ""
    raw_balance = raw_balance_full[:120] if is_probable_balance_label(raw_balance_full[:120]) else ""
    if is_pseudo_balance(raw_balance):
        raw_balance = ""
    cleaned_raw_balance = detect_balance(raw_balance_full[:1000], page_url) if raw_balance_full else ""
    detected_balance = "" if is_pricing_like(page_url, text) else raw_balance or cleaned_raw_balance or detect_balance(text, page_url)
    if is_pseudo_balance(detected_balance):
        detected_balance = ""
    if provider.name == "KBQ":
        detected_accounts = []
        detected_rates = []
    excerpt = compact_excerpt(text)
    status = sanitize(str(item.get("status") or ""))[:80]
    if status not in ALLOWED_STATUS:
        status = "browser_observed" if (detected_balance or detected_rates or detected_accounts or excerpt) else "browser_observed_empty"
    detail = sanitize(str(item.get("detail") or ""))[:500]
    if not detail:
        detail = (
            "Chrome Tampermonkey read-only snapshot; "
            f"balance={'yes' if detected_balance else 'no'}; "
            f"account_lines={len(detected_accounts)}; "
            f"rate_lines={len(detected_rates)}"
        )
    if script_version and "Chrome Tampermonkey read-only snapshot" in detail and "script=" not in detail:
        detail = f"{detail}; script={script_version}"
    if provider.name == "KBQ":
        detail = f"{detail}; KBQ browser rows ignored for pricing truth"

    return {
        "provider": provider.name,
        "site": provider.site,
        "browser": "chrome",
        "script_version": script_version,
        "status": status,
        "detail": detail,
        "observed_at": clean_observed_at(item.get("observed_at")),
        "page_url": page_url,
        "page_title": page_title,
        "detected_balance": detected_balance,
        "detected_accounts": detected_accounts,
        "detected_rates": detected_rates,
        "sanitized_excerpt": excerpt,
    }


def load_token(path: Path) -> str:
    token = path.read_text(encoding="utf-8").strip()
    if not token:
        raise RuntimeError(f"empty collector token file: {path}")
    return token


def init_token(path: Path) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    os.chmod(path.parent, 0o700)
    if path.exists():
        os.chmod(path, 0o600)
        print(f"token file already exists: {path}")
        return
    token = secrets.token_urlsafe(32)
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as handle:
        handle.write(token + "\n")
    os.chmod(path, 0o600)
    print(f"created token file: {path}")
    print("open this local file yourself and paste the token into the Tampermonkey menu; do not paste it into chat.")


def request_source_allowed(headers: Any) -> bool:
    origin = headers.get("Origin", "")
    referer = headers.get("Referer", "")
    collector_source = headers.get("X-Collector-Source", "")
    checked_any = False
    for value in (origin, referer):
        if not value:
            continue
        checked_any = True
        host = urlparse(value).netloc.lower().split("@")[-1].split(":")[0]
        if not host or not any(host_matches(host, alias) for alias in all_aliases()):
            return False
    if checked_any:
        return True
    if collector_source:
        source_host = str(collector_source).lower().strip().split("@")[-1].split(":")[0]
        return bool(source_host) and any(host_matches(source_host, alias) for alias in all_aliases())
    return False


def read_existing_latest(path: Path) -> list[dict[str, Any]]:
    if not path.exists():
        return []
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except (json.JSONDecodeError, OSError):
        return []
    if not isinstance(data, list):
        return []
    rows: list[dict[str, Any]] = []
    for item in data:
        normalized = normalize_observation(item)
        if normalized:
            rows.append(normalized)
    return rows


def merge_latest(existing: list[dict[str, Any]], incoming: list[dict[str, Any]]) -> list[dict[str, Any]]:
    merged: dict[tuple[str, str], dict[str, Any]] = {
        (item["provider"], item["site"]): item for item in existing
    }
    for item in incoming:
        key = (item["provider"], item["site"])
        previous = merged.get(key)
        merged[key] = merge_observation_preserving_signal(previous, item)
    return sorted(merged.values(), key=lambda row: (row["provider"], row["site"]))


def observation_signal_score(item: dict[str, Any] | None) -> int:
    if not item:
        return 0
    accounts = item.get("detected_accounts") or []
    rates = item.get("detected_rates") or []
    balance = str(item.get("detected_balance") or "").strip()
    return (len(accounts) * 3) + (len(rates) * 3) + (1 if balance else 0)


def merge_observation_preserving_signal(previous: dict[str, Any] | None, current: dict[str, Any]) -> dict[str, Any]:
    if not previous:
        return current
    if observation_signal_score(current) == 0 and observation_signal_score(previous) > 0:
        preserved = dict(previous)
        preserved["observed_at"] = current.get("observed_at") or previous.get("observed_at")
        preserved["detail"] = (
            f"{previous.get('detail', '')}; ignored low-signal collector snapshot "
            f"from {current.get('observed_at', '')}"
        ).strip("; ")
        return preserved
    merged = dict(current)
    if not merged.get("detected_accounts") and previous.get("detected_accounts"):
        merged["detected_accounts"] = previous["detected_accounts"]
        merged["detail"] = (
            f"{merged.get('detail', '')}; preserved previous account lines "
            f"from {previous.get('observed_at', '')}"
        ).strip("; ")
    elif (
        merged.get("detected_accounts")
        and previous.get("detected_accounts")
        and len(merged.get("detected_accounts") or []) < len(previous.get("detected_accounts") or [])
    ):
        merged["detail"] = (
            f"{merged.get('detail', '')}; partial account snapshot "
            f"{len(merged.get('detected_accounts') or [])}/{len(previous.get('detected_accounts') or [])} "
            f"compared with previous {previous.get('observed_at', '')}"
        ).strip("; ")
    if not merged.get("detected_rates") and previous.get("detected_rates"):
        merged["detected_rates"] = previous["detected_rates"]
        merged["detail"] = (
            f"{merged.get('detail', '')}; preserved previous rate lines "
            f"from {previous.get('observed_at', '')}"
        ).strip("; ")
    if not merged.get("detected_balance") and previous.get("detected_balance"):
        merged["detected_balance"] = previous["detected_balance"]
    return merged


def write_json_atomic(path: Path, data: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp_path = path.with_suffix(path.suffix + ".tmp")
    tmp_path.write_text(json.dumps(data, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    os.replace(tmp_path, path)


def commands_path(command_dir: Path) -> Path:
    return command_dir / COMMANDS_FILE_NAME


def now_utc() -> datetime:
    return datetime.now(timezone.utc)


def isoformat_utc(value: datetime) -> str:
    return value.astimezone(timezone.utc).isoformat(timespec="seconds")


def parse_datetime(value: Any) -> datetime | None:
    raw = str(value or "").strip()
    if not raw:
        return None
    try:
        parsed = datetime.fromisoformat(raw.replace("Z", "+00:00"))
    except ValueError:
        return None
    if not parsed.tzinfo:
        parsed = parsed.replace(tzinfo=timezone.utc)
    return parsed.astimezone(timezone.utc)


def load_commands(command_dir: Path) -> list[dict[str, Any]]:
    path = commands_path(command_dir)
    if not path.exists():
        return []
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except (json.JSONDecodeError, OSError):
        return []
    if not isinstance(data, list):
        return []
    commands: list[dict[str, Any]] = []
    for item in data:
        if not isinstance(item, dict):
            continue
        provider = provider_for_site(str(item.get("site") or ""))
        if not provider:
            continue
        action = str(item.get("action") or "")
        if action not in ALLOWED_COMMAND_ACTIONS:
            continue
        status = str(item.get("status") or "pending")
        if status not in {"pending", "claimed", "done", "error", "expired"}:
            status = "pending"
        command = {
            "id": sanitize(str(item.get("id") or ""))[:80],
            "provider": provider.name,
            "site": provider.site,
            "action": action,
            "status": status,
            "created_at": clean_observed_at(item.get("created_at")),
            "expires_at": clean_observed_at(item.get("expires_at")),
            "reason": sanitize(str(item.get("reason") or ""))[:240],
            "claim_count": int(item.get("claim_count") or 0),
        }
        for key in ("claimed_at", "acknowledged_at", "detail"):
            if item.get(key):
                command[key] = sanitize(str(item.get(key)))[:500]
        if command["id"]:
            commands.append(command)
    return commands


def save_commands(command_dir: Path, commands: list[dict[str, Any]]) -> None:
    command_dir.mkdir(parents=True, exist_ok=True)
    write_json_atomic(commands_path(command_dir), commands[-MAX_STORED_COMMANDS:])


def command_is_expired(command: dict[str, Any], now: datetime | None = None) -> bool:
    expires_at = parse_datetime(command.get("expires_at"))
    if not expires_at:
        return True
    return expires_at <= (now or now_utc())


def prune_commands(commands: list[dict[str, Any]], now: datetime | None = None) -> list[dict[str, Any]]:
    now = now or now_utc()
    kept: list[dict[str, Any]] = []
    keep_after = now - timedelta(hours=24)
    for command in commands:
        status = str(command.get("status") or "")
        if status in {"done", "error", "expired"}:
            acknowledged_at = parse_datetime(command.get("acknowledged_at")) or parse_datetime(command.get("expires_at"))
            if acknowledged_at and acknowledged_at < keep_after:
                continue
        if status in {"pending", "claimed"} and command_is_expired(command, now):
            command = {**command, "status": "expired", "acknowledged_at": isoformat_utc(now), "detail": "command expired before completion"}
        kept.append(command)
    return kept[-MAX_STORED_COMMANDS:]


def prune_snapshots(snapshot_dir: Path, keep: int = MAX_STORED_SNAPSHOTS) -> None:
    """Keep the rolling collector archive bounded without touching latest.json."""

    if keep <= 0 or not snapshot_dir.exists():
        return
    snapshots = sorted(
        (path for path in snapshot_dir.glob("snapshot-*.json") if path.is_file()),
        key=lambda path: (path.stat().st_mtime, path.name),
    )
    for old_snapshot in snapshots[:-keep]:
        try:
            old_snapshot.unlink()
        except OSError:
            continue


def create_command(command_dir: Path, target: str, *, action: str = "refresh_then_send", ttl_seconds: int = DEFAULT_COMMAND_TTL_SECONDS, reason: str = "") -> dict[str, Any]:
    provider = provider_for_command_target(target)
    if not provider:
        raise ValueError("unknown_provider")
    if action not in ALLOWED_COMMAND_ACTIONS:
        raise ValueError("invalid_action")
    ttl_seconds = max(30, min(int(ttl_seconds), MAX_COMMAND_TTL_SECONDS))
    now = now_utc()
    commands = prune_commands(load_commands(command_dir), now)
    command = {
        "id": secrets.token_urlsafe(12),
        "provider": provider.name,
        "site": provider.site,
        "action": action,
        "status": "pending",
        "created_at": isoformat_utc(now),
        "expires_at": isoformat_utc(now + timedelta(seconds=ttl_seconds)),
        "reason": sanitize(reason)[:240],
        "claim_count": 0,
    }
    commands.append(command)
    save_commands(command_dir, commands)
    return command


def command_public_view(command: dict[str, Any]) -> dict[str, Any]:
    return {
        "id": command["id"],
        "provider": command["provider"],
        "site": command["site"],
        "action": command["action"],
        "expires_at": command["expires_at"],
        "reason": command.get("reason", ""),
    }


def claim_commands(command_dir: Path, site: str, *, limit: int = 1) -> list[dict[str, Any]]:
    provider = provider_for_site(site)
    if not provider:
        return []
    now = now_utc()
    commands = prune_commands(load_commands(command_dir), now)
    claimed: list[dict[str, Any]] = []
    for command in commands:
        if len(claimed) >= limit:
            break
        if command.get("provider") != provider.name or command.get("site") != provider.site:
            continue
        if command.get("status") != "pending" or command_is_expired(command, now):
            continue
        command["status"] = "claimed"
        command["claimed_at"] = isoformat_utc(now)
        command["claim_count"] = int(command.get("claim_count") or 0) + 1
        claimed.append(command_public_view(command))
    save_commands(command_dir, commands)
    return claimed


def acknowledge_command(command_dir: Path, command_id: str, *, status: str = "done", detail: str = "") -> bool:
    if status not in ALLOWED_COMMAND_ACK_STATUS:
        status = "done"
    now = now_utc()
    commands = prune_commands(load_commands(command_dir), now)
    matched = False
    for command in commands:
        if hmac.compare_digest(str(command.get("id") or ""), str(command_id or "")):
            command["status"] = status
            command["acknowledged_at"] = isoformat_utc(now)
            command["detail"] = sanitize(detail)[:500]
            matched = True
            break
    save_commands(command_dir, commands)
    return matched


def write_snapshots(snapshot_dir: Path, incoming: list[dict[str, Any]]) -> tuple[Path, int]:
    snapshot_dir.mkdir(parents=True, exist_ok=True)
    latest_path = snapshot_dir / "latest.json"
    existing = read_existing_latest(latest_path)
    merged = merge_latest(existing, incoming)
    timestamp = datetime.now(timezone.utc).strftime("%Y%m%d-%H%M%S-%fZ")
    write_json_atomic(snapshot_dir / f"snapshot-{timestamp}.json", merged)
    prune_snapshots(snapshot_dir, keep=MAX_STORED_SNAPSHOTS)
    write_json_atomic(latest_path, merged)
    return latest_path, len(merged)


class CollectorServer(ThreadingHTTPServer):
    allow_reuse_address = True

    def __init__(
        self,
        server_address: tuple[str, int],
        handler_class: type[BaseHTTPRequestHandler],
        *,
        token: str,
        snapshot_dir: Path,
        command_dir: Path,
        max_body_bytes: int,
        userscript_path: Path,
    ):
        super().__init__(server_address, handler_class)
        self.collector_token = token
        self.snapshot_dir = snapshot_dir
        self.command_dir = command_dir
        self.max_body_bytes = max_body_bytes
        self.userscript_path = userscript_path
        self.command_lock = threading.Lock()

    def server_bind(self) -> None:
        # HTTPServer.server_bind calls socket.getfqdn(host), which can hang on
        # some macOS DNS setups. This collector is strictly local, so the bound
        # IP is enough for server metadata.
        if self.allow_reuse_address:
            self.socket.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        self.socket.bind(self.server_address)
        self.server_address = self.socket.getsockname()
        self.server_name = str(self.server_address[0])
        self.server_port = int(self.server_address[1])


class CollectorHandler(BaseHTTPRequestHandler):
    server: CollectorServer

    def do_POST(self) -> None:
        parsed = urlparse(self.path)
        if parsed.path == "/ingest":
            self.handle_ingest()
            return
        if parsed.path == "/commands":
            self.handle_create_command()
            return
        if parsed.path == "/command-ack":
            self.handle_command_ack()
            return
        self.send_json(HTTPStatus.NOT_FOUND, {"error": "not_found"})

    def do_GET(self) -> None:
        parsed = urlparse(self.path)
        if parsed.path == "/health":
            self.send_json(HTTPStatus.OK, {"status": "ok"})
            return
        if parsed.path == "/commands":
            self.handle_claim_commands(parsed.query)
            return
        if parsed.path in {"/userscript", "/userscript/fluter-upstream-readonly-collector.user.js"}:
            self.handle_userscript()
            return
        if parsed.path in {"/", "/install"}:
            self.handle_install_page()
            return
        self.send_json(HTTPStatus.NOT_FOUND, {"error": "not_found"})

    def do_HEAD(self) -> None:
        parsed = urlparse(self.path)
        if parsed.path in {"/userscript", "/userscript/fluter-upstream-readonly-collector.user.js"}:
            self.handle_userscript(head_only=True)
            return
        if parsed.path == "/health":
            body = b'{"status": "ok"}'
            self.send_response(HTTPStatus.OK.value)
            self.send_header("Content-Type", "application/json; charset=utf-8")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            return
        self.send_response(HTTPStatus.NOT_FOUND.value)
        self.end_headers()

    def token_allowed(self) -> bool:
        return hmac.compare_digest(self.headers.get("X-Collector-Token", ""), self.server.collector_token)

    def handle_ingest(self) -> None:
        if not self.token_allowed():
            self.send_json(HTTPStatus.FORBIDDEN, {"error": "forbidden"})
            return
        if not request_source_allowed(self.headers):
            self.send_json(HTTPStatus.FORBIDDEN, {"error": "source_not_allowed"})
            return
        try:
            body = self.read_limited_body()
        except ValueError as exc:
            self.send_json(HTTPStatus.REQUEST_ENTITY_TOO_LARGE, {"error": str(exc)})
            return
        try:
            data = json.loads(body.decode("utf-8"))
        except (UnicodeDecodeError, json.JSONDecodeError):
            self.send_json(HTTPStatus.BAD_REQUEST, {"error": "invalid_json"})
            return
        if not isinstance(data, list):
            self.send_json(HTTPStatus.BAD_REQUEST, {"error": "payload_must_be_list"})
            return
        observations = [row for row in (normalize_observation(item) for item in data) if row]
        if not observations:
            self.send_json(HTTPStatus.BAD_REQUEST, {"error": "no_valid_observations"})
            return
        latest_path, merged_count = write_snapshots(self.server.snapshot_dir, observations)
        self.send_json(
            HTTPStatus.OK,
            {
                "ok": True,
                "accepted": len(observations),
                "latest": str(latest_path),
                "latest_count": merged_count,
            },
        )

    def handle_create_command(self) -> None:
        # This operator endpoint is for local Codex/shell only. Browser command
        # polling and acknowledgements still require a trusted upstream page
        # source; creation relies on localhost binding plus the collector token.
        if not self.token_allowed():
            self.send_json(HTTPStatus.FORBIDDEN, {"error": "forbidden"})
            return
        try:
            body = self.read_limited_body()
        except ValueError as exc:
            self.send_json(HTTPStatus.REQUEST_ENTITY_TOO_LARGE, {"error": str(exc)})
            return
        try:
            data = json.loads(body.decode("utf-8"))
        except (UnicodeDecodeError, json.JSONDecodeError):
            self.send_json(HTTPStatus.BAD_REQUEST, {"error": "invalid_json"})
            return
        if not isinstance(data, dict):
            self.send_json(HTTPStatus.BAD_REQUEST, {"error": "payload_must_be_object"})
            return
        target = str(data.get("site") or data.get("provider") or "")
        action = str(data.get("action") or "refresh_then_send")
        ttl_seconds = int(data.get("ttl_seconds") or DEFAULT_COMMAND_TTL_SECONDS)
        reason = str(data.get("reason") or "")
        try:
            with self.server.command_lock:
                command = create_command(self.server.command_dir, target, action=action, ttl_seconds=ttl_seconds, reason=reason)
        except ValueError as exc:
            self.send_json(HTTPStatus.BAD_REQUEST, {"error": str(exc)})
            return
        self.send_json(HTTPStatus.CREATED, {"ok": True, "command": command_public_view(command)})

    def handle_claim_commands(self, query: str) -> None:
        if not self.token_allowed():
            self.send_json(HTTPStatus.FORBIDDEN, {"error": "forbidden"})
            return
        if not request_source_allowed(self.headers):
            self.send_json(HTTPStatus.FORBIDDEN, {"error": "source_not_allowed"})
            return
        params = parse_qs(query)
        site = (params.get("site") or [""])[0]
        if not site:
            self.send_json(HTTPStatus.BAD_REQUEST, {"error": "missing_site"})
            return
        with self.server.command_lock:
            commands = claim_commands(self.server.command_dir, site, limit=1)
        self.send_json(HTTPStatus.OK, {"ok": True, "commands": commands})

    def handle_command_ack(self) -> None:
        if not self.token_allowed():
            self.send_json(HTTPStatus.FORBIDDEN, {"error": "forbidden"})
            return
        if not request_source_allowed(self.headers):
            self.send_json(HTTPStatus.FORBIDDEN, {"error": "source_not_allowed"})
            return
        try:
            body = self.read_limited_body()
        except ValueError as exc:
            self.send_json(HTTPStatus.REQUEST_ENTITY_TOO_LARGE, {"error": str(exc)})
            return
        try:
            data = json.loads(body.decode("utf-8"))
        except (UnicodeDecodeError, json.JSONDecodeError):
            self.send_json(HTTPStatus.BAD_REQUEST, {"error": "invalid_json"})
            return
        if not isinstance(data, dict):
            self.send_json(HTTPStatus.BAD_REQUEST, {"error": "payload_must_be_object"})
            return
        command_id = sanitize(str(data.get("id") or ""))[:80]
        if not command_id:
            self.send_json(HTTPStatus.BAD_REQUEST, {"error": "missing_id"})
            return
        status = sanitize(str(data.get("status") or "done"))[:20]
        detail = sanitize(str(data.get("detail") or ""))[:500]
        with self.server.command_lock:
            matched = acknowledge_command(self.server.command_dir, command_id, status=status, detail=detail)
        if not matched:
            self.send_json(HTTPStatus.NOT_FOUND, {"error": "not_found"})
            return
        self.send_json(HTTPStatus.OK, {"ok": True, "id": command_id, "status": status})

    def handle_userscript(self, *, head_only: bool = False) -> None:
        try:
            body = self.server.userscript_path.read_bytes()
        except OSError:
            self.send_json(HTTPStatus.NOT_FOUND, {"error": "userscript_not_found"})
            return
        self.send_response(HTTPStatus.OK.value)
        self.send_header("Content-Type", "application/javascript; charset=utf-8")
        self.send_header("Content-Disposition", 'inline; filename="fluter-upstream-readonly-collector.user.js"')
        self.send_header("Cache-Control", "no-store")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        if not head_only:
            self.wfile.write(body)

    def handle_install_page(self) -> None:
        script_url = "/userscript/fluter-upstream-readonly-collector.user.js"
        body = f"""<!doctype html>
<html lang="zh-CN">
<meta charset="utf-8">
<title>Fluter 上游采集脚本</title>
<style>
body{{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;max-width:760px;margin:48px auto;padding:0 20px;line-height:1.7;color:#172033}}
a.button{{display:inline-block;background:#172033;color:#fff;text-decoration:none;padding:10px 14px;border-radius:8px}}
code{{background:#f3f5f8;padding:2px 5px;border-radius:5px}}
.note{{color:#5a667a}}
</style>
<h1>Fluter 上游采集脚本</h1>
<p>这个本地页面只用于安装或更新 Tampermonkey 只读采集脚本。collector 只监听 <code>127.0.0.1</code>，不会对公网开放。</p>
<p><a class="button" href="{script_url}">安装 / 更新 userscript</a></p>
<p class="note">更新后刷新任意上游页面，右下角应出现“Fluter 上游采集”悬浮窗。</p>
</html>
""".encode("utf-8")
        self.send_response(HTTPStatus.OK.value)
        self.send_header("Content-Type", "text/html; charset=utf-8")
        self.send_header("Cache-Control", "no-store")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def read_limited_body(self) -> bytes:
        header = self.headers.get("Content-Length", "")
        try:
            content_length = int(header)
        except ValueError:
            raise ValueError("invalid_content_length") from None
        if content_length < 0 or content_length > self.server.max_body_bytes:
            raise ValueError("payload_too_large")
        return self.rfile.read(content_length)

    def send_json(self, status: HTTPStatus, payload: dict[str, Any]) -> None:
        body = json.dumps(payload, ensure_ascii=False).encode("utf-8")
        self.send_response(status.value)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, format: str, *args: Any) -> None:
        return


def run_server(args: argparse.Namespace) -> None:
    if args.host != DEFAULT_HOST:
        raise RuntimeError("collector must bind to 127.0.0.1")
    token = load_token(args.token_file)
    server = CollectorServer(
        (args.host, args.port),
        CollectorHandler,
        token=token,
        snapshot_dir=args.snapshot_dir,
        command_dir=args.command_dir,
        max_body_bytes=args.max_body_bytes,
        userscript_path=args.userscript_path,
    )
    print(f"collector listening on http://{args.host}:{args.port}/ingest")
    print(f"userscript URL: http://{args.host}:{args.port}/userscript")
    print(f"snapshot dir: {args.snapshot_dir}")
    print(f"command dir: {args.command_dir}")
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        print("\ncollector stopped")
    finally:
        server.server_close()


def sync_latest(args: argparse.Namespace) -> int:
    latest_path = args.snapshot_dir / "latest.json"
    if not latest_path.exists():
        raise RuntimeError(f"latest snapshot not found: {latest_path}")
    command = [
        "python3",
        str(args.import_script),
        "--import-json",
        str(latest_path),
        "--remote-ssh-host",
        args.remote_ssh_host,
    ]
    if args.no_remote_render_dashboard:
        command.append("--no-remote-render-dashboard")
    print("running:", " ".join(shlex.quote(part) for part in command))
    proc = subprocess.run(command, text=True, capture_output=True, check=False)
    if proc.stdout.strip():
        print(proc.stdout.strip())
    if proc.returncode != 0:
        if proc.stderr.strip():
            print(proc.stderr.strip())
        return proc.returncode
    return 0


def main() -> int:
    args = parse_args()
    if args.init_token:
        init_token(args.token_file)
        return 0
    if args.sync_latest:
        return sync_latest(args)
    if args.queue_command:
        command = create_command(
            args.command_dir,
            args.queue_command,
            action=args.command_action,
            ttl_seconds=args.command_ttl_seconds,
            reason=args.command_reason,
        )
        print(json.dumps({"ok": True, "command": command_public_view(command)}, ensure_ascii=False, indent=2))
        return 0
    scrub_sensitive_environment()
    run_server(args)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
