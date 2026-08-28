#!/usr/bin/env python3
"""Preflight KBQ mappings against live upstream and production billing prices.

The production PostgreSQL access in this script is read-only.  It exports only
account IDs/names, model mappings, group rates, user rate overrides, channel
status, and channel pricing.  Full credentials, API keys, tokens, cookies, and
request bodies are never selected.  Results are written to the independent
upstream-rates SQLite ledger.
"""

from __future__ import annotations

import argparse
import json
import shlex
import sqlite3
import subprocess
import urllib.request
from dataclasses import dataclass
from datetime import datetime, timezone
from decimal import Decimal
from pathlib import Path
from typing import Any


DEFAULT_DB = "/var/lib/fluterapi-upstream-rates/upstream_rates.sqlite"
DEFAULT_COMPOSE_DIR = "/www/sub2api"
DEFAULT_KBQ_PRICING_URL = "https://xn--vduyey89e.com/api/pricing"
DEFAULT_KBQ_RECHARGE_FACTOR = Decimal("0.9")
DEFAULT_KBQ_WEB_SEARCH_PRICE_PER_CALL = Decimal("0.01")
DEFAULT_SITE_WEB_SEARCH_PRICE_PER_CALL = Decimal("0.01")
DEFAULT_MODEL_PRICING_PATH = "/app/resources/model-pricing/model_prices_and_context_window.json"
MILLION = Decimal("1000000")
LOSS_TOLERANCE = Decimal("1.000001")
BLOCKING_STATUSES = {
    "REAL_LOSS",
    "NO_UPSTREAM_PRICE",
    "NO_LOCAL_PRICE",
    "NO_USER_PRICE",
    "AMBIGUOUS_GROUP_RATIO",
    "CHANNEL_PRICING_AMBIGUOUS",
    "CHANNEL_BINDING_AMBIGUOUS",
    "CHANNEL_MAPPING_AMBIGUOUS",
    "INVALID_BILLING_MODEL_SOURCE",
    "TOOL_FEE_UNCOVERED",
    "TOOL_FEE_UNCOVERED_LOSS",
    "POOL_PRICE_UNDERCUT",
}

# Keep this aligned with BillingService.initFallbackPricing/getFallbackPricing.
# Values are the site's base prices per one million tokens, before user/group
# multipliers.  Keep these aligned with BillingService.initFallbackPricing and
# getFallbackPricing.  Unknown models deliberately have no fallback and fail
# closed.
FALLBACK_PRICES_PER_MILLION = {
    "claude-opus-4.5": ("5", "25", "0.5", "6.25"),
    "claude-opus-4.6": ("5", "25", "0.5", "6.25"),
    "claude-opus-4.7": ("5", "25", "0.5", "6.25"),
    "claude-sonnet-4": ("3", "15", "0.3", "3.75"),
    "claude-3-5-sonnet": ("3", "15", "0.3", "3.75"),
    "claude-3-5-haiku": ("1", "5", "0.1", "1.25"),
    "claude-3-opus": ("15", "75", "1.5", "18.75"),
    "claude-3-haiku": ("0.25", "1.25", "0.03", "0.3"),
    "deepseek-v4-flash": ("0.14", "0.28", "0.0028", None),
    "deepseek-v4-pro": ("0.435", "0.87", "0.003625", None),
    "kimi-k2.6": ("0.95", "4", "0.15", None),
    "kimi-for-coding": ("0.95", "4", "0.15", None),
    "kimi-k2.5": ("0.60", "3", "0.098", None),
    "kimi-k2-thinking": ("0.56", "2.24", "0.14", None),
    "kimi-k2": ("0.56", "2.24", "0.14", None),
}


SCHEMA = """
create table if not exists kbq_configuration_audit_runs (
  id integer primary key autoincrement,
  observed_at text not null,
  pricing_version text not null,
  account_count integer not null,
  mapping_count integer not null,
  active_mapping_count integer not null default 0,
  ok_count integer not null,
  blocking_count integer not null,
  active_blocking_count integer not null default 0,
  dormant_blocking_count integer not null default 0,
  channel_pricing_ambiguous_count integer not null default 0,
  channel_binding_ambiguous_count integer not null default 0,
  channel_mapping_ambiguous_count integer not null default 0,
  invalid_billing_model_source_count integer not null default 0,
  pool_underpriced_count integer not null default 0,
  real_loss_count integer not null,
  ambiguous_group_ratio_count integer not null,
  missing_upstream_price_count integer not null,
  missing_local_price_count integer not null,
  missing_user_price_count integer not null,
  tool_fee_uncovered_count integer not null,
  tool_fee_unknown_count integer not null,
  source text not null,
  note text not null
);

create table if not exists kbq_configuration_audit_rows (
  id integer primary key autoincrement,
  run_id integer not null,
  status text not null,
  account_id integer not null,
  account_name text not null,
  account_status text not null,
  schedulable integer not null,
  group_id integer,
  group_name text not null,
  group_status text not null,
  requested_model text not null,
  channel_mapped_model text not null default '',
  upstream_model text not null,
  billing_model text not null default '',
  billing_model_source text not null default '',
  pricing_status text not null,
  kbq_group_key text not null,
  kbq_group_ratio real,
  group_ratio_source text not null,
  group_rate_multiplier real,
  minimum_user_multiplier real,
  channel_id integer,
  channel_name text not null,
  channel_status text not null,
  local_pricing_source text not null,
  upstream_input_price real,
  upstream_output_price real,
  upstream_cache_read_price real,
  upstream_cache_write_price real,
  local_input_price real,
  local_output_price real,
  local_cache_read_price real,
  local_cache_write_price real,
  site_input_price real,
  site_output_price real,
  site_cache_read_price real,
  site_cache_write_price real,
  minimum_break_even_multiplier real,
  minimum_break_even_multiplier_priority real,
  minimum_break_even_multiplier_flex real,
  minimum_safe_user_multiplier real,
  pool_account_count integer not null default 0,
  pool_minimum_safe_user_multiplier real,
  pool_price_source_account_id integer,
  pool_price_source_account_name text not null default '',
  pool_status text not null default 'NOT_EVALUATED',
  checked_service_tiers text not null default 'default',
  group_web_search_price_per_call real,
  upstream_web_search_price_per_call real,
  site_web_search_price_per_call real,
  tool_fee_status text not null,
  note text not null,
  foreign key(run_id) references kbq_configuration_audit_runs(id)
);

create table if not exists metadata (
  key text primary key,
  value text not null
);
"""


@dataclass(frozen=True)
class TokenPrices:
    input: Decimal | None = None
    output: Decimal | None = None
    cache_read: Decimal | None = None
    cache_write: Decimal | None = None


@dataclass(frozen=True)
class BasePricingTiers:
    default: TokenPrices
    priority_explicit: TokenPrices | None = None


@dataclass(frozen=True)
class BillingModelContext:
    requested_model: str
    channel_mapped_model: str
    upstream_model: str
    billing_model: str
    billing_model_source: str
    error_status: str = ""


def has_column(conn: sqlite3.Connection, table_name: str, column_name: str) -> bool:
    return any(row[1] == column_name for row in conn.execute(f"pragma table_info({table_name})"))


