# Account Image Upstream Cost Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend the existing account-statistics pricing rules so image upstream cost can be resolved per account, model, operation, and existing 1K/2K/4K pricing tier without changing user billing.

**Architecture:** Keep `usage_logs.account_stats_cost` as the only internal-cost result. Add one nullable `image_operation` field to account-statistics model pricing, reuse existing image pricing intervals for size, derive operation from existing usage endpoints, and pass an immutable usage context through the current resolver. Extend the existing channel rule editor instead of adding another pricing page or table.

**Tech Stack:** Go, Gin, PostgreSQL migrations, `database/sql`, Vue 3, TypeScript, Vitest, Docker Compose.

---

## File Map

**Create**

- `backend/migrations/173_account_stats_image_operation.sql`
- `backend/internal/service/account_stats_image_pricing.go`
- `backend/internal/service/account_stats_image_pricing_test.go`
- `backend/internal/repository/channel_repo_account_stats_pricing_test.go`
- `frontend/src/components/admin/channel/accountStatsImageCost.ts`
- `frontend/src/components/admin/channel/__tests__/accountStatsImageCost.spec.ts`
- `frontend/src/components/admin/channel/__tests__/PricingEntryCard.accountStats.spec.ts`

**Modify**

- `backend/internal/service/channel.go`
- `backend/internal/service/account_stats_pricing.go`
- `backend/internal/service/account_stats_pricing_test.go`
- `backend/internal/service/channel_service.go`
- `backend/internal/service/channel_service_test.go`
- `backend/internal/repository/channel_repo_account_stats_pricing.go`
- `backend/internal/handler/admin/channel_handler.go`
- `backend/internal/handler/admin/channel_handler_test.go`
- `backend/internal/service/openai_gateway_record_usage_test.go`
- `backend/internal/service/gateway_record_usage_test.go`
- `frontend/src/api/admin/channels.ts`
- `frontend/src/components/admin/channel/types.ts`
- `frontend/src/components/admin/channel/PricingEntryCard.vue`
- `frontend/src/views/admin/ChannelsView.vue`
- `frontend/src/i18n/locales/en/admin/channels.ts`
- `frontend/src/i18n/locales/zh/admin/channels.ts`

---

### Task 1: Persist One Operation Dimension And Reuse Existing Size Tiers

**Files:**
- Create: `backend/migrations/173_account_stats_image_operation.sql`
- Modify: `backend/internal/service/channel.go:84-100`
- Modify: `backend/internal/repository/channel_repo_account_stats_pricing.go:60-235`
- Test: `backend/internal/repository/channel_repo_account_stats_pricing_test.go`

- [ ] **Step 1: Write the migration**

```sql
ALTER TABLE channel_account_stats_model_pricing
    ADD COLUMN IF NOT EXISTS image_operation VARCHAR(24);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'channel_account_stats_model_pricing_image_operation_check'
          AND conrelid = 'channel_account_stats_model_pricing'::regclass
    ) THEN
        ALTER TABLE channel_account_stats_model_pricing
            ADD CONSTRAINT channel_account_stats_model_pricing_image_operation_check
            CHECK (
                image_operation IS NULL
                OR image_operation IN ('generation', 'responses', 'edit')
            ) NOT VALID;
    END IF;
END $$;

ALTER TABLE channel_account_stats_model_pricing
    VALIDATE CONSTRAINT channel_account_stats_model_pricing_image_operation_check;

COMMENT ON COLUMN channel_account_stats_model_pricing.image_operation IS
    'Optional image cost scope: generation, responses, or edit; NULL matches any operation';
```

- [ ] **Step 2: Add the domain type and field**

Add to `backend/internal/service/channel.go`:

```go
type AccountStatsImageOperation string

const (
	AccountStatsImageOperationAny        AccountStatsImageOperation = ""
	AccountStatsImageOperationGeneration AccountStatsImageOperation = "generation"
	AccountStatsImageOperationResponses  AccountStatsImageOperation = "responses"
	AccountStatsImageOperationEdit       AccountStatsImageOperation = "edit"
)

func (o AccountStatsImageOperation) IsValid() bool {
	switch o {
	case AccountStatsImageOperationAny,
		AccountStatsImageOperationGeneration,
		AccountStatsImageOperationResponses,
		AccountStatsImageOperationEdit:
		return true
	default:
		return false
	}
}
```

Add `ImageOperation AccountStatsImageOperation` to `ChannelModelPricing`. Do not add a size field; `PricingInterval.TierLabel` remains the configured 1K/2K/4K dimension.

- [ ] **Step 3: Write failing repository tests**

Create SQLMock tests:

```go
func TestBatchLoadAccountStatsModelPricing_LoadsImageOperation(t *testing.T)
func TestBatchLoadAccountStatsModelPricing_NullImageOperationLoadsAsAny(t *testing.T)
func TestCreateAccountStatsModelPricingTx_PersistsImageOperation(t *testing.T)
func TestCreateAccountStatsModelPricingTx_AnyImageOperationPersistsNull(t *testing.T)
```

The first load test returns `image_operation="responses"` and asserts `AccountStatsImageOperationResponses`; the null test returns the SQL projection `""` and asserts `AccountStatsImageOperationAny`. The first insert test expects the string `edit`; the Any-operation test expects `nil` in the `INSERT` arguments. Include the existing interval query/insert expectations and call `mock.ExpectationsWereMet()`.

- [ ] **Step 4: Verify the tests fail before implementation**

```bash
cd backend
go test -tags=unit ./internal/repository -run 'Test(BatchLoadAccountStatsModelPricing|CreateAccountStatsModelPricingTx)' -count=1
```

Expected: FAIL because repository SQL does not include `image_operation`.

- [ ] **Step 5: Update account-statistics repository SQL**

Select and scan `COALESCE(image_operation, '')` after `per_request_price`, so historical null rows load as `AccountStatsImageOperationAny`. Add this helper:

```go
func nullableAccountStatsImageOperation(operation service.AccountStatsImageOperation) any {
	if operation == service.AccountStatsImageOperationAny {
		return nil
	}
	return string(operation)
}
```

Add `image_operation` to `createAccountStatsModelPricingTx` and pass `nullableAccountStatsImageOperation(pricing.ImageOperation)` as its argument. Do not write an empty string and do not change primary `channel_model_pricing` queries.

- [ ] **Step 6: Verify repository and migration tests**

```bash
cd backend
go test -tags=unit ./internal/repository -run 'Test(BatchLoadAccountStatsModelPricing|CreateAccountStatsModelPricingTx)' -count=1
go test ./migrations -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit Task 1 only**

```bash
git add backend/migrations/173_account_stats_image_operation.sql backend/internal/service/channel.go backend/internal/repository/channel_repo_account_stats_pricing.go backend/internal/repository/channel_repo_account_stats_pricing_test.go
git commit -m "Keep image cost context inside account statistics pricing" \
  -m "Constraint: Reuse existing pricing intervals for image size instead of adding another size column." \
  -m "Confidence: high" \
  -m "Scope-risk: narrow" \
  -m "Tested: Account statistics pricing repository and migration tests."
