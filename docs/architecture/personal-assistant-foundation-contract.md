# Personal Assistant Foundation Contract v1

Status: accepted for implementation

Amendment 1 — Guided Personal HQ Map quest: hiring no longer creates Personal
HQ. This amendment supersedes the automatic-HQ requirements of the historical
PAF PRD (`tasks/prd-personal-assistant-foundation.md`, FR19–FR24) and the
automatic-HQ hire sequence previously recorded in this contract. The rest of the
PAF contract is unchanged. The superseded decision is recorded, not erased: an
installation hired before this amendment keeps its existing active relationship
and never replays the quest.

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
| Onboarding guide name | `onboarding.Manager` and `onboarding.js` | `app_state.json` defaults the app guide name to `Ori`; PAF never treats this presentation field as the hired assistant's stable identity. |
| Personal HQ setup | `personalhq.SetupCoordinator` through `sessionhttp.Handler.CreateFromTemplate` | Creates `personal-ops`, seeds global profiles plus stable workspace agent instances, designates the HQ, and stores provisional brief input. Partial failures return the created workspace ID. Unchanged for legacy/non-PAF builds. |
| Post-hire HQ setup | `personalassistant.HQSetupCoordinator` over the same canonical services | The PAF-owned consequence of the guided Map quest. Reuses the already-hired global profile as entry agent, designates HQ, saves Daily Brief, and activates the relationship. Versioned and idempotent; partial failures stay resumable. |
| Ori HQ quest walkthrough | `personal-hq-quest.js` over `ori-guide.js` presentation and the Home cockpit view seam | Deterministic, model-free, focus-only. Observational: removing it leaves the Map and build flow fully functional. |
| Personal HQ identity | session/workspace agent-instance model | `Workspace.EntryAgent` selects the entry profile by name today; `AgentInstance.ID` is the stable workspace attachment that PAF binds. |
| Daily Brief | `dailybrief.Service`, HTTP handler, scheduler, and Home renderer | Durable user/HQ-scoped config and revisions. First-open/scheduled claims enforce existing deduplication. |
| Tickets | `workspace.TicketService` | Target-workspace-owned canonical project work; confirmation remains in the caller before creation/execution. |
| Follow-ups | `followup.Service` | User/HQ-owned commitments with source-key deduplication and optional project-task links. |
| Profile and memory | `userprofile` and workspace `MemoryStore` | Global preferences are field-allowlisted; workspace facts remain in `MEMORY.md`; both reject secret-like text. |

## State and action matrix

| State | Meaning | Required copy | Allowed next actions |
|---|---|---|---|
| `needs_hire` | No durable relationship | “Hire your assistant”; name defaults to editable **Assistant** | Preview/confirm hire |
| `hiring` | A confirmed hire is finalizing the assistant profile and relationship | “Finishing your assistant setup” | Retry/resume the same request; inspect bounded failure |
| `needs_hq` | A real assistant profile and relationship exist; no Personal HQ has been built | Chosen name plus “Let’s give <name> a home base” | Start/resume the Ori HQ Map quest (`build_hq`); defer it |
| `provisioning_hq` | A confirmed HQ setup is partially applied and resumable | “Finishing your Personal HQ” | Resume the same HQ request (`resume_hq_setup`); inspect bounded failure |
| `active` | Binding, HQ, and entry-agent instance resolve | Chosen name and “Your personal assistant” | Ask, assign, pause, edit agreement/profile, open HQ |
| `paused` | Relationship exists but proactive/background behavior is paused | “Assistant paused” | Resume, inspect/edit, deterministic manual assignment |
| active with no model | Healthy hire but no chat-capable model resolves | **“Hired — choose a model to chat”** | Choose model; deterministic first assignment and deterministic Daily Brief remain available |
| `repair_needed` | A durable result exists but its known safe continuation failed, or HQ/agent linkage is missing, foreign, or invalid | “Assistant setup needs repair” with no invented identity | Retry deterministic repair, choose replacement explicitly, or remain paused |

