# Spec 01: Resumable / Chunked File Upload

**Status:** Draft
**Date:** 2026-02-23
**Affects:** `internal/handler/assets.go`, `internal/db/`, `internal/model/`, `internal/cleanup/`, `internal/app/`, `templates/asset_upload.html`

---

## 1. Problem Statement

The current upload path (`POST /assets/upload`, handled by `AssetUploadSubmit` in `internal/handler/assets.go`) calls `r.ParseMultipartForm(32 << 20)` and then streams each `multipart.FileHeader` directly to disk via `io.Copy`. This works for small files but breaks down for the primary use case: 4K video files that routinely reach 10–50 GB.

Concrete failure modes:

- **Network timeouts.** A 20 GB file over a 100 Mbps upload link takes ~27 minutes. Browser and reverse-proxy (Caddy, nginx) default timeout values (60–120 s) will abort the connection long before transfer completes.
- **No recovery.** If the connection drops at 99%, the entire transfer must restart from zero. There is no mechanism to resume at the byte offset already received.
- **No progress visibility.** The form submits synchronously. The browser shows no progress indicator and the page appears frozen for the entire transfer duration.
- **Server-side memory pressure.** `ParseMultipartForm` buffers up to 32 MB in memory before spilling to a temp file managed by the Go stdlib. For concurrent uploads this adds up and the temp file is held open across the full duration of the request.
- **HTTP server timeout.** `http.Server` has no `WriteTimeout` configured today, but any deployment behind Caddy (which the project ships with via `Caddyfile`) will hit the upstream timeout.
- **Proxy body-size limits.** Default Caddy and nginx installations cap request bodies at 0 (no limit) or a configurable value; operators frequently cap at 1–2 GB without realising it.

The net result: uploading a 4K video is unreliable in practice and gives no user feedback. This is the single largest reliability gap for the target persona (filmmakers distributing large files).

---

## 2. Goals and Non-Goals

### Goals

- Allow files up to `MAX_UPLOAD_BYTES` (default 50 GB) to be uploaded reliably over flaky connections.
- Split uploads into fixed-size chunks (default 8 MB); each chunk is a separate HTTP request.
- Resume an interrupted upload by re-sending only the missing chunks, identified by byte range.
- Show a real-time progress bar in the browser with bytes transferred, percentage, and estimated time remaining.
- Validate the assembled file (MIME type, max size) before creating the asset record.
- Preserve the existing post-upload pipeline: FFprobe analysis, thumbnail extraction, and `db.CreateAsset`.
- Clean up incomplete sessions automatically after 24 hours.
- Keep zero new external dependencies (no TUS library, no Redis).

### Non-Goals

- Parallel multi-part upload (S3-style): chunks are sequential for simplicity; the bottleneck is disk I/O on a single server.
- Uploading from a URL or third-party source.
- Server-side deduplication by SHA-256 (may be added later; `sha256_original` already exists on the `assets` table).
- Multi-file chunked sessions in a single upload session (each file gets its own session).
- Progress persistence across browser tabs/restarts beyond what the `upload_sessions` table provides.

---

## 3. Chosen Approach

### Options Evaluated

**Option A: TUS Protocol (`tus-go-server`)**

TUS (tus.io) is a well-specified open protocol for resumable uploads. A Go server implementation (`github.com/tus/tusd`) exists. Benefits: well-tested, client libraries available (`tus-js-client`). Drawbacks:
- Adds a non-trivial dependency (`tusd` pulls in its own storage backend abstraction, hooks system, and HTTP handler that largely bypasses chi).
- Requires the client to speak TUS; the existing form upload JS would need to be replaced wholesale with `tus-js-client` (~40 kB minified), adding a JS bundle to a project that currently ships zero JavaScript dependencies.
- The TUS `PATCH` method conflicts with gorilla/csrf's method-override assumptions and requires additional CSRF exemption logic.
- Overkill: TUS handles features (concatenation, creation-with-upload) the project will never need.

**Option B: Custom chunked upload with Content-Range (chosen)**

A small set of three JSON endpoints (`init`, `PUT chunk`, `complete`) using standard `Content-Range` headers. The browser-side uploader is ~120 lines of vanilla JavaScript. Benefits:
- Zero new Go dependencies.
- Fully integrable into the existing chi router, `RequireAuth` middleware, and CSRF bypass pattern (Bearer or custom `X-CSRF-Token` header on XHR).
- The protocol is well-understood (mirrors the AWS S3 multipart upload mental model).
- Complete control over error messages, progress reporting, and SQLite state.

