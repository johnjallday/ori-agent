# Portable workspace continuity contract v1

Status: implementation contract. The "Delivered composition" section below
describes what the server actually runs; later sections keep the design
record and name the few places where a delivered decision supersedes them.
Requirements: `tasks/prd-portable-workspace-continuity.md`, FR-01–FR-36.
Source investigation baseline: `8e560c5e358e4d7dbfad2432865ee94ae87fe4e7`.

## Delivered composition

**Source preparation.** `internal/continuityprep` composes every domain
collector (workspace + SQL metadata, scoped agent profiles and owned
appearance, assistant agreement, follow-ups, brief config and every brief
revision, sessions/messages/tags, tool history, uploads, notes, knowledge
summary, first-assignment evidence) over one shared SQL read view, plus a
whole-folder file inventory (`workspace.CollectContinuityFolderFiles`: every
regular file except the managed checkpoint, child workspaces, locks, OS junk
and Ori's own writer scratch). `continuityprep.Worker` runs in the server
(started in `Server.Start`, stopped first in `shutdownBackground`), holds one
reset `WorkGate` permit per pass, considers only workspaces registered in this
installation's database, debounces changes, retries failures with backoff and
detects out-of-band file edits with a stat signature (a full `Inspect` the
first time after start). A parent's manifest names its physical children;
each child publishes its own generation. Status is Preparing / Ready as of /
Update needed / Unavailable with a content-free reason code, served at
`GET /api/workspaces/{id}/continuity` (`POST …/prepare` runs one attempt now).
The UI calls Update needed "Saving latest work": it is automatic, and any save
of `workspace.json` (including the no-content re-save a workspace page makes
when it opens) moves the folder past its last checkpoint until the next pass.

**Folder is the portable truth for workspace content.** The workspace record
is taken from `workspace.json`. Identity must agree with the SQL row (owner,
name, parent, folder slug, creation time) or preparation reports a change.
Folder-owned content that the SQL mirror holds differently is only recorded
(debug log, column names only) and never selected: existing writers leave such
differences on real installations — Build My HQ's brief-config stash and the
HQ upgrade's provisioning marker are saved only to SQL, a canonicalized agent
node only to the file — and refusing them kept those workspaces from ever
becoming Ready. Both copies of the fields every read migrates (agent
instances, tasks, scheduled tasks, store nodes, capabilities, folders) are
compared after that same migration. `version` and `updated_at` are each
store's own save counters, not content. This supersedes "rejects split
canonical work" below; concurrent changes are still fenced by the dirty
sequence and publication checks.

**Discovery guard.** The server builds the folder store with
`workspace.NewFileStoreWithContinuity`. A folder that carries
`.ori/continuity/` is loaded (and therefore migrated, reconciled, registered,
allowlisted or scheduled) only when this installation's database owns it and
its attachment is not unreviewed, detached or still restoring. A copied
folder, or one retained across an app-record reset, stays invisible until a
reviewed import completes. Folders without a checkpoint keep today's behavior.

**Admission.** `workspacecontinuity.LocalStore.CheckExecution` backs the
folder store's `ExecutionAdmission`, Daily Brief generation (first-open and
scheduled are automatic; manual refresh is manual) and follow-up wake/nudge.
Attachment rows are written only by imports, native preparation registration
and discovery classification, so **a workspace with no row is an existing
native workspace** (this supersedes "missing attachment state means
unreviewed" below for the ordinary store; the opt-in private store keeps its
stricter rule). Imported workspaces are `imported_inactive`: readable and
usable by hand, no background routines until the explicit
`POST /api/workspaces/{id}/continuity/activate {"enable":true,"version":n}`.
Loops that list workspaces themselves (File Janitor daily catch-up, blueprint
reintake) receive only automatically admitted IDs. The trigger service loads
an imported workspace's definitions when the import completes and starts or
stops its file watches and webhook tokens when routines are turned on or off
here (`Service.ReconcileWorkspace`), without a restart.

**Local connection settings.** The ordinary (non-private) store is what the
server runs. A native `workspace.json` or agent `config.json` may still hold
MCP/skill configuration, grants, a native CLI opt-in or an agent-specific
API key entered on that machine. Instead of refusing such folders (which would
leave nearly every real workspace unpreparable), every decoded value is the
**denied projection**: checkpoint records never contain those fields and an
import installs only denied definitions (webhook tokens are reissued, queued
trigger fires dropped). The copied physical files themselves are the user's
private data and the UI says so. This supersedes the earlier requirement that
native files be separated into the encrypted local store before Ready; that
store stays available but is not switched on by this feature.

**Reviewed import.** `/api/workspaces/import/check` reviews the tree and, in
one read view of this installation, reports the destination digest and the
allowed actions: `continue` (exactly one assistant candidate, no local
relationship or HQ designation) and/or `workspace_only`, plus
`already_imported` (identical completed receipt: open, no-op) or
`conflict: workspace_exists`. `POST /api/workspaces/import/continuity`
requires the reviewed tree and destination digests, re-inspects, creates the
inactive receipt (`BeginReviewedImport`) and an install plan
(`continuity_installs`, migration 70), then:

1. restores every domain from the receipt-bound generation, each in its own
   receipt-owned transaction (records already claimed are skipped on retry);
   an unfinished brief attempt becomes a failed, non-current revision marked
   interrupted; a finished first-open/scheduled brief restores its terminal
   claim so later automation does not regenerate it;
2. adopts the assistant only for `continue`: paused relationship with the
   agreement, HQ designation in the same transaction; first-assignment
   completion only when every created record was itself restored. For
   `workspace_only`, the agreement (verified against the reviewed entry
   profile) and first-assignment records are kept inert in
   `continuity_retained_records` (migration 71). They are never read as this
   installation's relationship; the collectors re-export them when no local
   relationship owns that HQ, so the workspace can be prepared here and later
   moved to a machine that can adopt its assistant;
3. installs the folder: a folder outside the Workspace Directory is copied
   file by file (verified against the manifest) into operation scratch,
   projected there and renamed into place; a folder already inside the
   Workspace Directory is renamed to its slug and projected where it is
   (`workspace.json` last, so a partial projection is resumable), and its
   copied checkpoint subtree is made private again;
4. completes the receipt (`imported_inactive`), reloads the folder store,
   trusts the workspace's own agents and wakes the preparation worker.

A failure interrupts the receipt; `POST /api/workspaces/import/continuity/{op}/retry`
resumes it and `GET …/{op}` reopens the report. Nothing pre-existing is
overwritten, deleted or adopted by ID. Uploads are installed into the normal
session-files store (linked/missing source files become unavailable entries;
source paths are never installed or read). Notes restore their SQL rows and
rebuild headings/links. Knowledge metadata of an adopted HQ is rebound to this
folder; unresolved or mid-operation items become Needs review.

