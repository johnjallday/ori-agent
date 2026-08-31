# Personal Assistant Foundation Contract v1

Status: accepted for implementation

## Purpose

Ori exposes one user-chosen, durable personal-assistant relationship. The
assistant is hired during onboarding, lives as the entry-agent instance of the
user's designated Personal HQ workspace, and remains the same identity across
Home, Ask Ori, Personal HQ, and project handoffs.

This contract makes that relationship a product boundary instead of a UI label.
It does not create a second agent runtime, a new workspace kind, or a parallel
router. Existing workspace agents, Home routing, task/follow-up records, Daily
Brief generation, workspace memory, and user-profile services remain canonical.

## Current seam inventory

| Surface | Current owner | Dependencies and consequence boundary |
|---|---|---|
| `Ask Ori` Home composer and activity | `dashboard.js`, `/api/home-assistant/ask`, and `/api/home-assistant/route` | Resolves a plan and target workspace/agent, displays confirmation for consequential work, then starts the existing execution path. |
| Ori Guide panel | `ori-guide.js` and `agenthttp.OriGuideHandler` | Deterministic, structurally read-only help. Work-shaped requests escalate to Home routing; the guide itself cannot mutate. |
| Protected system assistant | `systemassistant.Identity` plus agent-store lookup | Canonical internal `Ask Ori` record used by infrastructure. It is never a user hire or display-name source. |
| Global launcher/navbar/help | navbar/dashboard templates and `app.js` | Opens the Home composer or guide panel; it does not choose or persist identity. |
| Legacy onboarding assistant name | `onboarding.Manager` and `onboarding.js` | `app_state.json` currently defaults `assistant_name` to `Ori`; PAF must not treat this legacy presentation field as a stable assistant. |
| Personal HQ setup | `personalhq.SetupCoordinator` through `sessionhttp.Handler.CreateFromTemplate` | Creates `personal-ops`, seeds global profiles plus stable workspace agent instances, designates the HQ, and stores provisional brief input. Partial failures return the created workspace ID. |
| Personal HQ identity | session/workspace agent-instance model | `Workspace.EntryAgent` selects the entry profile by name today; `AgentInstance.ID` is the stable workspace attachment that PAF binds. |
| Daily Brief | `dailybrief.Service`, HTTP handler, scheduler, and Home renderer | Durable user/HQ-scoped config and revisions. First-open/scheduled claims enforce existing deduplication. |
| Tickets | `workspace.TicketService` | Target-workspace-owned canonical project work; confirmation remains in the caller before creation/execution. |
| Follow-ups | `followup.Service` | User/HQ-owned commitments with source-key deduplication and optional project-task links. |
| Profile and memory | `userprofile` and workspace `MemoryStore` | Global preferences are field-allowlisted; workspace facts remain in `MEMORY.md`; both reject secret-like text. |

## State and action matrix

| State | Meaning | Required copy | Allowed next actions |
|---|---|---|---|
| `ineligible` | Legacy install, rollout disabled at first state-file creation, or explicit version 0 | Existing Home/Ask Ori copy; no hire claim | Continue legacy Home and Help only |
| `needs_hire` | Eligible install with no durable relationship | “Hire your assistant”; name defaults to editable **Assistant** | Preview/confirm hire |
| `hiring` | A confirmed hire is partially provisioned | “Finishing your assistant setup” | Retry/resume the same request; inspect bounded failure |
| `active` | Binding, HQ, and entry-agent instance resolve | Chosen name and “Your personal assistant” | Ask, assign, pause, edit agreement/profile, open HQ |
| `paused` | Relationship exists but proactive/background behavior is paused | “Assistant paused” | Resume, inspect/edit, deterministic manual assignment |
| active with no model | Healthy hire but no chat-capable model resolves | **“Hired — choose a model to chat”** | Choose model; deterministic first assignment and deterministic Daily Brief remain available |
| `repair_needed` | Binding exists but HQ/agent linkage is missing, foreign, or invalid | “Assistant setup needs repair” with no invented identity | Retry deterministic repair, choose replacement explicitly, or remain paused |

