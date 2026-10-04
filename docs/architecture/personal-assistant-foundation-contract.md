# Personal Assistant Foundation Contract v1

Status: accepted for implementation

Amendment 1 — Guided Personal HQ Map quest: hiring no longer creates Personal
HQ. This amendment supersedes the automatic-HQ requirements of the historical
PAF PRD (`tasks/prd-personal-assistant-foundation.md`, FR19–FR24) and the
automatic-HQ hire sequence previously recorded in this contract. The rest of the
PAF contract is unchanged. The superseded decision is recorded, not erased: an
installation hired before this amendment keeps its existing active relationship
and never replays the quest.

Amendment 2 — Follow-up ownership and HQ projection: a Follow-Up's
`workspace_id` is its canonical operational owner, while its source fields
record provenance rather than ownership. Personal HQ continues to own direct
first-assignment follow-ups. The built-in Email Ops workspace owns follow-ups
created or captured for Email Ops. Today and Daily Brief may show both as
read-only projections under the saved workspace scope; this never moves,
clones, re-keys, or expands the lifecycle authority over either record.

Amendment 3 — Reviewed portable workspace import: the
[workspace continuity contract](workspace-continuity-contract.md) defines a new,
explicit import/adoption boundary, distinct from orphan recovery and hiring.
One confirmed eligible HQ import may restore its exact workspace-local assistant,
agreement and supported history without a second Hire/Build/Reconnect. Another
local assistant (including a pending hire/HQ setup) or designation is never
replaced. Workspace-only imports retain inert incoming evidence and readable
history without becoming the local personal assistant. Credentials and execution
grants do not travel, and imported automation requires separate local activation.
This is an implementation contract: characterization tests of legacy imports
must not be described as completed portable restoration.

Confirmed legacy import may accept independently verified identity without Daily
Brief configuration. It reports that component as Not configured and missing
agreement fields as Unknown; defaults are new choices, not recovered values.
This exception does not relax `RecoveryCoordinator.Inspect` or authorize startup
repair. Imported profile access and rename must resolve the exact workspace and
stable entry instance, never a same-named global roster entry. The global-profile
lookup and rename descriptions below remain the native-hire path, not permission
to promote an imported agent or rename unrelated sessions.

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
| Ori HQ quest walkthrough | `personal-hq-quest.js` over `ori-spotlight.js` (briefing, spotlight, callout; `ori-guide.js` presentation as the fallback) and the Home cockpit view seam | Deterministic, model-free, focus-only. Observational: removing it leaves the Map and build flow fully functional. |
| Personal HQ identity | session/workspace agent-instance model | `Workspace.EntryAgent` selects the entry profile by name today; `AgentInstance.ID` is the stable workspace attachment that PAF binds. |
| Daily Brief | `dailybrief.Service`, HTTP handler, scheduler, and Home renderer | Durable user/HQ-scoped config and revisions. First-open/scheduled claims enforce existing deduplication. |
| Tickets | `workspace.TicketService` | Target-workspace-owned canonical project work; confirmation remains in the caller before creation/execution. |
| Follow-ups | `followup.Service` | User-scoped commitments canonically owned by `FollowUp.WorkspaceID`, with source-key provenance/deduplication and optional project-task links. Direct first-assignment rows remain HQ-owned; authorized Email Ops rows remain Email-Ops-owned when projected in HQ. |
| Profile and memory | `userprofile` and workspace `MemoryStore` | Global preferences are field-allowlisted; workspace facts remain in `MEMORY.md`; both reject secret-like text. |

## State and action matrix

| State | Meaning | Required copy | Allowed next actions |
|---|---|---|---|
| `needs_hire` | No durable relationship | Mission 01, “Meet your assistant”: the Agents page's New Agent preset; name defaults to editable **Assistant** | Hire from the preset (Hire assistant is the confirmation) |
| `hiring` | A confirmed hire is finalizing the assistant profile and relationship | “Finishing your assistant setup” | Retry/resume the same request; inspect bounded failure |
| `needs_hq` | A real assistant profile and relationship exist; no Personal HQ has been built | Chosen name plus “Let’s give <name> a home base” | Start/resume the Ori HQ Map quest (`build_hq`); defer it |
| `provisioning_hq` | A confirmed HQ setup is partially applied and resumable | “Finishing your Personal HQ” | Resume the same HQ request (`resume_hq_setup`); inspect bounded failure |
| `active` | Binding, HQ, and entry-agent instance resolve | Chosen name and “Your personal assistant” | Ask, assign, pause, edit agreement/profile, open HQ |
| `paused` | Relationship exists but proactive/background behavior is paused | “Assistant paused” | Resume, inspect/edit, deterministic manual assignment |
| active with no model | Healthy hire but no chat-capable model resolves | **“Hired — choose a model to chat”** | Choose model; deterministic first assignment and deterministic Daily Brief remain available |
| `repair_needed` | A durable result exists but its known safe continuation failed, HQ/agent linkage is missing, foreign, or invalid, or the relationship row is absent while PAF provenance remains | “Assistant setup needs repair” with no invented identity | Retry deterministic repair, explicitly reconnect one validated orphan, choose replacement explicitly, or remain paused |

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
| Commitments/dependencies | Follow-Ups user-scoped and operationally owned by `FollowUp.WorkspaceID`; direct first-assignment rows use Personal HQ, while Email Ops capture uses the resolved built-in Email Ops workspace. Ticket links and source provenance do not change that owner. |
| Agent runtime/profile configuration | existing global agent store plus stable HQ agent instance |

## Mutation and idempotency contract

| Mutation | Version/idempotency rule |
|---|---|
| Hire preview | Pure bounded normalization; hash normalized payload. No workspace, agent, or state mutation. |
| Hire apply/resume | Requires a request ID. `last_hire_request_id` returns the existing outcome on replay; state transitions use compare-and-swap `state_version`. Creates the owned profile and relationship only. Persisted pre-amendment auto-HQ operations are distinguishable by payload version and resume through their old safe finalization path; they are never abandoned or duplicated. |
| HQ setup apply/resume | Requires the current `state_version` and a stable HQ request ID bound to a normalized payload hash. The client supplies only the bounded HQ form fields — never assistant, profile, or workspace identity. Replay returns the same canonical result; a changed payload under the same request ID, or a stale version, returns `409`. Partial results are durable, bounded, and resumable with a safe repair step code that carries no provider or database text. |
| Missing-relationship recovery | `GET /api/personal-assistant` may project one server-discovered orphan as `repair_needed` without writing it. `POST /api/personal-assistant/repair` accepts only `if_version: 0`; clients cannot select assistant, profile, workspace, or instance IDs. Repair reruns the complete identity proof and inserts exactly one relationship only if no row exists. A complete HQ returns `paused`; a profile-only recovery returns `needs_hq`. Any stale, ambiguous, incomplete, or contradictory evidence fails closed. |
| Recovery fixes | `GET /api/personal-assistant/repair/diagnosis` names the first failed check (`issue`), the evidence, the fixes that are safe for exactly that issue, and a `digest` of the evidence; it never writes. `POST /api/personal-assistant/repair/resolve` (JSON only) takes `fix_id` and `evidence_digest`: evidence that changed since review is `409`, a fix the current diagnosis does not offer is `400`. One fix edits only ownership markers, the HQ designation, or a new schedule-off Daily Brief configuration, never deletes, then reconnects `paused` once the records agree or returns the next diagnosis. |
| Pause/resume | Requires current `state_version`; stale writes return conflict and the current version. |
| Profile/working-agreement edit | Requires current state version; profile and memory fields additionally use their canonical validators. |
| First-assignment preview | Creates one journal row keyed by opaque preview ID and stores normalized payload/hash only. Repeated identical request IDs return that preview. |
| First-assignment apply | Requires preview ID, assignment version, and matching payload hash. One terminal application stores canonical refs; replay returns those refs without recreating records. |
| Daily Brief manual generation | Uses existing Daily Brief claim/revision idempotency; PAF stores no brief body or schedule duplicate. |
| Ticket creation | Uses source `assistant` and assignment-derived source ID; replay resolves the existing target-workspace record. |
| Follow-up capture | Uses the existing source-dedup key derived from user and source ID. First-assignment apply writes to the designated HQ; manual Email Ops capture writes to the resolved Email Ops workspace. Today and Daily Brief perform no capture or lifecycle mutation. |
| Rename | Requires current state version and updates global profile plus bound instance name without changing stable IDs. |

