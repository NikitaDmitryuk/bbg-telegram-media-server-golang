#!/usr/bin/env python3
"""Validate the repository's version-controlled agent context."""

from __future__ import annotations

import argparse
import datetime as dt
import re
import sys
from pathlib import Path
from urllib.parse import unquote


REQUIRED_FILES = (
    "AGENTS.md",
    "CLAUDE.md",
    "GEMINI.md",
    ".agents/README.md",
    ".agents/memory/MEMORY.md",
    ".agents/memory/repository-map.md",
    ".agents/memory/verification.md",
    ".agents/decisions/template.md",
    ".agents/decisions/0001-adopt-agent-harness.md",
    ".agents/plans/template.md",
    ".agents/tech-debt.md",
    ".agents/skills/maintain-repository-context/SKILL.md",
    "scripts/validate_agent_context.py",
    "scripts/test_validate_agent_context.py",
    ".github/workflows/agent-context.yml",
)

REQUIRED_DIRECTORIES = (
    ".agents/plans/active",
    ".agents/plans/completed",
)

REQUIRED_METADATA = (
    "kind",
    "status",
    "date",
    "last-verified",
    "agent",
    "evidence",
)

ALLOWED_STATUSES = {
    "policy": {"active"},
    "memory-index": {"active", "archived", "superseded"},
    "memory": {"active", "archived", "superseded"},
    "adr-template": {"template"},
    "adr": {"proposed", "accepted", "rejected", "deprecated", "superseded"},
    "plan-template": {"template"},
    "plan": {"active", "completed", "abandoned"},
    "ledger": {"active"},
}

ADR_HEADINGS = (
    "Context",
    "Decision Drivers",
    "Considered Options",
    "Decision",
    "Consequences",
    "Confirmation",
)

PLAN_HEADINGS = (
    "Goal",
    "Acceptance Criteria",
    "Tasks",
    "Progress",
    "Decisions",
    "Verification",
    "Handoff",
)

PLACEHOLDER_RE = re.compile(
    r"\b(?:TODO|TBD|CHANGEME|PLACEHOLDER)\b|\{\{[^}]+\}\}|<[^>\n]+>",
    re.IGNORECASE,
)
LINK_RE = re.compile(r"(?<!!)\[[^\]]+\]\(([^)]+)\)")
SKILL_NAME_RE = re.compile(r"^[a-z0-9]+(?:-[a-z0-9]+)*$")
ADR_FILENAME_RE = re.compile(r"^(\d{4})-[a-z0-9]+(?:-[a-z0-9]+)*\.md$")


def _relative(path: Path, root: Path) -> str:
    try:
        return path.relative_to(root).as_posix()
    except ValueError:
        return path.as_posix()


def _read(path: Path, root: Path, errors: list[str]) -> str | None:
    try:
        return path.read_text(encoding="utf-8")
    except (OSError, UnicodeError) as exc:
        errors.append(f"{_relative(path, root)}: cannot read UTF-8 text: {exc}")
        return None


def _parse_frontmatter(
    path: Path, text: str, root: Path, errors: list[str]
) -> tuple[dict[str, str], str] | None:
    lines = text.splitlines()
    if not lines or lines[0] != "---":
        errors.append(f"{_relative(path, root)}: missing opening frontmatter delimiter")
        return None

    try:
        end = lines.index("---", 1)
    except ValueError:
        errors.append(f"{_relative(path, root)}: missing closing frontmatter delimiter")
        return None

    metadata: dict[str, str] = {}
    for line_number, line in enumerate(lines[1:end], start=2):
        if not line.strip() or line.startswith((" ", "\t", "-")) or ":" not in line:
            errors.append(
                f"{_relative(path, root)}:{line_number}: frontmatter must use flat key: value entries"
            )
            continue
        key, value = line.split(":", 1)
        key = key.strip()
        value = value.strip()
        if not key or not value:
            errors.append(
                f"{_relative(path, root)}:{line_number}: frontmatter key and value must be non-empty"
            )
            continue
        if key in metadata:
            errors.append(f"{_relative(path, root)}:{line_number}: duplicate metadata key {key!r}")
            continue
        metadata[key] = value

    return metadata, "\n".join(lines[end + 1 :])


def _parse_date(value: str) -> dt.date | None:
    try:
        parsed = dt.date.fromisoformat(value)
    except ValueError:
        return None
    return parsed if parsed.isoformat() == value else None


