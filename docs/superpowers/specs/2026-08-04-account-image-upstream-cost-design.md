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

Add one nullable column to `channel_account_stats_model_pricing`:

```text
image_operation VARCHAR(24) NULL
```

Canonical values:

```text
image_operation: generation | responses | edit | NULL
```

`NULL` means any operation. Existing rows remain null, so their current
behavior is unchanged.

Image size continues to use the existing
`channel_account_stats_pricing_intervals.tier_label` and
`channel_account_stats_pricing_intervals.per_request_price` fields. A model
pricing row's existing `per_request_price` is the any-size fallback. No second
image-size column is introduced.

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
| Other endpoint with generated images | `generation` |

This derives the cost context from existing normalized request data and avoids
persisting a duplicate operation field in each usage log. The fallback to
`generation` covers native Gemini, Antigravity, chat-compatible, and other
image-producing routes that do not use OpenAI's image endpoint names.

## Size Resolution

The canonical `usageLog.ImageSize` is used for single-size requests. The
resolver selects an existing pricing interval whose normalized `tier_label`
equals 1K, 2K, or 4K, then uses that interval's `per_request_price`. If there is
no matching interval, the model pricing row's `per_request_price` is the
any-size fallback. When `ImageSizeBreakdown` contains multiple size buckets,
each bucket is costed independently using its generated-image count.

If the image size is missing, the resolver may only use the row-level default
`per_request_price`. It must not guess or select a 1K, 2K, or 4K interval
inside the account-cost resolver; size normalization remains owned by the
existing image billing pipeline.

## Matching Order

For each matching account/group rule, image context is matched in this order:

1. Exact model + exact operation.
2. Exact model + any operation.
3. Wildcard model + exact operation.
4. Wildcard model + any operation.

Image-mode rows are preferred for image requests. Existing per-request rows
remain compatible as any-operation fallbacks, but new operation-specific
configuration is only accepted on image-mode rows. After a model/operation row
is selected, size resolution uses an exact `tier_label` interval first and the
row's default `per_request_price` second. A candidate row must price the whole
request. If it cannot, the resolver tries the next row in the matching order;
it must not combine size prices from different rows.

Within account-statistics custom pricing, image-mode rows are attempted first,
then a legacy any-operation `per_request` row, and then the next matching
account/group rule. If all custom rules miss, the resolver continues through
the existing fallback chain:

1. Channel `apply_pricing_to_account_stats` estimate.
2. LiteLLM/default model pricing.
3. Existing `total_cost * account_rate_multiplier` report fallback.

An image-specific row with neither a usable positive size interval nor a
positive default `per_request_price` is treated as not matched. Zero is not
accepted as a configured upstream cost because it can silently create false
profit.

## Cost Calculation

For a single-size response:

```text
account_stats_cost = resolved_interval_or_default_cost * image_count
```

For a mixed-size response:

```text
account_stats_cost = sum(
  resolved_interval_or_default_cost(operation, size)
  * image_size_breakdown[size]
)
```

If any mixed-size bucket cannot resolve through the image-rule fallback order,
or the sum of positive breakdown counts differs from `image_count`, the entire
image-specific calculation is abandoned and the existing fallback chain is
used. Partial configured cost must not be combined with an unrelated default
estimate or stored as though it covered every generated image.

The stored amount is the raw upstream account cost. The existing reporting
layer may continue applying `account_rate_multiplier` exactly as it does today.
User billing fields such as `total_cost`, `actual_cost`, group image prices, and
rate multipliers are untouched.

## Backend Changes

- Extend `ChannelModelPricing` and account-statistics pricing DTOs with the
  optional image-operation field.
- Persist and load the field only in the existing account-statistics pricing
  repository. Primary channel pricing does not use it.
- Pass the usage log's image metadata and normalized endpoints into
  `resolveAccountStatsCost`.
- Keep token pricing and ordinary per-request pricing on their current paths.
- Add a dedicated image-context matcher used only when `image_count > 0`.
- Reuse `PricingInterval.TierLabel` for size matching and the existing default
  `PerRequestPrice` for any-size fallback.
- Keep existing model wildcard semantics and rule ordering.

## Admin UI

Extend the existing "custom account statistics pricing rules" editor in the
channel form. Do not add a new page or another top-level setting.

When billing mode is `image`, show:

- an operation selector: Any, Native generation, Responses bridge, Image edit;
- the existing per-request price input, labelled as upstream cost per image for
  image mode.
- the existing image-tier editor for 1K, 2K, and 4K size-specific costs.

For all non-image modes, hide the operation selector and submit it as null.
Existing image rows display Any operation and retain their current interval
configuration.

## Validation

- Reject unknown operation values at the API boundary.
- Reject a non-null operation on non-image pricing rows.
- Require at least one positive upstream per-image cost from the row default or
  a size interval. A configured default or interval cost must be positive;
  zero must not silently create false profit.
- Continue validating image interval labels and prices through the existing
  interval validation path.
- Allow multiple rows for the same model only when their operation selectors
  differ.
- Reject exact duplicate model + operation combinations within the same
  account-statistics rule.
- Preserve the existing model wildcard conflict checks where applicable.

## Compatibility And Migration

- The migration only adds one nullable column and is reversible.
- Existing rows resolve as Any operation; existing image intervals continue to
  represent size tiers.
- Existing API clients may omit the new field.
- Existing channels and usage reports retain current behavior until an
  administrator configures more-specific image cost rows.
- No production data backfill is required.

## Testing

### Backend unit tests

- Existing any-operation row retains current per-image behavior.
- Exact model and operation outrank wildcard variants.
- Native generation, Responses generation, and image edit classify correctly.
- Existing 1K, 2K, and 4K pricing intervals multiply by image count.
- Missing size intervals use the existing row-level `per_request_price`.
- Mixed-size breakdown sums independent buckets.
- Missing mixed-size bucket abandons partial calculation and uses the existing
  fallback chain.
- An incomplete operation-specific row falls back to one complete
  any-operation row; costs from the two rows are never combined.
- Mixed-size breakdown whose counts do not equal `image_count` uses the
  existing fallback chain instead of undercounting cost.
- Non-image token and per-request requests are unchanged.
- User charge fields remain unchanged when account cost differs.

### Repository tests

- The new operation field round-trips through create, update, and load
  operations.
- Existing null operation values and pricing intervals round-trip without
  behavior changes.

### Frontend tests

- Image operation controls appear only for image mode.
- Existing entries load as Any operation and preserve image tiers.
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
column. The migration column may remain in place. Configuration rollback is
performed by clearing the operation selector or restoring the pre-change
channel rules from the production backup.
