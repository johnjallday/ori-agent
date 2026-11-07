# Adaptive learning missions

Status: implementation contract, **not yet delivered**. Source trace:
`f4bc47422c9cac3e7fc7868132e11e8ac32d7a97` (2026-11-05). This document records the
approved host-only v1. Tests, production wiring, UI and real-model quality must
be verified separately before changing this status.

## Ownership and trust

Learning is practice alongside ordinary work, never a prerequisite for it.
`internal/learningmissions` owns device-local preferences, proposals, accepted
instances, operations, result decisions and practice evidence. It does not own
Tickets, execution, permission grants, onboarding completions or Craft.
`internal/learningmissionshttp` serves owner-facing interactions only; no
learning mutation is registered as an assistant tool, MCP operation or generated
action URL. The design model can propose text, never accept, review, execute,
complete or grant permission.

Ori currently resolves its single local owner through
`userprofile.UserProvider` (`LocalUserProvider` returns `local`), not an account
login or authenticated human-presence mechanism. The HTTP adapter must derive
that owner server-side; never take `owner`, `actor` or approval from a body.
Apply the existing loopback Host guard plus exact same-origin validation and
JSON-only mutations to the new endpoints. A result-specific review nonce is
issued only by an explicit owner-facing POST, bound to owner, epoch, instance
revision and canonical evidence, and consumed transactionally. Reads never
issue decisions. This isolates the mission-design model and prevents accidental
browser claims, CSRF and stale tabs; it is **not proof of human presence against
a process already controlling this device/browser**. Do not call a local-user
constant authenticated identity in documentation or UI. Multi-user deployment
and hostile local-process authentication are outside this feature.

Existing owners stay authoritative:

| Domain | Owner / verified seam |
| --- | --- |
| Finite Getting Started, skip/reset/backfill | `progression.Engine`, onboarding manager, progression HTTP |
| Existing Craft | progression callback into economy; learning never calls it |
| Ticket lifecycle and creation | `workspace.TicketService`, workspace store atomic Update |
| Exact attempt/result | `workspacerun.Store.GetRun/ListArtifacts`; Run scope + immutable artifact identity and digest |
| Execution | existing Ticket start path, TaskRunBridge, TaskHandler, autonomy/readiness/admission gates |
| Default model | `config.Manager.GetSystemModel()` (UI calls it Default) and `llm.Factory` |
| App reset admission | shared `resetstate.WorkGate`, reset schema inventory |
| Context ownership | canonical workspace owner and continuity attachment, not an assistant summary |

### What the existing flow proves

1. `TicketService.CreateWithKey` atomically deduplicates source + source ID in a
   workspace; unlike `CreateIdempotent`, it tolerates later lifecycle movement.
   Its replay check only compares the title, so learning must also keep a
   normalized payload digest and exact created ID. A deleted/changed record
   cannot be blindly recreated by retrying this API.
2. `TicketStartHandler` uses `PrepareStart` and an asynchronous starter, with
   in-memory short-window duplicate suppression. It is not a durable start
   receipt. The learning adapter records an operation before invoking this
   existing path, and never retries a start with unknown outcome.
3. `TaskRunBridge` creates a fresh Run per execution with
   `Scope.TargetTaskID`, persists `CurrentRunID` best-effort, then executes.
   `CurrentRunID` is mutable; its persistence error is currently ignored. Read
   the canonical Run and its scope, not only that pointer. Production tracing
   also found that status transitions did not populate Run timestamps. The
   canonical SQLite/memory owners now stamp first execution and terminal times;
   historical missing timestamps remain unavailable, never backfilled.
4. OriAgentExecutor stores an `ArtifactFile` with metadata role `task_result`
   and a raw-result artifact. Select the unique canonical file-result artifact,
   pin its ID and SHA-256 of exact bytes. Never select the latest arbitrary
   artifact or a file path. Ambiguity/missing inline bytes is unavailable.
5. Run success moves a one-shot Ticket to Review, never Done. Failed attempts
   leave work open. A Done transition permits client actor, actor ID and Run ID;
   those strings are not evidence of a result-specific review.
6. Execution history is capped at 200 records; each result is capped at 16 KiB
   with a truncation marker. Latest result and historical snippets are not the
   authoritative full artifact. `TaskOperationRun` can bind one assistant
   operation to its recorded execution time but does not describe learning or
   coaching and must not be reused/overwritten by learning.
