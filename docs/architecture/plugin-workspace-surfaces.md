# Plugin Workspace Surfaces v1 Architecture Decision

**Status:** Implemented; protocol v1 frozen; compiled REAPER extraction complete

**Decision date:** 2026-08-24

**Planning source:** `tasks/prd-plugin-workspace-surfaces.md`

**Parity source:** `tasks/reaper-parity-matrix-plugin-workspace-surfaces.md`

## Decision summary

Ori will support trusted, code-authored plugin contributions through one
versioned **Workspace Surface protocol**. A plugin may contribute inert
workspace-capability metadata, one or more Map-station/modal surfaces, an
optional local service, runtime/setup providers, agent operations, and inert
blueprints. Ori remains the authority for installation trust, workspace
ownership, attachment, runtime grants, setup presentation, browser isolation,
confirmation, state quotas, service lifecycle, and execution scope.

Version 1 has exactly one placement: a sanitized station on a workspace Map
opens a host-owned modal containing a plugin document in a sandboxed iframe.
The plugin document cannot call Ori APIs or a service directly. It communicates
through a plain-JavaScript SDK using bounded `postMessage` requests. The parent
host validates the exact source window and forwards eligible requests to one
authenticated broker. Plugins never add routes to Ori's router.

The plugin service is trusted native code running as the user. The iframe is an
isolation boundary for browser credentials, DOM, and CSS; it is not a sandbox
for the native process. Install and update disclosures must state both facts.

## Fixed v1 choices

| Item | V1 decision |
| --- | --- |
| Surface protocol | Integer `1`; host and contribution ranges must intersect at `1` |
| Contribution manifest | Optional `.ori-plugin/plugin.json` at the resolved plugin root |
| Portable identity | At least one Claude or Codex manifest remains required; every present manifest and the Ori manifest must have the same normalized name and exact non-empty version |
| Placement | Workspace Map station → host-owned modal only |
| Browser isolation | `<iframe sandbox="allow-scripts">`; no `allow-same-origin`, forms, popups, downloads, or top navigation |
| Browser/service path | Frame SDK → exact parent window → authenticated Ori broker → trusted service manager |
| Plugin routes | None; only generic catalog/frame/asset/session/state/intent/broker routes |
| Workspace state | Inert capability/provenance identifiers only; no plugin key/value state in `workspace.json` |
| Plugin key/value state | Ori-managed global namespaced store, per plugin + workspace, bounded and non-executable |
| Blueprint boundary | Install-time validated inert blueprint descriptors and skeleton bytes in the trusted installed-plugin registry |
| Service artifact | Host-selected OS/architecture artifact, SHA-256 verified into Ori's managed plugin directory before executable permission |
| First REAPER artifact | `darwin/arm64`; all other platforms unavailable until explicitly declared |
| Native trust | Full local code execution under the user's account; iframe isolation does not reduce this trust |
| Legacy REAPER | No automatic attachment, state import, or fallback; generic missing-provider behavior after core removal |

## Actors

1. **User** — confirms install/update trust, attaches a capability, grants exact
   runtime access, confirms protected operations, and controls setup.
2. **Ori top-level browser host** — renders catalog stations, owns the modal and
   focus, creates the sandboxed frame, validates frame messages, and performs
   authenticated broker requests.
3. **Plugin frame** — plugin-authored HTML/CSS/JavaScript and the Ori author SDK.
   It is executable but isolated from Ori's origin identity, DOM, cookies, and
   direct network access.
4. **Workspace Surface HTTP boundary** — authenticates the user, checks workspace
   ownership, issues/invalidates sessions, serves bounded assets/frames, and
   exposes generic broker/state/host-intent endpoints.
5. **Installed-plugin registry** — the trusted, global resolution source for
   plugin identity, generation, compatibility, asset roots, service commands,
   artifact digests, operation schemas, provider bindings, and blueprints.
6. **Workspace capability registry/service** — resolves inert per-workspace
   attachment records against installed global contributions.
