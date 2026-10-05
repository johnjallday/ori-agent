# Assistant conversation folder context

## Findings (task 2.1, inspected at `3ea4d340`)

- `personalassistant.FolderDigestService.scanSelectedRoot` both scans and writes
  the HQ offer sidecar, postponing a pending offer. It is **not** an attachment
  API. Its picker, known-chip resolution, root validator and metadata scanner
  can be reused without invoking this method.
- `folderdigest.Scan` reads directory entries and `Lstat` only. Defaults are
  depth 3, 5,000 entries and three seconds. It skips links and tooling/OS
  directories. Depth and unreadable subdirectories are coverage limitations
  even when the entry/time budget did not trip. Candidate names are root and
  immediate subfolders; this is not a complete tree or document inventory.
- Picked paths in the existing digest are process-local; chip paths can be
  reconstructed by the old offer path. Conversation selections must **not**
  reconstruct either kind after restart. The existing portfolio continuation's
  canonical-path plus directory-identity witness supplies the relevant pattern.
- Conversations are Sessions under Personal HQ, owned by the relationship's
  hired profile; Messages are the transcript. Ask creates a session only when
  storing an answered first turn. Session deletion cascades to Messages; shallow
  HQ deletion instead moves its Sessions to root (`ON DELETE SET NULL`). The
  hybrid store caches messages, and continuity imports mark historical rows.
- The route endpoint can choose a specialist before Ask sees a conversation.
  Ask separately intercepts generic create requests before its model path.
  Both boundaries therefore need an explicit, validated contextual path;
  adding text in the browser is not sufficient.
- The drawer and conversation module load from `layout/head.tmpl`; the existing
  folder card loads in the Home layout. A new composer controller must load
  alongside the drawer, not rely on Home's Today/card DOM.
- Generic setup uses digest Decide -> Creator/Linker -> persisted outcome.
  Specialized setup uses the reviewed plan digest, request receipt, journey and
  separate provider/root/staffing gates. Neither model prose nor a saved card
  is confirmation. An unrelated pending offer must remain untouched on attach.

## Implemented contract

### Local observations and authority

`FolderObservationService` reuses a digest service's trusted dependencies, but
never writes its sidecar while observing. Inputs are a known chip or native
folder picker mode, an opaque conversation/draft identity and expected revision.
Unknown request fields and HTTP paths/facts are refused. The handler resolves
user/HQ/profile and checks the canonical conversation before selection, then
rechecks after asynchronous work.

A selection stores in memory only: random observation ID, relationship binding,
conversation/draft target, canonical root, directory identity, bounded scan
result and expiry. At most 16 live selections per owner and 128 globally;
expiry is 30 minutes, with opportunistic pruning on access. Failed/cancelled
replacement leaves the previous choice intact. No model, workspace, memory,
provider, directory grant or permanent decline is produced by selection.

The path-free snapshot contains version 1, observation ID, root display name,
scan time, file/entry counts, at most 8 kind counts and 8 project/subfolder
summaries (observed counts and known marker labels only). Names are limited to
96 Unicode characters/384 UTF-8 bytes; the encoded snapshot is at most 8 KiB.
Counts never exceed the 5,000-entry scan budget. Coverage always states depth,
entry/time limits, skipped links and omissions; partial does not mean complete
otherwise. File bytes, absolute/relative paths, arbitrary filenames and directory
identity witnesses are excluded. Names are untrusted data, not instructions.

A saved snapshot is discussable, not a filesystem grant. Current authority is
checked independently. Lost (restart), expired and changed selections retain
their dated snapshot; no automatic rescan occurs. A new explicit pick is needed
for inspection or setup. Provider transmission occurs only on Send.

### Canonical persistence and concurrency