7. The highlight → follow-up UI stores `input_task_ids`/optional parent ID and
   excerpt text, not the source Run/artifact or comparison decision. Reuse and
   improvement need separate lineage receipts even when using this UI pattern.
8. Canonical and legacy events coexist and can be duplicated, missed or delayed.
   They wake reconciliation only. Imported content/history is inert, not a new
   local decision, start, approval, guidance record or successful reuse.

## Three version-1 structures

Structure IDs: `delegate_inspect`, `improve_result`, `repeat_success`. Definitions
are compiled, immutable and versioned at `1`. No arbitrary step composition.
Each offer pins a structure, candidate, workspace, scope digest, permitted
ordered step IDs and definition version. Acceptance freezes these and the
visible goal, scope and guidance. The later reviewed execution input is pinned
at Ticket preparation. Changed copy/criteria is a separate offer
requiring acceptance, never an edit to active work.

| Structure / ordered steps | Evidence required |
| --- | --- |
| Delegate and inspect: `scope`, `execute`, `review` | User-reviewed bounded outcome/input; explicit real Ticket start; exact successful Run/result; owner decision `use`, `revise` or `reject`, with allowed reason or bounded note |
| Improve a result: `original`, `correction`, `execute`, `compare` | Exact original result; approved correction and original digest; distinct revised Ticket/Run/result; comparison decision and nonempty change description. Rejecting a revision still counts as practice |
| Repeat a success: `process`, `inputs`, `execute`, `review` | Prior local `use` receipt for an eligible process; fresh reviewed inputs with changed normalized digest and user explanation of material difference; distinct successful execution; fresh `use` review. Different bytes alone do not prove material difference |

No structure completes for opening a page, creating a Ticket, generic Done,
model self-rating, automatic retry, duplicate receipt or unchanged input. The
records prove a reviewed workflow was exercised, **not understanding, objective
quality, truthfulness of a reason, or mastery**.

Decision reasons: `meets_scope`, `missing_detail`, `incorrect`, `not_useful`,
`other`; `other` requires a note. Notes and comparison/input-change explanations
are at most 500 Unicode code points. A revision/reuse record refers to its
original receipt and exact original result, not a title similarity.

The implemented original reader verifies one locally consumed receipt and its
exact current canonical artifact; it does not recursively traverse an ancestral
chain. Original inspection is an explicit read-only owner route and creates no
nonce, credit or model call. Revision execution uses only a host-reserved
same-workspace original reference plus the reviewed correction, with complete
inline bytes disclosed before Start. It remains executable after learning is
cleared when those ordinary work inputs still exist.

Reuse candidates are currently plain supplied-text draft processes (delegate
or prior repeat), not revision recipes whose original would otherwise be
silently discarded. Whitespace-normalized input must differ and the owner must
explain the material difference. A new revision may return identical bytes and
still be evaluated: distinct execution identity, not a claimed quality increase,
is required. A reuse decision other than `use` records evaluation practice only;
the instance remains blocked as `result_not_useful` until explicitly stopped.

### Practice projection

Each completed distinct practice contributes once to its structure/competency:
`delegate`, `evaluate`, `improve`, `reuse`. Dedup key includes owner, workspace
incarnation, structure version, competency and exact Run/artifact digest.
Retries of an attempt never count twice. The projection is rebuildable from
durable evidence; receipt insertion, instance completion and credit insertion
share a transaction. No economy event is emitted.

- No qualifying evidence: **not yet observed**, not “beginner”.
- Guided practice: **practiced with guidance**.
- At least one qualifying attempt with explicit basic guidance chosen and no
  guided hints shown/used: **practiced independently**. This means no Ori
  guidance recorded, not absence of outside assistance.
- Successful fresh-input reuse with the same verified lineage: **reused
  successfully**.
- Unknown historical guidance stays unknown, never becomes independent.

New host-issued definitions carry `support_version: 1`, copied into their
immutable review and evidence records. Missing/future support versions remain
unknown even when an old record says `guidance: basic`; no backfill turns that
into independence. The metadata-only competency projection preserves earned
facts when sources disappear and reports unknown/partial coverage explicitly.
The existing Missions surface exposes **Recorded practice**, with explicit
read-only inspection of a saved decision, canonical input/result, original
comparison when applicable, and supporting Ticket. This GET issues no nonce,
operation, decision or credit; inaccessible sources disclose no saved contents.

