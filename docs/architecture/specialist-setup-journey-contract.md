# Specialist Setup Journey Contract

**Status:** Settled findings-first implementation contract

**Scope:** Generic specialist setup journeys, with REAPER as the only production declaration in v1
**Requirements:** `tasks/prd-specialist-setup-journeys.md`

This document records the host contracts that must be settled before product implementation. It is intentionally narrower than the PRD: the PRD owns product behavior, while this document identifies canonical stores, trusted resolution boundaries, idempotency rules, and test seams.

## 1. Authoritative project entry and filesystem scope

### 1.1 Current producer and consumer inventory

The current project-entry contract is split across `Workspace.ProjectPath` and `Workspace.SharedData["project_entry_path"]`:

- `internal/projecttemplates/project_entry.go` validates a template-authored path relative to the generated project and verifies the scaffold source contains a regular non-symlink file.
- `internal/projecttemplates/instantiate.go` resolves filename tokens and returns `InstantiationResult.ProjectEntryPath`.
- `internal/sessionhttp/project_templates.go` and `internal/chathttp/workspace_project_tools.go` are the two production writers. They persist `ProjectPath` plus `project_entry_path` to canonical `workspace.json`, mirror them to the session row, register the generated project directory, and publish `project.created`.
- `internal/workspace/project_entry_metadata.go` owns the legacy portable relative-path validation and storage helper. Workspace sync preserves that shared-data key.
- `internal/workspace/http_handlers_project_open.go` resolves the two relative path components beneath the canonical workspace folder, rejects symlinked/non-regular targets, and exposes the bodyless loopback-only OS-open action.
- `internal/server/workspace_surfaces.go` independently resolves an absolute entry for `workspacesurface.WorkspaceContext`. `internal/plugin/runtime_provider.go` passes that host-injected context to both Workspace Surface operations and plugin runtime-provider operations used by `runtimecapability` and the Setup Wizard.
- `internal/workspace/project_file_fallback.go` independently resolves the entry before staging and again before commit, then requires unchanged file identity/content before an atomic replacement.
- `internal/setupwizard/migrate.go` reads the legacy entry as non-authoritative migration evidence. It does not resolve or open the file.
- `internal/web/static/js/modules/workspace-detail.js`, `workspace-command.js`, and `sessions.js` decide whether to show/call the path-free project-open action. Browser code does not resolve the path.
- The REAPER service receives only the absolute `HostContext.ProjectEntry` injected by Ori. Its browser/service inputs cannot replace that value.

Directory and agent scope are separate from project-entry identity:

- `Workspace.DirectoryReferences` in canonical `workspace.json` owns linked-folder IDs and paths; `directory_references_json` is a session-store mirror.
- `internal/sessionhttp/workspace_scaffolding.go` gives concrete workspaces an explicit `workspace-files` filesystem MCP binding rooted at the managed workspace folder. Groups instead receive only `files/` and `notes/` roots; `sub-workspaces/` is deliberately excluded.
- `internal/projecttemplates/directory_ref.go` registers generated project directories but does not need to widen the existing whole-workspace root.
- `internal/workspace/agent_runtime_resolver.go` materializes explicit filesystem-binding roots. It synthesizes roots from a workspace's own Directory References only when no filesystem binding exists; it never traverses `ParentID` or child records.
- Broad native-MCP execution remains double gated by `Workspace.AllowNativeMCPCLI` and agent `Settings.AllowNativeMCPTools`. Its filesystem server receives only the exact materialized binding roots.
- Capability-scoped CLI execution in `internal/runtimecapability/service.go` starts at the workspace `files/` directory and adds only roots returned by a compiled capability adapter. Plugin symbolic scopes in `internal/plugin/scopes.go` resolve only host-injected roots.

### 1.2 Settled typed locator

Add one persisted v1 locator under `Workspace.SharedData["project_entry"]`:

```json
{
  "version": 1,
  "kind": "workspace_relative",
  "relative_path": "song/song.rpp"
}
```

or:

```json
{
  "version": 1,
  "kind": "directory_reference",
  "directory_reference_id": "stable-reference-id",
  "relative_path": "Song.rpp"
}
```

Rules:

1. `relative_path` is a non-empty portable slash path relative to the selected root. It rejects NUL, backslashes, absolute/volume/UNC forms, `.`/`..` segments, template tokens, empty segments, and bounded-length violations.
2. `workspace_relative` is relative to the canonical managed workspace folder, not `ProjectPath`. It must not carry `directory_reference_id`.
3. `directory_reference` requires exactly one stable Directory Reference ID belonging to that exact child workspace. No path is copied into the locator.
4. The typed key is authoritative when present. A malformed/unsupported typed value fails closed; code must not fall back to a stale legacy value.
5. `project_entry_path` remains read-compatible for existing workspaces only. Its locator is synthesized as `workspace_relative` by safely joining the validated legacy `ProjectPath` and entry. New writers store only the typed locator. Existing manifests keep their inert `project_entry.relative_path`; instantiation converts that declaration into the persisted workspace-relative locator.
6. Absolute paths exist only in a short-lived host result. They are never accepted from a client, journey declaration, plugin manifest, runtime operation, or agent.

The host exposes one resolver returning a trusted value equivalent to:

```go
type ResolvedProjectEntry struct {
    Locator     ProjectEntryLocator
    Root        string // canonical selected root; host-only
    AbsolutePath string // canonical regular file; host-only
}
```

The resolver reads the canonical folder-backed workspace and folder path itself. Callers supply only `workspaceID`; they cannot pair a workspace snapshot with a different root.

### 1.3 Resolution and containment matrix

| Persisted form                              | Selected root                                                          | Resolution checks on every use                                                                                                                                                                                                                                       | Canonical consumers                                                                                                 | Regression/repair result                                                                                                             |
| ------------------------------------------- | ---------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------ |
| Legacy `ProjectPath` + `project_entry_path` | Canonical managed workspace folder from `GetFolderPath(workspaceID)`   | Validate both portable components, join into one workspace-relative locator, reject malformed typed coexistence, then apply workspace-relative checks below                                                                                                          | Migration compatibility for all consumers                                                                           | Missing/malformed evidence is unfinished or `needs_attention`; never guess by filename                                               |
| Typed `workspace_relative`                  | Canonical managed workspace folder                                     | Require absolute existing directory root; canonicalize it; walk every relative component with `Lstat`; reject symlinks, non-directory parents, escape after `EvalSymlinks`, missing target, and non-regular final entry                                              | OS open, Workspace Surface/runtime `HostContext`, Setup Wizard runtime adapter, file fallback                       | Missing/moved/unsafe entry blocks only project-dependent actions                                                                     |
| Typed `directory_reference`                 | Exact active Directory Reference on the same canonical child workspace | Resolve ID server-side on every call; require an absolute existing directory; canonicalize it; walk every relative component with `Lstat`; reject symlinks, devices, non-directory parents, escape after `EvalSymlinks`, missing target, and non-regular final entry | Same four consumers; absolute external path is injected only after their existing ownership/attachment/grant checks | Deleted/changed/missing reference or containment change blocks; no fallback to another reference, workspace path, or same-named file |
| Browser/project-open UI                     | No filesystem root                                                     | Consume only a server-published availability/action projection; POST remains bodyless and loopback-only                                                                                                                                                              | Workspace detail and post-create open action                                                                        | Hide/disable with bounded reason; browser never constructs a path                                                                    |
| Plugin/runtime service                      | Host resolver result                                                   | Keep managed `WorkspaceRoot` distinct from resolved external `ProjectEntry`; service input cannot override either                                                                                                                                                    | Workspace Surface broker and plugin runtime provider                                                                | Context resolution failure makes the operation/provider unavailable                                                                  |
| File-only fallback                          | Host resolver result, then isolated temporary staging root             | Re-resolve the same typed locator before commit; require the source to remain the same canonical file and unchanged by identity/hash; require one regular staged file; preserve bounded atomic-write behavior                                                        | `ProjectFileFallbackPreparer`                                                                                       | Any locator/root/file change becomes conflict/scope failure; never create a second project                                           |

### 1.4 MCP and native-agent root matrix

| Workspace/resource             | Directory References                                                                        | Explicit `workspace-files` roots                                   | Native/agent result                                                                                                                                                                                                                                  |
| ------------------------------ | ------------------------------------------------------------------------------------------- | ------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Music Production Home group    | Home `files/` reference; later explicitly approved Home-only sample references              | Home `files/` and `notes/` only for the normal binding             | No `sub-workspaces/` root and no child external/managed root. Parentage is never a grant. Sample capability operations use their own reviewed boundary rather than silently widening every Home agent.                                               |
| New managed REAPER child       | Normal managed workspace/project references                                                 | Managed child workspace root                                       | Child agents can reach only their child root through the existing binding and gates. Home/siblings are not derived.                                                                                                                                  |
| Existing-project managed child | Normal managed child reference plus the exact approved external-project Directory Reference | Managed child root plus exactly the approved external-project root | The attach commit must update the explicit binding; relying on synthesized roots would fail because normal workspaces already have an explicit filesystem binding. No parent/sibling root is added.                                                  |
| Capability-scoped live task    | Unchanged                                                                                   | Unchanged                                                          | `RuntimeCapability.ResolveTaskExecutionScope` retains the child `files/` base and only compiled-adapter additional roots/MCP names after exact grant validation. The project-entry absolute path is context, not an automatically writable CLI root. |
| Explicit file fallback         | Unchanged                                                                                   | Unchanged                                                          | The model sees only the temporary one-file staging directory with tools disabled; commit is host-owned and revalidated.                                                                                                                              |

### 1.5 Required implementation seam

Create the typed locator and central resolver in `internal/projecttemplates/project_entry.go` (portable locator normalization) and `internal/workspace` (canonical workspace/reference resolution without importing templates). Replace the three independent absolute-path implementations in project open, Workspace Surface context, and file fallback with that resolver. Update both creation writers, Setup Wizard migration evidence, and the frontend availability projection together so there is no mixed-authority window.

The attach-existing service must use a dedicated trusted-picker preview/commit path. The generic Directory Reference HTTP API currently accepts a raw path and `os.Stat` follows symlinks; it is not the consent or containment boundary for this journey. Attach commit repeats canonical selection checks, creates/reuses the exact reference, and explicitly adds that root to the child's existing `workspace-files` binding.

## 2. Reviewed integration registry and REAPER candidate flow

### Existing canonical lifecycle and source-of-truth inventory

The setup journey must compose the existing `plugin.Manager`; it must not create
another installer. The canonical paths are:

| Concern                                      | Existing authority                                     | Journey rule                                                                                                                                                                                                                             |
| -------------------------------------------- | ------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Source resolution and manifest normalization | `plugin.Load` / `ResolveSource` / `Normalize`          | The client supplies only a stable integration key. A host registry supplies an immutable, reviewed Git source pin and expected identity.                                                                                                 |
| Pre-install trust review                     | `plugin.Manager.Preview` and `plugin.BuildTrustReport` | The journey renders this complete report plus registry identity/source and resolved version/platform/artifact facts; it does not summarize away executable access.                                                                       |
| Confirmed install                            | `plugin.Manager.Install`                               | Install remains disabled on success and retains artifact download, size/digest, protocol, host-feature, blueprint, and registration validation.                                                                                          |
| Enable                                       | `plugin.Manager.SetEnabled`                            | This is a separate explicit mutation after install or when an existing compatible install is disabled.                                                                                                                                   |
| Update preview/apply                         | `plugin.Manager.UpdatePreview` / `Update`              | The journey needs a reviewed-source replacement adapter for an older compatible identity; it may not blindly follow a mutable source recorded by an old install. Preview and apply must use the same registry pin and stale-state guard. |
| Installed state                              | `plugin.Store` via `plugin.Manager.List`               | `InstalledPlugin` identity, version, generation, enabled state, resolved artifacts, blueprints, contribution, and trusted component fingerprint are canonical. Journey receipts only retain bounded identity/version evidence.           |
| Runtime contribution                         | contribution lifecycle and runtime capability service  | An enabled, matching installed generation is required; a same-named but mismatched source/identity/declaration blocks.                                                                                                                   |
| Generic Plugins UI/API                       | `internal/pluginhttp`                                  | Remains available for manual lifecycle operations. The journey endpoint does not accept its generic `source` or marketplace fields.                                                                                                      |
| Blueprint recovery                           | `handleBlueprintPluginRecovery`                        | Useful precedent for generation-gated preview/confirm and safe outcomes, but its template-provided source is not trusted enough for this pre-install journey.                                                                            |