**Recommendation: Option B.**

The project philosophy is a single self-contained binary with minimal external dependencies. A custom chunked upload API adds three handlers and one DB table — a proportionate addition that fits naturally into the existing architecture.

---

## 4. UX Flow

### 4.1 Upload Page (`/assets/upload`)

The existing `asset_upload.html` template is replaced with a richer form. The `<form>` element no longer has `method="POST"` — upload is driven entirely by JavaScript. The legacy `POST /assets/upload` route is kept as a fallback for environments with JavaScript disabled (behaviour unchanged from today).

**Upload page layout:**

```
[ Choose Files ]  (file picker, multiple=false initially; multiple files = sequential sessions)

--- after file selection ---

filename.mp4                              42.3 GB
[=============================-------]  73%  ~4m remaining
                                         [ Pause ]  [ Cancel ]

filename2.png                             18.4 MB
[====================================]  100%  Processing...

[ Upload Another ]
```

### 4.2 State Machine (per file)

```
IDLE -> INITIALISING -> UPLOADING -> PAUSED -> UPLOADING
                                  -> COMPLETING -> DONE
                                  -> ERROR (retry button shown)
```

### 4.3 Error Recovery

- **Network drop mid-chunk:** the XHR fails; the JS retries the same chunk up to 3 times with exponential back-off (1 s, 2 s, 4 s) before showing an error banner with a "Resume" button.
- **Page reload / browser restart:** the JS stores `upload_id` in `sessionStorage`. On page load, if a stale `upload_id` exists, the JS calls `GET /upload/chunks/{upload_id}` to retrieve bytes received so far and offers to resume.
- **Session expired (>24 h):** server returns `410 Gone`; JS clears sessionStorage and prompts the user to start over.

---

## 5. API Design

All endpoints are under the authenticated route group and inherit `RequireAuth`. CSRF is bypassed for XHR requests that send `X-CSRF-Token: {token}` (gorilla/csrf accepts both the cookie+header pattern and the form field pattern; the JS will read the CSRF token from a `<meta>` tag injected into the page by the template).

For API-key (Bearer) callers, CSRF is already bypassed by the existing middleware in `routes.go`.

### 5.1 `POST /upload/chunks/init` — Initialise a session

**Request:**

```http
POST /upload/chunks/init HTTP/1.1
Content-Type: application/json
X-CSRF-Token: <token>

{
  "filename": "rushes_day3_4k.mp4",
  "total_size": 21474836480,
  "mime_type": "video/mp4"
}
```

**Validation:**
- `total_size` must be > 0 and <= `cfg.MaxUploadBytes`.
- `mime_type` must be a key in `watermark.MimeToExt`.
- `filename` must be non-empty; sanitised server-side (path separators stripped).

**Response `201 Created`:**

```json
{
  "upload_id": "550e8400-e29b-41d4-a716-446655440000",
  "chunk_size": 8388608,
  "expires_at": "2026-02-24T12:00:00Z"
}
```

**Response `400 Bad Request`:** invalid MIME type, size out of range.
**Response `413 Request Entity Too Large`:** `total_size` > `cfg.MaxUploadBytes`.

Side effect: inserts a row into `upload_sessions`; creates `data/uploads/<upload_id>/` on disk.

---

### 5.2 `PUT /upload/chunks/{upload_id}` — Upload a chunk

**Request:**

```http
PUT /upload/chunks/550e8400-e29b-41d4-a716-446655440000 HTTP/1.1
Content-Type: application/octet-stream
Content-Range: bytes 0-8388607/21474836480
Content-Length: 8388608

<binary chunk data>
```

`Content-Range` follows RFC 7233: `bytes <first>-<last>/<total>`.
`<first>` must equal `bytes_received` for the session (strictly sequential; out-of-order chunks are rejected).
`<last>` = `<first> + len(chunk) - 1`.
Final chunk: `<last>` = `<total> - 1`.

**Response `200 OK`:**

```json
{
  "bytes_received": 8388608,
  "total_size": 21474836480
}
```

**Response `400 Bad Request`:** malformed `Content-Range`, chunk too large (> 2× configured chunk size), or first byte does not match `bytes_received`.
**Response `404 Not Found`:** unknown `upload_id`.
**Response `409 Conflict`:** `first` byte < `bytes_received` (already have this data — idempotent retry is safe; return `200` with current state instead).
**Response `410 Gone`:** session expired or cancelled.
**Response `507 Insufficient Storage`:** disk write failed due to space.