7. **Plugin Service manager** — lazily starts, probes, calls, restarts once, and
   stops the selected local artifact using bounded lifecycle rules.
8. **Plugin Service** — trusted domain implementation. It receives validated
   operation input plus host-injected authoritative context, never browser
   authority.
9. **Runtime Capability and Setup Wizard services** — retain modes, disclosures,
   grants, execution scopes, step state, retries, cancellation, and summaries;
   they call a generic plugin-provider proxy.
10. **Agent operation adapter** — invokes declared operations through the same
    broker policy as the UI after exact agent/runtime authorization.
11. **Plugin state store** — atomic, quota-bound JSON values scoped to one
    installed plugin identity and workspace.

## Trust boundaries

### Untrusted before installation

A source directory, git checkout, marketplace record, manifest, release URL,
artifact bytes, UI asset, blueprint, operation schema, and all plugin-authored
text are untrusted input. Parsing and preview do not register a contribution,
make a file executable, attach a capability, or launch a process.

The installer normalizes the complete contribution, selects an artifact for the
current platform, downloads with a byte limit, verifies its declared SHA-256,
and computes one trusted-component fingerprint. The user confirms the complete
browser/native/operation/scope footprint. Only then does Ori place verified
files in its managed plugin directory and atomically publish the trusted
installed record.

### Trusted native code, constrained authority

A confirmed Plugin Service is trusted to run as the user and can misuse the
user account like any other installed local executable. Operation declarations,
confirmation classes, and symbolic scopes constrain what Ori will request and
what honest plugins can receive from Ori. They do not make a dishonest native
binary safe. Product copy must not call the service sandboxed.

The service still receives less ambient authority than an arbitrary shell
integration: Ori does not forward cookies, bearer tokens, raw workspace JSON,
browser-selected paths, arbitrary writable roots, or arbitrary endpoints. It
injects only the canonical context and symbolic scopes approved for that
operation.

### Plugin frame

The frame is treated as hostile web content even though it came from a trusted
installation. It cannot share Ori's origin, inspect the parent DOM, receive Ori
cookies/tokens, submit forms, open windows, navigate the top level, choose its
z-index, or fetch internal APIs. Its CSP denies arbitrary network connections.
All frame strings and response data remain untrusted when rendered by the host.

Because a sandboxed opaque-origin frame reports a `null` origin, origin-string
checks are insufficient. The parent bridge requires all of:

- `event.source === iframe.contentWindow`;
- the exact active local bridge ID and challenge;
- a known v1 message type and closed payload shape;
- message byte, nesting, member, array, and string limits;
- an active server session bound to the same user/workspace/plugin/capability/
  surface/plugin generation;
- current eligibility on every forwarded request.

The parent sends messages to the exact `contentWindow`; `targetOrigin="*"` is
used only because opaque origins cannot be named. The source-window and random
challenge checks are therefore mandatory and tested.

### Workspace and blueprint data

Workspace files and plugin blueprints are inert selectors, never executable
registries. Opening/importing a workspace cannot introduce code. A persisted
record may name a qualified capability/provider/blueprint and carry bounded
provenance/version state. Only the installed-plugin registry may resolve that
name to a command, asset root, schema, service operation, or implementation.

## Identity and manifest merge

### Location

A plugin that contributes Ori components adds:

```text
<plugin-root>/.ori-plugin/plugin.json
```

The location is intentionally separate from `.claude-plugin/plugin.json` and
`.codex-plugin/plugin.json`. Ori does not overload fields owned by another host,
and portable MCP/skill-only plugins remain byte-behavior compatible when the
Ori file is absent.

### Merge rule

1. Existing Claude/Codex discovery still selects the preferred packaging format
   for MCP and skill normalization.
2. If `.ori-plugin/plugin.json` is absent, no Surface parsing occurs and current
   plugin behavior is unchanged.
3. If it is present, Ori reads every Claude/Codex manifest present for identity,
   not only the preferred one. At least one base manifest must exist.