**Ownership of new manual follow-ups.** A follow-up added by hand is owned by
Email Ops when it exists, otherwise by the designated Personal HQ, so it
travels with that folder instead of being ownerless.

**Known limits.** Renaming an adopted (imported) assistant returns
`imported_rename_unavailable` rather than running the global-name rename.
Empty directories are not carried. A workspace larger than the v1 limits, or
containing links or names another filesystem cannot hold, reports
Unavailable with a reason instead of Ready.

## Authority and consent

Existing domain stores remain the only live authorities. A completed checkpoint
is a private, offline reconstruction input, never a live database or permission.
Only a reviewed, confirmed import may restore it. Startup, changing the workspace
root, Rescan, opening Home, or finding a portable marker is not adoption consent.
Digests establish consistency, not authorship, authentication, or trust.

An import set consists of the selected directory and physically contained Ori
`sub-workspaces/` children (the actual canonical on-disk name), not referenced workspaces or external linked files. The format also reserves the older `sub_workspaces/` spelling; neither may be collected as a single owner's file payload.
Each workspace stores only its own history. Children carry their own checkpoints;
a parent checkpoint names its immediate physical children, not their history.
Review validates the entire selected tree and binds every child's checkpoint and
folder fingerprint into one operation digest. A changed/missing child is not a
complete tree. Different independent checkpoint times are displayed, not combined
into an invented common transaction time.

One eligible HQ and no conflicting local assistant/designation offers **Import
and continue**. Any existing different, pending, corrupt, or incomplete local
identity, or multiple incoming HQ candidates, offers **Import workspace only**.
No replacement or name-based adoption. Ordinary workspace imports never designate
HQ. Local owner assignment is permitted only for the supported local-user mode,
after review validates consistent source ownership; a matching `local` string is
not authentication. Unsupported/contradictory ownership blocks the component.

A completed identical operation is a no-op, even after local edits or activation.
Another generation targeting existing IDs is a conflict; there is no merge,
refresh, fork, overwrite flag or agent-library promotion in v1.

## Source ownership and mutation map

The writer observes the canonical persistence boundaries below. UI event hooks
alone are insufficient; tools, maintenance, retries and server jobs use the same
stores without passing through a browser.

| Component | Inclusion and restore authority | Mutations that invalidate preparation |
| --- | --- | --- |
| Workspace tree, tasks/tickets, collaboration messages, layout, capability definitions | `workspace.json`, session workspace projection and stable instance IDs. Physical children only. Existing task/number/instance IDs survive; no blueprint replay. | `FileStore.Save/SaveAt/Update/RebindExistingFolder`, physical move/reparent/root reconciliation, agent attachment changes, task/ticket/workflow/mission writes, notes and direct file edits. Old and new parents must become dirty on a move. |
| Workspace agents and assets | Exact `GetWorkspaceAgent(workspaceID, instance.Name)` after validating that the stable instance belongs to that workspace. Never `GetAgent(name)` as identity proof. | `SaveWorkspaceAgent`, attach/detach, snapshot backfill, workspace-scoped edit/rename, asset/skill edits and deletions. Existing independent workspace definitions must not be overwritten from the global roster. |
| Assistant agreement | `personal_assistant_state` only when its HQ and exact entry instance belong to the owner checkpoint; profile provenance must agree. | Create/update/CAS, hire/HQ finalization, pause/resume, mandate/focus/appearance edits, specialist accept/decline, rename and assignment finalization. Moving a binding dirties both former and new owners. |
| Sessions and messages | `sessions.workspace_id` (`Session.FolderID`). Include all retained pages, not Daily Brief's bounded recent-session projection. Agent name alone never scopes a session. | `CreateSession/UpdateSession/AddMessage/UpdateTags`; tag rename/removal; session move/unlink, single/bulk/agent/workspace deletion; hybrid periodic flush, inactivity cleanup and maximum-session pruning. SQLite write tracking covers raw maintenance deletes as well as normal APIs. |
| Historical tool calls | `tool_calls.session_id` must resolve to an included session; a nonempty message reference must resolve to that session's message. | `AddToolCall` and cascaded session/message deletion. Arguments/results/errors are private history, not queued calls. |
| Session uploads | Session-files manifest keyed to an included session. Copy owned regular files only; linked files keep unavailable references. | `AddFile/AddFileFromReader/LinkFile/RemoveFile/RelinkFile/DeleteSession`, manifest status changes, out-of-band replacement and reset-owned-upload removal. The current production base is CWD `session_files/`, not the workspace root. |
| Follow-ups | `personal_hq_followup.workspace_id` is operational ownership. Include all six states, not `OpenOnly` or the five-item Home projection. | Store Create/Update/Delete, including capture/dedup refresh, confirm/edit/snooze/wake/complete/dismiss/reopen, task-link and nudge bookkeeping. HQ projection of Email Ops rows does not transfer ownership. |
| Briefs | Config/revisions/terminal claims/notification evidence keyed by the owning workspace. Historical revision config numbers remain historical. | `UpsertConfig`, generation-claim lifecycle, `CreateRevision/SetCurrentRevision/RecordNotification/PruneHistory`. `ListHistory` collapses dates: it is not a complete revision exporter. |
| Notes and memory | Existing note IDs/front matter, workspace note store and `MEMORY.md`; knowledge sidecar is lifecycle metadata, not an alternative fact source. | Note create/update/delete/tag changes/file hydration; MemoryStore Append/AppendUnique/EditAt/DeleteAt/Forget; sidecar proposal/edit/approve/reject/suspend/reconfirm/Forget/recovery/interview operations and canonical-memory changes. |
| Setup evidence | Workspace setup milestones and bounded completed assistant outcomes with canonical refs. Setup journeys are shared-DB records with live owner readers; progression is app state. | Canonical result creation/deletion, completed assignment refs and brief completion, workspace milestone updates. Profile/account/model/plugin changes affect local availability, not recovered setup consent. |

SQL dirty tracking must be committed with the canonical write, including OLD and
NEW owners on moves. It must observe child/cascade deletion before ownership is
lost. File writers must durably mark pending work before publication and signal
a coalescing worker after saving. Startup/status preparation also reconciles
file fingerprints, catching external edits and missed best-effort notifications.
No worker may prepare an unreviewed/detached folder from an empty local DB.

`internal/workspacecontinuity` owns only format, confined files, checkpoints and
local operation/admission records. Each domain owns typed portable DTO validation,
`Export(ctx, queryer, owner, sink)` and `Restore(ctx, tx, operationID, owner, batch)`
adapters. The shared queryer is the coordinator's read transaction; sinks accept
bounded batches, not an entire installation in memory. Restore transactions insert
records plus receipt ownership without normal service callbacks; caches/indexes
are rebuilt explicitly after commit. Filesystem adapters stage verified bytes
and reconcile by digest/operation ownership. Server composition supplies the
registry; the core format must not depend on every domain or become a second
runtime. Cross-component invariants are checked before writes and before final
adoption. The concrete APIs are private Go contracts, not a published SDK change.

