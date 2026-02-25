# Spec 06: Webhook Delivery Log + Retry Queue

**Status:** Draft
**Date:** 2026-02-23
**Scope:** `internal/webhook`, `internal/db`, `internal/worker`, `internal/handler`, `internal/cleanup`, `migrations/`

---

## 1. Problem Statement

### Current Behavior

`webhook.Dispatcher.Dispatch()` fires a single goroutine per configured webhook endpoint. If the HTTP POST fails for any reason — the endpoint is unreachable, returns a 5xx, times out, or the URL was misconfigured — the event is logged at `WARN` level and silently dropped. No record of the attempt is written to SQLite.

```go
// current code in internal/webhook/webhook.go
go func(url, secret string) {
    if err := postWebhook(url, secret, payload); err != nil {
        slog.Warn("webhook delivery failed", "url", url, "error", err)
    }
}(wh.URL, wh.Secret)
```

### Failure Scenarios

| Scenario | Current Impact |
|---|---|
| Destination server temporarily down (deploy, restart, outage) | Event lost permanently |
| Wrong URL configured (typo, stale domain) | Event lost permanently; user has no diagnostic output |
| Invalid or rotated HMAC secret on receiver | Receiver rejects with 4xx; event lost permanently |
| Network partition between DownloadOnce and receiver | Event lost permanently |
| Server restarted mid-flight goroutine | In-flight goroutine killed, event lost |
| Receiver rate-limits and returns 429 | Event lost permanently |

### Why Silent Loss Is Especially Bad Here

DownloadOnce is a **forensic audit product**. The core value proposition is that every file download is traceable and every stakeholder notification is reliable. When a recipient downloads a watermarked file, the owning account may rely on the webhook to feed a SIEM, a legal hold system, a compliance dashboard, or a custom alerting pipeline. Silent failure breaks the audit trail without any indication to the operator.

Users currently have no way to:
- Verify that a webhook is correctly configured and reachable
- Know whether a specific download event was delivered
- Replay a missed event after fixing a misconfigured endpoint
- Diagnose whether failures are transient (endpoint down) or permanent (wrong URL/secret)

---

## 2. Goals and Non-Goals

### Goals

- **Reliable delivery with retry:** Failed deliveries are retried with exponential backoff up to 5 total attempts.
- **Delivery visibility:** Every attempt (success or failure) is recorded in a `webhook_deliveries` table with status code, error message, and response preview.
- **Replay capability:** Operators can manually re-enqueue an exhausted delivery from the UI.
- **Health indicators:** Settings page surfaces a warning when any webhook has exhausted deliveries in the last 24 hours.
- **Data retention:** Old delivery records are pruned automatically; no unbounded table growth.
- **Consistent payload format:** Standardize the JSON envelope and HMAC header name.

### Non-Goals

- **Exactly-once delivery:** The system provides at-least-once semantics. Receivers must be idempotent (they can use `event_id` to deduplicate).
- **External message broker:** No Redis, RabbitMQ, or SQS. The retry queue is the `webhook_deliveries` SQLite table, consistent with the rest of the architecture.
- **Ordered delivery:** Events are delivered in approximate order; strict ordering is not guaranteed under retry.
- **Webhook signing key rotation UI:** Secret rotation is out of scope for this milestone.
- **Per-event-type subscriptions beyond the current comma-separated `events` column:** Existing filtering logic is preserved as-is.

---

## 3. Delivery Log Schema

### Migration: `006_webhook_delivery.sql`

