# DownloadOnce

**Token-based file distribution with forensic watermarking.** Each recipient gets a unique download link; their copy is invisibly watermarked so leaks can be traced back to the source.

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

---

## Features

- **Forensic watermarking** — visible overlay + invisible DWT-DCT steganographic embedding that survives JPEG re-compression
- **Token-based distribution** — each recipient gets a unique link with optional download limits and expiry dates
- **Leak detection** — decode a leaked file to identify which recipient's copy it was
- **Multi-user** — admin and member roles; shared recipient/asset library
- **Recipient groups** — organise recipients into named groups for bulk campaign creation
- **Resumable uploads** — chunked upload with progress bar for large video files
- **Campaign management** — draft → publish workflow; per-recipient watermarking jobs run in background
- **Email notifications** — SMTP delivery of download links and campaign-complete alerts
- **Webhooks** — outgoing HTTP hooks for campaign and download events
- **Audit log** — append-only log of every action taken
- **Disk monitoring** — configurable free-space warnings with admin dashboard
- **REST API** — Bearer-token API for headless/CI automation
- **Single binary** — SQLite embedded, no external queue or cache required

---

## Quick Start

```bash
git clone https://github.com/YannKr/downloadonce
cd downloadonce
cp .env.example .env        # edit BASE_URL and SESSION_SECRET at minimum
docker compose up
```

Visit **http://localhost:8080** — the first registered account becomes admin automatically.

---

## Requirements

| Deployment | Requirements |
|---|---|
| **Docker (recommended)** | Docker + Docker Compose — everything else (FFmpeg, ImageMagick, Python) is bundled in the image |
| **Bare metal** | Go 1.22+, `ffmpeg`, `imagemagick`, `python3`, pip packages `invisible-watermark` + `opencv-python-headless` |

---

## Configuration

All configuration is via environment variables. Copy `.env.example` to `.env` and adjust:

| Variable | Default | Description |
|---|---|---|
| `BASE_URL` | `http://localhost:8080` | Public-facing URL used in download links |
| `SESSION_SECRET` | — | **Required.** 32+ byte random secret. Generate: `openssl rand -hex 32` |
| `LISTEN_ADDR` | `:8080` | HTTP listen address |
| `DATA_DIR` | `./data` | Persistent storage root (assets, watermarked files, SQLite DB) |
| `WORKER_COUNT` | `2` | Concurrent watermark encoding workers |
| `MAX_UPLOAD_BYTES` | `53687091200` | Maximum upload file size (50 GB) |
| `ALLOW_REGISTRATION` | `false` | Allow public self-registration (off = invite-only via admin) |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `FONT_PATH` | `/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf` | Font used for visible watermark overlay |
| `VENV_PATH` | `/opt/venv` | Python venv containing `invisible-watermark` |
| `SMTP_HOST` | — | SMTP server hostname (leave empty to disable email) |
| `SMTP_PORT` | `587` | SMTP port |
| `SMTP_USER` | — | SMTP username |
| `SMTP_PASS` | — | SMTP password |
| `SMTP_FROM` | — | Sender address (e.g. `noreply@example.com`) |
| `CLEANUP_INTERVAL_MINS` | `60` | How often the cleanup scheduler runs (minutes) |
| `UPLOAD_SESSION_TTL_HOURS` | `24` | How long an incomplete chunked upload is kept before expiry |
| `DISK_WARN_YELLOW_PCT` | `20` | Free-disk % below which a yellow warning is shown |
| `DISK_WARN_RED_PCT` | `10` | Free-disk % below which a red alert is shown |
| `DISK_WARN_BLOCK_PCT` | `5` | Free-disk % below which new uploads are blocked |
| `MAX_STORAGE_BYTES` | `0` | App-level storage cap in bytes (0 = unlimited) |
| `WM_COMPRESSION_FACTOR` | `0.9` | Estimated compression ratio used for disk-space estimates |

---

## Deployment

### Docker Compose (recommended)

```bash
cp .env.example .env
# Edit .env — set BASE_URL and SESSION_SECRET at minimum
docker compose up -d
```

For HTTPS, uncomment the Caddy service in `docker-compose.yml` and point `Caddyfile` at your domain.

### Bare metal (Debian/Ubuntu)

Download the latest binary from [GitHub Releases](https://github.com/YannKr/downloadonce/releases), then:

```bash
scp downloadonce setup.sh root@your-server:~
ssh root@your-server 'BASE_URL=https://dl.example.com ./setup.sh'
```

`setup.sh` installs system dependencies, sets up a Python venv, installs a systemd service, and generates a random `SESSION_SECRET` automatically.

### Reverse proxy

The app speaks plain HTTP on `LISTEN_ADDR`. Put it behind Caddy, nginx, or any TLS-terminating proxy.

Minimal **Caddyfile**:

```caddyfile
dl.example.com {
    reverse_proxy app:8080
}
```

---

## Architecture

Single-binary, single-process — no external services required beyond the system tools.

| Layer | Technology |
|---|---|
| HTTP router | [chi](https://github.com/go-chi/chi) |
| Database | SQLite (WAL mode, `modernc.org/sqlite` — no CGO) |
| Auth | Session cookies + CSRF; optional Bearer API keys |
| Background jobs | In-process goroutine pool polling a `jobs` table |
| Real-time progress | Server-Sent Events |
| Video watermarking | FFmpeg subprocess with `drawtext` overlay |
| Image watermarking | ImageMagick subprocess + Python DWT-DCT embedding |
| Invisible watermark | `invisible-watermark` Python library |
| File delivery | Pre-computed watermarked files served directly |
| File embedding | Templates, static assets, migrations, and Python scripts compiled into the binary |

The 16-byte watermark payload encodes: version (2 B) + truncated SHA-256 of token ID (8 B) + truncated SHA-256 of campaign ID (4 B) + CRC-16 checksum (2 B).

See [`CLAUDE.md`](CLAUDE.md) for a full developer guide.

---

## Releases

Releases follow [Semantic Versioning](https://semver.org/). Binary builds are available on the [GitHub Releases](https://github.com/YannKr/downloadonce/releases) page.

| Version | Highlights |
|---|---|
| **v1.1.2** | Fix `auto_publish` API flag skipping watermark job enqueue |
| **v1.1.1** | Campaign archive, clone on published campaigns, add recipients after publish |
| **v1.1.0** | REST API with Bearer auth, OpenAPI spec, webhook delivery log + retry, campaign clone, export links, Go-native DWT-DCT-SVD watermarking |
| **v1.0.1** | Reduce Docker image size from ~2.3 GB to ~1.1 GB |
| **v1.0.0** | Initial release |

---

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## Security

See [SECURITY.md](SECURITY.md) for how to report vulnerabilities.

## License

[MIT](LICENSE) © 2025 ypk
