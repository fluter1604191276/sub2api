#!/usr/bin/env python3
from __future__ import annotations

import importlib.util
import sqlite3
import sys
import unittest
from decimal import Decimal
from pathlib import Path


SCRIPT = Path(__file__).with_name("discount_profiles.py")


def load_module():
    spec = importlib.util.spec_from_file_location("discount_profiles", SCRIPT)
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class DiscountProfilesTest(unittest.TestCase):
    def setUp(self) -> None:
        self.mod = load_module()

    def test_seed_default_profiles_records_confirmed_recharge_factors(self):
        conn = sqlite3.connect(":memory:")
        conn.row_factory = sqlite3.Row

        inserted = self.mod.seed_default_discount_profiles(conn, overwrite=True)
        profiles = self.mod.load_discount_profiles(conn)

        self.assertGreaterEqual(inserted, 1)
        self.assertAlmostEqual(0.9, float(profiles["xn--vduyey89e.com"].recharge_factor))
        self.assertAlmostEqual(100 / 108, float(profiles["vip.lcodex.cn"].recharge_factor))
        self.assertAlmostEqual(148.88 / 2000, float(profiles["api.tokenskingdom.com"].recharge_factor))
        self.assertEqual("discount_profile", profiles["vip.lcodex.cn"].source)

    def test_effective_discount_prefers_site_profile_and_falls_back_to_row_snapshot(self):
        conn = sqlite3.connect(":memory:")
        conn.row_factory = sqlite3.Row
        self.mod.seed_default_discount_profiles(conn, overwrite=True)
        profiles = self.mod.load_discount_profiles(conn)

        junche = self.mod.effective_discount_for_site(
            profiles,
            "vip.lcodex.cn",
            fallback_factor="1",
            fallback_label="行内旧 1:1",
        )
        unknown = self.mod.effective_discount_for_site(
            profiles,
            "example.invalid",
            fallback_factor="0.42",
            fallback_label="临时折扣",
        )
        no_fallback = self.mod.effective_discount_for_site(profiles, "missing.invalid")

        self.assertAlmostEqual(100 / 108, float(junche.recharge_factor))
        self.assertEqual("discount_profile", junche.source)
        self.assertEqual(Decimal("0.42"), unknown.recharge_factor)
        self.assertEqual("row_fallback", unknown.source)
        self.assertEqual("行内兜底", unknown.status)
        self.assertEqual(Decimal("1"), no_fallback.recharge_factor)
        self.assertEqual("待核对", no_fallback.status)


if __name__ == "__main__":
    unittest.main()
