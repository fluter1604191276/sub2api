#!/usr/bin/env python3
"""Run the real Linux backup script with synthetic Docker and narrow fault injection.

No network, credentials, database service or production directory is used here.
Archive tools, comparisons, flush and atomic publication are real GNU/Linux tools.
"""
import hashlib
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tarfile
import tempfile
import unittest

SCRIPT = Path(__file__).with_name("backup-sub2api.sh")
WRAPPER = r'''
import io,json,os,shutil,subprocess,sys,tarfile
from pathlib import Path
cmd=Path(sys.argv[0]).name; args=sys.argv[1:]; mode=os.environ['FAULT']
root=Path(os.environ['FIXTURE_ROOT'])
with (root/'events').open('a') as f:f.write(json.dumps([cmd,args])+'\n')
real=json.loads(os.environ['REAL_TOOLS'])[cmd]
if cmd=='docker':
    assert args==['compose','-f',os.environ['DEPLOY_DIR']+'/docker-compose.yml','exec','-T','postgres','pg_dump','-U','synthetic','-d','synthetic','--clean','--if-exists','--no-owner','--no-privileges'],args
    if mode=='dump_failure':sys.exit(42)
    if mode!='empty_dump':sys.stdout.write('CREATE TABLE synthetic(id integer);\nINSERT INTO synthetic VALUES(1);\n')
    sys.exit(0)
if cmd=='date':print('20261009T000000Z');sys.exit(0)
if cmd=='tar' and '-czf' in args:
    target=Path(args[args.index('-czf')+1]);outer=target.name.startswith('.archive-')
    if outer and mode=='partial':target.write_bytes(b'partial gzip');sys.exit(28)
    if outer and mode=='empty_archive':target.write_bytes(b'');sys.exit(0)
    if outer and mode=='corrupt_archive':target.write_bytes(b'corrupt gzip');sys.exit(0)
    if not outer and mode=='corrupt_nested':target.write_bytes(b'corrupt nested');sys.exit(0)
    if outer and mode in ('wrong_member','extra_member','duplicate_empty'):
        stage=Path(args[args.index('-C')+1])
        with tarfile.open(target,'w:gz') as t:
            if mode=='wrong_member':
                t.add(stage,arcname='.',recursive=False)
                for member in stage.iterdir():
                    if member.name=='postgres.sql':
                        v=tarfile.TarInfo('./postgres.sql');v.size=7;t.addfile(v,io.BytesIO(b'ALTERED'))
                    else:t.add(member,arcname='./'+member.name)
            else:
                t.add(stage,arcname='.')
                v=tarfile.TarInfo('./env.example' if mode=='duplicate_empty' else './unexpected');v.size=0;t.addfile(v,io.BytesIO(b''))
        sys.exit(0)
    if outer and mode=='close_failure':
        code=subprocess.call([real,*args]);sys.exit(code or 74)
if cmd=='sync' and mode=='flush_failure':sys.exit(75)
if cmd=='chmod' and mode=='chmod_failure' and any('.archive-' in x for x in args):sys.exit(72)
if cmd=='ln':
    target=Path(args[-1])
    if mode=='publish_failure':sys.exit(76)
    if mode=='collision_race':shutil.copyfile(root/'old-valid.tar.gz',target)
    if mode=='directory_race':target.mkdir()
    if mode=='symlink_directory_race':
        directory=root/'unowned-directory';directory.mkdir();target.symlink_to(directory,target_is_directory=True)
if cmd=='rm' and mode in ('cleanup_temp_failure','cleanup_preserve_error') and any('.archive-' in x for x in args):sys.exit(77)
if cmd=='rm' and mode=='cleanup_work_failure' and any('.tmp-' in x for x in args):sys.exit(78)
if cmd=='rmdir' and mode=='cleanup_lock_failure':sys.exit(79)
if cmd=='find' and mode=='retention_failure':sys.exit(44)
if cmd=='tar' and '-czf' in args and mode=='cleanup_preserve_error' and '.archive-' in args[args.index('-czf')+1]:sys.exit(28)
os.execv(real,[real,*args])
'''


class BackupArchiveTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="sub2api-backup-test-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.deploy = self.root / "deploy"
        self.backups = self.root / "backups"
        self.bins = self.root / "bin"
        for p in (self.deploy, self.backups, self.bins):
            p.mkdir()
        self.role = self.root / "role"
        self.role.write_text("production\n")
        (self.deploy / ".env").write_text("POSTGRES_USER=synthetic\nPOSTGRES_DB=synthetic\n")
        (self.deploy / "docker-compose.yml").write_text("services: {}\n")
        (self.deploy / ".env.example").write_text("")
        (self.deploy / "data/logs").mkdir(parents=True)
        (self.deploy / "data/state").write_bytes(b"synthetic application")
        (self.deploy / "data/logs/excluded").write_text("synthetic excluded log")
        (self.deploy / "redis_data").mkdir()
        (self.deploy / "redis_data/state").write_bytes(b"synthetic redis")
        self.old = self.backups / "sub2api-backup-20000101T000000Z.tar.gz"
        with tarfile.open(self.old, "w:gz") as t:
            item = tarfile.TarInfo("old-valid")
            item.size = 3
            t.addfile(item, io.BytesIO(b"OLD"))
        shutil.copyfile(self.old, self.root / "old-valid.tar.gz")
        self.old_hash = hashlib.sha256(self.old.read_bytes()).hexdigest()
        self.final = self.backups / "sub2api-backup-20261009T000000Z.tar.gz"
        commands = ("docker", "date", "tar", "sync", "ln", "rm", "rmdir", "chmod", "find", "cmp")
        tools = {c: shutil.which(c) or "/bin/false" for c in commands}
        for c in commands:
            p = self.bins / c
            p.write_text(f"#!{sys.executable}\n" + WRAPPER)
            p.chmod(0o755)
        self.env = dict(os.environ, PATH=f"{self.bins}:/usr/bin:/bin", DEPLOY_DIR=str(self.deploy),
                        BACKUP_DIR=str(self.backups), NODE_ROLE_FILE=str(self.role),
                        BACKUP_CLEANUP_MODE="delete", FIXTURE_ROOT=str(self.root),
                        REAL_TOOLS=json.dumps(tools), FAULT="none")

    def run_script(self, fault="none", **extra):
        result = subprocess.run(["/bin/bash", str(SCRIPT)], env=dict(self.env, FAULT=fault, **extra),
                                capture_output=True, text=True, timeout=30)
        self.assertNotIn(result.returncode, (126, 127), result.stderr)
        self.assertEqual(hashlib.sha256(self.old.read_bytes()).hexdigest(), self.old_hash)
        return result

    def events(self):
        p = self.root / "events"
        return [json.loads(x) for x in p.read_text().splitlines()] if p.exists() else []

    def assert_no_temporaries(self):
        self.assertEqual(list(self.backups.glob(".tmp-*")), [])
        self.assertEqual(list(self.backups.glob(".archive-*")), [])
        self.assertFalse((self.backups / ".sub2api-backup.lock").exists())

    def assert_failed_before_publish(self, fault, expected=None):
        result = self.run_script(fault)
        self.assertNotEqual(result.returncode, 0, result.stdout)
        if expected is not None:
            self.assertEqual(result.returncode, expected, result.stderr)
        self.assertFalse(self.final.exists())
        self.assertNotIn("backup created:", result.stdout)
        self.assertFalse(any(cmd == "find" for cmd, _ in self.events()))
        self.assert_no_temporaries()

    def test_success_content_restore_permissions_publication_order(self):
        result = self.run_script()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("backup created:", result.stdout)
        self.assertEqual(self.final.stat().st_mode & 0o777, 0o600)
        with tarfile.open(self.final) as archive:
            self.assertEqual(archive.extractfile("./env").read(), (self.deploy / ".env").read_bytes())
            self.assertEqual(archive.extractfile("./postgres.sql").read(), b"CREATE TABLE synthetic(id integer);\nINSERT INTO synthetic VALUES(1);\n")
            self.assertEqual(archive.extractfile("./docker-compose.yml").read(), (self.deploy / "docker-compose.yml").read_bytes())
            for name, expected in (("data", b"synthetic application"), ("redis_data", b"synthetic redis")):
                with tarfile.open(fileobj=io.BytesIO(archive.extractfile(f"./{name}.tar.gz").read())) as nested:
                    self.assertEqual(nested.extractfile(f"{name}/state").read(), expected)
                    self.assertNotIn("data/logs/excluded", nested.getnames())
        commands = [cmd for cmd, _ in self.events()]
        self.assertLess(commands.index("sync"), commands.index("ln"))
        self.assertLess(commands.index("ln"), commands.index("find"))
        self.assert_no_temporaries()

    def test_partial_enospc(self): self.assert_failed_before_publish("partial", 28)
    def test_late_producer_close_error(self): self.assert_failed_before_publish("close_failure", 74)
    def test_flush_error(self): self.assert_failed_before_publish("flush_failure", 75)
    def test_atomic_publication_error(self): self.assert_failed_before_publish("publish_failure", 76)
    def test_permission_error(self): self.assert_failed_before_publish("chmod_failure", 72)
    def test_empty_archive(self): self.assert_failed_before_publish("empty_archive")
    def test_corrupt_archive(self): self.assert_failed_before_publish("corrupt_archive")
    def test_valid_gzip_altered_member(self):
        self.assert_failed_before_publish("wrong_member", 1)
        comparisons = [args for cmd, args in self.events() if cmd == "cmp"]
        self.assertIn(".expected-members", comparisons[0][0])
        self.assertEqual(comparisons[-1][0], "-")
        self.assertTrue(comparisons[-1][1].endswith("/postgres.sql"))
    def test_valid_gzip_unexpected_member(self): self.assert_failed_before_publish("extra_member")
    def test_duplicate_empty_member(self): self.assert_failed_before_publish("duplicate_empty")
    def test_corrupt_nested_archive(self): self.assert_failed_before_publish("corrupt_nested")
    def test_empty_dump(self): self.assert_failed_before_publish("empty_dump")
    def test_dump_failure(self): self.assert_failed_before_publish("dump_failure", 42)

    def test_existing_name_preserved_without_dump(self):
        shutil.copyfile(self.old, self.final)
        expected = self.final.read_bytes()
        result = self.run_script()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.final.read_bytes(), expected)
        self.assertFalse(any(cmd == "docker" for cmd, _ in self.events()))
        self.assert_no_temporaries()

    def test_late_existing_file_never_overwritten(self):
        result = self.run_script("collision_race")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.final.read_bytes(), self.old.read_bytes())
        self.assertNotIn("backup created:", result.stdout)
        self.assertFalse(any(cmd == "find" for cmd, _ in self.events()))
        self.assert_no_temporaries()

    def test_directory_publication_race_rejected(self):
        result = self.run_script("directory_race")
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue(self.final.is_dir())
        self.assertEqual(list(self.final.iterdir()), [])
        self.assertNotIn("backup created:", result.stdout)
        self.assert_no_temporaries()

    def test_symlink_directory_publication_race_rejected(self):
        result = self.run_script("symlink_directory_race")
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue(self.final.is_symlink())
        self.assertEqual(list((self.root / "unowned-directory").iterdir()), [])
        self.assertNotIn("backup created:", result.stdout)
        self.assert_no_temporaries()

    def test_foreign_lock_preserved_without_dump(self):
        lock = self.backups / ".sub2api-backup.lock"
        lock.mkdir()
        (lock / "owner").write_text("unowned")
        result = self.run_script()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual((lock / "owner").read_text(), "unowned")
        self.assertFalse(any(cmd == "docker" for cmd, _ in self.events()))

    def test_invalid_policy_precedes_dump_and_publication(self):
        for options in ({"BACKUP_CLEANUP_MODE": "bad"}, {"RETENTION_DAYS": "-1"}, {"MANUAL_SQL_RETENTION_DAYS": "bad"}):
            with self.subTest(options=options):
                result = self.run_script(**options)
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(self.final.exists())
                self.assertFalse(any(cmd == "docker" for cmd, _ in self.events()))
                self.assert_no_temporaries()

    def test_env_cannot_replace_retention_controls(self):
        with (self.deploy / ".env").open("a") as f: f.write("BACKUP_CLEANUP_MODE=bad\n")
        result = self.run_script()
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.final.exists())
        self.assertFalse(any(cmd == "find" for cmd, _ in self.events()))
        self.assert_no_temporaries()

    def test_cleanup_attempts_work_and_lock_after_temp_unlink_failure(self):
        result = self.run_script("cleanup_temp_failure")
        self.assertEqual(result.returncode, 77)
        self.assertTrue(self.final.is_file())  # Already verified and atomically published.
        self.assertNotIn("backup created:", result.stdout)
        self.assertEqual(len(list(self.backups.glob(".archive-*"))), 1)
        self.assertEqual(list(self.backups.glob(".tmp-*")), [])
        self.assertFalse((self.backups / ".sub2api-backup.lock").exists())

    def test_cleanup_does_not_mask_original_error(self):
        result = self.run_script("cleanup_preserve_error")
        self.assertEqual(result.returncode, 28)
        self.assertFalse(self.final.exists())
        self.assertEqual(list(self.backups.glob(".tmp-*")), [])
        self.assertFalse((self.backups / ".sub2api-backup.lock").exists())

    def test_cleanup_work_error_is_failure_and_still_releases_lock(self):
        result = self.run_script("cleanup_work_failure")
        self.assertEqual(result.returncode, 1)
        self.assertTrue(self.final.is_file())
        self.assertEqual(len(list(self.backups.glob(".tmp-*"))), 1)
        self.assertEqual(list(self.backups.glob(".archive-*")), [])
        self.assertFalse((self.backups / ".sub2api-backup.lock").exists())

    def test_cleanup_lock_error_is_failure(self):
        result = self.run_script("cleanup_lock_failure")
        self.assertEqual(result.returncode, 1)
        self.assertTrue((self.backups / ".sub2api-backup.lock").is_dir())
        self.assertEqual(list(self.backups.glob(".tmp-*")), [])
        self.assertEqual(list(self.backups.glob(".archive-*")), [])

    def test_retention_failure_keeps_valid_published_archive_and_returns_error(self):
        result = self.run_script("retention_failure")
        self.assertEqual(result.returncode, 44)
        self.assertTrue(self.final.is_file())
        with tarfile.open(self.final) as archive:
            self.assertTrue(archive.extractfile("./postgres.sql").read())
        self.assertNotIn("backup created:", result.stdout)
        self.assert_no_temporaries()

    def test_role_and_config_guard(self):
        self.role.write_text("production suffix\n")
        result = self.run_script()
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.final.exists())
        self.assertFalse(any(cmd == "docker" for cmd, _ in self.events()))
        self.assert_no_temporaries()
        self.role.write_text("production\n")
        (self.deploy / ".env").unlink()
        result = self.run_script()
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(any(cmd == "docker" for cmd, _ in self.events()))
        self.assert_no_temporaries()


if __name__ == "__main__":
    unittest.main(verbosity=2)
