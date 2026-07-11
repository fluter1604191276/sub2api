#!/usr/bin/env python3
"""Admin-only AI assistant endpoint for the Fluter upstream ledger.

The browser never receives the API key. This local VPS-only service reads the
dedicated `台账ai` key from production PostgreSQL into process memory for a
single local gateway call, then returns only the model answer. The key is not
written to browser responses, logs, or the SQLite ledger.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import shlex
import shutil
import sqlite3
import subprocess
import urllib.error
import urllib.request
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any


DEFAULT_DB = "/var/lib/fluterapi-upstream-rates/upstream_rates.sqlite"
DEFAULT_COMPOSE_DIR = "/www/sub2api"
DEFAULT_API_BASE = "http://127.0.0.1:8080"
DEFAULT_MODEL = "gpt-5.4-mini"
DEFAULT_KEY_NAME = "台账ai"
MIN_TAMPERMONKEY_SNAPSHOT_VERSION = (0, 1, 15)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Serve the Fluter ledger AI endpoint")
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=8751)
    parser.add_argument("--db", default=DEFAULT_DB)
    parser.add_argument("--compose-dir", default=DEFAULT_COMPOSE_DIR)
    parser.add_argument("--api-base", default=DEFAULT_API_BASE)
    parser.add_argument("--model", default=DEFAULT_MODEL)
    parser.add_argument("--key-name", default=DEFAULT_KEY_NAME)
    parser.add_argument("--max-rows", type=int, default=120)
    return parser.parse_args()


def sql_quote(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


def number_or_none(value: Any) -> float | None:
    if value is None or value == "":
        return None
    try:
        return float(value)
    except (TypeError, ValueError):
        return None


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


def parse_loadavg(text: str) -> list[float | None]:
    parts = text.split()
    values = [safe_float(item) for item in parts[:3]]
    while len(values) < 3:
        values.append(None)
    return values


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
    return {
        "total_bytes": total,
        "available_bytes": available,
        "used_bytes": used,
        "used_percent": percent(used, total),
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
    primary = max(interfaces, key=lambda item: int(item["rx_bytes"]) + int(item["tx_bytes"]), default=None)
    return {
        "rx_bytes": total_rx if interfaces else None,
        "tx_bytes": total_tx if interfaces else None,
        "primary_interface": primary["name"] if primary else None,
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
        containers.append(
            {
                "name": name[:80],
                "status": status[:160],
                "health": "ok" if status.lower().startswith("up") else "warn",
            }
        )
    return containers


def collect_containers() -> dict[str, Any]:
    try:
        proc = subprocess.run(
            ["docker", "ps", "--format", "{{.Names}}\t{{.Status}}"],
            text=True,
            capture_output=True,
            timeout=3,
            check=False,
        )
    except (OSError, subprocess.SubprocessError) as exc:
        return {"available": False, "error": type(exc).__name__, "items": []}
    if proc.returncode != 0:
        return {"available": False, "error": (proc.stderr or "docker ps failed")[:180], "items": []}
    return {"available": True, "error": "", "items": parse_docker_ps(proc.stdout)[:30]}


def collect_metrics() -> dict[str, Any]:
    load = parse_loadavg(read_text("/proc/loadavg"))
    cpu_cores = os.cpu_count()
    return {
        "status": "ok",
        "ts": datetime.now(timezone.utc).isoformat(timespec="seconds"),
        "cpu": {
            "cores": cpu_cores,
            "load1": load[0],
            "load5": load[1],
            "load15": load[2],
            "load1_per_core_percent": percent(load[0], cpu_cores) if load[0] is not None else None,
        },
        "memory": collect_memory(),
        "disk": collect_disk("/"),
        "net": parse_net_dev(read_text("/proc/net/dev")),
        "uptime_sec": collect_uptime(),
        "containers": collect_containers(),
    }


def run_psql(compose_dir: str, sql: str) -> str:
    command = (
        f"cd {shlex.quote(compose_dir)} && "
        "docker compose exec -T postgres psql -U sub2api -d sub2api -At"
    )
    proc = subprocess.run(
        command,
        input=sql,
        shell=True,
        text=True,
        capture_output=True,
        check=False,
    )
    if proc.returncode != 0:
        raise RuntimeError("PostgreSQL query failed")
    return proc.stdout.strip()


def load_ledger_api_key(compose_dir: str, key_name: str) -> str:
    sql = f"""
