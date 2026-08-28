#!/usr/bin/env python3
"""Seed or refresh the Fluter upstream rate ledger.

This script stores page-observed upstream multipliers in a small SQLite
database on the VPS. It does not store API keys, passwords, cookies, or tokens.
"""

from __future__ import annotations

import argparse
import sqlite3
from datetime import datetime, timezone
from pathlib import Path

from discount_profiles import seed_default_discount_profiles


DEFAULT_DB = "/var/lib/fluterapi-upstream-rates/upstream_rates.sqlite"
KINGDOM_RECHARGE_FACTOR = 148.88 / 2000
KBQ_RECHARGE_FACTOR = 0.9
JUNCHE_RECHARGE_FACTOR = 100 / 108
BALANCE_SNAPSHOT_AT = "2026-06-08 15:31 CST"


BALANCES = {
    "api.mouubox.com": "$17.02",
    "sub2api.mouubox.com": "$18.85",
    "api.saki.lat": "$38.17（最近观察；此前 $38.22）",
    "pool.gptstore.club": "$19.27",
    "vip.lcodex.cn": "令牌无限额度/余额未显示",
    "xn--vduyey89e.com": "¥35.76",
    "api.tokenskingdom.com": "$582.85",
    "image.tokenskingdom.com": "$582.85",
}


