"""Shared recharge-discount profiles for the upstream ledger.

The ledger records upstream page rates separately from the purchase discount
we actually receive when topping up each upstream site.  This module keeps that
discount layer independent from production accounts and from per-row seed data.
"""

from __future__ import annotations

import sqlite3
from dataclasses import dataclass
from datetime import datetime, timezone
from decimal import Decimal, InvalidOperation
from typing import Any


DEFAULT_DISCOUNT_PROFILES: tuple[dict[str, Any], ...] = (
    {
        "site": "api.mouubox.com",
        "provider_name": "超超 Mouubox",
        "discount_name": "1:1",
        "recharge_factor": "1",
        "paid_amount": None,
        "credited_amount": None,
        "currency": "USD",
        "effective_from": "2026-06-01",
        "effective_to": "",
        "status": "已确认",
        "confidence": "manual",
        "note": "普通 1:1 充值；真实成本倍率 = 页面倍率。",
    },
    {
        "site": "sub2api.mouubox.com",
        "provider_name": "超超 Mouubox 副站",
        "discount_name": "1:1",
        "recharge_factor": "1",
        "paid_amount": None,
        "credited_amount": None,
        "currency": "USD",
        "effective_from": "2026-06-01",
        "effective_to": "",
        "status": "已确认",
        "confidence": "manual",
        "note": "普通 1:1 充值；真实成本倍率 = 页面倍率。",
    },
    {
        "site": "api.saki.lat",
        "provider_name": "Meow",
        "discount_name": "1:1",
        "recharge_factor": "1",
        "paid_amount": None,
        "credited_amount": None,
        "currency": "USD",
        "effective_from": "2026-06-01",
        "effective_to": "",
        "status": "已确认",
        "confidence": "manual",
        "note": "普通 1:1 充值；真实成本倍率 = 页面倍率。",
    },
    {
        "site": "pool.gptstore.club",
        "provider_name": "Magic",
        "discount_name": "1:1",
        "recharge_factor": "1",
        "paid_amount": None,
        "credited_amount": None,
        "currency": "USD",
        "effective_from": "2026-06-01",
        "effective_to": "",
        "status": "已确认",
        "confidence": "manual",
        "note": "普通 1:1 充值；真实成本倍率 = 页面倍率。",
    },
    {
        "site": "sub2.congmingai.com",
        "provider_name": "聪明AI",
        "discount_name": "1:1",
        "recharge_factor": "1",
        "paid_amount": None,
        "credited_amount": None,
        "currency": "USD",
        "effective_from": "2026-06-14",
        "effective_to": "",
        "status": "已确认",
        "confidence": "manual",
        "note": "用户确认无充值折扣；最低售价仍覆盖成本。",
    },
    {
        "site": "vip.lcodex.cn",
        "provider_name": "钧澈",
        "discount_name": "充100到账108",
        "recharge_factor": str(Decimal("100") / Decimal("108")),
        "paid_amount": "100",
        "credited_amount": "108",
        "currency": "CNY",
        "effective_from": "2026-06-16",
        "effective_to": "",
        "status": "已确认",
        "confidence": "manual",
        "note": "充值反 8%，即充 100 实际到账 108；真实成本倍率 = 页面倍率 × 100/108。",
    },
    {
        "site": "xn--vduyey89e.com",
        "provider_name": "KBQ",
        "discount_name": "9折充值",
        "recharge_factor": "0.9",
        "paid_amount": "0.9",
        "credited_amount": "1",
        "currency": "CNY",
        "effective_from": "2026-06-15",
        "effective_to": "",
        "status": "已确认",
        "confidence": "manual",
        "note": "当前 KBQ 充值优惠按 9 折折算；KBQ token/按次成本都应乘该系数。",
    },
    {
        "site": "api.tokenskingdom.com",
        "provider_name": "Kingdom",
        "discount_name": "148.88 RMB = 2000 USD",
        "recharge_factor": str(Decimal("148.88") / Decimal("2000")),
        "paid_amount": "148.88",
        "credited_amount": "2000",
        "currency": "CNY/USD",
        "effective_from": "2026-06-16",
        "effective_to": "",
        "status": "已确认",
        "confidence": "manual",
        "note": "按新充值包折算，1 刀额度约等于 ¥0.07444 成本；文字倍率同样乘 0.07444。",
    },
    {
        "site": "image.tokenskingdom.com",
        "provider_name": "Kingdom Image",
        "discount_name": "148.88 RMB = 2000 USD",
        "recharge_factor": str(Decimal("148.88") / Decimal("2000")),
        "paid_amount": "148.88",
        "credited_amount": "2000",
        "currency": "CNY/USD",
        "effective_from": "2026-06-16",
        "effective_to": "",
        "status": "已确认",
        "confidence": "manual",
        "note": "Kingdom image 是 Kingdom 生图子域名，共用 148.88 RMB = 2000 USD 折算。",
    },
    {
        "site": "api.solov.cc",
        "provider_name": "神风",
        "discount_name": "1:1",
        "recharge_factor": "1",
        "paid_amount": None,
        "credited_amount": None,
        "currency": "USD",
        "effective_from": "2026-06-01",
        "effective_to": "",
        "status": "待移除",
        "confidence": "manual",
        "note": "用户计划移除；保留 1:1 兜底显示。",
    },
    {
        "site": "mdkj.lol",
        "provider_name": "乔燃",
        "discount_name": "1:1",
        "recharge_factor": "1",
        "paid_amount": None,
        "credited_amount": None,
        "currency": "USD",
        "effective_from": "2026-06-16",
        "effective_to": "",
        "status": "已确认",
        "confidence": "manual",
        "note": "暂按普通 1:1 充值；如后续有优惠，只改本折扣档案。",
    },
)