```

---

### Task 2: Add Account-Statistics-Specific Validation And DTO Transport

**Files:**
- Modify: `backend/internal/service/channel_service.go:592-655,700-766,938-952`
- Modify: `backend/internal/service/channel_service_test.go`
- Modify: `backend/internal/handler/admin/channel_handler.go:59-121,199-280`
- Modify: `backend/internal/handler/admin/channel_handler_test.go`

- [ ] **Step 1: Write failing validation tests**

Add `TestValidateAccountStatsPricingEntries_ImageOperation` with these cases:

```text
PASS: same model in token mode and responses image mode
PASS: same image model in generation and edit operations
PASS: image row with nil default and a positive 1K tier
FAIL MODEL_PATTERN_CONFLICT: duplicate model and operation
FAIL IMAGE_OPERATION_REQUIRES_IMAGE_MODE: operation on token/per_request row
FAIL INVALID_IMAGE_OPERATION: unknown operation
FAIL IMAGE_COST_MUST_BE_POSITIVE: no positive default/tier, or a configured default/tier is zero
```

- [ ] **Step 2: Verify validation tests fail**

```bash
cd backend
go test -tags=unit ./internal/service -run TestValidateAccountStatsPricingEntries -count=1
```

Expected: FAIL because account-statistics pricing still uses the main pricing conflict validator.

- [ ] **Step 3: Implement scoped validation**

```go
func validateAccountStatsPricingEntries(pricing []ChannelModelPricing) error {
	if err := validatePricingIntervals(pricing); err != nil {
		return err
	}
	if err := validatePricingBillingMode(pricing); err != nil {
		return err
	}
	if err := validateAccountStatsImageOperations(pricing); err != nil {
		return err
	}
	return validateAccountStatsModelScopes(pricing)
}
```

`validateAccountStatsImageOperations` must reject invalid values and non-image rows with an operation. For every image row, require at least one positive `PerRequestPrice` from the row default or its intervals; nil defaults are valid when a positive tier exists, but any configured default or interval `PerRequestPrice` must be greater than zero. `validateAccountStatsModelScopes` groups patterns by platform and conflict scope before calling existing `detectConflicts`: every non-image billing mode shares one `non-image` scope, while image rows use `image:<operation>` (with an empty suffix for Any operation). This preserves the existing token/per-request duplicate checks while allowing image rows and distinct image operations to coexist. Replace only the account-statistics validation loops in channel `Create` and `Update`; keep primary pricing validation unchanged.

- [ ] **Step 4: Transport the operation through admin DTOs**

Add to the request type so Gin validates incoming values:

```go
ImageOperation string `json:"image_operation,omitempty" binding:"omitempty,oneof=generation responses edit"`
```

Add to the response type without a binding tag:

```go
ImageOperation string `json:"image_operation,omitempty"`
```

Map request to service:

```go
ImageOperation: service.AccountStatsImageOperation(r.ImageOperation),
```

Map service to response:

```go
ImageOperation: string(p.ImageOperation),
```

Add a primary-channel validation error when `ModelPricing` contains a non-empty operation so the shared DTO cannot silently discard unsupported configuration.

- [ ] **Step 5: Add DTO tests**

Test request-to-service and service-to-response round trips for `responses`, test omitted values remain empty, and test Gin binding rejects `image_operation: "other"` with HTTP 400.

- [ ] **Step 6: Run service and handler tests**

```bash
cd backend
go test -tags=unit ./internal/service -run 'TestValidate(AccountStatsPricingEntries|Pricing)' -count=1
go test -tags=unit ./internal/handler/admin -run 'Test.*Channel.*ImageOperation' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit Task 2**

```bash
git add backend/internal/service/channel_service.go backend/internal/service/channel_service_test.go backend/internal/handler/admin/channel_handler.go backend/internal/handler/admin/channel_handler_test.go
git commit -m "Prevent ambiguous account image cost rules" \
  -m "Constraint: Token and image cost rows for the same model must coexist without weakening duplicate detection within one scope." \
  -m "Confidence: high" \
  -m "Scope-risk: moderate" \
  -m "Tested: Channel validation and admin DTO unit tests."
```

---

### Task 3: Resolve Operation, Model Specificity, And Existing Size Intervals

**Files:**
- Create: `backend/internal/service/account_stats_image_pricing.go`
- Create: `backend/internal/service/account_stats_image_pricing_test.go`
- Modify: `backend/internal/service/account_stats_pricing.go:19-240`
- Modify: `backend/internal/service/account_stats_pricing_test.go`

