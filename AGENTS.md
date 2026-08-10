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

## Where Work Happens: One Worktree Per Change

Every change is implemented in its own feature worktree. `ori-agent-dev` is for planning and review only — **never** implementation. This holds no matter how much planning preceded the change: a PRD and a task list are opt-in (see the two rules below), the worktree is not.

| Starting point | Command |
|---|---|
| A PRD, optionally with a task list, already in `ori-agent-dev/tasks/` | `wt start [feature-name]` — copies the planning docs into the new worktree |
| Ad-hoc work with no planning artifacts | `wt new <name>`, or `wt new <type>/<name>` to set the branch prefix |

Both accept `--yes` for non-interactive runs and `--no-herdr` to skip the agent handoff. If `wt` itself is broken, that is a bug to fix — not a reason to fall back to implementing in `ori-agent-dev`. Bootstrapping a fix to `wt` is the one case where creating the worktree by hand with `git worktree add` is correct.

**Why a worktree and not just a branch:** `ori-agent-dev` is shared — other sessions commit in it, and a `git switch` there is visible to every one of them, and can disturb a running server or build mid-flight. Separate worktrees let several changes be in flight at once without any of them touching each other's checkout.

## Planning Artifact Location
For the PRD and task-list workflows below, create planning artifacts in this dev worktree's `tasks/` directory (that is, `ori-agent-dev/tasks/`), creating it if necessary. `/tasks/` is not an absolute filesystem path. Finish both planning artifacts there before running `wt start`; it copies them to the isolated feature worktree. Because `tasks/` is gitignored, verify planning artifacts by reading the files directly (and, if needed, use `git status --ignored`) rather than relying on `git diff`.

## Feature Naming: Issue Number First

Ideas are captured as GitHub Issues. `./scripts/devops.sh` is the read-only human
interface: with no arguments it lists every open Issue and starts a small REPL;
one-shot commands expose the same views to scripts and agents.

| Command | Reads |
|---|---|
| `./scripts/devops.sh` or `./scripts/devops.sh all` | every open Issue |
| `./scripts/devops.sh decisions` | open Issues labeled `needs-decision` |
| `./scripts/devops.sh backlog` | open Issues labeled `backlog` |
| `./scripts/devops.sh proposals` | open Issues labeled `feature-proposal` |
| `./scripts/devops.sh view <n>` | one Issue in full |

The script delegates directly to `gh issue list` and `gh issue view`. Its filters
are literal GitHub labels, not Project columns, and every read is fresh. The REPL
does not create, label, comment on, close, rank, or otherwise mutate an Issue.
Use `gh issue create` for capture and GitHub itself for lifecycle changes.

Work selected from an Issue uses the Issue number at the front of its identity:

```
Issue #292 "Coordinate based map"
  → feature slug   292-coordinate-based-map
  → PRD            tasks/prd-292-coordinate-based-map.md
  → task list      tasks/tasks-292-coordinate-based-map.md
  → worktree       292-coordinate-based-map
  → branch         feature/292-coordinate-based-map   (prefix still states intent: feature/, fix/, docs/, …)
```

The number is the repository-local integer GitHub shows. Never derive it from title text, body text, a timestamp, or a position in a list.

**Why the number and not the title:** it is the one part of an Issue that cannot change. Renaming an Issue after planning starts must never require renaming the branch, the worktree, the PRD, or the pull request — and later tooling that joins delivery back to an Issue can then match on an exact identifier instead of comparing prose.

Work that did not come from an Issue keeps a plain descriptive slug. Existing features whose slugs have no number remain valid and are **not** renamed.


# Rule: Generating a Product Requirements Document (PRD)

## Goal

To guide an AI assistant in creating a detailed Product Requirements Document (PRD) in Markdown format, based on an initial user prompt. The PRD should be clear, actionable, and suitable for a junior developer to understand and implement the feature.

**Only apply this rule when the user explicitly asks for a PRD.**

## Process

