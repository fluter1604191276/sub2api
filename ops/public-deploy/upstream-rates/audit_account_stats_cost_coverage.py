#!/usr/bin/env python3
"""Audit stored account-cost overrides and current candidate pricing coverage.

The PostgreSQL query is read-only and selects only non-secret pricing and usage
fields. Current channel/rule configuration can indicate candidate coverage, but
cannot prove which pricing source produced a historical account_stats_cost.
"""

from __future__ import annotations

import argparse
import json
import subprocess
import sys
from decimal import Decimal
from pathlib import Path
from typing import Any


PSQL_ARGV = [
    "docker", "exec", "-i", "sub2api-postgres", "psql",
    "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1",
    "-U", "sub2api", "-d", "sub2api",
]
EVIDENCE_NOTE = (
    "Candidate coverage is evaluated from current channel/rule configuration. "
    "It does not prove the historical source of a stored account_stats_cost. "
    "A default/model-file candidate is neutral and is not classified as a defect."
)


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--ssh-host", default="fluterapi-prod")
    parser.add_argument("--hours", type=int, default=24)
    parser.add_argument("--timeout", type=int, default=30)
    parser.add_argument("--local-postgres", action="store_true")
    parser.add_argument("--output", type=Path, help="Optionally write the full JSON report")
    return parser.parse_args(argv)


def _clamp(value: int, minimum: int, maximum: int) -> int:
    return max(minimum, min(value, maximum))


