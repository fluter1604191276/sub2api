-- Account statistics image pricing needs operation context so generation, responses, and edit costs can diverge.

ALTER TABLE channel_account_stats_model_pricing
    ADD COLUMN IF NOT EXISTS image_operation VARCHAR(24);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'chk_cas_model_pricing_image_operation'
          AND conrelid = 'channel_account_stats_model_pricing'::regclass
    ) THEN
        ALTER TABLE channel_account_stats_model_pricing
            ADD CONSTRAINT chk_cas_model_pricing_image_operation
            CHECK (image_operation IS NULL OR image_operation IN ('generation', 'responses', 'edit'));
    END IF;
END $$;

COMMENT ON COLUMN channel_account_stats_model_pricing.image_operation IS '账号统计图片定价操作类型；NULL 表示任意图片操作';
