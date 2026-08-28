#!/usr/bin/env python3
"""Refresh logged-in browser read-only upstream observations.

This adapter is for upstream providers that do not expose a public pricing API.
It reads already-open Safari tabs, extracts only small sanitized balance/rate
summaries, and writes those summaries to the independent ledger SQLite DB.

It does not store cookies, passwords, full API keys, Bearer tokens, or raw page
HTML. It also does not modify sub2api production accounts, groups, channels, or
pricing.
"""

from __future__ import annotations

import argparse
import json
import re
import shlex
import sqlite3
import subprocess
import sys
from dataclasses import dataclass
from datetime import datetime, timezone
from decimal import Decimal
from pathlib import Path
from typing import Any
from urllib.parse import urlparse


DEFAULT_DB = "/var/lib/fluterapi-upstream-rates/upstream_rates.sqlite"
DEFAULT_REMOTE_SCRIPT = "/var/lib/fluterapi-upstream-rates/refresh_browser_readonly_adapters.py"
DEFAULT_REMOTE_RENDER_SCRIPT = "/var/lib/fluterapi-upstream-rates/render_upstream_dashboard.py"
DEFAULT_REMOTE_RENDER_OUTPUT = "/www/fluterapi-home/admin/upstream-rates/index.html"
DEFAULT_REMOTE_BACKUP_DIR = "/var/lib/fluterapi-upstream-rates/backups"
MIN_TAMPERMONKEY_ACCOUNT_SNAPSHOT_VERSION = (0, 1, 15)
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
    note: str


PROVIDERS = [
    Provider("Meow", "api.saki.lat", ("api.saki.lat", "saki.lat"), "logged-in dashboard/key page"),
    Provider("Magic", "pool.gptstore.club", ("pool.gptstore.club", "gptstore.club"), "logged-in dashboard/key page"),
    Provider(
        "Kingdom",
        "api.tokenskingdom.com",
        ("api.tokenskingdom.com", "image.tokenskingdom.com", "tokenskingdom.com"),
        "logged-in dashboard/key page; image.tokenskingdom.com is an API fast subdomain, not a separate dashboard",
    ),
    Provider("超超 Mouubox", "api.mouubox.com", ("api.mouubox.com",), "logged-in dashboard/key page"),
    Provider("超超 Mouubox 副站", "sub2api.mouubox.com", ("sub2api.mouubox.com",), "logged-in dashboard/key page"),
    Provider("聪明AI", "sub2.congmingai.com", ("sub2.congmingai.com",), "logged-in dashboard/key page"),
    Provider("乔燃", "mdkj.lol", ("mdkj.lol",), "logged-in dashboard/key page"),
    Provider("KBQ", "xn--vduyey89e.com", ("xn--vduyey89e.com",), "logged-in usage logs for cross-check"),
    Provider("钧澈", "vip.lcodex.cn", ("vip.lcodex.cn", "lcodex.cn"), "logged-in key page for balance/rate cross-check"),
]


SCHEMA = """
create table if not exists browser_adapter_status (
  provider text not null,
  site text not null,
  browser text not null,
  status text not null,
  detail text not null,
  observed_at text not null,
  unique(provider, site)
);

create table if not exists browser_adapter_snapshots (
  provider text not null,
  site text not null,
  browser text not null,
  page_url text not null,
  page_title text not null,
  detected_balance text not null,
  detected_accounts_json text not null default '[]',
  detected_rates_json text not null,
  sanitized_excerpt text not null,
  observed_at text not null,
  unique(provider, site)
);

create table if not exists metadata (
  key text primary key,
  value text not null
);

create table if not exists browser_adapter_rate_observations (
  provider text not null,
  site text not null,
  upstream_group text not null,
  page_rate real not null,
  source_line text not null,
  matched_ledger_rows integer not null,
  observed_at text not null,
  unique(provider, site, upstream_group)
);

create table if not exists browser_adapter_account_observations (
  provider text not null,
  site text not null,
  account_name text not null,
  normalized_account_name text not null,
  upstream_group text not null,
  page_rate real,
  source_line text not null,
  matched_ledger_rows integer not null,
  observed_at text not null,
  unique(provider, site, normalized_account_name)
);

create table if not exists browser_adapter_ledger_updates (
  id integer primary key autoincrement,
  provider text not null,
  site text not null,
  fluter_account_name text not null,
  upstream_group text not null,
  old_page_rate real,
  new_page_rate real not null,
  source_line text not null,
  observed_at text not null,
  applied_at text not null
);
"""


UPSERT_STATUS = """
insert into browser_adapter_status (
  provider, site, browser, status, detail, observed_at
) values (?, ?, ?, ?, ?, ?)
on conflict(provider, site) do update set
  browser = excluded.browser,
  status = excluded.status,
  detail = excluded.detail,
  observed_at = excluded.observed_at;
"""


UPSERT_SNAPSHOT = """
insert into browser_adapter_snapshots (
  provider, site, browser, page_url, page_title, detected_balance,
  detected_accounts_json, detected_rates_json, sanitized_excerpt, observed_at
) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
on conflict(provider, site) do update set
  browser = excluded.browser,
  page_url = excluded.page_url,
  page_title = excluded.page_title,
  detected_balance = excluded.detected_balance,
  detected_accounts_json = excluded.detected_accounts_json,
  detected_rates_json = excluded.detected_rates_json,
  sanitized_excerpt = excluded.sanitized_excerpt,
  observed_at = excluded.observed_at;
"""


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Refresh browser read-only upstream ledger adapters")
    parser.add_argument("--db", default=DEFAULT_DB)
    parser.add_argument("--browser", default="Safari", choices=("Safari",))
    parser.add_argument("--max-text-chars", type=int, default=16000)
    parser.add_argument(
        "--reload-wait-seconds",
        type=float,
        default=10.0,
        help="Wait this many seconds after refreshing open upstream tabs before reading text",
    )
    parser.set_defaults(reload_open_tabs=True)
    parser.add_argument(
        "--reload-open-tabs",
        dest="reload_open_tabs",
        action="store_true",
        help="Refresh matching logged-in upstream tabs before reading them. This is the default.",
    )
    parser.add_argument(
        "--no-reload-open-tabs",
        dest="reload_open_tabs",
        action="store_false",
        help="Do not refresh Safari tabs before reading. Use only for debugging stale snapshots.",
    )
    parser.add_argument(
        "--import-json",
        metavar="PATH_OR_DASH",
        help="Import observations from JSON instead of reading the local browser",
    )
    parser.add_argument(
        "--remote-ssh-host",
        help="Read the local browser, then import sanitized observations into the remote VPS ledger over SSH",
    )
    parser.add_argument("--remote-db", default=DEFAULT_DB)
    parser.add_argument("--remote-script", default=DEFAULT_REMOTE_SCRIPT)
    parser.add_argument("--remote-render-script", default=DEFAULT_REMOTE_RENDER_SCRIPT)
    parser.add_argument("--remote-render-output", default=DEFAULT_REMOTE_RENDER_OUTPUT)
    parser.add_argument("--remote-backup-dir", default=DEFAULT_REMOTE_BACKUP_DIR)
    parser.set_defaults(remote_render_dashboard=True)
    parser.add_argument(
        "--remote-render-dashboard",
        dest="remote_render_dashboard",
        action="store_true",
        help="After remote import, back up and re-render the static VPS dashboard. This is the default.",
    )
    parser.add_argument(
        "--no-remote-render-dashboard",
        dest="remote_render_dashboard",
        action="store_false",
        help="Import remote browser observations without re-rendering the static dashboard.",
    )
    return parser.parse_args()


def applescript_quote(value: str) -> str:
    return '"' + value.replace("\\", "\\\\").replace('"', '\\"') + '"'


def provider_aliases() -> list[str]:
    return sorted({alias for provider in PROVIDERS for alias in provider.aliases})


def run_osascript(script: str, *, language: str | None = None) -> subprocess.CompletedProcess[str]:
    command = ["osascript"]
    if language:
        command.extend(["-l", language])
    return subprocess.run(
        command,
        input=script,
        text=True,
        capture_output=True,
        check=False,
    )