copy (
  select coalesce((
    select row_to_json(t)::text
    from (
      select k.key
      from api_keys k
      where k.deleted_at is null
        and k.status = 'active'
        and k.name = {sql_quote(key_name)}
      order by k.updated_at desc nulls last, k.id desc
      limit 1
    ) t
  ), '')
) to stdout;
"""
    payload = run_psql(compose_dir, sql)
    if not payload:
        raise RuntimeError(f"Active API key named {key_name!r} was not found")
    data = json.loads(payload)
    key = str(data.get("key") or "").strip()
    if not key:
        raise RuntimeError(f"Active API key named {key_name!r} has no key value")
    return key


def table_exists(conn: sqlite3.Connection, table: str) -> bool:
    return (
        conn.execute(
            "select 1 from sqlite_master where type = 'table' and name = ?",
            (table,),
        ).fetchone()
        is not None
    )


def automation_scope_context() -> dict[str, str]:
    return {
        "hourly": (
            "当前每小时自动化覆盖 upstream-hub 脱敏快照导入、KBQ /api/pricing、钧澈 /api/pricing、"
            "生产账号/分组倍率只读快照、KBQ usage_logs 真实成本审计、静态台账渲染、生产账号倍率 dry-run；"
            "浏览器只读快照仅作为诊断兜底，未登录或未打开的上游页会显示 needs_browser_tab。"
        ),
        "drafts": "账号倍率同步脚本默认 dry-run。需要写入时使用 --create-drafts 创建 active + schedulable=false + 未分组的（修改）草案账号；旧 --apply 直接改老账号路径已禁用。",
    }


def load_ledger_context(db_path: str, question: str, max_rows: int) -> dict[str, Any]:
    path = Path(db_path)
    if not path.exists():
        raise RuntimeError(f"Ledger DB not found: {db_path}")

    conn = sqlite3.connect(path)
    conn.row_factory = sqlite3.Row
    metadata = {
        row["key"]: row["value"]
        for row in conn.execute("select key, value from metadata order by key")
    } if table_exists(conn, "metadata") else {}

    terms = [term for term in question.lower().replace("，", " ").replace("？", " ").split() if len(term) >= 2]
    rows: list[dict[str, Any]] = []
    for row in conn.execute(
        """
        select category, kind, site, fluter_account_name, upstream_group,
               page_rate, recharge_ratio_label, recharge_factor,
               actual_cost_label, balance_label, balance_updated_at,
               site_account_multiplier, site_group_multiplier, status, note,
               updated_at
        from upstream_rate_records
        order by
          case status
            when '需核对' then 0
            when '接近成本/需谨慎' then 1
            when '未确认' then 2
            when '需按单张流水核对' then 3
            else 4
          end,
          category, fluter_account_name, upstream_group
        """
    ):
        data = dict(row)
        haystack = " ".join(str(value or "") for value in data.values()).lower()
        risky = any(word in str(data.get("status") or "") for word in ("核对", "谨慎", "未确认", "漂移"))
        matched = not terms or any(term in haystack for term in terms)
        if matched or risky or len(rows) < 40:
            page_rate = number_or_none(data.get("page_rate"))
            recharge_factor = number_or_none(data.get("recharge_factor")) or 1
            actual_cost_multiplier = page_rate * recharge_factor if page_rate is not None else None
            rows.append(
                {
                    "category": data.get("category"),
                    "kind": data.get("kind"),
                    "site": data.get("site"),
                    "fluter_account_name": data.get("fluter_account_name"),
                    "upstream_group": data.get("upstream_group"),
                    "page_rate": page_rate,
                    "recharge_ratio": data.get("recharge_ratio_label"),
                    "recharge_factor": recharge_factor,
                    "actual_cost_multiplier": actual_cost_multiplier,
                    "actual_cost_label": data.get("actual_cost_label"),
                    "site_account_multiplier": number_or_none(data.get("site_account_multiplier")),
                    "site_group_multiplier": data.get("site_group_multiplier"),
                    "balance": data.get("balance_label"),
                    "balance_updated_at": data.get("balance_updated_at"),
                    "status": data.get("status"),
                    "note": data.get("note"),
                    "updated_at": data.get("updated_at"),
                }
            )
        if len(rows) >= max_rows:
            break

    kbq_audit = None
    kbq_buckets: list[dict[str, Any]] = []
    if table_exists(conn, "kbq_true_cost_audit_runs"):
        audit_run = conn.execute(
            "select * from kbq_true_cost_audit_runs order by id desc limit 1"
        ).fetchone()
        if audit_run:
            kbq_audit = {
                "observed_at": audit_run["observed_at"],
                "hours": audit_run["hours"],
                "request_count": audit_run["request_count"],
                "user_billed_cost": number_or_none(audit_run["user_billed_cost"]),
                "true_upstream_cost": number_or_none(audit_run["true_upstream_cost"]),
                "margin": number_or_none(audit_run["margin"]),
                "real_loss_bucket_count": audit_run["real_loss_bucket_count"],
                "display_drift_bucket_count": audit_run["display_drift_bucket_count"],
                "missing_price_bucket_count": audit_run["missing_price_bucket_count"],
                "note": audit_run["note"],
            }
            for row in conn.execute(
                """
                select status, display_status, account_name, channel_name, group_name,
                       model, upstream_model, request_count, user_billed_cost,
                       true_upstream_cost, margin, displayed_account_cost, note
                from kbq_true_cost_audit_buckets
                where run_id = ?
                order by
                  case status when 'REAL_LOSS' then 0 when 'NO_PRICE' then 1 else 2 end,
                  case display_status when 'DISPLAY_DRIFT' then 0 else 1 end,
                  margin asc
                limit 30
                """,
                (audit_run["id"],),
            ):
                kbq_buckets.append({key: row[key] for key in row.keys()})

    kbq_models: list[dict[str, Any]] = []
    if table_exists(conn, "kbq_token_model_records"):
        for row in conn.execute(
            """
            select category, model_name, base_model, cost_multiplier, endpoints,
                   input_usd_per_1m, output_usd_per_1m,
                   cache_read_usd_per_1m, cache_write_usd_per_1m,
                   official_label, updated_at
            from kbq_token_model_records
            order by category, cost_multiplier, model_name
            limit 80
            """
        ):
            kbq_models.append({key: row[key] for key in row.keys()})

    adapter_status: list[dict[str, Any]] = []
    browser_status_by_pair: dict[tuple[str, str], dict[str, Any]] = {}
    if table_exists(conn, "browser_adapter_status"):
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
              provider, site
            """
        ):
            item = {
                "provider": row["provider"],
                "site": row["site"],
                "adapter_kind": f"{row['browser']} browser_readonly",
                "status": row["status"],
                "detail": row["detail"],
                "observed_at": row["observed_at"],
            }
            browser_status_by_pair[(row["provider"], row["site"])] = item
            adapter_status.append(item)
    if table_exists(conn, "upstream_adapter_status"):
        for row in conn.execute(
            """
            select provider, site, adapter_kind, status, detail, observed_at
            from upstream_adapter_status
            order by
              case status when 'failed' then 0 when 'needs_adapter' then 1 else 2 end,
              provider, site
            """
        ):
            item = {key: row[key] for key in row.keys()}
            browser_item = browser_status_by_pair.get((row["provider"], row["site"]))
            if (
                item["status"] == "needs_adapter"
                and browser_item
                and browser_status_is_current_coverage(browser_item["status"], browser_item["detail"])
            ):
                item["status"] = "covered_by_browser"
                item["detail"] = (
                    f"{item['detail']}; browser read-only adapter has current coverage for this provider: "
                    f"{browser_item['detail']}"
                )
            adapter_status.append(item)

    site_accounts: list[dict[str, Any]] = []
    if table_exists(conn, "site_account_snapshots"):
        for row in conn.execute(
            """
            select account_id, account_name, platform, base_host, status,
                   schedulable, rate_multiplier, group_label,
                   production_updated_at, observed_at
            from site_account_snapshots
            order by
              case when schedulable = 1 then 0 else 1 end,
              account_name,
              account_id
            limit 160
            """
        ):
            data = {key: row[key] for key in row.keys()}
            data["rate_multiplier"] = number_or_none(data.get("rate_multiplier"))
            site_accounts.append(data)

    conn.close()
    return {
        "metadata": metadata,
        "ledger_rows": rows,
        "site_account_snapshots": site_accounts,
        "kbq_true_cost_audit": kbq_audit,
        "kbq_audit_buckets": kbq_buckets,
        "kbq_token_models": kbq_models,
        "upstream_adapter_status": adapter_status,
        "automation_scope": automation_scope_context(),
    }