**Server behaviour:**
1. Load session from `upload_sessions`; verify `account_id` matches authenticated user.
2. Parse and validate `Content-Range`.
3. Open `data/uploads/<upload_id>/data` in append mode (`O_WRONLY|O_APPEND`).
4. Stream request body to file; track bytes written.
5. Update `bytes_received` in the DB.
6. Return `200` with updated counts.

**Idempotency:** if `first` == previously received range start (exact retry of a chunk), seek to `first` and overwrite rather than appending, then return `200`. This handles the browser retrying after a network error where the server wrote the chunk but the response was lost.

---

### 5.3 `GET /upload/chunks/{upload_id}` — Query session state

Used by the JS on page reload to discover how many bytes have already been received.

**Response `200 OK`:**

```json
{
  "upload_id": "550e8400-e29b-41d4-a716-446655440000",
  "filename": "rushes_day3_4k.mp4",
  "total_size": 21474836480,
  "bytes_received": 8388608,
  "expires_at": "2026-02-24T12:00:00Z"
}
```

**Response `404`:** unknown session.
**Response `410`:** expired session.

---

### 5.4 `POST /upload/chunks/{upload_id}/complete` — Finalise upload

Called after the last chunk is confirmed received.

**Request:**

```http
POST /upload/chunks/550e8400-e29b-41d4-a716-446655440000/complete HTTP/1.1
Content-Type: application/json
X-CSRF-Token: <token>

{}
```

**Validation:**
- `bytes_received` must equal `total_size`; if not, return `400 {"error": "incomplete upload"}`.
- Re-read the first 512 bytes of the assembled file and verify MIME type matches what was declared at init time.
- Re-check `total_size` <= `cfg.MaxUploadBytes`.

**Response `200 OK`:**

```json
{
  "asset_id": "a1b2c3d4-...",
  "redirect": "/assets"
}
```

**Response `400`:** incomplete, MIME mismatch, or size violation.
**Response `410`:** session expired.

**Server behaviour** (mirrors existing `processOneUpload`):
1. Verify session completeness.
2. Detect MIME from first 512 bytes of assembled file.
3. Compute `assetID = uuid.New()`.
4. Move `data/uploads/<upload_id>/data` to `data/originals/<assetID>/source<ext>` (same filesystem → `os.Rename` is atomic).
5. Compute SHA-256 of the assembled file (streaming read).
6. Run FFprobe / thumbnail extraction (same code path as today).
7. Call `db.CreateAsset`.
8. Mark session as `COMPLETE` in `upload_sessions`, then delete the `data/uploads/<upload_id>/` directory.
9. Write audit log entry (`asset_uploaded`, account_id, asset_id).
10. Return `200` with `asset_id` and `redirect` URL.

---

### 5.5 `DELETE /upload/chunks/{upload_id}` — Cancel

Allows the JS "Cancel" button to clean up server-side state immediately.

**Response `204 No Content`:** session row deleted, temp directory removed.
**Response `404`:** unknown session.

---

## 6. Server-Side State

### 6.1 New table: `upload_sessions`

```sql
CREATE TABLE IF NOT EXISTS upload_sessions (
    id             TEXT PRIMARY KEY,                -- UUID
    account_id     TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    filename       TEXT NOT NULL,                   -- original client filename (sanitised)
    mime_type      TEXT NOT NULL,                   -- declared at init; re-validated at complete
    total_size     INTEGER NOT NULL,                -- declared total bytes
    bytes_received INTEGER NOT NULL DEFAULT 0,      -- bytes written to tmp file so far
    tmp_path       TEXT NOT NULL,                   -- absolute path: data/uploads/<id>/data
    state          TEXT NOT NULL DEFAULT 'UPLOADING'
                     CHECK (state IN ('UPLOADING','COMPLETE','CANCELLED')),
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    expires_at     TEXT NOT NULL                    -- created_at + 24h
);
CREATE INDEX IF NOT EXISTS idx_upload_sessions_account
    ON upload_sessions(account_id);
CREATE INDEX IF NOT EXISTS idx_upload_sessions_expires
    ON upload_sessions(expires_at);
```

This is added as migration `006_upload_sessions.sql`.

### 6.2 On-disk layout

