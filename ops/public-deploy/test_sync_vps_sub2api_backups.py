import importlib.util
import sys
import tempfile
import unittest
from datetime import datetime, timezone
from pathlib import Path
from unittest.mock import patch
from zoneinfo import ZoneInfo


SCRIPT = Path(__file__).with_name("scripts") / "sync-vps-sub2api-backups.py"
SPEC = importlib.util.spec_from_file_location("sync_vps_sub2api_backups", SCRIPT)
MODULE = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
sys.modules[SPEC.name] = MODULE
SPEC.loader.exec_module(MODULE)


def item(name: str, kind: str = "file", size: int = 1, mtime: float = 0) -> object:
    return MODULE.RemoteItem(
        name=name,
        path=f"/www/sub2api/backups/{name}",
        type=kind,
        size=size,
        mtime=mtime,
    )


class SyncRetentionTests(unittest.TestCase):
    def test_remote_identity_requires_expected_role_and_records_fingerprint(self) -> None:
        with patch.object(MODULE, "ssh", side_effect=["production\n", "prod-host\n", "a" * 64 + "\n"]):
            self.assertEqual(
                MODULE.verify_remote_role("fluterapi-prod", "production"),
                {
                    "role": "production",
                    "hostname": "prod-host",
                    "node_fingerprint": "a" * 64,
                },
            )

        with patch.object(MODULE, "ssh", return_value="legacy\n"):
            with self.assertRaisesRegex(RuntimeError, "remote role mismatch"):
                MODULE.verify_remote_role("fluterapi-prod", "production")

    def test_directory_and_supported_file_classification(self) -> None:
        self.assertTrue(MODULE.is_large_backup(item("config-before-20260828", "dir"), 0))
        self.assertTrue(MODULE.is_large_backup(item("records.tsv", size=1), 0))
        self.assertFalse(MODULE.is_large_backup(item("manifest.json", size=1), 0))
        self.assertFalse(MODULE.is_large_backup(item("records.tsv", size=0), 1))

    def test_plan_keeps_today_and_newest_daily_archive(self) -> None:
        now = datetime(2026, 8, 29, 12, 0, tzinfo=timezone.utc)
        tz = ZoneInfo("Asia/Shanghai")
        today = now.timestamp()
        yesterday = today - 86400
        items = [
            item("sub2api-backup-20260829T034218Z.tar.gz", mtime=today),
            item("sub2api-backup-20260828T034218Z.tar.gz", mtime=yesterday),
            item("old.sql.gz", mtime=yesterday - 86400),
        ]
        with tempfile.TemporaryDirectory() as root:
            plan = MODULE.build_plan(
                items,
                local_root=Path(root),
                min_size=0,
                local_retention_days=7,
                now=now,
                retention_tz=tz,
                retention_timezone_name="Asia/Shanghai",
            )
        self.assertEqual([x.name for x in plan.keep], ["sub2api-backup-20260829T034218Z.tar.gz"])
        self.assertEqual(
            [x.name for x in plan.transfer],
            ["sub2api-backup-20260828T034218Z.tar.gz", "old.sql.gz"],
        )

    def test_noop_apply_does_not_create_archive_directory(self) -> None:
        with tempfile.TemporaryDirectory() as root:
            plan = MODULE.Plan(
                today=datetime.now(timezone.utc).date(),
                timezone="UTC",
                keep=[],
                transfer=[],
                ignored=[],
                local_prune=[],
            )
            self.assertIsNone(
                MODULE.apply_plan(
                    "fluterapi-prod",
                    {"role": "production", "hostname": "prod", "node_fingerprint": "a" * 64},
                    plan,
                    Path(root),
                )
            )
            self.assertEqual(list(Path(root).iterdir()), [])


if __name__ == "__main__":
    unittest.main()
