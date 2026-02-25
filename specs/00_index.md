# DownloadOnce — Next Iteration Master Index

**Version:** v0.3 Planning
**Date:** 2026-02-23
**Status:** Draft

This document is the authoritative index for the v0.3 feature roadmap. It ties together eight individual specs, resolves cross-spec conflicts (migration numbering, shared file modifications), establishes implementation phases, and summarises the cumulative schema and architectural impact.

---

## Table of Contents

1. [Spec Inventory](#1-spec-inventory)
2. [Implementation Phases](#2-implementation-phases)
3. [Cross-Spec Dependencies](#3-cross-spec-dependencies)
4. [Resolved Migration Numbering](#4-resolved-migration-numbering)
5. [Cumulative Schema Changes](#5-cumulative-schema-changes)
6. [New Packages & Files](#6-new-packages--files)
7. [Shared File Modification Map](#7-shared-file-modification-map)
8. [Effort Estimates](#8-effort-estimates)
9. [What Is Explicitly Out of Scope](#9-what-is-explicitly-out-of-scope)
10. [Open Cross-Spec Questions](#10-open-cross-spec-questions)

---

## 1. Spec Inventory

| # | File | Title | Category | Priority |
|---|------|--------|----------|----------|
| 01 | [`01_resumable_uploads.md`](01_resumable_uploads.md) | Resumable / Chunked File Upload | Reliability | P1 |
| 02 | [`02_recipient_groups.md`](02_recipient_groups.md) | Recipient Groups / Contact Lists | UX | P1 |
| 03 | [`03_disk_space_monitoring.md`](03_disk_space_monitoring.md) | Disk Space Monitoring & Quotas | Operations | P1 |
| 04 | [`04_job_retry.md`](04_job_retry.md) | Job Retry on Failure with Notifications | Reliability | P1 |
| 05 | [`05_rest_api_v1.md`](05_rest_api_v1.md) | REST API v1 | Integration | P2 |
| 06 | [`06_webhook_delivery.md`](06_webhook_delivery.md) | Webhook Delivery Log + Retry Queue | Integration | P2 |
| 07 | [`07_go_native_watermarking.md`](07_go_native_watermarking.md) | Go-Native Invisible Watermarking | Tech Debt | P3 |
| 08 | [`08_quick_wins.md`](08_quick_wins.md) | Campaign Cloning + Export All Links | UX | P0 |

### One-line summaries

**Spec 01 — Resumable Uploads:** Replace the single-POST multipart upload with a five-endpoint chunked API (`init` → `PUT chunks` → `complete`) and a vanilla-JS progress bar. Enables reliable upload of 10–50 GB 4K video files over flaky connections with mid-session resume. Zero new Go dependencies.

**Spec 02 — Recipient Groups:** Named contact lists (`recipient_groups`, `recipient_group_members` tables). Full CRUD UI at `/recipients/groups`. Campaign creation form gains a multi-select group picker that expands and deduplicates recipients server-side. Eliminates the most common manual repetition in the product.

**Spec 03 — Disk Space Monitoring:** New `internal/diskstat` package wraps `syscall.Statfs` and a background `filepath.Walk` cache. Pre-publish storage estimate on campaign detail. Warning banners at 20%/10% free, publish blocking at 5%. `GET /admin/storage.json` for external monitoring. No schema changes.

**Spec 04 — Job Retry:** Adds `retry_count`, `max_retries`, `next_retry_at` to the `jobs` table. Workers retry transient FFmpeg/Python failures with exponential backoff (1 min → 5 min → 15 min). Introduces `PARTIAL` and `FAILED` campaign states. Stuck-job detection resets jobs that have been `RUNNING` for >30 min. Manual per-token retry button in the UI.

**Spec 05 — REST API v1:** 15 JSON endpoints under `/api/v1/` covering assets, recipients, campaigns, and detection. Reuses existing Bearer API key auth. Consistent pagination, error envelopes, and rate-limit headers. Embedded `static/openapi.yaml`. Four new handler files; no changes to existing HTML routes.

**Spec 06 — Webhook Delivery Log:** New `webhook_deliveries` table persists every attempt. Failed deliveries retried up to 5 times with exponential backoff (30s → 5m → 30m → 2h). Settings page shows last-delivery status, health warning banner, and a delivery history page with manual replay. Standardises the HMAC-SHA256 `X-DownloadOnce-Signature` header format.

**Spec 07 — Go-Native Watermarking:** Port the Python `imwatermark` DWT-DCT-SVD algorithm to pure Go. Eliminates the ~410 MB Python/OpenCV Docker layer (image drops from ~1.2 GB to ~200 MB) and 200–600 ms per-image subprocess overhead. Full R&D spec including algorithm pseudocode, robustness test plan, and cross-compatibility migration strategy for files already watermarked by Python.

**Spec 08 — Quick Wins:** Two independent features: (A) `POST /campaigns/{id}/clone` creates a new DRAFT campaign with copied settings and the same recipient list — no schema changes; (B) `GET /campaigns/{id}/export-links?format=csv|txt` returns all download URLs for a campaign in bulk, plus a clipboard copy button — no schema changes.

---

## 2. Implementation Phases

Work is organised into four phases. Within a phase, features can be developed in parallel by different engineers. Phase boundaries represent integration and stabilisation points.

### Phase 0 — Quick Wins (1 week, no dependencies)

These require no schema changes, no new packages, and touch only two handler files. Ship immediately.

| Spec | Feature | Key deliverable |
|------|---------|-----------------|
| **08A** | Campaign cloning | `POST /campaigns/{id}/clone` + confirmation modal |
| **08B** | Export all links | `GET /campaigns/{id}/export-links` + clipboard JS |

**Acceptance gate:** Both features work end-to-end; existing campaign tests pass; no regressions.

---

### Phase 1 — Reliability Foundation (3–4 weeks)

These three specs address the most dangerous production failure modes. They share no blocking dependencies on each other and can be built in parallel by separate engineers.

| Spec | Feature | Why now |
|------|---------|---------|
| **04** | Job retry + stuck-job detection | A FAILED watermark job currently has no recovery path. This is the most important reliability fix. Must land before Phase 2 so disk-full errors (Spec 03) correctly trigger retry rather than permanent failure. |
| **03** | Disk space monitoring | Disk exhaustion causes silent cascading failures (FFmpeg errors, SQLite WAL corruption). The pre-publish estimate prevents the most common surprise. |
| **02** | Recipient groups | The most common manual workflow pain. Unblocks Phase 2 REST API (groups should be queryable via API). |

**Sequencing note within Phase 1:** Spec 04 (job retry) should reach M1 (schema + auto-retry logic) before Spec 03 (disk monitoring) reaches M3 (publish blocking). The reason: once publish blocking is live, the only recovery for a blocked campaign after freeing disk space is re-publishing — which currently creates duplicate jobs for already-completed tokens. Job retry's `isPermanentFailure` classification prevents this from being catastrophic, but the per-token retry button (Spec 04 M3) provides the cleanest operator experience.

**Acceptance gate:** A simulated OOM FFmpeg kill retries automatically; a simulated disk-full produces a visible warning banner and pre-publish estimate; recipient groups round-trip through create/add-member/campaign-select/publish.

---

### Phase 2 — Integration Layer (4–6 weeks)

These specs depend on Phase 1 being stable (especially: recipient groups should exist before the REST API exposes them).

| Spec | Feature | Notes |
|------|---------|-------|
| **01** | Resumable uploads | Largest engineering effort. Backend M1 can ship independently of the JS frontend M2. Spec 03 (disk monitoring) should be live first so the upload path inherits disk-space protection. |
| **05** | REST API v1 | Depends on Spec 02 (recipient groups) being deployed so the API can expose them. Should expose `POST /api/v1/campaigns/{id}/clone` (Spec 08A) as part of the campaigns surface. |
| **06** | Webhook delivery log | Independent of other Phase 2 specs. M1 (logging without retry) can ship immediately; M2 (retry goroutine) and M3 (UI) follow. |

**Acceptance gate:** A CI/CD script can upload an asset, create and publish a campaign, and poll for completion — all via API keys. Webhook test endpoint confirms delivery with visible history in settings.

---

### Phase 3 — Technical Debt / R&D (6–10 weeks, parallel track)

This phase runs as a parallel R&D track and does not block Phases 1 or 2. It has the largest risk surface and requires the most testing.

| Spec | Feature | Notes |
|------|---------|-------|
| **07** | Go-native invisible watermarking | Four milestones: M1 (DWT/DCT math primitives with unit tests), M2 (embed + detect), M3 (robustness + cross-compatibility testing against Python output), M4 (worker integration + Docker update). **Do not remove Python from Docker until M3 test gate passes.** |

**Acceptance gate:** T8 and T9 from Spec 07 (cross-compatibility: Go embed → Python detect; Python embed → Go detect) both pass. Docker image size verified < 250 MB. At least 30 existing watermarked images are detectable by the Go detect path.

---

## 3. Cross-Spec Dependencies

```
Spec 08 (Quick Wins)
  └── no dependencies; ship first

Spec 04 (Job Retry)
  └── no blocking dependencies
  └── synergises with: Spec 03 (disk-full errors become transient retries)

Spec 03 (Disk Monitoring)
  └── no blocking dependencies
  └── recommendation: deploy after Spec 04 M1 is live

Spec 02 (Recipient Groups)
  └── no blocking dependencies
  └── blocking: Spec 05 (REST API should expose groups)

Spec 01 (Resumable Upload)
  └── recommendation: Spec 03 M1 live first (disk stats protect uploads)
  └── no hard dependency

Spec 05 (REST API v1)
  └── soft dependency: Spec 02 M1 (recipient groups should exist to be queryable)
  └── includes: Spec 08A clone endpoint in the campaigns surface

Spec 06 (Webhook Delivery)
  └── no hard dependencies; can ship any time

Spec 07 (Go Watermarking)
  └── no dependencies; parallel R&D track
  └── requires: migration (Spec 07 adds wm_algorithm column) to land before
      Python is removed from Dockerfile
```

**No circular dependencies exist.**

---

## 4. Resolved Migration Numbering

Multiple specs independently proposed `006_*.sql`. The table below assigns non-conflicting sequential numbers for a deployment ordering that respects dependencies. Specs 03 and 08 require no migrations.

| Migration file | Spec | Contents | Modifies existing tables? |
|---|---|---|---|
| `006_recipient_groups.sql` | 02 | `recipient_groups`, `recipient_group_members` | No — additive only |
| `007_upload_sessions.sql` | 01 | `upload_sessions` | No — additive only |
| `008_job_retry.sql` | 04 | `jobs` (3 new columns), `campaigns` (recreated with expanded CHECK) | Yes — `campaigns` table recreated |
| `009_webhook_delivery.sql` | 06 | `webhook_deliveries` | No — additive only |
| `010_wm_algorithm.sql` | 07 | `watermark_index` and `download_tokens` (1 new column each) | Yes — additive columns |

**Important notes on migration 008:**

`008_job_retry.sql` recreates the `campaigns` table to expand the `state` CHECK constraint to include `PARTIAL` and `FAILED`. SQLite does not support `ALTER TABLE … ALTER COLUMN`. The migration uses the standard SQLite pattern: `CREATE TABLE campaigns_new … ; INSERT INTO campaigns_new SELECT * FROM campaigns; DROP TABLE campaigns; ALTER TABLE campaigns_new RENAME TO campaigns`. This runs inside an implicit migration transaction in `db.Migrate()` — verify that `db.Migrate()` uses `BEGIN EXCLUSIVE` or equivalent to make this safe on a live database before deploying.

---

## 5. Cumulative Schema Changes

Across all eight specs, the database gains these new or modified objects:

### New Tables

| Table | Added by | Purpose |
|---|---|---|
| `recipient_groups` | Spec 02 | Named contact lists per account |
| `recipient_group_members` | Spec 02 | Many-to-many group membership |
| `upload_sessions` | Spec 01 | In-progress chunked upload sessions |
| `webhook_deliveries` | Spec 06 | Per-attempt webhook delivery log and retry queue |

### Modified Tables

| Table | Change | Added by |
|---|---|---|
| `jobs` | `+ retry_count INT DEFAULT 0` | Spec 04 |
| `jobs` | `+ max_retries INT DEFAULT 3` | Spec 04 |
| `jobs` | `+ next_retry_at TEXT` | Spec 04 |
| `campaigns` | CHECK constraint expanded: `+ 'PARTIAL' + 'FAILED'` | Spec 04 |
| `watermark_index` | `+ wm_algorithm TEXT DEFAULT 'dwtDctSvd-python'` | Spec 07 |
| `download_tokens` | `+ wm_algorithm TEXT DEFAULT 'dwtDctSvd-python'` | Spec 07 |

### New Indexes

| Index | Table | Added by | Purpose |
|---|---|---|---|
| `idx_recipient_groups_account` | `recipient_groups` | Spec 02 | List groups by account |
| `idx_rgm_recipient` | `recipient_group_members` | Spec 02 | Badge lookup (which groups does recipient belong to) |
| `idx_upload_sessions_account` | `upload_sessions` | Spec 01 | List sessions per account |
| `idx_upload_sessions_expires` | `upload_sessions` | Spec 01 | Cleanup scheduler expiry query |
| `idx_jobs_retry` | `jobs` | Spec 04 | Efficient poll for jobs with elapsed backoff window |
| `idx_webhook_deliveries_webhook` | `webhook_deliveries` | Spec 06 | List deliveries per webhook |
| `idx_webhook_deliveries_pending` | `webhook_deliveries` | Spec 06 | Retry goroutine poll |
| `idx_webhook_deliveries_event` | `webhook_deliveries` | Spec 06 | Deduplicate by event_id |

---

## 6. New Packages & Files

### New Go packages

| Package path | Added by | Purpose |
|---|---|---|
| `internal/diskstat/` | Spec 03 | `StatFS`, `WalkDirSizes`, `Cache`, `PublishEstimate`, `DiskWarning` |
| `internal/watermark/dwt/` | Spec 07 | 2D Haar DWT forward/inverse transform |
| `internal/watermark/dct/` | Spec 07 | 2D DCT on image blocks |

### New Go files (in existing packages)

| File | Added by | Purpose |
|---|---|---|
| `internal/db/queries_groups.go` | Spec 02 | Group/member CRUD queries |
| `internal/db/queries_upload_sessions.go` | Spec 01 | Upload session CRUD |
| `internal/handler/upload.go` | Spec 01 | Chunked upload API handlers |
| `internal/handler/groups.go` | Spec 02 | Recipient group page handlers |
| `internal/handler/admin_storage.go` | Spec 03 | Storage stats page + JSON endpoint |
| `internal/handler/api_assets.go` | Spec 05 | REST API: assets |
| `internal/handler/api_campaigns.go` | Spec 05 | REST API: campaigns |
| `internal/handler/api_recipients.go` | Spec 05 | REST API: recipients |
| `internal/handler/api_detect.go` | Spec 05 | REST API: detection |
| `internal/watermark/image_go.go` | Spec 07 | Go-native DWT-DCT-SVD embed/detect (replaces `invisible.go`) |
| `internal/webhook/retrier.go` | Spec 06 | Retry goroutine polling `webhook_deliveries` |

### New static & template files

| File | Added by | Purpose |
|---|---|---|
| `static/upload.js` | Spec 01 | Vanilla JS chunked uploader (~120 lines) |
| `static/openapi.yaml` | Spec 05 | Hand-maintained OpenAPI 3.0 spec |
| `templates/recipient_groups.html` | Spec 02 | Group list page |
| `templates/recipient_group_detail.html` | Spec 02 | Group detail + member management |
| `templates/admin_storage.html` | Spec 03 | Full disk stats admin page |
| `templates/admin_webhook_deliveries.html` | Spec 06 | Delivery history page |

### New migration files

| File | Added by |
|---|---|
| `migrations/006_recipient_groups.sql` | Spec 02 |
| `migrations/007_upload_sessions.sql` | Spec 01 |
| `migrations/008_job_retry.sql` | Spec 04 |
| `migrations/009_webhook_delivery.sql` | Spec 06 |
| `migrations/010_wm_algorithm.sql` | Spec 07 |

---

## 7. Shared File Modification Map

Files touched by more than one spec — coordinate to avoid merge conflicts.

| File | Modified by | Nature of changes |
|---|---|---|
| `internal/handler/routes.go` | 01, 02, 03, 04, 05, 06, 08 | New route registrations. Assign one engineer per phase to consolidate. |
| `internal/handler/campaigns.go` | 02, 03, 04, 05, 08 | Group expansion (02), disk gate (03), retry endpoint (04), clone + export (08). Spec 05 adds JSON variants of existing handlers. |
| `internal/handler/handler.go` | 03, 05 | `PageData` gains `DiskWarning` (03); `Handler` gains `DiskCache` field (03); `renderJSON` helper added (05). |
| `internal/model/model.go` | 01, 02, 04, 07 | New structs per spec. Additive changes — low conflict risk. |
| `internal/cleanup/cleanup.go` | 01, 03, 04 | Expired upload sessions (01), disk usage logging (03), stuck-job reset (04). All additive steps in `runOnce()`. |
| `internal/app/app.go` | 01, 03 | `data/uploads/` directory creation (01); `diskstat.Cache` instantiation and wiring (03). |
| `internal/config/config.go` | 01, 03 | `UploadSessionTTLHours` (01); `MaxStorageBytes`, `WMCompressionFactor`, `DiskWarn*` thresholds (03). |
| `internal/email/email.go` | 04 | `SendJobFailed`, `SendCampaignPartial`, `SendCampaignFailed`. Additive — low conflict risk. |
| `internal/worker/pool.go` | 04, 07 | Retry logic (04); Go watermark calls replace Python subprocess calls (07). These changes touch the same `processJob` code path — coordinate carefully or sequence 04 before 07. |
| `internal/watermark/invisible.go` | 07 | Either deleted or replaced by `image_go.go`. Spec 07 M4 only — do not modify in earlier milestones. |
| `templates/layout.html` | 01, 03 | CSRF meta tag (01); disk warning banner (03). Simple additions to different areas of the template. |
| `templates/campaign_detail.html` | 03, 04, 08 | Pre-publish estimate panel (03); FAILED banner + retry buttons (04); clone + export buttons (08). |
| `templates/dashboard.html` | 03 | Compact disk widget for admins. |
| `templates/recipients.html` | 02 | Groups column + "Manage Groups" link. |
| `templates/campaign_new.html` | 02 | Group multi-select section. |
| `templates/campaigns.html` | 04 | `PARTIAL` state badge. |
| `templates/settings.html` | 06 | Webhook last-delivery status column; health warning banner. |
| `Dockerfile` | 07 | Remove Python/pip/venv/opencv layers (M4 only). Do not touch until Spec 07 M3 test gate passes. |

---

## 8. Effort Estimates

Estimates are in engineering-days for a single engineer. The wide ranges reflect uncertainty in testing time.

| Spec | Backend | Frontend/Templates | Testing | Total |
|---|---|---|---|---|
| **08 Quick Wins** | 2 | 1 | 0.5 | **~3.5 days** |
| **04 Job Retry** | 4 | 1.5 | 1.5 | **~7 days** |
| **03 Disk Monitoring** | 3 | 1.5 | 1 | **~5.5 days** |
| **02 Recipient Groups** | 4 | 3 | 1.5 | **~8.5 days** |
| **01 Resumable Uploads** | 4 | 3 | 2.5 | **~9.5 days** |
| **06 Webhook Delivery** | 4 | 2 | 2 | **~8 days** |
| **05 REST API v1** | 6 | 0.5 | 3 | **~9.5 days** |
| **07 Go Watermarking** | 14 | 0 | 5 | **~19 days** |
| **Total** | | | | **~70 days** |

With parallel execution across phases (and Spec 07 as a background track), calendar time is roughly:

```
Phase 0 (Quick Wins):   Week 1
Phase 1 (Reliability):  Weeks 2–5  (3 specs in parallel)
Phase 2 (Integration):  Weeks 5–10 (3 specs in parallel, 1 sequential)
Phase 3 (R&D):          Weeks 2–12 (background track, ships when ready)
```

---

## 9. What Is Explicitly Out of Scope

These items were considered and deferred. They should not creep into v0.3 work without a new spec.

| Item | Reason deferred |
|---|---|
| Two-factor authentication (TOTP) | Operational burden vs. benefit low for self-hosted single-owner deployments |
| S3 / MinIO storage backend | rclone FUSE mount covers this without code changes |
| Audio-only watermarking | No user demand; out of original SPEC scope |
| PDF / document watermarking | Separate problem domain; would require a new pipeline |
| Multi-tenancy with per-tenant billing | Target is single-owner self-hosted deployment |
| Horizontal scaling / PostgreSQL migration | SQLite ceiling is well above current scale targets |
| Recipient email OTP verification before download | Adds friction; forensic notice on download page is sufficient deterrent |
| Real-time adaptive streaming (HLS/DASH) | File download platform; out of scope in original SPEC |
| HLS/DASH video packaging | See above |

---

## 10. Open Cross-Spec Questions

These questions span multiple specs and need a decision before implementation begins.

### Q1: Migration 008 safety (Spec 04)

`008_job_retry.sql` drops and recreates the `campaigns` table. Is `db.Migrate()` transactional enough to make this safe if the server crashes mid-migration? **Decision needed:** either verify that `db.Migrate()` wraps each file in `BEGIN EXCLUSIVE … COMMIT`, or rewrite the migration to use the add-column + trigger approach for the CHECK constraint instead of a table recreation.

### Q2: Migration file ordering (all specs)

The numbers `006–010` are assigned in this index. Ensure no existing unreleased branch has already used `006_` for something else before merging any of these specs. Run `ls migrations/` on all active branches before creating migration files.

### Q3: `isPermanentFailure` accuracy (Spec 04)

The error-pattern list for permanent vs. transient failure classification in `pool.go` needs validation against real FFmpeg error output. Before Spec 04 M1 ships, collect 2–3 months of FFmpeg error messages from server logs and verify that the patterns in `isPermanentFailure` correctly classify at least the top 5 most common errors. False negatives (treating a permanent error as transient) waste 3 retry cycles; false positives (treating a transient error as permanent) are the more dangerous failure.

### Q4: Disk monitoring on non-Linux hosts (Spec 03)

`syscall.Statfs` is available on Linux and macOS but not Windows. The Dockerfile deploys to Linux, and local development on macOS works. Confirm that `go build` on Windows is not a requirement before shipping. If it is, a build-tag stub is specified in Spec 03 Appendix B.

### Q5: Go watermarking cross-compatibility gate (Spec 07)

Spec 07 defines two hard pass/fail tests: T8 (Go embed → Python detect) and T9 (Python embed → Go detect). **Who signs off on these results?** This needs a designated reviewer with access to a large set of pre-existing watermarked images from production. Schedule this review before Spec 07 M4 begins.

### Q6: REST API — file upload limit (Spec 05)

Spec 05 documents a 2 GB per-request limit for `POST /api/v1/assets` until chunked upload (Spec 01) is available. Once Spec 01 ships, the API should expose the chunked upload endpoints too. Add `POST /api/v1/upload/chunks/init` etc. to the REST API surface as part of Spec 01 M3 (polish) rather than creating a separate API spec for it. This cross-spec work should be explicitly assigned during Phase 2 planning.

### Q7: Webhook event type for PARTIAL/FAILED campaigns (Specs 04 + 06)

Spec 04 notes that the existing `campaign_ready` webhook should include a `state` field (`"READY"`, `"PARTIAL"`, or `"FAILED"`). Spec 06 standardises the webhook payload format. These two changes must be coordinated to ship in the same deployment — a breaking payload change must not ship ahead of the delivery log (which makes it possible to observe and replay the new format). Sequence: Spec 06 M1 (logging) lands first, then Spec 04 M4 (email notifications) includes the payload update.

---

## Appendix A: Milestone Summary Table

A compressed view of every milestone across all specs, useful for sprint planning.

| Spec | Milestone | Key deliverable | Phase |
|---|---|---|---|
| 08 | A-M1 | `CampaignClone` handler + DB transaction | 0 |
| 08 | B-M1 | CSV + txt export endpoints | 0 |
| 08 | B-M2 | Clipboard JS button | 0 |
| 04 | M1 | Schema + automatic retry logic + `PARTIAL`/`FAILED` campaign states | 1 |
| 04 | M2 | Stuck-job detection in cleanup scheduler | 1 |
| 03 | M1 | `diskstat` package + admin storage widget + JSON endpoint | 1 |
| 03 | M2 | Pre-publish estimate + config vars | 1 |
| 02 | M1 | Recipient groups schema + CRUD + badge rendering | 1 |
| 02 | M2 | Campaign form group integration | 1 |
| 04 | M3 | Manual retry UI + SSE `token_failed` | 1 |
| 03 | M3 | Publish blocking at threshold | 1 |
| 04 | M4 | Failure email notifications | 1 |
| 02 | M3 | Group CSV import | 1 |
| 06 | M1 | Webhook delivery logging (no retry yet) | 2 |
| 01 | M1 | Chunked upload backend API | 2 |
| 05 | M1 | Auth + helpers + assets API endpoints | 2 |
| 06 | M2 | Retry goroutine | 2 |
| 01 | M2 | Frontend JS progress UI | 2 |
| 05 | M2 | Campaigns + recipients API endpoints | 2 |
| 06 | M3 | Delivery history UI | 2 |
| 05 | M3 | Detection API endpoint | 2 |
| 01 | M3 | Multi-file + retry hardening + audit log | 2 |
| 06 | M4 | Replay button + 90-day pruning | 2 |
| 05 | M4 | OpenAPI spec + CI lint + smoke test script | 2 |
| 07 | M1 | Go DWT/DCT math primitives + unit tests | 3 |
| 07 | M2 | Go embed + detect working on images | 3 |
| 07 | M3 | Robustness + cross-compatibility testing | 3 |
| 07 | M4 | Worker integration + Docker image update | 3 |

---

## Appendix B: New Environment Variables

All new env vars introduced across the specs, consolidated for `docker-compose.yml` and documentation updates.

| Variable | Default | Spec | Description |
|---|---|---|---|
| `UPLOAD_SESSION_TTL_HOURS` | `24` | 01 | Hours before incomplete upload sessions are purged |
| `MAX_STORAGE_BYTES` | `0` (disabled) | 03 | Application-level storage cap; blocks publishes if exceeded |
| `WM_COMPRESSION_FACTOR` | `0.9` | 03 | Estimated H.265 output size as fraction of original |
| `DISK_WARN_YELLOW_PCT` | `20.0` | 03 | Percent free disk below which yellow warning banner appears |
| `DISK_WARN_RED_PCT` | `10.0` | 03 | Percent free disk below which red warning banner appears |
| `DISK_WARN_BLOCK_PCT` | `5.0` | 03 | Percent free disk below which new publishes are blocked |

---

*End of master index.*
