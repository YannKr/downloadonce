-- DownloadOnce schema v1
-- SQLite with WAL mode

CREATE TABLE IF NOT EXISTS accounts (
    id            TEXT PRIMARY KEY,
    email         TEXT NOT NULL UNIQUE,
    name          TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS sessions (
    id          TEXT PRIMARY KEY,
    account_id  TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    expires_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sessions_account ON sessions(account_id);

CREATE TABLE IF NOT EXISTS assets (
    id              TEXT PRIMARY KEY,
    account_id      TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    title           TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    asset_type      TEXT NOT NULL CHECK (asset_type IN ('video', 'image')),
    original_path   TEXT NOT NULL,
    file_size_bytes INTEGER NOT NULL,
    sha256_original TEXT NOT NULL,
    mime_type       TEXT NOT NULL,
    duration_secs   REAL,
    resolution_w    INTEGER,
    resolution_h    INTEGER,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_assets_account ON assets(account_id);

CREATE TABLE IF NOT EXISTS recipients (
    id         TEXT PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    email      TEXT NOT NULL,
    org        TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (account_id, email)
);
CREATE INDEX IF NOT EXISTS idx_recipients_account ON recipients(account_id);

CREATE TABLE IF NOT EXISTS campaigns (
    id             TEXT PRIMARY KEY,
    account_id     TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    asset_id       TEXT NOT NULL REFERENCES assets(id),
    name           TEXT NOT NULL,
    max_downloads  INTEGER,
    expires_at     TEXT,
    visible_wm     INTEGER NOT NULL DEFAULT 1,
    invisible_wm   INTEGER NOT NULL DEFAULT 1,
    state          TEXT NOT NULL DEFAULT 'DRAFT'
                     CHECK (state IN ('DRAFT','PROCESSING','READY','EXPIRED')),
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    published_at   TEXT
);
CREATE INDEX IF NOT EXISTS idx_campaigns_account ON campaigns(account_id);

CREATE TABLE IF NOT EXISTS download_tokens (
    id               TEXT PRIMARY KEY,
    campaign_id      TEXT NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    recipient_id     TEXT NOT NULL REFERENCES recipients(id),
    max_downloads    INTEGER,
    download_count   INTEGER NOT NULL DEFAULT 0,
    state            TEXT NOT NULL DEFAULT 'PENDING'
                       CHECK (state IN ('PENDING','ACTIVE','CONSUMED','EXPIRED')),
    watermarked_path TEXT,
    watermark_payload BLOB,
    sha256_output    TEXT,
    output_size_bytes INTEGER,
    expires_at       TEXT,
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (campaign_id, recipient_id)
);
CREATE INDEX IF NOT EXISTS idx_tokens_campaign ON download_tokens(campaign_id);

CREATE TABLE IF NOT EXISTS download_events (
    id            TEXT PRIMARY KEY,
    token_id      TEXT NOT NULL REFERENCES download_tokens(id) ON DELETE CASCADE,
    campaign_id   TEXT NOT NULL,
    recipient_id  TEXT NOT NULL,
    asset_id      TEXT NOT NULL,
    ip_address    TEXT NOT NULL,
    user_agent    TEXT NOT NULL DEFAULT '',
    downloaded_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_events_token ON download_events(token_id);
CREATE INDEX IF NOT EXISTS idx_events_campaign ON download_events(campaign_id);

CREATE TABLE IF NOT EXISTS watermark_index (
    payload_hex  TEXT PRIMARY KEY,
    token_id     TEXT NOT NULL REFERENCES download_tokens(id) ON DELETE CASCADE,
    campaign_id  TEXT NOT NULL,
    recipient_id TEXT NOT NULL,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS jobs (
    id            TEXT PRIMARY KEY,
    job_type      TEXT NOT NULL,
    campaign_id   TEXT,
    token_id      TEXT,
    state         TEXT NOT NULL DEFAULT 'PENDING'
                    CHECK (state IN ('PENDING','RUNNING','COMPLETED','FAILED')),
    progress      INTEGER NOT NULL DEFAULT 0,
    error_message TEXT,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    started_at    TEXT,
    completed_at  TEXT
);
CREATE INDEX IF NOT EXISTS idx_jobs_state ON jobs(state);
CREATE INDEX IF NOT EXISTS idx_jobs_campaign ON jobs(campaign_id);
