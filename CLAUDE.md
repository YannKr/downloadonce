# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Run

```bash
# Build the binary
go build ./cmd/server

# Cross-compile for Linux (e.g. deploying to an LXC container)
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o downloadonce ./cmd/server

# Run locally (requires ffmpeg, imagemagick, python3+invisible-watermark in PATH/venv)
./server

# Build and run via Docker (preferred for full watermarking support)
docker compose up --build

# Run with custom config
BASE_URL=https://example.com SESSION_SECRET=$(openssl rand -hex 32) ./server

# Deploy to a Debian Trixie LXC / VM (see setup.sh)
scp downloadonce setup.sh root@<host>:~
ssh root@<host> 'BASE_URL=https://dl.example.com ./setup.sh'
```

Seven packages carry tests — `config`, `db`, `handler`, `safehttp`, `sse`, `watermark/dct` and `watermark/dwt` — covering secret validation, scoped queries, handler authorization, outbound-URL rejection and the SSE hub. Run them with `go test ./... -count=1`. CI gates on the same suite plus `gofmt -l .` reporting nothing, `go vet ./...` and `govulncheck ./...`, so run those before pushing.

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `TRUSTED_PROXIES` | (none) | Comma-separated CIDRs or IPs allowed to set `X-Forwarded-For` / `X-Real-IP`. Empty means the headers are ignored |
| `LISTEN_ADDR` | `:8080` | HTTP listen address |
| `DATA_DIR` | `./data` | Persistent storage root |
| `BASE_URL` | `http://localhost:8080` | Public base URL (used in download links) |
| `SESSION_SECRET` | (generated) | 32+ byte secret for session/CSRF signing. There is no built-in default: if unset, a random one is generated and stored in `$DATA_DIR/session_secret` (mode 0600). Startup fails if a value is supplied but is shorter than 32 bytes or is a known placeholder — see `config.PlaceholderSecrets`, which is append-only |
| `MAX_UPLOAD_BYTES` | `5368709120` | Maximum single request body (5 GB), applied to asset upload, detect and import. The chunked upload API caps each chunk instead |
| `WORKER_COUNT` | `2` | Concurrent watermark encoding workers |
| `FONT_PATH` | `/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf` | Font for visible watermark overlay |
| `VENV_PATH` | `/opt/venv` | Python venv with `invisible-watermark` + `opencv-python-headless` |
| `ALLOW_REGISTRATION` | `false` | Allow new users to self-register |
| `SMTP_HOST/PORT/USER/PASS/FROM` | (empty) | Optional SMTP for email delivery |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |

## Architecture

### Single-Binary, Single-Process Design

The entire application lives in one Go binary (`cmd/server/main.go → internal/app/app.go`):
- HTTP server (chi router)
- Background worker pool (in-process goroutines polling a SQLite jobs table)
- SSE hub for real-time progress updates
- Cleanup scheduler for expired campaigns/files

No Redis, no external job queue. The `jobs` table in SQLite is the queue.

### Package Layout

- **`internal/config`** — all config loaded from env vars via `config.Load()`
- **`internal/db`** — all SQL queries, one file per domain (`queries_assets.go`, `queries_campaigns.go`, etc.). SQLite opened in WAL mode with a single connection (`MaxOpenConns(1)`).
- **`internal/model`** — plain Go structs for all domain types; no ORM
- **`internal/handler`** — HTTP handlers wired in `routes.go`; `handler.go` owns template parsing and `render()`/`renderAuth()` helpers; `PageData` is the universal template context struct
- **`internal/watermark`** — FFmpeg subprocess (`ffmpeg.go`), ImageMagick subprocess (`imagemagick.go`), the Go-native DWT-DCT-SVD embedder/detector (`image_go.go` over the `dct/` and `dwt/` subpackages), Python subprocess wrappers for the same (`invisible.go`), and the 16-byte payload encoding/CRC logic (`payload.go`)
- **`internal/worker`** — `Pool` polls for `PENDING` jobs, dispatches to `processJob()` (watermarking) or `processDetectJob()` (leak detection), updates progress via SSE, fires webhooks and emails on campaign completion
- **`internal/sse`** — lightweight pub/sub hub; channels are named `campaign:<id>` and `token:<id>`
- **`internal/auth`** — bcrypt password helpers and session context keys
- **`internal/safehttp`** — the HTTP client for any URL a user supplies (webhook targets, asset imports). It refuses loopback, private, link-local and multicast destinations, re-checking on each connection so a name that re-resolves cannot slip past. Use it for outbound requests instead of `http.Get`
- **`internal/diskstat`** — cached filesystem usage sampling behind the storage warning thresholds
- **`internal/cleanup`** — periodic goroutine that expires campaigns and deletes watermarked files from disk
- **`internal/email`** — SMTP mailer (optional)
- **`internal/webhook`** — outgoing HTTP webhook dispatcher

