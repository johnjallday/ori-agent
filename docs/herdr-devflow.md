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

Setup checks the configured executable and reports optional Pi/Claude/Codex
integration commands, but it never installs, rewrites, or enables an agent
integration for you. Install or update an integration explicitly with Herdr if
you choose to use native session restore.

## Persistent agent defaults

Feature handoffs resolve a kind/model pair from `.herdr/devflow.toml`:

~~~toml
[primary]
role = "builder"
kind = "claude"
model = "" # empty: let the external integration choose

[roles]
default_kind = "claude"
default_model = ""

[roles.defaults]
reviewer = "pi"

[roles.models]
reviewer = "provider/reviewer-model"
~~~

Use the local DevOps action instead of hand-editing the two fallback pairs:

~~~bash
./scripts/devops.sh agent-defaults        # prompts in a terminal; reads in a pipe
# Picker or line REPL: press g

./scripts/devops.sh agent-defaults \
  --primary-kind pi --primary-model 'provider/primary-model' \
  --role-kind claude --clear-role-model --yes
~~~

It reads and validates the complete file, previews current → proposed values,
then confirms before atomically replacing it. It preserves unrelated sections,
comments, per-role overrides, newline style, and file mode. Malformed or
symlinked files, missing/duplicate target keys, invalid values, and failed writes
leave the original untouched. `HERDR_DEVFLOW_CONFIG` selects an isolated config
for tests. `HERDR_DEVFLOW_PRIMARY_MODEL` is an explicit runtime override,
including an explicitly empty value; it does not rewrite the file.

Kind and model are selected together:

1. A saved feature or partial role launch wins on every retry.
2. An explicit `--model` replaces the model while keeping the effective kind.
3. An explicit `--kind` equal to the configured kind inherits its model.
4. A different explicit kind with no model uses that integration's default; it
   never inherits a model chosen for the old kind.
5. With no one-run override, the configured primary, per-role, or role-fallback
   pair applies.

Model values are opaque, bounded argv values. Ori does not maintain a
provider/model allow-list. It rejects control characters, flag-shaped values,
and values over 256 bytes, then forwards a non-empty value after Herdr's native
argument separator: `herdr agent start … -- --model <value>`. Each value is one
argv word and never enters a shell. **This proves Ori's local persistence,
Herdr parser compatibility, and forwarding only. Live integration behavior
remains deliberately unconfirmed; do not interpret a successful local preview
as proof that the installed integration honors the model.** A Herdr rejection
uses the normal non-fatal handoff contract: the worktree remains ready and the
recorded pair remains available to `wt herd retry`.

## Planning an Issue

`wt plan --issue <N>` is the stage *before* a feature exists. It turns a Ready
GitHub Issue into planning artifacts in `ori-agent-dev` and starts a selected
**Claude or Pi** session there to finish the plan. No branch, no implementation
worktree, no implementation.

~~~bash
wt plan --issue 342 --kind claude                               # Claude defaults
wt plan --issue 342 --kind claude --model fable --thinking high # Claude selection
wt plan --issue 342 --kind pi                                   # Pi defaults
wt plan --issue 342 --kind pi --model openai/id --thinking max  # Pi selection
wt plan --issue 342 --yes                                       # legacy default Pi

# Create a reviewed, explicitly sized brief and enter this same planning path.
./scripts/devops.sh plan-new "Camera framing" --body-file notes.md \
  --size planned --kind pi --thinking high --yes
~~~

`plan-new` is the deliberate entry point for a human who is taking ownership of
triage and sizing instead of sending a raw capture through grooming. It requires
non-empty context and `quick`, `planned`, or `prd`, creates exactly one open
Issue with `backlog` plus that `size:*` label, never adds `approved`, recovers the
positive number from one anchored GitHub Issue URL, and then invokes this same
`wt plan` path. Interactive use collects Claude/Pi, model, and thinking intent
before the create confirmation; non-interactive use requires explicit `--kind`
and `--yes`.

