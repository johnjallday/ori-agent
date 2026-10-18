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

### Implementation mode

An authorized builder handoff into an existing feature worktree, or the user's
request to continue its approved implementation, starts this mode. The assignment
is the whole approved scope through an open PR, not only the next unchecked
item. Do not repeat the planning `Go` gate or create another worktree.

Mode comes from the current invocation, not historical planning notes copied into
a checklist. An explicit planning-only bootstrap still wins over the general
continuous execution rules; a saved plan cannot authorize its own implementation.
A later authorized implementation handoff does not inherit the planner's stop.
All worktree, dependency, permission, and scope restrictions still apply.

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
   work before choosing an execution shape. Assess companion dependencies
   below, including whether local plugins need changes; present that decision
   and its evidence with the parent groups.
3. Write only the high-level parent groups, usually about five. Prefer thin
   end-to-end outcomes over architecture layers.
4. Add a recommended model chip to every implementation parent group. The parent
   chip is the default for most of that group; reserve sub-task overrides for
   small deep-reasoning or mechanical pockets.
5. Present the parent groups and wait for `Go` before expanding sub-tasks.

This is the first of two planned checklist pauses. In planning-only mode, do not ask
for an implementation-agent choice: the later handoff owns that decision. In a
direct plan-and-deliver session, honor any agent choice the user supplied;
otherwise use the repository's configured primary agent.

### Detailed checklist

After `Go`:

1. Break every parent into concrete, ordered sub-tasks.
2. Add an `Execution Topology` section that names the integration owner, writing
   rule, delegation lanes, model hotspots, parallel-safe boundaries, and
   validation cadence. Name which of the standard lanes (demo verification,
   scribe, exploration; see _Execution topology and delegation_) apply. Write
   `none` with a reason only for work that is cheaper in the primary context.
3. Add a `Companion Dependencies` section using the assessment below. Include
   concrete companion implementation, integration-test, and delivery tasks when
   needed; identify which Ori steps depend on them. Prefer vertical slices: a
   group should produce a narrow real path through the necessary layers, not
   “all backend” followed by “all frontend.”
4. Order each group for the fastest honest feedback. Consolidate related
   external or live-system unknowns into one early findings-first spike when its
   result will govern multiple later groups.
5. Keep the parent model chip as the group's default. Add a model override to an
   individual sub-task when expensive reasoning or mechanical work is localized;
   do not assign a deep model to routine implementation after the contract is
   settled.
6. End each group with `Commit: "<conventional message>"`. Plan one `Validate:`
   checkpoint per implementation group, naming the affected checks. Put it before
   the `Demo:` item, or directly before the commit when no demo applies. Keep test
   authoring alongside the behavior it covers; do not add suite runs to every
   sub-task. Follow _Validation cadence_ for early risk checks and delivery gates.
7. For every user-visible group, place a `Demo:` item immediately before its
   commit. Scope it to the group's changed path, not a full feature tour.
8. Include the following delivery checkpoints, keeping the merge separate from
   the actions that precede it:
   - `Permission sweep`
   - `Write manual test guide: tasks/test-guide-<feature>.md`
   - Complete required validation, demos, and milestone commits
   - Prepare the PR body with evidence and limitations
   - `PR readiness: verify the gate, then open the PR with wt pr`
   - `Merge: a person reviews the open PR and squash-merges it to dev`
   - `Run wt done <feature> after merge`
9. List relevant implementation and test files.
10. Replace the planning starter with the complete checklist. The final file must
    not contain `ori-devflow: planning-starter`.
11. Keep the checklist reusable by a later builder. Planning history may record
    approval and preparation facts, but must not address the future reader with
    session-specific commands such as “this session stops after saving.” Those
    instructions belong to the planning invocation, not the implementation scope.
    Name task dependencies explicitly; a blocked companion task must not become
    an accidental prerequisite for otherwise independent host work.

Use this shape:

```markdown
## Relevant Files

- `path/file.go` - Why this file is relevant.
- `path/file_test.go` - Tests for the behavior above.

### Notes

- Use the repository's own test commands.

## Instructions for Completing Tasks

Implementation agent: `[configured primary, explicit override, or worktree-only]`

Follow `.agents/skills/task-planning/SKILL.md` for operating mode, continuous
execution, dependency preflight, and PR readiness. The current authorized handoff
supplies the mode; this document defines scope, not permission to start.
Check off each sub-task immediately after it is complete. Record findings, real
test counts, and evidence paths in `tasks/findings-<feature>.md`; this checklist
holds checkbox state only.

## Execution Topology

- Integration owner: `[primary implementation agent]`
- Writing rule: one active writer at a time; the integration owner owns shared files
- Delegation: `[demo verification for every Demo: item; scribe for checkbox ticks, findings log, test guide, and PR body; exploration sweeps at group starts; other bounded findings-only review; or none with a reason]`
- Deep-model hotspots: `[specific unresolved decisions, or none]`
- Parallel-safe work: `[file-disjoint, independently verifiable work, or none]`
- Validation cadence: `[smallest useful checks after coherent changes; one affected-test checkpoint and changed-path demo per group; broad regression and full repository gate at delivery; name any early risk checks]`

## Companion Dependencies

- Assessment: `[required / not required / unresolved]` — `[evidence and reason]`
- For each required or unresolved companion: record source, change scope,
  owner/preparation, validation, and delivery using the canonical skill's
  assessment fields.

## Cross-Repository Contract

Only when a companion is `required`; see _Cross-repository contract_.

- Host features: `[flag → where Ori declares it; which companion manifest requires it]`
- Versions: `[protocol / manifest / blueprint / schema: before → after]`
- Releases: `[companion version to publish; Ori reviewed-floor bump; approval owner]`
- Candidate path: `[companion worktree; harness flag Ori uses to test against it]`
- Companion checklist: `[tasks/tasks-<feature>.md inside that companion worktree]`
- Order: `[contract → candidates → Ori integration → companion PR + release → floor bump → Ori PR]`

## Tasks

- [ ] 1.0 First vertical outcome `Model: Sonnet 5`
  - [ ] 1.1 Concrete implementation step with focused behavior tests
  - [ ] 1.2 Validate: run the named affected package/module checks
  - [ ] 1.3 Demo: drive the changed user-visible path with `wt demo`
  - [ ] 1.4 Commit: "feat(scope): deliver first outcome"
- [ ] 2.0 Final delivery outcome `Model: Sonnet 5`
  - [ ] 2.1 Concrete implementation step
  - [ ] 2.2 Permission sweep
  - [ ] 2.3 Write manual test guide: `tasks/test-guide-<feature>.md`
  - [ ] 2.4 Complete required validation and demos
  - [ ] 2.5 Commit: "test(scope): verify the completed feature"
  - [ ] 2.6 Prepare PR body with evidence and limitations
  - [ ] 2.7 PR readiness: verify the gate, then open the PR with `wt pr`
  - [ ] 2.8 Merge: a person reviews the open PR and squash-merges it to `dev`
  - [ ] 2.9 Run `wt done <feature>` after merge
```

## Companion dependency assessment

Every task list generated through this skill must explicitly assess changes
outside the Ori repository, including local plugins, shared SDKs, and separately
shipped integrations. Use `required`, `not required`, or `unresolved`, with evidence.
A REAPER-related feature does not automatically require a REAPER plugin update:
host-only behavior can be `not required`, while a plugin-owned blueprint,
manifest, prompt, or runtime contract may require coordinated changes. Missing
source or unavailable evidence means `unresolved`, not `not required`.

For each required or unresolved companion, record:

- **Source identity:** canonical repository, observed checkout path and exact
  base revision, and applicable repository guidance. Distinguish editable source
  from installed checkouts, generated copies, and published/reviewed pins; record
  their versions separately when relevant. Do not pick a repository by folder
  name alone. Mark facts not inspected as unverified.
- **Change scope:** affected files/contracts, plugin/blueprint/schema versions,
  host-feature compatibility, and why a host-only change is insufficient. For
  unresolved scope, add an early bounded findings task and name the dependent
  steps it must settle before implementation.
- **Owner and preparation:** companion implementation owner, separately isolated
  worktree/branch and preparation status (`not prepared` until verified), and
  who will arrange its authorized handoff. A proposed path is not a prepared
  worktree; a source seen during planning may not exist in the Ori feature
  worktree. Planning never creates either worktree.