- [ ] **Step 1: Write failing operation tests**

```go
func TestDeriveAccountStatsImageOperation(t *testing.T) {
	tests := []struct {
		imageCount int
		endpoint   string
		want       AccountStatsImageOperation
	}{
		{0, "/v1/responses", AccountStatsImageOperationAny},
		{1, "/v1/responses", AccountStatsImageOperationResponses},
		{1, "/v1/images/edits", AccountStatsImageOperationEdit},
		{1, "/v1/images/generations", AccountStatsImageOperationGeneration},
		{1, "/v1beta/models", AccountStatsImageOperationGeneration},
		{1, "/v1/chat/completions", AccountStatsImageOperationGeneration},
	}
	for _, tt := range tests {
		require.Equal(t, tt.want, deriveAccountStatsImageOperation(tt.imageCount, tt.endpoint))
	}
}
```

- [ ] **Step 2: Write failing matcher and calculation tests**

Add these exact tests:

```go
func TestFindImagePricing_ExactModelAndOperationWins(t *testing.T)
func TestFindImagePricing_ExactModelAnyOperationBeatsWildcardExactOperation(t *testing.T)
func TestCalculateAccountStatsImageCost_UsesExactTier(t *testing.T)
func TestCalculateAccountStatsImageCost_UsesDefaultWhenTierMissing(t *testing.T)
func TestCalculateAccountStatsImageCost_SumsMixedSizeBreakdown(t *testing.T)
func TestCalculateAccountStatsImageCost_RejectsPartialMixedSizeCost(t *testing.T)
func TestCalculateAccountStatsImageCost_RejectsIncompleteBreakdownCount(t *testing.T)
func TestResolveAccountStatsImageCost_UnusableSpecificRowFallsBackAsWholeRequest(t *testing.T)
func TestCalculateAccountStatsImageCost_LegacyPerRequestFallback(t *testing.T)
```

Use `1K=0.04`, `2K=0.08`, `4K=0.16`; mixed `{"1K":2,"4K":1}` must cost `0.24`. For whole-request fallback, give the exact `responses` row only a 1K tier and the Any-operation row both 1K and 4K tiers; a mixed 1K/4K request must use the Any-operation row for both buckets, never one bucket from each row.

- [ ] **Step 3: Verify focused tests fail**

```bash
cd backend
go test -tags=unit ./internal/service -run 'Test(DeriveAccountStatsImageOperation|FindImagePricing|CalculateAccountStatsImageCost)' -count=1
```

Expected: FAIL because these helpers do not exist.

- [ ] **Step 4: Add an immutable usage context**

```go
type AccountStatsUsageContext struct {
	Tokens             UsageTokens
	ImageCount         int
	ImageSize          string
	ImageSizeBreakdown map[string]int
	InboundEndpoint    string
}

func (c AccountStatsUsageContext) RequestCount() int {
	if c.ImageCount > 0 {
		return c.ImageCount
	}
	return 1
}
```

Change `resolveAccountStatsCost` and `tryCustomRules` to receive this context. Existing non-image tests use `AccountStatsUsageContext{Tokens: tokens}`.

- [ ] **Step 5: Implement operation classification**

```go
func deriveAccountStatsImageOperation(imageCount int, endpoint string) AccountStatsImageOperation {
	if imageCount <= 0 {
		return AccountStatsImageOperationAny
	}
	switch strings.TrimSpace(endpoint) {
	case "/v1/responses":
		return AccountStatsImageOperationResponses
	case "/v1/images/edits":
		return AccountStatsImageOperationEdit
	default:
		return AccountStatsImageOperationGeneration
	}
}
```

- [ ] **Step 6: Implement deterministic image row selection**

Evaluate candidate rows in four passes in this order:

```text
exact model + exact operation
exact model + any operation
wildcard model + exact operation
wildcard model + any operation
```

