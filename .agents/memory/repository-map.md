---
kind: memory
status: active
date: 2026-08-29
last-verified: 2026-08-29
agent: codex
evidence: cmd/telegram-media-server/main.go, internal/app/app.go, internal/api/server.go, internal/config/config.go
---

# Repository map

## Composition and lifecycle

`cmd/telegram-media-server/main.go` is the composition root. It loads environment
configuration, opens the database, initializes localization, download and deletion
services, creates the Telegram bot and shared `app.App`, resumes incomplete work,
optionally starts the REST API, starts periodic updaters, routes Telegram updates,
and performs graceful shutdown on `SIGINT` or `SIGTERM`.

`internal/app/app.go` defines the dependency container passed to handlers. It owns
the bot, database, configuration, download manager, and deletion queue references.

## Package boundaries

- `internal/api`: optional HTTP server, versioned API routes, and embedded OpenAPI UI.
- `internal/bot` and `internal/handlers`: Telegram transport and update handlers.
- `internal/config`: environment-derived configuration and defaults.
- `internal/database`: persistent state access.
- `internal/downloader`: orchestration plus torrent, qBittorrent, and video backends.
- `internal/deletion` and `internal/filemanager`: deletion scheduling and file handling.
- `internal/models`: shared domain types.
- `internal/notifier`, `internal/prowlarr`, and `internal/tvcompat`: integrations and
  compatibility support.
- `internal/testutils`: shared test helpers.

## Security invariant

`internal/api/server.go` distinguishes local or Docker-host callers from remote
callers. Local access can be allowed without an API key. Remote requests must not
gain access by silently bypassing the configured API-key policy; when no remote key
is configured, non-local requests are rejected.