def ensure_ledger_schema(conn: sqlite3.Connection) -> None:
    conn.executescript(SCHEMA)
    additions = {
        "kbq_configuration_audit_runs": [
            ("active_mapping_count", "integer not null default 0"),
            ("active_blocking_count", "integer not null default 0"),
            ("dormant_blocking_count", "integer not null default 0"),
            ("channel_pricing_ambiguous_count", "integer not null default 0"),
            ("channel_binding_ambiguous_count", "integer not null default 0"),
            ("channel_mapping_ambiguous_count", "integer not null default 0"),
            ("invalid_billing_model_source_count", "integer not null default 0"),
            ("pool_underpriced_count", "integer not null default 0"),
        ],
        "kbq_configuration_audit_rows": [
            ("channel_mapped_model", "text not null default ''"),
            ("billing_model", "text not null default ''"),
            ("billing_model_source", "text not null default ''"),
            ("minimum_break_even_multiplier_priority", "real"),
            ("minimum_break_even_multiplier_flex", "real"),
            ("minimum_safe_user_multiplier", "real"),
            ("pool_account_count", "integer not null default 0"),
            ("pool_minimum_safe_user_multiplier", "real"),
            ("pool_price_source_account_id", "integer"),
            ("pool_price_source_account_name", "text not null default ''"),
            ("pool_status", "text not null default 'NOT_EVALUATED'"),
            ("checked_service_tiers", "text not null default 'default'"),
            ("upstream_web_search_price_per_call", "real"),
            ("site_web_search_price_per_call", "real"),
        ],
    }
    for table_name, columns in additions.items():
        for column_name, definition in columns:
            if not has_column(conn, table_name, column_name):
                conn.execute(f"alter table {table_name} add column {column_name} {definition}")


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Preflight KBQ mappings against current production billing prices"
    )
    parser.add_argument("--db", default=DEFAULT_DB)
    parser.add_argument("--ssh-host", default="fluterapi-prod")
    parser.add_argument("--compose-dir", default=DEFAULT_COMPOSE_DIR)
    parser.add_argument("--pricing-url", default=DEFAULT_KBQ_PRICING_URL)
    parser.add_argument("--timeout", type=int, default=30)
    parser.add_argument(
        "--recharge-factor",
        default=str(DEFAULT_KBQ_RECHARGE_FACTOR),
        help="Effective KBQ purchase-cost factor, e.g. 0.9",
    )
    parser.add_argument(
        "--kbq-web-search-price-per-call",
        default=str(DEFAULT_KBQ_WEB_SEARCH_PRICE_PER_CALL),
        help="Verified KBQ DeepSeek web_search list price per call, before recharge factor",
    )
    parser.add_argument(
        "--site-default-web-search-price-per-call",
        default=str(DEFAULT_SITE_WEB_SEARCH_PRICE_PER_CALL),
        help=(
            "Configured site default when groups.web_search_price_per_call is null; "
            "reported for review only because the current gateway does not add this fee to actual_cost"
        ),
    )
    parser.add_argument(
        "--model-pricing-json",
        default="",
        help="Optional local copy of the running image's LiteLLM model-price JSON",
    )
    parser.add_argument(
        "--local-postgres",
        action="store_true",
        help="Run Docker commands locally instead of through SSH",
    )
    parser.add_argument(
        "--fail-on-loss",
        action="store_true",
        help="Exit 2 when a currently routable mapping has a blocking pricing risk",
    )
    return parser.parse_args(argv)


def decimal_or_none(value: Any) -> Decimal | None:
    if value is None or value == "":
        return None
    return Decimal(str(value))


def to_float(value: Decimal | None) -> float | None:
    return None if value is None else float(value)


def run_remote_or_local(
    args: argparse.Namespace,
    command: str,
    input_text: str | None = None,
) -> subprocess.CompletedProcess[str]:
    if args.local_postgres:
        return subprocess.run(
            command,
            input=input_text,
            text=True,
            shell=True,
            capture_output=True,
            check=False,
        )
    return subprocess.run(
        ["ssh", "-T", args.ssh_host, command],
        input=input_text,
        text=True,
        capture_output=True,
        check=False,
    )


def compose_exec(args: argparse.Namespace, service: str, command: str) -> str:
    shell_command = (
        f"cd {shlex.quote(args.compose_dir)} && "
        f"docker compose exec -T {shlex.quote(service)} {command}"
    )
    proc = run_remote_or_local(args, shell_command)
    if proc.returncode != 0:
        raise SystemExit(f"Docker read failed: {proc.stderr.strip()}")
    return proc.stdout


def run_psql(args: argparse.Namespace, sql: str) -> str:
    shell_command = (
        f"cd {shlex.quote(args.compose_dir)} && "
        "docker compose exec -T postgres psql -U sub2api -d sub2api -At"
    )
    proc = run_remote_or_local(args, shell_command, sql)
    if proc.returncode != 0:
        raise SystemExit(f"PostgreSQL read failed: {proc.stderr.strip()}")
    return proc.stdout


def fetch_kbq_pricing(args: argparse.Namespace) -> dict[str, Any]:
    request = urllib.request.Request(
        args.pricing_url,
        headers={
            "Accept": "application/json",
            "User-Agent": "fluter-kbq-configuration-audit/1.0",
        },
    )
    with urllib.request.urlopen(request, timeout=args.timeout) as response:
        payload = json.loads(response.read().decode("utf-8"))
    if not payload.get("success") or not isinstance(payload.get("data"), list):
        raise SystemExit("Unexpected KBQ pricing response")
    return payload


def load_model_pricing_document(args: argparse.Namespace) -> dict[str, Any]:
    if args.model_pricing_json:
        return json.loads(Path(args.model_pricing_json).read_text(encoding="utf-8"))
    raw = compose_exec(
        args,
        "sub2api",
        f"cat {shlex.quote(DEFAULT_MODEL_PRICING_PATH)}",
    )
    return json.loads(raw)


def load_production_configuration(
    args: argparse.Namespace,
) -> tuple[list[dict[str, Any]], list[dict[str, Any]], dict[str, Any]]:
    account_sql = """
copy (
  select coalesce(jsonb_agg(row_to_json(t) order by account_id, group_id), '[]'::jsonb)
  from (
    select
      a.id as account_id,
      a.name as account_name,
      coalesce(a.platform, '') as account_platform,
      coalesce(a.type, '') as account_type,
      coalesce(a.status, '') as account_status,
      coalesce(a.schedulable, false) as schedulable,
      coalesce(a.extra->>'kbq_group_key', a.extra->>'kbq_group', '') as explicit_kbq_group,
      case
        when jsonb_typeof(a.credentials->'model_mapping') = 'object'
          then a.credentials->'model_mapping'
        else '{}'::jsonb
      end as model_mapping,
      g.id as group_id,
      g.name as group_name,
      coalesce(g.platform, '') as group_platform,
      coalesce(g.status, '') as group_status,
      g.rate_multiplier::text as group_rate_multiplier,
      g.web_search_price_per_call::text as group_web_search_price_per_call,
      least(
        g.rate_multiplier,
        coalesce(
          (select min(ugr.rate_multiplier)
           from user_group_rate_multipliers ugr
           where ugr.group_id = g.id and ugr.rate_multiplier is not null),
          g.rate_multiplier
        )
      )::text as minimum_user_multiplier,
      coalesce(cb.channel_binding_count, 0) as channel_binding_count,
      cb.channel_id,
      coalesce(cb.channel_name, '') as channel_name,
      coalesce(cb.channel_status, 'missing') as channel_status,
      coalesce(cb.billing_model_source, '') as billing_model_source,
      coalesce(cb.channel_model_mapping, '{}'::jsonb) as channel_model_mapping
    from accounts a
    join account_groups ag on ag.account_id = a.id
    join groups g on g.id = ag.group_id and g.deleted_at is null
    left join lateral (
      select
        count(*)::int as channel_binding_count,
        (jsonb_agg(c.id order by c.id)->>0)::bigint as channel_id,
        jsonb_agg(c.name order by c.id)->>0 as channel_name,
        jsonb_agg(c.status order by c.id)->>0 as channel_status,
        jsonb_agg(coalesce(c.billing_model_source, '') order by c.id)->>0 as billing_model_source,
        jsonb_agg(coalesce(c.model_mapping, '{}'::jsonb) order by c.id)->0 as channel_model_mapping
      from channel_groups cg
      join channels c on c.id = cg.channel_id
      where cg.group_id = g.id
    ) cb on true
    where a.deleted_at is null
      and a.credentials->>'base_url' ilike '%xn--vduyey89e%'
  ) t
) to stdout;
"""
    pricing_sql = """
copy (
  select coalesce(jsonb_agg(row_to_json(t) order by channel_id, pricing_id), '[]'::jsonb)
  from (
    select
      c.id as channel_id,
      c.name as channel_name,
      c.status as channel_status,
      cg.group_id,
      cmp.id as pricing_id,
      coalesce(cmp.platform, '') as platform,
      coalesce(cmp.models, '[]'::jsonb) as models,
      coalesce(cmp.billing_mode, 'token') as billing_mode,
      (cmp.input_price * 1000000)::text as input_price,
      (cmp.output_price * 1000000)::text as output_price,
      (cmp.cache_write_price * 1000000)::text as cache_write_price,
      (cmp.cache_read_price * 1000000)::text as cache_read_price,
      coalesce(
        (select jsonb_agg(
           jsonb_build_object(
             'min_tokens', i.min_tokens,
             'max_tokens', i.max_tokens,
             'input_price', i.input_price * 1000000,
             'output_price', i.output_price * 1000000,
             'cache_write_price', i.cache_write_price * 1000000,
             'cache_read_price', i.cache_read_price * 1000000
           ) order by i.sort_order, i.id
         ) from channel_pricing_intervals i where i.pricing_id = cmp.id),
        '[]'::jsonb
      ) as intervals
    from channels c
    join channel_groups cg on cg.channel_id = c.id
    join channel_model_pricing cmp on cmp.channel_id = c.id
    where cg.group_id in (
      select distinct ag.group_id
      from account_groups ag
      join accounts a on a.id = ag.account_id
      where a.deleted_at is null
        and a.credentials->>'base_url' ilike '%xn--vduyey89e%'
    )
  ) t
) to stdout;
"""
    fast_policy_sql = """
select coalesce(
  (select value from settings where key = 'openai_fast_policy_settings'),
  '{"rules":[]}'
);
"""
    accounts = json.loads(run_psql(args, account_sql).strip() or "[]")
    pricing_rows = json.loads(run_psql(args, pricing_sql).strip() or "[]")
    fast_policy_raw = run_psql(args, fast_policy_sql).strip() or '{"rules":[]}'
    try:
        fast_policy = json.loads(fast_policy_raw)
    except json.JSONDecodeError:
        # Production falls back to an empty policy when this setting is
        # malformed. Empty rules expose every known tier, which is also the
        # conservative pricing-audit interpretation.
        fast_policy = {"rules": []}
    if not isinstance(fast_policy, dict) or not isinstance(fast_policy.get("rules"), list):
        fast_policy = {"rules": []}
    return accounts, pricing_rows, fast_policy