The create and planning stages are intentionally separate consequences. Once
the Issue exists, a planning decline, zsh failure, or Herdr degradation never
rolls it back. The durable Issue number and exact shell-safe retry print after
the child returns. If GitHub succeeds but its output cannot be parsed safely,
planning does not start; the output and a manual `wt plan --issue <number>`
recovery shape are shown instead.

The shell only validates arguments and resolves the exact `dev` worktree; the
Go helper (`wt herd issue-plan`) does the GitHub read, eligibility checks,
identity resolution, rendering, confirmation, writes, and the Herdr calls.

The bootstrap prompt and size-routed starter carry only Issue-specific paths and
state. Planning workflow lives once in
`.agents/skills/task-planning/SKILL.md`, which the selected planner reads
directly; it runs planning-only mode and stops after replacing the starter.

**Planner sessions are not feature handoffs.** They are stored separately from
`BridgeState.Features`, keyed by repository plus Issue *number* — the one part
of an Issue that cannot change. That separation is the point:

- A planner is never a feature binding, an Overnight Run participant, a
  continuation target, a PR owner, or a `wt done` cleanup target.
- Its kind is explicitly Claude or Pi (a direct command with no `--kind` keeps
  Pi as the backward-compatible default). The DevOps action asks for kind first.
  Claude then offers Integration default, Sonnet, Opus, Fable, or a custom
  alias/full model name, followed by Integration default, low, medium, high,
  xhigh, or max thinking. Pi discovers available provider/model IDs with an
  offline `pi --list-models` call that disables extensions, skills, prompts,
  themes, context files, and project trust. `openai-codex` is promoted to the
  first provider option, followed by the remaining discovered order; Pi then
  offers Integration default, off, minimal, low, medium, high, xhigh, or max
  thinking. Pi itself clamps unavailable levels to the selected model's
  capabilities. Blank means the integration default, `c` permits a custom opaque
  model, and catalog failure keeps default/custom/cancel available. Planning
  never consumes feature primary or role defaults, and its selection cannot leak
  into a later feature handoff. The selected kind/model/thinking intent is
  recorded before Herdr starts, retained across launch failure/reuse, and cannot
  be replaced by a conflicting retry. A bare `wt start` uses the
  configured primary pair; the Issue picker's later implementation action
  passes the owner's explicit Claude, Codex, Pi, or worktree-only kind choice
  and deliberately adds no implementation-model prompt.
- A generic live-agent view may still truthfully show the running Claude or Pi
  process. It is real; it is simply not a *managed feature* agent.

**Placement and reuse.** Each Issue gets its own tab in the currently focused
workspace, labelled `issue-<N>-plan`, and a deterministically named planner.
Several Issue planners can therefore share `ori-agent-dev` without colliding.
Re-running the same command resumes: it re-enters the recorded tab, adopts the
saved planner, and does not resend a confirmed prompt. If the tab was closed
by hand, it is placed again. A pending planner saved by the former Codex-based
flow is migrated to a fresh tab for the newly selected kind; the old Herdr tab
is left untouched rather than being closed behind the user's back.

**Degradation.** The planning files are written before Herdr is contacted and
are never rolled back. A Herdr or agent failure reports the stage that failed
and the exact retry, and the command still reports what it did write — it
never claims a planner started when none did.

**Prompt privacy.** The Issue body and comments live in
`tasks/issue-<feature>.md` and are referenced by *path*. They are never copied
into the persisted bootstrap prompt, bridge state, audit events, or error
messages.

## Starting a feature

After planning artifacts are ready, wt start <feature> creates the Git
worktree as usual and then attempts the Herdr handoff:

~~~bash
wt start herdr-devflow-bridge

# Keep the configured pair for other features, but override this run.
wt start experimental-codex-flow --kind codex
wt start experimental-pi-flow --kind pi --model 'provider/model'
wt start same-kind-new-model --model 'provider/other-model'
~~~

The handoff opens the existing checkout, finds an interactive pane, starts the
configured primary agent, and sends a bootstrap prompt that points it to
AGENTS.md and whichever planning artifacts actually exist in the worktree.

A feature needs a PRD **or** a task list, not both: work planned from a
`size:quick`/`size:planned` Issue has no PRD, and the bootstrap prompt says so
honestly rather than naming a `prd-<feature>.md` that was never written.

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

