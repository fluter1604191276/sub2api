-- Group-scoped recovery probes are opt-in. They periodically re-check accounts
-- that have had no successful real traffic for one hour and persist a model-
-- scoped recovery state for the smart scheduler.

ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS recovery_probe_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS recovery_probe_mode VARCHAR(16) NOT NULL DEFAULT 'smart',
    ADD COLUMN IF NOT EXISTS recovery_probe_model VARCHAR(200) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS recovery_probe_interval_seconds INTEGER NOT NULL DEFAULT 900,
    ADD COLUMN IF NOT EXISTS recovery_probe_attempts_per_round INTEGER NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS recovery_probe_idle_threshold_seconds INTEGER NOT NULL DEFAULT 3600,
    ADD COLUMN IF NOT EXISTS recovery_probe_backoff_cap_seconds INTEGER NOT NULL DEFAULT 1800;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'groups_recovery_probe_mode_check') THEN
        ALTER TABLE groups ADD CONSTRAINT groups_recovery_probe_mode_check
            CHECK (recovery_probe_mode IN ('manual', 'smart'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'groups_recovery_probe_interval_check') THEN
        ALTER TABLE groups ADD CONSTRAINT groups_recovery_probe_interval_check
            CHECK (recovery_probe_interval_seconds BETWEEN 60 AND 86400);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'groups_recovery_probe_attempts_check') THEN
        ALTER TABLE groups ADD CONSTRAINT groups_recovery_probe_attempts_check
            CHECK (recovery_probe_attempts_per_round BETWEEN 1 AND 5);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'groups_recovery_probe_idle_check') THEN
        ALTER TABLE groups ADD CONSTRAINT groups_recovery_probe_idle_check
            CHECK (recovery_probe_idle_threshold_seconds = 3600);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'groups_recovery_probe_backoff_cap_check') THEN
        ALTER TABLE groups ADD CONSTRAINT groups_recovery_probe_backoff_cap_check
            CHECK (recovery_probe_backoff_cap_seconds BETWEEN 60 AND 86400);
    END IF;
END $$;

CREATE TABLE IF NOT EXISTS group_recovery_probe_states (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    model VARCHAR(200) NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    consecutive_successes INTEGER NOT NULL DEFAULT 0,
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    last_probe_at TIMESTAMPTZ,
    next_probe_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_success_at TIMESTAMPTZ,
    last_failure_at TIMESTAMPTZ,
    last_error_class VARCHAR(16) NOT NULL DEFAULT '',
    last_error TEXT NOT NULL DEFAULT '',
    latency_ms BIGINT NOT NULL DEFAULT 0,
    probe_count BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT group_recovery_probe_states_unique UNIQUE (group_id, account_id, model),
    CONSTRAINT group_recovery_probe_states_status_check
        CHECK (status IN ('pending', 'probing', 'warm', 'eligible', 'failed', 'paused')),
    CONSTRAINT group_recovery_probe_states_error_class_check
        CHECK (last_error_class IN ('', 'transient', 'permanent')),
    CONSTRAINT group_recovery_probe_states_successes_check CHECK (consecutive_successes >= 0),
    CONSTRAINT group_recovery_probe_states_failures_check CHECK (consecutive_failures >= 0),
    CONSTRAINT group_recovery_probe_states_probe_count_check CHECK (probe_count >= 0)
);

CREATE INDEX IF NOT EXISTS idx_group_recovery_probe_states_due
    ON group_recovery_probe_states (next_probe_at, updated_at, id);

CREATE INDEX IF NOT EXISTS idx_group_recovery_probe_states_group_model
    ON group_recovery_probe_states (group_id, model, account_id);

COMMENT ON TABLE group_recovery_probe_states IS
    'Model-scoped recovery probe state used by opt-in group smart scheduling';
