# Spec 04: Job Retry on Failure with Notifications

**Status:** Draft
**Date:** 2026-02-23
**Affects:** `internal/worker/pool.go`, `internal/db/queries_jobs.go`, `internal/cleanup/cleanup.go`, `internal/model/model.go`, `internal/handler/campaigns.go`, `internal/handler/routes.go`, `internal/email/email.go`, `migrations/006_job_retry.sql`

---

## 1. Problem Statement

### 1.1 Current Failure Modes

When a watermarking job fails, the system currently:

1. Calls `db.FailJob(database, job.ID, processErr.Error())` — setting `state = 'FAILED'` permanently.
2. Calls `checkCampaignCompletion()` — which may move the campaign to READY even with failed tokens.
3. Leaves FAILED jobs with no automatic retry, no user notification, and no manual recovery button.

Concrete failure scenarios:

| Scenario | Root Cause | Classification |
|---|---|---|
| FFmpeg OOM kill | Large 4K video, low-memory host | Transient |
| Disk full mid-encode | Temporary burst of uploads | Transient |
| Python venv not found | Container restart with volume mismatch | Transient |
| FFmpeg decode error on corrupt upload | Bad source file | Permanent |
| Worker process crash (SIGKILL) | OS-level kill, Docker OOM killer | Transient |

### 1.2 User Impact

- A campaign with 50 recipients can end up with 3–5 permanently failed tokens. The owner only discovers the issue by loading the campaign detail page.
- The only recovery path is re-publishing the entire campaign via `POST /campaigns/{id}/publish`. This creates duplicate jobs for already-completed tokens (wasted CPU/disk) and orphans old FAILED rows.

---

## 2. Goals and Non-Goals

### Goals

- Automatically retry transient job failures with exponential backoff (up to a configurable maximum).
- Detect and reset stuck jobs (worker crashed mid-job, state stuck at `RUNNING`).
- Introduce a `PARTIAL` campaign state for campaigns where some tokens failed permanently.
- Provide a manual per-token retry button on the campaign detail page.
- Notify campaign owners via email and SSE when a job exhausts all retries.
- Notify campaign owners when an entire campaign finishes in a `PARTIAL` or `FAILED` state.

### Non-Goals

- Retrying `detect` jobs — detection failures have their own result display.
- Resumable FFmpeg encoding — separate concern.
- Distributed job queues or external broker integration.
- Per-campaign configurable retry limits via the UI — max_retries is a system-level default.

---

## 3. Retry Strategy

### 3.1 New Schema Columns

Three new columns on the `jobs` table:

- `retry_count INT NOT NULL DEFAULT 0` — number of retries so far.
- `max_retries INT NOT NULL DEFAULT 3` — ceiling on automatic retries.
- `next_retry_at TEXT` — ISO-8601 UTC timestamp. `NULL` means eligible immediately. Set on each failure.

### 3.2 Backoff Schedule

| Attempt | `retry_count` at failure | Delay before next attempt |
|---|---|---|
| 1st failure | 0 | 1 minute |
| 2nd failure | 1 | 5 minutes |
| 3rd failure | 2 | 15 minutes |
| 4th failure (final) | 3 = max_retries | Permanent FAILED |

```go
var backoffDelays = []time.Duration{
    1 * time.Minute,
    5 * time.Minute,
    15 * time.Minute,
}

func nextRetryDelay(retryCount int) time.Duration {
    if retryCount < len(backoffDelays) {
        return backoffDelays[retryCount]
    }
    return backoffDelays[len(backoffDelays)-1]
}
```

### 3.3 Job Lifecycle with Retry

```
PENDING → RUNNING → (success) → COMPLETED
                 → (transient fail, retry_count < max_retries)
                     → PENDING  [retry_count++, next_retry_at = now + delay]
                 → (permanent fail OR retry_count >= max_retries)
                     → FAILED   [error_message set, next_retry_at = NULL]
```

---

## 4. Worker Changes

### 4.1 `ClaimNextJob` — Poll Filter

The `ClaimNextJob` query must filter on `next_retry_at`:

```sql
WHERE state = 'PENDING'
  AND job_type IN (...)
  AND (next_retry_at IS NULL OR next_retry_at <= strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
ORDER BY created_at ASC
LIMIT 1
```

### 4.2 `RetryOrFailJob` — New DB Function