```
data/
  uploads/
    <upload_id>/
      data          <- raw bytes, appended chunk by chunk
```

The `uploads/` directory is created at startup alongside `originals/`, `watermarked/`, and `detect/` in `internal/app/app.go`.

### 6.3 Go model struct

```go
// internal/model/model.go addition
type UploadSession struct {
    ID            string
    AccountID     string
    Filename      string
    MimeType      string
    TotalSize     int64
    BytesReceived int64
    TmpPath       string
    State         string // "UPLOADING", "COMPLETE", "CANCELLED"
    CreatedAt     time.Time
    ExpiresAt     time.Time
}
```

### 6.4 DB query functions (new file: `internal/db/queries_upload_sessions.go`)

```go
func CreateUploadSession(db *sql.DB, s *model.UploadSession) error
func GetUploadSession(db *sql.DB, id string) (*model.UploadSession, error)
func UpdateUploadSessionProgress(db *sql.DB, id string, bytesReceived int64) error
func MarkUploadSessionComplete(db *sql.DB, id string) error
func DeleteUploadSession(db *sql.DB, id string) error
func ListExpiredUploadSessions(db *sql.DB) ([]model.UploadSession, error)
```

---

## 7. Cleanup

### 7.1 Policy

Incomplete upload sessions older than 24 hours are automatically purged. This prevents the `data/uploads/` directory from accumulating abandoned partial files from network drops or abandoned browser tabs.

### 7.2 Integration with existing cleanup scheduler

`internal/cleanup/cleanup.go` already has a `runOnce()` method called on a configurable ticker. A new step is added there:

```go
// pseudocode — added to runOnce()
sessions, err := db.ListExpiredUploadSessions(c.DB)
for _, s := range sessions {
    os.RemoveAll(filepath.Dir(s.TmpPath)) // removes data/uploads/<id>/
    db.DeleteUploadSession(c.DB, s.ID)
    slog.Info("cleanup: removed expired upload session", "id", s.ID)
}
```

`ListExpiredUploadSessions` selects rows where `expires_at < now() AND state = 'UPLOADING'` (complete and cancelled sessions are cleaned up inline at the time of completion/cancellation).

### 7.3 `UPLOAD_SESSION_TTL_HOURS` config (optional)

The 24-hour TTL is hardcoded as a constant initially. A follow-up can expose `UPLOAD_SESSION_TTL_HOURS` as an env var if operators need longer windows (e.g., very slow upload connections on remote locations).

---

## 8. Integration with Existing Asset Pipeline

Once `POST /upload/chunks/{upload_id}/complete` assembles the file, the code path merges with the existing `processOneUpload` logic. The key steps are identical:

1. **MIME detection** — `http.DetectContentType` on first 512 bytes.
2. **Asset type** — `watermark.MimeToAssetType[mimeType]`.
3. **Extension** — `watermark.MimeToExt[mimeType]`.
4. **Asset directory** — `data/originals/<assetID>/`.
5. **File placement** — `os.Rename(tmpPath, srcPath)` (same filesystem, atomic).
6. **SHA-256** — streamed during the rename step or as a separate pass; stored in `assets.sha256_original`.
7. **FFprobe** — `watermark.Probe(srcPath)` → duration, width, height.
8. **Thumbnail** — `watermark.ExtractVideoThumbnail` or `watermark.ExtractImageThumbnail`.
9. **`db.CreateAsset`** — inserts into `assets` table.

The existing `processOneUpload` helper in `internal/handler/assets.go` can be refactored into a shared `finaliseAsset(accountID, srcPath, originalName, mimeType string) (string, error)` function that both the legacy multipart handler and the new complete handler call. This avoids duplicating the FFprobe/thumbnail/DB logic.

The legacy `POST /assets/upload` multipart handler remains active and unchanged for backward compatibility (API clients, CI scripts, small test uploads).

---

## 9. Error Handling

