# Tasks: Refactor Server Initialization

## Relevant Files

- `internal/server/server.go` - Main server file to be refactored (1,548 lines)
- `internal/server/builder.go` - New file for ServerBuilder pattern
- `internal/server/initialization.go` - New file for initialization helpers
- `internal/server/routes.go` - New file for route registration
- `internal/server/middleware.go` - New file for CORS and middleware
- `internal/server/server_test.go` - Tests for server lifecycle
- `internal/server/builder_test.go` - Tests for builder pattern
- `internal/server/initialization_test.go` - Tests for initialization helpers
- `cmd/server/main.go` - Entry point (verify no changes needed)

### Notes

- Unit tests should be placed alongside the code files they are testing
- Use `go test ./internal/server/...` to run all server package tests
- Run `go test ./...` from project root to run full test suite

## Instructions for Completing Tasks

**IMPORTANT:** As you complete each task, you must check it off in this markdown file by changing `- [ ]` to `- [x]`. This helps track progress and ensures you don't skip any steps.

Example:
- `- [ ] 1.1 Read file` → `- [x] 1.1 Read file` (after completing)

Update the file after completing each sub-task, not just after completing an entire parent task.

## Tasks

- [ ] 0.0 Create feature branch
  - [x] 0.1 Create and checkout a new branch (feature/refactor-server-initialization)

- [x] 1.0 Analyze current server.go structure
  - [x] 1.1 Read internal/server/server.go completely to understand current structure
  - [x] 1.2 Identify line ranges for each section (struct definition, New(), Handler(), lifecycle methods, page handlers)
  - [x] 1.3 Document all dependencies in Server struct (count fields and group by type)
  - [x] 1.4 Map out initialization order in New() function (create phase list)
  - [x] 1.5 Identify all route registrations in Handler() method
  - [x] 1.6 Note all existing log statements to preserve during refactoring

- [x] 2.0 Extract middleware to separate file
  - [x] 2.1 Create internal/server/middleware.go
  - [x] 2.2 Move corsHandler() method to middleware.go and rename to CORSMiddleware()
  - [x] 2.3 Update corsHandler references in server.go to use CORSMiddleware
  - [x] 2.4 Add package documentation to middleware.go
  - [x] 2.5 Run tests to verify no breakage: `go test ./internal/server/...`
  - [x] 2.6 Commit changes: "refactor: extract CORS middleware to separate file"

- [x] 3.0 Extract route registration to separate file
  - [x] 3.1 Create internal/server/routes.go
  - [x] 3.2 Create registerRoutes(mux *http.ServeMux, s *Server) function
  - [x] 3.3 Copy all route registration code from Handler() method to registerRoutes()
  - [x] 3.4 Group routes by domain with comments (agent routes, chat routes, plugin routes, etc.)
  - [x] 3.5 Update Handler() method to call registerRoutes(mux, s)
  - [x] 3.6 Add package documentation to routes.go explaining route organization
  - [x] 3.7 Run tests to verify no breakage: `go test ./internal/server/...`
  - [x] 3.8 Test server startup manually: `go run ./cmd/server`
  - [x] 3.9 Commit changes: "refactor: extract route registration to separate file"

- [x] 4.0 Create initialization helper functions
  - [x] 4.1 Create internal/server/initialization.go
  - [x] 4.2 Add package documentation explaining initialization helpers
  - [x] 4.3 Create loadDefaultSettings() helper function
  - [x] 4.4 Create createLLMFactory(config *config.Manager) helper function
  - [x] 4.5 Create registerLLMProviders(factory *llm.Factory, config *config.Manager) helper function
  - [x] 4.6 Create initializePluginRegistry(mgr *registry.Manager) helper function
  - [x] 4.7 Create createWorkspaceStore(agentStorePath string) helper function
  - [x] 4.8 Add error handling and logging to each helper function
  - [x] 4.9 Add godoc comments to each helper function
  - [x] 4.10 Run tests to verify compilation: `go build ./internal/server/...`
  - [x] 4.11 Commit changes: "refactor: add initialization helper functions"

