# ---- Config ----
GO            ?= go
BINDIR        ?= bin
SERVER_BIN    ?= $(BINDIR)/dolphin-agent
STATIC_DIR    ?= internal/web/static      # adjust if your embedded static dir is elsewhere
REGISTRY_FILE ?= $(STATIC_DIR)/plugin_registry.json

PLUGIN_DIRS   := plugins/math plugins/weather plugins/music_project_manager
PLUGINS       := $(PLUGIN_DIRS:%=%/$(notdir %).so)

# ---- Default ----
.PHONY: all
all: build plugins

# ---- Server ----
$(SERVER_BIN):
	@mkdir -p $(BINDIR)
	$(GO) build -o $(SERVER_BIN) ./cmd/server
	@chmod +x $(SERVER_BIN)

.PHONY: build
build: $(SERVER_BIN) ## Build the server binary

.PHONY: build-all
build-all: ## Build for multiple architectures
	@echo "Building for multiple architectures..."
	@mkdir -p $(BINDIR)
	GOOS=darwin GOARCH=amd64 $(GO) build -o $(BINDIR)/dolphin-agent-darwin-amd64 ./cmd/server
	GOOS=darwin GOARCH=arm64 $(GO) build -o $(BINDIR)/dolphin-agent-darwin-arm64 ./cmd/server
	GOOS=linux GOARCH=amd64 $(GO) build -o $(BINDIR)/dolphin-agent-linux-amd64 ./cmd/server
	GOOS=linux GOARCH=arm64 $(GO) build -o $(BINDIR)/dolphin-agent-linux-arm64 ./cmd/server
	GOOS=windows GOARCH=amd64 $(GO) build -o $(BINDIR)/dolphin-agent-windows-amd64.exe ./cmd/server
	@echo "Cross-compilation complete! Binaries in $(BINDIR)/"

.PHONY: install
install: $(SERVER_BIN) ## Install the binary to /usr/local/bin
	@echo "Installing $(SERVER_BIN) to /usr/local/bin/dolphin-agent"
	@sudo cp $(SERVER_BIN) /usr/local/bin/dolphin-agent
	@sudo chmod +x /usr/local/bin/dolphin-agent
	@echo "dolphin-agent installed successfully!"

.PHONY: uninstall
uninstall: ## Remove the installed binary from /usr/local/bin
	@echo "Removing /usr/local/bin/dolphin-agent"
	@sudo rm -f /usr/local/bin/dolphin-agent
	@echo "dolphin-agent uninstalled successfully!"

.PHONY: run
run: $(SERVER_BIN) ## Run the built server (requires OPENAI_API_KEY)
	@if [ -z "$$OPENAI_API_KEY" ]; then echo "ERROR: OPENAI_API_KEY not set"; exit 1; fi
	$(SERVER_BIN)

.PHONY: run-dev
run-dev: ## Run the server directly with go run (requires OPENAI_API_KEY)
	@if [ -z "$$OPENAI_API_KEY" ]; then echo "ERROR: OPENAI_API_KEY not set"; exit 1; fi
	$(GO) run ./cmd/server

# ---- Plugins (.so) ----
# Pattern: plugins/<name>/<name>.so
plugins/%/%.so: FORCE
	@echo "Building plugin $*"
	cd plugins/$* && $(GO) build -buildmode=plugin -o $*.so

.PHONY: plugins
plugins: $(PLUGINS) ## Build all plugins

# ---- Plugin Registry (embedded JSON) ----
# Writes absolute paths for the built .so files into the embedded registry file
.PHONY: registry
registry: plugins ## Regenerate embedded plugin registry JSON with absolute paths
	@mkdir -p "$(STATIC_DIR)"
	@echo "Writing $(REGISTRY_FILE)"
	@echo '{'                                                     >  "$(REGISTRY_FILE)"
	@echo '  "plugins": ['                                        >> "$(REGISTRY_FILE)"
	@echo '    {"name":"math","description":"Perform basic math operations: add, subtract, multiply, divide","path":"'"$(PWD)"'/plugins/math/math.so"},'  >> "$(REGISTRY_FILE)"
	@echo '    {"name":"get_weather","description":"Get weather for a given location","path":"'"$(PWD)"'/plugins/weather/weather.so"}'                    >> "$(REGISTRY_FILE)"
	@echo '  ]'                                                   >> "$(REGISTRY_FILE)"
	@echo '}'                                                     >> "$(REGISTRY_FILE)"
	@echo "OK -> $(REGISTRY_FILE)"

# ---- Housekeeping ----
.PHONY: test
test: ## Run all tests
	$(GO) test ./...

.PHONY: tidy
tidy: ## go mod tidy in root and plugin modules
	$(GO) mod tidy
	@if [ -d plugins/math ]; then (cd plugins/math && $(GO) mod tidy); fi
	@if [ -d plugins/weather ]; then (cd plugins/weather && $(GO) mod tidy); fi

.PHONY: lint
lint: ## Run golangci-lint if available
	@command -v golangci-lint >/dev/null 2>&1 || { echo "golangci-lint not installed; skipping"; exit 0; }
	golangci-lint run ./...

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(BINDIR)
	@find plugins -type f -name '*.so' -delete

.PHONY: FORCE
FORCE:

# ---- Help ----
.PHONY: version
version: ## Show version information
	@if [ -f VERSION ]; then echo "Version: $$(cat VERSION)"; else echo "Version: development"; fi
	@echo "Go version: $$($(GO) version)"
	@echo "Binary: $(SERVER_BIN)"

.PHONY: help
help: ## Show this help
	@echo "\n\033[1mDolphin Agent Build System\033[0m\n"
	@echo "\033[1mUsage:\033[0m make <target>"
	@echo "\n\033[1mTargets:\033[0m\n"
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z0-9_.-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)
	@echo "\n\033[1mExamples:\033[0m"
	@echo "  make build                 # Build the dolphin-agent binary"
	@echo "  make all                   # Build binary and plugins"
	@echo "  make run OPENAI_API_KEY=xxx# Run the built server"
	@echo "  make install               # Install to /usr/local/bin"
	@echo ""