def safari_reload_jxa(wait_seconds: float) -> str:
    aliases = json.dumps(provider_aliases(), ensure_ascii=False)
    delay_seconds = max(0.0, min(wait_seconds, 30.0))
    return f"""
function run() {{
  const Safari = Application("Safari");
  const aliases = {aliases};
  let touched = 0;

  function safeUrl(tab) {{
    try {{
      return String(tab.url() || "");
    }} catch (error) {{
      return "";
    }}
  }}

  function matchesProvider(url) {{
    return aliases.some(alias => url.includes(alias));
  }}

  Safari.windows().forEach(win => {{
    win.tabs().forEach(tab => {{
      const url = safeUrl(tab);
      if (!matchesProvider(url)) {{
        return;
      }}
      try {{
        Safari.doJavaScript("location.reload()", {{in: tab}});
        touched += 1;
      }} catch (error) {{
        // Keep the hourly adapter useful even if one tab is mid-navigation.
      }}
    }});
  }});

  if (touched > 0) {{
    delay({delay_seconds});
  }}
  return String(touched);
}}
"""


def reload_safari_provider_tabs(wait_seconds: float) -> int:
    proc = run_osascript(safari_reload_jxa(wait_seconds), language="JavaScript")
    if proc.returncode != 0:
        # Fallback for older macOS/JXA edge cases. This path is intentionally
        # best-effort: a reload failure should not prevent read-only snapshots.
        aliases = provider_aliases()
        conditions = " or ".join(f"tabUrl contains {applescript_quote(alias)}" for alias in aliases)
        delay_seconds = max(0.0, min(wait_seconds, 30.0))
        script = f'''
tell application "Safari"
  set touched to 0
  repeat with w in windows
    repeat with t in tabs of w
      try
        set tabUrl to URL of t
        if {conditions} then
          try
            do JavaScript "location.reload()" in t
            set touched to touched + 1
          end try
        end if
      end try
    end repeat
  end repeat
  if touched > 0 then delay {delay_seconds}
  return touched as string
end tell
'''
        proc = run_osascript(script)
    if proc.returncode != 0:
        raise RuntimeError(proc.stderr.strip() or "Safari tab reload failed")
    try:
        return int((proc.stdout or "0").strip())
    except ValueError:
        return 0


def safari_capture_jxa(max_text_chars: int) -> str:
    js = (
        "JSON.stringify({"
        "url: location.href,"
        "title: document.title,"
        "text: (document.body ? document.body.innerText : '').slice(0, %d)"
        "})"
    ) % max_text_chars
    aliases = json.dumps(provider_aliases(), ensure_ascii=False)
    extractor = json.dumps(js, ensure_ascii=False)
    return f"""
function run() {{
  const Safari = Application("Safari");
  const aliases = {aliases};
  const extractor = {extractor};
  const rows = [];

  function safeUrl(tab) {{
    try {{
      return String(tab.url() || "");
    }} catch (error) {{
      return "";
    }}
  }}

  function safeTitle(tab) {{
    try {{
      return String(tab.name() || "");
    }} catch (error) {{
      return "";
    }}
  }}

  function matchesProvider(url) {{
    return aliases.some(alias => url.includes(alias));
  }}

  Safari.windows().forEach(win => {{
    win.tabs().forEach(tab => {{
      const url = safeUrl(tab);
      if (!matchesProvider(url)) {{
        return;
      }}
      try {{
        rows.push(String(Safari.doJavaScript(extractor, {{in: tab}})));
      }} catch (error) {{
        rows.push(JSON.stringify({{url: url, title: safeTitle(tab), text: ""}}));
      }}
    }});
  }});

  return rows.join("\\n");
}}
"""


def safari_script(max_text_chars: int) -> str:
    js = (
        "JSON.stringify({"
        "url: location.href,"
        "title: document.title,"
        "text: (document.body ? document.body.innerText : '').slice(0, %d)"
        "})"
    ) % max_text_chars
    escaped_js = js.replace("\\", "\\\\").replace('"', '\\"')
    return f'''
tell application "Safari"
  set out to ""
  repeat with w in windows
    repeat with t in tabs of w
      try
        set out to out & (do JavaScript "{escaped_js}" in t) & linefeed
      on error errMsg
        set fallbackUrl to ""
        set fallbackTitle to ""
        try
          set fallbackUrl to URL of t
          set fallbackTitle to name of t
        end try
        set out to out & "{{\\"url\\":\\"" & fallbackUrl & "\\",\\"title\\":\\"" & fallbackTitle & "\\",\\"text\\":\\"\\"}}" & linefeed
      end try
    end repeat
  end repeat
  return out
end tell
'''


def read_safari_tabs(max_text_chars: int) -> list[dict[str, str]]:
    proc = run_osascript(safari_capture_jxa(max_text_chars), language="JavaScript")
    if proc.returncode != 0:
        proc = run_osascript(safari_script(max_text_chars))
    if proc.returncode != 0:
        raise RuntimeError(proc.stderr.strip() or "Safari AppleScript failed")
    tabs: list[dict[str, str]] = []
    for line in proc.stdout.splitlines():
        if not line.strip():
            continue
        try:
            data = json.loads(line)
        except json.JSONDecodeError:
            continue
        tabs.append(
            {
                "url": str(data.get("url") or ""),
                "title": str(data.get("title") or ""),
                "text": str(data.get("text") or ""),
            }
        )
    return tabs


def compact_error(value: BaseException | str) -> str:
    return sanitize(" ".join(str(value).split()))[:500]


def parse_observed_datetime(value: Any) -> datetime | None:
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


def is_fresh_observation(item: dict[str, Any], applied_at: str, max_age_seconds: int = 3600) -> bool:
    observed = parse_observed_datetime(item.get("observed_at"))
    applied = parse_observed_datetime(applied_at)
    if observed is None or applied is None:
        return False
    age = abs((applied - observed).total_seconds())
    return age <= max_age_seconds


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


def detail_account_line_count(detail: str) -> int | None:
    for pattern in (r"\bfresh_account_lines\s*=\s*(\d+)\b", r"\baccount_lines\s*=\s*(\d+)\b"):
        match = re.search(pattern, str(detail or ""))
        if match:
            return int(match.group(1))
    return None


def account_snapshot_quality_allows_ledger(item: dict[str, Any]) -> bool:
    """Return whether a browser account snapshot may update ledger facts.

    The Provider dashboard is strict about old/partial account rows; the ledger
    update path must be at least as strict or a fresh timestamp from an old
    Tampermonkey script can still train the cost ledger with stale/partial rows.
    """

    detail = str(item.get("detail") or "")
    if (
        "preserved previous account lines" in detail
        or "preserved previous non-empty snapshot" in detail
        or re.search(r"\bfresh_account_lines\s*=\s*0\b", detail)
        or "account_lines=0" in detail
    ):
        return False
    if "partial account snapshot" in detail:
        return False
    reported_account_count = detail_account_line_count(detail)
    if reported_account_count is not None and reported_account_count > len(detected_accounts(item)):
        return False
    if re.search(r"\bwait_state\s*=\s*timeout\b", detail):
        return False
    if "Chrome Tampermonkey read-only snapshot" in detail:
        version = detail_script_version(detail)
        if version is None or semver_lt(version, MIN_TAMPERMONKEY_ACCOUNT_SNAPSHOT_VERSION):
            return False
    return True


def rate_snapshot_quality_allows_ledger(item: dict[str, Any]) -> bool:
    """Return whether browser rate lines may update ledger/browser observations.

    Rate rows have the same "current page truth" requirement as account rows.
    Preserved rows are useful as diagnostics, but they must not train the ledger
    or trigger removed-group cleanup as if they were captured from this page.
    """

    detail = str(item.get("detail") or "")
    if (
        "preserved previous rate lines" in detail
        or "preserved previous non-empty snapshot" in detail
        or re.search(r"\bfresh_rate_lines\s*=\s*0\b", detail)
    ):
        return False
    if "partial account snapshot" in detail:
        return False
    if re.search(r"\bwait_state\s*=\s*timeout\b", detail):
        return False
    if "Chrome Tampermonkey read-only snapshot" in detail:
        version = detail_script_version(detail)
        if version is None or semver_lt(version, MIN_TAMPERMONKEY_ACCOUNT_SNAPSHOT_VERSION):
            return False
    return True