The checked-in configuration defaults the primary agent to Claude with an empty
model. Use `wt start <feature> --kind <kind>` and/or `--model <model>` only for
a one-run override. The confirmation summary shows the effective pair, including
`integration default` when the model is empty. The pair is recorded before
Herdr is contacted, so a launch rejection and every later `wt herd retry` keep
that intent instead of reading newly changed repository defaults. Retry rejects
new kind/model overrides; it is a continuation, not a reselection.

Use wt start <feature> --no-herdr for a one-run opt-out. A successful retry
completes only missing stages. It does not create a second workspace or primary
agent, and it does not resend an already confirmed bootstrap prompt unless
--resend is supplied.

For Issue-backed work, the interactive `./scripts/devops.sh` flow makes this
choice explicit. Press `s` for an existing Ready Issue and choose its planner,
or global `p` to collect a reviewed title, required context, explicit size, and
the same Claude/Pi model/thinking selection before creating one. Return after
the planner has replaced the planning starter, then press `i`. The later action resolves the
number-first task list locally, prompts for Claude, Codex, Pi, worktree-only, or
cancel, and invokes the corresponding `wt start --kind <kind>` or `--no-herdr`.
It never chains to the planner or polls it; `wt start` still owns its normal plan
summary, confirmation, worktree creation, and handoff.

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
wt herd add tester --kind pi --model 'provider/tester-model'
wt herd prompt reviewer "Review the current implementation."
wt herd focus reviewer
wt herd read reviewer --lines 160

# Same role, but in a different managed feature.
wt herd prompt builder "Continue after the CI result." --feature another-feature
wt herd status --feature another-feature
~~~

Each managed role has a generated globally unique Herdr name, saved kind/model
intent, and saved native session identity when Herdr provides one. New roles use
`[roles.defaults]`/`[roles.models]` when present, then the role-fallback pair.
The same one-run pair rules apply to `wt herd add`. The pair is saved before a
pane split or agent start, so retry, reuse, rename, recovery, and rebind preserve
it. Herdr does not report a live model, so model intent is never used as live
identity or as a continuation/Overnight target field. The bridge resolves the
saved feature/role/session association first; it does not select an agent merely
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
./scripts/devops.sh status             # checked-out feature implementation overview
./scripts/devops.sh release            # what has merged to dev since the latest release
./scripts/devops.sh agent-defaults     # local persistent kind/model fallback pairs
./scripts/devops.sh view <number>      # one Issue in full
./scripts/devops.sh new <title>                    # quick title-only capture
./scripts/devops.sh new <title> --body <text>      # optional inline context
./scripts/devops.sh new <title> --body-file <path|-> # Markdown file or stdin
./scripts/devops.sh plan-new <title...> --body <context> --size <quick|planned|prd>
./scripts/devops.sh plan-new <title...> --body-file <path|-> --size <size> \
  --kind <claude|pi> [--model <model>] [--thinking <level>] --yes
./scripts/devops.sh decide <n> <answers> [--rationale <why>] # marked decision
./scripts/devops.sh answer <n> <answers>             # alias for decide
./scripts/devops.sh approve <n>        # add `approved`, confirm-gated
~~~

In a terminal, the colorful picker shows the shared checked-out-feature
implementation table and the number of PRs merged into `dev` since the latest
Release directly above the Issue views; `w` opens the full implementation
report and `r` refreshes both dashboard sections with the Issue data. It accepts `↑/↓` or `j/k` to select an Issue, `←/→` or `h/l`
for those five list views, and `1`–`5` in the same order as the line REPL.
`Enter` opens an Issue with an action bar where `c` decides, `s` starts Claude/Pi
planning, `i` starts implementation from a completed local plan, `r` refreshes
the detail, and Enter returns to the list; the list's `c` key opens that same
Issue directly at its decision answers. Decide and Plan each appear on that bar
only when the opened Issue's own live labels make them eligible, read fresh
every time the Issue opens; Start implementation is always available so its
local resolver can explain whether planning is incomplete or work already
exists. `n` captures one unlabelled Issue with an optional body. Global `p`
works from every view and an empty list; it creates and plans a Ready Issue after
required context, size, planner selection, and confirmation, and never approves
it. `o` approves, `s` starts Claude/Pi planning for the selected Ready row, and
`i` starts the selected Issue's later implementation flow. `g` manages persistent agent defaults locally,
list-level `r` refreshes, `?` shows help, and `q` quits.
`:edit` at the body prompt opens `$VISUAL` or `$EDITOR` for multiline Markdown. In a pipe or redirected shell, the line REPL accepts
`1/a`, `2/d`, `3/b`, `4/f`, and `5/y`, plus `v <number>`, `n <title>`,
`c <number> <answers>`, `ok <number>`, and `g` for the interactive local
defaults action. Lists include every author and only open Issues. Filters are literal labels; no Project board or rank
participates.

