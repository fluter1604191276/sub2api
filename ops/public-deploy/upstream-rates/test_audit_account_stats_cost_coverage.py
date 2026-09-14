import argparse
import json
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest import mock

import audit_account_stats_cost_coverage as audit


class QueryConstructionTests(unittest.TestCase):
    def test_query_is_read_only_json_and_uses_historical_multiplier(self):
        query = audit.build_query(24, 12)

        self.assertIn("begin transaction read only", query.lower())
        self.assertIn("set local statement_timeout = '12000ms'", query.lower())
        self.assertIn("jsonb_agg(row_to_json(result_row)", query)
        self.assertIn("coalesce(account_stats_cost, total_cost) * historical_account_rate_multiplier", query)
        self.assertNotIn("a.rate_multiplier", query)
        self.assertNotIn("credentials", query)
        self.assertNotIn("user_id", query)
        self.assertNotIn("api_key_id", query)

    def test_rule_for_wrong_account_and_group_is_not_a_candidate(self):
        query = audit.build_query(24)

        self.assertIn("ul.account_id = any(rule.account_ids) or ul.group_id = any(rule.group_ids)", query)

    def test_rule_for_wrong_model_is_not_a_candidate(self):
        query = audit.build_query(24)

        self.assertIn("coalesce(nullif(trim(ul.upstream_model), ''), ul.model)", query)
        self.assertIn("lower(configured_model) = lower", query)
        self.assertIn("right(lower(configured_model), 1) = '*'", query)
        self.assertIn("length(configured_model) - 1", query)
        self.assertNotIn("like left(lower(configured_model), -1) || '%'", query)

    def test_rule_for_wrong_platform_is_not_a_candidate(self):
        query = audit.build_query(24)

        self.assertIn("coalesce(g.platform, '') = ''", query)
        self.assertIn("or pricing.platform = coalesce(g.platform, '')", query)

    def test_composite_platform_is_not_rewritten_to_account_platform(self):
        query = audit.build_query(24)

        self.assertIn("coalesce(g.platform, '') as platform", query)
        self.assertNotIn("when g.platform = 'composite' then a.platform", query)
        self.assertNotIn("pricing.platform = a.platform", query)

    def test_rule_must_apply_to_observed_token_or_image_usage(self):
        query = audit.build_query(24)

        self.assertIn("ul.image_count > 0", query)
        self.assertIn("ul.image_count = 0", query)
        self.assertIn("pricing.billing_mode = 'image'", query)
        self.assertIn("pricing.billing_mode = 'per_request'", query)
        self.assertIn("interval_price.min_tokens <", query)
        self.assertNotIn("interval_price.min_tokens <=", query)
        self.assertIn("jsonb_each_text", query)
        self.assertIn("upper(trim(size_bucket.key)) in ('1K', '2K', '4K')", query)
        self.assertIn("when pricing.billing_mode = 'per_request' then coalesce(pricing.per_request_price, 0) > 0", query)

    def test_unusable_exact_nonimage_price_blocks_later_wildcard_or_rule(self):
        query = audit.build_query(24)

        self.assertIn("end as applicable", query)
        self.assertIn("where terminal_rule.applicable", query)
        self.assertLess(query.index("limit 1\n      ) terminal_rule"), query.index("where terminal_rule.applicable"))
        self.assertIn("then 0 else 1 end", query)

    def test_matching_interval_does_not_inherit_missing_base_prices(self):
        query = audit.build_query(24)

        self.assertIn("ul.input_tokens * coalesce(interval_price.input_price, 0)", query)
        self.assertNotIn("coalesce(interval_price.input_price, pricing.input_price", query)
        self.assertNotIn("coalesce(interval_price.output_price, pricing.output_price", query)

    def test_zero_total_cost_does_not_claim_channel_total_cost_override(self):
        query = audit.build_query(24)

        self.assertIn(
            "when current_channel_applies_pricing and total_cost > 0 then 'candidate_channel_total_cost'",
            query,
        )

    def test_query_distinguishes_snapshot_and_current_channel(self):
        query = audit.build_query(24)

        self.assertIn("ul.channel_id as snapshot_channel_id", query)
        self.assertIn("current_link.group_id = ul.group_id", query)
        self.assertIn("channel_snapshot_matches_current", query)
        self.assertIn("candidate_explicit_rule", query)
        self.assertIn("candidate_default_or_model_file", query)
        self.assertNotIn("litellm_fallback", query)

    def test_query_distinguishes_null_zero_and_nonzero_override(self):
        query = audit.build_query(24)

        self.assertIn("account_stats_cost is null then 'null_default_formula'", query)
        self.assertIn("account_stats_cost = 0 then 'zero_override'", query)
        self.assertIn("else 'nonzero_override'", query)

    def test_hours_and_statement_timeout_are_clamped(self):
        self.assertIn("interval '1 hours'", audit.build_query(0, 0))
        query = audit.build_query(999999, 999999)
        self.assertIn("interval '8760 hours'", query)
        self.assertIn("statement_timeout = '300000ms'", query)


