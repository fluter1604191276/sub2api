#!/usr/bin/env python3
"""Admin-only health and metrics service for the upstream-rates dashboard."""

from __future__ import annotations

import argparse
import glob
import json
import os
import re
import shutil
import sqlite3
import subprocess
import time
import urllib.error
import urllib.request
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any


DEFAULT_DB = "/var/lib/fluterapi-upstream-rates/upstream_rates.sqlite"
DEFAULT_DASHBOARD = "/www/fluterapi-home/admin/upstream-rates/index.html"
MIN_TAMPERMONKEY_SNAPSHOT_VERSION = (0, 1, 15)
SLOW_METRICS_CACHE_SECONDS = 30
SERVICE_CHECKS = (
    {"id": "site", "label": "主站", "url": "https://fluterapi.top/", "protected": False},
    {"id": "api", "label": "API", "url": "https://api.fluterapi.top/health", "protected": False},
    {"id": "img_api", "label": "img API", "url": "https://img-api.fluterapi.top/health", "protected": False},
    {"id": "s2a", "label": "S2A", "url": "https://fluterapi.top/admin/s2a-manager", "protected": True},
    {"id": "upstream_rates", "label": "upstream-rates", "url": "https://fluterapi.top/admin/upstream-rates/", "protected": True},
)
EXPECTED_CONTAINERS = (
    ("sub2api", "sub2api", (r"^sub2api$", r"^sub2api-(?:backend|app)(?:-|$)")),
    ("postgres", "Postgres", (r"^sub2api-(?:postgres|db)(?:-|$)",)),
    ("redis", "Redis", (r"^sub2api-(?:redis|cache)(?:-|$)",)),
    ("s2a_web", "S2A web", (r"(?:^|[-_])s2a-manager-web(?:-|$)",)),
    ("s2a_worker", "S2A worker", (r"(?:^|[-_])s2a-manager-worker(?:-|$)",)),
    ("s2a_postgres", "S2A postgres", (r"(?:^|[-_])s2a-manager-postgres(?:-|$)",)),
)
FRESHNESS_FIELDS = (
    ("kbq_pricing", "KBQ pricing", "kbq_pricing_updated_at"),
    ("upstream_hub", "upstream-hub", "last_upstream_hub_imported_at"),
    ("site_account", "site account", "site_account_snapshot_refreshed_at"),
    ("kbq_true_cost", "KBQ true-cost", "kbq_true_cost_audit_updated_at"),
)
BACKUP_CHECKS = (
    ("sub2api", "sub2api", "sub2api-backup.timer", "/www/sub2api/backups/sub2api-backup-*.tar.gz"),
    ("s2a_manager", "S2A manager", "s2a-manager-backup.timer", "/www/s2a-manager/backups/s2a-manager-backup-*.tar.gz"),
)
BACKUP_POLICY = {
    "remote_retention": "远端完整归档仅保留北京时间当天或最新 1 份",
    "local_return": "回传 Mac mini 后校验 SHA-256，再清理远端旧归档",
    "local_database_retention": "加密 PostgreSQL dump 本地保留 7 个自然日和 4 个更早周备份",
}
_slow_metrics_cache: dict[str, Any] = {"key": None, "expires_at": 0.0, "value": None}


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Serve upstream-rates health and metrics")
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=8751)
    parser.add_argument("--db", default=DEFAULT_DB)
    parser.add_argument("--dashboard-path", default=DEFAULT_DASHBOARD)
    parser.add_argument("--node-label", default="fluterapi-prod")
    parser.add_argument("--serve-dashboard", action="store_true")
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


def detail_script_version(detail: str) -> tuple[int, ...] | None:
    match = re.search(r"\bscript\s*=\s*([0-9]+(?:\.[0-9]+){0,3})\b", str(detail or ""))
    return parse_semver(match.group(1)) if match else None


def browser_status_is_current_coverage(status: Any, detail: Any) -> bool:
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
        version = detail_script_version(text)
        if version is None or semver_lt(version, MIN_TAMPERMONKEY_SNAPSHOT_VERSION):
            return False
    return bool(
        re.search(r"\b(?:account_lines|rate_lines)\s*=\s*[1-9]\d*\b", text)
        or re.search(r"\bfresh_(?:account|rate)_lines\s*=\s*[1-9]\d*\b", text)
    )


def safe_float(value: str) -> float | None:
    try:
        return float(value)
    except (TypeError, ValueError):
        return None


