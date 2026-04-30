---
name: workspace-planning
description: Plan workspace feature work before execution. Use when a request should be clarified, translated into PRD or task files under /tasks, turned into workspace tasks, or staged for safer step-by-step execution.
---

# Workspace Planning

Use this skill for planning-heavy requests inside a workspace. Treat it as a planning profile that sits in front of implementation work.

## Core Workflow

1. Decide whether the request needs formal planning or can be executed directly.
2. If formal planning is needed, follow the repository `AGENTS.md` rules for PRDs and task lists exactly.
3. Respect any `Workspace Binding Settings` attached to this skill in the active workspace. Those settings override the default planning shape unless the user explicitly asks for something else.
4. Prefer writing enabled planning artifacts into the configured planning directory before implementation.
5. If workspace task sync is enabled and the plan is approved, translate the plan into executable workspace tasks.
6. When implementation starts immediately after planning, use the configured default execution mode unless the user explicitly asks for a different mode.

## Planning Rules

- Ask only the minimum missing questions needed to produce a safe, useful plan.
- Keep the output concrete enough for a junior developer to follow.
- Separate planning from implementation unless the user explicitly wants both in the same session.
- Do not invent requirements that are not supported by the request, workspace context, or repo instructions.
- When a branch is required by workspace settings, check the current branch state before implementation work and call out any mismatch.

## Artifact Rules

- For PRDs: use the repo PRD workflow, save to `/tasks/prd-[feature-name].md`, and stop after drafting unless the user asks to continue.
- For task lists: use the repo task-list workflow, save to `/tasks/tasks-[feature-name].md`, pause after parent tasks when required, and keep progress checkboxes updated during implementation.
- For execution-ready plans: prefer short parent tasks with actionable sub-tasks and clear file references.

## Task-List Results

- When a task result is meant to become executable work, format it as Markdown with a clear task-list heading and checkbox items.
- Use headings for parent/group names and checklist lines for actionable subtasks, for example `- [ ] 1.1 Define scope`.
- Keep caveats, summaries, and rationale outside checklist lines so only actionable work becomes subtasks.
- Add assignees at the end of a checklist item with `@agent-name` only when the assignee is intentional.
