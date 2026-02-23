# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Run

```bash
# Build the binary
go build ./cmd/server

# Run locally (requires ffmpeg, imagemagick, python3+invisible-watermark in PATH/venv)
./server

# Build and run via Docker (preferred for full watermarking support)
docker compose up --build

# Run with custom config
BASE_URL=https://example.com SESSION_SECRET=mysecret ./server
```

There are no automated tests in this codebase yet.

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `LISTEN_ADDR` | `:8080` | HTTP listen address |
| `DATA_DIR` | `./data` | Persistent storage root |
| `BASE_URL` | `http://localhost:8080` | Public base URL (used in download links) |
| `SESSION_SECRET` | (weak default) | 32-byte secret for session/CSRF signing — **must be changed in production** |
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
- **`internal/watermark`** — FFmpeg subprocess (`ffmpeg.go`), ImageMagick subprocess (`imagemagick.go`), Python subprocess wrappers for invisible watermark (`invisible.go`), and the 16-byte payload encoding/CRC logic (`payload.go`)
- **`internal/worker`** — `Pool` polls for `PENDING` jobs, dispatches to `processJob()` (watermarking) or `processDetectJob()` (leak detection), updates progress via SSE, fires webhooks and emails on campaign completion
- **`internal/sse`** — lightweight pub/sub hub; channels are named `campaign:<id>` and `token:<id>`
- **`internal/auth`** — bcrypt password helpers and session context keys
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

### Authentication & Authorization

Two authentication paths:
1. **Session cookie** (browser): login creates a session row; `RequireAuth` middleware loads it via cookie
2. **Bearer API key** (API clients): prefix `do_` followed by random hex; stored as bcrypt hash; CSRF middleware is bypassed for Bearer requests

Two roles: `admin` and `member`. Admin routes are under `/admin/*` and guarded by `RequireAdmin` middleware. The first account registered on a fresh install becomes admin.

### Watermarking Pipeline

Campaign publish triggers one job per recipient/token:

1. **Video** (`watermark_video`): FFmpeg re-encodes to H.265 with two `drawtext` overlays (corner + center, low opacity). Optionally followed by invisible DWT-DCT embedding via `embed_watermark.py` on extracted key frames.
2. **Image** (`watermark_image`): ImageMagick composites a visible text overlay → temp PNG → Python `embed_watermark.py` for invisible DWT-DCT → final JPEG.

The watermark payload (16 bytes) encodes: version (2 bytes) + truncated SHA-256 of token ID (8 bytes) + truncated SHA-256 of campaign ID (4 bytes) + CRC-16 (2 bytes). See `internal/watermark/payload.go`.

Pre-computed watermarked files are stored at `data/watermarked/<campaign_id>/<token_id>.<ext>`. Downloads serve these files directly — no on-demand processing.

### Template Rendering

Templates use Go `html/template`. `layout.html` is the base; each page template is cloned from it and added to the `templates` map in `handler.New()`. Render calls use `ExecuteTemplate(w, "layout.html", data)` so the layout drives the structure. Flash messages use a short-lived cookie (`downloadonce_flash`).

### Adding a New Page

1. Add a `.html` file in `templates/` — it auto-loads at startup
2. Add handler method(s) to the relevant `internal/handler/*.go` file
3. Register the route in `internal/handler/routes.go`
4. If new DB queries are needed, add them to the appropriate `internal/db/queries_*.go` file
