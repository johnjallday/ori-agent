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
	$(GOBUILD) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/server
	@echo "$(GREEN)✓ Build complete: $(BUILD_DIR)/$(BINARY_NAME)$(NC)"

menubar: ## Build the menu bar app
	@echo "$(BLUE)Building menu bar app...$(NC)"
	$(GOBUILD) -o $(BUILD_DIR)/$(MENUBAR_BINARY_NAME) ./cmd/menubar
	@echo "$(GREEN)✓ Build complete: $(BUILD_DIR)/$(MENUBAR_BINARY_NAME)$(NC)"

plugins: ## Build all plugins
	@echo "$(BLUE)Building plugins...$(NC)"
	@./scripts/build-plugins.sh || (echo "$(RED)Plugin build failed$(NC)" && exit 1)
	@echo "$(GREEN)✓ Plugins built$(NC)"

all: deps build menubar plugins ## Build everything (server + menubar + plugins)
	@echo "$(GREEN)✓ Build complete$(NC)"

clean: ## Clean build artifacts
	@echo "$(BLUE)Cleaning build artifacts...$(NC)"
	rm -rf $(BUILD_DIR)
	rm -rf $(COVERAGE_DIR)
	rm -rf build-dmg
	rm -rf $(PLUGINS_DIR)/*/$(shell basename $(PLUGINS_DIR))
	@echo "$(GREEN)✓ Clean complete$(NC)"

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
	@if [ -z "$$OPENAI_API_KEY" ] && [ -z "$$ANTHROPIC_API_KEY" ]; then \
		echo "$(YELLOW)Skipping user tests (no API key set)$(NC)"; \
	else \
		$(GOTEST) -v -timeout 5m ./tests/user/...; \
		echo "$(GREEN)✓ User tests passed$(NC)"; \
	fi

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
	@echo "$(GREEN)✓ Tools installed$(NC)"
