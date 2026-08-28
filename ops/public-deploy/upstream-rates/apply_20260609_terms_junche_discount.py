#!/usr/bin/env python3
"""Apply the 2026-06-09 terminology and Junche discount ledger migration.

This only edits the independent upstream-rate SQLite ledger. It does not touch
sub2api production PostgreSQL accounts, groups, channels, or pricing.
"""

from __future__ import annotations

import argparse
import sqlite3
from datetime import datetime, timezone
from pathlib import Path


DEFAULT_DB = "/var/lib/fluterapi-upstream-rates/upstream_rates.sqlite"
JUNCHE_RECHARGE_FACTOR = 50 / 55


JUNCHE_ROWS = [
    (
        "钧澈 codex team 0.025",
        "TEAM号池",
        0.025,
        "2026-06-09 用户确认钧澈充值充50到账55，成本系数 0.909091；TEAM号池页面倍率 0.025x，折扣后真实成本约 0.022727x。生产库账号成本倍率仍记录 0.025x，属于偏保守。",
    ),
    (
        "钧澈 codex team狂欢 0.002",
        "team狂欢",
        0.002,
        "2026-06-09 用户确认钧澈充值充50到账55，成本系数 0.909091；team狂欢页面倍率 0.002x，折扣后真实成本约 0.001818x。生产库账号成本倍率仍记录 0.002x，属于偏保守。该账号放入 codex 生图+文字是为了提供低倍率文字调度，不代表要能生图；直接上游原生 1K/2K/4K 与桥接 1K 均提示该分组没有 gpt-image-2/gpt-5.3-codex 渠道，属于预期限制。",
    ),
    (
        "钧澈 codex 生图 0.06",
        "生图专用分组",
        0.06,
        "2026-06-09 用户确认钧澈充值充50到账55，成本系数 0.909091；生图专用分组页面倍率 0.06x，折扣后真实倍率约 0.054545x。直接上游 smoke：原生 1K/2K/4K 均 HTTP 200，但 2K/4K 下载后真实尺寸都是 1254x1254，不适合宣传真 2K/4K；桥接 1K 失败，提示该分组没有 gpt-5.3-codex channel。当前 schedulable=false、未分配，暂不作为公开生图来源。",
    ),
    (
        "钧澈 codex 优质plus 0.05",
        "优质-plus",
        0.05,
        "2026-06-09 用户确认钧澈充值充50到账55，成本系数 0.909091；优质-plus 页面倍率 0.05x，折扣后真实成本约 0.045455x。生产库账号成本倍率仍记录 0.05x，属于偏保守。",
    ),
    (
        "钧澈 codex pro/plus 0.07",
        "GPT-PLUS号池",
        0.045,
        "2026-06-09 用户确认钧澈充值充50到账55，成本系数 0.909091；GPT-PLUS号池页面倍率 0.045x，折扣后真实成本约 0.040909x。生产库账号成本倍率仍记录 0.07x，明显偏保守/含安全垫。",
    ),
    (
        "钧澈 codex 对接倍率 0.04",
        "对接倍率",
        0.02,
        "2026-06-09 用户确认钧澈充值充50到账55，成本系数 0.909091；对接倍率页面倍率 0.02x，折扣后真实成本约 0.018182x。生产库账号成本倍率仍记录 0.04x，明显偏保守，有安全垫。",
    ),
    (
        "钧澈 codex 福利plus 0.04",
        "专享福利",
        0.02,
        "2026-06-09 用户确认钧澈充值充50到账55，成本系数 0.909091；专享福利页面倍率 0.02x，折扣后真实成本约 0.018182x。生产库账号成本倍率仍记录 0.04x，明显偏保守，有安全垫。",
    ),
]


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--db", default=DEFAULT_DB)
    return parser.parse_args()


def actual_cost_label(page_rate: float) -> str:
    actual = page_rate * JUNCHE_RECHARGE_FACTOR
    return (
        f"实际成本倍率 {actual:.9f}x"
        f"（页面倍率 {page_rate:g} × 充值系数 {JUNCHE_RECHARGE_FACTOR:.9f}）"
    )


def apply_migration(db_path: str) -> None:
    if not Path(db_path).exists():
        raise FileNotFoundError(db_path)

    now = datetime.now(timezone.utc).isoformat(timespec="seconds")
    conn = sqlite3.connect(db_path)
    with conn:
        for name, group, page_rate, note in JUNCHE_ROWS:
            if "生图" in name:
                actual_label = "价格接口：gpt-image-2 ¥0.04/次（折扣后约 ¥0.0364）；codex-gpt-image-2 ¥0.06/次（折扣后约 ¥0.0545）；2K/4K 实测 1254x1254"
            else:
                actual_label = actual_cost_label(page_rate)
            result = conn.execute(
                """
                update upstream_rate_records
                set recharge_ratio_label = ?,
                    recharge_factor = ?,
                    page_rate = ?,
                    actual_cost_label = ?,
                    note = ?,
                    updated_at = ?
                where category = ?
                  and site = ?
                  and fluter_account_name = ?
                  and upstream_group = ?
                """,
                (
                    "充50到账55（成本系数0.909091）",
                    JUNCHE_RECHARGE_FACTOR,
                    page_rate,
                    actual_label,
                    note,
                    now,
                    "钧澈",
                    "vip.lcodex.cn",
                    name,
                    group,
                ),
            )
            if result.rowcount != 1:
                raise RuntimeError(f"Expected to update one Junche row, got {result.rowcount}: {name} / {group}")

        result = conn.execute(
            """
            update upstream_rate_records
            set status = ?,
                note = ?,
                actual_cost_label = ?,
                updated_at = ?
            where site = ?
              and fluter_account_name = ?
              and upstream_group = ?
            """,
            (
                "已覆盖/成本记录接近",
                "2026-06-08 刷新 Kingdom 钥匙页：余额 $587.05，CC Max 1号池页面 25x，今日 $6.2315、近30天 $97.6394。按充值折算，页面 25x 约等于 1.944x；账号成本倍率 2x 是内部成本记录，贴近真实成本且略偏保守；用户分组倍率 2.5x 才是售价，售价覆盖约 1.286x，不应把账号成本倍率当成售价来判断利润。",
                "实际成本倍率 1.944x（页面倍率 25 × 充值系数 0.07776）",
                now,
                "api.tokenskingdom.com",
                "kingdom claude 2",
                "CC Max 1号池",
            ),
        )
        if result.rowcount != 1:
            raise RuntimeError(f"Expected to update one Kingdom row, got {result.rowcount}")

        conn.execute(
            "insert or replace into metadata(key, value) values (?, ?)",
            ("terms_and_junche_discount_updated_at", now),
        )
        conn.execute(
            "insert or replace into metadata(key, value) values (?, ?)",
            (
                "terms_and_junche_discount_note",
                "Clarified account cost multiplier vs user group multiplier; Junche recharge factor set to 50/55 for ledger true-cost display; Kingdom CC Max note corrected.",
            ),
        )
    conn.close()
    print(f"Applied terms/Junche discount migration at {now}")


def main() -> int:
    args = parse_args()
    apply_migration(args.db)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
