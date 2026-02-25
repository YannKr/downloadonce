# Spec 03: Disk Space Monitoring & Quotas

**Status:** Draft
**Date:** 2026-02-23
**Author:** Engineering

---

## 1. Problem Statement

DownloadOnce pre-computes one watermarked copy of each asset per recipient. The storage multiplication factor is linear in campaign size:

```
disk_needed = num_recipients × estimated_output_size_per_file
```

A campaign with 50 recipients distributing a 3 GB H.265 video consumes ~150 GB in `data/watermarked/{campaign_id}/`. On a 2 TB disk, 13 such campaigns fill the disk. There are currently zero warnings, zero quotas, and zero monitoring.

### Concrete Failure Modes

1. **FFmpeg mid-encode write failure.** When the filesystem fills during a watermark job, FFmpeg receives an I/O error. The worker marks the job FAILED in the DB, but the error is only visible in server logs — not in the UI. The campaign stays in PROCESSING indefinitely with a partially written output file on disk.

2. **SQLite WAL write failure.** SQLite WAL mode writes to `downloadonce.db-wal` on every transaction. Because the WAL file lives in the same `DATA_DIR` as watermarked assets, a full disk from watermarking also breaks the database. `SQLITE_FULL` errors from `database/sql` can corrupt the WAL if the process is killed mid-flush.

3. **Asset upload blocked silently.** Large uploads stream to `data/originals/{asset_id}/source.*`. A full disk causes the write to fail partway, leaving a partial file referenced by a valid DB record.

4. **No operator visibility.** An admin has no way to see disk usage, identify the largest campaigns, or judge how close the system is to capacity. The first sign of a problem is typically a cluster of FAILED watermark jobs.

---

## 2. Goals

- Expose real-time disk usage metrics in the admin dashboard and via a JSON API endpoint.
- Warn operators proactively at configurable thresholds (20%, 10%, 5% free space).
- Block new campaign publishes when free space drops below 5% (or a configurable hard cap).
- Show a pre-publish storage estimate so operators can make informed decisions.
- Integrate periodic disk-usage logging into the existing `internal/cleanup` scheduler.
- Keep implementation simple: no new external dependencies, no schema changes for runtime stats.

## 3. Non-Goals

- Per-user storage quotas (multi-tenant billing). Out of scope.
- Automatic deletion of campaigns to recover space. Operators act manually.
- Distributed or object storage backends (S3, GCS). Out of scope.
- Real-time inotify/FSEvents watching. Polling with a 60-second cache is sufficient.

---

## 4. Disk Usage Metrics

### 4.1 Filesystem Free Space

Use `syscall.Statfs` to get available and total bytes on the `DATA_DIR` partition.

```go
// internal/diskstat/diskstat.go

package diskstat

import "syscall"

type FSStats struct {
    TotalBytes     uint64
    FreeBytes      uint64
    AvailableBytes uint64 // available to unprivileged processes (f_bavail)
}

func StatFS(path string) (FSStats, error) {
    var stat syscall.Statfs_t
    if err := syscall.Statfs(path, &stat); err != nil {
        return FSStats{}, err
    }
    return FSStats{
        TotalBytes:     stat.Blocks * uint64(stat.Bsize),
        FreeBytes:      stat.Bfree * uint64(stat.Bsize),
        AvailableBytes: stat.Bavail * uint64(stat.Bsize),
    }, nil
}
```

`syscall.Statfs` avoids adding a new direct `require` line to `go.mod` and works on both Linux and macOS (the two deployment targets).

### 4.2 Directory Size Breakdown

Walk `DATA_DIR` sub-directories to compute per-category sizes. Because this is I/O-intensive on large datasets, it **must be run in a background goroutine and cached** (see Section 10).