def resolve_group_ratio(
    pricing: dict[str, Any],
    item: dict[str, Any],
    explicit_group: str = "",
) -> tuple[Decimal | None, str, str]:
    ratios = pricing.get("group_ratio") or {}
    enabled = [
        str(key)
        for key in item.get("enable_groups") or []
        if decimal_or_none(ratios.get(str(key))) is not None
    ]
    explicit = str(explicit_group or "").strip()
    if explicit:
        value = decimal_or_none(ratios.get(explicit))
        if value is not None and (not enabled or explicit in enabled):
            return value, explicit, "account_metadata"
        return None, "", "explicit_group_not_enabled"
    if len(enabled) == 1:
        key = enabled[0]
        return decimal_or_none(ratios.get(key)), key, "single_enabled_group"
    return None, "", "ambiguous"


def upstream_prices(
    item: dict[str, Any],
    group_ratio: Decimal,
    recharge_factor: Decimal,
) -> TokenPrices:
    model_ratio = decimal_or_none(item.get("model_ratio"))
    if model_ratio is None:
        return TokenPrices()
    completion_ratio = decimal_or_none(item.get("completion_ratio")) or Decimal("1")
    input_price = model_ratio * group_ratio * Decimal("2") * recharge_factor
    cache_read_ratio = decimal_or_none(item.get("cache_ratio"))
    cache_write_ratio = decimal_or_none(item.get("create_cache_ratio"))
    return TokenPrices(
        input=input_price,
        output=input_price * completion_ratio,
        cache_read=input_price * cache_read_ratio if cache_read_ratio is not None else None,
        cache_write=input_price * cache_write_ratio if cache_write_ratio is not None else None,
    )


def scaled_prices(prices: TokenPrices, multiplier: Decimal) -> TokenPrices:
    return TokenPrices(
        input=prices.input * multiplier if prices.input is not None else None,
        output=prices.output * multiplier if prices.output is not None else None,
        cache_read=prices.cache_read * multiplier if prices.cache_read is not None else None,
        cache_write=prices.cache_write * multiplier if prices.cache_write is not None else None,
    )


def has_explicit_priority_prices(prices: TokenPrices | None) -> bool:
    if prices is None:
        return False
    return any(
        value is not None and value > 0
        for value in (prices.input, prices.output, prices.cache_read)
    )


def effective_priority_prices(prices: BasePricingTiers) -> TokenPrices:
    """Mirror BillingService.computeTokenBreakdown priority semantics."""
    explicit = prices.priority_explicit
    if not has_explicit_priority_prices(explicit):
        return scaled_prices(prices.default, Decimal("2"))
    assert explicit is not None
    return TokenPrices(
        input=explicit.input if explicit.input is not None and explicit.input > 0 else prices.default.input,
        output=explicit.output if explicit.output is not None and explicit.output > 0 else prices.default.output,
        cache_read=(
            explicit.cache_read
            if explicit.cache_read is not None and explicit.cache_read > 0
            else prices.default.cache_read
        ),
        # Production has no dedicated priority cache-write field. Once any
        # explicit priority price is present, cache creation stays at default.
        cache_write=prices.default.cache_write,
    )


def flex_prices(prices: BasePricingTiers) -> TokenPrices:
    return scaled_prices(prices.default, Decimal("0.5"))


def model_matches_policy_pattern(pattern: str, model: str) -> bool:
    if pattern == model:
        return True
    return pattern.endswith("*") and model.startswith(pattern[:-1])


def fast_policy_scope_matches(scope: str, account_type: str, platform: str) -> bool:
    normalized_scope = str(scope or "").strip().lower()
    normalized_type = str(account_type or "").strip().lower()
    is_oauth = normalized_type in {"oauth", "setup_token"}
    is_bedrock = platform == "anthropic" and normalized_type == "bedrock"
    if normalized_scope == "oauth":
        return is_oauth
    if normalized_scope == "apikey":
        return not is_oauth and not is_bedrock
    if normalized_scope == "bedrock":
        return is_bedrock
    # Match production's fail-open behavior for empty/unknown scopes.
    return True


def fast_policy_action(
    account: dict[str, Any],
    model: str,
    service_tier: str,
    settings: dict[str, Any] | None,
) -> str:
    platform = str(account.get("account_platform") or account.get("group_platform") or "").strip().lower()
    account_type = str(account.get("account_type") or "").strip().lower()
    for raw_rule in (settings or {}).get("rules") or []:
        if not isinstance(raw_rule, dict):
            continue
        if not fast_policy_scope_matches(raw_rule.get("scope", ""), account_type, platform):
            continue
        rule_tier = str(raw_rule.get("service_tier") or "").strip().lower()
        if rule_tier not in {"", "all", service_tier}:
            continue
        whitelist = raw_rule.get("model_whitelist") or []
        if whitelist and not any(
            model_matches_policy_pattern(str(pattern), model) for pattern in whitelist
        ):
            return str(raw_rule.get("fallback_action") or "pass").strip().lower()
        return str(raw_rule.get("action") or "pass").strip().lower()
    return "pass"


def reachable_service_tiers(
    account: dict[str, Any],
    model: str = "",
    fast_policy_settings: dict[str, Any] | None = None,
) -> tuple[str, ...]:
    """Return billing tiers that clients can reach through this group platform."""
    platform = str(account.get("group_platform") or "").strip().lower()
    if platform == "openai":
        reachable = ["default"]
        for requested_tier in ("priority", "flex", "auto", "default", "scale"):
            action = fast_policy_action(
                account,
                model,
                requested_tier,
                fast_policy_settings,
            )
            if action in {"block", "filter"}:
                continue
            if action == "force_priority":
                effective_tier = "priority"
            elif requested_tier in {"auto", "default", "scale"}:
                effective_tier = "default"
            else:
                effective_tier = requested_tier
            if effective_tier not in reachable:
                reachable.append(effective_tier)
        return tuple(reachable)
    if platform == "anthropic":
        # Anthropic Messages has no service_tier field, but fast-mode beta is
        # translated to priority by the OpenAI-compatible gateway path.
        return ("default", "priority")
    return ("default",)


def dimension_ratios(numerator: TokenPrices, denominator: TokenPrices) -> list[Decimal]:
    ratios: list[Decimal] = []
    for key in ("input", "output", "cache_read", "cache_write"):
        top = getattr(numerator, key)
        bottom = getattr(denominator, key)
        if top is not None and bottom is not None and bottom > 0:
            ratios.append(top / bottom)
    return ratios