Request IDs, preview IDs, hashes, and canonical refs are bounded opaque values.
No mutation stores credentials, chat transcripts, source contents, or brief
contents in PAF tables.

## Onboarding creation path

The setup sequence is:

```text
hire profile -> needs_hq -> Ori Map quest -> confirmed HQ setup -> active
```

The hire is Mission 01, **Meet your assistant**
(`tasks/prd-meet-your-assistant-mission.md`), not part of first-run onboarding:
the onboarding modal ends after Welcome and Model and never sends a hire. While
the relationship is `needs_hire` or `hiring`, the Agents page's own **New
Agent** panel opens in a personal-assistant preset
(`personal-assistant-hire.js`, rendered by `agents-roster.js`): the name
(default **Assistant**), a suggested orchestrator face, the six focus areas, an
optional mandate, and the boundary line directly above one **Hire assistant**
button. Pressing it is the one explicit confirmation; there is no separate
checkbox. Nothing is sent before it. The name must be non-empty, bounded, plain
text, and secret-safe. The request ID is created once per attempt and kept in
`localStorage` (`ori.personalAssistantHireRequestId`), and a relationship's own
`hire_request_id` wins over it, so a retry replays the same request. Ori's
deterministic walkthrough (`meet-assistant-quest.js`) points at each control and
never acts on one. The Daily Brief rhythm is **not** collected here: it has no
canonical workspace to be written against until HQ exists, so it moves to the
Map's HQ build form.

The walkthrough has six steps and is shown in Ori's own layer
(`ori-spotlight.js`). A single-click step is a spotlight: the page is dimmed and
blocked except for a hole cut over the one control, and Ori's callout says what
to press. Step 1 is on Home, on the Agents nav entry
(`meet-assistant-home-prompt.js`). Step 2 is New Agent on the Agents page. Steps
3 to 6 (the name, the face, the focus, Hire) are Ori's callout beside the form,
pointing at each field, with nothing dimmed. The user presses and types
everything; Not now (or Escape on a spotlight) always leaves, and nothing is
skipped.

First-run onboarding hands over to `/?quest=meet-assistant&briefing=1`
(`MEET_ASSISTANT_BRIEFING_ROUTE`): Ori in the centre of a dimmed, inert Home
with the Mission 01 card, its six steps, its reward and what it unlocks, and
Start mission or Not now. Start mission opens step 1. Every other entry starts
at step 1 directly, `/?quest=meet-assistant` (`MEET_ASSISTANT_QUEST_ROUTE`,
`progression.MeetAssistantActionURL`): Home's mission card, Today's `needs_hire`
banner, Ask Ori's hand-off, and the retired `/?hire=1`, which redirects there.
Pressing Agents from step 1 sets `ori:meet-assistant-guided` in
sessionStorage, so the Agents page carries on in the same layer. Entries already
about the Agents page start at step 2, `/agents?quest=meet-assistant`
(`MEET_ASSISTANT_AGENTS_ROUTE`): `/agents/create`'s pointer, the repair banners,
and Ask Ori's hand-off on `/agents`. A plain `/agents` visit before the hire
also continues at step 2, in Ori's docked panel, and so does any step when there
is no room beside the form (a phone-width sheet). A plain Home visit shows
nothing and makes no request. `/?quest=meet-assistant` on an install that needs
a repair goes to the Agents page; on a hired install it does nothing. A provable
orphan identity (`relationship_recovery`) opens the same panel's reconnect view
with one Reconnect button; `relationship_recovery_blocked` opens the fix view:
what does not match, what Ori found, and the server's safe fixes (one Apply fix
button, the recommended fix preselected), or what to change by hand when no fix
is safe; a partial hire shows one Finish setup button that replays the same
request. A successful hire goes to `/?panel=today`, where the hired assistant proposes
its Personal HQ in a confirm card. The old `/?quest=build-hq` Map briefing is
still available by explicit choice; it is not the default.

One final, confirmed hire then:

1. creates or resolves exactly one owned global agent profile from the canonical
   `personal-ops` entry specification — same orchestrator role, trusted Personal
   Assistant prompt fragment, model defaults, and validated appearance the
   eventual HQ entry agent must use;
2. persists bounded profile provenance binding that profile to the stable
   assistant ID and hire request ID, so a retry can tell its own profile from an
   unrelated name collision;
3. persists the relationship, working agreement, and `HiredAt`; and
4. transitions to `needs_hq`. Ordinary onboarding was already complete: it ends
   after Model, before any hire.

The hire creates **no** workspace, no Personal HQ designation, no Journal or
other support profile, no workspace membership, no Daily Brief configuration,
and no tool/skill/MCP/Vault/filesystem change.

After completion, the client opens Today on the HQ confirm card. HQ Build is
retired from the mission board but its completion event remains persisted.
Plan my first day is a branch of Mission 03, and `?quest=plan-first-day`
still does not open while `needs_hq`.

Confirming the card's Build or the Map's alternate Build My HQ form is the HQ
creation boundary. That one
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

The default post-hire path is now the HQ confirm card in Today's assistant panel.
It shows the proposed plan and waits for the user's **Build** confirmation before
creating anything. The Map walkthrough at `/?quest=build-hq` remains an alternate
path for people who prefer the guided site and the full HQ form. Both paths use
the same server-owned HQ setup consequence; neither starts automatically just
because Home was visited after a hire.

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

The explicitly opened Map walkthrough uses Ori's own layer
(`ori-spotlight.js`) and its three registered steps. The
`ori:assistant-just-hired` flag now supplies the HQ card's Mission 01 eyebrow;
it does not auto-start this walkthrough. `/?quest=build-hq` starts at step 1;
without room beside a dialog (a phone width), the steps are shown in Ori's
docked panel.

1. `/?quest=build-hq` opens Map view and dims it around the existing reserved
   Personal HQ site (a spotlight: only the site can be clicked). It does **not**
   preselect it — selecting the highlighted landmark is the first user
   interaction.
2. A real user selection opens the existing context dialog, which dims the page
   itself. Ori's callout stands beside it, level with **Build My HQ**, which Ori
   marks. The dialog's own Do this later and close are the ways out.
3. A real user activation opens the existing HQ form, which owns all editing and
   confirmation. Ori's callout stands beside it, marks nothing in it, and offers
   no way out beside the form's own Cancel. It only explains that nothing is
   created until confirmation.

### Defer and resume

