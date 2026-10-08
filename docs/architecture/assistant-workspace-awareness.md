# Personal Assistant workspace awareness: investigation contract

**Status: host implementation complete on its feature branch; not merged.**
Context-following conversations (group 2), setup continuity/placement (group 3),
grounded notes/tasks with checked sources (group 4), permitted file reading
(group 5) and final cross-slice validation (group 6) are implemented. Vendor-model
behavior, live REAPER and release verification were not run.
The full feature includes context-following conversations, setup continuity,
notes/tasks, and permitted files. A context badge alone is not the delivery.
Implementation proceeds in that order with one writer. This document records
non-sensitive decisions; local runs/screenshots and exact source inventory are
in `tasks/findings-assistant-workspace-awareness.md` and its evidence directory.

## Characterized baseline

The original `TestAssistantWorkspaceContext_BaselinePendingReviewMissingFromModel`
characterized production Route/Ask, relationship, file/session stores, metadata
observation, and review. Its current successor is
`TestAssistantWorkspaceContext_CurrentWorkspaceWithPendingReview`. It creates a generic group with Album-1, a second project, and an
unlinked Album-5 folder. Only the configured model is an inspecting stand-in.

- A live pending review is in canonical conversation hydration, with a digest.
- The accepted folder turn retains its offer but supplies `[]` for *new* setup
  options. No existing review summary or current page workspace reaches the
  model. The existing prompt says an empty list means no suggested review.
- Navigation references do not move conversation ownership out of Personal HQ.
- Metadata selection does not deliver a sentinel file body or absolute path.
- No creation/confirmation runs on Send or hydration. Model input has no native
  execution workspace, directory, MCP server or execution scope.

Group 2 replaced the current-workspace absence assertion with the validated
projection. Group 3 supplies a separate bounded canonical review projection:
empty NEW options no longer mean no existing review. It also binds the exact
named destination, resolves placement, and gives the drawer the same review
status the model receives (see below).

The real-host browser fixture in
`tests/personal-assistant-workspace-awareness.spec.ts` captures Route/Ask payloads
and the pending card without a model. Set up is initially **enabled**; holding a
confirmation response makes it disabled/faded, then it re-enables. This proves
one busy-state path, **not** the original screenshot's cause or specialized
REAPER setup. The busy text originally used the observation root (Documents)
rather than selected project (Album-5); group 3 now names the reviewed subject.

`tests/personal-assistant-workspace-baseline.spec.ts` separately stages the exact
compatible Music 0.1.1 and REAPER 0.9.0 candidates in disposable HOME/data. The
host creates a genuine Music-owned Home, a physically grouped Album-1 fixture
(not a staffed/project-linked child), and a pending recognized Album-5 review.
On `/workspaces/{slug}/assistant`, the primary action is **enabled**, opacity 1,
but paper-colored text has a transparent background: `--hc-signal` is absent.
`home-command.css` defines that token only on the Home dashboard body, while
`.pa-specialist-offer .btn-primary` consumes it on all drawer hosts. This
reproduces faded styling, not a disabled/expired permission gate; the original
uncaptured screenshot is not asserted to have the identical condition.

The original standalone `workspace-assistant.tmpl` omitted shared DX utilities,
so Send failed locally before Route/Ask. Group 2 now loads that dependency exactly
once. The exact-candidate browser reaches real Route/Ask with canonical ID/slug
references and the review/source/workspace state unchanged. Group 3 supplies the
missing drawer-local signal tokens; the exact-candidate regression confirms an
enabled primary button with a non-transparent background. Before/fixed evidence
is preserved separately. No vendor model, confirmation, project runtime or
live REAPER runs in this specialized check.

## Current group-2 implementation

- `assistantcontext` supplies data-only overview and bounded turn-attribution
  DTOs. `POST /api/home-assistant/context` refreshes canonical metadata without
  loading profile/memory, scanning a directory, calling a model or storing a turn.
- Route validates panel references; Ask independently freezes canonical location
  and subject IDs at acceptance. Ordinary project questions remain hired-assistant
  conversations; explicit specialist/execution intents retain their existing
  gates. Workspace browsing never sets native CLI execution fields.
- Per-turn panel registries recheck user/relationship and pinned workspace ownership
  before each read. The existing home metadata tools receive owner/subject-filtered
  stores; separate discovery includes groups and preserves labeled project/group
  totals. Notes, task details and validated citations arrive in group 4 and file
  contents in group 5 (both below).
- Provider capability controls tool advertisement. Snapshot-only paths keep the
  validated overview and explain the lack of brokered readers; no provider switch
  or native-MCP workaround is used.
- Migration 77 adds optional `messages.turn_context_json`. The canonical atomic
  writer saves user/answer attribution with optional folder observation, checks
  session owner, relationship version and workspace ownership inside the same
  transaction, and retains the existing folder-revision CAS/cache invalidation.
  Generic message/JSON/continuity inputs cannot author this field. Hydrated context
  is historical data, never new authority; missing old attribution remains unknown.