def missing_positive_price_dimensions(upstream: TokenPrices, local: TokenPrices) -> list[str]:
    missing: list[str] = []
    for key in ("input", "output", "cache_read", "cache_write"):
        upstream_value = getattr(upstream, key)
        local_value = getattr(local, key)
        if upstream_value is not None and upstream_value > 0 and (local_value is None or local_value <= 0):
            missing.append(key)
    return missing


def mapping_is_currently_routable(row: dict[str, Any]) -> bool:
    return (
        str(row.get("account_status") or "").lower() == "active"
        and bool(row.get("schedulable"))
        and str(row.get("group_status") or "").lower() == "active"
    )


def base_prices_for_model(model: str, pricing_document: dict[str, Any]) -> tuple[BasePricingTiers | None, str]:
    key = str(model or "").lower()
    item = pricing_document.get(key)
    if isinstance(item, dict) and not item.get("token_pricing_absent"):
        prices = TokenPrices(
            input=(decimal_or_none(item.get("input_cost_per_token")) or Decimal("0")) * MILLION,
            output=(decimal_or_none(item.get("output_cost_per_token")) or Decimal("0")) * MILLION,
            cache_read=(decimal_or_none(item.get("cache_read_input_token_cost")) or Decimal("0")) * MILLION,
            cache_write=(decimal_or_none(item.get("cache_creation_input_token_cost")) or Decimal("0")) * MILLION,
        )
        if prices.input > 0 or prices.output > 0 or prices.cache_read > 0 or prices.cache_write > 0:
            priority = TokenPrices(
                input=(decimal_or_none(item.get("input_cost_per_token_priority")) or Decimal("0")) * MILLION,
                output=(decimal_or_none(item.get("output_cost_per_token_priority")) or Decimal("0")) * MILLION,
                cache_read=(decimal_or_none(item.get("cache_read_input_token_cost_priority")) or Decimal("0")) * MILLION,
            )
            return BasePricingTiers(
                default=prices,
                priority_explicit=priority if has_explicit_priority_prices(priority) else None,
            ), "litellm"
    fallback_key = fallback_model_key(key)
    fallback = FALLBACK_PRICES_PER_MILLION.get(fallback_key)
    if fallback:
        return BasePricingTiers(
            default=TokenPrices(*(decimal_or_none(value) for value in fallback))
        ), "fallback"
    return None, "unavailable"


def fallback_model_key(model: str) -> str:
    """Mirror BillingService.getFallbackPricing's known family fallback."""
    key = str(model or "").lower()
    if "deepseek-v4-flash" in key:
        return "deepseek-v4-flash"
    if "deepseek-v4-pro" in key:
        return "deepseek-v4-pro"
    if "deepseek-chat" in key or "deepseek-reasoner" in key:
        return "deepseek-v4-flash"
    if "opus" in key:
        if "4.7" in key or "4-7" in key:
            return "claude-opus-4.7"
        if "4.6" in key or "4-6" in key:
            return "claude-opus-4.6"
        if "4.5" in key or "4-5" in key:
            return "claude-opus-4.5"
        return "claude-3-opus"
    if "sonnet" in key:
        if "4" in key and "3" not in key:
            return "claude-sonnet-4"
        return "claude-3-5-sonnet"
    if "haiku" in key:
        if "3-5" in key or "3.5" in key:
            return "claude-3-5-haiku"
        return "claude-3-haiku"
    if "claude" in key:
        return "claude-sonnet-4"
    if "kimi-for-coding" in key:
        return "kimi-for-coding"
    if "kimi-k2.6" in key or "kimi-k2-6" in key:
        return "kimi-k2.6"
    if "kimi-k2.5" in key or "kimi-k2-5" in key:
        return "kimi-k2.5"
    if "kimi-k2-thinking" in key:
        return "kimi-k2-thinking"
    if "kimi-k2" in key or "kimi/k2" in key:
        return "kimi-k2"
    return key


def model_pattern_matches(pattern: str, model: str) -> bool:
    pattern_lower = str(pattern or "").strip().lower()
    model_lower = str(model or "").strip().lower()
    if pattern_lower.endswith("*"):
        return model_lower.startswith(pattern_lower[:-1])
    return pattern_lower == model_lower


def resolve_channel_mapped_model(
    mapping: Any,
    platform: str,
    requested_model: str,
) -> tuple[str, str]:
    if not isinstance(mapping, dict):
        return requested_model, ""
    platform_mapping = mapping.get(platform)
    if not isinstance(platform_mapping, dict):
        return requested_model, ""

    requested_lower = requested_model.lower()
    for pattern, target in platform_mapping.items():
        if str(pattern).lower() == requested_lower:
            return str(target), ""

    wildcard_targets = {
        str(target)
        for pattern, target in platform_mapping.items()
        if str(pattern).endswith("*") and model_pattern_matches(str(pattern), requested_model)
    }
    if len(wildcard_targets) > 1:
        return requested_model, "CHANNEL_MAPPING_AMBIGUOUS"
    if wildcard_targets:
        return next(iter(wildcard_targets)), ""
    return requested_model, ""


def resolve_billing_model_context(
    account: dict[str, Any],
    requested_model: str,
    upstream_model: str,
) -> BillingModelContext:
    binding_count = int(account.get("channel_binding_count") or 0)
    if binding_count > 1:
        return BillingModelContext(
            requested_model=requested_model,
            channel_mapped_model=requested_model,
            upstream_model=upstream_model,
            billing_model="",
            billing_model_source="",
            error_status="CHANNEL_BINDING_AMBIGUOUS",
        )

    channel_active = (
        binding_count == 1
        and str(account.get("channel_status") or "").lower() == "active"
    )
    source = str(account.get("billing_model_source") or "").strip().lower()
    if not channel_active:
        return BillingModelContext(
            requested_model=requested_model,
            channel_mapped_model=requested_model,
            upstream_model=upstream_model,
            billing_model=upstream_model or requested_model,
            billing_model_source="upstream",
        )

    if not source:
        source = "channel_mapped"
    if source not in {"requested", "upstream", "channel_mapped"}:
        return BillingModelContext(
            requested_model=requested_model,
            channel_mapped_model=requested_model,
            upstream_model=upstream_model,
            billing_model="",
            billing_model_source=source,
            error_status="INVALID_BILLING_MODEL_SOURCE",
        )

    mapped, mapping_error = resolve_channel_mapped_model(
        account.get("channel_model_mapping"),
        str(account.get("group_platform") or ""),
        requested_model,
    )
    if mapping_error:
        return BillingModelContext(
            requested_model=requested_model,
            channel_mapped_model=mapped,
            upstream_model=upstream_model,
            billing_model="",
            billing_model_source=source,
            error_status=mapping_error,
        )
    billing_model = {
        "requested": requested_model,
        "channel_mapped": mapped,
        "upstream": upstream_model or mapped,
    }[source]
    return BillingModelContext(
        requested_model=requested_model,
        channel_mapped_model=mapped,
        upstream_model=upstream_model,
        billing_model=billing_model,
        billing_model_source=source,
    )


def matching_channel_pricing(
    account: dict[str, Any],
    requested_model: str,
    pricing_rows: list[dict[str, Any]],
) -> dict[str, Any] | None:
    row, status = resolve_channel_pricing(account, requested_model, pricing_rows)
    return row if status == "matched" else None


def channel_pricing_signature(row: dict[str, Any]) -> str:
    """Return the effective-price identity used to detect conflicting rows."""
    return json.dumps(
        {
            "billing_mode": row.get("billing_mode") or "token",
            "input_price": row.get("input_price"),
            "output_price": row.get("output_price"),
            "cache_read_price": row.get("cache_read_price"),
            "cache_write_price": row.get("cache_write_price"),
            "intervals": row.get("intervals") or [],
        },
        sort_keys=True,
        separators=(",", ":"),
        default=str,
    )