Record guidance selection, host-rendered hint version, shown/used receipts and
attempt identity. UI acknowledgements describe displayed support, not human
attention. An opted-in ordinary-work review uses the same decision/evidence
verifier without manufacturing a mission. Only work reviewed and observed after
the relevant learning epoch **and observation revision** qualifies; old
Done/history cannot be backfilled. The implemented ordinary path supports the
compiled, separately reviewed **plain supplied-text bounded draft**. General
agent executions and unlabeled historical assistance remain unknown; revision
or reuse needs its own exact lineage contract, not an inferred similarity.

The winning owner Start captures observation consent before execution approval,
then mints a fresh nonportable Start token. An `observe` operation binds owner,
local incarnation, learning epoch, observation revision, Ticket, input digest
and the first reserved Run. Twenty pending observations is the admission cap;
tracking failure never cancels ordinary execution. `observe_review` operations
prepare/consume the same exact-result owner decision without a synthetic
Instance. Preference revocation invalidates pending claims. Canonical metadata
checks prioritize available existing reviews before new default suggestions;
GET reads no Run payload/artifact bytes for that check. Consumed ordinary reviews
can also supply a later explicitly selected improvement/reuse original; no
ordinary input/result/title is added to the design prompt.

New evidence has a secondary Ticket/input/competency identity: unchanged or
whitespace-only ordinary retries cannot add practice or upgrade prior support.
Legacy receipts retain exact-input deduplication. Ordinary support remains
unknown even if the current preference says Basic. Earned review inspection
continues after observation is revoked, while unfinished old grants cannot be
revived by same-millisecond reapproval or history clear.
No decay for inactivity, deferral, rejection, missing sources or coaching off.

## Eligible work and action authority

V1 effects are only bounded assessment/read and workspace-internal textual draft
results. No installs, shell commands, messages, publishing, file moves or new
executors/tools/grants. Prefer an already-ready bounded assessment with its
canonical host recipe. A data-only draft may use the existing
`workspace.RecipeRunner` + `LLMTaskHandler` + `ori_agent` Run pipeline, taking only
explicit reviewed input and (for revision) the exact selected original result;
it exposes no tools or filesystem. This is a host recipe using the existing
executor, not a new executor or a permission bypass. Add a narrow recipe adapter
only for this approved effect class; do not turn ordinary agent tools into a
“safe” draft through prompt wording.

Only server-issued opaque candidate references identify eligible work. An
existing unrestricted Ticket is not made eligible merely because its title says
“draft” or “read”. Creation uses a server-owned learning source key and recipe
marker; public task mutation cannot author those markers. Readiness is checked
before offering, accepting, preparing work and starting. The recipe independently
rechecks canonical scope so an edited/imported task cannot regain ordinary tools.
Existing execution, workspace membership and autonomy gates remain authoritative.

Accepting a mission does not run anything. A separate reviewed work action
creates/links a Ready Ticket, and a separate existing explicit Start starts it.
Stopping learning never cancels an ordinary Ticket or Run. Unknown execution
outcome means “Check the linked Ticket”, never automatic replay.

The bounded draft uses the existing Ticket Start route, with an executor/input
review from `GET /api/workspaces/{studioID}/tickets/{ticketID}/execution`.
`POST .../start` supplies its `executor_digest`. The host binds exact input,
recipe version, assigned agent, provider/model and effort. Only the winning
start claim records approval; duplicate clicks cannot renew consumed approval.
The recipe consumes it before a single isolated call (1200 output tokens,
30-second deadline). Bounded learning calls additionally require
`llm.BoundedTextProvider`, `SingleAttempt`, and a provider-configuration pin.
API providers implement this contract; a CLI that ignores `MaxTokens` is
unavailable even if it can isolate tools. Destination changes require fresh
review, and partial/unknown-finish or over-budget replies are not completed
results. Edited text/model and automatic retries require fresh review. Import
removes the approval while retaining the restrictive recipe.
These execution rules do not depend on the learning mission remaining active.

## Durable records and transitions

Use the application's SQLite connection and migration **89**, after upstream's
workspace-updates migration 88. New tables are local-only and enter
`ResetRecordTables` in the
same change. No continuity collector/export/import registration.

