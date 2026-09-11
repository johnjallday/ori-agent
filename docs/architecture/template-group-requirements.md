# Template Group Requirements v1

Status: accepted contract; host and coordinated plugin implementation in progress

## Purpose

A project template may state whether a newly created project is standalone or
belongs in the exact Assistant Program Home derived from its trusted source.
Users may create source-linked template variants that change that placement
policy without editing installed plugin files.

This contract is domain-neutral. Ori does not special-case REAPER, `.rpp`
files, Music Production, or any concrete Assistant Program, role, capability,
or group name.

An ordinary workspace group and an Assistant Program Home are different:

- `ParentID` and the physical `sub-workspaces/` layout provide organization.
- Assistant Program membership requires the exact owner/program key, a stable
  project link, reciprocal Home membership, and (for policy-aware projects) a
  matching immutable template snapshot.
- Parentage grants no filesystem, runtime, agent, capability, portfolio, or
  sibling authority.

## Manifest declarations

### Group requirement

`template.json` may contain one optional, isolated `group_requirement` object.
Its maximum encoded size is 4 KiB. It is decoded with unknown-field and
trailing-data rejection and normalized all-or-nothing.

```json
{
  "group_requirement": {
    "schema_version": 1,
    "policy": "required",
    "assistant_program_id": "music-producer-assistant",
    "missing_home": "offer_create",
    "default_home_name": "Music Production Home"
  }
}
```

The v1 fields are:

| Field | Rule |
| --- | --- |
| `schema_version` | Required integer, exactly `1`. |
| `policy` | Required: `none`, `recommended`, or `required`. |
| `assistant_program_id` | Required for grouped policies; lower-case stable ID matching the effective Assistant Program. |
| `missing_home` | Required for grouped policies: `offer_create` or `existing_only`. |
| `default_home_name` | Required only for `offer_create`; display text, never identity. |

Omitted or JSON `null` means **legacy absence**, not None. Explicit None is a
durable reviewed declaration:

```json
{
  "group_requirement": {
    "schema_version": 1,
    "policy": "none"
  }
}
```

None forbids all target fields. Recommended and Required require a matching
Assistant Program and missing-Home policy. `offer_create` requires a name;
`existing_only` forbids one. IDs use the existing 64-byte lower-case stable-ID
grammar. Home display names are trimmed, valid UTF-8, 1–120 bytes, and reject
control characters, path separators, and protocol-like text.

V1 cannot declare an owner, plugin, workspace/Home ID, arbitrary target type,
path, route, URL, command, action, role, grant, permission, or executable hook.
The server derives all identity from authenticated and trusted state.

### Standalone composition

A program-bearing source that permits a Home-free variant declares the separate
`standalone_composition` schema. It does not reinterpret Assistant Program v2.

```json
{
  "standalone_composition": {
    "schema_version": 1,
    "project_roles": [
      {
        "role_id": "producer",
        "system_prompt": "You are the Producer for only this workspace and project."
      }
    ]
  }
}
```

The block is at most 64 KiB and strict at every level. It requires:

- a valid Assistant Program v2 and project connection;
- exactly one adaptation for every project-scoped role, with no Home/foreign or
  duplicate role IDs;
- a complete, plain-text prompt of at most 8 KiB for each project role;
- no ordinary source `agents` competing with transformed program roles;
- at least one constructible new/existing project connection mode; and
- when runtime requirements exist, at least one mode without live requirements.

For standalone creation, the host removes the Assistant Program, Home roles,
stages, reflection/portfolio/learning behavior, project link, and pre-workspace
plugin/user quest. It converts project-scoped roles into ordinary workspace
agents using the standalone prompts. It retains project files/entry,
connection-mode tasks, project tools/skills, capability declarations, runtime
requirements, post-workspace setup, dashboard, and existing child-scoped
consent boundaries.

No empty or hidden Home, station state, roster, reflection schedule, portfolio,
or Assistant Project link is created and concealed.

### Composition matrix