Only `BillingModeImage` participates in these passes. A candidate is accepted only if `calculateAccountStatsImageCost` resolves the entire request; otherwise continue to the next candidate without retaining a partial sum. After all four image passes miss, one legacy pass may use `BillingModePerRequest` with empty operation. Non-image requests must skip `BillingModeImage` rows.

- [ ] **Step 7: Implement exact-tier then default cost**

```go
func imageUnitCost(p *ChannelModelPricing, size string) (float64, bool) {
	if size != "" {
		if tier := p.GetTierByLabel(size); tier != nil && tier.PerRequestPrice != nil && *tier.PerRequestPrice > 0 {
			return *tier.PerRequestPrice, true
		}
	}
	if p.PerRequestPrice != nil && *p.PerRequestPrice > 0 {
		return *p.PerRequestPrice, true
	}
	return 0, false
}
```

For `ImageSizeBreakdown`, resolve every positive bucket against the same candidate row and sum `unit * count`. Before returning the mixed-size cost, require the sum of all positive bucket counts to equal `ImageCount`. If a bucket has neither an exact tier nor a default, or the count sum differs, return nil for the entire candidate calculation. For a single size, multiply the resolved unit by `ImageCount`.

- [ ] **Step 8: Preserve the current fallback chain**

Inside each matching account/group rule, use the image matcher only when `ImageCount > 0`; otherwise retain token/per-request calculation. If image matching or costing returns nil, continue to the next rule and then the existing chain: `apply_pricing_to_account_stats`, LiteLLM, report fallback.

- [ ] **Step 9: Run focused and existing resolver tests**

```bash
cd backend
go test -tags=unit ./internal/service -run 'Test(DeriveAccountStatsImageOperation|FindImagePricing|CalculateAccountStatsImageCost|ResolveAccountStatsCost|TryCustomRules|CalculateStatsCost)' -count=1
```

Expected: PASS.

- [ ] **Step 10: Commit Task 3**

```bash
git add backend/internal/service/account_stats_image_pricing.go backend/internal/service/account_stats_image_pricing_test.go backend/internal/service/account_stats_pricing.go backend/internal/service/account_stats_pricing_test.go
git commit -m "Measure image upstream cost from real request context" \
  -m "Constraint: Mixed-size requests must never combine partial configured cost with an unrelated estimate." \
  -m "Rejected: A new image-size field | Existing pricing interval labels already represent 1K, 2K, and 4K." \
  -m "Confidence: high" \
  -m "Scope-risk: moderate" \
  -m "Tested: Operation, matching, tier, mixed-size, and fallback unit tests."
```

---

### Task 4: Wire Existing Usage Metadata And Prove Billing Isolation

**Files:**
- Modify: `backend/internal/service/account_stats_pricing.go:217-240`
- Verify: `backend/internal/service/gateway_usage_billing.go:696-715,900-945`
- Verify: `backend/internal/service/openai_gateway_usage.go:225-315`
- Modify: `backend/internal/service/openai_gateway_record_usage_test.go`
- Modify: `backend/internal/service/gateway_record_usage_test.go`

- [ ] **Step 1: Write failing usage-record tests**

Configure user image price `0.20` and account 1K upstream cost `0.04`. Record one Responses image and assert:

```go
require.NotNil(t, usageRepo.lastLog.AccountStatsCost)
require.InDelta(t, 0.04, *usageRepo.lastLog.AccountStatsCost, 1e-12)
require.InDelta(t, 0.20, usageRepo.lastLog.TotalCost, 1e-12)
require.Equal(t, 1, usageRepo.lastLog.ImageCount)
require.Equal(t, "1K", *usageRepo.lastLog.ImageSize)
require.Equal(t, "/v1/responses", *usageRepo.lastLog.InboundEndpoint)
```

Add the generic gateway equivalent for a native image endpoint and generation operation.