1.  **Receive Initial Prompt:** The user provides a brief description or request for a new feature or functionality.
2.  **Ask Clarifying Questions:** Before writing the PRD, the AI *must* ask only the most essential clarifying questions needed to write a clear PRD. Limit questions to 3-5 critical gaps in understanding. The goal is to understand the "what" and "why" of the feature, not necessarily the "how" (which the developer will figure out). Make sure to provide options in letter/number lists so I can respond easily with my selections.
3.  **Generate PRD:** Based on the initial prompt and the user's answers to the clarifying questions, generate a PRD using the structure outlined below.
4.  **Save PRD:** Save the generated document as `prd-[feature-name].md` in the planning artifact location defined above.

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
*   **Location:** `ori-agent-dev/tasks/` (the planning artifact location above)
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
- **Location:** `ori-agent-dev/tasks/` (the planning artifact location above)
- **Filename:** `tasks-[feature-name].md` (e.g., `tasks-user-profile-editing.md`)

## Process

1.  **Receive Requirements:** The user provides a feature request, task description, or points to existing documentation
2.  **Analyze Requirements:** The AI analyzes the functional requirements, user needs, and implementation scope from the provided information
3.  **Phase 1: Generate Parent Tasks:** Based on the requirements analysis, create the file and generate the main, high-level tasks required to implement the feature. Use your judgement on how many high-level tasks to use. It's likely to be about 5. Present these tasks to the user in the specified format (without sub-tasks yet). Inform the user: "I have generated the high-level tasks based on your requirements. Ready to generate the sub-tasks? Respond with 'Go' to proceed."
4.  **Wait for Confirmation:** Pause and wait for the user to respond with "Go".
5.  **Phase 2: Generate Sub-Tasks:** Once the user confirms, break down each parent task into smaller, actionable sub-tasks necessary to complete the parent task. Ensure sub-tasks logically follow from the parent task and cover the implementation details implied by the requirements. **Prefer vertical slices over layer-first grouping** — a group should be a thin end-to-end feature, not "all backend" then "all frontend" (see "Slicing & Build Order"); within a group, order the sub-tasks for the fastest honest feedback. Include a `Commit: "<conventional message>"` sub-task after each parent group that represents a tested, reviewable milestone, and end the final group with the feature-delivery steps described below.
6.  **Identify Relevant Files:** Based on the tasks and requirements, identify potential files that will need to be created or modified. List these under the `Relevant Files` section, including corresponding test files if applicable.
7.  **Generate Final Output:** Combine the parent tasks, sub-tasks, relevant files, and notes into the final Markdown structure.
8.  **Save Task List:** Save the generated document in the planning artifact location with the filename `tasks-[feature-name].md`, where `[feature-name]` describes the main feature or task being implemented (e.g., if the request was about user profile editing, the output is `tasks-user-profile-editing.md`).
9.  **Create Worktree:** Immediately after saving the task list, create the feature's isolated worktree by running `wt start [feature-name]` (the `ori-devflow` agent owns this workflow; see `scripts/wt.sh`). This fetches `origin/dev`, creates the worktree and feature branch, copies the PRD and task list into it, and switches into it — **no additional confirmation needed**, this runs right after step 8 without waiting for another "Go". Do not add a separate branch-creation task. All sub-tasks from 1.1 onward are implemented in that worktree, never in `ori-agent-dev`.

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

**Execution is continuous.** Work straight through **all** parent groups without pausing for approval between them: finish a group, tick its boxes, demo it, commit it, and start the next group in the same turn. A finished group is not a checkpoint — never end a turn with "Group 3 done, shall I continue?". The only human gate is the `Open PR` sub-task at the very end; stop there and nowhere else, unless you hit a real blocker (see "Interaction Model" in AGENTS.md).

## Delivery Checkpoints

By default, one PRD and task list maps to one feature worktree, one branch, and one PR targeting `dev`. Keep commits conventional and focused. Include a `Commit: "<conventional message>"` sub-task after every parent group that is a tested, reviewable milestone. The final parent group must end with `Open PR → squash-merge to dev` (using `wt pr` when authorized) and `Run wt done [feature-name] after merge` to archive the completed checklist back to the dev worktree and clean up the feature worktree.

## Slicing & Build Order

The problem to avoid is not "backend before frontend" — it's building one whole **layer** before the other, in either direction. Backend-first leaves nothing to look at until late; strict frontend-first leaves the demo wired to fake data, which *looks* done but does nothing.