| Scenario | Server Response | Client Behaviour |
|---|---|---|
| Chunk arrives with `first` > `bytes_received` (gap) | `400 Bad Request`, `{"error": "expected offset <N>, got <M>"}` | JS retries the chunk at the correct offset |
| Chunk arrives with `first` < `bytes_received` (overlap) | `200 OK` with current state (idempotent) | JS advances to next chunk |
| `Content-Range` total does not match `total_size` in session | `400` | JS aborts, shows error |
| Chunk body larger than 2× configured chunk size | `413` | JS never sends chunks this large; shown as error if hit |
| Session expired mid-upload | `410 Gone` | JS clears sessionStorage, prompts restart |
| Disk full during chunk write | `507 Insufficient Storage` | JS shows "Server storage full" error; does not retry |
| FFprobe fails at complete step | `200` still returned, asset created without duration/dimensions; warning logged | User sees asset in list without metadata |
| MIME mismatch at complete step | `400 Bad Request`, `{"error": "mime type mismatch: declared video/mp4, detected image/jpeg"}` | JS shows error, upload must restart |
| Concurrent `PUT` for same session | SQLite `UPDATE` serialises via WAL + busy timeout; second writer gets the correct `bytes_received` from DB after first commits | Chunks remain sequential |
| `os.Rename` fails (cross-device) | `500`; session left in `UPLOADING` state; cleanup will purge after TTL | JS shows error; user retries |

---

## 10. Security

### 10.1 Authentication

All five chunk endpoints (`init`, `PUT`, `GET`, `complete`, `DELETE`) live inside the `r.Group` that applies `h.RequireAuth`. Unauthenticated requests get `302 /login` (cookie flow) or `401` (Bearer flow).

Session ownership is enforced: every handler loads the session from the DB and checks `session.AccountID == auth.AccountFromContext(r.Context())`, returning `404` on mismatch (does not reveal the session exists).

### 10.2 Content-Range Validation

Server-side validation (not trusting client):
- Parse `Content-Range` header with a strict regex: `bytes (\d+)-(\d+)/(\d+)`.
- Assert `first >= 0`, `last >= first`, `total == session.TotalSize`.
- Assert `first == session.BytesReceived` (sequential enforcement).
- Assert `(last - first + 1) == Content-Length` value.
- Assert chunk body length (bytes actually read) matches `Content-Length`.

### 10.3 Max File Size Enforcement

`total_size` declared at init is checked against `cfg.MaxUploadBytes`. Additionally, `bytes_received` is checked on every chunk PUT: if `bytes_received + Content-Length > session.TotalSize`, the request is rejected with `400`. This prevents a client from sneaking extra bytes in the final chunk.

### 10.4 MIME Validation

The declared `mime_type` at init is checked against `watermark.MimeToExt`. At complete time, the first 512 bytes of the assembled file are re-read and `http.DetectContentType` is run again. If the detected type differs from the declared type, the complete request is rejected and the session is left in `UPLOADING` state (the user can call DELETE or wait for cleanup).

### 10.5 Path Traversal

`filename` from the init request is sanitised: `filepath.Base(filename)` strips any directory separators. The tmp path is constructed from `upload_id` (a UUID), not from the filename.

### 10.6 Upload Session ID Enumeration

Upload IDs are UUIDs generated by `uuid.New()` (crypto/rand-backed). A 128-bit random ID is not guessable. The account-ownership check ensures even a leaked UUID cannot be exploited by a different account.

### 10.7 CSRF

The chunk `PUT` carries binary data with `Content-Type: application/octet-stream`, so the gorilla/csrf middleware would normally require a CSRF token. Two options:
1. The JS reads the CSRF token from a `<meta name="csrf-token">` tag and sends it as `X-CSRF-Token` header on the `init` and `complete` requests. The `PUT` requests are exempt because they use `Content-Type: application/octet-stream` and gorilla/csrf only validates the token for non-GET, non-HEAD requests with `application/x-www-form-urlencoded` or `multipart/form-data` content types — however this behaviour should be confirmed and a custom exemption added for the PUT chunk route if needed.
2. Alternatively, register the chunk PUT route with explicit CSRF exemption using a wrapper (similar to the Bearer-token bypass already in `routes.go`).

The safest approach is to require `X-CSRF-Token` on `init` and `complete` (JSON bodies), and exempt the binary `PUT` from CSRF (it is authenticated via session cookie and the upload_id acts as a capability token — possession of a valid UUID that belongs to the authenticated account is sufficient).

---

## 11. Implementation Milestones

### M1: Backend Chunked Upload API (no JS changes)

