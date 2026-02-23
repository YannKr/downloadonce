# DownloadOnce — Product & Technical Specification

**Version:** 0.2 (Draft)
**Date:** 2026-02-21
**Status:** Proposal

---

## Table of Contents

1. [Overview](#1-overview)
2. [Problem Statement](#2-problem-statement)
3. [Goals and Non-Goals](#3-goals-and-non-goals)
4. [User Personas](#4-user-personas)
5. [Core Feature Set](#5-core-feature-set)
6. [System Architecture](#6-system-architecture)
7. [Watermarking Subsystem](#7-watermarking-subsystem)
8. [Token & Download Subsystem](#8-token--download-subsystem)
9. [Data Model](#9-data-model)
10. [API Design](#10-api-design)
11. [Frontend](#11-frontend)
12. [Security Considerations](#12-security-considerations)
13. [Legal & Forensic Considerations](#13-legal--forensic-considerations)
14. [Implementation Phases](#14-implementation-phases)
15. [Tech Stack Recommendation](#15-tech-stack-recommendation)
16. [Open Questions](#16-open-questions)

---

## 1. Overview

**DownloadOnce** is a file-distribution platform for creators that ties every download to a named recipient and invisibly brands each distributed file with a unique, recipient-specific forensic fingerprint. If the file is ever leaked, shared, or pirated, the origin can be traced back to the specific recipient using only the leaked file itself.

Primary media: **video files** (screeners, raw footage, pre-release cuts, licensed content).
Secondary media: **images** (RAW/JPEG originals for photographers, artwork, design assets).

The product sits in the overlap between:
- **File delivery for creators** (think WeTransfer, MASV, or Frame.io)
- **Forensic watermarking** (think NAGRA NexGuard, IMATAG, PallyCon/DoveRunner)

It is designed to be **self-hosted on a single small server** with minimal dependencies. A single `docker-compose.yml` brings up the entire stack.

---

## 2. Problem Statement

Content creators, studios, production companies, and photographers routinely distribute files to a controlled audience — reviewers, clients, partners, festival programmers. Today's common methods (shared Google Drive links, email attachments, Dropbox folders) provide no traceability. When a file leaks:

- It is impossible to identify which recipient shared it.
- The creator has no forensic evidence to pursue action.
- There is no deterrent: recipients know there is no accountability.

Existing enterprise solutions (NAGRA, Synamedia, Irdeto) require six-figure licensing deals and CDN-level infrastructure integration, placing them out of reach for independent filmmakers, small studios, or individual photographers. Entry-level services (MASV, Frame.io watermarking) offer only visible burn-in watermarks, which are trivially defeated by cropping or blurring.

---

## 3. Goals and Non-Goals

### Goals

- Provide a **per-recipient, unique download link** — one link per person, multiple downloads permitted by default. Optionally limitable to a fixed count.
- Embed an **invisible forensic watermark** into every downloaded file that uniquely identifies the recipient and download event.
- Support an **audit log** and **leak detection** flow: given a leaked file, extract the watermark and identify the original recipient.
- Support **video files** (MP4, MOV, MKV, ProRes, H.264/HEVC; up to 4K) as the primary asset type. Output codec is **H.265 (x265)**.
- Support **image files** (JPEG, PNG, TIFF, WebP) as a secondary asset type.
- Offer a clean web UI for uploading content, creating distribution lists, and managing download events.
- Be **deployable on a single small server** (2–4 vCPU, 4–8 GB RAM). Storage is a **local filesystem directory** — no cloud object storage required.
- **Pre-compute watermarked files at ingestion/campaign-publish time**, not at download time. This frontloads CPU-intensive encoding and makes downloads a simple file serve.
- Provide a **REST API** for programmatic integration.
- Written in **Go** for a small, efficient, single-binary deployment with low memory footprint.

### Non-Goals

- Real-time adaptive bitrate (HLS/DASH) streaming. This is a **file download** platform only.
- DRM (Widevine/PlayReady/FairPlay) integration.
- Audio-only file watermarking.
- Document/PDF watermarking.
- High-traffic optimization or horizontal scaling. The target is a single server handling tens to low hundreds of recipients per campaign.
- Multi-tenancy with separate billing per tenant — v1 targets a single-owner deployment.

---

## 4. User Personas

### 4.1 Independent Filmmaker / Small Studio
Distributing a pre-release cut to 30–100 screener recipients (festival programmers, press, award voters). Needs to know if a screener leaks, who leaked it, and have evidence for a DMCA takedown or civil action.

Key needs: simple upload, paste a list of email addresses, generate links, receive a notification when a link is used, detect a leak if one occurs.

### 4.2 Photographer / Photo Agency
Distributing high-resolution RAW or JPEG images to a client before licensing is finalized, or providing deliverables to a magazine. If the image appears online early, the photographer needs to prove which client leaked it.

Key needs: batch upload of images, per-client links, invisible watermark that survives JPEG re-save and resizing.

### 4.3 Production Company / Post-House (Secondary)
Delivering VFX rushes or rough cuts to a remote partner or executive. Needs an audit trail and large file support.

---

## 5. Core Feature Set

### 5.1 Upload & Asset Management

- Authenticated upload of video and image files via web UI and API.
- Files stored on **local filesystem** in a configurable data directory (e.g., `./data/originals/`).
- Per-asset metadata: title, description, content type (video/image), upload timestamp, SHA-256 hash of the original.
- On upload, a lightweight analysis job (FFprobe) determines file duration (video), resolution, and format.
- A preview thumbnail/poster frame is extracted and stored for the UI.

### 5.2 Distribution Campaigns

- A **campaign** groups an asset with one or more named recipients.
- Each recipient is defined by: name, email address, optional organization.
- Creating a campaign produces one **download token** per recipient.
- **Publishing a campaign triggers the watermark pre-computation job**: one uniquely watermarked file is generated per recipient and stored on disk. Downloads become instant file serves.
- Campaign expiry: a configurable deadline after which all tokens stop working.
- Download limit per token: default unlimited, optionally limitable to a fixed count.

### 5.3 Token-Based Download Links

- Each token produces a unique, opaque URL: `https://example.com/d/<token>`
- The token is a UUID v4 mapped to a record in the database — it encodes no information on its own.
- On access, the platform validates the token and serves the **pre-computed, recipient-specific watermarked file** directly from disk.
- If an optional download limit is configured and reached, the link returns `410 Gone`.
- Optional: link expiry by timestamp.

### 5.4 Forensic Watermarking

- **Watermarking happens at campaign publish time**, not at download time. This frontloads all encoding cost.
- Each recipient gets a uniquely watermarked copy of the asset, stored on disk until the campaign is deleted or expires.
- The watermark payload encodes: `campaign_id`, `token_id`, and a timestamp.
- For video: H.265 (x265) encode with an invisible/visible overlay applied via FFmpeg (see section 7).
- For images: an invisible frequency-domain watermark using the `invisible-watermark` library (`dwtDct` algorithm), invoked as a subprocess or via a small Python sidecar.
- Watermark records are persisted in the database, enabling leak detection queries.

### 5.5 Audit Log & Notifications

- Every download event is logged: token, recipient identity, IP address, User-Agent, timestamp.
- Campaign dashboard shows per-recipient download status (pending / downloaded / expired) with timestamps.
- Optional: webhook notification on each download event.

### 5.6 Leak Detection

- The platform exposes a **detection endpoint**: the owner submits a suspected leaked file.
- For images: automated detection via the `invisible-watermark` decoder extracts the payload and returns the matching recipient.
- For video: automated frame extraction + invisible watermark detection on key frames. Visible watermark OCR (Tesseract) as fallback.
- A forensic report is produced linking the extracted watermark to a specific campaign, token, and recipient.

### 5.7 Deterrence Features

- Optional: a **visible semi-transparent overlay** (in addition to the invisible mark) showing the recipient's name and a timestamp, appearing at randomized positions during playback.
- Optional: a **fingerprint notice** presented to the recipient at download time (see section 13.2).

---

## 6. System Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                     Client (Browser / API)                   │
└───────────────────────────┬─────────────────────────────────┘
                            │ HTTPS
┌───────────────────────────▼─────────────────────────────────┐
│                  Go Application Server                       │
│                  (single binary)                             │
│                                                              │
│  ┌──────────────┐  ┌──────────┐  ┌────────────────────────┐ │
│  │  HTTP Router  │  │ REST API │  │  Background Workers    │ │
│  │  (chi/echo)   │  │          │  │  (in-process goroutine │ │
│  │  + static     │  │          │  │   pool + job table)    │ │
│  │  file server  │  │          │  │                        │ │
│  └──────────────┘  └──────────┘  └────────────────────────┘ │
└────────────┬──────────────┬──────────────┬──────────────────┘
             │              │              │
   ┌─────────▼────┐  ┌─────▼──────┐  ┌────▼─────────────────┐
   │  SQLite DB   │  │  FFmpeg /  │  │  Local Filesystem     │
   │  (WAL mode)  │  │  FFprobe   │  │  ./data/              │
   │              │  │  (subprocess│  │    originals/         │
   │              │  │   calls)   │  │    watermarked/        │
   │              │  │            │  │    thumbnails/         │
   └──────────────┘  └────────────┘  └───────────────────────┘
```

### Key Design Decisions

**Pre-compute watermarked files at campaign publish time.**
When a campaign is published, a background job encodes one watermarked copy per recipient. Video files are re-encoded to H.265 with the watermark burned in; images get invisible DWT-DCT marks. Once complete, downloads are simple static file serves with zero processing latency. This trades disk space for CPU time at the right moment — ingestion is infrequent; downloads should be instant.

**Single Go binary, no external services beyond FFmpeg.**
The application server, HTTP router, background worker pool, and embedded database all live in one process. No Redis, no separate job queue, no message broker. The job queue is a database table polled by in-process goroutines. This minimizes operational complexity and resource usage.

**SQLite in WAL mode as the database.**
For a single-server deployment handling tens to hundreds of recipients, SQLite is sufficient and eliminates the PostgreSQL dependency. WAL mode allows concurrent reads during writes. If the deployment scales beyond a single server in the future, a migration path to PostgreSQL exists.

**Local filesystem for all file storage.**
Originals, watermarked copies, and thumbnails are stored in a configurable directory (default: `./data/`). No S3, no object storage API. The directory can be backed up with standard tools (rsync, restic, borgbackup). For users who want S3-compatible storage, a FUSE mount (s3fs, rclone mount) or a local MinIO container can be placed in front of the data directory — but this is not a first-class concern.

**Docker Compose as the primary deployment artifact.**
A single `docker-compose.yml` brings up:
1. The Go application container (includes the compiled binary, FFmpeg, and the Python `invisible-watermark` tool).
2. A volume mount for `./data/` (persistent storage).
3. Optional: Caddy as a reverse proxy for HTTPS termination.

That's it. No PostgreSQL container, no Redis container.

### Directory Layout

```
./data/
  originals/
    {asset_id}/
      source.mp4              # original uploaded file
      thumb.jpg               # poster frame / thumbnail
      metadata.json           # ffprobe output
  watermarked/
    {campaign_id}/
      {token_id}.mp4          # per-recipient watermarked video
      {token_id}.jpg          # per-recipient watermarked image
  db/
    downloadonce.db           # SQLite database
    downloadonce.db-wal       # WAL file
    downloadonce.db-shm       # shared memory file
```

---

## 7. Watermarking Subsystem

### 7.1 Video Watermarking

All video watermarking uses **H.265 (libx265)** as the output codec regardless of the input codec. This provides good compression for large files, reducing storage and bandwidth for watermarked copies.

**Primary method: FFmpeg visible + invisible overlay (burn-in at publish time)**

A semi-transparent text or code string is composited onto every frame. The overlay contains an encoded payload (a short alphanumeric hash of the `token_id`) and optionally the recipient's name.

```
ffmpeg -i originals/{asset_id}/source.mp4 \
  -vf "drawtext=
    text='[%{eif\:rand(0,1)\:d}a3f8c2d1]':
    fontcolor=white@0.15:fontsize=11:
    x='if(lt(mod(t,60),30),w-text_w-20,20)':
    y='if(lt(mod(t,60),30),h-text_h-20,20)':
    fontfile=/fonts/DejaVuSans.ttf" \
  -c:v libx265 -crf 22 -preset medium \
  -tag:v hvc1 \
  -c:a copy \
  watermarked/{campaign_id}/{token_id}.mp4
```

Key properties:
- **libx265 / CRF 22 / preset medium**: good quality-to-size ratio. CRF 22 is visually near-lossless for most content. Preset `medium` balances encode speed and compression.
- **`-tag:v hvc1`**: ensures compatibility with Apple/QuickTime players.
- Opacity 0.15 — very subtle, but present in every frame.
- Position varies over time (oscillates between corners every 30 seconds) to defeat cropping.
- A second `drawtext` pass with even lower opacity (0.08) at center-frame provides robustness against edge cropping.

**Secondary method: invisible steganographic watermark on key frames**

Using the `invisible-watermark` Python library (dwtDct algorithm), a machine-readable watermark is embedded into selected I-frames (key frames) extracted from the video. This is done as a post-processing step after the FFmpeg encode:

```
Pipeline:
1. FFmpeg encodes source → watermarked/{campaign_id}/{token_id}_temp.mp4
2. Python script extracts N evenly-spaced I-frames (e.g., 1 per minute)
3. Each frame is watermarked with dwtDct (payload = token_id bytes)
4. Frames are re-injected (or the approach uses FFmpeg's select+overlay filter
   to embed during the main encode pass)
5. Final file written to watermarked/{campaign_id}/{token_id}.mp4
```

The invisible watermark provides automated detection on clean digital copies (files shared without re-encoding).

**Pre-computation pipeline (triggered on campaign publish):**

```
1. Campaign published → for each recipient/token:
2.   Enqueue job: {asset_id, token_id, campaign_id, recipient_name, settings}
3.   Worker picks up job from DB job table
4.   Read original from data/originals/{asset_id}/source.*
5.   FFmpeg: apply drawtext overlay + encode to H.265
6.   Optional: Python sidecar applies invisible watermark to key frames
7.   Write output to data/watermarked/{campaign_id}/{token_id}.<ext>
8.   Compute SHA-256 of output file
9.   Update DB: job status = complete, store watermark_payload + sha256
10.  When all jobs for a campaign are done: mark campaign as READY
11.  Tokens become ACTIVE (downloadable)
```

**Performance estimates (libx265, CRF 22, preset medium, 4-core CPU):**

| Resolution | Source Duration | Approx. Encode Time | Output Size (typical) |
|---|---|---|---|
| 1080p | 90 min film | ~45–90 min | 2–4 GB |
| 1080p | 10 min short | ~5–10 min | 200–500 MB |
| 4K | 90 min film | ~3–6 hours | 5–10 GB |
| 4K | 10 min short | ~20–40 min | 600 MB–1.2 GB |

> **Implication:** For a campaign with 50 recipients, a 90-minute 1080p film requires 50 independent encodes. On a 4-core machine running 2 concurrent encodes, this takes ~25–45 hours to pre-compute. This is acceptable for a "publish and wait" workflow. For 4K content, consider a beefier machine or limiting recipient count.

**Optimization: parallel workers.**
The number of concurrent FFmpeg workers is configurable (default: 2). On a 4-core machine with 8 GB RAM, 2 concurrent x265 encodes keep CPU near 100% without swapping. On an 8-core machine, 3–4 workers are viable.

### 7.2 Image Watermarking

**Invisible watermark (primary): DWT-DCT frequency-domain embedding**

Images are watermarked at campaign publish time using the `invisible-watermark` Python library, invoked as a subprocess from the Go application.

```python
# watermark_image.py — called by Go as a subprocess
import sys, json
from imwatermark import WatermarkEncoder
import cv2

args = json.loads(sys.argv[1])
img = cv2.imread(args['input_path'])
encoder = WatermarkEncoder()
encoder.set_watermark('bytes', bytes.fromhex(args['payload_hex']))
watermarked = encoder.encode(img, 'dwtDct')
cv2.imwrite(args['output_path'], watermarked,
    [int(cv2.IMWRITE_JPEG_QUALITY), args.get('jpeg_quality', 92)])
print(json.dumps({"status": "ok"}))
```

Algorithm properties:
- `dwtDct`: Discrete Wavelet Transform + Discrete Cosine Transform combination.
- Payload: up to 32 bytes (256 bits). Sufficient for a UUID (16 bytes).
- Survives: JPEG recompression at quality >= 75, scaling down to ~50% of original dimensions, moderate color/exposure adjustments.
- Does not survive: heavy editing, JPEG compression <50%, significant format conversion.
- Processing time: ~100–500ms per image on CPU. A batch of 100 images finishes in under a minute.

**Visible overlay (optional, per-campaign setting):**

When enabled, a subtle visible watermark (recipient name, date) is composited before invisible embedding, using Pillow or ImageMagick (invoked as a subprocess).

**Image processing pipeline (at campaign publish):**

```
1. For each recipient/token:
2.   Read original image from data/originals/{asset_id}/source.*
3.   Apply visible overlay if campaign setting enabled (ImageMagick composite)
4.   Apply invisible DWT-DCT watermark (Python subprocess)
5.   Write to data/watermarked/{campaign_id}/{token_id}.<ext>
6.   Compute SHA-256, log watermark payload in DB
```

### 7.3 Watermark Payload Structure

The watermark embeds a compact binary payload that uniquely identifies the download token. The payload is designed to be extractable from a degraded file and mapped back to a specific recipient via the database.

```
Payload (16 bytes / 128 bits):
  Bytes 0–1:   Format version (0x0001)
  Bytes 2–9:   Token ID (8 bytes, truncated SHA-256 of UUID)
  Bytes 10–13: Campaign ID (4 bytes, uint32 big-endian)
  Bytes 14–15: CRC-16 checksum of bytes 0–13
```

The full recipient identity is never embedded in the file — only an opaque ID. The database is the only link between the ID and the recipient. The database must be preserved and secured.

### 7.4 Detection Workflow

**Image detection:**
```python
from imwatermark import WatermarkDecoder
import cv2

img = cv2.imread('leaked_image.jpg')
decoder = WatermarkDecoder('bytes', 16)
payload = decoder.decode(img, 'dwtDct')
# Parse payload → token_id → query DB → return recipient
```

**Video detection:**
1. Extract key frames from the leaked video using FFmpeg (`-vf "select=eq(pict_type\,I)" -vsync vfr`).
2. Run invisible watermark detection on each extracted frame.
3. If no invisible mark found: attempt visible watermark OCR (Tesseract) on extracted frames.
4. Majority-vote across frames to determine the most likely payload.
5. Query the `watermark_index` table to return the matching recipient and campaign.

**Robustness note:** Invisible video watermarks survive clean digital copying but are destroyed by screen capture, heavy re-encoding, or re-recording. The visible overlay survives everything except cropping or blurring. Both layers together provide defense in depth.

---

## 8. Token & Download Subsystem

### 8.1 Token Lifecycle

```
State Machine:
  PENDING  → ACTIVE   (when campaign publish completes and watermarked file is ready)
  ACTIVE   → CONSUMED (on download, if optional limit is reached)
  ACTIVE   → EXPIRED  (on campaign deadline or manual revocation)
  CONSUMED / EXPIRED  → [terminal]
```

### 8.2 Token Generation

Tokens are UUID v4 strings stored in SQLite. They are opaque — all metadata is server-side. This prevents enumeration and allows tokens to be revoked at any time.

### 8.3 Download Enforcement (Atomic)

On download request:
```
1. Receive GET /d/<token>
2. Query SQLite: SELECT ... FROM download_tokens WHERE id = <token>
3. Check: state = ACTIVE AND (max_downloads IS NULL OR download_count < max_downloads)
         AND (expires_at IS NULL OR expires_at > NOW())
4. Resolve watermarked file path: data/watermarked/{campaign_id}/{token_id}.<ext>
5. Begin transaction:
   a. Increment download_count
   b. If max_downloads IS NOT NULL AND download_count >= max_downloads: set state = CONSUMED
   c. Insert download_event record
   d. Commit
6. Serve the pre-computed file with Content-Disposition: attachment
```

Since watermarked files are pre-computed, the download is a **direct file serve** — no encoding, no processing, no waiting. The server uses `http.ServeFile` (or equivalent) with range request support for resumable downloads.

### 8.4 Download URL Structure

```
https://example.com/d/<token>

Example:
https://example.com/d/a8f3c291-4e72-4b1a-9fe1-3c7a2b05d8e4
```

No information is encoded in the URL. The token maps to all metadata in the database.

### 8.5 Download Flow (HTTP)

```
GET /d/<token>

→ If token valid and file ready:
  200 OK
  Content-Disposition: attachment; filename="<campaign_name>.<ext>"
  Content-Type: video/mp4 (or image/jpeg, etc.)
  Accept-Ranges: bytes
  Content-Length: <file_size>
  [file body]

→ If token valid but file still being prepared:
  202 Accepted
  Content-Type: text/html
  [HTML page with progress indicator and auto-refresh]

→ If token consumed:
  410 Gone

→ If token expired or invalid:
  404 Not Found
```

### 8.6 Rate Limiting

- Per-IP: simple in-memory sliding window (Go `sync.Map` + timestamps), 10 requests per minute on `/d/` routes.
- Per-token: SQLite transaction serialization prevents race conditions. No additional locking needed.

---

## 9. Data Model

SQLite schema:

```sql
-- Accounts (single owner in v1)
CREATE TABLE accounts (
  id          TEXT PRIMARY KEY,  -- UUID v4
  email       TEXT NOT NULL UNIQUE,
  name        TEXT NOT NULL,
  password_hash TEXT NOT NULL,
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

-- Assets (uploaded files)
CREATE TABLE assets (
  id              TEXT PRIMARY KEY,  -- UUID v4
  account_id      TEXT NOT NULL REFERENCES accounts(id),
  title           TEXT NOT NULL,
  description     TEXT NOT NULL DEFAULT '',
  asset_type      TEXT NOT NULL CHECK (asset_type IN ('video', 'image')),
  original_path   TEXT NOT NULL,     -- relative path under data/originals/
  file_size_bytes INTEGER NOT NULL,
  sha256_original TEXT NOT NULL,
  mime_type       TEXT NOT NULL,
  duration_secs   REAL,              -- video only
  resolution_w    INTEGER,
  resolution_h    INTEGER,
  created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

-- Recipients
CREATE TABLE recipients (
  id         TEXT PRIMARY KEY,  -- UUID v4
  account_id TEXT NOT NULL REFERENCES accounts(id),
  name       TEXT NOT NULL,
  email      TEXT NOT NULL,
  org        TEXT,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
  UNIQUE (account_id, email)
);

-- Campaigns
CREATE TABLE campaigns (
  id              TEXT PRIMARY KEY,  -- UUID v4
  account_id      TEXT NOT NULL REFERENCES accounts(id),
  asset_id        TEXT NOT NULL REFERENCES assets(id),
  name            TEXT NOT NULL,
  max_downloads   INTEGER,           -- NULL = unlimited
  expires_at      TEXT,              -- NULL = never
  visible_wm      INTEGER NOT NULL DEFAULT 1,
  invisible_wm    INTEGER NOT NULL DEFAULT 1,
  state           TEXT NOT NULL DEFAULT 'DRAFT'
                    CHECK (state IN ('DRAFT','PROCESSING','READY','EXPIRED')),
  created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
  published_at    TEXT
);

-- Download tokens (one per recipient per campaign)
CREATE TABLE download_tokens (
  id              TEXT PRIMARY KEY,  -- UUID v4, this IS the token in the URL
  campaign_id     TEXT NOT NULL REFERENCES campaigns(id),
  recipient_id    TEXT NOT NULL REFERENCES recipients(id),
  max_downloads   INTEGER,           -- NULL = unlimited (inherits from campaign)
  download_count  INTEGER NOT NULL DEFAULT 0,
  state           TEXT NOT NULL DEFAULT 'PENDING'
                    CHECK (state IN ('PENDING','ACTIVE','CONSUMED','EXPIRED')),
  watermarked_path TEXT,             -- relative path to pre-computed file
  watermark_payload BLOB,            -- 16-byte payload
  sha256_output   TEXT,              -- SHA-256 of watermarked file
  output_size_bytes INTEGER,
  expires_at      TEXT,
  created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
  UNIQUE (campaign_id, recipient_id)
);

-- Download events (immutable audit log)
CREATE TABLE download_events (
  id              TEXT PRIMARY KEY,  -- UUID v4
  token_id        TEXT NOT NULL REFERENCES download_tokens(id),
  campaign_id     TEXT NOT NULL,
  recipient_id    TEXT NOT NULL,
  asset_id        TEXT NOT NULL,
  ip_address      TEXT NOT NULL,
  user_agent      TEXT,
  downloaded_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

-- Watermark index (for detection lookups)
CREATE TABLE watermark_index (
  payload_hex     TEXT PRIMARY KEY,  -- hex of 16-byte watermark payload
  token_id        TEXT NOT NULL REFERENCES download_tokens(id),
  campaign_id     TEXT NOT NULL,
  recipient_id    TEXT NOT NULL,
  created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

-- Background jobs (in-process queue)
CREATE TABLE jobs (
  id              TEXT PRIMARY KEY,  -- UUID v4
  job_type        TEXT NOT NULL,     -- 'watermark_video' | 'watermark_image' | 'detect'
  campaign_id     TEXT,
  token_id        TEXT,
  state           TEXT NOT NULL DEFAULT 'PENDING'
                    CHECK (state IN ('PENDING','RUNNING','COMPLETED','FAILED')),
  progress        INTEGER NOT NULL DEFAULT 0,  -- 0-100
  error_message   TEXT,
  created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
  started_at      TEXT,
  completed_at    TEXT
);
CREATE INDEX idx_jobs_state ON jobs(state) WHERE state IN ('PENDING', 'RUNNING');
```

---

## 10. API Design

All endpoints are prefixed `/api/v1/`. Authentication uses Bearer tokens (opaque API key, checked against `accounts` table).

The same Go binary serves both the API and the frontend (embedded static files or server-rendered templates).

### Assets

| Method | Endpoint | Description |
|---|---|---|
| `POST` | `/api/v1/assets` | Upload file (multipart/form-data) |
| `GET` | `/api/v1/assets` | List assets |
| `GET` | `/api/v1/assets/:id` | Get asset metadata |
| `DELETE` | `/api/v1/assets/:id` | Delete asset and all associated files |

### Recipients

| Method | Endpoint | Description |
|---|---|---|
| `POST` | `/api/v1/recipients` | Create recipient |
| `GET` | `/api/v1/recipients` | List recipients |
| `DELETE` | `/api/v1/recipients/:id` | Delete recipient |

### Campaigns

| Method | Endpoint | Description |
|---|---|---|
| `POST` | `/api/v1/campaigns` | Create campaign (DRAFT state) |
| `POST` | `/api/v1/campaigns/:id/publish` | Publish: triggers watermark pre-computation |
| `GET` | `/api/v1/campaigns/:id` | Get campaign detail + token statuses |
| `GET` | `/api/v1/campaigns/:id/tokens` | List tokens with per-recipient download info |
| `POST` | `/api/v1/campaigns/:id/recipients` | Add recipient(s) to campaign |
| `DELETE` | `/api/v1/campaigns/:id/tokens/:token_id` | Revoke a specific token |

### Downloads (public, no auth)

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/d/:token` | Download page / serve file |
| `GET` | `/d/:token/status` | Poll preparation status (JSON) |

### Detection

| Method | Endpoint | Description |
|---|---|---|
| `POST` | `/api/v1/detect` | Upload a suspected leaked file |
| `GET` | `/api/v1/detect/:job_id` | Poll detection result |

### Example: Create & Publish Campaign

```http
POST /api/v1/campaigns
Authorization: Bearer do_a1b2c3d4...
Content-Type: application/json

{
  "asset_id": "f3a2c1d0-...",
  "name": "Sundance 2026 Screener",
  "expires_at": "2026-03-01T23:59:59Z",
  "visible_wm": true,
  "invisible_wm": true,
  "recipients": [
    { "name": "Alice Berger", "email": "alice@festival.org" },
    { "name": "Bob Chen", "email": "bob@press.com", "org": "Variety" }
  ]
}

→ 201 Created
{
  "campaign_id": "c1b2a3...",
  "state": "DRAFT",
  "tokens": [
    {
      "token_id": "a8f3c291-...",
      "recipient": { "name": "Alice Berger", "email": "alice@festival.org" },
      "download_url": "https://example.com/d/a8f3c291-...",
      "state": "PENDING"
    },
    ...
  ]
}
```

```http
POST /api/v1/campaigns/c1b2a3.../publish
Authorization: Bearer do_a1b2c3d4...

→ 202 Accepted
{
  "campaign_id": "c1b2a3...",
  "state": "PROCESSING",
  "jobs_total": 2,
  "jobs_completed": 0
}
```

The client can poll `GET /api/v1/campaigns/c1b2a3...` to track progress. When all jobs complete, state becomes `READY` and tokens become `ACTIVE`.

---

## 11. Frontend

### 11.1 Approach

The frontend is server-rendered HTML templates (Go `html/template`) with minimal JavaScript for interactivity (progress polling, form validation). No React, no build step, no Node.js dependency. The templates and static assets are **embedded in the Go binary** via `embed.FS`.

If a richer UI is desired in the future, a separate SPA can be built against the REST API and served as static files.

### 11.2 Pages

| Route | Description |
|---|---|
| `/` | Login (or dashboard redirect if authenticated) |
| `/dashboard` | Asset list + campaign overview |
| `/assets/upload` | Upload form |
| `/campaigns/new` | Campaign creation: pick asset, add recipients |
| `/campaigns/:id` | Campaign detail: per-recipient status, progress |
| `/d/:token` | Public download page (no auth required) |
| `/detect` | Leak detection file upload |

### 11.3 Download Page UX (`/d/:token`)

The download page is shown to the recipient. It should:

1. Display the file title and an optional poster frame thumbnail.
2. Show the recipient's name (pre-filled, non-editable) and a brief notice that the file is uniquely fingerprinted.
3. A "Download" button that triggers the browser download (`Content-Disposition: attachment`).
4. If the watermarked file is still being prepared (campaign just published), show a progress bar with auto-refresh.
5. After download limit reached (if configured): show "This link has been used."

**No login required for recipients.** The token is the sole credential.

### 11.4 Campaign Dashboard

- Table of recipients with status: `Preparing...` / `Ready (not yet downloaded)` / `Downloaded (2026-02-15 14:32 UTC)` / `Expired` / `Revoked`.
- Per-recipient download event log (IP, User-Agent, timestamp) on row expand.
- "Revoke" button per token.
- Campaign-level progress bar during watermark pre-computation.

---

## 12. Security Considerations

### 12.1 Token Security

- Tokens are UUID v4 (122 bits of entropy). Brute-force is computationally infeasible.
- All token lookups use constant-time string comparison to prevent timing attacks.
- Tokens are transmitted only over HTTPS (enforced by the reverse proxy).

### 12.2 File Access Control

- The `data/` directory is served exclusively through the application — there is no directory listing or direct static file serving for originals or watermarked files. Only the `/d/:token` route serves watermarked files after token validation.
- Original files are never directly accessible via HTTP.

### 12.3 Rate Limiting

- In-memory per-IP sliding window on download routes (10 req/min default).
- SQLite transaction serialization prevents concurrent race conditions on the same token.

### 12.4 Watermarked File Retention

- Watermarked files are retained on disk for the lifetime of the campaign.
- When a campaign expires or is deleted, all watermarked files for that campaign are removed from disk.
- The `download_events` and `watermark_index` records are retained permanently for forensic purposes, even after files are deleted.

### 12.5 API Authentication

- API keys stored as bcrypt hashes in the `accounts` table.
- Key format: `do_<32 random hex bytes>` — prefixed for easy identification.

### 12.6 Input Validation

- Uploaded files validated against allowed MIME types and magic bytes (not just extension).
- Configurable max file size (default: 50 GB).
- FFmpeg is invoked via `exec.Command` with an explicit argument list — no shell interpolation, no user strings in shell context.

---

## 13. Legal & Forensic Considerations

### 13.1 Evidence Admissibility

For a forensic watermark to be useful as evidence:

1. **The database must be preserved** — `watermark_index` and `download_events` are the authoritative link between a watermark payload and a recipient identity. Regular database backups are essential.

2. **Chain of custody** — the system can produce, for any detected watermark:
   - The identity of the recipient (name, email).
   - All download events for that token (IP, User-Agent, timestamp).
   - The SHA-256 of the watermarked file that was served.
   - The watermark payload embedded.

3. **Expert attestation** — the platform operator should be prepared to explain the watermarking system's design and testing to support legal proceedings.

### 13.2 Recipient Notice

The download page presents a brief notice before the recipient clicks "Download":

> "This file has been prepared specifically for [Recipient Name]. It contains a digital fingerprint that uniquely identifies your copy. Unauthorized redistribution may allow the source to be traced."

The timestamp of the page view is logged alongside the download event.

### 13.3 Jurisdiction Notes

- **US (DMCA Section 1202):** Removing or circumventing digital watermarks (copyright management information) carries additional statutory damages of $2,500–$25,000 per violation.
- **EU (Copyright Directive 2001/29/EC, Art. 7):** Equivalent protection for rights management information.
- Consult with an IP attorney before using watermark evidence in legal proceedings.

### 13.4 Privacy Considerations

- Recipient email addresses and IP addresses are personal data under GDPR/CCPA. A privacy policy should disclose their collection and purpose.
- Recommended retention: 7 years for download logs, aligned with copyright claim limitation periods.

---

## 14. Implementation Phases

### Phase 1: Core MVP

**Scope:**
- Single-owner account, login/auth.
- Asset upload (video + image) via web UI.
- Campaign creation with recipient list.
- Token generation.
- **Visible** burn-in watermark for video (FFmpeg drawtext, H.265 output) and image (ImageMagick overlay).
- Pre-computation pipeline (background goroutine workers).
- Download page serving pre-computed files.
- Basic audit log and campaign dashboard.
- `docker-compose.yml` with single container + Caddy.

**Success criteria:** A filmmaker uploads a film, creates a screener campaign for 20 recipients, publishes it, waits for encoding to complete, and all 20 recipients can download their unique copy. The dashboard shows who has downloaded.

### Phase 2: Invisible Watermarking + Detection

**Scope:**
- Integrate `invisible-watermark` Python sidecar for image forensic embedding.
- Invisible watermark on video key frames (post-encode injection or inline FFmpeg filter).
- Detection endpoint: upload a suspected leaked file, get back the matching recipient.
- Watermark index table and detection query logic.

**Success criteria:** Given a JPEG re-saved at quality 85, the detect endpoint correctly identifies the recipient.

### Phase 3: Polish

**Scope:**
- Email delivery of download links (via configurable SMTP or API provider).
- Webhook support for download events.
- Campaign expiry and automatic cleanup of watermarked files.
- API key management.
- Batch image upload.
- Improved progress reporting during pre-computation.

---

## 15. Tech Stack Recommendation

| Layer | Choice | Rationale |
|---|---|---|
| Language | **Go 1.22+** | Single binary, low memory, excellent HTTP stdlib, goroutines for background work |
| HTTP router | **chi** or **echo** | Lightweight, idiomatic Go routing |
| Database | **SQLite 3 (via `modernc.org/sqlite` or `mattn/go-sqlite3`)** | Zero external dependencies, WAL mode for concurrency, sufficient for single-server scale |
| File storage | **Local filesystem** (`./data/` directory) | Simplest possible; no S3 SDK, no credentials, standard backup tools |
| Video processing | **FFmpeg 7.x** (subprocess) | H.265 encoding, drawtext overlay, thumbnail extraction, ffprobe analysis |
| Image watermark | **`invisible-watermark` (Python 3.11)** | Proven DWT-DCT implementation; called as subprocess from Go |
| Image visible overlay | **ImageMagick** (subprocess) or Go `image` stdlib | Compositing text/logo overlays |
| Frontend | **Go `html/template`** + embedded static files | No build step, no Node.js, no SPA framework |
| Auth | **bcrypt** password hashing + session cookies | Simple, no external auth provider needed |
| Deployment | **Docker Compose** | Single `docker-compose.yml` with one app container + optional Caddy |
| Reverse proxy | **Caddy** (optional, for HTTPS) | Automatic Let's Encrypt, zero-config TLS |

### Dockerfile outline

```dockerfile
FROM golang:1.22-bookworm AS builder
WORKDIR /src
COPY . .
RUN go build -o /downloadonce ./cmd/server

FROM debian:trixie-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    ffmpeg \
    python3 python3-pip python3-venv \
    imagemagick \
    tesseract-ocr \
  && python3 -m venv /opt/venv \
  && /opt/venv/bin/pip install invisible-watermark opencv-python-headless \
  && apt-get clean && rm -rf /var/lib/apt/lists/*

COPY --from=builder /downloadonce /usr/local/bin/downloadonce
COPY templates/ /app/templates/
COPY static/ /app/static/

ENV DATA_DIR=/data
VOLUME /data
EXPOSE 8080

ENTRYPOINT ["downloadonce"]
```

### docker-compose.yml

```yaml
services:
  app:
    build: .
    ports:
      - "8080:8080"
    volumes:
      - ./data:/data
    environment:
      - DATA_DIR=/data
      - LISTEN_ADDR=:8080
      - ADMIN_EMAIL=you@example.com
      # - SMTP_HOST=smtp.example.com  # optional, for email delivery
      # - SMTP_PORT=587
    restart: unless-stopped

  # Optional: HTTPS reverse proxy
  caddy:
    image: caddy:2
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile
      - caddy_data:/data
    restart: unless-stopped

volumes:
  caddy_data:
```

### Why not S3/MinIO?

Local filesystem is the simplest option for a single-server deployment. Adding MinIO introduces another container, another API, another thing to monitor. If S3-compatible storage is desired later, options include:
- **rclone mount**: mount any S3-compatible bucket as a local FUSE directory — transparent to the application.
- **Direct S3 integration**: add an S3 storage backend behind a Go interface. The filesystem backend remains the default.

---

## 16. Open Questions

1. **Watermark robustness vs. quality tradeoff:** The `dwtDct` algorithm degrades image quality slightly. Should there be a per-campaign setting for watermark strength (robustness vs. quality)?

2. **Video invisible watermark in Phase 1 or 2?** The visible overlay alone is legally actionable and simpler. Invisible video watermarking adds significant complexity (frame extraction, re-injection). Recommend deferring to Phase 2.

3. **File format support scope:** Should ProRes (`.mov`), DNxHD, and other production-grade codecs be transcoded to H.265, or should lossless pass-through with overlay be supported? H.265 transcode is simpler and reduces file sizes but introduces a generation loss.

4. **Disk space management for pre-computed files:** A campaign with 100 recipients and a 3 GB H.265 output = 300 GB of watermarked copies. Should there be campaign-level disk quotas? Warnings when available space is low?

5. **Recipient experience:** Should recipients optionally verify their email (OTP) before downloading? This increases accountability but adds friction.

6. **SQLite scaling ceiling:** At what point (number of concurrent campaigns, recipients, download events) should a migration to PostgreSQL be recommended? The answer is likely "thousands of concurrent users" which is well beyond the v1 target.

7. **Go-native image watermarking:** The Python `invisible-watermark` dependency adds Python + OpenCV to the Docker image (~500 MB). Is there a Go-native DWT-DCT watermarking library, or should one be written? This could eliminate the Python dependency entirely.

---

*End of specification.*
