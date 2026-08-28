#!/usr/bin/env python3
"""Unit tests for the repository agent-context validator."""

from __future__ import annotations

import datetime as dt
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from validate_agent_context import validate_repository  # noqa: E402


METADATA = """---
kind: {kind}
status: {status}
date: 2026-08-29
last-verified: 2026-08-29
agent: test-agent
evidence: AGENTS.md
---
"""

ADR_BODY = """
# ADR-0001: Test decision

## Context
Verified context.

## Decision Drivers
- One driver.

## Considered Options
- One option.

## Decision
Select the option.

## Consequences
Known consequence.

## Confirmation
The fixture confirms the decision.
"""

PLAN_BODY = """
# Plan: Test

## Goal
Complete the fixture.

## Acceptance Criteria
- Validation passes.

## Tasks
- Build fixture.

## Progress
Fixture built.

## Decisions
No task-local decisions.

## Verification
Run validator tests.

## Handoff
No remaining work.
"""


class ValidatorTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary.name)
        self._build_valid_repository()

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def _write(self, relative: str, content: str) -> None:
        path = self.root / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content, encoding="utf-8")

    def _record(self, kind: str, status: str, body: str) -> str:
        return METADATA.format(kind=kind, status=status) + body

    def _build_valid_repository(self) -> None:
        self._write(
            "AGENTS.md",
            "# Agent guide\n\nRead [.agents/README.md](.agents/README.md).\n",
        )
        self._write("CLAUDE.md", "@AGENTS.md\n")
        self._write("GEMINI.md", "@AGENTS.md\n")
        self._write(".agents/README.md", self._record("policy", "active", "\n# Policy\n"))
        self._write(
            ".agents/memory/MEMORY.md",
            self._record("memory-index", "active", "\n# Memory\n"),
        )
        self._write(
            ".agents/memory/repository-map.md",
            self._record("memory", "active", "\n# Map\n"),
        )
        self._write(
            ".agents/memory/verification.md",
            self._record("memory", "active", "\n# Verification\n"),
        )
        self._write(
            ".agents/decisions/template.md",
            """---
kind: adr-template
status: template
date: YYYY-MM-DD
last-verified: YYYY-MM-DD
agent: agent-name
evidence: path-or-commit
---
# ADR template
""",
        )
        self._write(
            ".agents/decisions/0001-adopt-agent-harness.md",
            self._record("adr", "accepted", ADR_BODY),
        )
        self._write(
            ".agents/plans/template.md",
            """---
kind: plan-template
status: template
date: YYYY-MM-DD
last-verified: YYYY-MM-DD
agent: agent-name
evidence: path-or-issue
---
# Plan template
""",
        )
        self._write(
            ".agents/plans/completed/2026-08-29-test.md",
            self._record("plan", "completed", PLAN_BODY),
        )
        (self.root / ".agents/plans/active").mkdir(parents=True)
        self._write(
            ".agents/tech-debt.md",
            self._record("ledger", "active", "\n# Debt ledger\nNo entries.\n"),
        )
        self._write(
            ".agents/skills/maintain-repository-context/SKILL.md",
            """---
name: maintain-repository-context
description: Maintain durable repository context after verified changes.
---
# Maintain context
""",
        )
        self._write("scripts/validate_agent_context.py", "# fixture\n")
        self._write("scripts/test_validate_agent_context.py", "# fixture\n")
        self._write(".github/workflows/agent-context.yml", "name: fixture\n")

    def assert_has_error(self, errors: list[str], fragment: str) -> None:
        self.assertTrue(
            any(fragment in error for error in errors),
            msg=f"Expected an error containing {fragment!r}; got {errors!r}",
        )

    def test_valid_harness(self) -> None:
        self.assertEqual([], validate_repository(self.root))

    def test_missing_metadata(self) -> None:
        path = self.root / ".agents/memory/repository-map.md"
        path.write_text(path.read_text().replace("agent: test-agent\n", ""), encoding="utf-8")
        self.assert_has_error(validate_repository(self.root), "missing metadata field 'agent'")

    def test_broken_relative_link(self) -> None:
        path = self.root / ".agents/memory/repository-map.md"
        path.write_text(path.read_text() + "\n[Missing](does-not-exist.md)\n", encoding="utf-8")
        self.assert_has_error(validate_repository(self.root), "relative link does not exist")

    def test_unknown_status(self) -> None:
        path = self.root / ".agents/memory/repository-map.md"
        path.write_text(path.read_text().replace("status: active", "status: mysterious"), encoding="utf-8")
        self.assert_has_error(validate_repository(self.root), "is not allowed for kind 'memory'")

    def test_accepted_adr_without_confirmation(self) -> None:
        path = self.root / ".agents/decisions/0001-adopt-agent-harness.md"
        content = path.read_text().replace(
            "## Confirmation\nThe fixture confirms the decision.\n", "## Confirmation\n\n"
        )
        path.write_text(content, encoding="utf-8")
        self.assert_has_error(validate_repository(self.root), "accepted ADR requires")

    def test_plan_in_wrong_directory(self) -> None:
        source = self.root / ".agents/plans/completed/2026-08-29-test.md"
        source.write_text(source.read_text().replace("status: completed", "status: active"), encoding="utf-8")
        self.assert_has_error(validate_repository(self.root), "active plan must be in plans/active")

    def test_stale_active_memory(self) -> None:
        path = self.root / ".agents/memory/repository-map.md"
        path.write_text(path.read_text().replace("2026-08-29", "2026-01-01"), encoding="utf-8")
        errors = validate_repository(
            self.root,
            check_stale=True,
            stale_after_days=180,
            today=dt.date(2026, 8, 29),
        )
        self.assert_has_error(errors, "active memory is stale")


if __name__ == "__main__":
    unittest.main()