`plugins/src/reaper-plugin` is a standalone checkout and is intentionally
ignored by the Ori repository. At inventory time the local checkout was clean
at tagged `v0.3.0` (`8afa5ec`), while its refreshed `origin/main`, the planning
worktree's `plugins/preview/reaper-plugin`, and tag `v0.4.1` all identify commit
`75a9869` and the same tree. The preview is therefore generated/current upstream
content, not the editable source in this worktree. The stale local clone also
lacks `scripts/with-local-artifact.sh`, so the delivery demo cannot truthfully
run from it until the canonical clone is fast-forwarded. No Ori product code is
to depend on `plugins/preview`.

The existing release chain is deterministic and remains authoritative:

1. `scripts/build-local-artifact.sh` builds the macOS arm64 service with pinned
   Go 1.25.0 settings, writes the bundled artifact, and updates manifest digest
   and size.
2. `scripts/verify-artifact.sh` checks executable mode, size, SHA-256, and CLI
   version against the manifest.
3. `scripts/with-local-artifact.sh` temporarily rewrites only the artifact
   source to `bundled` for clean local Ori install/demo validation and restores
   the release manifest afterward.
4. `scripts/package-release.sh vX.Y.Z` proves the committed release metadata
   already matches a reproducible build and produces the asset plus checksum.
5. The tag-triggered `release.yml` reruns tests/vet/UI tests, packages, and
   publishes. Tagging/publishing remains a separate human action.

Current `v0.4.1` declares Workspace Surface protocol 1,
`assistant_program_v1`, one macOS arm64 HTTPS artifact, and Reaper Song
blueprint version 3. It contains the shared music producer program but not the
specialist journey declaration, scoped Home/project roster required here, or
sample-library add-on. Its checked-in README also quotes a checksum that differs
from the manifest's current pinned checksum; the candidate must regenerate and
make manifest, executable version, README, release notes, digest, size, and URL
agree instead of carrying that drift forward.

### Host-owned reviewed integration registry

V1 ships a small built-in registry; journey declarations never contain a URL,
path, marketplace, command, adapter, checksum, or source format. A normalized
entry has bounded inert fields:

```text
key                    stable host key (REAPER uses `ori_reaper`)
plugin_id              exact normalized plugin identity (`reaper-plugin`)
expected_version       exact reviewed candidate version
source                  host-private GitHub repository + immutable commit SHA
source_format           optional host-selected manifest format
display publisher       host-reviewed publisher/repository labels
expected_blueprint_id   exact contributed blueprint ID (`reaper-song`)
expected_program_id     exact normalized Assistant Program ID
required_host_features exact minimum reviewed feature set
```

The server resolves `SetupJourney.integration_key` against this registry and
then checks the loaded descriptor and installed record against every expected
field. The API publishes a bounded projection of the key, identity, expected
version, publisher/source label, and canonical trust/artifact facts, but never
accepts replacements from the browser. Unknown keys and identity, source,
version, blueprint, program, protocol, feature, artifact, or platform mismatch
fail closed.

The registry source is pinned to the final reviewed plugin candidate commit,
not mutable `main` and not preview content. Until that commit and its artifact
release exist remotely, install failure is an honest resumable blocked state;
implementation must not publish them automatically. Local end-to-end delivery
uses the canonical plugin checkout and `with-local-artifact.sh`. The demo script
configures that one absolute staged source as a process-local development
exception: the adapter still validates version, format, component fingerprint,
protocol, host features, blueprint, program, and platform, marks the completed
receipt as a development copy, and never projects the path or calls it
release-verified. An arbitrary local install remains blocked, and production
launches without that explicit process setting retain the exact pinned-source
rule. The final registry pin is recorded only after the nested candidate commit
is built and reviewed.

The journey integration adapter supports exactly these canonical states:

- absent: `Preview` from the registry pin, then explicit confirmed `Install`;
- matching but disabled: explicit `SetEnabled(true)`;
- matching, enabled, and compatible: read-only completion with no reinstall;
- the one process-configured local demo source, enabled and otherwise compatible:
  read-only development completion with visibly unverified provenance and no
  path in the journey projection;
- older accepted identity/source: explicit preview and transactional
  replacement from the registry pin, preserving the previous generation on
  failure;
- same name with any unproven identity/source/owner or incompatible
  contribution: blocked, never adopted by name.

Preview returns a state revision derived from the installed generation and the
reviewed registry revision/pin. Confirm requires that revision plus an
idempotency key, re-resolves/revalidates the candidate at mutation time, and
returns `409` with fresh bounded state when stale. Install, replacement, and
enable are separate consequence-owner calls; a successful install does not
implicitly authorize a project, folder, mode, role, runner, or live operation.

### REAPER candidate version and compatibility plan

The nested candidate starts from canonical upstream `v0.4.1` and targets
**plugin/service version `0.5.0`** and **Reaper Song blueprint version `4`**.
This is a feature release because it adds trusted declaration and setup
semantics while preserving existing service protocol operations.

Workspace Surface protocol remains **v1**. The change is additive declaration
data, not an incompatible service wire change. The contribution retains
`assistant_program_v1` and adds the minimum fail-closed host feature
`specialist_setup_journey_v1`; no broader version or capability is claimed.
The plugin declaration supplies the blueprint-owned role templates/scopes and
optional add-on contract after install, while Ori's built-in specialist entry
continues to own the only pre-install journey declaration allowed in v1.

Candidate preparation must:

1. fast-forward the clean canonical clone to `origin/main`, create a dedicated
   nested-repository feature branch, and never edit `plugins/preview`;
2. update Claude/Ori identities, service/CLI constants and tests, blueprint
   version/provenance, required host features, declarations, README, and release
   notes to `0.5.0` / blueprint `4` consistently;
3. build the local artifact, pin exact size and SHA-256, verify the artifact,
   run nested Go race/vet and UI tests, and run a clean local Ori install/demo
   through the temporary bundled-artifact wrapper;
4. commit the nested candidate separately, record its commit/version/checksum
   in the Ori delivery notes and reviewed registry, and leave tag/release
   creation to a human release owner.

This separate nested commit is an explicit Ori delivery dependency. The Ori PR
must not claim production installation is available until the reviewed commit
is reachable and its exact `v0.5.0` artifact has been published, but local
candidate validation and all fail-closed host behavior are required before the
Ori PR gate.

The reviewed local candidate is nested commit
`0f938746231be27e85b8597784afeb1883ed8a50`, version `0.5.0`, Reaper Song
blueprint `4`, with macOS arm64 artifact size `8,763,458` and SHA-256
`ad4680d371024d43e2264c00b1a2e2a5a532343d31073e3754a94662bea2cb9d`.
The commit is intentionally recorded with release readiness false: it has not
been pushed, tagged, or published by this delivery.

## 3. `SetupJourney` v1 declaration and action adapters

### 3.1 Normalized declaration

`specialist.Entry` gains one optional `*SetupJourney`. An absent declaration is
behavior-preserving: it creates no run, publishes no setup action, and leaves
the current specialist offer/capability behavior unchanged. Only the compiled
built-in specialist registry can supply one in v1. Plugin, blueprint,
marketplace, workspace, HTTP, and persisted data cannot register or replace a
journey.

The normalized JSON shape is deliberately small:

```json
{
  "schema_version": 1,
  "version": 1,
  "id": "reaper_setup",
  "title": "Set up REAPER",
  "description": "Plain explanatory text.",
  "integration_key": "ori_reaper",
  "expected_blueprint_id": "reaper-song",
  "expected_assistant_program_id": "music-producer-assistant",
  "steps": [
    {
      "id": "integration",
      "kind": "integration_install",
      "title": "Review Ori's REAPER integration",
      "description": "Plain text."
    },
    {
      "id": "project",
      "kind": "project_connect",
      "title": "Connect a project",
      "description": "Plain text."
    },
    {
      "id": "workspace",
      "kind": "workspace_setup",
      "title": "Choose how Ori works",
      "description": "Plain text."
    },
    {
      "id": "staffing",
      "kind": "assistant_program_staffing",
      "title": "Add your studio team",
      "description": "Plain text."
    },
    { "id": "summary", "kind": "summary", "title": "Review setup", "description": "Plain text." }
  ]
}
```

The REAPER integration step's description includes the exact required
not-a-VST explanation. This is inert host copy, not a source or install
instruction.

Normalization is all-or-nothing and must enforce:

- strict JSON decoding with unknown fields and trailing values rejected at the
  journey and step levels;
- exact `schema_version: 1`, a positive bounded declaration `version`, and a
  maximum serialized declaration size of 16 KiB;
- lower-case stable IDs matching `^[a-z0-9][a-z0-9_-]{0,63}$`, with IDs trimmed
  and normalized before duplicate checks;
- non-empty `title` (at most 200 bytes) and `description` (at most 2,000 bytes)
  for the journey and every step;
- plain display text only: no control characters other than newline/tab, NUL,
  HTML delimiters, Markdown link/image syntax, URL/protocol text, or strings
  that the frontend treats as markup;
- non-empty stable integration, blueprint, and Assistant Program references,
  each at most 64 bytes and matching the same conservative ID grammar;
- exactly five steps and exactly one of each allowed kind, in this order:
  `integration_install`, `project_connect`, `workspace_setup`,
  `assistant_program_staffing`, `summary`;
- no `required`, condition, branch, route, adapter, action, method, request,
  payload, source, URL, path, command, script, HTML, module, MCP, scope,
  credential, confirmation-policy, or executable/custom-render field; and
- equality validation of `integration_key` against the reviewed host registry,
  then of blueprint/program IDs against that registry and, after installation,
  against the normalized installed contribution. No display-name fallback is
  permitted.

The `version` identifies declaration semantics separately from the store schema
and plugin/blueprint versions. Progress written for another declaration version
is never reinterpreted merely because step IDs happen to match; section 4 owns
explicit compatible migration.

A strict JSON normalizer is kept even though production declarations are
compiled Go data. It gives fixtures and future import boundaries the same
fail-closed contract, and registry startup/tests normalize every built-in entry
before it can be returned by `specialist.Get`/`All`. Returned entries are deep
copies so callers cannot mutate the registry's declaration or steps.

### 3.1.1 Group-first workspace launch presentation

The approved UX follow-up adds optional inert `workspace_launch` copy: group
heading/default name and runtime heading/instructions. Each field uses the same
plain-text validator (maximum 1,000 bytes). It supplies no executable source,
route, action, renderer, runtime adapter, or permission policy.

For this presentation, the first-run screens are **Install plugin → Create music
group → Set up REAPER → Create New Workspace**. The five canonical v1 kinds,
IDs, readiness tests, stored revisions, and operation receipts remain unchanged;
this is not a migration that reinterprets stored stage indexes. Existing projects
retain their canonical records. A historical project ID alone cannot make the
workspace screen complete after a regression.