- [x] 5.0 Create initialization helper tests
  - [x] 5.1 Create internal/server/initialization_test.go
  - [x] 5.2 Write test for loadDefaultSettings() - verify default values
  - [x] 5.3 Write test for createLLMFactory() - test with valid and empty config
  - [x] 5.4 Write test for registerLLMProviders() - test OpenAI, Claude, Ollama registration
  - [x] 5.5 Write test for initializePluginRegistry() - test refresh and error handling
  - [x] 5.6 Write test for createWorkspaceStore() - test creation with valid path
  - [x] 5.7 Run initialization tests: `go test ./internal/server/... -run TestInitialization`
  - [x] 5.8 Verify test coverage: `go test -cover ./internal/server/initialization.go`
  - [x] 5.9 Commit changes: "test: add initialization helper tests"

- [x] 6.0 Design and implement ServerBuilder struct
  - [x] 6.1 Create internal/server/builder.go
  - [x] 6.2 Define ServerBuilder struct with same fields as Server struct
  - [x] 6.3 Implement NewServerBuilder() constructor
  - [x] 6.4 Add server field to builder: `server *Server`
  - [x] 6.5 Add package documentation explaining builder pattern usage
  - [x] 6.6 Run tests to verify compilation: `go build ./internal/server/...`
  - [x] 6.7 Commit changes: "refactor: create ServerBuilder struct" (combined in commit 7975f73)

- [x] 7.0 Implement builder initialization phases
  - [x] 7.1 Implement initializeConfiguration() method - load settings, create config manager
  - [x] 7.2 Implement initializeRegistry() method - create registry manager, refresh from GitHub
  - [x] 7.3 Implement initializeFactories() method - create client factory and LLM factory
  - [x] 7.4 Implement initializeProviders() method - register OpenAI, Claude, Ollama providers
  - [x] 7.5 Implement initializeStorage() method - create file store and workspace store
  - [x] 7.6 Implement initializeHandlers() method - create all HTTP handlers
  - [x] 7.7 Implement initializeOrchestration() method - setup task executor, event bus, scheduler
  - [x] 7.8 Implement initializeMCP() method - setup MCP registry and config manager
  - [x] 7.9 Add error handling with descriptive messages to each phase
  - [x] 7.10 Add logging with phase markers (🔧 Initializing...) to each phase
  - [x] 7.11 Commit changes: "refactor: implement builder initialization phases" (commit 7975f73)

- [x] 8.0 Implement Build() method
  - [x] 8.1 Create Build() method that calls all initialization phases in order
  - [x] 8.2 Add error handling - return on first error with context
  - [x] 8.3 Call registerRoutes() at end of Build()
  - [x] 8.4 Return constructed server from Build()
  - [x] 8.5 Add godoc comment explaining Build() process and phase order
  - [x] 8.6 Run tests to verify compilation: `go build ./internal/server/...`
  - [x] 8.7 Commit changes: "refactor: implement ServerBuilder Build method" (commit 7975f73)

- [x] 9.0 Implement optional builder configuration methods
  - [x] 9.1 Implement WithLLMFactory(f *llm.Factory) method
  - [x] 9.2 Implement WithConfigManager(c *config.Manager) method
  - [x] 9.3 Implement WithRegistryManager(r *registry.Manager) method
  - [x] 9.4 Implement WithStore(s store.Store) method
  - [x] 9.5 Implement WithWorkspaceStore(ws agentstudio.Store) method
  - [x] 9.6 Add godoc comments explaining usage for testing
  - [x] 9.7 Ensure With* methods return *ServerBuilder for chaining
  - [x] 9.8 Commit changes: "refactor: add builder configuration methods for testing" (commit 7975f73)