Use one narrowly typed optional `folder_context_json` column on canonical
Messages (migration 76), with Go `Message.FolderContext` excluded from ordinary
JSON input/output. Only a dedicated internal append operation writes that
column; normal AddMessage and continuity import cannot author it. Context rows
use the system role and are never replayed as system instructions. Assistant
reads project them as typed dated events in chronological order, not editable
chat replies or memory/backlog candidates.

The last locally authored context event's message ID is the opaque revision.
Each contextual turn atomically appends its event, user text and answer after
comparing the expected revision and canonical session owner. Detach appends an
empty event through the same compare-and-swap seam. A valid local replacement
also retires an already saved binding through an empty event, returning the new
revision: old reviews become stale immediately, while the replacement's new
metadata remains staged until Send. Cancellation/failure preserves the previous
binding. Old events remain history.
The SQLite transaction is authoritative, not the LRU; the hybrid adapter
invalidates cached sessions after writes and reads context state from SQLite.
Deleting a session uses the existing message cascade. Migration 76 additionally
clears typed context when a session's workspace/profile changes, including
shallow HQ deletion; ordinary chat remains. Renaming also drops the binding and
requires re-picking rather than carrying authority to a newly named owner.
Failed first-turn storage discards the newly created session. No new
transcript/sidecar exists.

Before the first accepted turn, selection remains staged in server memory and
the tab. A draft identity cannot be silently reused for another conversation.
First-turn storage associates its selection with the created conversation only
after success. Model/save failure retains input and staged selection. A lost
response is not retried automatically; resume canonical history to reconcile.
Requests against an existing conversation compare its revision **before** a
model/consequence, then again on persistence. Same-conversation folder requests
are serialized in-process; stale tabs receive a visible conflict.

Continuity and generic session JSON retain ordinary chat, not active bindings or
folder events in this first version. Imported prose is existing untrusted
history. No import/reset/read path recreates a live selection. Omitted folder
fields keep legacy text-only behavior; a supplied invalid reference fails
closed. Existing history limits remain 40 messages / 24,000 characters with
6,000 characters per message; folder snapshot adds at most 8 KiB per turn and
is never reconstructed by parsing prose.

### UI and routing

One Add folder chooser in the composer on every personal-drawer page; one chip
and local preview. Cancel keeps text and prior context. Each async request
captures conversation/draft identity, revision and UI generation; late responses
cannot replace a new conversation or choice. No path is stored in the browser.

Send displays any attachment-only default request (`Explore this folder`) and
carries only opaque references. The validated personal-folder route bypasses
specialist/generic-create detection, not intentional workspace routing. Reviewed
memory/backlog actions retain their own gates. Prompt context is escaped,
delimited reference data with explicit contents/coverage limits and historical
status. A provider cannot invent trusted setup buttons.

### Optional setup handoff

Only explicit Review workspace setup mints a canonical digest offer. The handoff
checks current conversation revision, selection owner/expiry/directory identity,
and explicit candidate (never silently the first of several; the whole root is
labeled separately). Candidate paths
come from the held scan, not HTTP. It refuses an unrelated pending offer rather
than postponing it; retries reuse the same linked offer. Attachment-bound offers
carry server-owned conversation/observation/candidate provenance, not paths or
permissions. The current canonical offer reference is checked under the same
in-process lease as folder turns and mutations. Detach/replacement makes
unconfirmed reviews stale.
Completed outcomes are not undone. The existing creator, reviewed plan digest,
receipt and provider-specific gates remain the sole execution path. Canonical
outcome references are read back in the owning conversation; hydration executes
nothing.

## Implemented contextual turns (group 3)

Route and Ask independently validate supplied references before specialist or
create detection. Ask holds an in-process conversation gate through provider and
atomic canonical storage; the store rechecks owner/revision after generation.
The actual provider prompt receives an identifier-free JSON projection, escaped
inside a reference-data delimiter. Content-reading requests get an explicit
metadata-only refusal without a provider call. Ordinary text-only callers retain
their existing behavior.