```go
type DirSizes struct {
    Originals   int64 // data/originals/ (excluding thumb.jpg)
    Watermarked int64 // data/watermarked/
    Thumbnails  int64 // data/originals/**/thumb.jpg
    Database    int64 // data/downloadonce.db + wal + shm
    Total       int64 // sum of all files in DATA_DIR
}

func WalkDirSizes(dataDir string) (DirSizes, error) {
    var sizes DirSizes
    err := filepath.Walk(dataDir, func(path string, info os.FileInfo, err error) error {
        if err != nil || info.IsDir() {
            return nil
        }
        sizes.Total += info.Size()
        rel, _ := filepath.Rel(dataDir, path)
        switch {
        case strings.HasPrefix(rel, "watermarked/"):
            sizes.Watermarked += info.Size()
        case strings.HasSuffix(rel, "/thumb.jpg"):
            sizes.Thumbnails += info.Size()
        case strings.HasPrefix(rel, "originals/"):
            sizes.Originals += info.Size()
        case strings.HasPrefix(rel, "downloadonce.db"):
            sizes.Database += info.Size()
        }
        return nil
    })
    return sizes, err
}
```

### 4.3 Per-Campaign Watermarked Storage

Walk `data/watermarked/` one level deep to get per-campaign disk usage for the "top 5 campaigns" widget.

```go
type CampaignDiskUsage struct {
    CampaignID string
    Bytes      int64
}

func WatermarkedPerCampaign(dataDir string) ([]CampaignDiskUsage, error) {
    wmDir := filepath.Join(dataDir, "watermarked")
    entries, err := os.ReadDir(wmDir)
    if err != nil {
        if os.IsNotExist(err) {
            return nil, nil
        }
        return nil, err
    }
    var results []CampaignDiskUsage
    for _, e := range entries {
        if !e.IsDir() {
            continue
        }
        size, _ := dirSize(filepath.Join(wmDir, e.Name()))
        results = append(results, CampaignDiskUsage{CampaignID: e.Name(), Bytes: size})
    }
    sort.Slice(results, func(i, j int) bool { return results[i].Bytes > results[j].Bytes })
    return results, nil
}
```

---

## 5. Pre-Publish Storage Estimate

### 5.1 Estimation Formula

```
estimated_output_bytes = original_file_size × compression_factor × num_recipients
```

- **Video (H.265 re-encode):** `compression_factor = 0.9` (default, configurable via `WM_COMPRESSION_FACTOR` env var). Conservative estimate.
- **Image (JPEG re-encode at quality 92):** `compression_factor = 1.0` (output size approximately unchanged from input).

### 5.2 Go Implementation

```go
// internal/diskstat/estimate.go

package diskstat

type PublishEstimate struct {
    OriginalBytes     int64
    NumRecipients     int
    CompressionFactor float64
    EstimatedBytes    int64
    AvailableBytes    uint64
    TotalBytes        uint64
    WouldUsePercent   float64
    WillWarnYellow    bool   // resulting free < 20%
    WillWarnRed       bool   // resulting free < 10%
    WillBlock         bool   // resulting free < 5%
}

func EstimatePublish(
    assetBytes int64,
    assetType string,
    numRecipients int,
    compressionFactor float64,
    fs FSStats,
) PublishEstimate {
    factor := compressionFactor
    if assetType == "image" {
        factor = 1.0
    }
    estimated := int64(float64(assetBytes) * factor * float64(numRecipients))
    freeBytesAfter := int64(fs.AvailableBytes) - estimated
    freePercentAfter := float64(0)
    if fs.TotalBytes > 0 {
        freePercentAfter = float64(freeBytesAfter) / float64(fs.TotalBytes) * 100.0
        if freePercentAfter < 0 {
            freePercentAfter = 0
        }
    }
    wouldUsePct := float64(0)
    if fs.TotalBytes > 0 {
        wouldUsePct = float64(estimated) / float64(fs.TotalBytes) * 100.0
    }
    return PublishEstimate{
        OriginalBytes:     assetBytes,
        NumRecipients:     numRecipients,
        CompressionFactor: factor,
        EstimatedBytes:    estimated,
        AvailableBytes:    fs.AvailableBytes,
        TotalBytes:        fs.TotalBytes,
        WouldUsePercent:   wouldUsePct,
        WillWarnYellow:    freePercentAfter < 20.0,
        WillWarnRed:       freePercentAfter < 10.0,
        WillBlock:         freePercentAfter < 5.0,
    }
}
```

### 5.3 UI: Publish Confirmation Panel

Added to `templates/campaign_detail.html` when `campaign.State == "DRAFT"`:

