# Scripts

Developer and CI scripts for the ori-agent project. Run all scripts from the
project root unless noted otherwise.

> Tool capabilities are provided via MCP servers and skills — the legacy gRPC
> plugin system has been removed. There are no plugin build/clean scripts.

## Build

### `build.sh` — Full local build
```bash
./scripts/build.sh
```
Builds the server binary (`bin/ori-agent`) and, on macOS, the menu bar app
(`bin/ori-menubar`), embedding version, git commit, and build date.

Environment overrides:
- `BUILD_MENUBAR=false` — skip the menu bar app
- `GOOS` / `GOARCH` — cross-compile for another target

### `build-server.sh` — Server only
```bash
./scripts/build-server.sh
```
Builds just `bin/ori-agent` for faster iteration.

### `build-folder-picker.sh` — Native folder-picker helper
```bash
./scripts/build-folder-picker.sh
```
Builds the platform folder-picker helper used by the workspace launcher.
Invoked by the release and smoke-test workflows.

## Release

The full release flow is documented in `docs/RELEASE_CHECKLIST.md`.

- `release.sh` — Main release driver (tag, changelog, goreleaser). Guarded; do
  not run casually.
- `create-release.sh` — Wrapper that runs `pre-release-check.sh` then `release.sh`.
- `pre-release-check.sh` — Aggregate pre-release gate (lint, cross-platform,
  Go version, installer tests, docs, dependabot).
- `release-ready.sh` — Lightweight readiness probe used by the auto-release
  workflow.

## Testing & diagnostics

- `reaper-demo.sh` — Build and verify the coordinated local REAPER plugin, then launch an isolated Ori server with the plugin installed and enabled. Run `./scripts/reaper-demo.sh` for a manual demo, `./scripts/reaper-demo.sh test` for the coordinated browser specs, or `./scripts/reaper-demo.sh artifact` to refresh only the root binary.
- `test-all-installers.sh` — Exercise the generated installers.
- `docker-test-installers.sh` — Installer tests inside Docker.
- `test-with-ollama.sh` — Run the suite against a local Ollama provider.
- `diagnose-test-failures.sh` — Summarize and triage failing Go tests.
- `run-test-command.sh` — Run one Go, Node, or Playwright test command inside
  an exact run-owned temp sandbox. The sandbox is removed on success, failure,
  or interruption. Set `ORI_KEEP_TEST_SANDBOX=1` to preserve it for debugging.
- `prune-test-cache.sh` — Automatic post-test maintenance. It removes stale
  Ori-owned temp artifacts older than 24 hours and runs `go clean -cache` only
  when the shared Go build cache exceeds 20 GiB. Set
  `ORI_SKIP_CACHE_PRUNE=1` to opt out. It never clears the Go module cache or
  Playwright browser cache. Use `make cache-report` to preview the decision.
- `clean-test-artifacts.sh` — Preview or delete Ori-owned test logs and
  temporary directories left in the system temp folder. Run it with no
  arguments for a dry run, with `--delete` to clean, or use
  `make clean-test-artifacts`. Automatic maintenance applies a 24-hour age
  guard; manual cleanup has no age guard unless `--older-than-hours` is set.
  The script only matches known Ori-owned artifact prefixes.
- `check-cross-platform.sh` — Verify the code cross-compiles for release targets.
- `check-go-version.sh` — Assert the toolchain matches the required Go version.

## Lint & maintenance

- `fix-all-lint.sh` — Run linters and apply common auto-fixes.
- `fix-orihttp-errcheck.sh` / `fix-orihttp-errcheck.go` — Opt-in helper
  (`FIX_ORIHTTP_ERRCHECK=1`) that adds error handling for `internal/http`
  (`orihttp`) response calls flagged by errcheck.
- `merge-dependabot.sh` — Batch-merge open dependabot PRs.
- `update-readme.sh` — Update only the README version and Go badges during the
  release flow. It does not capture or publish product screenshots.
- `readme-refresh.sh` — Isolated staged README screenshot capture and cleanup.
  Use `make readme-audit`, `make readme-capture`, `make readme-propose`, and
  `make readme-check` for the versioned workflow described in
  `docs/README_MAINTENANCE.md`.
- `pre-release-check.sh` — Runs `make readme-check` before the separate badge
  updater, so releases get a read-only README screenshot-contract backstop.

## Assets

