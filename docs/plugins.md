# Plugins

Ori can install **Claude Code-** and **Codex-compatible plugins** — bundles that
package MCP servers and skills (and, in those ecosystems, commands/agents/hooks).
Plugins may also carry an Ori Workspace Surface contribution: sandboxed UI and a
verified private service behind Ori's host broker. This is not a return of the
old gRPC plugin system. Portable MCP/skill components use Ori's existing
registries; Workspace Surfaces use the constrained v1 protocol documented below.

## Installing a plugin

**From the UI** — open **Plugins** in the sidebar:

- **Browse official Claude plugins** — click **Browse official plugins** to open
  Anthropic's official, managed directory in a modal (it loads on first open — no
  setup step), then search/filter the card grid by keyword, category, or tag, open
  **Details** to see what a plugin registers, and **Install** with one click
  (confirmed inline in the modal). Already-installed plugins are marked.
- Paste a **local path** or **git URL** under _Install a plugin_, click **Preview**,
  review what it will register, then **Confirm install**.
- Or **add a marketplace** (git URL / local folder) and install a listed plugin by name.

**From the API:**

```
# Preview (no changes) — returns the trust disclosure
POST /api/plugins/install     {"source": "<path|git>", "confirm": false}
# Install
POST /api/plugins/install     {"source": "<path|git>", "confirm": true}
GET  /api/plugins                                  # list installed
POST /api/plugins/{name}/enable | /disable         # per-workspace binding state
DELETE /api/plugins/{name}                         # uninstall (removes all components)

# Marketplaces
GET  /api/plugins/marketplaces            # also returns {"official": {name, source, added}}
POST /api/plugins/marketplaces            {"source": "<path|git>"}
POST /api/plugins/marketplaces/install    {"marketplace": "...", "plugin": "...", "confirm": true}
```

The **official marketplace** is `anthropics/claude-plugins-official`. Its source is
held server-side and exposed via the `official` block of `GET /api/plugins/marketplaces`
(with `added` reflecting whether it has been added). The browse modal adds it
automatically on first open (POSTing that source — a one-time catalog clone), so there
is no manual "add" step. Adding is idempotent (keyed by catalog name), and the official
catalog is shown only in the browse modal, never duplicated in the user-added list.

### Marketplace entry sources

A catalog entry's `source` may be a string (a path relative to the catalog, or a git
URL) or an object. Object forms are normalized to one installable source:

- `{"source":"local","path":"./plugins/x"}` → path relative to the catalog
- `{"source":"github","repo":"owner/name"}` → `https://github.com/owner/name.git`
- `{"source":"git-subdir"|"url","url":"…","path":"plugins/x","ref":"v1","sha":"…"}` →
  a **git repo + subdirectory**: Ori clones the repo, **pins to `sha` (preferred) or
  `ref`**, and installs from the subpath. Pinned commits are fetched shallowly
  (`git fetch --depth 1 <pin>`) into a per-commit clone directory so different pins of
  the same repo don't collide.

## Trust

Before anything is registered, install shows a **disclosure**: the exact commands each
MCP server will run, the skills it adds, any unsupported components, and warnings (e.g. a
missing binary). Nothing is registered until you confirm; declining makes **no changes**.

## Blueprint dependencies and in-wizard recovery

A workspace blueprint (built-in, user-authored, or plugin-contributed) can
declare a plugin dependency. The Create Workspace wizard's blueprint catalog
carries one **readiness** projection per blueprint — `ready`,
`action_required`, or `unavailable`, with a closed set of reason codes
(`plugin_install_required`, `plugin_enable_required`,
`plugin_update_required`, `platform_unsupported`, `protocol_incompatible`,
`blueprint_retired`, `manifest_invalid`, `runtime_provider_unavailable`,
`dependency_state_unknown`) and an allowlisted set of recovery actions
(`install_plugin`, `enable_plugin`, `review_plugin_update`, `retry`,
`manage_plugins`, `change_blueprint`, `edit_template_manifest`). The server
re-derives this from the installed-plugin store on every catalog load and
again immediately before workspace creation; the catalog a client is holding
is guidance, never an authorization to create.

### Install versus enable

