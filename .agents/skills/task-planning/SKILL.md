---
name: task-planning
description: Create Ori PRDs and implementation task lists, or execute an approved checklist through its pull-request gate. Use when asked for a PRD, specification, task breakdown, implementation plan, or when continuing a tasks-*.md checklist.
---

# Task Planning

This is the canonical planning protocol for every agent harness used in this
repository. Do not copy these rules into `AGENTS.md`, bootstrap prompts, or
starter checklists; those surfaces should link here and carry only run-specific
state.

PRDs and task lists are both opt-in:

- A **PRD** is useful when the team does not yet know exactly what to build. Its
  most valuable output is the clarification round.
- A **task list** is useful when agreed work spans several files or surfaces and
  benefits from resumable checkpoints, demos, commits, and a delivery gate.
- Work whose shape is already agreed skips both and starts in an isolated
  worktree.

Implementation always happens in its own worktree, never in the shared
`ori-agent-dev` planning worktree.

## Operating modes

Determine the mode from the instruction that invoked this skill.

### Planning-only mode

`wt plan --issue <N>` launches this mode. Read the Issue snapshot named by the
bootstrap prompt and write the requested planning artifacts in
`ori-agent-dev/tasks/`. Never implement, create a branch, create a worktree, or
run `wt start`. Stop after the detailed task list is saved; a person or a
separate handoff action starts implementation.

### Plan-and-deliver mode

Use this mode only when the user directly asked the current session to carry the
work through implementation. After the detailed task list is approved and saved,
create the feature worktree with `wt start <feature>` and continue there. Work
that intentionally skips planning starts with `wt new <name>`.

An explicit planning-only bootstrap always wins over the general continuous
execution rules below.

## Part 1: PRD

Generate a PRD only when explicitly requested or when an Issue is routed as
`size:prd`.

1. Read the request and available repository evidence.
2. Ask only the 3–5 most important questions whose answers cannot reasonably be
   inferred. Ask about the problem, user outcome, scope boundaries, and success
   criteria—not implementation trivia.
3. Number questions and letter suggested answers so the user can reply with
   `1A, 2C`. Free-form answers remain valid.
4. Wait for the answers.
5. Write `tasks/prd-<feature>.md`.
6. Do not implement from the PRD.

Use this structure:

1. Introduction / overview
2. Goals
3. User stories
4. Numbered functional requirements
5. Non-goals / out of scope
6. Design considerations, when relevant
7. Technical constraints and integrations, when relevant
8. Success metrics
9. Open questions

Write for a junior developer: explicit, testable, and free of unexplained
jargon.

## Part 2: Task list

Generate a task list only when explicitly requested or when a `wt plan` starter
requests one. Approved Ori Plans are exported by compiled application code, not
by invoking this skill a second time.

### Planning gate

1. Read the requirements, PRD, approved Ori Plan, and relevant repository
   evidence.
2. Identify uncertainty hotspots, external or live-system questions, repeated
   implementation patterns, shared-file contention, and genuinely independent
   work before choosing an execution shape.
3. Write only the high-level parent groups, usually about five. Prefer thin
   end-to-end outcomes over architecture layers.
4. Add a recommended model chip to every implementation parent group. The parent
   chip is the default for most of that group; reserve sub-task overrides for
   small deep-reasoning or mechanical pockets.
5. Present the parent groups and wait for `Go` before expanding sub-tasks.

This is the first of exactly two human pauses. In planning-only mode, do not ask
for an implementation-agent choice: the later handoff owns that decision. In a
direct plan-and-deliver session, honor any agent choice the user supplied;
otherwise use the repository's configured primary agent.

### Detailed checklist

After `Go`:

1. Break every parent into concrete, ordered sub-tasks.
2. Add an `Execution Topology` section that names the integration owner, writing
   rule, bounded delegation lanes, model hotspots, parallel-safe boundaries, and
   validation cadence. Use `none` rather than inventing delegation for work that
   is cheaper in the primary context.
3. Prefer vertical slices. A group should produce a narrow real path through the
   necessary layers, not “all backend” followed by “all frontend.”
