# Ori–Herdr Devflow Bridge

The Ori–Herdr Devflow Bridge connects Ori's Git-worktree lifecycle to an
existing local Herdr installation. It establishes one narrow mapping:

~~~text
Ori feature Git worktree → tab in the focused Herdr workspace → primary agent + explicit role agents
~~~

A feature is a **tab**, not a workspace. Several features live side by side in
the workspace you are already working in, and cleanup closes only the feature's
own tab.

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
AGENTS.md, the PRD, and the task list.

The handoff resolves the **currently focused** Herdr workspace and creates the
feature's tab inside it, labelled with the feature slug. It never opens a
worktree as a workspace of its own, and it never creates, switches, or removes a
Git worktree. Focus is sampled when the handoff runs, so the tab lands wherever
you are at that moment.

Unlike opening a worktree, creating a tab is not idempotent, so a retry
rehydrates the tab already recorded for the feature — or adopts the tab of an
agent already running in the worktree — rather than adding another one beside
it.

A brand-new tab's shell needs a moment before it is usable; the handoff waits
for it to settle rather than mistaking a starting shell for a busy pane. If it
never settles, the completed Git worktree is still preserved; wait for the shell
and run the printed retry command:

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

An ad-hoc feature created with `wt new` has no PRD and no task list, so it gets
a tab and an agent but **no bootstrap prompt** — there is nothing truthful to
point one at. That decision is recorded on the feature, so retries and
`--resend` do not talk it round.

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

# Opt in to a coordinated macOS wake shortly before the due time.
wt herd continue builder --feature another-feature \
  --at "2026-07-24 05:00" --wake

wt herd schedule list
wt herd schedule show sch-example
wt herd schedule cancel sch-example
~~~

Before saving, the command displays the normalized absolute time, timezone,
feature, role, agent kind, retry deadline, and a prompt summary. Recurrence
syntax is rejected: v1 supports one-time continuations only.

`--wake` asks the separately installed **Herdr Wake Service** to make the
machine awake by the due time. It is deliberately opt-in because waking the
whole Mac is a system-wide side effect. The command does not report success
until the service directly verifies the exact fixed-owner `pmset` event. Install
and diagnose it with `wt herd wake install` and `wt herd wake doctor`; no Ori
server, Device Capabilities setting, or Ori runtime is required. If registration
or confirmation fails, the continuation is marked failed and cannot prompt
later as an ordinary non-waking schedule.

The wake candidate is scoped to the continuation schedule. Delivery, failure,
or cancellation withdraws only that candidate; workspace-task and Overnight
Run wakes are preserved. Ori may program a small lead before the requested
time so macOS, networking, Herdr, and the saved agent session are ready, but
the continuation prompt is still withheld until the original due time.

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

## Overnight Runs

A one-time continuation sends one prompt and stops. An **Overnight Run** watches
an ordered queue of Claude agents for hours, and — when the active one reaches
its included five-hour session limit — puts this Mac to sleep until Claude's
own reported reset, wakes it, and continues the same conversation.

That last part is why every command below is explicit about consequences. The
run cannot start without you reading a summary and answering it.

~~~bash
# Plan a run over exactly the agents you name, in the order you name them.
wt herd overnight start --agent blueprint-setup-wizards --agent another-feature \
  --start 23:00 --deadline 07:00 --timezone America/New_York

# Keep the Mac awake with a verified run-owned idle-sleep assertion. This mode
# never deliberately sleeps or registers a reset wake.
wt herd overnight start --agent blueprint-setup-wizards --stay-awake

# See what it would do without creating anything.
wt herd overnight start --agent blueprint-setup-wizards --dry-run

wt herd overnight list
wt herd overnight show            # the active run
wt herd overnight watch           # the active run, refreshed
wt herd overnight report          # the morning summary
wt herd overnight cancel
~~~

There is no "enrol every agent" option. The set of agents controlled while you
are asleep is a decision, not a default.

### What the run may and may not do

An enrolled agent may implement code, update tests and documentation its plan
calls for, run the validations the plan lists, and make the milestone commits
the plan already requires. It stops before anything else.

Specifically, it stops before a `Demo:`, a design sign-off, anything needing
credentials or external authorization, `Open PR`, a merge, a deploy, a release,
and `wt done`. A checkpoint the parser does not recognize is treated as manual,
because reading manual work as safe means an agent opens a PR overnight, while
reading safe work as manual means the run stops early and tells you why.

