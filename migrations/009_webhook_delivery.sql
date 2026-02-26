CREATE TABLE IF NOT EXISTS webhook_deliveries (
    id                    TEXT PRIMARY KEY,
    webhook_id            TEXT NOT NULL REFERENCES webhooks(id) ON DELETE CASCADE,
    event_type            TEXT NOT NULL,
    event_id              TEXT NOT NULL,
    payload_json          TEXT NOT NULL,
    attempt_number        INTEGER NOT NULL DEFAULT 1,
    response_status       INTEGER,
    response_body_preview TEXT NOT NULL DEFAULT '',
    error_message         TEXT NOT NULL DEFAULT '',
    state                 TEXT NOT NULL DEFAULT 'pending'
                            CHECK (state IN ('pending', 'delivered', 'failed', 'exhausted')),
    next_retry_at         TEXT,
    delivered_at          TEXT,
    created_at            TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_wdeliveries_webhook ON webhook_deliveries(webhook_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_wdeliveries_pending ON webhook_deliveries(state, next_retry_at)
    WHERE state = 'pending';
CREATE INDEX IF NOT EXISTS idx_wdeliveries_event   ON webhook_deliveries(event_id);
CREATE INDEX IF NOT EXISTS idx_wdeliveries_created ON webhook_deliveries(created_at DESC);