def build_query(hours: int, statement_timeout_seconds: int = 30) -> str:
    hours = _clamp(hours, 1, 24 * 365)
    statement_timeout_ms = _clamp(statement_timeout_seconds, 1, 300) * 1000
    return f"""
begin transaction read only;
set local statement_timeout = '{statement_timeout_ms}ms';
copy (
  with usage_with_config as (
    select
      ul.account_id,
      a.name as account_name,
      ul.group_id,
      coalesce(g.name, '') as group_name,
      coalesce(g.platform, '') as platform,
      ul.channel_id as snapshot_channel_id,
      coalesce(snapshot_channel.name, '') as snapshot_channel_name_current,
      current_channel.id as current_channel_id,
      coalesce(current_channel.name, '') as current_channel_name,
      coalesce(nullif(trim(ul.upstream_model), ''), ul.model) as upstream_model,
      ul.input_tokens,
      ul.output_tokens,
      ul.cache_creation_tokens,
      ul.cache_creation_5m_tokens,
      ul.cache_creation_1h_tokens,
      ul.cache_read_tokens,
      ul.image_output_tokens,
      ul.image_count,
      coalesce(ul.image_size, '') as image_size,
      coalesce(ul.image_size_breakdown, '{{}}'::jsonb) as image_size_breakdown,
      coalesce(ul.inbound_endpoint, '') as inbound_endpoint,
      ul.total_cost,
      ul.actual_cost,
      ul.account_stats_cost,
      coalesce(ul.account_rate_multiplier, 1) as historical_account_rate_multiplier,
      coalesce(current_channel.apply_pricing_to_account_stats, false) as current_channel_applies_pricing,
      matched_rule.rule_id as candidate_rule_id
    from usage_logs ul
    join accounts a on a.id = ul.account_id
    left join groups g on g.id = ul.group_id
    left join channels snapshot_channel on snapshot_channel.id = ul.channel_id
    left join channel_groups current_link on current_link.group_id = ul.group_id
    left join channels current_channel
      on current_channel.id = current_link.channel_id and current_channel.status = 'active'
    left join lateral (
      select terminal_rule.rule_id
      from (
        select
          rule.id as rule_id,
          case
            when ul.image_count > 0 then true
            when pricing.billing_mode = 'per_request' then coalesce(pricing.per_request_price, 0) > 0
            else coalesce(
              (
                select
                  ul.input_tokens * coalesce(interval_price.input_price, 0)
                  + ul.output_tokens * coalesce(interval_price.output_price, 0)
                  + ul.cache_creation_5m_tokens * coalesce(interval_price.cache_write_price, 0)
                  + ul.cache_creation_1h_tokens * coalesce(
                      interval_price.cache_write_1h_price,
                      interval_price.cache_write_price,
                      0
                    )
                  + greatest(
                      ul.cache_creation_tokens
                      - ul.cache_creation_5m_tokens
                      - ul.cache_creation_1h_tokens,
                      0
                    ) * coalesce(interval_price.cache_write_price, 0)
                  + ul.cache_read_tokens * coalesce(interval_price.cache_read_price, 0)
                from channel_account_stats_pricing_intervals interval_price
                where interval_price.pricing_id = pricing.id
                  and interval_price.min_tokens < (
                    ul.input_tokens + ul.output_tokens
                    + ul.cache_creation_tokens + ul.cache_read_tokens
                  )
                  and (
                    interval_price.max_tokens is null
                    or interval_price.max_tokens >= (
                      ul.input_tokens + ul.output_tokens
                      + ul.cache_creation_tokens + ul.cache_read_tokens
                    )
                  )
                order by interval_price.sort_order, interval_price.id
                limit 1
              ),
              ul.input_tokens * coalesce(pricing.input_price, 0)
              + ul.output_tokens * coalesce(pricing.output_price, 0)
              + ul.cache_creation_5m_tokens * coalesce(pricing.cache_write_price, 0)
              + ul.cache_creation_1h_tokens * coalesce(
                  pricing.cache_write_1h_price,
                  pricing.cache_write_price,
                  0
                )
              + greatest(
                  ul.cache_creation_tokens
                  - ul.cache_creation_5m_tokens
                  - ul.cache_creation_1h_tokens,
                  0
                ) * coalesce(pricing.cache_write_price, 0)
              + ul.cache_read_tokens * coalesce(pricing.cache_read_price, 0)
              + ul.image_output_tokens * coalesce(pricing.image_output_price, 0)
            ) > 0
          end as applicable
        from channel_account_stats_pricing_rules rule
        join channel_account_stats_model_pricing pricing on pricing.rule_id = rule.id
        where rule.channel_id = current_channel.id
          and (ul.account_id = any(rule.account_ids) or ul.group_id = any(rule.group_ids))
          and (
            coalesce(g.platform, '') = ''
            or pricing.platform = ''
            or pricing.platform = coalesce(g.platform, '')
          )
          and exists (
            select 1
            from jsonb_array_elements_text(pricing.models) configured_model
            where lower(configured_model) = lower(coalesce(nullif(trim(ul.upstream_model), ''), ul.model))
               or (
                 right(lower(configured_model), 1) = '*'
                 and left(
                   lower(coalesce(nullif(trim(ul.upstream_model), ''), ul.model)),
                   length(configured_model) - 1
                 ) = left(lower(configured_model), -1)
               )
          )
          and (
          (
            ul.image_count > 0
            and (
              (
                pricing.billing_mode = 'image'
                and coalesce(pricing.image_operation, '') in (
                  '',
                  case coalesce(ul.inbound_endpoint, '')
                    when '/v1/responses' then 'responses'
                    when '/v1/images/edits' then 'edit'
                    else 'generation'
                  end
                )
                and (
                  coalesce(pricing.per_request_price, 0) > 0
                  or (
                    not exists (
                      select 1
                      from jsonb_each_text(coalesce(ul.image_size_breakdown, '{{}}'::jsonb))
                    )
                    and exists (
                      select 1 from channel_account_stats_pricing_intervals interval_price
                      where interval_price.pricing_id = pricing.id
                        and upper(trim(coalesce(ul.image_size, ''))) in ('1K', '2K', '4K')
                        and upper(trim(interval_price.tier_label)) = upper(trim(coalesce(ul.image_size, '')))
                        and coalesce(interval_price.per_request_price, 0) > 0
                    )
                  )
                  or (
                    exists (
                      select 1
                      from jsonb_each_text(coalesce(ul.image_size_breakdown, '{{}}'::jsonb))
                    )
                    and (
                      select coalesce(sum(value::int), 0)
                      from jsonb_each_text(coalesce(ul.image_size_breakdown, '{{}}'::jsonb))
                    ) = ul.image_count
                    and not exists (
                      select 1
                      from jsonb_each_text(coalesce(ul.image_size_breakdown, '{{}}'::jsonb)) size_bucket
                      where size_bucket.value::int > 0
                        and not exists (
                          select 1 from channel_account_stats_pricing_intervals interval_price
                          where interval_price.pricing_id = pricing.id
                            and upper(trim(size_bucket.key)) in ('1K', '2K', '4K')
                            and upper(trim(interval_price.tier_label)) = upper(trim(size_bucket.key))
                            and coalesce(interval_price.per_request_price, 0) > 0
                        )
                    )
                  )
                )
              )
              or (
                pricing.billing_mode = 'per_request'
                and coalesce(pricing.image_operation, '') = ''
                and coalesce(pricing.per_request_price, 0) > 0
              )
            )
          )
          or (
            ul.image_count = 0
            and coalesce(pricing.billing_mode, 'token') in ('', 'token', 'per_request')
          )
        )
        order by
          rule.sort_order,
          rule.id,
          case when exists (
            select 1 from jsonb_array_elements_text(pricing.models) configured_model
            where lower(configured_model) = lower(coalesce(nullif(trim(ul.upstream_model), ''), ul.model))
          ) then 0 else 1 end,
          case
            when ul.image_count > 0 and coalesce(pricing.image_operation, '') =
              case coalesce(ul.inbound_endpoint, '')
                when '/v1/responses' then 'responses'
                when '/v1/images/edits' then 'edit'
                else 'generation'
              end then 0
            else 1
          end,
          pricing.id
        limit 1
      ) terminal_rule
      where terminal_rule.applicable
    ) matched_rule on true
    where ul.created_at >= now() - interval '{hours} hours'
  ), classified as (
    select *,
      case
        when account_stats_cost is null then 'null_default_formula'
        when account_stats_cost = 0 then 'zero_override'
        else 'nonzero_override'
      end as stored_override_state,
      case
        when current_channel_id is null then 'no_current_channel'
        when candidate_rule_id is not null then 'candidate_explicit_rule'
        when current_channel_applies_pricing and total_cost > 0 then 'candidate_channel_total_cost'
        else 'candidate_default_or_model_file'
      end as candidate_config_coverage
    from usage_with_config
  )
  select coalesce(jsonb_agg(row_to_json(result_row) order by historical_account_cost desc), '[]'::jsonb)
  from (
    select
      snapshot_channel_id,
      snapshot_channel_name_current,
      current_channel_id,
      current_channel_name,
      (snapshot_channel_id is not distinct from current_channel_id) as channel_snapshot_matches_current,
      account_id,
      account_name,
      group_id,
      group_name,
      platform,
      upstream_model,
      stored_override_state,
      candidate_config_coverage,
      candidate_rule_id,
      count(*)::bigint as requests,
      coalesce(sum(total_cost), 0)::text as standard_cost,
      coalesce(sum(coalesce(account_stats_cost, total_cost) * historical_account_rate_multiplier), 0)::text
        as historical_account_cost,
      coalesce(sum(actual_cost), 0)::text as user_billed_cost
    from classified
    group by
      snapshot_channel_id, snapshot_channel_name_current,
      current_channel_id, current_channel_name,
      account_id, account_name, group_id, group_name, platform, upstream_model,
      stored_override_state, candidate_config_coverage, candidate_rule_id
  ) result_row
) to stdout;
commit;
"""