SCHEMA = """
create table if not exists upstream_discount_profiles (
  id integer primary key autoincrement,
  site text not null unique,
  provider_name text not null,
  discount_name text not null,
  recharge_factor real not null,
  paid_amount real,
  credited_amount real,
  currency text not null,
  effective_from text not null default '',
  effective_to text not null default '',
  status text not null,
  confidence text not null,
  note text not null,
  updated_at text not null
);

create table if not exists metadata (
  key text primary key,
  value text not null
);
"""


UPSERT = """
insert into upstream_discount_profiles (
  site, provider_name, discount_name, recharge_factor, paid_amount,
  credited_amount, currency, effective_from, effective_to, status,
  confidence, note, updated_at
) values (
  :site, :provider_name, :discount_name, :recharge_factor, :paid_amount,
  :credited_amount, :currency, :effective_from, :effective_to, :status,
  :confidence, :note, :updated_at
)
on conflict(site) do update set
  provider_name = excluded.provider_name,
  discount_name = excluded.discount_name,
  recharge_factor = excluded.recharge_factor,
  paid_amount = excluded.paid_amount,
  credited_amount = excluded.credited_amount,
  currency = excluded.currency,
  effective_from = excluded.effective_from,
  effective_to = excluded.effective_to,
  status = excluded.status,
  confidence = excluded.confidence,
  note = excluded.note,
  updated_at = excluded.updated_at;
"""


@dataclass(frozen=True)
class DiscountProfile:
    site: str
    provider_name: str
    discount_name: str
    recharge_factor: Decimal
    recharge_ratio_label: str
    status: str
    confidence: str
    note: str
    source: str


def decimal_or_none(value: Any) -> Decimal | None:
    if value is None or value == "":
        return None
    try:
        return Decimal(str(value))
    except (InvalidOperation, ValueError):
        return None


def compact_decimal(value: Decimal | float | int | None) -> str:
    if value is None:
        return "-"
    decimal = value if isinstance(value, Decimal) else Decimal(str(value))
    return format(decimal.quantize(Decimal("0.000000001")).normalize(), "f")


def recharge_ratio_label(profile: sqlite3.Row | dict[str, Any]) -> str:
    factor = decimal_or_none(profile["recharge_factor"]) or Decimal("1")
    discount = str(profile["discount_name"] or "").strip()
    if discount == "1:1":
        return "1:1"
    return f"{discount}（成本系数{compact_decimal(factor)}）"


def ensure_discount_profile_schema(conn: sqlite3.Connection) -> None:
    conn.executescript(SCHEMA)


def seed_default_discount_profiles(conn: sqlite3.Connection, *, overwrite: bool = False) -> int:
    ensure_discount_profile_schema(conn)
    now = datetime.now(timezone.utc).isoformat(timespec="seconds")
    changed = 0
    for profile in DEFAULT_DISCOUNT_PROFILES:
        if not overwrite:
            exists = conn.execute(
                "select 1 from upstream_discount_profiles where site = ?",
                (profile["site"],),
            ).fetchone()
            if exists:
                continue
        payload = dict(profile)
        payload["updated_at"] = now
        conn.execute(UPSERT, payload)
        changed += 1
    conn.execute(
        "insert or replace into metadata(key, value) values (?, ?)",
        ("discount_profiles_seeded_at", now),
    )
    conn.execute(
        "insert or replace into metadata(key, value) values (?, ?)",
        (
            "discount_profiles_note",
            "site-level recharge discounts are the preferred source for true cost multipliers; row-level recharge_factor remains as a compatibility snapshot",
        ),
    )
    return changed


def load_discount_profiles(conn: sqlite3.Connection) -> dict[str, DiscountProfile]:
    ensure_discount_profile_schema(conn)
    rows = conn.execute(
        """
        select site, provider_name, discount_name, recharge_factor, status,
               confidence, note
        from upstream_discount_profiles
        order by site
        """
    ).fetchall()
    profiles: dict[str, DiscountProfile] = {}
    for row in rows:
        factor = decimal_or_none(row["recharge_factor"]) or Decimal("1")
        profiles[row["site"]] = DiscountProfile(
            site=row["site"],
            provider_name=row["provider_name"],
            discount_name=row["discount_name"],
            recharge_factor=factor,
            recharge_ratio_label=recharge_ratio_label(row),
            status=row["status"],
            confidence=row["confidence"],
            note=row["note"],
            source="discount_profile",
        )
    return profiles


def effective_discount_for_site(
    profiles: dict[str, DiscountProfile],
    site: str,
    fallback_factor: Any = None,
    fallback_label: str = "",
) -> DiscountProfile:
    profile = profiles.get(str(site or ""))
    if profile:
        return profile
    factor = decimal_or_none(fallback_factor) or Decimal("1")
    label = fallback_label or ("1:1" if factor == 1 else f"行内旧系数 {compact_decimal(factor)}")
    return DiscountProfile(
        site=str(site or ""),
        provider_name="",
        discount_name=label,
        recharge_factor=factor,
        recharge_ratio_label=label,
        status="待核对" if fallback_factor is None else "行内兜底",
        confidence="fallback",
        note="未找到站点级折扣档案；使用 upstream_rate_records 行内 recharge_factor 兜底。",
        source="row_fallback",
    )
