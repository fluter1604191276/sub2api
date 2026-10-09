-- Preserve historical unlimited quota state (policy B, 2026-10-09).
-- Only active rows with all three limits NULL are archived. Explicit zero or
-- positive limits and already archived rows are unchanged. Retain counters,
-- window starts, IDs, created_at and updated_at; only deleted_at changes.
-- A future first limit creates a fresh active row with zero counters and NULL
-- windows through UpsertForUser; archived state remains available for audit.
-- Idempotent: archived rows no longer match. Never hard-delete quota history.
-- This replacement applies only where this exact migration filename is pending.
-- Previously applied DELETE checksums must fail closed; no checksum exception.

UPDATE user_platform_quotas
   SET deleted_at = CURRENT_TIMESTAMP
 WHERE deleted_at IS NULL
   AND daily_limit_usd IS NULL
   AND weekly_limit_usd IS NULL
   AND monthly_limit_usd IS NULL;