4. Order each group for the fastest honest feedback. Consolidate related
   external or live-system unknowns into one early findings-first spike when its
   result will govern multiple later groups.
5. Keep the parent model chip as the group's default. Add a model override to an
   individual sub-task when expensive reasoning or mechanical work is localized;
   do not assign a deep model to routine implementation after the contract is
   settled.
6. End each group with `Commit: "<conventional message>"`.
7. For every user-visible group, place a `Demo:` item immediately before its
   commit.
8. End the final delivery group with:
   - `Permission sweep`
   - `Write manual test guide: tasks/test-guide-<feature>.md`
   - `Open PR → squash-merge to dev`
   - `Run wt done <feature> after merge`
9. List relevant implementation and test files.
10. Replace the planning starter with the complete checklist. The final file must
    not contain `ori-devflow: planning-starter`.

Use this shape:

```markdown
## Relevant Files

- `path/file.go` - Why this file is relevant.
- `path/file_test.go` - Tests for the behavior above.

### Notes

- Use the repository's own test commands.

## Instructions for Completing Tasks

Implementation agent: `[configured primary, explicit override, or worktree-only]`

Check off each sub-task immediately after it is complete. Execution is continuous
between the planning gate and the final PR gate.

## Execution Topology

- Integration owner: `[primary implementation agent]`
- Writing rule: one active writer at a time; the integration owner owns shared files
- Delegation: `[bounded findings-only research/review, sequential test/docs edits, or none]`
- Deep-model hotspots: `[specific unresolved decisions, or none]`
- Parallel-safe work: `[file-disjoint, independently verifiable work, or none]`
- Validation cadence: `[scoped checks per slice; affected suites per group; full repository gate once at delivery]`

## Tasks

- [ ] 1.0 First vertical outcome `Model: Sonnet 5`
  - [ ] 1.1 Concrete implementation step
  - [ ] 1.2 Demo: drive the user-visible result with `wt demo`
  - [ ] 1.3 Commit: "feat(scope): deliver first outcome"
- [ ] 2.0 Final delivery outcome `Model: Sonnet 5`
  - [ ] 2.1 Concrete implementation step
  - [ ] 2.2 Permission sweep
  - [ ] 2.3 Write manual test guide: `tasks/test-guide-<feature>.md`
  - [ ] 2.4 Open PR → squash-merge to dev
  - [ ] 2.5 Run `wt done <feature>` after merge
```

## Slicing and build order

- Default to vertical slices that are independently demoable.
- Front-load a single bounded spike when several groups depend on the same
  uncertain external API, hardware behavior, or architectural contract. Record
  observed findings before product implementation continues.
- Within one branch, frontend-first prototyping is allowed when UX is the
  uncertainty. Mark stub-backed checks as `Prototype demo`; wire the real path
  before the delivery demo.
- Never merge a mock-backed frontend to `dev`.
- Backend-only groups may replace a browser demo with a cheap endpoint or CLI
  exercise.

## Execution topology and delegation

Adding agents is not itself a cost optimization: every separate context pays a
repository-reading and coordination tax. Optimize first by routing the smallest
uncertain unit to the right model.

1. Keep one primary integration owner for a feature branch.
2. Prefer a model switch in the primary session when the work is short and the
   existing context is valuable.
3. Use a short-lived subagent for a bounded research spike, test matrix, focused
   review, or other output that can return as findings without editing shared
   product files.
4. Use a persistent separate role only for a genuinely independent long-running
   workstream or repeated review responsibility.
5. Route deep models to specific decision-bearing tasks. Once the contract or
   pattern is settled, return implementation to the balanced or fast tier.
6. Allow only one active writer in a shared worktree. Delegated edits must be
   sequential and explicitly file-bounded; concurrent coding requires separate
   branches, file-disjoint ownership, independent verification, and a planned
   integration point.
7. Keep reviewers and testers diff-focused. Give them the goal, changed files,
   constraints, acceptance criteria, and expected output instead of asking them
   to rediscover the whole feature.