- `projectconnection` can review/create only the canonical named Home, or reuse
  it without renaming. No child, agent, schedule, or runtime grant is created by
  group preparation. Legacy/unavailable ownership fails closed.
  Step 2 opens the shared map **Build Group** dialog with a setup-bound name
  and reviewed owner transport; it never falls back to generic group creation.
  The ordinary map action explicitly sends `create_template_agents: false` to
  build an empty group. Existing API callers that omit that flag retain their
  historical manager behavior, and no selected workspaces are silently moved.
  An existing project receipt cannot mark setup complete or offer replacement
  group creation when its canonical Home is unverified.
- A template-identity-bound preparation acknowledgement lives in canonical Home
  shared data. It records a decision to proceed, not application or live readiness.
  The bounded preparation projection contains only canonical group/template IDs,
  display name, and existence/acknowledgement flags—no folder path or owner blob.
- `GET .../runs/{runID}/preparation` checks the current caller/run, integration,
  and Home, then invokes only the installed runtime provider's prerequisite
  operation with a zero-valued context (required MCP keys, empty identities,
  roots, and scopes). It returns only `ready`; it invents no
  workspace, project, grant, exchange root, execution scope, or verification.
  Failed rechecks clear any prior rendered prerequisite success; an unavailable
  provider cannot leave a stale Continue action behind.
- The shared creator skips already-selected Blueprint, then owns Details, Team,
  and Review. Native selection tokens and exact project-entry choices still flow
  through the existing reviewed project owner. The final confirmation covers
  project files, File-only mode, and separately scoped team consequences.
  A staffed Home is never presented as a staffed new project; only explicit Home
  bindings are reused, and legacy roster entries are not promoted into access.
  The shared post-create helper must not skip project staffing because the
  canonical Home's legacy `hired` flag is true.
- Creating requires an already-visible exact project review, never a replacement
  review fetched under the same click. In-flight commits lock dismissal. An
  uncertain retry retains the original envelope and is labelled **Retry Confirmed
  Change**; **Check Setup Status** reads the exact root or child run instead.
  Browser-only names/folder selections survive cancellation within one run;
  cancellation discards review consent and does not write browser storage.
- Interrupted group/project receipts and File-only selection may settle only from
  their unambiguous canonical consequence. This recovery does not generalize to
  staffing/plugin commits and never executes a mutation or settles an active claim.
- The production summary reader offers only closed, existing-scope destinations;
  the canonical reconciler still withholds them until readiness. Another-workspace
  continuation is root-owned, so a child revision cannot be mistaken for its
  root's. Child action authorization follows that exact root and checks its
  current owner, relationship, specialist, and journey identity; those root-only
  fields are not duplicated onto child rows. Project receipts and selected mode
  remain child-owned. Team/optional-role and Sample Library management remain
  reachable after the four-screen launch rather than being silently removed by
  presentation.

### 3.2 Closed host adapter registry

A declaration names only a `kind`. A compiled `setupjourney` registry maps that
kind to one host adapter; there is no declaration-level adapter key. Startup
parity tests require exactly the five v1 kinds and reject aliases, missing
adapters, and extra production kinds.

| Step kind                    | Canonical consequence owner                                                 | Canonical completion test                                                                                                                                                        |
| ---------------------------- | --------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `integration_install`        | reviewed-integration adapter over `plugin.Manager`                          | Exact reviewed plugin source/identity/version is installed, enabled, protocol/host features are compatible, and the expected blueprint/program declarations are present.         |
| `project_connect`            | template-aware project connect/create service                               | The exact live child workspace has matching plugin/blueprint provenance, valid authoritative project entry, required Home parent, and one matching Assistant Project Link.       |
| `workspace_setup`            | the child workspace's existing Setup Wizard and `runtimecapability` service | A declared mode is selected and canonically accepted; every requirement for that mode has its owner-derived readiness. File-only does not inherit live prerequisites.            |
| `assistant_program_staffing` | Assistant Program Home/project staffing service                             | The one Home binding and every required role binding for this exact child link exist under stable scoped role IDs. Names or partially created sibling/Home rosters do not count. |
| `summary`                    | read-only journey reconciler                                                | Every required preceding step is canonically complete. The summary itself performs no setup consequence and projects the server receipt/actions.                                 |

Every adapter implements a domain-neutral contract equivalent to:

```text
Read(scope) -> canonical step state + available host actions
Review(scope, action, strictly decoded input) -> bounded review + review receipt
Commit(scope, action, review receipt, state revision, idempotency key)
    -> canonical result receipt
```

`scope` is built by the service from the current local accepted relationship,
root/child run, declaration, reviewed integration entry, and already persisted
canonical resource IDs. The browser cannot set user, owner, specialist,
journey, plugin source, adapter, scope, Home/station, parent, child workspace,
Assistant Project Link, runtime context, role scope, or sibling target.

`Read` and `Review` are non-mutating. A review receipt binds the run, step,
action, normalized bounded input digest, canonical state revision, and digest
of the canonical disclosure. It stores no manifest, path, prompt, file content,
or credential. `Commit` repeats canonical resolution and validation. For plugin
install/update, the `plugin.Manager` confirmation callback accepts only when the
fresh complete `TrustReport` digest equals the reviewed digest; an unconditional
`true` callback is not a sufficient journey confirmation. Stale/mismatched or
expired review receipts are refused.

Each adapter publishes only actions valid for the state. The v1 action
vocabulary is closed per kind:

| Kind            | Host-published actions                                                                                                                        |
| --------------- | --------------------------------------------------------------------------------------------------------------------------------------------- |
| integration     | `review_install`, `install`, `review_enable`, `enable`, `review_update`, `update`, `manage_integration`                                       |
| project         | `review_existing_project`, `connect_existing_project`, `review_new_project`, `create_new_project`, `open_project`                             |
| workspace setup | `open_workspace_setup`, `refresh_workspace_setup`, `open_project`                                                                             |
| staffing        | `review_home_staffing`, `add_home_staffing`, `review_project_staffing`, `add_project_staffing`, `open_home_staffing`, `open_project_staffing` |
| summary         | `open_project`, `open_home`, `connect_another_project`, `open_live_setup`, `open_sample_library_setup`, `review_setup`                        |

Route/open actions are host-generated navigation intents and never accept a
client URL. Workspace Setup Wizard mutations continue through its canonical
revision/confirmation API and are then observed by `Read`; the journey does not
duplicate those forms. Project connection and coordinated three-role staffing
are each explicit canonical composite boundaries with their own rollback or
partial-repair receipt. They are not broadened into an install-plus-project or
Home-plus-child-plus-runtime super-transaction.

Inputs are strict structs selected by `(kind, action)`, not arbitrary JSON.
Examples are a server picker token plus candidate ID, a bounded new-project
name, a review receipt, or editable role names/provider choices allowed by the
staffing owner. Unknown fields are rejected. Route selection before project
review is browser-only draft state. Changing the route discards its uncommitted
review; the guided project form may retain separate in-memory entries for Back
and retry, but never in browser storage or across run identities. New projects
use one project name by default; an optional Ori display-name override changes
only the reviewed `workspace_name`. Choice, form, and confirmation are separate
screens. Review and commit never persist absolute project paths in journey state.

All mutations require the current run revision and a non-empty idempotency key.
One request invokes at most one adapter and one canonical consequence boundary.
A stale revision or disclosure returns `409` plus a freshly reconciled bounded
projection; replay of an accepted `(run, action, idempotency key, input digest)`
returns its original canonical receipt without duplicating resources.

### 3.3 State and error vocabulary

Adapters return normalized step states only: `pending`, `active`, `complete`,
`blocked`, or `optional_skipped`. `optional_skipped` is reserved for a truly
optional resource such as the separately composed sample add-on; none of the
five main REAPER steps can be made optional by declaration. Unknown adapter or
persisted values normalize to unfinished/blocked, never complete.

Public failures contain a closed `reason_code`, compiled safe guidance, the
fresh state revision when available, and no arbitrary wrapped error. The v1
code families are:

```text
declaration_invalid, journey_unavailable, relationship_not_accepted,
run_not_found, revision_conflict, idempotency_conflict, step_not_current,
action_unavailable, input_invalid, review_required, review_stale,
owner_unavailable, operation_failed,
integration_not_installed, integration_disabled, integration_update_required,
integration_local_unverified, integration_identity_mismatch, integration_unsupported,
blueprint_unavailable, assistant_program_mismatch,
project_selection_required, project_scope_invalid, project_already_connected,
project_unavailable, runtime_setup_required, runtime_needs_attention,
home_unavailable, staffing_required, staffing_needs_attention
```

Specific canonical sentinels (unsupported platform or artifact checksum
failure, for example) may select narrower compiled guidance while retaining a
closed code. Git output, provider/database errors, commands, paths, credentials,
manifest bodies, project/folder/agent names, and user-authored content never
enter an API error or analytics event. Internal diagnostics may record stable
journey/step/action/resource IDs and the closed code, but not those sensitive
values.

Reconciliation, not stored browser progress, controls completion. Opening,
dismissing, reviewing, cancelling, choosing a project route, and navigating to
a canonical setup surface do not complete a step. Removal, disablement,
revocation, missing roles/workspaces/links, invalid project containment, or
runtime regression moves only the narrowest dependent scope back to blocked or
`needs_attention`; it never repeats an unrelated consequence.

## 4. Durable root and child runs

### 4.1 Identity and persisted shape

Setup progress belongs in `internal/setupjourney`, not in
`personal_assistant_state`, workspace metadata, plugin records, or browser
storage. The current relationship remains the authority for whether a user has
accepted a specialist; a run never keeps an acceptance alive after that
relationship stops qualifying.

A single additive `setup_journey_run` table can represent both scopes while
keeping independently revisioned rows:

```text
id                         server UUID; never a name/path-derived value
run_kind                   root | child
root_run_id                empty for root; exact root FK for child
owner_user_id              root only, from current relationship
relationship_id            root only, current Personal Assistant assistant_id
specialist_slug             root only, exact accepted built-in slug
journey_id                  stable declaration ID
declaration_schema_version  version interpreted when the row was written
declaration_version         declaration semantics interpreted when written
state_revision              positive monotonic CAS token
lifecycle_state             not_started | in_progress | ready | needs_attention
current_step_id             first unresolved step or empty when ready
step_states_json            ordered bounded {step_id,status,reason_code?} values
dismissed                   presentation flag
integration_plugin_id       bounded root receipt identity, not source/manifest
integration_version         bounded root receipt version
home_workspace_id           canonical root Home/station workspace ID
project_workspace_id        root's first project or this child's project
selected_mode_id            bounded receipt; runtime owner remains authoritative
first_opened_at             nullable
last_dismissed_at            nullable
first_completed_at           nullable and never cleared by regression
created_at / updated_at      UTC
```

The table enforces one root for
`(owner_user_id, relationship_id, specialist_slug, journey_id)`. A child points
to that exact root, receives its own server UUID before a workspace exists, and
has its own revision, lifecycle, ordered statuses, dismissal and completion
timestamps. A partial unique index enforces at most one nonterminal unbound
child per root and at most one active child run for a non-empty
`project_workspace_id`. This makes concurrent **Connect another project**
requests converge rather than create sibling setup records for one project.

The root owns the first project's onboarding receipt and the Home/integration
receipts. Later children reuse current canonical integration and Home readiness
but own only their selected project, mode, and project-team progress. An
unfinished or broken later child never rewrites the root's first project,
`first_completed_at`, or ready receipt. Home summaries join children by stable
root/run/workspace IDs; they do not copy an authoritative project registry into
the root row.

