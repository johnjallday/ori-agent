# Workspace Surface Protocol v1 Contract

**Status:** Frozen Workspace Surface protocol v1 contract

**Protocol version:** `1`

**Architecture:** `docs/architecture/plugin-workspace-surfaces.md`

**Canonical vectors:** `internal/plugin/testdata/workspace-surface-v1/`

This document is normative for Workspace Surface protocol v1. Task 1.4 selected
`mcp_stdio` from measured process behavior and fixed the timeout classes below;
Task 1.9 re-read the observed prototype and froze the contract. Semantic changes
require a new coordinated protocol revision across this document, vectors, host,
SDK, and plugin.

## Conformance vocabulary

- **MUST/MUST NOT** are security or compatibility requirements.
- **Host-only** fields may exist in the trusted installed-plugin registry or a
  host-to-service request but MUST NOT appear in a workspace, surface catalog,
  frame message, or browser/agent result.
- JSON objects are closed unless a schema explicitly sets
  `additionalProperties: true`. Contract objects never do.
- IDs are ASCII, lower case, 1–64 bytes, and match:
  `^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`.
- Request IDs are 1–64 printable ASCII bytes and carry correlation only, never
  authority.
- All text MUST be valid UTF-8 and contain no C0/C1 control characters other
  than ordinary JSON whitespace before normalization.

## Global limits

| Value | Limit |
| --- | ---: |
| Manifest bytes | 1 MiB |
| Capabilities per plugin | 16 |
| Surfaces per capability | 8 |
| Services per plugin | 8 |
| Operations per service | 128 |
| Artifacts per service | 16 |
| Blueprints per plugin | 16 |
| Label | 120 UTF-8 bytes |
| Description/summary/status detail | 500 UTF-8 bytes |
| Station value | 160 UTF-8 bytes |
| Frame/bridge encoded message | 64 KiB |
| Operation input | 64 KiB unless lower schema/operation limit |
| Operation output | 256 KiB maximum; declaration may only lower it |
| JSON nesting | 16 |
| Object members | 256 |
| Array elements | 256 |
| Asset file | 8 MiB |
| Total UI assets per plugin | 32 MiB |
| Blueprint files | 512 |
| Blueprint total bytes | 64 MiB |
| State keys per plugin/workspace | 64 |
| State value | 64 KiB |
| State namespace | 256 KiB |

The host rejects over-limit input before service dispatch. It never truncates a
schema-bearing value into a different valid value.

## Contribution manifest

The optional contribution is `<plugin-root>/.ori-plugin/plugin.json`.

### Top-level shape

```json
{
  "schema_version": 1,
  "name": "workspace-surface-demo",
  "version": "0.1.0",
  "protocol": { "min": 1, "max": 1 },
  "capabilities": [],
  "services": [],
  "blueprints": []
}
```

Fields:

- `schema_version` MUST equal `1`.
- `name` and `version` MUST be non-empty and match every present Claude/Codex
  base manifest according to the architecture merge rule.
- `protocol.min` and `protocol.max` are inclusive positive integers; `max`
  defaults to `min` and MUST NOT be less than `min`.
- component arrays default to empty but at least one Ori component MUST be
  present when the file exists.
- component IDs MUST be unique within their owner scope. References MUST resolve
  within the same contribution.
- unknown top-level or nested fields are errors.

### Capability

```json
{
  "id": "demo-tools",
  "version": 1,
  "display": {
    "name": "Surface Demo",
    "description": "A harmless Workspace Surface example."
  },
  "service_id": "demo-service",
  "surfaces": [],
  "runtime_provider": null,
  "agent_operations": []
}
```

- `version` is a positive plugin-owned definition version.
- `display` is bounded untrusted text.
- `service_id` is optional only when every surface is asset/state/host-intent
  only. If present it MUST resolve to a service in this contribution.
- `surfaces` contains zero or more surface descriptors.
- `runtime_provider` is optional and binds host roles to declared operations.
- `agent_operations` is an allowlist of operation IDs that the generic agent
  adapter may expose. It does not expose the whole service.

### Surface

