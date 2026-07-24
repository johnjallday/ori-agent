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

Use wt start <feature> --no-herdr for a one-run opt-out. A successful retry
completes only missing stages. It does not create a second workspace or primary
agent, and it does not resend an already confirmed bootstrap prompt unless
--resend is supplied.

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

## Status board and automation

~~~bash
wt herd status
wt herd status --current --watch
wt herd status --json
wt herd status --clear-view
~~~

The normal board shows all managed features. Each row includes feature/branch,
role, generated agent name, kind, Herdr semantic state, task progress, next
incomplete task, Git state, activity information, and the next continuation.
Herdr's idle, working, blocked, done, and unknown states remain authoritative;
task progress is supplemental.

Interactive output uses color and text labels together. It honors NO_COLOR and
--no-color. --json emits the versioned machine-readable snapshot instead,
making it suitable for scripts. --watch prefers Herdr events and falls back to
bounded polling, marking a disconnected live view as stale.

The plugin also provides a source-scoped Ori Devflow view and board. Clear it
with wt herd status --clear-view; doing so never changes unrelated user views
or metadata.

## Guarded cleanup

wt done calls the bridge guard before task archival, dirty checks, or Git
worktree removal:

~~~bash
wt done herdr-devflow-bridge
~~~

The guard blocks cleanup when a managed agent is working or blocked, or when a
schedule is pending, waiting, delivering, or uncertain. It prints exact
focus/read/show/cancel commands. If Herdr cannot be verified, non-interactive
cleanup fails closed.

When every managed agent is idle or done and no unresolved schedule remains,
the guard closes the matching Herdr workspace and then lets the existing Ori
Git cleanup proceed. It never calls Herdr worktree create/remove.

The safety check uses an immediate local socket snapshot when Herdr supplies
one. Without that socket, a slow CLI state query is bounded; it fails closed
instead of waiting through live agent activity and then treating the later
settled state as permission to remove the worktree.

wt done <feature> --herdr-override is a recovery tool only for unavailable or
failed workspace-close checks. It cannot override known active agents or
unresolved schedules, and it records an orphan-risk audit event.

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