def browser_error_observations(browser: str, now: str, detail: str) -> list[dict[str, Any]]:
    safe_detail = compact_error(detail)
    return [
        {
            "provider": provider.name,
            "site": provider.site,
            "browser": browser,
            "status": "browser_read_failed",
            "detail": f"Safari read-only adapter could not read tabs: {safe_detail}",
            "observed_at": now,
            "page_url": "",
            "page_title": "",
            "detected_balance": "",
            "detected_rates": [],
            "sanitized_excerpt": "",
        }
        for provider in PROVIDERS
    ]


def host_matches(url: str, provider: Provider) -> bool:
    host = urlparse(url).netloc.lower().split("@")[-1].split(":")[0]
    if not host:
        return False
    return any(host == alias or host.endswith("." + alias) for alias in provider.aliases)


def sanitize(text: str) -> str:
    text = text.replace("\x00", "")
    text = re.sub(r"(sk-[A-Za-z0-9_-]{8})[A-Za-z0-9_-]{12,}", r"\1...redacted", text)
    text = re.sub(r"(?i)(bearer\s+)[A-Za-z0-9._-]{16,}", r"\1...redacted", text)
    text = re.sub(r"(?i)(api[_ -]?key\s*[:=]\s*)[A-Za-z0-9._-]{12,}", r"\1...redacted", text)
    text = re.sub(r"[A-Za-z0-9_-]{48,}", "...redacted-long-token...", text)
    lines = [line.strip() for line in text.splitlines()]
    return "\n".join(line for line in lines if line)


def repair_detected_account_name(site: str, account_name: str) -> str:
    name = sanitize(str(account_name or "")).strip()
    if site == "api.tokenskingdom.com" and name.startswith("ingdom "):
        return "k" + name
    return name


LOG_URL_PARTS = (
    "/log",
    "/logs",
    "/usage",
    "/record",
    "/records",
    "/task",
    "/tasks",
    "/bill",
    "/billing/log",
)

LOG_QUERY_MARKERS = (
    "trade_no=",
    "out_trade_no=",
    "request_id=",
    "requestid=",
)

LOG_TEXT_MARKERS = (
    "使用日志",
    "绘图日志",
    "任务日志",
    "请求日志",
    "日志详情",
    "计费过程",
    "请求路径",
    "请求并计费模型",
    "Request ID",
)

PRICING_URL_PARTS = (
    "/pricing",
    "/price",
    "/models",
)

PRICING_TEXT_MARKERS = (
    "可用令牌分组",
    "模型倍率",
    "模型名称",
    "计费模型",
    "按Token",
    "按次",
    "输入价格",
    "输出价格",
)

BALANCE_URL_PARTS = (
    "/console",
    "/dashboard",
    "/panel",
    "/user",
    "/profile",
    "/account",
    "/wallet",
    "/balance",
    "/billing",
    "/token",
    "/tokens",
    "/key",
    "/keys",
)

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


def is_usage_log_like(url: str, text: str) -> bool:
    parsed = urlparse(url)
    url_lower = (parsed.path + "?" + parsed.query).lower()
    if any(part in url_lower for part in LOG_URL_PARTS):
        return True
    if any(marker in url_lower for marker in LOG_QUERY_MARKERS):
        return True

    sanitized = sanitize(text)
    if sum(1 for marker in LOG_TEXT_MARKERS if marker in sanitized) >= 2:
        return True
    if "时间" in sanitized and "令牌" in sanitized and "模型" in sanitized and "用时" in sanitized:
        return True
    return False


def is_pricing_like(url: str, text: str) -> bool:
    parsed = urlparse(url)
    url_lower = (parsed.path + "?" + parsed.query).lower()
    if any(part in url_lower for part in PRICING_URL_PARTS):
        return True

    sanitized = sanitize(text)
    return sum(1 for marker in PRICING_TEXT_MARKERS if marker in sanitized) >= 2


def is_logged_in_dashboard_page(page_url: str, text: str) -> bool:
    parsed = urlparse(page_url or "")
    path = parsed.path.lower().rstrip("/")
    if is_usage_log_like(page_url, text) or is_pricing_like(page_url, text):
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


def tab_score(provider: Provider, tab: dict[str, str]) -> tuple[float, str, list[str]]:
    url = tab.get("url", "")
    title = tab.get("title", "")
    text = tab.get("text", "")
    lower_url = url.lower()
    lower_title = title.lower()

    balance = detect_balance(text, page_url=url)
    rates = detect_rate_lines(text)
    score = 0.0
    if text.strip():
        score += 1000
    if balance:
        score += 5000
    score += min(len(rates), 8) * 100
    score += min(len(text), 3000) / 100

    if any(part in lower_url for part in BALANCE_URL_PARTS):
        score += 800
    if any(word in title for word in ("控制台", "首页", "余额", "令牌", "钥匙", "账户", "账号")):
        score += 1200
    if any(word in lower_title for word in ("dashboard", "console", "balance", "wallet", "token", "key")):
        score += 1200

    if is_usage_log_like(url, text):
        score -= 5000
    if is_pricing_like(url, text) and not balance:
        score -= 1500

    if provider.name == "KBQ":
        parsed = urlparse(url)
        path = parsed.path.rstrip("/")
        if path in ("", "/console"):
            score += 5000
        if "/console/log" in lower_url or "trade_no=" in lower_url or "out_trade_no=" in lower_url:
            score -= 8000

    return score, balance, rates


def compact_excerpt(text: str, limit: int = 1200) -> str:
    sanitized = sanitize(text)
    interesting = []
    for line in sanitized.splitlines():
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
        interesting = sanitized.splitlines()[:10]
    return "\n".join(interesting)[:limit]


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


def is_token_quota_context(value: str) -> bool:
    text = sanitize(str(value or ""))
    return bool(
        "剩余额度/总额度" in text
        or ("剩余额度" in text and "总额度" in text)
        or ("剩余额度" in text and any(marker in text for marker in ("密钥", "分组", "已启用", "未启用", "已禁用")))
    )


def detect_balance(text: str, page_url: str = "") -> str:
    sanitized = sanitize(text)
    if is_token_quota_context(sanitized):
        return ""
    source_lines = [" ".join(line.split()) for line in sanitized.splitlines() if line.strip()]

    balance_pattern = (
        r"(当前余额|账户余额|账号余额|可用余额|剩余余额|余额|剩余额度|可用额度|"
        r"balance|remaining|quota)[^\n$¥￥0-9-]{0,40}"
        r"([$¥￥]?\s*-?\d+(?:\.\d+)?)"
    )
    balance_match = re.search(balance_pattern, "\n".join(source_lines), re.IGNORECASE)
    if balance_match and not (is_usage_log_like(page_url, sanitized) or is_pricing_like(page_url, sanitized)):
        label = balance_match.group(1)
        amount = " ".join(balance_match.group(2).split())
        return f"{label} {amount}"[:120]

    for idx, line in enumerate(source_lines):
        lower = line.lower()
        if not any(keyword in line or keyword in lower for keyword in BALANCE_KEYWORDS):
            continue
        window = source_lines[idx : min(len(source_lines), idx + 4)]
        for candidate in window:
            # Token/key pages often contain rows like
            # "无限额度 / 对接倍率 / 0.05x / 无限制".  That is not a balance.
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

    patterns = [
        r"(?:当前余额|账户余额|账号余额|可用余额|剩余余额|余额|剩余额度|可用额度)[^\n$¥￥0-9-]{0,24}([$¥￥]?\s*-?\d+(?:\.\d+)?)",
        r"(?i)(?:balance|remaining|quota)[^\n$¥￥0-9-]{0,24}([$¥￥]?\s*-?\d+(?:\.\d+)?)",
    ]
    for pattern in patterns:
        match = re.search(pattern, sanitized)
        if match:
            return " ".join(match.group(0).split())[:120]
    return isolated_dashboard_money_balance(sanitized, page_url)


