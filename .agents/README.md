---
kind: policy
status: active
date: 2026-08-29
last-verified: 2026-08-29
agent: codex
evidence: AGENTS.md, scripts/validate_agent_context.py
---

# Agent context protocol

This directory is the repository's version-controlled context for coding agents.
`AGENTS.md` is the single general entry point; this file governs the records kept
under `.agents/`.

## Authorship and review

- Agents author changes in `.agents/` on a user's instruction or as part of an
  authorized repository task.
- Humans may read and review these records and ask an agent to change them.
- The `agent` field records the producing agent. Git identity is not proof that a
  human or agent physically typed a change.
- Repository context is reviewed like code and must pass mechanical validation.

## Loading protocol

Before non-trivial work, read this file and `memory/MEMORY.md`. Then load only the
topic records, ADRs, active plans, or skills that apply. Do not ingest the whole
directory by default.

## Required metadata

Every Markdown record except an Agent Skill has flat YAML frontmatter with:

- `kind`: record type;
- `status`: lifecycle state allowed for that kind;
- `date`: creation date in `YYYY-MM-DD` form;
- `last-verified`: most recent evidence check in `YYYY-MM-DD` form;
- `agent`: agent name or stable identifier;
- `evidence`: comma-separated repository paths, commit identifiers, or URLs.

Agent Skills use their standard `name` and `description` frontmatter instead.

## Classification

- `memory/MEMORY.md`: short index, never a knowledge dump.
- `memory/*.md`: durable facts that are expensive to rediscover and supported by
  code, tests, configuration, or Git history.
- `decisions/*.md`: shortened MADR records for structural or hard-to-reverse choices.
- `plans/active/*.md`: multi-step work needing progress and handoff state.
- `plans/completed/*.md`: finished or abandoned plans with verification recorded.
- `tech-debt.md`: verified debt ledger with impact, evidence, and exit condition.
- `skills/*/SKILL.md`: reusable procedures following the Agent Skills format.

## Record lifecycle

- Code, tests, configuration, and Git history remain the source of truth.
- Update an existing fact when it changes. If history matters, mark the old record
  `superseded` and link the replacement; do not keep active contradictions.
- Memory statuses: `active`, `archived`, `superseded`.
- ADR statuses: `proposed`, `accepted`, `rejected`, `deprecated`, `superseded`.
- Plan statuses: `active`, `completed`, `abandoned`.
- An agent may accept an ADR only after implementation and successful verification.
- Do not semantically rewrite an accepted ADR. Create a new ADR and supersede it.
- Move a completed or abandoned plan from `active/` to `completed/`.
- Active memory should be reverified within 180 days.

## Content rules

Write conclusions and evidence, never hidden reasoning transcripts. Do not store:

- secrets, credentials, tokens, personal or production data;
- chain-of-thought, scratch work, raw command output, or full session transcripts;
- guesses, unverified conclusions, volatile task state, or copied source code;
- artificial plans, decisions, or debt created only to populate the directory.

Keep links relative where possible. Prefer precise paths and named tests over prose
assertions. Run `make agent-context-check` after every context change.
