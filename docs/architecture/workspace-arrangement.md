# Workspace arrangement v1

Status: **bounded core and characterization, not a shipped arrangement workflow**.
Inert intent, held/saved evidence adapters, batch-validated tools and the bounded
API-provider planning loop, atomic PA draft storage, opt-in PA/Home endpoints,
read-only pinned Manager guidance and shared inert history cards are implemented.
The PA attachment disclosure/Send path now reaches a canonical saved proposal.
Home Organize library also reaches canonical proposals and clarifications through
explicit sharing/upgrade consent. Home proposals now have paged host review,
explicit confirmation and atomic collection metadata / activation-selection receipts.
Collection changes may be combined with one exact activation-review selection;
queue preparation never approves project creation. Existing per-project reviews
still own file choice, provider/staffing disclosure and activation confirmation.
Home conversational revisions and manual names/member additions/exclusions are
implemented. PA proposals now apply one arranged group at a time through the
existing conversation-bound folder setup review, its digest-checked Set up,
structure run and canonical receipt (see **PA arranged groups** below). Manual
action conversion/global-choice editing remain incomplete.
No new plugin protocol or filesystem permission.

## Evidence behind the contract

At Ori `053914a5886e4be607cd7b36578cdbf48cea7d15`:

- `FolderObservationService` holds the metadata scan process-locally. The scan
  inspects at most 5,000 entries, depth 3, for 3 seconds. Creator candidates are
  the root and immediate children. The optional tree retains at most 64 observed
  nodes; it does not promote nested nodes to creator candidates.
- Discussion has **8** project names and an **8 KiB** snapshot; the deterministic
  structure has a separate **24**-member limit. Neither is the full held scan.
  Names/mtime are observations, not purpose, authoritative version or progress.
- A nested `Music/Albums/Album A/Song 1` can appear in the tree without a song
  candidate or ancestor marker. Deeper omissions do not necessarily set today's
  `partial` flag. Always show depth/entry/time limits, not “complete inventory”.
- The canonical Home library is schema 1, at most 5,000 entries/8 MiB, inside
  `AssistantProgramState.ProjectLibrary`. Strict readers reject unknown fields
  and versions. CollectionSummary/CollectionBrief do not own membership.
- Exact Manager authorization already checks a live local required-primary role,
  instance, agent snapshot, Home binding and installed provider. A PA naming a
  Home is not that Manager. Existing Manager searches return five rows.
- Structure execution can create a plain group with disclosed Group Manager and
  selected linked plain workspaces. Activation has independent exact-file,
  File-only, provider/ownership review and canonical creator receipts. A queue
  holds 2–100 choices, not 100 approvals.

Maintained characterization: `internal/llm/arrangement_transport_test.go`,
`internal/personalassistant/folder_arrangement_characterization_test.go`,
`internal/projectlibrary/arrangement_characterization_test.go`; existing real
server `personal_assistant_folder_{turn,setup,collection}_test.go`, Manager policy
and activation/recovery suites. Fake providers prove protocol, not judgment.

## Source and authority matrix

| Source | Authority derived by host | Allowed evidence | Not authorized |
| --- | --- | --- | --- |
| `attachment` | Current PA owner/relationship, workspace and agent; canonical conversation revision or unconsumed draft; exact held observation | Already-held candidate counts, names, marker hints, observed dates, bounded tree relationships | New listing, resolving a tool path, content reads, another attachment, private workspace files |
| `home_library` | Authenticated owner plus actual currently bound local primary Manager instance, Home/program/provider identity and binding revision | Canonical saved catalog metadata, source coverage and collection membership | PA impersonation, child transcripts/Tickets, song-detail reads, another Home, fresh discovery |
| historical attachment | Canonical saved conversation, imported rows excluded | Original dated 8 KiB snapshot | Live selectors, review/creation, restoring lost held authority |

Owner, workspace, conversation, Home, agent/instance and provider IDs are **not
model arguments**. Adapters close over the validated execution context. Every
call and completion rechecks current authority and pinned revisions. Attachment
query adapters must not call the existing path-resolving `Resolve` as their
query implementation: add a held-evidence-only projection with target/binding,
expiry and canonical-selection checks. Review, separately, revalidates directory
identity through the existing reviewed creator boundary. A metadata query cannot
detect an offline root by probing it; report last-observed availability as such.

Home Organize runs in the Home as its real Manager, using that agent's configured
model. It is an explicit requested operation, not a broader scan-completed brief.
The PA offers a contextual **Open this Home to organize its library** handoff,
never silently executes as another identity or reads that Home's library. Before
a Home exists, generic attachment planning remains useful. Home creation, plugin
installation, library initialization and root/scope discovery are separate
reviewed prerequisites; after them the user requests a new plan.

## Sharing and entry points

Picking/attaching a folder makes no model call. Show dated metadata and a
**Rule-based setup option** label; never call it an agent assessment.