```sql
-- Webhook delivery log and retry queue
-- State machine: pending -> delivered (terminal)
--                         -> failed (transient, will retry)
--                         -> exhausted (terminal, max attempts reached)

CREATE TABLE IF NOT EXISTS webhook_deliveries (
    id                    TEXT PRIMARY KEY,
    webhook_id            TEXT NOT NULL REFERENCES webhooks(id) ON DELETE CASCADE,
    event_type            TEXT NOT NULL,
    event_id              TEXT NOT NULL,   -- UUID, stable across retries; use for dedup
    payload_json          TEXT NOT NULL,   -- full JSON body sent (or attempted)
    attempt_number        INTEGER NOT NULL DEFAULT 1,
    response_status       INTEGER,         -- HTTP status code, NULL if connection failed
    response_body_preview TEXT NOT NULL DEFAULT '',  -- first 500 chars of response body
    error_message         TEXT NOT NULL DEFAULT '',  -- transport/network error string
    state                 TEXT NOT NULL DEFAULT 'pending'
                            CHECK (state IN ('pending', 'delivered', 'failed', 'exhausted')),
    next_retry_at         TEXT,            -- ISO8601, NULL when terminal
    delivered_at          TEXT,            -- set when state = 'delivered'
    created_at            TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_wdeliveries_webhook  ON webhook_deliveries(webhook_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_wdeliveries_pending  ON webhook_deliveries(state, next_retry_at)
    WHERE state = 'pending';
CREATE INDEX IF NOT EXISTS idx_wdeliveries_event    ON webhook_deliveries(event_id);
CREATE INDEX IF NOT EXISTS idx_wdeliveries_created  ON webhook_deliveries(created_at DESC);
```

### Column Notes

- `id` — UUIDv4, generated at delivery creation time.
- `event_id` — UUIDv4, generated once when the event fires and carried across all retry attempts for that logical delivery. Receivers use this for idempotency checks.
- `payload_json` — the exact JSON body that is (or will be) sent. Stored once at creation; all retries send the same payload (same `event_id`, same `timestamp`).
- `attempt_number` — incremented on each retry attempt (1-based).
- `response_body_preview` — truncated to 500 bytes to limit storage while still providing diagnostic signal.
- `state = 'failed'` — a transient failure; `next_retry_at` is set and the retry worker will pick it up.
- `state = 'exhausted'` — max attempts reached; record is kept for audit/replay but no further automatic retries occur.
- `next_retry_at` — NULL for terminal states (`delivered`, `exhausted`).

---

## 4. Retry Strategy

### Backoff Schedule

| Attempt | Delay Before Attempt | `next_retry_at` offset from previous failure |
|---|---|---|
| 1 | immediate (synchronous in Dispatcher) | — |
| 2 | 30 seconds | +30s |
| 3 | 5 minutes | +5m |
| 4 | 30 minutes | +30m |
| 5 | 2 hours | +2h |

After attempt 5 fails, state transitions to `exhausted`. No further automatic retries occur.

```go
// internal/webhook/retry.go (illustrative)
var backoffSchedule = []time.Duration{
    30 * time.Second,
    5 * time.Minute,
    30 * time.Minute,
    2 * time.Hour,
    8 * time.Hour, // not used; exhausted after attempt 5
}

func nextRetryAt(attemptNumber int) *time.Time {
    // attemptNumber is the attempt that just failed (1-based)
    // We schedule the NEXT attempt
    idx := attemptNumber - 1 // 0-based index into schedule
    if idx >= len(backoffSchedule) {
        return nil // exhausted
    }
    t := time.Now().Add(backoffSchedule[idx])
    return &t
}
```

### State Transitions

```
                    attempt succeeds (2xx)
                   ┌──────────────────────────────┐
                   │                              ▼
[event fires] ─► pending ──► (attempt 1) ──► delivered  (terminal)
                   │
                   │  attempt fails
                   ▼
                 failed ──► (attempt 2..5) ──► delivered  (terminal)
                   │                       └──► failed (loop)
                   │
                   │  attempt 5 fails
                   ▼
                exhausted  (terminal)
```

Replay from UI resets `attempt_number` to 0 and `state` to `pending`, then the retry worker picks it up on the next poll as a fresh attempt-1 delivery.

---

## 5. Retry Mechanism

### Retry Worker Goroutine

A new lightweight goroutine is started alongside the existing `worker.Pool` in `internal/app/app.go`. It polls `webhook_deliveries` every 30 seconds for rows that are due for retry.

**Location:** `internal/webhook/retrier.go`

