# Ori–Herdr Devflow Bridge

The Ori–Herdr Devflow Bridge connects Ori's Git-worktree lifecycle to an
existing local Herdr installation. It establishes one narrow mapping:

~~~text
Ori feature Git worktree → Herdr workspace → primary agent + explicit role agents
~~~

Ori remains the authority for Git worktrees, branches, pull requests, archival,
and removal. Herdr remains the authority for terminal panes and attached coding
agent sessions. The bridge is local-only: it does not require the Ori server,
an Ori API key, a browser tab, a cloud scheduler, or an external control plane.

## Setup

The checked-in .herdr/devflow.toml is the opt-in configuration. Its initial
defaults use a builder role with a claude agent kind and require Herdr 0.7.5 or
newer.

~~~bash
source scripts/wt.sh
wt herd setup
wt herd doctor
~~~

Setup copies the helper and plugin files to a stable user-local runtime
directory, then links that stable copy to Herdr. This matters because a feature
worktree can later be removed by wt done; Herdr is never left linked to files
inside that removable checkout. Setup is idempotent.

Setup checks the configured executable and reports optional Claude/Codex
integration commands, but it never installs, rewrites, or enables an agent
integration for you. Install or update an integration explicitly with Herdr if
you choose to use native session restore.

## Starting a feature

After planning artifacts are ready, wt start <feature> creates the Git
worktree as usual and then attempts the Herdr handoff:

~~~bash
wt start herdr-devflow-bridge

# Keep the configured Claude default for other features, but use Codex here.
wt start experimental-codex-flow --kind codex
~~~

The handoff opens the existing checkout, finds an interactive pane, starts the
configured primary agent, and sends a bootstrap prompt that points it to
AGENTS.md, the PRD, and the task list. For a linked Git worktree, Herdr 0.7.5
requires the repository's normal source checkout as parent context. The bridge
resolves that checkout from Git and uses Herdr's existing-worktree open
operation with both paths. Herdr may create or reuse its source/worktree
workspace mapping, but it never creates, switches, or removes a Git worktree.

If a newly opened root pane is still running startup work, the handoff records
its workspace and stops before launching an agent. The completed Git worktree
is still preserved; wait for the shell and run the printed retry command:

~~~bash
wt herd retry --feature herdr-devflow-bridge --worktree /absolute/path/to/herdr-devflow-bridge
wt herd doctor
~~~

The checked-in configuration defaults the primary agent to Claude. Use
wt start <feature> --kind <kind> only when one feature should start with a
different supported Herdr kind. The selected kind is recorded with the handoff,
so a later wt herd retry keeps that choice instead of falling back to the
default.

Use wt start <feature> --no-herdr for a one-run opt-out. A successful retry
completes only missing stages. It does not create a second workspace or primary
agent, and it does not resend an already confirmed bootstrap prompt unless
--resend is supplied.

If an agent is already running in the worktree — someone opened a pane by
hand, or a previous handoff's workspace was closed and reopened — the handoff
adopts it as the primary instead of starting a second agent beside it. No new
flag was needed for this: it is always safe to recognise what is already
there. To run a genuinely additional agent on the same feature, use the
existing explicit `wt herd add <role>` (see below), which has always required
naming a distinct role.

## Selecting the exact agent

Commands default to the primary role in the current managed feature worktree.
Short roles such as builder, reviewer, and tester are feature-scoped, so the
same role may safely exist in several worktrees.

~~~bash
# Current worktree's primary agent.
wt herd prompt "Begin the next incomplete task."

# Explicit role in the current worktree.
wt herd add reviewer --kind claude
wt herd prompt reviewer "Review the current implementation."
wt herd focus reviewer
wt herd read reviewer --lines 160

# Same role, but in a different managed feature.
wt herd prompt builder "Continue after the CI result." --feature another-feature
wt herd status --feature another-feature
~~~

Each managed role has a generated globally unique Herdr name and saved native
session identity when Herdr provides one. The bridge resolves the saved
feature/role/session association first; it does not select an agent merely
because a generic label, terminal title, or current directory matches.

