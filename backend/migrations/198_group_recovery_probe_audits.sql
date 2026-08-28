-- Independent audit trail for group recovery probes.
-- Probe calls intentionally do not enter usage_logs or user billing ledgers.
-- actual_cost stays NULL until a provider-backed reconciliation can prove it.
CREATE TABLE IF NOT EXISTS group_recovery_probe_audits (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    model VARCHAR(200) NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ NOT NULL,
    status VARCHAR(16) NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    success_count INTEGER NOT NULL DEFAULT 0,
    failure_count INTEGER NOT NULL DEFAULT 0,
    latency_ms BIGINT NOT NULL DEFAULT 0,
    error_class VARCHAR(16) NOT NULL DEFAULT '',
    sanitized_error TEXT NOT NULL DEFAULT '',
    upstream_status_code INTEGER,
    actual_cost NUMERIC(20, 10),
    cost_status VARCHAR(16) NOT NULL DEFAULT 'unavailable',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT group_recovery_probe_audits_status_check
        CHECK (status IN ('warm', 'eligible', 'failed', 'paused', 'pending', 'probing')),
    CONSTRAINT group_recovery_probe_audits_error_class_check
        CHECK (error_class IN ('', 'transient', 'permanent')),
    CONSTRAINT group_recovery_probe_audits_cost_status_check
        CHECK (cost_status IN ('unavailable', 'estimated', 'actual')),
    CONSTRAINT group_recovery_probe_audits_attempts_check CHECK (attempts >= 0),
    CONSTRAINT group_recovery_probe_audits_successes_check CHECK (success_count >= 0),
    CONSTRAINT group_recovery_probe_audits_failures_check CHECK (failure_count >= 0),
    CONSTRAINT group_recovery_probe_audits_latency_check CHECK (latency_ms >= 0)
);
CREATE INDEX IF NOT EXISTS idx_group_recovery_probe_audits_group_created
    ON group_recovery_probe_audits (group_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_group_recovery_probe_audits_account_created
    ON group_recovery_probe_audits (account_id, created_at DESC);
COMMENT ON TABLE group_recovery_probe_audits IS
    'Independent audit trail for background group recovery probes; not a billing ledger';
