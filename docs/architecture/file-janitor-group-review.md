# File Janitor group-first review contract

Status: contract selected in the task 1.1 characterization spike; implementation
and acceptance evidence are tracked separately in the feature findings log.
Base inspected: `48d8e5392857a0517084bf1ce70cf9313530826c`.

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
redundant verdict. Audio questions group by existing audio metadata and explicit
music-project extensions; they do not assert a shared project. Archives are not
opened or compared. Unrecognized types remain reachable, not guessed safe.

Cards state the rule and its uncertainty. Classification reasons/provenance
remain the saved actual values (`metadata`, `model`, `content`, `fallback`);
only the grouping rules are metadata-only. Do not make a blanket claim that all
saved candidates were classified without content access.

## Read shapes and bounds

- `GET /batches/{batchID}/groups`: `batch_id`, `revision`, `rule_version`,
  whole-batch `counts`, and fixed-vocabulary `groups`. `latest` is a navigation
  convenience only; edits always name the resolved concrete batch ID.
- Each card: opaque `id`, `kind` (`filing` or `question`), fixed `label`,
  `reason`, proposed `category`/relative destination where applicable, whole
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
  are removed atomically on the next edit/preview transaction.

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
category, clear group choices, clear all choices, mark/unmark per-file Trash,
Keep here, and targeted reconsider. There is no group Trash action.

Resolve group members under the canonical `UpdateScanState` lock. Group select
and select-all use the exact revision displayed, not currently fetched member
pages, not a future rule. Routine select-all excludes every question group,
unresolved Needs review member, non-actionable/failed member, and existing
explicit Trash mark. No group is selected on entry. A category edit without a
selection edits the canonical candidate but leaves it unselected.

Use **1,000 choices per batch**, **1,000 candidate IDs per edit**, **10,000 total
retained draft choices per workspace**, and **256 KiB edit bodies**. Refuse
oversized edits with a stable bounds error before writes; never truncate. Pending
choices survive pruning. Resolved/orphaned choices prune with their canonical
batch/candidate; if pending work fills the cap, reject additional choices with a
clear recovery rather than discarding old pending work. No client-side fetching
of all pages is needed to select a 206-member group. Preview caps its exact plan
at 1,000 operations; above the cap requires an explicit smaller selection.

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

## Keep here, Not now, and Review again

Keep here records existing exact fingerprint skips, marks those candidates
skipped, clears only their staged choices, and invalidates matching unspent
preview authority in one transaction. It changes no managed file. Existing skip
retention remains 2,000 newest skips, no age expiry: disclose that changed files
may be proposed again and old skips can age out by the retention cap.

Review again accepts candidate IDs (never client-supplied fingerprint keys),
derives each requested skip server-side, and clears only those skip records.
The old candidate stays skipped/resolved. Offer an explicit **Scan now**; do not
silently scan, select a replacement, restore old approval, or resurrect the old
candidate. Existing whole-skip-reset endpoint stays compatible. Not now is only
an accessible collapsed/de-emphasized question in this console: it stays pending
and reopenable, grants nothing, and sets no timer. Use the same semantics for
music and versions; do not label it “for now”.

## Exact preview and confirmation fence

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
results, never replayed automatically. Retrying requires fresh explicit review.

## Characterized current behavior and required changes

- Category picker sends legacy `DecisionMove` while checkbox selection lives in
  a separate in-memory Set. Restart restores destinations but no selections.
- Existing candidate endpoints already page at 50/200 and return whole-batch
  counts, but serialize the full batch including candidate IDs. New group reads
  use a bounded batch summary shape instead.
- Batch provenance is captured by scans, and saved review projections already
  reject foreign scope without probing the managed folder. Reuse these seams.
- Protection departure saves ID-only choices, invalidates local approval, and
  waits for category writes; return pages through every candidate and currently
  requires the batch to still be latest. Replace latest-only matching with the
  pinned concrete batch and canonical draft reconciliation, not replay of a
  browser selection snapshot.
- Store locking and atomic rename already serialize canonical scan writes.
  Raw scan schema is currently normalized unconditionally; the draft migration
  must preserve unsupported-schema evidence before any write.
- No new plugin/provider/devtools release or connector protocol is required.

## Non-goals

No physical staging folders, recursive/folder moves, custom trees, content or
archive inspection, hashing/duplicate verification, inferred disposal, learned
approval, scheduled deletion, model dependency, workspace-navigation redesign,
cross-batch approval, or backup guarantee. Explicit per-file system Trash and
supported move Undo remain separate existing actions.