**Do this later** is always allowed: on the briefing, on step 1's callout, and in
the site's dialog. Each explicitly invokes the existing optional HQ quest's skip
path, once, ends the walkthrough, clears coachmarks and quest query state, and
leaves **Build My HQ / Resume quest** prominent on Home. Escape on the briefing or
the step 1 spotlight is Do this later. Closing a dialog (or, in the panel
fallback, Ori's panel) only pauses presentation; it does not skip or complete the
server-side quest. Abandoning the walkthrough, reloading, or disabling
JavaScript leaves a resumable server-owned state, never an assumed completion.

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
marker is read or persisted: an installation without a relationship and without
PAF provenance enters `needs_hire`. Durable PAF provenance is not a cohort
marker: if it survives without its relationship row, the bounded recovery path
runs instead of offering a duplicate hire. No parallel legacy adoption wizard
is maintained.

## Starter missions

Source: `tasks/prd-starter-missions.md` and
`tasks/prd-meet-your-assistant-mission.md`. The personal-assistant graph
(`progression.PersonalAssistantGraph`) opens with a Tier 1 named **Starter**:
four featured missions. Mission 01 hires the assistant and is the only required
one. Missions 02 to 04 are optional, each ends with Ori visibly doing something,
and each carries `LockedUntil: pa-meet-assistant`: until Mission 01 is complete
the status view marks them `locked`, with `locked_reason` "Meet your assistant
first", and the widget renders them with a lock and no Start, Skip, or Resume.
Locking is presentation only: `Match`, `Complete`, and backfill still run for a
locked quest. Build My HQ (`t2-build-hq`), the older Tier 2 steps, and the built-in
Tier 3–6 steps are presentation-retired, not deleted. Their IDs still match
and their completions survive backfill and Reset, but none appears in tiers,
mission counts or the next mission, and none pays Craft. The four visible
missions each pay 7 Craft, enough for the first Farm. `t1-plan-first-day`
and `t2-create-workspace` are not in this graph; their persisted completions
stay in place, and a `t1-plan-first-day` completion counts as evidence for
Mission 03.

| Order | ID | Card | Completes when |
| --- | --- | --- | --- |
| 01 | `pa-meet-assistant` | Meet your assistant, `/?quest=meet-assistant` | the request that makes a hire durable (`HireResult.NewlyHired`), or a repair that leaves the relationship hired; never a replay |
| 02 | `pa-show-folder` | Show your assistant a folder, `/?quest=show-folder`; while an offer is pending, the offer itself renders on the card with its own buttons | the folder offer's outcome — a workspace linked to the shown folder or a tidy prepared for it (`FolderDigestService.SetOnOutcome`), a `workspace.created` whose `entry_point` is `folder_digest`, or a `file-janitor` (or retired `downloads-janitor`) workspace's setup wizard first reaching ready |
| 03 | `pa-connect-source` | resolved from the hire's focus areas (below) | any branch's signal, not only the one offered |
| 04 | `pa-first-brief` | Read your first Daily Brief, `/`; while open and with no model configured, adds "Add a model in Settings to generate one." | Today is first served with a Daily Brief revision for an active or paused relationship |

Mission 01's completion is `personalassistanthttp.Handler.SetOnHired`, bound in
`completeProgressionWiring`. Its evidence (`Snapshot.AssistantHired`) is the
persisted relationship status owning a hired profile (`awaiting_hq`,
`provisioning_hq`, `active`, `paused`), not the read projection, so an active
assistant with a broken HQ link is still hired. Because Mission 01 gates every
other mission and a hire cannot be repeated, startup also completes it from that
evidence after the backfill (for example after a quest reset). The economy pays
at most once per quest.

Before the hire, Home is quiet: the card shows Mission 01, and Today says only
"Meet your assistant to start Today." Ori's briefing appears once, when
onboarding hands over; after that, the card's Start (or Today's link) dims Home
around the Agents nav entry at "Step 1 of 6 · Click Agents"
(`meet-assistant-home-prompt.js`). The navbar wraps rather than collapses, so
the Agents entry can be lit at every width.

Mission 03 branches, in priority order when several focus areas match:

| Branch | Focus area | Card | Completion signal |
| --- | --- | --- | --- |
| email | `help_with_email` | Set up email, the host `email_ops_setup` quest; "In progress · Resume" once started | the journey's first ready (`setupjourney.Service.SetOnFirstReady`) |
| calendar | `prepare_for_meetings` | Connect your calendar, `/?create=1&blueprint=calendar-ops` | `workspace.updated` with `mcp_binding_created` on a `calendar-ops` workspace that has a ready calendar binding |
| project | `keep_projects_moving` | the plan's card (starting a project workspace is what Mission 02 does now, so this branch no longer offers one) | `workspace.created` from the creator whose `template_id` is blank or not `personal-ops`, `file-janitor`, `downloads-janitor`, `email-ops`, or `calendar-ops`, and which is not a group |
| plan | anything else, or none | Plan my first day, `/?quest=plan-first-day` | a successful first-assignment apply |

The calendar branch promises "So your brief can prepare you for today's
meetings", and since Issue #533 that is what connecting delivers. Today's
**Meetings** section and the Daily Brief's **Today's Meetings** section both
read the connected calendar through one bounded Calendar Ops read
(`calendarhttp.Handler.TodayAgenda`, resolved by the same FR49 resolver as the
Home portal). The read is read-only and uses the Calendar Ops workspace's
existing connection: no new scope, no calendar write. Today bounds it to 5
seconds and brief generation to 8 seconds (the email source's bound). Each
meeting carries only its time, a truncated title and location, overlap and
back-to-back flags, and its meeting-prep status. It never carries a description,
attendees, or links, and a private meeting has no title or location. A meeting
row opens that meeting's drawer in the Calendar console, where "Prepare me"
lives.

| State | Today | Daily Brief |
| --- | --- | --- |
| Not connected | "Connect your calendar" nudge, only for hires with `prepare_for_meetings`; otherwise absent. Never makes Today partial | No section, no gap |
| Calendar Ops workspace, connection not ready | "Finish Calendar Ops setup", routed to that workspace. Not partial | Gap: "the calendar connection needs attention, so today's meetings are not listed" |
| Connected, meetings today | Meetings with Overlaps / Back-to-back / Prepare / Preparing… / Prep ready | Today's Meetings section; overlaps rank with waiting-for-choice items in Needs Attention, meetings without a prep note fill spare slots |
| Connected, nothing today | "Nothing scheduled today." | "No meetings today." |
| Read failed or timed out | Named as unavailable; Today is partial | Gap: "today's meetings could not be read"; every other section intact |

Some selected calendars failing is partial on both: the readable meetings stay,
and the brief adds the gap "some calendars could not be read".
`internal/dailybrief/calendar_ops_boundary_test.go` pins the brief's side of
this bound and records that FR54 ("Calendar Ops must never add a live call to
Daily Brief generation") was reversed deliberately.

How the card works:

- `GET /api/progression` returns `missions`, the featured quests in order,
  resolved per user by each quest's `Resolve` from a server-supplied
  `MissionContext`. The widget shows the first mission that is neither
  completed nor skipped and lists the others beneath it. It holds no quest IDs.
- Every completion is observed on the server. The browser never claims one.
- Mission 02's start (`show-folder-quest.js`) opens the assistant panel on
  Today with the folder chooser unfolded and scrubs `?quest=show-folder`
  without a history entry. It makes no request of its own and never completes
  anything. A pending offer also renders on the mission card
  (`progression-widget.js`, matched on the action URL
  `personal-assistant-folder.js` exports, so the widget still holds no quest
  IDs); its buttons run the chooser's own actions through
  `window.PersonalAssistantFolder.act`.
- Hooks that need the assistant, the setup journey, or Today are installed in
  `completeProgressionWiring`, after the Daily Brief phase. Wiring them with
  progression itself bound nil on a real server.

Grandfathering: the one-time backfill reads each mission's evidence (a ready
File Janitor, a connected source, a project workspace, a completed first
assignment or legacy first day, an existing brief). An install whose backfill
predates the starter missions gets one silent pass, recorded under the
`starter-missions-v1` key in `ProgressionState.Reconciled`. It pays no Craft
and survives a reset, so a reset stays a blank slate. Mission 01 has its own
pass, `meet-assistant-v1`, with the same rules: an install that hired before the
mission existed sees it complete, with no toast and no Craft. Mission 02 has
`show-folder-v1`: a persisted `pa-tidy-downloads` completion, a ready File
Janitor, or an active workspace whose primary project directory is a folder
outside the workspace's own folder (a linked folder, not a blueprint scaffold)
marks Show your assistant a folder complete. A Tidy that was only skipped leaves
it open. `pa-tidy-downloads` itself stays out of the graph; its ID and action
URL remain as constants so persisted state that names it still loads.

## Show me a folder

`tasks/prd-show-me-a-folder.md`. The assistant's first proactive act: the user
points it at a folder, the server looks at the folder's shape, and the
assistant makes one explained offer.

- **Entry.** Today's folder scene and chooser (`personal-assistant-today.tmpl`,
  `#personalAssistantFolder`) are already expanded for an `active` or `paused`
  relationship, even after a scan returns an offer or the panel is reopened.
  There is no second inline launch button. Home's primary **Explore a folder**
  header action opens the panel and focuses the chooser; Mission 02's Start
  and the mission card still reach the same flow. Before HQ, Home opens its
  confirm card instead.
