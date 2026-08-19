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
wt plan --issue <N>    # start Pi planning a Ready Issue in the dev worktree
wt start <feature>     # create a worktree from a PRD and/or task list
wt new <name>          # create a worktree
wt rm <name>           # remove a worktree
wt ls                  # list worktrees
wt cd <name>           # navigate to a worktree
```
Source it (don't execute) so `cd` affects your current shell.

`wt demo` removes its exact sandbox when the demo exits. Set
`ORI_KEEP_DEMO_SANDBOX=1` when the sandbox needs to be retained for debugging.

#### `wt plan --issue <N>` — plan a Ready Issue

Planning and implementation are separate stages. `wt plan` handles the first:
it reads the Issue once, writes a snapshot plus a size-routed starter
checklist into `ori-agent-dev/tasks/`, and starts a **Pi** session there to
finish the plan. `wt start` handles the second, on Claude.

```bash
wt plan --issue 342          # show the plan, then ask before writing anything
wt plan --issue 342 --yes    # same plan, no prompt
```

| Issue size | What Pi is told to do first |
|---|---|
| `size:quick`, `size:planned` | Generate parent tasks, wait for `Go`, then expand them — no PRD |
| `size:prd` | Ask 3–5 clarifying questions, write the PRD, *then* generate parent tasks |

It accepts only an open Issue that currently matches the same Ready semantics
`./scripts/devops.sh ready` uses and carries exactly one `size:*` label;
anything else stops before writing a file. It never comments on, labels, or
otherwise changes the Issue. Re-running on the same Issue resumes: an existing
PRD or a real (non-starter) task list is never replaced.

If Herdr is unavailable, the planning files are still written and the command
prints the exact retry — they are never rolled back.

The feature slug is `<issue-number>-<title-slug>`, and the number comes first
because it cannot change. Renaming the Issue mid-planning reuses the existing
slug rather than deriving a second identity.

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
./scripts/devops.sh status             # which group each task list is on
./scripts/devops.sh release            # what has merged to dev since the latest release
./scripts/devops.sh view <number>      # one Issue in full
./scripts/devops.sh new <title>                         # quick title-only capture
./scripts/devops.sh new <title> --body <text>           # optional inline context
./scripts/devops.sh new <title> --body-file <path|->     # Markdown file or stdin
./scripts/devops.sh decide <n> <answers> [--rationale <why>] # marked decision
./scripts/devops.sh answer <n> <answers>                   # alias for decide
./scripts/devops.sh approve <n>        # add the approved label (confirm-gated)
./scripts/devops.sh unapprove <n>      # remove it again
```

`ready` is the view worth living in: feature proposals plus backlog Issues that
are neither already covered by a proposal (`bundled`) nor already chosen
(`approved`). It exists so the same work never appears twice — once as a bundle
and again as its members.

In a terminal, the colorful picker uses `↑/↓` or `j/k` to select an Issue,
`←/→` or `h/l` to change views, and the same `1`–`5` view order shown by the
line REPL. `Enter` opens an Issue and keeps you there with an action bar:
`c` records a decision, `s` starts its Pi planner, `r` refreshes the
opened Issue, and Enter returns to the list. Decide and Plan each appear only
when that Issue's own live labels make them eligible - the bar is drawn for
one known Issue, so it never offers a write or a command the Issue does not
actually support. The list's `c` key is a shortcut that opens the same Issue
directly at its decision answers. `n` captures a new Issue with an optional
body, `o` approves it, `s` starts Pi planning for the selected Ready row,
`r` refreshes the list, `?` shows help, and `q` quits. At the new-Issue body
prompt, a blank line keeps capture title-only and `:edit` opens `$VISUAL` or
`$EDITOR` for multiline Markdown.

In a pipe or redirected shell, the line REPL remains available: use `1/a`,
`2/d`, `3/b`, `4/f`, or `5/y` to change views, `v <number>` to inspect,
`n <title>` to capture, `c <number> <answers>` to decide, and `ok <number>` to
approve. The default and `all` view include every author; closed Issues stay out
of lists.

Every row shows the Issue's `size:*` label in its own column, so a long label
list can never truncate away the signal that says whether to open a PRD first.

