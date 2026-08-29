---
name: maintain-repository-context
description: Maintain this repository's agent-authored memory, ADRs, plans, and technical-debt ledger when work creates, changes, or invalidates durable repository knowledge.
---

# Maintain repository context

Use this skill after a durable discovery, structural decision, multi-session task, or
verified technical-debt finding. Do not create a record for routine edits or facts
that are cheap to rediscover.

## Procedure

1. Read `.agents/README.md` and `.agents/memory/MEMORY.md`.
2. Classify the information:
   - durable verified fact: update an existing memory topic or add one and index it;
   - structural or hard-to-reverse choice: create an ADR from the template;
   - multi-step work needing handoff: create or update an active plan;
   - verified liability: update the technical-debt ledger with an exit condition.
3. Search for an existing record before adding one. Update the source record or mark
   it `superseded`; never leave active contradictions.
4. Cite repository paths, tests, configuration, commits, or authoritative URLs in the
   flat `evidence` field and in the body where that improves traceability.
5. Keep conclusions concise. Never store secrets, personal data, raw logs,
   chain-of-thought, scratch work, copied source, or unsupported claims.
6. Accept an ADR only after its implementation and confirmation checks pass.
7. Move completed or abandoned plans to `.agents/plans/completed/` and record the
   final verification and handoff state.
8. Run `make agent-context-check` and fix every reported issue.

Source code, tests, configuration, and Git history always override repository memory.
