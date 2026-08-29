---
kind: adr
status: accepted
date: 2026-08-29
last-verified: 2026-08-29
agent: codex
evidence: AGENTS.md, .agents/README.md, scripts/validate_agent_context.py, .github/workflows/agent-context.yml
---

# ADR-0001: Adopt a single agent-first repository harness

## Context

Several coding agents may work in this repository, but vendor-specific instruction
files can drift and ephemeral sessions cannot reliably retain repository knowledge.
The repository needs one discoverable instruction source, curated durable context,
and mechanical protection against stale or malformed records.

## Decision Drivers

- One canonical set of general instructions across supported agents.
- Progressive disclosure instead of a monolithic prompt.
- Reviewable, evidence-backed repository knowledge stored with the code.
- Portable conventions and standard-library-only validation.
- No change to application runtime or public API behavior.

## Considered Options

- Maintain independent vendor instruction files. This is easy to discover but
  duplicates policy and allows silent drift.
- Keep all knowledge in one large root prompt. This is canonical but costly to load
  and difficult to maintain.
- Keep agent memory outside Git. This avoids repository files but loses shared review,
  portability, and historical linkage.
- Use `AGENTS.md` as a map and `.agents/` as curated progressive context, with thin
  vendor adapters and automated validation.

## Decision

Use root `AGENTS.md` as the sole general instruction source. Store agent-maintained,
version-controlled memory, decisions, plans, debt, and skills under `.agents/`.
`CLAUDE.md` and `GEMINI.md` contain only `@AGENTS.md`. Validate the harness locally,
in pre-commit, and in a dedicated GitHub Actions workflow.

## Consequences

Agents share one reviewed contract and load details only when relevant. Context edits
carry metadata and evidence and can fail CI when structurally invalid or stale. The
repository gains documentation maintenance work, and the agent-only authorship rule
remains a process contract rather than a reliably enforceable Git identity check.

## Confirmation

The harness files, adapters, validator tests, Make target, pre-commit hook, and
GitHub Actions workflow are present. On 2026-08-29, `make agent-context-check`,
the 180-day freshness check, full `make pre-commit-run`, and `make test` passed.
