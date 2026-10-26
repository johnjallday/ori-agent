# Assisted File Janitor placement

Status: implementation contract (not a claim of shipped behavior).
Scope: the built-in inbox-folder proposal only. Music Home, REAPER, generic
folder recipes and ordinary map/wizard creation keep their existing semantics.

## Authority

The interaction is **propose → place/edit → review → explicitly confirm**.
The editable `WorkspaceBuildSession` is the only destination draft. Its
`BuildDraft` is compiled by the existing normal creator and reviewed File
Janitor preparation path. `FolderDigestService` owns the source offer;
`FolderObservationService` owns held folder identity. File Janitor continues to
own grants, automation consent, scan operations and receipts. No second setup,
grant or scan engine is introduced.

`sessions.js` owns network requests, canonical draft setters, generation checks,
team configuration and preparation. `assisted-workspace-placement.js` renders a
bounded panel and emits user intentions to that owner; it must not construct
create requests or retain an independent material draft. The map owns geometry
only. Presentation strings, artwork, drag payloads and source filenames are
untrusted display data, never execution authority.

## Typed handoff

Add `POST /api/workspaces/build-sessions/from-inbox`. Strict, bounded JSON:

- `offer_id`, `plan_digest`, `conversation_id`, `observation_id`,
  `source_revision`: exact source review references, not a serialized plan;
- optional explicit `name`, `brief`, `monitoring`: current unsaved card inputs,
  validated by existing name/brief rules, applied only when first seeding.

No root path, blueprint, role, parent, grant, workspace ID or executable plan
is accepted. Resolve the authenticated owner without a fallback on error.
Require active/paused assistant relationship and its readable HQ store, a local
canonical active conversation/revision, eligible unaccepted standalone inbox
root, current source plan and a still-held selection. Reject imported,
historical, foreign, replaced and already accepted offers. Accepted inbox runs
remain on their existing recovery path, not a new draft.

The host seeds the built-in File Janitor name/goal and one File Curator role
from the current reviewed template. Existing default profile → assign unchanged;
otherwise → draft create. No profile is persisted. Monitoring starts false
unless the initiating card carries the user's explicit choice. No model
resolver/provider call, automatic `suggest_folder`, plugin install or metadata
sharing occurs. Generic build availability and inference gates remain unchanged.

Response: normal canonical `session`, plus `resumed` on exact replay. Conflicts
return a stable code (`source_changed`, `selection_expired`, `build_conflict`,
`handoff_incomplete`), and an owner-visible conflicting build ID where applicable.
No response contains a fabricated workspace/scan receipt. A replay returns the
current draft and **never re-applies initial inputs** over newer edits.

## Durable single-owner handoff

A browser-only fence is insufficient. Add bounded immutable source provenance to
the build and a destination binding to the folder offer:

- Build schema **4 → 5**: optional `placement_source` (offer, conversation,
  observation, source revision and plan digest), plus draft monitoring choice.
- Folder digest schema **1 → 2**: optional `placement_handoff` (build ID, source
  revision, plan digest and a `draft_saved` acknowledgment). A handed-off source is closed, not accepted,
  declined or tombstoned. It cannot enter the original setup/ordinary create.
- Old records decode with these fields absent, never with inferred consent.
  Unix and Windows stores share validation/codec behavior. Unknown future
  schemas fail closed. No blueprint version or companion protocol changes.

The handoff service holds the existing source conversation lease while checking
canonical source state and seeding. Inside the build store mutation, reject an
unrelated open **or preparing** build before changing the source. Keep the lock
order **conversation lease → build store → folder store**; no inverse nesting.
Preparation first serializes on its existing preparation mutex, then acquires
that lease, so concurrent confirmations read the latest accepted claim.
Persist the immutable source binding before the build document. That ordering
can leave a closed source with a missing destination on failure, but never two
live creation owners. An explicit retry may finish that same reserved build
identity after revalidation, but only while `draft_saved` is false. After the
destination is proven durable, acknowledge it in the source; acceptance requires
this acknowledgment. A later missing/pruned acknowledged draft is never rebuilt.
GET does not repair or create anything. A conflicting
build that appeared after an interrupted write is not overwritten; report it.
Do not roll the source back to independently confirmable state on uncertainty.