```go
func RetryOrFailJob(database *sql.DB, id, errorMsg string, delay time.Duration) (retried bool, err error) {
    var retryCount, maxRetries int
    err = database.QueryRow(
        `SELECT retry_count, max_retries FROM jobs WHERE id = ?`, id,
    ).Scan(&retryCount, &maxRetries)
    if err != nil {
        return false, err
    }

    newRetryCount := retryCount + 1

    if newRetryCount > maxRetries {
        _, err = database.Exec(`
            UPDATE jobs
            SET state = 'FAILED', error_message = ?, retry_count = ?,
                next_retry_at = NULL,
                completed_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
            WHERE id = ?`, errorMsg, newRetryCount, id)
        return false, err
    }

    nextRetry := time.Now().UTC().Add(delay).Format(time.RFC3339Nano)
    _, err = database.Exec(`
        UPDATE jobs
        SET state = 'PENDING', error_message = ?, retry_count = ?,
            next_retry_at = ?, progress = 0, started_at = NULL
        WHERE id = ?`, errorMsg, newRetryCount, nextRetry, id)
    return err == nil, err
}
```

### 4.3 `ResetJobForManualRetry` — New DB Function

```go
func ResetJobForManualRetry(database *sql.DB, id string) error {
    _, err := database.Exec(`
        UPDATE jobs
        SET state = 'PENDING', retry_count = 0, max_retries = 3,
            next_retry_at = NULL, progress = 0,
            error_message = NULL, started_at = NULL, completed_at = NULL
        WHERE id = ? AND state = 'FAILED'`, id)
    return err
}
```

### 4.4 `pool.go run()` Loop Changes

```go
if processErr != nil {
    slog.Error("job failed", "job", job.ID, "type", job.JobType, "error", processErr)

    isPermanent := isPermanentFailure(processErr)
    var retried bool

    if isPermanent {
        db.FailJob(p.database, job.ID, processErr.Error())
        retried = false
    } else {
        delay := nextRetryDelay(job.RetryCount)
        retried, _ = db.RetryOrFailJob(p.database, job.ID, processErr.Error(), delay)
    }

    if !retried {
        p.publishJobFailed(job, processErr.Error())
    }
} else {
    db.CompleteJob(p.database, job.ID)
}

if job.JobType != "detect" {
    p.checkCampaignCompletion(job.CampaignID)
}
```

### 4.5 `isPermanentFailure` Classification

```go
func isPermanentFailure(err error) bool {
    if err == nil {
        return false
    }
    msg := strings.ToLower(err.Error())
    return strings.Contains(msg, "invalid data found when processing input") ||
        strings.Contains(msg, "moov atom not found") ||
        (strings.Contains(msg, "no such file or directory") && strings.Contains(msg, "input")) ||
        strings.Contains(msg, "unknown job type")
}
```

| Error pattern | Classification | Reasoning |
|---|---|---|
| `invalid data found when processing input` | Permanent | FFmpeg decode error on corrupt file |
| `moov atom not found` | Permanent | Corrupt/truncated MP4 container |
| `no such file or directory` (input path) | Permanent | Asset file deleted from disk |
| `unknown job type` | Permanent | Programming error |
| `exit status 137` (OOM kill) | Transient | Memory pressure |
| `context deadline exceeded` | Transient | Subprocess timeout |
| disk write errors | Transient | Disk full or permission issue |

---

## 5. Campaign State on Partial Failure

### 5.1 New Campaign States

Add `PARTIAL` and `FAILED` to the `campaigns.state` CHECK constraint:

```
'DRAFT' | 'PROCESSING' | 'READY' | 'PARTIAL' | 'FAILED' | 'EXPIRED'
```

- `PARTIAL`: some tokens succeeded and at least one failed permanently.
- `FAILED`: all tokens failed permanently.

### 5.2 Updated `checkCampaignCompletion` Logic

```go
func (p *Pool) checkCampaignCompletion(campaignID string) {
    total, completed, failed, pending, running, err := db.CountJobsByCampaignDetailed(campaignID)
    if err != nil { return }

    if pending > 0 || running > 0 {
        return // still in progress
    }

    switch {
    case failed == 0:
        db.UpdateCampaignState(p.database, campaignID, "READY")
        p.notifyCampaignReady(campaignID, completed)
    case completed == 0:
        db.UpdateCampaignState(p.database, campaignID, "FAILED")
        p.notifyCampaignFailed(campaignID, failed)
    default:
        db.UpdateCampaignState(p.database, campaignID, "PARTIAL")
        p.notifyCampaignPartial(campaignID, completed, failed)
    }
}
```

### 5.3 Updated Campaign State Machine

```
DRAFT → (publish) → PROCESSING
PROCESSING → (all COMPLETED) → READY
PROCESSING → (some COMPLETED, some FAILED) → PARTIAL
PROCESSING → (all FAILED) → FAILED
READY | PARTIAL | FAILED → (expires_at reached) → EXPIRED
PARTIAL | FAILED → (manual retry → all COMPLETED) → READY
```

