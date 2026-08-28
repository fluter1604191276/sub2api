#!/usr/bin/env python3
"""Pull verified sub2api VPS backups to local storage and prune remote copies.

Default mode is dry-run.  In --apply mode each removable remote backup is:

1. hashed on the VPS,
2. copied to the local backup root,
3. hashed locally,
4. deleted remotely only when the hashes match.

The script intentionally manages only files it classifies as large backup
artifacts.  Small marker/config files and backup directories are left alone.
"""

from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import os
import re
import shlex
import shutil
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path
from typing import Iterable
from zoneinfo import ZoneInfo


DEFAULT_HOST = "fluterapi-prod"
DEFAULT_REMOTE_DIR = "/www/sub2api/backups"
DEFAULT_LOCAL_ROOT = "~/Backups/fluterapi-sub2api"
DEFAULT_MIN_SIZE_MB = 100
DEFAULT_RETENTION_TIMEZONE = "Asia/Shanghai"

REMOTE_LIST_SCRIPT = r"""
import json
import os
from pathlib import Path

root = Path(%(remote_dir)r)
items = []
for p in root.iterdir():
    try:
        st = p.stat()
    except FileNotFoundError:
        continue
    items.append({
        "name": p.name,
        "path": str(p),
        "type": "dir" if p.is_dir() else "file",
        "size": st.st_size,
        "mtime": st.st_mtime,
    })
print(json.dumps(items, ensure_ascii=False))
"""


@dataclass(frozen=True)
class RemoteItem:
    name: str
    path: str
    type: str
    size: int
    mtime: float

    @property
    def mtime_utc(self) -> dt.datetime:
        return dt.datetime.fromtimestamp(self.mtime, tz=dt.timezone.utc)

    def mtime_at(self, tz: dt.tzinfo) -> dt.datetime:
        return self.mtime_utc.astimezone(tz)

    def mtime_date(self, tz: dt.tzinfo) -> dt.date:
        return self.mtime_at(tz).date()


@dataclass(frozen=True)
class Plan:
    today: dt.date
    timezone: str
    keep: list[RemoteItem]
    transfer: list[RemoteItem]
    ignored: list[RemoteItem]
    local_prune: list[Path]


def run(cmd: list[str], *, check: bool = True, capture: bool = True) -> subprocess.CompletedProcess[str]:
    proc = subprocess.run(
        cmd,
        text=True,
        stdout=subprocess.PIPE if capture else None,
        stderr=subprocess.PIPE if capture else None,
    )
    if check and proc.returncode != 0:
        rendered = " ".join(shlex.quote(part) for part in cmd)
        stdout = (proc.stdout or "").strip()
        stderr = (proc.stderr or "").strip()
        details = [f"command failed ({proc.returncode}): {rendered}"]
        if stdout:
            details.append(f"stdout:\n{stdout}")
        if stderr:
            details.append(f"stderr:\n{stderr}")
        raise RuntimeError("\n".join(details))
    return proc


def ssh(host: str, remote_cmd: str) -> str:
    proc = run(["ssh", host, remote_cmd])
    return proc.stdout


def shell_quote(value: str) -> str:
    return shlex.quote(value)


def list_remote(host: str, remote_dir: str) -> list[RemoteItem]:
    script = REMOTE_LIST_SCRIPT % {"remote_dir": remote_dir}
    out = ssh(host, "python3 - <<'PY'\n" + script + "\nPY")
    raw_items = json.loads(out)
    return [RemoteItem(**item) for item in raw_items]


def is_large_backup(item: RemoteItem, min_size: int) -> bool:
    if item.type != "file" or item.size < min_size:
        return False
    patterns = (
        r"^sub2api-backup-\d{8}T\d{6}Z\.tar\.gz$",
        r".*\.sql\.zst$",
        r".*\.sql\.gz$",
        r".*\.sql$",
        r".*\.dump$",
        r".*\.tar\.gz$",
    )
    return any(re.match(pattern, item.name) for pattern in patterns)