Ordinary Send retains the old discussion sharing contract. An explicit
**Suggest arrangement** mode must disclose *before Send* that bounded tools can
share additional names/counts/marker/date metadata already held from that same
scan (up to the turn evidence budget below), not just the visible 8 KiB summary.
Selecting this mode and sending its goal is permission to share this metadata,
not to scan, read contents or apply. If the disclosure cannot be shown, keep the
old discussion/manual path. Home Organize similarly discloses saved Home catalog
metadata and the exact Manager/model. No automatic provider switch.

The PA backend accepts an explicit `arrangement.share_held_metadata: true` on
its existing Ask request, with a current conversation/draft and live held folder
reference. It bypasses ordinary path resolution, research and general-purpose
tools. Mixed approvals, focused/historical references and unsupported transports
are refused. The PA drawer's **Suggest arrangement** entry wires this mode into
Ask directly, without ordinary routing, draft/research approvals or operation
controls. Its disclosure names the system provider/model; paired
`expected_provider` / `expected_model` pins reject configuration drift before
sharing. Metadata inspection and reopening history call no planning model.
Home Organize library reads a separate owner-only planning-context disclosure,
including the actual Manager/model and library version. Its `context_pin` binds
Send to that runtime/catalog snapshot; the host reauthorizes independently and
rejects stale pins before sharing metadata. Readiness reads never initialize,
upgrade, learn or trust skills. The shared `workspace-arrangement-consent.js`
component defaults disabled; the integrated PA and Home entries explicitly opt in. It discloses the exact displayed agent/provider/model,
expanded held versus catalog metadata, per-turn evidence bounds and repeated
requests, and a separate v1-to-v2 storage-upgrade choice. In-memory choices are
bound to the current goal/context/revision/runtime display and consumed before
handoff; they never grant host authority. An uncertain request/save blocks another
Send until the owning adapter successfully reopens canonical history. No planning
request, provider fallback or retry originates from mounting or checking it. Saved summaries can be reopened through a
shared read-only card in PA conversations and the Home library's Saved arrangements
section. Cards distinguish clarification, proposal, stale/redacted history and
partial coverage; every intention remains inert. Only canonical references/GETs
are used, never assistant prose. Late reads cannot attach to a switched context.
Successful drafts accompany an atomically saved
attributed turn; replies contain bounded inert summaries and conversation reads
hydrate canonical record/message references with `review_available: false`.
Provider registration/model and owner/context/attachment pins are rechecked
across awaits. Reload never substitutes a deterministic setup suggestion for a
typed arrangement's answer. Explicit existing setup controls remain separate.
The sharing object rejects unknown/duplicate/case-aliased fields and non-boolean
choices. Explicit null/duplicate/aliased mode fields cannot fall through to the
ordinary tool loop. Safe `arrangement_error` codes distinguish missing consent,
invalid input, unavailable sources/models/transports, stale state, cancellation,
time/budget/storage limits and failed saves; raw errors are never returned.
Both draft and clarification writers must be wired before sharing metadata.
Failed-first-turn cleanup uses an owner/revision-checked transaction and refuses
any conversation with messages or concurrent changes. Cleanup has a separate
bounded cancellation-independent context, skips a busy store rather than waiting,
and never deletes a committed draft after an uncertain save result.

Saved PA and Home records have owner-checked, read-only summary endpoints (see
[API reference](../api/API_REFERENCE.md)). Their shared projection is capped at
64 KiB, independently of model tool budgets, and includes origin, saved dates,
goal, rationale, coverage, unknowns and proposed-operation/review-question counts.
It never returns full membership, selectors or authority fingerprints. Explicit
unchanged counts exclude the implicitly unchanged remainder; needs-review counts
deduplicate items with several questions. Context/catalog-current flags are only
historical revision comparisons, not live readiness. Reads do not initialize or
upgrade a library, probe a source or impersonate a Home Manager. Planning and
compiled review remain separate. The server owns one shared Planner for route
adapters so constructing another handler does not create fresh concurrency limits.

Add folder and Home Explore use this same distinction and review semantics.
Assisted Create Workspace stays a separate single-workspace draft. Historical
or expired evidence is discussable but cannot acquire new executable authority.
No model/plugin/Manager: label the rule-based/manual alternative honestly and
keep it usable, with prerequisites separated from desired effects.

## Tools, references and bounds

Only these tools are advertised in arrangement mode:

- `arrangement_overview`: aggregate counts by known format/connection/observed
  availability/root, coverage, capabilities and at most five explicitly labeled
  samples. Samples are not representative, exhaustive or an ordering decision.
  Optional literal `collection_text` and `collection_cursor` arguments page
  existing collection summaries five at a time; total/matching counts and a
  separate signed cursor keep collections beyond the initial sample reachable.
  Collection cursors cannot be used as item-search cursors.
- `arrangement_search`: typed AND filters (literal name text, format, connection,
  observed availability, root handle, collection handle, and optional
  `selection_handle` for an already-issued frozen set), sort by name/ID, opaque
  cursor. No glob, regex, SQL, path, expression or future-match rule.
- `arrangement_detail`: one issued item ID; source-scoped metadata only. Never
  reuse Home detail's filesystem source projection or its larger byte budget.