- **Validation:** companion tests and the host-plus-candidate integration path,
  including how disposable demo state receives the exact candidate. Check that
  staging scripts accept the isolated source rather than assuming an installed
  nested checkout exists. Local tests do not prove live-system behavior.
- **Delivery:** ordered Ori/companion commits or PRs, compatibility gates, and
  separately approved release/pin follow-up. Track source changes, local demo
  installation, and published/reviewed release verification as distinct states.
  A successful local candidate is not a production rollout.

Keep the assessment in the task list, not in a second workflow embedded in the
handoff prompt. The default remains one Ori branch/PR; a companion has its own
repository lifecycle. Neither a plan nor an Ori handoff authorizes installing
into the user's plugin store, editing installed plugin sources, publishing or
tagging a release, updating a reviewed pin, or resetting real data.

### Cross-repository contract

When the assessment marks a companion `required`, the task list carries a
`## Cross-Repository Contract` section. It is the only planning text a companion
builder receives besides its own checklist, so it must stand on its own:

- **Host features:** every new host-feature flag, where Ori declares it
  (`internal/plugin/surface_descriptor.go`), and which companion manifest
  requires it.
- **Versions:** the protocol, manifest, blueprint, program, quest, or Home
  schema versions that change, with exact before and after numbers.
- **Releases:** the version each companion will publish and the Ori reviewed
  floor (`internal/reviewedintegration/entries.go`) that follows, as separate
  tasks with their approval owner.
- **Candidate path:** the companion worktree each builder works in and the
  harness flag Ori uses to test against it before any release
  (`scripts/reaper-demo.sh --reaper-source` / `--music-source`,
  `scripts/music-home-demo.sh --source`).
- **Companion checklist:** one `tasks/tasks-<feature>.md` written into each
  companion worktree from this contract. It names only that repository's work
  and the verification Ori will run against the candidate.
- **Order:** contract, companion candidates, Ori integration against the
  candidates, companion PRs and releases, the floor bump as the last Ori commit,
  then the Ori PR.

Each repository gets its own builder session, started inside that repository's
worktree so its own `AGENTS.md` and permissions apply. The Ori builder never
adds a companion directory to its session and never edits a companion checkout;
a companion builder never edits Ori. Only the Ori integration owner changes the
contract. It tells the affected companion session which field changed, and that
session updates its own checklist. A companion candidate becomes evidence for an
Ori task once the owner has built or installed it into disposable demo state;
the published release and the reviewed floor remain later, separately approved
states.

## Implementation dependency preflight

Before initial implementation and on continuation:

1. Read the full planning artifacts, including `Execution Topology` and
   `Companion Dependencies`, before acting on the next unchecked item. The
   handoff's next-item preview can be truncated; it is not the complete scope.
2. Revalidate relevant source identity, revisions, ownership, and preparation
   from the current worktree. For older plans without an assessment, perform a
   bounded read-only assessment and record the result in the active checklist
   (or session findings when no checklist exists). Missing assessment never
   implies `not required`; no new PRD or planning approval loop is needed.
3. Keep the builder's writes inside the worktree authorized by its handoff.
   A companion path in a checklist is evidence, not permission to write there.
   Companion work requires a separately authorized owner/handoff following its
   own repository guidance. Do not create/remove worktrees to repair a missing
   companion when the current handoff forbids it, and never edit an installed
   checkout as a shortcut.
4. If required companion work is unprepared, or evidence leaves a dependency
   unresolved, report the companion, missing preparation/decision, and blocked
   task IDs. Request the separate companion handoff before dependent work;
   continue only explicitly independent in-scope steps. Revalidate the resulting
   candidate and integration evidence before checking off dependent tasks.
5. Keep source implementation, disposable local installation, and production
   release evidence separate through the final report. Do not claim the feature
   is fully activated while a required release or reviewed pin remains blocked.
6. A companion builder is a separate session started inside that companion's
   worktree. It receives the `Cross-Repository Contract` section and its own
   checklist, not the Ori checklist or an added Ori directory, and follows that
   repository's `AGENTS.md`.

This preflight is an agent workflow, not automatic multi-repository worktree
orchestration. Normal independent work adds no human pause; a missing companion
owner, preparation, or authorization is a real execution boundary.

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

### Standard lanes