`pre-hire` in product prose maps to `needs_hire`; `partial-hire` maps to
`hiring` unless deterministic validation proves that the recorded HQ/agent can
no longer be resumed, in which case it maps to `repair_needed`.

Ori Help is always available from the global Help menu and contextual **Ask Ori
about this screen** action. Both open the same guide-only panel. Help may read
only bounded route/screen/topic metadata. It cannot receive the hired
assistant's mandate, profile memory, workspace memory, source content, or chat
history, and it cannot create a task, follow-up, brief, workspace, connection,
or run. The hired assistant may receive the canonical user profile, its
Personal HQ memory/working agreement, approved source snapshots, and the
current request subject to existing access and confirmation gates.

## Identity and ownership

A personal assistant has one stable binding per user:

- `user_id` owns the relationship;
- `workspace_id` identifies the designated Personal HQ;
- `agent_instance_id` identifies that workspace's entry agent;
- `display_name` is user-controlled presentation data, not identity;
- `provenance` records that onboarding explicitly selected and hired it;
- `status` and bounded failure fields support resumable provisioning; and
- created/updated timestamps provide lifecycle provenance.

The identity key is `(user_id, agent_instance_id)`. Names, prompts, models,
workspace titles, folder slugs, and the protected system-assistant marker are
never identity inputs. Renaming changes presentation only. A Personal HQ rename
or restart does not change the binding.

The canonical protected system assistant (`Ask Ori`, marker
`ori:system-assistant`) remains an internal implementation detail. It may answer
structural help or execute existing Home routing internals, but it must never be
returned as the hired identity, shown as an alternative relationship, or share
an agent-instance ID with the personal assistant.

The persisted linkage is:

```text
personal_assistant_state.assistant_id (stable relationship identity)
  -> global_agent_profile_name (mutable lookup key for the current agent store)
  -> hq_entry_agent_instance_id (stable workspace attachment UUID)
  -> hq_workspace_id (stable Personal HQ UUID)
```

Reads validate every arrow. Display name and global profile name may be updated
together, but `assistant_id`, entry-instance ID, and HQ workspace ID remain
stable. A mismatch never falls back to a name search.

## Canonical ownership

| Data | Canonical owner |
|---|---|
| Relationship lifecycle, chosen appearance, mandate, focus areas, hire receipt | `personal_assistant_state` |
| First-assignment preview/apply journal and resulting canonical references | `personal_assistant_assignment` |
| Daily routine/schedule and generated brief content | Daily Brief config/revision tables |
| User-wide identity and preferences | `userprofile` |
| HQ working agreement and operational facts | designated HQ `MEMORY.md` |
| Project work | target workspace Tickets |
| Commitments/dependencies | Follow-Ups owned by the user/HQ, optionally linked to a Ticket |
| Agent runtime/profile configuration | existing global agent store plus stable HQ agent instance |

## Mutation and idempotency contract

| Mutation | Version/idempotency rule |
|---|---|
| Hire preview | Pure bounded normalization; hash normalized payload. No workspace, agent, or state mutation. |
| Hire apply/resume | Requires a request ID. `last_hire_request_id` returns the existing outcome on replay; state transitions use compare-and-swap `state_version`. |
| Pause/resume | Requires current `state_version`; stale writes return conflict and the current version. |
| Profile/working-agreement edit | Requires current state version; profile and memory fields additionally use their canonical validators. |
| First-assignment preview | Creates one journal row keyed by opaque preview ID and stores normalized payload/hash only. Repeated identical request IDs return that preview. |
| First-assignment apply | Requires preview ID, assignment version, and matching payload hash. One terminal application stores canonical refs; replay returns those refs without recreating records. |
| Daily Brief manual generation | Uses existing Daily Brief claim/revision idempotency; PAF stores no brief body or schedule duplicate. |
| Ticket creation | Uses source `assistant` and assignment-derived source ID; replay resolves the existing target-workspace record. |
| Follow-up capture | Uses existing source-dedup key derived from user and assignment source ID. |
| Rename | Requires current state version and updates global profile plus bound instance name without changing stable IDs. |