The `s` key is the direct link from this REPL to the planning flow above. In
the Ready view, or on the opened Issue's own action bar when its live labels
satisfy the same eligibility rule as Ready, it first asks for Claude or Pi.
Claude offers Integration default, Sonnet, Opus, Fable, or a custom model, then
a thinking level from Integration default through max. Pi presents numbered
providers (`openai-codex` first), numbered models, then Integration default,
off, minimal, low, medium, high, xhigh, or max thinking. Integration default,
custom, back, and cancel remain available. It then runs `wt plan --issue <N>`
with explicit `--kind` and optional `--model`/`--thinking`. Bundle key `b` asks
once and applies the same selection to its one planner. `wt plan` then performs
its own fresh eligibility read, shows the selected kind/model/thinking in the
normal consequence summary and
confirmation, writes the planning artifacts, and launches the Herdr-managed
planner. Off Ready, or on an Issue whose labels are not Ready
(or could not be read), the key refuses before launching anything.

Planning is asynchronous, so `s` never chains to implementation. Return later
and press `i` on a selected or opened Issue. That action contacts neither GitHub
nor Herdr while it resolves exactly one `tasks/tasks-<N>-*.md` in the dev
worktree and refuses a missing or ambiguous artifact, the planning-starter
marker, or an existing branch/worktree. If ready, it prompts for Claude, Codex,
Pi, worktree-only, or cancel, then starts an argument-safe zsh child that invokes
`wt start` with `--kind <kind>` or `--no-herdr`. `wt start` remains the final
source of truth and displays its own confirmation.

Reads never mutate. The write commands cover the four things only a human does
here — capturing an idea, explicitly taking over triage and sizing for an
already-reviewed brief, answering a spec's open questions, and setting
`approved`, the one label the grooming routine may never write. All confirm
first, and refuse without a terminal unless given `--yes`. A captured Issue gets
no labels: it must reach the grooming routine untriaged so the spec step runs.
Its optional body can come from an inline prompt, `$VISUAL`/`$EDITOR`, a file,
or stdin.

New & Plan is distinct in both the picker (`p`) and one-shot CLI (`plan-new`). It
requires context and a size, resolves the complete planner selection before any
write, and creates only `backlog` plus the chosen size label. Scripted callers
must pass `--kind` and `--yes`; the latter also confirms `wt plan`, while an
interactive call keeps the normal downstream evidence gate. The picker refreshes
only after a durable create, selects the row if the current view contains it,
and leaves the printed retry readable when planning declined or failed.

`decide` (and its `answer` alias) posts `<!-- ori-decision -->` plus the selected
answers and optional rationale. Only after that comment succeeds, the same
confirmed operation additively applies the `answered` label as a receipt. It
deliberately leaves `needs-decision` in place: the grooming routine reads that
marked comment and owns the later triage and sizing transition. If the label
write fails, the command warns that the receipt is missing but returns the
successfully posted comment as the answer of record; the opened Issue still
refreshes so that durable answer is visible.