RECORDS = [
    {
        "category": "超超 Mouubox",
        "kind": "Codex",
        "site": "api.mouubox.com",
        "fluter_account_name": "超超(主站) codex 0.01",
        "upstream_group": "gpt",
        "page_rate": 0.01,
        "recharge_ratio_label": "1:1",
        "recharge_factor": 1,
        "site_account_multiplier": 0.01,
        "site_group_multiplier": "codex 超低价渠道@0.06",
        "status": "已确认",
        "note": "2026-06-09 刷新上游钥匙页：页面倍率 0.01x；生产库账号倍率 0.01x。",
    },
    {
        "category": "超超 Mouubox",
        "kind": "Codex",
        "site": "sub2api.mouubox.com",
        "fluter_account_name": "超超(副站) codex 0.01",
        "upstream_group": "gpt",
        "page_rate": 0.01,
        "recharge_ratio_label": "1:1",
        "recharge_factor": 1,
        "site_account_multiplier": 0.01,
        "site_group_multiplier": "codex 超低价渠道@0.06",
        "status": "已确认",
        "note": "2026-06-09 刷新上游钥匙页：页面倍率 0.01x；生产库账号倍率 0.01x。",
    },
    {
        "category": "Meow",
        "kind": "Claude",
        "site": "api.saki.lat",
        "fluter_account_name": "meow claude kiro 0.2",
        "upstream_group": "KiroCC 无限制客户端 无上限并发 高缓存",
        "page_rate": 0.22,
        "recharge_ratio_label": "1:1",
        "recharge_factor": 1,
        "site_account_multiplier": 0.2,
        "site_group_multiplier": "未分配/当前不调度",
        "status": "未分配/倍率漂移",
        "note": "2026-06-08 刷新上游钥匙页：余额 $38.92；可见活跃 Kiro 高缓存为 0.22x。生产账号仍记录 0.20x 且未分配/不调度，暂不影响用户；若重新启用需先修正成本记录。",
    },
    {
        "category": "Meow",
        "kind": "生图",
        "site": "api.saki.lat",
        "fluter_account_name": "meow codex 生图 0.05",
        "upstream_group": "Image-2 无限刷客户端 无上限并发",
        "page_rate": 0.05,
        "recharge_ratio_label": "1:1",
        "recharge_factor": 1,
        "site_account_multiplier": 0.5,
        "site_group_multiplier": "codex自用@0.001; codex 生图+文字@0.06；生图独立计费 ¥0.10/张",
        "actual_cost_label": "1K/2K/4K 均 $0.05/张（按 1:1 约 ¥0.05/张）",
        "status": "生图来源/已确认",
        "note": "2026-06-08 直接上游 smoke + Meow 使用记录核对：原生 /v1/images/generations 的 1K/2K/4K 均成功，真实尺寸分别为 1024/2048/4096，流水均扣 $0.05/张；响应只返 url、b64_json 为空属正常。直接上游 /responses 桥接 1K 返回 503，公开卖点仍以原生生图为准。生产库账号 rate_multiplier 当前为 0.5；公开生图独立计费 ¥0.10/张，有安全垫。",
    },
    {
        "category": "Meow",
        "kind": "生图",
        "site": "api.saki.lat",
        "fluter_account_name": "meow codex pro 生图 0.07",
        "upstream_group": "codex pro 生图",
        "page_rate": 0.07,
        "recharge_ratio_label": "1:1",
        "recharge_factor": 1,
        "site_account_multiplier": None,
        "site_group_multiplier": "本站未接入/未调度",
        "actual_cost_label": "上游新 key，真实 1K/2K/4K 成本待测",
        "status": "上游观察/本站未接入",
        "note": "2026-06-08 用户反馈 Meow 上游页面新增 key：codex pro 生图 0.07。仅作为台账观察记录，尚未接入我站账号管理、分组或渠道；启用前需做原生 1K/2K/4K、桥接/图生图能力和流水成本核对。",
    },
    {
        "category": "Meow",
        "kind": "Codex",
        "site": "api.saki.lat",
        "fluter_account_name": "meow codex plus 0.05",
        "upstream_group": "codex plus",
        "page_rate": 0.05,
        "recharge_ratio_label": "1:1",
        "recharge_factor": 1,
        "site_account_multiplier": None,
        "site_group_multiplier": "本站未接入/未调度",
        "actual_cost_label": "上游新 key，是否支持生图待测",
        "status": "上游观察/本站未接入",
        "note": "2026-06-08 用户反馈 Meow 上游页面新增 key：codex plus 0.05。仅作为台账观察记录，尚未接入我站账号管理、分组或渠道；如后续作为文字或生图来源，需要先确认模型范围、图片权限和真实扣费。",
    },
    {
        "category": "Meow",
        "kind": "Claude",
        "site": "api.saki.lat",
        "fluter_account_name": "meow claude ccmax仅客户端 1.1",
        "upstream_group": "CC Max 限制客户端 无上限并发",
        "page_rate": 1.1,
        "recharge_ratio_label": "1:1",
        "recharge_factor": 1,
        "site_account_multiplier": 1.1,
        "site_group_multiplier": "claude ccmax 最强号池 仅客户端@1.5",
        "status": "已确认",
        "note": "2026-06-08 刷新上游钥匙页：余额 $38.92，页面显示 1.1x；账号成本倍率记录 1.1x。",
    },
    {
        "category": "聪明AI",
        "kind": "Codex",
        "site": "sub2.congmingai.com",
        "fluter_account_name": "聪明ai codex 对接 仅文字0.05",
        "upstream_group": "中转站对接分组，开池模式10次重试（个人用户不要选）",
        "page_rate": 0.05,
        "recharge_ratio_label": "1:1（无充值折扣）",
        "recharge_factor": 1,
        "site_account_multiplier": 0.05,
        "site_group_multiplier": "codex 超低价渠道@0.06; 低价渠道@0.08; 高性价比渠道@0.10; 王炸低价pro@0.15; 兜底稳定pro@0.25",
        "status": "已确认",
        "note": "2026-06-14 用户确认聪明AI account id=7400 真值：成本 0.05x、无充值折扣。最低用户售价 codex 超低价渠道 0.06x 仍覆盖成本，其余 0.08/0.10/0.15/0.25 档均为正毛利；余额由浏览器只读快照动态贴上，不在 seed 里固化。",
    },
    {
        "category": "Meow",
        "kind": "Codex",
        "site": "api.saki.lat",
        "fluter_account_name": "meow codex pro 0.25",
        "upstream_group": "Pro分组 无限刷客户端 无上限并发",
        "page_rate": 0.25,
        "recharge_ratio_label": "1:1",
        "recharge_factor": 1,
        "site_account_multiplier": 0.25,
        "site_group_multiplier": "未分配/当前不调度",
        "status": "已确认",
        "note": "2026-06-08 刷新上游钥匙页：余额 $38.92，页面倍率 0.25x；当前未分配/不调度。",
    },
    {
        "category": "Magic",
        "kind": "Codex",
        "site": "pool.gptstore.club",
        "fluter_account_name": "magic codex 0.04",
        "upstream_group": "代理快速渠道（不能生图）",
        "page_rate": 0.04,
        "recharge_ratio_label": "1:1",
        "recharge_factor": 1,
        "site_account_multiplier": 0.04,
        "site_group_multiplier": "codex 超低价渠道@0.06",
        "status": "已确认",
        "note": "2026-06-08 刷新上游钥匙页：余额 $19.27，页面倍率 0.04x；该分组不能生图。",
    },
    {
        "category": "Magic",
        "kind": "Codex Pro",
        "site": "pool.gptstore.club",
        "fluter_account_name": "magic codex pro 0.16",
        "upstream_group": "Pro号池（生图请选择生图分组）",
        "page_rate": 0.16,
        "recharge_ratio_label": "1:1",
        "recharge_factor": 1,
        "site_account_multiplier": 0.16,
        "site_group_multiplier": "codex pro渠道@0.18",
        "status": "已确认",
        "note": "2026-06-08 刷新上游钥匙页：余额 $19.27，页面倍率 0.16x。",
    },
    {
        "category": "Magic",
        "kind": "Claude",
        "site": "pool.gptstore.club",
        "fluter_account_name": "magic claude ccmax 1",
        "upstream_group": "ClaudeCode Max20",
        "page_rate": 1,
        "recharge_ratio_label": "1:1",
        "recharge_factor": 1,
        "site_account_multiplier": 1,
        "site_group_multiplier": "claude ccmax 最强号池 不限客户端@1.2",
        "status": "已确认",
        "note": "2026-06-14 生产账号快照确认 account id=7395，active 且 schedulable=true，账号成本倍率 1x；Chrome/Tampermonkey 上游 key 页观察到 magic claude ccmax 1 / ClaudeCode Max20 / 1x。用户分组倍率 1.2x，作为 Magic Claude CC Max 当前接入行。",
    },
    {
        "category": "钧澈",
        "kind": "Codex",
        "site": "vip.lcodex.cn",
        "fluter_account_name": "钧澈 codex team 0.025",
        "upstream_group": "TEAM号池",
        "page_rate": 0.025,
        "recharge_ratio_label": "充100到账108（成本系数0.925926）",
        "recharge_factor": JUNCHE_RECHARGE_FACTOR,
        "site_account_multiplier": 0.025,
        "site_group_multiplier": "codex自用@0.001; codex 超低价渠道@0.06",
        "status": "已确认",
        "note": "2026-06-09 用户确认钧澈充值充100到账108，成本系数 0.925926；TEAM号池页面倍率 0.025x，折扣后真实成本约 0.023148x。生产库账号成本倍率仍记录 0.025x，属于偏保守。",
    },
    {
        "category": "钧澈",
        "kind": "Codex",
        "site": "vip.lcodex.cn",
        "fluter_account_name": "钧澈 codex team狂欢 0.002",
        "upstream_group": "team狂欢",
        "page_rate": 0.002,
        "recharge_ratio_label": "充100到账108（成本系数0.925926）",
        "recharge_factor": JUNCHE_RECHARGE_FACTOR,
        "site_account_multiplier": 0.002,
        "site_group_multiplier": "codex自用@0.001; codex 限时超低价@0.01; codex 生图+文字@0.06（文字调度，不承担生图）",
        "status": "文字调度/非生图来源",
        "note": "2026-06-09 用户确认钧澈充值充100到账108，成本系数 0.925926；team狂欢页面倍率 0.002x，折扣后真实成本约 0.001852x。生产库账号成本倍率仍记录 0.002x，属于偏保守。该账号放入 codex 生图+文字是为了提供低倍率文字调度，不代表要能生图；直接上游原生 1K/2K/4K 与桥接 1K 均提示该分组没有 gpt-image-2/gpt-5.3-codex 渠道，属于预期限制。",
    },
    {
        "category": "钧澈",
        "kind": "生图",
        "site": "vip.lcodex.cn",
        "fluter_account_name": "钧澈 codex 生图 0.06",
        "upstream_group": "生图专用分组",
        "page_rate": 0.06,
        "recharge_ratio_label": "充100到账108（成本系数0.925926）",
        "recharge_factor": JUNCHE_RECHARGE_FACTOR,
        "site_account_multiplier": 0.06,
        "site_group_multiplier": "未分配/当前不调度",
        "actual_cost_label": "价格接口：gpt-image-2 ¥0.04/次（折扣后约 ¥0.0370）；codex-gpt-image-2 ¥0.06/次（折扣后约 ¥0.0556）；2K/4K 实测 1254x1254",
        "status": "未调度/尺寸异常",
        "note": "2026-06-09 用户确认钧澈充值充100到账108，成本系数 0.925926；生图专用分组页面倍率 0.06x，折扣后真实倍率约 0.055556x。直接上游 smoke：原生 1K/2K/4K 均 HTTP 200，但 2K/4K 下载后真实尺寸都是 1254x1254，不适合宣传真 2K/4K；桥接 1K 失败，提示该分组没有 gpt-5.3-codex channel。当前 schedulable=false、未分配，暂不作为公开生图来源。",
    },
    {
        "category": "钧澈",
        "kind": "Codex",
        "site": "vip.lcodex.cn",
        "fluter_account_name": "钧澈 codex 优质plus 0.05",
        "upstream_group": "优质-plus",
        "page_rate": 0.05,
        "recharge_ratio_label": "充100到账108（成本系数0.925926）",
        "recharge_factor": JUNCHE_RECHARGE_FACTOR,
        "site_account_multiplier": 0.05,
        "site_group_multiplier": "codex 低价渠道@0.08",
        "status": "已确认",
        "note": "2026-06-09 用户确认钧澈充值充100到账108，成本系数 0.925926；优质-plus 页面倍率 0.05x，折扣后真实成本约 0.046296x。生产库账号成本倍率仍记录 0.05x，属于偏保守。",
    },
    {
        "category": "钧澈",
        "kind": "Codex Pro",
        "site": "vip.lcodex.cn",
        "fluter_account_name": "钧澈 codex pro/plus 0.07",
        "upstream_group": "GPT-PLUS号池",
        "page_rate": 0.045,
        "recharge_ratio_label": "充100到账108（成本系数0.925926）",
        "recharge_factor": JUNCHE_RECHARGE_FACTOR,
        "site_account_multiplier": 0.07,
        "site_group_multiplier": "codex 高性价比渠道@0.10",
        "status": "偏保守",
        "note": "2026-06-09 用户确认钧澈充值充100到账108，成本系数 0.925926；GPT-PLUS号池页面倍率 0.045x，折扣后真实成本约 0.041667x。生产库账号成本倍率仍记录 0.07x，明显偏保守/含安全垫。",
    },
    {
        "category": "钧澈",
        "kind": "Codex",
        "site": "vip.lcodex.cn",
        "fluter_account_name": "钧澈 codex 对接倍率 0.04",
        "upstream_group": "对接倍率",
        "page_rate": 0.02,
        "recharge_ratio_label": "充100到账108（成本系数0.925926）",
        "recharge_factor": JUNCHE_RECHARGE_FACTOR,
        "site_account_multiplier": 0.04,
        "site_group_multiplier": "codex 超低价渠道@0.06",
        "status": "偏保守",
        "note": "2026-06-09 用户确认钧澈充值充100到账108，成本系数 0.925926；对接倍率页面倍率 0.02x，折扣后真实成本约 0.018519x。生产库账号成本倍率仍记录 0.04x，明显偏保守，有安全垫。",
    },
    {
        "category": "钧澈",
        "kind": "Codex",
        "site": "vip.lcodex.cn",
        "fluter_account_name": "钧澈 codex 福利plus 0.04",
        "upstream_group": "专享福利",
        "page_rate": 0.02,
        "recharge_ratio_label": "充100到账108（成本系数0.925926）",
        "recharge_factor": JUNCHE_RECHARGE_FACTOR,
        "site_account_multiplier": 0.04,
        "site_group_multiplier": "codex 超低价渠道@0.06",
        "status": "偏保守",
        "note": "2026-06-09 用户确认钧澈充值充100到账108，成本系数 0.925926；专享福利页面倍率 0.02x，折扣后真实成本约 0.018519x。生产库账号成本倍率仍记录 0.04x，明显偏保守，有安全垫。",
    },
    {
        "category": "KBQ",
        "kind": "Codex",
        "site": "xn--vduyey89e.com",
        "fluter_account_name": "KBQ codex plus 仅文字0.12*0.9=0.108",
        "upstream_group": "[plus]gpt-5.4 / [plus]gpt-5.5",
        "page_rate": 0.12,
        "recharge_ratio_label": "9折充值（成本系数0.9）",
        "recharge_factor": KBQ_RECHARGE_FACTOR,
        "site_account_multiplier": 0.108,
        "site_group_multiplier": "未分配/当前不调度",
        "status": "未分配/未调度",
        "note": "生产账号 id=61，active 但 schedulable=false，未绑定用户分组。KBQ 当前 /api/pricing 显示 plus 档 gpt-5.4/gpt-5.5 成本约 0.12x；9折充值后实付约 0.108x，保留作成本参考，不参与当前销售路由。",
    },
    {
        "category": "KBQ",
        "kind": "Codex Pro",
        "site": "xn--vduyey89e.com",
        "fluter_account_name": "KBQ codex pro 仅文字0.2*0.9=0.18",
        "upstream_group": "[pro]gpt-5.4 / [pro]gpt-5.5",
        "page_rate": 0.2,
        "recharge_ratio_label": "9折充值（成本系数0.9）",
        "recharge_factor": KBQ_RECHARGE_FACTOR,
        "site_account_multiplier": 0.18,
        "site_group_multiplier": "codex pro渠道@0.18；渠道 codex pro渠道 active",
        "status": "已覆盖",
        "note": "生产账号 id=62，active 且 schedulable=true。KBQ 当前 pro 档成本约 0.2x；9折充值后实付约 0.18x。账号成本倍率 0.18x 是内部成本记录，用户分组 0.18x 是售价，利润需看真实扣费和缓存命中结构。",
    },
    {
        "category": "KBQ",
        "kind": "Claude",
        "site": "xn--vduyey89e.com",
        "fluter_account_name": "KBQ claude kiro 低缓 0.15",
        "upstream_group": "[kiro量低缓] Claude",
        "page_rate": 0.15,
        "recharge_ratio_label": "9折充值（成本系数0.9）",
        "recharge_factor": KBQ_RECHARGE_FACTOR,
        "site_account_multiplier": 0.15,
        "site_group_multiplier": "claude 超低价渠道@0.20；渠道 claude 超低价渠道 active",
        "status": "已覆盖",
        "note": "生产账号 id=66，active 且 schedulable=true。KBQ 当前低缓 Claude 档页面约 0.15x；9折充值后实付约 0.135x。已覆盖 haiku/sonnet/opus 4.x 常用短名，用户分组倍率 0.20x。",
    },
    {
        "category": "KBQ",
        "kind": "Claude",
        "site": "xn--vduyey89e.com",
        "fluter_account_name": "KBQ claude kiro/anti高缓 0.40",
        "upstream_group": "[kiro量高缓] Claude + [Azure量]haiku",
        "page_rate": 0.40,
        "recharge_ratio_label": "9折充值（成本系数0.9）",
        "recharge_factor": KBQ_RECHARGE_FACTOR,
        "site_account_multiplier": 0.40,
        "site_group_multiplier": "claude 高性价比渠道@0.45；渠道 claude 高性价比渠道 active",
        "status": "已覆盖",
        "note": "生产账号 id=67，active 且 schedulable=true。KBQ 当前高缓/补充模型页面约 0.40x；9折充值后实付约 0.36x。用户分组倍率 0.45x，安全垫比 1:1 充值时更足。",
    },
    {
        "category": "KBQ",
        "kind": "Claude",
        "site": "xn--vduyey89e.com",
        "fluter_account_name": "KBQ claude anti稳定 0.50",
        "upstream_group": "[稳定AG量] Claude + [Azure量]haiku",
        "page_rate": 0.50,
        "recharge_ratio_label": "9折充值（成本系数0.9）",
        "recharge_factor": KBQ_RECHARGE_FACTOR,
        "site_account_multiplier": 0.50,
        "site_group_multiplier": "claude 超稳定渠道@0.55；渠道 claude 超稳定渠道 active",
        "status": "已覆盖",
        "note": "生产账号 id=68，active 且 schedulable=true。KBQ 当前稳定 AG 页面约 0.50x；9折充值后实付约 0.45x。若映射 [Azure量]claude-haiku-4-5，则其当前实付成本约 0.36x，属于低成本模型补齐卖稳定档。用户分组倍率 0.55x，主要作为稳定池。",
    },
    {
        "category": "KBQ",
        "kind": "生图",
        "site": "xn--vduyey89e.com",
        "fluter_account_name": "KBQ codex 仅生图 0.008/次",
        "upstream_group": "default",
        "page_rate": None,
        "recharge_ratio_label": "9折充值（成本系数0.9）",
        "recharge_factor": KBQ_RECHARGE_FACTOR,
        "site_account_multiplier": 1,
        "site_group_multiplier": "未分配/当前不调度",
        "actual_cost_label": "gpt-image-2 标价 ¥0.08/次，9折后约 ¥0.072/次；4K 标价 ¥0.10/次，9折后约 ¥0.09/次；2K/4K 实测 1254x1254",
        "status": "停用/尺寸异常",
        "note": "2026-06-08 查 /api/pricing：gpt-image-2 为 ¥0.08/次，gpt-image-2-4k 为 ¥0.10/次。直接上游 smoke：原生 1K/2K/4K 均 HTTP 200，但 2K/4K 下载后真实尺寸都是 1254x1254；桥接 1K 失败，提示没有 gpt-5.3-codex channel。当前 schedulable=false，若按 ¥0.10/张开放，4K 基本无利润，且不适合宣传真 2K/4K。",
    },
    {
        "category": "KBQ",
        "kind": "Claude",
        "site": "xn--vduyey89e.com",
        "fluter_account_name": "KBQ claude Azure 0.80",
        "upstream_group": "[Azure量]claude-opus/sonnet 4-6",
        "page_rate": 0.80,
        "recharge_ratio_label": "9折充值（成本系数0.9）",
        "recharge_factor": KBQ_RECHARGE_FACTOR,
        "site_account_multiplier": 0.80,
        "site_group_multiplier": "claude 备用兜底@1.00；渠道 claude Azure 备用渠道 active",
        "status": "已覆盖",
        "note": "生产账号 id=1111，active 且 schedulable=true。KBQ 当前 Azure Claude 4-6 档页面约 0.80x；9折充值后实付约 0.72x。用户分组倍率 1.00x，作为备用兜底池。",
    },
    {
        "category": "Kingdom",
        "kind": "特殊",
        "site": "api.tokenskingdom.com",
        "fluter_account_name": "kingdom codex bugteam 0",
        "upstream_group": "无分组",
        "page_rate": None,
        "recharge_ratio_label": "148.88 RMB = 2000 USD",
        "recharge_factor": KINGDOM_RECHARGE_FACTOR,
        "site_account_multiplier": 0,
        "site_group_multiplier": "未分配/特殊账号",
        "status": "特殊",
        "note": "页面显示无分组；不参与倍率判断。",
    },
    {
        "category": "Kingdom",
        "kind": "生图",
        "site": "image.tokenskingdom.com",
        "fluter_account_name": "kingdom codex 优质生图 1",
        "upstream_group": "Image-2 高质",
        "page_rate": 0.65,
        "recharge_ratio_label": "148.88 RMB = 2000 USD",
        "recharge_factor": KINGDOM_RECHARGE_FACTOR,
        "site_account_multiplier": 1,
        "site_group_multiplier": "codex自用@0.001; codex 生图+文字@0.06；生图独立计费 ¥0.10/张；当前为 Meow 后备",
        "actual_cost_label": "$0.65/张 = ¥0.048386/张；1K/2K/4K 尺寸正常",
        "status": "已按流水确认",
        "note": "2026-06-09 用户确认 Kingdom 优质生图上游已调整为 $0.65/张；按 148.88 元=2000 刀折算，1 刀=¥0.07444，单张成本约 ¥0.048386。此前 smoke 记录：1K/2K/4K 真实尺寸分别为 1024/2048/4096；2K 耗时约 151 秒，偏慢。桥接 1K 返回 502，不作为桥接来源。",
    },
    {
        "category": "Kingdom",
        "kind": "生图",
        "site": "image.tokenskingdom.com",
        "fluter_account_name": "kingdom codex 生图 1",
        "upstream_group": "Image-2",
        "page_rate": 0.65,
        "recharge_ratio_label": "148.88 RMB = 2000 USD",
        "recharge_factor": KINGDOM_RECHARGE_FACTOR,
        "site_account_multiplier": 1,
        "site_group_multiplier": "codex自用@0.001; codex 生图+文字@0.06；生图独立计费 ¥0.10/张；当前为 Meow 后备",
        "actual_cost_label": "$0.65/张 = ¥0.048386/张；2K 实测 1254x1254，4K 正常",
        "status": "已按流水确认/2K缩水",
        "note": "2026-06-08 直接上游 smoke + Kingdom 流水核对：账号余额最近观察 $582.85；上游 148.88 元=2000 刀，1 刀=¥0.07444；普通生图 gpt-image-2 的 1K/2K/4K 均扣 $0.65/张，折合 ¥0.048386/张。真实尺寸：1K 为 1024x1024，2K 请求只返回 1254x1254，4K 为 4096x4096；桥接 1K 返回 502，不作为桥接来源。",
    },
    {
        "category": "神风",
        "kind": "生图",
        "site": "api.solov.cc",
        "fluter_account_name": "神风 codex 生图 1",
        "upstream_group": "未记录",
        "page_rate": 1,
        "recharge_ratio_label": "1:1",
        "recharge_factor": 1,
        "site_account_multiplier": 1,
        "site_group_multiplier": "未分配/当前不调度",
        "status": "未调度/无生图权限",
        "note": "生产库有账号但 schedulable=false。2026-06-08 直接上游 smoke：原生 1K 与桥接 1K 均返回 403 Image generation is not enabled for this group；本台账不把它当公开生图成本来源，启用前需先确认上游分组已开生图，再按 1K/2K/4K 真实流水核价。",
    },
    {
        "category": "Kingdom",
        "kind": "Claude",
        "site": "api.tokenskingdom.com",
        "fluter_account_name": "kingdom claude 2",
        "upstream_group": "CC Max 1号池",
        "page_rate": 25,
        "recharge_ratio_label": "148.88 RMB = 2000 USD",
        "recharge_factor": KINGDOM_RECHARGE_FACTOR,
        "site_account_multiplier": 2,
        "site_group_multiplier": "claude ccmax 最强号池@2.5",
        "status": "已覆盖/成本记录接近",
        "note": "2026-06-08 刷新 Kingdom 钥匙页：余额 $587.05，CC Max 1号池页面 25x，今日 $6.2315、近30天 $97.6394。按充值折算，页面 25x 约等于 1.944x；账号成本倍率 2x 是内部成本记录，贴近真实成本且略偏保守；用户分组倍率 2.5x 才是售价，售价覆盖约 1.286x，不应把账号成本倍率当成售价来判断利润。",
    },
    {
        "category": "Kingdom",
        "kind": "Codex",
        "site": "api.tokenskingdom.com",
        "fluter_account_name": "kingdom codex 0.08",
        "upstream_group": "Openai 1 允许V1",
        "page_rate": 0.9,
        "recharge_ratio_label": "148.88 RMB = 2000 USD",
        "recharge_factor": KINGDOM_RECHARGE_FACTOR,
        "site_account_multiplier": 0.1,
        "site_group_multiplier": "codex 兜底稳定渠道@0.3",
        "status": "已覆盖",
        "note": "2026-06-08 刷新 Kingdom 钥匙页：余额 $587.05，Openai 1 允许V1 页面 0.9x；按充值折算约等于 0.069984x。账号成本倍率 0.1x 是内部成本记录，用户分组倍率 0.3x 是售价，有安全垫。",
    },
]