```json
{
  "id": "main",
  "label": "Surface Demo",
  "description": "Open the harmless demo surface.",
  "icon": { "kind": "host", "value": "puzzle" },
  "placement": "map_modal",
  "entry_asset": "ui/index.html",
  "modal": { "width": 720, "height": 560 },
  "status_operation": "status.read",
  "operations": ["status.read", "greeting.create"],
  "polling": { "map_seconds": 5, "open_seconds": 1 },
  "host_intents": {
    "ask_ori": { "required_capabilities": [] },
    "open_setup": { "provider_id": "" },
    "confirmation": true,
    "state": true,
    "close": true
  }
}
```

- `placement` MUST be `map_modal` in v1.
- `entry_asset` is a slash-separated relative path beneath the trusted asset
  root. It MUST NOT be absolute, empty, contain `.`/`..`, backslashes, percent-
  encoded separators, NUL, or resolve through a symlink.
- `icon.kind` is `host` with an allowlisted token or `asset` with a safe SVG/PNG
  relative path. SVG is served as an image, never injected as markup.
- requested modal width is 320–1600 and height is 240–1200; the host applies
  stricter responsive clamps.
- each operation and the status operation MUST resolve on the capability's
  service and appear in `operations`.
- map polling requests are 5–60 seconds; open polling requests are 1–60 seconds.
- Ask Ori requirements and Setup provider are trusted fixed references. A frame
  request cannot modify them.

### Station status operation

A declared status operation MUST be `read_only`, use an empty closed-object
input schema, and return:

```json
{
  "state": "ready",
  "value": "Available",
  "description": "The demo service is ready.",
  "checked_at": "2026-08-24T12:00:00Z"
}
```

`state` is one of `checking`, `ready`, `attention`, `degraded`, `unavailable`,
or `disabled`. The host bounds and text-renders `value` and `description`.
`checked_at` is RFC 3339; the host may replace invalid or future-skewed values
with its own check time.

### Service

```json
{
  "id": "demo-service",
  "transport": "mcp_stdio",
  "protocol_version": 1,
  "entrypoint": { "artifact_id": "demo-darwin-arm64", "args": ["serve"] },
  "artifacts": [],
  "operations": []
}
```

- `transport` MUST be `mcp_stdio` in v1. It never permits HTTP/browser
  transport. A future transport is a protocol change.
- `protocol_version` MUST equal `1`.
- `entrypoint.artifact_id` MUST resolve to exactly one artifact for each
  supported platform. Arguments are fixed strings, bounded to 32 entries and
  256 bytes each. Shell metacharacters have no special meaning because Ori
  executes an argument vector without a shell.
- entrypoint and artifact fields are host-only after installation.

### Artifact

```json
{
  "id": "demo-darwin-arm64",
  "os": "darwin",
  "arch": "arm64",
  "source": {
    "kind": "bundled",
    "path": "artifacts/demo-service-darwin-arm64"
  },
  "sha256": "<64 lower-case hex characters>",
  "size": 123456
}
```

- `(os, arch)` MUST be unique within a service.
- `source.kind` is `bundled` with a safe relative path or `https` with an HTTPS
  URL. Redirects must remain HTTPS and are bounded. Other schemes are invalid.
- `sha256` is mandatory for every executable artifact.
- `size` is mandatory, positive, and no greater than the installer limit. The
  streamed byte count and digest must exactly match before executable mode.
- No build/install script field exists.

### Operation

```json
{
  "id": "greeting.create",
  "input_schema": {
    "type": "object",
    "properties": {
      "name": { "type": "string", "minLength": 1, "maxLength": 80 }
    },
    "required": ["name"],
    "additionalProperties": false
  },
  "output_schema": {
    "type": "object",
    "properties": {
      "message": { "type": "string", "maxLength": 160 }
    },
    "required": ["message"],
    "additionalProperties": false
  },
  "max_output_bytes": 4096,
  "timeout_class": "fast",
  "policy": "read_only",
  "scopes": []
}
```

- operation IDs are opaque to browser callers. They are not commands, paths,
  MCP tool names, or URLs.
- `timeout_class` is `fast`, `normal`, or `long`, with fixed host maximums of
  3, 15, and 60 seconds respectively.
- `policy` is `read_only`, `reversible`, or `confirmation_required`.
- `scopes` contains only host-known symbolic scope IDs. Raw paths, hostnames,
  ports, URLs, or environment keys are invalid.
- input/output schemas use the closed v1 JSON Schema subset below.
- `max_output_bytes` is 1–262144.