`hiring` stays scoped to creating and finalizing the assistant profile and the
relationship row. It never means “creating a workspace”.

`needs_hq` is an expected setup stage, not corruption. The relationship is
healthy but incomplete: the hired identity (assistant ID, display name,
appearance, working agreement, state version) is fully readable, while Personal
HQ, the HQ agent instance, and Daily Brief report `not_configured` with the
stable reason `hq_not_built`. They must not report `unavailable`, must not
report `repair_needed`, and must not be fabricated as healthy-and-empty.

`pre-hire` in product prose maps to `needs_hire`; `partial-hire` maps to
`hiring` or `provisioning_hq` depending on which consequence was claimed, unless
deterministic validation proves that the recorded profile/HQ can no longer be
resumed, in which case it maps to `repair_needed`.

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
| Hire apply/resume | Requires a request ID. `last_hire_request_id` returns the existing outcome on replay; state transitions use compare-and-swap `state_version`. Creates the owned profile and relationship only. Persisted pre-amendment auto-HQ operations are distinguishable by payload version and resume through their old safe finalization path; they are never abandoned or duplicated. |
| HQ setup apply/resume | Requires the current `state_version` and a stable HQ request ID bound to a normalized payload hash. The client supplies only the bounded HQ form fields — never assistant, profile, or workspace identity. Replay returns the same canonical result; a changed payload under the same request ID, or a stale version, returns `409`. Partial results are durable, bounded, and resumable with a safe repair step code that carries no provider or database text. |
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

The setup sequence is:

```text
hire profile -> needs_hq -> Ori Map quest -> confirmed HQ setup -> active
```

Fresh onboarding collects the assistant's name/appearance and focus/mandate
across three focused client-side steps — Meet, Focus, Confirm — before the
hiring consequence. Back/Next preserves the in-memory draft and sends no hire
request. The name must be non-empty, bounded, plain text, and secret-safe. The
Daily Brief rhythm is **not** collected here: it has no canonical workspace to
be written against until HQ exists, so it moves to the Map's HQ build form.

One final, confirmed hire then:

1. creates or resolves exactly one owned global agent profile from the canonical
   `personal-ops` entry specification — same orchestrator role, trusted Personal
   Assistant prompt fragment, model defaults, and validated appearance the
   eventual HQ entry agent must use;
2. persists bounded profile provenance binding that profile to the stable
   assistant ID and hire request ID, so a retry can tell its own profile from an
   unrelated name collision;
3. persists the relationship, working agreement, and `HiredAt`; and
4. transitions to `needs_hq` and completes ordinary onboarding only after the
   relationship can be read back.

The hire creates **no** workspace, no Personal HQ designation, no Journal or
other support profile, no workspace membership, no Daily Brief configuration,
and no tool/skill/MCP/Vault/filesystem change.

After completion, the client navigates to `/?quest=build-hq` and Home features
the optional **Build My HQ** mission, followed by **Plan my first day**. Build My
HQ stays featured while it is available or skipped; designation completes it
exactly once. Plan my first day is featured only after the relationship reads
back `active` with validated HQ and entry-instance linkage; `?quest=plan-first-day`
does not open while `needs_hq`.

Confirming the Map's Build My HQ form is the sole HQ creation boundary. That one
confirmed request:

1. claims a versioned, idempotent HQ setup operation;
2. creates the `personal-ops` workspace through the canonical PAF template path,
   reusing the already-hired global profile as the stable entry-agent instance
   rather than creating a second one;
3. adds only the truthful support roster (for example Journal) exactly once and
   never a Personal Chief of Staff;
4. persists the returned workspace ID and entry-agent instance ID at the first
   safe checkpoint;
5. designates the workspace as Personal HQ through `personalhq.Service`;
6. saves the Daily Brief configuration through the canonical Daily Brief service
   against the real workspace ID;