Request IDs, preview IDs, hashes, and canonical refs are bounded opaque values.
No mutation stores credentials, chat transcripts, source contents, or brief
contents in PAF tables.

## Onboarding creation path

Fresh onboarding collects only the assistant's name before the hiring
consequence. The name must be non-empty, bounded, plain text, and secret-safe.
Completion then:

1. creates a normal `personal-ops` workspace through the existing template
   creation path;
2. designates that workspace as the user's Personal HQ;
3. resolves the created workspace's stable entry-agent instance;
4. renames that instance to the chosen name without changing its ID;
5. persists the selected-hire binding; and
6. completes onboarding only after the binding can be read back.

Retries are idempotent. A persisted active binding is returned rather than
creating another assistant. A partial attempt records a resumable state and the
created workspace ID when available. Recovery may finish the missing step, but
must not guess an agent by display name or silently bind the system assistant.

Existing users without a binding enter a bounded migration path: they may adopt
the designated Personal HQ entry agent, build a new HQ and assistant, or defer.
No existing workspace, assistant, memory, task, follow-up, or profile field is
deleted or rewritten without an explicit choice.

## Surfaces and routing

Home, Ask Ori, and Personal HQ resolve the same binding. If it is healthy they
show the chosen assistant name and route work through the existing
`/api/home-assistant/route` and execution pipeline. Ori Guide remains a
structurally read-only deterministic guide and may escalate to that same route;
it is not a peer assistant.

Read surfaces degrade safely when the binding, workspace, or agent is missing:
they return a bounded unavailable/repair state, never a fabricated identity.
Legacy UI continues to function when the personal-assistant service is absent.

## Delegation and ownership

The personal assistant owns intake and remains the user-visible delegator.
Delegation reuses existing workspace routing, specialist selection,
confirmation, execution, and receipts. A delegated run preserves:

- personal-assistant requester identity;
- target workspace and specialist agent identity;
- source request/trace/run IDs;
- confirmation and risk policy; and
- a user-visible result or failure receipt.

Delegation never grants extra tools, changes filesystem scope, bypasses
confirmation, or impersonates the specialist. Cross-workspace work remains
owned by the target project workspace. Personal HQ records only links and
status needed to explain the handoff.

## Fixed commitment and Journal rules

The first-assignment preview classifies before any save:

- an explicit fixed commitment with an actionable title and due date/time maps
  to one due-dated Personal HQ **Ready** Ticket;
- a time-only statement with no actionable title maps to one
  `needs_decision` Follow-Up;
- ambiguous text remains in preview with an explicit choice and creates nothing;
- every preview names the target record type, owner, due value, and source; and
- apply refuses a changed payload/hash so the user can never confirm one mapping
  and save another.

Journal is not shown in the hire flow or ordinary Home UI. The existing Journal
specialist remains attached to Personal HQ with unchanged permissions and may
appear only in the truthful Advanced roster under the **Assistant support**
group. It is never silently removed from an existing HQ.

## Personal HQ data boundaries

Canonical stores remain authoritative:

- project work uses workspace Tickets and keeps ticket source provenance;
- commitments use Follow-Ups and remain user/HQ-owned even when linked to a
  project task;
- preferences and identity facts use User Profile fields;
- workspace-specific operational facts use that workspace's `MEMORY.md`;
- Daily Brief uses its existing durable configuration/revision stores; and
- source integrations continue to enforce their existing consent gates.

Project work must not be duplicated into Personal HQ. Follow-ups may link to a
project Ticket but are not moved into it. Assistant profile edits must preserve
stable IDs and pass existing free-text/secret validation. Memory writes use
`ValidateMemoryText`, fixed workspace roots, and atomic `0600` persistence.

## Privacy, permissions, and telemetry