```go
package webhook

import (
    "context"
    "database/sql"
    "log/slog"
    "time"
)

// Retrier polls for pending webhook deliveries and re-attempts them.
type Retrier struct {
    DB       *sql.DB
    Interval time.Duration // default: 30s
}

func (r *Retrier) Start(ctx context.Context) {
    go r.loop(ctx)
    slog.Info("webhook retrier started", "interval", r.Interval)
}

func (r *Retrier) loop(ctx context.Context) {
    ticker := time.NewTicker(r.Interval)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            r.runOnce(ctx)
        }
    }
}

func (r *Retrier) runOnce(ctx context.Context) {
    deliveries, err := db.ListDueWebhookDeliveries(r.DB, time.Now())
    if err != nil {
        slog.Error("webhook retrier: list due deliveries", "error", err)
        return
    }
    for _, d := range deliveries {
        wh, err := db.GetWebhookByID(r.DB, d.WebhookID)
        if err != nil || wh == nil {
            continue
        }
        attemptAndRecord(r.DB, wh, &d)
    }
}
```

### DB Query: `ListDueWebhookDeliveries`

```sql
SELECT id, webhook_id, event_type, event_id, payload_json,
       attempt_number, state, next_retry_at
FROM   webhook_deliveries
WHERE  state = 'pending'
  AND  next_retry_at <= ?   -- bound: time.Now()
ORDER  BY next_retry_at ASC
LIMIT  100;                 -- process at most 100 per tick to avoid lock contention
```

The `LIMIT 100` prevents a single poll tick from monopolizing the WAL write connection if a large backlog has accumulated.

### Concurrency Safety

Because SQLite is opened with `MaxOpenConns(1)` and WAL mode, concurrent writes are serialized. The retry goroutine and the main `Dispatcher` goroutines may race to update the same row, but `UPDATE ... WHERE state = 'pending'` is atomic — only one writer wins. There is no need for optimistic locking beyond the existing single-connection constraint.

---

## 6. Webhook Payload Format

### Standardized JSON Envelope

All webhook requests share a common top-level envelope. The `data` field is event-specific.

```json
{
  "event_type": "download.completed",
  "event_id":   "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "timestamp":  "2026-02-23T14:05:32Z",
  "data": { ... }
}
```

**Field definitions:**

| Field | Type | Description |
|---|---|---|
| `event_type` | string | Dot-namespaced event identifier (see below) |
| `event_id` | string | UUIDv4; stable across retries; use for deduplication |
| `timestamp` | string | RFC3339 UTC; set when the event first fires, not when retried |
| `data` | object | Event-specific payload (see per-event schemas below) |

### Event Types and Payloads

#### `download.completed`

Fired by `handler.DownloadFile` each time a recipient successfully downloads a file.

```json
{
  "event_type": "download.completed",
  "event_id":   "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "timestamp":  "2026-02-23T14:05:32Z",
  "data": {
    "download_event_id": "de001122-...",
    "token_id":          "tok-uuid",
    "campaign_id":       "camp-uuid",
    "campaign_name":     "Q1 2026 Investor Deck",
    "recipient_id":      "rec-uuid",
    "recipient_name":    "Alice Johnson",
    "recipient_email":   "alice@example.com",
    "recipient_org":     "Acme Corp",
    "ip_address":        "203.0.113.42",
    "user_agent":        "Mozilla/5.0 ..."
  }
}
```

#### `campaign.ready`

Fired by `worker.Pool.checkCampaignCompletion` when all watermark jobs for a campaign finish.

```json
{
  "event_type": "campaign.ready",
  "event_id":   "b2c3d4e5-...",
  "timestamp":  "2026-02-23T13:00:00Z",
  "data": {
    "campaign_id":       "camp-uuid",
    "campaign_name":     "Q1 2026 Investor Deck",
    "total_tokens":      12,
    "completed_tokens":  12,
    "failed_tokens":     0
  }
}
```

#### `token.revoked` (future)

Reserved for future use when `TokenRevoke` fires. Not implemented in this milestone.

