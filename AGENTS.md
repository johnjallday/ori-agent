# Repository Guidelines

## Project Structure & Module Organization
Ori Agent is a Go-first monorepo with supporting assets. Key areas: `cmd/server` hosts the HTTP/WebSocket entry point, while `cmd/menubar` builds the macOS helper. Reusable services (LLM integrations, MCP integration, skills, health checks) live under `internal/…`. The web UI (HTML/CSS/JS) is embedded under `internal/web`. Tests are split between `internal/*/*_test.go` for package-level coverage and `tests/integration` plus `tests/e2e` for higher-level suites.

## Build, Test, and Development Commands
Run `make deps` to sync Go modules. `make build` emits `bin/ori-agent`, and `make menubar` builds `bin/ori-menubar`. Use `go run cmd/server/main.go` or `make run PORT=8765` for iterative backend work; `./scripts/build.sh` compiles the server for release checks. Clean artifacts with `make clean`. Docker users can validate images with `make docker-build && make docker-run`.

## Coding Style & Naming Conventions
All Go code must stay `gofmt` clean—use `make fmt` before submitting. Favor idiomatic Go naming (mixedCaps for exported symbols, lowercase for internals) and keep files scoped by package responsibility (`llm_factory.go`, `agent_store.go`). Lint with `make lint` (golangci-lint) and gate changes through `make vet` for static analysis. Config files (`settings.json`, `agents.json`) are snake_case.

## Testing Guidelines
`make test-unit` runs fast package tests (`*_test.go`) without external calls. `make test-integration` exercises live LLM providers and requires `OPENAI_API_KEY` (and optionally `ANTHROPIC_API_KEY`). `make test-e2e` first builds the binary and then runs `tests/e2e` against a live server; skip locally when keys are absent, but ensure CI coverage before merging. Generate coverage reports via `make test-coverage`, which drops `coverage/coverage.html`. Match test names to the behavior under test (e.g., `TestProviderIntegration_WithRetries`) for easy filtering.

## Commit & Pull Request Guidelines
Recent history shows short, imperative commits (“Fix race conditions in location manager tests”). Follow that style, group logical changes, and reference issues with `#123` when applicable. Pull requests should describe motivation, summarize major touches (paths or packages), and call out manual test steps. Attach screenshots or terminal logs when UI changes or CLI outputs shift. Require passing `make test` (and any affected integration/E2E suites) before requesting review.

## Security & Configuration Tips
Never commit API keys; load them through environment variables or `settings.json` (listed in `.gitignore`). Use `make check-env` to verify keys before running agents. Tool capabilities come from MCP servers and skills configured per workspace; store any secrets they need in environment variables or the vault rather than committing them.

CLI-provider agents (Claude Code, Codex) can run a workspace's MCP + built-in tools natively, but this is **off by default** and gated by a two-level opt-in (`Workspace.AllowNativeMCPCLI` *and* the agent's `Settings.AllowNativeMCPTools`), toggleable from the workspace MCP pane. When enabled, the CLI executes tools itself — outside Ori's per-call confirmation gate — sandboxed to the workspace folder; treat it as a trusted-autonomy setting. Native-MCP runs use a separate, longer timeout (`native_mcp_exec_timeout_seconds`, default 300s).


# Rule: Generating a Product Requirements Document (PRD)

## Goal

To guide an AI assistant in creating a detailed Product Requirements Document (PRD) in Markdown format, based on an initial user prompt. The PRD should be clear, actionable, and suitable for a junior developer to understand and implement the feature.

**Only apply this rule when the user explicitly asks for a PRD.**

## Process

1.  **Receive Initial Prompt:** The user provides a brief description or request for a new feature or functionality.
2.  **Ask Clarifying Questions:** Before writing the PRD, the AI *must* ask only the most essential clarifying questions needed to write a clear PRD. Limit questions to 3-5 critical gaps in understanding. The goal is to understand the "what" and "why" of the feature, not necessarily the "how" (which the developer will figure out). Make sure to provide options in letter/number lists so I can respond easily with my selections.
3.  **Generate PRD:** Based on the initial prompt and the user's answers to the clarifying questions, generate a PRD using the structure outlined below.
4.  **Save PRD:** Save the generated document as `prd-[feature-name].md` inside the `/tasks` directory.

## Clarifying Questions (Guidelines)

Ask only the most critical questions needed to write a clear PRD. Focus on areas where the initial prompt is ambiguous or missing essential context. Common areas that may need clarification:

*   **Problem/Goal:** If unclear - "What problem does this feature solve for the user?"
*   **Core Functionality:** If vague - "What are the key actions a user should be able to perform?"
*   **Scope/Boundaries:** If broad - "Are there any specific things this feature *should not* do?"
*   **Success Criteria:** If unstated - "How will we know when this feature is successfully implemented?"

**Important:** Only ask questions when the answer isn't reasonably inferable from the initial prompt. Prioritize questions that would significantly impact the PRD's clarity.

### Formatting Requirements

- **Number all questions** (1, 2, 3, etc.)
- **List options for each question as A, B, C, D, etc.** for easy reference
- Make it simple for the user to respond with selections like "1A, 2C, 3B"

### Example Format

```
1. What is the primary goal of this feature?
   A. Improve user onboarding experience
   B. Increase user retention
   C. Reduce support burden
   D. Generate additional revenue

2. Who is the target user for this feature?
   A. New users only
   B. Existing users only
   C. All users
   D. Admin users only

3. What is the expected timeline for this feature?
   A. Urgent (1-2 weeks)
   B. High priority (3-4 weeks)
   C. Standard (1-2 months)
   D. Future consideration (3+ months)
```

## PRD Structure

The generated PRD should include the following sections:

1.  **Introduction/Overview:** Briefly describe the feature and the problem it solves. State the goal.
2.  **Goals:** List the specific, measurable objectives for this feature.
3.  **User Stories:** Detail the user narratives describing feature usage and benefits.
4.  **Functional Requirements:** List the specific functionalities the feature must have. Use clear, concise language (e.g., "The system must allow users to upload a profile picture."). Number these requirements.
5.  **Non-Goals (Out of Scope):** Clearly state what this feature will *not* include to manage scope.
6.  **Design Considerations (Optional):** Link to mockups, describe UI/UX requirements, or mention relevant components/styles if applicable.
7.  **Technical Considerations (Optional):** Mention any known technical constraints, dependencies, or suggestions (e.g., "Should integrate with the existing Auth module").
8.  **Success Metrics:** How will the success of this feature be measured? (e.g., "Increase user engagement by 10%", "Reduce support tickets related to X").
9.  **Open Questions:** List any remaining questions or areas needing further clarification.

## Target Audience

Assume the primary reader of the PRD is a **junior developer**. Therefore, requirements should be explicit, unambiguous, and avoid jargon where possible. Provide enough detail for them to understand the feature's purpose and core logic.

## Output

*   **Format:** Markdown (`.md`)
*   **Location:** `/tasks/`
*   **Filename:** `prd-[feature-name].md`

## Final instructions

1. Do NOT start implementing the PRD
2. Make sure to ask the user clarifying questions
3. Take the user's answers to the clarifying questions and improve the PRD




# Rule: Generating a Task List from User Requirements

## Goal

To guide an AI assistant in creating a detailed, step-by-step task list in Markdown format based on user requirements, feature requests, or existing documentation. The task list should guide a developer through implementation.

**Only apply this rule when the user explicitly asks for a task list.**

## Output

- **Format:** Markdown (`.md`)
- **Location:** `/tasks/`
- **Filename:** `tasks-[feature-name].md` (e.g., `tasks-user-profile-editing.md`)

## Process

1.  **Receive Requirements:** The user provides a feature request, task description, or points to existing documentation
2.  **Analyze Requirements:** The AI analyzes the functional requirements, user needs, and implementation scope from the provided information
3.  **Phase 1: Generate Parent Tasks:** Based on the requirements analysis, create the file and generate the main, high-level tasks required to implement the feature. **IMPORTANT: Always include task 0.0 "Create feature branch" as the first task, unless the user specifically requests not to create a branch.** If the user already created a branch, include the task but mark it complete. Use your judgement on how many additional high-level tasks to use. It's likely to be about 5. Present these tasks to the user in the specified format (without sub-tasks yet). Inform the user: "I have generated the high-level tasks based on your requirements. Ready to generate the sub-tasks? Respond with 'Go' to proceed."
4.  **Wait for Confirmation:** Pause and wait for the user to respond with "Go".
5.  **Phase 2: Generate Sub-Tasks:** Once the user confirms, break down each parent task into smaller, actionable sub-tasks necessary to complete the parent task. Ensure sub-tasks logically follow from the parent task and cover the implementation details implied by the requirements.
6.  **Identify Relevant Files:** Based on the tasks and requirements, identify potential files that will need to be created or modified. List these under the `Relevant Files` section, including corresponding test files if applicable.
7.  **Generate Final Output:** Combine the parent tasks, sub-tasks, relevant files, and notes into the final Markdown structure.
8.  **Save Task List:** Save the generated document in the `/tasks/` directory with the filename `tasks-[feature-name].md`, where `[feature-name]` describes the main feature or task being implemented (e.g., if the request was about user profile editing, the output is `tasks-user-profile-editing.md`).

## Output Format

The generated task list _must_ follow this structure:

```markdown
## Relevant Files

- `path/to/potential/file1.go` - Brief description of why this file is relevant (e.g., Contains the main handler for this feature).
- `path/to/file1_test.go` - Unit tests for `file1.go`.
- `path/to/another/file.go` - Brief description (e.g., API route handler for data submission).
- `path/to/another/file_test.go` - Unit tests for `another/file.go`.
- `internal/utils/helpers.go` - Brief description (e.g., Utility functions needed for calculations).
- `internal/utils/helpers_test.go` - Unit tests for `helpers.go`.

### Notes

- Unit tests should typically be placed alongside the code files they are testing (e.g., `foo.go` and `foo_test.go` in the same directory).
- Use `go test ./...` or `make test-unit` to run tests. Running without a package path executes all tests in the repo.

## Instructions for Completing Tasks

**IMPORTANT:** As you complete each task, you must check it off in this markdown file by changing `- [ ]` to `- [x]`. This helps track progress and ensures you don't skip any steps.

Example:
- `- [ ] 1.1 Read file` → `- [x] 1.1 Read file` (after completing)

Update the file after completing each sub-task, not just after completing an entire parent task.

## Tasks

- [ ] 0.0 Create feature branch
  - [ ] 0.1 Create and checkout a new branch for this feature (e.g., `git checkout -b feature/[feature-name]`)
- [ ] 1.0 Parent Task Title
  - [ ] 1.1 [Sub-task description 1.1]
  - [ ] 1.2 [Sub-task description 1.2]
- [ ] 2.0 Parent Task Title
  - [ ] 2.1 [Sub-task description 2.1]
- [ ] 3.0 Parent Task Title (may not require sub-tasks if purely structural or configuration)
```

## Interaction Model

The process explicitly requires a pause after generating parent tasks to get user confirmation ("Go") before proceeding to generate the detailed sub-tasks. This ensures the high-level plan aligns with user expectations before diving into details.

## Target Audience

Assume the primary reader of the task list is a **junior developer** who will implement the feature.

## Terminology Note

- UI uses "workspace" while many backend query params expect `studio_id`. Prefer `studio_id` in API calls even when the UI label says workspace.
