-- Recreate campaigns table to add ARCHIVED to the state CHECK constraint.
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
                     CHECK (state IN ('DRAFT','PROCESSING','READY','EXPIRED','ARCHIVED')),
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    published_at   TEXT
);

INSERT INTO campaigns_new SELECT * FROM campaigns;

DROP TABLE campaigns;
ALTER TABLE campaigns_new RENAME TO campaigns;

CREATE INDEX idx_campaigns_account ON campaigns(account_id);