7. marks HQ onboarding completed, clears the provisional operation payload, and
   transitions the relationship to `active`.

The first-assignment quest keeps its existing shape: its three category screens
mutate only the browser draft; review persists the existing bounded preview
journal, and final confirmation remains the sole path to atomic canonical apply.
A successful durable apply independently completes the quest and generates the
first Daily Brief.

Retries are idempotent. A persisted relationship is returned rather than
creating another assistant, and a persisted HQ operation is resumed rather than
creating another workspace. A partial attempt records a resumable state and the
created workspace ID when available. Recovery may finish the missing step, but
must not guess an agent or workspace by display name, silently bind the system
assistant, or delete-and-recreate as a repair.

## Guided Personal HQ Map quest

### Who drives it

Ori — the deterministic app guide — owns the walkthrough. The hired assistant is
its named *subject*, never its driver. No model call, assistant runtime, prompt,
or `/api/ori-guide` request is involved in a fixed quest step: the quest works
with no provider configured at all.

Approved framing is “<name> is hired. Let’s give <name> a home base.” The name is
user-controlled data and is rendered as a text node, never as HTML.

### Allowed and forbidden actions

Ori may:

- open the existing Map view through the Home cockpit's public view seam;
- present fixed quest copy naming the hired assistant; and
- apply registered, typed, route-bound coachmarks that highlight and focus a
  hand-written Home selector.

Only the user may:

- select the reserved Personal HQ site;
- open **Build My HQ**;
- edit the HQ name, Daily Brief rhythm, and scope fields; and
- confirm creation.

Coachmarks are focus-only. The quest controller's action union is structurally
incapable of expressing a click, selection, form open, submit, navigation to a
mutating endpoint, or any other mutation. Coachmark keys are a closed registry
mirrored on the server and the browser, and a key resolves only to a
hand-written selector (`[data-hq-site]`, the active visible build action); a
selector is never accepted from API, model, or user input.

A coachmark carries a decorative pointer that taps at the marked control, so the
step's "click this next" reads without having to be parsed out of the copy. The
pointer is presentation only and is bound by the same focus-only rule: it is
`aria-hidden`, never focusable, `pointer-events: none` so it cannot intercept the
click it indicates, and never the sole signal — the outline and the panel copy
still name the control, and under `prefers-reduced-motion` the pointer remains as
a static marker with its motion dropped.

Two properties keep a mark truthful against a live page:

- **Re-anchoring.** The Map re-mounts its tiles when HQ status arrives, swapping
  the marked node for an identical new one. A mark re-resolves onto the live node
  from the key it was made with, and is dropped only when the control is gone for
  good. Without this a mark silently survives as a detached node: invisible, and
  positioning its pointer at an all-zero rect.
- **A bounded wait for a mounting control.** A step is presented in the same tick
  as the dialog that mounts the control it names, so a walkthrough step may wait
  a bounded number of short retries for its target before degrading to words.
  This is opt-in per call and set only by walkthrough steps, which know a control
  is about to exist; an ordinary guide answer still reports an absent control
  immediately, because there it really is absent.

The quest is observational. Removing the quest controller entirely must leave
the Map, the site context dialog, and the HQ build flow fully functional.

### Steps

1. `/?quest=build-hq` opens Map view and highlights the existing reserved
   Personal HQ site. It does **not** preselect it — selecting the highlighted
   landmark is the first user interaction.
2. A real user selection opens the existing context dialog; Ori then clears the
   old mark and highlights **Build My HQ**.
3. A real user activation opens the existing HQ form, which owns all editing and
   confirmation. Ori only explains that nothing is created until confirmation.

### Defer and resume

**Do this later** is always allowed. It explicitly invokes the existing optional
HQ quest's skip path, clears coachmarks and quest query state, and leaves **Build
My HQ / Resume quest** prominent on Home. Closing Ori alone pauses presentation;
it does not skip or complete the server-side quest. Abandoning the walkthrough,
reloading, or disabling JavaScript leaves a resumable server-owned state, never
an assumed completion.