def resolve_channel_pricing(
    account: dict[str, Any],
    requested_model: str,
    pricing_rows: list[dict[str, Any]],
) -> tuple[dict[str, Any] | None, str]:
    """Mirror production precedence while rejecting materially conflicting rows."""
    if str(account.get("channel_status") or "") != "active":
        return None, "inactive"
    exact: list[dict[str, Any]] = []
    wildcard: list[dict[str, Any]] = []
    for row in pricing_rows:
        if row.get("group_id") != account.get("group_id"):
            continue
        if (
            account.get("channel_id") is not None
            and row.get("channel_id") is not None
            and row.get("channel_id") != account.get("channel_id")
        ):
            continue
        if str(row.get("platform") or "") != str(account.get("group_platform") or ""):
            continue
        for pattern in row.get("models") or []:
            if str(pattern).lower() == requested_model.lower():
                exact.append(row)
                break
            if str(pattern).endswith("*") and model_pattern_matches(str(pattern), requested_model):
                wildcard.append(row)
                break
    candidates = exact if exact else wildcard
    if not candidates:
        return None, "missing"
    signatures = {channel_pricing_signature(row) for row in candidates}
    if len(signatures) > 1:
        return None, "ambiguous"
    return candidates[0], "matched"


def minimum_price(values: list[Decimal | None], fallback: Decimal | None) -> Decimal | None:
    usable = [value for value in values if value is not None]
    if fallback is not None:
        usable.append(fallback)
    return min(usable) if usable else None


def apply_channel_pricing(
    base: BasePricingTiers | None,
    pricing_row: dict[str, Any] | None,
) -> tuple[BasePricingTiers | None, str]:
    if pricing_row is None:
        return base, "base"
    if str(pricing_row.get("billing_mode") or "token") != "token":
        return None, "channel_non_token"
    flat = TokenPrices(
        input=decimal_or_none(pricing_row.get("input_price")),
        output=decimal_or_none(pricing_row.get("output_price")),
        cache_read=decimal_or_none(pricing_row.get("cache_read_price")),
        cache_write=decimal_or_none(pricing_row.get("cache_write_price")),
    )
    base = base or BasePricingTiers(default=TokenPrices())
    flat_applied = TokenPrices(
        input=flat.input if flat.input is not None else base.default.input,
        output=flat.output if flat.output is not None else base.default.output,
        cache_read=flat.cache_read if flat.cache_read is not None else base.default.cache_read,
        cache_write=flat.cache_write if flat.cache_write is not None else base.default.cache_write,
    )
    priority_base = base.priority_explicit or TokenPrices()
    priority_flat = TokenPrices(
        input=flat.input if flat.input is not None else priority_base.input,
        output=flat.output if flat.output is not None else priority_base.output,
        cache_read=flat.cache_read if flat.cache_read is not None else priority_base.cache_read,
        cache_write=priority_base.cache_write,
    )
    flat_tiers = BasePricingTiers(
        default=flat_applied,
        priority_explicit=(
            priority_flat if has_explicit_priority_prices(priority_flat) else None
        ),
    )
    intervals = pricing_row.get("intervals") or []
    valid_intervals = [
        item
        for item in intervals
        if any(item.get(key) is not None for key in ("input_price", "output_price", "cache_read_price", "cache_write_price"))
    ]
    if not valid_intervals:
        return flat_tiers, "channel_flat"

    # Preflight must not miss a loss that only occurs in one context interval.
    # Use the lowest effective price of every configured interval per dimension.
    # This is intentionally conservative; it may warn on mixed sparse intervals,
    # but it cannot silently bless a cheaper interval.
    def interval_dimension_price(item: dict[str, Any], key: str) -> Decimal:
        # A matched production interval is converted into a fresh ModelPricing.
        # Any omitted token dimension therefore bills at zero, not at the base
        # price. Preserve that behavior here so sparse intervals fail closed.
        return decimal_or_none(item.get(key)) or Decimal("0")

    conservative = TokenPrices(
        input=minimum_price(
            [interval_dimension_price(item, "input_price") for item in valid_intervals],
            flat_applied.input,
        ),
        output=minimum_price(
            [interval_dimension_price(item, "output_price") for item in valid_intervals],
            flat_applied.output,
        ),
        cache_read=minimum_price(
            [interval_dimension_price(item, "cache_read_price") for item in valid_intervals],
            flat_applied.cache_read,
        ),
        cache_write=minimum_price(
            [interval_dimension_price(item, "cache_write_price") for item in valid_intervals],
            flat_applied.cache_write,
        ),
    )
    priority_fallback = effective_priority_prices(flat_tiers)
    conservative_priority = TokenPrices(
        input=minimum_price(
            [interval_dimension_price(item, "input_price") for item in valid_intervals],
            priority_fallback.input,
        ),
        output=minimum_price(
            [interval_dimension_price(item, "output_price") for item in valid_intervals],
            priority_fallback.output,
        ),
        cache_read=minimum_price(
            [interval_dimension_price(item, "cache_read_price") for item in valid_intervals],
            priority_fallback.cache_read,
        ),
        cache_write=priority_fallback.cache_write,
    )
    # A matched interval constructs a fresh ModelPricing and copies each
    # configured token price into both default and priority fields. Therefore
    # priority does not receive the generic 2x multiplier for interval hits.
    return BasePricingTiers(
        default=conservative,
        priority_explicit=TokenPrices(
            input=conservative_priority.input,
            output=conservative_priority.output,
            cache_read=conservative_priority.cache_read,
        ),
    ), "channel_intervals_conservative"