- `learning_owners`: owner, revision, deletion epoch, scoped consent/settings,
  configured-model fingerprint, cadence counters; off/on-request/automatic.
- `learning_sources`: device-local owner/workspace incarnation, foreign-keyed to
  the canonical workspace registration. Deletion revokes it even if an import
  later restores the same ID/timestamp. Only explicit proposal admission creates
  it; no GET or portable content can restore it.
- `learning_instances`: ID, owner/epoch, revision, structure/version, immutable
  proposal JSON, workspace incarnation, status/blocker, acceptance and timestamps.
- `learning_operations`: owner + operation ID unique, kind, request digest,
  expected revision/epoch, persisted status, exact instance/Ticket/attempt links,
  provider fingerprint and bounded usage. One live generation claim per owner.
  A `milestone` operation carries only a newly earned review/evidence identity,
  exact scope/epoch/suggestion grant and deadline, never raw content.
- `learning_reviews`: local nonce and receipt, owner/epoch, instance/version,
  exact workspace/Ticket/Run/artifact/digest, decision/reason/note, guidance,
  original receipt/correction/input lineage. Nonces expire and cannot be exported.
- `learning_evidence`: dedup keys, minimal competency/support/attempt data and
  source availability; no copied result bodies. Earned aggregates survive source
  removal but are removed by explicit learning-history clear.

A partial unique index on owner for `active`/`blocked` gives one active contract;
a separate unique slot gives one `offered` recommendation. Operations are
idempotent by owner + ID + normalized request digest. Same ID/different payload
is a conflict; an unknown response is recovered by reading that same operation.
Expected revisions are mandatory for mutations; actor/complete/permission fields
are rejected, not ignored. Unsupported structure/schema versions are readable
history only, never executable.

| From | Allowed operation → state |
| --- | --- |
| none | explicit request or admitted opted-in milestone → offered |
| offered | accept → active; defer → deferred; decline → declined; source/consent/model change → invalidated |
| active | verified evidence → completed; prerequisite unavailable → blocked; stop → stopped |
| blocked | same prerequisites restored → active; stop → stopped; irrecoverable contract change → invalidated |
| active/blocked + new offer | explicit replacement atomically stops old learning contract and accepts new; linked work continues |
| completed/deferred/declined/stopped/invalidated | terminal; no late reply/event resurrection; new operation creates new offer |

Cross-store sequence: persist operation and intended Ticket ID/source key before
the workspace write; create idempotently; record actual Ticket linkage; only
then expose Start. Reconciliation looks up the exact intended ID and source
marker and validates payload, scope and incarnation. If creation outcome cannot
be proven (including deletion after creation), show recoverable unknown, never
create a second Ticket. A separate attempt claim is written before start and
bound to a server-reserved Run ID **before** canonical Run creation, not by
“latest Run” heuristics or event timestamps. The Run bridge's compiled reservation
hook may refuse creation; `CreateRunRequest.HostRunID` is not a JSON field. An
interruption after reservation keeps that exact identity unavailable/unknown,
never adopts a later attempt. A later unrelated Run cannot fulfill the claim.
After restart, reconcile persisted exact references without replaying execution.

WorkGate permits cover every finite mutation and callback through persistence.
History clear increments a durable owner epoch transactionally; provider replies
and event handlers must match it. Preference changes advance the owner revision and
invalidate pending offers/generation without rewriting accepted contracts.
Revoked observation requires fresh scope consent; its host-owned observation
revision distinguishes grants even within the same wall-clock millisecond.
Never let a stale callback upsert a deleted owner. Workspace deletion/incarnation and scope revisions are
checked before publishing and before actions. Use content-free blocker codes
and never leak foreign record existence through errors.

## Context, model and cost contract

Default is **on request**, with no authorized workspace until explicitly chosen.
Observation and automatic suggestions are separate opt-ins. User-entered focus
and effort preferences are explicit; activity is an observation with coverage,
not an inferred sensitive interest or permanent preference. Sharing workspace
metadata for suggestion design does not grant source-file or execution access.

Ongoing scopes bind exact local incarnations, not just portable workspace IDs.
Only owned-metadata resolution during an explicit preference mutation can save
these bindings; legacy/missing/reimported bindings cannot revive observation.
Saving automatic mode requires the current Default-model fingerprint and
separate consent, but is not itself a generation trigger. History clear retains
only identities already covered by retained ongoing scope consent, while
advancing the learning epoch and deleting unapproved historical identities.
Preference clear revokes observation/automatic consent and exclusions, not earned
practice or actual work.

