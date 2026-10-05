# Ori development-tool extraction contract

**Status: staged source cutover, not an installed capability.** Ori's three
entrypoints now delegate to the selected companion. Original helper sources and
an immutable legacy-script fixture remain until the final compatibility gate.
Independent companion gates, fake-only Ori adapter checks and the paired Git
lifecycle pass. Installed-runtime validation is still incomplete: do not run
setup or delete the remaining embedded sources to try this candidate.

Independent CLI milestone: companion `ca5b6b3c4f743c0c79a163fdcd096a83c8831c02`
(`make build test test-race check cross` passed). This is not a runtime cutover
pin; the adapter/installed-caller gates below still apply.
Wrapper/adapter milestone: companion `9d78c085996666ee0a32a8805a00eecfad46c548`,
validated with the paired fixture lifecycle and same-shell candidate rollback.
Live/runtime compatibility remains pending group 4.

The approved destination is the separate, local-only `ori-devtools` repository at
`/Users/jjdev/Projects/ori/devtools`. No remote, publication, installer run, or
system-service update is implied. This remains personal **Ori-specific** tooling,
not a multi-project framework. Application builds must work without the toolbox.

## Three roots, never interchangeable

| Root | Owns | Resolution |
| --- | --- | --- |
| Tool source/assets | Workflow scripts, helper/daemon Go sources, prompts, plugin templates, tool tests/docs | Explicit `ORI_DEVTOOLS_HOME`, otherwise the one stable local location above |
| Target Ori worktree | Git/GitHub operations, `.herdr/devflow.toml`, `tasks/`, canonical task-planning skill, product adapters | Wrapper anchoring or explicit target as below; common Git directory supplies repository identity |
| Stable runtime/state | Installed helper/plugin, bridge state, logs, usage, continuation dispatcher | Existing `HERDR_DEVFLOW_HOME` / `--home`, otherwise `os.UserConfigDir()/herdr/ori-devflow` |

All roots must be absolute/canonical, including symlink ancestors. Runtime/state
must not be inside either source checkout. A tool Git directory must never supply
an Ori repository ID, GitHub context, branch inventory, or planning directory.
`--repo-root` and `HERDR_DEVFLOW_REPO_ROOT` always mean **Ori**, not build inputs.
`HERDR_DEVFLOW_CONFIG` remains an explicit config override, never target selection.
Stable installed commands that only use saved runtime state must not require a
checkout; setup/build and repository operations require the appropriate roots.

Preserve `ori.devflow`, bridge state schema 1, config schema 1, snapshot headers,
existing `HERDR_DEVFLOW_*` configuration overrides, and all wake identities.
The [accepted wake v1 contract](architecture/herdr-standalone-wake-v1-contract.md)
is unchanged. Away's `com.ori.wt-away-tick` owner is distinct from
`com.ori.herdr-wake`; neither may cancel the other's events.

## Selection and targeting (to implement)

### Tool selection

1. A **set** `ORI_DEVTOOLS_HOME` selects exactly that trusted local checkout.
   Empty, relative, control-character-containing, missing, or malformed values
   are errors, not requests to try another installation.
2. With the variable absent, Ori wrappers and restored `wt` functions use only
   the approved stable path above. Directly invoking a companion script by path
   explicitly selects that script's own checkout; a conflicting override fails.
   Do not search sibling folders, `PATH`, installed plugins, or user shell startup files.
3. Before executing candidate code, check a small, inert, versioned contract
   file (`devtools-contract`, exact content `ori-devtools-v1` plus newline), the
   required entrypoint and module/assets. This is a compatibility assertion,
   **not** a security signature; only select source you trust. Reject unknown
   versions before Git, GitHub, agent, or state mutation.
4. A selected-but-invalid tool fails with its exact path and recovery guidance.
   Never fetch, install, update, or silently fall back. Normal Ori CI uses fake
   installations; paired tests select a candidate explicitly.

