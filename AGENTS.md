# Repository Guidelines

## Project Structure & Module Organization
Ori Agent is a Go application with an embedded web UI. `cmd/server` contains the HTTP/WebSocket server, `cmd/menubar` builds the macOS helper, and `cmd/test-cli` supports testing. Shared services live under `internal/` for LLMs, workspaces, MCP, skills, health checks, and web handlers. UI templates and static assets live in `internal/web`. Tests live beside Go packages as `*_test.go` plus higher-level suites under `tests/`.

## Build, Test, and Development Commands
Use `make deps` to download and tidy modules. `make build` creates `bin/ori-agent`; `make menubar` creates `bin/ori-menubar`; `make all` builds both. For local work, run `make run-dev PORT=8765`, or `make run PORT=8765` to build first. `make clean` removes build and coverage artifacts. Frontend checks use `npm run lint`, `npm run format:check`, and `npm run test:smoke`.

## Coding Style & Naming Conventions
Keep Go code `gofmt` clean with `make fmt`; run `make vet` and `make lint` for static checks. Use idiomatic Go mixedCaps names and package-focused filenames such as `agent_store.go` or `llm_factory.go`. Frontend code in `internal/web/static` uses ESLint and Prettier through npm scripts. Runtime config files such as `settings.json` and `agents.json` use snake_case keys.

## Testing Guidelines
`make test-unit` runs fast Go tests with `-short`; `make test` runs the main suite; `make test-coverage` writes `coverage/coverage.html`. JS module tests run with `make test-js`; Playwright smoke tests run with `npm run test:smoke`. Integration, e2e, and user suites may require `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, or `USE_OLLAMA=true`. Name tests after observable behavior, for example `TestProviderIntegration_WithRetries`.

## Commit & Pull Request Guidelines
Recent history uses Conventional Commit-style subjects such as `feat(workspace): ...` and `chore(config): ...`. Keep commits focused and reference issues or PRs with `#123` when relevant. Pull requests should explain motivation, summarize touched paths, list validation commands, and include screenshots or terminal output for UI, CLI, or workflow changes. Run `make test` plus affected JS, integration, e2e, or smoke suites before review.

## Security & Configuration Tips
Never commit API keys or local state. Load provider credentials through environment variables or ignored local config, and use `make check-env` before running provider-backed agents. Keep generated binaries, coverage output, and workspace state out of commits unless explicitly required.

## Agent-Specific Instructions
CLI-provider agents can run native workspace MCP only after both `Workspace.AllowNativeMCPCLI` and `Settings.AllowNativeMCPTools` are enabled. Treat this as trusted autonomy: calls execute outside Ori's per-call confirmation gate, sandboxed to the workspace folder. Native MCP execution uses `native_mcp_exec_timeout_seconds`, defaulting to 300 seconds.


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
3.  **Phase 1: Generate Parent Tasks:** Based on the requirements analysis, create the file and generate the main, high-level tasks required to implement the feature. Use your judgement on how many high-level tasks to use. It's likely to be about 5. Present these tasks to the user in the specified format (without sub-tasks yet). Inform the user: "I have generated the high-level tasks based on your requirements. Ready to generate the sub-tasks? Respond with 'Go' to proceed."
4.  **Wait for Confirmation:** Pause and wait for the user to respond with "Go".
5.  **Phase 2: Generate Sub-Tasks:** Once the user confirms, break down each parent task into smaller, actionable sub-tasks necessary to complete the parent task. Ensure sub-tasks logically follow from the parent task and cover the implementation details implied by the requirements.
6.  **Identify Relevant Files:** Based on the tasks and requirements, identify potential files that will need to be created or modified. List these under the `Relevant Files` section, including corresponding test files if applicable.
7.  **Generate Final Output:** Combine the parent tasks, sub-tasks, relevant files, and notes into the final Markdown structure.
8.  **Save Task List:** Save the generated document in the `/tasks/` directory with the filename `tasks-[feature-name].md`, where `[feature-name]` describes the main feature or task being implemented (e.g., if the request was about user profile editing, the output is `tasks-user-profile-editing.md`).
9.  **Create Worktree:** Immediately after saving the task list, create the feature's isolated worktree by running `wt start [feature-name]` (the `ori-devflow` agent owns this workflow; see `scripts/wt.sh`). This fetches `origin/dev`, creates the worktree, copies the PRD and this task list into it, and switches into it — **no additional confirmation needed**, this runs right after step 8 without waiting for another "Go". All sub-tasks from 1.1 onward are implemented in that worktree, never in `ori-agent-dev`.

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

- [ ] 1.0 Parent Task Title
  - [ ] 1.1 [Sub-task description 1.1]
  - [ ] 1.2 [Sub-task description 1.2]
- [ ] 2.0 Parent Task Title
  - [ ] 2.1 [Sub-task description 2.1]
- [ ] 3.0 Parent Task Title (may not require sub-tasks if purely structural or configuration)
```

## Interaction Model

The process has exactly one pause: after generating parent tasks, wait for user confirmation ("Go") before generating the detailed sub-tasks. This ensures the high-level plan aligns with user expectations before diving into details.

There is a second checkpoint, but it is **not** a pause: once the sub-tasks are generated and the task list is saved (step 8), immediately proceed to step 9 (`wt start [feature-name]`) without asking again. PRD generation and the full task list (parent + sub-tasks) both happen in the `ori-agent-dev` worktree, which is the single source of truth for planning docs (`tasks/` is gitignored, so it doesn't sync between worktrees on its own). Only after the complete task list is saved does a feature worktree get created — implementation never starts in dev.

## Target Audience

Assume the primary reader of the task list is a **junior developer** who will implement the feature.

## Terminology Note

- UI uses "workspace" while many backend query params expect `studio_id`. Prefer `studio_id` in API calls even when the UI label says workspace.