def percent(used: float | int | None, total: float | int | None) -> float | None:
    if used is None or total in (None, 0):
        return None
    return round(float(used) / float(total) * 100, 2)


def read_text(path: str) -> str:
    try:
        return Path(path).read_text(encoding="utf-8", errors="replace")
    except OSError:
        return ""


def dashboard_preview_bytes(path: str, enabled: bool, dashboard_path: str) -> bytes | None:
    if not enabled or path not in ("/", "/index.html"):
        return None
    return Path(dashboard_path).read_bytes()


def parse_loadavg(text: str) -> list[float | None]:
    parts = text.split()
    values = [safe_float(item) for item in parts[:3]]
    while len(values) < 3:
        values.append(None)
    return values


def parse_cpu_stat(text: str) -> dict[str, int | None]:
    first = next((line for line in text.splitlines() if line.startswith("cpu ")), "")
    fields = first.split()[1:]
    try:
        values = [int(value) for value in fields]
    except ValueError:
        values = []
    if len(values) < 4:
        return {"total_ticks": None, "idle_ticks": None}
    total = sum(values[:8])
    idle = values[3] + (values[4] if len(values) > 4 else 0)
    return {"total_ticks": total, "idle_ticks": idle}


def parse_meminfo(text: str) -> dict[str, int]:
    values: dict[str, int] = {}
    for line in text.splitlines():
        if ":" not in line:
            continue
        key, raw = line.split(":", 1)
        match = raw.strip().split()
        if not match:
            continue
        try:
            values[key] = int(match[0]) * 1024
        except ValueError:
            continue
    return values


def collect_memory() -> dict[str, Any]:
    info = parse_meminfo(read_text("/proc/meminfo"))
    total = info.get("MemTotal")
    available = info.get("MemAvailable")
    used = total - available if total is not None and available is not None else None
    swap_total = info.get("SwapTotal")
    swap_free = info.get("SwapFree")
    swap_used = swap_total - swap_free if swap_total is not None and swap_free is not None else None
    return {
        "total_bytes": total,
        "available_bytes": available,
        "used_bytes": used,
        "used_percent": percent(used, total),
        "swap_total_bytes": swap_total,
        "swap_used_bytes": swap_used,
        "swap_used_percent": percent(swap_used, swap_total),
    }


def collect_disk(path: str = "/") -> dict[str, Any]:
    try:
        usage = shutil.disk_usage(path)
    except OSError:
        return {"path": path, "total_bytes": None, "used_bytes": None, "free_bytes": None, "used_percent": None}
    return {
        "path": path,
        "total_bytes": usage.total,
        "used_bytes": usage.used,
        "free_bytes": usage.free,
        "used_percent": percent(usage.used, usage.total),
    }


def parse_net_dev(text: str) -> dict[str, Any]:
    interfaces: list[dict[str, Any]] = []
    total_rx = 0
    total_tx = 0
    for line in text.splitlines():
        if ":" not in line:
            continue
        name, raw = line.split(":", 1)
        iface = name.strip()
        fields = raw.split()
        if iface == "lo" or len(fields) < 16:
            continue
        try:
            rx = int(fields[0])
            tx = int(fields[8])
        except ValueError:
            continue
        total_rx += rx
        total_tx += tx
        interfaces.append({"name": iface, "rx_bytes": rx, "tx_bytes": tx})
    physical = [
        item
        for item in interfaces
        if not re.match(r"^(?:docker|br-|veth|virbr|tun|tap)", str(item["name"]), re.IGNORECASE)
    ]
    primary = max(physical or interfaces, key=lambda item: int(item["rx_bytes"]) + int(item["tx_bytes"]), default=None)
    return {
        "rx_bytes": total_rx if interfaces else None,
        "tx_bytes": total_tx if interfaces else None,
        "primary_interface": primary["name"] if primary else None,
        "primary_rx_bytes": primary["rx_bytes"] if primary else None,
        "primary_tx_bytes": primary["tx_bytes"] if primary else None,
        "interfaces": interfaces[:12],
    }


def collect_uptime() -> float | None:
    first = read_text("/proc/uptime").split()
    return safe_float(first[0]) if first else None


def parse_docker_ps(text: str) -> list[dict[str, str]]:
    containers: list[dict[str, str]] = []
    for line in text.splitlines():
        parts = line.split("\t")
        if len(parts) < 2:
            continue
        name = parts[0].strip()
        status = parts[1].strip()
        if not name:
            continue
        lowered = status.lower()
        healthy = lowered.startswith("up") and "unhealthy" not in lowered
        containers.append(
            {
                "name": name[:80],
                "status": status[:160],
                "health": "ok" if healthy else "risk",
            }
        )
    return containers