```json
{
  "event_type": "token.revoked",
  "event_id":   "c3d4e5f6-...",
  "timestamp":  "2026-02-23T15:00:00Z",
  "data": {
    "token_id":       "tok-uuid",
    "campaign_id":    "camp-uuid",
    "recipient_id":   "rec-uuid",
    "recipient_name": "Alice Johnson",
    "revoked_by":     "account-uuid"
  }
}
```

### HMAC-SHA256 Signature

The current code signs with key `X-Signature-256`. This spec standardizes the header name to `X-DownloadOnce-Signature` to match the product name and avoid confusion with GitHub's `X-Hub-Signature-256`.

**Signing algorithm:**

```
HMAC-SHA256(key=webhook.secret, message=raw_request_body_bytes)
```

**Header value format:**

```
X-DownloadOnce-Signature: sha256=<lowercase hex digest>
```

**Verification pseudocode for receivers:**

```python
import hmac, hashlib
expected = hmac.new(secret.encode(), request.body, hashlib.sha256).hexdigest()
received = request.headers["X-DownloadOnce-Signature"].removeprefix("sha256=")
assert hmac.compare_digest(expected, received)
```

**Migration note:** The old header `X-Signature-256` is removed. Operators upgrading must update their receiver validation. The settings page should display the current header name and a copy button for the secret value (secret is currently hidden entirely).

### Go Struct

```go
// internal/webhook/event.go

// Event is the canonical outgoing webhook envelope.
// event_id is generated once and stored in webhook_deliveries.event_id;
// all retries of the same logical event carry the same event_id.
type Event struct {
    EventType string      `json:"event_type"`
    EventID   string      `json:"event_id"`
    Timestamp string      `json:"timestamp"`
    Data      interface{} `json:"data"`
}

// DownloadCompletedData is the data payload for "download.completed".
type DownloadCompletedData struct {
    DownloadEventID string `json:"download_event_id"`
    TokenID         string `json:"token_id"`
    CampaignID      string `json:"campaign_id"`
    CampaignName    string `json:"campaign_name"`
    RecipientID     string `json:"recipient_id"`
    RecipientName   string `json:"recipient_name"`
    RecipientEmail  string `json:"recipient_email"`
    RecipientOrg    string `json:"recipient_org"`
    IPAddress       string `json:"ip_address"`
    UserAgent       string `json:"user_agent"`
}
```

---

## 7. Delivery Log UI

### 7a. Settings Page Changes (`/settings`)

The existing webhooks table gains two new columns: **Last Status** and **Actions**.

```
Webhooks
────────────────────────────────────────────────────────────────────
URL                         Events          Last Status    Actions
https://hooks.example.com   download        ✓ 2m ago       [View] [Delete]
https://legacy.corp/hook    download        ✗ 4h ago       [View] [Delete]
────────────────────────────────────────────────────────────────────
```

- **Last Status** is fetched with a single query joining `webhook_deliveries` on `webhook_id`, ordered by `created_at DESC LIMIT 1` per webhook. A green checkmark with relative timestamp indicates the most recent delivery succeeded (`state = 'delivered'`). A red X indicates the most recent record is `failed` or `exhausted`.
- **[View]** links to `/settings/webhooks/{id}/deliveries`.

**Health Warning Banner**

If any webhook for the current account has at least one `exhausted` delivery in the last 24 hours, a banner is rendered at the top of the settings page:

```html
<div class="alert alert-warning">
  One or more webhooks have failed deliveries that could not be retried.
  <a href="/settings/webhooks/{id}/deliveries">View delivery history</a>
</div>
```

This check is a single SQL query run at settings page load:

```sql
SELECT COUNT(*)
FROM   webhook_deliveries wd
JOIN   webhooks w ON w.id = wd.webhook_id
WHERE  w.account_id = ?
  AND  wd.state     = 'exhausted'
  AND  wd.created_at >= strftime('%Y-%m-%dT%H:%M:%fZ', 'now', '-24 hours');
```