- `arrangement_select`: freeze a validated filter into exact current item IDs
  and item revisions. Return opaque handle, count, source revision and digest;
  membership stays server-side and is separately pageable for user review. A
  zero-match result returns count zero without a handle; an explicit empty
  selection can clear a collection.
- `arrangement_propose`: terminal typed `Draft`; must be the sole call in its
  batch. Validate the entire batch before invoking any tool. A text clarification
  or a valid no-change draft is allowed. The tool cannot persist approval or apply.

Every item/collection/cursor/selector exposed to the model is an opaque scoped
reference, not a basename/path or a cross-Home capability. References bind source
identity + revision + owner/turn; a fresh selection does not reinterpret old IDs.
Cursor binds normalized filters, sort, source revision and last exact ID. Reject
stale cursors, never silently restart from page 1. Count/digest describe a frozen
set, not a live query. Handles expire after 10 minutes or earlier on source/owner
loss; cap at 16 handles per turn/64 per owner and 128 active turns process-wide.
They do not survive restart. Persist resolved exact membership when saving a
draft, not an executable filter or a dependence on the ephemeral handle.

The host-only `Evidence.RebindDraft` prepares canonical prior intent for a Home
revision turn. Its caller must independently authorize the owning context and
runtime, never pass browser/model records. It checks every referenced item and
collection revision, retains the owner's names/exclusions/questions, and replaces
large membership arrays with fresh exact handles. Distinct frozen sets share the
existing 16-handle budget; failure issues no partial references. Search/select
can intersect an issued set with ordinary literal filters without widening it.
This helper is evidence preparation only: it neither persists a revision nor
revives approval. Home **Revise with assistant** separately discloses the target
and prior-proposal/question sharing, then requires fresh consent. Planning pins
both target and catalog revisions. Canonical completion keeps the proposal ID,
increments its revision and invalidates old approval; delayed replies cannot
replace newer owner edits or commits. An unrelated live review blocks revision.
A clarification saves a bounded `RevisionDiscussion` alongside the unchanged
exact typed proposal and prevents review until resolved. A subsequent turn uses
both that proposal and question, never a lossy summary as editable intent.
Model revision diffs retain counts/flags only; Forget clears the question, and
collection removal clears it while staling affected intent.

| Budget | v1 bound / failure behavior |
| --- | --- |
| Source | 5,000 catalog entries or existing bounded attachment scan; never scan to fill gaps |
| Model search | 20 rows per call; user review pages at most 50 |
| Tool result | 16 KiB encoded JSON; return a bounded error/narrowing request, never cut JSON |
| Newly shared evidence | 64 KiB per turn including overview and results |
| Each full model request | 96 KiB, including rules, skill, history, tools and evidence; no silent truncation of authority |
| Provider work | 8 calls, 12 total tools, 2,048 output tokens per response, 24,000 cumulative charged tokens, 90-second deadline |
| Context window | Each request asks for the model's known window, never less than 26,048 tokens (one bounded turn), so a local server (Ollama, LM Studio) does not truncate rules or evidence at its small default; cloud providers ignore the field |
| Pre-call prompt estimate | One token per three UTF-8 bytes until the provider reports prompt usage, then that request's observed tokens per byte with a 20% margin, never below one token per eight bytes; plus the 2,048-token output reservation. Stop before the next call when the estimate does not fit |
| Missing token accounting | Charge conservative UTF-8 byte upper bound for request + response after a call whose usage is missing |
| Concurrency | One planning turn per conversation/Home; 2 per owner; cancellation/expired lease prevents terminal persistence |
| Draft | 32 KiB model intent; 32 operations; 100 explicit item IDs per selection, larger sets use a handle |
| Generic creation | Existing 24-member limit per group; each group separately reviewed; no silent tail drop |
| Selected activation | At most 100, each retains independent activation confirmation |
| Storage memberships | 20,000 references per arrangement/collection set, within the 8 MiB document bound |

A host compiled review has its own bounded page projection, not the model's
32 KiB draft. Enumerate every exact affected item for the owner. When a limit
prevents a draft/save/review, refuse it with a narrowing explanation; do not
claim the remaining items were arranged.

### Provider decision

Fake HTTP round trips cover OpenAI, Anthropic, Gemini, Ollama, LM Studio and MLX.
Supply **all** host rules and resolved guidance in `ChatRequest.SystemPrompt`.
Anthropic drops system-role messages. Keep tool result `Name` as well as call ID
for Gemini. Model capability/failure still needs honest handling per selection;
a provider's tool flag does not guarantee a particular model's quality.

Codex ordinary brokered tools run with inherited CLI configuration and a
read-only sandbox (which permits reads). The verified `Isolated` request rejects
tools/results. Claude Code declares no brokered tools. **Both CLI paths offer
manual arrangement in v1**; do not enable native tools or reuse a merely read-only
CLI run as a metadata sandbox. Adding a verified isolated brokered transport is
separate work, not a reason to weaken this feature's permission boundary.

## Inert intent and compiled review