def expected_container_inventory(items: list[dict[str, str]]) -> list[dict[str, Any]]:
    inventory: list[dict[str, Any]] = []
    for container_id, label, patterns in EXPECTED_CONTAINERS:
        match = next(
            (
                item
                for item in items
                if any(re.search(pattern, str(item.get("name", "")), re.IGNORECASE) for pattern in patterns)
            ),
            None,
        )
        inventory.append(
            {
                "id": container_id,
                "label": label,
                "name": match["name"] if match else "",
                "status": match["status"] if match else "missing",
                "health": match["health"] if match else "risk",
                "present": match is not None,
            }
        )
    return inventory


def unavailable_container_inventory(reason: str) -> list[dict[str, Any]]:
    return [
        {
            "id": container_id,
            "label": label,
            "name": "",
            "status": reason,
            "health": "risk",
            "present": None,
        }
        for container_id, label, _patterns in EXPECTED_CONTAINERS
    ]


def collect_containers() -> dict[str, Any]:
    try:
        proc = subprocess.run(
            ["docker", "ps", "-a", "--format", "{{.Names}}\t{{.Status}}"],
            text=True,
            capture_output=True,
            timeout=3,
            check=False,
        )
    except (OSError, subprocess.SubprocessError) as exc:
        error = type(exc).__name__
        return {"available": False, "error": error, "items": unavailable_container_inventory(error)}
    if proc.returncode != 0:
        error = (proc.stderr or "docker ps failed")[:180]
        return {"available": False, "error": error, "items": unavailable_container_inventory(error)}
    observed = parse_docker_ps(proc.stdout)[:30]
    return {
        "available": True,
        "error": "",
        "items": expected_container_inventory(observed),
        "observed_count": len(observed),
    }


def iso_datetime(value: Any) -> datetime | None:
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


def age_tone(age_seconds: float | None, warn_seconds: int, risk_seconds: int) -> str:
    if age_seconds is None or age_seconds > risk_seconds:
        return "risk"
    if age_seconds > warn_seconds:
        return "warn"
    return "ok"


def freshness_record(
    item_id: str,
    label: str,
    updated_at: Any,
    now: datetime,
) -> dict[str, Any]:
    parsed = iso_datetime(updated_at)
    age_seconds = max(0, int((now - parsed).total_seconds())) if parsed else None
    record = {
        "id": item_id,
        "label": label,
        "updated_at": parsed.isoformat(timespec="seconds") if parsed else "",
        "age_seconds": age_seconds,
        "tone": age_tone(age_seconds, 2 * 3600, 24 * 3600) if parsed else "warn",
    }
    if not parsed:
        record["condition"] = "unconfirmed"
        record["summary"] = "未确认更新时间"
    return record


def collect_data_freshness(
    db_path: str,
    dashboard_path: str,
    now: datetime | None = None,
) -> list[dict[str, Any]]:
    current = (now or datetime.now(timezone.utc)).astimezone(timezone.utc)
    metadata: dict[str, str] = {}
    hub_login_failures = 0
    public_pricing_ok = 0
    try:
        uri = Path(db_path).resolve().as_uri() + "?mode=ro"
        conn = sqlite3.connect(uri, uri=True, timeout=1)
        try:
            keys = [field[2] for field in FRESHNESS_FIELDS]
            placeholders = ",".join("?" for _ in keys)
            metadata = {
                str(key): str(value)
                for key, value in conn.execute(
                    f"select key, value from metadata where key in ({placeholders})",
                    keys,
                )
            }
            tables = {
                str(row[0])
                for row in conn.execute(
                    "select name from sqlite_master where type = 'table' and name in (?, ?)",
                    ("upstream_hub_channels", "upstream_adapter_status"),
                )
            }
            if "upstream_hub_channels" in tables:
                hub_login_failures = int(
                    conn.execute(
                        "select count(*) from upstream_hub_channels where trim(last_error) <> ''"
                    ).fetchone()[0]
                )
            if "upstream_adapter_status" in tables:
                public_pricing_ok = int(
                    conn.execute(
                        "select count(*) from upstream_adapter_status where status = 'ok'"
                    ).fetchone()[0]
                )
        finally:
            conn.close()
    except (OSError, sqlite3.Error, ValueError):
        metadata = {}
    items = [freshness_record(item_id, label, metadata.get(key), current) for item_id, label, key in FRESHNESS_FIELDS]
    if hub_login_failures and public_pricing_ok:
        hub_item = next(item for item in items if item["id"] == "upstream_hub")
        if hub_item["tone"] == "ok":
            hub_item["tone"] = "warn"
        hub_item["condition"] = "public_pricing_fallback"
        hub_item["summary"] = "公开 pricing 可用；采集登录态需恢复"
    try:
        modified = datetime.fromtimestamp(Path(dashboard_path).stat().st_mtime, tz=timezone.utc)
    except OSError:
        modified = None
    items.append(freshness_record("static_dashboard", "静态页面", modified, current))
    return items


