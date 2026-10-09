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
  immediate subfolders. Explicit attachments additionally capture a bounded
  metadata tree during the same walk; neither projection is a complete inventory.
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
otherwise. Explicit attachments may additionally contain a genuine bounded tree
of recorded file/folder display names and parent relationships. File bytes,
filesystem paths and directory identity witnesses are excluded. Names are
untrusted data, not instructions; Send discloses metadata sharing with the
configured model.

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
is never reconstructed by parsing prose. Optional version-1 tree/focus fields
use those existing envelopes; they require no migration or new preference.

### UI and routing

One Add folder chooser in the composer on every personal-drawer page; one chip
and local preview. Cancel keeps text and prior context. Each async request
captures conversation/draft identity, revision and UI generation; late responses
cannot replace a new conversation or choice. While choices load, Cancel already
owns keyboard focus so Escape closes only the chooser. Closing the drawer or
resetting context invalidates late choice loads; selection restores Add folder
focus only when the user has not moved elsewhere. No path is stored in the browser.

Send displays any attachment-only default request (`Explore this folder`) and
carries only opaque references, including optional next-turn focus node IDs. The validated personal-folder route bypasses
specialist/generic-create detection, not intentional workspace routing. Reviewed
memory/backlog actions retain their own gates. Prompt context is escaped,
delimited reference data with explicit contents/coverage limits and historical
status. A provider cannot invent trusted setup buttons.

### Compact folder reply and discussion choices

Local preview and canonical events share a text-node-only presentation helper.
The heading is the folder, followed by a localized snapshot date, bounded/partial
or saved-historical status, and **Attached folder: contents not read**. That
statement concerns the attachment, not other separately authorized workspace
sources on the turn. At most three non-root observed folders/markers appear
initially; a native disclosure reveals the remaining recorded rows. The root
is not an independent project total. Omitted summaries have a visible warning;
Show more cannot recover them. Scan details contains counts, overlap explanation,
precise time and coverage/marker limits. Replay retains chronological placement
and unchanged saved prose, with one factual card per unchanged observation.

The folder prompt adds a short interpretation rather than a repeated inventory.
Initial exploration targets two or three sentences, normally about 80 words or
less; explicit questions and detail requests take priority. Follow-ups avoid a
new introduction or repeated setup pitch. A locally authored typed event/user/
answer triple for the same snapshot qualifies only when both chat rows are in
the actual bounded provider history. Internal events use the system role with
version-1 typed context; viewer events serialize as `folder_context`. Transient
history message IDs establish eligibility, not a browser flag or model prose.
Text-only callers still get relevant facts. This is guidance, not a response
schema, renderer truncation or cross-model quality guarantee.

One current strip offers whole-folder discussion and, when there are observed
children, an explicit local chooser. It binds the successfully saved assistant
row to its conversation, observation, revision and UI generation, rechecking
ownership and DOM presence on activation. Imported/legacy or unsaved prose,
event-only reviews, detached context and trimmed-away answers cannot create it.
Saved snapshots use saved-observation wording; lost/expired authority uses only
the existing historical request path. No pick, rescan or setup is revived.

Discussion candidates come from all typed non-root observations, independently
of setup compatibility. Names and markers remain literal text. Identical
names/markers cannot identify distinct targets and are disabled rather than
silently resolved by opaque IDs. The chooser changes neither attachment nor
persistent scope; it prepares a plain-text question only. A dedicated
`suggestReply` entry fills an empty composer within its UTF-16 maxlength, never
cuts a name or overwrites/appends to an existing draft, focuses and announces
**Review, then Send**. Cancel/Escape returns to the trigger. Only ordinary Send
transmits references through the existing validated route. Other `prefill`
callers keep their original semantics.

Ordinary chat retains one transcript viewport and pinned composer. Native
headings, lists, disclosures/selects, labelled 44px controls and polite statuses
preserve keyboard order. Reflow recovery scrolls only the relevant assistant
pane, without changing existing saved-draft/memory focus lifecycles.

### Bounded Tree + Chat exploration

`CaptureTree` is attachment-only: the existing metadata walk records at most 64
real entries, opaque `entry-N` IDs, kind and actual parent ID. Files at the last
visited directory level can have four name segments under the existing depth-3
walk. Background digest scans remain tree-free. Exclusions, links, entry/time
limits and unreadable-directory behavior are unchanged; no second scan or file
reader is added. Entries whose parent was omitted are omitted too, not falsely
reparented. Sanitization and a parent-before-child prefix trim keep the snapshot
inside 8 KiB, with declared omissions. Held evidence is cloned on return/resolve.
Legacy snapshots get no invented hierarchy.

