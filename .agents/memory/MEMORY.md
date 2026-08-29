---
kind: memory-index
status: active
date: 2026-08-29
last-verified: 2026-08-29
agent: codex
evidence: .agents/memory/repository-map.md, .agents/memory/verification.md
---

# Repository memory index

Load only the topics needed for the current task:

- [Repository map](repository-map.md): composition root, package responsibilities,
  and stable runtime boundaries.
- [Verification](verification.md): supported Make targets and CI expectations.
- [yt-dlp runtime integration](ytdlp.md): cached metadata, retries, managed cookie
  lifecycle with anonymous fallback, sanitized diagnostics, and execution coordination.
- [qBittorrent lifecycle](qbittorrent.md): authenticated API recovery, indefinite
  stall handling, persistent warning deduplication, and restart monitoring.

Architecture decisions are under [decisions](../decisions/). Multi-step task state
is under [plans](../plans/). Source code, tests, configuration, and Git history
override these summaries when they disagree.
