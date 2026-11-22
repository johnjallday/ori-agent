# File Janitor group-first review contract

The feature is shown to users as **Filing**; internal File Janitor identifiers remain unchanged.

Status: contract selected in the task 1.1 characterization spike and extended by
the `filing-picture-review` feature (the Review tab is a picture of the folder
after filing, filed by one press, with a receipt and Undo all). Implementation and
acceptance evidence are tracked separately in the feature findings log. Base
inspected: `48d8e5392857a0517084bf1ce70cf9313530826c`.

Four clauses of the original contract changed with that feature and are rewritten
in place below: confident folders are ticked on open, a question group can be placed
with one choice, the plan check and one File request replace preview then confirm,
and "Not now" became "Leave here". The **Picture review** section near the end
keeps the new routes, fields and storage, and a table of what each clause said
before.

## Authority and compatibility

Groups describe **one saved scan batch**, not a live folder inventory. Reading
saved proposals reads Ori state only: no Status/readiness call, root resolution,
stat, directory enumeration, content read, classifier call, setup, directory
creation, approval issuance, or draft persistence. A missing pending batch is a
known empty result; missing access, invalid provenance, or unreadable state is
unavailable, not empty success. Historical batches are explicitly selectable;
a newly arriving batch must not retarget an open review.

Use `reviewInstall`, `StoredManagedRoot`, and `reviewBatchScope` to validate the
current owner, workspace, capability installation, saved grant/reference, root
generation, and batch/candidate identity. Recheck the full scope after reads and
inside locked writes. Never serialize the managed root path or fingerprint.
An attached directory without completed Janitor setup is not consent.

New scans already save `root_id` and `capability_installed_at`. Legacy batches
without those fields remain readable under the existing `reviewBatchScope`
policy: a non-migration installation newer than a legacy batch cannot adopt it.
Legacy category decisions remain destinations only; they **never** manufacture
selected draft entries. The optional Curator is not an authority or a required
runtime dependency. Both `file-janitor` and `downloads-janitor` prefixes register
the same handlers. The filesystem MCP connector contract is unchanged.

## Projection and accounting

Implement a pure projection from one validated batch and its canonical
candidates. Use stable, fixed group keys hashed into opaque IDs (not names or
paths), with one primary group per actionable candidate. Proposed categories,
not `EffectiveCategory`, determine membership; editing a destination cannot
move a file between proposal cards.

Precedence: sensitive-looking name → possible copy/version family → audio or
related-material question → unresolved classification → recognized category →
unmatched review. Keep every overlapping caution as an annotation. A failed
candidate is accounted separately and requires fresh retry review, not routine
select-all. Skipped, applied, stale, applying, and unknown states are separate
accounting buckets, never quietly actionable. Ineligible observations are scan
observations, not additional candidates: report their count separately. The sum
of candidate accounting buckets equals the batch candidate total.

Sensitive-name rules match bounded whole tokens (e.g. passport, password,
medical, tax, confidential), not arbitrary substrings such as “taxi”. Possible
version/copy rules require an explicit bounded suffix (`(2)`, `copy`, `v2`,
`version 2`) and another same-stem/same-extension candidate; ordinary numeric
sequences such as `photo-001` are not version evidence. No newest/signed/
redundant verdict. Rule version **3** groups audio metadata/project extensions
and other bounded filenames sharing an exact normalized saved audio stem.
There is no REAPER-specific project-format recognizer in this host capability;
unknown standalone project files retain the classifier's unresolved question. These are broad questions, not verified shared projects; unrelated
documents do not join by proximity or a guessed project name. Archives are not
opened or compared. Overlong legacy names route to the unmatched metadata
question, never a fabricated sensitive-word finding. Unrecognized types remain
reachable, not guessed safe.

Cards state the rule and its uncertainty. Classification reasons/provenance
remain the saved actual values (`metadata`, `model`, `content`, `fallback`);
only the grouping rules are metadata-only. Do not make a blanket claim that all
saved candidates were classified without content access.

## Read shapes and bounds

- `GET /batches/{batchID}/groups`: `batch_id`, `revision`, `rule_version`,
  whole-batch `counts`, and fixed-vocabulary `groups`. `latest` is a navigation
  convenience only; edits always name the resolved concrete batch ID.
- Each card: opaque `id`, `kind` (`filing` or `question`), fixed `label`,
  `reason`, optional fixed `question`, proposed `category`/relative destination where applicable, whole
  member `count`, and at most **3** safe display-name samples. Do not serialize
  all member IDs, batch `candidate_ids`, ineligible names, paths, or tokens.
- `GET /batches/{batchID}/groups/{groupID}/members?limit=50&offset=0`:
  `batch_id`, `group_id`, `revision`, whole-group `total`, `limit`, `offset`,
  and bounded existing path-free candidate DTOs plus caution annotations.
  Retain the existing **50 default / 200 maximum** member-page policy. Invalid
  numeric parameters are 400, large positive limits clamp to 200, offsets past
  the end yield an empty page. Addition uses remaining length, avoiding integer
  overflow for a very large valid offset. A requested revision mismatch is 409.
- `GET /batches/{batchID}/draft`: current intent revision, scope-bound saved
  explicit choices, and removed/invalid-choice accounting. This is read-only:
  reconciliation is a projection; stale stored entries confer no authority and
  are removed atomically on the next edit/preview transaction. Returning saved
  intent revalidates metadata for **selected names only**, using existing root
  resolution and fingerprints: no enumeration, content opens or classification.
  This is intentionally distinct from the pure proposal/group reads above.
  Choice DTOs include a derived current `group_id` for off-page counts; it is
  not persisted provenance or a rule for future group members.