**Installing a plugin never enables it, and enabling never re-installs it.**
These are two separate, explicit user acts everywhere in the product — the
Plugins page and the wizard use the identical wording
(`internal/web/static/js/modules/plugin-lifecycle.js` is the one source of
those labels, read by both surfaces):

- **Installed, still disabled** — the plugin is on disk and registered, but
  switched off. Nothing it declared is running.
- **Installed and enabled** — the plugin is on disk, registered, and active.

A blueprint's "Install" recovery action performs both steps under that one
press (its button reads **Install and enable**), but reports them as two
outcomes: if install succeeds and enable fails, the wizard states
**"Installed, still disabled"** and offers Enable as the next action — never a
bare install failure, because the install itself worked.

### In-wizard recovery

A blueprint card whose dependency is `action_required` shows a badge, an
accessible description, and — in the Step 1 briefing — a readiness panel with
the one next action. Choosing a plugin lifecycle action (Install, Enable,
Review update) opens a recovery panel that follows the same confirm/cancel
contract as the standalone Plugins page:

1. **Preview** — a request with `confirm:false` returns the trust disclosure.
   Nothing is registered yet.
2. **Disclosure** — the complete trust report renders inline: commands,
   skills, background services, downloadable artifacts (with their SHA-256),
   permission scopes, and any workspace UI surfaces the plugin adds. Nothing
   is summarized or hidden. The install source itself is shown here, and only
   here — the catalog never echoes a template-declared source, because a
   manifest is untrusted input and the disclosure is where a source is finally
   read in context, immediately before the user agrees to it.
3. **Confirm or cancel** — confirming applies the action; cancelling makes no
   changes. A pending confirmation is invalidated (silently, not as a decline)
   if the user selects a different blueprint or closes the modal — consent
   given for one blueprint's disclosure can never be applied to another.
4. **Result and catalog reload** — on completion the wizard re-reads the
   catalog and re-selects the trusted replacement by **plugin owner and
   blueprint ID**, never by display text, since an update can rename a
   blueprint. The workspace name, description, context, team, and every other
   field the user already entered are untouched — the recovery panel is the
   only part of the wizard a lifecycle action ever writes to.

The endpoint is `POST /api/project-templates/{templateID}/plugin-recovery`:

```
{"action": "install_plugin" | "enable_plugin" | "review_plugin_update",
 "plugin": "<name>", "confirm": false | true, "generation": <uint64>}
```

The client sends an action name and a plugin name — **never a source, a path,
or a command**; the server resolves the source from the blueprint's own
declaration, and refuses the request outright if the named blueprint does not
actually depend on the named plugin. `generation` is the installed-plugin
generation the client's disclosure was read at; a confirmation carrying a
stale generation is refused with `409`, so a captured/replayed confirmation
can never be applied after the plugin has moved on. The response always
carries the freshly re-derived readiness and, on success, the blueprint's
current qualified ID.

### Unsupported and incompatible states

`platform_unsupported` (no artifact for this OS/architecture) and
`protocol_incompatible` (the plugin's declared surface protocol range excludes
this host) are hard blockers: no lifecycle action can resolve them, so the
wizard offers explanation and **Manage Plugins** / **Change blueprint**
instead of a retry loop. Both are rechecked against the installed record on
every projection, not trusted from install time — a host upgrade can move a
previously-compatible plugin outside the supported range.

### Retired built-ins

An on-disk manifest that claims `"builtin": true` under an ID this build no
longer ships is classified **retired**: `blueprint_retired`, ownership
`builtin`. Its files are preserved exactly as they are — retirement is a
catalog decision, never a filesystem one — and it is excluded from ordinary
creation. A retired built-in is never told to "fix its template.json"; nobody
using it wrote that file, and telling them to edit shipped JSON would be
wrong regardless of whether the edit is even reachable in the UI. If an
installed plugin's contributed blueprint carries the same ID, that trusted
candidate supersedes the retired built-in in the catalog — even while the
plugin is still disabled, so the user sees the current, correct blueprint and
its one remaining step rather than a stale shipped copy sitting in front of
it.

### Genuinely invalid manifests

