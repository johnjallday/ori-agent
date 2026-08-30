.PHONY: help build run test test-unit test-unit-verbose test-integration test-e2e test-all test-coverage test-watch test-js test-clean-test-artifacts test-prune-test-cache test-run-test-command test-list-unit-packages test-test-maintenance lint lint-fix lint-new lint-js lint-js-fix fmt fmt-js check-js check-wails-modes vet clean clean-test-artifacts prune-test-cache cache-report server menubar run-menubar deps docker-build docker-run check-env merge-dependabot readme-audit readme-capture readme-propose readme-check readme-accept herdr-devflow test-herdr-devflow test-herdr-devflow-cross

# Default target
.DEFAULT_GOAL := help

# Variables
BINARY_NAME=ori-agent
MENUBAR_BINARY_NAME=ori-menubar
BUILD_DIR=bin
COVERAGE_DIR=coverage
GO=go
GOTEST=$(GO) test
GOBUILD=$(GO) build
GORUN=$(GO) run
GOVET=$(GO) vet
GOFMT=$(GO) fmt
TEST_RUNNER=./scripts/run-test-command.sh
PORT?=8765
MONOREPO_ROOT?=$(abspath $(CURDIR)/..)

# Version information
VERSION?=$(shell cat VERSION 2>/dev/null || echo "dev")
GIT_COMMIT?=$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE?=$(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
# Official builds bake in a verified Google identity OAuth client so Connect
# works with no per-operator setup. Provide these ONLY at release time via the
# environment (never commit them); they are empty for from-source/dev builds,
# which stay "unconfigured" until an operator sets the ORI_GOOGLE_CONNECTION_*
# env vars. Self-hosters can always override the embedded client via those env
# vars at runtime.
ORI_GOOGLE_EMBEDDED_CLIENT_ID?=
ORI_GOOGLE_EMBEDDED_CLIENT_SECRET?=
LDFLAGS=-X 'github.com/johnjallday/ori-agent/internal/version.Version=$(VERSION)' \
        -X 'github.com/johnjallday/ori-agent/internal/version.GitCommit=$(GIT_COMMIT)' \
        -X 'github.com/johnjallday/ori-agent/internal/version.BuildDate=$(BUILD_DATE)' \
        -X 'github.com/johnjallday/ori-agent/internal/connections.embeddedClientID=$(ORI_GOOGLE_EMBEDDED_CLIENT_ID)' \
        -X 'github.com/johnjallday/ori-agent/internal/connections.embeddedClientSecret=$(ORI_GOOGLE_EMBEDDED_CLIENT_SECRET)'

# Colors for output
RED=\033[0;31m
GREEN=\033[0;32m
YELLOW=\033[1;33m
BLUE=\033[0;34m
NC=\033[0m # No Color

help: ## Show this help message
	@echo "$(BLUE)Ori Agent - Development Commands$(NC)"
	@echo ""
	@grep -E '^[a-zA-Z_-]+:.*## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*## "}; {printf "  $(GREEN)%-20s$(NC) %s\n", $$1, $$2}'
	@echo ""

## README maintenance targets

readme-audit: ## Report README contract state without writing files
	@node scripts/readme/audit.mjs

readme-capture: ## Capture staged README screenshots into ignored run artifacts
	@bash scripts/readme-refresh.sh capture

readme-propose: ## Audit and stage a README candidate for a successful capture run
	@test -n "$(RUN_ID)" || (echo "RUN_ID is required" >&2; exit 2)
	@node scripts/readme/propose.mjs --run-id "$(RUN_ID)"

readme-check: ## Validate README screenshot manifest, references, and accepted assets
	@node scripts/readme/manifest.mjs

readme-accept: ## Apply an approved staged README refresh (available after acceptance setup)
	@test -n "$(RUN_ID)" || (echo "RUN_ID is required" >&2; exit 2)
	@test "$(APPROVE)" = "1" || (echo "Checkpoint 1 approval is required: rerun with APPROVE=1" >&2; exit 2)
	@node scripts/readme/accept.mjs --run-id "$(RUN_ID)" --approve

## Build targets

deps: ## Install dependencies
	@echo "$(BLUE)Installing dependencies...$(NC)"
	$(GO) mod download
	$(GO) mod tidy

merge-dependabot: ## Merge Dependabot PRs with green checks (requires gh)
	@./scripts/merge-dependabot.sh

build: ## Build the server binary
	@echo "$(BLUE)Building server...$(NC)"
	@echo "$(BLUE)Version: $(VERSION) | Commit: $(GIT_COMMIT) | Date: $(BUILD_DATE)$(NC)"
	$(GOBUILD) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/server
	@echo "$(GREEN)✓ Build complete: $(BUILD_DIR)/$(BINARY_NAME)$(NC)"

menubar: ## Build the menu bar app
	@echo "$(BLUE)Building menu bar app...$(NC)"
	@echo "$(BLUE)Version: $(VERSION) | Commit: $(GIT_COMMIT) | Date: $(BUILD_DATE)$(NC)"
	$(GOBUILD) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(MENUBAR_BINARY_NAME) ./cmd/menubar
	@echo "$(GREEN)✓ Build complete: $(BUILD_DIR)/$(MENUBAR_BINARY_NAME)$(NC)"

herdr-devflow: ## Build the Ori-to-Herdr devflow helper
	@echo "$(BLUE)Building Herdr devflow helper...$(NC)"
	$(GOBUILD) -o $(BUILD_DIR)/herdr-devflow ./tools/herdr-devflow/cmd/herdr-devflow
	$(GOBUILD) -o $(BUILD_DIR)/herdr-wake ./tools/herdr-devflow/cmd/herdr-wake
	@echo "$(GREEN)✓ Build complete: $(BUILD_DIR)/herdr-devflow$(NC)"


icons: ## Generate menubar and app icons from SVG
	@echo "$(BLUE)Generating icons...$(NC)"
	@./scripts/generate-menubar-icons.sh
	@./scripts/generate-app-icon.sh
	@echo "$(GREEN)✓ Icons generated$(NC)"

all: deps build menubar ## Build everything (server + menubar)
	@echo "$(GREEN)✓ Build complete$(NC)"

build-all: all ## Alias for 'all' - Build everything

clean: ## Clean build artifacts
	@echo "$(BLUE)Cleaning build artifacts...$(NC)"
	rm -rf $(BUILD_DIR)
	rm -rf $(COVERAGE_DIR)
	rm -rf build-dmg
	@echo "$(GREEN)✓ Clean complete$(NC)"

clean-test-artifacts: ## Delete Ori test artifacts from the system temp directory
	@./scripts/clean-test-artifacts.sh --delete

prune-test-cache: ## Prune stale Ori artifacts and an oversized Go build cache
	@./scripts/prune-test-cache.sh

cache-report: ## Preview automatic test artifact and Go cache pruning
	@./scripts/prune-test-cache.sh --dry-run

clean-state: ## Show how to reset app state (points to the canonical selective reset)
	@echo "$(YELLOW)clean-state no longer deletes files itself.$(NC)"
	@echo ""
	@echo "Ori resolves all state to one canonical data directory (ORI_DATA_DIR,"
	@echo "or the platform default under your home directory - see"
	@echo "config.DefaultDataDir()), not this Makefile's working directory. A"
	@echo "second, cwd-relative deletion list here would drift from the real one"
	@echo "and could silently target the wrong path."
	@echo ""
	@echo "To reset app state, use the canonical selective reset instead:"
	@echo "  1. In the app: Settings -> Reset, choose what to reset, confirm."
	@echo "  2. Scripted, with the server running on port 8765 (adjust for PORT):"
	@echo "     curl -X POST http://localhost:8765/api/reset \\"
	@echo "       -H 'X-Requested-With: XMLHttpRequest' -H 'Content-Type: application/json' \\"
	@echo "       -d '{\"settings\":true,\"agents\":true,\"sessions\":true,\"onboarding\":true,\"confirmation\":\"RESET\"}'"

reset: clean-state ## Alias for clean-state

## Run targets

check-env: ## Check required environment variables
	@if [ -z "$$OPENAI_API_KEY" ] && [ -z "$$ANTHROPIC_API_KEY" ]; then \
		echo "$(RED)ERROR: No API key set. Set OPENAI_API_KEY or ANTHROPIC_API_KEY$(NC)"; \
		exit 1; \
	fi

run: check-env build ## Build and run the server
	@echo "$(BLUE)Starting server on port $(PORT)...$(NC)"
	PORT=$(PORT) ./$(BUILD_DIR)/$(BINARY_NAME)

run-dev: check-env ## Run server in development mode (no build)
	@echo "$(BLUE)Starting server in dev mode on port $(PORT)...$(NC)"
	PORT=$(PORT) $(GORUN) ./cmd/server

run-menubar: menubar ## Build and run the menu bar app
	@echo "$(BLUE)Starting menu bar app...$(NC)"
	./$(BUILD_DIR)/$(MENUBAR_BINARY_NAME)

## Test targets

test: ## Run all tests (unit + integration; excludes node_modules)
	@echo "$(BLUE)Running all tests...$(NC)"
	$(TEST_RUNNER) $(GOTEST) -v $$(go list ./... | grep -v '/node_modules/')
	@echo "$(GREEN)✓ All tests passed$(NC)"

test-unit: ## Run unit tests only (concise; see test-unit-verbose for -v)
	@echo "$(BLUE)Running unit tests...$(NC)"
	$(TEST_RUNNER) $(GOTEST) -short $$(./scripts/list-unit-packages.sh)
	@echo "$(GREEN)✓ Unit tests passed$(NC)"

test-unit-verbose: ## Run unit tests with -v output, for focused diagnosis
	@echo "$(BLUE)Running unit tests (verbose)...$(NC)"
	$(TEST_RUNNER) $(GOTEST) -v -short $$(./scripts/list-unit-packages.sh)
	@echo "$(GREEN)✓ Unit tests passed$(NC)"

test-herdr-devflow-cross: ## Cross-compile the local Herdr helper for supported targets
	@mkdir -p $(BUILD_DIR)
	GOOS=darwin GOARCH=arm64 $(GOBUILD) -o $(BUILD_DIR)/herdr-devflow-darwin-arm64 ./tools/herdr-devflow/cmd/herdr-devflow
	GOOS=darwin GOARCH=arm64 $(GOBUILD) -o $(BUILD_DIR)/herdr-wake-darwin-arm64 ./tools/herdr-devflow/cmd/herdr-wake
	GOOS=linux GOARCH=amd64 $(GOBUILD) -o $(BUILD_DIR)/herdr-devflow-linux-amd64 ./tools/herdr-devflow/cmd/herdr-devflow
	GOOS=linux GOARCH=amd64 $(GOBUILD) -o $(BUILD_DIR)/herdr-wake-linux-amd64 ./tools/herdr-devflow/cmd/herdr-wake
	GOOS=windows GOARCH=amd64 $(GOBUILD) -o $(BUILD_DIR)/herdr-devflow-windows-amd64.exe ./tools/herdr-devflow/cmd/herdr-devflow
	GOOS=windows GOARCH=amd64 $(GOBUILD) -o $(BUILD_DIR)/herdr-wake-windows-amd64.exe ./tools/herdr-devflow/cmd/herdr-wake

test-herdr-devflow: test-herdr-devflow-cross ## Run focused Ori-to-Herdr bridge tests
	$(TEST_RUNNER) $(GOTEST) ./tools/herdr-devflow/...
	@$(TEST_RUNNER) bash scripts/herdr-devflow.test.sh
	@$(TEST_RUNNER) zsh scripts/wt-herd.test.sh
	@$(TEST_RUNNER) zsh scripts/wt-config.test.sh
	@$(TEST_RUNNER) bash scripts/devops-cli.test.sh
	@$(TEST_RUNNER) zsh scripts/check-backlog-docs.sh

test-integration: ## Run integration tests (needs OPENAI_API_KEY; sets the provider opt-in)
	@echo "$(BLUE)Running integration tests...$(NC)"
	@if [ -z "$$OPENAI_API_KEY" ]; then \
		echo "$(YELLOW)Skipping integration tests (OPENAI_API_KEY not set)$(NC)"; \
	else \
		ORI_RUN_PROVIDER_INTEGRATION=1 $(TEST_RUNNER) $(GOTEST) -v -run Integration ./...; \
		echo "$(GREEN)✓ Integration tests passed$(NC)"; \
	fi

test-e2e: build ## Run end-to-end tests (needs OPENAI_API_KEY; sets the provider opt-in)
	@echo "$(BLUE)Running E2E tests...$(NC)"
	@if [ -z "$$OPENAI_API_KEY" ]; then \
		echo "$(YELLOW)Skipping E2E tests (OPENAI_API_KEY not set)$(NC)"; \
	else \
		ORI_RUN_PROVIDER_INTEGRATION=1 $(TEST_RUNNER) $(GOTEST) -v ./tests/e2e/...; \
		echo "$(GREEN)✓ E2E tests passed$(NC)"; \
	fi

test-coverage: ## Run tests with coverage report
	@echo "$(BLUE)Running tests with coverage...$(NC)"
	@mkdir -p $(COVERAGE_DIR)
	$(TEST_RUNNER) $(GOTEST) -v -coverprofile=$(COVERAGE_DIR)/coverage.out ./...
	$(GO) tool cover -html=$(COVERAGE_DIR)/coverage.out -o $(COVERAGE_DIR)/coverage.html
	$(GO) tool cover -func=$(COVERAGE_DIR)/coverage.out | grep total:
	@echo "$(GREEN)✓ Coverage report generated: $(COVERAGE_DIR)/coverage.html$(NC)"

test-watch: ## Run tests in watch mode (requires entr)
	@echo "$(BLUE)Watching for changes...$(NC)"
	@if ! command -v entr > /dev/null; then \
		echo "$(RED)entr not found. Install with: brew install entr$(NC)"; \
		exit 1; \
	fi
	@find . -name '*.go' | entr -c make test-unit

test-all: test test-e2e ## Run all tests including E2E
	@echo "$(GREEN)✓ All tests completed$(NC)"

test-user: build ## Run user workflow tests
	@echo "$(BLUE)Running user tests...$(NC)"
	@if [ -z "$$OPENAI_API_KEY" ] && [ -z "$$ANTHROPIC_API_KEY" ] && [ "$$USE_OLLAMA" != "true" ]; then \
		echo "$(YELLOW)Skipping user tests (no LLM provider configured)$(NC)"; \
		echo "$(YELLOW)Set OPENAI_API_KEY, ANTHROPIC_API_KEY, or USE_OLLAMA=true$(NC)"; \
	else \
		$(TEST_RUNNER) $(GOTEST) -p 1 -v -timeout 5m ./tests/user/...; \
		echo "$(GREEN)✓ User tests passed$(NC)"; \
	fi

test-ollama: ## Run user tests with Ollama (local LLM)
	@echo "$(BLUE)Running tests with Ollama...$(NC)"
	@$(TEST_RUNNER) ./scripts/test-with-ollama.sh

test-cli: build ## Build and run interactive testing CLI
	@echo "$(BLUE)Building test CLI...$(NC)"
	$(GOBUILD) -o $(BUILD_DIR)/ori-test-cli ./cmd/test-cli
	@echo "$(GREEN)✓ Test CLI built$(NC)"
	@echo ""
	@echo "$(BLUE)Starting test CLI...$(NC)"
	./$(BUILD_DIR)/ori-test-cli

test-scenarios: ## Run manual scenario runner
	@echo "$(BLUE)Starting scenario runner...$(NC)"
	@cd tests/user/scenarios && go run scenario_runner.go

## Code quality targets

fmt: ## Format code
	@echo "$(BLUE)Formatting code...$(NC)"
	$(GOFMT) ./...
	@echo "$(GREEN)✓ Code formatted$(NC)"

vet: ## Run go vet
	@echo "$(BLUE)Running go vet...$(NC)"
	$(GOVET) ./...
	@echo "$(GREEN)✓ Vet passed$(NC)"

lint: ## Run the full linter (includes the pre-existing legacy baseline; see lint-new)
	@echo "$(BLUE)Running linter...$(NC)"
	@if ! command -v golangci-lint > /dev/null; then \
		echo "$(YELLOW)golangci-lint not found. Install from: https://golangci-lint.run/usage/install/$(NC)"; \
		exit 1; \
	fi
	golangci-lint run ./...
	@echo "$(GREEN)✓ Lint passed$(NC)"

lint-new: ## Run the ratcheted linter: only issues new vs origin/dev (mirrors the CI gate)
	@echo "$(BLUE)Running linter (new issues vs origin/dev only)...$(NC)"
	@if ! command -v golangci-lint > /dev/null; then \
		echo "$(YELLOW)golangci-lint not found. Install from: https://golangci-lint.run/usage/install/$(NC)"; \
		exit 1; \
	fi
	golangci-lint run --new-from-merge-base=origin/dev ./...
	@echo "$(GREEN)✓ No new lint findings$(NC)"

lint-fix: ## Auto-fix lint issues where possible (gofmt rewrites + golangci-lint --fix)
	@echo "$(BLUE)Auto-fixing Go lint issues...$(NC)"
	@if ! command -v golangci-lint > /dev/null; then \
		echo "$(YELLOW)golangci-lint not found. Install from: https://golangci-lint.run/usage/install/$(NC)"; \
		exit 1; \
	fi
	gofmt -r 'interface{} -> any' -w .
	golangci-lint run --fix ./... || true
	@echo "$(GREEN)✓ Auto-fix pass complete (review remaining unparam/modernize findings manually)$(NC)"

test-js: ## Run JS unit tests (Node 18+ built-in test runner; no npm install needed)
	@echo "$(BLUE)Running JS unit tests...$(NC)"
	$(TEST_RUNNER) node --test internal/web/static/js/modules/*.test.js
	@echo "$(GREEN)✓ JS tests passed$(NC)"

test-clean-test-artifacts: ## Test the temp artifact cleanup script in an isolated directory
	@./scripts/clean-test-artifacts.test.sh

test-prune-test-cache: ## Test automatic cache pruning in isolated fixtures
	@./scripts/prune-test-cache.test.sh

test-run-test-command: ## Test run-owned sandbox cleanup and opt-outs
	@./scripts/run-test-command.test.sh

test-list-unit-packages: ## Test the shared unit-package selection contract
	@./scripts/list-unit-packages.test.sh

test-test-maintenance: test-clean-test-artifacts test-prune-test-cache test-run-test-command test-list-unit-packages ## Test all test-maintenance helpers

lint-js: ## Run ESLint on JavaScript files (requires npm install)
	@echo "$(BLUE)Running ESLint on JavaScript...$(NC)"
	@if [ ! -d "node_modules" ]; then \
		echo "$(YELLOW)Installing npm dependencies...$(NC)"; \
		npm install; \
	fi
	npm run lint
	@echo "$(GREEN)✓ ESLint passed$(NC)"

lint-js-fix: ## Run ESLint and auto-fix issues
	@echo "$(BLUE)Running ESLint with auto-fix...$(NC)"
	@if [ ! -d "node_modules" ]; then \
		echo "$(YELLOW)Installing npm dependencies...$(NC)"; \
		npm install; \
	fi
	npm run lint:fix
	@echo "$(GREEN)✓ ESLint auto-fix complete$(NC)"

fmt-js: ## Format JavaScript files with Prettier (requires npm install)
	@echo "$(BLUE)Formatting JavaScript with Prettier...$(NC)"
	@if [ ! -d "node_modules" ]; then \
		echo "$(YELLOW)Installing npm dependencies...$(NC)"; \
		npm install; \
	fi
	npm run format
	@echo "$(GREEN)✓ Prettier formatting complete$(NC)"

check-js: ## Check JavaScript formatting without modifying files
	@echo "$(BLUE)Checking JavaScript formatting...$(NC)"
	@if [ ! -d "node_modules" ]; then \
		echo "$(YELLOW)Installing npm dependencies...$(NC)"; \
		npm install; \
	fi
	npm run format:check
	@echo "$(GREEN)✓ JavaScript formatting check passed$(NC)"

check: fmt vet test ## Run all checks (fmt, vet, test)
	@echo "$(GREEN)✓ All checks passed$(NC)"

check-cross-platform: ## Check builds for all platforms (Linux, Windows, macOS)
	@echo "$(BLUE)Checking cross-platform builds...$(NC)"
	@./scripts/check-cross-platform.sh
	@echo "$(GREEN)✓ All platforms build successfully$(NC)"

check-wails-modes: ## Verify Wails bindings stay regular files after successful and failed builds
	@./scripts/check-wails-binding-modes.sh
	@bash ./scripts/check-wails-binding-modes.test.sh
	@bash ./scripts/build-folder-picker.test.sh


## Docker targets

docker-build: ## Build Docker image
	@echo "$(BLUE)Building Docker image...$(NC)"
	docker build -t ori-agent:latest .
	@echo "$(GREEN)✓ Docker image built$(NC)"

docker-run: ## Run Docker container
	@echo "$(BLUE)Running Docker container...$(NC)"
	docker run -p $(PORT):$(PORT) \
		-e OPENAI_API_KEY=$$OPENAI_API_KEY \
		-e ANTHROPIC_API_KEY=$$ANTHROPIC_API_KEY \
		ori-agent:latest

## Development targets

dev-setup: deps build ## Initial development setup
	@echo "$(GREEN)✓ Development environment ready$(NC)"
	@echo ""
	@echo "$(BLUE)Next steps:$(NC)"
	@echo "  1. Set your API key: export OPENAI_API_KEY=your-key"
	@echo "  2. Run the server: make run"
	@echo "  3. Visit: http://localhost:8765"

install-tools: ## Install development tools (golangci-lint pinned to .golangci-lint-version)
	@echo "$(BLUE)Installing development tools...$(NC)"
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$$(cat .golangci-lint-version)
	go install golang.org/x/vuln/cmd/govulncheck@latest
	@echo "$(GREEN)✓ Tools installed$(NC)"

## Dependency management targets

deps-check: ## Check for available dependency updates
	@echo "$(BLUE)Checking for dependency updates...$(NC)"
	@go list -m -u all | grep '\[' || echo "$(GREEN)All dependencies are up to date$(NC)"

deps-check-direct: ## Check for updates (direct dependencies only)
	@echo "$(BLUE)Checking direct dependencies for updates...$(NC)"
	@go list -m -u -json all | jq -r 'select(.Update != null and .Indirect != true) | "\(.Path) \(.Version) → [\(.Update.Version)]"' 2>/dev/null || (echo "$(YELLOW)jq not found, showing all updates:$(NC)" && go list -m -u all | grep '\[')
	@echo ""
	@echo "$(YELLOW)Note: Indirect dependencies are controlled by their parent packages$(NC)"

deps-update: ## Update all direct dependencies to latest compatible versions
	@echo "$(BLUE)Updating dependencies...$(NC)"
	@echo "$(YELLOW)This will update direct dependencies to latest minor/patch versions$(NC)"
	@echo ""
	$(GO) get -u ./...
	$(GO) mod tidy
	@echo ""
	@echo "$(GREEN)✓ Dependencies updated$(NC)"
	@echo "$(YELLOW)Run 'make deps-check' to see remaining indirect dependency updates$(NC)"

deps-update-patch: ## Update dependencies (patch versions only)
	@echo "$(BLUE)Updating patch versions...$(NC)"
	$(GO) get -u=patch ./...
	$(GO) mod tidy
	@echo "$(GREEN)✓ Patch updates applied$(NC)"

deps-vuln: ## Check for known vulnerabilities
	@echo "$(BLUE)Checking for vulnerabilities...$(NC)"
	@if ! command -v govulncheck > /dev/null; then \
		echo "$(YELLOW)govulncheck not found. Installing...$(NC)"; \
		go install golang.org/x/vuln/cmd/govulncheck@latest; \
	fi
	@govulncheck ./... || (echo "$(RED)Vulnerabilities found!$(NC)" && exit 1)
	@echo "$(GREEN)✓ No vulnerabilities found$(NC)"

deps-graph: ## Show dependency graph (requires graphviz)
	@echo "$(BLUE)Generating dependency graph...$(NC)"
	@if ! command -v dot > /dev/null; then \
		echo "$(YELLOW)graphviz not found. Install with: brew install graphviz$(NC)"; \
		exit 1; \
	fi
	@go mod graph | modgraphviz | dot -Tpng -o deps-graph.png
	@echo "$(GREEN)✓ Dependency graph saved to deps-graph.png$(NC)"

deps-why: ## Explain why a dependency is needed (usage: make deps-why DEP=package-name)
	@if [ -z "$(DEP)" ]; then \
		echo "$(RED)Usage: make deps-why DEP=package-name$(NC)"; \
		exit 1; \
	fi
	@echo "$(BLUE)Checking why $(DEP) is needed...$(NC)"
	@go mod why $(DEP)

deps-tidy: ## Clean up go.mod and go.sum
	@echo "$(BLUE)Tidying dependencies...$(NC)"
	$(GO) mod tidy
	@echo "$(GREEN)✓ Dependencies tidied$(NC)"

deps-verify: ## Verify dependencies have expected content
	@echo "$(BLUE)Verifying dependencies...$(NC)"
	$(GO) mod verify
	@echo "$(GREEN)✓ Dependencies verified$(NC)"

deps-outdated: ## Show outdated dependencies in a readable format
	@echo "$(BLUE)Checking for outdated dependencies...$(NC)"
	@go list -m -u -json all | go run scripts/parse-deps.go 2>/dev/null || go list -m -u all | grep '\['

deps-security: deps-vuln ## Alias for deps-vuln (run security checks)

security: deps-vuln ## Run security vulnerability scan

deps-summary: ## Show dependency update summary
	@echo "$(BLUE)Dependency Summary$(NC)"
	@echo ""
	@DIRECT_OUTDATED=$$(go list -m -u -json all 2>/dev/null | jq -r 'select(.Update != null and .Indirect != true)' 2>/dev/null | jq -s 'length' 2>/dev/null || echo "0"); \
	INDIRECT_OUTDATED=$$(go list -m -u -json all 2>/dev/null | jq -r 'select(.Update != null and .Indirect == true)' 2>/dev/null | jq -s 'length' 2>/dev/null || echo "0"); \
	TOTAL_OUTDATED=$$(go list -m -u all 2>/dev/null | grep -c '\[' || echo "0"); \
	TOTAL_DEPS=$$(go list -m all 2>/dev/null | wc -l | tr -d ' '); \
	echo "  Total dependencies: $$TOTAL_DEPS"; \
	echo "  Dependencies with updates: $$TOTAL_OUTDATED"; \
	if [ "$$DIRECT_OUTDATED" != "0" ]; then \
		echo "    - Direct: $(YELLOW)$$DIRECT_OUTDATED$(NC) (can be updated with 'make deps-update')"; \
	else \
		echo "    - Direct: $(GREEN)0$(NC) (all up to date!)"; \
	fi; \
	if [ "$$INDIRECT_OUTDATED" != "0" ]; then \
		echo "    - Indirect: $(YELLOW)$$INDIRECT_OUTDATED$(NC) (controlled by parent packages)"; \
	else \
		echo "    - Indirect: $(GREEN)0$(NC) (all up to date!)"; \
	fi
	@echo ""
	@echo "$(BLUE)Quick actions:$(NC)"
	@echo "  make deps-check-direct  - Show direct dependency updates"
	@echo "  make deps-update        - Update direct dependencies"
	@echo "  make deps-vuln          - Check for vulnerabilities"

## Release targets

pre-release: ## Run pre-release checks (format, lint, vet, security, tests)
	@echo "$(BLUE)Running pre-release checks...$(NC)"
	@./scripts/pre-release-check.sh
	@echo "$(GREEN)✓ Pre-release checks complete$(NC)"

pre-release-full: ## Run complete pre-release checks including smoke tests
	@echo "$(BLUE)Running complete pre-release checks...$(NC)"
	@echo "This will run all checks + smoke tests (~15-20 minutes)"
	@echo ""
	@read -p "Continue? [y/N]: " -n 1 -r && echo && \
	if [[ $$REPLY =~ ^[Yy]$$ ]]; then \
		./scripts/pre-release-check.sh --full; \
	else \
		echo "$(YELLOW)Cancelled$(NC)"; \
		exit 1; \
	fi

smoke-test: ## Run installer smoke tests
	@echo "$(BLUE)Running smoke tests...$(NC)"
	@./scripts/test-all-installers.sh
	@echo "$(GREEN)✓ Smoke tests complete$(NC)"
