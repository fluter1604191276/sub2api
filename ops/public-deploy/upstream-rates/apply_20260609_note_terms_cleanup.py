#!/usr/bin/env python3
"""Clean up ambiguous multiplier wording in the upstream ledger notes."""

from __future__ import annotations

import argparse
import sqlite3
from datetime import datetime, timezone
from pathlib import Path


DEFAULT_DB = "/var/lib/fluterapi-upstream-rates/upstream_rates.sqlite"


NOTE_UPDATES = [
    (
        "api.saki.lat",
        "meow claude ccmax仅客户端 1.1",
        "CC Max 限制客户端 无上限并发",
        "2026-06-08 刷新上游钥匙页：余额 $38.92，页面显示 1.1x；账号成本倍率记录 1.1x。",
    ),
    (
        "xn--vduyey89e.com",
        "KBQ codex pro 0.15",
        "[pro]gpt-5.4 / [pro]gpt-5.5",
        "KBQ 9折充值已计入台账：真实成本=页面倍率×0.9。生产账号 id=62，active 且 schedulable=true。KBQ 当前 pro 档约 0.15x 成本；账号成本倍率 0.15x 是内部成本记录，用户分组倍率 0.18x 是售价。",
    ),
    (
        "xn--vduyey89e.com",
        "KBQ claude kiro 低缓 0.15",
        "[kiro量低缓] Claude",
        "KBQ 9折充值已计入台账：真实成本=页面倍率×0.9。生产账号 id=66，active 且 schedulable=true。KBQ 当前低缓 Claude 档约 0.15x 成本；已覆盖 haiku/sonnet/opus 4.x 常用短名，用户分组倍率 0.20x。",
    ),
    (
        "xn--vduyey89e.com",
        "KBQ claude kiro/anti高缓 0.40",
        "[kiro量高缓] Claude + [Azure量]haiku",
        "KBQ 9折充值已计入台账：真实成本=页面倍率×0.9。生产账号 id=67，active 且 schedulable=true。KBQ 当前高缓/补充模型约 0.40x 成本；用户分组倍率 0.45x，空间较薄但仍覆盖。",
    ),
    (
        "xn--vduyey89e.com",
        "KBQ claude anti稳定 0.50",
        "[稳定AG量] Claude",
        "KBQ 9折充值已计入台账：真实成本=页面倍率×0.9。生产账号 id=68，active 且 schedulable=true。KBQ 当前稳定 AG 约 0.50x 成本；用户分组倍率 0.55x，主要作为稳定池。",
    ),
    (
        "xn--vduyey89e.com",
        "KBQ claude Azure 0.80",
        "[Azure量]claude-opus/sonnet 4-6",
        "KBQ 9折充值已计入台账：真实成本=页面倍率×0.9。生产账号 id=1111，active 且 schedulable=true。KBQ 当前 Azure Claude 4-6 档约 0.80x 成本；用户分组倍率 1.00x，作为备用兜底池。",
    ),
    (
        "api.tokenskingdom.com",
        "kingdom codex 0.08",
        "Openai 1 允许V1",
        "2026-06-08 刷新 Kingdom 钥匙页：余额 $587.05，Openai 1 允许V1 页面 0.9x；按充值折算约等于 0.069984x。账号成本倍率 0.1x 是内部成本记录，用户分组倍率 0.3x 是售价，有安全垫。",
    ),
]


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--db", default=DEFAULT_DB)
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    if not Path(args.db).exists():
        raise FileNotFoundError(args.db)

    now = datetime.now(timezone.utc).isoformat(timespec="seconds")
    conn = sqlite3.connect(args.db)
    with conn:
        for site, account_name, upstream_group, note in NOTE_UPDATES:
            result = conn.execute(
                """
                update upstream_rate_records
                set note = ?,
                    updated_at = ?
                where site = ?
                  and fluter_account_name = ?
                  and upstream_group = ?
                """,
                (note, now, site, account_name, upstream_group),
            )
            if result.rowcount != 1:
                raise RuntimeError(f"Expected one row for {site} / {account_name}, got {result.rowcount}")
        conn.execute(
            "insert or replace into metadata(key, value) values (?, ?)",
            ("note_terms_cleanup_updated_at", now),
        )
    conn.close()
    print(f"Cleaned ambiguous note terms at {now}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
