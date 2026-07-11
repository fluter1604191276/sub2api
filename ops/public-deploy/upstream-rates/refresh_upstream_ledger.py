#!/usr/bin/env python3
"""Run the safe Fluter upstream-rate ledger refresh pipeline.

This orchestrator only refreshes deterministic/read-only sources and renders
the admin dashboard. It does not run paid image smoke tests and does not modify
sub2api accounts, groups, channels, or pricing.
"""

from __future__ import annotations

import argparse
import sqlite3
import subprocess
import sys
from datetime import datetime, timezone
from pathlib import Path

from discount_profiles import load_discount_profiles, seed_default_discount_profiles


DEFAULT_DB = "/var/lib/fluterapi-upstream-rates/upstream_rates.sqlite"
DEFAULT_OUTPUT = "/www/fluterapi-home/admin/upstream-rates/index.html"
DEFAULT_HUB_COMPOSE_DIR = "/Users/fluter_claw/Desktop/study_project/upstream-hub"


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Refresh Fluter upstream ledger and dashboard")
    parser.add_argument("--db", default=DEFAULT_DB)
    parser.add_argument("--output", default=DEFAULT_OUTPUT)
    parser.add_argument("--hours", type=int, default=24, help="KBQ true-cost audit window")
    parser.add_argument("--ssh-host", default="us-api-vps")
    parser.add_argument(
        "--local-postgres",
        action="store_true",
        help="Run audit against the local VPS Docker PostgreSQL container",
    )
    parser.add_argument(
        "--seed",
        action="store_true",
        help="Also apply the manually curated seed ledger before live refreshes",
    )
    parser.add_argument(
        "--seed-reset",
        action="store_true",
        help="Reset manually curated ledger rows when --seed is used",
    )
    parser.add_argument("--skip-upstream-hub", action="store_true")
    parser.add_argument("--hub-compose-dir", default=DEFAULT_HUB_COMPOSE_DIR)
    parser.add_argument(
        "--upstream-hub-snapshot-json",
        default="",
        help=(
            "Import a sanitized upstream-hub snapshot JSON instead of connecting to the local hub Postgres. "
            "If the file is missing, the hub step is skipped and the rest of the refresh continues."
        ),
    )
    parser.add_argument("--skip-kbq-pricing", action="store_true")
    parser.add_argument("--skip-public-pricing-adapters", action="store_true")
    parser.add_argument(
        "--skip-balance-api-adapters",
        action="store_true",
        help="Deprecated compatibility flag; legacy API balance adapters are skipped unless explicitly included.",
    )
    parser.add_argument(
        "--include-balance-api-adapters",
        action="store_true",
        help="Run legacy API balance adapters explicitly. They are not part of the default hub-ledger pipeline.",
    )
    parser.add_argument("--skip-site-account-snapshot", action="store_true")
    parser.add_argument("--skip-priority-plan-preview", action="store_true")
    parser.add_argument("--skip-kbq-audit", action="store_true")
    parser.add_argument("--skip-true-loss-alert", action="store_true")
    parser.add_argument(
        "--true-loss-alert-endpoint",
        default="",
        help="Optional loopback OpenClaw endpoint for REAL_LOSS alerts; disabled by default.",
    )
    parser.add_argument("--skip-removed-group-cleanup", action="store_true")
    parser.add_argument("--skip-render", action="store_true")
    parser.add_argument(
        "--fail-on-loss",
        action="store_true",
        help="Return non-zero if KBQ true-cost audit finds real loss buckets",
    )
    return parser.parse_args()


def run_step(name: str, command: list[str]) -> None:
    print(f"==> {name}")
    print(" ".join(command))
    proc = subprocess.run(command, text=True, check=False)
    if proc.returncode != 0:
        raise SystemExit(f"{name} failed with exit code {proc.returncode}")


def upstream_hub_import_command(args: argparse.Namespace, script_dir: Path, python: str) -> list[str] | None:
    command = [
        python,
        str(script_dir / "refresh_from_upstream_hub.py"),
        "--db",
        args.db,
        "--update-ledger-page-rates",
    ]
    if args.upstream_hub_snapshot_json:
        snapshot_path = Path(args.upstream_hub_snapshot_json)
        if not snapshot_path.exists():
            print(f"==> import read-only upstream-hub observations")
            print(f"skip: sanitized upstream-hub snapshot not found: {snapshot_path}")
            return None
        command.extend(["--import-json", str(snapshot_path)])
    else:
        compose_dir = Path(args.hub_compose_dir)
        if not compose_dir.exists():
            print(f"==> import read-only upstream-hub observations")
            print(f"skip: upstream-hub compose dir not found: {compose_dir}")
            return None
        command.extend(["--hub-compose-dir", args.hub_compose_dir])
    return command


def write_metadata(db_path: str, key: str, value: str) -> None:
    path = Path(db_path)
    path.parent.mkdir(parents=True, exist_ok=True)
    conn = sqlite3.connect(path)
    with conn:
        conn.execute(
            "create table if not exists metadata (key text primary key, value text not null)"
        )
        conn.execute(
            "insert or replace into metadata(key, value) values (?, ?)",
            (key, value),
        )
    conn.close()