### Runtime provider

```json
{
  "id": "demo-runtime",
  "requirement_key": "demo_runtime",
  "operations": {
    "prerequisites": "runtime.prerequisites",
    "readiness": "runtime.readiness",
    "live_status": "runtime.live_status",
    "verify": "runtime.verify",
    "repair": "runtime.repair"
  },
  "scopes": ["plugin_data_write"]
}
```

Provider IDs and requirement keys are inert references in blueprint/workspace
records. Operation bindings and scopes are host-only installed-registry data.
Required roles must be `read_only` except `verify`/`repair`, whose confirmation
and mutation policy is explicitly declared by their operation. A provider
cannot mint grants or return raw scopes.

### Blueprint

```json
{
  "id": "demo-workspace",
  "version": 1,
  "manifest": "blueprints/demo-workspace/template.json",
  "skeleton": "blueprints/demo-workspace/project",
  "capabilities": ["demo-tools"]
}
```

All paths are safe relative plugin paths. At install time the host loads the
strict inert template descriptor, verifies every capability/provider/skill
reference belongs to the contribution or another already trusted generic host
component, inventories regular non-symlink skeleton files, and fingerprints
all bytes. Blueprint data cannot name a service command, artifact, operation
implementation, route, module, or raw scope/path.

## JSON Schema subset

V1 operation schemas allow only:

- keywords: `$schema`, `type`, `properties`, `required`,
  `additionalProperties`, `items`, `enum`, `const`, `minLength`, `maxLength`,
  `minimum`, `maximum`, `minItems`, `maxItems`, `description`;
- types: `object`, `array`, `string`, `integer`, `number`, `boolean`, `null`;
- a type may be one string or an array containing `null` plus one other type;
- object schemas MUST explicitly set `additionalProperties: false`;
- `required` entries MUST name declared properties and contain no duplicates;
- array schemas MUST declare one `items` schema and a bounded `maxItems`;
- string schemas MUST declare bounded `maxLength` no greater than 32768;
- recursive `$ref`, pattern/format evaluation, conditional schemas, external
  references, content encodings, defaults, and unknown keywords are invalid.

The host validates schema structure at install and values before/after service
calls. Service output is not trusted merely because the service is installed.

## Inert workspace projection

A plugin-backed installed capability may project:

```json
{
  "id": "demo-tools",
  "version": 1,
  "installed_at": "2026-08-24T12:00:00Z",
  "source": "plugin-blueprint",
  "owner": {
    "kind": "plugin",
    "plugin_id": "workspace-surface-demo",
    "plugin_version": "0.1.0"
  }
}
```

Allowed fields are the normalized identity/provenance/version/timestamp and
inert resource IDs already defined by the workspace model. The following names
are forbidden anywhere in the workspace projection, including unknown nested
objects: `command`, `args`, `env`, `path`, `asset`, `entry_asset`, `artifact`,
`sha256`, `route`, `url`, `endpoint`, `port`, `module`, `script`, `operation`,
`method`, `scope`, and `mcp_tool`.

The authoritative service/artifact/operation/asset mapping exists only in the
installed-plugin registry. A workspace projection with an unknown owner or
capability remains visible and unavailable; it never activates code.

## Public surface catalog

One item has this closed shape:

```json
{
  "key": "plugin:workspace-surface-demo:demo-tools:main",
  "plugin": {
    "id": "workspace-surface-demo",
    "version": "0.1.0",
    "generation": "7"
  },
  "capability_id": "demo-tools",
  "surface_id": "main",
  "label": "Surface Demo",
  "description": "Open the harmless demo surface.",
  "icon": { "kind": "host", "value": "puzzle" },
  "placement": "map_modal",
  "modal": { "width": 720, "height": 560 },
  "status": {
    "state": "ready",
    "value": "Available",
    "description": "The demo service is ready.",
    "checked_at": "2026-08-24T12:00:00Z"
  },
  "available": true,
  "unavailable_code": "",
  "polling": { "map_seconds": 5, "open_seconds": 1 }
}
```

Catalog generation is a stale-session fence, not a command or secret. No asset
path, service identity/command, operation schema, platform artifact, endpoint,
raw scope, or workspace filesystem path is public.

## Browser bridge

### Envelope

Every message is a plain JSON-compatible object:

```json
{
  "protocol_version": 1,
  "bridge_id": "opaque-parent-local-id",
  "type": "ori.surface.operation.invoke",
  "request_id": "req-1",
  "payload": {}
}
```

Unknown fields/types/version, non-object data, transferables, over-limit values,
or wrong source/challenge are ignored or receive a bounded protocol error. The
server session credential is never in this envelope.

### Handshake

1. Parent creates a random bridge ID and 256-bit challenge after recording the
   exact frame `contentWindow`.
2. Parent → frame `ori.surface.challenge` payload:
   `{ "challenge": "<base64url>", "surface": {"plugin_id":"...","capability_id":"...","surface_id":"..."} }`.
3. Frame → parent `ori.surface.ready` echoes `challenge` and supplies
   `{ "sdk_version":"1.x", "supported_protocols":[1] }`.
4. Parent verifies source, bridge ID, exact challenge, and protocol, consumes the
   challenge, then sends `ori.surface.init` with bounded public catalog metadata
   and feature booleans. It does not send auth/session credentials, paths,
   endpoints, grants, or operation implementation metadata.
5. Requests before successful init fail with `bridge_not_ready` and are not
   forwarded.

### Frame request types

- `ori.surface.operation.invoke` — `{operation_id, input}`.
- `ori.surface.state.get` — `{key}`.
- `ori.surface.state.set` — `{key, schema_version, expected_revision, value}`.
- `ori.surface.state.delete` — `{key, expected_revision}`.
- `ori.surface.host.confirm` — `{confirmation_id}` only for a broker-projected
  pending confirmation; arbitrary effect/payload text is not accepted.
- `ori.surface.host.ask_ori` — `{context}` bounded to 2000 UTF-8 bytes.
- `ori.surface.host.open_setup` — `{}`.
- `ori.surface.host.close` — `{}`.
- `ori.surface.status_changed` — `{}`; it asks the host to refresh the declared
  status operation and carries no claimed status.

### Parent response/event types

- `ori.surface.response` — `{ok:true,result}` or
  `{ok:false,error:{code,message,confirmation_id?}}`, correlated by request ID.
- `ori.surface.host.visibility` — `{visible}`.
- `ori.surface.host.invalidated` — `{code,message}` followed by frame teardown.

The host owns the actual confirmation, Ask Ori, Setup, and close flows. The SDK
exposes promises/events but does not reproduce authorization logic.

## Surface session

The authenticated open response contains a cryptographically random server
session credential for parent memory and a generic frame URL. Server records
bind:

- user ID;
- workspace ID;
- plugin ID/version/generation;
- capability and surface IDs;
- created, idle-expiry, and absolute-expiry times;
- invalidation state.

The parent additionally binds the record to the exact frame window and bridge
challenge. Session idle expiry is 15 minutes and absolute expiry is 8 hours for
the prototype; eligible status/operation traffic may refresh idle expiry but
never absolute expiry. Close, detach, generation change, disable, update,
uninstall, and relevant service restart invalidate immediately.

## Broker operation request

The parent sends an authenticated generic request equivalent to:

```json
{
  "session": "<parent-only-random-credential>",
  "operation_id": "greeting.create",
  "input": { "name": "Ori" },
  "confirmation_token": ""
}
```

The route determines user from Ori authentication and workspace/plugin/surface
from the session. No request field can override them. Agent calls use an
internal caller descriptor rather than a frame session but enter the same
operation policy after ownership/capability checks.

## Host-to-service protocol

The logical service protocol is carried over the selected MCP stdio session.
Each declared operation is an internal MCP tool binding; the host validates the
installed descriptor before calling it and does not expose the service as an
unrestricted agent MCP server.

### Initialize

Host request:

```json
{
  "protocol_version": 1,
  "request_id": "init-1",
  "type": "service.initialize",
  "payload": {
    "plugin_id": "workspace-surface-demo",
    "plugin_version": "0.1.0",
    "service_id": "demo-service"
  }
}
```

Service result MUST echo compatible identity/protocol and list no operations
outside the installed descriptor. The host descriptor remains authoritative if
lists disagree; mismatch fails startup.

### Operation call