Recheck the handoff fence **inside** original setup admission mutations as well
as their initial reads. A source request already in flight cannot decide a
handed-off offer. The handoff likewise refuses any now-accepted source.
Close/decline/re-review paths may not erase or retarget the binding. The build
keeps provenance after settling; bounded history pruning may make recovery
unavailable but must never authorize a new operation under the old source.

Before acceptance, material writes and preparation require the current owner,
source provenance and canonical source context. Source revision changes cause a
conflict, not a re-seed. Reopening the same handed-off review preserves its
canonical event revision. Owner-scoped abandonment remains available after
detach/expiry, and uses the draft version fence. After acceptance, retain the accepted envelope and use
the existing canonical ownership/receipt recovery rules; detach is not undo.
Ordinary `POST /api/workspaces` cannot bypass preparation by naming a placement
build, even before acceptance. A placed draft stays built-in File Janitor,
standalone, with the supported File Curator role; unsupported blueprint/group/
team configurations are refused rather than silently generalized.

## Folder authority and editing

Rebind through a host-only observation method, never through a browser path.
Validate the exact source target and held root, relationship binding and inode
witness. Copy to a fresh observation handle scoped to `build:<id>` while
preserving `scanned_at`, evidence and TTL. Keep the original hold for source
review/recovery; copying is not a new scan or an extension of its lifetime.
Source and destination binding checks gate its use. A restart or expired hold
requires a fresh trusted pick; persistence cannot revive access.

Scoped placement folder choices/attachment do not require a conversational
model. Normal picker/approved selection tokens remain the inputs for a new
selection. Placement-only `mode: held` accepts an `observation_id` that the
existing service can still resolve for this exact build target. It reuses the
observation without rescanning or extending its lifetime; another conversation,
build or owner cannot supply one. A slow, cancelled or rejected replacement
leaves the previous material draft intact. A refused attachment can still advance
its reservation version: read the same build, adopt only unchanged material
choices, and use the existing conflict fence if another tab edited them. Reject
incompatible/managed roots before publishing a replacement. Folder replacement
invalidates review and requires seeing its new terms, but does not erase a
previous deliberate monitoring choice. Redraw never rescans.

Agent and name/goal changes use existing team/form setters and PATCH version
fences. Saved assignment does not edit the global profile. New-agent settings
remain a draft until confirmation. Monitoring updates also increment the build
version. Unsaved or in-flight material edits disable confirmation. Late model
replies cannot overwrite user-owned fields; placement drafts do not request one.
Optional in-app dragging resolves typed same-owner references through the same
setters as buttons; raw files/paths/serialized plans are rejected with a reason.
The optional setup-card shelf holds only display projections and opaque local
handles, scoped to the editor generation and canonical version. Saved-agent
cards call normal Clear/Assign; folder cards reference only live selections
previously received from this build's server responses. Click-select plus the
labelled slot button and native drag/drop emit the same intention. Active drag
source DOM stays stable across permission-read replies. Neither card handles nor
displayed folder paths are execution authority.

## Exact review and acceptance

`janitor-review` compiles the current canonical draft, live saved profiles,
blueprint, exact held folder/terms and monitoring into its server revision.
Display the entire effective result: name and goal, actual create/assign team,
linked-not-moved folder, top-level names/types/sizes/dates only, creation/reuse of
`Filed/`, first metadata-only filing review, and optional watch/daily time.
“Prepare a filing review” is fixed, not a create-without-scan option. File moves,
Trash and content reads still require their existing separate permission steps.