Order matters as much as classification. If a demo sits between the agent and
the next implementation task, there is no safe next task — the run does not
reach past it.

### Selection, ordering, and the queue

Agents run **one at a time**, in the order you listed them. The next agent
starts only when the current one completes or stops for a reason that is not a
usage limit. If the active agent hits its limit, it keeps the queue head across
the sleep: Claude's allowance is shared between your sessions, so promoting
somebody else would just consume the same exhausted window.

A run refuses to start when it cannot control something safely:

| Refusal | Why |
| --- | --- |
| the agent is not eligible | see readiness below |
| two agents in one worktree | two autonomous agents editing one checkout cannot be untangled |
| two selected agents already working | the supervisor cannot tell which one it is watching |
| an unresolved continuation for that session | two plans aiming a prompt at one conversation |
| another active run | two supervisors each believing they own the queue |

### Claude readiness, and why an agent may be ineligible

Overnight Runs are Claude-only in v1, and they use **included plan capacity
only**. They never accept an extra-usage offer, never switch to API-key
billing, and never spend credits.

Ori establishes that positively rather than assuming it. Claude Code reports
its subscription rate-limit windows only for Claude.ai Pro and Max sessions,
so their presence *is* the proof; their absence means an API-key session, an
unsupported version, or a session that has not called the API yet — all
ineligible, all left awake.

Reading those windows needs a small recorder in your own Claude configuration.
Ori prints it and never installs it:

~~~bash
wt herd claude-usage install    # prints the settings; changes nothing
wt herd claude-usage status     # reports whether records are being written
~~~

The printed `statusLine` entry **wraps** whatever status line you already have
and forwards its output unchanged, so installing this does not cost you the one
you wrote. The recorder stores window state and session identity only: no
prompts, transcripts, paths, or costs.

Until it is installed, every agent reports `overnight: not eligible` with the
reason, and no run can be created. That is deliberate.

### Deadlines and the resume ceiling

Every run has an absolute morning deadline and a maximum number of resumes,
three by default.

- At or after the deadline: no new prompt, no new wake. An agent that is
  already working is **observed to a stop, never interrupted** — the run reports
  `overrun` and lets the turn finish.
- A reset that falls at or after the deadline ends the run with
  `deadline_reached` rather than scheduling a wake it could not use.
- A resume is consumed only when a post-reset continuation is **acknowledged by
  the exact session**. Scheduling a wake, waking early, and an unconfirmed
  delivery all cost nothing.
- When the ceiling is used up, the next limit ends the run with
  `cycle_limit_reached` instead of sleeping again.

Deadlines are wall-clock times in the zone you chose, so a 23:00 → 07:00 run
still ends at seven in the morning on the night the clocks change.

### Sleeping this Mac

**A verified included-session limit puts the whole machine to sleep.** Every
other process on it is suspended by macOS until the wake. Unsaved work in other
applications is your responsibility.

Before that happens, all of these must hold — and each refuses independently,
with a reason you can read in `wt herd overnight show`:

- macOS, and a healthy standalone Herdr Wake Service
- external power (unknown counts as battery)
- an exact native Claude session to return to
- a reset that is in the future, before the deadline, and newer than any this
  participant already handled
- remaining resume budget
- a wake that the standalone service has **programmed and confirmed**, not merely requested

That last one matters most. The standalone daemon owns exactly one fixed-owner
macOS wake event and arbitrates Herdr continuation and Overnight candidates.
The unprivileged helper cannot run `pmset`; it uses authenticated local IPC and
sleeps only after direct read-back verification. If the service is unavailable,
the Mac stays awake.

Cancelling a run withdraws only that run's wake candidate; a scheduled workspace
task's wake is recomputed and preserved.

### After the wake

Being awake proves nothing — you may have opened the lid. Durable state decides:

- **before** the reset: the run waits, and prompts nobody
- **after** the deadline: the run ends without even restoring the session
- **in between**: the exact recorded session is revalidated and continued once

If that session is gone or ambiguous, the run stops. It never creates a
replacement conversation to talk to instead.

### Reading it in the morning

~~~bash
wt herd overnight report
~~~