Prompt delivery waits only for Herdr's immediate structured
agent_prompted acknowledgement, bounded by the bridge timeout. It deliberately
does not use Herdr's later-state wait mode: an otherwise healthy idle session
can remain idle after accepting a prompt, which would make that mode report a
false stalled delivery.

--target <live-target> is an explicit recovery escape hatch for prompt, focus,
read, and rebind. Use it only after inspecting wt herd status or herdr agent
list; normal commands should use a feature-scoped role.

~~~bash
wt herd rebind builder --target ori-repo-feature-builder --feature another-feature
~~~

If a saved session is missing or ambiguous, the bridge sends nothing and does
not silently launch a replacement conversation.

## One-time continuations

wt herd continue schedules one prompt for an existing managed agent. It never
starts a new workspace, pane, agent process, or coding-agent session.

~~~bash
# RFC 3339 time with an explicit offset.
wt herd continue --at 2026-07-24T09:30:00-04:00

# Local time in the current system timezone.
wt herd continue reviewer --at "2026-07-24 09:30" \
  --prompt "Re-read the task list and continue the next incomplete item."

wt herd schedule list
wt herd schedule show sch-example
wt herd schedule cancel sch-example
~~~

Before saving, the command displays the normalized absolute time, timezone,
feature, role, agent kind, retry deadline, and a prompt summary. Recurrence
syntax is rejected: v1 supports one-time continuations only.

On macOS, setup registers a user-level LaunchAgent that runs the stable
user-local helper. At the due time, only idle and done agents are eligible.
Busy, blocked, unknown, missing, and unreachable agents are retried within the
configured scheduler.retry_window (15 minutes by default). A late wake after
that window fails rather than delivering unexpectedly.

A schedule is delivered at most once. If Herdr may have accepted the prompt
but the acknowledgement is inconclusive, its state becomes uncertain and the
bridge never retries it automatically. Inspect it and the exact agent before
deciding how to proceed:

~~~bash
wt herd schedule show sch-example
wt herd read reviewer
~~~

The local dispatch service is intentionally macOS-only in v1. The helper still
builds on Linux and Windows, but scheduling commands report that unsupported
platform clearly instead of installing cron, systemd, or Windows scheduling.

## Path is identity, IDs are hints

The bridge resolves a feature's agents by **canonical worktree path**, not by a
saved workspace ID. An agent belongs to a feature when its pane's working
directory is equal to, or inside, that feature's canonical worktree root
(symlinks resolved, `/private/var` normalised, matched only on directory
boundaries — `feature-a` never matches `feature-abc`). Workspace-level
`worktree.checkout_path` is used as a fallback when a pane reports no usable
directory of its own.