A manifest whose `runtime_requirements` or `setup_wizard` block could not be
understood is `manifest_invalid`. The instruction to fix `template.json`, and
the underlying parser diagnostic (bounded, behind an accessible disclosure),
are shown **only when the manifest's ownership is `user`** — a template the
person looking at the message actually authored or imported. For a shipped
built-in or a plugin-contributed blueprint whose manifest fails validation,
the same reason code produces generic copy with no file-edit instruction and
no diagnostic text, on both the server (the readiness projection withholds
`Diagnostic` for any ownership but `user`) and the client (the same rule is
enforced independently in `blueprint-readiness.js`).

## Supported components

| Component                                      | Status                                                    |
| ---------------------------------------------- | --------------------------------------------------------- |
| MCP servers (`.mcp.json`)                      | ✅ registered                                             |
| Skills (`skills/<name>/SKILL.md`)              | ✅ installed (discovered by Ori)                          |
| Workspace Surfaces (`.ori-plugin/plugin.json`) | ✅ sandboxed UI + brokered private service                |
| Slash commands (`commands/`)                   | ⏸ recognized, **skipped + reported** (not yet registered) |
| Agents (`agents/`)                             | ⏸ recognized, skipped + reported                          |
| Hooks (`hooks/`)                               | ⏸ recognized, skipped + reported                          |
| Codex app connectors (`.app.json`)             | ⏸ metadata only                                           |

Unsupported components never fail an install — they're listed in the trust disclosure and
on the plugin's entry.

## Plugin layout

**Claude Code:**

```
my-plugin/
├── .claude-plugin/plugin.json     # name (required), version, description, …
├── .mcp.json                      # { "<server>": { "command": "${CLAUDE_PLUGIN_ROOT}/bin/x", "args": [] } }
└── skills/<name>/SKILL.md
```

**Codex** (also supports a versioned `<plugin>/<version>/` layout):

```
my-plugin/
├── .codex-plugin/plugin.json      # name, version, "mcpServers": "./.mcp.json", "skills": "./skills/", "interface": {…}
├── .mcp.json                      # { "mcpServers": { "<server>": { "command": "./bin/x", "cwd": "." } } }
└── skills/<name>/SKILL.md
```

Ori normalizes both formats into one internal descriptor. Notable differences it handles:
the `.mcp.json` shape (Codex wraps servers under `mcpServers`; Claude uses a bare keyed
map), and the plugin-root model (Claude's `${CLAUDE_PLUGIN_ROOT}` vs. Codex relative
command + `cwd`, which Ori resolves to an absolute command).

## Workspace Surface v1

A plugin may add an Ori contribution at the fixed path
`.ori-plugin/plugin.json`. The contribution can declare sandboxed workspace UI,
a private MCP stdio service, verified platform artifacts, bounded operations,
runtime providers, grant-gated agent operations, and workspace blueprints.
It supplements rather than replaces the portable Claude or Codex identity.

Start with the copyable, runnable example:

```bash
go test ./internal/plugin \
  -run '^TestWorkspaceSurfaceExampleValidatesAndInstallsArtifact$' -v
open examples/plugins/workspace-surface-demo/README.md
```

The strict wire contract and canonical fixtures are in
[`docs/architecture/plugin-workspace-surfaces-v1.md`](architecture/plugin-workspace-surfaces-v1.md)
and `internal/plugin/testdata/workspace-surface-v1/`.

### Identity and manifest merge

At least one base manifest is required. If Claude and Codex manifests are both
present, **every** present base identity and the Ori contribution must have the
same case-normalized name and exact version. A mismatch rejects the whole
contribution; Ori never combines executable components from differently named
plugins.

```text
my-plugin/
├── .claude-plugin/plugin.json       # portable identity/components
├── .codex-plugin/plugin.json        # optional second portable identity
├── .ori-plugin/plugin.json          # Workspace Surface v1 contribution
├── ui/index.html                    # inert relative asset paths from manifest
└── artifacts/my-service-<os>-<arch> # exact size + SHA-256 in manifest
```

The Ori manifest's top-level fields are:

- `schema_version`: exactly `1`;
- `name`, `version`: exact shared plugin identity;
- `protocol`: supported inclusive `min`/`max` range;
- `capabilities`: owner-scoped display metadata and surfaces;
- `services`: MCP stdio entrypoints, platform artifacts, and operations; and
- `blueprints`: bounded inert template references.

