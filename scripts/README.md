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
wt backlog             # read Ideas, Doing, and retained terminal history
wt backlog prune       # force seven-day Shipped / dropped retention + push
```
Source it (don't execute) so `cd` affects your current shell.

`wt demo` removes its exact sandbox when the demo exits. Set
`ORI_KEEP_DEMO_SANDBOX=1` when the sandbox needs to be retained for debugging.

Every backlog mutation (`add`, `sync`, `wt start`, and `wt done`) automatically
removes date-prefixed `## Shipped / dropped` entries older than seven days,
then commits only `BACKLOG.md` and pushes `dev` through the existing scoped
workflow. `Ideas`, `Doing`, undated terminal records, and plain `wt backlog`
listing are never pruned. Set `WT_BACKLOG_RETENTION_DAYS` before sourcing the
script to choose a different retention window; Git remains the archive for
removed history.

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
planning artifacts, `BACKLOG.md`, feature worktrees, local Git, GitHub pull
requests, and live Herdr agents on the exact feature slug. It is read-only: it
never writes planning files, Git, GitHub, bridge, or Herdr state.

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

One-time continuations can opt into the shared macOS wake owner:

~~~bash
wt herd continue builder --feature <feature> --at "2026-07-30 05:00" --wake
~~~

The command succeeds only after a running Ori server confirms the wake was
programmed; Mac wake scheduling and administrator approval must already be
enabled in Settings → Device Capabilities.

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