### Embedded Assets

`embed.go` (root package `downloadonce`) embeds four FS trees into the binary:
- `templates/*` → `TemplateFS`
- `static/*` → `StaticFS`
- `migrations/*` → `MigrationFS`
- `scripts/*` → `ScriptFS` (Python watermark scripts extracted to a temp dir at startup)

### Database Migrations

SQL files in `migrations/` are applied in filename order by `db.Migrate()` at startup. Add new migrations as `NNN_description.sql` — they run automatically on next start. Never modify existing migration files.

`Migrate` runs each file in a transaction with foreign keys off, so a table rebuild with `DROP TABLE` does not fire `ON DELETE CASCADE`. It rolls the file back if `PRAGMA foreign_key_check` then reports more violations than before the file ran.

### Authentication & Authorization

Two authentication paths:
1. **Session cookie** (browser): login creates a session row; `RequireAuth` middleware loads it via cookie
2. **Bearer API key** (API clients): prefix `do_` followed by random hex; stored as bcrypt hash; CSRF middleware is bypassed for Bearer requests

Two roles: `admin` and `member`. Admin routes are under `/admin/*` and guarded by `RequireAdmin` middleware. The first account registered on a fresh install becomes admin.

### Authorizing Request-Supplied IDs

**A second ID taken from the request must be constrained in the query, not merely validated.**

Handlers commonly check that the caller owns a parent object — a campaign, an account — and then act on a child ID that also came from the request: a token ID in the route, an `asset_id` in the form. Verifying the parent is necessary but not sufficient. The two IDs still have to be bound together. This shape survives review precisely because the ownership check is present and reads correctly; nothing on the page looks missing.

Put the binding in SQL rather than in a second handler lookup, matching how the account filters are written. The query then reports no match instead of acting, and the handler turns that into a 404:

```go
// Before: the campaign is the caller's, but the token ID was never tied to it
db.ExpireToken(h.DB, tokenID)

// After: UPDATE ... WHERE id = ? AND campaign_id = ?, reporting whether a row matched
revoked, err := db.ExpireTokenInCampaign(h.DB, tokenID, campaignID)
if !revoked {
    http.NotFound(w, r)
    return
}
```

Scoped queries live alongside their unscoped counterparts in `internal/db/queries_*.go`; keep the unscoped one only where the caller already holds the object it is acting on, as the download path does. Where a scoped query is not practical, compare ownership explicitly — `CampaignClone` requires `a.AccountID == accountID` for the asset, not just that the asset exists.

When reviewing a handler: for every ID it reads from the request, name the query or comparison that constrains it to the owner or the parent. If you cannot name one, it is missing.

### Watermarking Pipeline

Campaign publish triggers one job per recipient/token:

1. **Video** (`watermark_video`): FFmpeg re-encodes to H.265 with two `drawtext` overlays (corner + center, low opacity). Optionally followed by invisible DWT-DCT embedding via `embed_watermark.py` on extracted key frames.
2. **Image** (`watermark_image`): ImageMagick composites a visible text overlay → temp PNG → invisible DWT-DCT-SVD → final JPEG. The Go-native embedder runs first; Python `embed_watermark.py` is only the fallback when it errors and `ScriptsDir` is configured, and a failure of both degrades to the visible-only output rather than failing the job.

The watermark payload (16 bytes) encodes: version (2 bytes) + truncated SHA-256 of token ID (8 bytes) + truncated SHA-256 of campaign ID (4 bytes) + CRC-16 (2 bytes). See `internal/watermark/payload.go`.

Watermarked files are stored at `data/watermarked/<campaign_id>/<token_id>.<ext>`. Publishing a campaign normally watermarks every token up front and the download then serves the finished file directly.

There is one exception. If a recipient opens a link whose token is still `PENDING` — the campaign was published while the worker was behind, or the job failed and was cleared — `DownloadPage` enqueues the watermark job on demand and shows `download_preparing.html` with live progress. See `internal/handler/download.go`.

### Template Rendering

Templates use Go `html/template`. `layout.html` is the base; each page template is cloned from it and added to the `templates` map in `handler.New()`. Render calls use `ExecuteTemplate(w, "layout.html", data)` so the layout drives the structure. Flash messages use a short-lived cookie (`downloadonce_flash`).

### Adding a New Page

1. Add a `.html` file in `templates/` — it auto-loads at startup
2. Add handler method(s) to the relevant `internal/handler/*.go` file
3. Register the route in `internal/handler/routes.go`
4. If new DB queries are needed, add them to the appropriate `internal/db/queries_*.go` file