### Home and legacy session observations

`dashboard.js:openOrCreateWorkspaceAssistantSession` persists `folder_id` for a
known workspace. `sessionhttp.createSession` validates that folder and defaults
the agent from its entry agent. Agent-only creation and `GatewaySessionStore`
can persist sessions without a workspace. `HomeAssistantAskHandler.Ask` currently
builds a transient model conversation and returns an answer without saving a
session; Home capability-direct calls can also lack a session ID. Browser-local
Home activity is not independently owned persisted session history.

New hired-HQ chat paths must save an explicit server-validated HQ/instance
binding, while workspace handoffs keep the target workspace as owner. Introduce
an optional stable session agent-instance binding for new scoped sessions; retain
legacy absent bindings as unknown. Do not retag old global or gateway sessions
because their agent names match. Report known ambiguous/unpersisted legacy history
as not included, not recovered or verified empty. Preserve any session timestamp
ordering available from source; equal timestamps use source row order exported as
an explicit sequence, not a destination-created UUID sort.

A partial session-history adapter now keyset-pages explicit `sessions.workspace_id`
owners, their tags and every retained message under one caller SQL view, with
original dates/content/model and source order for equal timestamps. Migration 69
dirties exact owners for session/message/tag/tool-call changes and retains
imported message source order in a nullable local column. A separate tool-history
collector requires the exact included session/message (never a same-name agent);
receipt-owned inserts preserve historic calls without invoking tools. Restored
session rows start with zero counted messages; a final same-transaction check
must verify the total before a coordinator may mark them complete. These adapters
are **not** a product importer; interrupted turns, agent-instance
binding, preparation and admission remain. A separate session-files owner now
reads only SQL-scoped session manifests from its original CWD upload root,
streams owned regular bytes into bounded `uploads` objects, and preserves
missing/linked entries as inert references without opening external targets.
It refuses linked copied bytes, unclaimed physical files, ambiguous names and
broken manifest ownership. Read-only modern tree review now additionally
validates typed sessions/messages/tools/uploads/follow-ups/brief config and
terminal revisions against exact owner and in-copy relationships; an orphan
message/tool/upload, an unreferenced private upload object, or a casefold
upload path collision makes the reviewed tree unavailable rather than claiming
a digest-valid but unrestorable history is complete. A directory-copy test verifies the object without
the source upload root and streams the exact reviewed record's object into an
exclusive private stage, without using the portable external path. This is
inert test-only staging: no destination upload installer, receipt-owned
filesystem write, manifest registration, ordinary attachment view or
deletion/mutation fence is wired. Blob staging now checks the entire inspected
manifest rather than accepting a forged inspection with the same generation. Imported system-role messages keep
their historical role in storage, but chat rehydration projects them as labeled
untrusted user context, never a fresh system instruction.

## Portable records, not commands

Assistant payloads allowlist stable IDs, profile ownership markers, chosen
name/appearance, mandate/focus, specialist choice/decline, source pause intent,
hire/lifecycle timestamps and verified completion refs. Exclude pending hire/HQ
payloads, rename steps and executable assignment previews/apply journals.
Unfinished operations become reported interrupted evidence, not instructions.

First-assignment completion includes the original preview/result identity,
assistant ID, completed timestamp, canonical result refs and brief-result refs.
Validate referenced tickets/follow-ups/briefs before projecting completion. Missing
external refs remain unavailable. Do not run `Apply`, create replacement work,
or repay quest rewards. Pending previews and prepared mutations remain inactive.

Brief completion evidence consists of terminal generation ID, workspace/user,
local date, original trigger/status/times and revision ID, plus notification
revision/workspace/time. Only validated successful/partial scheduled/first-open
claims suppress another automatic generation. No pending/running claim is restored
as live. Manual refresh remains a new explicit operation. Preserve source current
selection only if it points to a valid included revision; do not pick the latest
failed revision. Source `all` scope becomes selected imported IDs with
`include_future_workspaces=false`, with original intent retained in the report.
External source IDs and old prose remain history, not fetch permission.

Knowledge restoration checks the exact assistant/HQ/instance, rebases only the
validated local folder binding, preserves tombstones/hashes and forgotten/rejected
states, and excludes interrupted operations from context. Global profile targets,
SQL preference generations and external-source approvals are not portable
canonical values: preserve their historical receipts as Needs review without
writing the destination profile or enabling context. Prepared Forget remains
excluded even if its old managed line still exists in `MEMORY.md`.

Workspace-local imported agents need scoped profile-read/edit/rename adapters.
Current `AgentStoreProfileReader` and `RenameCoordinator` use global name APIs;
`RenameSessionsByAgent` is broad. Imported rename must update only the bound
workspace profile, stable instance and scoped sessions. It must never rename a
same-name global agent or unrelated sessions. Do not satisfy a legacy API by
silently adding the imported profile to the global library.

### Initial canonical adapter slice

`personalassistant.SnapshotContinuityAgreement` and
`dailybrief.SnapshotContinuityConfig` enumerate through the shared SQL queryer,
not their store pools. Their explicit v1 DTOs have no runtime settings or pending
operation payloads. Typed record decoding also requires every non-optional
field: an omitted/null enable flag is not a recovered false choice. Missing rows
return absence, not default agreements or schedules. Modern assistant evidence
requires the exact workspace entry/profile, unambiguous provenance and supported
presentation version, with SQL identity and designation agreeing. Ordinary orphan
recovery remains unchanged.

The corresponding `RestoreContinuity*` methods require a caller-owned transaction
and receipt scope. The caller must roll back on any error. Only a new claim checks
current canonical ownership and inserts; an exact prior claim returns a no-op
before mutable identity checks, preserving later edits/deletions. The workspace
must itself have a receipt-owned canonical insertion, not just the same ID.
Assistant restoration additionally requires the operation's adopted-HQ action;
workspace-only imports must retain their incoming agreement without calling this
relationship writer. Designation remains a coordinator responsibility in the same
transaction. Destination relationship CAS starts at 1; source version/dates stay
in the portable evidence. First-assignment status alone cannot establish verified
completion or reconstruct a preview/apply journal.

Brief restore preserves source dates/config revision while bounding selected IDs
to the reviewed tree. Frozen `all` also honors the source update-time cutoff.
Scheduling and notifications start off; original enable/notification/scope intent
remains in the reviewed payload for the report and later activation review. This
is defense in depth, **not** the automatic-admission gate: first-open generation
does not consult the schedule-enabled flag. No production importer, worker or
runtime consumer is wired to these initial adapters yet.

### Physical-folder privacy prerequisite