- [x] 10.0 Refactor New() to use builder
  - [x] 10.1 Read current New() implementation completely
  - [x] 10.2 Replace New() body with builder usage: NewServerBuilder() -> Build()
  - [x] 10.3 Preserve existing function signature: func New() (*Server, error)
  - [x] 10.4 Ensure all error handling is preserved
  - [x] 10.5 Verify all log statements are present in builder phases
  - [x] 10.6 Remove old initialization code from New()
  - [x] 10.7 Run full test suite: `go test ./...`
  - [x] 10.8 Test server startup manually: `go run ./cmd/server`
  - [x] 10.9 Verify all page handlers still work (visit /agents, /settings, etc. in browser)
  - [x] 10.10 Commit changes: "refactor: update New() to use ServerBuilder"

- [x] 11.0 Write builder tests
  - [ ] 11.1 Create internal/server/builder_test.go
  - [ ] 11.2 Write TestNewServerBuilder - verify builder creation
  - [ ] 11.3 Write TestServerBuilder_Build - verify full build process
  - [ ] 11.4 Write TestServerBuilder_BuildWithMocks - test With* methods for dependency injection
  - [ ] 11.5 Write TestServerBuilder_BuildFailure - test error handling in phases
  - [ ] 11.6 Write TestServerBuilder_PhaseOrder - verify phases execute in correct order
  - [ ] 11.7 Run builder tests: `go test ./internal/server/... -run TestServerBuilder`
  - [ ] 11.8 Verify test coverage for builder.go: `go test -cover ./internal/server/builder.go`
  - [ ] 11.9 Commit changes: "test: add ServerBuilder tests"

- [ ] 12.0 Update server_test.go
  - [ ] 12.1 Read existing tests in internal/server/server_test.go
  - [ ] 12.2 Update any tests that directly reference New() implementation details
  - [ ] 12.3 Ensure lifecycle tests (Start/Shutdown) still pass
  - [ ] 12.4 Run server tests: `go test ./internal/server/... -run TestServer`
  - [ ] 12.5 Commit changes if any updates made: "test: update server tests for builder refactoring"

- [ ] 13.0 Verify backward compatibility
  - [ ] 13.1 Run full test suite: `go test ./...`
  - [ ] 13.2 Test server startup: `go run ./cmd/server`
  - [ ] 13.3 Test agent creation via UI
  - [ ] 13.4 Test chat functionality with agent
  - [ ] 13.5 Test plugin upload
  - [ ] 13.6 Test settings page
  - [ ] 13.7 Verify all logs appear as expected during startup
  - [ ] 13.8 Check for any new warnings or errors in console

- [ ] 14.0 Cleanup and documentation
  - [ ] 14.1 Review all new files for code quality and consistency
  - [ ] 14.2 Ensure all exported functions have godoc comments
  - [ ] 14.3 Add package-level documentation to each new file
  - [ ] 14.4 Run `go fmt ./internal/server/...`
  - [ ] 14.5 Run `go vet ./internal/server/...`
  - [ ] 14.6 Check for any TODO comments and address or document
  - [ ] 14.7 Update internal/server/README.md if it exists
  - [ ] 14.8 Verify line counts match targets (server.go ~600-700 lines, others <400)
  - [ ] 14.9 Commit changes: "docs: add documentation for server refactoring"

- [ ] 15.0 Final validation
  - [ ] 15.1 Run full test suite with verbose output: `go test -v ./...`
  - [ ] 15.2 Run full test suite with race detector: `go test -race ./...`
  - [ ] 15.3 Build binary: `go build -o bin/ori-agent ./cmd/server`
  - [ ] 15.4 Run binary and test all major features
  - [ ] 15.5 Review git diff for entire change
  - [ ] 15.6 Verify all commits have good messages
  - [ ] 15.7 Squash commits if needed for clean history
  - [ ] 15.8 Push branch to remote: `git push -u origin feature/refactor-server-initialization`

- [ ] 16.0 Create pull request
  - [ ] 16.1 Review all changes one final time
  - [ ] 16.2 Write PR description summarizing refactoring
  - [ ] 16.3 List files changed and line count reductions
  - [ ] 16.4 Include before/after structure comparison
  - [ ] 16.5 Add testing notes (all tests pass, manual testing completed)
  - [ ] 16.6 Create PR against main branch
  - [ ] 16.7 Address any code review feedback
  - [ ] 16.8 Merge PR after approval