`internal/workspacearrangement/contract.go` freezes the version-1 Draft shape and
pure validator. It does not load a source, validate a live grant or authorize a
mutation. Host-owned `ValidationView` includes only currently issued references.
Unknown/duplicate fields, malformed/trailing/oversized JSON, invented/stale IDs,
path-like names/IDs, conflicting dispositions and unsupported kinds are refused
whole. Collections have flat membership in source items only; nested groups,
collection-to-collection membership and cycles are unrepresentable. Free-text
rationale, unknowns and names are inert, escaped data; no expression interpreter
or text-to-action conversion exists. Instruction-like filenames cannot act.

Operations:

| Kind | Source | Truthful compiled effect |
| --- | --- | --- |
| `create_collection` | Home | New user-named logical record, possibly empty |
| `rename_collection` | Home | Exact ID/revision name change only |
| `set_members` | Home | Replace exact membership, including explicit empty set |
| `remove_collection` | Home | Remove metadata record only, never entries/files/workspaces |
| `create_group` | Attachment | One new plain group and explicitly selected immediate candidates as linked plain workspaces; disclose creator's Manager/roster effects |
| `select_activation` | Home | Offer exact selected items for existing activation review/queue; no creation approval |

Logical collections may overlap, including with selected activation. Explicit
unchanged/needs-review items cannot simultaneously receive a requested effect.
Only one change per existing collection per draft: revise separately rather than
relying on ambiguous order. No new group adopts an existing workspace. Refuse
already-owned or overlapping folder targets in compiled review rather than
reparenting or duplicating them. Keep single-project software setup separate.
Music-shaped filenames never choose an executable Home/blueprint. Explicit plain
workspace creation must say **plain workspace**, not silently substitute it for
unsupported project activation. Unsupported requests stay needs-review advice.

Host compiles from fresh source/ownership/provider/roster identity and canonical
names, resolves every selector, and pins the draft revision plus exact effects
digest. UI confirmation token is scoped to that compiled review for 10 minutes.
Changing goal, membership, name, source, binding or provider requires a new review.
An old tab, bare yes, generated/imported transcript or model tool cannot confirm.

Lifecycle: `draft` → `needs_more_evidence` or `awaiting_user_review` → `applying`
→ `completed` or `partially_applied`. Revision/authority loss yields `stale`;
explicit decline yields `cancelled`. `completed` means its canonical metadata
writes/receipts exist; **activation choices awaiting separate review are not
created projects**. An apply journal stores per-effect canonical consequence IDs
and digests; a partial retry uses those IDs, never names or blind creator calls.
No automatic resume after restart or cancel. Reconcile first, then offer explicit
retry of remaining approved effects under current authority. Never roll back by
deleting successfully created workspaces or source files.

## PA arranged groups

A saved PA proposal's `create_group` intentions are applied one group at a time
through the existing conversation-bound folder setup review. Nothing new
confirms an effect: **Review group** compiles and binds a review; **Set up** on
that review card is the only confirmation.

- **Compile.** `FolderObservationService.ReviewArrangementGroup` builds a
  version-2 *selected* `FolderStructure` from the record's exact resolved
  candidate IDs over the whole held scan, not the 8-name preview or the
  collection threshold. Version 2 has its own validator: a new plain group,
  1–24 members (the existing creator bound), every member included and plain,
  no omitted tail, no blueprint and no Home/library type. The version-1
  recognition rule and its validator are unchanged. The root, an invented or
  padded ID, a nested candidate, a member that moved or became a link, a member
  that already belongs to a workspace, an invalid or taken group name, and a
  software-project root are refused by name, never dropped or substituted.
  Member names are made free as other structures do; the group keeps the
  arrangement's name.
- **Bind.** The structure carries `arrangement {record_id, record_revision,
  operation}`; the stamped plan intent carries the same binding, so the
  confirmed digest covers it, and the plan adds an
  `arrangement: Only the folders your saved arrangement selected` line. The
  agenthttp handler first requires an open (`draft`/`needs_more_evidence`),
  local, non-imported record at the expected revision and current assistant
  state version; the conversation's current folder event must be the record's
  own observation; no other setup review may be open; a model-authored record's
  provider/model/registered instance must be unchanged; and the live held scan
  must still validate every item revision. Session migration 82 adds
  `assistant_arrangement_effects` (record, operation, record revision, offer ID,
  binding message, state, read-back group ID/route/workspace count, receipt
  digest; canonical IDs only). `BindAssistantArrangementReview` appends the
  canonical folder event and upserts the binding in one transaction under the
  folder-revision CAS. It moves the record's context pin forward only when the
  pin was current before this append: the review's own event never stales the
  record, and an unrelated change is never laundered into current.
- **Protect.** One pending review per owner, as before. A manual review or its
  Adjust never adopts, refreshes or retires a pending arranged review, and an
  arranged review never replaces a manual one; each must be closed first (Keep
  chatting). A finished, closed or pruned review is history: the next group can
  follow it. Arranged reviews have no Adjust (a change is a new proposal), and
  Decide refuses `later` and `no` on them: a put-off offer could return pending
  on its own, and a no would become a folder decline outliving the arrangement.