The report gives the queue in the order you confirmed it, what each agent
completed against where it started, the commits and Git state, every limit,
sleep and wake, anything uncertain, and what to do next. It says an
implementation boundary was completed — never "shipped" or "merged", because
the run never crossed a delivery checkpoint.

Anything the run could not establish either way is listed under **Uncertain**
rather than resolved optimistically. A continuation that was in flight when the
run ended may have arrived; check the agent before prompting it again.

### When it will not sleep

`wt herd doctor` reports each requirement separately, because their fixes
differ:

~~~
Claude usage recorder    records are being written to …
Claude overnight readiness  1 saved Claude session reports plan-backed capacity
wake service             the standalone socket, protocol, UID, and self-test are healthy
wake event               the exact fixed-owner event is directly verified
power source             this Mac is on external power
~~~

A failed check disables only what depends on it. Missing wake approval stops
sleeping; it does not hide the agent roster.

Setting `[bridge] enabled = false` stops new Overnight actions entirely while
leaving existing runs inspectable.

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

`wt herd status` shows **every open agent Herdr reports**, whether or not the
bridge started it. It does not require or display a saved bridge role.

The broader `wt status` feature overview also discovers live agents whose
working directory resolves inside a feature worktree. There, an agent with no
saved bridge record is labelled `unmanaged`, and saved/live identity is
compared for diagnostics. A feature can have zero, one, or several agents.
Panes with no agent running still count toward **occupancy** in `wt status`;
they are never rendered as live-agent rows themselves.

Discovery is strictly diagnostic. Finding an unmanaged agent never creates a
bridge record, writes Herdr metadata, renames, rebinds, starts, stops, or
prompts it — it is reported, not touched. There is deliberately no `wt herd
adopt` in this release; claiming a discovered agent is a distinct, explicit
action for a future release, not something a status query does implicitly.

When two live agents both plausibly match one saved role, the bridge raises
`agent_ambiguous` and chooses neither, rather than guessing.

### The Ori Devflow board uses the feature overview

The "Ori Devflow" board registered inside Herdr renders the expanded
feature/delivery snapshot used by `wt status`. It may therefore include saved
binding diagnostics and feature history that the deliberately smaller
`wt herd status` live roster omits.

## Issues and the backlog

`./scripts/devops.sh` is the repository's small Issue REPL. With no arguments it
lists every open Issue before prompting for another view.

~~~bash
./scripts/devops.sh                    # all open Issues, then the REPL
./scripts/devops.sh ready              # proposals + unbundled, unapproved backlog
./scripts/devops.sh all                # all open Issues, one-shot
./scripts/devops.sh decisions          # label: needs-decision
./scripts/devops.sh backlog            # label: backlog
./scripts/devops.sh proposals          # label: feature-proposal
./scripts/devops.sh status             # which group each task list is on
./scripts/devops.sh view <number>      # one Issue in full
./scripts/devops.sh new <title>        # capture, confirm-gated
./scripts/devops.sh answer <n> <text>  # comment, confirm-gated
./scripts/devops.sh approve <n>        # add `approved`, confirm-gated
~~~

In a terminal, the colorful picker accepts `↑/↓` or `j/k` to select an Issue,
`←/→` or `h/l` for those five list views, `Enter` to inspect it, `n` to capture a
new Issue, `c` to answer its open questions, `o` to approve it, `r` to refresh,
and `q` to quit. In a pipe or redirected shell, the line REPL accepts `1/a`,
`2/d`, `3/b`, `4/f`, and `5/y`, plus `v <number>`, `n <title>`,
`c <number> <text>`, and `ok <number>`. Lists include every author and only open
Issues. Filters are literal labels; no Project board or rank participates.

Reads never mutate. The write commands cover the three things only a human does
here — capturing an idea, answering a spec's open questions, and setting
`approved`, the one label the grooming routine may never write. All confirm
first, and refuse without a terminal unless given `--yes`. A captured Issue gets
no labels: it must reach the grooming routine untriaged so the spec step runs.

`status`, and the picker's in-flight column, are the one part of the REPL that
overlaps this document's subject — and they deliberately do **not** call Herdr.
`scripts/wt-herd.test.sh` fails if `scripts/devops.sh` so much as mentions
`wt_herd`, `herdr-devflow`, or the retired bootstrap, because shedding that
dependency is why the REPL exists. Instead they read `git worktree list`,
`git branch --all`, and the task files on disk, resolving an Issue to work via
the naming convention: branch `fix/339-slug`, task file `tasks-339-slug.md`.
Branches predating the number-first convention resolve by slug.