Managed payloads exclude API/OAuth/Vault/provider-native credentials, MCP secrets,
webhook tokens, runtime/toolbox/filesystem approvals and native CLI autonomy flags.
Routine definitions and source enable/pause preferences may travel as inert
intent. Arbitrary user-authored transcripts, brief prose and uploads remain exact
private data; do not promise secret-free content or silently censor it.

Sanitized DTOs alone are insufficient for a directory-only copy. Existing
`agents/*/config.json` may contain `Settings.APIKey`, which native agent-specific
clients still consume; workspace MCP/skill `Config` maps can also hold secrets.
The new `PreparationRun.PrepareOnce` owns one fail-closed *attempt* under a
caller-held reset/operation permit: one admitted SQL view, bounded spool,
required domain coverage and physical inventory, source-root identity checks,
pinned-root pointer publication (a replaced path cannot redirect writes),
dirty-sequence/file fence, fresh inspection and SQL acknowledgement. Incomplete
collectors or physical children retain the previous generation but cannot report
Ready. This is **not** a scheduled production worker: no complete trusted source
collector set, lease composition, retry lifetime or readiness UI is wired.
Before source preparation can report Ready, those physical-file owners need an
explicit local-private/portable split, with native read/write compatibility and
reset coverage. Do not silently clear a native user's key, put it in a receipt,
or advertise a credential-bearing folder as prepared because its new checkpoint
DTO is clean.

The isolated private owner now exists: migration 65 adds installation-local
`workspace_local_config`, with encrypted agent/binding DTOs and cascading
workspace deletion. Its AES-GCM key is held by the existing installation secret
backend; there is no plaintext fallback. Ciphertext binds workspace, item, slot
and attachment authority. Native authority and `import:<operation-id>` occupy
separate namespaces, so even an operation named `native` cannot reuse native
configuration. Copied references alone never grant access. App-record reset
removes the private rows and admission while retaining encryption material;
retained files cannot reconstruct those connections or grants.

Agent projections explicitly deny web search, native MCP and cloud fallback;
binding projections remove opaque configuration/scope, trust, classifications,
runtime grants/readiness and native CLI opt-in. An empty MCP allowed-tools list
must serialize as `[]`, not disappear into legacy nil/all-tools semantics.
Authored content, identities, enabled intent and mode selection are preserved.
Native migration stages encrypted data before conditional file publication and
never silently strips the only usable native configuration on backend failure.
It also checks unused canonical agent profiles, not merely current instances.

`NewFileStoreWithLocalConfig` is an opt-in canonical composition, not an admission
mechanism. Its writes, scoped snapshots, rename and folder-move reads use the local
owner; unconfigured writers refuse marked files. Private-aware `SyncStore` and
its SQLite adapter share one snapshot owner without plaintext shadow copies or
fallback after private-read errors. Legacy live documents keep their existing
size semantics until preparation. Separated live workspace documents have an
independent 256 MiB bound; the checkpoint's 8 MiB file bound can make preparation
unavailable without blocking ordinary larger live saves. Native streaming reads
are not untrusted import decoders.

This is still **not production restoration**. The server builder, authenticated
native inventory/creation, global-library boundary, complete move/rebase paths,
existing-profile appearance migration, runtime consumers and preparation/reset
lifecycle are not yet fully composed. The helper does not establish whole-tree privacy, publish a checkpoint
or grant Ready. Configured file writes now hold durable mutation barriers, and
SyncStore retains an enclosing barrier through the SQL-primary write and rollback.
The worker still needs complete multi-file/topology coverage, crash reconciliation
and publication orchestration, not merely these hooks.

The isolated profile adapter now emits an explicit settings/definition DTO and
preserves manual-disable intent, but excludes keys, live status, statistics and
rewards. Profile/appearance receipt IDs bind workspace ID plus canonical profile
slug; same-name profiles in different workspaces cannot collide. Uploaded images
(including inactive retained choices) use scoped owned bytes and explicit missing
state, not a global-name fallback. The `agents` component can carry bounded image
objects; its domain adapter additionally enforces the canonical 5 MiB image limit.
New native snapshots in the private composition now seed their exact global
owner's uploaded bytes under the workspace profile, including retained inactive
uploads. They refuse workspace-roster fallback, changed definitions, links and
existing image collisions. A durable enclosing barrier covers image publication,
profile/key separation and cleanup. Existing local profiles are not refreshed
from global names; legacy missing-image recovery still needs independent evidence.

Scoped serving uses `/avatars/<file>?studio_id=<id>&agent=<name>` and refuses all
global fallback, malformed scope, unknown definitions and unsafe image bytes.
Responses are private/no-store. Separated profile API projections carry
`appearance_workspace_id`; workspace detail/Command and agent detail forward that
scope through the shared avatar renderer. This is not complete roster/HQ/editor
integration or evidence that production uses the private persistence constructor.

The workspace adapter fingerprints canonical bytes, preserves original work/dates
and numeric user data, separates executable external references, and invalidates
interrupted process state without invoking templates. Receipt-owned SQL insertion
uses no creation defaults or upsert. Shared-view registration capture preserves
SQL-owned color and compares every mirrored JSON field, scalar status/kind/order,
and ticket state; private bindings are compared through denied projections. A
file-only descriptor cannot fabricate SQL metadata. This is still not the final
publication fence: it does not orchestrate mutation ownership, folder publication
or execution admission.
Exact prior receipt claims remain no-ops after workspace deletion detaches the
member; detachment still forbids every new claim. Reset removes those receipts.

Private collection spooling bounds in-memory records to one chunk and streams
owned blobs into an installation-selected private temporary directory. Missing
adapters are explicitly unsupported, never empty. Collection errors/cancellation
poison the generation. Cleanup deletes only known objects and fails on unknown
content. A future worker must own crash cleanup/reset inventory and retain its
permit through cleanup before acknowledging readiness; this helper is not that
worker.

## Versioned format and limits

Reserved directory: `<workspace>/.ori/continuity/`, independent of other `.ori`
sidecars. JSON wire keys are snake_case, UTF-8. Format version is integer `1`.
A writer owns this reserved subtree and its precisely named, private
`.ori/.continuity-init-<uuid>` initialization scratch. Scratch holds only the fixed
`format.json` marker, never history/configuration; exclusive directory rename
publishes initialized ownership without overwriting a pre-existing directory.
Unpublished scratch is incomplete-modern evidence. Never replace a non-Ori
collision or clean up arbitrary `.ori` files.

```text
.ori/continuity/
  format.json                     # reserved-subtree ownership/version marker
  current.json                    # atomic pointer: version, generation, digest
  generations/<generation>.json    # immutable manifest
  objects/<sha256>                 # immutable, bounded payload chunks / uploads
  staging/                        # unpublished, resumable writer scratch
```