- **Choosing.** Chips name Downloads, Documents, and Desktop under the server's
  home; "Pick another folder…" runs the native picker on the server
  (`platform.ChooseFolder`, osascript). The browser never sends a filesystem
  path: `POST /api/personal-assistant/folder-digest/scan` accepts `{chip}` or
  `{picker: true}` and answers 400 to any `path`/`folder` key.
- **Presentation.** The scene, chooser, and resulting offer share one card in
  Needs you, ahead of the compact queue of other requests. A small visual field
  trip beside the chooser uses the assistant's already-rendered avatar. A folder graphic starts moving
  only when a real scan begins (and finishes the motion on a fast response);
  the real server offer produces the finding badges,
  and a failed scan produces an error, never a made-up result. The explanation
  is one disclosure away, the server-observed receipt rows remain visible, and
  `prefers-reduced-motion` keeps the same states without animation. The visual
  metaphor moves no files and sends no client filesystem paths.
- **Scan.** `internal/folderdigest` reads names, dates, sizes, and kinds with
  `os.ReadDir`/`Lstat`; it never opens a file. Depth 3, 5,000 entries, 3 s;
  hidden entries, `node_modules`, `Library`, `.Trash`, iCloud placeholders and
  dataless files are skipped; symlinks are counted, not followed. Verdicts:
  `project`, `dump`, `mixed`, `ambiguous`, `empty`, plus `declined` for a folder
  the user asked not to be asked about. Reason lines carry counts, never
  contents.
- **Offer.** One pending offer per user, persisted in
  `<HQ>/.ori/folder-digest-v1.json` with the knowledge store's discipline
  (no-follow open, lock, temp+fsync+rename, 0600, size cap, owner check).
  Decisions: `no` (tombstone: never asked again about that folder), `later`
  (asked again in a week), `yes` with a `project` or `tidy` choice. Replays
  through `request_id` return the same result.
- **Continuation.** `GET /api/personal-assistant/folder-digest/continuations?home_id=`
  answers, for a Home reopened without its original query string, which
  collection the owner chose for that exact Home: opaque `offer_id`, folder
  name, `state` (`ready` or `needs_pick`) and, when a pick is needed, a
  `reason` (`expired`, `lost` after a restart, or `changed` directory). It is a
  pure read of the owner's own offers (newest first, at most five, within a day
  of the Home's creation), accepts only `home_id`, returns no path, and grants
  nothing: the library's `roots/pick-offer`, root review and commit re-verify the
  Home, provider and folder identity before any consequence. See
  `music-setup-onboarding.md`.
