# Security Policy

## Supported versions

Only the latest release receives security fixes.

## Reporting a vulnerability

**Please do not open a public GitHub issue for security vulnerabilities.**

Report vulnerabilities via [GitHub Security Advisories](https://github.com/YannKr/downloadonce/security/advisories/new) or by emailing the maintainers directly (see the repository contact information).

Please include:
- A description of the vulnerability and its potential impact
- Steps to reproduce or a proof-of-concept
- Any suggested mitigations if known

You can expect an acknowledgement within 72 hours and a fix or mitigation plan within 14 days for confirmed issues.

## Security design notes

- **SESSION_SECRET** must be a random 32+ byte value. There is no default. If the variable is unset the server generates a random secret on first start and stores it in `$DATA_DIR/session_secret` with mode 0600. The server refuses to start if the value is shorter than 32 bytes or equal to the old `change-me-in-production-32-bytes!` placeholder.
- **CSRF protection** is enabled on all state-changing routes via `gorilla/csrf`.
- **API keys** are stored as bcrypt hashes; the raw key is only shown once at creation.
- **Passwords** are hashed with bcrypt (cost 12).
- **Download tokens** are UUIDs generated with `crypto/rand` via `github.com/google/uuid`.
- **SQLite WAL mode** with a single writer connection prevents data corruption.
- The application does not execute user-supplied shell commands. Watermarking subprocesses receive controlled arguments only; no shell interpolation of user data is performed.