SCHEMA = """
create table if not exists upstream_rate_records (
  id integer primary key autoincrement,
  category text not null,
  kind text not null,
  site text not null,
  fluter_account_name text not null,
  upstream_group text not null,
  page_rate real,
  recharge_ratio_label text not null,
  recharge_factor real not null,
  site_account_multiplier real,
  site_group_multiplier text not null,
  actual_cost_label text not null default '',
  balance_label text not null default '',
  balance_updated_at text not null default '',
  status text not null,
  note text not null,
  updated_at text not null,
  unique(site, fluter_account_name, upstream_group)
);

create table if not exists metadata (
  key text primary key,
  value text not null
);

create table if not exists kbq_token_model_records (
  id integer primary key autoincrement,
  category text not null,
  model_name text not null,
  base_model text not null,
  kbq_group_key text not null default '',
  kbq_group_ratio real,
  group_ratio_source text not null default '',
  pricing_status text not null default 'OK',
  cost_multiplier real,
  endpoints text not null,
  input_usd_per_1m real,
  output_usd_per_1m real,
  cache_read_usd_per_1m real,
  cache_write_usd_per_1m real,
  raw_model_ratio real,
  official_input_usd_per_1m real,
  official_output_usd_per_1m real,
  official_cache_read_usd_per_1m real,
  official_cache_write_usd_per_1m real,
  official_label text not null,
  pricing_version text not null,
  source_url text not null,
  note text not null,
  updated_at text not null,
  unique(category, model_name, kbq_group_key)
);
"""


