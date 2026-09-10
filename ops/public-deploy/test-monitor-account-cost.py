#!/usr/bin/env python3
"""Run the actual reconciliation SQL against a disposable PostgreSQL database."""
import os
from pathlib import Path
import re
import secrets
import subprocess
import time

root = Path(__file__).resolve().parents[2]
name = 'monitor-account-cost-' + secrets.token_hex(4)
env = dict(os.environ, POSTGRES_PASSWORD=secrets.token_urlsafe(24))
def run(*args, **kwargs):
    return subprocess.run(args, check=True, capture_output=True, text=True, env=env, **kwargs)
def sql(query):
    return run('docker','exec','-i',name,'psql','-U','postgres','-v','ON_ERROR_STOP=1',input=query)
try:
    run('docker','run','-d','--name',name,'--network','none','-e','POSTGRES_PASSWORD','postgres:18-alpine')
    for _ in range(90):
        try:
            run('docker','exec',name,'pg_isready','-U','postgres')
            break
        except subprocess.CalledProcessError:
            time.sleep(1)
    sql('''CREATE TABLE channel_monitor_histories(id BIGINT PRIMARY KEY, checked_at TIMESTAMPTZ);
    CREATE TABLE usage_logs(id BIGINT PRIMARY KEY, request_id TEXT, api_key_id BIGINT,
      account_id BIGINT, account_stats_cost NUMERIC, total_cost NUMERIC, account_rate_multiplier NUMERIC,
      created_at TIMESTAMPTZ, user_agent TEXT);''')
    sql('BEGIN;'+(root/'backend/migrations/235_channel_monitor_account_cost.sql').read_text()+'COMMIT;')
    sql('''INSERT INTO channel_monitor_histories VALUES
      (1,'2026-09-10T12:00Z','client:a',7), (2,'2026-09-10T12:00Z','client:b',7),
      (3,'2026-09-10T12:00Z','client:c',7), (4,'2026-09-10T12:00Z','client:d',7);
    INSERT INTO usage_logs VALUES
      (1,'client:a',7,11,2,9,0.1,'2026-09-10T12:00Z','sub2api-channel-monitor/1'),
      (2,'client:b',8,12,2,9,0.1,'2026-09-10T12:00Z','sub2api-channel-monitor/1'),
      (3,'client:c',7,13,NULL,9,0,'2026-09-10T12:00Z','sub2api-channel-monitor/1');''')
    source=(root/'backend/internal/repository/channel_monitor_cost.go').read_text()
    query=re.search(r'`(WITH candidates AS .*?)`',source,re.S).group(1)
    query=query.replace('$1',"'2026-09-10T00:00Z'::timestamptz").replace('$2',"'2026-09-11T00:00Z'::timestamptz")
    sql(query)
    sql('''DO $$ BEGIN
      IF (SELECT account_cost_usd FROM channel_monitor_cost_records WHERE history_id=1) <> 0.2
        THEN RAISE EXCEPTION 'custom cost or multiplier ignored'; END IF;
      IF (SELECT account_cost_usd FROM channel_monitor_cost_records WHERE history_id=3) <> 0
        THEN RAISE EXCEPTION 'zero multiplier lost'; END IF;
      IF EXISTS(SELECT 1 FROM channel_monitor_cost_records WHERE history_id IN (2,4) AND reconciled_at IS NOT NULL)
        THEN RAISE EXCEPTION 'wrong key or absent usage matched'; END IF;
    END $$;
    UPDATE usage_logs SET account_rate_multiplier=100 WHERE id=1;
    INSERT INTO usage_logs VALUES (4,'client:d',7,14,NULL,2,0.25,'2026-09-10T12:01Z','sub2api-channel-monitor/1');''')
    sql(query)
    sql('''DO $$ BEGIN
      IF (SELECT account_cost_usd FROM channel_monitor_cost_records WHERE history_id=1) <> 0.2
        THEN RAISE EXCEPTION 'snapshot changed'; END IF;
      IF (SELECT account_cost_usd FROM channel_monitor_cost_records WHERE history_id=4) <> 0.5
        THEN RAISE EXCEPTION 'delayed usage not reconciled'; END IF;
    END $$;
    DELETE FROM channel_monitor_histories; DELETE FROM usage_logs;
    DO $$ BEGIN
      IF (SELECT sum(account_cost_usd) FROM channel_monitor_cost_records) <> 0.7
        THEN RAISE EXCEPTION 'cleanup lost cost evidence'; END IF;
    END $$;''')
    print('PASS: account pricing override, rate snapshot, zero rate, key isolation, delayed usage, idempotency, retention')
finally:
    subprocess.run(['docker','rm','-f','-v',name],capture_output=True)