def detect_rate_lines(text: str) -> list[str]:
    sanitized = sanitize(text)
    lines: list[str] = []
    source_lines = [" ".join(line.split()) for line in sanitized.splitlines()]
    for idx, line in enumerate(source_lines):
        if len(line) < 2:
            continue
        lower = line.lower()
        has_rate = bool(re.search(r"\b\d+(?:\.\d+)?x\b", lower))
        has_price = bool(re.search(r"[$¥￥]\s*\d+(?:\.\d+)?", line))
        has_keyword = any(word in line for word in ("倍率", "分组", "号池", "缓存", "价格", "余额", "额度")) or any(
            word in lower for word in ("group", "ratio", "price", "cache", "balance", "quota")
        )
        if (has_rate and has_keyword) or (has_price and has_keyword):
            if line not in lines:
                lines.append(line[:220])
        elif has_rate:
            context = " / ".join(
                part for part in source_lines[max(0, idx - 2) : min(len(source_lines), idx + 2)] if part
            )
            if context and context not in lines:
                lines.append(context[:220])
        if len(lines) >= 16:
            break
    return lines


def rate_value_matches(value: str) -> list[re.Match[str]]:
    return list(RATE_VALUE_RE.finditer(value))


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


def browser_rate_line_is_too_noisy(value: str) -> bool:
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


def parse_browser_rate_line(line: str) -> tuple[str, float] | None:
    if browser_rate_line_is_too_noisy(line):
        return None
    text = trim_source_before_select_group(line).strip()
    parts = [part.strip() for part in text.split("/") if part.strip()]
    for idx, part in enumerate(parts):
        matches = rate_value_matches(part)
        if not matches:
            continue
        before_first_rate = strip_rate_markers(part[: matches[0].start()])
        if before_first_rate:
            group = before_first_rate
        elif idx > 0:
            group = parts[idx - 1].strip()
        else:
            continue
        if not group or group.lower().startswith("sk-"):
            continue
        # Some NewAPI key pages concatenate old/current rates like
        # "0.13x0.08x". The last value matches the active account rate.
        return group[:160], rate_value_from_match(matches[-1])

    matches = rate_value_matches(text)
    if not matches:
        return None
    group = strip_rate_markers(text[: matches[0].start()])
    if not group or group.lower().startswith("sk-"):
        return None
    return group[:160], rate_value_from_match(matches[-1])


def normalize_match_text(value: str) -> str:
    value = value.lower()
    value = re.sub(r"sk-[a-z0-9._-]+", "", value)
    value = re.sub(r"\.\.\.redacted(?:-long-token)?\.\.\.", "", value)
    value = value.replace("（", "(").replace("）", ")")
    value = re.sub(r"[\s/_:：,，;；|｜·\-+()（）\[\]【】<>《》\"'“”‘’]", "", value)
    return value


def normalize_account_name(value: str) -> str:
    value = normalize_match_text(value)
    while value.startswith("修改"):
        value = value[2:]
    return value


def is_collector_panel_noise(value: str) -> bool:
    text = str(value or "")
    if not text:
        return False
    markers = (
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
    return any(marker in text for marker in markers)


def is_pseudo_balance(value: str) -> bool:
    text = str(value or "").strip()
    if not text:
        return False
    if is_collector_panel_noise(text):
        return True
    return text.startswith(("本页识别：", "脚本："))


def is_noise_account_name(value: str) -> bool:
    text = str(value or "").strip()
    if not text:
        return True
    if is_collector_panel_noise(text):
        return True
    markers = (
        "管理您的 API 密钥和访问令牌",
        "点击可复制此端点",
        "苹果生图工作台",
        "生图工作台",
        "统计Tokens",
        "toggle token visibility",
        "copy token key",
        "You need to enable JavaScript to run this app.",
    )
    if any(marker in text for marker in markers):
        return True
    if re.search(r"已启用|未启用|已禁用", text) and re.search(r"[¥￥$]\s*\d", text):
        return True
    if re.match(r"^\d{1,2}\s+\d{2}:\d{2}:\d{2}\b", text):
        return True
    if re.match(r"^\d{4}[/-]\d{2}[/-]\d{2}\b", text):
        return True
    return False


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
    has_key_page_markers = any(marker in source for marker in ("quota usage", "已启用", "无限额度", "有限额度", "Tag:"))
    has_group_rate = bool(re.search(r"\b\d+(?:\.\d+)?\s*x\b", source, re.IGNORECASE))
    return has_quota_pair and has_key_page_markers and has_group_rate


def clean_account_candidate(value: str) -> str:
    text = sanitize(str(value or "")).strip(" /:：|｜-")
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
    text = sanitize(str(value or "")).strip(" /:：|｜-")
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


def parse_group_and_rate(value: str) -> tuple[str, float | None]:
    text = trim_source_before_select_group(value)
    rate_matches = rate_value_matches(text)
    if not rate_matches:
        return "", None
    before_rate = strip_rate_markers(text[: rate_matches[0].start()])
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
    source = " ".join(sanitize(str(source_line or "")).split())
    match = MASKED_KEY_RE.search(source)
    if not match:
        return None
    before_key = source[: match.start()].strip(" /:：|｜-")
    after_key = source[match.end() :].strip()
    account_name = first_account_candidate(semantic_parts(before_key), before_key)
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
        "source_line": source[:500],
    }


def normalized_group_matches(browser_group: str, ledger_group: str) -> bool:
    group_norm = normalize_match_text(browser_group)
    row_group_norm = normalize_match_text(ledger_group)
    if not group_norm or not row_group_norm:
        return False
    if group_norm == row_group_norm:
        return True

    # Do not treat short labels like "gpt" as matching "gpt-pro".
    # A loose contains match is only safe for descriptive Chinese group names
    # such as "代理快速渠道" vs "代理快速渠道（不能生图）".
    shorter = min(len(group_norm), len(row_group_norm))
    if shorter < 6:
        return False
    return group_norm in row_group_norm or row_group_norm in group_norm


def browser_account_matches_row(account_name: str, row: sqlite3.Row) -> bool:
    return normalize_account_name(account_name) == normalize_account_name(row["fluter_account_name"])


def browser_rate_matches_row(site: str, browser_group: str, row: sqlite3.Row) -> bool:
    if normalized_group_matches(browser_group, row["upstream_group"]):
        return True

    account = str(row["fluter_account_name"]).lower()
    browser_group_lower = browser_group.lower()
    if site == "api.saki.lat":
        if "plus分组" in browser_group_lower and "codex plus" in account:
            return True
        if "pro分组" in browser_group_lower and "codex pro" in account:
            return True
        if "team分组" in browser_group_lower and "team" in account:
            return True
        if "cc max" in browser_group_lower and "ccmax" in account:
            return True
    if site == "pool.gptstore.club":
        if "代理快速" in browser_group and "代理快速" in row["upstream_group"]:
            return True
        if "代理快速" in browser_group and "代理快速" in account:
            return True
        if "pro" in browser_group_lower and "pro" in account:
            return True
        if "claudecode max" in browser_group_lower and "claude" in account and "max" in account:
            return True
    return False


def compact_rate(value: float | Decimal | None) -> str:
    if value is None:
        return "-"
    decimal = Decimal(str(value))
    decimal = decimal.quantize(Decimal("0.000000001")).normalize()
    return format(decimal.normalize(), "f")


def actual_cost_label(page_rate: float, recharge_factor: float) -> str:
    actual = Decimal(str(page_rate)) * Decimal(str(recharge_factor))
    return (
        f"实际成本倍率 {compact_rate(actual)}x"
        f"（浏览器刷新页面倍率 {compact_rate(page_rate)} × 充值系数 {compact_rate(recharge_factor)}）"
    )