Stored plugin version and selected mode are historical/resume receipts only.
Every read compares them with `plugin.Manager`, workspace/runtime state, and the
Assistant Program owner. Authoritative project entry and Directory Reference,
Assistant Project Link, runtime readiness, role bindings, model choices,
catalogs, and grants stay with those owners.

`step_states_json` is structural progress, not proof. Decode applies strict
size/count/ID bounds. Unknown, duplicate, missing, or unknown-status entries
normalize to the declaration's ordered steps with affected values unfinished;
they never become complete. Corrupt JSON returns a bounded
`needs_attention` projection rather than making the relationship or workspace
unreadable. The store never persists paths, manifests, trust-report bodies,
plugin commands, prompts, role bindings, credentials, file contents, folder or
agent names, runtime grants, sample records, or arbitrary errors.

### 4.2 Lifecycle and first-unresolved reconciliation

A root may be inserted inertly after acceptance with `not_started`, revision 1,
and no `first_opened_at`; alternatively the first explicit open performs the
same create-or-get. It has no side effect beyond setup persistence. `Open`
stamps `first_opened_at` once, clears `dismissed`, and enters `in_progress`.
`Dismiss` sets the flag and `last_dismissed_at`; it does not skip a step, alter a
canonical resource, or change readiness.

On every read and after every mutation, the service independently asks each
canonical owner whether its result exists now. Existing downstream results can
remain complete when an earlier shared prerequisite regresses: if the plugin is
disabled, the project/link/roles are preserved and only integration execution
needs repair. The current step is the first ordered step not canonically
complete. That step is `active` when a safe action is available or `blocked`
with a closed reason when it is not; unresolved later steps are `pending`
unless their own canonical result already exists. `summary` becomes complete
only when all four consequence steps are complete.

Lifecycle is then derived, never accepted from the browser:

- `not_started`: no explicit open yet and no canonical setup consequence;
- `in_progress`: opened, at least one required result is unresolved, and the
  first unresolved result has an ordinary safe next action;
- `ready`: every required canonical result for this root/child is valid now;
- `needs_attention`: a prior ready run regressed, an operation is in bounded
  repair/reconciliation, or the first unresolved result is blocked rather than
  merely awaiting the user's normal next action.

`first_completed_at` is set only on the first transition to `ready` and retained
as historical evidence after regression. A repair can return the lifecycle to
`ready` without changing it. `current_step_id` is empty while ready. Timestamps
change only for their named event or a material reconciled state change, not on
every GET.

A GET loads, derives, and CAS-persists a materially changed normalization before
returning it, retrying a bounded number of times if another tab won. If a
canonical owner is temporarily unavailable, the projection uses a closed
blocked/owner-unavailable state and never overwrites known canonical resource
IDs or a historical completion timestamp with guesses.

### 4.3 Revisions, reviews, and idempotency journal

Every state-changing request carries `if_revision > 0` (or zero only for an
atomic create-or-get) and a bounded opaque idempotency key. The store first
claims the operation in the same transaction that CAS-increments the run. No
plugin, filesystem, workspace, runtime, or agent action begins before that
claim succeeds. A concurrent request with the old revision receives `409` and
the freshly reconciled bounded projection.

`setup_journey_operation_receipt` is keyed by `(run_kind, run_id,
idempotency_key)` and contains only:

```text
action/step IDs, normalized input digest, disclosure/review digest if required,
status (claimed | reconcile_required | succeeded | failed),
closed result/reason code, bounded canonical result IDs/owner revisions,
run revision before/after, created/completed timestamps
```

The result payload is a strict typed bounded object (maximum 8 KiB), never raw
adapter JSON. Reusing a key for another action or input digest returns
`idempotency_conflict`. Replaying a succeeded or definitively failed accepted
operation returns the original receipt. An uncertain timeout/crash is marked
`reconcile_required`; replay or restart recovery first checks the canonical
owner. If the intended result exists it finalizes success, otherwise it retries
only through that owner's idempotent operation key/repair contract. It never
blindly repeats a possibly completed side effect.

For project connection, a normal authorized status read can settle a
`reconcile_required` receipt only when the registered canonical owner observes
that run's exact project consequence. It does not execute a commit, settle an
active `claimed` operation, or infer success from a missing/unavailable record.
Project observation combines primary identity/link state with folder-owned
project path and template provenance; SQLite omits those portable fields.
A browser retry after a lost response reuses the exact in-memory commit envelope
and idempotency key, rather than requesting a second creation.

Review receipts live in a sibling bounded table (or the same journal with a
`review` record kind). They contain server token, run/step/action, input digest,
run and canonical-owner revisions, disclosure digest, creation/expiry, and
consumption status. They contain no displayed trust body or path. Commit must
match all fields and repeat canonical preview validation; a stale review cannot
be converted into consent for changed bytes, source, project selection, folder
scope, roster, or handoff.

Process-local locks may reduce duplicate work but are not correctness. Database
uniqueness, CAS, the operation claim, and canonical-owner idempotency handle
concurrent tabs and restarts. While a claimed operation is executing, reads may
project it as busy but do not race-persist a new revision; another mutation is
refused. On normal completion, receipt finalization and the new reconciled run
state are committed together. Startup/first-read recovery handles claims left
by a dead process.

### 4.4 Browser drafts, child creation, and restart behavior

Project route choice, unsubmitted names, native-picker cancellation, selected
but unconfirmed files, trust dialogs, role-form edits, and focus state are
browser drafts. Switching routes or closing loses only those values. They do
not create a workspace and are not serialized into a run.

A later child run is created only by the explicit server-owned **Connect another
project** action. The action's idempotency receipt points to the server-issued
child ID, so a lost response can be replayed. If a different tab asks while one
unbound nonterminal child exists, it receives that resumable child rather than
a duplicate. Dismissal leaves that run resumable. V1 does not infer abandonment
from elapsed time, route changes, or browser closure; an unconfirmed browser
route never made a run in the first place. Once a child has a canonical
workspace, only explicit Assistant Program disconnect/impact-review behavior
may detach it; cleanup never guesses from inactivity.

After restart the service reloads rows and journals, resolves the current
accepted relationship/declaration, recovers uncertain claims, and derives all
canonical results again. It asks again for ephemeral picker/trust/staffing form
input that was never committed. A missing workspace/plugin/link/reference is a
narrow repair state, not a reason to recreate it by mutable name. Already valid
resources remain complete and are never repeated just to advance stored
progress.

### 4.5 Declaration updates and migrations

A persisted `(schema_version, declaration_version)` is interpreted only by that
exact contract. If the built-in declaration changes, the service looks for one
compiled migration keyed by
`(journey_id, from_schema, from_version, to_schema, to_version)`. A migration
may map stable step IDs and bounded receipts, but cannot invoke an adapter,
create/delete a resource, invent canonical completion, copy browser data, or
change ownership. Canonical reconciliation decides completion after migration.

An allowlisted compatible migration is CAS-applied, records a bounded migration
receipt, preserves resource IDs and all historical timestamps, and is visible
in the next projection. If no exact migration exists, the old row and receipts
remain untouched and the API returns `needs_attention` with
`declaration_invalid`/upgrade guidance. Ambiguous step mappings or identity
changes are incompatible by default. Merely sharing labels, step positions, or
resource names is never migration evidence.

## 5. Scoped Assistant Program state

### 5.1 Current model and incompatibilities

The existing Assistant Program v1 implementation is intentionally shared:
`EnsureProjectStation` creates a plain station only after a project exists,
`HireAssistantProgram` creates every declared global Agent, creates one roster,
then copies the same `AgentInstance` values (including UUIDs) into the station
and every linked project. A later project receives that shared roster
automatically. `AssistantProgramState.Provider`, `Model`, `Hired`, and `Roster`
are station-global.

That behavior cannot be stretched into the new topology:

| Existing layer                 | Current identity/behavior                                                                                        | Required scoped behavior                                                                                                                                                                          |
| ------------------------------ | ---------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Global Agent definition        | `store.Store`, keyed by case-sensitive display name; prompt/model/provider are reusable definition data.         | A staffing operation creates a fresh profile for each role binding unless its own interrupted-operation receipt proves ownership. A same-named existing profile is a collision, not silent reuse. |
| Workspace-local Agent snapshot | Stored by `(workspace ID, agent name)` and preferred by task runtime resolution.                                 | Each staffed Home/child receives its own snapshot from the trusted role template and selected provider/model. User edits remain in that workspace and are never copied to a later child.          |
| `AgentInstance`                | Workspace attachment with UUID but the current station flow copies one instance into all projects.               | A new UUID is minted per `(owning workspace, scoped role ID)`. Instance, node, toolbox/MCP/skill grants, task history, memory, and runtime state are never copied across Home/siblings.           |
| Assistant role binding         | One station `role_id -> instance/name` roster.                                                                   | Home bindings live in Home state; project bindings live on the exact child link. Stable role/scope/instance IDs, not the mutable name, establish attribution.                                     |
| Entry/coordinator              | One station primary is projected into every project.                                                             | Home primary is Music Portfolio Manager. Every child has its own Producer primary.                                                                                                                |
| Delegation                     | `delegate_task` is exposed only to the current workspace coordinator and targets only members of that workspace. | Keep unchanged. Producer can target only that child's Mix Engineer/Songwriter; Portfolio Manager can target only Home's optional Sample Library Manager.                                          |
| Managed learning               | Station sidecar and reflection consume bounded accepted-task summaries from exact links.                         | Keep Home-owned and review-gated. No transcript/project memory is copied to Home/siblings; only current approved revision IDs/text may be resolved as bounded program context.                    |

Agent-name validation currently converges on 1-100 characters containing only
ASCII letters, digits, spaces, underscores, and hyphens. Scoped staffing reuses
that boundary, normalizes whitespace, checks case-insensitive duplicates within
the submitted roster and the global store, and rechecks atomically immediately
before profile creation. A collision, unavailable agent store, stale roster
revision, invalid provider/model, or partial provisioning is a bounded failure
for that one Home or child only. It never reuses another user's/customized
profile merely because the display name matches.

### 5.2 Assistant Program declaration v2

Scoped roles change persisted meaning, so the candidate uses Assistant Program
**schema version 2** rather than silently extending v1. Ori continues to decode
v1 snapshots for readable legacy state; only v2 can satisfy this journey.

Each `AssistantProgramRoleSpec` adds required, closed declaration fields:

```text
scope       home | project
required    explicit boolean
primary     existing boolean, interpreted within scope
```

Role IDs remain globally unique within the program. V2 requires at least one
required role in each scope and exactly one required primary per scope. Optional
roles cannot be primary in v2. Home staffing selects required Home roles only;
project staffing selects required project roles only. The optional Sample
Library Manager is one non-primary `home` role with `required:false` and is
materialized only by its separately reviewed add-on action. No client may
change scope, required/primary status, role ID, prompt, skills, capability
requirements, or target workspace.

The REAPER v2 declaration has:

| Stable role              | Scope     | Required | Primary |
| ------------------------ | --------- | -------: | ------: |
| `portfolio_manager`      | `home`    |      yes |     yes |
| `sample_library_manager` | `home`    |       no |      no |
| `producer`               | `project` |      yes |     yes |
| `mix_engineer`           | `project` |      yes |      no |
| `songwriter`             | `project` |      yes |      no |

Role templates remain reusable immutable declaration data. A role **binding**
is not reusable. Provider/model/name choices are reviewed per binding. Omitted
provider/model fields use existing default-resolution semantics and remain
empty in the Assistant Program receipt rather than being saved there as
invented choices. Canonical agent/runtime reads report whether the resulting
profile can execute; missing model configuration does not block deterministic
staffing or portfolio status.

### 5.3 Separate Home and project roster ownership

