-- Request metadata only; independent of billing and SLA error counts.
CREATE TABLE IF NOT EXISTS ops_response_diagnostics (
    id BIGSERIAL PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    request_id VARCHAR(256) NOT NULL DEFAULT '',
    client_request_id VARCHAR(256) NOT NULL DEFAULT '',
    upstream_request_id VARCHAR(256) NOT NULL DEFAULT '',
    response_id VARCHAR(256) NOT NULL DEFAULT '',
    model VARCHAR(256) NOT NULL DEFAULT '',
    status VARCHAR(256) NOT NULL DEFAULT '',
    reason VARCHAR(128) NOT NULL DEFAULT '',
    user_id BIGINT,
    account_id BIGINT,
    group_id BIGINT,
    stream BOOLEAN NOT NULL DEFAULT false,
    client_disconnected BOOLEAN NOT NULL DEFAULT false,
    http_status INTEGER NOT NULL DEFAULT 0,
    requested_max_output_tokens BIGINT CHECK (requested_max_output_tokens >= 0),
    forwarded_max_output_tokens BIGINT CHECK (forwarded_max_output_tokens >= 0),
    upstream_max_output_tokens BIGINT CHECK (upstream_max_output_tokens >= 0),
    input_tokens BIGINT CHECK (input_tokens >= 0),
    cache_read_tokens BIGINT CHECK (cache_read_tokens >= 0),
    cache_creation_tokens BIGINT CHECK (cache_creation_tokens >= 0),
    output_tokens BIGINT CHECK (output_tokens >= 0),
    reasoning_tokens BIGINT CHECK (reasoning_tokens >= 0),
    CHECK (status IN ('completed','incomplete','failed','client_disconnected','missing_terminal')),
    UNIQUE(request_id),
    CHECK (request_id <> '')
);
CREATE INDEX IF NOT EXISTS idx_ops_response_diagnostics_time ON ops_response_diagnostics (created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_ops_response_diagnostics_status_time ON ops_response_diagnostics (status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_ops_response_diagnostics_reason_time ON ops_response_diagnostics (reason, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_ops_response_diagnostics_user_time ON ops_response_diagnostics (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_ops_response_diagnostics_account_time ON ops_response_diagnostics (account_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_ops_response_diagnostics_client_request ON ops_response_diagnostics (client_request_id);
CREATE INDEX IF NOT EXISTS idx_ops_response_diagnostics_upstream_request ON ops_response_diagnostics (upstream_request_id);
CREATE INDEX IF NOT EXISTS idx_ops_response_diagnostics_response ON ops_response_diagnostics (response_id);
