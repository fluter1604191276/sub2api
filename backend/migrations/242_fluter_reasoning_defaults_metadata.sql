-- Follow-up metadata only: keep upstream migration 239 and its checksum intact.
-- No implicit multiplier is materialized in stored JSON. The application resolves
-- missing/cleared levels against its production model defaults. Explicit max=1
-- disables the Fable 5.1 default surcharge. Legacy explicit values were converted
-- by 239; a canonical map, including {}, supersedes the old scalar field.
COMMENT ON COLUMN channel_model_pricing.reasoning_effort_multipliers IS
    'Explicit reasoning overrides; omitted/cleared levels use model defaults (Fable 5.1 max=3, others=1); explicit max=1 disables surcharge';
COMMENT ON COLUMN channel_account_stats_model_pricing.reasoning_effort_multipliers IS
    'Explicit account-cost reasoning overrides; omitted/cleared levels use model defaults (Fable 5.1 max=3, others=1); explicit max=1 disables surcharge';