- The drawer's live context line refreshes on opening and navigation. Per-reply
  labels use accepted server attribution, not the current page. Tab-local draft
  and drawer-open state survive full navigation; attachment/review identities
  remain independently canonical.

Real-host browser evidence covers A→B before Send, in-page back/forward,
navigation during a held real Ask response, draft retention, reopening and
app-wide clearing. The complete group-2 demo additionally uses `wt demo` and the
production Ollama adapter against a loopback-only deterministic provider. It
holds actual generation at the provider while navigation changes A→B, then
reloads canonical saved user/answer attribution, the same pending review and
unsent draft. Legacy rows remain `Earlier workspace unknown`. App Home group
selection is a browsing reference without changing legacy execution targets.
Run `python3 scripts/assistant-workspace-demo.py --port 8954`; fixture-only tests
are `python3 scripts/assistant-workspace-demo.test.py`. Both child server and
sandbox are cleaned; vendor credentials and Codex home are not passed through.
The server/provider fixture separately verifies current pinned reads and
revocation during generation. These are deterministic host/adapter checks,
not configured-vendor-model proof. Review projection/placement and substantive
source readers remain the following groups' work.

## Group-3 review projection, placement and reuse

Every conversation model path receives the latest local canonical review, even
without a live folder reference on that request. Imported prose/events cannot
select one. Detached/retired references remain discussable but carry no control
labels; foreign/missing/failed reads are state unavailable, not no proposal.
The projection distinguishes pending confirmation, running, stopped/choice,
completed with a receipt, closed and declined. It excludes IDs, digests, paths,
URLs and internal error text, escapes reference data and bounds serialized JSON
to 8,000 Unicode characters. Hydration supplies the same summary status alongside
the existing canonical card.

**One status vocabulary for the model and the drawer.** The Ask response and
hydration both return that projection as `folder_review_context`, so the drawer
shows one sentence (`#personalAssistantFolderReviewStatus`, a polite live
region) built from the facts the model was given for that turn. The closed list
of statuses is `internal/agenthttp/testdata/review_status_vocabulary.json`. A Go
test asserts the projection produces exactly that list; a JS test asserts the
drawer has a distinct sentence for each and treats an unknown status as "could
not be read", never as "no review". The sentence names the reviewed destination
only when it is disclosed, and says why a control is blocked (stop reason,
changed or unreadable destination, folder access needed again). When the card
later reports a different state, the earlier sentence is dropped so the two
never disagree.

A review pending in another conversation is `pending_elsewhere`, distinct from
unsupported setup. This also holds for a conversation that is not saved yet,
which previously read as "state unavailable". The model gets the subject and the
label *Open existing conversation*; only the drawer gets the owning conversation
ID (`folder_review_elsewhere`), which opens that conversation and cannot review,
confirm or resume anything.

Existing reviewed Homes now have a separate metadata-only destination reader,
independent of software/model prerequisites. An explicit Review pins the canonical
Home ID/name/kind/parent/owner, record/program revisions, typed provider evidence
and a declaration/compatibility hash in the canonical offer. Read/hydration never
updates that witness. Model context receives only its safe name/kind/status, not
IDs, owner identity, hashes or confirmation digests. The same named relationship
is visible on the card even if a one-click plan is unavailable.

Plan digests bind the destination witness as well as disclosed effects; generic
review digests also bind it. Fresh confirmation and resumed-run guards reject
changed IDs, names, parent, owner, provider or declaration. Resume ignores record
revision bumps caused by the run's own progress, not material witness changes.
The project runner checks the prepared Home ID/name and project review's parent
name, and freshly validates the material destination before each owner
review/commit. Canonical failures do not fall back to a stale file-store Home.
Destination changes/unavailability retain the old disclosure and remove setup
controls; an explicit pending Review refreshes the witness. Re-picking a confirmed
or completed subject preserves the original witness instead of retargeting it.

Versioned explicit Review now resolves placement from canonical browsing references.
A supported group proposes a separate child; a project first offers supporting
folder versus separate project. Selecting an operation only prepares its review.
A mismatched managed Home produces a targeted named decision, never adoption or
reparenting. Existing exact-candidate refresh follows its persisted operation and
destination even after navigation. Another conversation's verified review produces
an Open existing conversation handoff rather than a duplicate offer.

Supporting-folder confirmation adds only a purpose-empty directory reference,
under a pinned project witness and canonical review lease. It does not replace the
primary directory/entry, blueprint, mode, roster, tasks or source bytes. Operation
and destination participate in reuse and digest identity. Generic group creation
uses the ordinary creation pipeline's parent reference; canonical outcomes retain
a verified resulting-parent witness and receipt. Completed projections prefer that
receipt over an earlier new-Home promise.

