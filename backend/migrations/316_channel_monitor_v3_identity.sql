CREATE TABLE IF NOT EXISTS channel_monitor_v3_identity (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    user_id BIGINT NOT NULL REFERENCES users(id),
    identity_marker TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS channel_monitor_v3_keys (
    group_id BIGINT PRIMARY KEY REFERENCES groups(id) ON DELETE CASCADE,
    key_id BIGINT NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE
);