**Deliverables:**
- Migration `006_upload_sessions.sql` with the `upload_sessions` table.
- `internal/model/model.go`: add `UploadSession` struct.
- `internal/db/queries_upload_sessions.go`: all six query functions.
- `internal/handler/upload.go`: new handler file with `UploadInit`, `UploadChunk`, `UploadStatus`, `UploadComplete`, `UploadCancel` methods.
- `internal/handler/routes.go`: register the five new routes.
- `internal/app/app.go`: ensure `data/uploads/` directory is created at startup.
- `internal/cleanup/cleanup.go`: add expired-session cleanup step.
- Manual test: `curl` a 1 GB file in 8 MB chunks using shell script.

**Acceptance criteria:**
- A file chunked via curl arrives at `data/originals/<id>/source.mp4` with correct SHA-256.
- The asset appears in `/assets` list with correct metadata.
- Aborting mid-upload and re-running resumes at the correct byte offset.

### M2: Frontend Progress UI

**Deliverables:**
- `templates/asset_upload.html`: replace form submission with XHR-based uploader.
- `static/upload.js`: vanilla JS (~120 lines) implementing the chunk loop, progress bar, pause/resume, and sessionStorage resume logic.
- `templates/layout.html`: add `<meta name="csrf-token">` tag.

**Acceptance criteria:**
- Progress bar updates in real time during upload.
- Pausing stops the XHR loop; resuming continues from the last confirmed offset.
- Page reload mid-upload prompts the user to resume; clicking "Resume" continues correctly.
- A successful upload redirects to `/assets` with a flash message "Asset uploaded successfully."

### M3: Polish and Hardening

**Deliverables:**
- Multi-file support: the file picker allows multiple files; each gets its own sequential upload session shown in the UI.
- Retry logic: up to 3 automatic retries per chunk with exponential back-off before surfacing an error to the user.
- `UPLOAD_SESSION_TTL_HOURS` env var to configure session TTL (default 24).
- Audit log entries for upload start and completion.
- Admin view: admins can see in-progress upload sessions (count, total bytes pending) in `/admin` dashboard.
- Load test: upload a 20 GB file with an artificial 1% packet loss simulation to validate retry logic.

---

## 12. Schema Changes

### New migration file: `migrations/006_upload_sessions.sql`

```sql
-- Upload sessions for chunked resumable uploads
CREATE TABLE IF NOT EXISTS upload_sessions (
    id             TEXT PRIMARY KEY,
    account_id     TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    filename       TEXT NOT NULL,
    mime_type      TEXT NOT NULL,
    total_size     INTEGER NOT NULL,
    bytes_received INTEGER NOT NULL DEFAULT 0,
    tmp_path       TEXT NOT NULL,
    state          TEXT NOT NULL DEFAULT 'UPLOADING'
                     CHECK (state IN ('UPLOADING','COMPLETE','CANCELLED')),
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    expires_at     TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_upload_sessions_account
    ON upload_sessions(account_id);

CREATE INDEX IF NOT EXISTS idx_upload_sessions_expires
    ON upload_sessions(expires_at);
```

No existing tables are modified. The migration is additive only.

---

## 13. Files to Modify or Create

### New files

| Path | Description |
|---|---|
| `migrations/006_upload_sessions.sql` | Schema for `upload_sessions` table |
| `internal/db/queries_upload_sessions.go` | CRUD queries for `upload_sessions` |
| `internal/handler/upload.go` | Five HTTP handler methods for the chunked upload API |
| `static/upload.js` | Vanilla JS uploader (progress bar, pause/resume, retry) |

### Modified files

| Path | Change |
|---|---|
| `internal/model/model.go` | Add `UploadSession` struct |
| `internal/handler/assets.go` | Extract `processOneUpload` logic into shared `finaliseAsset` helper; both multipart and chunk-complete handlers call it |
| `internal/handler/routes.go` | Register 5 new routes under `/upload/chunks/...`; add CSRF exemption for the binary PUT route |
| `internal/handler/handler.go` | No structural changes; `Handler` struct gains no new fields (DB and Cfg are already present) |
| `internal/app/app.go` | Add `data/uploads` to the directory creation list at startup |
| `internal/cleanup/cleanup.go` | Add expired upload session cleanup step to `runOnce()` |
| `internal/config/config.go` | Optionally add `UploadSessionTTLHours int` (M3) |
| `templates/asset_upload.html` | Replace synchronous form with JS-driven upload UI and progress bar |
| `templates/layout.html` | Add `<meta name="csrf-token" content="{{.CSRFToken}}">` for JS to read |

### No changes needed