def check_service(item: dict[str, Any]) -> dict[str, Any]:
    started = time.monotonic()
    status_code: int | None = None
    error = ""
    try:
        request = urllib.request.Request(str(item["url"]), headers={"User-Agent": "FluterServerHealth/1.0"})
        with urllib.request.urlopen(request, timeout=2.5) as response:
            status_code = int(response.status)
    except urllib.error.HTTPError as exc:
        status_code = int(exc.code)
        if not (item.get("protected") and status_code in (401, 403)):
            error = f"HTTP {status_code}"
        exc.close()
    except (OSError, urllib.error.URLError, TimeoutError) as exc:
        error = type(exc).__name__
    ok = status_code is not None and not error and (200 <= status_code < 400 or bool(item.get("protected")))
    return {
        "id": item["id"],
        "label": item["label"],
        "url": item["url"],
        "protected": bool(item.get("protected")),
        "status_code": status_code,
        "latency_ms": round((time.monotonic() - started) * 1000),
        "tone": "ok" if ok else "risk",
        "error": error,
    }


def collect_services() -> list[dict[str, Any]]:
    return [check_service(dict(item)) for item in SERVICE_CHECKS]


def parse_systemd_timer_show(text: str, unit: str) -> dict[str, Any]:
    values = {}
    for line in text.splitlines():
        if "=" in line:
            key, value = line.split("=", 1)
            values[key] = value
    active_state = values.get("ActiveState", "unknown")
    tone = "ok" if active_state == "active" else ("risk" if active_state in ("inactive", "failed") else "warn")
    return {
        "unit": unit,
        "active_state": active_state,
        "sub_state": values.get("SubState", "unknown"),
        "last_trigger": values.get("LastTriggerUSec", ""),
        "next_trigger": values.get("NextElapseUSecRealtime", ""),
        "tone": tone,
    }


def collect_timer(unit: str) -> dict[str, Any]:
    try:
        proc = subprocess.run(
            [
                "systemctl",
                "show",
                unit,
                "--property=LoadState,ActiveState,SubState,LastTriggerUSec,NextElapseUSecRealtime",
                "--no-pager",
            ],
            text=True,
            capture_output=True,
            timeout=3,
            check=False,
        )
    except (OSError, subprocess.SubprocessError) as exc:
        return {"unit": unit, "active_state": "unknown", "sub_state": "unknown", "last_trigger": "", "next_trigger": "", "tone": "warn", "error": type(exc).__name__}
    result = parse_systemd_timer_show(proc.stdout, unit)
    if proc.returncode != 0:
        result.update({"tone": "risk", "error": (proc.stderr or "systemctl show failed")[:180]})
    return result


def latest_backup_file(pattern: str, now: datetime | None = None) -> dict[str, Any]:
    current = (now or datetime.now(timezone.utc)).astimezone(timezone.utc)
    candidates: list[tuple[Path, os.stat_result]] = []
    for raw_path in glob.glob(pattern):
        path = Path(raw_path)
        try:
            candidates.append((path, path.stat()))
        except OSError:
            continue
    if not candidates:
        return {"path": "", "updated_at": "", "age_seconds": None, "size_bytes": None, "tone": "risk"}
    path, stat = max(candidates, key=lambda item: item[1].st_mtime)
    modified = datetime.fromtimestamp(stat.st_mtime, tz=timezone.utc)
    age_seconds = max(0, int((current - modified).total_seconds()))
    return {
        "path": str(path),
        "updated_at": modified.isoformat(timespec="seconds"),
        "age_seconds": age_seconds,
        "size_bytes": stat.st_size,
        "tone": age_tone(age_seconds, 20 * 3600, 36 * 3600),
    }