New-Home disclosure reads the same trusted managed catalog as the group reviewer
without minting a token. Missing/ambiguous declarations remain unavailable, not an
invented name. New consent binds typed provider/declaration evidence and the exact
proposed name. Project creation checks that name and records the created parent ID
from the canonical journey; portfolio creation records its own committed ID.
Resume never adopts a Home merely because its name or creation time matches. The
portfolio runner freshly fences Home, staffing, library, consent and receipt
operations, including each review-to-commit boundary. A creation that loses its
parent progress receipt fails closed rather than silently adopting an unreceipted
Home. Fresh exact-candidate browser acceptance now proves project and portfolio
new-Home creation plus same-offer completed recovery (`--new-home` / `--portfolio`
on the demo runner). A legacy refresh previously dropped the saved contextual
operation and prepared a duplicate pending offer; exact-candidate refresh now
preserves its guarded operation/destination even without navigation references.
That compatibility path does not authorize a new destination or supporting grant.

The exact-candidate browser now explicitly confirms Album-5 as a separate Music
Home child and a supporting source in Album-1, verifying canonical parent/receipts,
retained draft, unchanged original project and source bytes. Screenshots are
`compatible-confirmed-project.png` and `compatible-confirmed-supporting-folder.png`.
The owned loopback provider supplies model configuration only; this is local
candidate/File-only evidence, not vendor-model, live REAPER or release proof.
A process-local `ORI_REVIEWED_HOME_PROVIDER_DEV_SOURCE` recognizes only the exact
enabled normalized candidate with valid typed Home metadata. Ordinary local
installs retain release refusal; preview cannot install/enable an arbitrary source
or mark it release-verified. Demo plans label the development copy explicitly.
Natural-language continuation can return a canonical card-focus handoff bound to
its saved reply/revision/observation/conversation/offer. It is absent for unrelated
chat and cannot execute Review/Confirm/Resume. New proposals retain non-executing
choice controls. Browser acceptance verifies focus with unchanged pending state,
workspaces, sources and historical attribution.

**A named workspace carries into Review.** When the user names one workspace in
the accepted turn ("add this to Music Home" while Album-1 is on screen), the
turn's subject is that workspace. A NEW suggestion now carries it as
`subject: {workspace_id, name, kind}`, and the drawer sends it back as
`context.subject_workspace_id` when the user opens Review, so placement is
resolved for the named workspace and not for the page. It is a reference only:
the server reads the workspace again when it builds the suggestion and again in
Review. Saved attribution records `subject_explicit`, so a reload restores the
same suggestion; if the named workspace can no longer be read, the suggestion is
withdrawn and Review refuses, instead of falling back to the page. A subject
that merely followed the page is never presented as named. Refreshing an
existing pinned review still follows its saved operation and destination.

**A folder that is already a project is pointed at, not set up twice.** Before
Review allocates an offer for "create a project", `CompletedProject` looks for
this relationship's completed project outcome for the exact folder: same
canonical folder key **and** same directory identity. A namesake, or a different
folder that replaced it at the same path, does not match. The host then confirms
through the canonical store that the project is still active, owned by the user
and still holds the folder as a user-visible linked directory (`ProjectLinked`);
a trashed project or an unproven outcome is not reuse. The response is
`folder_existing_project` (project name and its in-app page), with no offer,
conversation or workspace created. A supporting-folder link is a different
operation and is not blocked. The same conversation's own outcome for the same
operation and destination is still reused in place with its receipt.

A blocked review card says only why it is blocked and how to refresh it; it no
longer appends the "Confirming creates…" description of a confirmation that is
not available. Pausing the assistant changes the relationship the folder was
picked under: a confirmation prepared earlier is refused, the card stops
promising to remember the project, and the review asks for the folder again.

Group-3 acceptance on plain `wt demo` is
`python3 scripts/assistant-workspace-demo.py --placement`
(`tests/personal-assistant-workspace-placement.spec.ts`): named group review,
the same review reused on a second "add this", navigation without retargeting,
a renamed destination blocking and then refreshing the same offer, one explicit
confirmation with its receipt, Album-1's supporting-folder choice, the completed
project pointed at from a new conversation, and a named workspace carried into
Review from another page. It uses the loopback deterministic provider, so it
shows host and drawer behavior, not vendor-model behavior.

## Group-4 grounded notes and tasks

**Readers.** The panel's model gets four brokered, read-only tools for the
turn's pinned workspace, and only on a provider path that can run Ori's tools:

| Tool | Returns | Recorded as a source |
| --- | --- | --- |
| `assistant_workspace_notes` | Note titles, IDs and update times (up to 50) | No: a title is not content |
| `assistant_workspace_note` | One note's current content, in parts | Yes |
| `assistant_workspace_tasks` | Task titles with recorded state, assignee, update time | No |
| `assistant_workspace_task` | One task's description, state, assignee, result, error, parent/subtasks | Yes |

They read the canonical stores through narrow interfaces: `AssistantNoteReader`
has two reads over the session note store (the same rows the Notes page shows)
and no create, update, delete, tag or search; tasks come from the workspace
store the resolver already uses. The workspace chat's `Tools()` bundle and its
auto-save prompt are not imported. The model can name a record, never a
workspace: every call re-checks the user, the relationship and both pinned
workspaces, then checks the record belongs to the pinned workspace. A note in
another workspace is reported exactly like one that does not exist, so an ID
cannot probe. Two notes with the same title are returned as a choice, never
guessed. A failed listing or read is `unavailable`, never `empty`.