| Path | Reason |
|---|---|
| `internal/worker/pool.go` | Chunked upload does not affect the watermark job pipeline |
| `internal/watermark/*.go` | Assembled file enters the existing pipeline unchanged |
| `internal/sse/hub.go` | Upload progress is reported via XHR JSON responses, not SSE |
| `embed.go` | `static/upload.js` is in the `static/` tree already embedded by `StaticFS` |
| `go.mod` / `go.sum` | No new dependencies |
| `Dockerfile` / `docker-compose.yml` | No changes; `data/uploads` is inside `DATA_DIR` which is already a volume mount |

---

## Appendix A: Chunk Size Rationale

8 MB per chunk is chosen as the default because:
- It is large enough to amortise HTTP round-trip overhead (even at 1 s RTT, 8 MB gives ~64 Mbps effective throughput per connection).
- It is small enough that a failed chunk wastes at most 8 MB of re-upload (< 0.1% of a 10 GB file).
- It fits comfortably in the Go HTTP server's default buffer; no special `MaxBytesReader` tuning is needed.
- It matches the minimum part size for AWS S3 multipart uploads (5 MB), so the mental model is familiar to engineers.

The chunk size is returned by the server at init time and is not configurable by the client. This ensures the server can enforce limits uniformly.

## Appendix B: `Content-Range` Parsing Reference

RFC 7233 §4.2 defines the `Content-Range` header for PUT/PATCH:

```
Content-Range: bytes 0-8388607/21474836480
               ^^^^^ ^^^^^^^ ^^^^^^^^ ^^^^^^^^^^^^^^^^^
               unit  first   last     complete-length
```

Go stdlib does not have a built-in `Content-Range` parser for request headers (only `Range` for responses). A small parsing helper is needed:

```go
// internal/handler/upload.go
import "fmt"

type contentRange struct {
    First int64
    Last  int64
    Total int64
}

func parseContentRange(header string) (contentRange, error) {
    var cr contentRange
    n, err := fmt.Sscanf(header, "bytes %d-%d/%d", &cr.First, &cr.Last, &cr.Total)
    if err != nil || n != 3 {
        return cr, fmt.Errorf("invalid Content-Range: %q", header)
    }
    if cr.First < 0 || cr.Last < cr.First || cr.Total <= cr.Last {
        return cr, fmt.Errorf("Content-Range bounds invalid: %q", header)
    }
    return cr, nil
}
```

## Appendix C: JavaScript Upload Loop Sketch

```javascript
// static/upload.js  (vanilla ES2020, no dependencies)

const CHUNK_SIZE = 8 * 1024 * 1024; // returned by server; hardcode as fallback

async function uploadFile(file, csrfToken, onProgress) {
  // 1. Init session
  const init = await fetch('/upload/chunks/init', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken },
    body: JSON.stringify({ filename: file.name, total_size: file.size, mime_type: file.type }),
  });
  if (!init.ok) throw new Error(await init.text());
  const { upload_id, chunk_size } = await init.json();
  sessionStorage.setItem('upload_id', upload_id);

  let offset = 0;

  // 2. Check for resume
  const status = await fetch(`/upload/chunks/${upload_id}`);
  if (status.ok) {
    const s = await status.json();
    offset = s.bytes_received;
  }

  // 3. Chunk loop
  while (offset < file.size) {
    const slice = file.slice(offset, offset + chunk_size);
    const last = Math.min(offset + slice.size - 1, file.size - 1);
    let attempts = 0;
    while (attempts < 3) {
      const resp = await fetch(`/upload/chunks/${upload_id}`, {
        method: 'PUT',
        headers: {
          'Content-Type': 'application/octet-stream',
          'Content-Range': `bytes ${offset}-${last}/${file.size}`,
          'Content-Length': slice.size,
        },
        body: slice,
      });
      if (resp.ok) {
        const r = await resp.json();
        offset = r.bytes_received;
        onProgress(offset, file.size);
        break;
      }
      if (resp.status === 410) throw new Error('Upload session expired');
      attempts++;
      await new Promise(r => setTimeout(r, 1000 * Math.pow(2, attempts - 1)));
    }
    if (attempts === 3) throw new Error('Chunk upload failed after 3 attempts');
  }

  // 4. Complete
  const done = await fetch(`/upload/chunks/${upload_id}/complete`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken },
    body: '{}',
  });
  if (!done.ok) throw new Error(await done.text());
  const { redirect } = await done.json();
  sessionStorage.removeItem('upload_id');
  window.location.href = redirect;
}
```
