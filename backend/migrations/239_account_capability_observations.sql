CREATE TABLE IF NOT EXISTS account_capability_observations (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    upstream_model TEXT NOT NULL DEFAULT '',
    protocol TEXT NOT NULL DEFAULT '',
    capability_type TEXT NOT NULL,
    outcome TEXT NOT NULL,
    reason TEXT NOT NULL DEFAULT '',
    request_id TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT account_capability_observations_type_check CHECK (
        capability_type IN ('function_tool', 'tool_roundtrip', 'client_tool', 'terminal_contract')
    ),
    CONSTRAINT account_capability_observations_outcome_check CHECK (
        outcome IN ('success', 'failure', 'excluded')
    )
);

CREATE INDEX IF NOT EXISTS idx_account_capability_observations_account_created
    ON account_capability_observations (account_id, created_at DESC);

CREATE TABLE IF NOT EXISTS account_capability_states (
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    upstream_model TEXT NOT NULL DEFAULT '',
    protocol TEXT NOT NULL DEFAULT '',
    capability_type TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT 'unknown',
    sample_count BIGINT NOT NULL DEFAULT 0,
    success_count BIGINT NOT NULL DEFAULT 0,
    failure_count BIGINT NOT NULL DEFAULT 0,
    last_success_at TIMESTAMPTZ,
    last_failure_at TIMESTAMPTZ,
    last_reason TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (account_id, upstream_model, protocol, capability_type),
    CONSTRAINT account_capability_states_type_check CHECK (
        capability_type IN ('function_tool', 'tool_roundtrip', 'client_tool', 'terminal_contract')
    )
);

CREATE INDEX IF NOT EXISTS idx_account_capability_states_account
    ON account_capability_states (account_id, updated_at DESC);

COMMENT ON TABLE account_capability_observations IS
    'Redacted passive account tool capability observations; no request body or credentials';
COMMENT ON TABLE account_capability_states IS
    'Latest aggregated passive account tool capability state; display-only and scheduling-neutral';