class QueryExecutionTests(unittest.TestCase):
    def args(self, **overrides):
        values = {
            "local_postgres": False,
            "ssh_host": "host with tabs\tand spaces",
            "timeout": 9,
        }
        values.update(overrides)
        return argparse.Namespace(**values)

    @mock.patch.object(audit.subprocess, "run")
    def test_ssh_uses_argv_without_shell_interpolation(self, run):
        run.return_value = subprocess.CompletedProcess([], 0, stdout="[]\n", stderr="")

        self.assertEqual(audit.run_query("select 1", self.args()), [])

        command = run.call_args.args[0]
        self.assertEqual(command[:7], [
            "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=9", "--",
            "host with tabs\tand spaces",
        ])
        self.assertEqual(command[7:], audit.PSQL_ARGV)
        self.assertFalse(run.call_args.kwargs["shell"])
        self.assertEqual(run.call_args.kwargs["timeout"], 9)

    @mock.patch.object(audit.subprocess, "run")
    def test_local_postgres_uses_psql_argv(self, run):
        run.return_value = subprocess.CompletedProcess([], 0, stdout="[]", stderr="")

        audit.run_query("select 1", self.args(local_postgres=True))

        self.assertEqual(run.call_args.args[0], audit.PSQL_ARGV)

    @mock.patch.object(audit.subprocess, "run")
    def test_sql_error_is_not_silently_ignored(self, run):
        run.return_value = subprocess.CompletedProcess([], 3, stdout="", stderr="ERROR: bad query")

        with self.assertRaisesRegex(SystemExit, "PostgreSQL read failed: ERROR: bad query"):
            audit.run_query("broken", self.args())

    @mock.patch.object(audit.subprocess, "run")
    def test_json_parsing_failure_is_not_silently_ignored(self, run):
        run.return_value = subprocess.CompletedProcess([], 0, stdout="not-json\n", stderr="")

        with self.assertRaisesRegex(SystemExit, "invalid JSON"):
            audit.run_query("select", self.args())

    @mock.patch.object(audit.subprocess, "run", side_effect=subprocess.TimeoutExpired(["ssh"], 9))
    def test_timeout_is_reported(self, _run):
        with self.assertRaisesRegex(SystemExit, "timed out after 9s"):
            audit.run_query("select", self.args())

    def test_json_parser_preserves_names_with_tabs(self):
        rows = audit.parse_query_json(json.dumps([{"account_name": "name\twith\ttabs"}]))

        self.assertEqual(rows[0]["account_name"], "name\twith\ttabs")


class ReportTests(unittest.TestCase):
    def test_null_nonzero_fallback_uses_snapshot_cost_total(self):
        rows = [
            {
                "requests": 2,
                "stored_override_state": "null_default_formula",
                "candidate_config_coverage": "candidate_default_or_model_file",
                "standard_cost": "2.00",
                "historical_account_cost": "0.30",
                "user_billed_cost": "2.40",
            },
            {
                "requests": 1,
                "stored_override_state": "zero_override",
                "candidate_config_coverage": "candidate_explicit_rule",
                "standard_cost": "1.00",
                "historical_account_cost": "0.00",
                "user_billed_cost": "1.20",
            },
        ]

        report = audit.build_report(rows, 24)

        self.assertEqual(report["summary"]["requests"], 3)
        self.assertEqual(
            report["summary"]["requests_by_stored_override_state"],
            {"null_default_formula": 2, "zero_override": 1},
        )
        self.assertEqual(report["summary"]["historical_account_cost"], "0.30")
        self.assertIn("not classified as a defect", report["evidence_note"])

    @mock.patch.object(audit, "run_query", return_value=[])
    def test_main_writes_optional_json_output(self, _run_query):
        with tempfile.TemporaryDirectory() as temp_dir:
            output = Path(temp_dir) / "report.json"
            with mock.patch("sys.stdout"), mock.patch("sys.stderr"):
                result = audit.main(["--local-postgres", "--output", str(output)])

            self.assertEqual(result, 0)
            payload = json.loads(output.read_text(encoding="utf-8"))
            self.assertEqual(payload["summary"]["requests"], 0)


class ArgumentTests(unittest.TestCase):
    def test_defaults_to_production_alias(self):
        args = audit.parse_args([])

        self.assertEqual(args.ssh_host, "fluterapi-prod")
        self.assertEqual(args.timeout, 30)


if __name__ == "__main__":
    unittest.main()
