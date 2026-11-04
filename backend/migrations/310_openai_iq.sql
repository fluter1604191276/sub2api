-- Durable state for the OpenAI group IQ benchmark. Credentials remain in api_keys.
CREATE TABLE IF NOT EXISTS openai_iq_runs (
    id BIGSERIAL PRIMARY KEY,
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ,
    question TEXT NOT NULL,
    expected_answer TEXT NOT NULL,
    model VARCHAR(200) NOT NULL,
    settings_snapshot JSONB NOT NULL DEFAULT '{}'::jsonb,
    status VARCHAR(16) NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'complete', 'error')),
    lease_expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_openai_iq_runs_due ON openai_iq_runs (status, started_at DESC);

CREATE TABLE IF NOT EXISTS openai_iq_results (
    id BIGSERIAL PRIMARY KEY,
    run_id BIGINT NOT NULL REFERENCES openai_iq_runs(id) ON DELETE CASCADE,
    -- This is an immutable historical snapshot. Do not cascade-delete a run
    -- when an administrator removes a group later.
    group_id BIGINT NOT NULL,
    group_name TEXT NOT NULL,
    rate_multiplier NUMERIC(20, 10) NOT NULL,
    model VARCHAR(200) NOT NULL,
    expected_answer TEXT NOT NULL,
    response_model VARCHAR(200) NOT NULL DEFAULT '',
    model_answer TEXT NOT NULL DEFAULT '',
    output TEXT NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    checked_at TIMESTAMPTZ,
    error TEXT NOT NULL DEFAULT '',
    UNIQUE (run_id, group_id),
    CONSTRAINT openai_iq_results_status_check CHECK (status IN ('correct','incorrect','error','skipped','pending'))
);
CREATE INDEX IF NOT EXISTS idx_openai_iq_results_group_checked ON openai_iq_results (group_id, checked_at DESC);

CREATE TABLE IF NOT EXISTS openai_iq_budget_reservations (
    id BIGSERIAL PRIMARY KEY,
    budget_date DATE NOT NULL,
    run_id BIGINT REFERENCES openai_iq_runs(id) ON DELETE SET NULL,
    group_id BIGINT,
    reserved_usd NUMERIC(20, 10) NOT NULL CHECK (reserved_usd > 0),
    key_id BIGINT,
    user_id BIGINT,
    request_id TEXT,
    actual_cost_usd NUMERIC(20, 10),
    usage_log_id BIGINT,
    account_id BIGINT,
    account_base_cost_usd NUMERIC(20, 10),
    account_rate_multiplier NUMERIC(20, 10),
    settled_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (run_id, group_id),
    UNIQUE (request_id),
    UNIQUE (usage_log_id)
);
CREATE INDEX IF NOT EXISTS idx_openai_iq_budget_date ON openai_iq_budget_reservations (budget_date);
CREATE INDEX IF NOT EXISTS idx_openai_iq_budget_pending_reconcile
    ON openai_iq_budget_reservations (request_id, key_id, user_id, group_id)
    WHERE actual_cost_usd IS NULL AND request_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS openai_iq_identity (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    user_id BIGINT NOT NULL REFERENCES users(id),
    identity_marker TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS openai_iq_keys (
    group_id BIGINT PRIMARY KEY,
    key_id BIGINT NOT NULL REFERENCES api_keys(id),
    user_id BIGINT NOT NULL REFERENCES users(id)
);
