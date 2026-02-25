# Spec 05: REST API v1

**Status:** Proposed
**Date:** 2026-02-23
**Author:** Engineering

---

## Table of Contents

1. [Problem Statement & Use Cases](#1-problem-statement--use-cases)
2. [Goals & Non-Goals](#2-goals--non-goals)
3. [Authentication & API Key Lifecycle](#3-authentication--api-key-lifecycle)
4. [Common Conventions](#4-common-conventions)
5. [Rate Limiting](#5-rate-limiting)
6. [Endpoint Reference](#6-endpoint-reference)
   - [Assets](#61-assets)
   - [Recipients](#62-recipients)
   - [Campaigns](#63-campaigns)
   - [Detection](#64-detection)
7. [File Upload via API](#7-file-upload-via-api)
8. [Campaign Create & Publish Flow](#8-campaign-create--publish-flow)
9. [Progress Polling](#9-progress-polling)
10. [Versioning Strategy](#10-versioning-strategy)
11. [OpenAPI Specification](#11-openapi-specification)
12. [Implementation Plan](#12-implementation-plan)
13. [Implementation Milestones](#13-implementation-milestones)

---

## 1. Problem Statement & Use Cases

DownloadOnce currently serves all functionality through server-rendered HTML pages. There are no machine-readable endpoints. This makes the following workflows either impossible or require fragile screen-scraping:

**CI/CD integration for post-production pipelines.** A video production house renders a final cut and wants to automatically create a DownloadOnce campaign, attach recipients from a roster stored in their project management system, and publish it — all triggered from a GitHub Actions step or a Makefile target immediately after the render job completes. Without an API, this requires a human to log in, upload the file, and click through the UI.

**Custom dashboards.** A distribution team wants to embed a live campaign status widget in their internal ops dashboard (Retool, Grafana, or a bespoke React app). They need to query campaign states, download counts, and per-recipient token statuses in JSON, then render the data with their own styling and alerting logic.

**Bulk automation scripts.** A legal department distributes a package of documents to 200 law firms every quarter. They maintain the recipient list as a CSV in a shared drive. A script should be able to diff the CSV against the current recipient database, add new entries, create a campaign, and publish — without touching the browser.

**n8n / Zapier / Make.com integrations.** Low-code automation tools connect services together using HTTP actions. An n8n workflow could watch an S3 bucket for new video files, call `POST /api/v1/assets` to upload the file, call `POST /api/v1/campaigns` to create a campaign, and call `POST /api/v1/campaigns/{id}/publish` to distribute it. Without a documented JSON API, there is no path to build this integration.

**Webhook-driven receipts.** DownloadOnce already fires outbound webhooks when a download event occurs. The inverse — receiving structured JSON commands to perform actions — is the API described in this spec.

---

## 2. Goals & Non-Goals

### Goals

- Expose all major CRUD operations (assets, recipients, campaigns, tokens) as JSON REST endpoints under `/api/v1/`.
- Support campaign publish and token revoke operations programmatically.
- Support forensic watermark detection via API (upload suspect file, poll result).
- Reuse existing Bearer API key authentication — no new auth scheme.
- Return consistent JSON error envelopes and HTTP status codes.
- Document the API surface with an embedded OpenAPI 3.0 YAML file.
- Keep the implementation additive: no changes to existing HTML routes or handler behavior.

### Non-Goals

- The API does not replace the web UI. The UI remains the primary interface for human operators. The API is a parallel surface for machines.
- The API does not expose admin-only operations (user management, global campaign views) in this version. Admin operations may be added in a future `/api/v1/admin/` sub-group.
- Real-time push (WebSockets, SSE) over the API is out of scope. Progress is observed via polling.
- OAuth 2.0 or OpenID Connect is out of scope. Bearer API keys are sufficient.
- Chunked/resumable upload is out of scope for this spec; see Spec 06 (Resumable Upload). For now the API enforces a 2 GB per-request limit matching the web UI.

---

## 3. Authentication & API Key Lifecycle

### 3.1 Bearer Token Format

API keys follow the format `do_<64 hex characters>` (the prefix `do_` plus 32 random bytes encoded as 64 lowercase hex characters, for a total of 67 printable characters). Example:

```
do_4a7f3c9e1b2d8f06a3c5e7d9b1f4a2c8e6d0b3f7a9c1e5d2b4f6a8c0e2d4f6a
```

The first 8 hex characters after `do_` serve as the lookup prefix stored in the `api_keys` table. The full key is never stored; only a bcrypt hash is persisted. The plaintext key is shown to the user exactly once at creation time.

### 3.2 Key Lifecycle (UI)

API keys are managed from the **Settings** page (`/settings`). The existing UI flows are:

- **Create:** `POST /settings/apikeys` — generates key, shows plaintext once, stores bcrypt hash.
- **Delete:** `POST /settings/apikeys/{id}/delete` — removes row by ID scoped to the authenticated account.
- **List:** Rendered in the Settings page template; keys are listed with name, prefix, created timestamp, and last-used timestamp. The full key is never shown again after initial creation.

No new HTML routes are needed for key management. The API itself does not expose endpoints to create or revoke API keys; key lifecycle management remains a UI-only operation.

### 3.3 Sending the Key

Every API request must include the key in the `Authorization` header:

```
Authorization: Bearer do_4a7f3c9e1b2d8f06a3c5e7d9b1f4a2c8e6d0b3f7a9c1e5d2b4f6a8c0e2d4f6a
```

The existing `RequireAuth` middleware in `internal/handler/middleware.go` already validates this header, looks up the key by prefix, verifies the bcrypt hash, and loads the account into the request context. CSRF protection is already bypassed for Bearer requests. No changes to the middleware are required.

### 3.4 Authentication Error Response

When a request is made to an `/api/v1/` endpoint without a valid key, or with a key that does not match, the server returns:

```
HTTP/1.1 401 Unauthorized
Content-Type: application/json

{
  "error": "invalid or missing API key",
  "code": "UNAUTHORIZED"
}
```

The current `RequireAuth` middleware returns a plain-text `http.Error` for invalid keys. The API handler registration must wrap these routes with a version of `RequireAuth` that detects API routes and returns JSON instead of redirecting to `/login`. See [Section 12](#12-implementation-plan) for the implementation approach.

---

## 4. Common Conventions

### 4.1 Content Type

All API endpoints accept and return `application/json` unless noted (file upload endpoints accept `multipart/form-data`). The `Content-Type: application/json` response header is set on every response, including errors.

### 4.2 IDs

All resource IDs are UUID v4 strings (lowercase, hyphenated). Example: `"3f8a1c2d-4e5b-6f7a-8c9d-0e1f2a3b4c5d"`.

### 4.3 Timestamps

All timestamps are ISO 8601 UTC with millisecond precision and a `Z` suffix. Example: `"2026-02-23T14:37:22.481Z"`. Nullable timestamps are `null` when not set.

### 4.4 Pagination

All list endpoints support optional pagination query parameters:

| Parameter | Default | Maximum | Description |
|---|---|---|---|
| `page` | `1` | — | 1-indexed page number |
| `per_page` | `50` | `200` | Items per page |

List responses always use the following envelope:

```json
{
  "data": [ ... ],
  "total": 142,
  "page": 1,
  "per_page": 50
}
```

`total` is the total number of matching records across all pages, allowing clients to compute the number of pages without fetching them all.

### 4.5 Error Envelope

All error responses use a consistent JSON body regardless of HTTP status code:

```json
{
  "error": "human-readable description of what went wrong",
  "code": "MACHINE_READABLE_CODE"
}
```

Standard error codes:

| HTTP Status | `code` | Meaning |
|---|---|---|
| 400 | `BAD_REQUEST` | Malformed JSON, missing required field, invalid value |
| 401 | `UNAUTHORIZED` | Missing or invalid API key |
| 403 | `FORBIDDEN` | Authenticated but lacks permission for the resource |
| 404 | `NOT_FOUND` | Resource does not exist or is not visible to this account |
| 409 | `CONFLICT` | State transition is invalid (e.g. publishing an already-published campaign) |
| 413 | `PAYLOAD_TOO_LARGE` | Upload exceeds the 2 GB limit |
| 415 | `UNSUPPORTED_MEDIA_TYPE` | File type not accepted |
| 429 | `RATE_LIMITED` | Rate limit exceeded |
| 500 | `INTERNAL_ERROR` | Unexpected server error |

### 4.6 renderJSON Helper

A helper function `renderJSON` will be added to `internal/handler/handler.go` (alongside the existing `render` and `renderAuth`):

```go
func renderJSON(w http.ResponseWriter, status int, v any) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    if err := json.NewEncoder(w).Encode(v); err != nil {
        slog.Error("renderJSON encode", "error", err)
    }
}

func renderJSONError(w http.ResponseWriter, status int, code, message string) {
    renderJSON(w, status, map[string]string{
        "error": message,
        "code":  code,
    })
}
```

---

## 5. Rate Limiting

API endpoints are registered inside the same `RequireAuth`-protected group as HTML routes and share the existing in-memory per-IP `RateLimiter` (`internal/handler/ratelimit.go`). The limiter uses a token bucket: currently configured as 10 requests/second with a burst of 20 (exact values set in `cmd/server/main.go`).

### 5.1 Rate Limit Headers

When an API request is allowed, the server includes informational headers:

```
X-RateLimit-Limit: 10
X-RateLimit-Remaining: 7
X-RateLimit-Reset: 1740318000
```

- `X-RateLimit-Limit`: Sustained requests per second allowed.
- `X-RateLimit-Remaining`: Approximate tokens remaining in the current window. Because the existing limiter uses `golang.org/x/time/rate` (a token bucket, not a sliding window), this is an approximation derived from calling `limiter.Tokens()`.
- `X-RateLimit-Reset`: Unix timestamp (seconds) of when the bucket will be fully replenished. Computed as `time.Now().Add(time.Duration((burst - tokens) / rate) * time.Second).Unix()`.

Adding these headers requires a thin middleware wrapper around the existing `RateLimiter.Middleware` for API routes. The new middleware sets the headers before calling `next`.

### 5.2 Rate Limit Exceeded Response

```
HTTP/1.1 429 Too Many Requests
Content-Type: application/json
Retry-After: 2
X-RateLimit-Limit: 10
X-RateLimit-Remaining: 0
X-RateLimit-Reset: 1740318002

{
  "error": "rate limit exceeded",
  "code": "RATE_LIMITED"
}
```

---

## 6. Endpoint Reference

### Route Table

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/v1/assets` | Upload a new asset |
| `GET` | `/api/v1/assets` | List assets |
| `GET` | `/api/v1/assets/{id}` | Get a single asset |
| `DELETE` | `/api/v1/assets/{id}` | Delete an asset |
| `POST` | `/api/v1/recipients` | Create a recipient |
| `GET` | `/api/v1/recipients` | List recipients |
| `DELETE` | `/api/v1/recipients/{id}` | Delete a recipient |
| `POST` | `/api/v1/campaigns` | Create a campaign (DRAFT) |
| `GET` | `/api/v1/campaigns/{id}` | Get campaign status |
| `POST` | `/api/v1/campaigns/{id}/publish` | Publish a campaign |
| `GET` | `/api/v1/campaigns/{id}/tokens` | List tokens for a campaign |
| `POST` | `/api/v1/campaigns/{id}/recipients` | Add recipients to a campaign |
| `DELETE` | `/api/v1/campaigns/{id}/tokens/{token_id}` | Revoke a token |
| `POST` | `/api/v1/detect` | Submit a suspect file for detection |
| `GET` | `/api/v1/detect/{job_id}` | Poll detection job result |
| `GET` | `/api/v1/openapi.yaml` | OpenAPI 3.0 spec (public) |

---

### 6.1 Assets

#### POST /api/v1/assets

Upload a new asset. Accepts `multipart/form-data`. See [Section 7](#7-file-upload-via-api) for upload details.

**Auth required:** Yes
**Content-Type:** `multipart/form-data`

**Form fields:**

| Field | Required | Description |
|---|---|---|
| `file` | Yes | The file to upload (video or image) |
| `title` | No | Human-readable name; defaults to the original filename |

**Response — 201 Created:**

```json
{
  "id": "3f8a1c2d-4e5b-6f7a-8c9d-0e1f2a3b4c5d",
  "account_id": "a1b2c3d4-e5f6-7a8b-9c0d-1e2f3a4b5c6d",
  "title": "final_cut_v3.mp4",
  "asset_type": "video",
  "mime_type": "video/mp4",
  "file_size_bytes": 1073741824,
  "sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
  "duration_secs": 312.5,
  "width": 1920,
  "height": 1080,
  "created_at": "2026-02-23T14:37:22.481Z"
}
```

**Error codes:** `BAD_REQUEST` (no file / unsupported type), `PAYLOAD_TOO_LARGE` (>2 GB), `INTERNAL_ERROR`

**curl example:**

```bash
curl -X POST https://example.com/api/v1/assets \
  -H "Authorization: Bearer do_4a7f3c9e..." \
  -F "file=@/path/to/final_cut_v3.mp4" \
  -F "title=Final Cut v3"
```

---

#### GET /api/v1/assets

List all assets visible to the authenticated account.

**Auth required:** Yes
**Query params:** `page`, `per_page`

**Response — 200 OK:**

```json
{
  "data": [
    {
      "id": "3f8a1c2d-4e5b-6f7a-8c9d-0e1f2a3b4c5d",
      "title": "final_cut_v3.mp4",
      "asset_type": "video",
      "mime_type": "video/mp4",
      "file_size_bytes": 1073741824,
      "sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
      "duration_secs": 312.5,
      "width": 1920,
      "height": 1080,
      "created_at": "2026-02-23T14:37:22.481Z"
    }
  ],
  "total": 1,
  "page": 1,
  "per_page": 50
}
```

**curl example:**

```bash
curl https://example.com/api/v1/assets \
  -H "Authorization: Bearer do_4a7f3c9e..."
```

---

#### GET /api/v1/assets/{id}

Fetch a single asset by ID.

**Auth required:** Yes

**Response — 200 OK:** Same schema as an individual item in the list response above.

**Error codes:** `NOT_FOUND`

**curl example:**

```bash
curl https://example.com/api/v1/assets/3f8a1c2d-4e5b-6f7a-8c9d-0e1f2a3b4c5d \
  -H "Authorization: Bearer do_4a7f3c9e..."
```

---

#### DELETE /api/v1/assets/{id}

Delete an asset and its stored file. The asset must belong to the authenticated account (or the account must be an admin).

**Auth required:** Yes

**Response — 204 No Content** (empty body)

**Error codes:** `NOT_FOUND`, `FORBIDDEN`

**curl example:**

```bash
curl -X DELETE https://example.com/api/v1/assets/3f8a1c2d-4e5b-6f7a-8c9d-0e1f2a3b4c5d \
  -H "Authorization: Bearer do_4a7f3c9e..."
```

---

### 6.2 Recipients

#### POST /api/v1/recipients

Create a new recipient. If a recipient with the same email already exists in the account's workspace, the existing record is returned (idempotent by email).

**Auth required:** Yes
**Content-Type:** `application/json`

**Request body:**

```json
{
  "name": "Jane Smith",
  "email": "jane.smith@law-firm.com",
  "org": "Smith & Associates LLP"
}
```

| Field | Required | Description |
|---|---|---|
| `name` | Yes | Full name |
| `email` | Yes | Email address (used for deduplication) |
| `org` | No | Organisation or company name |

**Response — 201 Created** (or 200 OK if existing record returned):

```json
{
  "id": "b9c8d7e6-f5a4-3b2c-1d0e-9f8a7b6c5d4e",
  "name": "Jane Smith",
  "email": "jane.smith@law-firm.com",
  "org": "Smith & Associates LLP",
  "created_at": "2026-02-23T14:37:22.481Z"
}
```

**Error codes:** `BAD_REQUEST` (missing name or invalid email format)

**curl example:**

```bash
curl -X POST https://example.com/api/v1/recipients \
  -H "Authorization: Bearer do_4a7f3c9e..." \
  -H "Content-Type: application/json" \
  -d '{"name":"Jane Smith","email":"jane.smith@law-firm.com","org":"Smith & Associates LLP"}'
```

---

#### GET /api/v1/recipients

List all recipients.

**Auth required:** Yes
**Query params:** `page`, `per_page`

**Response — 200 OK:**

```json
{
  "data": [
    {
      "id": "b9c8d7e6-f5a4-3b2c-1d0e-9f8a7b6c5d4e",
      "name": "Jane Smith",
      "email": "jane.smith@law-firm.com",
      "org": "Smith & Associates LLP",
      "created_at": "2026-02-23T14:37:22.481Z"
    }
  ],
  "total": 1,
  "page": 1,
  "per_page": 50
}
```

**curl example:**

```bash
curl "https://example.com/api/v1/recipients?page=1&per_page=100" \
  -H "Authorization: Bearer do_4a7f3c9e..."
```

---

#### DELETE /api/v1/recipients/{id}

Delete a recipient record. Does not affect existing download tokens already issued to this recipient.

**Auth required:** Yes

**Response — 204 No Content**

**Error codes:** `NOT_FOUND`

**curl example:**

```bash
curl -X DELETE https://example.com/api/v1/recipients/b9c8d7e6-f5a4-3b2c-1d0e-9f8a7b6c5d4e \
  -H "Authorization: Bearer do_4a7f3c9e..."
```

---

### 6.3 Campaigns

#### POST /api/v1/campaigns

Create a new campaign. By default the campaign is created in `DRAFT` state. Pass `"auto_publish": true` to publish immediately after creation (equivalent to calling `POST /api/v1/campaigns/{id}/publish` in the same request).

**Auth required:** Yes
**Content-Type:** `application/json`

**Request body:**

```json
{
  "name": "Q1 2026 Distribution",
  "asset_id": "3f8a1c2d-4e5b-6f7a-8c9d-0e1f2a3b4c5d",
  "recipient_ids": [
    "b9c8d7e6-f5a4-3b2c-1d0e-9f8a7b6c5d4e",
    "c0d1e2f3-a4b5-6c7d-8e9f-0a1b2c3d4e5f"
  ],
  "max_downloads": 3,
  "expires_at": "2026-04-01T00:00:00Z",
  "visible_wm": true,
  "invisible_wm": true,
  "auto_publish": false
}
```

| Field | Required | Type | Description |
|---|---|---|---|
| `name` | Yes | string | Human-readable campaign name |
| `asset_id` | Yes | UUID | ID of an existing asset to distribute |
| `recipient_ids` | Yes | array of UUIDs | At least one recipient must be specified |
| `max_downloads` | No | integer | Download limit per token; null means unlimited |
| `expires_at` | No | ISO 8601 timestamp | When the campaign and tokens expire |
| `visible_wm` | No | boolean | Apply visible watermark overlay; defaults to `true` |
| `invisible_wm` | No | boolean | Apply invisible DWT-DCT watermark; defaults to `true` |
| `auto_publish` | No | boolean | Immediately publish after creation; defaults to `false` |

**Response — 201 Created:**

```json
{
  "id": "d4e5f6a7-b8c9-0d1e-2f3a-4b5c6d7e8f9a",
  "name": "Q1 2026 Distribution",
  "asset_id": "3f8a1c2d-4e5b-6f7a-8c9d-0e1f2a3b4c5d",
  "state": "DRAFT",
  "max_downloads": 3,
  "expires_at": "2026-04-01T00:00:00.000Z",
  "visible_wm": true,
  "invisible_wm": true,
  "jobs_total": 0,
  "jobs_completed": 0,
  "jobs_failed": 0,
  "recipient_count": 2,
  "created_at": "2026-02-23T14:37:22.481Z",
  "published_at": null
}
```

When `auto_publish: true` is set, the response reflects the post-publish state (`"state": "PROCESSING"` or `"state": "READY"` depending on watermark options) and `published_at` is populated.

**Error codes:** `BAD_REQUEST` (missing required fields, invalid asset ID, no recipients), `NOT_FOUND` (asset or recipient ID does not exist)

**curl example — create draft:**

```bash
curl -X POST https://example.com/api/v1/campaigns \
  -H "Authorization: Bearer do_4a7f3c9e..." \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Q1 2026 Distribution",
    "asset_id": "3f8a1c2d-4e5b-6f7a-8c9d-0e1f2a3b4c5d",
    "recipient_ids": ["b9c8d7e6-f5a4-3b2c-1d0e-9f8a7b6c5d4e"],
    "visible_wm": true,
    "invisible_wm": true
  }'
```

**curl example — create and auto-publish:**

```bash
curl -X POST https://example.com/api/v1/campaigns \
  -H "Authorization: Bearer do_4a7f3c9e..." \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Q1 2026 Distribution",
    "asset_id": "3f8a1c2d-4e5b-6f7a-8c9d-0e1f2a3b4c5d",
    "recipient_ids": ["b9c8d7e6-f5a4-3b2c-1d0e-9f8a7b6c5d4e"],
    "auto_publish": true
  }'
```

---

#### GET /api/v1/campaigns/{id}

Fetch current campaign state. Used for progress polling during watermarking (see [Section 9](#9-progress-polling)).

**Auth required:** Yes

**Response — 200 OK:**

```json
{
  "id": "d4e5f6a7-b8c9-0d1e-2f3a-4b5c6d7e8f9a",
  "name": "Q1 2026 Distribution",
  "asset_id": "3f8a1c2d-4e5b-6f7a-8c9d-0e1f2a3b4c5d",
  "state": "PROCESSING",
  "max_downloads": 3,
  "expires_at": "2026-04-01T00:00:00.000Z",
  "visible_wm": true,
  "invisible_wm": true,
  "jobs_total": 50,
  "jobs_completed": 12,
  "jobs_failed": 0,
  "recipient_count": 50,
  "downloaded_count": 0,
  "created_at": "2026-02-23T14:37:22.481Z",
  "published_at": "2026-02-23T14:38:00.000Z"
}
```

Campaign `state` values:

| State | Description |
|---|---|
| `DRAFT` | Created, not yet published |
| `PROCESSING` | Published, watermark jobs running |
| `READY` | All jobs complete, download links active |
| `EXPIRED` | Past `expires_at` or manually expired |

**Error codes:** `NOT_FOUND`, `FORBIDDEN`

**curl example:**

```bash
curl https://example.com/api/v1/campaigns/d4e5f6a7-b8c9-0d1e-2f3a-4b5c6d7e8f9a \
  -H "Authorization: Bearer do_4a7f3c9e..."
```

---

#### POST /api/v1/campaigns/{id}/publish

Publish a campaign. The campaign must be in `DRAFT` state and have at least one recipient token. Triggers the watermark job pipeline for all tokens.

**Auth required:** Yes
**Request body:** Empty or `{}`

**Response — 200 OK:** Same schema as `GET /api/v1/campaigns/{id}` with updated `state` and `published_at`.

**Error codes:** `NOT_FOUND`, `FORBIDDEN`, `CONFLICT` (campaign not in DRAFT state), `BAD_REQUEST` (no recipients attached)

**curl example:**

```bash
curl -X POST https://example.com/api/v1/campaigns/d4e5f6a7-b8c9-0d1e-2f3a-4b5c6d7e8f9a/publish \
  -H "Authorization: Bearer do_4a7f3c9e..."
```

---

#### GET /api/v1/campaigns/{id}/tokens

List all download tokens for a campaign, with recipient details and download stats.

**Auth required:** Yes
**Query params:** `page`, `per_page`

**Response — 200 OK:**

```json
{
  "data": [
    {
      "id": "e5f6a7b8-c9d0-1e2f-3a4b-5c6d7e8f9a0b",
      "campaign_id": "d4e5f6a7-b8c9-0d1e-2f3a-4b5c6d7e8f9a",
      "recipient_id": "b9c8d7e6-f5a4-3b2c-1d0e-9f8a7b6c5d4e",
      "recipient_name": "Jane Smith",
      "recipient_email": "jane.smith@law-firm.com",
      "recipient_org": "Smith & Associates LLP",
      "state": "ACTIVE",
      "download_count": 1,
      "max_downloads": 3,
      "last_download_at": "2026-02-23T15:02:11.000Z",
      "expires_at": "2026-04-01T00:00:00.000Z",
      "download_url": "https://example.com/d/e5f6a7b8-c9d0-1e2f-3a4b-5c6d7e8f9a0b",
      "created_at": "2026-02-23T14:38:00.000Z"
    }
  ],
  "total": 50,
  "page": 1,
  "per_page": 50
}
```

Token `state` values: `PENDING`, `ACTIVE`, `CONSUMED`, `EXPIRED`.

**Error codes:** `NOT_FOUND`, `FORBIDDEN`

**curl example:**

```bash
curl https://example.com/api/v1/campaigns/d4e5f6a7-b8c9-0d1e-2f3a-4b5c6d7e8f9a/tokens \
  -H "Authorization: Bearer do_4a7f3c9e..."
```

---

#### POST /api/v1/campaigns/{id}/recipients

Add additional recipients to an existing campaign. The campaign must be in `DRAFT` state. A new `PENDING` download token is created for each recipient added. Recipients are identified by their existing IDs in the `recipients` table.

**Auth required:** Yes
**Content-Type:** `application/json`

**Request body:**

```json
{
  "recipient_ids": [
    "c0d1e2f3-a4b5-6c7d-8e9f-0a1b2c3d4e5f"
  ]
}
```

**Response — 200 OK:**

```json
{
  "added": 1,
  "skipped": 0
}
```

`skipped` counts recipient IDs that already had a token in this campaign (idempotent).

**Error codes:** `NOT_FOUND` (campaign or recipient), `FORBIDDEN`, `CONFLICT` (campaign not in DRAFT state), `BAD_REQUEST` (empty recipient_ids array)

**curl example:**

```bash
curl -X POST https://example.com/api/v1/campaigns/d4e5f6a7-b8c9-0d1e-2f3a-4b5c6d7e8f9a/recipients \
  -H "Authorization: Bearer do_4a7f3c9e..." \
  -H "Content-Type: application/json" \
  -d '{"recipient_ids":["c0d1e2f3-a4b5-6c7d-8e9f-0a1b2c3d4e5f"]}'
```

---

#### DELETE /api/v1/campaigns/{id}/tokens/{token_id}

Revoke a specific download token. Sets its state to `EXPIRED`. The recipient's link will stop working immediately. Cannot be undone.

**Auth required:** Yes

**Response — 204 No Content**

**Error codes:** `NOT_FOUND` (campaign or token), `FORBIDDEN`

**curl example:**

```bash
curl -X DELETE \
  https://example.com/api/v1/campaigns/d4e5f6a7-b8c9-0d1e-2f3a-4b5c6d7e8f9a/tokens/e5f6a7b8-c9d0-1e2f-3a4b-5c6d7e8f9a0b \
  -H "Authorization: Bearer do_4a7f3c9e..."
```

---

### 6.4 Detection

#### POST /api/v1/detect

Submit a suspected leaked file for forensic watermark detection. Accepts `multipart/form-data`. The detection job is processed asynchronously by the worker pool. Poll the returned `job_id` to get the result.

**Auth required:** Yes
**Content-Type:** `multipart/form-data`

**Form fields:**

| Field | Required | Description |
|---|---|---|
| `file` | Yes | Suspected leaked file (image or video) |

**Accepted file extensions:** `.jpg`, `.jpeg`, `.png`, `.webp`, `.mp4`, `.mkv`, `.avi`, `.mov`, `.webm`

**Response — 202 Accepted:**

```json
{
  "job_id": "f6a7b8c9-d0e1-2f3a-4b5c-6d7e8f9a0b1c",
  "state": "PENDING",
  "created_at": "2026-02-23T14:45:00.000Z"
}
```

**Error codes:** `BAD_REQUEST` (no file), `UNSUPPORTED_MEDIA_TYPE` (unrecognised extension), `PAYLOAD_TOO_LARGE`

**curl example:**

```bash
curl -X POST https://example.com/api/v1/detect \
  -H "Authorization: Bearer do_4a7f3c9e..." \
  -F "file=@/path/to/suspected_leak.mp4"
```

---

#### GET /api/v1/detect/{job_id}

Poll the status and result of a detection job.

**Auth required:** Yes

**Response — 200 OK (while pending or running):**

```json
{
  "job_id": "f6a7b8c9-d0e1-2f3a-4b5c-6d7e8f9a0b1c",
  "state": "RUNNING",
  "progress": 42,
  "created_at": "2026-02-23T14:45:00.000Z",
  "started_at": "2026-02-23T14:45:01.000Z",
  "completed_at": null,
  "result": null
}
```

**Response — 200 OK (completed, watermark found):**

```json
{
  "job_id": "f6a7b8c9-d0e1-2f3a-4b5c-6d7e8f9a0b1c",
  "state": "COMPLETED",
  "progress": 100,
  "created_at": "2026-02-23T14:45:00.000Z",
  "started_at": "2026-02-23T14:45:01.000Z",
  "completed_at": "2026-02-23T14:46:12.000Z",
  "result": {
    "match_found": true,
    "token_id": "e5f6a7b8-c9d0-1e2f-3a4b-5c6d7e8f9a0b",
    "campaign_id": "d4e5f6a7-b8c9-0d1e-2f3a-4b5c6d7e8f9a",
    "recipient_id": "b9c8d7e6-f5a4-3b2c-1d0e-9f8a7b6c5d4e",
    "recipient_name": "Jane Smith",
    "recipient_email": "jane.smith@law-firm.com",
    "confidence": "exact"
  }
}
```

**Response — 200 OK (completed, no match):**

```json
{
  "job_id": "f6a7b8c9-d0e1-2f3a-4b5c-6d7e8f9a0b1c",
  "state": "COMPLETED",
  "progress": 100,
  "created_at": "2026-02-23T14:45:00.000Z",
  "started_at": "2026-02-23T14:45:01.000Z",
  "completed_at": "2026-02-23T14:46:12.000Z",
  "result": {
    "match_found": false,
    "token_id": null,
    "campaign_id": null,
    "recipient_id": null,
    "recipient_name": null,
    "recipient_email": null,
    "confidence": null
  }
}
```

**Response — 200 OK (failed):**

```json
{
  "job_id": "f6a7b8c9-d0e1-2f3a-4b5c-6d7e8f9a0b1c",
  "state": "FAILED",
  "progress": 0,
  "error": "python subprocess exited with code 1",
  "created_at": "2026-02-23T14:45:00.000Z",
  "started_at": "2026-02-23T14:45:01.000Z",
  "completed_at": "2026-02-23T14:45:03.000Z",
  "result": null
}
```

The `confidence` field in the result reflects the detection method:
- `"exact"` — the watermark payload matched byte-for-byte in the watermark index.
- `"fuzzy"` — a fuzzy match was found within the allowed Hamming distance threshold. The result is highly likely but not guaranteed.

**Error codes:** `NOT_FOUND` (job does not exist or belongs to a different account)

**curl example:**

```bash
curl https://example.com/api/v1/detect/f6a7b8c9-d0e1-2f3a-4b5c-6d7e8f9a0b1c \
  -H "Authorization: Bearer do_4a7f3c9e..."
```

---

## 7. File Upload via API

### 7.1 Form Fields

`POST /api/v1/assets` and `POST /api/v1/detect` both accept `multipart/form-data`. The file field name is `file` in both cases. For asset upload, an optional `title` text field may be included; for detect, no additional fields are needed.

### 7.2 Size Limit

The current maximum upload size is **2 GB per request**. This matches the web UI. The limit is enforced by `r.ParseMultipartForm(cfg.MaxUploadBytes)` where `MaxUploadBytes = 2 * 1024 * 1024 * 1024`. Requests exceeding this limit will receive a `413 Payload Too Large` response before any file data is saved to disk.

This limit exists because the entire multipart body must fit within a single HTTP request. Chunked/resumable upload is tracked separately in Spec 06. Until Spec 06 is implemented, large file workflows (>2 GB) are not supported via API.

### 7.3 Supported MIME Types

Accepted types are determined by the existing `watermark.MimeToExt` and `watermark.MimeToAssetType` maps:

| MIME type | Asset type | Extension |
|---|---|---|
| `video/mp4` | `video` | `.mp4` |
| `video/x-matroska` | `video` | `.mkv` |
| `video/x-msvideo` | `video` | `.avi` |
| `video/quicktime` | `video` | `.mov` |
| `video/webm` | `video` | `.webm` |
| `image/jpeg` | `image` | `.jpg` |
| `image/png` | `image` | `.png` |
| `image/webp` | `image` | `.webp` |

MIME type is detected from the first 512 bytes of the uploaded content using Go's `http.DetectContentType`, not from the filename extension. If detection fails or returns an unsupported type, the extension from the filename is used as a fallback.

### 7.4 Response after Upload

On success the asset record is returned immediately (HTTP 201). Thumbnail generation and metadata probing (duration, resolution via ffprobe/ImageMagick) happen synchronously during the upload handler. The asset is ready to use in a campaign immediately after the 201 response.

---

## 8. Campaign Create & Publish Flow

### 8.1 Two-Step Flow

The standard API flow mirrors the UI:

```
POST /api/v1/campaigns          → creates DRAFT, returns campaign id
POST /api/v1/campaigns/{id}/publish → transitions to PROCESSING or READY
GET  /api/v1/campaigns/{id}     → poll until state == "READY"
GET  /api/v1/campaigns/{id}/tokens → retrieve download URLs
```

The two-step design allows adding recipients incrementally after creation:

```
POST /api/v1/campaigns                          → DRAFT
POST /api/v1/campaigns/{id}/recipients          → add batch 1
POST /api/v1/campaigns/{id}/recipients          → add batch 2
POST /api/v1/campaigns/{id}/publish             → publish with all recipients
```

### 8.2 Single-Call Shortcut

When `"auto_publish": true` is included in the `POST /api/v1/campaigns` body, the handler creates the campaign, creates tokens for all specified `recipient_ids`, and immediately calls the publish logic before returning. The response reflects the post-publish state. This is useful for simple automation where all recipients are known upfront:

```bash
curl -X POST https://example.com/api/v1/campaigns \
  -H "Authorization: Bearer do_4a7f3c9e..." \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Emergency Distribution",
    "asset_id": "...",
    "recipient_ids": ["...", "..."],
    "auto_publish": true
  }'
# Response: { "state": "READY", "published_at": "2026-02-23T...", ... }
```

### 8.3 Watermarking Behaviour

When `visible_wm` or `invisible_wm` is true, publishing triggers one watermark job per token. As of the current codebase (after the on-demand watermarking refactor), `CampaignPublish` calls `SetCampaignPublishedReady` directly and watermarking is deferred to the first download request per token. The API mirrors this: the campaign transitions to `READY` immediately on publish, `jobs_total` starts at 0, and watermark jobs are created on-demand as recipients access their download links.

If the watermarking pipeline is later changed to pre-compute all watermarked files at publish time, the `state` during that period will be `PROCESSING` and the progress polling pattern in [Section 9](#9-progress-polling) becomes the primary completion signal.

---

## 9. Progress Polling

For async operations, clients poll `GET /api/v1/campaigns/{id}` or `GET /api/v1/detect/{job_id}` at a reasonable interval.

### 9.1 Campaign Processing Progress

When a campaign is in `PROCESSING` state, the response includes:

```json
{
  "state": "PROCESSING",
  "jobs_total": 50,
  "jobs_completed": 12,
  "jobs_failed": 0
}
```

Suggested polling algorithm:

```
interval = 2s
max_interval = 30s
deadline = now + 60min

while campaign.state == "PROCESSING" and now < deadline:
    sleep(interval)
    campaign = GET /api/v1/campaigns/{id}
    interval = min(interval * 1.5, max_interval)

if campaign.state == "READY":
    // success
elif campaign.state == "PROCESSING" and now >= deadline:
    // timeout — check jobs_failed for partial failures
```

A campaign moves from `PROCESSING` to `READY` when all jobs reach `COMPLETED` or `FAILED` state. Jobs that `FAILED` do not block `READY`; `jobs_failed > 0` in the response flags partial failure.

### 9.2 Detection Job Progress

Detection jobs also have a `progress` integer (0–100) updated by the worker. Poll `GET /api/v1/detect/{job_id}` until `state` is `COMPLETED` or `FAILED`.

```
interval = 3s
while job.state in ("PENDING", "RUNNING"):
    sleep(interval)
    job = GET /api/v1/detect/{job_id}
```

There is no SSE stream for API clients. The SSE hub (`/campaigns/{id}/events` and `/d/{token}/events`) is browser-only.

---

## 10. Versioning Strategy

### 10.1 URL Prefix Versioning

All API endpoints are prefixed with `/api/v1/`. This is the only version currently in production.

URL versioning is chosen over header versioning (`Accept: application/vnd.downloadonce.v1+json`) for the following reasons:
- Routes are visible in server logs and proxies without parsing headers.
- `curl` examples and browser testing work without custom headers.
- chi router makes version sub-routing trivial (`r.Route("/api/v1", ...)`).

### 10.2 Breaking Changes

A breaking change is defined as any change that requires a client to update its code to continue working:
- Removing a field from a response.
- Changing the type of an existing field.
- Removing an endpoint.
- Changing the meaning of an error code.

Non-breaking changes (additive, allowed without version bump):
- Adding optional new fields to request or response bodies.
- Adding new endpoints under `/api/v1/`.
- Adding new optional query parameters.

When a breaking change is required, a new `/api/v2/` prefix is introduced. `/api/v1/` continues to be served in parallel for a deprecation period (minimum 6 months). A `Deprecation` response header and `Sunset` header will be added to v1 responses during this period. No version is ever encoded in a request header.

### 10.3 Changelog

Breaking changes between versions will be documented in `specs/CHANGELOG_API.md`.

---

## 11. OpenAPI Specification

### 11.1 Recommendation

An OpenAPI 3.0 YAML spec (`openapi.yaml`) should be maintained alongside the Go source. It serves as:
- The source of truth for external API consumers.
- Input to code generators (client SDKs, Postman collections, n8n nodes).
- Living documentation auto-rendered by tools like Swagger UI or Redoc.

### 11.2 File Location

The spec lives at `static/openapi.yaml` in the source tree. It is embedded into the binary via the `embed.go` root package (which already embeds `static/*` as `StaticFS`). No additional embedding is needed.

### 11.3 Serving the Spec

A public (no auth required) GET route serves the raw YAML:

```
GET /api/v1/openapi.yaml
```

Registered in `routes.go` before the `RequireAuth` middleware group:

```go
r.Get("/api/v1/openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/yaml")
    http.ServeFileFS(w, r, staticFS, "openapi.yaml")
})
```

### 11.4 Maintenance

The `openapi.yaml` is maintained by hand alongside code changes. It is not auto-generated from Go structs. The spec is updated in the same commit as any API endpoint change. A CI lint step (`npx @redocly/cli lint static/openapi.yaml`) should be added to the future CI pipeline to catch structural errors.

---

## 12. Implementation Plan

### 12.1 New Files

Four new handler files will be created, one per domain:

| File | Handlers |
|---|---|
| `internal/handler/api_assets.go` | `APIAssetUpload`, `APIAssetList`, `APIAssetGet`, `APIAssetDelete` |
| `internal/handler/api_campaigns.go` | `APICampaignCreate`, `APICampaignGet`, `APICampaignPublish`, `APICampaignTokenList`, `APICampaignAddRecipients`, `APICampaignRevokeToken` |
| `internal/handler/api_recipients.go` | `APIRecipientCreate`, `APIRecipientList`, `APIRecipientDelete` |
| `internal/handler/api_detect.go` | `APIDetectSubmit`, `APIDetectGet` |

### 12.2 API Route Registration in routes.go

API routes are registered in a dedicated sub-router inside the existing `RequireAuth` group. The only new code needed in `routes.go` is:

```go
// JSON API v1
r.Route("/api/v1", func(r chi.Router) {
    r.Use(h.apiRateLimit(authRL))   // adds X-RateLimit-* headers, returns JSON 429
    r.Use(h.requireAPIAuth)         // like RequireAuth but returns JSON errors, no redirect

    r.Post("/assets", h.APIAssetUpload)
    r.Get("/assets", h.APIAssetList)
    r.Get("/assets/{id}", h.APIAssetGet)
    r.Delete("/assets/{id}", h.APIAssetDelete)

    r.Post("/recipients", h.APIRecipientCreate)
    r.Get("/recipients", h.APIRecipientList)
    r.Delete("/recipients/{id}", h.APIRecipientDelete)

    r.Post("/campaigns", h.APICampaignCreate)
    r.Get("/campaigns/{id}", h.APICampaignGet)
    r.Post("/campaigns/{id}/publish", h.APICampaignPublish)
    r.Get("/campaigns/{id}/tokens", h.APICampaignTokenList)
    r.Post("/campaigns/{id}/recipients", h.APICampaignAddRecipients)
    r.Delete("/campaigns/{id}/tokens/{tokenID}", h.APICampaignRevokeToken)

    r.Post("/detect", h.APIDetectSubmit)
    r.Get("/detect/{jobID}", h.APIDetectGet)
})
```

The OpenAPI spec route sits outside the auth group (public):

```go
r.Get("/api/v1/openapi.yaml", serveOpenAPISpec(staticFS))
```

### 12.3 requireAPIAuth Middleware

A new `requireAPIAuth` middleware is needed that behaves like `RequireAuth` but returns JSON `401` instead of redirecting to `/login`, and returns JSON `403` instead of the plain-text `Forbidden` string:

```go
func (h *Handler) requireAPIAuth(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        authHeader := r.Header.Get("Authorization")
        if !strings.HasPrefix(authHeader, "Bearer do_") {
            renderJSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid or missing API key")
            return
        }
        apiKey := strings.TrimPrefix(authHeader, "Bearer ")
        accountID, ok := h.validateAPIKey(apiKey)
        if !ok {
            renderJSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid or missing API key")
            return
        }
        account, err := db.GetAccountByID(h.DB, accountID)
        if err != nil || account == nil || !account.Enabled {
            renderJSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "account is disabled")
            return
        }
        ctx := auth.ContextWithAccountAndRole(r.Context(), accountID, account.Role, account.Name)
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}
```

Note: this middleware only accepts Bearer tokens. Session cookies are not valid for API routes. This is intentional — the API is designed for machine clients.

### 12.4 Reusing Existing Business Logic

API handlers share the same DB query functions as HTML handlers. There is no duplication of business logic. The difference is only in how the response is serialised.

For example, `APIAssetList` calls `db.ListAssets(h.DB)` (identical to `AssetList`) but instead of calling `h.renderAuth(...)`, it calls `renderJSON(w, 200, paginatedResponse(assets, total, page, perPage))`.

For `APIAssetUpload`, the existing `processOneUpload` method is reused directly. The API handler calls it for the single `file` field instead of iterating `r.MultipartForm.File["files"]`.

For `APICampaignPublish`, the existing `SetCampaignPublishedReady` DB call and the email sending loop are reused verbatim. The HTML redirect at the end is replaced with a `renderJSON` call.

### 12.5 Pagination Helper

A small helper avoids repeating the pagination envelope construction:

```go
type paginatedResult struct {
    Data    any `json:"data"`
    Total   int `json:"total"`
    Page    int `json:"page"`
    PerPage int `json:"per_page"`
}

func paginate(r *http.Request) (page, perPage int) {
    page, _ = strconv.Atoi(r.URL.Query().Get("page"))
    if page < 1 { page = 1 }
    perPage, _ = strconv.Atoi(r.URL.Query().Get("per_page"))
    if perPage < 1 { perPage = 50 }
    if perPage > 200 { perPage = 200 }
    return
}
```

Pagination in the DB layer currently does not use `LIMIT`/`OFFSET` (all rows are fetched). For the initial implementation, the API handler fetches all rows and slices in Go. Adding DB-level pagination is a future optimisation.

---

## 13. Implementation Milestones

### M1: Foundation (auth, conventions, assets)

**Deliverables:**
- `renderJSON` and `renderJSONError` helpers added to `internal/handler/handler.go`
- `requireAPIAuth` middleware added to `internal/handler/middleware.go`
- `apiRateLimit` wrapper middleware with `X-RateLimit-*` headers
- `internal/handler/api_assets.go` with `APIAssetUpload`, `APIAssetList`, `APIAssetGet`, `APIAssetDelete`
- `/api/v1/assets` routes registered in `routes.go`
- `/api/v1/openapi.yaml` route serving from embedded static FS
- Initial `static/openapi.yaml` covering assets endpoints

**Acceptance criteria:**
- `curl -X POST /api/v1/assets` with a valid MP4 file and valid Bearer key returns 201 with the asset JSON.
- `curl /api/v1/assets` returns the paginated list.
- `curl -X DELETE /api/v1/assets/{id}` returns 204 and removes the file from disk.
- A request with no Bearer header returns 401 JSON, not a redirect.
- A request exceeding rate limit returns 429 JSON with `X-RateLimit-*` headers.

### M2: Campaigns & Recipients

**Deliverables:**
- `internal/handler/api_recipients.go`
- `internal/handler/api_campaigns.go`
- All campaign and recipient routes registered in `routes.go`
- OpenAPI spec updated to cover recipients and campaigns sections

**Acceptance criteria:**
- Full two-step flow (create DRAFT, add recipients, publish) works end-to-end via `curl`.
- `auto_publish: true` shortcut returns a READY campaign in a single call.
- `GET /api/v1/campaigns/{id}` returns `jobs_total`, `jobs_completed`, `jobs_failed`.
- `DELETE /api/v1/campaigns/{id}/tokens/{token_id}` revokes the token; subsequent download attempts return an expired/invalid error page.

### M3: Detection

**Deliverables:**
- `internal/handler/api_detect.go`
- `/api/v1/detect` routes registered
- OpenAPI spec updated to cover detection endpoints

**Acceptance criteria:**
- `POST /api/v1/detect` with a watermarked image returns 202 with a `job_id`.
- Polling `GET /api/v1/detect/{job_id}` eventually returns `"state": "COMPLETED"` with a populated `result` object.
- Polling a job ID belonging to a different account returns 404.

### M4: OpenAPI & Polish

**Deliverables:**
- Complete `static/openapi.yaml` covering all v1 endpoints with request/response schemas, example values, and error descriptions
- CI lint step (`@redocly/cli lint`) added to `Makefile` or GitHub Actions workflow
- API key lifecycle documented in `openapi.yaml` under a top-level `security` section
- `Deprecation` header logic stubbed (empty for v1, ready for when v2 is introduced)
- Integration test script (`scripts/api_smoke_test.sh`) exercising all endpoints against a local instance

**Acceptance criteria:**
- `npx @redocly/cli lint static/openapi.yaml` exits 0.
- The smoke test script passes against a freshly started Docker container.
- `GET /api/v1/openapi.yaml` returns the YAML with `Content-Type: application/yaml`.