| Declaration | Creator consequence |
| --- | --- |
| Legacy absence | Existing behavior; no v1 policy is inferred or snapshotted. |
| None | Fixed standalone composition; optional ordinary parent remains organizational only. |
| Recommended, no choice | `choice_required`; both consequences are disclosed and no mutation occurs. |
| Recommended, grouped | Exact source Home create/reuse and reciprocal link. |
| Recommended, standalone | Fixed standalone transformation; zero Home/program consequences. |
| Required | Fixed grouped consequence; there is no per-creation opt-out. A separate variant may select another policy. |

A valid standalone composition is required for explicit None or Recommended on
a program-bearing source. An ordinary template with no program or
pre-workspace quest is already standalone.

## Source-linked variants

Installed plugin blueprints remain read-only. Customize creates a user-owned,
manifest-only overlay in the ordinary template library; it never copies plugin
files, plugin ownership, a setup quest, or executable declarations.

```json
{
  "template_variant": {
    "schema_version": 1,
    "variant_id": "tv_0123456789abcdef01234567",
    "owner_user_id": "local",
    "source": {
      "plugin_id": "reaper-plugin",
      "plugin_version": "0.6.0",
      "blueprint_id": "reaper-song",
      "blueprint_version": 6,
      "definition_digest": "<64 lower-case hex characters>"
    },
    "overrides": {
      "name": "My Reaper Song",
      "description": "My preferred placement.",
      "icon": "🎚️",
      "group_requirement": {
        "schema_version": 1,
        "policy": "recommended",
        "assistant_program_id": "music-producer-assistant",
        "missing_home": "offer_create",
        "default_home_name": "Music Production Home"
      }
    }
  }
}
```

The serialized block is at most 16 KiB. The server creates immutable IDs
matching `tv_[a-f0-9]{24}`, resolves/writes the current owner, and copies source
identity only from one active trusted `ResolvedBlueprint`. Create/update
requests submit a catalog source and allowlisted overrides, never owner/source
objects.

The source definition digest is SHA-256 over a canonical normalized envelope:
exact plugin/blueprint identity, skeleton digest, and every behavior-bearing
source declaration used for creation. It excludes paths, diagnostics,
readiness, timestamps, and presentation-only server state.

Effective resolution must match plugin version, blueprint version, definition
digest, enablement, protocol, host features, platform, and trust. The host then
copies the in-memory source, removes plugin authoring/quest ownership, applies
only name/description/icon/group-policy overrides, and revalidates the whole
effective template. Source files remain the creation skeleton, but the variant
remains user-owned.

`variant_revision` is SHA-256 over the normalized persisted variant block.
Preview/save require immutable variant ID plus the current revision and run
inside the library mutation lock. Source or revision changes fail before an
atomic manifest rename and leave prior bytes untouched.

A source update never silently rebases v1. Missing/disabled/changed sources keep
the variant readable and deletable but unavailable for creation. Customize the
current source to make a new variant. Duplicating a ready variant issues new
folder and variant IDs while retaining its exact source pin. Import requires
explicit local rehosting and writes new server-owned identity; copied owner
claims are never trusted.

### Source and Home identity matrix

| Effective template | Authoring owner | Home-key source | Plugin authority on variant |
| --- | --- | --- | --- |
| Trusted plugin blueprint | Plugin contribution | `(current user, plugin_id, program_id)` | Existing plugin authority only. |
| Source-linked variant | Current user | Nested validated source plugin plus program ID | None; `PluginOwner` stays nil. |
| Ordinary user template + user quest/program | Current user | Existing host-generated template/attachment key | None. |
| Copied plugin folder without variant envelope | Current user | No trusted plugin source identity | None; cannot reuse plugin Home by claim/name. |

A variant's nested source is inert provenance. It cannot register routes,
quests, services, tools, grants, or permissions. Compatible variants and their
source blueprint reuse one Home because they resolve the same stable source
program key—not because their names match.

## Evaluation and reviewed consequences