def compact_number(value: float) -> str:
    text = f"{value:.9f}".rstrip("0").rstrip(".")
    return text or "0"


def computed_actual_cost_label(row: dict) -> str:
    page_rate = row.get("page_rate")
    if page_rate is None:
        return ""
    recharge_factor = row.get("recharge_factor", 1)
    actual = float(page_rate) * float(recharge_factor)
    return (
        f"实际成本倍率 {compact_number(actual)}x"
        f"（页面倍率 {compact_number(float(page_rate))} × 充值系数 {compact_number(float(recharge_factor))}）"
    )


UPSERT = """
insert into upstream_rate_records (
  category, kind, site, fluter_account_name, upstream_group, page_rate,
  recharge_ratio_label, recharge_factor, site_account_multiplier,
  site_group_multiplier, actual_cost_label, balance_label, balance_updated_at, status, note, updated_at
) values (
  :category, :kind, :site, :fluter_account_name, :upstream_group, :page_rate,
  :recharge_ratio_label, :recharge_factor, :site_account_multiplier,
  :site_group_multiplier, :actual_cost_label, :balance_label, :balance_updated_at, :status, :note, :updated_at
)
on conflict(site, fluter_account_name, upstream_group) do update set
  category = excluded.category,
  kind = excluded.kind,
  page_rate = excluded.page_rate,
  recharge_ratio_label = excluded.recharge_ratio_label,
  recharge_factor = excluded.recharge_factor,
  site_account_multiplier = excluded.site_account_multiplier,
  site_group_multiplier = excluded.site_group_multiplier,
  actual_cost_label = excluded.actual_cost_label,
  balance_label = excluded.balance_label,
  balance_updated_at = excluded.balance_updated_at,
  status = excluded.status,
  note = excluded.note,
  updated_at = excluded.updated_at;
"""


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--db", default=DEFAULT_DB)
    parser.add_argument("--reset", action="store_true")
    return parser.parse_args()