Only **Create workspace & scan** sends `janitor-prepare` with the exact build
version/review revision/mode. The existing preparation claim freezes identity
before effects. Concurrent confirmations join/replay that claim; stale tabs
cannot overwrite or confirm newer drafts. No ordinary-create fallback on a
network error. Reserved IDs and resource names do not prove creation.

## Geometry, fallback and layout repair

Add an explicitly opted-in draft-selection mode to `beginPlacement`; existing
callers retain creation-on-click/Enter. In draft mode, ground clicks, arrow
nudges and Enter select a candidate only. A translucent building has a visible
Draft label and says **Standalone workspace — map position does not join a
district**. Input keys in the adjacent panel belong to its controls, not the
canvas. Selection uses existing snap/collision/camera rules. Candidate changes
are UI-only and do not invalidate material server consent.

The panel is outside the transformed canvas and scrolls independently, with
focused controls and final consent visible at desktop zoom. On narrow screens,
keyboard-only use or unavailable/read-only map, use the same compact controls.
**Use suggested setup** opens the same current draft/review, preserving edits.
Without manual placement, leave layout to the existing automatic layout; say
so explicitly. Do not invent a coordinate or district membership.

After canonical proof of a created workspace, the owner passes its returned ID
and exact chosen coordinate to `commitPlacement` before normal navigation.
Introduce a narrow preparation outcome callback rather than a second creator.
A layout failure does not undo setup or recreate anything. Show setup result
and position result independently. Position repair only updates layout for that
verified existing workspace, with fresh collision checks; it cannot move it into
a group or retry grants/scans. Never silently substitute a nearby position.

## Return and recovery matrix

| Event | Required behavior |
| --- | --- |
| Back / switch map ↔ compact | Keep the current draft, valid candidate and deliberate mode; no effects. |
| Cancel before acceptance | Abandon the destination draft; source remains retired, no grant/profile/workspace/Filed/scan/watch. New setup requires an explicit new review. |
| Close / refresh before acceptance | Keep recoverable canonical draft; coordinate may be lost. No automatic confirmation or inference. |
| Unrelated open/preparing build | Explain conflict, preserve both source and existing build; user resolves it explicitly. |
| Expired folder / restart before acceptance | Keep edits; require trusted re-pick and fresh review. No snapshot/path restoration. |
| Lost confirmation response | Read canonical claim/status, retain accepted request; show uncertainty if unreadable. Never ordinary-create or adopt by name. |
| Partial accepted setup | Keep created resources and expose existing exact-operation recovery. Status reads never continue work. |
| Close after acceptance | Closing is not undo; retain operation and canonical recovery action. |
| Scan failure | Explicit operation-aware scan retry only, not grant/monitoring reconfiguration. |
| Position failure | Setup survives; explicit position-only repair, no recreation/reparenting. |
| Reopen consumed history | Verified result/recovery only; no task replay and no live old setup prompt. |
| Newer unrelated conversation | Ignore late UI navigation/completion callbacks; never consume its attachment. |

Completion is derived from observed membership and real batch/empty/failure
receipts. Reuse the verified completion adapter and retire only the exact source
attachment/prompt. Do not treat the planned role, a reserved ID or an HTTP 200
with malformed content as success.

## Verification boundary

Characterize ordinary map confirmation and existing exact edited preparation
before extending them. Cover no-provider admission, source-vs-build races,
fail-closed interrupted handoff, unrelated open/preparing builds, expiry,
identity changes, late responses, replay and ordinary-create bypass at the
server boundary. Real-server acceptance must count resources/grants/batches/
watches and check source bytes, not just stub receipts. Prototype fixtures are
inert test-only pages and cannot be enabled by a production URL parameter.

Use isolated HOME/data, synthetic directories and explicit no-provider launch.
Desktop native picker and actual 200% zoom checks remain required observations,
not claims inferred from scripted picker responses. Before confirming, a
reviewer must identify the workspace, agent, folder, first action and monitoring
state from the primary surface (A12); this is a walkthrough, not a statistical
UX improvement claim.