def _section_content(body: str, heading: str) -> str | None:
    match = re.search(
        rf"^## {re.escape(heading)}\s*$\n(.*?)(?=^##\s|\Z)",
        body,
        flags=re.MULTILINE | re.DOTALL,
    )
    return match.group(1).strip() if match else None


def _validate_evidence(
    path: Path, value: str, root: Path, errors: list[str]
) -> None:
    for item in (part.strip() for part in value.split(",")):
        if not item:
            errors.append(f"{_relative(path, root)}: evidence contains an empty entry")
            continue
        if item.startswith(("http://", "https://", "git:", "commit:")):
            continue
        evidence_path = root / item
        if not evidence_path.exists():
            errors.append(
                f"{_relative(path, root)}: evidence path does not exist: {item}"
            )


def _validate_record(
    path: Path,
    text: str,
    root: Path,
    errors: list[str],
    *,
    check_stale: bool,
    stale_after_days: int,
    today: dt.date,
) -> None:
    parsed = _parse_frontmatter(path, text, root, errors)
    if parsed is None:
        return
    metadata, body = parsed

    for key in REQUIRED_METADATA:
        if key not in metadata:
            errors.append(f"{_relative(path, root)}: missing metadata field {key!r}")

    kind = metadata.get("kind")
    status = metadata.get("status")
    if kind not in ALLOWED_STATUSES:
        errors.append(f"{_relative(path, root)}: unknown kind {kind!r}")
        return
    if status not in ALLOWED_STATUSES[kind]:
        errors.append(
            f"{_relative(path, root)}: status {status!r} is not allowed for kind {kind!r}"
        )

    is_template = status == "template"
    if not is_template:
        for key in ("date", "last-verified"):
            value = metadata.get(key)
            if value and _parse_date(value) is None:
                errors.append(
                    f"{_relative(path, root)}: metadata field {key!r} must be YYYY-MM-DD"
                )
        if metadata.get("agent", "").lower() in {"unknown", "agent", "n/a", "none"}:
            errors.append(f"{_relative(path, root)}: agent attribution is not specific")
        if "evidence" in metadata:
            _validate_evidence(path, metadata["evidence"], root, errors)
        if PLACEHOLDER_RE.search(text):
            errors.append(f"{_relative(path, root)}: real record contains a placeholder value")

    if kind == "adr":
        if not ADR_FILENAME_RE.fullmatch(path.name):
            errors.append(f"{_relative(path, root)}: ADR filename must be NNNN-lowercase-slug.md")
        for heading in ADR_HEADINGS:
            if _section_content(body, heading) is None:
                errors.append(f"{_relative(path, root)}: missing ADR heading '## {heading}'")
        if status == "accepted":
            confirmation = _section_content(body, "Confirmation")
            if not confirmation or PLACEHOLDER_RE.search(confirmation):
                errors.append(
                    f"{_relative(path, root)}: accepted ADR requires a completed Confirmation"
                )

    if kind == "plan":
        for heading in PLAN_HEADINGS:
            if _section_content(body, heading) is None:
                errors.append(f"{_relative(path, root)}: missing plan heading '## {heading}'")
        directory = path.parent.name
        if status == "active" and directory != "active":
            errors.append(f"{_relative(path, root)}: active plan must be in plans/active")
        if status in {"completed", "abandoned"} and directory != "completed":
            errors.append(
                f"{_relative(path, root)}: {status} plan must be in plans/completed"
            )

    if (
        check_stale
        and kind in {"memory", "memory-index"}
        and status == "active"
        and (verified := _parse_date(metadata.get("last-verified", ""))) is not None
    ):
        age = (today - verified).days
        if age > stale_after_days:
            errors.append(
                f"{_relative(path, root)}: active memory is stale ({age} days; limit {stale_after_days})"
            )


def _validate_skill(path: Path, text: str, root: Path, errors: list[str]) -> None:
    parsed = _parse_frontmatter(path, text, root, errors)
    if parsed is None:
        return
    metadata, _ = parsed
    allowed = {"name", "description"}
    missing = allowed - metadata.keys()
    extra = metadata.keys() - allowed
    for key in sorted(missing):
        errors.append(f"{_relative(path, root)}: missing skill metadata field {key!r}")
    for key in sorted(extra):
        errors.append(f"{_relative(path, root)}: unsupported skill metadata field {key!r}")

    name = metadata.get("name", "")
    if len(name) > 64 or not SKILL_NAME_RE.fullmatch(name):
        errors.append(f"{_relative(path, root)}: invalid Agent Skill name {name!r}")
    if name != path.parent.name:
        errors.append(f"{_relative(path, root)}: skill name must match its directory")
    description = metadata.get("description", "")
    if not description or PLACEHOLDER_RE.search(description):
        errors.append(f"{_relative(path, root)}: skill description is empty or placeholder text")