- [ ] **Step 2: Verify usage tests fail**

```bash
cd backend
go test -tags=unit ./internal/service -run 'Test(OpenAIGatewayServiceRecordUsage|GatewayServiceRecordUsage).*AccountStatsImage' -count=1
```

Expected: FAIL because `applyAccountStatsCost` does not yet pass image metadata.

- [ ] **Step 3: Build the context from `UsageLog`**

```go
usage := AccountStatsUsageContext{Tokens: tokens}
if usageLog != nil {
	usage.ImageCount = usageLog.ImageCount
	if usageLog.ImageSize != nil {
		usage.ImageSize = strings.TrimSpace(*usageLog.ImageSize)
	}
	usage.ImageSizeBreakdown = usageLog.ImageSizeBreakdown
	if usageLog.InboundEndpoint != nil {
		usage.InboundEndpoint = strings.TrimSpace(*usageLog.InboundEndpoint)
	}
}
usageLog.AccountStatsCost = resolveAccountStatsCost(
	ctx, cs, bs, accountID, groupID, model, usage, totalCost,
)
```

Do not modify user cost calculation, group image prices, multipliers, quota updates, or balance deduction.

- [ ] **Step 4: Run record-usage and image-billing tests**

```bash
cd backend
go test -tags=unit ./internal/service -run 'Test.*(AccountStatsImage|ImageBilling|RecordUsage)' -count=1
```

Expected: PASS with different `account_stats_cost` and `total_cost`.

- [ ] **Step 5: Commit Task 4**

```bash
git add backend/internal/service/account_stats_pricing.go backend/internal/service/openai_gateway_record_usage_test.go backend/internal/service/gateway_record_usage_test.go
git commit -m "Separate configured image cost from the user charge" \
  -m "Constraint: Existing user prices, quota deduction, and group multipliers must remain untouched." \
  -m "Confidence: high" \
  -m "Scope-risk: moderate" \
  -m "Tested: OpenAI and generic gateway usage recording with distinct account and user costs."
```

---

### Task 5: Extend The Existing Account-Statistics Rule Editor

**Files:**
- Modify: `frontend/src/api/admin/channels.ts:8-46`
- Modify: `frontend/src/components/admin/channel/types.ts:1-30`
- Create: `frontend/src/components/admin/channel/accountStatsImageCost.ts`
- Create: `frontend/src/components/admin/channel/__tests__/accountStatsImageCost.spec.ts`
- Modify: `frontend/src/components/admin/channel/PricingEntryCard.vue:70-265`
- Create: `frontend/src/components/admin/channel/__tests__/PricingEntryCard.accountStats.spec.ts`
- Modify: `frontend/src/views/admin/ChannelsView.vue:694-713,1127-1148,1241-1264,1539-1579,1673-1699`
- Modify: `frontend/src/i18n/locales/en/admin/channels.ts`
- Modify: `frontend/src/i18n/locales/zh/admin/channels.ts`

- [ ] **Step 1: Add API and form types**

```ts
export type AccountStatsImageOperation = 'generation' | 'responses' | 'edit'
```

Add `image_operation?: AccountStatsImageOperation | null` to `ChannelModelPricing` and `PricingFormEntry`.

- [ ] **Step 2: Write failing conflict-helper tests**

Test that token + image rows for the same model pass, generation + edit rows pass, token + ordinary per-request rows for the same model still conflict, and duplicate model + operation returns the conflicting pair.

- [ ] **Step 3: Implement options and scoped conflict detection**