def _command(args: argparse.Namespace) -> list[str]:
    if args.local_postgres:
        return list(PSQL_ARGV)
    return [
        "ssh", "-o", "BatchMode=yes", "-o", f"ConnectTimeout={args.timeout}",
        "--", args.ssh_host, *PSQL_ARGV,
    ]


def parse_query_json(stdout: str) -> list[dict[str, Any]]:
    payload = stdout.strip()
    if not payload:
        raise ValueError("PostgreSQL returned an empty JSON result")
    try:
        rows = json.loads(payload)
    except json.JSONDecodeError as exc:
        raise ValueError(f"PostgreSQL returned invalid JSON: {exc.msg}") from exc
    if not isinstance(rows, list) or not all(isinstance(row, dict) for row in rows):
        raise ValueError("PostgreSQL JSON result must be an array of objects")
    return rows


def run_query(sql: str, args: argparse.Namespace) -> list[dict[str, Any]]:
    try:
        result = subprocess.run(
            _command(args), input=sql, text=True, capture_output=True,
            shell=False, check=False, timeout=args.timeout,
        )
    except subprocess.TimeoutExpired as exc:
        raise SystemExit(f"PostgreSQL read timed out after {args.timeout}s") from exc
    if result.returncode != 0:
        detail = result.stderr.strip() or "psql exited without an error message"
        raise SystemExit(f"PostgreSQL read failed: {detail}")
    try:
        return parse_query_json(result.stdout)
    except ValueError as exc:
        raise SystemExit(str(exc)) from exc