def _validate_links(path: Path, text: str, root: Path, errors: list[str]) -> None:
    for raw_target in LINK_RE.findall(text):
        target = raw_target.strip().split(maxsplit=1)[0].strip("<>\"")
        if not target or target.startswith(("#", "http://", "https://", "mailto:")):
            continue
        target = unquote(target).split("#", 1)[0].split("?", 1)[0]
        if not target:
            continue
        resolved = (path.parent / target).resolve()
        try:
            resolved.relative_to(root.resolve())
        except ValueError:
            errors.append(f"{_relative(path, root)}: relative link escapes repository: {target}")
            continue
        if not resolved.exists():
            errors.append(f"{_relative(path, root)}: relative link does not exist: {target}")


def validate_repository(
    root: Path,
    *,
    check_stale: bool = False,
    stale_after_days: int = 180,
    today: dt.date | None = None,
) -> list[str]:
    """Return validation errors for an agent-context repository root."""
    root = root.resolve()
    today = today or dt.date.today()
    errors: list[str] = []

    for relative in REQUIRED_FILES:
        if not (root / relative).is_file():
            errors.append(f"{relative}: required file is missing")
    for relative in REQUIRED_DIRECTORIES:
        if not (root / relative).is_dir():
            errors.append(f"{relative}: required directory is missing")

    agents_path = root / "AGENTS.md"
    agents_text = _read(agents_path, root, errors) if agents_path.is_file() else None
    if agents_text is not None and len(agents_text.splitlines()) > 120:
        errors.append("AGENTS.md: entry point exceeds 120 lines")

    memory_path = root / ".agents/memory/MEMORY.md"
    memory_text = _read(memory_path, root, errors) if memory_path.is_file() else None
    if memory_text is not None:
        if len(memory_text.splitlines()) > 200:
            errors.append(".agents/memory/MEMORY.md: index exceeds 200 lines")
        if len(memory_text.encode("utf-8")) > 25 * 1024:
            errors.append(".agents/memory/MEMORY.md: index exceeds 25 KB")

    for shim in ("CLAUDE.md", "GEMINI.md"):
        shim_path = root / shim
        if shim_path.is_file():
            content = _read(shim_path, root, errors)
            if content is not None and content != "@AGENTS.md\n":
                errors.append(f"{shim}: must contain only '@AGENTS.md'")

    documents: list[tuple[Path, str]] = []
    if agents_text is not None:
        documents.append((agents_path, agents_text))

    agents_dir = root / ".agents"
    if agents_dir.is_dir():
        for path in sorted(agents_dir.rglob("*.md")):
            text = _read(path, root, errors)
            if text is None:
                continue
            documents.append((path, text))
            if path.name == "SKILL.md" and "skills" in path.parts:
                _validate_skill(path, text, root, errors)
            else:
                _validate_record(
                    path,
                    text,
                    root,
                    errors,
                    check_stale=check_stale,
                    stale_after_days=stale_after_days,
                    today=today,
                )

    for path, text in documents:
        _validate_links(path, text, root, errors)

    return errors


def _build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path(__file__).resolve().parents[1])
    parser.add_argument("--check-stale", action="store_true")
    parser.add_argument("--stale-after-days", type=int, default=180)
    parser.add_argument("--today", type=dt.date.fromisoformat)
    parser.add_argument("--github", action="store_true", help="emit GitHub annotation syntax")
    return parser


def main(argv: list[str] | None = None) -> int:
    args = _build_parser().parse_args(argv)
    if args.stale_after_days < 1:
        print("--stale-after-days must be positive", file=sys.stderr)
        return 2

    errors = validate_repository(
        args.root,
        check_stale=args.check_stale,
        stale_after_days=args.stale_after_days,
        today=args.today,
    )
    if errors:
        for error in errors:
            prefix = "::error::" if args.github else "ERROR: "
            print(f"{prefix}{error}")
        print(f"Agent context validation failed with {len(errors)} error(s).")
        return 1

    stale = " including freshness" if args.check_stale else ""
    print(f"Agent context validation passed{stale}.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