One server-owned evaluator sits between effective-template resolution and every
policy-bearing mutation path. Browser and agent requests cannot submit trusted
owner, plugin, program, source, or Home objects.

The evaluator derives current user, exact template/revision/source, available
compositions, stable Assistant Program key, and zero or one exact Home. A client
`parent_id` and a same-name group are not target-resolution inputs.

| State | Meaning |
| --- | --- |
| `ready_grouped` | Exact compatible Home exists. |
| `ready_standalone` | Effective standalone composition is valid. |
| `choice_required` | Recommended needs an explicit grouped/standalone choice. |
| `home_creation_review_required` | Grouped + `offer_create` has no exact Home. Project creation stays blocked while a separate inert Home-only review is offered. |
| `home_required` | Grouped + `existing_only` has no exact Home. |
| `source_unavailable` | Exact source/owner state cannot be proven. |
| `target_ambiguous` | Multiple Homes carry one stable key or canonical state conflicts. |
| `contract_invalid` | Declaration/composition/owner validation failed. |

Review is side-effect free. Its 15-minute durable receipt binds owner,
operation kind, template/variant/source revisions and digests, selected
composition, program declaration/key, existing Home or reviewed create intent,
and connection/folder/entry fingerprints. It stores no credential, prompt,
absolute path, command, route, or plugin action.

Missing `offer_create` placement uses two independent consequence boundaries.
`prepare_home` review/commit can create or reuse only the canonical empty Home;
it has no child workspace ID, project-link ID, project files, roster, tasks, or
grants. The response explicitly reports that no project workspace was created.
The client then obtains a fresh project receipt bound to that exact existing
Home, and only a later explicit action may create/connect the project.
`create_required_home` on a workspace/project request cannot collapse these
steps and never receives a commit receipt.

A project commit consumes its own receipt with a caller idempotency key,
revalidates every bound fact, and journals `claimed`, `home_ready`,
`child_ready`, `link_ready`, `succeeded`, or `reconcile_required`. It
creates/resumes a deterministic child at its final parent, persists
files/tasks/effective provenance, and for grouped creation establishes and
observes the reciprocal link before reporting success or starting
Home-dependent work.

Concurrent reviewed first-Home preparations converge on the same stable key. A
matching concurrently created Home may satisfy create intent; a replaced Home
cannot satisfy a receipt that bound an existing ID. Rename is harmless. If the
user stops after Home preparation, the empty canonical Home remains visible and
reusable; cancellation never implies permission to delete it.

Rollback may remove only a provably operation-owned incomplete project child.
It never deletes a separately prepared or reused Home, external project/folder,
user file, agent, or grant. Uncertain cleanup records `reconcile_required`;
retry observes and resumes the same IDs rather than creating duplicates.

## Durable workspace snapshot

`TemplateProvenance` stores a deep-copied `GroupRequirementSnapshot` schema v1:

- declared policy and selected composition;
- effective template, variant, source, and definition revisions/digests;
- standalone role-source mapping when selected;
- derived Assistant Program key plus Home/link IDs when grouped; and
- review/operation digest evidence and applied time.

It contains no path, source URL, executable behavior, or permission. Folder
`workspace.json` is canonical; SQLite mirrors preserve the envelope where
needed. Later template/plugin/variant edits do not rewrite existing snapshots.
Required stays Required after disconnect.

Grouped success and healthy status require all three matching facts:

1. snapshot target;
2. physical/logical `ParentID`; and
3. reciprocal Assistant Program link/Home membership.

No one fact substitutes for another.

## Lifecycle and clean-start boundary

A structurally valid v1 snapshot is the only new-contract marker. Old template
IDs, file extensions, names, tags, parentage, or Assistant Program links are not
inferred into it. Reads, startup, cataloging, rescans, plugin lifecycle, and
ordinary saves never backfill, move, link, or create a Home.

A policy-aware operation on an old workspace returns recreate/reconnect
guidance. Reconnecting may create a fresh Ori workspace that references the
same exact external project; it does not alter that folder or adopt old agents,
tasks, grants, setup progress, or receipts. Existing managed projects are not
deleted to manufacture a clean start.