def status_after_browser_rate(row: sqlite3.Row, new_page_rate: float) -> str:
    status = str(row["status"])
    if status not in ("已确认", "已覆盖", "偏保守", "需核对/倍率漂移", "上游分组已消失/待重映射"):
        return status
    site_multiplier = row["site_account_multiplier"]
    if site_multiplier is None:
        return "需核对" if status == "上游分组已消失/待重映射" else status
    actual = Decimal(str(new_page_rate)) * Decimal(str(row["recharge_factor"] or 1))
    current = Decimal(str(site_multiplier))
    if actual <= 0:
        return status
    coverage = current / actual
    if coverage < Decimal("0.999"):
        return "需核对/倍率漂移"
    if coverage > Decimal("1.05"):
        return "偏保守"
    return "已确认" if status == "需核对/倍率漂移" else status


def table_exists(conn: sqlite3.Connection, table_name: str) -> bool:
    return bool(
        conn.execute(
            "select 1 from sqlite_master where type = 'table' and name = ?",
            (table_name,),
        ).fetchone()
    )


def ensure_upstream_rate_columns(conn: sqlite3.Connection) -> None:
    columns = {
        row["name"] for row in conn.execute("pragma table_info(upstream_rate_records)")
    }
    if "actual_cost_label" not in columns:
        conn.execute(
            "alter table upstream_rate_records add column actual_cost_label text not null default ''"
        )


def ensure_browser_snapshot_columns(conn: sqlite3.Connection) -> None:
    columns = {
        row["name"] for row in conn.execute("pragma table_info(browser_adapter_snapshots)")
    }
    if "detected_accounts_json" not in columns:
        conn.execute(
            "alter table browser_adapter_snapshots add column detected_accounts_json text not null default '[]'"
        )


def clean_detected_account(item: Any) -> dict[str, Any] | None:
    if not isinstance(item, dict):
        return None
    account_name = sanitize(str(item.get("account_name") or item.get("name") or ""))[:160]
    upstream_group = sanitize(str(item.get("upstream_group") or item.get("group") or ""))[:160]
    source_line = sanitize(str(item.get("source_line") or ""))[:500]
    site = sanitize(str(item.get("site") or ""))[:160]
    if source_line and is_collector_panel_noise(source_line):
        return None
    page_rate = item.get("page_rate", item.get("rate"))
    parsed = parse_account_from_source_line(source_line)
    if parsed:
        account_name = parsed["account_name"]
        upstream_group = parsed["upstream_group"] or upstream_group
        page_rate = parsed["page_rate"] if parsed["page_rate"] is not None else page_rate
        source_line = parsed["source_line"]
    account_name = repair_detected_account_name(site, account_name)
    if looks_like_quota_only_account_line(account_name, source_line):
        return None
    if not account_name or is_noise_account_name(account_name):
        return None
    try:
        page_rate_value = float(page_rate) if page_rate not in (None, "") else None
    except (TypeError, ValueError):
        page_rate_value = None
    if page_rate_value is None and not upstream_group:
        return None
    if "Tag:" in source_line and normalize_match_text(account_name) == normalize_match_text(upstream_group):
        return None
    return {
        "account_name": account_name,
        "upstream_group": upstream_group,
        "page_rate": page_rate_value,
        "source_line": source_line or account_name,
    }


def detected_accounts(item: dict[str, Any]) -> list[dict[str, Any]]:
    if str(item.get("site") or "") == "xn--vduyey89e.com":
        return []
    raw = item.get("detected_accounts") or []
    if not isinstance(raw, list):
        return []
    rows: list[dict[str, Any]] = []
    seen: set[tuple[str, str, str]] = set()
    for candidate in raw:
        if isinstance(candidate, dict) and not candidate.get("site"):
            candidate = {**candidate, "site": item.get("site")}
        cleaned = clean_detected_account(candidate)
        if not cleaned:
            continue
        key = (cleaned["account_name"], cleaned["upstream_group"], str(cleaned["page_rate"]))
        if key in seen:
            continue
        seen.add(key)
        rows.append(cleaned)
        if len(rows) >= 40:
            break
    return rows


def apply_browser_rates_to_ledger(
    conn: sqlite3.Connection,
    observations: list[dict[str, Any]],
    applied_at: str,
) -> list[str]:
    if not table_exists(conn, "upstream_rate_records"):
        return []

    ensure_upstream_rate_columns(conn)
    messages: list[str] = []
    fresh_pairs = {
        (str(item.get("provider") or ""), str(item.get("site") or ""))
        for item in observations
        if is_fresh_observation(item, applied_at)
        and rate_snapshot_quality_allows_ledger(item)
        and isinstance(item.get("detected_rates"), list)
        and len(item.get("detected_rates") or []) > 0
    }
    for provider, site in fresh_pairs:
        if provider and site:
            conn.execute(
                """
                delete from browser_adapter_rate_observations
                where provider = ?
                  and site = ?
                """,
                (provider, site),
            )

    for item in observations:
        provider = str(item.get("provider") or "")
        site = str(item.get("site") or "")
        if site == "xn--vduyey89e.com":
            continue
        observed_at = str(item.get("observed_at") or applied_at)
        if not is_fresh_observation(item, applied_at):
            continue
        if not rate_snapshot_quality_allows_ledger(item):
            continue
        rate_lines = item.get("detected_rates") or []
        if not isinstance(rate_lines, list):
            continue
        for source_line in rate_lines:
            parsed = parse_browser_rate_line(str(source_line))
            if not parsed:
                continue
            upstream_group, page_rate = parsed
            candidates = conn.execute(
                """
                select id, fluter_account_name, upstream_group, kind, status, page_rate,
                       recharge_factor, site_account_multiplier, note
                from upstream_rate_records
                where site = ?
                  and instr(kind, '生图') = 0
                  and instr(kind, '特殊') = 0
                """,
                (site,),
            ).fetchall()
            matches = [
                row for row in candidates if browser_rate_matches_row(site, upstream_group, row)
            ]
            conn.execute(
                """
                insert into browser_adapter_rate_observations (
                  provider, site, upstream_group, page_rate, source_line,
                  matched_ledger_rows, observed_at
                ) values (?, ?, ?, ?, ?, ?, ?)
                on conflict(provider, site, upstream_group) do update set
                  page_rate = excluded.page_rate,
                  source_line = excluded.source_line,
                  matched_ledger_rows = excluded.matched_ledger_rows,
                  observed_at = excluded.observed_at
                """,
                (
                    provider,
                    site,
                    upstream_group,
                    page_rate,
                    str(source_line)[:500],
                    len(matches),
                    observed_at,
                ),
            )
            for row in matches:
                old_page_rate = row["page_rate"]
                if old_page_rate is not None and abs(float(old_page_rate) - page_rate) < 0.0000001:
                    recharge_factor = float(row["recharge_factor"] or 1)
                    conn.execute(
                        """
                        update upstream_rate_records
                        set actual_cost_label = ?,
                            status = ?,
                            updated_at = ?
                        where id = ?
                        """,
                        (
                            actual_cost_label(page_rate, recharge_factor),
                            status_after_browser_rate(row, page_rate),
                            applied_at,
                            row["id"],
                        ),
                    )
                    continue
                recharge_factor = float(row["recharge_factor"] or 1)
                note_line = (
                    f"[{applied_at}] 浏览器只读 adapter：页面刷新后将上游倍率 "
                    f"{compact_rate(old_page_rate)}x -> {compact_rate(page_rate)}x；"
                    f"来源分组 {upstream_group}。"
                )
                old_note = str(row["note"] or "")
                new_note = (note_line + "\n" + old_note)[:1800]
                conn.execute(
                    """
                    update upstream_rate_records
                    set page_rate = ?,
                        actual_cost_label = ?,
                        status = ?,
                        note = ?,
                        updated_at = ?
                    where id = ?
                    """,
                    (
                        page_rate,
                        actual_cost_label(page_rate, recharge_factor),
                        status_after_browser_rate(row, page_rate),
                        new_note,
                        applied_at,
                        row["id"],
                    ),
                )
                conn.execute(
                    """
                    insert into browser_adapter_ledger_updates (
                      provider, site, fluter_account_name, upstream_group,
                      old_page_rate, new_page_rate, source_line, observed_at, applied_at
                    ) values (?, ?, ?, ?, ?, ?, ?, ?, ?)
                    """,
                    (
                        provider,
                        site,
                        row["fluter_account_name"],
                        row["upstream_group"],
                        old_page_rate,
                        page_rate,
                        str(source_line)[:500],
                        observed_at,
                        applied_at,
                    ),
                )
                messages.append(
                    f"{provider} {row['fluter_account_name']}: "
                    f"{compact_rate(old_page_rate)}x -> {compact_rate(page_rate)}x"
                )
    return messages