`AssistantProgramState` remains owned by the Home/station and gains:

- `HomeRoster`, independently revisioned, containing only Home-scoped bindings,
  per-role provisioning status, and bounded idempotency/repair receipts;
- the existing stable program key and sorted linked project IDs;
- Home-owned portfolio state and Home learning/reflection state; and
- explicit `LegacyRoster` / migration state for v1 records.

`AssistantProjectLink` gains a stable server-issued link ID and an independently
revisioned `ProjectRoster`, containing only bindings for that exact child. The
link ID plus project role ID is the durable project-role key. The owning
workspace must contain the bound instance and workspace-local definition;
station or sibling membership never satisfies it.

A normalized binding contains only stable role ID/scope, AgentInstance ID,
current Agent profile name, explicitly selected provider/model receipt fields,
created timestamp, and definition/version evidence. It contains no prompt,
memory, task, grant, path, or runtime state. Role lookup always starts from the
Home roster or exact project link and verifies the referenced workspace
instance. Hardcoded `Reaper Producer`, label matching, entry-agent name,
`ParentID`, and global Agent name are not authority for new attribution.

The Home and project staffing reviews are separate:

- **Add Music Portfolio Manager** submits exactly the required Home-role set
  (one role in REAPER v2) against `HomeRoster.Revision`.
- **Add project team** submits exactly all required project-role IDs for one
  exact live link against that link's `ProjectRoster.Revision`. It is one
  coordinated user decision but creates no Home or sibling binding.
- **Add Sample Library Manager** submits only the declared optional Home role
  through the add-on's separate revision/consent boundary.

Each review shows stable role label/responsibility, trusted prompt/skills,
current provider/model availability, editable validated name, and exact owning
Home or child. The request may send only role IDs, names, and allowed
provider/model choices. The service fills prompts, skills, scope, target,
entry/coordinator status, and capability requirements from the normalized
installed declaration.

Provisioning is restart-safe per role. Before creating profiles, the owner
records an operation receipt with deterministic intended role IDs, fresh
AgentInstance IDs, normalized names/configuration fingerprints, expected roster
revision, and idempotency key. Each successfully created profile/snapshot/
instance/binding is checkpointed. If role two fails, role one remains a
truthfully reported partial binding and retry resumes only missing roles; it
does not duplicate or silently delete it. A profile found after a crash is
reused only when that exact pending receipt proves it was created for the same
role/configuration. Otherwise it is a name collision.

A roster is ready only when every required declared role has exactly one valid
binding and owning-workspace instance/snapshot. Missing or duplicate roles,
wrong scope, stale declaration, missing instance, or a partial journal produces
`needs_attention` on only that roster. Adding a role creates no task, run,
reflection, capability, directory, runtime grant, live action, or REAPER
change. Profile execution unavailability is shown separately from binding
existence.

### 5.4 Portfolio ownership and exact-link handoffs

Music Production Home owns one revisioned portfolio record per exact live
Assistant Project Link. The record may contain bounded deterministic fields for
status, priority, milestones, session/release dates, blockers, deliverables,
and archive-review state. Fields are keyed by link/project IDs; names are
current display text only. User/model-authored text is bounded and rendered as
text. A model may propose a change, but only a host-owned preview/commit action
with portfolio revision and idempotency receipt mutates these fields.

Portfolio discovery uses `LinkedProjectIDs` plus reciprocal exact link
validation. It never searches disk, infers membership from `ParentID`, or reads
a child folder. Read-only rollups may combine these records with bounded
canonical Ticket summaries that already carry their true child owner. Archive
work in v1 is inventory/recommendation data and child tasks only; it cannot
move, rename, deduplicate, or delete a physical project.

**Send to project** is not `delegate_task`. Its host preview names one exact
live `AssistantProjectLink.ID`, target child, bounded Ticket title/details and
state; commit revalidates station membership and requires Home portfolio
revision, review receipt, and idempotency key. It calls
`TicketService.CreateIdempotent` with the child workspace as owner and a stable
Assistant Program handoff source key, then stores only the link/child/Ticket
receipt at Home. The resulting Ticket is non-running. The Portfolio Manager
receives no child AgentInstance, tool, directory/MCP root, runtime grant,
project entry, memory, or direct mutation authority.

### 5.5 Learning and project isolation

Current managed-learning separation remains authoritative:

- candidate generation reads only bounded accepted-task summaries from
  reciprocal live links, never child transcripts, memories, files, or runtime
  state;
- candidates require review, and only current approved non-tombstoned revisions
  can enter bounded model context;
- learnings and reflection diagnostics remain Home sidecar state;
- project ordinary memory, conversations, tasks, grants, and role refinements
  remain child-owned;
- a newly staffed child may resolve specifically approved current learning
  revisions through its exact link, but no other child's state is copied into
  its workspace; and
- disabling/removing the plugin pauses generated reflection/suggestions while
  preserving readable Home/child state.

Project Producer delegation continues through the existing same-workspace
coordinator roster, so only that child's Mix Engineer and Songwriter appear as
targets. Home's coordinator roster contains Home members only. Personal HQ has
neither roster and cannot use setup as cross-workspace delegation.

### 5.6 Legacy roster preservation and explicit migration

V1 station records and their copied project instances are never normalized into
v2 by role label. On first v2 read, Ori preserves the complete old
`Hired/PrimaryName/Provider/Model/Roster` fields as readable `LegacyRoster`
evidence and marks `legacy_review_required`. It does not rename a shared
Producer into Portfolio Manager, assign a copied instance to a project role,
clone its prompt/memory, delete profiles, or infer scope from workspace
membership.

An impact review lists the preserved legacy topology and the fresh v2
consequences separately:

1. create the required Home binding through ordinary Home staffing; and
2. for each explicitly selected live child, create an independent project
   roster through that child's ordinary staffing operation.

Because historical v1 instances were intentionally shared across station and
children, they are not safe evidence for independent project bindings. They
remain readable legacy members until a separate existing detach/delete action
is explicitly reviewed. If duplicate/malformed state prevents proving the
stable station key or reciprocal links, migration performs no role mutation and
reports `needs_attention`.

A legacy compiled Reaper Song workspace with no compatible plugin provenance,
v2 declaration, or exact Assistant Project Link remains on the legacy path and
is never enrolled. New Workspace Surface/Assistant Program attribution reads
only scoped bindings; fixed `Reaper Producer` attribution remains display-only
legacy compatibility.

## 6. Music Production Home hierarchy

### 6.1 Existing hierarchy and lifecycle inventory

The existing hierarchy already makes physical placement authoritative for
managed workspaces:

- `FileStore.Save` places a child at
  `<parent>/sub-workspaces/<child-slug>` and enforces `MaxNestingDepth == 5`.
  `MoveWorkspaceFolder` moves a whole managed subtree, rejects self-parenting,
  cycles, depth overflow, and destination slug collisions, and uses rename or a
  copy-then-delete cross-device fallback.
- `sessionhttp.updateWorkspace` is the generic reparent boundary. It requires a
  group parent, rejects imported/external physical workspaces, rejects active
  work in the whole moving subtree, invokes the physical move, and then updates
  SQLite and path-keyed references. Drag, keyboard and map/list grouping
  ultimately depend on this route. It currently has no Assistant Program link
  guard.
- Disk reconcile treats physical nesting as the organizational source of truth
  and repairs stale `ParentID` projections from folder location. A manual disk
  move can therefore create a link/parent mismatch that Assistant Program
  reconciliation must notice; disk sync is not authority to add or remove
  program membership.
- Group creation through the generic session route provisions
  `sub-workspaces/`, `files/`, `notes/`, a Directory Reference to only
  `files/`, and a `workspace-files` binding rooted at only `files/` and
  `notes/`. It also currently creates a generic group manager. Startup backfill
  applies the same scoped scaffolding to legacy groups.
- `AssistantProgramStore.EnsureProjectStation` currently creates a plain root
  workspace only after loading a compatible project, finds it by stable
  `(owner_user_id, plugin_id, program_id)`, and then writes reciprocal station
  and project evidence. It neither creates a group nor parents the project, and
  its v1 hired-state path copies one station roster into the project.
- Generic group deletion defaults to `group_only`: it blocks active work,
  physically moves direct children to the workspace root, then trashes or
  deletes the empty group. `contents` recursively trashes/deletes the entire
  managed tree. Neither mode understands Assistant Program state. Ordinary
  workspace deletion/trash likewise does not require a link disconnect first.
  Group restore restores the physical tree and marks descendant rows active.
- Folder rename and move return every changed managed path. The session layer
  rebases managed Directory References, `workspace-files` roots, and legacy
  absolute `ProjectPath` values. The current MCP-root rewrite drops roots that
  are outside both the old and new managed folder, which would incorrectly
  discard an explicitly authorized external REAPER root during a child move.

Runtime scope is otherwise already non-recursive. Agent runtime resolution
loads only the current workspace's own bindings (or, only when no filesystem
binding exists, its own Directory References); it never follows `ParentID` or
enumerates descendants. Broad native-MCP CLI work starts in that workspace's
`files/` directory. Capability-scoped CLI work starts from the same child- or
Home-owned `files/` root and adds only roots from the exact capability grant.
A group is safe only while its explicit/synthesized roots retain the scoped
`files/`/`notes/` rule: passing the group folder itself to a recursive
filesystem tool would expose every physically nested child.

### 6.2 Home identity and consequence-free creation

Music Production Home is the Assistant Program station; no wrapper group is
created. Its durable identity is its normalized `AssistantProgramKey` and
server workspace ID. **Music Production Home**, its description, and its
folder slug are trusted initial display values only and may later be renamed.
Name, tag, slug, folder name, `ParentID`, and descendant position never identify
or enroll a Home.

Split `EnsureProjectStation` into two canonical operations:

1. `EnsureHome(key, normalized declaration, operation receipt)` creates or
   returns one top-level active `Kind: group` workspace carrying matching
   Assistant Program state. It is callable before a project exists only by the
   trusted project-connect service, using the normalized installed blueprint
   declaration and current owner; no browser-supplied key, station name, kind,
   parent or workspace ID is accepted.
2. `LinkProject(homeID, childID, expected revisions, operation receipt)` writes
   one stable server-issued reciprocal `AssistantProjectLink` only after the
   exact managed child, provenance, owner, Home key, and required parent
   projection have been re-read.

`EnsureHome` uses the same SyncStore-backed workspace owner as normal creation
and a reusable group-scaffolding helper, but deliberately bypasses the generic
route's manager/template-agent branch. It creates only the Home record/folder,
`sub-workspaces/`, its own `files/` and `notes/`, the `files/` Directory
Reference, and the two-root `workspace-files` binding. It creates no Agent,
AgentInstance, role binding, task, schedule, reflection, capability, sample
root, runtime grant, project, or plugin action. The unique stable-key check and
operation journal, not the current process mutex, make concurrent creation
converge. More than one matching record, a key match on a different owner, or
an ambiguous trashed/missing record fails closed rather than selecting by name.

For either connection route, creation order is Home, managed child with
`ParentID` set at its initial save, child connection metadata, and finally the
reciprocal link. An existing REAPER directory is never the physical child:
only the new managed child is nested, while the external folder remains an
explicit child-owned Directory Reference and binding root. A new project is
scaffolded directly in the managed child. Rollback may remove a just-created
empty Home or child only when the same operation receipt proves ownership and
there are no links, roles, tasks, add-ons, or unrelated writes; uncertainty is
checkpointed for repair.

The canonical connected invariant is all of the following, checked
independently:

- one active Home with the exact stable key and compatible declaration;
- one active managed child with matching owner/provenance and a valid project
  entry;