The stable path is intentionally one owner-selected local location, not a
registry/configuration language. A future move is an explicit selector change
with tests. No process persists a feature-worktree candidate selection into a
launchd job, installed plugin, global shell file, or runtime config.

### Ori target precedence

| Invocation | Target rule |
| --- | --- |
| Ori `scripts/devops.sh` / helper / Away compatibility entrypoints | Anchor to the containing Ori checkout, even from an unrelated CWD. An explicit target override must canonicalize to that same root or be rejected; use the external entrypoint to deliberately target another checkout. |
| External scripts / helper called directly | Explicit `--repo-root` (where accepted), then `HERDR_DEVFLOW_REPO_ROOT`, then caller's Git root. Conflicting explicit selections fail. Never use tool script location as target fallback. |
| Sourced `wt` | Resolve caller's current Ori Git root on **every invocation**, never the file's source-time location. Outside a checkout require an explicit valid target. Inside a checkout reject a conflicting target override rather than silently operating elsewhere. Navigation executes in the caller's shell. |
| Installed plugin / continuation dispatcher | Preserve fixed stable helper argv and runtime-only behavior. Repository-scoped operations use validated saved Ori feature paths or explicit target, not launchd CWD or toolbox location. |
| Legacy Away tick/support shims | Their Ori path supplies the target; the stable selector works with launchd's minimal environment. No interactive startup files. Preserve source-only dispatcher use where callers require functions, not just an executable. |

Validate a target as a real Git root with the Ori module identity
`github.com/johnjallday/ori-agent` and the necessary Ori files for the operation.
A missing operation-specific adapter on a migrated target is a refusal, not a
successful no-op. An arbitrary Git repository, including the toolbox itself,
is not a valid target. Historical roots that cannot establish Ori identity get
an actionable refusal. Do not require `.herdr` config for legacy Git-only reads;
bridge mutation still requires valid bridge configuration/compatibility.

Use argument arrays and `"$@"`, never `eval` or interpolated shell programs.
Preserve stdin/stdout/stderr and child status. Selection/usage errors return 2;
missing selected tools/dependencies return nonzero with recovery. Cleanup's
special occupancy status 20 must survive wrappers. Sourced wrappers must return,
not exit the caller's shell or hide `cd` in a subshell. All safety defaults,
including protected-worktree guards, must initialize inside restored functions.
Avoid cached target/source globals surviving a switch between worktrees.

### Source versus prebuilt helper

- `HERDR_DEVFLOW_USE_SOURCE=1` runs only selected companion source with
  `GOWORK=off`; no old Ori `bin/` or stable-runtime helper substitution. Go program
  arguments follow the package path directly, with no extra `--` token.
- An explicit `ORI_DEVTOOLS_HOME` is candidate mode: prefer its source by default
  so an ignored old binary cannot masquerade as the candidate. An explicit
  `HERDR_DEVFLOW_BINARY` may select a paired prebuilt only after its companion
  contract/revision evidence is verified; forced-source mode overrides it.
- `make build` in the companion stamps the helper's `build-info` with a SHA-256
  digest of the Go files, module files, and contract marker. The launcher checks
  that against the selected source before executing an explicit prebuilt. This
  is local compatibility evidence, not authentication of an untrusted binary.
- Without candidate/source forcing, a prebuilt from the selected tool may be
  used only with matching build provenance. Otherwise build selected source,
  not an unrelated install. An explicitly selected binary that fails is an
  error, not an excuse to choose another binary.
- Stable runtime installation and staged wake builds use **companion** source,
  never the target's module or ambient `go.work`. Target config/state identity
  remains Ori-owned. Installed plugin/dispatcher invocation continues to use
  the already-installed stable binary; source selection alone never refreshes it.

## Authoritative move / retain / adapter manifest

Extraction source revision: `8c076a15bb32895c114705a1e3a2cdf72395572c` from
`https://github.com/johnjallday/ori-agent.git`. Preserve relative paths, Apache-2.0
`LICENSE`, attribution, and exact source revision in companion provenance.
Copy tracked source only; no tasks/history, secrets, state, caches, binaries, or
installed copies. Do not rewrite Ori history. Unlisted application files stay.

