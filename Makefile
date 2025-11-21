.PHONY: help build run test test-unit test-integration test-e2e test-all test-coverage test-watch lint fmt vet clean plugins server menubar run-menubar deps docker-build docker-run check-env

# Default target
.DEFAULT_GOAL := help

# Variables
BINARY_NAME=ori-agent
MENUBAR_BINARY_NAME=ori-menubar
BUILD_DIR=bin
PLUGINS_DIR=plugins
COVERAGE_DIR=coverage
GO=go
GOTEST=$(GO) test
GOBUILD=$(GO) build
GORUN=$(GO) run
GOVET=$(GO) vet
GOFMT=$(GO) fmt
PORT?=8765

# Version information
VERSION?=$(shell cat VERSION 2>/dev/null || echo "dev")
GIT_COMMIT?=$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE?=$(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS=-X 'github.com/johnjallday/ori-agent/internal/version.Version=$(VERSION)' \
        -X 'github.com/johnjallday/ori-agent/internal/version.GitCommit=$(GIT_COMMIT)' \
        -X 'github.com/johnjallday/ori-agent/internal/version.BuildDate=$(BUILD_DATE)'

# Colors for output
RED=\033[0;31m
GREEN=\033[0;32m
YELLOW=\033[1;33m
BLUE=\033[0;34m
NC=\033[0m # No Color

help: ## Show this help message
	@echo "$(BLUE)Ori Agent - Development Commands$(NC)"
	@echo ""
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  $(GREEN)%-20s$(NC) %s\n", $$1, $$2}'
	@echo ""

## Build targets

deps: ## Install dependencies
	@echo "$(BLUE)Installing dependencies...$(NC)"
	$(GO) mod download
	$(GO) mod tidy

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

plugin-gen: ## Build the plugin code generator
	@echo "$(BLUE)Building plugin generator...$(NC)"
	$(GOBUILD) -o $(BUILD_DIR)/ori-plugin-gen ./cmd/ori-plugin-gen
	@echo "$(GREEN)✓ Plugin generator built: $(BUILD_DIR)/ori-plugin-gen$(NC)"

generate: plugin-gen ## Generate code for all plugins
	@echo "$(BLUE)Generating plugin code...$(NC)"
	@cd example_plugins/weather && ../../$(BUILD_DIR)/ori-plugin-gen -yaml=plugin.yaml -output=weather_generated.go
	@cd example_plugins/math && ../../$(BUILD_DIR)/ori-plugin-gen -yaml=plugin.yaml -output=math_generated.go
	@cd example_plugins/result-handler && ../../$(BUILD_DIR)/ori-plugin-gen -yaml=plugin.yaml -output=result_handler_generated.go
	@cd example_plugins/minimal && ../../$(BUILD_DIR)/ori-plugin-gen -yaml=plugin.yaml -output=minimal_generated.go
	@cd example_plugins/webapp && ../../$(BUILD_DIR)/ori-plugin-gen -yaml=plugin.yaml -output=webapp_generated.go
	@echo "$(GREEN)✓ Code generation complete$(NC)"

plugins: ## Build all plugins
	@echo "$(BLUE)Building plugins...$(NC)"
	@./scripts/build-plugins.sh || (echo "$(RED)Plugin build failed$(NC)" && exit 1)
	@echo "$(GREEN)✓ Plugins built$(NC)"

icons: ## Generate menubar and app icons from SVG
	@echo "$(BLUE)Generating icons...$(NC)"
	@./scripts/generate-menubar-icons.sh
	@./scripts/generate-app-icon.sh
	@echo "$(GREEN)✓ Icons generated$(NC)"

all: deps generate build menubar plugins ## Build everything (server + menubar + plugins with code generation)
	@echo "$(GREEN)✓ Build complete$(NC)"

build-all: all ## Alias for 'all' - Build everything

clean: ## Clean build artifacts
	@echo "$(BLUE)Cleaning build artifacts...$(NC)"
	rm -rf $(BUILD_DIR)
	rm -rf $(COVERAGE_DIR)
	rm -rf build-dmg
	rm -rf $(PLUGINS_DIR)/*/$(shell basename $(PLUGINS_DIR))
	@echo "$(GREEN)✓ Clean complete$(NC)"

clean-generated: ## Clean generated plugin code
	@echo "$(BLUE)Cleaning generated files...$(NC)"
	rm -f example_plugins/*/*.generated.go
	rm -f example_plugins/*/*_generated.go
	@echo "$(GREEN)✓ Generated files cleaned$(NC)"

clean-state: ## Delete all configuration and state files (fresh start)
	@echo "$(YELLOW)⚠️  WARNING: This will delete ALL configuration, agents, workspaces, and settings!$(NC)"
	@echo "$(YELLOW)You will need to reconfigure the app from scratch.$(NC)"
	@echo ""
	@read -p "Are you sure? [y/N]: " -n 1 -r && echo && \
	if [[ $$REPLY =~ ^[Yy]$$ ]]; then \
		echo "$(BLUE)Cleaning state files...$(NC)"; \
		rm -rf agents/; \
		rm -rf workspaces/; \
		rm -rf sessions/; \
		rm -rf uploaded_plugins/; \
		rm -f settings.json; \
		rm -f agents.json; \
		rm -f local_plugin_registry.json; \
		rm -f plugin_registry_cache.json; \
		rm -f app_state.json; \
		rm -f mcp_registry.json; \
		rm -f locations.json; \
		rm -f .feature-sessions.json; \
		echo "$(GREEN)✓ All state files cleaned - app will start fresh$(NC)"; \
	else \
		echo "$(YELLOW)Cancelled$(NC)"; \
		exit 1; \
	fi

reset: clean-state ## Alias for clean-state (fresh start)

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

test: ## Run all tests (unit + integration)
	@echo "$(BLUE)Running all tests...$(NC)"
	$(GOTEST) -v $$(go list ./... | grep -v '/tests$$')
	@echo "$(GREEN)✓ All tests passed$(NC)"

test-unit: ## Run unit tests only
	@echo "$(BLUE)Running unit tests...$(NC)"
	$(GOTEST) -v -short $$(go list ./... | grep -v '/tests$$')
	@echo "$(GREEN)✓ Unit tests passed$(NC)"

test-integration: ## Run integration tests
	@echo "$(BLUE)Running integration tests...$(NC)"
	@if [ -z "$$OPENAI_API_KEY" ]; then \
		echo "$(YELLOW)Skipping integration tests (OPENAI_API_KEY not set)$(NC)"; \
	else \
		$(GOTEST) -v -run Integration ./...; \
		echo "$(GREEN)✓ Integration tests passed$(NC)"; \
	fi

test-e2e: build plugins ## Run end-to-end tests
	@echo "$(BLUE)Running E2E tests...$(NC)"
	@if [ -z "$$OPENAI_API_KEY" ]; then \
		echo "$(YELLOW)Skipping E2E tests (OPENAI_API_KEY not set)$(NC)"; \
	else \
		$(GOTEST) -v ./tests/e2e/...; \
		echo "$(GREEN)✓ E2E tests passed$(NC)"; \
	fi

test-coverage: ## Run tests with coverage report
	@echo "$(BLUE)Running tests with coverage...$(NC)"
	@mkdir -p $(COVERAGE_DIR)
	$(GOTEST) -v -coverprofile=$(COVERAGE_DIR)/coverage.out ./...
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

test-user: build plugins ## Run user workflow tests
	@echo "$(BLUE)Running user tests...$(NC)"
	@if [ -z "$$OPENAI_API_KEY" ] && [ -z "$$ANTHROPIC_API_KEY" ] && [ "$$USE_OLLAMA" != "true" ]; then \
		echo "$(YELLOW)Skipping user tests (no LLM provider configured)$(NC)"; \
		echo "$(YELLOW)Set OPENAI_API_KEY, ANTHROPIC_API_KEY, or USE_OLLAMA=true$(NC)"; \
	else \
		$(GOTEST) -p 1 -v -timeout 5m ./tests/user/...; \
		echo "$(GREEN)✓ User tests passed$(NC)"; \
	fi

test-ollama: ## Run user tests with Ollama (local LLM)
	@echo "$(BLUE)Running tests with Ollama...$(NC)"
	@./scripts/test-with-ollama.sh

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

lint: ## Run linter (requires golangci-lint)
	@echo "$(BLUE)Running linter...$(NC)"
	@if ! command -v golangci-lint > /dev/null; then \
		echo "$(YELLOW)golangci-lint not found. Install from: https://golangci-lint.run/usage/install/$(NC)"; \
		exit 1; \
	fi
	golangci-lint run ./...
	@echo "$(GREEN)✓ Lint passed$(NC)"

check: fmt vet test ## Run all checks (fmt, vet, test)
	@echo "$(GREEN)✓ All checks passed$(NC)"

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

dev-setup: deps build plugins ## Initial development setup
	@echo "$(GREEN)✓ Development environment ready$(NC)"
	@echo ""
	@echo "$(BLUE)Next steps:$(NC)"
	@echo "  1. Set your API key: export OPENAI_API_KEY=your-key"
	@echo "  2. Run the server: make run"
	@echo "  3. Visit: http://localhost:8765"

install-tools: ## Install development tools
	@echo "$(BLUE)Installing development tools...$(NC)"
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
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