def evaluate_mapping(
    *,
    account: dict[str, Any],
    requested_model: str,
    upstream_item: dict[str, Any] | None,
    pricing: dict[str, Any],
    recharge_factor: Decimal,
    local_prices: BasePricingTiers | None,
    local_pricing_source: str = "unknown",
    mapped_upstream_model: str = "",
    billing_model: str = "",
    billing_model_source: str = "",
    channel_mapped_model: str = "",
    local_pricing_error: str = "",
    kbq_web_search_price_per_call: Decimal = DEFAULT_KBQ_WEB_SEARCH_PRICE_PER_CALL,
    site_default_web_search_price_per_call: Decimal = DEFAULT_SITE_WEB_SEARCH_PRICE_PER_CALL,
    fast_policy_settings: dict[str, Any] | None = None,
) -> dict[str, Any]:
    policy_model = str(
        (upstream_item or {}).get("model_name")
        or mapped_upstream_model
        or channel_mapped_model
        or requested_model
    )
    checked_service_tiers = reachable_service_tiers(
        account,
        policy_model,
        fast_policy_settings,
    )
    result: dict[str, Any] = {
        "account_id": int(account.get("account_id") or 0),
        "account_name": account.get("account_name") or "",
        "account_status": account.get("account_status") or "",
        "schedulable": bool(account.get("schedulable")),
        "group_id": account.get("group_id"),
        "group_name": account.get("group_name") or "",
        "group_status": account.get("group_status") or "",
        "requested_model": requested_model,
        "channel_mapped_model": channel_mapped_model or requested_model,
        "upstream_model": (upstream_item or {}).get("model_name") or mapped_upstream_model,
        "billing_model": billing_model or requested_model,
        "billing_model_source": billing_model_source or "requested",
        "pricing_status": (upstream_item or {}).get("pricing_status") or "",
        "channel_id": account.get("channel_id"),
        "channel_name": account.get("channel_name") or "",
        "channel_status": account.get("channel_status") or "missing",
        "local_pricing_source": local_pricing_source,
        "group_rate_multiplier": decimal_or_none(account.get("group_rate_multiplier")),
        "minimum_user_multiplier": decimal_or_none(account.get("minimum_user_multiplier")),
        "group_web_search_price_per_call": decimal_or_none(account.get("group_web_search_price_per_call")),
        "kbq_group_key": "",
        "kbq_group_ratio": None,
        "group_ratio_source": "",
        # Compatibility field for existing callers. The persisted audit uses
        # the more explicit three-state tool_fee_status field.
        "tool_fee_unknown": False,
        "tool_fee_status": "NOT_APPLICABLE",
        "upstream_web_search_price_per_call": None,
        "site_web_search_price_per_call": None,
        "upstream_input_price": None,
        "upstream_output_price": None,
        "upstream_cache_read_price": None,
        "upstream_cache_write_price": None,
        "local_input_price": None,
        "local_output_price": None,
        "local_cache_read_price": None,
        "local_cache_write_price": None,
        "site_input_price": None,
        "site_output_price": None,
        "site_cache_read_price": None,
        "site_cache_write_price": None,
        "minimum_break_even_multiplier": None,
        "minimum_break_even_multiplier_priority": None,
        "minimum_break_even_multiplier_flex": None,
        "minimum_safe_user_multiplier": None,
        "pool_account_count": 0,
        "pool_minimum_safe_user_multiplier": None,
        "pool_price_source_account_id": None,
        "pool_price_source_account_name": "",
        "pool_status": "NOT_EVALUATED",
        "checked_service_tiers": ",".join(checked_service_tiers),
    }
    if local_pricing_error in {
        "CHANNEL_BINDING_AMBIGUOUS",
        "CHANNEL_MAPPING_AMBIGUOUS",
        "INVALID_BILLING_MODEL_SOURCE",
        "CHANNEL_PRICING_AMBIGUOUS",
    }:
        return {
            **result,
            "status": local_pricing_error,
            "note": "billing context or active channel pricing is not uniquely resolvable",
        }
    if (
        not upstream_item
        or upstream_item.get("quota_type", 0) != 0
        or decimal_or_none(upstream_item.get("model_ratio")) is None
    ):
        return {**result, "status": "NO_UPSTREAM_PRICE", "note": "token price missing or model is per-call"}

    ratio, group_key, ratio_source = resolve_group_ratio(
        pricing,
        upstream_item,
        str(account.get("explicit_kbq_group") or ""),
    )
    result.update(
        {
            "kbq_group_key": group_key,
            "kbq_group_ratio": ratio,
            "group_ratio_source": ratio_source,
        }
    )
    if ratio is None:
        return {**result, "status": "AMBIGUOUS_GROUP_RATIO", "note": "set accounts.extra.kbq_group_key"}

    upstream = upstream_prices(upstream_item, ratio, recharge_factor)
    result.update(
        {
            "upstream_input_price": upstream.input,
            "upstream_output_price": upstream.output,
            "upstream_cache_read_price": upstream.cache_read,
            "upstream_cache_write_price": upstream.cache_write,
        }
    )
    if local_prices is None:
        status = local_pricing_error or "NO_LOCAL_PRICE"
        note = (
            "multiple active channel pricing rows have conflicting effective prices"
            if status == "CHANNEL_PRICING_AMBIGUOUS"
            else "site billing price unavailable"
        )
        return {**result, "status": status, "note": note}

    minimum_user_multiplier = decimal_or_none(account.get("minimum_user_multiplier"))
    if minimum_user_multiplier is None:
        minimum_user_multiplier = decimal_or_none(account.get("group_rate_multiplier"))
    if minimum_user_multiplier is None:
        return {**result, "status": "NO_USER_PRICE", "note": "group/user multiplier unavailable"}

    local_default = local_prices.default
    local_priority = effective_priority_prices(local_prices)
    local_flex = flex_prices(local_prices)
    site = scaled_prices(local_default, minimum_user_multiplier)
    site_priority = scaled_prices(local_priority, minimum_user_multiplier)
    site_flex = scaled_prices(local_flex, minimum_user_multiplier)
    missing_price_dimensions = missing_positive_price_dimensions(upstream, local_default)
    missing_price_dimensions_priority = missing_positive_price_dimensions(upstream, local_priority)
    missing_price_dimensions_flex = missing_positive_price_dimensions(upstream, local_flex)
    break_even_ratios = dimension_ratios(upstream, local_default)
    break_even_ratios_priority = dimension_ratios(upstream, local_priority)
    break_even_ratios_flex = dimension_ratios(upstream, local_flex)
    break_even_by_tier = {
        "default": max(break_even_ratios) if break_even_ratios else None,
        "priority": max(break_even_ratios_priority) if break_even_ratios_priority else None,
        "flex": max(break_even_ratios_flex) if break_even_ratios_flex else None,
    }
    reachable_break_even = [
        break_even_by_tier[tier]
        for tier in checked_service_tiers
        if break_even_by_tier[tier] is not None
    ]
    result.update(
        {
            "local_input_price": local_default.input,
            "local_output_price": local_default.output,
            "local_cache_read_price": local_default.cache_read,
            "local_cache_write_price": local_default.cache_write,
            "site_input_price": site.input,
            "site_output_price": site.output,
            "site_cache_read_price": site.cache_read,
            "site_cache_write_price": site.cache_write,
            "minimum_break_even_multiplier": break_even_by_tier["default"],
            "minimum_break_even_multiplier_priority": break_even_by_tier["priority"],
            "minimum_break_even_multiplier_flex": break_even_by_tier["flex"],
            "minimum_safe_user_multiplier": max(reachable_break_even) if reachable_break_even else None,
        }
    )
    all_missing_by_tier = {
        "default": missing_price_dimensions,
        "priority": missing_price_dimensions_priority,
        "flex": missing_price_dimensions_flex,
    }
    missing_by_tier = {
        tier: all_missing_by_tier[tier]
        for tier in checked_service_tiers
        if all_missing_by_tier[tier]
    }
    if missing_by_tier:
        details = "; ".join(
            f"{tier}: {', '.join(values)}" for tier, values in missing_by_tier.items()
        )
        return {
            **result,
            "status": "NO_LOCAL_PRICE",
            "note": "site billing price missing for upstream-paid dimensions: " + details,
        }
    prices_by_tier = {
        "default": site,
        "priority": site_priority,
        "flex": site_flex,
    }
    candidate_tiers = [
        (tier, prices_by_tier[tier]) for tier in checked_service_tiers
    ]
    loss_tiers = [
        tier
        for tier, prices in candidate_tiers
        if any(value > LOSS_TOLERANCE for value in dimension_ratios(upstream, prices))
    ]
    token_loss = bool(loss_tiers)
    model_name = str(upstream_item.get("model_name") or "").lower()
    if "deepseek" in model_name:
        configured_tool_price = result["group_web_search_price_per_call"]
        configured_site_tool_unit_price = (
            configured_tool_price
            if configured_tool_price is not None
            else site_default_web_search_price_per_call
        )
        upstream_tool_price = kbq_web_search_price_per_call * recharge_factor
        configured_site_tool_price = configured_site_tool_unit_price * minimum_user_multiplier
        # groups.web_search_price_per_call exists in the production schema, but
        # the current gateway billing path never reads it and usage_logs has no
        # tool-call count. Treat the effective user charge as zero until that
        # path is implemented; a configured value must not create false safety.
        site_tool_price = Decimal("0")
        result.update(
            {
                "tool_fee_unknown": True,
                "upstream_web_search_price_per_call": upstream_tool_price,
                "site_web_search_price_per_call": site_tool_price,
                "tool_fee_status": "TOOL_FEE_UNCOVERED_LOSS",
            }
        )
    if token_loss:
        result["status"] = "REAL_LOSS"
    elif result["tool_fee_status"].startswith("TOOL_FEE_UNCOVERED"):
        result["status"] = result["tool_fee_status"]
    else:
        result["status"] = "OK"
    checked_tier_note = f"; checked service tiers: {', '.join(checked_service_tiers)}"
    tier_note = f"; loss tiers: {', '.join(loss_tiers)}" if loss_tiers else ""
    result["note"] = (
        "token pricing checked against the lowest effective user multiplier; "
        "DeepSeek web_search count is not populated by the generic token response path and "
        "the configured site price is not added to actual_cost; effective user tool charge is 0 "
        f"(configured post-multiplier price would be {configured_site_tool_price})"
        f"{checked_tier_note}{tier_note}"
        if "deepseek" in model_name
        else "token pricing checked against the lowest effective user multiplier"
        + checked_tier_note
        + tier_note
    )
    return result