The overview now lists up to five note titles and a note count (metadata only,
`content_read: false`); a note's content is delivered only by a read. Reviewed
memory still reaches the assistant only through its existing eligible reader for
Personal HQ. Another workspace's memory file has no eligible reader and is
reported as `workspace_memory_has_no_eligible_reader`; it is not read in its
place.

**Evidence ledger.** One ledger per accepted turn (`evidenceLedger`) holds the
aggregate budget and the list of delivered sources. The overview and review
context are charged first, then every tool result in every round, against
64,000 characters. Content is charged at its **delivered** size: JSON escaping
can make hostile text several times longer, and that growth is not free. A note
is read in parts of at most 40,000 characters, cut on character boundaries, with
`start`/`end`/`total` and `next_offset`; a part requested after the note changed
is refused instead of being joined to the earlier part. Two separate parts are
two records, so a range never claims text that was not read. When the budget is
spent the reader says so and nothing more is delivered. Secret-like lines are
replaced before delivery; the rest of the text stays in order.

**Citations.** A content read returns a server-issued key (`S1`, `S2`, …). The
model is asked to cite `[S1]` next to the statement it supports. After the
answer, each marker is checked against that turn's ledger; a marker for anything
not read in this turn is removed from the text. URLs and paths in prose are
never treated as citations. The response carries the validated source list in
`workspace_context.sources`: kind, workspace, record ID, label, content version,
update and read times, coverage and range, a server-authored in-app link
(`/workspaces/<slug>/notes/<id>` or `/workspaces/<slug>/task/<id>`), and whether
the reply pointed at it.

**Saved with the turn, as references.** The same list is stored in the turn's
`turn_context_json` (at most 12 sources, inside the existing 8,000-character
bound; references are dropped from the end rather than cut). No source body is
stored. A reloaded reply shows what it read then, labeled as history. A later
turn reads the records again and gets new versions; the earlier turn's sources
are not rewritten. When history is replayed to the model, an earlier turn's
scope is restated but its source references are not, so a later answer reads
current records instead of leaning on an old reference.

**Drawer.** Each reply with sources gets a native `details` disclosure,
"Sources used (n)" when the reply pointed at sources and "Sources read (n)" when
it read some but pointed at none. Labels are text nodes; only in-app workspace
pages become links.

**Selected task.** The app's task page is `/workspaces/<slug>/task/<id>`. The
collector and resolver recognized only `/tasks/`, so a task page never selected
its task; both forms are accepted now.

The model instructions for this path tell it to read before advising on a
substantive workspace question, to read nothing for a greeting or a general
request, to separate recorded facts from its own suggestions, to say when
sources disagree, and to treat everything a reader returns as data. On a path
that cannot run readers the prompt says so and no reader is advertised.

Acceptance on plain `wt demo` is
`python3 scripts/assistant-workspace-demo.py --sources`
(`tests/personal-assistant-workspace-sources.spec.ts`). The loopback provider
there is a deterministic stand-in for a tool-using model: it picks readers from
the user's words and repeats only what a reader returned, so it proves host
reads, citation checks and persistence, not what a vendor model would choose.

## Group-5 permitted files and attachments

**What is readable.** Three kinds of file a workspace already holds, and nothing
else:

| Source | Resolved from | Not readable |
| --- | --- | --- |
| Attachment | The attachment record's own stored file under the workspace files folder | An attachment that only remembers where a file once was (`OriginalPath`, a `file://` link): that location is metadata and is never opened |
| File in a linked folder | A `DirectoryReference` with this workspace's ID and an empty `Purpose` | Folders a capability owns (for example a sample library), another workspace's folders |
| Project file | The workspace's typed project-entry locator via `ResolveProjectEntry` | Home, library or sibling membership grants nothing; no live control |

A folder attached to the conversation is none of these. Picking it never becomes
permission to read it: it is not a `DirectoryReference`, so no reader can reach
it, by ID, by a climbing path or by an absolute path.