**Planning key.** In the Ready view, `s` runs `wt plan --issue <N>` for the
selected row and launches a Herdr-managed Pi planner after wt shows its normal
summary and confirmation. The same `s` is also on the opened-Issue action bar
(`Enter` on any row), where it reads that Issue's own live labels through the
same `labels_are_ready` rule the Ready view itself uses, so it works from any
view - not only rows already sitting in Ready. The picker starts the sourced
zsh `wt` function in a child process; `wt plan` then repeats the live
eligibility check before writing files or contacting Herdr. The key is a no-op
with a clear message outside the Ready view or on an empty list; the
opened-Issue bar gives the same clear refusal for a non-Ready Issue or a label
read that fails, and never offers `[s] Plan` when it would refuse.

**In-flight status.** A second column shows whether work has already started and
how far it has got — `2/7 wt` means two of seven task-list groups are done and a
worktree is checked out; `br` means only a branch exists. `./scripts/devops.sh
status` prints the same thing for every task list at once:

```
  0/8     worktree           workspace-ticket-management
  3/3     branch    #339     339-workspace-map-camera-framing
  5/6     -                  build-my-hq-button-fix
```

This is **entirely local** — plain `git worktree list`, `git branch`, and the
task files on disk. It is deliberately *not* a Herdr integration:
`scripts/wt-herd.test.sh` asserts this script never reaches for the devflow
bridge, which is the whole point of the REPL having replaced that helper. The
Issue-number-first convention already encodes the link in the branch name
(`fix/339-slug`) and the task file (`tasks-339-slug.md`), so no network, no
second contract, and no `wt` dependency is needed. Branches predating that
convention still resolve, by slug.

Task files are gitignored and live in one place, the dev worktree's `tasks/`, so
progress is read from disk rather than from anything pushed. That is
deliberately fresher: checkboxes get ticked while you work, but a pushed copy
would only update when you commit. It also means the numbers are exactly as
honest as the file — a shipped feature whose boxes were never ticked will read
`0/6`.

**`release` — what has not shipped yet.** `./scripts/devops.sh release` prints
the latest GitHub Release's tag and publish time, plus how many PRs have merged
into `main` strictly after that instant:

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

**Writes.** `new`, `decide`, `approve` and `unapprove` are the only mutating
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
gh issue create --title "<title>" --body "<optional context>"
gh issue list --state open --limit 1000 --json number,title,author,labels,url,createdAt,updatedAt
```

The script exits `0` after a completed operation, `2` for invalid arguments,
and otherwise preserves the failed `gh` command's status.

New Issue-backed work uses the Issue number in its identity —
`<issue-number>-<slug>`, for example `292-coordinate-based-map` — so a PRD,
task list, worktree, branch, and pull request can be joined on an exact number
instead of a title that may change. Existing features whose slugs have no
number remain valid and are never renamed.

### `wt done` — finish delivery and clean up

```bash
wt done 292-coordinate-based-map
wt done 292-coordinate-based-map --keep-issue-open # intentional exception
```

For Issue-backed work, `wt done` treats the exact
`tasks/issue-<feature>.md` snapshot created by `wt plan` as the attachment. It
requires the generated header marker to agree with the number-first feature
slug and a merged PR for the exact branch targeting `dev`. An open attached
Issue is closed as `completed` with a comment linking the merged PR; an already
closed Issue is left unchanged. This explicit transition is necessary because
delivery PRs target `dev`, not the repository's default branch, so GitHub
closing keywords do not complete the Issue at that merge. A number-looking slug
without the snapshot is never enough to infer an Issue, so ad-hoc and legacy
cleanup keeps working.

Issue inspection or closure failures preserve the feature worktree so the same
command can be retried. `--keep-issue-open` is the explicit escape hatch for a
feature whose Issue must intentionally remain open; it skips all Issue reads
and writes. The Herdr guard still runs before archival, Issue closure, or Git
removal, and `--herdr-override` retains its separate safety meaning.

### `wt status` — feature overview

```bash
wt status                      # compact, feature-first overview
wt status --feature <slug>     # one feature in detail
wt status --json               # complete normalized snapshot (schema v2)
wt status --watch              # live board
wt status --no-color           # plain text; NO_COLOR is honored too
wt status --worktrees          # the legacy Git-only worktree table
```

`wt status` answers "what features exist, and where is each one" — joining
planning artifacts, feature worktrees, local Git, GitHub pull requests, and live
Herdr agents on the exact feature slug. It is read-only: it never writes
planning files, Git, GitHub, bridge, or Herdr state. It describes selected and
executing work; unselected ideas live in GitHub Issues and are listed by
`./scripts/devops.sh`.

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