A live-linked grouped project uses reviewed disconnect/removal flows. A
Required disconnect preserves the snapshot and reports the requirement
unfulfilled until reviewed reconnect or separately reviewed
standalone/recreation. None and standalone Recommended projects follow ordinary
organization rules and never gain membership merely by moving under a Home.
Home removal retains existing impact review and never recursively grants or
deletes child data.

Source disable/remove pauses only source-dependent execution. It does not
remove or relocate snapshots, links, workspaces, tasks, project files, Homes,
or roles.

## Error and recovery vocabulary

Group-policy responses use closed, domain-neutral reasons:

```text
choice_required
home_creation_review_required
home_required
source_missing
source_disabled
source_changed
source_incompatible
target_owner_unavailable
target_ambiguous
contract_invalid
review_stale
operation_incomplete
group_requirement_unfulfilled
legacy_contract_absent
```

Allowed server-generated recovery actions are:

```text
choose_grouped
choose_standalone
review_create_home
open_guided_setup
customize_template
recreate_workspace
reconnect_project
change_template
manage_plugins
retry
```

Responses return sanitized summary/detail and at most four actions. No reason
or action carries a domain name, path, URL, command, permission, or untrusted
plugin recovery text. Malformed input is `400`; hidden/unrelated ownership is
`404`; stale/dynamic conflict is `409`; unavailable canonical storage is `503`.
No policy-bearing route returns success until canonical consequences are
observed.

## Entry-point and failure ownership

| Entry point | Policy behavior / owner |
| --- | --- |
| Template library list/find, unified catalog, readiness | Resolve effective declaration/source; read-only, no Home or setup mutation. |
| Template create/update/import/duplicate/delete/files | Existing library lock plus variant owner; variant surface is manifest-only and revision checked. |
| Plugin blueprint resolver | Strict declaration and required-host-feature validation; no fallback to same-named library data. |
| Templates editor | Preview/save/cancel against owner endpoints; installed source is read-only and offers Customize. |
| `POST /api/workspaces/group-requirement/home/{review,commit}` | Inert review plus idempotent Home-only preparation; no project identity or project consequence. |
| `POST /api/workspaces` and legacy `/api/folders` | Shared evaluator/review-commit owner for policy-bearing templates; missing grouped Home blocks rather than being created in the project commit. |
| `/api/workspaces/{id}/project`, map/group creator | Same effective resolver and evaluator; no arbitrary-parent or combined Home/project bypass. |
| Setup quest/journey new/existing project | Existing project-connection owner plus the same group review; exact external file remains unchanged. |
| Chat list/create project tools | Unified catalog/resolver; use supported owner or return bounded guided-create action. |
| Capability/runtime/surface/plugin routes | Unrelated unless the concrete feature genuinely depends on live reciprocal membership; grouping grants nothing. |
| Direct move/trash/delete and Home removal | Snapshot/link-driven protected lifecycle; existing depth/cycle/slug/path guards remain. |
| Personal Assistant hire / Personal HQ | Unrelated; profile hire never creates HQ. |

Structural manifest/source failures belong to template/plugin readiness.
Composition choice and missing Home belong to the grouping evaluator. Folder,
entry, scaffold, and topology failures belong to the reviewed operation and
remain resumable—not warnings. Agent/capability/runtime grants stay with their
existing explicit owners after successful topology.

## Implemented host behavior and local evidence

The host implementation is split into independently reviewable commits:

- `690ac620` defines this contract and its rollout boundary.
- `175bfa59` adds strict authoring, preview, and source-linked variants.
- `856bf2f6` adds the canonical evaluator, durable review/operation receipts,
  creation-path enforcement, clean-start snapshots, and grouped/standalone
  composition.
- `d5e6413e` adds snapshot-driven lifecycle projection, move/delete protection,
  reviewed disconnect/reconnect and Home removal, and workspace recovery UI.