The host ranks eligible candidates before prose generation, using literal
explicit-focus overlap, scoped local review-practice counts, effort and bounded
recent repetition. Missing receipts leave coverage unknown, never zero ability.
One selected candidate goes to the designer; compiled `host_signals` explain the
actual basis separately from generated rationale. Defer/decline exclusions live
in bounded preferences and survive history clear/reimport. Their topic is the
normalized explicit-focus digest, not an inferred interest.

Input is data-only JSON: bounded workspace display name, user-reviewed goal,
explicit preferences, host candidate references/structure IDs, readiness,
minimal recent outcome counts and practice state with unknown/partial markers.
No directory paths, file contents, unrestricted chat/messages, raw results,
secrets, internal feedback, arbitrary URLs or inherited assistant memory.
Untrusted names/prose remain data. Model-authored prose fields are `goal`, `scope`,
`why`, `hints`; all are escaped plain text. Guidance mode remains host-owned. Host alone renders labels,
actions, routes, cost disclosures, criteria, blockers and permission claims.

Named defaults (implementation constants; raising them requires review):

| Limit | Default |
| --- | --- |
| approved workspaces / candidates | 5 / 8 |
| recent activity records scanned per workspace | 20, at most previous 30 days |
| proposal steps | structure's exact 3 or 4 steps |
| total model input / response bytes | 16 KiB / 8 KiB |
| name / goal / scope / rationale / each guidance text | 120 / 300 / 1000 / 500 / 500 runes |
| explicit input/correction draft text | 4 KiB; original inline result review cap 64 KiB, refuse larger rather than silently truncate execution input |
| generation output tokens / deadline | 1200 / 30 seconds |
| model calls per proposal operation | 1, no repair/retry/provider fallback |
| generation concurrency | 1 per owner, 1 process-wide |
| explicit generation spacing / daily maximum | 30 seconds / 10 admitted calls per rolling 24 hours |
| automatic generation spacing / daily maximum | 24 hours / 1 (also consumes the shared daily limit) |
| offer / review nonce expiry | 24 hours / 10 minutes |
| automatic milestone debounce / expiry | 2 seconds / 5 minutes; no restart catch-up |
| Not now cooldown / Not interested | 7 days / persistent workspace + topic + structure suppression until reset |
| durable exclusions / preferences record | 12 / 8 KiB including five scope bindings |
| recent terminal proposals / non-evidence operations | 100 / 200 retained for at least 30 days |
| full local review receipts | 1000 per owner; preserve active/recovery/original dependencies, compact eligible receipts older than 30 days to content-free replay metadata |
| dedup evidence / total logical learning data | 10,000 per owner / 16 MiB per database; refuse new admission at cap, never silently discard dedup authority |

Retention runs only within a new admitted mutation, in batches of at most 200
per category, never on GET/visits or a background timer. Keep at least 30 days
and the newest 100 terminal instances / 200 terminal operations. Live contracts,
pending/unknown effects, retained original dependencies and current-epoch
automatic candidate fingerprints are protected even if this exceeds those soft
counts. Full receipts may compact only when unreferenced by retained instances
or uncertain operations; the evidence keeps its exact key, secondary identity,
support and competency. Minimal receipt replay metadata preserves legacy dedup
and scoped counts but cannot authorize inspection, a new challenge or an
original. Full content becomes explicitly unavailable; earned facts do not
shrink. Durable exclusions never expire through retention (only explicit reset,
or the documented defer deadline). If no safe compaction is possible, refuse
admission rather than evict required evidence.

The 16-MiB ceiling measures logical records, conservative key/row overhead and
pending-publication reservations, not the SQLite file, WAL/freelist or actual
Ticket/Run storage. Transactions check it under the same writer lock before
commit. Each design admission reserves 32 KiB of publication space before its
provider call; 256 KiB within the ceiling is withheld from ordinary admission
for finishing existing effects and Stop/Off/clear. New receipts and evidence
respect their own caps with atomic rollback. A distinct `storage_limit_reached`
response points to explicit history clearing. History clear removes non-billing
operation history and old design records beyond 30 days, while retaining recent
known/unknown call accounting and advancing the epoch. No automatic deletion
resets the rolling call budget.