Build My HQ is never marked complete by hiring, profile creation, opening the
quest, selecting the site, or opening the modal. Only a completed designation
completes it.

### While HQ is missing

During `needs_hq` the relationship projects the hired name and appearance, and
nothing more is invented. Today, ordinary assistant work, `/api/home-assistant/ask`
handoffs, memory, capabilities, pause/routine controls, working-agreement
schedule mutations, and the first assignment are unavailable, each with a direct
**Build Personal HQ** action rather than a “hire” prompt or a generic repair
message. Direct first-assignment URLs and APIs stay closed and create no
preview, Ticket, follow-up, or brief.

### New HQ only

The guided `needs_hq` path offers **Build My HQ** and **Do this later** only. It
does not offer Import HQ, because an imported workspace must never be silently
rebound as the hired assistant's HQ. Legacy and import behavior — including
`POST /api/personal-hq/setup` and the generic Build My HQ copy — remains
unchanged outside this state.

Personal-assistant onboarding is the only supported first-run path. No cohort
marker is read or persisted: an installation without a relationship enters
`needs_hire`. This project made that clean break before broad adoption, so no
parallel legacy cohort or adoption wizard is maintained.

## Surfaces and routing

Home, Ask Ori, and Personal HQ resolve the same binding. If it is healthy they
show the chosen assistant name and route work through the existing
`/api/home-assistant/route` and execution pipeline. Ori Guide remains a
structurally read-only deterministic guide and may escalate to that same route;
it is not a peer assistant.

Read surfaces degrade safely when the binding, workspace, or agent is missing:
they return a bounded unavailable/repair state, never a fabricated identity.
The personal-assistant service is a required part of the canonical server build.

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

Hire and HQ quest lifecycle events — hire completed, HQ quest started, HQ quest
deferred, HQ setup started, HQ activated — carry only closed state names, stable
IDs, counts, durations, and reason codes. They never carry assistant names, HQ
names, Daily Brief schedule fields, mandate text, paths, or quest copy.

## Failure and recovery semantics

- Profile creation failure leaves no relationship and keeps the hire retryable
  under the same request ID.
- Profile created but relationship finalization failed reports a bounded partial
  and resumes to the same profile; it never adopts a same-named profile it does
  not provably own.
- Workspace creation failure leaves the relationship in `needs_hq` with no HQ
  linkage and keeps the quest retryable.
- Failure after workspace creation records that workspace in the provisioning
  binding so retry can resume rather than duplicate it.
