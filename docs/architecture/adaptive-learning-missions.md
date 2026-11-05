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
   the canonical Run and its scope, not only that pointer.
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
visible goal, input and guidance. Changed copy/criteria is a separate offer
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

Record guidance selection, host-rendered hint version, shown/used receipts and
attempt identity. UI acknowledgements describe displayed support, not human
attention. An opted-in ordinary-work review uses the same decision/evidence
verifier without manufacturing a mission. Only work reviewed and observed after
the relevant consent epoch qualifies; old Done/history cannot be backfilled.
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

## Durable records and transitions

Use the application's SQLite connection and migration **88** (recheck head at
introduction). New tables are local-only and enter `ResetRecordTables` in the
same change. No continuity collector/export/import registration.

- `learning_owners`: owner, revision, deletion epoch, scoped consent/settings,
  configured-model fingerprint, cadence counters; off/on-request/automatic.
- `learning_instances`: ID, owner/epoch, revision, structure/version, immutable
  proposal JSON, workspace incarnation, status/blocker, acceptance and timestamps.
- `learning_operations`: owner + operation ID unique, kind, request digest,
  expected revision/epoch, persisted status, exact instance/Ticket/attempt links,
  provider fingerprint and bounded usage. One live generation claim per owner.
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
bound by the canonical execution owner at Run creation, not by “latest Run”
heuristics or event timestamps. A later unrelated Run cannot fulfill the claim.
After restart, reconcile persisted exact references without replaying execution.

WorkGate permits cover every finite mutation and callback through persistence.
History/preferences clear increments a durable owner epoch transactionally;
provider replies/event handlers must match it. Never let a stale callback
upsert a deleted owner. Workspace deletion/incarnation and scope revisions are
checked before publishing and before actions. Use content-free blocker codes
and never leak foreign record existence through errors.

## Context, model and cost contract

Default is **on request**, with no authorized workspace until explicitly chosen.
Observation and automatic suggestions are separate opt-ins. User-entered focus
and effort preferences are explicit; activity is an observation with coverage,
not an inferred sensitive interest or permanent preference. Sharing workspace
metadata for suggestion design does not grant source-file or execution access.

Input is data-only JSON: bounded workspace display name, user-reviewed goal,
explicit preferences, host candidate references/structure IDs, readiness,
minimal recent outcome counts and practice state with unknown/partial markers.
No directory paths, file contents, unrestricted chat/messages, raw results,
secrets, internal feedback, arbitrary URLs or inherited assistant memory.
Untrusted names/prose remain data. Model-authored fields are `goal`, `scope`,
`why`, `guidance`; all are escaped plain text. Host alone renders labels,
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
| Not now cooldown / Not interested | 7 days / persistent topic + structure suppression until reset |
| recent terminal proposals / non-evidence operations | 100 / 200 retained for at least 30 days |
| full local review receipts | 1000; preserve active/recovery dependencies, compact old earned evidence to dedup tombstones |
| dedup tombstones / total learning data | 10,000 / 16 MiB; refuse new admission at cap, never silently discard dedup authority |

Generation selects only the configured Default provider/model/effort. Require
`SupportsIsolatedChat`, then `ChatRequest.Isolated = true`, exactly host system
instructions + one data message; no Tools, MCPServers, WorkspaceID/Dir,
ExecutionScope, agent prompt/loadout, files or streams. Use ResponseSchema only
when supported, and strictly validate the response regardless. Reject tool
calls, unsupported IDs/steps, duplicate/extra JSON fields, trailing JSON,
malformed/fabricated candidate references, action URLs/commands and scope
expansion. Schema validity alone does not establish useful or safe prose.

Persist claims before calls; include timeout/cancellation/unknown usage in
accounting, with a reservation at the ceiling. Revalidate owner epoch, scope,
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

## API and presentation

Namespace `/api/learning-missions`; all replies no-store. Strict bounded JSON,
reject duplicate keys/unknown fields/trailing values. Resolve owner outside
body. Workspace references use `studio_id`. Errors: 400 malformed, 404
unavailable/foreign, 409 stale/unknown/replayed-different-content, 429 quota,
503 unavailable provider/store; sanitized reason codes only.

- `GET /api/learning-missions`: saved active/featured/history/preferences,
  competencies and read-only availability. No mutations or generation.
- `POST /api/learning-missions/proposals`: explicit selected scope +
  `operation_id`, `expected_revision`, mode request/smaller/alternative.
- `POST /api/learning-missions/instances/{id}/decisions`: accept/defer/decline/
  stop/replace, `operation_id`, expected instance and owner revisions.
- `POST /api/learning-missions/instances/{id}/work`: reviewed bounded input,
  operation/revision; prepares real Ticket only, never implicit start.
- `POST /api/learning-missions/instances/{id}/review`: issue pinned review
  challenge or submit its exact nonce/decision/reason; never caller completion.
- `POST /api/learning-missions/preferences`: scoped consent, mode, guidance,
  explicit interests/effort; revocation fences in-flight writes.
- `POST /api/learning-missions/clear`: explicit history/preferences/exclusions
  scope; increments relevant epochs, leaves actual work intact.
- `GET /api/learning-missions/operations/{id}`: exact operation recovery only.

The existing Missions flyout contains **Getting Started** (existing finite
fraction/percentage and all optional starters) and **Learning** (one active
contract, else at most one featured recommendation). Learning is requestable
with an eligible existing workspace even if optional onboarding is incomplete.
No extra mission board, duplicate Home recommendation or changing denominator.

Basic guidance gives goal, host-owned criteria, permission/cost disclosure and
one next action. Guided adds optional step help; users can turn coaching off.
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
| Learning-history clear | increment epoch; clear instances/receipts/credit/claims; no late resurrection | Tickets/Runs and Getting Started remain |
| Clear preferences | revoke scopes/observation/automatic consent; fence proposals; require fresh opt-in | preserve earned minimal aggregate and actual work |
| Stop/coaching off | fence pending recommendations; stop learning tracking as selected | do not cancel Ticket/Run |
| Delete/move/revoke workspace/source | invalidate new actions; redact inaccessible titles/text/links; show evidence unavailable | earned minimal aggregate remains until explicit clear |
| Import/export | no learning records, consents, operations, grants or review authority travel | portable work stays ordinary inert history |
| Full app-record reset | clear all learning tables/caches under established fence; new process required | existing full reset contract |
| Unsupported definition/schema | read-only unavailable history, no execution/backfill | normal work unchanged |

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