```
┌─────────────────────────────────────────────────────────────────┐
│  Storage Estimate for This Publish                              │
│                                                                 │
│  Asset size:          2.8 GB                                    │
│  Recipients:          50                                        │
│  Estimated output:    ~126 GB  (50 × 2.8 GB × 0.9)             │
│  Available now:       340 GB of 2.0 TB                         │
│  Available after:     214 GB  (~29% free)                      │
│                                                                 │
│  ⚠ After publish, disk will drop below 30%.                    │
└─────────────────────────────────────────────────────────────────┘
[Publish Campaign]
```

When `WillBlock` is true, replace the Publish button with a disabled error state:

```
┌─────────────────────────────────────────────────────────────────┐
│  [ERROR] Insufficient disk space to publish this campaign.      │
│  Estimated output: ~126 GB. Available: 8 GB (< 5% free).       │
│  Delete expired campaigns or expand disk before publishing.     │
└─────────────────────────────────────────────────────────────────┘
[Publish Campaign — DISABLED]
```

---

## 6. Warning Thresholds & Banners

Three threshold levels apply globally across all admin-authenticated pages. `PageData` gains a `DiskWarning *diskstat.DiskWarning` field rendered in `layout.html`.

### 6.1 Threshold Table

| Level  | Condition     | Behaviour                                                   |
|--------|---------------|-------------------------------------------------------------|
| YELLOW | `< 20%` free  | Yellow banner in admin nav; non-blocking                    |
| RED    | `< 10%` free  | Red banner on all admin pages; warn before publish          |
| BLOCK  | `< 5%` free   | Red banner + publish POST returns 503                       |

### 6.2 `DiskWarning` Struct

```go
type DiskWarning struct {
    FreePercent float64
    FreeBytes   uint64
    TotalBytes  uint64
    Level       string // "ok", "yellow", "red", "block"
}

func ComputeWarning(fs FSStats) DiskWarning {
    return ComputeWarningWithThresholds(fs, 20.0, 10.0, 5.0)
}

func ComputeWarningWithThresholds(fs FSStats, yellow, red, block float64) DiskWarning {
    pct := float64(0)
    if fs.TotalBytes > 0 {
        pct = float64(fs.AvailableBytes) / float64(fs.TotalBytes) * 100.0
    }
    level := "ok"
    switch {
    case pct < block:
        level = "block"
    case pct < red:
        level = "red"
    case pct < yellow:
        level = "yellow"
    }
    return DiskWarning{FreePercent: pct, FreeBytes: fs.AvailableBytes, TotalBytes: fs.TotalBytes, Level: level}
}
```

### 6.3 Banner in `templates/layout.html`

Inserted after `</nav>` before `<main>`:

```html
{{if .DiskWarning}}
  {{if eq .DiskWarning.Level "yellow"}}
  <div class="alert alert-warning">
    Disk space low: {{printf "%.1f" .DiskWarning.FreePercent}}% free.
    <a href="/admin/storage">View details</a>
  </div>
  {{else if eq .DiskWarning.Level "red"}}
  <div class="alert alert-error">
    Disk space critical: {{printf "%.1f" .DiskWarning.FreePercent}}% free.
    <a href="/admin/storage">View details</a>
  </div>
  {{else if eq .DiskWarning.Level "block"}}
  <div class="alert alert-error">
    Disk space exhausted: {{printf "%.1f" .DiskWarning.FreePercent}}% free.
    Publishing is BLOCKED. <a href="/admin/storage">View details</a>
  </div>
  {{end}}
{{end}}
```

`DiskWarning` is `nil` for non-admin users and when level is `"ok"`.

---

## 7. Admin Dashboard Storage Widget

### 7.1 Full Storage Page: `GET /admin/storage`

New route in the `r.Route("/admin", ...)` block, guarded by `RequireAdmin`.
Handler: `AdminStorage` in `internal/handler/admin_storage.go`.
Template: `templates/admin_storage.html`.

