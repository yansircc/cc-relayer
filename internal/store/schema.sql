CREATE TABLE IF NOT EXISTS accounts (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'created',
    schedulable INTEGER NOT NULL DEFAULT 1,
    priority INTEGER NOT NULL DEFAULT 50,
    error_message TEXT NOT NULL DEFAULT '',
    refresh_token_enc TEXT NOT NULL DEFAULT '',
    access_token_enc TEXT NOT NULL DEFAULT '',
    expires_at INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    last_used_at INTEGER,
    last_refresh_at INTEGER,
    proxy_json TEXT NOT NULL DEFAULT '',
    ext_info_json TEXT NOT NULL DEFAULT '',
    five_hour_status TEXT NOT NULL DEFAULT '',
    five_hour_auto_stopped INTEGER NOT NULL DEFAULT 0,
    five_hour_stopped_at INTEGER,
    session_window_start INTEGER,
    session_window_end INTEGER,
    auto_stop_on_warning INTEGER NOT NULL DEFAULT 0,
    opus_rate_limit_end_at INTEGER,
    overloaded_at INTEGER,
    overloaded_until INTEGER,
    rate_limited_at INTEGER
);

CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    token_hash TEXT NOT NULL UNIQUE,
    token_prefix TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active',
    created_at INTEGER NOT NULL,
    last_active_at INTEGER
);

CREATE TABLE IF NOT EXISTS request_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id TEXT NOT NULL,
    account_id TEXT NOT NULL,
    model TEXT NOT NULL,
    input_tokens INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    cache_read_tokens INTEGER NOT NULL DEFAULT 0,
    cache_create_tokens INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_request_log_created ON request_log(created_at);
CREATE INDEX IF NOT EXISTS idx_request_log_user ON request_log(user_id, created_at);