```json
{
  "protocol_version": 1,
  "request_id": "call-1",
  "type": "service.operation.call",
  "payload": {
    "operation_id": "greeting.create",
    "context": {
      "workspace_id": "canonical-workspace-id",
      "workspace_root": "<host-only canonical path>",
      "project_entry": "<host-only canonical path or empty>",
      "plugin_data_root": "<host-only canonical path>",
      "scopes": [
        { "id": "plugin_data_write", "roots": ["<host-only canonical path>"] }
      ]
    },
    "input": { "name": "Ori" }
  }
}
```

Context is assembled only after ownership/grant/scope checks. The service MUST
not return it to the browser. Each response is either:

```json
{
  "protocol_version": 1,
  "request_id": "call-1",
  "type": "service.operation.result",
  "payload": { "output": { "message": "Hello, Ori." } }
}
```

or a bounded typed failure:

```json
{
  "protocol_version": 1,
  "request_id": "call-1",
  "type": "service.operation.error",
  "payload": { "code": "domain_unavailable", "user_message": "The demo is unavailable." }
}
```

The host allowlists/maps error codes and re-sanitizes `user_message`. Unknown
fields/results, wrong IDs, over-limit output, raw stderr, and malformed frames
become generic `service_unavailable`/`output_invalid` responses.

## Confirmation token

A pending confirmation record binds:

```text
random token hash
user ID
workspace ID
plugin ID + generation
capability ID
surface ID or agent caller ID
operation ID
SHA-256(canonical JSON input)
created/expiry (maximum 2 minutes)
unused flag
```

The raw token is shown only to the authenticated parent/host flow, never to
plugin frame JavaScript or a model. Consumption occurs before service dispatch.
Retry, changed payload, cross-workspace/surface/user/operation, generation
change, cancellation, expiry, and client `confirmed` fields all fail before a
service call.

## Namespaced state wire result

Get returns:

```json
{
  "found": true,
  "schema_version": 1,
  "revision": "4",
  "value": null
}
```

Missing returns `{ "found": false, "revision": "0" }` with no `value` field.
Set/delete require the exact current revision (`"0"` for create). Conflicts
return `state_conflict`; quota failures return `state_quota_exceeded`. The frame
never provides plugin/workspace namespace IDs.

## Sanitized errors

All public errors have `{code,message}` plus an optional opaque confirmation ID.
Messages are host-owned except allowlisted, sanitized plugin status/help text.
Public errors MUST NOT include command, args, environment, artifact/asset/project
or state paths, endpoints/ports, local usernames, stack traces, stderr, panic
text, credentials, service request context, or raw schema diagnostics.

The canonical generic provider absence is:

```json
{
  "code": "provider_unavailable",
  "title": "Capability provider unavailable",
  "message": "This workspace's capability provider is not available. Install or enable a compatible plugin, then check setup again.",
  "action": { "code": "review_plugins", "label": "Review plugins" }
}
```

## Compatibility failures

- Manifest protocol range excludes v1 → inspectable `protocol_incompatible`; no
  registration or service start.
- No exact OS/architecture artifact → inspectable `platform_unsupported`; no
  download/launch.
- Base/Ori name or version mismatch → install validation failure; no partial Ori
  contribution.
- Service handshake identity/protocol mismatch → `service_start_failed`; no
  operation.
- Unknown bridge/service message or field → `protocol_message_invalid`; no
  dispatch.
- Workspace owner/capability record cannot resolve current installed owner →
  inert `provider_unavailable`; no fallback to a similarly named plugin.
- New plugin generation invalidates old frame sessions and confirmations rather
  than negotiating in place.

## Canonical vectors

`internal/plugin/testdata/workspace-surface-v1/` contains:

- `valid-identities.json` — matching Claude, Codex, and Ori identity;
- `valid-contribution.json` — complete non-REAPER contribution;
- `valid-workspace-projection.json` — executable-free persisted projection;
- `valid-catalog.json` — sanitized public station descriptor;
- `valid-bridge-transcript.json` — challenge, init, operation, state, Ask Ori,
  Setup, status-changed, and close envelopes;
- `valid-service-transcript.json` — initialize/call/result/error shapes;
- `invalid-vectors.json` — identity, protocol, platform, collision, unsafe path,
  schema, message, operation, payload, projection, and redaction failures.

Tests MUST consume these canonical bytes rather than independently rewriting
lookalike fixtures in Ori and `reaper-plugin`.