The picker's short in-flight cell and Ready guard remain local: `git worktree
list`, `git branch --all`, and trusted generated Issue snapshots resolve an
Issue to an existing branch/worktree without a remote read. Active worktree
snapshots preserve every member of an Issue bundle even when dev no longer has
a planning copy. Branches predating the number-first convention resolve by
slug.

The dashboard and `devops.sh status` use the read-only
`feature-overview --implementations` surface instead of deriving a second
lifecycle in Bash. It reads the active worktree's task list as authoritative and
joins Git, GitHub PR, and Herdr agent state exactly as `wt status` does. The
other permitted Go-helper boundary remains the local, confirm-gated `config
agent-defaults` operation.

`release` answers a different question than `status`: not "what am I part-way
through" but "what has landed in `dev` since we last shipped." Feature PRs
target `dev`, while a Release snapshots `main`, so the post-release `dev` merges
are the unshipped delivery queue. It reads the latest GitHub Release's tag and
`publishedAt`, then counts PRs merged into `dev` strictly after that instant —
an exact-timestamp comparison, so a PR merged earlier the same calendar day as
the release is correctly excluded rather than double-counted. It is two reads
and nothing else: `gh release view` and `gh pr list --base dev --state merged`,
capped at a practical result limit since GitHub returns merged PRs newest-first.
Either read failing — no release exists yet or the PR query errors — exits
non-zero with `gh`'s own
message rather than reporting a misleading zero count.

Task files are gitignored and never pushed, so progress comes from the active
checked-out copy and reflects ticked checkboxes immediately rather than at the
next commit. `wt status --implementations` and `devops.sh status` are the same
feature/delivery view.

The Issue commands run `gh issue list`, `gh issue view`, `gh issue create`,
`gh issue comment`, or `gh issue edit` directly from the checkout.
`agent-defaults` also works when `gh` is not installed. The terminal picker
fetches the complete open-Issue index, implementation overview, and release
count once and filters Issues locally until `r` refreshes all three. It does not persist a
cache or define a JSON contract. Its eligible `s`/`b` actions keep the planner
model, thinking level, and Issue numbers as separate arguments while starting
`wt plan` in two constrained zsh children; the later local-ready `i` action starts `wt start` in
the third constrained child. Neither path calls Herdr directly or evaluates
Issue text as shell code. Agents that need structured data should use
`gh issue list --json` directly. Capture remains a thin wrapper over the GitHub
CLI; New & Plan deliberately adds Ready labels and the constrained planning
handoff:

~~~bash
./scripts/devops.sh new "<title>" --body "<optional context>"
./scripts/devops.sh new "<title>" --body-file notes.md
./scripts/devops.sh plan-new "<reviewed title>" --body-file notes.md \
  --size planned --kind pi --yes
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
live and keeps its own history). `scripts/devops.sh` still has no promote,
ship, drop, close, select, or Project operation; daily selection stays manual
in GitHub.

Delivery has one deliberately narrow primary state transition: after an exact
PR to `dev` merges, `wt done` closes the one Issue explicitly attached by
`tasks/issue-<feature>.md`. The generated marker must agree with the
number-first feature identity; a numeric-looking slug, PR prose, and Issue body
text are never treated as attachments. `wt start` and `wt pr` still do not
update the Issue. `wt done --keep-issue-open` skips this transition for an
intentional exception.

That primary attachment is not the only Issue a delivery PR can close. Once
the primary closes, `wt done` reads the confirmed merged PR's body once and
additionally closes every OPEN Issue it names with a case-insensitive
`Closes`/`Fixes`/`Resolves #N` reference — mirroring what GitHub's own closing
keywords would do had the PR targeted the repository's default branch instead
of `dev`. References are deduplicated in first-seen order, the primary Issue
is never processed a second time even if the body repeats it, and each closed
secondary gets the same `Delivered by PR #N.` attribution as the primary. This
is strictly additive to the single trusted attachment, never a second way to
infer one: with no attachment at all, the PR body is never read, so ad-hoc and
legacy cleanup still cannot acquire Issue authority it was never given. A
failed PR-body read is a nonfatal warning after a successful primary close —
it never undoes that close, cleanup still proceeds, and secondary Issues are
simply not found that run. A secondary Issue's own state-read or close
failure is fatal, preserving the worktree for retry exactly like a primary
failure. `--keep-issue-open` skips every Issue read and write, primary and
secondary alike, and the PR body is never fetched on that path.

