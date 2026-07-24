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
- `clean-test-artifacts.sh` — Preview or delete Ori-owned test logs and
  temporary directories left in the system temp folder. Run it with no
  arguments for a dry run, with `--delete` to clean, or use
  `make clean-test-artifacts`. It only matches known `ori-test-*`,
  `ori-vault-files-*`, `ori-agent-test-*`, `ori-db-test-*`, and
  `ori-db-migration-*` names.
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

### Herdr devflow bridge

The opt-in repository-owned bridge lives in tools/herdr-devflow/ and is exposed
through the sourced wt function:

~~~bash
wt herd setup
wt herd doctor
wt herd status --watch
~~~

wt herd setup installs a stable user-local helper/plugin copy, rather than
linking Herdr to a removable feature worktree. wt start <feature> then attempts
an automatic Herdr handoff; for linked checkouts it supplies Herdr with the
normal Git source checkout plus the feature path, while Ori remains the only
Git-worktree creator/remover. wt start <feature> --no-herdr skips it once. See
[Herdr Devflow Bridge](../docs/herdr-devflow.md) for exact feature-scoped agent
selection, one-time continuations, guarded cleanup, and recovery.

Run make test-herdr-devflow for focused helper, shell, and wt tests. It also
runs make test-herdr-devflow-cross, which cross-compiles the local helper for
macOS, Linux, and Windows; only macOS registers and executes the LaunchAgent
scheduler.

## Requirements

- Go toolchain installed (see `check-go-version.sh` for the required version)
- Run from the project root
