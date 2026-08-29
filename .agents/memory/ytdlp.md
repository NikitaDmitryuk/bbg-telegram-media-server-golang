---
kind: memory
status: active
date: 2026-08-29
last-verified: 2026-08-29
agent: codex
evidence: internal/downloader/video/ytdlp.go, internal/downloader/video/cookies.go, internal/downloader/video/cookies_test.go, internal/downloader/video/execution.go, internal/downloader/video/updater.go, internal/config/config.go, ops/ansible/site.yml, scripts/ytdlp-cookies-login.sh
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
- Optional managed mode provisions a YouTube-only jar through a temporary Chromium
  session exposed only over an SSH tunnel. The browser profile is deleted after export.
  A daily exclusive check refreshes valid cookies and persists lifecycle state outside
  the jar.
- Rejected cookies are disabled and both metadata and download receive one anonymous
  retry. Empty or unprovisioned cookie configuration stays silently anonymous. Each
  invalid episode produces one persistent, deduplicated notification per active admin;
  successful renewal resets the latch without a recovery message.
- Ansible emits an empty `YTDLP_COOKIES_PATH` when neither managed mode nor a
  controller-provided cookie file is configured. This keeps disabled deployments
  strictly anonymous instead of inheriting a stale server-side path.
- A process-wide read/write guard prevents yt-dlp updates from overlapping active
  metadata probes, downloads, or cookie refresh. Startup update is synchronous; a busy
  periodic update or cookie check is skipped until its next scheduled interval.

See the focused fake-binary coverage in
[`metadata_test.go`](../../internal/downloader/video/metadata_test.go) and
[`cookies_test.go`](../../internal/downloader/video/cookies_test.go).
