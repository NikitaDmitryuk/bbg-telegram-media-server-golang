---
kind: memory
status: active
date: 2026-08-29
last-verified: 2026-08-29
agent: codex
evidence: internal/downloader/qbittorrent/client.go, internal/downloader/qbittorrent/downloader.go, internal/downloader/manager/monitor.go, internal/app/resume.go
---

# qBittorrent lifecycle

- qBittorrent downloads are long-lived jobs. Waiting for metadata or peers,
  `stalledDL`, an absent hash, and the `error` or `missingFiles` states do not
  delete the movie or torrent automatically.
- Safe Web API reads retry with jittered exponential backoff capped at one minute.
  HTTP `401` and `403` trigger one reauthentication and replay. Mutating requests
  are replayed only after an explicit authentication rejection, not after an
  ambiguous transport failure; an ambiguous add failure keeps the movie record
  for manual inspection instead of running automatic cleanup.
- `TORRENT_STALL_WARNING_AFTER` defaults to `30m`; `0` disables warnings. The
  per-movie latch is stored in SQLite, excluded from JSON, and reset only after
  actual progress or episode completion.
- Live Telegram jobs warn their initiator. API-originated and restarted jobs warn
  every active administrator. A configured non-zero `DOWNLOAD_TIMEOUT` remains
  an explicit terminal override.
- Restart attachment uses capped exponential backoff and does not reset it merely
  because a monitor was attached. Successful completion and manual deletion keep
  their existing cleanup behavior.

Verification is covered by qBittorrent client/downloader tests, manager stall tests,
resume notifier tests, and the full repository pre-commit target.