def collect_backups() -> list[dict[str, Any]]:
    backups = []
    for backup_id, label, unit, pattern in BACKUP_CHECKS:
        timer = collect_timer(unit)
        latest = latest_backup_file(pattern)
        tones = (timer["tone"], latest["tone"])
        tone = "risk" if "risk" in tones else ("warn" if "warn" in tones else "ok")
        backups.append(
            {
                "id": backup_id,
                "label": label,
                "tone": tone,
                "timer": timer,
                "latest": latest,
                "retention": BACKUP_POLICY["remote_retention"],
            }
        )
    return backups


def collect_slow_metrics(db_path: str, dashboard_path: str) -> dict[str, Any]:
    return {
        "services": collect_services(),
        "containers": collect_containers(),
        "freshness": collect_data_freshness(db_path, dashboard_path),
        "backups": collect_backups(),
    }


def cached_slow_metrics(db_path: str, dashboard_path: str) -> dict[str, Any]:
    key = (db_path, dashboard_path)
    now = time.monotonic()
    if _slow_metrics_cache["key"] == key and now < float(_slow_metrics_cache["expires_at"]) and _slow_metrics_cache["value"] is not None:
        return dict(_slow_metrics_cache["value"])
    value = collect_slow_metrics(db_path, dashboard_path)
    _slow_metrics_cache.update({"key": key, "expires_at": now + SLOW_METRICS_CACHE_SECONDS, "value": value})
    return dict(value)


def collect_metrics(
    db_path: str = DEFAULT_DB,
    dashboard_path: str = DEFAULT_DASHBOARD,
    node_label: str = "fluterapi-prod",
) -> dict[str, Any]:
    load = parse_loadavg(read_text("/proc/loadavg"))
    cpu_ticks = parse_cpu_stat(read_text("/proc/stat"))
    cpu_cores = os.cpu_count()
    root_disk = collect_disk("/")
    slow = cached_slow_metrics(db_path, dashboard_path)
    return {
        "status": "ok",
        "metrics_version": 2,
        "ts": datetime.now(timezone.utc).isoformat(timespec="seconds"),
        "node": {"label": str(node_label or "fluterapi-prod")[:120]},
        "cpu": {
            "cores": cpu_cores,
            "load1": load[0],
            "load5": load[1],
            "load15": load[2],
            "load1_per_core_percent": percent(load[0], cpu_cores) if load[0] is not None else None,
            **cpu_ticks,
        },
        "memory": collect_memory(),
        "disk": root_disk,
        "disks": {"root": root_disk, "www": collect_disk("/www")},
        "net": parse_net_dev(read_text("/proc/net/dev")),
        "uptime_sec": collect_uptime(),
        "backup_policy": dict(BACKUP_POLICY),
        "slow_checks_cache_seconds": SLOW_METRICS_CACHE_SECONDS,
        **slow,
    }


class LedgerAIHandler(BaseHTTPRequestHandler):
    server_version = "FluterUpstreamMetrics/1.0"

    def _send_json(self, status: int, payload: dict[str, Any]) -> None:
        raw = json.dumps(payload, ensure_ascii=False).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Cache-Control", "no-store")
        self.send_header("X-Robots-Tag", "noindex, nofollow")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def _send_html(self, status: int, raw: bytes) -> None:
        self.send_response(status)
        self.send_header("Content-Type", "text/html; charset=utf-8")
        self.send_header("Cache-Control", "no-store")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def log_message(self, format: str, *args: Any) -> None:
        return

    def do_GET(self) -> None:
        path = self.path.split("?", 1)[0]
        args = self.server.args  # type: ignore[attr-defined]
        try:
            preview = dashboard_preview_bytes(path, bool(args.serve_dashboard), args.dashboard_path)
        except OSError:
            self._send_json(404, {"error": "dashboard_not_found"})
            return
        if preview is not None:
            self._send_html(200, preview)
            return
        if path in ("/health", "/admin/upstream-rates/health"):
            self._send_json(200, {"status": "ok", "db_exists": Path(args.db).exists()})
            return
        if path in ("/metrics", "/admin/upstream-rates/metrics"):
            self._send_json(200, collect_metrics(args.db, args.dashboard_path, args.node_label))
            return
        self._send_json(404, {"error": "not_found"})

    def do_POST(self) -> None:
        self._send_json(404, {"error": "not_found"})


def main() -> int:
    args = parse_args()
    server = ThreadingHTTPServer((args.host, args.port), LedgerAIHandler)
    server.args = args  # type: ignore[attr-defined]
    print(f"Fluter upstream metrics listening on {args.host}:{args.port}")
    server.serve_forever()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
