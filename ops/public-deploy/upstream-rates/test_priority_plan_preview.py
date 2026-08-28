#!/usr/bin/env python3
from __future__ import annotations

import importlib.util
import sqlite3
import sys
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name('plan_account_priority_buckets.py')
RENDER_SCRIPT = Path(__file__).with_name('render_upstream_dashboard.py')
SEED_SCRIPT = Path(__file__).with_name('seed_upstream_rates.py')


def load_module(path: Path, name: str):
    spec = importlib.util.spec_from_file_location(name, path)
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class PriorityPlanPreviewTest(unittest.TestCase):
    def setUp(self) -> None:
        self.plan_mod = load_module(SCRIPT, 'plan_account_priority_buckets')
        self.render_mod = load_module(RENDER_SCRIPT, 'render_upstream_dashboard')
        self.seed_mod = load_module(SEED_SCRIPT, 'seed_upstream_rates')

    def test_seed_includes_congmingai_main_row(self):
        row = next(item for item in self.seed_mod.RECORDS if item['site'] == 'sub2.congmingai.com')
        self.assertEqual('聪明AI', row['category'])
        self.assertEqual('Codex', row['kind'])
        self.assertEqual('聪明ai codex 对接 仅文字0.05', row['fluter_account_name'])
        self.assertEqual(0.05, row['page_rate'])
        self.assertEqual(1, row['recharge_factor'])
        self.assertEqual(0.05, row['site_account_multiplier'])
        self.assertIn('codex 超低价渠道@0.06', row['site_group_multiplier'])
        self.assertIn('兜底稳定pro@0.25', row['site_group_multiplier'])
        self.assertEqual('已确认', row['status'])

    def test_preview_db_writer_persists_read_only_plan_rows(self):
        account = self.plan_mod.AccountRow(
            id=7,
            name='Example codex 0.12',
            priority=29,
            status='active',
            schedulable=True,
            rate_multiplier=self.plan_mod.Decimal('0.12'),
            notes='',
            groups=(self.plan_mod.GroupLink(name='codex 低价渠道', rate_multiplier=self.plan_mod.Decimal('0.06'), link_priority=1),),
        )
        change = self.plan_mod.PlannedPriority(
            account=account,
            bucket=self.plan_mod.BUCKETS['codex_pro_low'],
            target_priority=41,
            reason='命中 codex pro 王炸低价档',
            mode='bucket_fix',
        )
        tmp = tempfile.NamedTemporaryFile(suffix='.sqlite', delete=False)
        tmp.close()
        try:
            args = self.plan_mod.argparse.Namespace(preview_db=tmp.name)
            preview_path = self.plan_mod.write_priority_preview_db(args, [change])
            self.assertEqual(tmp.name, preview_path)
            conn = sqlite3.connect(tmp.name)
            conn.row_factory = sqlite3.Row
            row = conn.execute('select * from account_priority_plan_rows').fetchone()
            meta = {r['key']: r['value'] for r in conn.execute('select key, value from metadata')}
            conn.close()
            self.assertIsNotNone(row)
            self.assertEqual('Example codex 0.12', row['account_name'])
            self.assertEqual(41, row['target_priority'])
            self.assertEqual('40-49 codex pro 王炸低价', row['bucket'])
            self.assertEqual('bucket_fix', row['mode'])
            self.assertNotIn('priority_plan_manual_write_command', meta)
            self.assertIn('production notes write path retired', meta['priority_plan_preview_note'])
        finally:
            Path(tmp.name).unlink(missing_ok=True)

    def test_write_notes_flag_is_retired_before_any_production_work(self):
        old_argv = sys.argv
        sys.argv = [str(SCRIPT), '--write-notes']
        try:
            with self.assertRaises(SystemExit) as raised:
                self.plan_mod.main()
        finally:
            sys.argv = old_argv

        self.assertIn('--write-notes has been retired', str(raised.exception))

    def test_rank_target_priorities_skips_bucket_anchor_priorities(self):
        bucket = self.plan_mod.BUCKETS['codex_pro_low']

        def account(account_id: int, rate: str, current_priority: int):
            row = self.plan_mod.AccountRow(
                id=account_id,
                name=f'Example codex {rate}',
                priority=current_priority,
                status='active',
                schedulable=True,
                rate_multiplier=self.plan_mod.Decimal(rate),
                notes='',
                groups=(self.plan_mod.GroupLink(name='codex pro 王炸低价', rate_multiplier=self.plan_mod.Decimal('0.20'), link_priority=1),),
            )
            return self.plan_mod.ClassifiedAccount(account=row, bucket=bucket, reason='test bucket')

        targets = self.plan_mod.rank_target_priorities([
            account(101, '0.10', 40),
            account(102, '0.11', 41),
            account(103, '0.12', 42),
        ])

        self.assertEqual({101: 41, 102: 42, 103: 43}, targets)
        for target in targets.values():
            self.assertGreaterEqual(target, 41)
            self.assertNotEqual(0, target % 10)

    def test_bucket_anchor_accounts_are_skipped_from_note_plan(self):
        anchor = self.plan_mod.AccountRow(
            id=7404,
            name='-----王炸低价pro占位 codex 0.1-0.15-----',
            priority=40,
            status='active',
            schedulable=False,
            rate_multiplier=self.plan_mod.Decimal('1'),
            notes='',
            groups=(),
        )
        normal = self.plan_mod.AccountRow(
            id=7392,
            name='magic codex 代理快速通道 仅文字0.09',
            priority=33,
            status='active',
            schedulable=True,
            rate_multiplier=self.plan_mod.Decimal('0.09'),
            notes='',
            groups=(self.plan_mod.GroupLink(name='codex 王炸低价pro渠道', rate_multiplier=self.plan_mod.Decimal('0.15'), link_priority=1),),
        )

        class Args:
            include_inactive = False
            include_drafts = False
            include_protected = False
            family = 'all'
            write_all_notes = False
            strict_order = False

        original_load_accounts = self.plan_mod.load_accounts
        self.plan_mod.load_accounts = lambda _args: [anchor, normal]
        try:
            changes, _classified, skipped = self.plan_mod.plan(Args())
        finally:
            self.plan_mod.load_accounts = original_load_accounts

        self.assertEqual([7392], [change.account.id for change in changes])
        self.assertTrue(any('档位占位锚点账号默认跳过' in item for item in skipped))

    def test_dashboard_does_not_render_priority_preview(self):
        rows = [
            {
                'category': '聪明AI',
                'kind': 'Codex',
                'site': 'sub2.congmingai.com',
                'fluterAccountName': '聪明ai codex 对接 仅文字0.05',
                'upstreamGroup': '中转站对接分组，开池模式10次重试（个人用户不要选）',
                'pageRate': 0.05,
                'rechargeRatioLabel': '1:1（充值折扣待核对）',
                'rechargeFactor': 1,
                'actualCostMultiplier': 0.05,
                'siteAccountMultiplier': 0.05,
                'siteGroupMultiplier': '待补/用户售价未确认',
                'actualCostLabel': '',
                'balanceLabel': '$40.44',
                'balanceUpdatedAt': '只读观察 2026-06-13 10:00:00 北京时间',
                'status': '未确认/需核对',
                'note': 'note',
                'updatedAt': '2026-06-13 10:00:00 北京时间',
                'costRecordRatio': 1,
            }
        ]
        priority_plan = [
            {
                'runId': '2026-06-13T10:00:00+08:00',
                'accountId': 7,
                'accountName': 'Example codex 0.12',
                'currentPriority': 29,
                'targetPriority': 41,
                'rateMultiplier': '0.12',
                'bucket': 'codex_pro_low',
                'groups': 'codex 低价渠道@0.06',
                'reason': '命中 codex pro 王炸低价档',
                'mode': 'bucket_fix',
                'observedAt': '2026-06-13 10:00:00 北京时间',
            }
        ]
        html = self.render_mod.render(rows, [], [], None, [], [], {'last_priority_plan_preview_observed_at': '2026-06-13 10:00:00 北京时间'}, priority_plan)
        self.assertNotIn('优先级预览', html)
        self.assertNotIn('生产备注写入路径已废弃', html)
        self.assertNotIn('--write-notes', html)
        self.assertNotIn('Example codex 0.12', html)
        self.assertNotIn('只读预览卡片', html)


if __name__ == '__main__':
    unittest.main()
