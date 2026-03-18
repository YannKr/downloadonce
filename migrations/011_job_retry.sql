-- Add retry tracking columns to jobs
ALTER TABLE jobs ADD COLUMN retry_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN max_retries INTEGER NOT NULL DEFAULT 3;
ALTER TABLE jobs ADD COLUMN next_retry_at TEXT;

-- Recreate campaigns with expanded CHECK constraint (add PARTIAL + FAILED)
CREATE TABLE campaigns_new (
    id             TEXT PRIMARY KEY,
    account_id     TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    asset_id       TEXT NOT NULL REFERENCES assets(id),
    name           TEXT NOT NULL,
    max_downloads  INTEGER,
    expires_at     TEXT,
    visible_wm     INTEGER NOT NULL DEFAULT 1,
    invisible_wm   INTEGER NOT NULL DEFAULT 1,
    state          TEXT NOT NULL DEFAULT 'DRAFT'
                     CHECK (state IN ('DRAFT','PROCESSING','READY','PARTIAL','FAILED','EXPIRED','ARCHIVED')),
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    published_at   TEXT
);

INSERT INTO campaigns_new SELECT * FROM campaigns;
DROP TABLE campaigns;
ALTER TABLE campaigns_new RENAME TO campaigns;

CREATE INDEX idx_campaigns_account ON campaigns(account_id);

-- Index for efficient retry polling
CREATE INDEX IF NOT EXISTS idx_jobs_retry
    ON jobs(state, next_retry_at)
    WHERE state = 'PENDING';