def call_sub2api(api_base: str, api_key: str, model: str, question: str, context: dict[str, Any]) -> dict[str, Any]:
    system = (
        "你是 Fluter API 上游成本倍率台账助手。你只根据给定台账上下文回答，"
        "用中文、大白话、面向小白。不要编造余额、倍率、密钥或未给出的事实。"
        "字段口径必须分清：ledger_rows.site_account_multiplier 是“账号成本倍率（内部）”，"
        "用于我站自己记录账号成本，目标是尽量贴近 actual_cost_multiplier；"
        "ledger_rows.site_group_multiplier 是“用户分组倍率/售价”，才是卖给用户的口径。"
        "判断账号成本记录是否漂移，看 actual_cost_multiplier 与 site_account_multiplier；"
        "判断是否亏本或利润是否足，看 actual_cost_multiplier 与 site_group_multiplier，"
        "或者 KBQ 审计里的 true_upstream_cost 与 user_billed_cost。"
        "当用户问“倍率漂移巡检、智能体检查倍率、上游账号名和实际倍率是否漂移”时，"
        "要专门核对三类信号：1. 台账行 status 是否含“漂移/需核对/未确认”；"
        "2. actual_cost_multiplier 是否高于 site_account_multiplier；"
        "3. 上游账号命名里的倍率/成本标注是否可能和 page_rate、recharge_factor、site_account_multiplier 不一致。"
        "回答时先给“有没有新增需要处理的漂移”，再列账号、上游页面倍率、充值折算真实成本、账号成本倍率、账号名标注问题和建议动作。"
        "当用户问“倍率对比、我站账号倍率、上游倍率、是否亏本、是否漂移”时，固定按这个格式回答："
        "1. 总体结论；2. 核心对比表（账号｜上游/分组｜上游真实成本｜账号成本倍率（内部）｜用户分组倍率/售价｜判断）；3. 风险和下一步。"
        "不要把账号成本倍率当作用户售价，也不要把账号成本记录接近真实成本误说成利润薄；"
        "如果是生图按张成本，不要把文字 token 倍率硬套成图片成本，要说明按单张流水核对。"
        "如果问题涉及生产改价、创建草案账号、付费生图测试或外网暴露，要明确提示风险和先 dry-run/备份。"
        "不要要求用户把 API key、Cookie、密码发到聊天。"
    )
    user = {
        "question": question,
        "context": context,
    }
    body = json.dumps(
        {
            "model": model,
            "messages": [
                {"role": "system", "content": system},
                {"role": "user", "content": json.dumps(user, ensure_ascii=False)},
            ],
            "temperature": 0.2,
            "max_tokens": 900,
        },
        ensure_ascii=False,
    ).encode("utf-8")
    request = urllib.request.Request(
        api_base.rstrip("/") + "/v1/chat/completions",
        data=body,
        headers={
            "Authorization": f"Bearer {api_key}",
            "Content-Type": "application/json",
        },
        method="POST",
    )
    try:
        with urllib.request.urlopen(request, timeout=60) as response:
            payload = response.read().decode("utf-8")
            data = json.loads(payload)
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode("utf-8", errors="replace")[:600]
        raise RuntimeError(f"AI upstream returned HTTP {exc.code}: {detail}") from exc
    choice = (data.get("choices") or [{}])[0]
    message = choice.get("message") or {}
    answer = message.get("content") or data.get("output_text") or ""
    if not answer:
        raise RuntimeError("AI upstream returned an empty answer")
    return {
        "answer": answer,
        "model": data.get("model") or model,
        "usage": data.get("usage") or {},
    }