Generation selects only the configured Default provider/model/effort. Require
`SupportsIsolatedChat`, then `ChatRequest.Isolated = true`, exactly host system
instructions + one data message; no Tools, MCPServers, WorkspaceID/Dir,
ExecutionScope, agent prompt/loadout, files or streams. Use ResponseSchema only
when supported, and strictly validate the response regardless. Require the
bounded-text contract too; Ollama format downgrades are disabled for
`SingleAttempt` calls, and no provider without an enforceable output cap is
admitted. Resolve the configured provider directly, not through model catalog
discovery (which may perform network I/O). Reject tool
calls, unsupported IDs/steps, duplicate/extra JSON fields, trailing JSON,
malformed/fabricated candidate references, action URLs/commands and scope
expansion. Schema validity alone does not establish useful or safe prose.

Persist claims before calls; include timeout/cancellation/unknown usage in
accounting, reserving 17,584 tokens (16-KiB input ceiling plus 1200 output tokens)
until complete usage is known. Failed/unknown calls and learning-history clear
do not reset the rolling admission budget. Expired pending claims become unknown
on an explicit mutation or startup/event reconciliation, never automatic replay. Revalidate owner epoch, scope,
provider/model fingerprint and revision before publication. A changed configured
provider/model/endpoint, narrower/wider selected scope or disclosure version
requires fresh suggestion consent; do not keep automatic consent across changes.
Accepted contract text stays pinned; next execution gets its own current model
and permission review. No useful candidate, active work, off, suppression,
unknown eligibility or quota exhausted means **no call**. GET, hydration,
polling, focus and page visits never generate or execute. Restart does not
catch up missed automatic suggestions. Milestones coalesce by durable candidate
fingerprint; invalid/missing provider gives a clearly generic eligible offer or
quiet unavailable state, never a claimed personalized success.

The implemented v1 trigger is **new distinct credit from a consumed owner review**
(mission or opted-in ordinary work), including informed rejection. Ticket Done,
Run completion alone, imported labels and replayed reviews are not triggers.
The same review transaction queues at most one pending event for the owner,
using a durable event fingerprint and a separate host-owned suggestion grant
revision/time. Automatic consent must precede that review; same-millisecond
revocation/reapproval cannot adopt it. Legacy automatic preferences without the
new grant remain paused until explicitly reapproved.

A bounded worker coalesces for two seconds and drops events after five minutes.
It revalidates the exact earned receipt, scope, consent, configured model and
current work before durable generation admission. Candidate fingerprints prevent
repeating an already-admitted automatic candidate; failed/unknown calls count.
One automatic admission per rolling 24 hours also consumes the shared ten-call
budget/30-second spacing and process-wide slot. Admission atomically consumes
the event before the single model attempt. Publication rechecks grant/expiry as
well as the existing revision/source/model snapshot. Owner decisions and
preference changes retire queued/in-flight suggestions, not actual work.
Startup retires pending milestones without dispatching; shutdown cancels and
joins the owned worker/call. There is no polling timer or automatic repair loop.

## API and presentation

Namespace `/api/learning-missions`; all replies no-store. Strict bounded JSON,
reject duplicate keys/unknown fields/trailing values. Resolve owner outside
body. Workspace references use `studio_id`. Errors: 400 malformed, 404
unavailable/foreign, 409 stale/unknown/replayed-different-content, 429 quota,
503 unavailable provider/store; sanitized reason codes only.

- `GET /api/learning-missions`: saved active/featured/history/preferences,
  competencies and read-only availability. No mutations or generation.
- `POST /api/learning-missions/proposals`: explicit selected `studio_id` +
  `operation_id`, `expected_revision`; `personalize: true` additionally requires
  `consent_version: 1` and the current disclosed `model_fingerprint`. This grants
  one design call, not ongoing observation. Without personalization it remains
  deterministic. Smaller/alternative requests use their separate model-free route.
- `POST /api/learning-missions/instances/{id}/decisions`: accept/defer/decline/
  stop/replace, `operation_id`, expected instance and owner revisions.
- `POST /api/learning-missions/instances/{id}/work`: reviewed bounded input,
  operation/revision; prepares real Ticket only, never implicit start.