Generation IDs are lower-case UUIDs; object names are lowercase SHA-256 hex.
Payload paths are derived from those closed identifiers, not arbitrary strings
from imported JSON. Never follow a payload path outside this root. IDs in records
are bounded opaque strings (1–200 UTF-8 bytes, no controls or path separators);
legacy incompatible IDs are a reported validation conflict, never rewritten.

Manifest fields: schema version, generation, owner workspace ID, checkpoint UTC
time, source revision/fingerprint, immediate child IDs, canonical file
fingerprints, per-domain availability and required version, bounded chunk
references/counts/bytes/digests, and unavailable-dependency counts/reason codes.
Exactly one descriptor exists for each supported domain, including explicitly
empty ones. Missing a modern descriptor/chunk is damage, not legacy absence.
The owner workspace must agree with `workspace.json` and every owned record.
Counts include records by family (sessions/messages/tools/uploads/follow-ups/
brief revisions), not merely chunk count. Sum with checked arithmetic.

Initial supported limits (exceeding any is a visible limit error, not truncation):

| Item | Bound |
| --- | --- |
| Current pointer | 4 KiB |
| Manifest | 1 MiB |
| JSON chunk / individual JSON record | 8 MiB / 4 MiB |
| Records per chunk / enumeration page | 500 / 500 |
| Chunk and blob references per checkpoint / canonical file fingerprints | 4,096 / 4,096 |
| Immediate children / reviewed tree depth / tree size | 256 / 32 / 1,024 workspaces |
| Canonical `workspace.json` / copied upload / total checkpoint bytes | 8 MiB / 256 MiB / 16 GiB |
| Inspection duration | context-cancellable; no provider/network reads |

Limits are format implementation defaults surfaced in errors. Validation must
bound allocation before decode, count, recursion and copying. Every retained
record within limits is included, including the 100-session/10,000-message test.
Large records never split halfway through JSON; use bounded record batches.
Chunk assignment is stable by owner/session and page, so changing one session
does not rewrite every transcript. Reuse verified content-addressed objects.

Read files through confined directory handles (`os.Root` on supported Go), reject
symlinks/non-regular files, and compare file identity/size around reads. Reject
traversal, absolute/backslash paths, reserved-path case/trailing-dot aliases,
duplicate keys/IDs (including case aliases for typed fields), extra JSON values,
invalid UTF-8/lone surrogate escapes, invalid schema/version, digest/count mismatch
and arithmetic overflow.
Inspection validates source bytes but never writes/migrates/trusts the source.
Do not use the existing unconstrained `workspace.copyDir` for this boundary.

## Consistent publication and retention

1. Enter reset work admission; acquire one writer per local workspace. Verify
   local attachment/disposition permits preparation. Read the durable dirty
   sequence and open a shared-DB read transaction for domain enumerators.
2. Capture canonical workspace/agent/memory/note fingerprints through confined
   reads. Enumerate complete domain records from the same DB view, page/chunk
   boundedly, and stream owned uploads. Validate ownership and cross-references.
3. Write new objects with exclusive temp creation, `0600`, file sync, rename and
   directory sync (`0750` directories where supported). Existing digest objects
   must verify, not be overwritten based only on their name. Write/sync the
   immutable manifest only after all declared objects exist and verify.
4. Recheck file fingerprints and dirty sequence. Changed inputs mean retry and
   Update needed, not Ready. A later concurrent mutation may make the checkpoint
   stale but may not be acknowledged clean by an earlier worker.
5. Atomically replace/sync `current.json` last. Readers accept only that pointer,
   the matching manifest and matching canonical files. A copied mixed generation
   fails validation; no fallback to an old manifest or legacy import success.
6. Reconcile publication with the local status record using compare-and-swap of
   the captured sequence. Remove obsolete manifests and unreferenced objects
   before advertising preparation complete. Resume cleanup after crashes; never
   retain deleted history in a hidden fallback generation.

Atomic publication is not a transaction over arbitrary external filesystem
writers. A copy during writes either validates a completed checkpoint or reports
incompleteness. Ready describes the verified completed checkpoint as of its time;
users should wait for it before copying. Retention/Forget cannot erase old copies
already made elsewhere, which must be stated in help and privacy disclosure.

Snapshot failure never rolls back durable live work. Durable dirty queues retain
pending retry across restart. Preparing/Ready as of/Update needed/Unavailable are
separate from empty data; report disk-full/read-only/error classes without private
content or arbitrary filesystem paths in telemetry.

### Local file-mutation barriers

Migration 66 adds reset-owned `continuity_file_mutations`, FK-owned by canonical
workspaces. `BeginFileMutation` admits only an existing local, undeleted, attached
owner; it atomically increments dirty state and records a pending token. Capture,
publication fences, readiness and acknowledgement all refuse pending OR failed
barriers. A post-write sequence alone is not enough: collection could otherwise
capture the pre-write bytes and acknowledge them before the writer's late rename.

Completion invalidates again. Success removes only that exact token; failure
remains a barrier even if ordinary SQL triggers clear the last error. A serialized
full replacement may supersede a failed attempt, never a pending writer. Crash
recovery must retain the canonical owner lock, installation lease and reset permit,
validate SQL/file agreement, and reconcile the exact token. Time passing, process
restart or discovering a folder never clears it. Late completion/MarkDirty cannot
recreate reset-erased state or consume another token after reattachment.

`NewLocalConfigStoreWithWorkGate` is the tracked composition. It holds finite
permits around encryption/staging, whole canonical file operations, SyncStore
primary writes/rollback and AgentSnapshotStore callbacks. Its untracked constructor
remains for isolated adapters, not evidence of reset coverage. The server builder
has not switched to it. Multi-workspace moves, all domain hooks and preparation
worker lifetime ownership still need composition before the feature can ship.
The old staging mover refuses separated workspace trees before moving them, and
the private FileStore refuses the legacy unreviewed Import path. These are safety
refusals, not a completed private-aware root-move or reviewed-import implementation.

### Follow-up history provenance (partial adapter)

Migration 67 adds a bounded keyset index, exact workspace-owner dirty triggers,
and a reset-owned installation-local provenance sidecar. The typed follow-up
collector uses the caller's shared SQL read view, not the HQ Today projection;
all six states, authored dates, nudge/due/snooze history and source fields are
snapshot evidence. A receipt-owned SQL insert can restore that record while
leaving external account IDs, dedup keys and unverified task links **out of the
operational columns**. The sidecar retains those original references so a later
checkpoint can combine them with subsequent local edits; copying an account ID
cannot connect or deduplicate a local source. Retry never rewrites a claimed
record. Neither this adapter nor its migration is a production source worker,
confirmed import, task-link resolver, account reconnection flow or Ready report.