```
Storage Overview
════════════════════════════════════════════════════════════════

Disk Usage (DATA_DIR filesystem)
  Total:     2.0 TB
  Used:      1.4 TB   [██████████████░░░░░░]  70%
  Free:      600 GB

Breakdown by Category
  Watermarked files   1.2 TB  [████████████░░░░░░░░]  60%
  Original uploads    180 GB  [██░░░░░░░░░░░░░░░░░░]   9%
  Database (WAL)      1.2 MB  [░░░░░░░░░░░░░░░░░░░░]  <1%
  Thumbnails          24 MB   [░░░░░░░░░░░░░░░░░░░░]  <1%

Top 5 Campaigns by Watermarked Storage
  #  Campaign ID          Disk Used
  1  abc12345-...         148 GB
  2  def67890-...          89 GB
  3  ghi11121-...          61 GB
  4  jkl31415-...          37 GB
  5  mno16171-...          24 GB

  Stats last updated: 2026-02-23 14:05:00 UTC  [Refresh]
  [View All Campaigns →]
```

### 7.2 Compact Widget in `templates/dashboard.html`

```html
{{if .IsAdmin}}{{with .Data.DiskStats}}
<div class="stat-card">
  <div class="stat-label">Disk Usage</div>
  <div class="usage-bar">
    <div class="usage-fill" style="width: {{printf "%.0f" .UsedPercent}}%"></div>
  </div>
  <div class="stat-value-sm">
    {{formatBytes .FreeBytes}} free of {{formatBytes .TotalBytes}}
  </div>
  <a href="/admin/storage">View details</a>
</div>
{{end}}{{end}}
```

---

## 8. `MAX_STORAGE_BYTES` Soft Cap

An optional `MAX_STORAGE_BYTES` env var sets an application-level cap independent of the filesystem. If `DirSizes.Total` exceeds this value, new publishes are blocked.

### 8.1 New Config Fields

```go
// internal/config/config.go additions

MaxStorageBytes         int64   // 0 = disabled
WMCompressionFactor     float64 // default 0.9
DiskWarnThresholdYellow float64 // default 20.0 (percent free)
DiskWarnThresholdRed    float64 // default 10.0
DiskWarnThresholdBlock  float64 // default 5.0
```

New `Load()` entries:

```go
MaxStorageBytes:         envInt64Or("MAX_STORAGE_BYTES", 0),
WMCompressionFactor:     envFloat64Or("WM_COMPRESSION_FACTOR", 0.9),
DiskWarnThresholdYellow: envFloat64Or("DISK_WARN_YELLOW_PCT", 20.0),
DiskWarnThresholdRed:    envFloat64Or("DISK_WARN_RED_PCT", 10.0),
DiskWarnThresholdBlock:  envFloat64Or("DISK_WARN_BLOCK_PCT", 5.0),
```

### 8.2 Publish Gate in `CampaignPublish`

```go
if cached := h.DiskCache.Get(); cached != nil {
    warning := diskstat.ComputeWarningWithThresholds(cached.FS,
        h.Cfg.DiskWarnThresholdYellow,
        h.Cfg.DiskWarnThresholdRed,
        h.Cfg.DiskWarnThresholdBlock,
    )
    if warning.Level == "block" {
        http.Error(w,
            "Publish blocked: disk space below threshold.",
            http.StatusServiceUnavailable)
        return
    }
    if h.Cfg.MaxStorageBytes > 0 && cached.Dirs.Total >= h.Cfg.MaxStorageBytes {
        http.Error(w,
            fmt.Sprintf("Publish blocked: storage cap of %s reached.",
                formatBytesStr(h.Cfg.MaxStorageBytes)),
            http.StatusServiceUnavailable)
        return
    }
}
```

---

## 9. Cleanup Scheduler Integration

```go
// internal/cleanup/cleanup.go additions

func (c *Cleaner) runOnce() {
    // ... existing campaign expiry + file removal logic ...

    fs, err := diskstat.StatFS(c.DataDir)
    if err != nil {
        slog.Warn("cleanup: statfs failed", "error", err)
        return
    }
    dirs, _ := diskstat.WalkDirSizes(c.DataDir)

    freePct := float64(0)
    if fs.TotalBytes > 0 {
        freePct = float64(fs.AvailableBytes) / float64(fs.TotalBytes) * 100.0
    }

    slog.Info("disk usage report",
        "free_pct", fmt.Sprintf("%.1f%%", freePct),
        "free_bytes", fs.AvailableBytes,
        "total_bytes", fs.TotalBytes,
        "watermarked_bytes", dirs.Watermarked,
        "originals_bytes", dirs.Originals,
        "database_bytes", dirs.Database,
        "thumbnails_bytes", dirs.Thumbnails,
    )
    if freePct < 5.0 {
        slog.Error("CRITICAL: disk space below 5% — campaign publishing is blocked",
            "free_pct", freePct)
    }

    if c.DiskCache != nil {
        c.DiskCache.ForceRefresh()
    }
}
```