- `POST /api/learning-missions/instances/{id}/review`: issue pinned review
  challenge or submit its exact nonce/decision/reason; never caller completion.
  Preparing a challenge returns the exact canonical `result_text` it binds;
  result bodies are not copied into learning receipts, status or diagnostics.
  Production instance availability additionally uses `InstanceStatusSource`,
  scalar consumed-review references and `OutcomeMetadataReader` (including exact
  artifact identity/count/length), not Run payloads, artifact bodies or notes.
  Availability is not proof: canonical content verification stays in explicit
  review/inspection and event reconciliation. Missing pinned sources redact
  source details; an unavailable executor blocks new work, not reading owned
  past evidence.
- `POST /api/learning-missions/instances/{id}/alternatives`: explicit model-free
  `kind: smaller|alternative`, owner/instance revisions, and optional explicitly
  selected `studio_id` for an alternative. Smaller halves the supplied-input
  byte ceiling down to 256, without lowering structure/evidence requirements or
  promising lower provider charges. Alternative selects a different eligible
  host reference, respects exclusions, and returns none for a thin candidate set.
  One preview may coexist with the current active/blocked instance. Its immutable
  `replaces_id`/`replaces_revision` pins what renewed acceptance will retire;
  acceptance atomically stops old learning tracking and activates the new
  definition, never cancels/edits actual work. A `discard` decision keeps the
  current mission without adding an exclusion. Stopping the current mission
  invalidates its pending preview; changed progress makes stale acceptance fail.
  `input_limit` is enforced again during Ticket preparation, not only in prose.
- `POST /api/learning-missions/observations/{id}/review`: explicit prepare/submit
  of a saved post-opt-in ordinary Start/result, with `observed_revision` plus
  owner revision/operation identity. No claim/start/result/actor overrides.
  Read-only operation recovery can disclose the same still-current exact review
  bytes without minting a new nonce. No mission enrollment or reward event.
- `POST /api/learning-missions/preferences`: `workspace_ids`, mode, observation,
  guidance, `full_guidance` override, explicit focus/effort and revision/operation identity. Automatic mode
  additionally requires current model disclosure/consent. Scope incarnations and
  observation revision/time are host-owned, never accepted from the client.
  `reset_exclusions` explicitly resets saved defer/decline choices. Revocation
  fences pending recommendations while keeping accepted contracts and work.
- `POST /api/learning-missions/clear`: explicit `history` or `preferences` scope;
  fences the relevant state, leaves actual work intact.
- `GET /api/learning-missions/diagnostics`: explicit local aggregate inspection, without enrollment, source discovery, provider calls or outbound reporting. Last-30-day activity is capped at 200 rows per category, with explicit partial coverage. Shows created offers/their overlapping current outcomes, consumed decisions (including retries), admitted design calls/outcomes, known versus unknown reported usage, conservative reservations, and separately labelled retained per-competency practice. No prompt/result/notes or source identities; no claim about currency, provider billing, attention, retention or mastery. Actual Ticket execution calls are not included in design accounting. History clear removes practice/contracts/receipts but keeps recent design-call accounting; the UI requires separate unchecked confirmation.
- `GET /api/learning-missions/operations/{id}`: exact operation recovery only.
- `GET /api/learning-missions/evidence/{key}`: owner-requested inspection of one
  earned practice receipt and its exact available canonical sources. No new
  review nonce, decision, credit, enrollment or model call.

The existing Missions flyout contains **Getting Started** (existing finite
fraction/percentage and all optional starters) and **Learning** (one active
contract, else at most one featured recommendation). Learning is requestable
with an eligible existing workspace even if optional onboarding is incomplete.
No extra mission board, duplicate Home recommendation or changing denominator.