4. `name` is trimmed and normalized with the existing plugin-name rules for
   comparison. `version` is trimmed and compared exactly. The Ori manifest and
   every present base manifest must have the same non-empty name and version.
5. A mismatch, duplicate component ID, unknown field, invalid protocol range,
   unsafe path, unsupported schema, or platform artifact collision rejects the
   whole Ori contribution. It is never silently dropped while MCP/skills install.
6. The installed plugin name is the v1 owner identity. Ori rejects a second
   installed plugin with the same normalized name; source order never wins a
   collision.
7. Contribution-local capability, surface, service, operation, provider, and
   blueprint IDs are qualified by the owner name. A stable surface key is:
   `plugin:<plugin-name>:<capability-id>:<surface-id>`.

V1 does not claim cryptographic publisher identity. HTTPS, pinned git commits,
artifact digests, complete disclosure, and explicit confirmation protect bytes
and footprint; publisher signing is future work.

## Persisted records versus trusted records

### Workspace-persisted inert projection

A plugin-backed capability record may persist only:

- normalized plugin owner name;
- plugin version observed at attachment;
- local capability ID and definition version;
- selected surface/provider/blueprint IDs when needed as inert references;
- install source and timestamps;
- inert owned-resource IDs and unavailable/tombstone lifecycle state.

Template provenance may additionally snapshot bounded labels, setup steps,
runtime requirement keys, symbolic scope names, project-entry metadata, and
plugin blueprint ID/version. It may not carry an artifact URL/digest, command,
argument, environment value, filesystem asset root, executable path, route,
MCP tool name, arbitrary endpoint, module reference, operation implementation,
or raw writable path.

### Global trusted installed-plugin record

The global record owns:

- normalized contribution and compatibility data;
- trusted component fingerprint and monotonically changing plugin generation;
- verified artifact metadata and managed executable path;
- canonical asset and blueprint roots;
- service command/transport and lifecycle limits;
- operation input schemas, output limits, timeout classes, policy classes, and
  symbolic scopes;
- runtime/setup/agent provider operation bindings;
- validated blueprint descriptors and skeleton inventory.

The plugin generation changes on install, update, enable/disable transition, or
service replacement. Surface sessions and confirmations bind to it so stale
browser state cannot cross a lifecycle change.

## Plugin state decision

Plugin key/value state will **not** be added to `workspace.json`. It lives under
Ori's managed data root in a store keyed by normalized plugin owner and canonical
workspace ID; raw IDs are encoded/hashed before becoming path components.
Directories use `0750`, files use `0600`, and writes use lock + temporary file +
atomic rename.

V1 state rules:

- JSON values only; no executable type, command, path grant, or object hook;
- maximum 64 keys per plugin/workspace;
- key length 64 bytes and the same conservative ID grammar as operation IDs;
- maximum 64 KiB encoded value and 256 KiB total encoded state per
  plugin/workspace;
- maximum nesting depth 16, 256 members per object, and 256 elements per array;
- every write carries a positive schema version and an expected revision;
- reads return `{found:false}` for missing and `{found:true,value:null}` for an
  explicitly stored JSON null, preserving missing-versus-empty semantics;
- compare-and-swap revisions prevent lost concurrent writes;
- one plugin cannot enumerate or address another plugin's namespace;
- disable and update preserve bytes; the new plugin version receives the stored
  schema version and must handle or explicitly replace it;
- uninstall explicitly deletes that plugin's namespaced state only after service
  stop/session invalidation and as part of the disclosed uninstall. It is never
  copied into another plugin or a core workspace field;
- surface sessions, confirmations, pending plans, and other transient broker
  records are in memory and never written here.

This boundary avoids arbitrary plugin schema churn in the portable workspace
record, avoids sync-store special cases, and makes legacy REAPER's core pins
structurally impossible to reinterpret as plugin state.

## Blueprint component boundary

A plugin blueprint is a trusted installed component with two parts:

1. a strict, bounded inert descriptor using Ori's existing template vocabulary
   plus qualified plugin capability/provider references; and