def newest_daily_archive(items: Iterable[RemoteItem]) -> RemoteItem | None:
    daily = [item for item in items if re.match(r"^sub2api-backup-\d{8}T\d{6}Z\.tar\.gz$", item.name)]
    if not daily:
        return None
    return max(daily, key=lambda item: item.mtime)


def managed_local_dirs(local_root: Path) -> list[Path]:
    if not local_root.exists():
        return []
    return sorted(
        [p for p in local_root.iterdir() if p.is_dir() and p.name.startswith("vps-archive-")],
        key=lambda p: p.stat().st_mtime,
    )


def build_plan(
    items: list[RemoteItem],
    *,
    local_root: Path,
    min_size: int,
    local_retention_days: int,
    now: dt.datetime,
    retention_tz: dt.tzinfo,
    retention_timezone_name: str,
) -> Plan:
    today = now.astimezone(retention_tz).date()
    newest = newest_daily_archive(items)
    newest_path = newest.path if newest else None

    keep: list[RemoteItem] = []
    transfer: list[RemoteItem] = []
    ignored: list[RemoteItem] = []

    for item in sorted(items, key=lambda x: x.mtime, reverse=True):
        if not is_large_backup(item, min_size):
            ignored.append(item)
            continue
        if item.mtime_date(retention_tz) == today or item.path == newest_path:
            keep.append(item)
        else:
            transfer.append(item)

    cutoff = now.timestamp() - local_retention_days * 86400
    local_prune = [p for p in managed_local_dirs(local_root) if p.stat().st_mtime < cutoff]

    return Plan(
        today=today,
        timezone=retention_timezone_name,
        keep=keep,
        transfer=transfer,
        ignored=ignored,
        local_prune=local_prune,
    )