8. The integration owner synthesizes delegated findings and remains responsible
   for checklist updates, demos, commits, and the delivery gate. Delegation adds
   no human pause.

## Demo checkpoints

A green test suite does not prove that a user-visible surface is reachable or
usable.

For every user-visible group:

1. Build the branch.
2. Launch the isolated demo server with `wt demo` (default port 8931).
3. Drive the new surface in the browser in the mode users actually use.
4. Capture a screenshot.
5. Fix failures before committing.

Demos are self-checks, not extra approval pauses. Include screenshots in the
final report.

## Validation cadence

- After a sub-task or narrow slice, run the smallest affected package, module, or
  focused test that can disprove the change quickly.
- At a parent-group boundary, run the affected package and frontend suites before
  committing.
- Run the repository's complete required test, ratcheted lint, scoped security,
  and relevant frontend gates in the final delivery group.
- Do not replace the final gate with scoped checks, and do not repeatedly run the
  full repository suite after every mechanical sub-task unless coupling makes it
  necessary.

## Manual test guide

Write the guide after implementation, so it reflects the code that exists. Keep
it short and include:

1. How to run this branch and its isolated demo
2. Exact navigation path to the feature
3. Setup and required permissions
4. Numbered golden-path steps with expected results
5. Negative and edge cases with expected results
6. Explicitly out-of-scope behavior

## Permission sweep

Before the PR gate:

1. Recall commands that repeatedly triggered permission prompts.
2. Move reusable compound shell into a checked-in script.
3. Prefer existing entry points such as `wt demo` and `scripts/e2e.sh`.
4. Propose an allowlist entry only for a new stable entry point—never for a PID,
   line number, temporary path, or session-specific command.
5. Report the result, including a clean sweep.

## Commits, PRs, and epics

The default is one feature, one branch, and one PR targeting `dev`.

- Commit once per completed parent group, not once per sub-task.
- The second and final human pause is immediately before `wt pr` / PR creation.
- After approval, open the PR and squash-merge it to `dev`.

For work too large for one reviewable PR, default to one long-lived
`epic/<name>` branch with one final `epic → dev` PR. Merge `origin/dev` into the
epic at group boundaries; never rebase a shared epic. Optional sub-PRs may target
the epic for isolated review.

Split directly into serialized PRs to `dev` only when every slice is:

1. File-disjoint
2. Independently shippable and verifiable
3. Too risky to land together

## Model recommendations

Choose by uncertainty, not line count:

- **Haiku 4.5** — mechanical changes with a complete pattern
- **Sonnet 5** — normal well-scoped implementation
- **Opus 5 / Fable 5** — ambiguous, cross-cutting, architectural, concurrent,
  performance-sensitive, or security-sensitive work

When model names change, map these to fast, balanced, and deep-reasoning tiers.
A separate agent on the same model is usually more expensive than a model switch
because it must rebuild context. Use separate contexts for isolation or durable
independent work, and subagents for narrow outputs. Budget the deep tier around
unresolved decisions rather than whole parent groups whenever possible.

## Continuous execution

There are exactly two planned pauses:

1. After parent groups, waiting for `Go`
2. Before opening the PR

In plan-and-deliver mode, everything between those gates runs continuously:
create the worktree, implement each group, update checkboxes, demo, test, commit,
and continue. Stop early only for a real ambiguity, invalidated plan, unapproved
destructive or outward-facing action, missing user credential/decision, or a
step that has failed repeatedly with no new approach.

In planning-only mode, saving the detailed checklist ends the session's assigned
work. Do not cross the worktree boundary.

## Ori bindings

- Planning artifacts: `ori-agent-dev/tasks/`
- Issue-derived identity: `<issue-number>-<slug>`
- Planned worktree: `wt start <feature>`
- Ad-hoc worktree: `wt new <name>`
- Isolated demo: `wt demo [port]`
- PR: `wt pr [feature]`, always targeting `dev`
- Cleanup after merge: `wt done <feature>`
- Epic CI: `.github/workflows/ci.yml` accepts `epic/**` as a PR base