2. a bounded skeleton directory of ordinary project files copied as bytes.

The contribution manifest names the blueprint descriptor and a relative
skeleton root. During confirmed install/update, Ori canonicalizes both under the
plugin root, rejects absolute paths, `..`, symlinks, special files, file-count or
byte-limit excess, and validates all references against the same contribution.
The normalized descriptor and skeleton digest become part of the trusted
component fingerprint.

`projecttemplates` receives an injected read-only blueprint/capability/provider
catalog. It does not discover code from template JSON and no longer constructs a
built-in-only registry at normalization time. The creation catalog exposes a
plugin blueprint only while its owner is installed, enabled, platform/protocol
compatible, and fully registered. Disable/uninstall removes it from future
creation without deleting or rewriting workspaces already created from it.

Creation snapshots inert provenance and atomically attaches declared
capabilities. A failure rolls back the new workspace or leaves an explicit
recoverable setup failure; it never reports a normal ready workspace with a
missing required plugin component.

## Surface catalog and station model

One authenticated catalog is derived from:

1. current user and canonical workspace ownership;
2. active installed-plugin registry generation;
3. active capability attachments on that workspace;
4. protocol/platform compatibility;
5. sanitized status from a declared operation or provider.

The public descriptor contains no executable reference. It includes a stable
surface key, bounded label/icon token, requested modal size, generic state,
value, description, availability, checked-at, and host-clamped polling hints.
The generic states are `checking`, `ready`, `attention`, `degraded`,
`unavailable`, and `disabled`. Labels, values, and descriptions are rendered as
text only.

Map station polling is clamped to 5–60 seconds. An open frame may request live
polling clamped to 1–60 seconds. The host allows one request in flight, pauses
when `document.visibilityState === "hidden"`, and stops on close, detach,
session invalidation, update, disable, or uninstall.

Modal width/height requests are hints, clamped to host responsive limits. The
host owns title, close button, `role="dialog"`, accessible name, inert
background, focus entry/trap/restoration, Escape, stacking, responsive layout,
and deep-link eligibility. The plugin owns accessibility inside its frame.

## Frame and asset flow

1. Parent requests the authenticated catalog for `studio_id`.
2. On station/deep-link activation, parent asks the generic session endpoint to
   open the selected qualified surface.
3. Server repeats ownership and eligibility checks, creates a cryptographically
   random expiring session bound to user, workspace, owner, capability, surface,
   and plugin generation, and returns a generic frame URL plus host metadata.
4. Parent registers the modal with `workspace-overlay-coordinator.js`, creates
   an iframe with `sandbox="allow-scripts"`, and records the exact
   `contentWindow`.
5. The generic frame route serves the validated plugin entry document from the
   trusted asset root. Asset routes canonicalize paths, reject absolute/traversal
   and every symlink escape, allowlist MIME types, cap bytes, disable directory
   listing, and key immutable caching by plugin version/digest.
6. Response CSP uses `default-src 'none'`, explicit same-host versioned asset
   URLs for scripts/styles/images/fonts as needed, `connect-src 'none'`,
   `object-src 'none'`, `base-uri 'none'`, `form-action 'none'`, and a fixed
   `frame-ancestors` policy permitting only the Ori host. Because the sandboxed
   document has opaque origin `null`, versioned frame-token asset responses set
   `Access-Control-Allow-Origin: null` without credentials so ES modules can
   load; broker/session routes never set it. Inline executable content requires
   installer-computed hashes/nonces; arbitrary remote origins are never copied
   from a manifest into CSP.
7. SDK and parent complete a challenge handshake. The server session credential
   remains in parent memory and is not sent into the frame. The frame receives a
   local bridge ID only.
8. SDK requests go to the parent. Parent verifies source/challenge/schema, calls
   generic authenticated APIs, bounds the result, and posts a correlated
   response back to the same frame window.
9. Close unregisters the modal, restores focus, stops polling, destroys the
   iframe, and invalidates the server session. Lifecycle invalidation replaces
   or closes an open frame and rejects every later request.