A saved bridge record — workspace ID, pane ID, terminal ID, native session —
is retained, but only as a **hint for role attribution** ("this one is the
builder"), never as identity ("is an agent running here"). When the two
disagree, path resolution wins on existence and the saved record wins on
naming. This is why the bridge keeps working across ordinary Herdr use that
used to break it:

- **Closing and reopening a workspace on the same worktree** restores
  recognition immediately. No `wt herd retry`, no rebind, no bridge command in
  between — the next `wt status` simply finds the pane by its directory again.
- **A pane opened by hand**, never started by the bridge, is recognised too —
  see "Unmanaged agents" below.
- **Existing saved records need no migration.** Path is an additive primary
  binding; workspace/pane/terminal IDs are still recorded and used exactly as
  before for role naming, so a record written before this change resolves with
  no re-run of `wt start`.

A saved record whose path no longer resolves is reported as **drift**
(`agent_possible_drift`, `agent_missing`), never as an error, and never blocks
anything by itself.

### Unmanaged agents

`wt status` and `wt herd status` show **every** agent whose working directory
resolves inside a feature's worktree, whether or not the bridge started it. An
agent with no saved bridge record is labelled `unmanaged`, with its live
status and the reason: no bridge record for this worktree. A feature can have
zero, one, or several agents; each renders with its own status, and a feature
summary never collapses several agents into one. Panes with no agent running
still count toward **occupancy** (`wt status` reports "N pane(s) open" when a
worktree is busy but nothing is attributed as an agent) — they are never
rendered as agent rows themselves.

Discovery is strictly diagnostic. Finding an unmanaged agent never creates a
bridge record, writes Herdr metadata, renames, rebinds, starts, stops, or
prompts it — it is reported, not touched. There is deliberately no `wt herd
adopt` in this release; claiming a discovered agent is a distinct, explicit
action for a future release, not something a status query does implicitly.

When two live agents both plausibly match one saved role, the bridge raises
`agent_ambiguous` and chooses neither, rather than guessing.

### The Ori Devflow view shows every agent, same as `wt status`

The "Ori Devflow" view registered inside Herdr carries no filter — it is not
scoped to bridge-managed agents. It shows the same population as `wt status`
and `wt herd status`: every agent Herdr reports, managed or unmanaged. There
is no separate `ori_devflow` metadata token gating which agents appear in it.

## Feature overview

~~~bash
wt status                          # compact, feature-first overview
wt status --feature <slug>         # one feature in detail
wt status --json                   # the complete normalized snapshot
wt status --watch                  # live board
wt status --worktrees              # the legacy Git-only worktree table
~~~

`wt status` answers "what features exist in this repository, and where is each
one" rather than listing Git worktrees. One row per feature, joined on the
exact slug across every source.

### Where the answers come from

| Source | Contributes |
| --- | --- |
| planning | `prd-<slug>.md` and `tasks-<slug>.md` — exact filenames only |
| backlog | `BACKLOG.md` Doing / Shipped / dropped entries (never the Ideas section) |
| worktree | linked checkouts, resolved through the Git common directory |
| git | branch, HEAD, dirty state, ahead/behind versus `dev`, stale baseline |
| github | pull request, base, draft/open/closed/merged, required-check rollup |
| bridge | the saved role records the bridge once bound |
| herdr | live agents, observed status, schedules |

The authoritative planning copy is the one inside the feature's own worktree
while that worktree exists, and the archived copy in `dev` after `wt done`.

### Phases

Phase precedence is by evidence strength, not recency. A merged pull request is
a fact about the repository's history; an unticked task list is somebody
forgetting a checkbox.

| Phase | Means |
| --- | --- |
| `planning` | planning artifacts are incomplete and no worktree exists |
| `ready` | a PRD and task list exist but no worktree does |
| `implementing` | a feature worktree exists on disk |
| `review` | an exact open or draft pull request targets `dev` |
| `merged_cleanup` | merged, but a worktree, `Doing` entry, or unticked archive remains |
| `shipped` | merged and tidy, or recorded shipped in `BACKLOG.md` |
| `dropped` | `BACKLOG.md` records the feature as dropped |
| `unknown` | no evidence was strong enough to place it |

A phase is only marked confirmed when a fresh GitHub query succeeded. Without
one, every phase renders as `(unconfirmed)` — a local-only board cannot tell
"still implementing" from "merged an hour ago".

### GitHub is required

Every complete snapshot needs one fresh authenticated `gh` query. When `gh` is
missing, unauthenticated, timing out, or unreachable, the overview:

- renders every local fact it did observe,
- marks each phase unconfirmed and each remote column `unavailable`,
- prints a prominent `github_unavailable` finding with its recovery command,
- and **exits nonzero**.

That last point is deliberate: a green exit code is how scripts decide nothing
is wrong. Run `wt herd doctor` to check `gh` installation and authentication.

Branch prefix records intent, not size, so `feature/`, `feat/`, `fix/`,
`refactor/`, `docs/`, `test/`, and `chore/` branches are all matched — on the
exact slug after the prefix. A pull request whose head branch does not match a
feature's slug exactly is never attributed to it.

### Findings

Findings are diagnostic. Nothing is repaired, healed, or cleared automatically,
and a later collector never removes a finding raised earlier.

| Severity | Meaning |
| --- | --- |
| `error` | something is ambiguous or broken; the overview refuses to guess |
| `warning` | real drift worth acting on |
| `info` | a gap that is expected at this stage, or bookkeeping lag |

Common codes: `prd_missing`, `task_list_missing`, `plan_malformed`,
`worktree_without_plan`, `name_mismatch`, `backlog_drift`, `archive_stale`,
`archive_missing`, `branch_behind_base`, `worktree_dirty`, `identity_ambiguous`,
`github_unavailable`, `pr_ambiguous`, `pr_unexpected_base`, `pr_closed_unmerged`,
`checks_failing`, `agent_missing`, `agent_ambiguous`, `agent_possible_drift`,
`agent_unmanaged`, `no_agent`, `schedule_failed`, `metadata_stale`,
`herdr_unavailable`, `binding_path_stale`, `worktree_path_collision`.

`binding_path_stale` names a saved bridge record whose worktree path no longer
resolves to a live agent. `worktree_path_collision` names two features whose
canonical worktree paths resolve to the same directory, so an agent cannot be
safely attributed to either — every finding of this kind names the exact
canonical path that produced it.

### Watch cadence and staleness

`--watch` re-reads local evidence on the fast poll interval and queries GitHub
no more often than `status.github_refresh_interval` (default 60s, floor 30s).
After a successful query, a later failure reuses the last good result, labels
it `stale`, and keeps retrying — the board degrades rather than blanking.

## Status board and automation

~~~bash
wt herd status
wt herd status --current --watch
wt herd status --json
wt herd status --clear-view
~~~

`wt herd status` renders the same snapshot as `wt status`, expanded: each
feature followed by its agent rows. Because both surfaces render one snapshot,
they cannot disagree about progress, divergence, pull requests, or agents.

Every agent row separates what the bridge saved from what Herdr currently
reports, and grades the mapping between them:

| Binding | Means |
| --- | --- |
| `exact` | the saved identity resolves to exactly one live agent |
| `possible drift` | one plausible agent matches partially; the detail names each diverging field |
| `ambiguous` | several agents plausibly match; none is chosen |
| `missing` | no live agent matches the saved record |
| `unavailable` | Herdr could not be consulted |

Herdr's idle, working, blocked, done, and unknown states remain authoritative;
task progress is supplemental and is never written back as an agent status. A
saved status is a bridge record, never a live observation — during a Herdr
outage saved values stay visible but observed status reads `unavailable`.

Live agents in a feature's worktree without a bridge role appear as
`unmanaged` (see "Unmanaged agents" above); they are surfaced, never adopted.
A feature worktree with no agent at all says so, because silence would look
identical to a healthy, quietly working agent.

Interactive output uses color and text labels together, and every phase,
availability, binding, and severity also prints as full text — stripping the
styling yields exactly the plain rendering, so color is never the only carrier
of meaning. Color is emitted only when stdout is a terminal, and both NO_COLOR
and --no-color disable it entirely.

| Colour | Means |
| --- | --- |
| green | healthy: passing checks, an exact binding, a clean tree, no findings |
| yellow | needs attention: drift, a behind branch, pending checks, cleanup outstanding |
| red | broken or ambiguous: failing checks, a missing or ambiguous agent, an incomplete snapshot |
| cyan / blue / magenta | active phases (implementing, ready, review) |
| dim | history, absences, and supporting detail | --json emits the versioned machine-readable snapshot
instead, making it suitable for scripts.

The plugin also provides an unfiltered Ori Devflow view and board, rendered
from the same snapshot. Clear it with wt herd status --clear-view; doing so
never changes unrelated user views or metadata. Display metadata is refreshed
only after collection, never as part of it, and is never identity or
semantic-status authority.

### JSON contract

`--json` emits schema version 2 (`overview.Snapshot`). It carries the
generation and GitHub-check timestamps, repository and baseline identity,
overall completeness and staleness, one entry per feature, per-source
availability, and every finding.

Absent, unknown, unavailable, stale, and a real zero are encoded distinctly and
must not be collapsed by a consumer. An unparsed plan reports its availability
and no counts; it never reports `0/0`, which would read as "no work done".
Consumers must tolerate additive fields within a schema version.

Path binding added fields additively within the same schema version 2: each
agent carries `managed` (a saved bridge record exists) and `matched_path` (the
canonical worktree its working directory resolved to); each feature carries
`occupancy` (panes with no agent running, counted but not rendered as agent
rows). None of this required a schema bump.

## Guarded cleanup

wt done calls the bridge guard before task archival, dirty checks, or Git
worktree removal:

~~~bash
wt done herdr-devflow-bridge
~~~

The guard blocks cleanup when **any** agent resolved by path in this
worktree — managed or unmanaged — is `working` or `blocked`, or when a
schedule is pending, waiting, delivering, or uncertain. It names the specific
agent, its observed status, its pane, and the worktree path that matched. It
prints exact focus/read/show/cancel commands. If Herdr cannot be verified,
non-interactive cleanup fails closed — occupancy cannot be established, so the
explicit override remains required.

**What changed:** a saved bridge record that no longer resolves to any live
agent — a workspace closed days ago, a stale pointer — **no longer blocks**
cleanup by itself, because there is nothing running to orphan. Before this,
`wt done` required `--herdr-override` for every stale record, even when
nothing was actually running; now it only blocks when something demonstrably
is. An `idle` or `done` agent in the path does not block either. This is what
let the 2026-07-26 case (a saved builder pointing at a closed workspace, with
the real work running unrecognised in a different workspace) complete cleanly
once the real work finished, with no override.

When every agent in the path is idle or done and no unresolved schedule
remains, the guard closes the matching Herdr workspace and then lets the
existing Ori Git cleanup proceed. It never calls Herdr worktree create/remove.
A recorded workspace that no longer exists needs no closing and does not block
cleanup either — the guard treats "can't confirm the workspace still exists"
(an empty listing, a failed query) as unknown rather than gone, so a Herdr
hiccup can never be misread as permission to skip a close that should have
happened.

The safety check uses an immediate local socket snapshot when Herdr supplies
one. Without that socket, a slow CLI state query is bounded; it fails closed
instead of waiting through live agent activity and then treating the later
settled state as permission to remove the worktree.

wt done <feature> --herdr-override is a recovery tool only for unavailable or
failed workspace-close checks. It cannot override known active agents or
unresolved schedules, and it records an orphan-risk audit event.

### Testing setup against an isolated home

`--home` (or `HERDR_DEVFLOW_HOME`) redirects the helper's runtime root, so a
test run keeps its binary, plugin, state, and logs inside the temporary tree:

~~~bash
SETUP_HOME=$(mktemp -d)
wt herd setup --home "$SETUP_HOME"
~~~

**One thing `--home` does not sandbox:** on macOS, setup also registers the
scheduler LaunchAgent at `~/Library/LaunchAgents/com.ori.herdr-devflow.plist`,
and that path is not affected by the override. A setup run against a temporary
home rewrites the real LaunchAgent to point at the temporary helper, which
breaks scheduled continuations once the temporary directory is removed.

Re-run `wt herd setup` without `--home` afterwards to point it back at the
stable helper.

## Local state, logs, recovery, and removal

The bridge writes state, schedules, stable helper/plugin files, and
logs/events.jsonl under its user-local runtime root. No runtime state belongs
in Git. Audit events contain timestamp, operation, feature/role where
available, stage, outcome, and an optional safe warning. They intentionally
exclude prompt bodies, environment values, credentials, and terminal output.

For recovery, start with:

~~~bash
wt herd doctor
wt herd status --json
wt herd retry --feature <feature> --worktree <absolute-worktree-path>
~~~

To stop future bridge activity without deleting evidence, set
[bridge].enabled = false in .herdr/devflow.toml. On macOS, unload the
user-level com.ori.herdr-devflow LaunchAgent after first canceling or reviewing
pending schedules. Inspect the user-local runtime paths printed by wt herd
setup before removing a plugin link, helper, logs, or state; moving corrupt
state aside is safer than deleting it. Re-running wt herd setup recreates a
stable helper/plugin copy and rehydrates display-only metadata.

## Compatibility and safety contract

The bridge supports and tests the recorded Herdr 0.7.5 structured CLI/socket
contract. Herdr command shapes and response decoding live only in
tools/herdr-devflow/internal/herdr; the wt lifecycle does not parse terminal
tables, ANSI output, titles, or screen text for identity/state. Structured API
errors are decoded from either CLI output stream, since Herdr can place a JSON
error envelope on stderr.

Feature names, roles, agent kinds, schedule IDs, timestamps, metadata tokens,
and canonical linked-worktree paths are bounded and validated. Every external
process receives an argument vector; the bridge does not use eval or build a
shell command from untrusted values.