The first brief-history collector likewise reads every exact-owner revision
under one shared SQL view rather than collapsing days; migration 68 dirties
revision creation, current-selection changes, and retention. Receipt-owned
terminal revision inserts preserve prose, dates and current selection and refuse
incumbent conflicts; pending/running source attempts fail closed until a
separate interrupted-evidence projection exists. Claim/notification evidence,
activation and ordinary historical navigation are not yet implemented.

### Explicit native provisioning

`BeginNativeCreation` inserts a new canonical SQL workspace through its owner's
transaction callback, together with a Native attachment and reserved
`native-create` barrier. The derived `Attachment.Provisioning` blocks manual,
automatic and preparation admission. Only installation-local configuration
persistence may use the provisional native authority. Generic Save, discovery,
import, `RegisterNative`, and generic `BeginFileMutation` cannot manufacture or
clear this creation state.

The canonical creator must retain its reset permit and writer locks through all
folder/profile writes and final validation. Completion/restart reconciliation
requires the exact token, a still-local live canonical row and no other pending
or failed file mutation; only then does the caller's shared-view validation run.
Failure/cancellation keeps the workspace non-runnable, not a silently completed
creation. No checkpoint is fabricated. `workspace.CreateNativeWorkspace` now
composes this protocol with the shared SQL owner, canonical FileStore and profile
snapshot decorator. It starts new files separated (no initial connector-secret
plaintext), requires every selected profile and its live private slot, and checks
native SQL fields plus final canonical file fingerprints before admission. It
refuses existing database or indexed folder identities, including retained copies.
Native validation uses the independent live-document bound, not the checkpoint
limit. Separation/hydration and marked-file clones preserve exact authored JSON
numbers, including IDs beyond float64's integer precision.

The workspace and orchestration HTTP creators, Home action, routed-chat creation
and new planned-workspace path use that explicit boundary. Production composition
is still incomplete: the richer session HTTP/template pipeline, project/station
provisioning and bounded upgrade inventory are outstanding. This is not a blanket
SQLite create hook and must not be added to directory-sync/import callers.

The configured SQLite workspace adapter preserves the FileStore's exact saved
version/timestamp instead of invoking HybridStore's ordinary fresh timestamp.
A regression exercises real SyncStore save → private separation → shared-view
capture; independently stamped SQL/file times otherwise made every ordinary
save fail the checkpoint consistency fence.

### Early execution checks (partial composition)

The canonical FileStore/SyncStore/profile decorator and direct SQL adapter expose
installation-local `ExecutionAdmission`. Compiled callers select manual versus
automatic mode; no portable enabled flag chooses it. Legacy unconfigured stores
retain their previous behavior. The policy distinguishes ImportedInactive manual
use from automatic work, refuses provisioning/unreviewed/restoring/detached work,
and checks live canonical ownership. It is separate from the whole-run reset
permit and from provider/tool readiness.

Task boot reconciliation and automatic task/workflow polling check admission before
mutating or resolving providers; dispatch rechecks before claims. Scheduler task,
mission, reflection and wake collection paths also check it. The explicit task
HTTP request and its worker use manual admission; first-open task dispatch uses
automatic admission. The template setup endpoint checks before stamping its
consumed marker, leaving blocked setup genuinely unconsumed and manually available.
Tests count zero mutation/provider calls and prove an allowed manual task still
runs with automatic admission denied.

Trigger consumers consult both the canonical folder owner and runtime owner.
Discovery skips executable definitions for unadmitted owners; automatic checks
precede watch-path probes/registration, webhook authentication/rate consumption,
debounce/pending claims and mission/task/domain-scan dispatch. Inactive definitions
remain manually editable as saved intent without starting a watch. Test Fire
exercises the automatic action pipeline and requires automatic admission; manual
task execution is a separate endpoint. A synchronous dispatch refusal returns an
error, not an older successful fire record. Runtime watch/debounce/rate keys bind
workspace plus trigger ID, and inactive copied tokens cannot shadow native tokens.
Revocation checks discard volatile windows, leave durable pending slots untouched
and remove watches during event handling/validation without rewriting source intent.

These are admission checks, not trigger restoration or grant migration. Legacy
`triggers.json` still needs confined/bounded private-aware persistence, local-only
tokens/secrets/path grants and durable mutation fencing. Import must preserve
history but neutralize interrupted fires, never feed copied pending commands into
native restart recovery. Activation must reconnect local sources and reconcile
watch/token registrations; deactivation needs full run/observer lifetime ownership,
not just the checks and periodic watch sweep implemented here.

This is NOT the complete runtime cutover. Chat/provider entry points, remaining
manual assist/review mutations, other watchers, briefs, setup retries, native
CLI/grant consumers, discovery/allowlists and lifetime activation races still need
composed coverage. The server must not enable the private composition until those
consumers and all creation/upgrade owners are wired. The optional legacy fallback
must not become a missing-forwarder bypass in a configured decorator.

### Read-only import entry boundary (partial)

