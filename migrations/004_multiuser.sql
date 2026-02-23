-- Multi-user support: roles, enabled flag, global recipients

ALTER TABLE accounts ADD COLUMN role TEXT NOT NULL DEFAULT 'member';
ALTER TABLE accounts ADD COLUMN enabled INTEGER NOT NULL DEFAULT 1;

-- First account becomes admin
UPDATE accounts SET role = 'admin'
WHERE rowid = (SELECT MIN(rowid) FROM accounts);

-- Recipients become global: change uniqueness from (account_id, email) to (email)
-- SQLite cannot drop constraints, so recreate the table
CREATE TABLE recipients_new (
    id         TEXT PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    email      TEXT NOT NULL,
    org        TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE(email)
);
INSERT OR IGNORE INTO recipients_new SELECT * FROM recipients;
DROP TABLE recipients;
ALTER TABLE recipients_new RENAME TO recipients;
CREATE INDEX IF NOT EXISTS idx_recipients_account ON recipients(account_id);