An explicit historical turn can use only the exact active canonical snapshot;
it neither resolves nor identity-checks the source folder. Hydration reports
lost/expired/changed authority separately. Detach prevents even historical
reactivation. Failed first saves discard the newly created Session where
possible; transport uncertainty asks the user to reopen history, never retries.
The drawer hydrates chronological typed events and deduplicates unchanged
observations without conflating them with assistant replies or message actions.

Real-host browser evidence uses the production Ollama HTTP adapter against a
controlled loopback provider, including a server restart. Maintained browser
controller tests explicitly fixture selection/Route/Ask/history; race-enabled
handler tests capture real provider request construction and canonical writes.
Neither is a live LLM or native-picker smoke.

## Implemented optional review (group 4)

`POST /api/home-assistant/folder-context/review` accepts references only. It may
save an initial event-only conversation without a configured model. The returned
offer is unusable until its reference is canonically stored and staging is bound.
Failed writes close newly proposed sidecars and discard newly created Sessions
where possible. Reads project at most sixteen unique recent review references.

`POST /api/home-assistant/folder-context/review/close` retires the canonical
reference before closing the sidecar. Keep chatting is neither No thanks nor
Later: it creates no preference, tombstone, workspace, task, or learned memory.
Outdated pending reviews can also be closed without reauthorizing their source.
For a deleted or moved conversation, explicit closure checks the sidecar's
original user/HQ/profile provenance, then retires only that orphaned sidecar.
A read failure is not proof of deletion, and an empty revision cannot close a
live active review. No foreign Session is edited. This prevents an orphaned
pending offer from blocking all future reviews without resurrecting authority.
An unrelated pending review is refused, never displaced.

The existing card is moved into its canonical review slot, never cloned. It is
parked before history reset/trimming. Repeated observations are not repeated as
new model answers; setup events have compact review headings. Old slots retain
closed-review explanations or persisted receipts. The composer permits explicit
candidate selection, generic workspace-name adjustment and cancellation.
Generic cards disclose their actual blank/blueprint type, linking, first-task
and remembering effects. Future file reading belongs to the confirmed
workspace's separate permissions, not this metadata conversation.

Generic confirmations bind the offer/observation/subject/type/remembering state
through a review digest. Specialized setup retains its existing plan digest,
project-file selection, provider availability, root-grant and staffing gates.
Both use the existing creator/runner and receipts. Name adjustment does not
rename the linked source label. Confirmed work may continue after detach, but
its owning conversation and exact trusted review must still exist; deletion,
import and ownership changes cannot authorize continuation.

Process-local selections now record whether they have ever been saved. A
consumed ID is accepted for discussion or a new review only when it is still
the active canonical snapshot. Detach, import, or moving a Session away and
back cannot make an old ID look like fresh staging. This marker is not a grant
and does not survive restart.

The shared head loads the work controller and card styles once; every drawer
host has the same single card mount. Settings and other non-Home pages can send,
rehydrate and review in place, not only show a local preview. Late setup replies
and polling cannot replace another conversation's review.

Home toolbar, mission and legacy folder links delegate to the same composer
chooser. First-folder guidance is not a fabricated exchange. The existing
portrait animation is reused only while local selection is pending, followed
by bounded preview facts, not an automatic setup offer. New Workspace remains
a separate flow. Real-host/no-model browser tests and the updated folder-first
demo exercise canonical review, cancellation, creation, replay and receipts.

## Dependencies and evidence limits

Host-only: no plugin manifest, blueprint, release or pin changes are required.
Existing provider availability is checked rather than invented. Generic folder
fixtures prove the base path; provider/native-picker smokes require separately
available sandbox prerequisites. Local evidence is under
`tasks/evidence/assistant-chat-folder-context/` (`folder-chat/`, `folder-setup/`
and `folder-first/`). Live vendor-model and native-picker checks remain NOT RUN;
conditional music-provider browser suites also require their own sandbox
configuration. The checklist records actual validation separately from design.
