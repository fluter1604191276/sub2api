# Account Image Upstream Cost Design

## Goal

Extend the existing account statistics pricing rules so that image-generation
upstream cost can be configured per account, model, image operation, and image
size. The calculated value must remain an internal account cost and must not
change user-facing prices, group multipliers, quota deduction, or model access.

## Existing Capability

The project already has an account cost override system and must continue to
use it:

- `channel_account_stats_pricing_rules` matches accounts and groups.
- `channel_account_stats_model_pricing` matches platform and model and supports
  token, per-request, and image billing.
- `per_request_price * image_count` already calculates per-image account cost.
- `usage_logs.account_stats_cost` stores the resolved internal cost.
- Account and dashboard reports already prefer `account_stats_cost` over the
  default estimated cost.
- Usage logs already store image count, canonical image size, input/output
  sizes, image-size source and breakdown, inbound endpoint, and upstream
  endpoint.

This feature is therefore an incremental extension of the existing pricing
rules, not a second cost subsystem.

## Scope

### Included

- Configure an image cost rule by:
  - account or group, using the existing rule selector;
  - platform and model, using the existing model matcher;
  - operation: any, native generation, Responses image generation, or image
    edit;
  - size: any, 1K, 2K, or 4K;
  - upstream cost per generated image.
- Multiply the configured unit cost by the actual generated-image count.
- Support mixed-size responses by summing each size bucket from the existing
  `image_size_breakdown` metadata.
- Preserve all existing token and per-request pricing behavior.
- Preserve historical rules without requiring manual migration.

### Excluded

- User-facing image prices and group image prices.
- User quota deduction and subscription consumption.
- Account rate multipliers.
- Video pricing.
- Retrospective rewriting of historical usage logs.
- A separate image-cost table or a separate reporting pipeline.

## Data Model

Add two nullable columns to `channel_account_stats_model_pricing`:

```text
image_operation VARCHAR(24) NULL
image_size_tier VARCHAR(8) NULL
```

Canonical values:

```text
image_operation: generation | responses | edit | NULL
image_size_tier: 1K | 2K | 4K | NULL
```

`NULL` means any operation or any size. Existing rows remain `NULL/NULL`, so
their current behavior is unchanged.

No columns are added to `usage_logs`: the required request and image metadata
already exists.

## Operation Classification

Only requests with `image_count > 0` participate in image-specific matching.
The operation is derived from the normalized inbound endpoint:

| Inbound endpoint | Operation |
| --- | --- |
| `/v1/images/generations` | `generation` |
| `/v1/responses` with generated images | `responses` |
| `/v1/images/edits` | `edit` |
| Other image-producing endpoint | no specific operation |

This derives the cost context from existing normalized request data and avoids
persisting a duplicate operation field in each usage log.

## Size Resolution

The canonical `usageLog.ImageSize` is used for single-size requests. When
`ImageSizeBreakdown` contains multiple size buckets, each bucket is costed
independently using its generated-image count.

If the image size is missing, the resolver may only match a wildcard size
rule. It must not guess 1K, 2K, or 4K inside the account-cost resolver; size
normalization remains owned by the existing image billing pipeline.

## Matching Order

For each matching account/group rule, model matching continues to prefer an
exact model over a wildcard model. Among entries with the same model
specificity, image context is matched in this order:

1. Exact operation + exact size.
2. Exact operation + any size.
3. Any operation + exact size.
4. Any operation + any size.

If no image-context rule matches, the resolver continues through the existing
fallback chain:

1. Existing account statistics custom pricing.
2. Channel `apply_pricing_to_account_stats` estimate.
3. LiteLLM/default model pricing.
4. Existing `total_cost * account_rate_multiplier` report fallback.

An image-specific row with no usable positive `per_request_price` is treated as
not matched. Zero is not accepted as a configured upstream cost because it can
silently create false profit.

## Cost Calculation

For a single-size response:

```text
account_stats_cost = configured_per_image_cost * image_count
```

For a mixed-size response:

```text
account_stats_cost = sum(
  configured_cost(operation, size) * image_size_breakdown[size]
)
```

If any mixed-size bucket cannot resolve through the image-rule fallback order,
the entire image-specific calculation is abandoned and the existing fallback
chain is used. Partial configured cost must not be combined with an unrelated
default estimate.

The stored amount is the raw upstream account cost. The existing reporting
layer may continue applying `account_rate_multiplier` exactly as it does today.
User billing fields such as `total_cost`, `actual_cost`, group image prices, and
rate multipliers are untouched.

## Backend Changes

- Extend `ChannelModelPricing` and account-statistics pricing DTOs with the two
  optional image-context fields.
- Persist and load the fields in the existing account-statistics pricing
  repository.
- Pass the usage log's image metadata and normalized endpoints into
  `resolveAccountStatsCost`.
- Keep token pricing and ordinary per-request pricing on their current paths.
- Add a dedicated image-context matcher used only when `image_count > 0`.
- Keep existing model wildcard semantics and rule ordering.

## Admin UI

Extend the existing "custom account statistics pricing rules" editor in the
channel form. Do not add a new page or another top-level setting.

When billing mode is `image` or `per_request`, show:

- an operation selector: Any, Native generation, Responses bridge, Image edit;
- a size selector: Any, 1K, 2K, 4K;
- the existing per-request price input, labelled as upstream cost per image for
  image mode.

For token mode, hide these selectors and submit both values as null. Existing
rows display Any/Any.

## Validation

- Reject unknown operation and size values at the API boundary.
- Require a positive per-request price for image-specific entries.
- Allow multiple rows for the same model only when their operation/size
  selectors differ.
- Reject exact duplicate model + operation + size combinations within the same
  account-statistics rule.
- Preserve the existing model wildcard conflict checks where applicable.

## Compatibility And Migration

- The migration only adds nullable columns and is reversible.
- Existing rows resolve as Any operation + Any size.
- Existing API clients may omit the new fields.
- Existing channels and usage reports retain current behavior until an
  administrator configures more-specific image cost rows.
- No production data backfill is required.

## Testing

### Backend unit tests

- Existing Any/Any row retains current per-image behavior.
- Exact operation and size outrank wildcard variants.
- Native generation, Responses generation, and image edit classify correctly.
- 1K, 2K, and 4K costs multiply by image count.
- Mixed-size breakdown sums independent buckets.
- Missing mixed-size bucket abandons partial calculation and uses the existing
  fallback chain.
- Non-image token and per-request requests are unchanged.
- User charge fields remain unchanged when account cost differs.

### Repository tests

- New fields round-trip through create, update, and load operations.
- Existing null fields round-trip without behavior changes.

### Frontend tests

- Image controls appear only for image/per-request modes.
- Existing entries load as Any/Any.
- Duplicate combinations are rejected.
- API payload conversion preserves or clears image context correctly.

### Release smoke checks

- Configure one disabled or isolated test account with distinct 1K and 4K
  costs.
- Run one small 1K request and, only when explicitly authorized, one 4K
  request.
- Verify `account_stats_cost` follows configured upstream cost while user
  `total_cost` remains governed by the existing user-facing image price.
- Verify account and dashboard profit statistics use the new internal cost.

## Rollback

Application rollback is sufficient because older binaries ignore the nullable
columns. The migration columns may remain in place. Configuration rollback is
performed by clearing operation/size selectors or restoring the pre-change
channel rules from the production backup.

