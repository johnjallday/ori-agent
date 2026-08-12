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
wt new <name>          # create a worktree
wt rm <name>           # remove a worktree
wt ls                  # list worktrees
wt cd <name>           # navigate to a worktree
```
Source it (don't execute) so `cd` affects your current shell.

`wt demo` removes its exact sandbox when the demo exits. Set
`ORI_KEEP_DEMO_SANDBOX=1` when the sandbox needs to be retained for debugging.

### `scripts/devops.sh` — open Issues by workflow label

One command covers the human issue workflow:

```bash
./scripts/devops.sh                    # list every open Issue, then start the REPL
./scripts/devops.sh ready              # what you can actually pick up now
./scripts/devops.sh all                # every open Issue, one-shot
./scripts/devops.sh decisions          # label: needs-decision
./scripts/devops.sh backlog            # label: backlog
./scripts/devops.sh proposals          # label: feature-proposal
./scripts/devops.sh view <number>      # one Issue in full
./scripts/devops.sh new <title>        # capture an Issue (confirm-gated)
./scripts/devops.sh answer <n> <text>  # post a comment (confirm-gated)
./scripts/devops.sh approve <n>        # add the approved label (confirm-gated)
./scripts/devops.sh unapprove <n>      # remove it again
```

`ready` is the view worth living in: feature proposals plus backlog Issues that
are neither already covered by a proposal (`bundled`) nor already chosen
(`approved`). It exists so the same work never appears twice — once as a bundle
and again as its members.

In a terminal, the colorful picker uses `↑/↓` or `j/k` to select an Issue,
`←/→` or `h/l` to change views, `Enter` to inspect it, `n` to capture a new
Issue, `c` to answer its open questions, `o` to approve it, `r` to refresh, and
`q` to quit. In a pipe or redirected shell, the line REPL remains available: use
`1/a`, `2/d`, `3/b`, `4/f`, or `5/y` to change views, `v <number>` to inspect,
`n <title>` to capture, `c <number> <text>` to answer, and `ok <number>` to
approve. The default and `all` view include every author; closed Issues stay out
of lists.

Every row shows the Issue's `size:*` label in its own column, so a long label
list can never truncate away the signal that says whether to open a PRD first.

**Writes.** `new`, `answer`, `approve` and `unapprove` are the only mutating
commands. Each prints what it will do and asks for confirmation; without a
terminal they refuse unless given `--yes`, so a pipe can never write by
accident.

`new` exists because capture is supposed to take ten seconds — a title is
enough, and the grooming routine researches and specs it on its next run. **It
deliberately applies no labels.** Adding `backlog` here would skip the spec step
the whole pipeline is built around, and `needs-decision` would assert a spec
exists when none does. Titles are passed through verbatim, so an ampersand stays
an ampersand rather than becoming a literal `&amp;`.

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

Capture and machine-readable reads use the GitHub CLI directly:

```bash
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