def ensure_discount_profiles(db_path: str) -> None:
    path = Path(db_path)
    path.parent.mkdir(parents=True, exist_ok=True)
    conn = sqlite3.connect(path)
    with conn:
        inserted = seed_default_discount_profiles(conn, overwrite=False)
        write_note = (
            "default discount profiles inserted for missing sites"
            if inserted
            else "discount profiles already present"
        )
        conn.execute(
            "insert or replace into metadata(key, value) values (?, ?)",
            ("discount_profiles_refresh_note", write_note),
        )
    conn.close()


def discount_factor_for_site(db_path: str, site: str, default: str) -> str:
    conn = sqlite3.connect(db_path)
    conn.row_factory = sqlite3.Row
    try:
        profiles = load_discount_profiles(conn)
        profile = profiles.get(site)
        return str(profile.recharge_factor if profile else default)
    finally:
        conn.close()


def main() -> int:
    args = parse_args()
    script_dir = Path(__file__).resolve().parent
    python = sys.executable or "python3"

    if args.seed:
        command = [python, str(script_dir / "seed_upstream_rates.py"), "--db", args.db]
        if args.seed_reset:
            command.append("--reset")
        run_step("seed curated upstream ledger rows", command)

    ensure_discount_profiles(args.db)
    kbq_recharge_factor = discount_factor_for_site(args.db, "xn--vduyey89e.com", "0.9")

    if not args.skip_upstream_hub:
        command = upstream_hub_import_command(args, script_dir, python)
        if command:
            run_step("import read-only upstream-hub observations", command)

    if not args.skip_kbq_pricing:
        run_step(
            "refresh KBQ token model pricing",
            [
                python,
                str(script_dir / "refresh_kbq_token_models.py"),
                "--db",
                args.db,
                "--recharge-factor",
                kbq_recharge_factor,
            ],
        )

    if not args.skip_public_pricing_adapters:
        run_step(
            "refresh public upstream pricing adapters",
            [
                python,
                str(script_dir / "refresh_public_pricing_adapters.py"),
                "--db",
                args.db,
                "--update-ledger-page-rates",
            ],
        )

    if args.include_balance_api_adapters and not args.skip_balance_api_adapters:
        command = [
            python,
            str(script_dir / "refresh_balance_api_adapters.py"),
            "--db",
            args.db,
        ]
        if args.local_postgres:
            command.append("--local-postgres")
        else:
            command.extend(["--ssh-host", args.ssh_host])
        run_step("refresh API-based upstream balance adapters", command)

    if not args.skip_site_account_snapshot:
        command = [
            python,
            str(script_dir / "refresh_site_account_snapshot.py"),
            "--db",
            args.db,
        ]
        if args.local_postgres:
            command.append("--local-postgres")
        else:
            command.extend(["--ssh-host", args.ssh_host])
        run_step("refresh read-only site account multiplier snapshot", command)

    if not args.skip_priority_plan_preview:
        command = [
            python,
            str(script_dir / "plan_account_priority_buckets.py"),
            "--preview-db",
            args.db,
        ]
        if args.local_postgres:
            command.append("--local-postgres")
        else:
            command.extend(["--ssh-host", args.ssh_host])
        run_step("refresh read-only account priority plan preview", command)

    if not args.skip_kbq_audit:
        command = [
            python,
            str(script_dir / "audit_kbq_true_costs.py"),
            "--db",
            args.db,
            "--hours",
            str(args.hours),
            "--recharge-factor",
            kbq_recharge_factor,
        ]
        if args.local_postgres:
            command.append("--local-postgres")
        else:
            command.extend(["--ssh-host", args.ssh_host])
        if args.fail_on_loss:
            command.append("--fail-on-loss")
        run_step("audit KBQ true upstream cost", command)

    if not args.skip_true_loss_alert and not args.skip_kbq_audit:
        command = [
            python,
            str(script_dir / "emit_true_loss_alerts.py"),
            "--db",
            args.db,
            "--fail-soft",
        ]
        if args.true_loss_alert_endpoint:
            command.extend(["--endpoint", args.true_loss_alert_endpoint])
        run_step("emit optional KBQ true-loss alert", command)

    now = datetime.now(timezone.utc).isoformat(timespec="seconds")
    write_metadata(args.db, "last_orchestrated_refresh_at", now)
    write_metadata(
        args.db,
        "last_orchestrated_refresh_note",
        "safe refresh: upstream-hub read-only import from local hub or sanitized snapshot when available, KBQ pricing, public pricing adapters, read-only site account multiplier snapshot, read-only priority plan preview, KBQ true-cost audit, optional dry-run/loopback true-loss alert, removed upstream group cleanup, static dashboard render; no paid image smoke; no production account edits; legacy API balance adapters skipped unless explicitly included",
    )

    if not args.skip_removed_group_cleanup:
        run_step(
            "mark removed upstream groups in independent ledger",
            [
                python,
                str(script_dir / "cleanup_removed_upstream_groups.py"),
                "--db",
                args.db,
                "--apply",
            ],
        )

    if not args.skip_render:
        run_step(
            "render upstream-rate admin dashboard",
            [
                python,
                str(script_dir / "render_upstream_dashboard.py"),
                "--db",
                args.db,
                "--output",
                args.output,
            ],
        )

    print(f"Upstream ledger refresh completed at {now}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
