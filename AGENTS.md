# Repository Agent Guide

This file is the canonical entry point for every coding agent in this repository.
`CLAUDE.md` and `GEMINI.md` only import it and must not contain independent rules.

## Start here

Before a non-trivial task:

1. Read [the agent-context protocol](.agents/README.md).
2. Read [the memory index](.agents/memory/MEMORY.md).
3. Load only the memory, ADR, plan, or skill files relevant to the task.
4. Treat source code, tests, configuration, and Git history as the source of truth.

## Repository map

- `cmd/telegram-media-server/`: process composition, startup, and shutdown.
- `internal/api/`: optional REST API and embedded OpenAPI/Swagger assets.
- `internal/app/`: shared application dependencies passed to handlers.
- `internal/bot/`, `internal/handlers/`: Telegram client and update routing.
- `internal/config/`: environment-driven configuration.
- `internal/database/`: persistent application state.
- `internal/downloader/`: download orchestration and backend implementations.
- `internal/deletion/`, `internal/filemanager/`: file lifecycle management.
- `internal/models/`, `internal/notifier/`, `internal/prowlarr/`, `internal/tvcompat/`: domain and integration support.
- `web/`: browser-facing assets.
- `docs/`: user and operator documentation.

The verified package map and composition details live in
[repository-map.md](.agents/memory/repository-map.md).

## Required verification

Run checks proportional to the change. The standard commands are:

```sh
make agent-context-check
make lint
make vet
make test-unit
make test-integration
make build-simple
make check
make pre-commit-run
```

See [verification.md](.agents/memory/verification.md) for scope and CI mapping.

## Change rules

- Keep runtime and public API behavior stable unless the task explicitly changes it.
- Add or update tests for behavior changes and bug fixes.
- Keep handlers thin; shared runtime dependencies belong in `internal/app`.
- Prefer the existing package boundaries and Make targets over ad-hoc alternatives.
- Do not edit generated or embedded artifacts without identifying their source.
- Preserve unrelated working-tree changes.
- Do not push, rewrite history, delete material data, or publish externally unless asked.

## Security boundaries

- Never commit secrets, tokens, credentials, personal data, or raw production data.
- Do not print secret environment-variable values while diagnosing configuration.
- Preserve the REST API access invariant: local/Docker-host access may be allowed
  without a key; non-local access must not silently bypass configured API-key checks.
- Validate external paths, URLs, filenames, and Telegram-provided input at boundaries.

## Agent-maintained context

`.agents/` is version-controlled repository context authored by agents. Humans may
read and review it; requested changes are applied by an agent. Every real record
must be concise, evidence-backed, attributable, and free of chain-of-thought,
raw logs, secrets, personal data, and unverified conclusions.

Update existing facts instead of accumulating contradictions. Use:

- `memory/` for durable, verified repository facts;
- `decisions/` for structural or hard-to-reverse decisions;
- `plans/active/` and `plans/completed/` for multi-step work and handoff;
- `tech-debt.md` for evidence-backed debt with an explicit exit condition;
- `skills/` for reusable agent procedures.

Use the [context-maintenance skill](.agents/skills/maintain-repository-context/SKILL.md)
when a task creates or invalidates durable repository knowledge. Validate every
context change with `make agent-context-check`.