| Current Ori paths | Final ownership / action |
| --- | --- |
| `scripts/devops.sh`, `scripts/wt.sh`, `scripts/herdr-devflow.sh` | Move implementations; retain thin Ori entrypoints, including sourced-zsh `wt` |
| `scripts/lib/devflow-common.sh`, `scripts/lib/devops-explore.sh`, `scripts/lib/devops-explore-launch.sh`, `scripts/lib/devops-explore-evidence.py`, `scripts/devops-prompts/` | Move complete workflow libraries/assets |
| `tools/herdr-devflow/` (all commands, internal packages/tests/testdata, manifest, plugin, launchd templates) | Move complete tree; independent module with no Ori import/replace/symlink |
| `scripts/away-dispatch.sh`, `scripts/away-tick.sh`, `scripts/away/` | Move implementation/templates/installers; keep legacy tick/dispatcher/support entrypoint shims in Ori. Installed privileged artifacts are not moved or edited. |
| `scripts/devops-cli.test.sh`, `scripts/devops-explore.test.py`, `scripts/herdr-devflow.test.sh`, `scripts/wt-config.test.sh`, `scripts/wt-herd.test.sh`, `scripts/wt-done-archive.test.sh`, `scripts/wt-done-repl.test.sh`, `scripts/away-dispatch.test.sh` | Move tool behavior coverage; split product-specific RC/provisioning/pre-PR assertions into retained adapter tests, not deleted coverage. RC assertions now also run independently in Ori's `scripts/devops-release-candidate.test.sh` |
| `scripts/wt-demo-codex.test.sh` | Retain Ori demo-isolation behavior coverage; companion adds forwarding-only tests |
| `scripts/lib/devops-release-candidate.sh`, release/installer/report/smoke scripts and tests, `docs/RELEASE_CHECKLIST.md`, `docs/RC_TEST_PROTOCOL.md` | Retain product mechanics. Expose narrow target-side RC invocation; companion retains menu/read-only release views. RC library currently relies on menu globals/functions: adapt deliberately, not by copying its implementation. |
| `scripts/demo-server.sh`, `scripts/build-folder-picker.sh`, `scripts/ci-local.sh`, `scripts/smoke.sh`, all other app build/test/CI/README-capture/release tooling | Retain in Ori |
| `wt_provision_worktree` post-Git setup; `wt demo`; `wt pr` check execution | Move project behavior into narrow Ori adapters; Git creation/navigation/confirmation/push/PR flow remains tool-owned |
| `.herdr/devflow.toml`, `.agents/skills/task-planning/`, `.claude/skills/task-planning/`, `internal/workspaceplan/`, `tasks/` | Retain unchanged ownership/defaults; add approved-task producer fixture assertion |
| `docs/herdr-devflow.md`, `docs/herdr-devflow-claude-usage-signal.md`, `docs/devops-explore.md`, `docs/away-dispatcher.md`, `docs/herdr-standalone-wake-dogfood.md`, `docs/architecture/herdr-standalone-wake-v1-contract.md` | Move tool-operating material; keep short compatibility links where callers need discovery. Move accepted wake contract verbatim. |
| `.agents/skills/setup-herdr/` | Move operating content/metadata with tools; keep a tested Ori delegating entrypoint, no global skill installation |
| `Makefile`, `go.mod`, `go.sum`, `scripts/list-unit-packages{,.test}.sh`, `.github/workflows/ci.yml` | Retain application ownership; transfer tool gates and remove only actually unused dependencies; ordinary Ori gates run fake-tool adapter tests |
| `AGENTS.md`, `CLAUDE.md`, `docs/README.md`, `scripts/README.md`, `scripts/check-backlog-docs.sh` | Retain, shorten/update links and structural expectations without losing safety/worktree rules |
| `scripts/run-test-command.test.sh` | Retain test-runner coverage; replace its tool-package example with an Ori-owned fixture |
| `scripts/readme-new-refresh-worktree.sh`, `scripts/demo.sh`, `docs/feature-discovery/{DOGFOODING,PLAYBOOK}.md` | Retain product/demo/readme ownership; audit references, don't relocate merely because they mention `wt` |
| `docs/devtools.md`, new selector/adapters/fake integration tests | Retain as the cross-repository contract, not a second workflow manual |
| `scripts/devtools-baseline.test.py` | Temporary pre-cutover characterization harness; retire or port it at deletion, never require embedded tool sources in normal post-cutover Ori tests |
| `internal/workspaceplan/devtools_fixture_test.go`, `internal/workspaceplan/testdata/devtools-approved-tasks.md` | Retain producer contract; companion carries identical Markdown and consumer assertion |