def apply_pool_safety(rows: list[dict[str, Any]]) -> None:
    """Annotate routable group/model pools with their most expensive account.

    A request can land on any active, schedulable account mapped to the same
    group and requested model.  Therefore the group's lowest effective user
    multiplier must cover the highest break-even multiplier in that pool.
    Dormant draft accounts are deliberately excluded so they cannot raise a
    production safety line before they are enabled.
    """
    pools: dict[tuple[int, str], list[dict[str, Any]]] = {}
    for row in rows:
        if not mapping_is_currently_routable(row):
            continue
        group_id = row.get("group_id")
        requested_model = str(row.get("requested_model") or "").strip().lower()
        if group_id is None or not requested_model:
            continue
        pools.setdefault((int(group_id), requested_model), []).append(row)

    for pool_rows in pools.values():
        known = [
            row
            for row in pool_rows
            if decimal_or_none(row.get("minimum_safe_user_multiplier")) is not None
        ]
        if not known:
            for row in pool_rows:
                row["pool_account_count"] = len({item["account_id"] for item in pool_rows})
                row["pool_status"] = "INCOMPLETE"
            continue

        source = max(
            known,
            key=lambda row: decimal_or_none(row.get("minimum_safe_user_multiplier"))
            or Decimal("0"),
        )
        safe_multiplier = decimal_or_none(source.get("minimum_safe_user_multiplier"))
        account_count = len({item["account_id"] for item in pool_rows})
        for row in pool_rows:
            row["pool_account_count"] = account_count
            row["pool_minimum_safe_user_multiplier"] = safe_multiplier
            row["pool_price_source_account_id"] = source.get("account_id")
            row["pool_price_source_account_name"] = source.get("account_name") or ""
            user_multiplier = decimal_or_none(row.get("minimum_user_multiplier"))
            underpriced = (
                safe_multiplier is not None
                and user_multiplier is not None
                and safe_multiplier > user_multiplier * LOSS_TOLERANCE
            )
            row["pool_status"] = "POOL_PRICE_UNDERCUT" if underpriced else "OK"
            if underpriced and row.get("status") == "OK":
                row["status"] = "POOL_PRICE_UNDERCUT"
                row["note"] = (
                    f"{row.get('note') or ''}; pool safety line {safe_multiplier} is set by "
                    f"account {source.get('account_id')} ({source.get('account_name') or ''})"
                ).lstrip("; ")


def index_upstream_models(pricing: dict[str, Any]) -> dict[str, list[dict[str, Any]]]:
    result: dict[str, list[dict[str, Any]]] = {}
    for item in pricing.get("data") or []:
        if isinstance(item, dict) and item.get("model_name"):
            result.setdefault(str(item["model_name"]), []).append(item)
    return result


def select_upstream_item(
    candidates: list[dict[str, Any]],
    pricing: dict[str, Any],
    explicit_group: str,
) -> dict[str, Any] | None:
    resolved: list[tuple[dict[str, Any], Decimal, str, tuple[str, ...]]] = []
    for item in candidates:
        ratio, key, _source = resolve_group_ratio(pricing, item, explicit_group)
        # Keep the sole candidate so evaluate_mapping can report the precise
        # AMBIGUOUS_GROUP_RATIO or NO_UPSTREAM_PRICE reason. It has still been
        # validated through resolve_group_ratio and the required model fields.
        if len(candidates) == 1 and (
            ratio is None
            or item.get("quota_type", 0) != 0
            or decimal_or_none(item.get("model_ratio")) is None
        ):
            return item
        if ratio is not None:
            price_signature = tuple(
                str(item.get(field))
                for field in (
                    "quota_type",
                    "model_ratio",
                    "completion_ratio",
                    "cache_ratio",
                    "create_cache_ratio",
                )
            )
            resolved.append((item, ratio, key, price_signature))
    identities = {(ratio, key, signature) for _item, ratio, key, signature in resolved}
    return resolved[0][0] if len(identities) == 1 and resolved else None


def audit(args: argparse.Namespace) -> tuple[dict[str, Any], list[dict[str, Any]]]:
    pricing = fetch_kbq_pricing(args)
    pricing_document = load_model_pricing_document(args)
    accounts, channel_pricing_rows, fast_policy_settings = load_production_configuration(args)
    upstream_models = index_upstream_models(pricing)
    recharge_factor = decimal_or_none(args.recharge_factor) or DEFAULT_KBQ_RECHARGE_FACTOR
    kbq_web_search_price_per_call = (
        decimal_or_none(args.kbq_web_search_price_per_call)
        or DEFAULT_KBQ_WEB_SEARCH_PRICE_PER_CALL
    )
    site_default_web_search_price_per_call = (
        decimal_or_none(args.site_default_web_search_price_per_call)
        or DEFAULT_SITE_WEB_SEARCH_PRICE_PER_CALL
    )
    rows: list[dict[str, Any]] = []
    for account in accounts:
        model_mapping = account.get("model_mapping") or {}
        if not isinstance(model_mapping, dict):
            continue
        for requested_model, upstream_model in sorted(model_mapping.items()):
            context = resolve_billing_model_context(
                account,
                str(requested_model),
                str(upstream_model),
            )
            candidates = upstream_models.get(str(upstream_model), [])
            item = select_upstream_item(
                candidates,
                pricing,
                str(account.get("explicit_kbq_group") or ""),
            )
            base_prices, base_source = base_prices_for_model(
                context.billing_model,
                pricing_document,
            )
            channel_row, channel_status = resolve_channel_pricing(
                account,
                context.billing_model,
                channel_pricing_rows,
            )
            local_pricing_error = context.error_status
            if not local_pricing_error and channel_status == "ambiguous":
                local_pricing_error = "CHANNEL_PRICING_AMBIGUOUS"
            local_prices, channel_source = apply_channel_pricing(base_prices, channel_row)
            if local_pricing_error:
                local_prices = None
            local_source = base_source if channel_row is None else f"{base_source}+{channel_source}"
            if local_pricing_error:
                local_source = f"{base_source}+channel_ambiguous"
            rows.append(
                evaluate_mapping(
                    account=account,
                    requested_model=str(requested_model),
                    upstream_item=item,
                    pricing=pricing,
                    recharge_factor=recharge_factor,
                    local_prices=local_prices,
                    local_pricing_source=local_source,
                    local_pricing_error=local_pricing_error,
                    mapped_upstream_model=str(upstream_model),
                    billing_model=context.billing_model,
                    billing_model_source=context.billing_model_source,
                    channel_mapped_model=context.channel_mapped_model,
                    kbq_web_search_price_per_call=kbq_web_search_price_per_call,
                    site_default_web_search_price_per_call=site_default_web_search_price_per_call,
                    fast_policy_settings=fast_policy_settings,
                )
            )

    apply_pool_safety(rows)
    status_counts = {status: sum(1 for row in rows if row["status"] == status) for status in BLOCKING_STATUSES | {"OK"}}
    active_rows = [row for row in rows if mapping_is_currently_routable(row)]
    blocking_rows = [row for row in rows if row["status"] in BLOCKING_STATUSES]
    summary = {
        "observed_at": datetime.now(timezone.utc).isoformat(timespec="seconds"),
        "pricing_version": pricing.get("pricing_version") or "",
        "account_count": len({row["account_id"] for row in rows}),
        "mapping_count": len(rows),
        "active_mapping_count": len(active_rows),
        "ok_count": status_counts.get("OK", 0),
        "blocking_count": len(blocking_rows),
        "active_blocking_count": sum(
            1 for row in blocking_rows if mapping_is_currently_routable(row)
        ),
        "dormant_blocking_count": sum(
            1 for row in blocking_rows if not mapping_is_currently_routable(row)
        ),
        "real_loss_count": status_counts.get("REAL_LOSS", 0),
        "ambiguous_group_ratio_count": status_counts.get("AMBIGUOUS_GROUP_RATIO", 0),
        "missing_upstream_price_count": status_counts.get("NO_UPSTREAM_PRICE", 0),
        "missing_local_price_count": status_counts.get("NO_LOCAL_PRICE", 0),
        "missing_user_price_count": status_counts.get("NO_USER_PRICE", 0),
        "tool_fee_uncovered_count": sum(
            1 for row in rows if str(row.get("tool_fee_status") or "").startswith("TOOL_FEE_UNCOVERED")
        ),
        "channel_pricing_ambiguous_count": status_counts.get("CHANNEL_PRICING_AMBIGUOUS", 0),
        "channel_binding_ambiguous_count": status_counts.get("CHANNEL_BINDING_AMBIGUOUS", 0),
        "channel_mapping_ambiguous_count": status_counts.get("CHANNEL_MAPPING_AMBIGUOUS", 0),
        "invalid_billing_model_source_count": status_counts.get("INVALID_BILLING_MODEL_SOURCE", 0),
        "pool_underpriced_count": sum(
            1 for row in rows if row.get("pool_status") == "POOL_PRICE_UNDERCUT"
        ),
        "tool_fee_unknown_count": sum(1 for row in rows if row.get("tool_fee_unknown")),
        "source": args.pricing_url,
        "note": (
            "Read-only configuration preflight; production pricing order is active channel override, "
            "running-image LiteLLM data, then known hardcoded fallback. Inactive channels are ignored."
        ),
    }
    return summary, rows