Task files are gitignored and shared out of the dev worktree's `tasks/`, never
pushed, so progress reflects ticked checkboxes immediately rather than at the
next commit. `wt status` remains the richer, Herdr-backed feature/delivery
snapshot; `devops.sh status` is the cheap local glance.

The command runs `gh issue list`, `gh issue view`, `gh issue create`,
`gh issue comment`, or `gh issue edit` directly from its checkout. `status`
contacts GitHub not at all.
The terminal picker fetches the complete open-Issue index once and filters it
locally until `r` refreshes it; it does not persist a cache, source `wt`, invoke
the Herdr helper, or define a JSON contract. Agents that need structured data
should use `gh issue list --json` directly. Capture likewise remains the GitHub
CLI's job:

~~~bash
gh issue create --title "<title>" --body "<optional context>"
gh issue list --state open --limit 1000 --json number,title,author,labels,url,createdAt,updatedAt
~~~

The product backlog is GitHub Issues. There is no backlog file, no cache, and no
backlog commit: every invocation performs one fresh authenticated query, and a
failure is reported as a failure rather than as an empty backlog — the one wrong
answer that looks like a right one.

The script resolves the repository from its own checkout, so it behaves the
same when invoked from a nested directory or another current working directory.
`view` accepts one positive Issue number. Exit `2` means invalid input; a failed
GitHub call preserves `gh`'s status.

**Removed, and not coming back as a local file:** `sync` and `prune` (GitHub is
live and keeps its own history). **Not implemented
yet, deliberately:** any command that changes an Issue's state — promote, ship,
drop, close, select — and any Project operation. `wt start`, `wt pr`, and
`wt done` do not read, update, or close an Issue either. Daily selection stays
manual in GitHub until enough of it has been done by hand to know what the
command should be.

New Issue-backed work uses `<issue-number>-<slug>` as its feature identity; see
"Feature Naming: Issue Number First" in `AGENTS.md`.

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
| worktree | linked checkouts, resolved through the Git common directory |
| git | branch, HEAD, dirty state, ahead/behind versus `dev`, stale baseline |
| github | pull request, base, draft/open/closed/merged, required-check rollup |
| bridge | the saved role records the bridge once bound |
| herdr | live agents, observed status, schedules |

The authoritative planning copy is the one inside the feature's own worktree
while that worktree exists, and the archived copy in `dev` after `wt done`.

There is no backlog source. The repository's backlog is GitHub Issues, and an
Issue nobody has planned yet has no PRD, no branch, and no worktree — it would
be a row describing nothing. `wt status` describes selected and executing work;
`./scripts/devops.sh` reads every captured idea and offers the curated
`needs-decision`, `backlog`, and `feature-proposal` label views.

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
| `merged_cleanup` | merged, but a worktree or an unticked archived plan remains |
| `shipped` | merged, with no local cleanup outstanding |
| `unknown` | no evidence was strong enough to place it |

A merged pull request is the only thing that can call a feature delivered. There
is deliberately no `dropped` phase: the only source that ever reported one was a
hand-maintained file, and a closed Issue does not mean abandoned work, so nothing
infers it. Restoring a dropped state needs a source that actually means it.

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
`worktree_without_plan`, `name_mismatch`, `archive_stale`,
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

## Live agent status

~~~bash
wt herd status
wt herd status --current --watch
wt herd status --json
wt herd go                       # select and focus an open agent
wt herd overview                 # compatibility alias
wt herd status --clear-view
~~~

`wt herd status` answers only "which coding agents are open right now?" Its
human output has four columns: agent, kind, Herdr's live status, and worktree.
It reads Herdr's live `agent.list` result and does not join plans, saved bridge
records, Git state, GitHub pull requests, Issues, schedules, or Overnight
eligibility.

With no selector it lists every open Herdr agent. `--current` narrows to the
current checkout, `--worktree PATH` narrows by canonical path, and `--feature
NAME` resolves that feature's linked worktree before filtering. `--watch`
polls the same live list. An empty successful result prints `No open agents.`
and exits 0; failure means Herdr itself could not be queried. If direct local
socket access is unavailable, the adapter falls back to the structured
`herdr agent list` CLI operation.

