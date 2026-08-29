---
kind: memory
status: active
date: 2026-08-29
last-verified: 2026-08-29
agent: codex
evidence: internal/downloader/video/ytdlp.go, internal/downloader/video/metadata_test.go, internal/downloader/video/execution.go, internal/downloader/video/updater.go, internal/config/config.go, ops/ansible/site.yml
---

# yt-dlp runtime integration

- Downloader creation performs one JSON metadata probe and caches title, file size,
  and video codec. Temporary probe failures receive up to three attempts within a
  30-second budget; exhausted temporary failures use a URL-derived fallback title.
- Authentication-required responses are typed as
  `downloader.ErrVideoAuthenticationRequired` and are returned before database
  insertion. Diagnostics remove configured URLs and proxy values before logging.
- `YTDLP_EXTRA_ARGS` and the optional `YTDLP_COOKIES_PATH` apply to metadata and
  download commands. Ansible can copy an external cookies file with restricted
  permissions; cookie contents never belong in Git.
- A process-wide read/write guard prevents yt-dlp updates from overlapping active
  metadata probes or downloads. Startup update is synchronous; a busy periodic
  update is skipped until its next scheduled interval.

See the focused fake-binary coverage in
[`metadata_test.go`](../../internal/downloader/video/metadata_test.go) and
[`updater_test.go`](../../internal/downloader/video/updater_test.go).
