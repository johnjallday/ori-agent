# ---- Config ----
GO            ?= go
BINDIR        ?= bin
SERVER_BIN    ?= $(BINDIR)/server
STATIC_DIR    ?= internal/web/static      # adjust if your embedded static dir is elsewhere
REGISTRY_FILE ?= $(STATIC_DIR)/plugin_registry.json

PLUGIN_DIRS   := plugins/math plugins/weather
PLUGINS       := $(PLUGIN_DIRS:%=%/$(notdir %).so)

# ---- Default ----
.PHONY: all
all: build plugins

# ---- Server ----
$(SERVER_BIN):
	@mkdir -p $(BINDIR)
	$(GO) build -o $(SERVER_BIN) ./cmd/server

.PHONY: build
build: $(SERVER_BIN) ## Build the server binary

.PHONY: run
run: ## Run the server (requires OPENAI_API_KEY)
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
.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"; printf "\n\033[1mTargets:\033[0m\n\n"} /^[a-zA-Z0-9_.-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)