At most the category registry plus five question/unmatched cards are returned;
there is no per-family unbounded card list. Member lookup/group construction
uses indexed linear passes and stable batch order, not pairwise filename
comparison or per-file network requests. All-files remains paged independently.
Batch listing keeps its existing 20 default/100 maximum pagination. Its
additive `summary_only=true` view omits candidate IDs and ineligible names for
the saved-batch chooser. The existing candidate read also accepts an optional
`candidate_id` navigation hint: it locates that member's bounded page and returns
`linked_candidate_found`; an absent/foreign ID never widens to another batch.
This keeps even an off-page candidate deep link usable without fetching an
entire batch inventory.

## Revisions and typed edits

`revision` is a SHA-256 digest of a canonical encoding of rule version, full
server-only scope identity, concrete batch ID, ordered exact candidate IDs and
fingerprint keys, proposed classification/eligibility, state, and primary group
keys. Destination overrides, unrelated timestamps, or approval-token churn do
not change proposal membership. Draft intent has a separate revision, bound to
scope and canonical exact choices/categories; it is not a timestamp. Relevant
legacy destination edits change intent evidence even if membership is stable.

Use a closed JSON edit shape at `POST /batches/{batchID}/draft` with
`expected_revision`, `expected_group_revision`, one `action`, and only its
allowlisted fields (`group_id`, `candidate_ids`, `category`). Unknown fields,
unknown actions, duplicate IDs, foreign members, invalid categories, and stale
scope/revisions reject the **entire** transaction. No implicit partial selection.
Actions: select group, select filing groups, select file, exclude file, set
category, deselect group, clear group-added choices (Undo), clear all choices,
mark/unmark per-file Trash, Leave here, stop asking (`keep_group`, `keep_files`),
and targeted reconsider. Deselect group removes all exact move choices in that
group but leaves separate Trash marks; Undo removes only choices with that group's
selection provenance. There is no group Trash action.

Resolve group members under the canonical `UpdateScanState` lock. Group select
and select-all use the exact revision displayed, not currently fetched member
pages, not a future rule. Selecting a **question** group is allowed and requires
a `category`: it places exactly the members of that group at the revision shown,
once, in one folder. Routine select-all (`select_filing_groups`) excludes every
question group, unresolved Needs review member, non-actionable/failed member, and
existing explicit Trash mark. A scan that has no saved choices opens with every
filing group ticked (derived on each read, stored only when the person first
edits; see **Ticked on open**), and Ori never ticks a question group. The tick
binds to exact files only when File is pressed, at the membership the picture
showed. A category edit without a selection edits the canonical candidate but
leaves it unselected.