Unknown fields, blank or duplicate IDs, owner collisions, unsafe paths,
unsupported placements/transports, invalid protocol ranges, unknown symbolic
scopes, and invalid/unbounded schemas are errors. Ori does not silently drop an
executable field. Workspace records and public catalog responses contain only
inert IDs/display metadata; commands, asset roots, operation bindings, and
service objects remain in trusted process registries.

### Artifacts, services, operations, and schemas

Workspace Surface services use `mcp_stdio` and are private to Ori's broker. They
are not registered wholesale as agent tools. For each supported OS/architecture,
declare one artifact with an HTTPS or bundled relative source, exact byte size,
and lowercase SHA-256. Ori streams into a private staging directory, verifies
before applying executable permissions, and atomically publishes the artifact.
It never runs plugin build/install scripts. A missing platform remains visible
as `platform_unsupported` and is never launched.

Each operation declares:

- a stable ID;
- bounded input and output JSON schemas;
- `max_output_bytes`;
- a fixed timeout class;
- one policy (`read_only`, `reversible`, or `confirmation_required` in v1); and
- host-known symbolic scopes only.

Schemas use the bounded v1 JSON Schema subset documented in the contract. Do
not put commands, endpoints, environment values, raw filesystem paths, or
workspace selectors in operation metadata. Ori obtains user/workspace/plugin
identity and canonical roots from the authenticated session and injects context
after authorization. A frame may invoke only operations listed on that exact
surface. Output is schema/size checked and sanitized again before returning.

### Browser SDK and frame lifecycle

Copy `internal/web/static/js/plugin/workspace-surface-sdk.js` into the plugin's
asset tree and import `createWorkspaceSurfaceSDK`; do not import an unversioned
file from Ori at runtime. The frame is sandboxed with an opaque origin, no
ambient credentials, a restrictive CSP, and no parent DOM access. Communicate
only through the authenticated parent bridge:

```js
import { createWorkspaceSurfaceSDK } from './workspace-surface-sdk.js';

const sdk = createWorkspaceSurfaceSDK();
sdk.on('ready', async () => {
  const status = await sdk.invoke('status.read', {});
  document.querySelector('[data-status]').textContent = status.value;
});
sdk.on('visibility', ({ visible }) => {
  // Pause plugin-owned work while hidden; host status polling is also clamped.
});
sdk.on('invalidated', () => {
  // Stop timers and discard local session assumptions.
});
```

Available calls are `invoke`, `getState`, `setState`, `deleteState`, `askOri`,
`openSetup`, `statusChanged`, and `close`. State is namespaced by plugin and
workspace, quota bounded, revision checked, and cannot write core workspace
fields. Ask Ori context is bounded untrusted context, never authority. Setup
routes and required capabilities come from the trusted surface declaration.
Confirmation UI and single-use approval tokens are host-owned; a frame cannot
make itself confirmed by adding a field to an operation payload.

A surface session is bound to the current user, workspace, owner, capability,
surface, frame token, and plugin generation. It expires and is invalidated on
close, capability removal, service restart, update, disable, uninstall, or
server shutdown. Services start lazily, have bounded concurrency/timeouts, and
stop before plugin files are replaced.

### Install and lifecycle

Preview reports browser code, service executable/transport, operations and
policy classes, symbolic scopes, artifacts/digests, compatibility, and other
portable components. Confirmed install verifies artifacts and registers the
trusted contribution atomically; newly installed plugins remain disabled until
the user enables them.

- **Enable/re-enable:** register the current compatible generation.
- **Disable:** invalidate sessions, stop services, remove executable bindings,
  and preserve namespaced state.
- **Update:** stop and unregister the old generation before replacing files;
  changed executable footprint/access requires renewed confirmation.
- **Uninstall:** invalidate, stop, unregister, remove managed components, and
  explicitly delete namespaced plugin state.
- **Restart:** restore compatible trusted installed records without starting an
  unneeded service; shutdown stops all started services.

Native plugin code is trusted code after disclosure. The broker prevents a
frame, workspace file, or confused caller from exercising undeclared authority;
it cannot sandbox a dishonest native executable from authority the OS has
already granted to that process. Review source and disclosures accordingly.

