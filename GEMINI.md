# Ori Agent Context for Gemini

## Project Overview
**Ori Agent** is a local-first AI agent management platform. It enables users to run multiple named agents, each with unique configurations (model, system prompt, tools), directly on their machine.

*   **Core Philosophy:** Local-first, privacy-centric. Agents and data stay local unless cloud LLMs are explicitly used.
*   **Architecture:**
    *   **Backend:** Go (v1.25.5+) using standard library HTTP server + Wails for desktop integration.
    *   **Frontend:** Web interface located in `internal/web/static`, served by the Go backend.
    *   **Tools:** MCP (Model Context Protocol) servers and Skills provide tool capabilities. MCP servers are external processes; Skills are reusable prompt-based capabilities.
    *   **Database:** SQLite (via `modernc.org/sqlite`) for local storage of agents, sessions, and history.

## Building and Running

### Prerequisites
*   Go 1.25 or later.
*   Node.js & npm (for frontend linting/testing).
*   API Key (OpenAI `OPENAI_API_KEY` or Anthropic `ANTHROPIC_API_KEY`) OR Ollama running locally.

### Key Commands
*   **Build Server:** `make build` (creates `./bin/ori-agent`)
*   **Run Server:** `make run` (starts on `http://localhost:8765`)
*   **Run Dev Mode:** `make run-dev` (runs `cmd/server` directly without compiling binary)
*   **Build Menubar App:** `make menubar` (creates `./bin/ori-menubar`)
*   **Clean:** `make clean`

### Testing
*   **Unit Tests:** `make test-unit`
*   **Integration Tests:** `make test-integration` (requires API keys)
*   **End-to-End Tests:** `make test-e2e`
*   **UI Tests:** `npm run test` (Playwright)
*   **All Tests:** `make test-all`

## Development Conventions

### Code Structure
*   `cmd/`: Entry points (`server`, `menubar`, `ori-plugin-gen`).
*   `internal/`: Private application logic (server, core, agents, etc.).
*   `internal/mcp/`: MCP server integration.
*   `internal/skills/`: Skills management.
*   `tests/`: Integration, E2E, and user scenario tests.
*   `internal/web/static/`: Frontend assets (JS, CSS, HTML).

### Coding Style
*   **Go:** Follows standard Go idioms. Run `make fmt` and `make vet` before committing.
*   **Frontend:** Uses ESLint and Prettier.
    *   Lint: `npm run lint`
    *   Format: `npm run format`
*   **Tools:** Use MCP servers for external tool integration. See `internal/mcp/` for reference.

### Versioning
*   Version is managed in the `VERSION` file.
*   CI/CD handles releases (GitHub Actions).

### State Management
*   App state is stored in `ori-data` (or standard OS app data locations).
*   To reset state completely: `make reset` (WARNING: Deletes all local agents and history).