- `335035c8` makes the disposable REAPER demo accept an explicit candidate
  source without changing reviewed or user-installed plugin state.

Production receipts use SQLite tables `group_requirement_reviews` and
`group_requirement_operations`. Clean-start placement provenance remains in the
canonical folder `workspace.json`. Required success observes exact parent,
typed child link, reciprocal Home membership, stable owner/program identity,
and matching operation digest. Browser review data never supplies owner,
source, Home, or permission authority.

The canonical workspace, existing-workspace project, setup journey, Map/shared
creator, and applicable chat-tool paths use the same evaluator or return a
bounded guided-create refusal. The legacy Assistant Program Activate route
cannot repair an unfulfilled Required snapshot; only reviewed reconnect may do
so. Ordinary parent moves remain organizational and cannot mint program
membership or authority.

The separately owned REAPER implementation is commit `c3487d7` in
`/Users/jjdev/Projects/ori/worktrees/reaper-plugin-template-groups`. Two clean
local builds produced identical candidate bytes:

```text
plugin version: 0.6.0
Reaper Song blueprint: 6
artifact size: 8780098 bytes
sha256: 4def4fec14ecf083b0358c686c608514d4b9afff99dd810f1184213312770119
```

A disposable host run demonstrated a separate Home-only consequence before any
project existed, cancellation leaving only that inert empty Home, then one
renamed Home with two exact Required children, one standalone variant project,
idempotent Home/create/reconnect replay, stale-review and arbitrary-move refusal,
Required activation-bypass refusal, Home removal preserving project
paths/tasks/snapshots, and one canonical `workspace.json` per workspace ID.
Editing the variant from None to Recommended after creation left the existing
project's recorded None/standalone snapshot unchanged. Screenshots and endpoint
evidence are under the gitignored `tasks/screenshots/` and
`tasks/*evidence.json`; they are local development evidence only.

Validation completed with the main Go suite, 2,662 JS module tests, affected Go
package and race suites, ESLint, Prettier, vet, ratcheted golangci-lint, two
Playwright group-requirement acceptance cases, four coordinated REAPER browser
cases, and `git diff --check`. The original scoped `gosec` comparison produced
99 findings both at the branch merge base and current tree, with zero normalized
new findings; a final scan of the three correction packages reported seven
legacy package findings and none in the corrected Go files. The package-wide
baseline was not misreported as a feature regression. The final smoke run passed
50 of 51 tests; its one failure was the unrelated CSV-storage task setup
receiving `409 insufficient_resources` for `craft`. The same focused failure
previously reproduced on a clean server built from `origin/dev` commit
`ce18a03c`, so it is recorded as target-branch baseline rather than hidden or
attributed to this feature. The plugin separately passed all Go tests, vet, 16
UI tests, deterministic artifact verification, and release packaging.

## Host/plugin compatibility and delivery

The exact required host feature is `template_group_requirements_v1`. A trusted
plugin declaring either new block must require it. Older hosts reject such a
plugin through existing feature negotiation; new hosts preserve legacy absence
for old plugins.

The coordinated REAPER candidate is plugin `0.6.0`, Reaper Song blueprint v6,
setup quest v1, with Required/`offer_create` policy and standalone-composition
support for user variants. Ori host support lands first. Candidate changes are
made in a separate plugin worktree based on canonical `v0.5.1` commit
`972c33fb50b813c73beaafd779c71ced266477fa`.

Ori's current reviewed entry still expects plugin `0.5.0`, commit
`1f494db5d6f52c697dcf0682db1c7e6cb6479733`, and blueprint v4. It is not changed
from local evidence. Production pinning to `0.6.0` requires a separately
approved, reachable tag/source and release artifact whose URL, digest, size,
mode, package checksum, and binary version all match the exact reviewed commit
and host compatibility evidence.

A local build/staged demo is development evidence only. Publishing, tagging,
pushing, installing into a user's current store, resetting user data, and
claiming release readiness remain separate consequence boundaries.