## Broker authorization order

Every UI or agent operation converges on one broker policy function. Rejected
requests stop at the named stage and never call the service:

1. resolve authenticated user;
2. load canonical workspace and verify ownership;
3. resolve installed plugin owner and exact generation;
4. verify plugin enabled and protocol/platform compatibility;
5. verify workspace capability attachment and plugin provenance;
6. for frame calls, verify active session, expiry, surface binding, and local
   parent source-window proof;
7. resolve the declared opaque operation ID from the trusted installed record;
8. bound/decode input and validate the closed JSON schema, rejecting unknown
   fields where declared;
9. inject authoritative workspace ID, canonical project root/entry,
   plugin-data namespace, provider identity, and host-resolved symbolic scopes;
10. verify selected runtime mode, healthy provider where required, and exact
    per-agent grant for agent calls;
11. verify a host-issued confirmation token for confirmation-required policy;
12. apply operation concurrency/rate and timeout class;
13. lazily start/probe the service if needed and call the trusted binding;
14. validate/bound output, sanitize errors, and project only the declared public
    result.

Browser input can name only the operation and schema fields. It cannot provide a
command, MCP tool, path, endpoint, URL, workspace override, agent grant, scope,
service method, or confirmation boolean.

## Confirmation model

Policy classes are `read_only`, `reversible`, and `confirmation_required`.
`read_only` and `reversible` may execute immediately; reversible results may
carry a declared undo intent. `confirmation_required` uses a host-owned flow:

1. The initial normalized invocation returns `confirmation_required` without a
   service call.
2. Ori computes a canonical payload digest and renders a host dialog naming the
   plugin, workspace, operation, effect summary, and normalized payload.
3. On approval, Ori issues a cryptographically random, single-use token bound to
   user, workspace, plugin generation, capability, surface/caller, operation,
   canonical payload digest, and a two-minute expiry.
4. The parent/agent adapter retries the exact invocation with the token.
5. Any replay, expiry, changed payload, changed generation, different surface,
   workspace, user, or operation fails before service call. Cancellation issues
   no token. A client `confirmed: true` field is unknown input and is rejected.
6. The token is consumed before dispatch. A service crash does not make it
   reusable; the user confirms a new attempt.

Agent operations use the same policy function. A confirmation-required agent
mutation becomes a host review/plan card; the model never receives or mints a
confirmation token.

This policy aligns honest UI and agent behavior. It does not sandbox dishonest
native code, which remains part of installation trust.

## Host intents

The v1 frame bridge supports only:

- readiness handshake;
- declared operation invocation;
- status-changed notification;
- host confirmation request/result;
- namespaced state get/set/delete;
- Ask Ori;
- Open Setup;
- Close Surface.

Ask Ori uses capability requirements registered on the trusted surface. Frame
input may add bounded context text, marked as untrusted plugin-provided context,
but cannot add required capabilities, grants, tools, system instructions, or an
agent identity. Open Setup uses registered provider/repair targets and cannot
name a route. Close Surface is advisory to the parent, which performs actual
session invalidation and modal cleanup.

## Service lifecycle

The process manager has these logical states:

```text
absent
  -> installed_disabled
  -> enabled_idle
  -> starting
  -> ready
  -> degraded
  -> stopping
  -> enabled_idle | installed_disabled | absent

ready --unexpected exit--> degraded --one demand-triggered restart--> starting
ready --update/disable/uninstall--> stopping (sessions invalid first)
```

Rules:

- install does not start the service;
- first eligible status, frame operation, provider check, or agent operation
  starts it lazily;
- one process is shared by an installed plugin service unless the manifest
  explicitly declares a supported isolation unit in a future protocol;
- calls carry authoritative workspace context and remain isolated by broker
  authorization even when the process is shared;
- startup, each call, and stop are bounded; request cancellation propagates;
- concurrency is bounded and excess work is rejected/queued within a fixed
  limit, never unbounded;
