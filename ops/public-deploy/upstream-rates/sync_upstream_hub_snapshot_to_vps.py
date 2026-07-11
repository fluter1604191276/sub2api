#!/usr/bin/env python3
"""Export local upstream-hub observations and refresh the VPS ledger.

This is the bridge for the 13-upstream-hub merge plan: upstream-hub stays on
the Mac, its Postgres is never exposed to the VPS, and only a sanitized
observation JSON is copied to the server.
"""

from __future__ import annotations

import argparse
import json
import shlex
import subprocess
import sys
import tempfile
from pathlib import Path


DEFAULT_HUB_COMPOSE_DIR = "/Users/fluter_claw/Desktop/study_project/upstream-hub"
DEFAULT_REMOTE_SSH_HOST = "us-api-vps"
DEFAULT_REMOTE_SNAPSHOT = "/var/lib/fluterapi-upstream-rates/upstream-hub-snapshot.json"
DEFAULT_REMOTE_DB = "/var/lib/fluterapi-upstream-rates/upstream_rates.sqlite"
DEFAULT_REMOTE_OUTPUT = "/www/fluterapi-home/admin/upstream-rates/index.html"
FORBIDDEN_SNAPSHOT_MARKERS = ("password", "cookie", "Bearer", "access_token", "sk-")


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Sync sanitized upstream-hub snapshot to the VPS ledger")
    parser.add_argument("--hub-compose-dir", default=DEFAULT_HUB_COMPOSE_DIR)
    parser.add_argument("--hub-connection", choices=("auto", "docker", "tcp"), default="docker")
    parser.add_argument("--hub-psql-command", default="")
    parser.add_argument("--hub-query-timeout", type=int, default=15)
    parser.add_argument("--ssh-host", default=DEFAULT_REMOTE_SSH_HOST)
    parser.add_argument("--remote-snapshot", default=DEFAULT_REMOTE_SNAPSHOT)
    parser.add_argument("--remote-db", default=DEFAULT_REMOTE_DB)
    parser.add_argument("--remote-output", default=DEFAULT_REMOTE_OUTPUT)
    parser.add_argument("--hours", type=int, default=24)
    parser.add_argument("--local-snapshot", default="")
    parser.add_argument("--export-only", action="store_true", help="Only create the local sanitized snapshot")
    parser.add_argument("--skip-remote-refresh", action="store_true", help="Copy the snapshot but do not run VPS refresh")
    parser.add_argument(
        "--true-loss-alert-endpoint",
        default="",
        help="Optional VPS-local OpenClaw endpoint passed to refresh_upstream_ledger.py",
    )
    return parser.parse_args(argv)


def run_command(command: list[str], name: str) -> None:
    print(f"==> {name}")
    print(" ".join(shlex.quote(part) for part in command))
    proc = subprocess.run(command, text=True, check=False)
    if proc.returncode != 0:
        raise SystemExit(f"{name} failed with exit code {proc.returncode}")


def run_remote(ssh_host: str, remote_command: str, name: str) -> None:
    run_command(["ssh", "-T", ssh_host, remote_command], name)


def assert_snapshot_is_sanitized(path: Path) -> None:
    text = path.read_text(errors="replace")
    if "fluter-upstream-hub-snapshot/v1" not in text:
        raise SystemExit(f"snapshot schema marker missing: {path}")
    found = [marker for marker in FORBIDDEN_SNAPSHOT_MARKERS if marker in text]
    if found:
        raise SystemExit(f"snapshot contains forbidden marker(s): {', '.join(found)}")


def snapshot_summary(path: Path) -> dict[str, str | int]:
    payload = json.loads(path.read_text(errors="replace"))
    if payload.get("schema") != "fluter-upstream-hub-snapshot/v1":
        raise SystemExit(f"unsupported snapshot schema: {path}")
    return {
        "channels": len(payload.get("channels") or []),
        "rates": len(payload.get("rates") or []),
        "balances": len(payload.get("balances") or []),
        "rate_changes": len(payload.get("rate_changes") or []),
        "exported_at": payload.get("exported_at") or "",
    }


def print_snapshot_summary(path: Path) -> None:
    summary = snapshot_summary(path)
    print(
        "snapshot_summary "
        f"channels={summary['channels']} rates={summary['rates']} "
        f"balances={summary['balances']} rate_changes={summary['rate_changes']} "
        f"exported_at={summary['exported_at'] or '-'} path={path}"
    )


def local_snapshot_path(args: argparse.Namespace) -> Path:
    if args.local_snapshot:
        return Path(args.local_snapshot)
    tempdir = Path(tempfile.gettempdir())
    return tempdir / "fluter-upstream-hub-snapshot.json"


def export_snapshot(args: argparse.Namespace, snapshot: Path, script_dir: Path, python: str) -> None:
    command = [
        python,
        str(script_dir / "refresh_from_upstream_hub.py"),
        "--hub-compose-dir",
        args.hub_compose_dir,
        "--hub-connection",
        args.hub_connection,
        "--hub-query-timeout",
        str(args.hub_query_timeout),
        "--export-json",
        str(snapshot),
        "--export-only",
    ]
    if args.hub_psql_command:
        command.extend(["--hub-psql-command", args.hub_psql_command])
    run_command(command, "export sanitized upstream-hub snapshot")
    assert_snapshot_is_sanitized(snapshot)


def copy_snapshot(args: argparse.Namespace, snapshot: Path) -> None:
    remote_parent = str(Path(args.remote_snapshot).parent)
    run_remote(args.ssh_host, f"sudo mkdir -p {shlex.quote(remote_parent)}", "ensure remote snapshot directory")
    remote_tmp = f"/tmp/{Path(args.remote_snapshot).name}.tmp"
    run_command(["scp", str(snapshot), f"{args.ssh_host}:{remote_tmp}"], "copy sanitized snapshot to VPS")
    run_remote(
        args.ssh_host,
        (
            f"sudo install -m 0644 {shlex.quote(remote_tmp)} {shlex.quote(args.remote_snapshot)} "
            f"&& rm -f {shlex.quote(remote_tmp)}"
        ),
        "install sanitized snapshot on VPS",
    )


def refresh_remote_ledger(args: argparse.Namespace) -> None:
    command = [
        "sudo",
        "python3",
        "/var/lib/fluterapi-upstream-rates/refresh_upstream_ledger.py",
        "--local-postgres",
        "--hours",
        str(args.hours),
        "--db",
        args.remote_db,
        "--output",
        args.remote_output,
        "--upstream-hub-snapshot-json",
        args.remote_snapshot,
    ]
    if args.true_loss_alert_endpoint:
        command.extend(["--true-loss-alert-endpoint", args.true_loss_alert_endpoint])
    run_remote(args.ssh_host, " ".join(shlex.quote(part) for part in command), "refresh VPS ledger from sanitized snapshot")


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)
    script_dir = Path(__file__).resolve().parent
    python = sys.executable or "python3"
    snapshot = local_snapshot_path(args)
    snapshot.parent.mkdir(parents=True, exist_ok=True)

    export_snapshot(args, snapshot, script_dir, python)
    print_snapshot_summary(snapshot)
    if args.export_only:
        print(f"snapshot_exported={snapshot}")
        return 0

    copy_snapshot(args, snapshot)
    if not args.skip_remote_refresh:
        refresh_remote_ledger(args)
    print(f"snapshot_synced={snapshot} remote={args.remote_snapshot}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