def write_ledger(db_path: str, summary: dict[str, Any], rows: list[dict[str, Any]]) -> int:
    path = Path(db_path)
    path.parent.mkdir(parents=True, exist_ok=True)
    conn = sqlite3.connect(path)
    with conn:
        ensure_ledger_schema(conn)
        run_payload = dict(summary)
        run_payload.setdefault("active_mapping_count", 0)
        run_payload.setdefault("active_blocking_count", 0)
        run_payload.setdefault("dormant_blocking_count", run_payload.get("blocking_count", 0))
        run_payload.setdefault("channel_pricing_ambiguous_count", 0)
        run_payload.setdefault("channel_binding_ambiguous_count", 0)
        run_payload.setdefault("channel_mapping_ambiguous_count", 0)
        run_payload.setdefault("invalid_billing_model_source_count", 0)
        run_payload.setdefault("pool_underpriced_count", 0)
        cursor = conn.execute(
            """
            insert into kbq_configuration_audit_runs (
              observed_at, pricing_version, account_count, mapping_count,
              active_mapping_count, ok_count, blocking_count,
              active_blocking_count, dormant_blocking_count, real_loss_count,
              channel_pricing_ambiguous_count,
              channel_binding_ambiguous_count, channel_mapping_ambiguous_count,
              invalid_billing_model_source_count, pool_underpriced_count,
              ambiguous_group_ratio_count, missing_upstream_price_count,
              missing_local_price_count, missing_user_price_count,
              tool_fee_uncovered_count, tool_fee_unknown_count, source, note
            ) values (
              :observed_at, :pricing_version, :account_count, :mapping_count,
              :active_mapping_count, :ok_count, :blocking_count,
              :active_blocking_count, :dormant_blocking_count, :real_loss_count,
              :channel_pricing_ambiguous_count,
              :channel_binding_ambiguous_count, :channel_mapping_ambiguous_count,
              :invalid_billing_model_source_count, :pool_underpriced_count,
              :ambiguous_group_ratio_count, :missing_upstream_price_count,
              :missing_local_price_count, :missing_user_price_count,
              :tool_fee_uncovered_count, :tool_fee_unknown_count, :source, :note
            )
            """,
            run_payload,
        )
        run_id = int(cursor.lastrowid)
        decimal_keys = {
            "kbq_group_ratio",
            "group_rate_multiplier",
            "minimum_user_multiplier",
            "upstream_input_price",
            "upstream_output_price",
            "upstream_cache_read_price",
            "upstream_cache_write_price",
            "local_input_price",
            "local_output_price",
            "local_cache_read_price",
            "local_cache_write_price",
            "site_input_price",
            "site_output_price",
            "site_cache_read_price",
            "site_cache_write_price",
            "minimum_break_even_multiplier",
            "minimum_break_even_multiplier_priority",
            "minimum_break_even_multiplier_flex",
            "minimum_safe_user_multiplier",
            "pool_minimum_safe_user_multiplier",
            "group_web_search_price_per_call",
            "upstream_web_search_price_per_call",
            "site_web_search_price_per_call",
        }
        for row in rows:
            payload = dict(row)
            payload["run_id"] = run_id
            payload["schedulable"] = int(bool(payload.get("schedulable")))
            for key in decimal_keys:
                payload[key] = to_float(payload.get(key))
            conn.execute(
                """
                insert into kbq_configuration_audit_rows (
                  run_id, status, account_id, account_name, account_status,
                  schedulable, group_id, group_name, group_status,
                  requested_model, channel_mapped_model, upstream_model,
                  billing_model, billing_model_source, pricing_status,
                  kbq_group_key, kbq_group_ratio, group_ratio_source,
                  group_rate_multiplier, minimum_user_multiplier,
                  channel_id, channel_name, channel_status, local_pricing_source,
                  upstream_input_price, upstream_output_price,
                  upstream_cache_read_price, upstream_cache_write_price,
                  local_input_price, local_output_price,
                  local_cache_read_price, local_cache_write_price,
                  site_input_price, site_output_price,
                  site_cache_read_price, site_cache_write_price,
                  minimum_break_even_multiplier,
                  minimum_break_even_multiplier_priority,
                  minimum_break_even_multiplier_flex,
                  minimum_safe_user_multiplier,
                  pool_account_count, pool_minimum_safe_user_multiplier,
                  pool_price_source_account_id, pool_price_source_account_name,
                  pool_status,
                  checked_service_tiers,
                  group_web_search_price_per_call,
                  upstream_web_search_price_per_call, site_web_search_price_per_call,
                  tool_fee_status, note
                ) values (
                  :run_id, :status, :account_id, :account_name, :account_status,
                  :schedulable, :group_id, :group_name, :group_status,
                  :requested_model, :channel_mapped_model, :upstream_model,
                  :billing_model, :billing_model_source, :pricing_status,
                  :kbq_group_key, :kbq_group_ratio, :group_ratio_source,
                  :group_rate_multiplier, :minimum_user_multiplier,
                  :channel_id, :channel_name, :channel_status, :local_pricing_source,
                  :upstream_input_price, :upstream_output_price,
                  :upstream_cache_read_price, :upstream_cache_write_price,
                  :local_input_price, :local_output_price,
                  :local_cache_read_price, :local_cache_write_price,
                  :site_input_price, :site_output_price,
                  :site_cache_read_price, :site_cache_write_price,
                  :minimum_break_even_multiplier,
                  :minimum_break_even_multiplier_priority,
                  :minimum_break_even_multiplier_flex,
                  :minimum_safe_user_multiplier,
                  :pool_account_count, :pool_minimum_safe_user_multiplier,
                  :pool_price_source_account_id, :pool_price_source_account_name,
                  :pool_status,
                  :checked_service_tiers,
                  :group_web_search_price_per_call,
                  :upstream_web_search_price_per_call, :site_web_search_price_per_call,
                  :tool_fee_status, :note
                )
                """,
                payload,
            )
        conn.execute(
            "insert or replace into metadata(key, value) values (?, ?)",
            ("kbq_configuration_audit_updated_at", summary["observed_at"]),
        )
        conn.execute(
            "insert or replace into metadata(key, value) values (?, ?)",
            ("kbq_configuration_audit_blocking_count", str(summary["blocking_count"])),
        )
    conn.close()
    return run_id


def should_fail_on_loss(summary: dict[str, Any], fail_on_loss: bool) -> bool:
    return fail_on_loss and int(summary.get("active_blocking_count") or 0) > 0


def main() -> int:
    args = parse_args()
    summary, rows = audit(args)
    run_id = write_ledger(args.db, summary, rows)
    print(
        "KBQ configuration audit run #{run_id}: accounts={accounts}, mappings={mappings}, "
        "active_mappings={active_mappings}, ok={ok}, blocking={blocking}, "
        "active_blocking={active_blocking}, dormant_blocking={dormant_blocking}, "
        "real_loss={loss}, ambiguous={ambiguous}, "
        "missing_upstream={missing_upstream}, missing_local={missing_local}, "
        "tool_fee_uncovered={tool_fee_uncovered}".format(
            run_id=run_id,
            accounts=summary["account_count"],
            mappings=summary["mapping_count"],
            active_mappings=summary["active_mapping_count"],
            ok=summary["ok_count"],
            blocking=summary["blocking_count"],
            active_blocking=summary["active_blocking_count"],
            dormant_blocking=summary["dormant_blocking_count"],
            loss=summary["real_loss_count"],
            ambiguous=summary["ambiguous_group_ratio_count"],
            missing_upstream=summary["missing_upstream_price_count"],
            missing_local=summary["missing_local_price_count"],
            tool_fee_uncovered=summary["tool_fee_uncovered_count"],
        )
    )
    if should_fail_on_loss(summary, args.fail_on_loss):
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