def apply_browser_accounts_to_ledger(
    conn: sqlite3.Connection,
    observations: list[dict[str, Any]],
    applied_at: str,
) -> list[str]:
    if not table_exists(conn, "upstream_rate_records"):
        return []

    ensure_upstream_rate_columns(conn)
    messages: list[str] = []
    fresh_pairs = {
        (str(item.get("provider") or ""), str(item.get("site") or ""))
        for item in observations
        if is_fresh_observation(item, applied_at)
        and account_snapshot_quality_allows_ledger(item)
        and len(detected_accounts(item)) > 0
    }
    for provider, site in fresh_pairs:
        if provider and site:
            conn.execute(
                """
                delete from browser_adapter_account_observations
                where provider = ?
                  and site = ?
                """,
                (provider, site),
            )

    for item in observations:
        provider = str(item.get("provider") or "")
        site = str(item.get("site") or "")
        if site == "xn--vduyey89e.com":
            continue
        if not is_fresh_observation(item, applied_at):
            continue
        if not account_snapshot_quality_allows_ledger(item):
            continue
        observed_at = str(item.get("observed_at") or applied_at)
        for account in detected_accounts(item):
            page_rate = account["page_rate"]
            upstream_group = account["upstream_group"]
            candidates = conn.execute(
                """
                select id, fluter_account_name, upstream_group, kind, status, page_rate,
                       recharge_factor, site_account_multiplier, note
                from upstream_rate_records
                where site = ?
                  and instr(kind, '生图') = 0
                  and instr(kind, '特殊') = 0
                """,
                (site,),
            ).fetchall()
            matches = [
                row for row in candidates if browser_account_matches_row(account["account_name"], row)
            ]
            conn.execute(
                """
                insert into browser_adapter_account_observations (
                  provider, site, account_name, normalized_account_name,
                  upstream_group, page_rate, source_line, matched_ledger_rows, observed_at
                ) values (?, ?, ?, ?, ?, ?, ?, ?, ?)
                on conflict(provider, site, normalized_account_name) do update set
                  account_name = excluded.account_name,
                  upstream_group = excluded.upstream_group,
                  page_rate = excluded.page_rate,
                  source_line = excluded.source_line,
                  matched_ledger_rows = excluded.matched_ledger_rows,
                  observed_at = excluded.observed_at
                """,
                (
                    provider,
                    site,
                    account["account_name"],
                    normalize_account_name(account["account_name"]),
                    upstream_group,
                    page_rate,
                    account["source_line"][:500],
                    len(matches),
                    observed_at,
                ),
            )
            if page_rate is None:
                continue
            for row in matches:
                old_page_rate = row["page_rate"]
                recharge_factor = float(row["recharge_factor"] or 1)
                if old_page_rate is not None and abs(float(old_page_rate) - page_rate) < 0.0000001:
                    conn.execute(
                        """
                        update upstream_rate_records
                        set actual_cost_label = ?,
                            status = ?,
                            updated_at = ?
                        where id = ?
                        """,
                        (
                            actual_cost_label(page_rate, recharge_factor),
                            status_after_browser_rate(row, page_rate),
                            applied_at,
                            row["id"],
                        ),
                    )
                    continue
                note_line = (
                    f"[{applied_at}] 浏览器只读 adapter：按上游账号名 "
                    f"{account['account_name']} 精确命中，将页面倍率 "
                    f"{compact_rate(old_page_rate)}x -> {compact_rate(page_rate)}x。"
                )
                old_note = str(row["note"] or "")
                new_note = (note_line + "\n" + old_note)[:1800]
                conn.execute(
                    """
                    update upstream_rate_records
                    set page_rate = ?,
                        upstream_group = case when ? <> '' then ? else upstream_group end,
                        actual_cost_label = ?,
                        status = ?,
                        note = ?,
                        updated_at = ?
                    where id = ?
                    """,
                    (
                        page_rate,
                        upstream_group,
                        upstream_group,
                        actual_cost_label(page_rate, recharge_factor),
                        status_after_browser_rate(row, page_rate),
                        new_note,
                        applied_at,
                        row["id"],
                    ),
                )
                conn.execute(
                    """
                    insert into browser_adapter_ledger_updates (
                      provider, site, fluter_account_name, upstream_group,
                      old_page_rate, new_page_rate, source_line, observed_at, applied_at
                    ) values (?, ?, ?, ?, ?, ?, ?, ?, ?)
                    """,
                    (
                        provider,
                        site,
                        row["fluter_account_name"],
                        row["upstream_group"],
                        old_page_rate,
                        page_rate,
                        account["source_line"][:500],
                        observed_at,
                        applied_at,
                    ),
                )
                messages.append(
                    f"{provider} {row['fluter_account_name']}: "
                    f"{compact_rate(old_page_rate)}x -> {compact_rate(page_rate)}x"
                )
    return messages


def is_probable_balance_label(value: str) -> bool:
    text = sanitize(str(value or "")).strip()
    if not text:
        return False
    if is_pseudo_balance(text):
        return False
    if is_token_quota_context(text):
        return False
    if len(text) > 180:
        return False
    # Avoid treating group/model multipliers such as "0.002x" as balances.
    if re.search(r"\b\d+(?:\.\d+)?\s*x\b", text, re.IGNORECASE) and not has_money_amount(text):
        return False
    lower = text.lower()
    if has_money_amount(text):
        return True
    if any(keyword in text or keyword in lower for keyword in BALANCE_KEYWORDS):
        return bool(amount_from_line(text))
    return False


def apply_browser_balances_to_ledger(
    conn: sqlite3.Connection,
    observations: list[dict[str, Any]],
    applied_at: str,
) -> list[str]:
    if not table_exists(conn, "upstream_rate_records"):
        return []

    messages: list[str] = []
    for item in observations:
        provider = str(item.get("provider") or "")
        site = str(item.get("site") or "")
        balance = str(item.get("detected_balance") or "").strip()
        if is_pricing_like(str(item.get("page_url") or ""), str(item.get("sanitized_excerpt") or "")):
            continue
        if not site or not is_probable_balance_label(balance):
            continue
        before = conn.execute(
            """
            select count(*) as matched,
                   sum(case when coalesce(balance_label, '') <> ? then 1 else 0 end) as changed
            from upstream_rate_records
            where site = ?
            """,
            (balance, site),
        ).fetchone()
        if not before or int(before["matched"] or 0) == 0:
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
            (balance, applied_at, applied_at, site, balance),
        )
        changed = int(before["changed"] or 0)
        if changed > 0:
            messages.append(f"{provider}: balance -> {balance} ({changed} rows)")
    return messages


