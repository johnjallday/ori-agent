# Scripts

Application-owned development and CI scripts for Ori Agent. Run from the
project root unless noted otherwise. Personal workflow implementations live in
the separate local toolbox; see [the boundary contract](../docs/devtools.md).

> Tool capabilities are provided via MCP servers and skills — the legacy gRPC
> plugin system has been removed. There are no plugin build/clean scripts.

## Build

### `build.sh` — Full local build

```bash
./scripts/build.sh
```

Builds `bin/ori-agent` and, on macOS, `bin/ori-menubar`, embedding version, Git
commit and build date. `BUILD_MENUBAR=false` skips the menu bar app;
`GOOS` / `GOARCH` select a cross-compile target.

### `build-server.sh` — Server only

```bash
./scripts/build-server.sh
```

Builds just `bin/ori-agent` for faster iteration.

### `build-folder-picker.sh` — Native folder-picker helper

```bash
./scripts/build-folder-picker.sh
```

Builds the workspace launcher's platform helper; also used by release/smoke
workflows and Ori's worktree provisioning adapter.

## Release

[RELEASE_CHECKLIST.md](../docs/RELEASE_CHECKLIST.md) is authoritative.

- `release.sh candidate [--force]`: confirm/dispatch RC preparation. A frozen
  release branch lets `dev` keep accepting feature PRs.
- `release.sh candidate vX.Y.Z`: build the next RC after stabilization fixes.
- `release.sh promote vX.Y.Z-rc.N`: explicitly approve the exact tested RC.
- `create-release.sh vX.Y.Z-rc.N`: promotion compatibility wrapper; no direct
  stable tagging.
- `release-ready.sh`: read readiness without pushing/publishing.
- `release-candidate.py`: Actions lifecycle, exact refs, green workflow evidence,
  atomic tagging/promotion and release merge-back PR.
- `rc_test_report.py`: generate the pinned RC's blank manual test card without
  overwriting evidence or awarding PASS. Follow `docs/RC_TEST_PROTOCOL.md`.
- `smoke-installed.py`: verify a downloaded installer's health/exact version
  using disposable application state.
- `pre-release-check.sh`: legacy aggregate checks, not a release driver. Do not
  use its version-bump/autocommit behavior on shared/live profiles.
- `make test-release`: offline lifecycle/probe regressions.
- `devops-release-candidate.test.sh`: retained product RC adapter coverage. The
  external DevOps menu delegates to Ori's `lib/devops-release-candidate.sh`.

## Testing & diagnostics

- `ci-local.sh` / `make ci-local`: CI-equivalent gates with pinned scoped checks;
  `make ci-local-quick` is the mid-work subset.
- `reaper-demo.sh`: build/verify the coordinated local REAPER plugin and launch
  isolated Ori. `test` runs coordinated browser specs; `artifact` refreshes only
  the root binary. This separate integration is not devflow tooling.
- `demo-server.sh`: standalone isolated HOME/data server, browser opt-in.
  `wt demo` uses Ori's separate adapter preserving Codex pass-through/opt-out,
  default port 8931 and automatic sandbox cleanup; they are not interchangeable.
- `test-all-installers.sh`, `docker-test-installers.sh`: installer checks.
- `test-with-ollama.sh`: provider-backed suite, opt-in.
- `diagnose-test-failures.sh`: summarize failing Go tests.
- `run-test-command.sh`: run-owned temp sandbox for Go/Node/Playwright, removed
  on success/failure/interruption. `ORI_KEEP_TEST_SANDBOX=1` preserves it.
- `prune-test-cache.sh`: remove stale Ori-owned temp artifacts older than 24
  hours; clear only the Go build cache above 20 GiB, not module/browser caches.
  `ORI_SKIP_CACHE_PRUNE=1` opts out; `make cache-report` previews.
- `clean-test-artifacts.sh`: dry-run by default; `--delete` or
  `make clean-test-artifacts` cleans known Ori-owned prefixes. Manual cleanup
  has no age guard unless `--older-than-hours` is set.
- `list-unit-packages.sh`: application package/platform selection shared by CI
  and local tests. External tool packages are not part of this module.
- `check-cross-platform.sh`, `check-go-version.sh`: app build/toolchain checks.

## Lint & maintenance

- `make lint-new`: changed-code ratchet, not whole-tree legacy findings.
- `fix-all-lint.sh`: opt-in linter/fix aggregate.
- `fix-orihttp-errcheck.sh` / `.go`: opt-in (`FIX_ORIHTTP_ERRCHECK=1`) response
  error-handling helper.
- `merge-dependabot.sh`: batch-merge Dependabot PRs (an explicit remote write).
- `update-readme.sh`: release version/Go badges only, not product screenshots.
- `readme-refresh.sh`: staged screenshot capture/cleanup; use `make readme-audit`,
  `readme-capture`, `readme-propose`, `readme-check` with
  `docs/README_MAINTENANCE.md`. `pre-release-check.sh` checks that contract before
  badge updates.
- `check-backlog-docs.sh`: prevent retired file-backed workflow instructions.

## Assets

`generate-app-icon.sh` and `generate-menubar-icons.sh` produce application assets.

## Workflow compatibility entrypoints

```zsh
source scripts/wt.sh
wt status
./scripts/devops.sh ready
```

`wt.sh` is sourced zsh so navigation changes the caller's directory. `devops.sh`,
`herdr-devflow.sh`, `away-dispatch.sh`, `away-tick.sh` and `away/*.sh` are thin
compatibility paths. Implementations, prompts, platform templates and operating
docs belong to the selected toolbox, not hidden copies in Ori. Read
[docs/devtools.md](../docs/devtools.md) before selection, setup or recovery.
The established command names remain; missing/incompatible source fails without
fetching or falling back. Legacy Away paths remain available to installed jobs.

Ori owns `devtools-project.sh` / `lib/devtools-project.zsh`: same-repository
non-overwriting permission/secret provisioning, picker/npm setup, isolated demo,
and target pre-PR gates. Release implementation stays here. The canonical
planning protocol and `.herdr/devflow.toml` also remain Ori-owned.

- `make test-devtools`: normal offline wrapper/adapter coverage using fake tools;
  no companion checkout or installation required.
- `devtools-integration.test.sh --ori /absolute/ori --devtools /absolute/tools`:
  optional exact-candidate artifact/lifecycle checks with disposable Git and
  fake outward effects; `--fixtures-only` narrows to producer/consumer bytes.
- `make herdr-devflow`, `make test-herdr-devflow`, `make test-herdr-devflow-cross`:
  explicit convenience delegation to selected companion build/test/cross gates.
  Never prerequisites of normal app build/test. Binaries stay in the toolbox.
- `devtools-setup-skill.sh`: read-only resolver for the matching setup skill; no
  agent, installation or global harness change.

The selected toolbox's `docs/herdr-devflow.md`, `docs/devops-explore.md`,
`docs/away-dispatcher.md` and `docs/cutover.md` own command details and runtime
procedures. Source rollback does not reset state, reinstall helpers or cancel
jobs. Live/privileged changes require their own authorization.

## Requirements

Go toolchain installed (see `check-go-version.sh`); run from the project root.
External workflow commands additionally require the explicitly selected local
source and their documented dependencies. Application commands do not.