- one reciprocal live link with a stable link ID; and
- `child.ParentID == home.ID` plus a physical managed location beneath the
  Home's `sub-workspaces/` directory.

The link establishes Assistant Program membership and authorization; the
parent/physical checks are required organizational projections only. A
`ParentID` match cannot synthesize a link, and a link cannot hide a physical
mismatch. Manual disk changes or stale metadata produce the narrow
`needs_attention` state. Reconciliation never silently moves a tree to repair
it.

### 6.3 Scope isolation and path-keyed reconciliation

Home filesystem authority is an allowlist, not the recursive physical Home
folder. The normal Home binding contains exactly canonical Home `files/` and
`notes/` roots. Later sample roots are separate reviewed Home-owned references
and capability records; they do not replace the normal binding or flow to a
child. Every runtime builder and prompt/tool projection must derive roots from
the exact current workspace record and must have a regression test proving it
does not follow parents, children, sibling links, or `LinkedProjectIDs`.

Managed rename/move/root-switch repair must rebuild path-keyed authority from
stable ownership evidence:

- rebase only managed roots/references proven at or below the old managed
  workspace path onto the corresponding new path;
- preserve an external root exactly when an active same-workspace Directory
  Reference or capability grant proves that root is still authorized;
- preserve typed project-entry identity by its relative locator and stable
  Directory Reference ID rather than rewriting an absolute path;
- drop an unmatched stale absolute root instead of retaining it by name; and
- for groups, always reconstruct the defaults as `files/` and `notes/`, never
  the group root or `sub-workspaces/`.

Thus moving or renaming a managed child cannot revoke its reviewed external
project root, and cannot gain the Home or sibling roots. Moving/renaming the
Home updates its own two managed roots and descendant managed roots without
turning the common path prefix into authority. External REAPER/sample folders
are neither moved nor rewritten.

Home portfolio reads use exact reciprocal links and bounded canonical
workspace/Ticket metadata. They do not recursively walk the group directory,
open project entries, parse `.rpp` files, or derive membership from the folder
layout. No setup action enables a descendant watcher.

### 6.4 Protected move, disconnect, delete, and restore

Assistant Program lifecycle checks must sit in the common server/service gates
used by PATCH, drag, keyboard, map/list, bulk move, trash, delete, restore, and
sync-triggered repair actions. Calling `MoveWorkspaceFolder` directly remains a
low-level filesystem operation and cannot implicitly mutate membership.

A child carrying a **live** Assistant Project Link cannot be reparented,
trashed, or deleted through a generic action. The API returns a stable
impact-review-required result without moving either managed or external data.
The explicit disconnect review identifies the exact link/Home/child and states
that Home portfolio rollups and Home-mediated handoffs stop while the child
workspace, project roster/AgentInstances, tasks, project files, external
reference, mode/runtime state, and copied samples remain. Commit rechecks link,
Home and child revisions, active work, and idempotency before transitioning the
link out of live membership. Only a later separately selected generic delete
may delete the managed child; it never targets an external Directory Reference.

Disconnect keeps a bounded child-owned link tombstone containing the stable
link/key/Home/project IDs, project-roster ownership, lifecycle status,
revision, and disconnect receipt. It removes the child from the Home's live
link set and disables Home handoffs/rollups; it does not erase the project team.
After disconnect, an explicit reviewed organizational move may un-nest the
managed child. Reconnect requires the same identity/provenance checks and a new
review; a mutable parent/name match is insufficient.

A group carrying Assistant Program Home state cannot enter either generic
`group_only` or `contents` deletion, trash, bulk-delete, or ordinary restore.
Its specialized impact review distinguishes:

- Home roles and learning/portfolio state;
- optional sample capability, roots, catalog/collections and provenance;
- each live link and independently owned child team;
- Home-owned managed files/notes; and
- managed child projects, external project/sample folders, and confirmed child
  copies, all preserved by default.

Default Home removal is a restart-safe composite lifecycle operation. It blocks
while the Home subtree has active work; transitions each reciprocal link to a
bounded `home_removed`/recoverable state without deleting its project roster;
physically moves retained managed children to the workspace root through the
canonical move/rebase operation; and only then trashes/removes the now-empty
Home-owned group according to the reviewed choice. The destructive
`delete-with-contents` shortcut is never offered for a Home. External folders
and child copies are never deletion targets. Each stage is revisioned and
checkpointed so retry resumes rather than repeats; an uncertain partial result
is `needs_attention`.

Restoring a removed Home is likewise specialized. Restoring the Home's own
folder/state does not silently re-enroll or move retained children. A separate
impact review may reconnect selected compatible child tombstones and restore
their parent projection, subject to active-work, destination, depth, slug,
provenance, and scope checks. A folder conflict or missing child remains a
bounded repair state. Ordinary soft restore remains unchanged for unrelated
workspaces/groups.

Plugin disable/removal is not Home deletion: it preserves the Home, links,
parent projections, roles, sample state, children, and files in place, marks
plugin execution unavailable, and lets journey reconciliation expose the
narrow repair. Rename remains an ordinary display/physical operation because
stable IDs and path rebasing preserve identity and scope.

### 6.5 Hierarchy regression matrix

Tests must pin these boundaries:

| Case                                        | Required result                                                                                                                              |
| ------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------- |
| Concurrent first connection                 | One group Home, one managed child for the accepted operation, one reciprocal link; no generic manager/agents.                                |
| Existing-folder connection                  | External tree/hash unchanged; managed child physically under Home; only that child owns the external reference/root.                         |
| New-project connection                      | Scaffold exists under Home child; Home roots remain only Home `files/`/`notes/`.                                                             |
| Home/child runtime resolution               | Home cannot materialize child roots; child cannot materialize Home/sibling/sample roots; parentage/link enumeration adds nothing.            |
| Child/Home rename or managed move           | Managed roots rebase, reviewed external roots survive by stable ownership, no new roots appear.                                              |
| Manual physical mismatch                    | Link remains evidence, canonical state is `needs_attention`, no automatic move.                                                              |
| Generic reparent/trash/delete of live child | Refused before filesystem or membership mutation; explicit disconnect review required.                                                       |
| Generic Home group deletion/restore         | Refused and routed to the specialized impact flow; no recursive cascade.                                                                     |
| Default disconnect/Home removal             | Child/team/tasks/project/external roots/copied samples preserved; live portfolio/handoffs stop; retained topology is explicitly recoverable. |
| Active work, cycle, depth or slug conflict  | Mutation is refused with original folders, links and grants intact, or resumes from a bounded checkpoint after an already-completed stage.   |
| Plugin disable/remove                       | Topology and data remain; only contribution-backed execution/readiness regresses.                                                            |

## 7. Sample Library Manager capability

### 7.1 Existing seams and non-reusable hazards

The sample add-on composes the built-in `workspacecapability` lifecycle but
needs a new runtime/store. The useful existing guarantees are that a compiled
`Definition` cannot name executable code, `Service.Install` writes only one
workspace install record, runtime hooks are bound by the server, and removal
stops runtime work/releases owned state before dropping the install record.
The sample definition has **no** generic `CompanionDescriptor`: the optional
Sample Library Manager is provisioned only by the scoped Assistant Program Home
role action.

File Janitor supplies patterns, not an implementation to reuse. Its root
validation canonicalizes and overlap-checks a reviewed folder; its action paths
re-resolve the Directory Reference, compare file identity, use `Lstat` and
`O_NOFOLLOW` where available, keep errors bounded, and separate a read-only
agent from deterministic file actions. Its scanner is intentionally
non-recursive and its store/action semantics are for one filing inbox, so they
cannot represent a multi-root sample catalog or sample copies.

The generic Directory Reference API is not a setup boundary. It accepts a raw
path, `Stat` follows a final symlink, its list operation performs an unbounded
recursive `filepath.Walk`, and its read route can open arbitrary contained
files. Sample roots therefore use dedicated picker/review/commit and bounded
catalog routes. A Directory Reference ID known to the sample store cannot be
repointed or deleted through the generic API; it routes to the sample
revocation impact flow. The sample root is deliberately **not** added to the
Home's normal `workspace-files` MCP roots. The compiled sample service resolves
it directly, so neither Home agents nor children acquire raw library access.

### 7.2 Compiled capability and six consent boundaries

Register built-in capability `sample-library` version 1 with inert catalog/setup
copy and no watcher, schedule, command, URL, path, plugin source, or companion.
It can be installed only on an active Assistant Program Home with the exact
reviewed music-program key. The canonical capability install/review operation
is consequence one and writes only the Home's installed-capability record.

The add-on keeps these decisions separate and revisioned:

1. install/remove the deterministic Home capability;
2. add/remove the optional Home-scoped `sample_library_manager` role;
3. connect/revoke each exact sample root;
4. explicitly **Index samples** or **Refresh catalog**;
5. enable/revoke SHA-256 hashing and/or embedded-tag reads for each root; and
6. preview/commit copies of exact catalog entries to one exact linked child.

No action implies another. In particular, root connection does not scan,
capability install does not connect a root or add an agent, analysis consent
does not read until a later explicit index/refresh, indexing does not create or
run an agent, and adding the agent grants it no scan/copy/directory tool. The
main setup journey remains ready when any part of this optional add-on is
skipped, unavailable, or broken.

### 7.3 Supported files and fixed bounds

V1 indexes only case-insensitive matches for these regular-file extensions:

```text
.wav  .wave  .aif  .aiff  .flac  .mp3  .ogg  .m4a
```

Extension matching is classification, not permission to execute or decode
arbitrary content. `.rpp`, `.rpp-bak`, MIDI, archives, disk images, presets,
instruments, plug-ins, and extensionless files are excluded. Metadata-only
indexing does not MIME-sniff or open a file.

Production constants are fixed and published in every root/index review:

| Bound                                       |                       V1 limit |
| ------------------------------------------- | -----------------------------: |
| Active roots per Home                       |                              8 |
| Recursive depth below a root                |            16 directory levels |
| Directory entries visited per root scan     |                        200,000 |
| Regular sample entries retained per root    |                        100,000 |
| Directories visited per root                |                         20,000 |
| Wall time per root scan                     |                     60 seconds |
| Relative locator / one component            |        2,048 / 255 UTF-8 bytes |
| Bounded per-scan issue examples             |                            256 |
| Search results / query length               |          200 / 200 UTF-8 bytes |
| Collections / entries per collection        |                    128 / 1,000 |
| User tags per entry / tag length            |            32 / 64 UTF-8 bytes |
| One provenance/note field                   |              2,000 UTF-8 bytes |
| Hashable file / total hashed per refresh    |                512 MiB / 2 GiB |
| Embedded-tag parser read budget             |                 2 MiB per file |
| Content-analysis wall time per root refresh |                     60 seconds |
| Files / bytes in one copy handoff           | 64 / 2 GiB total, 512 MiB each |
| Copy commit wall time                       |                    120 seconds |

Tests inject smaller clocks/limits, but production callers and declarations
cannot raise them. Hitting a count, depth, byte, parser, permission, or time
bound produces a committed `partial` generation only from entries actually
revalidated during that run, with categorized skipped/error counts. It never
retains unvisited old entries as if they were current. A failure before any
safe enumeration keeps the previous generation and records a failed receipt.
Search and UI always expose generation completeness, so a partial catalog is
not represented as exhaustive.

### 7.4 Root picker, canonical identity, and overlap rules

A dedicated server-owned picker call returns an opaque short-lived selection
token plus the exact local display path. The subsequent review accepts the
token, not a path. It rejects an empty/NUL/non-absolute selection, a final
symlink, a non-directory, a filesystem/volume root, the whole home directory,
and any root equal to, containing, or contained by Ori's data root, managed
workspace root, a connected project's managed/external root, or an active
sample root. Active sample roots are checked across Homes, not only within one
Home. Exact, ancestor, and descendant aliases are all overlaps. Paths are
symlink-resolved before comparison and compared conservatively case-folded on
macOS/Windows; existing-file identity is used to catch spelling aliases.