- **Confirm and run.** The existing guard additionally requires the canonical
  record at the bound revision, this offer as the operation's current binding,
  and (before Set up) the open state and unchanged runtime. Set up then runs the
  existing `StructureRunner`: group with its disclosed Group Manager, each
  member's plain workspace linked to its own folder, no task, nothing moved,
  copied or read. Workspaces are found again by their recorded offer IDs, so
  Continue after a stopped member creates nothing twice.
- **Reconcile.** History reads (and the review/decline POSTs) record what each
  bound review's own stored offer already shows, monotonic and CAS on offer and
  prior state: `awaiting_user_review` → `applying` / `partially_applied` /
  `completed` (only with the read-back group and receipt digest) or `closed`.
  Reading an outcome never probes the folder. The history card shows each
  group's state, a review control only while reviewable, Decline only while the
  server says `declinable`, and Open group for a completed one; its aggregate
  state is completed, partially applied, applying or awaiting review.
- **Decline.** `DeclineAssistantArrangement` marks a current unapplied proposal
  `cancelled` (revision + 1), keeps typed intent as history and closes open
  bindings in the same transaction; the sidecar review is closed afterwards and
  its guard already refuses the cancelled record. A proposal with any confirmed,
  running or completed group cannot be declined; nothing is undone. Set up
  records nothing in this journal, so under the conversation lease the handler
  reads every awaiting group's own offer back first: one that cannot be read
  (pruned or rebound) or that shows a confirmation the journal missed is never
  declined (`arrangement_effects_unverified` / `arrangement_effects_applied`).

Restart drops the held scan: an unconfirmed arranged review then fails its guard
and asks for a new pick, while bindings, completed receipts and history survive.
A confirmed, unfinished arranged run is recovered like any confirmed run: pick
the folder again and review it, and the same offer returns for Continue; its
guard then needs only the local canonical event for that offer, not the
record's original observation. The record's other groups need that original
observation, so after a re-attach they ask for a new arrangement.

## Canonical storage and migration

One typed arrangement record (schema 1), one authoritative owner, no editable
shadow library. It contains origin (model/manual/rule-based), owner/context pins,
source revision, goal, exact resolved intent, revision, state, compiled-review
witness and operation journal. Pure wire Draft and stored record are distinct:
model handles are replaced by exact references/revisions at successful save.
Provider/model/skill fingerprints and coverage are provenance, not permission.

- **PA:** SQLite migration 81 adds `assistant_arrangements`, owned by the session
  store and keyed by arrangement UUID and canonical session ID. The
  `AppendArrangementTurn` writer saves it with the attributed turn in one
  transaction. Bounded reference listing supports history hydration without
  returning every draft's membership. `ContextCurrent` compares canonical
  context pins only; it never revives held source or provider authority.
  Ordinary/imported messages cannot populate the table. Reset clears it and
  deleting its session or originating message cascades. Save the first record
  with the atomic canonical answered folder turn, after ownership/revision checks.
  A separate host-only reference projection links each draft to its originating
  answer message; portable messages do not embed the record or its references.
  Later revision updates must use CAS and the current owning conversation and
  attachment revision; the initial insert API never overwrites a record. Cap 32 records/4 MiB
  per conversation; 1 MiB per resolved record. Byte limits may reject a record
  before its reference-count limit. Never evict retryable journals. Migration
  82 adds the `assistant_arrangement_effects` group-review journal (see **PA
  arranged groups**); its rows cascade with the record and session, are cleared
  by Reset, and a completion requires a read-back group and receipt digest at
  the schema level. Decline is the only PA intent-revision write today: CAS on
  record revision, `cancelled`, revision + 1.