New Issue-backed work uses `<issue-number>-<slug>` as its feature identity; see
"Feature Naming: Issue Number First" in `AGENTS.md`.

## Feature overview

~~~bash
wt status                          # compact, active-work-only overview
wt status --all                    # same table, full history included
wt status --feature <slug>         # one feature in detail, active or not
wt status --json                   # the complete normalized snapshot, every feature
wt status --implementations        # checked-out feature worktrees, including cleanup owed
wt status --implementations --color # preserve semantic colors when output is captured or piped
wt status --watch                  # live board, active-only by default
wt status --all --watch            # live board, full history
wt status --worktrees              # the legacy Git-only worktree table
~~~

`wt status` answers "what features exist in this repository, and where is each
one" rather than listing Git worktrees. One row per feature, joined on the
exact slug across every source.

### Active-only by default

The compact table hides `shipped`, `merged_cleanup`, and `unknown` rows by
default: settled or unplaced work is not what an operator opening `wt status`
is looking for. `--all` restores every row, and matters in particular for
`merged_cleanup` — it is the only standing reminder that a `wt done` is still
owed for that feature. `--implementations` is a second compact-table filter for
the DevOps dashboard: it shows only features with a checked-out worktree and
retains `merged_cleanup`, because that checkout remains ongoing local work.
The DevOps picker captures this table before rendering, so it requests
`--color` to preserve the overview's semantic palette; `NO_COLOR` and
`--no-color` remain authoritative opt-outs.

The filters are display-only and apply to the compact table alone:
`wt status --json` always emits every feature regardless of `--all` (see
"JSON contract" below), and `wt status --feature <slug>` still finds an
inactive feature's full detail. A repository whose only features are history
prints an explicit "No active features" message rather than claiming to have
none — `wt status --all` is the pointer back to them.

The PLAN cell also names the active parent Group immediately before the next
actionable item, taken from the plan's already-computed active milestone (for
example `6/8 milestones · 150/155 subtasks · G8 next 8.8`). A delivery-only
row, or one with no active milestone, falls back to the prior wording with no
Group named.

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
`refactor/`, `design/`, `docs/`, `test/`, and `chore/` branches are all matched — on the
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
availability, and every finding. This is unaffected by `--all`: the flag
controls only what the compact human table prints (see "Active-only by
default" above), and JSON always emits every feature, active or history.

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

wt done calls the bridge guard before task archival, dirty checks, attached
Issue closure, or Git worktree removal:

~~~bash
wt done 292-coordinate-based-map
wt done 292-coordinate-based-map --keep-issue-open # intentional exception
~~~

After the guard passes, Issue-backed cleanup verifies the fixed generated
header in `tasks/issue-<feature>.md`, the matching number-first identity, and a
merged PR for the exact branch targeting `dev`. It then closes an open Issue as
`completed` with a comment naming the merged PR. An already-closed Issue is
idempotent. Once that primary Issue is settled, it reads the same merged PR's
body once and closes every OPEN Issue it names with `Closes`/`Fixes`/`Resolves
#N`, deduplicated and with the primary skipped even if repeated — this is
`wt done` doing what GitHub's own closing keywords would have done had the PR
targeted the default branch instead of `dev`. A GitHub read/write failure for
the primary or a secondary stops before worktree removal so the command is
safely retryable; a PR-body read failure is a nonfatal warning that leaves the
primary close standing. `--keep-issue-open` bypasses every Issue inspection
and mutation, primary and secondary. Work without the exact snapshot remains
ordinary ad-hoc cleanup, and its merged PR's body is never read.

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

Feature names, roles, agent kinds, optional opaque model values, thinking
levels, schedule IDs, timestamps, metadata tokens, and canonical linked-worktree
paths are bounded and validated. Every external process receives an argument
vector; the bridge does not use `eval` or build a shell command from untrusted
values. An empty model/thinking selection keeps the prior command vector
byte-for-byte; any native selection follows Herdr's `--` separator. Model and
thinking each remain one value argument; the adapter emits Pi `--thinking` or
Claude `--effort` as required by the installed CLIs.