- A hired profile that is the current relationship is protected from ordinary
  single and bulk delete and from ordinary rename while HQ is missing, with an
  actionable Build HQ / relationship-management response. Unrelated unattached
  agents keep their current rename and delete behavior.
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
| Fresh state file | `needs_hire`, default field “Assistant” |
| Existing state file without a relationship | `needs_hire`; no cohort marker or migration gate |
| Active binding | same chosen identity on Home, Ask Ori, and HQ |
| Active binding with no model | “Hired — choose a model to chat”; deterministic assignment/brief actions enabled |
| Paused binding | reads/profile edits allowed; proactive runs suppressed |
| Missing/foreign HQ or missing bound agent ID | `repair_needed`; no name-based fallback and no memory leakage |
| Ori Help request | guide-only response; zero PAF/Ticket/follow-up/brief mutations |
| Fresh confirmed hire | one owned global profile; `needs_hq`; zero workspaces, zero HQ designation, zero Journal profile, zero Daily Brief config |
| `needs_hq` projection | named identity readable; Personal HQ, agent instance, and Daily Brief `not_configured` with reason `hq_not_built`; `next_action` is `build_hq` only |
| Quest opened, site selected, or modal opened | no workspace, designation, brief, or quest completion; Build My HQ still featured |
| **Do this later**, then resume | quest recorded skipped; Build My HQ / Resume mission still prominent; resume re-enters at step 1 |
| Ori closed during the walkthrough | presentation pauses; quest is neither skipped nor completed |
| Confirmed Map HQ build | one relationship, one global hired profile reused as entry agent, one HQ, one entry instance, one Journal support instance, one Daily Brief config, zero Personal Chief of Staff |
| Duplicate hire/apply or HQ setup request | same IDs/refs; one HQ, one assistant, one canonical record |
| HQ setup replay with a changed payload, or stale version | `409`; no second workspace and no partial overwrite |
| Pre-amendment active/paused relationship | unchanged byte-for-behavior; never replays the HQ walkthrough |
| Pre-amendment incomplete auto-HQ operation | resumes through its old safe path or reports bounded repair; never duplicates a profile or workspace |
| Hired profile before HQ exists | ordinary single/bulk delete and ordinary rename refused with a Build HQ action; unrelated agents unaffected |
| Quest with no model configured | every step still works; no `/api/ori-guide` or provider call |
| Fixed actionable commitment | due-dated HQ Ready Ticket shown in preview before save |
| Time-only commitment | `needs_decision` Follow-Up shown in preview before save |
| Journal presentation | hidden in hire/Home; visible under Advanced “Assistant support” only |
| Rename/restart | stable assistant/instance/workspace IDs; new display name everywhere |

## Canonical onboarding evidence

The first-run checkpoint is exercised against a temporary data directory with
the ordinary server launch; no feature flag is required:

```bash
ORI_DATA_DIR="$FRESH" go run ./cmd/server --port "$PORT" --no-browser
curl -s "http://127.0.0.1:$PORT/api/personal-assistant"
curl -s -X POST "http://127.0.0.1:$PORT/api/onboarding/reset"
curl -s "http://127.0.0.1:$PORT/api/personal-assistant"
```

Without `ORI_DATA_DIR`, `go run ./cmd/server` uses the ordinary `./ori-data`
profile and produces the same onboarding state.

Fresh state and an onboarding reset both resolve to the same bounded state:

```json
{"before":{"state":"needs_hire","next_action":"hire","model":{"status":"not_configured"}}}
{"after_reset":{"state":"needs_hire","next_action":"hire","model":{"status":"not_configured"}}}
```

Model absence remains an independent capability flag rather than a fabricated
assistant failure.

### Settings Reset behavior

Settings Reset remains selective and always requires an application restart.
Its exact Personal Assistant Foundation effects are:

| Selected category | PAF effect after restart |
|---|---|
| Settings | Removes provider/preferences configuration only. The relationship, assistant profile, Personal HQ, and records remain; model readiness can become `not_configured`. |
| Agents | Removes global agent profiles but not the relationship, Personal HQ, or its persisted entry-agent instance. The relationship read therefore keeps the same stable binding; profile-dependent management such as rename can report the missing profile and must never silently rebind by name. |
| Sessions | Removes `sessions.db`, workspaces, and session files. This intentionally removes the PAF relationship journal and Personal HQ records, so the installation returns to `needs_hire`. |
| Onboarding | Resets only onboarding progress. It preserves the relationship, stable IDs, agent, Personal HQ, records, and history. A `needs_hq` relationship survives the reset and resumes at the HQ quest rather than offering a second hire or creating another profile. |
| All categories | Equivalent to the four effects above: local PAF records are deleted by Sessions/Agents and restarted onboarding offers a fresh hire. |

A reset response describes filesystem work completed in the current process;
callers must not treat in-memory projections as rehydrated until the required
restart. None of these options changes external accounts, grants new tools, or
deletes external-provider data.

## Compatibility

No legacy onboarding cohort is maintained. Existing Personal HQ, Daily Brief,
Follow-Up, workspace, and protected system-assistant records remain valid, while
a complete development profile reset intentionally returns to a fresh hire.