### 7b. Delivery History Page (`/settings/webhooks/{id}/deliveries`)

A new page rendered by `handler.WebhookDeliveries`.

**Template:** `templates/webhook_deliveries.html`

**Page title:** "Delivery History — {webhook URL}"

**Table columns:**

| Column | Source |
|---|---|
| Timestamp | `created_at` formatted as local time |
| Event Type | `event_type` |
| Attempt | `attempt_number` |
| Status | HTTP `response_status` or "connection error" |
| State | Badge: `delivered` (green) / `failed` (yellow) / `exhausted` (red) / `pending` (grey) |
| Error | `error_message` truncated to 80 chars; full text on hover |
| Actions | "Replay" button (POST) shown for `exhausted` and `delivered` states |

**Pagination:** 50 rows per page, `?page=N` query param. Default sort: `created_at DESC`.

**Access control:** `RequireAuth` middleware enforces session. The handler additionally checks that the requested webhook's `account_id` matches the authenticated user's account ID; returns 404 otherwise (does not leak existence).

### 7c. Replay Endpoint

```
POST /settings/webhooks/{id}/deliveries/{deliveryID}/replay
```

Handler: `handler.WebhookDeliveryReplay`

**Behavior:**

1. Load the `webhook_deliveries` row; verify `webhook_id` belongs to the authenticated account.
2. Verify state is `exhausted` or `delivered` (replay of a delivered event is allowed for testing).
3. Reset the row in a single UPDATE:
   ```sql
   UPDATE webhook_deliveries
   SET    state          = 'pending',
          attempt_number = 0,
          next_retry_at  = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
          error_message  = '',
          response_status = NULL,
          response_body_preview = ''
   WHERE  id = ? AND state IN ('exhausted', 'delivered');
   ```
4. The retry worker will pick it up within 30 seconds and execute attempt 1.
5. Redirect to `/settings/webhooks/{id}/deliveries` with flash: "Delivery re-queued."

Note: `attempt_number = 0` means the retrier will treat this as a fresh delivery; after the first retry attempt completes the number becomes 1.

**Audit log entry:**

```go
db.InsertAuditLog(db, accountID, "webhook_delivery_replayed",
    "webhook_delivery", deliveryID, webhookURL, r.RemoteAddr)
```

---

## 8. Go Model and DB Query Additions

### New Model Struct

```go
// internal/model/model.go — add to existing file

type WebhookDelivery struct {
    ID                  string
    WebhookID           string
    EventType           string
    EventID             string
    PayloadJSON         string
    AttemptNumber       int
    ResponseStatus      *int
    ResponseBodyPreview string
    ErrorMessage        string
    State               string    // pending, delivered, failed, exhausted
    NextRetryAt         *time.Time
    DeliveredAt         *time.Time
    CreatedAt           time.Time
}
```

### New DB Query Functions (`internal/db/queries_webhooks.go`)

```go
// CreateWebhookDelivery inserts the initial pending delivery record.
func CreateWebhookDelivery(database *sql.DB, d *model.WebhookDelivery) error

// UpdateWebhookDelivery updates outcome fields after an attempt.
func UpdateWebhookDelivery(database *sql.DB, d *model.WebhookDelivery) error

// ListDueWebhookDeliveries returns pending deliveries whose next_retry_at <= now.
func ListDueWebhookDeliveries(database *sql.DB, now time.Time) ([]model.WebhookDelivery, error)

// ListWebhookDeliveries returns paginated deliveries for a specific webhook.
func ListWebhookDeliveries(database *sql.DB, webhookID string, limit, offset int) ([]model.WebhookDelivery, error)

// GetWebhookDelivery returns a single delivery row by ID.
func GetWebhookDelivery(database *sql.DB, id string) (*model.WebhookDelivery, error)

// ReplayWebhookDelivery resets a terminal delivery to pending.
func ReplayWebhookDelivery(database *sql.DB, id string) error

// GetLastDeliveryPerWebhook returns the most recent delivery row for each
// webhook in the provided account, keyed by webhook ID.
func GetLastDeliveryPerWebhook(database *sql.DB, accountID string) (map[string]*model.WebhookDelivery, error)

// CountExhaustedDeliveriesLast24h returns the count of exhausted deliveries
// across all webhooks owned by accountID in the last 24 hours.
func CountExhaustedDeliveriesLast24h(database *sql.DB, accountID string) (int, error)

// PruneOldWebhookDeliveries deletes delivery rows older than cutoff.
func PruneOldWebhookDeliveries(database *sql.DB, cutoff time.Time) (int64, error)
```