def observation_for_provider(provider: Provider, tabs: list[dict[str, str]], browser: str, now: str) -> dict[str, Any]:
    matching_tabs = [tab for tab in tabs if host_matches(tab.get("url", ""), provider)]
    if not matching_tabs:
        return {
            "provider": provider.name,
            "site": provider.site,
            "browser": browser,
            "status": "needs_browser_tab",
            "detail": f"no open logged-in tab for {provider.site}; {provider.note}",
            "observed_at": now,
            "page_url": "",
            "page_title": "",
            "detected_balance": "",
            "detected_rates": [],
            "sanitized_excerpt": "",
        }
    scored_tabs = [(tab_score(provider, tab), tab) for tab in matching_tabs]
    best_score, best = max(scored_tabs, key=lambda item: item[0][0])
    text = best.get("text", "")
    balance = best_score[1] or detect_balance(text, best.get("url", ""))
    rates = best_score[2] or detect_rate_lines(text)
    status = "browser_observed" if text else "browser_observed_empty"
    detail_parts = [f"tabs={len(matching_tabs)}"]
    if balance:
        detail_parts.append(f"balance={balance}")
    detail_parts.append(f"rate_lines={len(rates)}")
    if not text:
        detail_parts.append("page text empty; check browser permissions or page state")
    return {
        "provider": provider.name,
        "site": provider.site,
        "browser": browser,
        "status": status,
        "detail": "; ".join(detail_parts),
        "observed_at": now,
        "page_url": best.get("url", "")[:800],
        "page_title": sanitize(best.get("title", ""))[:240],
        "detected_balance": balance,
        "detected_rates": rates,
        "sanitized_excerpt": compact_excerpt(text),
    }


def observation_has_content(item: dict[str, Any]) -> bool:
    rates = item.get("detected_rates") or []
    accounts = detected_accounts(item)
    return bool(
        str(item.get("detected_balance") or "").strip()
        or accounts
        or (isinstance(rates, list) and len(rates) > 0)
        or str(item.get("sanitized_excerpt") or "").strip()
    )


def observation_rate_lines(item: dict[str, Any]) -> list[str]:
    rates = item.get("detected_rates") or []
    if not isinstance(rates, list):
        return []
    return [str(rate) for rate in rates if str(rate or "").strip()]


def merge_reloaded_observations(
    before_reload: list[dict[str, Any]],
    after_reload: list[dict[str, Any]],
) -> tuple[list[dict[str, Any]], int]:
    """Prefer post-reload data, but do not lose a good pre-reload read to SPA blanking."""

    before_by_pair = {
        (item.get("provider"), item.get("site")): item for item in before_reload
    }
    merged: list[dict[str, Any]] = []
    fallback_count = 0
    for item in after_reload:
        if observation_has_content(item):
            merged.append(item)
            continue
        fallback = before_by_pair.get((item.get("provider"), item.get("site")))
        if fallback and observation_has_content(fallback):
            preserved = dict(fallback)
            preserved["detail"] = (
                f"{fallback.get('detail', '')}; fallback_to_pre_reload_read "
                f"because post-reload page text was empty"
            ).strip("; ")
            preserved["observed_at"] = item.get("observed_at") or fallback.get("observed_at")
            merged.append(preserved)
            fallback_count += 1
        else:
            merged.append(item)
    return merged, fallback_count


def parse_snapshot_rates(row: sqlite3.Row | None) -> list[str]:
    if row is None:
        return []
    try:
        rates = json.loads(row["detected_rates_json"] or "[]")
    except json.JSONDecodeError:
        return []
    if not isinstance(rates, list):
        return []
    return [str(rate) for rate in rates if str(rate or "").strip()]


def parse_snapshot_accounts(row: sqlite3.Row | None) -> list[dict[str, Any]]:
    if row is None:
        return []
    try:
        accounts = json.loads(row["detected_accounts_json"] or "[]")
    except (IndexError, json.JSONDecodeError):
        return []
    if not isinstance(accounts, list):
        return []
    return [account for account in (clean_detected_account(item) for item in accounts) if account]


def existing_snapshot(conn: sqlite3.Connection, provider: str, site: str) -> sqlite3.Row | None:
    if not table_exists(conn, "browser_adapter_snapshots"):
        return None
    return conn.execute(
        """
        select browser, detected_balance, detected_accounts_json, detected_rates_json, sanitized_excerpt, observed_at
        from browser_adapter_snapshots
        where provider = ?
          and site = ?
        """,
        (provider, site),
    ).fetchone()


def snapshot_has_content(row: sqlite3.Row | None) -> bool:
    if not row:
        return False
    return bool(row["detected_balance"] or row["sanitized_excerpt"] or parse_snapshot_rates(row) or parse_snapshot_accounts(row))


def existing_snapshot_has_content(conn: sqlite3.Connection, provider: str, site: str) -> sqlite3.Row | None:
    row = existing_snapshot(conn, provider, site)
    return row if snapshot_has_content(row) else None


def browsers_differ(left: object, right: object) -> bool:
    left_text = str(left or "").strip().lower()
    right_text = str(right or "").strip().lower()
    return bool(left_text and right_text and left_text != right_text)


def should_keep_existing_account_snapshot(
    *,
    existing_for_pair: sqlite3.Row | None,
    incoming_browser: object,
    previous_accounts: list[dict[str, Any]],
    current_accounts: list[dict[str, Any]],
) -> bool:
    """Keep a good account snapshot from one browser when another browser sees no accounts.

    The status/snapshot tables are keyed only by provider+site.  Without this
    guard, a Safari fallback run that sees an empty page can overwrite the
    status for a fresh Chrome/Tampermonkey key-page snapshot and make the
    Provider table look stale or incomplete.
    """

    return bool(
        existing_for_pair
        and previous_accounts
        and not current_accounts
        and browsers_differ(existing_for_pair["browser"], incoming_browser)
    )