- `generate-app-icon.sh` — Generate the app icon set.
- `generate-menubar-icons.sh` — Generate the menu bar status icons.

## Worktrees

### `wt.sh` — Worktree manager
```bash
source scripts/wt.sh   # load the function
wt                     # interactive: pick a worktree to navigate
wt plan --issue <N> [--issue <N> ...] # plan one Ready Issue or an affirmed bundle
wt start <feature>     # create a worktree from a PRD and/or task list
wt new <name>          # create a worktree
wt rm <name>           # remove a worktree
wt ls                  # list worktrees
wt cd <name>           # navigate to a worktree
```
Source it (don't execute) so `cd` affects your current shell.

`wt demo` removes its exact sandbox when the demo exits. Set
`ORI_KEEP_DEMO_SANDBOX=1` when the sandbox needs to be retained for debugging.

#### `wt plan --issue <N> [--issue <N> ...]` — plan one unit

Planning and implementation are separate stages. One `--issue` preserves the
single-Issue flow. Repeated distinct values plan an ordinary-backlog bundle as
one unit: every Issue is fetched once, sorted by immutable number, written into
one combined snapshot and size-routed starter under `ori-agent-dev/tasks/`, and
sent to one selected **Claude or Pi** session. `wt start` handles implementation later.

```bash
wt plan --issue 342                         # one Issue
wt plan --issue 456 --issue 123             # one sorted bundle identity
wt plan --issue 123 --issue 456 --yes       # explicit scripted affirmation
```

| Issue size | What Pi is told to do first |
|---|---|
| `size:quick`, `size:planned` | Generate parent tasks, wait for `Go`, then expand them — no PRD |
| `size:prd` | Ask 3–5 clarifying questions, write the PRD, *then* generate parent tasks |

Every selected Issue must be open, currently Ready, and carry exactly one
`size:*` label. Ad-hoc bundles accept ordinary backlog Issues only;
`feature-proposal` keeps its existing single-Issue flow. Before mutation, the
command shows every body and comment and requires affirmation that the members
share a root cause, shared files, or the same UI surface. The highest selected
route wins (`prd` > `planned` > `quick`). Any failure is atomic, and planning
never comments on, labels, or otherwise changes GitHub.

If Herdr is unavailable, the planning files are still written and the command
prints the exact retry — they are never rolled back.

A single feature slug is `<issue-number>-<title-slug>`. A bundle contains every
sorted number plus a deterministic fragment, such as
`123-456-camera-workflow`; numbers are never truncated to meet the 80-character
limit. Reordering the same members and later title renames reuse the exact
artifacts and planning session. Existing real PRDs and task lists are never
overwritten.

#### `wt start` accepts a task list without a PRD

`wt start <feature>` starts anything with a PRD, a detailed task list, or
both — `size:quick`/`size:planned` work legitimately has no PRD. It copies
whichever of the Issue snapshot, PRD, and task list exist, independently.

It refuses while the task list is still `wt plan`'s planning starter: that
file is an instruction to Pi to write the plan, not a plan to implement.

### `scripts/devops.sh` — open Issues by workflow label

One command covers the human issue workflow:

```bash
./scripts/devops.sh                    # list every open Issue, then start the REPL
./scripts/devops.sh ready              # what you can actually pick up now
./scripts/devops.sh all                # every open Issue, one-shot
./scripts/devops.sh decisions          # label: needs-decision
./scripts/devops.sh backlog            # label: backlog
./scripts/devops.sh proposals          # label: feature-proposal
./scripts/devops.sh status             # checked-out feature implementation overview
./scripts/devops.sh release            # what has merged to dev since the latest release
./scripts/devops.sh view <number>      # one Issue in full
./scripts/devops.sh new <title>                         # quick title-only capture
./scripts/devops.sh new <title> --body <text>           # optional inline context
./scripts/devops.sh new <title> --body-file <path|->     # Markdown file or stdin
./scripts/devops.sh plan-new <title...> --body <context> --size <quick|planned|prd>
./scripts/devops.sh plan-new <title...> --body-file <path|-> --size <size> \
  --kind <claude|pi> [--model <model>] [--thinking <level>] --yes
./scripts/devops.sh decide <n> <answers> [--rationale <why>] # marked decision
./scripts/devops.sh answer <n> <answers>                   # alias for decide
./scripts/devops.sh approve <n>        # add the approved label (confirm-gated)
./scripts/devops.sh unapprove <n>      # remove it again
```

`ready` is the view worth living in: feature proposals plus backlog Issues that
are neither already covered by a proposal (`bundled`), already chosen
(`approved`), nor represented by an existing local branch/worktree. It exists
so the same work never appears twice — as a bundle and its members, or as both a
pickable Issue and an ongoing implementation.

In a terminal, the colorful picker uses `↑/↓` or `j/k` to select an Issue,
`←/→` or `h/l` to change views, and the same `1`–`5` view order shown by the
line REPL. `Enter` opens an Issue and keeps you there with an action bar:
`c` records a decision, `s` starts its Claude/Pi planner, `r` refreshes the
opened Issue, and Enter returns to the list. Decide and Plan each appear only
when that Issue's own live labels make them eligible - the bar is drawn for
one known Issue, so it never offers a write or a command the Issue does not
actually support. The list's `c` key is a shortcut that opens the same Issue
directly at its decision answers. `n` captures an unlabelled Issue with an
optional body. The distinct global `p` action works in every view and on an
empty list: it collects required context, a quick/planned/prd route, and the
Claude/Pi model/thinking selection before creating and planning one Ready Issue.
It never adds `approved`. `o` approves an existing Issue, and `s` plans the
selected Ready row. In Ready, Space marks/unmarks ordinary backlog rows and `b` plans at least two
marks together; cursor and marks render separately and the header shows the
count. Marks survive view changes. The dashboard above the Issue list embeds
the shared checked-out-feature implementation table; `w` opens its full report.
`r` refreshes implementations, release status, and Issues and visibly prunes
marks that vanished or became ineligible; `?` shows help and `q` quits. At the new-Issue body
prompt, a blank line keeps capture title-only and `:edit` opens `$VISUAL` or
`$EDITOR` for multiline Markdown.

In a pipe or redirected shell, the line REPL remains available: use `1/a`,
`2/d`, `3/b`, `4/f`, or `5/y` to change views, `v <number>` to inspect,
`n <title>` to capture, `c <number> <answers>` to decide, and `ok <number>` to
approve. The default and `all` view include every author; closed Issues stay out
of lists.

**Explore next work.** Press global `e` from any picker view, including an empty
list, or enter `e` / `explore` in the line REPL. Choose one of eight prompts, add
optional context, and preview before displaying/copying it or launching a fresh
foreground Claude/Pi advisor with model/thinking options. Quitting the advisor
returns to the same picker selection. Discovery does not create Issues, plans,
worktrees, or Herdr bindings; Capture and Plan stay separate actions.

```bash
./scripts/devops.sh explore
./scripts/devops.sh explore quick-win --context '45 minutes; no frontend' --print
./scripts/devops.sh explore next --kind pi --yes
```

`--print` prints the prompt without gh, Python, a native agent, or evidence
collection. Launch requires Python 3 and a compatible authenticated Claude/Pi
CLI; it gives the model read/search tools plus bounded read-only Git/GitHub/task
evidence, not shell/edit/write tools. Provider usage and native history are
previewed; these controls are not an OS sandbox. See
[the Explore guide](../docs/devops-explore.md) for presets, dependencies,
noninteractive behavior, privacy limits, and tests.

Every row shows the Issue's `size:*` label in its own column, so a long label
list can never truncate away the signal that says whether to open a PRD first.

**Planning keys.** `p` is New & Plan for a reviewed brief whose triage and size
a human is intentionally taking over from grooming. A pre-write cancellation
creates nothing. After a durable create, the picker refreshes, selects the new
row when it belongs to the current view, and otherwise reports it without
changing views. If planning is declined or fails, the Issue remains Ready and
the interaction leaves its exact retry receipt visible.

In Ready, `s` runs `wt plan --issue <N>` for the current
row. Space + `b` forwards every marked number as a separate argument and opens
the combined evidence/compatibility confirmation. `feature-proposal` rows stay
single and cannot be marked. The same `s` is also on the opened-Issue action bar
(`Enter` on any row), where it reads that Issue's own live labels through the
same `labels_are_ready` rule the Ready view itself uses, so it works from any
view - not only rows already sitting in Ready. The picker starts the sourced
zsh `wt` function in a child process; `wt plan` then repeats the live
eligibility check before writing files or contacting Herdr. The key is a no-op
with a clear message outside the Ready view or on an empty list; the
opened-Issue bar gives the same clear refusal for a non-Ready Issue or a label
read that fails, and never offers `[s] Plan` when it would refuse.

**Implementation status.** Issue rows retain a short local `wt`/`br` indicator
so duplicate Plan/Start actions can fail closed without a remote read. Ready
also excludes those rows. Exact generated snapshot headers in either dev or the
active feature worktree attach every member of an Issue bundle to the same
flight; numberless legacy branches still resolve by slug.

The dashboard and `./scripts/devops.sh status` deliberately do not expand that
small Bash model. They render the same normalized snapshot as `wt status
--implementations`, restricted to checked-out feature worktrees and including
`Merged (cleanup)` until `wt done` removes the checkout. The shared table reads
the active worktree's task list as authoritative and joins hierarchical
progress, Git divergence/dirty state, GitHub PR checks, live Herdr agents, and
attention findings. The dashboard preserves the overview's semantic colors even
though it captures the table before rendering; `NO_COLOR` still opts out. A
failed collector produces an explicit incomplete or unavailable dashboard state
rather than hiding the Issue list.

Task files remain gitignored, so the shared overview reads progress directly
from the checked-out implementation copy rather than anything pushed. That is
deliberately fresher: checkboxes get ticked while you work, but a pushed copy
would only update when you commit. It also means the numbers are exactly as
honest as the file — a shipped feature whose boxes were never ticked will read
`0/6`.

**`release` — what has not shipped yet.** `./scripts/devops.sh release` prints
the latest GitHub Release's tag and publish time, plus how many PRs have merged
into `dev` strictly after that instant:

```
Latest release: v0.0.106 (published 2026-08-15T10:00:00Z)
https://github.com/johnjallday/ori-agent/releases/tag/v0.0.106

2 PR(s) merged into dev since v0.0.106.
```

The full-screen Issue picker displays the compact count directly below its
`Ori DevOps` heading and refreshes it when you press `r`. If that refresh
fails, the banner reports `Release status unavailable` while the Issue list
remains usable.

It is read-only — one `gh release view` and one `gh pr list --base dev
--state merged`, nothing else. Feature PRs merge into `dev`; a Release snapshots
`main`, so those post-release `dev` merges are the unshipped delivery queue. The
comparison is an **exact timestamp**, not a calendar date, so a PR merged earlier
the same day as the release correctly does *not* count. A release with nothing
merged since prints `No PRs merged into dev since <tag>.` rather than a blank
line, and either read failing
(no release exists, the PR query errors) exits non-zero with `gh`'s own
message on stderr rather than reporting a misleading zero.

**GitHub writes.** `new`, `plan-new`, `decide`, `approve` and `unapprove` are the only GitHub-mutating
commands; `answer` is a backwards-compatible alias for `decide`. Each prints
what it will do and asks for confirmation; without a terminal they refuse unless
given `--yes`, so a pipe can never write by accident.

`new` exists because capture is supposed to take ten seconds — a title is
enough, and the grooming routine researches and specs it on its next run. An
optional body can come from the picker, `--body <text>`, `--body-file <path>`, or
stdin with `--body-file -`. **The command deliberately applies no labels.**
Adding `backlog` here would skip the spec step the whole pipeline is built
around, and `needs-decision` would assert a spec exists when none does. Titles
and bodies are passed through verbatim, so an ampersand stays an ampersand
rather than becoming a literal `&amp;`.

`plan-new` is an explicit grooming bypass, not a faster spelling of `new`. It
requires non-empty problem context and one size, then previews and creates an
Issue with exactly `backlog` plus `size:quick`, `size:planned`, or `size:prd`.
It never adds `approved` or any grooming marker. Interactive use prompts for the
same Claude/Pi model/thinking intent as `s` before the GitHub write. A script
must pass `--kind` and `--yes`; model and thinking are optional integration
overrides.

The create and plan stages are intentionally not atomic. After GitHub returns a
valid Issue URL/number, `plan-new` delegates to the existing `wt plan` path so
its fresh Ready check, evidence summary, artifact writes, and Herdr degradation
remain authoritative. A downstream decline or failure never rolls back the
Ready Issue; the command prints `Ready Issue: #N` and a shell-safe exact
`wt plan` retry. If a successful create returns malformed or multiline output,
no number is guessed and no planner starts—the raw result and manual recovery
steps are printed instead.

`decide` records choices such as `1B, 2A` plus an optional rationale in a comment
marked `<!-- ori-decision -->`. The opened Issue owns this interaction: its
`c` action collects the answers in place, posts them, and refreshes the Issue so
the recorded comment is visible before returning to the list. The list's `c`
key enters that same action directly. Recording the answer does **not** remove
`needs-decision`: grooming owns triage and sizing, so the row remains until that
routine processes the marked comment.

`approved` is the pipeline's single human gate — the grooming routine is
forbidden from writing it — which is why setting it belongs here. Label changes
use `--add-label`/`--remove-label` rather than a labels array write, so an
Issue's other labels cannot be dropped.

One-shot and line-REPL views are direct `gh issue list` calls. The terminal
picker fetches the complete open-Issue index once, then filters it locally until
`r` refreshes it. Because that local filter has to agree with the labels `gh`
applies server-side, `scripts/devops-cli.test.sh` unit-tests the matcher against
the same fixtures the integration tests use. There is no ProjectV2 query, Herdr
helper, persisted cache, local fallback, custom formatter, or Ori-owned JSON
contract. Empty label views say so explicitly. GitHub failures retain `gh`'s
non-zero exit status instead of looking like an empty backlog.

The product backlog is GitHub Issues. There is no backlog file to maintain,
sync, or prune, and no backlog commit ever lands on `dev`.

The same capture paths are available directly through `gh`, and agents that
need machine-readable reads should use its JSON output:

```bash
./scripts/devops.sh new "<title>" --body "<optional context>"
./scripts/devops.sh new "<title>" --body-file notes.md
./scripts/devops.sh plan-new "<reviewed title>" --body-file notes.md \
  --size planned --kind pi --yes
gh issue create --title "<title>" --body "<optional context>"
gh issue list --state open --limit 1000 --json number,title,author,labels,url,createdAt,updatedAt
```

The script exits `0` after a completed operation, `2` for invalid arguments,
and otherwise preserves the failed `gh` or constrained planning child status.

New Issue-backed work uses the Issue number in its identity —
`<issue-number>-<slug>`, for example `292-coordinate-based-map` — so a PRD,
task list, worktree, branch, and pull request can be joined on an exact number
instead of a title that may change. Existing features whose slugs have no
number remain valid and are never renamed.

### `wt done` — finish delivery and clean up

```bash
wt done 292-coordinate-based-map
wt done 123-456-camera-workflow                 # closes every attached member
wt done 123-456-camera-workflow --keep-issue-open # intentional exception
```

For Issue-backed work, `wt pr` and `wt done` trust only the generated marker on
line 3 of `tasks/issue-<feature>.md`. A bundle marker carries every sorted
member and must agree with the complete numeric slug prefix. `wt pr` keeps its
normal `--fill` content and adds one `Closes #N` reference per bundle member.
After that branch is confirmed merged to `dev`, `wt done` inspects every
attached member, closes each open one as `completed` with PR attribution, and
leaves already-closed members unchanged. This explicit transition is necessary because
delivery PRs target `dev`, not the repository's default branch, so GitHub
closing keywords do not complete the Issue at that merge. A number-looking slug
without the snapshot is never enough to infer an Issue, so ad-hoc and legacy
cleanup keeps working.

After all attached members close, `wt done` additionally reads the confirmed
merged PR's body once and closes every other OPEN Issue it names with a
case-insensitive `Closes`/`Fixes`/`Resolves #N` reference — the same way a PR
against a repository's default branch would, since a `dev`-targeted merge does
not trigger GitHub's own closing keywords. References are deduplicated against every attached member, and each closed
secondary gets the same `Delivered by PR #N.` comment. This is purely additive
to the trusted generated attachment: work with no attached Issue never has
its merged PR body read at all, so ad-hoc cleanup still cannot infer Issue
authority from PR text alone.

Issue inspection or closure failures — for any attached or secondary Issue —
preserve the feature worktree so the same command can be retried. Already
closed members make that retry idempotent. A failed PR-body read is a nonfatal
warning: it never undoes attached closures, and cleanup still proceeds; secondary Issues just are not found and
closed that time. `--keep-issue-open` is the explicit escape hatch for a
feature whose Issue must intentionally remain open; it skips every Issue read
and write, primary and secondary alike, and never reads the PR body. The
Herdr guard still runs before archival, Issue closure, or Git removal, and
`--herdr-override` retains its separate safety meaning.

### `wt status` — feature overview

```bash
wt status                      # compact, active-work-only overview
wt status --all                # same table, full history included
wt status --feature <slug>     # one feature in detail, active or not
wt status --json               # complete normalized snapshot (schema v3), every feature
wt status --implementations    # checked-out feature worktrees, including cleanup owed
wt status --watch              # live board, active-only by default
wt status --all --watch        # live board, full history
wt status --no-color           # plain text; NO_COLOR is honored too
wt status --worktrees          # the legacy Git-only worktree table
```

`wt status` answers "what features exist, and where is each one" — joining
planning artifacts, feature worktrees, local Git, GitHub pull requests, and live
Herdr agents on the exact feature slug. It is read-only: it never writes
planning files, Git, GitHub, bridge, or Herdr state. It describes selected and
executing work; unselected ideas live in GitHub Issues and are listed by
`./scripts/devops.sh`.

By default the table hides `Shipped`, `Merged (cleanup)`, and `Unknown` rows —
settled or unplaced work is not what you're looking at right now. `--all`
restores every row, which matters because `Merged (cleanup)` is the only
standing reminder that a `wt done` is still owed. `--implementations` instead
shows every checked-out feature worktree and retains that cleanup phase because
the local checkout still exists. The filter is display-only:
`wt status --json` always emits every feature regardless of `--all`, and
`wt status --feature <slug>` still finds an inactive feature's full detail.
The compact PLAN cell also names the active parent Group (for example
`G8 next 8.8`) immediately before the next actionable item.

Exit codes: `0` complete, `1` incomplete (a required source, normally GitHub,
was unavailable — the board still prints every local fact it observed), `2`
invalid arguments.

A complete snapshot requires one fresh authenticated `gh` query. Run
`wt herd doctor` if `wt status` keeps exiting 1. The full reference — phases,
finding codes, watch cadence, JSON contract — is in
[docs/herdr-devflow.md](../docs/herdr-devflow.md).

### Herdr devflow bridge

The opt-in repository-owned bridge lives in tools/herdr-devflow/ and is exposed
through the sourced wt function:

~~~bash
wt herd setup
wt herd doctor
wt herd status --watch
wt herd go
~~~

`wt herd status` is intentionally narrow: it lists only agents Herdr reports as
open now, with agent, kind, live status, and worktree columns. It does not join
planning, saved bridge history, Git, or GitHub. `wt herd overview` is a
compatibility alias for the same roster; use `wt status` for the broader
feature/delivery snapshot. `wt herd go` turns the live roster into a numbered
picker and focuses the selected agent; use `herd go` for the same picker inside
the `wt` REPL.

wt herd setup installs a stable user-local helper/plugin copy, rather than
linking Herdr to a removable feature worktree. wt start <feature> then attempts
an automatic Herdr handoff; for linked checkouts it supplies Herdr with the
normal Git source checkout plus the feature path, while Ori remains the only
Git-worktree creator/remover. wt start <feature> --no-herdr skips it once. See
[Herdr Devflow Bridge](../docs/herdr-devflow.md) for exact feature-scoped agent
selection, one-time continuations, guarded cleanup, and recovery. Claude is the
configured default; use wt start <feature> --kind codex for a one-feature
override without changing that default.

One-time continuations can opt into the standalone macOS Herdr Wake Service:

~~~bash
wt herd continue builder --feature <feature> --at "2026-07-30 05:00" --wake
~~~

The command succeeds only after `herdr-wake` directly verifies the wake. Install
the service once with `wt herd wake install`; it does not depend on an Ori
server, Device Capabilities, or Ori settings.

The bridge resolves a feature's agents by canonical worktree path, not a saved
workspace ID, so closing/reopening a workspace or opening a pane by hand on
the same worktree is recognised with no bridge command in between. `wt done`
now blocks only on a live `working`/`blocked` agent in the path — a stale
saved record with nothing running no longer needs `--herdr-override`. See
"Path is identity, IDs are hints" in
[docs/herdr-devflow.md](../docs/herdr-devflow.md) for the full model.

Run make test-herdr-devflow for focused helper, shell, and wt tests. It also
runs make test-herdr-devflow-cross, which cross-compiles the local helper for
macOS, Linux, and Windows; only macOS registers and executes the LaunchAgent
scheduler.

## Requirements

- Go toolchain installed (see `check-go-version.sh` for the required version)
- Run from the project root