- unexpected exit marks every dependent surface degraded and invalidates
  in-flight calls;
- at most one automatic restart is allowed for a demand episode; repeated crash
  stays unavailable until explicit retry or lifecycle change;
- disable/update/uninstall invalidates sessions, blocks new calls, waits for
  bounded calls, stops the process, unregisters contributions, and only then
  changes component files/records;
- shutdown stops all running services before the server exits;
- an unneeded plugin leaves no background process.

V1 limits are 10 seconds startup, 3/15/60 second fast/normal/long calls,
5 seconds stop, and one demand-triggered restart. The selected transport is MCP
stdio behind the dedicated service manager described here. A browser-accessible
plugin server is not an alternative.

## Transport benchmark and decision

Task 1.4 exercised Ori's real `internal/mcp.Registry`, `mcp.Server`, SDK
`CommandTransport`, and a hermetic child MCP process in
`internal/mcp/testdata/workspacesurfacefake`. The retained characterization is
`TestWorkspaceSurfaceMCPTransportSpike`; the hot-call benchmark is
`BenchmarkWorkspaceSurfaceMCPStatusCall`.

Measured on macOS arm64 (Apple M5), with five one-second samples and 12
concurrent 100 ms calls:

| Observation | Measured result |
| --- | --- |
| First process start + initialize + tool discovery | 201–325 ms across retained runs |
| One-second cadence call median / p95 | 1.21–1.48 ms / 1.40–1.61 ms |
| 12 concurrent 100 ms calls | 103.6–104.1 ms total; max active 12 |
| Caller deadline propagation | 75.6 ms for a 75 ms deadline; service observed cancellation |
| Background 200 ms call | 201.4–201.5 ms, proving no implicit per-call timeout |
| Orderly stop | 1.65–2.49 ms |
| Start after stopped/crashed cleanup | 14.8–16.9 ms |
| Hot status benchmark, 100 calls × 3 | 130–305 µs/op, about 141 KiB and 79–80 allocations/op |

The measurements establish:

- the MCP session easily sustains one-second status reads;
- independent UI/agent calls can execute concurrently and request cancellation
  reaches the service;
- the current registry does not lazy-start and does not bound concurrency or
  impose per-call timeouts;
- crash detection relies on the five-minute default health interval unless a
  call fails or a shorter loop is configured;
- there is no automatic restart budget; and
- `Registry.RestartServer` after exit reports the child `exit status 23` while
  closing the broken session. A separate start succeeds after cleanup, but raw
  process detail must not reach a caller.

**Decision:** use MCP stdio rather than inventing a second framed RPC. Its
latency, concurrency, cancellation, typed tool schemas, and process/session
reuse satisfy live control with large margin. The Workspace Surface service
manager must not treat the existing Registry as a complete lifecycle policy. It
adds lazy start, semaphore bounds, timeout classes, immediate exit/call-failure
detection, sanitized diagnostics, and one restart budget; after a crash it
reconstructs/starts the MCP server rather than returning the raw Restart error.
The service's declared operations are called internally and are not registered
as unrestricted agent MCP tools.

The fixed 10-second startup and 5-second stop limits are over thirty times the
slowest observed first startup and over one thousand times the observed orderly stop,
leaving packaging/host load margin while remaining bounded. Fast operations get
3 seconds, normal operations 15 seconds, and explicit long operations 60
seconds. One-second polling uses only fast read-only operations.

## Runtime, setup, scopes, and agent tools

A contribution may bind declared operations to generic provider roles such as
`prerequisites`, `readiness`, `live_status`, `verify`, and `repair`. The generic
proxy resolves those bindings only from the installed registry and sends
canonical context through the broker.

Manifests request symbolic scopes, not paths or endpoints. V1 host resolvers map
an allowlisted symbol to canonical workspace/plugin-owned resources, for
example `workspace_project_read`, `workspace_project_write`, or
`plugin_data_write`. Domain-named endpoints and exchange roots are not part of
the host vocabulary. Unknown symbols fail installation. Resolution rechecks canonical roots and symlinks at
call/grant time. Service responses cannot broaden scope.

