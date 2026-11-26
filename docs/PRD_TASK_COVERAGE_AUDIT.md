# PRD-to-Task Coverage Audit

## Purpose

Use this audit to confirm that a completed task list fully and accurately
implements its source PRD before Ori creates the feature worktree. The audit is
a planning-quality check, not an implementation or code-review step.

The audit does not create another routine user pause. It runs automatically
after the task list is approved and before it is considered final and
`wt start` runs.

A task list states each parent group's outcome, boundaries, and states, plus one
acceptance demo; it does not list implementation steps (see
`.agents/skills/task-planning/SKILL.md`). The audit checks coverage of the PRD
at that level. It must not add sub-tasks, file lists, or standing steps to make
a requirement look covered.

## Recommended Workflow

1. Generate the PRD with `gpt-5.6-sol` at `xhigh` or `max` reasoning effort.
2. Generate the task list with `gpt-5.6-terra` at `high` effort.
3. Pause for the user's required `Go` confirmation.
4. Audit the PRD and task list with `gpt-5.6-sol` at `high` effort.
5. Correct all safely resolvable coverage gaps in the task-list file.
6. If the audit passes, save and verify the final task list, then run
   `wt start [feature-name]`.
7. Pause only when the audit finds a material product decision that cannot be
   resolved from the PRD, repository, or prior user decisions.

If `gpt-5.6-luna` generated the task list, treat the Sol audit as mandatory. If
Terra generated it, the audit remains a recommended quality gate.

## Required Inputs

- `tasks/prd-[feature-name].md`
- `tasks/tasks-[feature-name].md`
- The repository's `AGENTS.md`
- The current repository code and tests
- Any mockups, decisions, or supporting documents referenced by the PRD

## Audit Criteria

The audit must check that:

1. Every numbered functional requirement maps to a parent group's outcome,
   boundaries, or states, or to the acceptance demo. A group may cite
   requirement numbers instead of restating them.
2. Applicable user stories, design considerations, technical constraints,
   error states, accessibility requirements, and compatibility requirements
   are represented.
3. Groups follow a valid dependency order, and any unknown that governs later
   groups is named as a `Settle first` item.
4. Boundaries and any named entry points are verified against the current
   repository.
5. Each implementation group is a reviewable, demoable outcome, not an
   architecture layer. Sub-tasks appear only where the skill allows them.
6. The acceptance demo covers the user-visible journey, including its refusal
   or no-action path.
7. The delivery group ends with an open PR; the merge to `dev` and
   `wt done [feature-name]` are a separate group that belongs to a person.
8. No group introduces functionality that the PRD lists as a non-goal.
9. Success metrics are not converted into new telemetry work unless the PRD
   explicitly requires telemetry or instrumentation.
10. The final task-list file follows the repository's required structure and
    can be read back successfully from `tasks/`.

## Reusable Audit Prompt

```text
Perform a final PRD-to-task coverage audit.

Inputs:
- PRD: tasks/prd-[feature-name].md
- Task list: tasks/tasks-[feature-name].md
- Repository instructions: AGENTS.md
- Current repository code and tests

Do not implement the feature and do not modify the PRD.

The task list states outcomes, boundaries and one acceptance demo per
.agents/skills/task-planning/SKILL.md. Do not add sub-tasks, file lists,
validation, demo or commit steps to close a gap.

1. Map every numbered functional requirement to a parent group's outcome,
   boundaries or states, or to the acceptance demo.
2. Check that applicable user stories, design considerations, technical
   constraints, error states, accessibility requirements, and compatibility
   requirements are covered.
3. Verify group ordering and dependencies against the current repository, and
   that any unknown governing later groups is a Settle first item.
4. Confirm boundaries and any named entry points by inspecting the code.
5. Confirm every implementation group is a reviewable, demoable outcome and
   that sub-tasks appear only where the skill allows them.
6. Confirm the acceptance demo covers the user-visible journey and its refusal
   or no-action path, the delivery group ends with an open PR, and the merge
   to dev and wt done are a separate group for a person.
7. Identify groups that introduce functionality listed as a non-goal and remove
   or flag them.
8. Do not add telemetry solely because the PRD contains success metrics unless
   telemetry is explicitly required.
9. Amend tasks/tasks-[feature-name].md to fix all safely resolvable gaps.
10. Read the final file back and report:

Verdict: PASS or BLOCKED
Requirements covered:
Gaps corrected:
Unresolved decisions:
Out-of-scope work removed:

If a material gap requires a product decision, return BLOCKED and ask only the
minimum necessary question. Otherwise return PASS.
```

## Expected Result

A successful audit returns `PASS`, summarizes any corrections, and leaves the
final task list ready for `wt start`. A `BLOCKED` result must identify the exact
uncovered requirement or conflicting decision and ask only the minimum question
needed to proceed.
