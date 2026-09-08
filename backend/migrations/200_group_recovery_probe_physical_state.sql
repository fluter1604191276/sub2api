-- Make the account/model probe identity physical and globally unique.
-- Group rows remain projections for group-level display and scheduling context.

CREATE TABLE IF NOT EXISTS group_recovery_probe_physical_states (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    model_key VARCHAR(200) NOT NULL,
    model VARCHAR(200) NOT NULL,
    owner_group_id BIGINT REFERENCES groups(id) ON DELETE SET NULL,
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
	CONSTRAINT group_recovery_probe_physical_unique UNIQUE (account_id, model_key),
	CONSTRAINT group_recovery_probe_physical_model_key_check CHECK (
		model_key = LOWER(BTRIM(model)) AND model_key <> ''
	),
    CONSTRAINT group_recovery_probe_physical_status_check CHECK (
        status IN ('pending', 'probing', 'warm', 'eligible', 'failed', 'paused')
    ),
    CONSTRAINT group_recovery_probe_physical_error_class_check CHECK (
        last_error_class IN ('', 'transient', 'permanent')
    ),
    CONSTRAINT group_recovery_probe_physical_successes_check CHECK (consecutive_successes >= 0),
    CONSTRAINT group_recovery_probe_physical_failures_check CHECK (consecutive_failures >= 0),
    CONSTRAINT group_recovery_probe_physical_count_check CHECK (probe_count >= 0)
);

CREATE INDEX IF NOT EXISTS idx_group_recovery_probe_physical_due
    ON group_recovery_probe_physical_states (next_probe_at, updated_at, id);

-- Older projection identity was case-sensitive. Keep the newest row before
-- enforcing the normalized group/account/model identity at the database layer.
WITH ranked AS (
    SELECT id,
        ROW_NUMBER() OVER (
            PARTITION BY group_id, account_id, LOWER(BTRIM(model))
            ORDER BY updated_at DESC, id DESC
        ) AS row_number
    FROM group_recovery_probe_states
)
DELETE FROM group_recovery_probe_states s
USING ranked r
WHERE s.id = r.id AND r.row_number > 1;

CREATE UNIQUE INDEX IF NOT EXISTS idx_group_recovery_probe_states_identity_key
    ON group_recovery_probe_states (group_id, account_id, LOWER(BTRIM(model)));

ALTER TABLE group_recovery_probe_states
    ADD COLUMN IF NOT EXISTS physical_state_id BIGINT
        REFERENCES group_recovery_probe_physical_states(id) ON DELETE CASCADE;

CREATE INDEX IF NOT EXISTS idx_group_recovery_probe_states_physical
    ON group_recovery_probe_states (physical_state_id, group_id, account_id);

ALTER TABLE group_recovery_probe_audits
    ADD COLUMN IF NOT EXISTS physical_state_id BIGINT
        REFERENCES group_recovery_probe_physical_states(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS beneficiary_group_count INTEGER NOT NULL DEFAULT 1;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'group_recovery_probe_audits_beneficiary_count_check'
    ) THEN
        ALTER TABLE group_recovery_probe_audits
            ADD CONSTRAINT group_recovery_probe_audits_beneficiary_count_check
            CHECK (beneficiary_group_count >= 1);
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_group_recovery_probe_audits_physical_created
    ON group_recovery_probe_audits (physical_state_id, created_at DESC);

-- Preserve the newest existing projection as the initial physical state.
INSERT INTO group_recovery_probe_physical_states (
    account_id, model_key, model, owner_group_id, status,
    consecutive_successes, consecutive_failures, last_probe_at, next_probe_at,
    last_success_at, last_failure_at, last_error_class, last_error, latency_ms,
    probe_count, created_at, updated_at
)
SELECT DISTINCT ON (s.account_id, LOWER(BTRIM(s.model)))
    s.account_id,
    LOWER(BTRIM(s.model)),
    BTRIM(s.model),
    s.group_id,
    s.status,
    s.consecutive_successes,
    s.consecutive_failures,
    s.last_probe_at,
    s.next_probe_at,
    s.last_success_at,
    s.last_failure_at,
    s.last_error_class,
    s.last_error,
    s.latency_ms,
    s.probe_count,
    s.created_at,
    s.updated_at
FROM group_recovery_probe_states s
ORDER BY s.account_id, LOWER(BTRIM(s.model)), s.updated_at DESC, s.id DESC
ON CONFLICT (account_id, model_key) DO NOTHING;

UPDATE group_recovery_probe_physical_states p
SET owner_group_id = owner.group_id
FROM (
    SELECT DISTINCT ON (s.account_id, LOWER(BTRIM(s.model)))
        s.account_id, LOWER(BTRIM(s.model)) AS model_key, s.group_id
    FROM group_recovery_probe_states s
    JOIN groups g ON g.id = s.group_id
    WHERE g.recovery_probe_enabled = TRUE AND g.status = 'active'
    ORDER BY s.account_id, LOWER(BTRIM(s.model)), s.group_id ASC
) owner
WHERE p.account_id = owner.account_id AND p.model_key = owner.model_key;

UPDATE group_recovery_probe_states s
SET physical_state_id = p.id
FROM group_recovery_probe_physical_states p
WHERE p.account_id = s.account_id
  AND p.model_key = LOWER(BTRIM(s.model))
  AND s.physical_state_id IS NULL;

COMMENT ON TABLE group_recovery_probe_physical_states IS
    'One physical recovery probe state per account and normalized model; group states are projections.';
COMMENT ON COLUMN group_recovery_probe_states.physical_state_id IS
    'Shared physical probe state. A physical probe is executed and billed once for all group projections.';
COMMENT ON COLUMN group_recovery_probe_audits.group_id IS
    'Deterministic owner group for the one physical probe charge; beneficiary groups are projections only.';
COMMENT ON COLUMN group_recovery_probe_audits.beneficiary_group_count IS
    'Number of enabled group projections that shared this physical probe result.';