- **Home:** `ProjectLibrary.arrangements` and `ProjectLibrary.collections` in
  the same canonical envelope, at most 32 arrangements and 100 collections.
  Home arrangements do not copy entry labels/notes into historical receipts:
  page them through current entries. Keep per-entry rationale with its entry
  reference so forgetting can redact it; clear free-text generated summary/goal
  and obsolete compiled projections when they might recover a forgotten name.
  The implemented `SaveArrangement` requires authenticated exact Manager access,
  live evidence and explicit schema-upgrade consent under the canonical Home CAS.
  A saved wrapper pins the **post-save** catalog revision and Manager binding:
  its own write does not invalidate it. Replays return canonical history, including
  redacted tombstones, never the replayed original text. There is no arbitrary
  collection writer or model-tool mutation entrypoint.
  Collection-only review/commit services compile exact canonical effects, persist
  a ten-minute witness, and atomically consume explicit owner confirmation with
  metadata plus per-effect consequence receipts. Owner-only HTTP and Home history
  controls expose this path: review persists no collection changes; each 50-row
  membership page reauthorizes current Manager/provider/runtime and catalog pins.
  Before/after names, membership counts, exclusions, rationale, unknowns and
  coverage are disclosed. Exact member IDs/names/last-observed availability are
  available across pages; paths and private child content are not. Explicitly
  unchanged/needs-review entries cannot be silently removed through an existing
  collection selector. Choices reset on page changes and uncertain outcomes
  require reopening canonical history, never an automatic retry. Unsupported
  creator intent is refused whole. One `select_activation` intention may be
  combined with collection changes: the whole metadata result plus a new review
  queue are written atomically. At most 100 distinct current catalog-only entries
  are selected, in stable ID order matching review pages. An existing active queue,
  linked entry or second selection operation fails the whole preparation. Single
  entry queues are allowed only in the v2 arrangement schema; existing manual bulk
  selection retains its two-entry minimum. The receipt records an opaque queue ID
  and selected count, never copied membership. Replaying it cannot restart a
  discarded/completed queue. It means only **projects awaiting independent review**:
  every actual project creation retains the existing exact-file, File-only,
  provider/grant/staffing review and explicit per-project confirmation. Preparation
  cannot use its token at that separate creation gate. Owner corrections in the Home
  review can rename create/rename intentions or exclude up to 50 currently
  proposed members from one create/set-members intention. These are local
  intention exclusions, not global exclusions from other overlapping collections.
  Saving a correction increments the canonical proposal revision, preserves
  original model provenance/runtime checks, attributes the correction to the
  owner, and invalidates every old review without applying metadata. A fresh
  review and explicit confirmation are required. The latest compact diff retains
  only counts/flags/timestamp, not old names or excluded IDs. Unsaved corrections
  block Apply/paging; uncertain saves retain the entered correction as an inert
  snapshot and require canonical-history recovery. An explicit saved-member picker
  offers 20 metadata-only candidates per page with literal name search; up to 50
  selected current Home entries may be added per correction. The review pins the
  catalog and proposal; additions receive exact current item revision pins and
  require another review. Global unchanged/unresolved selections cannot be
  overridden locally, while local exclusions can be re-added in a later revision.
  No picker action scans, grants access, calls a model or activates a project.
  Activation-selection membership uses the same editor and fresh-review boundary,
  excludes linked candidates, and must retain 1–100 catalog-only entries. A local
  activation edit does not alter overlapping collection intentions or approve
  project creation. The review's **Change what this proposal intends** section
  sends exactly one structural edit through the same witness/CAS: convert an
  activation selection back to catalog-only, add one activation selection of a
  collection intention's current catalog-only members (1–100, linked excluded,
  only when no activation or group intention exists), resolve an unresolved
  question as explicit unchanged or dismiss it, or release every explicit
  global unchanged item so a later local edit may add it. Each is a new owner
  revision with a count/flag-only diff, never an apply, queue or approval.
  PA proposals are applied per group through their own reviews (above);
  they have no member editor. Home conversational
  revisions use the separately consented planning path above.
  Owners can decline a current unapplied Home proposal without resolving a model.
  The same catalog/proposal CAS prevents closing an older proposal across another
  live review, and fences late model completions or old confirmations. Decline
  retains typed intent and saved revision questions as cancelled history; it does
  not undo effects, alter collections/queues or resurrect authority. Uncertain
  decline responses require canonical-history recovery. Unsaved editor changes
  must be saved or discarded first. An unresolved question can be declined but
  remains unavailable for effect review until explicitly resolved.
  Home
  `awaiting_user_review` and `completed` states require owning-store witnesses;
  an initial draft save cannot manufacture these transitions. Replay returns
  the stored receipt, not a recomputed success. Removal leaves referenced history
  readable/stale; Forget clears intent while opaque receipts retain no copied
  entry labels or membership. Read-only history may expose `collection_receipt`
  and must not label applied/redacted metadata as an unapplied draft.
  Home's Logical collections browser separately reads canonical collection cards
  (20 per page) and exact members (50 per page), with scope/catalog/root-state
  comparison pins and a 64 KiB projection cap. It deliberately uses saved catalog
  names, not child workspace lookups; exposes no paths, song facts or activation
  controls; and remains usable without a Manager/provider. Legacy reads never
  upgrade. Revocation preserves membership with truthful unavailable status;
  Forget removes references and invalidates old pages. Failed/stale reads clear
  the visible page and require explicit refresh rather than mixing snapshots.
  `SaveArrangementClarification` also completes into this same envelope, never
  a second transcript. Its explicit `kind: clarification` preserves the user's
  goal/source/provenance in an empty `needs_more_evidence` envelope and stores
  bounded untrusted discussion text separately from intent. It cannot carry
  operations, selections, references or a proposal rationale. Empty stored kind
  remains a proposal for earlier v2 records; history explicitly projects either
  `proposal` or `clarification`. Both outcomes share the 32-record limit, upgrade
  consent, owner/Manager checks, CAS and retry identity. Forget clears clarification
  text while preserving the kind of the stale tombstone; retry never restores it.
  The entire Home detail projection, including discussion and JSON escaping, is
  bounded to 64 KiB and checked before save. This is canonical storage support,
  now used by the opt-in Home planning backend and Organize library entry.
  The host-only `Store.PlanArrangement` now joins the shared Planner to these
  two completion paths. It resolves the exact currently bound primary Manager,
  checks owner/catalog/upgrade/sharing prerequisites before runtime resolution,
  and requires a read-only pinned runtime resolver with revalidation. It never
  allocates a private Planner, subscribes to scans or enables general tools.
  Changed goals, runtime/catalog drift and failed saves return no outcome;
  stale lost-response retries cannot call the model again. The goal must be the
  complete current goal; conversational revision/history wiring remains pending.
  `POST .../library/arrangements/plan` now supplies the application-owned Planner
  and `ServerBuilder.resolveArrangementRuntime`. This read-only resolver requires
  the local Manager (no global fallback), installed enabled pinned Home package,
  canonical primary-role prompt and plugin-only skill resolution without source
  collisions. Each declared skill must have one enabled/trusted binding admitted
  by the instance's pinned Toolbox (when assigned) or legacy access; broken pins,
  learned-source substitutions and configured prompt variants refuse rather than
  widen access. Role/skill trees, package generation/installation identity, local
  settings, registration pointer and access/assignment snapshots are revalidated.
  The general runtime resolver is not called: it can materialize MCP bindings.
  The route uses authenticated owner scope, exact request fields and explicit
  sharing/upgrade consent, returns canonical history only and sanitizes failures.
  Runtime unit tests stage synthetic package fixtures in disposable data. The
  maintained exact-candidate browser path additionally exports a clean Music
  commit, installs it only in sandbox state, and checks the complete staged skill
  and primary-role prompt in actual provider system messages. This remains
  scripted-provider evidence, not live-model judgment.
