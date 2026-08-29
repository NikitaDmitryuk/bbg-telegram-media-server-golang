---
kind: ledger
status: active
date: 2026-08-29
last-verified: 2026-08-29
agent: codex
evidence: .agents/README.md
---

# Agent-verified technical debt

Only evidence-backed debt belongs here. Each entry needs a stable identifier, impact,
repository evidence, lifecycle status, and an observable exit condition.

| ID | Status | Impact | Evidence | Exit condition |
| --- | --- | --- | --- | --- |
| TD-001 | open | A Telegram bot credential may remain valid after appearing in historical startup diagnostics; future bot errors are now redacted. | `internal/bot/bot.go`, `internal/bot/bot_test.go` | Rotate the Telegram bot token, update the managed secret, redeploy successfully, and verify authentication with the replacement token. |