The existing `/api/workspaces/import/check` now inspects the selected physical
`sub-workspaces/` tree and reports `legacy`, `review_required` or `unavailable`.
For each modern parent, its declared immediate child IDs must exactly match
verified physically contained modern children; missing, extra, legacy,
repeated identities or undeclared files in that child directory make the tree
unavailable rather than reviewed. Child enumeration is pinned beneath an opened
parent directory, never followed through a replacement external link. Each member
also needs exactly one typed workspace record bound to its canonical file bytes
and explicit SQL registration metadata; an empty, file-only or malformed owner
record makes the tree unavailable. A fully verified selected modern tree's
review totals bounded saved/source-labeled-completed/interrupted tasks,
collaboration messages, attachments, declared `files/` entries and inert
external references across root and physical children, never authored private
content. A source completed status is not yet verified setup/assignment completion
or a restored canonical result reference (task 3.6). Component chunks
are decoded one at a time against the inspected generation and rechecked
canonical file fingerprints; this read-only evidence is not a restore lease.
The review exposes verified checkpoint time, owner ID and component availability
for a modern **selected root** only; a modern child is indicated separately, not
represented as the selected workspace. A reviewable modern root also includes a
tree digest over each verified physical relative path, workspace ID, generation,
manifest hash and canonical-file fingerprint. A typed assistant candidate is
reported only when its exact workspace-owned entry profile agrees with the
denied canonical `agents/<slug>/config.json` and its typed agreement agrees
with that profile's provenance, entry instance and HQ presentation. An inline
key/default-allow grant, absent file, wrong instance or empty agreement on a
marked HQ makes the tree unavailable; same-name global roster entries never
provide missing evidence. `StageContinuityProfile` can materialize the same
verified scoped definition into private staging with all copied local slots,
keys, default-allow flags and runtime statistics denied; it will not install a
roster entry. The separate scoped asset stage binds a typed appearance to the
same verified profile, canonical image fingerprint and exact inspected blob;
missing files stay explicitly missing, and changed/linked assets fail closed.
Source scoped profile collection captures profile and present appearance bytes
under bounded spool/fingerprints and refuses a legacy inline key or unclaimed
files inside a scoped profile/appearance folder; it is not a production
preparation worker. The candidate verifier uses the same confined file check,
so a copied extra private file cannot be hidden by an otherwise valid manifest. The HTTP candidate now distinguishes no
uploaded image, present verified owned bytes, and explicitly missing bytes;
it still performs no import, adoption, or grant. An opt-in receipt-scoped relationship/knowledge profile
reader resolves the exact trusted workspace copy and refuses a same-named
roster fallback whenever a restored/unfinished imported attachment is in
scope; native relationships keep their previous lookup. This reader is **not**
yet composed into the production builder because private-store, import and
registration lifecycles remain incomplete. Moving
an otherwise unchanged child
changes it; mixed/damaged trees expose none. A future confirmation must compare
this value to a fresh inspection **and** a transaction-bound destination version.
The digest is not a MAC, trust proof, destination CAS, review receipt, permission
or filesystem lease. A separate SQL-only destination digest fingerprints the
local principal's HQ and relationship identity/version plus bounded current
workspace/attachment topology in one read view. `BeginReviewedImport` compares
it **within** the transaction that creates an inactive receipt, failing without
writes if SQL identity/topology changed; identical receipt retries keep their
original owner. The read-only HTTP check now returns both source-tree and
SQL-destination digests from one destination read view, but no confirmed submit
consumes them or invokes `BeginReviewedImport`. The coordinator must
also validate local filesystem/profile conflicts, reset state and the complete
source tree under the same reviewed operation; SQL-only CAS does not grant
adoption or cover changes to unclaimed rows during an incomplete retry. A
separate workspace-file staging primitive can materialize
the denied projection into an empty private directory outside the source tree.
It requires typed SQL evidence, keeps authored work, neutralizes interrupted
tasks, drops source external path grants and HQ designation, and checks source
consistency again after writing. A separate confined text-file step preserves
manifest-declared workspace `notes/` and `MEMORY.md` bytes in that same inert
private staging directory (up to 8 MiB per file), without following symlinks or
executing Markdown. Read-only review and the staging step now refuse undeclared
physical `notes/` entries, nested/linked notes and an unclaimed `MEMORY.md`;
staging checks the inventory both before and after copying. Read-only review
also checks the bounded physical `files/` subtree against its declared paths,
including nested files, and refuses unclaimed/linked entries and unrepresented
empty directories. Source-side bounded collectors fingerprint physical
one-level `notes/`, `MEMORY.md` and the streaming `files/` owner without
following links; `files/` fails closed on unrepresented empty directories.
These do not reconcile note SQL or claim a publishable generation. A separate
owned-file stage can
stream exactly declared `files/` bytes (up to the v1 per-file blob limit) into
the same inert private
folder, verifying content digest and refusing occupied or linked paths; it
rechecks the source tree and inventory after staging. Workspace, text,
profile, appearance and asset stages all write through their pinned private
root rather than reopening the staging path after its identity check; a path
swap may detach inert bytes but cannot redirect a write to a replacement
folder. Stages refuse changed input/linked stage paths or existing files.
These are not a whole-tree
inventory, installed asset publisher, operation lock or provider grant;
staging does not
reconcile canonical note SQL, interpret knowledge provenance, or
claim that all other folder-owned files/assets are included. That staging
directory is not registered,
trusted, import-owned, or executable; no coordinator yet supplies a durable
review or destination version, stages the other domains, publishes the folder,
or restores SQL. A test-only synthetic path exercises a real source SQL
workspace record through a directory-only copy into inert destination file
staging plus a receipt-owned SQL row; it deliberately cannot complete a receipt
or make the workspace visible as an imported product. It does not substitute
for reviewed HTTP confirmation, the preparation worker, or assistant/history
restoration. For source agreement collection, an ordinary workspace declares
an empty assistant component only after the shared SQL view shows no assistant
relationship/HQ designation and no assistant presentation in the workspace.
A presentation-bearing HQ with missing or unfinished agreement is unavailable,
not verified empty; contradictory identity is an error. This collector does not
prove exact profile/image publication or make an imported agreement runnable.
The old `/api/workspaces/import` endpoint rechecks and
refuses modern/damaged folders and a modern child before its legacy writes, even
with the duplicate override. The Import Folder modal shows this limitation and
disables its legacy submit action. Legacy-only folders retain their old path.

This is **not** a reviewed modern import: no coordinator, staging, restoration,
activation, receipt binding or cross-request source/destination CAS exists yet.
Filesystem replacement between this preliminary inspection and the legacy path
is not solved by the check. Do not label an `Inspect` success as adoptable or
allow the preview's source enabled flags/tokens to authorize runtime behavior.

## Destination receipts, review and restore

A local SQL coordinator owns attachment/admission, reviews, import receipts,
component outcomes and destination record ownership. None is restored from a
portable boolean. Tables must join the reset service's compiled reset inventory;
schema migrations alone are insufficient because reset rejects unknown domains.

Migration 64 introduces `continuity_dirty`, `continuity_operations`,
`continuity_attachments`, `continuity_components` and `continuity_records`.
Record claims and canonical domain inserts share a transaction. An exact owned
claim skips the insert on retry even if the current record was edited/deleted;
matching an ID without receipt ownership never permits an upsert. SQL dirty
triggers cover the initial workspace/assistant/config/designation owners, including
old/new owners and workspace detachment on deletion. Later-domain triggers belong
to their adapter slices. Reset deletes the compiled inventory in reverse order,
so `continuity_dirty` is listed first and cleared last, after canonical-delete
triggers run. Missing attachment state means unreviewed, never native permission.

The shared DB pool has one connection. `ReadSnapshot` enumerates through its one
read transaction and releases it before `Publish` calls a fresh dirty/admission
fence; calling the normal store/pool from inside that read transaction would
block. Readiness is a separate captured-sequence CAS after successful publication
and cleanup. A source snapshot revision is not a destination domain CAS version.

Read-only inspection returns tree, dates, counts, private-data disclosure,
missing/external dependencies, legacy/damaged distinction, destination identity
fingerprint and supported action. An opaque local review token binds the exact
source generation(s)/file digests and destination identity/version. It is not a
path authorization token or executable grant. Expiry, source change or destination
identity change requires a fresh review before mutation.

On confirmation: persist an operation/admission receipt **before** folder
registration/trust or any automatic consumer can see executable state. Copy to a
private staging directory using confined reads, revalidate bytes, then register
the same stable workspace IDs. Check ID collisions across every domain before
inserts. A component commits restored rows and receipt ownership atomically in
its domain transaction. Upload staging plus SQL outcomes require reconciliation;
never overwrite existing files or infer receipt ownership from a matching ID.

