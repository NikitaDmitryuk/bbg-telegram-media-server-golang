---
kind: adr
status: accepted
date: 2026-08-29
last-verified: 2026-08-29
agent: codex
evidence: internal/downloader/video/cookies.go, scripts/ytdlp-cookies-login.sh, ops/ansible/site.yml, https://github.com/yt-dlp/yt-dlp/wiki/Extractors#exporting-youtube-cookies
---

# ADR-0002: Manage YouTube cookies without stored login credentials

## Context

Some YouTube content requires an authenticated cookie jar. Google login cannot be
renewed safely with a stored password or OAuth, while expired cookies must not break
public downloads that work anonymously.

## Decision Drivers

- Keep passwords, 2FA data, browser profiles, and cookie contents out of Git and logs.
- Preserve anonymous yt-dlp behavior when cookies are absent or rejected.
- Make rare interactive renewal usable on a headless LAN server.
- Notify administrators once per expiry episode without reminders.

## Considered Options

- Store credentials and automate login: rejected because it expands credential risk
  and cannot reliably handle Google challenges.
- Periodically export a normal workstation profile: rejected because it couples the
  server to a workstation and exports unrelated browser state.
- Use a temporary server browser and maintain a filtered cookie jar: selected.

## Decision

Managed mode uses a temporary Chromium profile reachable only through an SSH tunnel.
The operator completes Google login interactively, after which only YouTube-domain
cookies are validated and installed. The profile is deleted. A daily exclusive check
refreshes the jar; rejected cookies are disabled and public downloads retry anonymously.

Cookie lifecycle state is persisted separately from the jar. Each invalid episode
causes one Telegram notification per active administrator; successful renewal silently
resets the latch. Empty cookie configuration remains fully anonymous.

## Consequences

- Final Google reauthentication remains an explicit human action.
- Managed hosts require Chromium and Xvfb, but expose no additional network listener.
- Static controller-provided cookie files remain supported and can be validated even
  when their root-owned location prevents in-place refresh.

## Confirmation

Implemented by the cookie lifecycle manager, anonymous metadata/download retry,
administrator lookup, SSH login helper, and managed Ansible resources. Confirmed by
`go test ./...`, `make test-integration`, `make pre-commit-run`,
`make ansible-check`, `make build-simple`, and `make agent-context-check`.
