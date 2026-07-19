# PRD-to-Task Coverage Audit

## Purpose

Use this audit to confirm that a completed task list fully and accurately
implements its source PRD before Ori creates the feature worktree. The audit is
a planning-quality check, not an implementation or code-review step.

The audit does not create another routine user pause. It runs automatically
after detailed subtasks are drafted and before the task list is considered
final and `wt start` runs.

## Recommended Workflow

1. Generate the PRD with `gpt-5.6-sol` at `xhigh` or `max` reasoning effort.
2. Generate parent tasks with `gpt-5.6-terra` at `high` effort.
3. Pause for the user's required `Go` confirmation.
4. Generate detailed subtasks with `gpt-5.6-terra` at `medium` or `high`
   effort.
5. Audit the PRD and task list with `gpt-5.6-sol` at `high` effort.
6. Correct all safely resolvable coverage gaps in the task-list file.
7. If the audit passes, save and verify the final task list, then run
   `wt start [feature-name]`.
8. Pause only when the audit finds a material product decision that cannot be
   resolved from the PRD, repository, or prior user decisions.

If `gpt-5.6-luna` generated the subtasks, treat the Sol audit as mandatory. If
Terra generated them, the audit remains a recommended quality gate.

## Required Inputs

- `tasks/prd-[feature-name].md`
- `tasks/tasks-[feature-name].md`
- The repository's `AGENTS.md`
- The current repository code and tests
- Any mockups, decisions, or supporting documents referenced by the PRD

## Audit Criteria

The audit must check that:

1. Every numbered functional requirement maps to one or more implementation
   and validation subtasks.
2. Applicable user stories, design considerations, technical constraints,
   error states, accessibility requirements, and compatibility requirements
   are represented.
3. Tasks follow a valid dependency and implementation order.
4. Relevant source and test files are plausible and verified against the
   current repository.
5. Each tested, reviewable parent group ends with validation and a conventional
   commit checkpoint.
6. User-visible work includes a manual `Demo:` checkpoint before PR creation.
7. Final delivery includes PR validation, squash-merge to `dev`, and
   `wt done [feature-name]` after merge.
8. No task introduces functionality that the PRD lists as a non-goal.
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

1. Map every numbered functional requirement to one or more implementation
   and validation subtasks.
2. Check that applicable user stories, design considerations, technical
   constraints, error states, accessibility requirements, and compatibility
   requirements are covered.
3. Verify task ordering and dependencies against the current repository.
4. Confirm relevant source and test files are plausible by inspecting them.
5. Confirm every reviewable parent group includes validation and a
   conventional commit checkpoint.
6. Confirm final delivery includes the required demo when user-visible, PR
   validation, squash-merge to dev, and wt done.
7. Identify tasks that introduce functionality listed as a non-goal and remove
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