### Errors and diagnostics

Public errors are stable `{code,message}` objects, optionally with an opaque
`confirmation_id`. Common bridge/broker codes include
`surface_unavailable`, `session_unknown`, `session_invalidated`,
`asset_not_found`, `operation_unknown`, `input_invalid`,
`runtime_grant_required`, `confirmation_required`, `confirmation_invalid`,
`service_timeout`, `service_unavailable`, `output_invalid`,
`intent_unavailable`, `state_conflict`, `state_quota_exceeded`, and
`state_invalid`. Install/compatibility errors include
`plugin_identity_mismatch`, `protocol_incompatible`, `platform_unsupported`,
and strict contribution/schema error codes.

Treat codes as the programmatic contract and messages as host-owned copy. Do
not parse messages or expect raw stderr. Ori redacts commands, environment,
paths, endpoints/ports, usernames, stack traces, credentials, panic text, and
raw schema/service diagnostics.

### Author accessibility duties

Ori owns the station button, modal shell, focus trap, Escape/close behavior,
and focus restoration. The plugin owns all semantics inside its frame and must:

- use headings, landmarks, labels, and native controls;
- provide complete keyboard operation and visible focus;
- expose status/results with appropriate `aria-live` behavior without noisy
  polling announcements;
- never rely on color alone and honor reduced motion/high contrast;
- keep layouts usable within declared, host-clamped modal dimensions;
- pause timers/work when hidden and stop on `invalidated`/`pagehide`; and
- render all plugin/service/user strings as text, never unsanitized HTML.

Test Tab/Shift+Tab, Enter/Space, Escape through the host modal, focus return to
the station, zoom, long bounded strings, unavailable/degraded states, and a
screen reader pass.

### Compatibility and lifecycle testing

Before release, run strict local validation and test each declared platform or
an explicit unsupported-platform projection. At minimum cover:

1. preview decline (zero mutations), confirmed install, and disabled initial state;
2. enable, catalog/station status, modal handshake, declared operation, and an
   undeclared-operation rejection that never reaches the service;
3. state reload and compare-and-swap conflict;
4. host-owned confirmation cancellation, approval, expiry, and replay denial;
5. deep link, keyboard flow, visibility polling pause, close/focus restoration;
6. service timeout/crash with sanitized output and bounded restart;
7. disable/re-enable, trust-changing update, and uninstall while a frame is open;
8. stale session/asset/generation refusal and clean reinstall; and
9. older/newer protocol mismatch plus every supported OS/architecture artifact.

Run Ori's affected Go/JavaScript/Playwright suites under the race detector where
applicable. Never claim compatibility from compilation alone; preserve canonical
fixture transcripts and observable browser/service evidence.

### Extracted integrations and legacy workspaces

A plugin can replace a previously compiled integration without migrating old
workspace state. Ori does not infer attachment from names, tags, project file
extensions, tasks, agents, or retired template provenance. Old records remain
inert and receive the generic `provider_unavailable` setup result; manually
attaching the plugin starts with fresh namespaced state and never imports a
retired integration's pins, grants, setup history, or provenance.

The first complete extracted integration is `reaper-plugin`; version 0.4.0 adds
Project Tidy's capability-scoped survey/apply operations. Its Workspace Surface
service currently declares only a **macOS arm64** artifact. Every other
OS/architecture is explicitly unsupported and Ori must not launch a fallback
binary. Portable Claude/Codex shell skills remain a separate plugin feature.
Capability-scoped Codex tasks use Ori's schema-constrained brokered tool loop,
which repeats the canonical folder-backed runtime grant check before each
operation and does not expose arbitrary localhost shell access or the private
plugin service wholesale.

## Binary delivery

Ori runs whatever command a plugin's `.mcp.json` specifies; it does **not** build the
binary. If the resolved command is missing or not executable, the plugin still installs but
the server is marked **"unavailable — binary missing"** until you provide the binary
(prebuilt, `go build`, or a downloaded release) and re-enable it.

## Component ownership & uninstall

Plugin-registered MCP servers and skills are **owned by the plugin** (managed via
install/uninstall, not edited directly). Uninstall removes every component the plugin
registered, recorded at install time, leaving no orphans.
