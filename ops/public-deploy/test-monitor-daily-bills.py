#!/usr/bin/env python3
"""Exercise the migration on a disposable PostgreSQL container, never production."""
import os
from pathlib import Path
import secrets
import subprocess
import time

root = Path(__file__).resolve().parents[2]
name = 'sub2api-daily-bills-test-' + secrets.token_hex(4)
env = os.environ.copy()
env['POSTGRES_PASSWORD'] = secrets.token_urlsafe(24)

def command(*args, **kwargs):
    return subprocess.run(args, check=True, text=True, capture_output=True, env=env, **kwargs).stdout

def sql(text):
    return command('docker', 'exec', '-i', name, 'psql', '-U', 'postgres', '-v', 'ON_ERROR_STOP=1', input=text)

try:
    command('docker', 'run', '-d', '--name', name, '--network', 'none', '-e', 'POSTGRES_PASSWORD', 'postgres:18-alpine')
    for _ in range(90):
        try:
            command('docker', 'exec', name, 'pg_isready', '-U', 'postgres')
            break
        except subprocess.CalledProcessError:
            time.sleep(1)
    sql("""
    CREATE TABLE channel_monitors (id BIGINT PRIMARY KEY, check_mode TEXT);
    CREATE TABLE channel_monitor_histories (
        monitor_id BIGINT, checked_at TIMESTAMPTZ, estimated_cost_usd FLOAT8, status TEXT);
    INSERT INTO channel_monitors VALUES (1, 'probe'), (2, 'quota');
    INSERT INTO channel_monitor_histories VALUES
        (1,'2026-09-10 15:59:59Z',0.25,'operational'),
        (1,'2026-09-10 16:00:00Z',0,'error');
    """)
    migration = (root / 'backend/migrations/234_channel_monitor_daily_bills.sql').read_text()
    sql('BEGIN;\n' + migration + '\nCOMMIT;')
    sql("""
    INSERT INTO channel_monitor_histories VALUES
        (1,'2026-09-10 16:00:01Z',0.5,'degraded'),
        (2,'2026-09-10 16:00:02Z',0,'operational'),
        (1,'2026-09-10 16:00:03Z','NaN','error');
    DO $$ BEGIN
        IF NOT EXISTS (SELECT 1 FROM channel_monitor_daily_bills WHERE bill_date='2026-09-10' AND base_cost_usd=0.25 AND checks=1)
            THEN RAISE EXCEPTION 'midnight attribution failed'; END IF;
        IF NOT EXISTS (SELECT 1 FROM channel_monitor_daily_bills WHERE bill_date='2026-09-11' AND base_cost_usd=0.5 AND checks=4 AND unknown_cost_checks=2)
            THEN RAISE EXCEPTION 'cost/unknown/zero-quota failed'; END IF;
    END $$;
    """)
    sql('BEGIN;\n' + migration + '\nCOMMIT;')
    sql("""
    DELETE FROM channel_monitor_histories;
    DELETE FROM channel_monitors;
    DO $$ BEGIN
        IF (SELECT SUM(checks) FROM channel_monitor_daily_bills) <> 5
            THEN RAISE EXCEPTION 'rerun doubled records or cleanup erased bills'; END IF;
        IF (SELECT SUM(base_cost_usd) FROM channel_monitor_daily_bills) <> 0.75
            THEN RAISE EXCEPTION 'durable estimate mismatch'; END IF;
    END $$;
    BEGIN;
    INSERT INTO channel_monitor_histories VALUES (1,'2026-09-11 16:00:00Z',100,'operational');
    ROLLBACK;
    DO $$ BEGIN
        IF EXISTS (SELECT 1 FROM channel_monitor_daily_bills WHERE bill_date='2026-09-12')
            THEN RAISE EXCEPTION 'rollback retained phantom bill'; END IF;
    END $$;
    """)
    print('PASS: midnight, base cost, unknowns, quota zero, NaN, idempotent migration, deletion retention, transactional rollback')
finally:
    subprocess.run(['docker', 'rm', '-f', '-v', name], capture_output=True)