These lanes exist because the integration owner's context fills with output it
never reads again: screenshots, search results, and re-reads of planning
documents. Each lane returns a short result and writes its full output to
`tasks/evidence-<feature>/` or `tasks/findings-<feature>.md`.

Scratch (probe scripts, CI logs, model transcripts, fixture dumps) goes to the
session scratchpad, never `tasks/`. After the merge, `wt done` archives only the
top-level planning markdown into `tasks/archive/` and drops
`tasks/evidence-<feature>/` and everything else with the worktree, so anything
worth keeping afterwards belongs in the PR body or the findings log.

- **Demo verification:** every `Demo:` item. The lane drives the running demo
  server, captures and inspects the screenshots, and returns a verdict of at
  most 30 lines with evidence paths. Screenshots never enter the owner's
  context. The owner fixes what fails and re-runs the lane for the failed state.
- **Scribe:** checkbox ticks, the append-only findings log, the manual test
  guide, and the PR body. The owner sends a summary of at most five lines per
  sub-task and receives the next open item back, so it never re-reads the
  checklist or PRD. Keep one scribe for the whole feature.
- **Exploration:** "where does this live" sweeps and file maps at the start of a
  group. Findings only, no edits.

Parent groups are never separate agents: implementation is edit-heavy and needs
the owner's accumulated context. A harness without a lane runs that step inline
and still keeps findings out of the checklist.

## Demo checkpoints

A green test suite does not prove that a user-visible surface is reachable or
usable.

For every user-visible group, verify its new or changed path and directly
impacted behavior, not the entire feature again. Keep the complete user journeys
and browser/layout/theme matrix for delivery unless an earlier risk requires them.

1. Use a current build of the branch; rebuild when relevant inputs change.
2. Launch the isolated demo server with `wt demo` (default port 8931), or reuse
   that server only while its build, configuration, and sandbox state remain
   appropriate for the check.
3. Hand the drive to the demo-verification lane: the changed states, their
   click paths, the port, and the evidence directory. Without a lane, drive the
   changed surface yourself in the mode users actually use.
4. Capture a screenshot per expected changed state under `tasks/evidence-<feature>/`.
5. Record the verdict in the findings log and fix failures before committing.

Prefer deterministic fixtures for repeated UI checks. Exercise a real integration
early when its behavior is uncertain, and retain required live acceptance checks
at delivery; fixture evidence does not prove live behavior. Do not repeat paid
model calls or unrelated screenshots merely because a new group began.

Demos are self-checks, not extra approval pauses. Include the screenshot paths
in the final report.

## Validation cadence

Optimize for useful feedback, not checkbox count. This cadence is shared by
Claude, Codex, and Pi; harness-specific entrypoints must not define another one.

- Write focused tests alongside behavior changes. Deferring broad suite execution
  is not deferring test authoring or reducing required acceptance coverage.
- During implementation, run the smallest useful check after a coherent change,
  not mechanically after every sub-task. Batch related mechanical edits before
  checking; use a focused test or module rather than multiple whole packages
  when that gives honest feedback.
- Check risky foundations before dependent work: permissions, data writes,
  concurrency, API contracts, and uncertain external integrations. Name those
  early checks in the plan rather than discovering a broken assumption at delivery.
- At each implementation group boundary, use one `Validate:` checkpoint for the
  affected package/module tests, plus the changed-path demo when applicable.
  Reuse a passing result only while its relevant code, dependencies, fixtures,
  and environment are unchanged; otherwise rerun the affected checks. Do not run
  the same suite twice merely to tick both a sub-task and its parent checkpoint.
- At delivery, run broad regression, complete user journeys, the relevant
  browser/layout/theme matrix, and the full required `make ci-local` gate
  (including ratcheted lint, scoped security, and frontend checks). Keep these
  out of routine group checkpoints unless coupling or an identified risk makes
  earlier coverage necessary; record that reason.
- The full repository gate belongs at delivery, not after each group. Rerun it
  when subsequent changes invalidate its result. This does not bypass `wt pr`'s
  mandatory pre-push checks or authorize `--skip-checks`.
- Scoped checks never replace the final gate. A failed check must be resolved or
  reported as an explicit blocker, not deferred silently to claim a group passed.

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
- Once the PR readiness gate below passes, open the PR with `wt pr` without
  asking. CI then runs while a person reviews the open PR.
