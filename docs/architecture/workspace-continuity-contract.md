# Portable workspace continuity contract v1

Status: implementation contract; not a claim that all adapters are implemented.
Requirements: `tasks/prd-portable-workspace-continuity.md`, FR-01–FR-36.
Source investigation baseline: `8e560c5e358e4d7dbfad2432865ee94ae87fe4e7`.

## Authority and consent

Existing domain stores remain the only live authorities. A completed checkpoint
is a private, offline reconstruction input, never a live database or permission.
Only a reviewed, confirmed import may restore it. Startup, changing the workspace
root, Rescan, opening Home, or finding a portable marker is not adoption consent.
Digests establish consistency, not authorship, authentication, or trust.

An import set consists of the selected directory and physically contained Ori
`sub_workspaces/` children, not referenced workspaces or external linked files.
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

Managed payloads exclude API/OAuth/Vault/provider-native credentials, MCP secrets,
webhook tokens, runtime/toolbox/filesystem approvals and native CLI autonomy flags.
Routine definitions and source enable/pause preferences may travel as inert
intent. Arbitrary user-authored transcripts, brief prose and uploads remain exact
private data; do not promise secret-free content or silently censor it.

## Versioned format and limits

Reserved directory: `<workspace>/.ori/continuity/`, independent of other `.ori`
sidecars. JSON wire keys are snake_case, UTF-8. Format version is integer `1`.
A writer owns only this reserved subtree; never replace an existing non-Ori
collision or clean up arbitrary `.ori` files.

```text
.ori/continuity/
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
| Chunk references per checkpoint | 4,096 |
| Immediate children / reviewed tree depth / tree size | 256 / 32 / 1,024 workspaces |
| Copied upload / total checkpoint bytes | 256 MiB / 16 GiB |
| Inspection duration | context-cancellable; no provider/network reads |

Limits are format implementation defaults surfaced in errors. Validation must
bound allocation before decode, count, recursion and copying. Every retained
record within limits is included, including the 100-session/10,000-message test.
Large records never split halfway through JSON; use bounded record batches.
Chunk assignment is stable by owner/session and page, so changing one session
does not rewrite every transcript. Reuse verified content-addressed objects.

Read files through confined directory handles (`os.Root` on supported Go), reject
symlinks/non-regular files, and compare file identity/size around reads. Reject
traversal, absolute/backslash paths, duplicate keys/IDs, extra JSON values, invalid
UTF-8, invalid schema/version, digest/count mismatch and arithmetic overflow.
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

## Destination receipts, review and restore

A local SQL coordinator owns attachment/admission, reviews, import receipts,
component outcomes and destination record ownership. None is restored from a
portable boolean. Tables must join the reset service's compiled reset inventory;
schema migrations alone are insufficient because reset rejects unknown domains.

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
