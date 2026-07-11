#!/usr/bin/env python3
from __future__ import annotations

import importlib.util
import sys
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("refresh_balance_api_adapters.py")


def load_module():
    spec = importlib.util.spec_from_file_location("refresh_balance_api_adapters", SCRIPT)
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class RefreshBalanceApiAdaptersTest(unittest.TestCase):
    def setUp(self) -> None:
        self.mod = load_module()

    def test_congmingai_account_is_named_consistently(self):
        account = self.mod.Account(
            id=1,
            name="聪明AI codex 0.05",
            platform="openai",
            base_url="https://sub2.congmingai.com/v1",
            api_key="sk-test-redacted",
            status="active",
            schedulable=True,
        )

        self.assertEqual("sub2.congmingai.com", account.host)
        self.assertEqual("聪明AI", self.mod.provider_name(account))

    def test_build_auth_headers_supports_bearer_and_raw(self):
        bearer = self.mod.build_auth_headers("sk-test-redacted", "bearer")
        raw = self.mod.build_auth_headers("sk-test-redacted", "raw")

        self.assertEqual("Bearer sk-test-redacted", bearer["Authorization"])
        self.assertEqual("sk-test-redacted", raw["Authorization"])

    def test_congmingai_endpoint_candidates_include_raw_auth(self):
        account = self.mod.Account(
            id=1,
            name="聪明AI codex 0.05",
            platform="openai",
            base_url="https://sub2.congmingai.com/v1",
            api_key="sk-test-redacted",
            status="active",
            schedulable=True,
        )

        candidates = self.mod.endpoint_candidates(account)

        self.assertIn(
            ("newapi_user_self", "https://sub2.congmingai.com/api/user/self", None, "raw"),
            candidates,
        )
        self.assertIn(
            ("newapi_user_self", "https://sub2.congmingai.com/api/user/self", None, "bearer"),
            candidates,
        )
        self.assertIn(
            ("newapi_dashboard", "https://sub2.congmingai.com/api/user/dashboard", None, "raw"),
            candidates,
        )

    def test_sanitize_error_body_masks_raw_token_echo(self):
        body = '{"error":"authorization sk-test-secret-token-123456 is invalid","api_key":"sk-test-secret-token-123456"}'

        sanitized = self.mod.sanitize_error_body(body, secrets=["sk-test-secret-token-123456"])

        self.assertNotIn("sk-test-secret-token-123456", sanitized)
        self.assertIn("***", sanitized)


if __name__ == "__main__":
    unittest.main()