- The merge is the human gate. Never enable auto-merge, run `gh pr merge`, or
  push to `dev`; a person reviews the PR and squash-merges it. Changes requested
  on the PR are further commits on the same branch.

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
2. After the PR is open, waiting for a person to review and merge it

In plan-and-deliver mode, create the worktree and then follow the same execution
loop as implementation mode:

1. Select the next authorized, unblocked task from the full scope. A next-item
   preview is a navigation hint, not the assignment's completion boundary.
2. Implement with focused tests, run checks at the _Validation cadence_ above,
   record evidence, and update completed checkboxes.
3. At group boundaries, complete the group's validation checkpoint, self-verify
   the changed-path demo when applicable, and commit.
4. Continue immediately to the next runnable task. Milestones, passing tests,
   demos, commits, and progress summaries are checkpoints, not stop conditions.
   Send progress as non-terminal updates; do not end with “next: X,” “not yet
   PR-ready,” or a request to say “continue” while runnable work remains.
5. When context needs compaction or the session is resumed, use the checklist and
   findings to recover scope, evidence, blockers, and the next action. A context
   checkpoint is not a new human approval gate.

A blocked task is not automatically a blocked feature. Record the exact blocked
task IDs, evidence, and missing decision/credential/authorization; continue only
explicitly independent, authorized work. Do not bypass dependency ordering or
mark blocked tasks complete to advance the checklist. Stop for the blocker only
when no such runnable work remains, or uncertainty invalidates the remaining
scope. Report what the user must supply and what remains unfinished.

Other early stops are user cancellation, an unapproved destructive or
outward-facing action with no independent work left, provider unavailability,
an explicit runtime/budget limit, or repeated failure with no new approach.
Never override cancellation, authorization, or a spending limit to keep running.
Do not invent a per-group stopping point or treat a normal model response ending
as proof that the assigned work is finished.

In planning-only mode, saving the detailed checklist ends the session's assigned
work. Do not cross the worktree boundary.

### PR readiness gate

Before opening the PR, verify all of the following:

- Required implementation and acceptance work is complete; no required dependency
  or acceptance check is silently skipped. Report NOT RUN checks as unresolved,
  not as evidence of readiness.
- Required tests, demos, and the full repository gate (`make ci-local`) passed for
  the current changes. Scope unrelated baseline findings according to AGENTS.md.
- Required milestone commits, the permission sweep, manual test guide, and PR body
  are complete, with real evidence paths and any limitations recorded.

Then open the PR with `wt pr` and end the assigned work with one readiness
summary and the PR URL. `wt pr` runs `make ci-local` first and pushes nothing
on failure; a failed gate is a blocker to fix, never a reason for
`--skip-checks`. The merge and post-merge cleanup items stay unchecked: a
person reviews the PR on GitHub and squash-merges it, and `wt done` runs only
after that. Opening the PR authorizes nothing else: no auto-merge, companion
publication, installation, release/pin changes, or cleanup. Follow the separate
approval and lifecycle rules for those actions.

These are agent instructions, not a runtime supervisor. An optional harness guard
must be explicitly armed for the authorized session/worktree/scope, preserve these
stop conditions, and bound retries/no-progress/spending. Never auto-continue solely
because a checklist contains unchecked merge, release, or cleanup items.

## Ori bindings

- Planning artifacts: `ori-agent-dev/tasks/`
- Issue-derived identity: `<issue-number>-<slug>`
- Planned worktree: `wt start <feature>`
- Ad-hoc worktree: `wt new <name>`
- Isolated demo: `wt demo [port]`
- PR: `wt pr [feature]`, always targeting `dev`
- Cleanup after merge: `wt done <feature>`
- Epic CI: `.github/workflows/ci.yml` accepts `epic/**` as a PR base
- Findings log: `tasks/findings-<feature>.md`; evidence: `tasks/evidence-<feature>/`
- Archive after merge: `tasks/archive/` (planning markdown only, labelled `(done #N)`)
- Claude lanes: the user-level `demo-verifier` and `task-scribe` agents (not
  tracked here) plus the built-in `Explore` agent. Codex and Pi run the lanes
  inline or through Herdr's tester and reviewer roles.