The review discloses the exact selected folder, supported extensions, recursive
behavior, all fixed bounds, metadata fields, that connection performs no scan,
that no watcher is registered, that agents/children get no folder grant, and
how revocation works. Its receipt binds the picker candidate, canonical root
identity, Home/capability/catalog revisions, current overlap set, and disclosure
digest. Commit repeats every check before atomically creating one Home-owned
Directory Reference and one sample-root record. It writes nothing in the
external folder.

The sample-root record points to the Directory Reference by stable ID and keeps
an opaque local directory identity/fingerprint, not a duplicate absolute path.
Every later scan, analysis, search freshness check, copy, and revoke resolves
the exact reference on the exact Home, rejects repointing/removal, re-runs
canonical containment/overlap checks, and requires the directory identity to
match. A replaced root or changed reference becomes `needs_attention`; the
service never follows a same-named folder. Re-adding the same/overlapping root,
concurrent commits, and retries converge through database uniqueness,
revisions and idempotency receipts.

### 7.5 Catalog storage and metadata scan

The catalog is canonical SQLite state in `internal/samplelibrary`, keyed by the
Home's stable workspace ID; a potentially 100,000-entry index is not embedded
in `workspace.json`, Assistant Program state, journey progress, or agent
memory. The normalized schema comprises:

- one `sample_library_state` row per Home with schema/catalog revision and
  lifecycle;
- revisioned root rows keyed by server root ID and Directory Reference ID, with
  active/revoked/missing state, opaque directory fingerprint, independent hash
  and embedded-tag consent flags, current generation and completeness;
- catalog entries unique by `(root ID, normalized relative locator)` with a
  server entry ID, portable slash-relative locator, filename, allowlisted
  extension, size, modified time, optional filesystem birth/creation time, and
  current generation;
- separately bounded content facts, user annotations, collections/memberships,
  scan/consent/revocation operations, and child-owned copy provenance; and
- no absolute path, audio bytes, prompt, credential, runtime grant, agent state,
  arbitrary error, or plugin manifest.

An index/refresh claims a root operation and expected catalog/root revisions
before touching the filesystem. One root scan may run at a time; an expired
claim is recovered on restart by checking whether a catalog generation was
committed, never by assuming success. Enumeration uses a context deadline,
deterministically sorted `ReadDir`, `Lstat`, and component-by-component
containment below the re-resolved root. It never follows symlinks. It skips every
hidden path component, symlink, non-regular file, device, socket, FIFO,
unsupported extension, overlong locator and over-limit entry, and never mounts,
extracts, executes, opens, auditions, or uploads a candidate. It records only
the metadata fields above.

A complete or partial staged generation replaces that root's prior active
generation in one transaction and increments the catalog/root revisions.
Catastrophic failure does not half-replace it. Receipts retain root/operation
IDs, revisions, complete/partial/failed state, timestamps/duration bucket,
visited/indexed/skipped/error counts and closed reason codes, but no paths or
filenames. At most the bounded issue examples returned to the currently
authorized Home UI may include sanitized relative locators; logs and local
events contain counts/codes only. There is no background watcher, schedule, or
startup scan.

### 7.6 Optional content analysis

Hashing and embedded-tag reading are two independent per-root booleans changed
through one explicit review surface. Enable/revoke mutates consent only; it does
not open files. A later explicit index/refresh performs only the selected
readers within the separate byte/time budgets. Revoking either reader deletes
that reader's active derived values from the root's catalog and prevents future
reads while leaving metadata access intact.

Hashing streams the exact contained regular file through a no-follow read-only
handle and records SHA-256. A hash computed solely while copying is transfer
integrity owned by the child copy receipt; it is not added to the searchable
Home catalog when hashing consent is off.

Embedded parsing uses a compiled format-reader registry and a budgeted
reader/seek interface. It retains only bounded textual fields that the parser
can establish for the supported container: title, artist, album, album artist,
genre, comment, year, track and disc. Unknown/custom fields, pictures/artwork,
lyrics, chapters, waveform/PCM data and parser blobs are discarded. A format
with no supported safe parser remains metadata/hash-only rather than invoking an
external program. Ori does not derive duration, BPM, key, loudness, similarity,
transcript, instrument, mood, or license from bytes/tags in v1.

Filenames and embedded/user text are untrusted data. Normalize valid UTF-8,
strip NUL/control/bidi characters for display/search, retain bounded text only,
and render with text nodes. They never select code, a path, root, link, role,
action, HTML, or prompt instruction. If bounded catalog facts are supplied to a
model, they are labelled as quoted untrusted data, capped to the deterministic
search result limit, and cannot authorize a mutation. Raw audio and tag blobs
never enter prompts, logs, events, errors or analytics.

### 7.7 Search, collections, notes, and agent authority

Search is deterministic over active entries from active, still-resolvable roots
only. It supports bounded normalized text/filter/sort over actual filename,
relative locator, extension, approved embedded fields, user tags, and explicitly
user-supplied pack/source/license notes. Missing fields stay missing. Results
carry the catalog/root revision and completeness; recommendations may cite only
returned entry IDs/facts. Neither deterministic code nor a model claims that a
file, license, BPM, key or analysis value exists without the corresponding
canonical field.

Collections, membership, tags and provenance notes are Home-owned revisioned
records. Simple UI edits use strict bounded CAS/idempotency mutations; an agent
may propose a typed draft, but only a host action accepted by the user commits
it. Collection names/notes are display data, not filesystem folders, and create
no sidecars. If a root is revoked, collection members remain only as redacted
unavailable entry IDs until removed; stale source filenames/locators are not
kept in unrelated projections.

The Sample Library Manager receives only compiled bounded catalog
search/explanation access after both its exact Home binding and the capability
are ready. It receives no Directory Reference, filesystem MCP root, picker,
scan, consent, copy, move, rename, delete, deduplicate, audition, import or
archive tool. The Portfolio Manager may ask it a catalog question through the
existing same-Home delegation roster, but gains no additional tool. No model is
needed for install, root consent, scan, analysis consent/revoke, search,
collections, copy, revocation or removal.

### 7.8 Exact-link copy and rollback

The project **Find samples** route carries one stable live
`AssistantProjectLink.ID`. The server resolves its Home/child and never accepts
a child workspace, sibling or destination root from the browser. A copy preview
accepts at most 64 active entry IDs plus the closed destination key
`project_samples`; that key resolves only to the exact child-owned managed
`files/Samples/` directory. V1 does not write to the attached external REAPER
folder and accepts no arbitrary destination path or overwrite policy.

Preview re-resolves the Home root/reference and live reciprocal link, opens no
content, and revalidates each relative locator component with `Lstat`. It shows
source filenames/relative locators, exact child and destination, whether the
destination directory will be created, byte/file totals, and every expected
write. Duplicate destination basenames, an existing destination, symlinked
component, unavailable/changed source, stale catalog generation, size limit, or
unwritable/uncontained child destination is a blocking conflict; v1 never
overwrites or silently renames a child file.

Commit requires the review digest, Home/root/catalog/link/child revisions and an
idempotency key, then repeats all checks. Sources are opened read-only with
no-follow handles and verified against catalog size/time/file identity (and
catalog hash when present). Files are copied into an operation-owned private
staging directory on the child's `files/` filesystem, checksummed while
streaming, flushed, and promoted with exclusive no-replace renames. Nothing
opens or edits `.rpp`, registers media with REAPER, mutates the source, or grants
the child the library root.

Multi-file commit checkpoints each promoted destination. On failure it removes
only staging and destination files whose operation receipt, identity, size and
post-copy hash prove this operation created and nobody changed them. Ambiguity
leaves the file in place and records `copy_partial`/`reconcile_required` for
explicit repair; it never deletes guessed content. Restart/replay reconciles
those checkpoints before copying a missing item. A succeeded operation returns
the same receipt on replay.

Each promoted file and bounded receipt are child-owned project data: destination
relative locator, size, SHA-256, copied time, exact link ID and stable source
root/entry IDs, plus explicitly reviewed provenance fields. Siblings receive
nothing. Later source-root/capability/Home revocation preserves the copied file
and this child-owned provenance.

### 7.9 Revocation and removal

Root revocation is a reviewed operation separate from content-analysis revoke.
It stops/refuses an in-flight scan at its operation boundary, revalidates the
exact Home/root/reference, removes that Directory Reference, marks the root
revoked, deletes its active catalog/content fields from search, and increments
revisions. It never opens, edits, moves or deletes a source file and never
removes a confirmed child copy. The retained root tombstone contains stable IDs,
closed reason, revisions and timestamps only: no absolute path or stale source
name/relative locator.

Capability/add-on removal uses the canonical capability `RemovalPlan` plus a
sample-specific impact projection listing root/catalog/collection counts,
analysis consent, the independent manager role, and preserved child-copy
receipts. Runtime removal revokes all active roots, removes active
catalog/collections/annotations, retains minimal revocation and child-owned copy
provenance, and removes the install record last. Removing the manager is a
separate explicit role choice and failure does not pretend capability removal
failed or vice versa. Home removal composes the hierarchy impact flow in
section 6. External roots and child copies are always preserved.

Generic update/delete of a sample-owned Directory Reference, root replacement,
permission loss, partial scan, parser failure and copy reconciliation affect
only the add-on/root/operation. They do not regress plugin, project connection,
mode, live readiness or staffing. Public failures use closed codes such as
`sample_root_invalid`, `sample_root_conflict`, `sample_root_missing`,
`sample_root_changed`, `sample_permission_denied`, `sample_revision_conflict`,
`sample_scan_in_progress`, `sample_scan_partial`, `sample_analysis_disabled`,
`sample_entry_unavailable`, `sample_link_unavailable`,
`sample_destination_conflict`, `sample_copy_partial` and
`sample_operation_failed`, with compiled guidance and no path, filename, tag,
note or wrapped error.

### 7.10 Threat-model regression matrix

| Threat/case                                                     | Required proof                                                                                                 |
| --------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------- |
| Raw/forged picker path or stale token                           | Refused before Directory Reference/catalog mutation.                                                           |
| Root symlink/replacement/alias/overlap/too-broad selection      | Canonical identity and global overlap checks fail closed; no scan or sidecar.                                  |
| Connect root                                                    | Exact Home reference/root only; external tree unchanged; zero scan, watcher, agent or child/MCP grant.         |
| Deep/huge/slow/permission-changing tree                         | Deterministic bounds and cancellation; honest partial/failed generation; no escape or stale-success claim.     |
| Hidden/symlink/device/socket/FIFO/unsupported file              | Never traversed/opened/indexed as a sample.                                                                    |
| Analysis declined/revoked                                       | Metadata only; no hashes/tag reads, and revoked derived fields are removed.                                    |
| Malicious filename/tag/note                                     | Bounded text-only rendering/search; no selector, prompt instruction, log/event/error leak or action authority. |
| Concurrent scans/restart/replay                                 | One claimed root operation/generation, monotonic revisions, canonical recovery and no duplicate entries.       |
| Search/manager without LLM                                      | Deterministic current results work; chat unavailability is labelled separately.                                |
| Forged sibling/link/destination or destination symlink/conflict | Refused before write; only exact live-link child `files/Samples/`, no overwrite.                               |
| Source swap during copy / partial copy crash                    | No-follow revalidation and checkpointed rollback/recovery; source unchanged and no guessed delete.             |
| Root/capability/Home revocation                                 | Search stops and active source metadata is redacted; source files and confirmed child files/provenance remain. |

