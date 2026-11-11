-- Record whether an IQ run was scheduled or explicitly requested by an
-- administrator. Existing rows are scheduled so their historical meaning and
-- interval calculations remain unchanged.
ALTER TABLE openai_iq_runs
    ADD COLUMN IF NOT EXISTS trigger VARCHAR(16) NOT NULL DEFAULT 'scheduled';

ALTER TABLE openai_iq_runs
    ADD COLUMN IF NOT EXISTS requested_by BIGINT NULL REFERENCES users(id);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'openai_iq_runs_trigger_check'
          AND conrelid = 'openai_iq_runs'::regclass
    ) THEN
        ALTER TABLE openai_iq_runs
            ADD CONSTRAINT openai_iq_runs_trigger_check
            CHECK (trigger IN ('scheduled', 'manual'));
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_openai_iq_runs_manual_started
    ON openai_iq_runs (trigger, started_at DESC)
    WHERE trigger = 'manual';