Basic guidance gives goal, host-owned criteria, permission/cost disclosure and
one next action. Guided adds optional step help; users can turn coaching off.
For new personalized offers, the host resolves `coaching_depth` and
`max_coaching_hints` before the model call. Basic always permits zero hints.
Guided normally permits at most one hint per compiled step; two distinct,
current-support-version independent Ori-path records for both the selected
structure's competency and evaluation in the selected current scope permit a
brief one-hint allowance. Unknown/historical/future support, visits, arbitrary
activity, incomplete coverage and other structures do not qualify. Explicit
`full_guidance` keeps the full guided allowance. This is a bounded support
policy, not an ability rating or an inference about help outside Ori. Brief
remains Guided for evidence; every compiled criterion remains required. The
model cannot expand the allowance, and changing preferences never rewrites an
accepted contract. Automatic candidate fingerprints include resolved depth.
Display why from actual signals, not inferred competence/interest. Active work
wins priority; a smaller/alternative offer cannot overwrite it without explicit
replacement. Start learning, Open Ticket, Review result, Make smaller,
Something else, Not now, Not interested, Stop mission and coaching off have
distinct labels/effects. Empty says no useful suggestion, not failure. Off
leaves ordinary work and finite onboarding usable. Result review lives on the
existing Ticket result surface with exact Run/artifact selection and coverage
labels, not a detached model rating. Restore keyboard focus on dismissal,
provide labelled inputs/live status, and escape all model/workspace text.

## Lifecycle and privacy matrix

| Event | Learning behavior | Ordinary work / onboarding |
| --- | --- | --- |
| Getting Started reset | preserve learning | existing reset/reward semantics unchanged |
| Learning-history clear | increment epoch; clear instances/receipts/credit; fence claims, retain minimal budget/idempotency records and explicitly retained ongoing scope identity; no late resurrection | Tickets/Runs and Getting Started remain |
| Clear preferences | revoke scopes/observation/automatic consent; fence proposals; require fresh opt-in | preserve earned minimal aggregate and actual work |
| Stop/coaching off | fence pending recommendations; stop learning tracking as selected | do not cancel Ticket/Run |
| Delete/move/revoke workspace/source | invalidate new actions; redact inaccessible titles/text/links; show evidence unavailable | earned minimal aggregate remains until explicit clear |
| Import/export | no learning records, consents, operations, grants or review authority travel | portable work stays ordinary inert history |
| Full app-record reset | clear all learning tables/caches under established fence; new process required | existing full reset contract |
| Unsupported definition/schema | opaque unavailable history, no execution/backfill; explicit stop/dismiss or offer invalidation changes only state/revision/time, preserving raw future definition/progress bytes | normal work unchanged |

Never keep copied prompts/results in evaluation analytics. Local diagnostics
contain structure, state, competency, distinct attempt count, bounded timestamps,
usage/call outcomes and unknown usage only. No outbound telemetry, streaks,
penalties, new Craft/XP or claims about retention. Source removal redacts notes
and source details in learning history while keeping content-free dedup credit.

## Required verification and quality gate

Deterministic invariant matrix:

| Boundary | Required negative cases |
| --- | --- |
| Owner/evidence | foreign owner/workspace, forged actor, Done without review, wrong Run/artifact/digest, failed/partial Run, missing/truncated/ambiguous result |
| Operations | simultaneous acceptance, double submit, same ID/different payload, stale revision, crash before/after each store write, deleted target, later unrelated retry |
| Generation | tools/MCP/paths absent, hostile names, extra/duplicate fields, unsupported steps, tool reply, unsupported isolation, timeout, changed consent/model, zero calls on reads/off |
| Progress | dedup events, distinct attempts, unknown guidance, changed input provenance, import/replay, no rewards, no inactivity penalty |
| Lifecycle | stop/clear/reset/deletion during held provider response, restart reconciliation, no automatic replay/catch-up, reverse reset inventory |
| UI | production routes, exact result comparison/reuse, unchanged onboarding fraction, keyboard/focus, both themes, narrow/desktop, escaped hostile content |

Real-model quality requires a separately approved isolated-capable provider,
configuration and spend ceiling. Until supplied/exercised it is **NOT RUN**, not
satisfied by scripted responses. Use identical synthetic contexts for generic
and personalized cases: two different explicit interests with equivalent
practice; experienced vs unknown guidance; one missing/partial source; one
unachievable candidate; malicious names demanding commands/permission changes;
a useful revision; meaningful vs unchanged-input reuse; thin candidates/off.
At most 10 calls, no retries, within the default per-call limits and the approved
spend ceiling (whichever is smaller). Human-review relevance, achievable next
action, guidance, unnecessary work, safety and clarity on 0–2; any unsafe or
unsupported suggestion must be rejected; any safety failure or score 0 blocks
quality acceptance. Preserve sanitized observations, distinguish protocol
validation from assessed quality, and fix or explicitly block failures. Later
7/28-day independent-use/retention study is opt-in and remains unmeasured.