def _decimal(value: Any) -> Decimal:
    return Decimal(str(value or "0"))


def build_report(rows: list[dict[str, Any]], hours: int) -> dict[str, Any]:
    override_requests: dict[str, int] = {}
    candidate_requests: dict[str, int] = {}
    requests = 0
    standard_cost = Decimal(0)
    historical_account_cost = Decimal(0)
    user_billed_cost = Decimal(0)
    for row in rows:
        count = int(row.get("requests") or 0)
        requests += count
        override = str(row.get("stored_override_state") or "unknown")
        candidate = str(row.get("candidate_config_coverage") or "unknown")
        override_requests[override] = override_requests.get(override, 0) + count
        candidate_requests[candidate] = candidate_requests.get(candidate, 0) + count
        standard_cost += _decimal(row.get("standard_cost"))
        historical_account_cost += _decimal(row.get("historical_account_cost"))
        user_billed_cost += _decimal(row.get("user_billed_cost"))
    return {
        "hours": _clamp(hours, 1, 24 * 365),
        "summary": {
            "requests": requests,
            "requests_by_stored_override_state": override_requests,
            "requests_by_candidate_config_coverage": candidate_requests,
            "standard_cost": str(standard_cost),
            "historical_account_cost": str(historical_account_cost),
            "user_billed_cost": str(user_billed_cost),
        },
        "evidence_note": EVIDENCE_NOTE,
        "rows": rows,
    }


def print_summary(report: dict[str, Any]) -> None:
    summary = report["summary"]
    print(f"Account stats cost coverage, last {report['hours']}h")
    print(f"Requests: {summary['requests']}")
    print("Stored override states: " + json.dumps(
        summary["requests_by_stored_override_state"], ensure_ascii=False, sort_keys=True
    ))
    print("Current candidate coverage: " + json.dumps(
        summary["requests_by_candidate_config_coverage"], ensure_ascii=False, sort_keys=True
    ))
    print(
        "Costs: standard={standard_cost}, historical_account={historical_account_cost}, "
        "user_billed={user_billed_cost}".format(**summary)
    )
    print(EVIDENCE_NOTE)


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)
    if args.timeout < 1:
        raise SystemExit("--timeout must be at least 1 second")
    rows = run_query(build_query(args.hours, args.timeout), args)
    report = build_report(rows, args.hours)
    print_summary(report)
    if args.output:
        args.output.write_text(
            json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
        )
        print(f"JSON report: {args.output}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