When manual retry is triggered on a `FAILED` or `PARTIAL` campaign, the campaign state is reset to `PROCESSING`.

---

## 6. Manual Retry

### 6.1 HTTP Endpoint

```
POST /campaigns/{id}/tokens/{tokenID}/retry
```

Handler: `h.TokenRetry` in `internal/handler/campaigns.go`

Logic:
1. Load campaign — 404 if not found or not owned by requesting account.
2. Load the latest job for `{tokenID}` via `db.GetJobByToken()`.
3. If job state is not `FAILED`, return 400 with flash "Token is not in a failed state."
4. Call `db.ResetJobForManualRetry(database, job.ID)`.
5. If campaign state is `FAILED` or `PARTIAL`, call `db.UpdateCampaignState(database, campaignID, "PROCESSING")`.
6. Redirect to `GET /campaigns/{id}` with flash "Retry queued for {recipientName}."

### 6.2 Route Registration

```go
r.Post("/campaigns/{id}/tokens/{tokenID}/retry", h.TokenRetry)
```

---

## 7. Failure Notifications

### 7.1 SSE: Per-Job Failure Event

```go
func (p *Pool) publishJobFailed(job *model.Job, errorMsg string) {
    if p.sseHub == nil {
        return
    }
    data := fmt.Sprintf(`{"token_id":"%s","error":"%s"}`,
        job.TokenID,
        strings.ReplaceAll(errorMsg, `"`, `\"`),
    )
    evt := sse.Event{Type: "token_failed", Data: data}
    p.sseHub.Publish("token:"+job.TokenID, evt)
    p.sseHub.Publish("campaign:"+job.CampaignID, evt)
}
```

### 7.2 Email: Job Exhausted All Retries

Add `SendJobFailed(to, ownerName, campaignName, recipientName, errorMsg string) error` to `internal/email/email.go`.

Called when `retried == false` from `pool.go`, after loading the recipient name from the DB.

### 7.3 Email: Campaign Partially or Fully Failed

Add:
- `SendCampaignPartial(to, ownerName, campaignName string, completed, failed int) error`
- `SendCampaignFailed(to, ownerName, campaignName string, failedCount int) error`

All email calls are gated on `p.mailer != nil && p.mailer.Enabled()` and fired in a goroutine.

---

## 8. Schema Migration

File: `migrations/006_job_retry.sql`

```sql
-- 006_job_retry.sql
-- Add retry tracking to jobs and expand campaign state machine

ALTER TABLE jobs ADD COLUMN retry_count  INTEGER NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN max_retries  INTEGER NOT NULL DEFAULT 3;
ALTER TABLE jobs ADD COLUMN next_retry_at TEXT;

-- Recreate campaigns with updated CHECK constraint (PARTIAL + FAILED states).
-- SQLite does not support ALTER COLUMN to modify a CHECK constraint.
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
                     CHECK (state IN ('DRAFT','PROCESSING','READY','PARTIAL','FAILED','EXPIRED')),
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    published_at   TEXT
);

INSERT INTO campaigns_new SELECT * FROM campaigns;
DROP TABLE campaigns;
ALTER TABLE campaigns_new RENAME TO campaigns;

CREATE INDEX IF NOT EXISTS idx_campaigns_account ON campaigns(account_id);

-- Efficient index for claiming jobs whose retry window has elapsed
CREATE INDEX IF NOT EXISTS idx_jobs_retry
    ON jobs(state, next_retry_at)
    WHERE state = 'PENDING';
```

**Note:** If `006_recipient_groups.sql` is already in use for that feature, rename this to `007_job_retry.sql`.

---

## 9. Stuck Job Detection

### 9.1 Definition

A job is stuck if: `state = 'RUNNING'` AND `started_at < now - 30 minutes`.

### 9.2 New DB Function

```go
func ResetStuckJobs(database *sql.DB, stuckThreshold time.Duration) (int, error) {
    cutoff := time.Now().UTC().Add(-stuckThreshold).Format(time.RFC3339Nano)
    res, err := database.Exec(`
        UPDATE jobs
        SET state = 'PENDING', started_at = NULL, progress = 0, next_retry_at = NULL
        WHERE state = 'RUNNING' AND started_at < ?`, cutoff)
    if err != nil {
        return 0, err
    }
    n, _ := res.RowsAffected()
    return int(n), nil
}
```

Stuck-job resets do **not** increment `retry_count` — the job was not attempted to completion.

### 9.3 Integration with Cleanup Scheduler

In `internal/cleanup/cleanup.go`, add to `runOnce()`:

```go
const stuckJobThreshold = 30 * time.Minute