Ori continues to own:

- operating mode selection and disclosures;
- setup step rendering, ordering, progress, retry, cancellation, and summary;
- persisted grants and exact agent identity;
- final writable roots/local-network posture;
- task preflight before model/tool execution;
- confirmation and output limits.

The service is not automatically exposed as an unrestricted MCP server. Agent
operations are an allowlisted adapter over individual declared broker
operations.

## Error and text handling

The broker returns stable generic codes such as:

- `surface_unavailable`, `plugin_disabled`, `plugin_incompatible`,
  `platform_unsupported`, `capability_not_attached`;
- `session_unknown`, `session_expired`, `session_invalidated`,
  `frame_source_mismatch`;
- `operation_unknown`, `input_too_large`, `input_invalid`,
  `confirmation_required`, `confirmation_invalid`;
- `runtime_grant_required`, `scope_unavailable`, `service_start_failed`,
  `service_timeout`, `service_unavailable`, `output_invalid`,
  `output_too_large`, `state_quota_exceeded`.

Browser and agent responses never contain service command/args/environment,
artifact or asset roots, project paths, plugin-data paths, endpoint/port,
stderr, stack traces, panic text, credentials, or raw service errors. Logs may
carry an internal correlation ID and redacted category; user responses carry
only the stable code and host-owned message. Plugin-authored status/help text is
UTF-8 validated, length bounded, stripped of control characters, and rendered
as text rather than HTML.

The exact generic missing-provider projection is:

- **Code:** `provider_unavailable`
- **Title:** `Capability provider unavailable`
- **Summary:** `This workspace's capability provider is not available. Install or enable a compatible plugin, then check setup again.`
- **Action label:** `Review plugins`

This copy is used for missing, disabled, unregistered, or retired providers when
no more specific safe compatibility result applies. It deliberately contains no
REAPER/plugin-name branch and is the legacy REAPER outcome after core removal.

## Compatibility strategy

1. Ori v1 supports surface protocol integer `1`.
2. An Ori manifest declares inclusive `protocol.min` and optional
   `protocol.max`. Omitted max equals min. Activation requires
   `min <= 1 <= max`; otherwise the contribution is inspectable but unavailable.
3. Browser SDK messages and service requests carry protocol version `1`, a
   bounded request ID, and one typed request/result. Unknown versions, message
   types, fields, and result variants fail closed.
4. The service handshake reports plugin name/version, service protocol, and
   implementation version. Identity/protocol mismatch fails before an operation.
5. Platform artifacts are selected by exact normalized OS/architecture tuple.
   No match means inspectable unavailable state and no process launch.
6. Plugin update creates a new generation, invalidates sessions/confirmations,
   stops the old service, verifies/stages the new artifact, and atomically swaps
   contribution registration. Failure restores the old trusted record/files and
   does not claim the update succeeded.
7. UI assets, blueprints, service artifacts, operation schemas/policies, and
   symbolic scopes are all part of the trusted-component fingerprint. Any change
   requires update preview and confirmation.
8. Existing MCP/skill-only plugins have no Ori manifest and retain current
   behavior.
9. Ori host support lands before a released plugin requires it. Cross-repository
   fixtures use protocol version plus canonical valid/invalid vectors.
10. A semantic change to authorization, confirmation, persisted meaning,
    sandbox privileges, operation policy, or service lifecycle requires a new
    protocol version. Additive host implementation details that preserve v1 wire
    meaning do not.

## REAPER extraction and legacy behavior

The external plugin ports behavior from Ori core, not from its smaller current
CLI implementation. It retains the existing global script library and runner
exchange where compatible, but UI and agent responses remain redacted. New
plugin-backed workspaces receive the plugin blueprint/capability and full parity.

