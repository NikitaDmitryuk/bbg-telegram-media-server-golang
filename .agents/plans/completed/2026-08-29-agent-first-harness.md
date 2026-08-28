---
kind: plan
status: completed
date: 2026-08-29
last-verified: 2026-08-29
agent: codex
evidence: AGENTS.md, .agents/README.md, .agents/decisions/0001-adopt-agent-harness.md, scripts/validate_agent_context.py, scripts/test_validate_agent_context.py, .github/workflows/agent-context.yml
---

# Plan: Implement the agent-first repository harness

## Goal

Give all supported coding agents one canonical entry point and a validated,
progressively loaded repository-context system without changing application runtime.

## Acceptance Criteria

- Root instructions are canonical and vendor files only import them.
- Memory, ADR, plan, debt, and portable skill conventions are documented.
- A dependency-free validator covers structure, metadata, links, lifecycle, and age.
- Make, pre-commit, and GitHub Actions execute the validator.
- Validator tests, existing pre-commit checks, and Go tests pass.

## Tasks

- Added the instruction entry points and `.agents/` records.
- Implemented and unit-tested validation.
- Integrated local, pre-commit, and CI automation.
- Verified the harness and existing application tests.
- Accepted ADR-0001 and archived this plan under `completed/`.

## Progress

All planned repository files and automation are implemented. The initial memory is
limited to the package map and verification surface supported by current code and CI.

## Decisions

[ADR-0001](../../decisions/0001-adopt-agent-harness.md) records the structural
choice. No runtime or API behavior changed.

## Verification

On 2026-08-29, the following checks passed:

- `make agent-context-check`, including seven validator unit tests;
- freshness validation with the 180-day threshold;
- `make pre-commit-run`, including context, YAML, security, lint, vet, and unit hooks;
- `make test` across all Go packages.

A read-only Codex CLI smoke test identified `AGENTS.md` as canonical and the vendor
files as import-only adapters. Claude Code is installed locally but could not run the
same test without authentication. Gemini CLI and Copilot CLI are not installed in the
verification environment. Exact adapter contents are enforced by the validator.

## Handoff

No implementation work remains. The remaining optional cross-client smoke tests can
be repeated when authenticated Claude Code, Gemini CLI, and Copilot CLI are available.
Future agents should follow `AGENTS.md`, load context progressively, and use the
context-maintenance skill for durable changes.