n, err := db.ResetStuckJobs(c.DB, stuckJobThreshold)
if err != nil {
    slog.Error("cleanup: reset stuck jobs", "error", err)
} else if n > 0 {
    slog.Warn("cleanup: reset stuck jobs", "count", n)
}
```

---

## 10. UI Changes

### 10.1 Campaign Detail Page — Token Table

New columns/elements in `templates/campaign_detail.html`:

| Element | Content |
|---|---|
| Status badge | PENDING / PROCESSING (%) / ACTIVE / FAILED / CONSUMED / EXPIRED |
| Retry count badge | `{retry_count}/{max_retries}` — only shown when `retry_count > 0` |
| Error details | `<details><summary>Error</summary>{error_message}</details>` — only when FAILED |
| Retry button | `<form method="POST" action="/campaigns/{id}/tokens/{tokenID}/retry">` — only when FAILED |

### 10.2 SSE JavaScript Updates

```javascript
evtSource.addEventListener('token_failed', function(e) {
    const data = JSON.parse(e.data);
    const row = document.querySelector(`[data-token-id="${data.token_id}"]`);
    if (!row) return;
    row.querySelector('.status-badge').textContent = 'FAILED';
    row.querySelector('.status-badge').className = 'status-badge status-failed';
    const errEl = row.querySelector('.error-details');
    if (errEl) { errEl.textContent = data.error; errEl.parentElement.removeAttribute('hidden'); }
    const retryForm = row.querySelector('.retry-form');
    if (retryForm) retryForm.removeAttribute('hidden');
});
```

### 10.3 Campaign List — PARTIAL State Badge

Add amber `PARTIAL` badge to `templates/campaigns.html`. Tooltip: "Some watermarking jobs failed. Click to review."

### 10.4 Campaign Detail Header Banner

When campaign state is `PARTIAL` or `FAILED`:

```
Warning: {N} watermarking job(s) failed permanently.
Use the retry buttons below to re-attempt individual tokens.
```

---

## 11. Implementation Milestones

### M1: Schema + Automatic Retry Logic

**Deliverables:**
1. `migrations/006_job_retry.sql`
2. `model.Job` — add `RetryCount int`, `MaxRetries int`, `NextRetryAt *time.Time`
3. `db.RetryOrFailJob()`, `db.ResetJobForManualRetry()`, `db.CountJobsByCampaignDetailed()`
4. `ClaimNextJob()` — add `next_retry_at` filter
5. `pool.go run()` — replace `db.FailJob()` with `db.RetryOrFailJob()` + `isPermanentFailure()`
6. `checkCampaignCompletion()` — set `READY`, `PARTIAL`, or `FAILED`

**Acceptance:** Transient failure retries 3 times with correct delays; corrupt-file failure is immediately permanent.

### M2: Stuck Job Detection

**Deliverables:**
1. `db.ResetStuckJobs()` in `queries_jobs.go`
2. `cleanup.go runOnce()` — call `ResetStuckJobs` with 30-minute threshold

**Acceptance:** Job manually set to `RUNNING` with `started_at = now - 31m` resets to `PENDING` on next cleanup tick.

### M3: UI + Manual Retry Button

**Deliverables:**
1. `handler.TokenRetry` in `campaigns.go`
2. Route in `routes.go`
3. `campaign_detail.html` — error display, retry count badge, retry form
4. `campaigns.html` — `PARTIAL` state badge
5. SSE JavaScript `token_failed` handler
6. Campaign detail header banner

**Acceptance:** Admin can retry a failed token; campaign reverts to `PROCESSING`; SSE updates badge without page reload.

### M4: Failure Notifications

**Deliverables:**
1. `email.SendJobFailed()`, `email.SendCampaignPartial()`, `email.SendCampaignFailed()`
2. `pool.go publishJobFailed()` — SSE + email on permanent failure
3. `pool.go notifyCampaignPartial()` and `notifyCampaignFailed()` in `checkCampaignCompletion()`

**Acceptance:** Campaign owner receives email when a job is permanently failed; SSE `token_failed` received by connected browser clients.

---

## 12. Open Questions

1. **Per-campaign configurable max_retries?** Deferred — system default of 3 covers most cases.
2. **Should stuck-job resets increment retry_count?** No — worker crash should not consume retry budget.
3. **What happens when manual retry on a `FAILED` campaign ultimately fully succeeds?** `checkCampaignCompletion()` runs after each job completes and transitions `FAILED` → `READY` automatically.
4. **Should `detect` jobs support retry?** No — excluded from this spec.
5. **Webhook payload for partial/failed campaigns:** The existing `campaign_ready` webhook should include a `state` field (`"READY"`, `"PARTIAL"`, or `"FAILED"`). This is a non-breaking additive change to the existing webhook spec.