Existing REAPER workspaces are not migration candidates. Ori does not attach the
plugin, import `PinnedReaperScripts`, copy grants/setup/provenance, or rewrite
workspace/project files. After core retirement, their compiled provider and
specific routes are absent; the generic `provider_unavailable` result above is
the only compatibility behavior. Manually attaching the new capability creates
fresh namespaced state and plugin defaults.

The compiled packages, routes, UI modules, template, and persistence fields were
deleted after all 47 rows in
`tasks/reaper-parity-matrix-plugin-workspace-surfaces.md` gained equivalent
plugin/generic-host tests and generated-artifact live evidence.

## Rejected alternatives

- **Import plugin JavaScript into Ori's page:** rejected; it shares cookies, DOM,
  globals, and CSS and cannot be bounded by host modal policy.
- **Plugin-defined Ori routes or browser-to-MCP/service access:** rejected;
  authorization, confirmation, schemas, and redaction would drift per plugin.
- **Workspace-persisted commands/assets/provider implementations:** rejected;
  importing a workspace would become code installation.
- **Plugin state in arbitrary workspace fields:** rejected; it breaks namespace,
  quota, sync, and legacy no-import guarantees.
- **Blueprints as ordinary untrusted template folders discovered at runtime:**
  rejected; install trust and provider resolution would be bypassed.
- **Treat native service as sandboxed because UI is framed:** rejected; these are
  separate trust boundaries.
- **Use REAPER conditionals in generic host code:** rejected; the non-REAPER
  example must work without host edits and final source audit forbids them.
- **Automatic legacy REAPER migration:** rejected by product scope and because
  pin/grant/setup semantics cannot be safely inferred.

## Prototype review outcome and v1 freeze

The Group 1 prototype was re-read against this decision before the v1 freeze.
Observed implementation seams are retained in production packages rather than
throwaway routes:

- `internal/workspacesurface.Registry` atomically pairs inert owner-qualified
  descriptors with separately held trusted bindings; returned values are
  defensive copies and exact-generation unregister prevents stale cleanup;
- `internal/workspacesurfacehttp.Handler` exposes only five generic route
  patterns for catalog, session open/close, versioned frame assets, and broker
  operations; no plugin route is registered;
- catalog projection omits asset roots, entry paths, operation schemas/IDs, and
  canonical workspace paths;
- a server session uses separate random parent and frame tokens. The parent
  credential authorizes broker requests; the frame token authorizes only
  bounded assets and cannot invoke a service;
- the browser host sets `sandbox="allow-scripts"` without same-origin, adds the
  `credentialless` attribute as defense in depth, owns the accessible modal,
  and keeps the broker credential out of frame messages;
- parent/frame handshake uses exact `contentWindow`, a random challenge, closed
  envelopes, 64 KiB/depth limits, and no origin-string assumption for the
  opaque frame;
- MCP stdio measurements selected the transport and fixed lifecycle limits as
  recorded above;
- retained tests reject unsupported protocol/platform, owner/component
  collisions, foreign/unattached workspaces, source-window mismatch, expired
  sessions, malformed/oversized input, unknown operations, unsafe/symlink
  assets, timeout, crash detail, and stale lifecycle state before unintended
  dispatch.

The observed prototype required no change to the manifest merge, managed state,
blueprint, confirmation, or legacy no-migration decisions. It clarified that
versioned relative frame subresources share the random frame-token path. Opaque
origins require `Access-Control-Allow-Origin: null` for ES-module subresources;
that header applies only to unguessable, asset-only frame-token URLs, never the
broker, and no credentialed CORS is allowed. It also confirmed that
`credentialless` is additive: security still relies on opaque origin, CSP,
`connect-src 'none'`, no direct broker credential, and exact parent-window
messages because browser support for that attribute is not universal.

**Protocol v1 is now frozen.** Changes to identity merge, persisted meaning,
route authority, sandbox flags, handshake/envelope semantics, operation policy,
confirmation binding, symbolic scopes, state missing/empty semantics, or
service transport require a coordinated protocol change. Later groups complete
the frozen contract; they do not silently reinterpret it.
