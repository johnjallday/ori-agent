# Repository Guidelines

## Project Structure & Module Organization
Ori Agent is a Go-first monorepo with supporting assets. Key areas: `cmd/server` hosts the HTTP/WebSocket entry point, while `cmd/menubar` builds the macOS helper. Reusable services (LLM integrations, health checks, plugin orchestration) live under `internal/…`. UI code sits in `frontend` and compiles into `dist/`, while native plugin examples live in `example_plugins`. Tests are split between `internal/*/*_test.go` for package-level coverage and `tests/integration` plus `tests/e2e` for higher-level suites.

## Build, Test, and Development Commands
Run `make deps` to sync Go modules. `make build` emits `bin/ori-agent`, and `make menubar` builds `bin/ori-menubar`. Use `go run cmd/server/main.go` or `make run PORT=8765` for iterative backend work; `./scripts/build.sh` compiles the server plus bundled plugins for release checks. Clean artifacts with `make clean`. Docker users can validate images with `make docker-build && make docker-run`.

## Coding Style & Naming Conventions
All Go code must stay `gofmt` clean—use `make fmt` before submitting. Favor idiomatic Go naming (mixedCaps for exported symbols, lowercase for internals) and keep files scoped by package responsibility (`llm_factory.go`, `agent_store.go`). Lint with `make lint` (golangci-lint) and gate changes through `make vet` for static analysis. Config files (`settings.json`, `locations.json`) are snake_case; plugin directories mirror their plugin IDs to match registry lookups.

## Testing Guidelines
`make test-unit` runs fast package tests (`*_test.go`) without external calls. `make test-integration` exercises live LLM providers and requires `OPENAI_API_KEY` (and optionally `ANTHROPIC_API_KEY`). `make test-e2e` first builds the binary and then runs `tests/e2e` against a live server; skip locally when keys are absent, but ensure CI coverage before merging. Generate coverage reports via `make test-coverage`, which drops `coverage/coverage.html`. Match test names to the behavior under test (e.g., `TestProviderIntegration_WithRetries`) for easy filtering.

## Commit & Pull Request Guidelines
Recent history shows short, imperative commits (“Fix race conditions in location manager tests”). Follow that style, group logical changes, and reference issues with `#123` when applicable. Pull requests should describe motivation, summarize major touches (paths or packages), and call out manual test steps. Attach screenshots or terminal logs when UI changes or CLI outputs shift. Require passing `make test` (and any affected integration/E2E suites) before requesting review.

## Security & Configuration Tips
Never commit API keys; load them through environment variables or `settings.json` (listed in `.gitignore`). Use `make check-env` to verify keys before running agents. When sharing plugins, prefer shipping `.so` files via `uploaded_plugins/` and include accompanying SHA256 hashes stored in `plugin_registry_cache.json` to keep the registry trust chain intact.
