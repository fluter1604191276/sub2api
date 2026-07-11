#!/usr/bin/env python3
from __future__ import annotations

import argparse
import importlib.util
import subprocess
import sys
import unittest
from decimal import Decimal
from pathlib import Path


SCRIPT = Path(__file__).with_name("sync_account_multipliers_from_ledger.py")


def load_module():
    spec = importlib.util.spec_from_file_location("sync_account_multipliers_from_ledger", SCRIPT)
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class SyncAccountMultipliersTest(unittest.TestCase):
    def setUp(self) -> None:
        self.mod = load_module()

    def args(self, **overrides):
        values = {
            "db": ":memory:",
            "ssh_host": "unused",
            "compose_dir": "/www/sub2api",
            "backup_dir": "/tmp",
            "local_postgres": True,
            "create_drafts": False,
            "apply": False,
            "include_inactive": False,
            "include_image": False,
            "include_conservative": False,
            "threshold": "0.000001",
            "max_changes": 50,
        }
        values.update(overrides)
        return argparse.Namespace(**values)

    def ledger(self, **overrides):
        values = {
            "id": 1,
            "category": "Codex",
            "kind": "文字",
            "site": "https://example.com",
            "account_name": "Example codex 0.15",
            "upstream_group": "default",
            "page_rate": Decimal("0.15"),
            "recharge_factor": Decimal("0.9"),
            "site_account_multiplier": Decimal("0.15"),
            "status": "已覆盖",
            "note": "",
        }
        values.update(overrides)
        return self.mod.LedgerRow(**values)

    def account(self, **overrides):
        values = {
            "id": 10,
            "name": "Example codex 0.15",
            "base_url": "https://example.com/v1",
            "base_host": "example.com",
            "stem": "example codex",
            "status": "active",
            "schedulable": True,
            "rate_multiplier": Decimal("0.15"),
            "notes": "",
        }
        values.update(overrides)
        return self.mod.AccountRow(**values)

    def patch_sources(self, rows, accounts):
        original_rows = self.mod.load_ledger_rows
        original_accounts = self.mod.load_accounts
        self.mod.load_ledger_rows = lambda _db: rows
        self.mod.load_accounts = lambda _args: accounts
        self.addCleanup(lambda: setattr(self.mod, "load_ledger_rows", original_rows))
        self.addCleanup(lambda: setattr(self.mod, "load_accounts", original_accounts))

    def test_cost_multiplier_uses_page_rate_times_recharge_factor(self):
        row = self.ledger(page_rate=Decimal("0.2"), recharge_factor=Decimal("0.8"))
        self.patch_sources([row], [self.account(rate_multiplier=Decimal("0.10"))])

        changes, skipped = self.mod.plan_changes(self.args())

        self.assertEqual([], skipped)
        self.assertEqual(1, len(changes))
        self.assertEqual(Decimal("0.16"), changes[0].target_multiplier)

    def test_default_skip_image_special_and_unconfirmed_records(self):
        rows = [
            self.ledger(account_name="image", kind="生图"),
            self.ledger(account_name="special", kind="特殊"),
            self.ledger(account_name="inactive", status="未接入"),
        ]
        self.patch_sources(rows, [self.account()])

        changes, skipped = self.mod.plan_changes(self.args())

        self.assertEqual([], changes)
        self.assertEqual(3, len(skipped))
        self.assertTrue(any("生图" in item for item in skipped))
        self.assertTrue(any("特殊" in item for item in skipped))
        self.assertTrue(any("未接入" in item for item in skipped))

    def test_existing_draft_note_source_id_blocks_duplicate_even_if_renamed(self):
        source = self.account(id=10, name="Example codex 0.15", rate_multiplier=Decimal("0.15"))
        renamed_draft = self.account(
            id=99,
            name="人工改名后的草案",
            rate_multiplier=Decimal("0.135"),
            notes="[2026-06-11 21:00 CST] 台账草案：原账号 id=10，原账号名=Example codex 0.15；等待人工核对。",
        )
        self.patch_sources([self.ledger(page_rate=Decimal("0.20"), recharge_factor=Decimal("1"))], [source, renamed_draft])

        changes, skipped = self.mod.plan_changes(self.args())

        self.assertEqual([], changes)
        self.assertTrue(any("已存在原账号 id=10 的草案账号" in item for item in skipped))
        self.assertTrue(renamed_draft.is_draft)

    def test_ambiguous_matches_are_skipped(self):
        account_a = self.account(id=10)
        account_b = self.account(id=11)
        self.patch_sources([self.ledger()], [account_a, account_b])

        changes, skipped = self.mod.plan_changes(self.args())

        self.assertEqual([], changes)
        self.assertTrue(any("匹配不唯一" in item for item in skipped))

    def test_semantic_match_pairs_magic_proxy_fast_rename(self):
        row = self.ledger(
            site="pool.gptstore.club",
            account_name="magic codex 0.04",
            upstream_group="代理快速渠道（不能生图）",
            page_rate=Decimal("0.045"),
            recharge_factor=Decimal("1"),
        )
        accounts = [
            self.account(
                id=1,
                name="magic codex pro 仅文字0.08",
                base_url="https://pool.gptstore.club/v1",
                base_host="pool.gptstore.club",
                stem="magic codex pro",
                rate_multiplier=Decimal("0.08"),
            ),
            self.account(
                id=2,
                name="magic codex 代理快速通道 仅文字0.045",
                base_url="https://pool.gptstore.club/v1",
                base_host="pool.gptstore.club",
                stem="magic codex 代理快速通道",
                rate_multiplier=Decimal("0.045"),
            ),
        ]
        self.patch_sources([row], accounts)

        changes, skipped = self.mod.plan_changes(self.args())

        self.assertEqual([], changes)
        self.assertFalse(any("magic codex 0.04" in item for item in skipped))

    def test_calculated_suffixes_share_account_stem(self):
        cases = {
            "KBQ claude kiro 低缓 0.135": "kbq claude kiro 低缓",
            "KBQ claude kiro 低缓 0.2*0.9=0.18": "kbq claude kiro 低缓",
            "kingdom claude ccmax 0.078*20=1.556": "kingdom claude max",
            "kingdom codex pro 仅文字0.078*3=0.234": "kingdom codex pro",
            "钧澈 codex 仅生图 3.6分*0.91=3.2728分": "钧澈 codex",
            "钧澈 codex 对接倍率 仅文字0.05*0.91=0.0455": "钧澈 codex 对接倍率",
            "钧澈 codex 优质plus 仅文字0.085*0.91=0.07735": "钧澈 codex 优质plus",
            "钧澈 codex pro破限 仅文字0.25*0.91=0.22728": "钧澈 codex pro破限",
            "meow claude ccmax仅客户端 1.1": "meow claude max",
            "meow claude max仅客户端 1.1": "meow claude max",
            "magic codex pro 仅文字0.08": "magic codex pro",
            "magic codex 代理快速通道 仅文字0.045": "magic codex 代理快速通道",
            "超超(主站) codex pro 仅文字0.1": "超超(主站) codex pro",
        }

        for name, expected in cases.items():
            with self.subTest(name=name):
                self.assertEqual(expected, self.mod.account_stem(name))

    def test_conservative_multiplier_is_skipped_by_default(self):
        row = self.ledger(page_rate=Decimal("0.008"), recharge_factor=Decimal("1"))
        account = self.account(name="Example codex 0.06", stem="example codex", rate_multiplier=Decimal("0.06"))
        self.patch_sources([row], [account])

        changes, skipped = self.mod.plan_changes(self.args())

        self.assertEqual([], changes)
        self.assertTrue(any("属于保守记录" in item for item in skipped))

    def test_conservative_multiplier_can_be_included_explicitly(self):
        row = self.ledger(page_rate=Decimal("0.008"), recharge_factor=Decimal("1"))
        account = self.account(name="Example codex 0.06", stem="example codex", rate_multiplier=Decimal("0.06"))
        self.patch_sources([row], [account])

        changes, skipped = self.mod.plan_changes(self.args(include_conservative=True))

        self.assertEqual([], skipped)
        self.assertEqual(1, len(changes))
        self.assertEqual(Decimal("0.008"), changes[0].target_multiplier)

    def test_create_draft_sql_does_not_assign_groups(self):
        change = self.mod.PlannedChange(
            ledger=self.ledger(),
            account=self.account(),
            match_type="exact_name",
            target_multiplier=Decimal("0.135"),
            new_name="（修改）Example codex 0.15",
            note_line="台账草案：原账号 id=10，等待人工核对。",
        )
        captured = {}
        original_backup = self.mod.create_backup
        original_run_psql = self.mod.run_psql
        self.mod.create_backup = lambda _args: "/tmp/backup.sql"

        def fake_run_psql(_args, sql):
            captured["sql"] = sql
            return "10|100|（修改）Example codex 0.15\n"

        self.mod.run_psql = fake_run_psql
        self.addCleanup(lambda: setattr(self.mod, "create_backup", original_backup))
        self.addCleanup(lambda: setattr(self.mod, "run_psql", original_run_psql))

        backup, created = self.mod.create_draft_accounts(self.args(), [change])

        sql = captured["sql"].lower()
        self.assertEqual("/tmp/backup.sql", backup)
        self.assertIn("schedulable", sql)
        self.assertIn("false", sql)
        self.assertNotIn("account_groups", sql)
        self.assertEqual([(100, "（修改）Example codex 0.15")], created)

    def test_apply_exits_before_database_access(self):
        proc = subprocess.run(
            [sys.executable, str(SCRIPT), "--apply"],
            text=True,
            capture_output=True,
            check=False,
            timeout=5,
        )

        self.assertNotEqual(0, proc.returncode)
        self.assertIn("--apply has been disabled", proc.stderr + proc.stdout)


if __name__ == "__main__":
    unittest.main()