`wt herd go` presents this same live roster as a numbered interactive picker
and asks Herdr to focus the selected agent. It works as a direct command or as
`herd go` inside the `wt` REPL. The picker includes unmanaged agents and agents
without a saved bridge role; it uses the exact live Herdr name or pane ID shown
by the roster. Enter `q` or press Return to cancel. Because selection and focus
require a terminal, the command refuses non-interactive use and does not support
`--json`.

`--json` emits the same narrow contract:

~~~json
{
  "agents": [
    {
      "agent": "ori-example-builder",
      "kind": "claude",
      "status": "working",
      "worktree": "/absolute/path/to/example"
    }
  ]
}
~~~

`wt herd overview` is retained as a compatibility alias for this roster.
`wt herd status --clear-view` remains an explicit cleanup operation for an old
source-scoped Herdr view; ordinary status reads do not refresh metadata or
write bridge state.

### Feature-overview agent diagnostics

`wt status` and the Herdr board still compare saved bridge identity with live
Herdr observations and grade the mapping:

| Binding | Means |
| --- | --- |
| `exact` | the saved identity resolves to exactly one live agent |
| `possible drift` | one plausible agent matches partially; the detail names each diverging field |
| `ambiguous` | several agents plausibly match; none is chosen |
| `missing` | no live agent matches the saved record |
| `unavailable` | Herdr could not be consulted |

Herdr's idle, working, blocked, done, and unknown states remain authoritative
there; task progress is supplemental and is never written back as an agent
status. A saved status is a bridge record, never a live observation.

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
| dim | history, absences, and supporting detail |

The plugin also provides the expanded Ori Devflow feature board. Clear its
source-scoped view with `wt herd status --clear-view`; doing so never changes
unrelated user views or metadata.

### Feature-overview JSON contract

`wt status --json` emits schema version 3 (`overview.Snapshot`). It carries the
generation and GitHub-check timestamps, repository and baseline identity,
overall completeness and staleness, one entry per feature, per-source
availability, and every finding.

Absent, unknown, unavailable, stale, and a real zero are encoded distinctly and
must not be collapsed by a consumer. An unparsed plan reports its availability
and no counts; it never reports `0/0`, which would read as "no work done".
Consumers must tolerate additive fields within a schema version.

**Version 3 removed the backlog contract**, which is why it is a version and not
an additive change: `features[].backlog`, the `backlog` source, the
`backlog_drift` finding code, and the `dropped` phase are all gone. A consumer
reading any of them should be told the shape changed rather than handed a
silently missing key.

Path binding added fields additively within schema version 2: each agent carries
`managed` (a saved bridge record exists) and `matched_path` (the canonical
worktree its working directory resolved to); each feature carries `occupancy`
(panes with no agent running, counted but not rendered as agent rows). None of
this required a schema bump.

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
remains, the guard closes **the feature's own Herdr tab** and then lets the
existing Ori Git cleanup proceed. It never calls Herdr worktree create/remove,
and it can no longer close a workspace at all: that call is not in the interface
cleanup is given. Closing a workspace is structurally able to take siblings with
it — on 2026-07-26 closing one bound to a repository's main checkout cascaded and
destroyed three workspaces — while closing a tab cannot reach beyond itself.

A feature recorded **before** tab-scoped handoff has a workspace and no tab. Its
workspace is left open and named in the result for you to close by hand; the Git
half still proceeds, so an old record can never make a worktree un-removable.

A recorded tab that no longer exists needs no closing and does not block cleanup
either — the guard treats "can't confirm it still exists" (an empty listing, a
failed query) as unknown rather than gone, so a Herdr hiccup can never be misread
as permission to skip a close that should have happened.

The safety check uses an immediate local socket snapshot when Herdr supplies
one. Without that socket, a slow CLI state query is bounded; it fails closed
instead of waiting through live agent activity and then treating the later
settled state as permission to remove the worktree.

wt done <feature> --herdr-override is a recovery tool only for unavailable or
failed tab-close checks. It cannot override known active agents or
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
wt herd status --json             # current open agents
wt status --json                  # feature and delivery diagnostics
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
