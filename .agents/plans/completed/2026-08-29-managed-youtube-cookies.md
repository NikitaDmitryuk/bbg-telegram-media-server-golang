---
kind: plan
status: completed
date: 2026-08-29
last-verified: 2026-08-29
agent: codex
evidence: .agents/decisions/0002-manage-youtube-cookies.md, internal/downloader/video/cookies.go, scripts/ytdlp-cookies-login.sh, ops/ansible/site.yml
---

# Plan: Managed YouTube cookie lifecycle

## Goal

Provide SSH-only interactive cookie provisioning, daily refresh, anonymous fallback,
and one administrator notification per expiry episode.

## Acceptance Criteria

- Empty or invalid cookies never prevent public anonymous downloads.
- Managed login stores only a filtered jar and deletes its browser profile.
- Validation refreshes cookies without overlapping active yt-dlp work.
- Administrators receive one expiry message with persistent deduplication.

## Tasks

- Implemented cookie lifecycle state, validation, fallback, and notification.
- Added managed Ansible infrastructure and login helper.
- Added focused tests, documentation, ADR, and durable memory.
- Completed repository, Go, shell, and Ansible checks.

## Progress

Implementation, automated verification, and a server deployment completed on
2026-08-29. The interactive flow was exercised, then deliberately disabled because
its operational UX needs further work. The production configuration is strictly
anonymous: no cookie path, cookie jar, browser profile, login service, or debug port
is active.

## Decisions

See [ADR-0002](../../decisions/0002-manage-youtube-cookies.md).

## Verification

Passed `go test ./...`, `make test-unit`, `make test-integration`, `make lint`,
`make vet`, `make build-simple`, `make pre-commit-run`, `make ansible-check`, and
`make agent-context-check`. Rendered shell templates pass `sh -n`. Server checks
confirmed TMS health `200`, an empty cookie path, absent cookie/profile files, a
disabled login unit, a stopped Chromium process, and a closed debug port.

## Handoff

Managed mode remains available but intentionally disabled. Before enabling it later,
revisit the login UX and repeat the first-session export test. Anonymous downloads
remain the production behavior until then.
