BINARY_NAME=telegram-media-server
BUILD_DIR=build
INSTALL_DIR=/usr/local/bin
CONFIG_DIR=/etc/telegram-media-server
SERVICE_DIR=/usr/lib/systemd/system
LOCALES_SRC=locales
LOCALES_DEST=/usr/local/share/telegram-media-server/locales

# Dependencies
DEPENDENCIES=yt-dlp aria2 ffmpeg
DEPENDENCY_BINARIES=yt-dlp aria2c ffmpeg
BUILD_DEPENDENCIES=go
OPTIONAL_DEPENDENCIES=minidlna prowlarr
OPTIONAL_SERVICES=minidlna.service prowlarr.service

# Version information
VERSION=$(shell git describe --tags --always --dirty)
BUILD_TIME=$(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS=-ldflags "-X main.Version=${VERSION} -X main.BuildTime=${BUILD_TIME}"

# Last log lines: make logs LOG_LINES=50
LOG_LINES ?= 100
SYSTEMD_UNIT ?= telegram-media-server
COMPOSE_SERVICE ?= telegram-media-server
REMOTE_GOOS ?= linux
REMOTE_GOARCH ?= amd64
REMOTE_BINARY := $(BUILD_DIR)/$(BINARY_NAME)-$(REMOTE_GOOS)-$(REMOTE_GOARCH)
REMOTE_SSH ?= telegram-server
REMOTE_BUILD_DIR ?= /tmp/telegram-media-server-build
ANSIBLE_INVENTORY ?= ops/ansible/inventory.ini
ANSIBLE_VAULT_ARGS ?=

# Dependency checks
.PHONY: check-deps
check-deps:
	@$(MAKE) check-build-deps
	@$(MAKE) check-runtime-deps
	@$(MAKE) check-optional-deps

.PHONY: check-build-deps
check-build-deps:
	@echo "Checking build dependencies..."
	@for dep in $(BUILD_DEPENDENCIES); do \
		if ! command -v $$dep >/dev/null 2>&1; then \
			echo "Error: Build dependency $$dep is not installed. Please install it."; \
			exit 1; \
		fi; \
	done
	@echo "Build dependencies: OK"

.PHONY: check-runtime-deps
check-runtime-deps:
	@echo "Checking runtime dependencies..."
	@i=0; \
	for dep in $(DEPENDENCIES); do \
		binary=$$(echo $(DEPENDENCY_BINARIES) | cut -d' ' -f$$((i+1))); \
		if ! command -v $$binary >/dev/null 2>&1; then \
			echo "Error: $$dep is not installed. Please install it."; \
			exit 1; \
		fi; \
		i=$$((i+1)); \
	done
	@echo "Runtime dependencies: OK"

.PHONY: check-optional-deps
check-optional-deps:
	@echo "Checking optional dependencies..."
	@if ! command -v systemctl >/dev/null 2>&1; then \
		echo "ℹ systemctl not available (not a systemd system)"; \
		for service in $(OPTIONAL_SERVICES); do \
			echo "ℹ Optional dependency $$service - status unknown"; \
		done; \
	else \
		for service in $(OPTIONAL_SERVICES); do \
			if systemctl is-active --quiet $$service 2>/dev/null; then \
				echo "✓ Optional dependency $$service is running"; \
			elif systemctl is-enabled --quiet $$service 2>/dev/null; then \
				echo "⚠ Optional dependency $$service is installed but not running"; \
			elif systemctl list-unit-files --type=service 2>/dev/null | grep -q "^$$service"; then \
				echo "⚠ Optional dependency $$service is installed but disabled"; \
			else \
				echo "ℹ Optional dependency $$service is not installed"; \
			fi; \
		done; \
	fi

# Core build commands
.PHONY: build
build: check-deps
	@echo "Building $(BINARY_NAME) version $(VERSION)..."
	@mkdir -p $(BUILD_DIR)
	go build $(LDFLAGS) -trimpath -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/telegram-media-server

.PHONY: build-simple
build-simple:
	@echo "Building $(BINARY_NAME) for CI..."
	go build $(LDFLAGS) -o $(BINARY_NAME) ./cmd/telegram-media-server

.PHONY: build-remote
build-remote:
	@echo "Building $(BINARY_NAME) for $(REMOTE_GOOS)/$(REMOTE_GOARCH) on $(REMOTE_SSH) with CGO..."
	@mkdir -p $(BUILD_DIR)
	rsync -az --delete \
		--exclude .git \
		--exclude .env \
		--exclude build \
		--exclude ops/ansible/inventory.ini \
		--exclude ops/ansible/group_vars/telegram_server.vault.yml \
		./ $(REMOTE_SSH):$(REMOTE_BUILD_DIR)/
	ssh $(REMOTE_SSH) 'cd $(REMOTE_BUILD_DIR) && mkdir -p $(BUILD_DIR) && GOOS=$(REMOTE_GOOS) GOARCH=$(REMOTE_GOARCH) CGO_ENABLED=1 go build -ldflags "-X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME)" -trimpath -o $(REMOTE_BINARY) ./cmd/telegram-media-server'
	scp $(REMOTE_SSH):$(REMOTE_BUILD_DIR)/$(REMOTE_BINARY) $(REMOTE_BINARY)

.PHONY: clean
clean:
	@echo "Cleaning build artifacts..."
	rm -rf $(BUILD_DIR) $(BINARY_NAME)
	go clean -cache -testcache

# Remote installation/deploy via Ansible (Arch Linux target).
.PHONY: install
install: build-remote
	@if ! command -v ansible-playbook >/dev/null 2>&1; then \
		echo "Error: ansible-playbook is required. Install Ansible first."; \
		exit 1; \
	fi
	@if [ ! -f "$(ANSIBLE_INVENTORY)" ]; then \
		echo "Error: $(ANSIBLE_INVENTORY) not found. Copy ops/ansible/inventory.ini.example first."; \
		exit 1; \
	fi
	ansible-playbook -i $(ANSIBLE_INVENTORY) $(ANSIBLE_VAULT_ARGS) ops/ansible/site.yml

.PHONY: deploy
deploy: build-remote
	@if ! command -v ansible-playbook >/dev/null 2>&1; then \
		echo "Error: ansible-playbook is required. Install Ansible first."; \
		exit 1; \
	fi
	@if [ ! -f "$(ANSIBLE_INVENTORY)" ]; then \
		echo "Error: $(ANSIBLE_INVENTORY) not found. Copy ops/ansible/inventory.ini.example first."; \
		exit 1; \
	fi
	ansible-playbook -i $(ANSIBLE_INVENTORY) $(ANSIBLE_VAULT_ARGS) ops/ansible/deploy.yml

.PHONY: ytdlp-cookies-login
ytdlp-cookies-login:
	REMOTE_SSH=$(REMOTE_SSH) ./scripts/ytdlp-cookies-login.sh

.PHONY: ansible-check
ansible-check:
	@if ! command -v ansible-playbook >/dev/null 2>&1; then \
		echo "Error: ansible-playbook is required. Install Ansible first."; \
		exit 1; \
	fi
	ansible-playbook --syntax-check -i $(ANSIBLE_INVENTORY) $(ANSIBLE_VAULT_ARGS) ops/ansible/site.yml
	ansible-playbook --syntax-check -i $(ANSIBLE_INVENTORY) $(ANSIBLE_VAULT_ARGS) ops/ansible/deploy.yml
	ansible-playbook --syntax-check -i $(ANSIBLE_INVENTORY) $(ANSIBLE_VAULT_ARGS) ops/ansible/test.yml

.PHONY: test-remote
test-remote:
	@if ! command -v ansible-playbook >/dev/null 2>&1; then \
		echo "Error: ansible-playbook is required. Install Ansible first."; \
		exit 1; \
	fi
	@if [ ! -f "$(ANSIBLE_INVENTORY)" ]; then \
		echo "Error: $(ANSIBLE_INVENTORY) not found. Copy ops/ansible/inventory.ini.example first."; \
		exit 1; \
	fi
	ansible-playbook -i $(ANSIBLE_INVENTORY) $(ANSIBLE_VAULT_ARGS) ops/ansible/test.yml

.PHONY: uninstall
uninstall:
	@echo "Uninstalling $(BINARY_NAME)..."
	systemctl stop telegram-media-server
	systemctl disable telegram-media-server
	rm -f $(INSTALL_DIR)/$(BINARY_NAME)
	rm -f $(CONFIG_DIR)/.env.example
	rm -f $(SERVICE_DIR)/telegram-media-server.service
	rm -rf $(LOCALES_DEST)
	@echo "Uninstallation complete."

# Code quality
.PHONY: format
format:
	@echo "Running code formatter..."
	golines --max-len=140 -w .
	go run golang.org/x/tools/cmd/goimports@latest -w .
	go mod tidy

GOLANGCI_LINT_VERSION_FILE := .golangci-lint-version
GOLANGCI_LINT_SEMVER := $(shell tr -d '\n\r ' < $(GOLANGCI_LINT_VERSION_FILE) 2>/dev/null | sed 's/^v//')
GOLANGCI_LINT_VERSION ?= v$(GOLANGCI_LINT_SEMVER)

.PHONY: update-golangci-lint-version
update-golangci-lint-version:
	@python3 -c 'import json, urllib.request; \
r = json.load(urllib.request.urlopen("https://api.github.com/repos/golangci/golangci-lint/releases/latest")); \
tag = r["tag_name"]; \
open(".golangci-lint-version", "w").write(tag[1:] + "\n" if tag.startswith("v") else tag + "\n")'
	@echo "Wrote golangci-lint $$(tr -d '\n\r ' < $(GOLANGCI_LINT_VERSION_FILE)) to $(GOLANGCI_LINT_VERSION_FILE)"

.PHONY: lint
lint:
	@echo "Running linter ($(GOLANGCI_LINT_VERSION))..."
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run

.PHONY: vet
vet:
	@echo "Running go vet..."
	go vet ./...

.PHONY: security-check
security-check:
	@echo "Running security checks..."
	@if command -v gosec >/dev/null 2>&1; then \
		gosec -fmt=json -out=gosec-report.json ./...; \
	else \
		echo "gosec not found, installing..."; \
		go install github.com/cosmos/gosec/v2/cmd/gosec@latest && \
		$(shell go env GOPATH)/bin/gosec -fmt=json -out=gosec-report.json ./...; \
	fi

# Testing
.PHONY: test
test:
	@echo "Running all tests..."
	go test -v ./...

.PHONY: test-unit
test-unit:
	@echo "Running unit tests (fast, no external dependencies)..."
	go test -v -short ./...

.PHONY: test-integration
test-integration:
	@echo "Running integration tests (without external tools)..."
	go test -v -run Integration ./internal/handlers/auth
	go test -v -run Integration ./internal/handlers/movies
	go test -v -run Integration ./internal/handlers/session
	go test -v -run Integration ./internal/filemanager
	go test -v -run "TestValidateContentIntegration" ./internal/downloader/torrent
	go test -tags=integration -v ./internal/api/ -run TestAPI_AddDownload_201TorrentBase64

.PHONY: test-docker
test-docker: docker-test-build
	@echo "Running tests that require external tools (yt-dlp, aria2, ffmpeg)..."
	docker run --rm \
		-v $${GITHUB_WORKSPACE:-$(PWD)}:/workspace \
		-w /workspace \
		telegram-media-server:test \
		go test -v ./internal/downloader/torrent ./internal/downloader/video ./internal/filemanager -run "Integration|TestTorrentDownload|TestVideo.*Integration|.*_Docker"

.PHONY: test-coverage
test-coverage:
	@echo "Running tests with coverage..."
	go test -v -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	go tool cover -func=coverage.out

# Docker commands
.PHONY: run
run:
	@echo "Running with Docker Compose..."
	docker-compose up -d prowlarr
	docker-compose up --build telegram-media-server

# Run locally on the host (macOS/Linux, no Docker). Requires .env with BOT_TOKEN, MOVIE_PATH, etc.
# LANG_PATH defaults to ./locales so locale files load from project root; override in .env if needed.
.PHONY: run-local
run-local:
	@echo "Running locally (no Docker)..."
	@$(MAKE) check-runtime-deps
	@if [ ! -f .env ]; then \
		echo "⚠️  .env not found. Copy .env.example to .env and set BOT_TOKEN, MOVIE_PATH, and other options."; \
		exit 1; \
	fi
	@( set -a; . ./.env; [ -z "$$LANG_PATH" ] && export LANG_PATH=./locales; [ -z "$$YTDLP_PATH" ] && command -v yt-dlp >/dev/null 2>&1 && export YTDLP_PATH=$$(command -v yt-dlp); set +a; go run $(LDFLAGS) ./cmd/telegram-media-server )

.PHONY: stop
stop:
	@echo "Stopping Docker Compose services..."
	docker-compose down

.PHONY: status
status:
	@echo "Checking service status..."
	@if systemctl is-active --quiet telegram-media-server; then \
		echo "✓ telegram-media-server is running"; \
	else \
		echo "✗ telegram-media-server is not running"; \
	fi

.PHONY: restart
restart:
	@echo "Restarting telegram-media-server..."
	systemctl restart telegram-media-server

# Recent service logs: systemd journal if available, otherwise Docker Compose (see LOG_LINES).
.PHONY: logs
logs:
	@if command -v journalctl >/dev/null 2>&1; then \
		journalctl -u $(SYSTEMD_UNIT) -n $(LOG_LINES) --no-pager; \
	elif docker compose version >/dev/null 2>&1; then \
		docker compose logs --tail=$(LOG_LINES) $(COMPOSE_SERVICE); \
	elif command -v docker-compose >/dev/null 2>&1; then \
		docker-compose logs --tail=$(LOG_LINES) $(COMPOSE_SERVICE); \
	else \
		echo "Neither journalctl nor docker compose found. On a host install: use journalctl after system install,"; \
		echo "or Docker Compose from the project directory. For 'make run-local', logs go to that terminal."; \
		exit 1; \
	fi

# Docker test commands
.PHONY: docker-test-build
docker-test-build:
	@echo "Building Docker test image..."
	docker build -f Dockerfile.test -t telegram-media-server:test .


# Utility commands
.PHONY: agent-context-test
agent-context-test:
	@echo "Running agent context validator tests..."
	python3 -m unittest scripts/test_validate_agent_context.py

.PHONY: agent-context-check
agent-context-check: agent-context-test
	@echo "Validating agent-maintained repository context..."
	python3 scripts/validate_agent_context.py

.PHONY: check
check: agent-context-check lint vet test-unit
	@echo "All checks passed!"

.PHONY: pre-commit
pre-commit: format check
	@echo "Pre-commit checks completed successfully!"

.PHONY: pre-commit-install
pre-commit-install:
	@echo "Installing pre-commit hooks..."
	@if command -v pre-commit >/dev/null 2>&1; then \
		pre-commit install; \
		echo "✅ Pre-commit hooks installed successfully!"; \
	else \
		echo "⚠️  pre-commit not found. Installing..."; \
		if command -v brew >/dev/null 2>&1; then \
			echo "Installing via Homebrew..."; \
			brew install pre-commit; \
		elif command -v pip3 >/dev/null 2>&1; then \
			echo "Installing via pip3..."; \
			pip3 install pre-commit; \
		elif command -v pip >/dev/null 2>&1; then \
			echo "Installing via pip..."; \
			pip install pre-commit; \
		else \
			echo "❌ No package manager found. Please install pre-commit manually."; \
			exit 1; \
		fi; \
		pre-commit install; \
		echo "✅ Pre-commit installed and hooks configured!"; \
	fi

.PHONY: pre-commit-run
pre-commit-run:
	@echo "Running pre-commit on all files..."
	@if command -v pre-commit >/dev/null 2>&1; then \
		pre-commit run --all-files; \
	else \
		echo "❌ pre-commit not installed. Run 'make pre-commit-install' first."; \
		exit 1; \
	fi

.PHONY: pre-commit-update
pre-commit-update:
	@echo "Updating pre-commit hooks..."
	@if command -v pre-commit >/dev/null 2>&1; then \
		pre-commit autoupdate; \
		echo "✅ Pre-commit hooks updated!"; \
	else \
		echo "❌ pre-commit not installed. Run 'make pre-commit-install' first."; \
		exit 1; \
	fi

.PHONY: env-update
env-update:
	@echo "Merging new parameters from .env.example into .env..."
	@bash scripts/merge-env.sh $(CONFIG_DIR)/.env $(CONFIG_DIR)/.env.example

.PHONY: env-update-local
env-update-local:
	@echo "Merging new parameters from .env.example into .env (local)..."
	@bash scripts/merge-env.sh .env .env.example

.PHONY: help
help:
	@echo "Available targets:"
	@echo ""
	@echo "Dependencies:"
	@echo "  check-deps     - Check all dependencies (build + runtime + optional)"
	@echo "  check-build-deps - Check build dependencies only"
	@echo "  check-runtime-deps - Check runtime dependencies only"
	@echo "  check-optional-deps - Check optional dependencies only"
	@echo ""
	@echo "Build:"
	@echo "  build          - Build the application (with dependency check)"
	@echo "  build-simple   - Build for CI (no dependencies check)"
	@echo "  build-remote   - Build linux/amd64 binary for Ansible deploy"
	@echo "  install        - Build and install remote stack with Ansible"
	@echo "  deploy         - Build and deploy only the TMS binary with Ansible"
	@echo "  ytdlp-cookies-login - Renew managed YouTube cookies through an SSH-only Chromium session"
	@echo "  ansible-check  - Run Ansible syntax checks"
	@echo "  test-remote    - Run post-install Ansible smoke and scenario tests"
	@echo "  uninstall      - Uninstall system service"
	@echo "  clean          - Clean build artifacts"
	@echo ""
	@echo "Code Quality:"
	@echo "  format         - Format code and fix issues"
	@echo "  lint           - Run linter"
	@echo "  vet            - Run go vet"
	@echo ""
	@echo "Testing:"
	@echo "  test           - Run all tests"
	@echo "  test-unit      - Run unit tests (fast, no external dependencies)"
	@echo "  test-integration - Run integration tests (without external tools)"
	@echo "  test-docker    - Run tests requiring external tools (yt-dlp, aria2, ffmpeg)"
	@echo "  test-coverage  - Run tests with coverage report"
	@echo ""
	@echo "Service Management:"
	@echo "  status         - Check service status"
	@echo "  restart        - Restart service"
	@echo "  logs           - Last LOG_LINES log lines (journalctl or docker compose; default LOG_LINES=100)"
	@echo ""
	@echo "Docker:"
	@echo "  run            - Run with Docker Compose"
	@echo "  run-local      - Run on host (macOS/Linux, no Docker). Requires .env and runtime deps (aria2, yt-dlp, ffmpeg)."
	@echo "  stop           - Stop Docker Compose services"
	@echo ""
	@echo "Configuration:"
	@echo "  env-update     - Merge new .env.example params into system .env"
	@echo "  env-update-local - Merge new .env.example params into local .env"
	@echo ""
	@echo "Utility:"
	@echo "  agent-context-test - Run agent context validator unit tests"
	@echo "  agent-context-check - Test and validate agent-maintained repository context"
	@echo "  check          - Run all checks (agent context + lint + vet + test-unit)"
	@echo "  pre-commit     - Run pre-commit checks (format + check)"
	@echo "  pre-commit-install - Install pre-commit hooks"
	@echo "  pre-commit-run - Run pre-commit on all files"
	@echo "  pre-commit-update - Update pre-commit hooks"
	@echo "  help           - Show this help"
