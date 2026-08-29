---
kind: plan
status: completed
date: 2026-08-29
last-verified: 2026-08-29
agent: codex
evidence: internal/downloader/qbittorrent, internal/downloader/manager/monitor.go, internal/app/resume.go, internal/logutils/logutils.go
---

# Plan: Stabilize qBittorrent downloads

## Goal

Keep torrent jobs alive across transient qBittorrent failures and provide one durable
stall warning without changing the public HTTP API.

## Acceptance Criteria

- qBittorrent authentication and read failures recover without deleting downloads.
- Stalled torrents wait indefinitely and notify once per stall episode.
- Resume retries use capped exponential backoff and logs retain root errors.
- Repository checks and Go tests pass.

## Tasks

- Fixed structured logger chaining and qBittorrent request recovery.
- Preserved movie records after ambiguous mutating API failures.
- Made qBittorrent monitoring stall-tolerant and repaired resume backoff.
- Persisted the stall notification latch and routed notifications.
- Added tests, configuration documentation, and durable repository context.

## Progress

- Implementation and documentation completed on 2026-08-29.

## Decisions

- Router configuration remains outside scope.
- A configured absolute download timeout still overrides indefinite waiting.
- Live Telegram jobs notify their initiator; resumed and API jobs notify administrators.

## Verification

- `go test ./...`
- `make pre-commit`
- `make ansible-check`
- `make agent-context-check`

All checks passed on 2026-08-29.

## Handoff

No implementation work remains. Deployment is intentionally separate from this task.