- **Library schema 1 → 2:** new reader accepts exact v1 and v2; no read-time write
  or authority switch. V1 cannot contain new fields even if empty. The first
  explicit arrangement outcome save (including clarification) or reviewed collection write atomically moves
  that already-initialized document to v2 under its old revision CAS. Disclose the
  local storage upgrade before Home Organize; it cannot be opened by older Ori.
  Preserve all existing roots, grants, links, scans, entries, sessions, history
  and receipts. Never initialize an absent library to save a suggestion.
  Minimal v2 decode/write foundation is needed with group 2's first Home draft;
  group 3 adds collection operations and full migration/recovery coverage.
- Existing schema-1 readers **refuse v2**; no downgrade/export rewriting or
  automatic rollback is supported. Preserve the raw envelope through disable,
  removal and reviewed package upgrades. The workspace outer schema and plugin
  Home declaration remain unchanged. Mirror divergence fails closed as today.
- A collection has stable ID, name, revision, exact member entry IDs, author,
  origin/arrangement reference and timestamps. No path, workspace parent, grant,
  stage or Assistant Project Link. Retain membership through rescan, revoked or
  offline roots and archive; show availability truthfully. Removed collections
  do not retain a hidden recoverable membership history.
- Explicit entry forget removes its memberships, invalidates affected proposals
  and reviews, redacts recoverable arrangement text/journal projections in the
  **same Home CAS**, and retains only opaque retry tombstones. Show these impacts
  in forget review. Pending creation effects cannot execute after forget. A
  rescan that later finds the source is new evidence, not history resurrection.
- Collection metadata + Home arrangement state + receipt are one envelope CAS.
  Any unrelated catalog revision conflicts until recompiled/reviewed. Replays
  use existing action/key/digest receipts; key collision is an error. External
  creator writes use the operation journal plus existing idempotent creator
  IDs, then a fenced Home/session update. If mirrors split, refuse and reconcile
  explicitly; never overwrite a receipt from the other mirror.

## Skill and companion boundary

Reuse installed plugin skill discovery and `ResolvePluginSkillByName`, which
refuses name collisions, plus exact Home instance skill bindings/access. Verify
installed/enabled package, trusted/enabled binding, canonical declaration's role
skill, provider generation/fingerprint and current skill digest. Generic runtime
skill merge alone is insufficient (agent-local names can override it). Read no
repository SKILL.md at runtime. Use `AppendSkillPromptsFromResolved` only after
these checks. A changed skill invalidates the pinned turn/review. Skill text does
not grant tools, decide confirmation or turn a PA into the Manager.

Music content-only candidate 0.3.0 retains both existing required host features,
Home/role/profile shape, schema/protocol version 1 and reciprocal REAPER contract.
Guidance checks the advertised tools; absent tools mean inert recommendation or
existing reviewed UI, not a claimed saved plan. Guidance-only Home upgrade already
supports prompt/skill changes. Exact-candidate setup, empty-Home 0.2.0/0.1.1
upgrades, both REAPER installation orders and restart have disposable browser
coverage; populated arrangement preservation and broader lifecycle acceptance
remain separate gates. Music source, disposable staging, immutable release and Ori's
reviewed floor are four distinct states. The candidate was published as Music
Project Management `v0.3.0` (commit `2d5f01e6daa24c77327a84376d8feb75b965d575`,
tree identical to the tested candidate) on 2026-10-18 by the owner's release
dispatch, and Ori's reviewed Home provider floor moved to `0.3.0` with that
commit as its fallback (`internal/reviewedintegration/entries.go`; evidence in
`docs/architecture/independent-program-homes.md`). REAPER/SDK/devtools changes
are not required by the effects above and the REAPER pin is unchanged.