**Temporary layout:** keep all legacy files at their original paths throughout
group 2. Before group 3 replaces entrypoints, retain an immutable fixture copy of
the manifest's original scripts/libraries/assets under
`tests/fixtures/devtools-legacy/` with provenance; never select it implicitly at
runtime. An exact `git archive` of the baseline offers recovery independent of
replaced wrappers. Group 5 removes migration copies only after the durable
companion and both sides' tests pass. Old active worktrees retain their own files;
no bulk edits, rebases, or runtime resets.

## Adapter invariants

- **Provisioning:** tool owns Git creation. Ori owns non-overwriting Claude local
  permissions with the existing deny floor, folder-picker build, `.env` copy
  only from dev when target absent (`cp -p`), and npm setup. Preserve actual
  warning/return behavior: the current function explicitly fails Git creation
  and warns on secret-copy failure; it has no explicit failure guard for
  picker/npm before its final `return 0`. Test caller shell options before
  changing that behavior. Use invented secret fixtures only.
- **Demo:** do not replace `wt demo` with a raw call to `demo-server.sh`.
  `wt demo` passes Codex home unless opted out and removes its sandbox on exit;
  the standalone script does neither automatically. Preserve port 8931, build
  in target, isolated HOME/data/CWD, desktop suppression, browser opt-in,
  Codex opt-out, exit status and guarded cleanup in an Ori-owned wrapper.
- **Pre-PR:** execute target `make ci-local` before push; failure pushes nothing;
  keep explicit `--skip-checks` visible. The old implementation skips a missing
  `ci-local.sh`; new tools must not apply that shortcut to a migrated checkout.
- **Cleanup:** never treat missing selected helper/config/adapter as proof of
  no agents or schedules. Preserve active-occupancy refusal, interactive
  unknown-state acknowledgement, trusted Issue membership and task archiving.
- **Release:** target-owned actions retain promotion confirmation, installer
  isolation and GitHub environment review. A toolbox Makefile/release script
  is never a substitute for target mechanics.

### Project adapter API (v1)

The companion invokes the target's `scripts/devtools-project.sh` with zsh and
separate argv: `v1 provision <dev-path> <permission-mode>`, `v1 demo [port]`, or
`v1 pre-pr`. Ori validates the target and same-repository provisioning source;
its library owns permissions/secrets, picker/npm, demo environment/cleanup, and
target `make ci-local`. No configuration/model defaults move to the companion.
Provisioning supports the documented `acceptEdits`/`bypassPermissions` modes and
refuses malformed modes rather than interpolating them into JSON.

Creating work against a base without the adapter refuses before Git creation.
A migrated checkout missing its adapter also refuses pre-PR checks. An older
checkout without the selector may still run its existing `scripts/ci-local.sh`
through `make ci-local`; no gate at all refuses unless `--skip-checks` was
explicit. Missing demo/RC adapters refuse rather than load old workflow code.