---

## 10. Caching Architecture

`filepath.Walk` can take several seconds on spinning disk. It must never run synchronously on an HTTP request path.

```go
// internal/diskstat/cache.go

package diskstat

import (
    "context"
    "sync"
    "time"
)

type CachedStats struct {
    FS          FSStats
    Dirs        DirSizes
    PerCampaign []CampaignDiskUsage
    ComputedAt  time.Time
}

type Cache struct {
    mu      sync.RWMutex
    current *CachedStats
    ttl     time.Duration
    dataDir string
    refresh chan struct{}
}

func NewCache(dataDir string, ttl time.Duration) *Cache {
    return &Cache{dataDir: dataDir, ttl: ttl, refresh: make(chan struct{}, 1)}
}

// Get returns the most recent cached stats, triggering a refresh if stale.
// Returns nil if no compute has completed yet.
func (c *Cache) Get() *CachedStats {
    c.mu.RLock()
    cur := c.current
    c.mu.RUnlock()
    if cur == nil || time.Since(cur.ComputedAt) > c.ttl {
        c.ForceRefresh()
    }
    return cur
}

// ForceRefresh signals the background goroutine to recompute. Non-blocking.
func (c *Cache) ForceRefresh() {
    select {
    case c.refresh <- struct{}{}:
    default:
    }
}

// Start runs the background refresh goroutine. Call once at startup.
func (c *Cache) Start(ctx context.Context) {
    go func() {
        c.compute()
        for {
            select {
            case <-ctx.Done():
                return
            case <-c.refresh:
                c.compute()
            }
        }
    }()
}

func (c *Cache) compute() {
    fs, err := StatFS(c.dataDir)
    if err != nil {
        return
    }
    dirs, _ := WalkDirSizes(c.dataDir)
    perCampaign, _ := WatermarkedPerCampaign(c.dataDir)
    c.mu.Lock()
    c.current = &CachedStats{FS: fs, Dirs: dirs, PerCampaign: perCampaign, ComputedAt: time.Now()}
    c.mu.Unlock()
}
```

### Wiring in `internal/app/app.go`

```go
diskCache := diskstat.NewCache(cfg.DataDir, 60*time.Second)
diskCache.Start(ctx)

cleaner := &cleanup.Cleaner{
    DB:        database,
    DataDir:   cfg.DataDir,
    Interval:  ...,
    DiskCache: diskCache,
}

h := handler.New(database, cfg, templateFS, mailer, webhookDispatcher, sseHub, diskCache)
```

---

## 11. JSON API Endpoint

`GET /admin/storage.json` — admin session or Bearer token required.

```json
{
  "computed_at": "2026-02-23T14:05:00Z",
  "filesystem": {
    "total_bytes": 2199023255552,
    "free_bytes": 644245094400,
    "available_bytes": 644245094400,
    "free_percent": 29.3
  },
  "data_dir": {
    "total_bytes": 1503238553600,
    "watermarked_bytes": 1288490188800,
    "originals_bytes": 193273528320,
    "thumbnails_bytes": 25165824,
    "database_bytes": 1258291
  },
  "top_campaigns": [
    {"campaign_id": "abc12345-...", "bytes": 159383552000},
    {"campaign_id": "def67890-...", "bytes": 95637700608}
  ],
  "warning_level": "yellow",
  "max_storage_bytes": 0,
  "publish_blocked": false
}
```

External monitoring can key on `warning_level != "ok"` or `publish_blocked == true`.

---

## 12. Schema Changes

**None.** All disk statistics are computed at runtime from the filesystem and cached in memory.

---

## 13. Files to Create / Modify

### New Files