- **Project.** The card says what the scan found ("Thesis looks like a LaTeX
  manuscript." — the marker's label, with a language's manifest ranking above
  `.git`) and asks to confirm the plan ("Set up Thesis as a Writing project
  workspace?"). Shapes map to blueprints: manuscript → Writing Project, corpus
  → Research Project, code → **Code Project** (a built-in with no scaffold and
  no roles), audio → REAPER song; a blueprint that is not installed is named
  in a note and the workspace starts blank. **Set up** (decide with
  `create: true`) has the server create the workspace through the ordinary
  creation pipeline (`sessionhttp.Handler.CreateFolderOfferWorkspace`, an
  in-process `POST /api/workspaces` carrying `entry_point: folder_digest` and
  `folder_offer_id`), link the folder as the primary directory (an outside
  linked directory, not `project_path`, superseding a blueprint scaffold),
  seed the shape's first task, and resolve the offer in the same request; a
  retried click reuses the workspace already made for the offer. **Adjust…**
  opens the Create Workspace modal pre-filled instead, and
  `POST …/offers/{id}/resolve` links the folder once the modal reports the
  workspace; the modal's Cancel leaves the offer pending. A mixed or
  ambiguous offer's "Start with X" / "It's a project" shows the same confirm
  card (with Back) before anything is decided. A resolved project Set up stays
  on Today and lists the server-observed workspace, primary linked folder,
  blueprint, actual agent instances and seeded task as text-only receipt rows,
  then offers **Open <workspace>**. Reuse after an interrupted setup says
  "already set up"; an exact request replay returns the stored receipt.
- **Tidy.** "Tidy it" first shows the plan ("Set up File Janitor for
  Downloads? Ori will create a File Janitor workspace with a File Curator,
  watch the folder while paused, scan it once and propose moves — nothing
  moves until you approve a batch." → Set up / Adjust… / Back). **Set up**
  has the server drive the File Janitor setup coordinator with the offer's
  folder (accept, folder intent, grant, first review; an invalidated earlier
  run is started over from, and a run still waiting for its folder is
  resumed); the decide response resolves the offer with the review batch's
  route, and the browser then shows the setup as it happened — the
  assistant-led setup card in the Today panel with its receipts and
  walkthrough, ending on the review — instead of jumping to the batch. A
  folder another File Janitor already manages opens that workspace instead
  (`outcome.existing`). A workspace that cannot be opened is a failed tidy
  (503) and the offer stays pending. **Adjust…** opens the ordinary File
  Janitor creator, whose own setup wizard asks for the folder.
- **Tools.** `workspace_directory_list` and `workspace_directory_read` (in
  `chathttp`) list and read files under a workspace's linked directories,
  symlink-safe, read-only, parsing PDF/DOCX/text through `internal/fileparser`
  in 40,000-character pages; content is labelled as data, not instructions.
- **Learning.** A resolved project offer proposes a reviewed `projects` fact
  ("You are working on a project in the folder X.") and tool suggestions from
  the marker and dominant extension under the `folder_scan` source; the
  authority re-validates against the offer's key, the workspace's primary
  directory, and the marker on disk, and a hidden or moved folder turns the
  fact into "Needs review".
- **First prompt.** After HQ becomes active, Today's chooser expands once per
  relationship with "Now show me a folder you're working in." The
  `POST /api/personal-assistant/folder-digest/prompted` receipt persists this
  presentation server-side; Reset Getting Started clears it. No chooser opens
  before HQ exists; further visits keep the chooser open without repeating the
  one-time hand-over line.
- **Mission.** See Mission 02 above. Every completion is server-observed.

Today's Done section also gains one `janitor_result` line per File Janitor
workspace with applied, not-undone actions in the last 24 hours ("Filed N files
into <folder>/Filed", "M sent to Trash · Undo from History"), linking to
`/workspaces/<slug>?panel=file-janitor&tab=history`. Resolved folder project,
tidy and HQ setup receipts remain in Done for seven days. The completed HQ
confirm card does not compete with the next action: its canonical Done item
has a collapsed setup receipt with the workspace, schedule, and (when observed
for the current build) directory. On reload the current workspace and schedule
are read from the server; the previous setup directory is not inferred from a
possibly changed root. Both honor the results cap; a failed receipt read does
not erase healthy results.

## Surfaces and routing

Home, Ask Ori, and Personal HQ resolve the same binding. If it is healthy they
show the chosen assistant name and route work through the existing
`/api/home-assistant/route` and execution pipeline. Ori Guide remains a
structurally read-only deterministic guide and may escalate to that same route;
it is not a peer assistant.

Home is Map-first: the Workspace Map/Tree occupies the available cockpit
viewport without an always-visible Today row. Its primary header action is
**Explore a folder** after hire; before HQ it opens the HQ confirm card, and
after HQ it opens the existing chooser. **New Workspace** stays a secondary
header action and retains its full modal and Map create pad. The empty Map and
launcher link to the chooser instead of instructing the user to create a
workspace by hand. Today remains a Home-owned projection and is available on
demand through the existing launcher and panel for that same bound Personal
Assistant. Today's only three named sections are **Needs you**, **Working on**,
and **Done**, in that display order. Needs you leads with the next action and
collapses other requests under **Also needs you**; the server's records and
ordering are unchanged. The Today heading leaves space for the assistant name
and a short local check-in time. Its **More** menu preserves the distinct HQ,
working agreement, workspace memory, remembered-facts review, optional
interview, and Agents destinations rather than showing a wall of header links. Empty sections disappear, unhealthy sources are named
once in a retryable footer, and machine reason/status identifiers are humanized.
The old Decisions/Priorities/Remembered/FollowUps/Results JSON fields remain
available for one release but no longer render as sections. Direct launcher
activation on Home opens Today, while prefilled handoffs open the Ask composer. Other authenticated
surfaces keep the existing Ask-only launcher behavior. This presentation change
adds no Personal Assistant page or route and does not change identity,
ownership, routing, confirmation, persistence, authorization, or API boundaries.

Read surfaces degrade safely when the binding, workspace, or agent is missing:
they return a bounded unavailable/repair state, never a fabricated identity.
The personal-assistant service is a required part of the canonical server build.

## Assistant conversations

An everyday request to the hired assistant — write this, make it warmer, give it
to me in Korean — is a conversation, not a handoff. It needs no new agent,
project workspace, connection, or setup mission.

### Which requests are conversations

`/api/home-assistant/route` returns intent `assistant_conversation` with route
mode `home_inline` only when all of these hold:

- the relationship is `active` or `paused`;
- the request carries no workspace context (a workspace page, or a workspace
  selected on the Home Map, keeps its existing workspace route);
- the request is not a complex project build that recommends a workspace;
- the request is either a general request with no more specific intent, or a
  composition request (it opens with a writing verb such as write, draft,
  rewrite, translate, or a follow-up such as “make it …”); and
- no specialist agent matched it. The protected system assistant and the hired
  profile itself are not specialists for this rule.

Everything else keeps its route: utility lookups, app activity and navigation,
workspace creation, and email, calendar, or app-launch requests that a
specialist can take. A composition request never forces the user to create an
agent or a connection, so "write an email to my landlord" is drafted in the
conversation when no email specialist exists and goes to that specialist when
one does. The response never names the protected system assistant as the
handler.

### Identity and scope

A conversation is one canonical `Session` in the designated Personal HQ:
`Session.FolderID` is the HQ workspace ID and `Session.AgentName` is the hired
profile's agent-store key. There is no second transcript store.

The server derives both values from the relationship on every request. The
browser supplies one thing: an opaque conversation ID, or none.

| Request | Result |
|---|---|
| No conversation ID | A new conversation. The session is created when the first turn is stored, so a failed first turn leaves nothing behind. |
| A conversation ID in scope | That conversation continues. |
| An ID that no longer exists | Refused as `conversation_not_found`. Nothing is recreated. |
| An ID in another workspace, bound to another agent, or left behind by a replaced assistant or a changed HQ | Refused as `conversation_out_of_scope`. |
| The session store cannot be read | Refused as `conversation_unavailable`. |

A refusal calls no model, stores nothing, and offers **New conversation**; the
user's text stays in the composer. The server never adopts the tab's active
session, the route context's `session_id`, an `X-Session-ID` header, the most
recent session, or a session found by title or agent display name. A rename
keeps conversations in scope because the rename already carries session
bindings to the new profile name.

Reads are validated the same way. `GET /api/home-assistant/conversations` lists
the conversations in scope and `GET /api/home-assistant/conversations/{id}`
returns one, both by canonical session and message ID. Ori Help has neither
route nor the store behind them.

### Continuation and new conversation

Each tab holds its own current conversation ID, so two tabs never share a
thread by accident. **New conversation** clears the tab's ID and any pending
confirmation. **Continue** picks a conversation from the validated list. Every
stored turn returns its canonical message IDs, and the rendered messages carry
them, so a later action can name the exact message it means.

A turn is stored only after it is answered: the user's message, then the
assistant's reply. If the reply cannot be stored, the answer is still shown and
marked as not saved; Ori does not claim history it does not have.

A conversation stores answered turns and nothing else. A request for an action
("save this draft", "remember that…", "create a workspace…"), its review or
confirmation prompt, and its outcome are shown in the panel but are not written
into the conversation. Every assistant message in a conversation is therefore a
reply the user can point at, and no stored text ever stands in for an approval.

### History is not memory

| Kind | Where it lives | Who reads it |
|---|---|---|
| Unsent input | The composer only | No one until Send |
| Conversation history | The session's messages, until the user deletes the session with the existing session controls | That one conversation |
| Long-term memory | Reviewed Personal HQ memory and the global Profile | Every eligible assistant turn |

Conversation history is never copied into memory, Today, Daily Brief, another
conversation, or another agent's prompt. Deleting a conversation removes its
history and nothing else: a saved Ticket or a remembered fact has its own owner
and stays. Translating one message does not change the profile language.

### Context bounds

A turn's prompt holds the system prompt, a window of the current conversation,
the current request, and the existing eligible personal context (working
agreement, Profile, reviewed HQ memory). The window is the most recent 40
messages within 24,000 characters; one message contributes at most 6,000.
Older turns stay stored and are left out of the prompt, and the response says
so. Only user and assistant messages are replayed. A stored system-role message
is never replayed, and an imported message is replayed as quoted history, not
as a turn.

No other workspace's transcript is injected. The read-only `home_*` tools
return counts and titles, not message text.

### No authority from a transcript

Nothing in a conversation can authorize a write — not the user's earlier
messages, the assistant's replies, imported history, or an old confirmation.
Every write is prepared and confirmed in the current request against the
current relationship, and executed only by the server's confirmed-action or
reviewed-save path. A pending confirmation or review is never stored as an
executable record and does not follow the user into another conversation. The
model has read-only tools in a conversation; it cannot save, remember,
schedule, send, or run anything.

### Model

A conversation uses the configured system model through the existing provider
factory — the same model the relationship's `model` availability reports. When
none resolves, the turn returns the existing "choose a model" state, stores
nothing, and keeps the user's text. Ori does not switch to another provider or
to the agent profile's own settings to produce an answer.

A paused relationship still answers a direct request and keeps every
confirmation gate; it starts no routine and no background run.

## Saving a draft to the HQ backlog

**Save to HQ backlog** keeps one reply from a conversation as one Backlog Ticket
in Personal HQ. It is a reviewed, two-step action. Neither step calls a model,
and nothing a model, a transcript, or the draft's own text says can start,
approve, or redirect it.

### Review

`POST /api/home-assistant/drafts/review` takes a conversation ID and a message
ID and writes nothing. The server:

- resolves the relationship and its conversation scope, as for any turn;
- reads the chosen message from the canonical session — the browser never
  supplies the text — and accepts only an assistant reply in that conversation;
- resolves the designated Personal HQ by its stable ID, never by name; and
- returns the review: the exact message text, a suggested single-line title,
  the HQ's name and ID, the placement ("Backlog · unassigned · not
  scheduled"), the canonical Ticket limits, and one **operation ID**.

The only normalization is the canonical Ticket validators' removal of
surrounding blank space, and the review says so when it applies. Text that
exceeds a Ticket limit is shown whole with a note; it is never cut.

The typed request ("save this draft and put it in my todo list") opens the same
review for the latest reply in the conversation. It asks instead of guessing
when the request names another version, when there is nothing to save, or when
it names a workspace other than Personal HQ — a conversation draft is not
redirected into a project, and the words of the request are never captured as
the item.

### Save

`POST /api/home-assistant/drafts/save` commits the title and draft in the form —
the user's final, attested text — under the review's operation ID:

1. The relationship and the HQ are resolved again. A target that is no longer
   the designated HQ is refused as `target_changed`.
2. An earlier attempt of this same save is looked up. If it exists, its Ticket
   is returned and nothing is written.
3. Otherwise the source message is validated again and one Ticket is created
   through the canonical Ticket service: Backlog, source `assistant`, title and
   description as reviewed. It has no assignee, no schedule, no due date, and
   is not runnable. No Note, memory fact, agent, or workspace is created.

### One save, one Ticket

The Ticket itself is the durable record of the save. Its immutable source key is

```text
assistant-draft:<conversation id>:<message id>:<operation id>:<payload digest>
```

where the digest covers the target workspace and the normalized title and body.
The key holds references and a fingerprint, never content. There is no
operation table, no stored review, and no second copy of the draft.

| Attempt | Result |
|---|---|
| Same operation, same content (double-click, retry, retry after restart) | The first Ticket, `created: false` |
| The same, after the Ticket was edited or promoted | The same Ticket as it is now, `changed_since: true`; nothing is overwritten |
| The same, after the source conversation was deleted | The same Ticket; a deleted chat does not turn a completed save into "not saved" |
| Same operation, different content, target, or source | Refused as `operation_conflict`, naming the Ticket already saved |
| A new review of the same message | A new operation and a new Ticket |
| The Ticket was deleted, then the save is retried | A new Ticket: a deleted Ticket leaves no evidence, and retrying is a new save |

The replay lookup and the insert share the workspace's atomic update, so
concurrent attempts settle on exactly one Ticket. The browser sends the same
operation ID on every attempt of a review and never mints another to get past a
failure.

### Outcomes the user is told

- **Saved**: read back from the Ticket — its number, state, owner workspace,
  and a link (`/workspaces/<slug>?ticket=<id>`). "Unassigned and not scheduled"
  is stated only when the Ticket shows it.
- **Not saved**: a validation or refusal response. The form keeps the user's
  text.
- **Unknown**: no response arrived. Ori says it could not confirm the save and
  offers the same save again; it does not claim success or failure.

Ori cannot deliver reminders. A save request that also asks for one opens the
review with that limitation stated and saves without a reminder; a reminder
request alone gets the limitation and no promise.

### Resuming a saved draft

A saved draft is found by its canonical Ticket ID. There is no draft catalog,
no title search, and no "most recent" guess. The link between a Ticket and its
conversation is the Ticket's own source key, read and re-validated every time.

| Starting from | What happens |
|---|---|
| The conversation (**Continue…**, or a reload in the same tab) | `GET /api/home-assistant/conversations/{id}` also returns `saved`: the Tickets saved from that conversation, each with its message ID, state, version, and link. The reply it came from is labelled "Saved as #n". |
| The Ticket in Personal HQ | The Ticket detail offers **Continue this draft with your assistant** (`/?assistant_draft=<ticket id>`). `GET /api/home-assistant/drafts/{ticketID}` returns the Ticket as it is now and whether its conversation can still be opened. |

Opening either one only reads. It calls no model, runs no task, and changes no
Ticket.

The saved copy and the conversation are separate sources and neither is changed
to match the other:

- `matches_source: false` — the saved text is no longer the reply it was saved
  from (edited in Personal HQ, or updated since). The label says so.
- `newer_replies` — replies after the saved one. They are not part of the saved
  item until the user updates it.

While a tab is working on a saved draft, each turn names it by Ticket ID. The
server reads its current title and text for that turn and gives them to the
model as bounded, untrusted reference data, so "make my saved draft shorter"
works from what is stored now. A turn never writes the Ticket. A draft that can
no longer be read is reported and the turn runs without it.

If the source conversation was deleted, the Ticket still opens. Ori says the
conversation is gone and offers to start a new one about the saved draft; the
user chooses. The deleted conversation is never recreated, and deleting a
conversation never deletes a saved Ticket or a remembered fact.

### Updating a saved draft

**Update saved draft** is a second reviewed action on the same Ticket.

- `POST /api/home-assistant/drafts/{ticketID}/review` writes nothing. It
  returns the text saved now, its version, and the proposal: the chosen reply,
  with the current title kept unless the user changes it.
- `POST /api/home-assistant/drafts/{ticketID}/update` applies the reviewed
  title and text through `TicketService.Update` with the reviewed version. A
  version is required; there is no unversioned update.

It changes the title and the text. It cannot change state, owner, assignment,
due date, schedule, or provenance, and it never creates a Ticket.

| Situation | Result |
|---|---|
| The Ticket was edited, promoted, or otherwise changed since the review | `saved_draft_changed` with the current Ticket. Nothing is overwritten; the user's proposal stays in the form and is reviewed against the current version. |
| The same update is sent again after it landed (a lost response) | Recognized from the Ticket itself — exactly the reviewed text at exactly the next version — and reported as already applied. Nothing is written twice. |
| The Ticket was deleted | `saved_draft_not_found`. It is not restored, and the update does not become a new save. |
| Work has started or the Ticket is closed (In Progress, Review, Done, Cancelled) | `saved_draft_not_editable`. It is edited in Personal HQ. Backlog and Ready drafts can be updated. |
| Personal HQ was replaced | The old HQ's Ticket is not reachable through the new one. |
| The Ticket is not a saved draft | Not found: this path cannot read or edit ordinary Tickets. |

## Remembering a fact from a conversation

**Remember…** is a third action, separate from the conversation and from
**Save to HQ backlog**. A draft is kept as a backlog item; a fact is one line
the assistant may use again later. Neither one does the other's job, and
neither sets a reminder.

It adds no store, route, or rule of its own. The save is the existing explicit
reviewed-memory action described under
[Reviewed Personal HQ memory](#reviewed-personal-hq-memory-532):
`POST /api/personal-assistant/knowledge/explicit` with the current assistant
state version, one retry key per action, an existing category, and the exact
wording. The same line is then edited or forgotten on the remembered-facts page
(`/profile#personalHQKnowledge`).

### Opening the review

The review opens from **Remember…** on any stored message, or from a typed
request in the conversation ("remember this", "remember that …"). Opening it
is local: nothing is requested from a model, written, or queued as a
suggestion, and a typed request is not stored as a conversation turn.

What the review starts with:

| Chosen context | Starting text |
|---|---|
| "remember that _statement_" | The statement, editable. |
| A short statement the user wrote themselves | That statement, editable. |
| A reply the assistant wrote (a greeting, a draft, an example) | Empty. A generated reply is never offered as a fact. |
| The user's request or question ("write…", "translate…", "when is…?") | Empty. |
| "remember this" with nothing stated | Empty: the user says what to remember. |

A date-bound subject with no date in the chosen message (a birthday, an
anniversary) gets a hint that the date is missing. Ori never supplies one: a
date the model used in an example is not a fact, and a birthday the user did not
give stays unknown. Translating a draft is not a language preference; only an
explicit preference statement reaches the existing global-profile confirmation,
which is unchanged.

### Saving

The review shows the exact wording, the destination (Personal HQ memory), the
kind of fact, and the UTF-8 byte count against the existing 500-byte limit.
Choosing **Remember this fact** is the user's approval of that wording; nothing
else is. Text over the limit, blank, multi-line, padded, or secret-like is
refused whole and stays in the form — it is never cut to fit.

| Situation | Result |
|---|---|
| Saved and read back | "Remembered in Personal HQ", with a link to review, edit, or forget it. |
| The exact wording is already remembered | Reported as already remembered. Nothing is added. |
| The response was lost, or the save could not be confirmed | The canonical list is read. If the fact is there it is reported as remembered; otherwise the user is told it could not be confirmed, and the same save — same retry key — can be sent again without being applied twice. |
| The assistant's state changed while the review was open | Nothing is remembered; the wording stays in the form and the next save uses the current state. |
| Personal HQ memory will not take it (queue full, needs repair, no HQ) | The reason the memory service gives is shown as given; the wording stays in the form. |

A remembered fact creates no Ticket, follow-up, schedule, or notification, does
not change the global profile, and adds nothing to the conversation. Editing or
forgetting it on the remembered-facts page changes reviewed memory from then
on. It does not edit history: a conversation in which the fact was said still
shows it, and continuing that same conversation still includes its own earlier
turns. History is never turned back into a remembered fact, so forgetting is
not undone by an old transcript, and other conversations do not see it.

A paused assistant follows the same canonical service: the user can still
remember a fact, and — as for all reviewed memory — a paused assistant is not
given those facts as context. Saving never calls a model, so it does not depend
on one being configured.

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
- commitments use Follow-Ups: `user_id` scopes access, `workspace_id` is the
  canonical operational owner, and source fields preserve creation provenance;
- direct first-assignment Follow-Ups remain Personal-HQ-owned, while follow-ups
  captured for the built-in Email Ops path remain Email-Ops-owned;
- Today and Daily Brief may project authorized active/reopened HQ and Email Ops
  rows under the saved selected/all/future-workspace scope, but cannot mutate
  them; lifecycle actions remain on the owning workspace's management surface;
- preferences and identity facts use User Profile fields;
- workspace-specific operational facts use that workspace's `MEMORY.md`;
- Daily Brief uses its existing durable configuration/revision stores; and
- source integrations continue to enforce their existing consent gates.

Project work must not be duplicated into Personal HQ. Follow-ups may link to a
project Ticket but are not moved into it. An HQ projection is likewise not a
second commitment: the original Follow-Up ID, owning workspace, source
provenance, and lifecycle remain canonical. Authorization is deliberately
bounded to the designated HQ plus active, current-user workspaces with built-in
`email-ops` template provenance; this is not a generic specialist framework.
Assistant profile edits must preserve stable IDs and pass existing
free-text/secret validation. Memory writes use
`ValidateMemoryText`, fixed workspace roots, and atomic `0600` persistence.

### Reviewed Personal HQ memory (#532)

The designated HQ's `MEMORY.md` remains the sole current-value owner of
reviewed operational facts; global identity and communication preferences
remain in `userprofile`. A schema-versioned
`<HQ>/.ori/personal-assistant-knowledge.json` sidecar in the **validated HQ
folder** holds proposal state, bounded review-revision text/evidence,
revision hashes, canonical target references, admission counts, suppression
keys and retry/recovery receipts. Reviewed plaintext is retained only for the
lifecycle and scrubbed on Forget; it is not a second active-fact store. Each
read and write verifies the current local user, hired assistant's owned profile,
relationship version/state, designated HQ folder and entry-agent instance.
Paused assistants may review metadata but do not receive these facts in context.
A missing/foreign/corrupt sidecar cannot be treated as approval.

Suggestions are inert until the user approves exact wording; an edit to a
candidate is still only a candidate. Source approval first prepares an inert
sidecar operation, then compares and writes the exact canonical revision, then
verifies and finalizes the approved state. Edit, suspension and Forget use the
same exclusion-before-canonical-mutation ordering. Retries check exact
request/revision receipts and resume an interrupted operation rather than
appending a second fact. Outside changes, ambiguous duplicate lines, stale
profile fields and changed folder/ownership return a conflict or an unavailable
state; Ori never restores a remembered previous value over outside edits.
Reject/Forget leave semantic suppression markers and normalized one-way
text hashes for prior reviewed revisions even after proposal plaintext is
scrubbed. The server-owned generic `memory_write` path holds the HQ lifecycle
lock while it checks current review revisions and hashes **and** appends the
canonical operational line; Forget cannot interleave its exclusion between
those two actions. It prevents an agent from adding a duplicate candidate or
resurrecting an exactly matching rejected/forgotten line as ordinary memory;
separately confirmed user re-entry stays available. It cannot infer
arbitrary paraphrases, and a concurrent external full-file write remains a
canonical outside edit, not a reviewed approval. The three-new-proposals-per-rolling-24h and six-pending
limits share one sidecar across saved-app and File Janitor sources. Explicitly
confirmed user facts do not consume that proposal quota and can be deliberately
re-entered after Forget; an automatic source observation cannot re-propose a
forgotten meaning. Forget removes an exact Ori-managed canonical line when it
still matches; if a user changed it outside review, the old managed revision is
excluded but the changed text is **not** deleted behind their back. This is not
a whole-file erasure or control of other copies, backups or legacy memory.
The pre-existing workspace-memory overview shows only ordinary legacy entries,
labels their origin as unverified and distinguishes an optional recorded entry
date from an observation time; managed HQ markers (including a prepared
interrupted Forget still on disk) appear only through the binding-validated
review dossier, never as a second active legacy fact. The global profile editor
remains the source of existing identity/preference values; it does not invent
per-field source or observation dates for earlier records. Explicit global
response-style, units and language edits/Forgets on that form can use
`PATCH /api/user/profile` with the current SQL profile version and exact old
field value. New one-field wording must pass `workspace.ValidateMemoryText`
unchanged (one line, 500 UTF-8 bytes, no secrets/control/bidi text); an empty
value explicitly clears the field. It changes only the allowlisted canonical
preference via CAS,
without a new HQ fact, tombstone, owner selector or fabricated approval date;
a different or empty current value excludes the interview's old receipt from Today.
The existing global agent `profile_set` tool remains available, but a
server-owned guard denies an exact completed-interview preference when the
current canonical field no longer matches; this applies even if the tool is
called from another workspace. Its multi-field write uses the guarded SQL
version so a concurrent user Forget conflicts rather than restoring a prior
value. Different new preferences and unrelated About writes remain supported.
An absent/corrupt sidecar is indistinguishable from a lost receipt and makes
agent preference writes fail closed for a hired HQ; unrelated About writes and
explicit user profile editing remain available. Before hire/HQ setup, legacy `profile_set` behavior remains.
Legacy unversioned profile `PUT` remains available to explicit clients; neither
this guard nor an old hash proves a *new* review. SQL-owned per-field generations
ensure even an identical user re-entry after a clear cannot inherit the old
interview confirmation or its date.
The pre-existing whole-profile `PUT` remains available for legacy unversioned
callers. The current editor now includes its last-read `updated_at`, so that
form uses an atomic full-row version check instead of bypassing a stale
single-field conflict; it leaves unsaved input intact on HTTP 409. Older
clients that omit the version retain their original `PUT` behavior.
Manual full-file and existing Memory-tab editing remain available; there is no
new full-file editor. Generic memory reads/prompts omit managed marker lines;
only the server-owned reviewed reader can put a current, canonical, eligible
revision into authorized hired Home and bound HQ chat/task contexts. Native CLI
runs without a verified hired principal continue to receive **no** managed
facts. Other workspaces and Ori Guide never inherit this HQ projection.

Home's Today panel, the hired assistant's global agent page, the current
Personal HQ workspace and its bound entry-agent page link to
`/profile#personalHQKnowledge` and the optional interview.
The workspace/agent links require a current hired, correctly bound and available
HQ/entry identity; a bare portable marker or same-name foreign agent does not
inherit them. The dossier API still validates authorization independently.

Saved-app and Janitor *inferred candidates* can only approve to canonical HQ
`MEMORY.md`; there is deliberately no browser/agent route to approve an
inference into the global profile. Existing allowlisted global preferences are
edited or cleared by the user via the profile form, or saved after the
interview's exact explicit review through one-field SQL CAS. The sidecar's
profile-target Forget recovery primitive is defensive for a previously
recorded target, not a new producer or a source-consent shortcut.

The optional three-question interview is offered only after a successful HQ
activation. Read and defer do not save answers. The user can skip every row or
edit its text, category and destination before an explicit **Save these facts**.
HQ rows cross the existing `MemoryService.Remember` validation/authority
boundary and its reviewed-HQ journal seam; a selected global communication
preference uses a one-field `userprofile` compare-and-swap, with per-row saved
receipts so a partial save can be reviewed and retried after restart without
clobbering later profile changes. For a changed global preference, a bounded
sidecar prepare record stores only the before/after value hashes and the exact
server-chosen SQL version before attempting the field CAS; a retry verifies
both that version and the current value before finalizing a lost receipt.
Identical wording written by an outside editor at a different version is a
conflict, not an interview save. A fresh explicit final review can supersede
an uncommitted prepare record without altering previously saved rows. Unsaved
drafts live only in the browser.
Today reads only currently eligible reviewed priorities/work-style facts and
completed, unchanged global preference receipts. A SQL-owned monotonic
per-field generation (migration 63; no copied values) ties each receipt to the
exact canonical preference: unrelated profile edits preserve its review date,
but clearing/re-entering identical wording through PATCH, legacy PUT or an
outside SQL writer advances the generation and cannot inherit an older
confirmation. Existing receipts lacking this generation fail closed. The date
shown for an eligible preference is the saved row receipt, not the profile's
last-write time for unrelated fields.
It may link one already existing next action. It creates no task, Follow-Up, brief revision, account,
scan or model call. Missing or failed reads appear as empty/partial/unavailable,
never as confirmation of absent work.

Saved-app suggestions read only durable onboarding evidence with its actual
observation time, never a fresh app detection or an automatically persisted
manual scan. File Janitor suggestions require three distinct verified,
user-approved applied moves for one category and one approved root generation
in a currently owned, built-in Janitor workspace inside the HQ's Daily Brief
scope. The adapter checks approved folder/directory/MCP read permissions and
passes fixed-label action IDs/counts/times, not names, paths or contents.
A reviewed context read checks source support twice and then rereads the
sidecar's ledger version: Forget/suspension can exclude a fact between its
first sidecar snapshot and the final source check while the old canonical line
is still present. A changed ledger version omits that in-flight section rather
than returning a fact withdrawn mid-read.
A current, explicit failed required folder/MCP read check makes the dossier
source card `revoked` (missing or revoked access); an unknown root, malformed
status, failed journal read or changed ownership/scope remains `unavailable`,
not an authorized empty source. Neither state permits a suggestion or prompt.
Email/Calendar source cards display bounded server-owned capability CanRead,
CanPropose-in-existing-workflows and confirmation copy as inert text. A
connection setup nudge appears only while not configured or revoked; a ready
Calendar connection yields an honest empty routine section rather than a
connect prompt. Neither card claims reviewed-memory learning from #533/#534.
A successful **`UndoDone`** removes supporting evidence; `UndoneAt` alone does
not, because a failed undo may set that timestamp. Durable apply/undo
notifications are best-effort; a fresh evidence read must exclude stale support
even if the notification failed. A verified contradiction suspends the
canonical fact into **Needs review** before reuse. Reconfirmation requires an
exact current revision and fresh acknowledged three-action checkpoint; a
previously reviewed undo cannot suspend that checkpoint again, while a later
successful contradiction can. Changed scope/root or missing/pruned evidence
never proves a reversal and cannot silently authorize an old source fact.

Public review routes under `/api/personal-assistant/knowledge` use current-user
resolution, bounded JSON, item IDs, state/item versions and retry keys. `GET`
reads the ledger (no candidate generation); POST-only bodyless `check-saved-apps`
and `check-janitor` read authorized existing evidence to propose. An explicit
fact save, item approve/reject/edit/forget/reconfirm, and interview read/defer/
save are separate actions. An interrupted prepared Forget has a bodyless
`POST .../knowledge/{item_id}/resume-forget`: the server reuses its stored retry
key and exact canonical target, and refuses if that target changed. A separate
bodyless `POST .../knowledge/{item_id}/resume-operation` continues only an
already-confirmed prepared approval, edit or host-verified contradiction
suspension using the stored reviewed revision and operation key; source
approvals revalidate current authority, and edits/suspension compare the exact
canonical target before changing it. Neither route accepts browser text or a new
retry key; a mismatched operation kind cannot be cast into the other action.
Other intermediate records remain excluded from context until recovered.
No endpoint accepts a browser-supplied owner, root,
source observation or arbitrary proposal. Validation errors are bounded;
conflicts require refreshing exact current values. A durable canonical save
with a lost HTTP response must be reconciled by retry or read, not reported as
an unsaved fact. This feature adds no calendar/email producer, new provider
integration, change to Assistant Program learning, plugin installation or
source-permission expansion; #533/#534 remain separate work.

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
- If the relationship row is absent, recovery enumerates bounded evidence only:
  owned orchestrator profiles, Personal HQ presentation IDs, ownership,
  designation, entry-agent instance IDs, Daily Brief ownership, and user ID.
  Names, prompts, model settings, tools, credentials, and arbitrary workspace
  metadata are not recovery evidence.
- Exactly one valid owned profile may be recovered. A profile without HQ can be
  reconnected into `needs_hq`. A complete HQ is accepted only when every stable
  identity and owner agrees, and it is restored as `paused` so a database reset
  never silently re-enables proactive routines.
- Missing required evidence, multiple candidate profiles or HQs, a foreign
  owner, stale designation, mismatched entry agent, or mismatched Daily Brief
  owner projects `relationship_recovery_blocked`. Automatic repair and hire are
  both unavailable; Ori never guesses by display name.
- A blocked recovery is explained, not guessed at. The diagnosis names the first
  failed check (`profile_missing`, `profile_duplicate`, `profile_incomplete`,
  `profile_role`, `designation_without_hq`, `hq_duplicate`, `hq_marker_invalid`,
  `hq_foreign_owner`, `assistant_mismatch`, `designation_mismatch`,
  `entry_mismatch`, `brief_missing`, `brief_mismatch`) and offers only fixes
  derived from the evidence it read: point the HQ marker at the one assistant
  profile, keep one of several marked profiles or HQs (the others lose only
  their marker), designate the marked HQ, mark the HQ's orchestrator lead as the
  assistant, rewrite a damaged profile marker, create schedule-off Daily Brief
  settings, or clear a designation whose workspace has no HQ marker. The user
  chooses and applies each fix; HQ markers are written to both the database and
  `workspace.json` so a rebuilt database does not bring the disagreement back.
  Foreign owners, a mismatched lead agent, a non-orchestrator profile, and
  another owner's brief settings have no automatic fix.

## Test matrix

The package/API/browser suites must pin at least these cases:

| Case | Expected projection / invariant |
|---|---|
| Fresh state file | `needs_hire`, default field “Assistant” |
| Existing state file without a relationship or PAF provenance | `needs_hire`; no cohort marker or migration gate |
| Missing relationship with one owned profile and no HQ | `repair_needed` / `relationship_recovery`; repair restores `needs_hq` without creating a profile |
| Missing relationship with one fully matching owned profile and HQ | `repair_needed` / `relationship_recovery`; repair restores the same IDs as `paused` without creating a profile or workspace |
| Missing relationship with ambiguous or contradictory PAF provenance | `repair_needed` / `relationship_recovery_blocked`; no automatic repair and no hire |
| Blocked recovery, HQ marker names an earlier hire | diagnosis `assistant_mismatch` with one recommended `link_hq`; applying it rewrites the marker in both stores and reconnects `paused`, creating nothing |
| Recovery fix reviewed against changed evidence | `409`; nothing written |
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
| Personal HQ upgrade preview or apply, or the Roles roster, on an assistant-built HQ | the hired entry agent fulfils the Chief of Staff role and Journal its own; zero Personal Chief of Staff added or offered; only a genuinely missing support role (Journal) is offered |
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
| Conversation & app records | Clears the enumerated shared-database domains, including relationship, HQ registration, chats, follow-ups and briefs, and removes owned uploads. Retains workspace backing files and suppresses automatic workspace adoption, profile seeding and external MCP import. Retained provenance alone is not startup consent to restore. Explicit portable import can restore only an individually reviewed directory; it must not clear global suppression or overwrite its retained checkpoint with empty backfill. |
| Setup steps | Resets only onboarding progress. It preserves the relationship, stable IDs, agent, Personal HQ, records, and history. A `needs_hq` relationship survives the reset and resumes at the HQ quest rather than offering a second hire or creating another profile. |
| All categories | Applies every selected deletion. If no PAF provenance survives, restarted onboarding offers a fresh hire. Any surviving incomplete or contradictory provenance blocks automatic recovery and hire rather than guessing or creating a duplicate. |

Settings Reset stages a reviewed operation, fences/drains participating writers,
and applies/verifies it before normal stores open after restart. The suppression
policy in `internal/settingsreset/policy.go` is authoritative; a folder marker
cannot override it. The reset review must disclose private portable history that
remains in retained folders. Deliberate private-data deletion and Forget remain
effective, unlike a retained-folder reset. None of these options changes external
accounts, grants new tools, or deletes external-provider data.

## Compatibility

No legacy onboarding cohort is maintained. Existing Personal HQ, Daily Brief,
Follow-Up, workspace, and protected system-assistant records remain valid. The
follow-up ownership amendment is read-only compatibility work: it adds no data
migration, re-key, clone, backfill, orphan adoption, mailbox-permission
expansion, proactive capture, task classification, or generic specialist
integration. Recovery of blank/orphaned legacy ownership, if needed, is separate
reviewed work. A
complete development profile reset returns to a fresh hire only when no PAF
provenance survives; otherwise bounded recovery or blocked review takes
precedence.