**Readers.** `assistant_workspace_files` lists those sources (names and sizes;
nothing is opened and readability is a hint from the file name only),
`assistant_workspace_folder` lists names inside one linked folder (500 entries,
three levels, hidden entries skipped, links listed but not followed; a listing
longer than 16,000 characters is shortened and says so), and
`assistant_workspace_file` reads one file as text. Text extraction is the shared
`fileparser.ExtractText` (also used by the workspace chat's directory tool):
PDF, Word, PowerPoint, Excel and the text formats through the existing parser;
any other file whose start holds no NUL byte as plain text, which is how a
`.rpp` is read; everything else is unsupported. The parser's 10 MB limit, the
40,000-character part and the turn's shared 64,000-character budget all apply,
across notes, tasks and files together. A Word, PowerPoint or Excel file is an
archive, and a small one can expand enormously: the parsers stop at 32 MB of
expanded content (`fileparser.MaxExpandedSize`) and report the document as too
large to parse. Audio is not decoded, nothing is run,
and a path written inside a file is delivered as text and never opened.

**The read itself enforces the boundary** (`workspace.ReadContainedFile`).
Checking a path and then opening it leaves a gap in which a folder on the way
can be swapped for a link that leaves the approved folder. The reader opens
through an `os.Root`, so nothing can lead outside the folder that was actually
opened, and it walks to the file one directory handle at a time without
following any link. A link that stays inside the folder is refused too: it could
give a hidden file or one of Ori's records a name that is not excluded. The
reader then uses only the open file: type and size come from the handle (a pipe
or device is refused without being opened, and the open cannot block), the
bytes come from that handle, and the handle and the folder are compared again
after the read. A file or folder replaced along the way is reported as changed;
a substitute is never read. Two tests swap the target and a parent folder for
links while reading 8,000 times, once to outside the folder and once to a hidden
file and folder inside it, and require that no read ever returns that content. The folder is resolved from canonical
state on every call, so unlinking or re-pointing it takes effect on the next
read, and a later part of a file that changed is refused.

**Left out on purpose.** Hidden entries, and Ori's own records wherever a linked
folder contains them: `MEMORY.md` (reviewed memory has its own eligible reader),
`workspace.json`, `agent_settings.json`, `mcp_servers.json` and
`skills_state.json` (absolute folder locations, agent definitions, tool-server
settings). They are neither listed nor readable through these readers.

**One workspace's readers never reach another workspace.** A workspace's own
folder can be a linked folder, and a parent's folder holds its children at
`sub-workspaces/<child>/`. Below the approved folder, any directory that is
itself a workspace folder (it holds `workspace.json`) is not entered and not
listed, and inside a workspace folder `agents/` (agent snapshots) and
`sub-workspaces/` are excluded. Being a Home therefore grants nothing over a
project's notes or files. An ordinary folder that happens to contain a
directory called `agents` is unaffected.

**Secrets.** Secret-like lines are withheld from delivered text, and a private
key is withheld whole, from its first line through its last, wherever a part
starts. Only the lines a part touches are examined, each as a whole line, so a
secret cut by a part's edge is withheld on both sides of it and one part of a
very large file does not cost a scan of all of it.

**Refusals are distinct and carry no path:** `file_not_found`,
`path_outside_the_approved_folder`, `links_are_not_followed`,
`not_readable_here`, `not_a_regular_file`, `file_too_large`,
`document_too_large_to_parse`, `not_a_supported_document_or_plain_text`,
`document_could_not_be_parsed`, `file_changed_while_reading`,
`folder_not_linked_to_this_workspace`, `attachment_has_no_stored_file`,
`project_file_unavailable`. None is reported as an empty workspace.

**Sources.** A file source records its kind (`file` or `attachment`), the
workspace, its name, where it lives in words ("Linked folder “Album assets” ·
lyrics/bridge.txt", "Workspace attachment", "Project file"), a content hash,
the file's modified time, the read time and the range read. No absolute path is
stored or shown, and a file has no link because the app has no page for one.

**Folder turns are authority-aware.** The blanket refusal for "summarize these
documents" is replaced by a decision. When the pinned workspace has readable
files of its own and the provider can run readers, the model answers from those
and is told to attribute the answer to that workspace source; the attached
folder stays metadata. Otherwise Ori refuses before any model call and names the
real ways forward (paste the text, or link the folder through a reviewed setup).

**Found and fixed while demoing:** after a first turn without a folder saved a
conversation, the drawer's folder controller still held a draft target, so
Add folder in that conversation discarded its own result and showed no preview.
The controller now adopts the saved conversation.

Acceptance on plain `wt demo` is
`python3 scripts/assistant-workspace-demo.py --files`
(`tests/personal-assistant-workspace-files.spec.ts`). The demo provider records
how many requests it received and whether any carried a watched marker (a file
in an attached-only folder, a hidden file, a same-named file in another
workspace) without storing its input; that run shows zero. The exact-candidate
suite additionally reads the created project's `.rpp` through the
provider-managed project entry and shows that its Home and its sibling cannot.

## Group-6 validation changes

Final validation changed behavior in these places. Each has a test named in the
acceptance matrix kept with the task list.

- **Budget.** A content read is charged for its whole result (names, labels,
  times and counters as well as the content), measured as the provider receives
  it. A listing is limited to 16,000 characters (`assistantcontext.ListingLimit`)
  and to what is left of the turn's budget; too long, it is shortened and marked
  partial instead of refused whole, so finding a source cannot use up the budget
  needed to read it.
- **Reader rounds.** After the four permitted reader rounds, the one remaining
  model call is told that the reader limit was reached, to answer only from what
  was read and to say what was not.
- **Citations.** An earlier answer is replayed to the model without its `[S#]`
  markers: keys are issued afresh each turn, so a marker copied forward would
  name whatever source holds that key now. The saved answer keeps its markers.
  When more sources were read than a turn lists (12), cited ones are kept first
  and a marker whose source is not listed is removed.
- **Continuation.** A continuation is checked against the latest read of a
  source, so a file that changed and was read again from the start can be
  continued.
- **Unavailable is not empty.** A project listed under a Home that cannot be
  read now makes the Home's project count partial (or unavailable), and a
  recorded project file that cannot be resolved is reported as unavailable.
- **Named workspace from a task page.** The page's selected task is not looked
  up in a different workspace the user names; the turn resolves.
- **Diagnostics.** Each workspace turn logs one line,
  `Home assistant workspace context`, with the context status and reason, the
  canonical location and subject IDs and kinds, whether readers were offered,
  reader calls by outcome, sources by kind, partial sources, evidence characters
  used, and preparation and read time in microseconds. It carries no workspace
  or file name, prompt, source text or filesystem path. The demo runner checks
  the server's log for the bodies it had Ori read and for the linked folder's
  location, and fails if either appears.
- **Keyboard.** Pressing Send from the keyboard no longer drops focus to the
  page: the button was disabled for an instant during submit, and a browser
  removes focus from a focused control the moment it is disabled.

Keyboard, screen-reader semantics and narrow layouts are checked by
`python3 scripts/assistant-workspace-demo.py --accessibility`
(`tests/personal-assistant-workspace-accessibility.spec.ts`): roles, names, live
regions, focus order and layout are asserted in Chromium. No screen reader was
run.

`python3 scripts/assistant-workspace-demo.py --integrated`
(`tests/personal-assistant-workspace-integrated.spec.ts`) walks the slices in a
single conversation in one sandbox: a review prepared on a group's page, a move
to a project inside it, notes and tasks read and cited while the review stays
pending and unchanged, a file read while the attached folder stays metadata, a
reply held during navigation, a linked folder removed, and only then the review
confirmed. `./scripts/assistant-workspace-acceptance.sh` runs every browser
acceptance run in order, each in its own sandbox, and reports each one; given
the two exact companion sources it also runs the three candidate setups and
checks that both sources are unchanged afterwards.

Known limits, recorded rather than fixed: an entry is excluded by name, so a
hard link under another name, or a filesystem that treats two spellings as one
name beyond ASCII case, is not detected; the PDF parser's page loop is the
existing library's.

Setup-plan software previews now call the existing canonical integration reader
directly. `setupjourney.Service.Read` creates/reconciles inert run rows, so it is
not used for observational preview. Actual confirmed runs retain the original
journey, review lease, digest and idempotency gates.

## Four separate scopes and the read principal

1. **Owner:** current server-resolved user, hired relationship/profile, state
   version and designated HQ. Canonical Session remains owned by HQ/profile.
2. **Location:** active page workspace; explicit Home map/tree selection only
   when the page is app Home. IDs and slugs are different reference types.
3. **Subject:** uniquely resolved explicit user target, otherwise location;
   an attachment names the folder observation independently. A subject change
   affects this turn only. Ambiguous/conflicting action intent needs one choice.
4. **Destination:** a reviewed operation's canonical workspace/Home and effects;
   never inferred from a later page, history, same display name or model prose.

Panel reads act **for the requesting user through the hired relationship**, not
as the project's entry agent. Host resolution uses `LocalUserProvider` (currently
`local`) and canonical `OwnerUserID`. Native-created workspaces explicitly set
`local`; existing owner normalization treats empty legacy ownership as local.
A nonempty different owner, missing/inactive workspace, broken relationship,
foreign selected task/source or disagreeing references fails closed. Do not
manufacture a workspace-local instance or copy its credentials/tools.

This is a new narrow *host read adapter*, not reuse of the workspace chat's
principal. In the local app, ordinary notes/tasks/attachments and purpose-empty
linked directories are already user-visible sources; a user-requested panel
read may use those existing source contracts after user/workspace validation.
Opening/navigating is not an instruction to read contents. User-selected metadata
observations are not such sources. A future non-local principal needs an explicit
access implementation; it must not reuse the local-owner default.

| Source | Canonical lookup and authority | Required exclusions/refresh |
| --- | --- | --- |
| Notes | Session note store; exact `WorkspaceID` after GetNote, not title/preview alone | Duplicate names clarify; moved/deleted IDs fail; current bytes/time each read |
| Tickets/tasks | Workspace/TicketService, exact subject workspace and task ID | Detail/result/error are read-only; no schedule/execute/state change |
| Ordinary attachment | Current workspace attachment ID plus workspace-owned relative file | Legacy OriginalPath/file:// is metadata, not an arbitrary file grant; never follow it |
| User-visible directory | Fresh exact DirectoryReference ID with WorkspaceID match and empty Purpose | Never derive root from request/history; removed/replaced reference fails; no inheritance |
| Managed knowledge | Existing relationship-bound HomeSection and eligible memory renderer | No borrowed project-agent ID; paused/pending/rejected/forgotten/revoked entries excluded; raw MEMORY.md/sidecars cannot bypass |
| Exact project entry | Fresh typed ProjectEntryLocator and contained approved managed root or purpose-empty DirectoryReference | No live capability/MCP; sample/library roots and capability-internal Purpose excluded; exact entry only |

Home children may supply bounded owner-checked project summaries; this does not
recursively read child notes, files, memory, runtime state or credentials. Folder
linking is a separate reviewed mutation. Where a source is not independently
eligible, offer linking/setup or direct file supply; do not read and then seek
permission or silently create a grant. No new companion permission API is needed
for these host-owned user-visible sources.

Refresh relationship/scope at Send and every tool read; check again before
persisting/confirming. Freeze location/subject by canonical ID for the turn.
Fresh reads may reject revoked scope, but must never replace it with the current
page or HQ. Cancellation stops new reads; a losing revision/save must not report
stored attribution or redirect into a replacement Session.

## Provider boundary

`assistant_workspace_contract_test.go` executes real Codex/Claude Code adapters
against controlled executables in temporary HOME. Existing vendor conversion
and Codex protocol tests cover request/response shape. These are deterministic
adapter checks, **not real vendor responses**.

- OpenAI/Claude/Gemini and local HTTP providers declare host tool support.
  Expose only the explicit read allowlist where the selected adapter supports it.
- Codex supports Ori-brokered calls: tools are prompt/schema data; Ori validates
  and executes calls. Native MCP is separate.
- Claude Code declares `SupportsTools=false`. Keep a validated overview and
  review controls, explain deeper-read limitations, and send no unavailable
  tool definitions. Its text-only posture uses tools empty / dontAsk.
- Both CLI adapters treat **nonempty ChatRequest.WorkspaceID as native execution
  opt-in**, even without MCP servers. Keep WorkspaceID, WorkspaceDir, MCPServers
  and ExecutionScope empty on this panel. Display/attribution IDs belong in the
  separate context DTO. Do not change providers, dual gates or models as a fix.
- `runModel` now makes advertisement capability-aware while preserving empty
  native-execution fields. Keep four tool rounds and final tool-free synthesis.

Configured-model checks require a separately selected authorized sandbox model;
credential presence alone is not a decision to change the application's model.
Live-model evidence remains separate from inspecting providers and browser mocks.

## Minimal context/evidence/persistence seam (proposed)

Use a small data-only package (e.g. `internal/assistantcontext`) shared by HTTP
and Session adapters. Do not import HTTP handlers into chat/workspace packages.

- Versioned browser references: page path/surface, explicit workspace ID versus
  slug, selected task. Browser names/descriptions/permissions are ignored.
- Server context: typed location and subject (ID, safe name, kind, breadcrumb,
  record version, updated/read times), source availability and bounded counts;
  separate relationship owner and review destination remain server-controlled.
- Availability: available, empty, unavailable, denied, unsupported, partial,
  historical; unavailable never means empty. Record safe reason codes and
  duration/count/truncation only, never source bodies or unrestricted paths.
- Review projection: canonical offer lookup after conversation validation;
  subject/operation/destination/effects/status/blocker/valid control labels.
  Do not send executable references/digests to the model. Failed state lookup
  is state unavailable, not no review. History cannot reactivate an old offer.
- Bind destination identity, relationship and compatibility/version witnesses
  into review validation/digest, not just display lines. Renames and material
  effects need refreshed disclosure; navigation alone does not retarget review.
  Creating a Home retains the exact reviewed creation intent and records the
  resulting parent identity in its canonical receipt.
- Evidence ledger per turn: server-issued reference keys, exact workspace/source
  identity, safe label, current version/hash/update/read time, preview/full/
  partial coverage, ranges and continuation. Model citations resolve only against
  evidence actually delivered for that turn. URL/path prose is never a citation.
- Source links reuse `/notes/{id}`, workspace slug + Ticket query, and contained
  file routes where safe. Directory content currently has API/Files-explorer
  surfaces; use a narrowly authorized source detail if no safe deep-link exists.
  Historical references are labels/evidence, not grants to fetch current bodies.

Add optional bounded `turn_context_json` to **canonical Messages** via the
numbered database migration mechanism (implemented by migration 77). Keep
Go Message metadata out of generic JSON input. A dedicated internal append seam
must atomically store user/answer attribution after owner/revision validation;
extend existing folder-turn transaction rather than adding an independent write.
Invalidate hybrid caches on success/failure, preserve order, FTS/retention and
session deletion. Generic/imported input cannot author fresh metadata. Imports
may retain only explicitly historical projections; old text-only turns show
unknown scope. Relationship/HQ replacement never upgrades history into authority.
Store bounded references/versions, not retrieved source bodies or a second memory
store. Saved answers may quote evidence under existing conversation behavior.

Readers should share small canonical data/parser functions, **not** Tools() or
runtime prompts (those include writes, delegation, agent management and auto-save
instructions). Per-reader errors degrade that source only. Secrets, managed
knowledge files and source instructions remain filtered untrusted data. Existing
path-returning helpers alone do not prove race-safe open: group 5 must validate
root/reference/file identity at actual open/read and test substitutions.

## Budgets and latency

Retain PRD defaults: 12,000 Unicode characters for overview, five previews per
category, 64,000 aggregate evidence characters including overview, 40,000 per
file chunk, existing parser byte limits, 500 directory entries/three levels,
four tool rounds. Enforce before provider input, across all calls in a round.
Any truncation/continuation carries precise coverage. The per-turn evidence
ledger (group 4) charges the initial overview, every metadata result and every
content read against the one aggregate budget. A directory listing's
readability check currently sniffs file bytes; do not copy it into panel-open or
greeting discovery and claim no content was read.

The baseline has **no viewed-workspace preparation path**, so its overhead is
zero by omission, not a meaningful success against the 250 ms target. Compare
new overview preparation to a warmed canonical-read fixture (group with two
projects, selected project containing 100 tasks); measure 200 samples, report
p50/p95 separately from model, parser and tool work. New overview fixtures add
representative note/file counts without content reads. No broad performance
refactor or budget revision is authorized by an unmeasured assumption.

The current non-race warm fixture (same 100 tasks, 200 samples, one warm-up
excluded) measured canonical project+parent p50 **178.625 µs**, p95 **321.333 µs**;
full bounded resolver/projection p50 **616.292 µs**, p95 **964.209 µs**. This is
local context preparation only, excluding provider, profile/memory, deeper reads
and parse latency. Concurrent race-instrumented checks are not this performance
comparison. The provisional 250 ms p95 target is met for this fixture.

**Large workspace (group 6).** `TestAssistantWorkspaceLatency_LargeWorkspace`
(run with `ORI_ASSISTANT_LATENCY=1`) measures through the real routes, stores
and filesystem on a Home of 41 projects whose subject project has 1,000 tasks,
300 notes (one of about 150,000 characters), 200 attachments, a linked folder of
2,000 files, a 9 MB text file and a 2,000-paragraph `.docx`. On an Apple M5
(10 cores, 32 GB, macOS 26.3.1, Go 1.27.1), without the race detector:

| Measured | p50 | p95 |
| --- | --- | --- |
| Warm overview, large project (200 samples, HTTP route included) | 5.2 ms | 5.7 ms |
| Warm overview, Home of 41 projects | 3.3 ms | 3.7 ms |
| Reader round: task list, task detail, note list, file sources (30 samples each) | 8.1–8.9 ms | 8.8–9.4 ms |
| Reader round: 40,000-character part of a 150,000-character note | 9.0 ms | 9.5 ms |
| Reader round: folder listing of 2,000 files (shortened to fit) | 11.9 ms | 12.5 ms |
| Reader round: 40,000-character part of a 9 MB text file | 22.5 ms | 23.3 ms |
| Reader round: parsed `.docx` | 13.0 ms | 13.4 ms |

A reader round is the time from the stand-in model asking for a reader to Ori's
next request, so it includes the access re-check made before every read; model
time is excluded. The 250 ms p95 target for overview preparation is met with a
wide margin and is kept. Two changes came out of measuring: a part of a large
file took about 460 ms because the whole file was scanned for secrets on every
part (now only the lines the part touches), and a listing of a large folder was
refused whole as over budget (now shortened). A turn that reads until the budget
is spent delivered 58,179 characters through readers, had its later reads
refused as over budget, and reported its one source as partial.

## Companion finding

Host-only source implementation is sufficient: reviewed REAPER blueprint v10
uses typed project entry + `.rpp` existing/new connection, requiring Music-owned
Home schema/version 1. Music Home schema 1 reciprocally allows exact REAPER
project-team schema/version 1. `projectconnection.RecordAttachedProject` creates
a purpose-empty workspace-owned directory reference and typed exact locator;
`ResolveProjectEntry` rechecks identity/containment without executing the plugin.
No blueprint, manifest, SDK, permission, version-floor or release change is
identified for this feature. Do not mistake a similarly named old checkout for
that reviewed contract.

Source-protected staging uses `scripts/reaper-demo.sh` with explicit source
arguments. Its build helper now runs only in a temporary Git export, not the
companion checkout; fake-only success/failure preservation tests run with
`python3 scripts/reaper-demo.test.py`. The exact REAPER artifact-only build also
passes locally without changing the external source. This is not installation
or published-release verification.

The exact candidates have been installed/enabled **only in disposable state**,
with a real Music Home and recognized REAPER review. Confirmed setup into an
existing Home, a declared new Home and a portfolio (task 3.5), and reading the
created project's `.rpp` through the project-entry reader (task 5.5), ran there
with a deterministic loopback provider. This does not prove the user's installed
version, real vendor-model behavior, live REAPER or release verification: those
were not run. No companion source edit, real-environment installation,
publication or floor change occurred.
