---
kind: memory
status: active
date: 2026-08-29
last-verified: 2026-08-29
agent: codex
evidence: Makefile, .pre-commit-config.yaml, .github/workflows/ci.yml
---

# Verification commands

The Makefile and CI define the supported verification surface.

| Command | Purpose |
| --- | --- |
| `make agent-context-check` | Unit-test and run the repository-context validator. |
| `make lint` | Run golangci-lint. |
| `make vet` | Run `go vet` across all packages. |
| `make test-unit` | Run unit tests with race detection and coverage. |
| `make test-integration` | Run integration-tagged Go tests. |
| `make test-docker` | Run Docker-tagged tests. |
| `make test-coverage` | Generate the HTML coverage report. |
| `make build-simple` | Build the server binary locally. |
| `make check` | Run the normal local verification suite. |
| `make pre-commit-run` | Execute every configured pre-commit hook. |

For documentation-only changes, the context validator and relevant formatting or
link checks are sufficient. For Go behavior changes, run `make check` at minimum;
add integration, Docker, build, or targeted tests according to the affected path.