| File | Purpose |
|------|---------|
| `internal/diskstat/diskstat.go` | `StatFS`, `WalkDirSizes`, `WatermarkedPerCampaign`, `DiskWarning`, `ComputeWarning` |
| `internal/diskstat/cache.go` | `Cache`, `CachedStats`, background goroutine |
| `internal/diskstat/estimate.go` | `PublishEstimate`, `EstimatePublish` |
| `internal/handler/admin_storage.go` | `AdminStorage` (HTML), `AdminStorageJSON` (API) |
| `templates/admin_storage.html` | Full disk stats admin page |

### Modified Files

| File | Change |
|------|--------|
| `internal/config/config.go` | Add 5 new config fields; add `envFloat64Or` helper |
| `internal/app/app.go` | Instantiate and start `diskstat.Cache`; pass to handler and cleaner |
| `internal/handler/handler.go` | Add `DiskCache` field; add `DiskWarning` to `PageData`; update `renderAuth` |
| `internal/handler/routes.go` | Add `/admin/storage` and `/admin/storage.json` routes |
| `internal/handler/campaigns.go` | Add disk gate in `CampaignPublish`; populate estimate in `CampaignDetail` |
| `internal/cleanup/cleanup.go` | Add `DiskCache` field; add usage logging + `ForceRefresh` call |
| `templates/layout.html` | Add disk warning banner block |
| `templates/dashboard.html` | Add compact storage widget for admins |
| `templates/campaign_detail.html` | Add pre-publish storage estimate panel |

---

## 14. Implementation Milestones

### M1 — Backend Stats API + Admin Dashboard Widget

**Deliverables:**
1. Create `internal/diskstat/` package (`diskstat.go`, `cache.go`).
2. Wire `diskstat.Cache` into `app.go` and `handler.New`.
3. Add `DiskWarning` to `PageData`; update `renderAuth` to set it for admins.
4. Add warning banner HTML to `layout.html`.
5. Implement `AdminStorage` and `AdminStorageJSON`.
6. Create `templates/admin_storage.html`.
7. Register routes, add compact widget to `dashboard.html`.

**Acceptance:** `GET /admin/storage.json` returns real data; yellow banner appears with `DISK_WARN_YELLOW_PCT=99`.

### M2 — Pre-Publish Estimate + Config Vars

**Deliverables:**
1. Add config fields and `envFloat64Or` helper.
2. Implement `internal/diskstat/estimate.go`.
3. Populate `Estimate` in `CampaignDetail` for DRAFT campaigns.
4. Render estimate panel in `campaign_detail.html`.
5. Add disk-usage logging to cleanup scheduler.

**Acceptance:** DRAFT campaign detail page shows estimate with correct arithmetic; cleanup log emits usage report.

### M3 — Publish Blocking at Threshold

**Deliverables:**
1. Add disk-space gate in `CampaignPublish`.
2. Add `MAX_STORAGE_BYTES` soft-cap gate.
3. Render disabled publish button when blocked.
4. Set `"publish_blocked": true` in `/admin/storage.json` when blocked.

**Acceptance:** `DISK_WARN_BLOCK_PCT=99` disables the Publish button and returns HTTP 503 on POST.

---

## Appendix A: `envFloat64Or` Helper

```go
func envFloat64Or(key string, fallback float64) float64 {
    if v := os.Getenv(key); v != "" {
        if f, err := strconv.ParseFloat(v, 64); err == nil {
            return f
        }
    }
    return fallback
}
```

## Appendix B: Cross-Platform Note

`syscall.Statfs` compiles on Linux and macOS. For a future Windows port, use a build-tag stub:

```go
// internal/diskstat/diskstat_windows.go
//go:build windows

package diskstat

func StatFS(path string) (FSStats, error) {
    // Zero-value TotalBytes causes ComputeWarning to return "block" — conservative safe-fail.
    return FSStats{}, nil
}
```

## Appendix C: Observability Integrations

- **Prometheus:** cron job fetches `/admin/storage.json`, writes `.prom` gauge metrics for node_exporter textfile collector.
- **Uptime monitors:** HTTP keyword check for `"publish_blocked":false`.
- **Log aggregators:** filter on `level=ERROR msg="CRITICAL: disk space below 5%"` from the cleanup scheduler.