def write_observations(
    db_path: str,
    observations: list[dict[str, Any]],
    reload_note: str = "",
) -> list[str]:
    path = Path(db_path)
    path.parent.mkdir(parents=True, exist_ok=True)
    conn = sqlite3.connect(path)
    conn.row_factory = sqlite3.Row
    now = datetime.now(timezone.utc).isoformat(timespec="seconds")
    with conn:
        conn.executescript(SCHEMA)
        ensure_browser_snapshot_columns(conn)
        for item in observations:
            if item.get("detected_balance"):
                raw_balance = str(item.get("detected_balance") or "")
                if is_pseudo_balance(raw_balance):
                    item = dict(item)
                    item["detected_balance"] = ""
                else:
                    cleaned_balance = detect_balance(
                        raw_balance[:1000],
                        str(item.get("page_url") or ""),
                    )
                    if cleaned_balance and cleaned_balance != item.get("detected_balance"):
                        item = dict(item)
                        item["detected_balance"] = cleaned_balance
                    elif not cleaned_balance and not is_probable_balance_label(raw_balance):
                        item = dict(item)
                        item["detected_balance"] = ""
                        item["detail"] = (
                            f"{item.get('detail', '')}; ignored noisy balance field"
                        ).strip("; ")
            if item.get("site") == "xn--vduyey89e.com":
                item = dict(item)
                item["detected_accounts"] = []
                item["detected_rates"] = []
                item["detail"] = (
                    f"{item.get('detail', '')}; KBQ browser rows ignored for pricing truth"
                ).strip("; ")
            if item.get("detected_balance") and is_pricing_like(
                str(item.get("page_url") or ""),
                str(item.get("sanitized_excerpt") or ""),
            ):
                item = dict(item)
                item["detected_balance"] = ""
                item["detail"] = (
                    f"{item.get('detail', '')}; ignored pricing-page pseudo balance"
                ).strip("; ")
            provider = item["provider"]
            site = item["site"]
            existing_for_pair = existing_snapshot(conn, provider, site)
            existing_content = None
            if not observation_has_content(item):
                existing_content = existing_for_pair if snapshot_has_content(existing_for_pair) else None

            status = item["status"]
            detail = item["detail"]
            if existing_content:
                status = "browser_read_failed" if status == "browser_read_failed" else "browser_observed"
                detail = (
                    "latest read was empty; preserved previous non-empty snapshot "
                    f"from {existing_content['observed_at']}; {detail}"
                )
            current_rates = observation_rate_lines(item)
            current_accounts = detected_accounts(item)
            previous_rates = parse_snapshot_rates(existing_for_pair)
            previous_accounts = parse_snapshot_accounts(existing_for_pair)
            if should_keep_existing_account_snapshot(
                existing_for_pair=existing_for_pair,
                incoming_browser=item["browser"],
                previous_accounts=previous_accounts,
                current_accounts=current_accounts,
            ):
                continue
            snapshot_rates = current_rates
            snapshot_accounts = current_accounts
            if (
                not current_rates
                and previous_rates
                and observation_has_content(item)
                and not existing_content
            ):
                snapshot_rates = previous_rates
                detail = (
                    f"{detail}; fresh_rate_lines=0; preserved previous rate lines "
                    f"from {existing_for_pair['observed_at']} ({len(previous_rates)} lines)"
                ).strip("; ")
            if (
                not current_accounts
                and previous_accounts
                and observation_has_content(item)
                and not existing_content
            ):
                snapshot_accounts = previous_accounts
                detail = (
                    f"{detail}; fresh_account_lines=0; preserved previous account lines "
                    f"from {existing_for_pair['observed_at']} ({len(previous_accounts)} lines)"
                ).strip("; ")
            conn.execute(
                UPSERT_STATUS,
                (
                    provider,
                    site,
                    item["browser"],
                    status,
                    detail,
                    item["observed_at"],
                ),
            )
            if item.get("page_url") and not existing_content:
                conn.execute(
                    UPSERT_SNAPSHOT,
                    (
                        provider,
                        site,
                        item["browser"],
                        item["page_url"],
                        item["page_title"],
                        item["detected_balance"],
                        json.dumps(snapshot_accounts, ensure_ascii=False),
                        json.dumps(snapshot_rates, ensure_ascii=False),
                        item["sanitized_excerpt"],
                        item["observed_at"],
                    ),
                )
        conn.execute(
            "insert or replace into metadata(key, value) values (?, ?)",
            ("browser_readonly_adapters_refreshed_at", observations[0]["observed_at"] if observations else ""),
        )
        if reload_note:
            conn.execute(
                "insert or replace into metadata(key, value) values (?, ?)",
                ("browser_readonly_adapters_reload_note", reload_note),
            )
        ledger_updates = apply_browser_balances_to_ledger(conn, observations, now)
        ledger_updates.extend(apply_browser_accounts_to_ledger(conn, observations, now))
        ledger_updates.extend(apply_browser_rates_to_ledger(conn, observations, now))
        conn.execute(
            "insert or replace into metadata(key, value) values (?, ?)",
            ("browser_readonly_ledger_updates", str(len(ledger_updates))),
        )
        conn.execute(
            "insert or replace into metadata(key, value) values (?, ?)",
            (
                "browser_readonly_adapters_note",
                "Browser read-only snapshots store sanitized balance/account/rate summaries only; no cookies, passwords, full keys, or raw HTML. Matching non-image key-page rates may update the independent ledger page_rate; production accounts are not edited.",
            ),
        )
    conn.close()
    return ledger_updates


def load_import_payload(path_or_dash: str) -> list[dict[str, Any]]:
    raw = sys.stdin.read() if path_or_dash == "-" else Path(path_or_dash).read_text(encoding="utf-8")
    data = json.loads(raw)
    if not isinstance(data, list):
        raise ValueError("import JSON must be a list")
    return data


def send_remote(args: argparse.Namespace, observations: list[dict[str, Any]]) -> None:
    if args.remote_render_dashboard:
        backup_remote_dashboard(args)

    payload = json.dumps(observations, ensure_ascii=False)
    remote_command = (
        f"sudo python3 {shlex.quote(args.remote_script)} "
        f"--db {shlex.quote(args.remote_db)} --import-json -"
    )
    proc = subprocess.run(
        ["ssh", "-T", args.remote_ssh_host, remote_command],
        input=payload,
        text=True,
        capture_output=True,
        check=False,
    )
    if proc.returncode != 0:
        raise RuntimeError(proc.stderr.strip() or proc.stdout.strip() or "remote import failed")
    if proc.stdout.strip():
        print(proc.stdout.strip())

    if args.remote_render_dashboard:
        render_remote_dashboard(args)


def run_remote_command(args: argparse.Namespace, remote_command: str, input_text: str | None = None) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        ["ssh", "-T", args.remote_ssh_host, remote_command],
        input=input_text,
        text=True,
        capture_output=True,
        check=False,
    )


def backup_remote_dashboard(args: argparse.Namespace) -> None:
    remote_command = (
        "ts=$(date +%Y%m%d-%H%M%S); "
        f"sudo mkdir -p {shlex.quote(args.remote_backup_dir)}; "
        f"sudo cp {shlex.quote(args.remote_db)} "
        f"{shlex.quote(args.remote_backup_dir)}/upstream_rates-before-browser-import-$ts.sqlite 2>/dev/null || true; "
        f"sudo cp {shlex.quote(args.remote_render_output)} "
        f"{shlex.quote(args.remote_backup_dir)}/index-before-browser-import-$ts.html 2>/dev/null || true; "
        "echo browser_import_backup_ts=$ts"
    )
    proc = run_remote_command(args, remote_command)
    if proc.returncode != 0:
        raise RuntimeError(proc.stderr.strip() or proc.stdout.strip() or "remote dashboard backup failed")
    if proc.stdout.strip():
        print(proc.stdout.strip())


def render_remote_dashboard(args: argparse.Namespace) -> None:
    remote_command = (
        f"sudo python3 {shlex.quote(args.remote_render_script)} "
        f"--db {shlex.quote(args.remote_db)} --output {shlex.quote(args.remote_render_output)}"
    )
    proc = run_remote_command(args, remote_command)
    if proc.returncode != 0:
        raise RuntimeError(proc.stderr.strip() or proc.stdout.strip() or "remote dashboard render failed")
    if proc.stdout.strip():
        print(proc.stdout.strip())


def main() -> int:
    args = parse_args()
    reload_note = ""
    if args.import_json:
        observations = load_import_payload(args.import_json)
    else:
        before_reload: list[dict[str, Any]] = []
        if args.reload_open_tabs:
            before_now = datetime.now(timezone.utc).isoformat(timespec="seconds")
            try:
                before_tabs = read_safari_tabs(args.max_text_chars)
                before_reload = [
                    observation_for_provider(provider, before_tabs, args.browser, before_now)
                    for provider in PROVIDERS
                ]
            except RuntimeError as exc:
                reload_note = f"pre_reload_read_failed={compact_error(exc)}"
        if args.reload_open_tabs:
            try:
                touched = reload_safari_provider_tabs(args.reload_wait_seconds)
                reload_message = (
                    f"refreshed {touched} open upstream Safari tab(s), "
                    f"waited {args.reload_wait_seconds:g}s before reading"
                )
            except RuntimeError as exc:
                reload_message = f"reload_failed={compact_error(exc)}"
            reload_note = f"{reload_note}; {reload_message}" if reload_note else reload_message
        now = datetime.now(timezone.utc).isoformat(timespec="seconds")
        try:
            tabs = read_safari_tabs(args.max_text_chars)
            observations = [observation_for_provider(provider, tabs, args.browser, now) for provider in PROVIDERS]
        except RuntimeError as exc:
            if before_reload:
                observations = before_reload
                fallback_note = f"post_reload_read_failed_used_pre_reload={compact_error(exc)}"
            else:
                observations = browser_error_observations(args.browser, now, str(exc))
                fallback_note = f"read_failed={compact_error(exc)}"
            reload_note = f"{reload_note}; {fallback_note}" if reload_note else fallback_note
        if args.reload_open_tabs and before_reload:
            observations, fallback_count = merge_reloaded_observations(before_reload, observations)
            if fallback_count:
                reload_note += f"; fallback_to_pre_reload={fallback_count}"

    if args.remote_ssh_host:
        send_remote(args, observations)
    else:
        ledger_updates = write_observations(args.db, observations, reload_note)
        for message in ledger_updates:
            print(f"ledger_update: {message}")

    if reload_note:
        print(reload_note)
    for item in observations:
        print(f"{item['provider']}: {item['status']} {item['detail']}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
