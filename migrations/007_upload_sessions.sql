CREATE TABLE IF NOT EXISTS upload_sessions (
    id              TEXT PRIMARY KEY,
    account_id      TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    filename        TEXT NOT NULL,
    size            INTEGER NOT NULL,
    mime_type       TEXT NOT NULL,
    chunk_size      INTEGER NOT NULL,
    total_chunks    INTEGER NOT NULL,
    received_chunks TEXT NOT NULL DEFAULT '[]',
    status          TEXT NOT NULL DEFAULT 'PENDING',
    storage_path    TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    expires_at      TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_upload_sessions_account ON upload_sessions(account_id);
CREATE INDEX IF NOT EXISTS idx_upload_sessions_expires ON upload_sessions(expires_at);