class LedgerAIHandler(BaseHTTPRequestHandler):
    server_version = "FluterLedgerAI/1.0"

    def _send_json(self, status: int, payload: dict[str, Any]) -> None:
        raw = json.dumps(payload, ensure_ascii=False).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Cache-Control", "no-store")
        self.send_header("X-Robots-Tag", "noindex, nofollow")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def log_message(self, format: str, *args: Any) -> None:
        return

    def do_GET(self) -> None:
        path = self.path.split("?", 1)[0]
        if path in ("/health", "/admin/upstream-rates/health"):
            args = self.server.args  # type: ignore[attr-defined]
            self._send_json(200, {"status": "ok", "db_exists": Path(args.db).exists()})
            return
        if path in ("/metrics", "/admin/upstream-rates/metrics"):
            self._send_json(200, collect_metrics())
            return
        self._send_json(404, {"error": "not_found"})

    def do_POST(self) -> None:
        if self.path not in ("/ai", "/admin/upstream-rates/ai"):
            self._send_json(404, {"error": "not_found"})
            return
        try:
            length = int(self.headers.get("Content-Length") or "0")
            if length <= 0 or length > 8192:
                raise ValueError("请求太大或为空")
            payload = json.loads(self.rfile.read(length).decode("utf-8"))
            question = str(payload.get("question") or "").strip()
            if not question:
                raise ValueError("问题不能为空")
            question = question[:600]

            args = self.server.args  # type: ignore[attr-defined]
            context = load_ledger_context(args.db, question, args.max_rows)
            api_key = load_ledger_api_key(args.compose_dir, args.key_name)
            result = call_sub2api(args.api_base, api_key, args.model, question, context)
            self._send_json(200, {"ok": True, **result})
        except Exception as exc:
            self._send_json(500, {"ok": False, "error": str(exc)})


def main() -> int:
    args = parse_args()
    server = ThreadingHTTPServer((args.host, args.port), LedgerAIHandler)
    server.args = args  # type: ignore[attr-defined]
    print(f"Fluter ledger AI listening on {args.host}:{args.port}")
    server.serve_forever()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