def sha256_file(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as fh:
        for chunk in iter(lambda: fh.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def remote_sha256(host: str, path: str) -> str:
    out = ssh(host, "sha256sum " + shell_quote(path))
    return out.split()[0]


def scp_from_remote(host: str, remote_path: str, local_path: Path) -> None:
    local_path.parent.mkdir(parents=True, exist_ok=True)
    tmp_path = local_path.with_suffix(local_path.suffix + ".partial")
    if tmp_path.exists():
        tmp_path.unlink()
    run(["scp", f"{host}:{remote_path}", str(tmp_path)], capture=False)
    tmp_path.replace(local_path)


def remote_delete(host: str, path: str) -> None:
    ssh(host, "rm -f -- " + shell_quote(path))


def assert_remote_production(host: str) -> None:
    role = ssh(host, "cat /etc/fluterapi-node-role").strip()
    if role != "production":
        raise RuntimeError(
            f"refusing remote backup deletion: {host} role is {role!r}, expected 'production'"
        )


def prune_local_dirs(paths: Iterable[Path]) -> None:
    for path in paths:
        shutil.rmtree(path)


def human_size(num: int) -> str:
    value = float(num)
    for unit in ("B", "KB", "MB", "GB", "TB"):
        if value < 1024 or unit == "TB":
            return f"{value:.1f}{unit}" if unit != "B" else f"{int(value)}B"
        value /= 1024
    return f"{num}B"


def print_plan(plan: Plan, local_root: Path, min_size: int) -> None:
    print(f"retention_timezone={plan.timezone}")
    print(f"today={plan.today.isoformat()}")
    print(f"local_root={local_root}")
    print(f"large_backup_min_size={human_size(min_size)}")
    print()

    print(f"REMOTE KEEP ({len(plan.keep)})")
    for item in plan.keep:
        print(f"  keep     {item.mtime_utc.isoformat()} {human_size(item.size):>8} {item.name}")
    if not plan.keep:
        print("  (none)")
    print()

    print(f"REMOTE TRANSFER+DELETE AFTER VERIFY ({len(plan.transfer)})")
    for item in plan.transfer:
        print(f"  migrate  {item.mtime_utc.isoformat()} {human_size(item.size):>8} {item.name}")
    if not plan.transfer:
        print("  (none)")
    print()

    print(f"LOCAL PRUNE MANAGED DIRS ({len(plan.local_prune)})")
    for path in plan.local_prune:
        print(f"  prune    {path}")
    if not plan.local_prune:
        print("  (none)")
    print()

    ignored_large = [item for item in plan.ignored if item.type == "file" and item.size >= min_size]
    print(f"IGNORED LARGE NON-MANAGED ITEMS ({len(ignored_large)})")
    for item in ignored_large:
        print(f"  ignored  {item.mtime_utc.isoformat()} {human_size(item.size):>8} {item.name}")
    if not ignored_large:
        print("  (none)")


def apply_plan(host: str, plan: Plan, local_root: Path) -> Path:
    run_id = dt.datetime.now(dt.timezone.utc).strftime("vps-archive-%Y%m%dT%H%M%SZ")
    run_dir = local_root / run_id
    run_dir.mkdir(parents=True, exist_ok=False)
    os.chmod(run_dir, 0o700)

    manifest: dict[str, object] = {
        "created_at_utc": dt.datetime.now(dt.timezone.utc).isoformat(),
        "host": host,
        "run_dir": str(run_dir),
        "transferred": [],
        "deleted_remote": [],
        "local_pruned": [],
    }

    try:
        for item in plan.transfer:
            print(f"[transfer] remote sha256 {item.name}")
            remote_hash = remote_sha256(host, item.path)
            local_path = run_dir / item.name
            print(f"[transfer] scp {item.name} -> {local_path}")
            scp_from_remote(host, item.path, local_path)
            local_hash = sha256_file(local_path)
            if local_hash != remote_hash:
                raise RuntimeError(f"sha256 mismatch for {item.name}: remote={remote_hash} local={local_hash}")

            sidecar = local_path.with_suffix(local_path.suffix + ".sha256")
            sidecar.write_text(f"{local_hash}  {item.name}\n")
            print(f"[verified] {item.name} sha256={local_hash}")
            remote_delete(host, item.path)
            print(f"[deleted remote] {item.path}")

            manifest["transferred"].append(  # type: ignore[index, union-attr]
                {
                    "name": item.name,
                    "remote_path": item.path,
                    "size": item.size,
                    "mtime_utc": item.mtime_utc.isoformat(),
                    "sha256": local_hash,
                }
            )
            manifest["deleted_remote"].append(item.path)  # type: ignore[index, union-attr]

        for path in plan.local_prune:
            print(f"[prune local] {path}")
            shutil.rmtree(path)
            manifest["local_pruned"].append(str(path))  # type: ignore[index, union-attr]

        (run_dir / "manifest.json").write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n")
        return run_dir
    except Exception:
        (run_dir / "manifest.failed.json").write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n")
        raise


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--host", default=DEFAULT_HOST)
    parser.add_argument("--remote-dir", default=DEFAULT_REMOTE_DIR)
    parser.add_argument("--local-root", default=DEFAULT_LOCAL_ROOT)
    parser.add_argument("--local-retention-days", type=int, default=7)
    parser.add_argument("--retention-timezone", default=DEFAULT_RETENTION_TIMEZONE)
    parser.add_argument("--min-size-mb", type=int, default=DEFAULT_MIN_SIZE_MB)
    parser.add_argument("--apply", action="store_true", help="copy, verify, then delete remote migrated files")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    local_root = Path(args.local_root).expanduser()
    min_size = args.min_size_mb * 1024 * 1024
    now = dt.datetime.now(dt.timezone.utc)
    retention_tz = ZoneInfo(args.retention_timezone)

    items = list_remote(args.host, args.remote_dir)
    plan = build_plan(
        items,
        local_root=local_root,
        min_size=min_size,
        local_retention_days=args.local_retention_days,
        now=now,
        retention_tz=retention_tz,
        retention_timezone_name=args.retention_timezone,
    )
    print_plan(plan, local_root, min_size)

    if not args.apply:
        print("\nDRY RUN ONLY. Re-run with --apply to copy, verify, and delete remote migrated files.")
        return 0

    assert_remote_production(args.host)
    run_dir = apply_plan(args.host, plan, local_root)
    print(f"\nDONE. Local verified archive dir: {run_dir}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