def main() -> None:
    args = parse_args()
    db_path = Path(args.db)
    db_path.parent.mkdir(parents=True, exist_ok=True)
    conn = sqlite3.connect(db_path)
    conn.row_factory = sqlite3.Row
    with conn:
        if args.reset:
            conn.execute("drop table if exists upstream_rate_records")
            conn.execute("drop table if exists metadata")
            conn.execute("drop table if exists upstream_discount_profiles")
        conn.executescript(SCHEMA)
        seed_default_discount_profiles(conn, overwrite=True)
        columns = {
            row["name"]
            for row in conn.execute("pragma table_info(upstream_rate_records)")
        }
        if "balance_label" not in columns:
            conn.execute(
                "alter table upstream_rate_records add column balance_label text not null default ''"
            )
        if "balance_updated_at" not in columns:
            conn.execute(
                "alter table upstream_rate_records add column balance_updated_at text not null default ''"
            )
        if "actual_cost_label" not in columns:
            conn.execute(
                "alter table upstream_rate_records add column actual_cost_label text not null default ''"
            )
        now = datetime.now(timezone.utc).isoformat(timespec="seconds")
        for record in RECORDS:
            row = dict(record)
            if not row.get("actual_cost_label"):
                row["actual_cost_label"] = computed_actual_cost_label(row)
            row.setdefault("balance_label", BALANCES.get(row["site"], "未记录"))
            row.setdefault("balance_updated_at", BALANCE_SNAPSHOT_AT)
            row["updated_at"] = now
            conn.execute(UPSERT, row)
        conn.execute(
            "insert or replace into metadata(key, value) values (?, ?)",
            ("last_seeded_at", now),
        )
        conn.execute(
            "insert or replace into metadata(key, value) values (?, ?)",
            ("source", "Safari 登录页只读观察；生产库 accounts.rate_multiplier 只读查询"),
        )
    print(f"Seeded {len(RECORDS)} records into {db_path}")


if __name__ == "__main__":
    main()
