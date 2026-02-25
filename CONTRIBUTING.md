# Contributing to DownloadOnce

Thank you for your interest in contributing. This document explains how to get set up and what to expect.

---

## Development environment

**Requirements:**
- Go 1.22+
- Docker + Docker Compose (easiest for running the full stack)
- ffmpeg, imagemagick (for local watermarking without Docker)

**Clone and build:**

```bash
git clone https://github.com/ypk/downloadonce
cd downloadonce

# Build
CGO_ENABLED=0 go build ./cmd/server

# Or via Docker (full environment including FFmpeg, Python, etc.)
docker compose up --build
```

Visit http://localhost:8080 — the first registered account becomes admin.

---

## Project structure

```
cmd/server/         entry point (main.go)
internal/
  app/              application bootstrap
  auth/             session + bcrypt helpers
  cleanup/          periodic expiry scheduler
  config/           env-var configuration
  db/               all SQL queries, one file per domain
  diskstat/         disk space monitoring
  email/            SMTP mailer
  handler/          HTTP handlers and routes
  model/            plain Go structs (no ORM)
  sse/              Server-Sent Events hub
  watermark/        FFmpeg/ImageMagick/Python subprocess wrappers
  webhook/          outgoing HTTP webhook dispatcher
  worker/           background job pool
migrations/         sequential numbered SQL files (applied at startup)
templates/          html/template pages
static/             CSS + JS assets
scripts/            Python watermark scripts (embedded in binary)
```

See [`CLAUDE.md`](CLAUDE.md) for a deeper architectural walkthrough.

---

## Making changes

### Adding a new page

1. Add a `.html` file in `templates/` — it loads automatically at startup.
2. Add handler method(s) to a `internal/handler/*.go` file.
3. Register the route in `internal/handler/routes.go`.
4. If new DB queries are needed, add them to the appropriate `internal/db/queries_*.go` file.

### Adding a database migration

Add a new file to `migrations/` named `NNN_description.sql` (sequential, e.g. `008_my_feature.sql`). It runs automatically on the next server start. **Never modify existing migration files.**

### Code style

- Standard Go formatting (`gofmt`). No linter configuration is required beyond what `go vet` enforces.
- No ORM — write raw SQL in `internal/db/queries_*.go`.
- Avoid adding abstractions for one-time use cases; prefer the simplest thing that works.
- All config comes from environment variables via `internal/config/config.go`.

---

## Submitting changes

1. Fork the repository and create a branch.
2. Make your changes. Run `go build ./...` and `go vet ./...` to check for issues.
3. Open a pull request against `main` with a clear description of what changed and why.

There are currently no automated tests. When adding significant new logic, please include at least a manual testing note in your PR description.

---

## Reporting issues

Please use [GitHub Issues](https://github.com/ypk/downloadonce/issues). Include:
- Steps to reproduce
- Expected vs actual behaviour
- Version or commit hash
- Relevant log output

For security vulnerabilities, see [SECURITY.md](SECURITY.md).
