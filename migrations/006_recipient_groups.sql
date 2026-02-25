-- Recipient groups: named contact lists per account
CREATE TABLE IF NOT EXISTS recipient_groups (
    id          TEXT PRIMARY KEY,
    account_id  TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE(account_id, name)
);
CREATE INDEX IF NOT EXISTS idx_recipient_groups_account ON recipient_groups(account_id);

-- Many-to-many: which recipients belong to which group
CREATE TABLE IF NOT EXISTS recipient_group_members (
    group_id     TEXT NOT NULL REFERENCES recipient_groups(id) ON DELETE CASCADE,
    recipient_id TEXT NOT NULL REFERENCES recipients(id) ON DELETE CASCADE,
    added_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (group_id, recipient_id)
);
CREATE INDEX IF NOT EXISTS idx_rgm_recipient ON recipient_group_members(recipient_id);