Hiring and delegation never expand authority. Effective tools, grants,
filesystem roots, native MCP policy, confirmation requirements, and provider
credentials are inherited from existing workspace/runtime boundaries.
Cross-workspace reads use approved connectors and scopes only.

Logs and telemetry contain stable IDs, statuses, counts, durations, route types,
and bounded error classes. They exclude prompts, generated answer text, memory
contents, profile free text, credentials, source document contents, and
absolute paths.

## Failure and recovery semantics

- Workspace creation failure leaves no active binding and keeps onboarding
  retryable.
- Failure after workspace creation records that workspace in the provisioning
  binding so retry can resume rather than duplicate it.
- A missing or trashed HQ returns `workspace_unavailable` and repair actions.
- A missing entry agent returns `assistant_unavailable`; recovery requires an
  explicit replacement or a deterministic ID-preserving repair.
- A stale display name is reconciled from the bound agent instance without
  changing identity.
- Repeated requests with the same idempotency key return the original result.
- Restart rehydrates the binding from durable storage before universal surfaces
  advertise the assistant as available.

## Test matrix

The package/API/browser suites must pin at least these cases:

| Case | Expected projection / invariant |
|---|---|
| Fresh state file + rollout enabled | `needs_hire`, default field “Assistant” |
| Fresh state file + rollout disabled | `ineligible`; reset remains ineligible |
| Existing state file without PAF marker | permanently `ineligible`; reset/restart does not add eligibility |
| Active binding | same chosen identity on Home, Ask Ori, and HQ |
| Active binding with no model | “Hired — choose a model to chat”; deterministic assignment/brief actions enabled |
| Paused binding | reads/profile edits allowed; proactive runs suppressed |
| Missing/foreign HQ or missing bound agent ID | `repair_needed`; no name-based fallback and no memory leakage |
| Ori Help request | guide-only response; zero PAF/Ticket/follow-up/brief mutations |
| Duplicate hire/apply request | same IDs/refs; one HQ, one assistant, one canonical record |
| Fixed actionable commitment | due-dated HQ Ready Ticket shown in preview before save |
| Time-only commitment | `needs_decision` Follow-Up shown in preview before save |
| Journal presentation | hidden in hire/Home; visible under Advanced “Assistant support” only |
| Rename/restart | stable assistant/instance/workspace IDs; new display name everywhere |

## Rollout demo evidence

The group-1 release checkpoint was exercised against two temporary data
directories with the same locally built server binary. The reproducible command
shape was:

```bash
ORI_DATA_DIR="$FRESH" ORI_PERSONAL_ASSISTANT_ROLLOUT=true \
  ori-agent --port "$PORT" --no-browser
curl -s "http://127.0.0.1:$PORT/api/personal-assistant"
curl -s -X POST "http://127.0.0.1:$PORT/api/onboarding/reset"
curl -s "http://127.0.0.1:$PORT/api/personal-assistant"
```

The legacy run pre-created `app_state.json` with ordinary onboarding fields and
no `personal_assistant_rollout_version`, then used the same commands. Bounded
observations (paths and personal data omitted):

```json
{"fresh_before":{"state":"needs_hire","rollout_version":1,"next_action":"hire","model":{"status":"not_configured"}}}
{"fresh_after_reset":{"state":"needs_hire","rollout_version":1,"next_action":"hire","model":{"status":"not_configured"}}}
{"legacy_before":{"state":"ineligible","rollout_version":0,"next_action":"continue_legacy"}}
{"legacy_after_reset":{"state":"ineligible","rollout_version":0,"next_action":"continue_legacy"}}
```

This proves reset does not enroll or remove eligibility and model absence is an
independent capability flag rather than a fabricated assistant failure.

## Compatibility

Old profiles with no binding retain existing Home and Ask Ori behavior until
migration is chosen. Existing Personal HQ, Daily Brief, Follow-Up, workspace,
and protected system-assistant records remain valid. The feature may be disabled
without deleting state; re-enabling resolves the exact stable binding and does
not create a duplicate assistant.