```ts
export const accountStatsImageOperationOptions = [
  { value: '', i18nKey: 'admin.channels.form.imageOperationAny' },
  { value: 'generation', i18nKey: 'admin.channels.form.imageOperationGeneration' },
  { value: 'responses', i18nKey: 'admin.channels.form.imageOperationResponses' },
  { value: 'edit', i18nKey: 'admin.channels.form.imageOperationEdit' },
] as const

export function findAccountStatsPricingConflict(entries: PricingFormEntry[]): [string, string] | null {
  const scopes = new Map<string, string[]>()
  for (const item of entries) {
    const scope = item.billing_mode === 'image'
      ? `image:${item.image_operation || ''}`
      : 'non-image'
    scopes.set(scope, [...(scopes.get(scope) || []), ...item.models])
  }
  for (const models of scopes.values()) {
    const conflict = findModelConflict(models)
    if (conflict) return conflict
  }
  return null
}
```

- [ ] **Step 4: Run helper tests**

```bash
cd frontend
pnpm test:run src/components/admin/channel/__tests__/accountStatsImageCost.spec.ts
```

Expected: PASS.

- [ ] **Step 5: Add the conditional operation selector**

Add `accountStats?: boolean` to `PricingEntryCard`, default false. Show the operation `Select` only when `accountStats && entry.billing_mode === 'image'`. On changing away from image mode, emit `image_operation: null`. Label the existing image price as “upstream cost per image” only in account-statistics mode.

- [ ] **Step 6: Add component tests**

Assert the selector is hidden for primary image pricing, shown for account-statistics image pricing, and hidden for account-statistics token pricing.

- [ ] **Step 7: Wire `ChannelsView`**

Pass `account-stats` only to the account-statistics card at `ChannelsView.vue:705`. Initialize with `image_operation: null`, serialize only for image mode, hydrate omitted values as null, and run `findAccountStatsPricingConflict` before submit. Require at least one positive default/tier image cost; allow a nil default when a positive tier exists, and reject any configured zero or negative default/tier image cost.

- [ ] **Step 8: Add English and Chinese labels**

Add copy for operation label, Any, Native generation, Responses bridge, Image edit, upstream cost per image, duplicate combination, and positive-cost validation. Chinese labels must use: `图片操作`、`任意操作`、`原生生图`、`Responses 生图桥接`、`图生图/编辑`、`上游单张成本`.

- [ ] **Step 9: Run frontend verification**

```bash
cd frontend
pnpm test:run src/components/admin/channel/__tests__/accountStatsImageCost.spec.ts src/components/admin/channel/__tests__/PricingEntryCard.accountStats.spec.ts src/components/admin/channel/__tests__/types.spec.ts
pnpm typecheck
pnpm lint:check
```

Expected: PASS and exit 0.

- [ ] **Step 10: Commit Task 5**

```bash
git add frontend/src/api/admin/channels.ts frontend/src/components/admin/channel/types.ts frontend/src/components/admin/channel/accountStatsImageCost.ts frontend/src/components/admin/channel/__tests__/accountStatsImageCost.spec.ts frontend/src/components/admin/channel/PricingEntryCard.vue frontend/src/components/admin/channel/__tests__/PricingEntryCard.accountStats.spec.ts frontend/src/views/admin/ChannelsView.vue frontend/src/i18n/locales/en/admin/channels.ts frontend/src/i18n/locales/zh/admin/channels.ts
git commit -m "Expose image upstream cost in the existing rule editor" \
  -m "Constraint: Keep primary channel pricing and user image prices separate from internal account cost." \
  -m "Confidence: high" \
  -m "Scope-risk: moderate" \
  -m "Tested: Frontend helper and component tests, typecheck, and ESLint check."
```

---

### Task 6: Full Regression, Candidate Build, And Production Verification

**Files:**
- Verify all files changed in Tasks 1-5.
- Production directory: `/www/sub2api`
- Production SSH alias: `fluterapi-prod`

- [ ] **Step 1: Format and run targeted backend tests**