### Updated `Dispatcher`

`Dispatcher.Dispatch` is refactored to:

1. Generate `event_id` (UUID) once per logical event.
2. Serialize the full `Event` envelope into `payload_json`.
3. Insert a `webhook_deliveries` row with `state = 'pending'`, `next_retry_at = now()` (immediate).
4. Attempt delivery synchronously within the goroutine (attempt 1).
5. Update the row with outcome: `delivered` on 2xx, `failed` + `next_retry_at` on error, `exhausted` if `attempt_number >= 5`.

This means the dispatcher goroutine now writes to SQLite, which is safe given `MaxOpenConns(1)` and WAL mode. The insert + attempt + update pattern ensures the record is always written even if the process crashes after step 3.

```go
// Dispatcher.Dispatch — revised signature (internal change only)
func (d *Dispatcher) Dispatch(accountID, eventType string, data interface{})
```

The public signature is unchanged; callers in `handler/download.go` and `worker/pool.go` require no modifications.

---

## 9. Data Retention

Delivery records older than 90 days are pruned by the existing `cleanup.Cleaner`. A new method is added to `cleanup.Cleaner.runOnce()`:

```go
cutoff := time.Now().AddDate(0, 0, -90)
n, err := db.PruneOldWebhookDeliveries(c.DB, cutoff)
if err != nil {
    slog.Error("cleanup: prune webhook deliveries", "error", err)
} else if n > 0 {
    slog.Info("cleanup: pruned old webhook deliveries", "count", n)
}
```

The underlying SQL:

```sql
DELETE FROM webhook_deliveries
WHERE created_at < ?
  AND state IN ('delivered', 'exhausted');
-- Note: do NOT prune 'pending' or 'failed' rows regardless of age;
-- those represent in-flight work that should be investigated manually.
```

Rationale for the 90-day window: sufficient for compliance review cycles, constrained enough to bound table growth. This value is not currently user-configurable (out of scope for this milestone).

---

## 10. Security

### Access Control

- All delivery history routes are behind `RequireAuth`.
- Every query that loads delivery rows filters by `webhook_id` which is in turn filtered by `account_id = authenticatedUser`. No cross-account data leakage is possible through the UI.
- The `payload_json` column may contain recipient PII (name, email, IP address). It is never served to unauthenticated requests.
- The replay endpoint requires CSRF token (same as all other POST forms in the application).

### Payload Preview Truncation

`response_body_preview` is limited to 500 bytes at write time in the dispatcher:

```go
preview := string(respBody)
if len(preview) > 500 {
    preview = preview[:500]
}
```

This prevents adversarial endpoints from storing large blobs in the delivery log by returning a very large response body on failure.

### Secret Display

The settings page will display the webhook HMAC secret (needed for receivers to verify signatures). It should be rendered in a password-style field with a "Reveal" toggle and a "Copy" button, consistent with the existing API key display pattern. The secret is stored in plaintext in `webhooks.secret` (this is necessary for HMAC signing); this is documented behavior.

### Audit Trail

The following actions are written to `audit_logs`:

| Action | `target_type` | `target_id` |
|---|---|---|
| `webhook_delivery_replayed` | `webhook_delivery` | delivery ID |

Existing `webhook_created` and `webhook_deleted` audit entries are unchanged.

---

## 11. Implementation Milestones

### M1: Schema + Delivery Logging (No Retry Yet)