Restore ordering: admission/receipt → verified folder/instance identities →
workspace rows and exact scoped agents → session metadata/messages/tool history
and copied uploads, follow-ups, briefs/config/completion, knowledge/setup evidence
→ optional confirmed relationship/designation → derived indexes/cache rebuild →
completion report. Independent components may restore while another reports
failure, but dependent components must not advertise completion or adopt an
invalid identity. Domain restore methods accept validated records/operation ID,
not normal user create/upsert services that generate IDs, timestamps or events.

Cancellation before writes has no consequences. After writes it leaves an inert,
durable partial operation and Retry restore. Restart reconciles only rows/files
owned by that receipt, not arbitrary matching records. Never delete pre-existing
data or change the original external directory as rollback. Completion preserves
original source dates/IDs; new import dates belong to the receipt. Imported
unfinished tasks/turns/claims become interrupted/needs review.

Component outcomes: Restored, Already present, Not included in older folder,
Needs local setup, Conflict, Failed, Interrupted. History and adoption report
separately. Missing history never means verified empty. Unknown required versions
block their dependent restoration; optional unavailable components are explicit.
A damaged modern copy may offer reviewed independent workspace-only/partial
content, never silently downgrade to legacy. Reports remain reopenable by receipt.

## Local admission, discovery and reset

Local attachment states: native, unreviewed, restoring, imported-inactive,
imported-active, detached. Adoption disposition is separate: ordinary,
adopted-HQ, workspace-only-HQ. Native migration is limited to already registered
local DB/folder identities at upgrade, not every folder under the root. Startup
and root rescan must classify unknown folders before registration/roster backfill;
unknown modern and legacy copies stay unreviewed. Current `NewFileStore` already
runs slug reconciliation and index rebuilding, and `TaskExecutor.Start` reconciles
in-progress/assigned work at boot: admission must precede those mutations too.
Use an observational discovery/guarded constructor path, not the existing
mutating constructor as the read-only import inspector. An ordinary later save must not
replace workspace-only incoming agreement evidence with the incumbent's data.

Read/manual capability is separate from automatic admission. Explicit local
activation checks current dependencies/permissions and source pause intent;
warn that enabling here does not stop the source installation. Never resume a
source pause or restore old account/native-CLI/filesystem grants. Keep unrelated
native destination work running. Reimport of a completed receipt must not reset
activation or roll back local edits.

Consumers requiring the local automatic-admission check, before any read of an
external dependency, claim, side effect or enqueue:

- TaskScheduler, legacy schedules, mission cadence, assistant reflection and
  macOS wake candidate collection (`workspace/scheduler*.go`).
- TaskExecutor and StepExecutor polling, queued/unfinished work, orchestration
  event/delegation loops and plan auto-execution.
- Daily Brief first-open and scheduled generation, guarded at the service as
  well as lister/HTTP boundaries; manual generation is a separate request.
- Template-setup first-open (`sessionhttp/workspace_template_tasks.go`), before
  stamping the consumed marker, not merely at its downstream manual starter.
- Trigger startup/watch registration/webhook ingestion/dispatch, File Janitor
  watcher/daily catch-up and blueprint reintake automation. File watchers must
  not even open an imported external root before destination approval.
- Setup retry workers, directory sync and plugin/runtime discovery of workspace
  declarations; workspace capability/runtime native MCP resolution and grant
  checks remain destination-local even for an explicitly requested manual run.

Reset `WorkGate` is a process-wide reset fence, not a substitute for these
per-workspace controls. Snapshot/import workers enter before touching stores,
retain permits through final writes/callbacks, stop/drain before store close,
and never unfence a reset. New HTTP streams use revocable stream permits.

Application-record reset retains folder checkpoints but deletes local records
and attachment/activation authority. Its existing suppression booleans remain
set. Startup/backfill must leave retained folders/checkpoints detached and intact.
A later explicit review creates only that workspace's local attachment exception;
it does not clear global policy or reactivate other retained folders. Deliberate
workspace/private-data deletion and Forget instead invalidate/purge current
managed payloads and must not be treated as a restore request. Reset review must
say that private portable history remains in retained folders.

## Legacy and presentation contract

No `.ori/continuity/current.json` or generation evidence is supported legacy
absence. Partial modern staging/manifests without a valid current generation are
incomplete modern data, not a legacy success. Legacy files can provide verified
workspace/instance/profile evidence without brief configuration: confirmed adoption
may establish the paused identity and expose missing agreement/config/history as
Unknown/Not configured. Do not fabricate recovered defaults or weaken ordinary
orphan-recovery inspection. Missing/contradictory core identity still blocks it.

Reuse Import Folder. Show Restored work / Missing from this copy / Set up here,
private history/upload disclosure, oldest/newest checkpoint times, explicit counts,
progress/cancel/retry, and background-off notice. Successful adoption opens HQ with
no transient Hire/Build/Reconnect quest; workspace-only opens local history without
changing the incumbent. Sessions use normal listing/search/context/continuation;
brief history is accessible by workspace even when it is not the designated HQ.
Missing model yields Choose a model while records stay readable. Render imported
text as data; historical system/tool text is not current system policy or approval.

## Requirement and validation map

| Requirements | Contract sections / release evidence |
| --- | --- |
| FR-01–03 | Ownership, record allowlists, exact identity; real save → directory-only copy → fresh destination parity |
| FR-04–08 | Domain map, sessions/uploads, inert completion; every lifecycle/page/revision/claim preserved |
| FR-09–10 | External-reference limits, knowledge review/setup evidence; no profile overwrite or forgotten-fact revival |
| FR-11–15 | Dirty ownership, bounded chunks, publication fence/retention; copy-during-write, disk failure, move/delete and scale tests |
| FR-16–22 | Inspection binding, adoption conflict, receipts; ordinary/recursive/multiple HQ, same-name, retry-after-edit, ID-collision tests |
| FR-23–27 | Local admission and nonportable grants; startup/restart/first-open/automatic consumers zero-call assertions, explicit manual continuation |
| FR-28–32 | Legacy versus damaged components, partial receipts/reports; failure/cancellation after each durable boundary |
| FR-33–35 | Privacy, confined reads, codecs, domain restoration; hostile paths/counts/HTML, secret exclusion, original dates and zero notification/reward tests |
| FR-36 | Real app-record reset with retained prepared folder, restart detached, explicit one-folder import, unrelated folder still detached |

Characterization tests pin current omissions; they are not golden-path acceptance.
Later fixtures must use real source preparation, a fresh destination DB and only
the copied workspace tree, with unrelated and same-name sentinels. Include 100
sessions/10,000 messages, all follow-up states, multiple brief revisions/date,
copied/linked/missing uploads and interrupted work. Fake providers count zero
import-time calls and verify restored-session-only context on explicit chat.
Live-provider/plugin behavior is unverified and not required for offline parity.