Use **1,000 choices per batch**, **1,000 candidate IDs per edit**, **10,000 total
retained draft choices per workspace**, and **256 KiB edit bodies**. Refuse
oversized edits with a stable bounds error before writes; never truncate. Pending
choices survive pruning. Resolved/orphaned choices prune with their canonical
batch/candidate; if pending work fills the cap, reject additional choices with a
clear recovery rather than discarding old pending work. No client-side fetching
of all pages is needed to select a 206-member group. Draft reads also return a
fixed-vocabulary per-group selection/destination projection, including exact
selected/Trash/group-added counts and mixed canonical destinations. These are
read projections, not stored group booleans or another destination map.
The plan check and the File
request cap a plan at 1,000 operations; above the cap the picture says so ("Ori files up to
1,000 files at a time. Untick a folder to continue."), the main button is off, and
the person unticks a folder to bring it back.

## Durable intent and schema

Selected migration: scan state **2 → 3**; settings remain **3**, compiled
capability definition remains **1**. Add `review_drafts` to the existing atomic
scan record; do not introduce browser persistence or another state file.

Each typed draft contains `schema_version: 1`, `batch_id`, `owner`, `root_id`,
installation timestamp/version, reference/binding scope digest, monotonic edit
generation, and exact `choices`. Each choice stores `candidate_id`, fingerprint
key, `operation` (`move` or explicit `trash`), and `source_group_id` (empty for
an explicit per-file choice). The candidate's canonical `DecisionCategory`
remains the single destination truth; the revision binds its current value.
The candidate's additive `decision_group_id` stores destination-edit provenance
only (never a second category). This distinguishes a prior per-file override
from a group destination after deselect/reselect. Legacy schemas clear this new
provenance on read; a legacy category edit remains a per-file override. Group
destination controls edit selected group-added choices only, preserving prior
per-file overrides; unselected member category edits remain per-file actions.
A draft contains no token, acknowledgement, expiry, reusable approval, path, or
second authoritative category map.

Group selection adds only absent choices, preserving prior per-file overrides,
Trash marks, and unrelated choices. Group Undo removes only choices originating
from that group; it never calls filesystem Undo. A subsequent per-file edit
promotes that choice to explicit per-file provenance, so later group Undo leaves
it intact. Per-file exclusion removes the exact choice; group state is computed
as selected/eligible counts (including partial and mixed destinations), not a
stored group boolean. Selecting the group again is another explicit action.

Inspect raw scan schema **before** normalization. Versions 0/1/2 load with empty
intent regardless of stray draft-shaped fields; loading never migrates by
writing. Unknown future scan schemas refuse mutation rather than down-writing
unknown intent. Unknown/malformed draft schemas produce no selected choices and
an explicit invalid-draft state requiring clear/review. Missing, duplicate,
foreign, malformed, orphaned, stale, or mismatched choice references never expand
to all members of a group with a similar label. Failed persistence leaves the
previous durable state unchanged; UI shows unsaved and cannot preview.

### Approved alias-reference compatibility

Delivery characterization found that existing setup canonicalizes `RootPath`
but intentionally preserves an adopted directory reference's alias spelling.
Strict saved-only scope comparison must not guess that arbitrary spellings
identify the same root or resolve symlinks during a group GET.

Settings schema remains **3**, with additive optional `approved_reference`:
`root_id`, `reference_id`, and the exact server-observed `path`. This is metadata
proof captured **only at an explicit successful folder grant**, after resolving
the current reference to the reviewed canonical root. It is not another root,
a new filesystem permission or an editable destination. Saved-only readers
accept the existing canonical spelling or this exact generation/reference-bound
proof. Execution still resolves and validates the current physical root.
Legacy missing proof is never inferred or migrated from an alias; canonical
legacy references remain compatible, while an unproven alias needs explicit
folder review/regrant. Mismatched proof, changed reference, revoked access or
reinstall cannot widen it. Older readers may ignore/drop the optional metadata;
that loses alias readability on re-upgrade rather than broadening permission.

## Leave here, stop asking, and Review again

Typed edits `keep_group` and `keep_files` resolve exact displayed IDs under the
same draft/group CAS and root → scan-state lock. Each current regular-file
fingerprint is checked; a changed member rejects the entire edit. **Leave here,
stop asking** (the menu entry for these edits; the code and journal still call it
keep) records existing exact fingerprint skips, marks those candidates
skipped, clears only their staged choices, and invalidates matching unspent
preview authority in one transaction. It changes no managed file. Existing skip
retention remains 2,000 newest skips, no age expiry: disclose that changed files
may be proposed again and old skips can age out by the retention cap.

The typed `reconsider_files` edit accepts candidate IDs (never client-supplied fingerprint keys),
derives each requested skip server-side, and clears only those skip records.
The old candidate stays skipped/resolved. Offer an explicit **Scan now**; do not
silently scan, select a replacement, restore old approval, or resurrect the old
candidate. All-files reads derive `skip_remembered` for kept candidates from the
canonical skip records, rather than a second remembered-state field. Kept access
and the saved-batch picker remain available after all candidates are resolved.
Existing whole-skip-reset endpoint stays compatible. **Leave here** is the plain
state a question is in until the person answers it: the files stay pending and
reachable, nothing is remembered, nothing is granted and no timer runs. There is no
separate "Not now" on a question or a card. Use the same semantics for music and
versions; do not label it “for now”.

## The approval fence: plan check, then one File request

The review no longer has a preview step or a confirm screen. `GET .../plan` draws
the picture's numbers and final file names with **no authority**; one
`POST .../file` states the plan it saw (the saved-choice revisions, the plan digest
and the acknowledged Trash count), the server works the plan out again under the
root lock, and only if it is identical does it issue **and spend** the approval
inside that request, then apply through the per-file path below. If anything
differs it moves nothing and returns the fresh plan with the changed files named.
Everything that follows in this section (token hashing, expiry, consumption,
idempotency, journal, scope binding, per-item admission) applies unchanged inside
that request. The older `POST .../preview` and `.../apply` routes stay for
compatibility and per-file retry, under the same fence.

The existing `PayloadHash` covers candidate IDs, operation, effective category,
and saved fingerprint, but not selection/deselection intent or current scope.
Characterization found that clearing a decision after preview does not change
that hash when the submitted category is explicit. Therefore plan hashing alone
cannot implement the required two-tab deselection guarantee.

Retain canonical approval records, token hashing, expiry, consumption,
idempotency and journal. Add optional draft/scope binding fields to that record,
not to the draft. New draft preview resolves the saved exact choices and compares
intent revision under the same scan-state transaction that issues approval.
Store the concrete scope and draft revision on that approval. Confirm compares
both under the token-consumption lock, independent of client-submitted items.
Any relevant legacy decision change also invalidates unconsumed approvals for
the touched batch; consumed records survive for honest replay.

Serialize edits and confirmation through a consistent workspace/root execution
boundary **before** acquiring scan-state locks; move the existing apply root
lock ahead of token consumption as needed. Thus a deselection that wins first
invalidates the token, while an apply already admitted cannot be retroactively
undone by a later edit. Recheck lifecycle/scope and candidate admission at the
journal-before-mutation boundary for move and Trash. Do not claim that a token
check outside the lock prevents a concurrent edit. Reset admission is the
server's ordinary finite-HTTP-work middleware; new routes must remain inside
it, without stream classifications or detached execution.

Preview never reads file contents, but unlike group reads it does re-stat
sources and resolve collision names using the existing execution path. Confirm
continues current exact-file checks and observed per-file outcomes; changed
sources do not move. Collisions may be re-resolved at apply time under the
existing never-overwrite policy. Cancellation discards local authority, not
saved intent. Neither reload nor protection return restores a confirmation or
Trash acknowledgement. Applied/stale/failed intent is reconciled against actual
results, never replayed automatically. Typed `retry_files` revalidates an
unchanged failed source and returns it to an **unselected** review; it requires
explicit selection and a fresh preview. Changed sources instead require a fresh
scan. Existing fresh legacy per-file retry previews remain compatible.

Execution rechecks settings/root and immutable approval scope before preparing
each destination, inside the atomic applying-journal write, and immediately
before the existing move/Trash handoff. It validates that exact candidate's
batch, operation/category and fingerprint; a canonical skip prevents execution
through another old candidate. It does not compare the original whole-draft
revision after each item, because preceding successful items legitimately
change the batch. A handoff already admitted cannot be retroactively undone by
later revocation; every following item has its own admission. No new executor,
connector protocol, background continuation or filesystem enumeration is added.

The plan check returns whole-operation/destination totals and final file names for
the files the picture draws (no mandatory row tour), and the Trash count is
acknowledged separately in the request: the tick is never set for the person and
never restored. A refused or failed press discards captured local authority while
leaving canonical choices to be reconciled. Canonical
protection returns pin the concrete batch, reload its winning server draft,
recheck the saved protection binding and consume the navigation correlation.
They neither require that batch to remain latest nor replay browser choice IDs;
partly completed/other-tab edits are reconciled normally.

## Pre-feature characterization

These observations describe base `48d8e5392`, not the new grouped path:

- Category picker sent legacy `DecisionMove` while checkbox selection lived in
  a separate in-memory Set. Restart restored destinations but no selections.
- Existing candidate endpoints already page at 50/200 and return whole-batch
  counts, but serialize the full batch including candidate IDs. New group reads
  use a bounded batch summary shape instead.
- Batch provenance is captured by scans, and saved review projections already
  reject foreign scope without probing the managed folder. Reuse these seams.
- Protection departure saved ID-only choices and invalidated local approval;
  return enumerated every candidate and required the batch to remain latest.
  The grouped path replaces this with pinned canonical draft reconciliation,
  not replay of a browser selection snapshot. Active concrete batch identity is now encoded in
  the review URL; scanning announces newer work without replacing an open or
  selected review. Reload restores that pinned batch, never confirmation.
- Store locking and atomic rename already serialize canonical scan writes.
  Raw scan schema was normalized unconditionally; the draft migration now
  preserves unsupported-schema evidence and refuses unsupported writes.
- No new plugin/provider/devtools release or connector protocol is required.

## Picture review

Contract for the `filing-picture-review` feature (PRD `R` numbers refer to its
requirements), selected in its task 1.1 on base `3add0ffb6`. The four clauses
below have been rewritten in place in the text above; this section keeps what each
one said before, and the routes, fields and storage the feature added.

### The four clauses that changed

| Clause above | After this feature |
| --- | --- |
| "No group is selected on entry." | A scan created by the picture-review build opens with every **filing** group ticked, derived on each read and stored only when the person first edits. Question groups are never ticked. A press binds the tick to exact files at the revision the picture showed. |
| `select_group` refuses question groups. | `select_group` on a question group is allowed with a required `category`; it places exactly that group's members at the revision shown. Question groups stay out of the on-open ticks and out of `select_filing_groups`. |
| Preview issues an approval; a second request confirms it. | `GET .../plan` draws the picture with no authority. One `POST .../file` states the plan it saw; the server recomputes it under the root lock and only then issues **and spends** the approval inside that request. The older preview/apply routes keep working unchanged. |
| "Not now" is a collapsed card. | "Leave here" is the same state: pending, nothing remembered. |

Unchanged: one scan at a time, exact fingerprints, never overwrite, no path or
token in any response, per-file Trash only, the 1,000-file cap, reads that do not
write, the connector contract (`mcp_mover.go`, the `Mover` interface).

### Ticked on open (task 1.2)

- `JanitorBatch.review_defaults` (string, `omitempty`) is set to
  `"filing_groups_v1"` only by `scanNow` when it creates a batch
  (`scan_service.go`; the only production site that appends batches).
- `reviewDefaultsApply(state, batch)` is true only when **all** hold: the batch
  has the marker; no `ReviewDraft` record exists for the batch (valid, invalid or
  stale); no `FilingRecord` exists for the batch; every candidate of the batch is
  `Pending` with `DecisionNone` and a zero `DecidedAt`.
- `projectReviewDraft` takes the groups. When the rule is true it returns
  `State:"default"` and one move choice per member of every `Kind=="filing"`
  group, `SourceGroupID` = that group's ID: exactly what `select_filing_groups`
  would add to an empty draft. `revalidatedReviewDraft` then checks metadata for
  those names as it does for saved ones. No enumeration, content read or write.
- Nothing is stored until the person edits. Any typed edit creates a draft
  record starting from the view's choices, so unticking one folder keeps the rest
  and clearing everything stores an empty record that never re-ticks. A legacy
  `/decisions` change sets `DecidedAt`; any apply or a filing record also ends the
  defaults for good.
- A file that arrives later is in no batch member list (fixed when the scan
  creates the batch), so it can never join a tick. It appears only in a new scan.
- **Older scans** (no marker, including ones saved by the card-grid version, and
  scan files of schema 0, 1 or 2) open with nothing ticked and their saved choices
  unchanged. This follows "older files load with no ticks invented"; it is a
  one-line change in `reviewDefaultsApply` if the owner prefers to tick them.
- A default view can exceed 1,000 files (a scan has no size cap). The caps do not
  change: the plan check reports `over_limit`, the press is refused, and an edit
  from such a view that would leave more than 1,000 choices is refused with
  `review_bounds` like any other, so the person must untick enough in one step.
  The picture says "Ori files up to 1,000 files at a time. Untick a folder to
  continue." for both the disabled button and that refusal (R39).
- Lock order is unchanged: reads take no lock; edits take `lockRoot` then
  `UpdateScanState`.

### Question group to one folder (task 3.1)

- `select_group` on `Kind=="question"` requires a non-empty `category`
  (`ErrInvalidAction` otherwise). For each member at the revision shown it skips a
  file with a Trash choice, a move choice from another group, or
  `Decision==DecisionTrash`; otherwise it sets `Decision=Move`,
  `DecisionCategory=category`, `DecisionGroupID=group.ID`, then selects the move
  with `SourceGroupID=group.ID`. Choosing another folder for the row rewrites only
  the choices that came from that group.
- A group tick (`select_group`, and `select_filing_groups` for each filing group)
  ticks the members that are still the scanned file and leaves a member that changed
  or went missing since the scan unticked, instead of refusing the folder: one
  changed file would otherwise make the whole folder impossible to tick. Only when a
  `select_group` ticks nothing because every candidate changed is it refused
  (`ErrCandidateNotActionable`, wrapping `errMemberChanged`, HTTP 409
  `review_member_changed`); the browser shows that as a notice, not a failed save.
- One predicate `moveAnswered(candidate)` (the fixed question-row IDs are a package
  set, `questionGroupIDs`, so it needs no group argument) replaces the four places that
  reject a `NeedsReview` move whose `DecisionGroupID != ""`:
  `selectMove` (`review_draft_service.go`), `projectReviewDraft`
  (`review_draft.go`), `validExecutionCandidate` (`review_execution.go`) and the
  plan check. It accepts a file that does not need review, or one with a
  `DecisionCategory` whose `DecisionGroupID` is empty or the ID of a **question**
  group. All must change together: changing only the draft side would let a placed
  file pass the picture and then fail the per-item fence as a conflict.
  `reconcileLegacyDraftDecision` keeps dropping such a choice on a legacy category
  edit, the safe direction.
- Choosing another folder for a row rewrites the choices that came from that row
  (`SourceGroupID == group.ID`, a move) and never a Trash mark or a choice the
  person made for one file.
- Row "Leave here" is `clear_group`; for question groups it takes back every move
  in the row, including a file the person placed one at a time (the picture counts
  it as placed in its row, so the row menu has to be able to undo it; a Trash mark
  stays), and it also clears
  `Decision`, `DecisionCategory`, `DecisionGroupID` and `DecidedAt` where
  `Decision==Move` and no choice is left, so nothing is remembered.
  Row "Leave here, stop asking" is `keep_group`; for question groups it skips
  only members without a Trash choice or a per-file move choice (those are drawn
  elsewhere, so each file stays in one place). Filing-group keep is as today.

### Pairs, bundles and the first screenful (tasks 1.3, 4.1)

- `ReviewGroupMember` gains `Family`, `Ending`, `Bundle`; `ReviewGroup` gains
  `Key`. They are filled in a second pass after the main grouping loop and are
  **not** added to the revision's encode calls, so membership and `revision` do
  not change and `ReviewGroupRuleVersion` stays **3**. A golden revision captured
  on the base commit proves it before any edit to `review_groups.go`.
- `family_id`: `fam-` plus 24 hex of `sha256("fj-family-v1\0"+scope+"\0"+batchID+"\0"+versionFamily(name))`
  for members of the possible-copies group. `ending`: `number`, `copy`, `v`,
  `version` or `none`. `bundle_id`: `bun-` plus the same kind of hash, for audio
  members whose `reviewNameStem` is shared by at least two audio-group members.
  The scope digest is server-only, so no ID reverses to a name.
- `GET .../batches/{batchID}/picture` is the one bounded read. It resolves
  `latest` like the group read, always returns the concrete `batch_id`, reads
  groups, revalidated draft and placement from one snapshot, and sets `no-store`.
  Fields: `batch_id`, `revision`, `draft_revision`, `draft_state`, `rule_version`,
  `counts`, `waiting`, `removed_stale`, `content_read` (`none|some|unknown`),
  `folders[]` in category-registry order `{category, destination, group_id,
  group_state: ticked|partial|unticked|none, count, files <=5, unfiled_count,
  unfiled_files <=5}`, `loose[]` per question group `{group_id, key, count,
  placed_count, placed_category, trash_count, loose_count, singles <=12, more}`
  (`count` is the whole group and equals `loose_count + placed_count +
  trash_count`; `placed_category` is set only when every placed file shares one
  folder). A row also carries `pairs` (at most three `{family_id, count, files
  <=6, more}` cards, possible-copies row) and `bundles` (at most three
  `{bundle_id, name, count, files <=12, more, loose_count, placed_count,
  placed_category, trash_count}` cards, music row). `singles` are then only the
  loose files with no sibling, and `more` counts the loose files beyond everything
  drawn. A card lists every member wherever it is now (`placement` loose, filed or
  trash), the file with no `ending` first, so a proposed Trash choice reads "Marked for Trash"
  on its own line; a card is drawn while any member is loose or placed and is
  left to the Trash box when every member is in Trash. `name` is the first file's
  name without its extension, display text only. The picture also has
  `left_out`, `trash`, `could_not_file` (`{count, files <=12}`). A file entry is
  `{id, name, size, modified_at, category, your_choice, placement, family_id,
  ending, bundle_id}` (the last three from task 4.1) plus `state`, `reason` and
  `can_retry` for a file that could not be filed: no path, fingerprint, token or
  choice list. `ReviewGroup` gains `key`, the fixed rule word behind its opaque
  ID; the picture's labels and "Why?" sentences are browser copy keyed by it.
- The picture response advertises `can_reveal` separately from classification consent.
  **Show in Finder** sends `POST .../batches/{batchID}/picture/files/{candidateID}/reveal?revision=…`
  from the local macOS window. It selects the current source in Finder for the person
  to inspect with Quick Look. It does not open contents in Ori, edit the draft, or
  execute the plan. The server derives the path from the approved folder and the
  unchanged saved candidate, never from browser-supplied path text. Remote clients
  and sessions with desktop launches disabled do not offer the control.
  **Open** posts to the sibling `/open` route with the same review binding and
  its own `open` action header. An optional per-file `viewer` names the Mac app:
  Preview for PDFs and supported images, TextEdit for supported documents, or
  QuickTime Player for supported media. The server chooses the reader from the
  verified source name, not the browser. Unknown formats remain Finder-only;
  opening never asks the OS to execute the source itself.
- One pure `placeReviewFiles` puts each waiting member in exactly one place:
  Trash choice, else move choice (its category's folder; `your_choice` for a
  question-group or hand-changed file), else a filing member goes to its folder's
  dashed `unfiled` list when none of its group is selected and to `left_out`
  otherwise, else a question member to its row. Failed and stale files go to
  `could_not_file`, and so does a waiting file whose saved choice was removed
  because the file changed or went missing since the scan (`state` `changed` or
  `missing`, never `can_retry`): it is not "left out by you", the person did not
  leave it out. `changed` in the picture counts those files. The section counts add
  up to `waiting` (R8).
- `GET .../picture/files?section=filed|unfiled|loose|left_out|trash|could_not_file|family|bundle&key=&revision=&draft_revision=&limit=50&offset=0`
  pages the same placement (50 default, 200 maximum); a mismatch is 409
  `review_changed`. For `family` and `bundle` the `key` is the pair's or bundle's
  opaque ID (`fam-` or `bun-` and 24 hex digits, else 400) and the page holds every
  member of that card with its own `placement`; the browser reads a bundle this way
  so one bundle choice covers every file in it, not only the twelve the card shows.
- `content_read` is computed over every candidate: `some` if any `Classifier` is
  `content`, `none` if all are `metadata`, `model` or `fallback`, else `unknown`.
  The lead line's third sentence follows it; for `unknown` it is left out rather
  than guessed. Nothing in production sets `content` today, so that wording is
  tested but cannot appear yet.

### Plan check (task 2.1)

- `GET .../batches/{batchID}/plan?revision=&group_revision=&destination=<category|trash>&limit=50&offset=0`
  or `...&ids=a,b,c` (at most 200 distinct candidate IDs, not together with
  `destination`; `items` is then exactly those files' lines, which is how the
  picture learns the final name of each file it shows), `no-store`; a revision
  mismatch is 409 `review_conflict`, a bad `ids` list 400.
  `Service.PlanReview(ctx, owner, ws, batchID, ReviewPlanQuery)` in
  `review_plan.go` reads `loadScanState(ws, true)` and never calls
  `UpdateScanState`, `MkdirAll` or the root lock. It `Lstat`s each file once and
  opens none, so a file readable by nobody is still planned.
- Response: `batch_id`, `revision`, `group_revision`, `plan_digest`, `move_count`,
  `trash_count`, `total`, `over_limit`, `destinations[{category, destination,
  count, renamed_count, first <=5}]`, `trash{count, first <=5}`, `changed{total,
  items <=50 {candidate_id, name, reason: changed|missing}}`, and with
  `destination` a page `items` of `{candidate_id, name, operation, category,
  final_name, renamed, size}` plus `items_total`, `limit`, `offset`.
- Walk the effective choices in batch candidate order with one `reserved` name
  map; apply runs in the same order, so final names agree when nothing changed.
  Above 1,000 operations the check returns 200 with `over_limit:true`, counts
  only, no stat, `plan_digest:""`; only the press returns 413 (R39 needs counts).
- `preview.go`'s per-item logic becomes `planLine(settings, root, candidate, op,
  category, reserved)`; `PreviewMoves` is that in a loop plus issuing approval,
  behaviour unchanged. Name-clash resolution stays only in
  `resolveAvailableNameReserving`. `PayloadHash` is unchanged.
- `plan_digest` = hex sha256 of `"file-janitor-plan-v1"`, workspace ID, batch ID,
  the scope digest, draft revision, group revision, and the sorted lines
  `cid|op|category|fingerprintKey|finalName` and `cid|changed|reason`. It covers
  everything `PayloadHash` covers.
- The server names only files whose own state changed. A change from a choice or
  a new clash is found by the browser comparing the old and fresh plans by
  `candidate_id`.

### One File request (task 2.2)

- `POST .../batches/{batchID}/file`: closed JSON, at most 4 KiB, unknown fields
  refused, one object: `{expected_revision, expected_group_revision, plan_digest,
  trash_acknowledged (int), client_key}`; `client_key` is 16 to 128 of
  `[A-Za-z0-9_-]`. `Service.FileReviewPlan(ctx, owner, ws, batchID, FileRequest)`.
- Order: bounds and fields, no locks; `StoredManagedRoot`, and
  `ResolveRoot(settings)` equal to the root path; **`lockRoot` for the whole
  request**; then one `UpdateScanState` that is the admission:
  1. a `FilingRecord` with this `client_key` exists: replay, write nothing (another
     owner: 409);
  2. mark older records with no `CompletedAt` that are not running as
     `Interrupted`;
  3. recompute the effective view and the plan; if either revision, the digest, or
     `trash_acknowledged != trash_count` differs, or `over_limit`, return
     `ErrReviewPlanChanged` / `ErrReviewBounds` with the fresh plan. The mutate
     returns an error, so nothing is written;
  4. on a match, `invalidateBatchApprovals`, append an **already consumed**
     `ApprovalRecord` (`IdempotencyKey` = the filing ID `"filing-"+uuid`, review
     revision and scope, `PayloadHash`, `ConsumedAt`, a random discarded
     `TokenHash`) and a `FilingRecord`. That write is the admission.
- Then, still under the root lock and registered in a `runningFilings` set, run
  the existing `applyOne` / `trashOne` per item in plan order and stamp
  `CompletedAt`. The response is built from the journal, so a first answer and a
  replay are identical. Responses: 200 `{filing, replayed, outcomes}`; 409
  `plan_changed` or `trash_not_acknowledged` with `plan`; 413 `review_bounds`.
- Never take `lockRoot` inside a mutate. An edit that commits before admission
  wins; one attempted after it blocks on `lockRoot`, then loses its compare-and-swap
  with `ErrReviewConflict`. A retry with the same key waits on the root lock, then
  replays. After a crash there is no resume: a retry returns the journal outcomes
  with `interrupted:true`, files never attempted counted `not_attempted`.
- **Limit of "nothing moves" (R36):** it holds up to admission. After it, the
  per-item fences in `review_execution.go` stay as they are: a file changed since
  is reported stale and a new name clash gets another free name, so a receipt can
  differ from the picture. The receipt says what happened.

### Filing identity, receipt, Undo all (tasks 5.1, 5.2)

- The filing ID is the existing `IdempotencyKey` (wire name `filing_id`); no new
  `FileAction` field. Per file the journal holds the action with `Undo`,
  `UndoneAt`, `UndoError`.
- `GET .../filings?limit=1` (newest first, at most 20) feeds History's last-filing
  line. `GET .../filings/{filingID}?offset=` returns the receipt: `filing_id`,
  `state` (`running|done|interrupted`), `admitted_at`, `completed_at`, `filed`,
  `trashed`, `by_destination[{category, destination, count}]`, `not_moved_total`,
  `not_moved[<=50 {candidate_id, name, operation, result: failed|stale, reason}]`
  (paged with `offset`), `not_attempted`, `loose_now` (files still waiting for the
  folder, in the filed scan or in another scan of it, each file state counted
  once: a newer scan does not propose a file an earlier pending scan holds), `undo_available`, `undo` (`{state, back, not_back, claimed_at,
  completed_at}` once Undo all was pressed) and `history_trimmed`. Every number
  comes from the journal, so it survives a restart. `undo_available` is true while
  the run has finished, Undo all was not pressed, the folder is the one that was
  filed and at least one filed file can still be reversed.
- `POST .../filings/{filingID}/undo` (empty body), `UndoFiling(ctx, owner, ws,
  filingID)`: `lockRoot` first, then one claim write: unknown filing or owner is
  `ErrUndoUnavailable`; `settings.RootID != filing.RootID` is refused (relinked);
  `UndoClaimedAt` already set is a replay; nothing `Undoable()` is refused;
  otherwise set `UndoClaimedAt/By` and mark each `Undoable()` action of the key
  `UndoInProgress`. Then in reverse journal order call `undoMove` /
  `restoreFromTrash` **directly** (never `Service.Undo`: it claims again and the
  root lock is a plain mutex), re-checking `RootID` per item.
- Result: `{filing_id, replayed, back, not_back, outcomes[{candidate_id, action_id,
  name, operation, result: undone|already_undone|failed|interrupted, message,
  location: folder|filed|trash|unknown, destination}]}`. A file already undone
  one at a time is `already_undone`. A second press or window waits on the root
  lock, then replays; a per-file undo during the run is refused by the existing
  in-progress check.
- Limits to state honestly: a crash leaves actions in applying or undo-in-progress
  (the startup reconciliation named in `apply.go` and `action.go` comments does
  not exist; receipt and Undo all report these `interrupted`); a second request
  waits for the first run, up to 1,000 file operations; `MaxRetainedActions`
  (1,000) equals the per-press cap, so the next filing prunes older actions
  (`history_trimmed`) and the newest filing is always complete.

### After Undo all (task 5.4)

"Scan again to review them" is **one press with two server steps**; Ori never
scans without it. The browser sends `POST .../filings/{filingID}/forget-restored`
and only after it succeeds the existing `POST .../scan`. Under the root lock, in
one scan-state write, forget clears the skip key of each restored file's current
fingerprint and its `BeforeFingerprint` key for every action undone by that run
(`UndoDone` with `UndoneAt >= UndoClaimedAt`). It changes no file or candidate, is
idempotent, is refused after a relink, and returns `{forgotten:n}`. It does not
reuse `reconsider_files`, which clears the candidate's own fingerprint key (not
necessarily what `suppressRestored` wrote) and needs the batch revisions. A failed
scan leaves the files forgotten, which is harmless; a retry sends both again.

### The receipt and Undone views in the browser (tasks 5.3 to 5.5)

`file-janitor-receipt.js` (an ES module, `window.FileJanitorReceipt`) only draws;
the console owns every request. After a press the console reads
`GET .../filings/{id}` and draws the receipt: "N files filed." (Trash apart as "M
moved to Trash", a change of unknown kind as "N changes completed", never "filed"),
"They are in <folder>/Filed. K files stay loose in <folder>.", one row per
destination folder, the "N files were not moved" section (each file with its
reason, 50 at a time), **Undo all** (only while the server says it is on offer) and
**Done**. If the receipt cannot be read, the answer to the press is drawn instead,
without the loose count. The per-file list is gone from this panel: History has it.

**Undo all** is `POST .../filings/{id}/undo` with no body, from the receipt or from
History's "Last filing" line. The answer is the Undone view on the Review tab: "N
files are back where they were.", the files that could not be put back each with
the reason and where the file is now (still filed, still in the Trash, a file with
the original name in the folder, or not found), and one button, **Scan again to
review them**, which sends `forget-restored` and, only after it succeeds, the
ordinary scan. `undo_unavailable` (409) is shown as one sentence and the receipt
is read again so the button goes; a lost request leaves the button for a retry
and claims nothing. The History line is `GET .../filings?limit=1`: "Last filing ·
N files · time" with an **Undo all** link while on offer and "put back" after.

### The backup strip, other states, access (tasks 6.1 to 6.4)

- **Backup strip (R53 to R55).** One sentence and two buttons (**Check protection**,
  **Not now**) directly above the File button, from `protectionStripSentence`: only
  for `not_covered`, `unknown` and `configured`, only when the protection fact has a
  destination and binding, and not once dismissed for that binding. A covered
  folder, an unreadable state and a dismissed strip show nothing about backups on
  Review; **Check protection** stays in a Protection section on the Settings tab
  (disabled with a sentence while there is no scan to return to). A protection trip
  restores the picture with the saved choices and never the Trash tick.
- **States (R59 to R64).** Nothing waiting: "Nothing to file. {folder} is tidy." and
  Scan now, only for a picture that was read and has no file anywhere in it (files the
  person told Ori to leave are named instead of called tidy). A picture that cannot
  be read says so with Retry and never draws as tidy. When another saved scan of the
  folder still has files waiting, "tidy" is not said ("Nothing to file in this scan.")
  and an earlier scan is named in one line ("N files are still waiting in an earlier
  scan." with **Show the earlier scan**), because a scan does not propose a file an
  earlier pending scan holds. A newer scan is one line with
  **Show the newer scan** and never replaces the open picture. A change from another
  window is read again and said ("Updated from another window."; a refused save adds
  "Your last change was not saved."); the manual reload state no longer exists.
- **Words (R56 to R58).** The Review tab, the All files list, the empty, loading and
  error states, the receipt, the Undone view and the server messages a person can
  reach use none of: batch, candidate, proposal, exact, explicitly, draft. A saved
  choice has no label; the main button says "Saving…" and the footer speaks only
  when saving failed, a save is out of date or files were taken out. Two console
  tests render each state and fail on any of those words. The Curator guidance
  (`internal/filecuratorguidance`, mirrored byte for byte in `.agents/skills`) was
  rewritten to describe the picture, **Leave here**, **Leave here, stop asking**,
  **Undo all** and the strip.
- **Access (R65 to R69).** A persistent visually hidden `role=status` region says what a
  change did, from the picture before and after ("2 files moved to Documents."). A long
  file name is shortened in the middle with the whole name in the page (read by a screen
  reader, copyable, in `title`, opened out on hover and focus). Browser cases cover axe
  on the picture, receipt and Undone view in both themes, reduced motion, Tab order, the
  announcement, 390 px and a 200%-zoom-equivalent viewport.

### Storage (both sides stay at scan state schema 3)

- Additive: `JanitorBatch.review_defaults`; `ScanState.filings []FilingRecord`
  (`omitempty`, newest 20, validated: unique IDs, `client_key` pattern, planned
  at most 1,000) with `id, owner, batch_id, root_id, client_key, plan_digest,
  planned_moves, planned_trash, admitted_at, completed_at, interrupted,
  unrecorded[{candidate_id, reason}], undo_claimed_at, undo_claimed_by,
  undo_completed_at`. `ApprovalRecord` and `FileAction` gain nothing.
- Moving to 4 would make every older binary refuse the whole file and break
  Filing after a downgrade. Staying at 3, an older binary that rewrites the file
  drops both additions: the batch then shows nothing ticked and Undo all and the
  receipt disappear. Neither widens what may move. Newer unknown schemas are still
  refused, never written over. If a later field's loss on downgrade would be
  unsafe, that is the reason to move to 4.

### Routes added (both prefixes, in `registerUnder`)

`GET .../batches/{id}/picture` and `/picture/files` (1.3), `GET
.../batches/{id}/plan` (2.1), `POST .../batches/{id}/file` (2.2), `GET
.../filings` and `.../filings/{id}` (5.1), `POST .../filings/{id}/undo` (5.2),
`POST .../filings/{id}/forget-restored` (5.4). Each is added to the route lists in
`authorization_audit_test.go` and `route_parity_test.go`, stays inside reset
admission, and returns no path, fingerprint or token.

## Non-goals

No physical staging folders, recursive/folder moves, custom trees, content or
archive inspection, hashing/duplicate verification, inferred disposal, learned
approval, scheduled deletion, model dependency, workspace-navigation redesign,
cross-batch approval, or backup guarantee. Explicit per-file system Trash and
supported move Undo remain separate existing actions.