**Deliverables:**
- `migrations/006_webhook_delivery.sql` — creates `webhook_deliveries` table and indexes.
- `internal/model/model.go` — add `WebhookDelivery` struct.
- `internal/db/queries_webhooks.go` — add `CreateWebhookDelivery`, `UpdateWebhookDelivery`, `GetLastDeliveryPerWebhook`, `CountExhaustedDeliveriesLast24h`.
- `internal/webhook/webhook.go` — refactor `Dispatcher.Dispatch` to create a delivery row, attempt once, and update with outcome. Standardize header to `X-DownloadOnce-Signature`. Rename `Event.Type` to `Event.EventType`, add `Event.EventID`.
- **Acceptance criteria:** Every webhook delivery attempt (success or failure) produces a row in `webhook_deliveries`. No behavior change for callers. Existing `slog` lines kept in addition.

### M2: Retry Goroutine

**Deliverables:**
- `internal/db/queries_webhooks.go` — add `ListDueWebhookDeliveries`.
- `internal/webhook/retrier.go` — `Retrier` struct with `Start(ctx)` and poll loop.
- `internal/app/app.go` — instantiate and start `Retrier` alongside `worker.Pool`.
- **Acceptance criteria:** A delivery row with `state = 'failed'` and `next_retry_at` in the past is re-attempted within 60 seconds of the due time. After 5 failed attempts, state becomes `exhausted` and no further automatic retries occur.

### M3: UI — Delivery History

**Deliverables:**
- `internal/handler/settings.go` — update `settingsData` struct; add `WebhookDeliveries` handler; populate last-delivery status and health warning flag.
- `internal/handler/routes.go` — add `GET /settings/webhooks/{id}/deliveries`.
- `templates/webhook_deliveries.html` — delivery history table with pagination.
- `templates/settings.html` — add last-status column to webhooks table; add health warning banner.
- **Acceptance criteria:** Settings page shows per-webhook last delivery status. Delivery history page lists attempts with all columns. Warning banner appears when exhausted deliveries exist in the last 24 hours.

### M4: Replay Button

**Deliverables:**
- `internal/db/queries_webhooks.go` — add `ReplayWebhookDelivery`, `GetWebhookDelivery`.
- `internal/handler/settings.go` — add `WebhookDeliveryReplay` handler.
- `internal/handler/routes.go` — add `POST /settings/webhooks/{id}/deliveries/{deliveryID}/replay`.
- `templates/webhook_deliveries.html` — add Replay form button for eligible rows.
- `internal/cleanup/cleanup.go` — add `PruneOldWebhookDeliveries` call to `runOnce`.
- `internal/db/queries_webhooks.go` — add `PruneOldWebhookDeliveries`.
- **Acceptance criteria:** Clicking Replay for an `exhausted` delivery resets it to `pending` and it is re-attempted by the retry worker within 60 seconds. An audit log entry is created. Records older than 90 days are pruned on each cleanup tick.

---

## 12. Open Questions

1. **Header rename backward compatibility:** Changing `X-Signature-256` to `X-DownloadOnce-Signature` is a breaking change for existing receiver integrations. Should a transition period send both headers? Recommendation: send both for one release, then remove the old header in the following release, gated behind a deprecation notice in the settings page.

2. **`payload_json` size limit:** Large `user_agent` strings or custom data fields could make payloads arbitrarily large. Consider enforcing a maximum serialized payload size (e.g., 64 KB) and failing fast if exceeded, rather than silently truncating.

3. **Delivery fan-out ordering:** When multiple webhooks are configured for the same event, deliveries are created and attempted in order of `webhooks.created_at ASC`. If the first delivery is slow to respond, it blocks the goroutine. Consider whether each webhook should get its own goroutine (current behavior) while still writing to the delivery log atomically first.

4. **Cleanup of `pending` rows after long outage:** The retention policy deliberately excludes `pending` and `failed` rows from the 90-day prune. After a very long outage (unlikely but possible), stale `failed` rows could accumulate. A separate threshold (e.g., auto-exhaust any `failed` row older than 7 days) may be warranted.