`make test-devtools` tests wrappers/project mechanics with fake tools and invented
secrets, independently of the companion. It runs in `ci-local` and the CI
adapter job. `scripts/devtools-integration.test.sh --ori /absolute/ori --devtools
/absolute/tools` stages exact adapters/wrappers into temporary Git repositories
without embedded helpers. It checks artifacts and the lifecycle with fake outward
effects; `--fixtures-only` limits it to the renderer/parser contract. Neither
command installs tools or touches live state.

## Artifact fixture and compatibility matrix

Ori owns `internal/workspaceplan/testdata/devtools-approved-tasks.md`, rendered
from the minimal Bridge Plan already tested by the embedded consumer. The
producer test compares exact bytes. Companion tests will carry an identical
fixture and assert the next task is
`1.1 Wire the approved path — _unassigned_`, with no planning-starter marker.
Run `python3 scripts/devtools-baseline.test.py` for the legacy CLI characterization
(no real worktree creation or live service calls). Source-mode unknown commands
currently exit 1 through `go run`, while prebuilt execution returns 2; preserve
and explicitly test that distinction rather than claim status parity.

Optional paired validation compares fixture bytes/hashes between exact candidate
paths. Never copy/import the renderer into the companion. A mismatch is a
cross-repository gate failure requiring review, not automatic fixture overwrite.

| Combination | Required behavior / evidence |
| --- | --- |
| Old Ori + its old scripts | Keep current behavior; no bulk mutation. Existing fixture coverage is baseline only. |
| New Ori wrappers + v1 candidate | Explicit independent roots, matching contract/fixture, fake-only normal tests plus paired candidate gate |
| New wrappers + absent/malformed/stale tool | Nonzero diagnostic, no fallback, no mutation |
| Candidate explicitly targets old Ori | Allow validated read-only paths; retained demo/CI/RC adapters may be used only where their tested contract exists. Refuse unsupported provisioning/demo/RC, missing CI or unverified cleanup with recovery. Never source an entire old `wt` as a fallback. |
| Legacy installed plugin / continuation dispatcher | Same stable helper and runtime-only argv; old installed binary remains unchanged until separately authorized setup |
| Legacy Away tick/support paths | Retained Ori shims resolve stable tool with sanitized environment; preserve source-only library contract |
| Forced-source cleanup | Build/run selected companion source against target Ori; never use stale Ori `bin/` or quietly skip guard |
| Toolbox or unrelated checkout as target | Reject before external calls or state writes |
| Existing feature/planner/schedule/overnight state | Same common-dir identity and schema; unknown/corrupt data fails closed; no reset, duplicate agent, or namespace migration |

New-tool rows are **requirements, not verified support** until paired tests pass.
Group-1 fixtures used inert Git metadata under the original no-worktree rule.
The user subsequently authorized linked worktrees only inside disposable test
repositories. The lifecycle gates exercise these; existing user worktrees remain
untouched. Live agents, GitHub writes and platform operations remain untested.

## Recovery and cutover gate

Until a committed companion exists, continue using the original Ori commands.
After cutover, an owner can select a known-good **durable** local candidate with
`ORI_DEVTOOLS_HOME`; unset it to return to the approved stable location. Validate
its contract, source revision, fixture hash and helper provenance before use.
A bad explicit selection must fail rather than appear to roll back successfully.
For an unavailable wrapper/selector, invoke the known-good companion script
explicitly with `HERDR_DEVFLOW_REPO_ROOT` set to the intended Ori root; for `wt`,
source that companion's `scripts/wt.sh` from the target checkout. These recovery
paths must pass the same integration tests before deleting embedded tooling.

Rollback selects source; it does **not** reinstall a helper, reset state, cancel
jobs, switch active agents, or modify root-owned wake files. Installed-runtime
refresh is a separate explicitly authorized operation. Detailed operator commands
and exact tested revisions will be added at group 4; all live runtime/privileged
capabilities remain **NOT RUN** until directly exercised with authorization.

Execution evidence and the checkout inventory are in the ignored active
`tasks/findings-extract-devflow-tooling.md`. The canonical planning protocol stays
in [task-planning](../.agents/skills/task-planning/SKILL.md).