```bash
cd backend
gofmt -w internal/service/channel.go internal/service/account_stats_image_pricing.go internal/service/account_stats_image_pricing_test.go internal/service/account_stats_pricing.go internal/service/account_stats_pricing_test.go internal/service/channel_service.go internal/service/channel_service_test.go internal/repository/channel_repo_account_stats_pricing.go internal/repository/channel_repo_account_stats_pricing_test.go internal/handler/admin/channel_handler.go internal/handler/admin/channel_handler_test.go
go test -tags=unit ./internal/service ./internal/repository ./internal/handler/admin -count=1
```

Expected: PASS.

- [ ] **Step 2: Run complete backend and frontend checks**

```bash
cd backend
go test -tags=unit ./... -count=1
cd ../frontend
pnpm test:run
pnpm typecheck
pnpm build
```

Expected: PASS and successful production frontend build.

- [ ] **Step 3: Inspect the final feature diff**

```bash
git diff --check
git status --short
git diff HEAD~5..HEAD -- backend/migrations/173_account_stats_image_operation.sql backend/internal/service/account_stats_image_pricing.go backend/internal/service/account_stats_pricing.go backend/internal/service/channel_service.go backend/internal/repository/channel_repo_account_stats_pricing.go backend/internal/handler/admin/channel_handler.go frontend/src/components/admin/channel/PricingEntryCard.vue frontend/src/views/admin/ChannelsView.vue
```

Expected: one nullable database field, no second pricing table, and no user-billing formula changes.

- [ ] **Step 4: Build a release candidate without switching production**

Build `fluter/sub2api:fluter-account-image-cost-20260804-rc1` through the established production Docker build path. Record its digest and validate migration 173 plus `/health` against an isolated candidate container or disposable database copy.

- [ ] **Step 5: Before production write, verify role and create backups**

```bash
ssh fluterapi-prod 'cat /etc/fluterapi-node-role'
ssh fluterapi-prod 'cd /www/sub2api && docker compose ps'
curl -fsS https://api.fluterapi.top/health
```

Expected role: exactly `production`; service healthy. Run the established Sub2API backup job, verify its local SHA-256, and preserve current Compose plus image tag before switching.

- [ ] **Step 6: Switch only after the explicit deployment gate**

Change only the `sub2api` image tag, run `docker compose up -d --no-deps sub2api`, and wait for healthy status. Do not restart PostgreSQL, Redis, Caddy, S2A Manager, or the legacy 8443 proxy.

- [ ] **Step 7: Run no-cost public smoke checks**

```bash
curl -fsS https://api.fluterapi.top/health
curl -fsSI https://fluterapi.top/
curl -fsSI https://fluterapi.top/docs/
curl -fsS https://img-api.fluterapi.top/health
```

Expected: configured endpoints return 200 and the image domain remains route-restricted.

- [ ] **Step 8: Run one authorized 1K billing smoke**

On an isolated account, configure a verified 1K upstream cost and run one small image. Verify the new row has configured `account_stats_cost`, existing user `total_cost`, `image_count=1`, `image_size=1K`, and the expected inbound endpoint. Delete a temporary test key. Run a 4K smoke only with separate authorization because it costs more.

- [ ] **Step 9: Verify reports and rollback evidence**

Confirm account and dashboard profit use the configured internal cost while user charging remains unchanged. Observe health and errors for 30 minutes. Retain the previous image, database backup, Compose snapshot, and candidate digest; rollback by restoring only the previous image tag. Leave nullable migration column 173 in place.

---

## Completion Criteria

- `generation`, `responses`, and `edit` image costs are independently configurable.
- Existing intervals provide 1K/2K/4K prices; no duplicate size column or pricing table exists.
- Exact model/operation and wildcard fallback order is deterministic.
- Mixed-size output is fully configured or fully falls back; partial cost is never stored.
- Historical null-operation and legacy per-request rules remain compatible.
- `account_stats_cost` reflects upstream cost while user charge, quota, group price, and multipliers remain unchanged.
- Backend unit tests, frontend tests, typecheck, lint check, and build pass.
- Production backup, health checks, one authorized 1K smoke, and rollback evidence are recorded.
