# Workspace arrangement v1

Status: **bounded core and characterization, not a shipped arrangement workflow**.
Inert intent, held/saved evidence adapters, batch-validated tools and the bounded
API-provider planning loop, atomic PA draft storage, opt-in PA/Home endpoints,
read-only pinned Manager guidance and shared inert history cards are implemented.
The PA attachment disclosure/Send path now reaches a canonical saved proposal.
Home Organize library also reaches canonical proposals and clarifications through
explicit sharing/upgrade consent. Correction/review and execution remain required;
no Apply UI is enabled.
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
  observed availability, root handle, collection handle), sort by name/ID, opaque
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

| Budget | v1 bound / failure behavior |
| --- | --- |
| Source | 5,000 catalog entries or existing bounded attachment scan; never scan to fill gaps |
| Model search | 20 rows per call; user review pages at most 50 |
| Tool result | 16 KiB encoded JSON; return a bounded error/narrowing request, never cut JSON |
| Newly shared evidence | 64 KiB per turn including overview and results |
| Each full model request | 96 KiB, including rules, skill, history, tools and evidence; no silent truncation of authority |
| Provider work | 8 calls, 12 total tools, 2,048 output tokens per response, 24,000 cumulative charged tokens, 90-second deadline |
| Missing token accounting | Charge conservative UTF-8 byte upper bound for request + response; stop before the next call when insufficient budget |
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
  before its reference-count limit. Never evict retryable journals.
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
  redacted tombstones, never the replayed original text. No public arbitrary
  collection writer or review/apply path is wired yet.
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
  Runtime tests stage synthetic package fixtures in disposable data, not the
  missing Music candidate; shared browser and candidate-guidance acceptance remain
  unfinished.
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
supports prompt/skill changes, but actual 0.2.0/0.1.1 → candidate integration is a
separate gate. Music source, disposable staging, immutable release and Ori's
reviewed floor are four distinct states. No pin/release is authorized here.
REAPER/SDK/devtools changes are not required by the effects above.

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
