# 二开登记：Astra 长上下文阶梯计费

```text
Capability ID: pricing-calibration
Business purpose: 为 gpt-6-astra 补齐官方标准、Fast/priority、缓存读写和超过 272K 上下文的阶梯价格，避免长上下文请求按基础价少收。
Backend/frontend files: backend/internal/service/openai_model_alias.go; backend/internal/service/billing_service.go; backend/internal/service/pricing_service.go; backend/resources/model-pricing/model_prices_and_context_window.json; related service tests.
Routes or jobs: OpenAI Responses; Chat Completions billing and pricing resolution paths. No new route or scheduled job.
Database migration/data dependency: none. Existing group LongContextPricingEnabled and account extra.openai_long_context_billing_enabled controls remain authoritative.
Environment-variable/config dependency: none.
Billing impact: user charge
Scheduling impact: none
Client protocol impact: OpenAI Responses; Chat Completions
Upstream version/base commit: 4a9f967c3
Tests/fixtures: exact Astra aliases; unknown GPT-6 rejection; static fallback; pricing catalog; standard/priority/flex; cache-write completion; 272K exclusive boundary.
Image smoke evidence: pending candidate image build.
First release manifest: pending candidate release manifest.
Rollback note: restore the previous production image and Compose; no schema rollback or data migration is required. Do not enable a group/account billing gate without a current production backup and pricing verification.
Owner/status: fluter / candidate implementation, not released to production
```

Pricing boundary:

- Official standard prices per MTok: input USD 10, cache read USD 1, cache
  write USD 12.50, output USD 50.
- Fast/priority is 2x standard before the long-context schedule is applied.
- Long context is only when `input + cache creation + cache read > 272000`.
  Input, cache read, and cache creation are multiplied by 2x; output by 1.5x.
- Only the exact model name, its `gpt6-astra` spelling variant, provider path,
  and 8-digit dated snapshots are recognized. Other `gpt-6-*` identifiers must
  obtain their own official pricing record.

This record contains no keys, cookies, bearer tokens, passwords, production
usage, or raw request bodies.