Run `./scripts/e2e-pa-arrangement.sh music --source /absolute/clean/candidate`
for exact-candidate planning, revision, correction, collection-only review/apply,
and provider-loss history. It records the commit/tree/archive identity and
provider trace in `tasks/evidence-agentic-workspace-arrangement/music-arrangement-browser/`.
The full skill body and role prompt must appear in the system message; opening
history/context must not call the model or establish trust. This empty-catalog
scripted test does not establish large-catalog investigation or model judgment.
`home` still selects the synthetic-package regression; `pa` tests the PA path.

Guidance must leave room for host rules, schemas, evidence and revision history
within the existing 24,000-token turn budget. The original Music candidate's
full guidance failed before any model call; concise guidance now passes the
exact-candidate flow without changing host limits or truncating instructions.
The package checks skill/primary-prompt byte bounds, but these do not guarantee
that every larger inventory or multi-call turn fits: the host still checks each
request and refuses exhausted budgets.

Measured on the populated six-song suite (scripted model): the candidate's
second request (after one search and one select) is 21,057 bytes. An earlier
pre-call check counted request bytes as tokens, which left about 375 bytes of
margin there and refused the published 0.2.0 and 0.1.1 packages (9,626-byte
skill) on their second call with `limit_reached` before anything was saved.
The check now estimates the prompt instead: one token per three bytes until the
provider reports usage, then the observed rate with a 20% margin (never below
one token per eight bytes). The same 21,057-byte request is estimated at about
7,019 tokens before calibration, and the published packages finish an
investigate → propose turn. Reported usage is still charged in full against the
24,000-token budget after every call, so a model that really spends the budget
is still stopped. The reviewed Home upgrade to the candidate keeps working as
before: library, roots and projects are kept, and the owner's existing skill
trust carries over.
The upgrade review names the release's skills but does not show that a skill's
text changed (only role-prompt diffs are shown); that disclosure belongs to the
package-upgrade review, not this feature.

A typed 409 planning refusal (`upgrade_required`, `model_unavailable`, `busy`,
`stale_context`, `limit_reached`, `invalid_result`, `unavailable`) is decided
before the single canonical save, so the Home panel reports it as certain and
lets the owner choose again. Cancellation, timeout, `planning_failed` and a lost
response may have raced a save and still require reopening canonical history.

## Implementation seams and validation gates

Freeze names: `workspacearrangement/{contract,evidence,tools,planner,record,review}.go`;
`personalassistant/folder_arrangement.go` for held-only projection;
`projectlibrary/{arrangements,collections}.go` for canonical Home operations;
`session/assistant_arrangement.go` for atomic PA persistence;
`agenthttp/personal_assistant_arrangement*.go` and
`server/workspace_arrangement*.go` for caller-bound wiring;
`sessionhttp/assistant_library_arrangement.go` for Home HTTP;
`web/static/js/modules/workspace-arrangement*.js` for shared paged review.
The new core package imports neither HTTP nor personalassistant/projectlibrary;
adapters depend on it. No general agent framework or second creator.

Required threats: prompt injection/forged IDs; Manager impersonation; cross-Home
and attachment replay; root/symlink swap; provider/skill drift during awaits;
failed canonical save; unknown JSON; old tabs; idempotency collision; restart;
forget redaction; source-file hashes unchanged. Test both API wire and real host
request composition. Maintain no-model/manual regressions and entry-point parity.

Performance/evaluation gates: 1,000 saved entries across ≥4 roots; separately
5,000-entry and 8 MiB limits. At least 100 real warm metadata requests, p95 ≤250 ms;
record cold/warm query/review/apply times, hardware and fixture bytes. Review first
page target ≤500 ms warm, metadata-only apply ≤1 s at 1,000 memberships; report
and justify failures rather than benchmarking an empty catalog. Cancellation
must return within 2 s after provider cancellation in the fake harness and never
publish a late draft. No creator latency promise. Per-turn call/token/byte/time
limits above are hard bounds. Record three goals over the same catalog, later
correction, ambiguous nested/partial evidence and no-model behavior; repeat paired
goals at least twice with an explicitly authorized real model. Scripted tests
and live judgment observations remain separate; unavailable live checks are
NOT RUN, never substituted by a green fake.

Measured (opt-in `ORI_ARRANGEMENT_PERF=1 go test -short ./internal/projectlibrary
-run TestArrangementEnvelope -v`, normal build, Apple M5): 5,000 entries over
four roots (2.4 MB document) — warm evidence search p95 151 ms, review page p95
126 ms, first review page 304 ms, save 446 ms, metadata apply of 1,250
memberships 267 ms; a 5,001st entry or an 8 MiB+ document is refused with no
write. Canonical reads re-read the Home on every call by design; review pages
project their members from the page's own authorized snapshot rather than
rebuilding full planning evidence, and in-memory workspace copies use compact
JSON. Seeded observations are fixture metadata, not a discovery claim.