- **Default to vertical slices, not layer-first.** Structure parent-task groups as thin end-to-end features (one narrow real path through both layers, demoable), not "Group 1 = all backend, Group 2 = all frontend." Thicken both sides in later groups.
- **Within a single branch, dev order is free — order it for the fastest honest feedback.** When the UX is the uncertain part (new screen, novel interaction), build the frontend first against hardcoded stubs, react to the layout, *then* wire the real backend, *then* open the PR. Throwaway stub scaffolding is a cheap price for seeing the surface early.
- **The one guardrail — never merge a mock-backed frontend.** Frontend-first is a *within-branch development order*, not a *merge order*. Every PR that merges to `dev` must be real end-to-end; a fake-data UI merged as-is ships dead/lying surface.
- **Optional prototype demo.** To get eyes on the UX before committing backend effort, add an early `Prototype demo: <surface> against stubs (not wired) — design sign-off only` sub-task, kept clearly distinct from the real end-to-end validation so a stubbed prototype is never mistaken for working software.

## Tasks

- [ ] 1.0 Parent Task Title
  - [ ] 1.1 [Sub-task description 1.1]
  - [ ] 1.2 [Sub-task description 1.2]
  - [ ] 1.3 Validate the completed parent task
  - [ ] 1.4 Commit: "feat(scope): deliver parent task title"
- [ ] 2.0 Parent Task Title
  - [ ] 2.1 [Sub-task description 2.1]
  - [ ] 2.2 Validate the completed parent task
  - [ ] 2.3 Commit: "feat(scope): deliver parent task title"
- [ ] 3.0 Parent Task Title (may not require sub-tasks if purely structural or configuration)
  - [ ] 3.1 Validate the completed feature
  - [ ] 3.2 Commit: "feat(scope): deliver parent task title"
  - [ ] 3.3 Open PR → squash-merge to dev
  - [ ] 3.4 Run `wt done [feature-name]` after merge
```

## Interaction Model

The pipeline has exactly **two** pauses:

1. **After parent tasks are generated** — wait for "Go" before writing sub-tasks. This ensures the high-level plan aligns with user expectations before diving into details.
2. **Before the PR is opened** — wait for explicit approval before `gh pr create` / `wt pr`.

**Everything between them runs unattended.** Once implementation starts, work straight through every parent group in one continuous run: complete a group, tick its boxes, run its `Demo:`, commit, and immediately begin the next group in the same turn. A completed group is not an approval point — don't ask "ready for group 4?", don't present a demo screenshot for sign-off. The plan was approved when the task list was approved. Multi-PR epics gate per **PR**, not per group: run all groups belonging to one seam-PR continuously and stop only at that seam-PR's own open step.

The `Demo:` checkpoint stays, but it is a **self**-check, not a review request: drive the new surface, screenshot it, fix what's broken, commit, keep going. Batch the screenshots into the final summary.

Break the continuous run only for a **real blocker**, never for a check-in: a genuine ambiguity where guessing could produce the wrong feature; a plan whose premise turns out to be invalid; a destructive or outward-facing action the plan didn't cover; something only the user can supply (credential, interactive login, product decision); or the same step failing ~3 times with no new idea. Stop immediately in those cases with the specific question rather than burning the remaining groups first. Otherwise deliver one final summary — what each group shipped, the screenshots, real test results, any deviations — and then ask for PR approval.

There is a third checkpoint, but it is **not** a pause: once the sub-tasks are generated and the task list is saved (step 8), immediately proceed to step 9 (`wt start [feature-name]`) without asking again. PRD generation and the full task list (parent + sub-tasks) both happen in the `ori-agent-dev` worktree, which is the single source of truth for planning docs (`tasks/` is gitignored, so it doesn't sync between worktrees on its own). Only after the complete task list is saved does *this flow's* feature worktree get created — implementation never starts in dev. Work that skips the PRD and task list still gets its own worktree, created up front with `wt new` (see "Where Work Happens: One Worktree Per Change").

## Target Audience

Assume the primary reader of the task list is a **junior developer** who will implement the feature.

## Terminology Note

- UI uses "workspace" while many backend query params expect `studio_id`. Prefer `studio_id` in API calls even when the UI label says workspace.