After a successful explicit attachment with a valid tree, the existing drawer
opens Tree + Chat. Desktop has independently scrolling side-by-side tree and
transcript panes; narrow layouts offer Tree/Chat controls, initially Tree. There
is still one conversation, form, textarea and pinned composer. Back to chat
collapses only the layout, preserving attachment, exact draft and next-turn
focus. A real tree can be reopened locally. Replies, reload and failed/cancelled
selections do not automatically reopen it. Expanding or checking reads/sends
nothing; coverage details retain scan time, bounds, omissions and exclusions.

Independent native checkboxes choose up to eight distinguishable discussion
topics. Checking a folder never checks its descendants. Empty focus means the
whole recorded folder, not additional permission. Focus is neither a file-read
grant, descendant scope nor a privacy filter: the bounded observation remains
shared on Send. Ambiguous sanitized names are disabled. Expansion and next-turn
focus are ephemeral; New/reload/replacement/detach clear them, while cancellation
preserves them. Existing textual shortcuts clear focus only after a draft is
successfully accepted; they cannot overwrite an existing draft.

The host resolves submitted IDs against the exact live or canonical historical
snapshot before provider calls or writes. Missing, duplicate, foreign, ambiguous
or oversized focus is rejected. Resolved topics contain names as segments and
kinds, not filesystem paths or opaque IDs; their encoded bound is 4 KiB. The
provider receives escaped entry relationships as parent indices and explicit
current discussion focus. Earlier focus is labelled untrusted historical data,
not current instruction. The metadata projection is bounded to 16 KiB.

The atomic event freezes optional `focus_ids`. Only validated, locally authored
system-event/user/answered-assistant triples produce server-projected
`folder_focus` badges on canonical user rows. Sent badges remain immutable on
live/replay views; changing checks during a reply affects only the next message.
Imported prose and model output cannot fabricate badges; legacy tree-less
snapshots cannot supply specific entry topics.
Saved snapshots remain discussable without rescanning or reviving setup/read
rights. Tree focus never supplies a reviewed-setup candidate or confirmation.

### Optional setup handoff

New setup options are optional background, not a required recommendation or
final paragraph. A lone compatible child must not become the preferred scope
merely because other observed children cannot be set up. Existing review state
remains separately discoverable even when no new options are available. A read-only
`ReviewOptions` projection reuses candidate preparation and capability/plan
availability from the existing setup service. It does not rescan, allocate an
offer, or execute a journey. Names and supported workspace types reach the model
as bounded escaped data without candidate IDs or paths. Unavailable, declined,
historical, or pending-review cases have no suggested action.

A successfully saved metadata turn can return `folder_setup_suggestion`, bound to
its conversation, revision, observation and canonical assistant-message ID. The
reply shows secondary **Optional: review setup**, after discussion choices,
opening the existing candidate selector; multiple scopes start unselected. The
suggestion is not persisted authority: reload projects it only for the latest
locally authored atomic folder turn and rechecks availability. Imported prose, unsaved/model-failure replies, content
refusals, replacement/detach, closed reviews and old answers cannot revive it.
Clicking still uses the existing explicit review endpoint and confirmation gates.
The composer **Review setup** entry remains available for direct/no-model review.
An existing review retains its status and **Show existing setup review** action;
discussion choices never supply its candidate default or confirmation.

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
configuration. Final delivery evidence is in `final/README.md` under that local
archive. Full CI, same-environment README capture, controlled-provider browser
captures and scoped accessibility checks are distinct from those unrun live
integrations. The checklist records actual validation separately from design.

Folder-response UX evidence is separately indexed under
`tasks/evidence/assistant-folder-response-ux/`. The maintained real-host scenario
is `python3 scripts/assistant-workspace-demo.py --folder-response`; it uses the
actual selection/Route/Ask/provider adapter/atomic history/reload path with
synthetic unread-content sentinels and scripted loopback prose. Browser fixture
stress checks cover stale identities, failed/delayed turns, literal names,
Chromium accessibility semantics, themes and reflow. These do not prove vendor
model concision, native picker use, native browser zoom or assistive-technology
behavior. The `--folder-response-evidence-stage explorer` variant additionally
checks genuine tree relationships, independent focus, exact drafts, immutable
sent/replayed badges and collapsed reload. Its source/run evidence is separate
from the original compact-response validation. See [folder-first manual testing](../testing/folder-first-manual.md)
for the separate discussion and explicit setup journeys.