## 8. FR-to-owner/test matrix

### 8.1 Complete numbered-requirement coverage

Every PRD requirement is assigned below; ranges are contiguous and leave no
unowned requirement.

| PRD requirements | Canonical owner(s)                                                                                                   | Contract asserted                                                                                                                                                                                              | Primary automated seam                                                                                                            |
| ---------------- | -------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------- |
| 1–12             | `personalassistant`, `personalassistanthttp`, specialist registry, generic shell                                     | Accepted read-back opens only the new eligible journey; decline/upgrade/dismiss/resume and copy remain truthful, deterministic and consequence-free.                                                           | Personal Assistant service/HTTP tests, specialist tests, generic-shell JS, e2e accept/dismiss/decline/no-model.                   |
| 13–24            | `specialist.SetupJourney` normalizer, built-in registry, `setupjourney` adapter registry, installed plugin blueprint | Strict inert v1 declaration, exact five-step order/IDs, equality constraints, host-only adapters/sources, synthetic-domain genericity, installed snapshots as authority.                                       | Table/fuzz tests in `internal/specialist` and `internal/setupjourney`; synthetic fixture template/JS tests.                       |
| 25–38            | `setupjourney` SQLite store/service/HTTP                                                                             | Independent root/child runs, bounded state, canonical reconciliation, CAS/idempotency/review receipts, closed errors, current relationship authority, restart/concurrency and explicit declaration migrations. | Migration/store/reconcile/action/HTTP tests including malformed state, restart recovery, race tests and `409` replay.             |
| 39–50            | reviewed-integration registry, `plugin.Manager`, nested REAPER source/release scripts                                | Exact not-a-VST copy; complete trust; separate install/update/enable; identity mismatch; resumable failures/preservation; source/version/checksum coherence; no publishing.                                    | Plugin adapter/manager tests, compatibility fixtures, nested unit/UI/artifact scripts, local coordinated demo.                    |
| 51–52            | project-connect adapter and browser-only draft state                                                                 | Exactly existing/new routes; review before mutation; route switching invalidates consent while retaining separate browser-only form entries.                                                                   | Project adapter tests and generic-shell JS/e2e route-switch/cancel tests.                                                         |
| 53–65            | trusted picker/attach service, typed project-entry resolver, workspace/blueprint creator, Home/link service          | Bounded exact `.rpp` selection; contained Directory Reference locator; untouched external tree; managed Home child; normalized snapshots/mode-aware tasks; duplicate/rollback safety.                          | Attach/path fuzz and service/HTTP tests, before/after tree hashes, symlink/swap/multiple-entry/idempotency/restart cases.         |
| 66–70            | canonical plugin-blueprint preview/create service, project-open action                                               | Reviewed scaffold directly under Home; one authoritative entry; no auto-open/agents/live/task execution.                                                                                                       | Blueprint/session creation and project-open tests plus e2e new-project path.                                                      |
| 71–73            | `AssistantProjectLink`, Home portfolio projection, hierarchy and runtime-scope resolvers                             | Stable membership plus required parent projection; bounded deterministic rollup; no recursive Home/child/sibling authority or watcher.                                                                         | Assistant Program link tests, MCP/native scope tests, hierarchy mismatch tests, Home summary tests.                               |
| 74–90            | child Setup Wizard, `runtimecapability`, plugin runtime provider/broker, typed entry resolver                        | Server modes; honest file-only; separately disclosed/confirmed live repair/grant/verify; exact-project verification; narrow regression; tasks remain confirmation-gated.                                       | Setup Wizard/runtime capability/provider tests, mocked REAPER cases, task capability tests, JS/e2e file-only/live fallback.       |
| 91–95            | Assistant Program v2 Home roster/staffing                                                                            | No pre-link staffing/generic manager; trusted Home scope; explicit Portfolio Manager binding/name/model review and no inherited authority.                                                                     | Declaration and Home staffing service/HTTP tests, no-side-effect snapshots, UI tests.                                             |
| 96–97            | Home portfolio service and exact-link child task preparation                                                         | Revisioned host-owned fields; bounded canonical summaries; archivist cannot move/delete trees.                                                                                                                 | Portfolio CAS/idempotency/authorization tests and forbidden-file-action tests.                                                    |
| 98–103           | per-link project roster, workspace-local Agent snapshots/instances, same-workspace delegation                        | Explicit coordinated three-role staffing, fresh independent instances per child, later-child isolation and exact same-child delegation.                                                                        | Partial provisioning/name collision/retry tests, two-child identity/delegation/runtime-scope tests.                               |
| 104–105          | exact-link handoff adapter and `TicketService.CreateIdempotent`; read-only Home rollup                               | Confirmed child-owned Ticket/receipt without child tools; no child data materialization in Home.                                                                                                               | Handoff stale-link/idempotency tests and Home data-leak assertions.                                                               |
| 106–112          | v2 Home/link records, managed learning/reflection, scoped attribution, Personal Assistant projection                 | Stable key/scoped bindings, bounded partial failure/no-model behavior, no role side effects, legacy-only hardcode, learning/Personal HQ isolation.                                                             | Assistant Program persistence/migration/learning tests, no-model and Personal HQ authorization tests.                             |
| 113–117          | Home add-on service, scoped optional role, `workspacecapability.Service`                                             | Optionality and six decisions; explicit role; inert Home-only capability install with no companion/root/scan/child/plugin action.                                                                              | Capability/role/add-on lifecycle and no-side-effect tests.                                                                        |
| 118–121          | sample picker/root service, Directory Reference owner, bounded scanner and per-root analysis consent                 | Exact non-inferred root/no source write; explicit bounded metadata scan; independent hash/tag consent; no watcher/derived audio behavior.                                                                      | Root canonical/overlap/symlink tests, source-tree snapshots, scanner limit/permission/restart tests, read-counter analysis tests. |
| 122–124          | Home-owned sample SQLite store/search/collections, bounded manager tools                                             | No external sidecars; deterministic active-entry facts; untrusted text; agents cannot perform host file actions or invent facts.                                                                               | Store/search/CAS tests, prompt/redaction/HTML fixtures, agent least-privilege tests, no-LLM tests.                                |
| 125–132          | exact-link sample copy service, child provenance, root/capability/Home removal plans                                 | Exact previewed contained no-overwrite copy; no full-root grant/`.rpp` import; sibling isolation; source-write non-goals; revocation/concurrency/restart; preserved source/copies.                             | Copy TOCTOU/conflict/rollback/replay tests with hashes; revoke/removal/overlap tests; e2e add-on path.                            |
| 133–141          | journey reconciler/summary, Personal Assistant/Home projections                                                      | Main/child completion formula, precise receipt vocabulary/actions, file-only/live rows, shared normalized reporting and independent resume.                                                                    | Reconcile/summary golden tests, reload/child-run/add-on isolation tests, JS accessibility copy assertions.                        |
| 142–151          | canonical reconcilers, explicit migration, Assistant Program disconnect/Home lifecycle, offer migration              | Narrow repair; no repeated consequences; non-modal accepted upgrade; stable reuse; preserved legacy topology; protected reparent/Home removal; unchanged declined/unanswered.                                  | Regression/migration/impact/CAS tests, group move/delete/restore tests, upgrade fixtures and e2e recovery.                        |
| 152–159          | strict HTTP projections, local event mapper, generic shell/Home/sample JS/CSS                                        | Text-only untrusted values; canonical containment; sensitive-data event/error redaction; no outbound analytics; keyboard/focus/live-region/responsive/busy/close safety.                                       | HTTP/log/event leak tests, DOM/unit accessibility tests, axe/Playwright mobile/desktop and in-flight close cases.                 |
| 160              | all owners above                                                                                                     | Every release state below is automated with disposable roots and mocked REAPER.                                                                                                                                | Go integration + JS modules + `tests/e2e/specialist-setup-journey.spec.js`; nested plugin contract suite.                         |
| 161              | coordinated browser/demo harness                                                                                     | Real acceptance/trust return, both project routes/modes, scoped teams, second project, add-on, handoff, resume and final receipt in isolated data.                                                             | Playwright release spec and `scripts/reaper-demo.sh`; screenshots/manual test guide.                                              |
| 162              | every HTTP/service trust boundary                                                                                    | Client cannot choose source/command/adapter/user/Home/parent/role/root/path/runtime scope/sibling/unlinked target.                                                                                             | Table-driven unknown-field/forged-ID tests, path fuzzing and negative e2e requests.                                               |
| 163              | integration regression owners                                                                                        | Existing Personal Assistant, workspace confirmation/grouping, Setup Wizard/runtime authority, stable-key/scoped delegation, sample isolation, plugin preservation and no-specialist path remain intact.        | Existing package suites plus dedicated cross-subsystem regression table and full delivery gate.                                   |

### 8.2 Requirement 160 release-case ownership

| Release case                                                                     | Owning acceptance test/result                                                                                                                  |
| -------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------- |
| Accept REAPER offer; dismiss/restart; declined offer                             | Personal Assistant + root-run e2e proves only relationship/progress changed, canonical continuation resumes, and decline creates nothing.      |
| Integration already compatible; update/enable required; checksum/install failure | Reviewed-integration adapter tests prove no reinstall, separate review/commit and resumable failure with no downstream resource.               |
| Existing one/many/unsafe `.rpp` folder                                           | Attach-service/e2e fixtures prove exact choice, containment refusal and byte-for-byte external-tree immutability.                              |
| New project; duplicate connect/create                                            | Canonical creator/idempotency tests prove one scaffolded child under one Home/link, no open/agent, and replayed identity.                      |
| Parent/child scope check                                                         | Runtime/MCP/native resolution tests prove Home, child, sibling and external roots do not flow through parentage.                               |
| File-only; blocked live; wrong project; successful live verify                   | Setup Wizard/runtime/provider tests prove optional live rows, fallback, no wrong-project mutation, and exact canonical verification timestamp. |
| Add Portfolio Manager; add first project team                                    | Scoped staffing tests prove one Home binding, three child-only bindings and no unrelated consequence.                                          |
| Second REAPER project                                                            | Root/child-run + roster tests prove same Home/Portfolio Manager, independent child run/instances, and unchanged first/sibling receipts.        |
| Portfolio handoff                                                                | Exact-link Ticket test proves confirmed child ownership and no Home child tools.                                                               |
| Add Sample Library Manager; connect root                                         | Optional-role/capability/root tests prove each consequence separate, exact Home reference, no scan/child inheritance/source change.            |
| Index/refresh metadata; content analysis declined                                | Scanner/read-counter tests prove bounded metadata-only enumeration and zero file opens.                                                        |
| Sample handoff; revoke sample root                                               | Copy/revoke tests prove exact child destination/no root grant/`.rpp` edit and preserved source/child bytes with inactive catalog entries.      |
| Reparent linked child; delete Music Production Home                              | Lifecycle tests prove generic refusal, reviewed disconnect/removal, and preserved child/team/external/sample/copied state.                     |
| Plugin disabled after completion                                                 | Plugin/Assistant Program/journey reconciliation test proves readable preserved data, paused execution and narrow attention state.              |
| Reload/concurrent tabs                                                           | SQLite/reconcile/HTTP tests prove first unresolved state, one consequence, replay and bounded `409`.                                           |
| No configured LLM                                                                | Deterministic journey, setup, portfolio and sample tests pass with model providers absent; role execution is separately unavailable.           |
| Synthetic second domain                                                          | Registry/service/template/JS fixture uses the same declaration/state/shell without production REAPER branching.                                |
| Generic/no-specialist Personal Assistant                                         | Existing regression suite and explicit fixture prove behavior and storage remain unchanged.                                                    |
