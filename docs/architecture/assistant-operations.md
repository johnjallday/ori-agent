# Assistant operations ("Operate Ori") — contract v1

Status: implementation contract for `feature/operate-ori`; groups 1–4 of
`tasks/tasks-operate-ori.md` build against it and 5.4 reconciles it with what
shipped. Source constants live in `internal/assistantops/types.go`
(`ContractVersion = 1`).

## Purpose and boundaries

The user asks the hired personal assistant to operate the actual app and may
watch it happen. V1 is one journey: open an existing workspace → select an
existing agent → prepare one ordinary, immediate task in the existing composer
→ the user reviews and starts it → real progress → the result.

- The **skill** (embedded `internal/assistantops/skills/operate-ori/SKILL.md`)
  chooses a procedure. It is loaded into the hired assistant's model turn only
  on the Personal Assistant conversation path, only when the operations
  service is wired, and only on a provider whose `Capabilities().SupportsTools`
  is true. It is never installed into a user skill folder, and instruction
  text grants no tool: the registry does.
- **Named tools** (`assistant_operate_*`, closed registry) run inside the
  existing `runModel` loop with the same turn/batch budgets, revalidation on
  every call, and the proposal rule for effectful calls: an operate call must
  be the only call in its batch and is validated before anything executes.
- **Browser commands** are named actions mapped to existing page controllers.
  Model output never supplies JavaScript, selectors, URLs or coordinates; the
  browser builds a workspace URL only from a server-resolved slug through the
  existing route helper.
- **Watch** is a per-tab choice. Only the tab holding the lease receives
  pending commands; other tabs and unrelated background work never move.
- **Nothing is created or started by preparing.** The existing task composer
  is the one create owner and `StartTaskAsync` the one start owner.

## Identity and scope

An operation is bound at creation to a server-resolved owner and context:

| Field | Source | Consequence of change |
| --- | --- | --- |
| `user_id`, `assistant_id`, `state_version`, `hq_workspace_id`, `profile_name` | the resolved relationship (`PersonalAssistantWorkContext`) | continuation is invalidated |
| `conversation_id`, `turn_message_id` | the stored turn that requested it | deleted/replaced conversation invalidates |
| `workspace_id`, `workspace_slug`, `workspace_name` | the turn's pinned subject or an explicit, uniquely resolved target | lost access invalidates; rename refreshes the slug on read |
| `agent_name`, `agent_node_id` | the workspace roster (`ws.AgentInstances`), never the global list or a namesake | a removed/replaced instance refuses commit and asks for a corrected review |
| `tab_id` | the originating tab's per-tab identity | only this tab consumes commands; others may adopt explicitly |

Every read, acknowledgement, prepare and claim revalidates the owner the way
the panel's `revalidateWorkspaceTurn` does. A mismatch returns
`ErrInvalidated`; nothing is retargeted to the current page or to Personal HQ.

## Lifecycle

```
requested ─► prepared ─► reviewed ─► start_requested ─► started
    │           │  ▲         │  ▲           │  │           ▲
    │           └──┘         │  └─ refused ─┘  └─► unresolved (retry ─► start_requested)
    │                        └─ the task ran from its own controls ──────┘
    └───────────────────────┴──────────────┴──────► cancelled | expired | invalidated
```

- `requested`: a validated tool call created it; commands may be pending.
- `prepared`: a draft revision was issued and handed to the composer. A
  repeat prepare is a new revision of the same operation. While a save is
  being written, a prepared operation also holds the **reservation** for its
  one task (see "Creating and starting the task"); a reservation is not a task
  and is not shown to the browser.
- `reviewed`: the composer's save created the task bound to this operation,
  and the server read that task back. A refused Start returns here.
- `start_requested`: Start was claimed exactly once (compare-and-swap). The
  start owner's answer is recorded as `start_accepted` or `start_refused`.
- `started`: the task itself shows execution (in progress, a run identity or
  an execution record), not merely an accepted request. It is also reached
  from `reviewed` when the user ran the task from the task's own controls:
  that is observed, not claimed.
- `unresolved`: a claimed Start showed no outcome. Either no answer from the
  start owner was recorded and nothing in this process is still delivering it
  (a restart between the claim and the answer), or the Start was accepted and
  the task showed no run within 90 seconds. Reads never start it again; only
  the user's explicit retry goes back through the same claim.
- `cancelled`, `expired`, `invalidated` are terminal. None of them cancels a
  task: cancelling work stays an explicit existing task action. An operation
  whose bound task was deleted is `invalidated` and is never given another.

Task outcome (completed, failed, needs-input, interrupted) is never copied
into the operation. It is read from the task each time, so a quiet event
stream or a stale snapshot cannot invent a finish.

## Commands and acknowledgements

| Action | Arguments | Acknowledged when |
| --- | --- | --- |
| `open_workspace` | `workspace_id`, `workspace_slug` | the **arrived** page's path equals the URL built from the slug; a queued navigation is not acknowledged |
| `select_agent` | `workspace_id`, `agent_name`, `agent_node_id` | the workspace page controller reports that key selected and its target control is mounted (bounded mount wait, as the Help coachmarks do) |
| `open_task_draft` | `workspace_id`, `draft_revision` | the composer is open, populated from that revision, and the assignment select holds the exact node value |

There is no command that opens a task or a result. The user opens those from
the drawer, on request, so a result view is never pushed onto a tab while
they are reading or typing somewhere else.

Command status: `pending → acknowledged | unavailable | superseded`. An
acknowledgement carries `{operation_id, command_id, revision, tab_id,
outcome, observed}`; a stale revision or a foreign tab is refused. Repeated
acknowledgements of a settled command are harmless (they return the current
snapshot). An acknowledgement is view evidence only: it records
`view_opened` / `agent_selected` / `draft_opened` receipts and is never write
authority.

## Receipts

Three kinds of evidence are kept distinct in the operation's receipts and in
the UI:

- browser acknowledgements establish **view** changes;
- server receipts establish **writes** (`draft_prepared`, `task_created`,
  `start_accepted`, `start_refused` with its reason, `start_unresolved`);
- task state establishes **execution** (`run_established` is recorded only
  when the task itself reports a run, and carries that run's identity when the
  task has one). The first run a task reports is the operation's run; a later
  rerun has its own identity and never replaces it.

Optional recaps are rendered from these recorded facts, labelled as recaps,
and never replay a write or delay a result.

When an operation reaches its receipt bound, view facts are dropped first and
then the oldest answers to Start (each retry adds one). `task_created` and
`run_established` are kept for as long as anything else can go.

## Bounds

| Bound | Value |
| --- | --- |
| commands per operation | 8 |
| receipts per operation | 16 |
| draft title / brief | 200 / 4,000 runes |
| reasons (any) | 240 runes |
| observed path / tab identity | 300 runes / 64 characters of `[A-Za-z0-9_-]` |
| stored JSON columns | context ≤ 4 KB, commands ≤ 16 KB, draft ≤ 24 KB, receipts ≤ 16 KB (checked in Go first, repeated by the schema) |
| time to live | 24 h from creation |
| accepted Start with no run | 90 s, then `unresolved` |
| active operations per user | 20 (the oldest open one is retired first; a start being decided is never retired) |
| rows expired / removed per sweep | 50 each; an ended operation stays readable for 24 h so a returning tab can be told why it ended |

A snapshot reports `pending`, the number of steps still waiting, to every tab
of the owner. Only the watching tab receives the commands themselves. A newer
request from the same tab supersedes that tab's still-pending commands, so two
operations never drive one view.

## Error meanings

`not_found`, `conflict` (stale revision; read again), `forbidden` (not this
owner), `expired`, `invalidated` (conversation/relationship/workspace
changed), `tab_mismatch` (no watch lease), `invalid_command`, `bound_exceeded`,
`transition_not_allowed`, `assignee` (the task's agent is not one instance in
this workspace's own roster; the operation continues), `create_in_flight`
(another request is writing this operation's task right now), `start_refused`
(the start owner said no; nothing ran) and `task_missing` (the bound task is
gone). Responses carry the category and a bounded user-safe reason, never
storage or provider text: a start owner's internal error is recorded as "Ori
could not start the task right now."

## Storage

One SQLite table, `assistant_operations`, added by migration 80 and listed in
`ResetRecordTables()`, so Reset wipes it with the other app records. One row
per operation with a `revision` compare-and-swap column; commands, draft and
receipts are bounded JSON columns checked by the schema. Operation-to-task
identity is stamped on the task at the canonical write boundary
(`assistant_operation_id` on the create request, validated against the store
before persisting), so a repeat or a recovery finds the task by its stamp
rather than by a matching title.

## Which turns may operate

The host decides, from the user's current message alone
(`operationRequest`). A clause must begin with an open or select verb ("open",
"go to", "take me to", "show me", "switch to", "select", "pick", "choose") and
name a real workspace or agent, or the word "workspace" or "agent". Quoted text
is ignored. A question ("how do I open…"), a hypothetical, a negation, "show me
how to…" and a target that is only "it" or "that" are not requests. Earlier
turns and anything a reader returned are never consulted.

When the check fails, no operation tool is advertised and the skill is not
loaded for that turn. When it passes and the configured provider can run Ori's
tools, the router keeps the request in the hired assistant's conversation, so
it has one owner; where Ori cannot operate, the existing owner (a navigation
answer, an app launch, a specialist) keeps it unchanged.

`assistant_operate_open` takes `workspace_id` or an exact `workspace_name`, and
an optional `agent_name` looked up in that workspace's own roster. It returns
`requested`, `choose` (several match, with the candidates), `not_found` (with
the closest names) or `unavailable`. It stages a request; the operation is
recorded only after the turn that asked for it is stored. A turn that is not
saved leaves no operation and the reply carries `operation_error`.

### Preparing a task

A clause that begins with "prepare", "create", "add", "make", "set up", "draft"
or "write" and names a task as the thing to make ("create a task for Mixer to
…", "add a task: …"), or "give/assign <someone> a task …", is a request to
prepare a task. "Create a task list", "write a task description", "create
tasks for the album" and "create that task" are not.

`assistant_operate_prepare_task` takes the same workspace reference plus
`agent_name`, an optional `agent_node_id`, a `title` (required, at most 200
characters) and a `brief` (at most 4,000). Nothing else can be set: a field
such as a schedule is refused, not ignored. It returns:

- `prepared`: a draft was staged for exactly one instance in that workspace's
  own roster;
- `choose` with `agent_not_named` (no agent was named: the roster is returned
  and nothing is assumed, because a task saved without an agent would go to the
  workspace's entry agent) or `several_agents_match` (instances that share the
  name, each with its node);
- `not_found` with `agent_not_in_workspace` (a same-named agent elsewhere in
  the app is not a match) or `workspace_not_found`;
- `unavailable` with `draft_out_of_bounds`, `secret_like_text`,
  `agent_cannot_be_assigned` or `one_operation_per_request`.

The recorded operation is `kind: task`, `status: prepared`, with a `draft`
(`revision`, `title`, `brief`, `agent_name`, `agent_node_id`), a
`draft_prepared` receipt and two commands: `open_workspace` and
`open_task_draft`. The agent is checked against the roster again when the
operation is recorded, so an agent who left between the tool call and the saved
turn gets no draft and no stand-in. **Preparing writes no task and starts no
run.** One request has one owner: where Ori can prepare a task, "create a task
in Album to …" is that operation's, not also the legacy one-click create that
would hand the task to the entry agent unreviewed. Starting work and every
workspace or agent change keep their own confirmation.

`open_task_draft` is carried out by the page's own task form
(`TaskModalController.openForCreate`, manual mode, with the draft's title,
brief and the exact `node:<node id>` assignment). The form adds that instance
to its assignment list when its own list lacks it. The step is acknowledged
only after the form is open and really holds the title, the brief and that
assignment; if the form cannot assign the agent it is closed and the step is
reported unavailable, rather than left showing "no agent". A form with unsaved
input is never overwritten: the step pauses, and replacing the input is an
explicit choice in the drawer. Nothing is typed, auto-parsed or saved by Ori.
The form keeps its one save owner; its save callback now receives what was
saved (`{ task }`), which existing callers ignore.

After the form has been shown, the drawer offers **Open draft again** on that
workspace's page. Reopening is a local view of the same draft: it reports
nothing to the server, changes no operation and does not replace unsaved
edits. When the user saves the form, the tab stops offering that draft.

### Creating and starting the task

There is no model tool for either. Creation is the user's Save in the task
form; Start is the user's click on **Start task** in the drawer. Watch, an
operation ID typed into a request, an earlier agreement in the conversation or
anything the model returns authorizes neither.

**Create.** The task form stays the one create owner. When it saves a draft
the assistant prepared, and the save is one ordinary task (no schedule, parent
or input tasks), its `POST /api/orchestration/tasks` carries
`assistant_operation_id` and `assistant_draft_revision`. With a schedule or
inputs the form sends neither: that is the user's own task, and the tab stops
offering the draft. The create handler validates every field exactly as for
any create, pre-assigns the task ID, and then asks the operation service to
**reserve** the operation's single task (`ClaimTaskCreate`). The reservation:

- revalidates the owner, the relationship, the conversation and the workspace,
  and requires a task operation for this workspace at the current draft
  revision;
- checks the **submitted** assignee (the human's reviewed choice, which may
  differ from the draft) against the workspace's own roster as it is now. An
  empty assignee, `unassigned`, an agent that left, a same-named agent
  elsewhere, or another instance's node is refused with `operation_assignee`.
  It is never added to the workspace and never replaced by the entry agent;
  the operation stays `prepared` so the corrected form can be saved;
- is a compare-and-swap that stores the task ID on the operation **before**
  the task is written.

The handler then writes the task with the roster's canonical name and node and
a stamp, `assistant_operation_id`, saves the workspace, and confirms
(`ConfirmTaskCreate`). Confirmation reads the task back and checks its stamp
before moving to `reviewed` with a `task_created` receipt: the browser's
report of a save is never the evidence. A failed save gives the reservation
back (`ReleaseTaskCreate`). Assignment provenance is `manual`: the person
reviewed and saved it.

One operation creates one task:

- a repeated save, a second tab or a retry after a lost response loses the
  swap and gets the first task back (`200`, `already_created: true`);
- a second save while the first is still being written gets
  `operation_create_in_flight`;
- after a restart the reservation and the stamp identify each other. On the
  next read, a reservation whose stamped task exists is confirmed, and one
  whose task never appeared is given back. A reservation that this process is
  still writing is left alone, and "could not read the task" is never treated
  as "missing". (Ori runs one server per data directory, so "not being written
  by this process" is exact.) "Missing" itself gives a reservation back or
  ends an operation, so it is believed only when a second read agrees;
- an operation that ends (dismissed, expired, invalidated, or expired by the
  sweep) while it only holds a reservation names no task afterwards. A task
  identity survives the end of an operation only if it was confirmed;
- an expired, invalidated, dismissed or unknown operation refuses the save
  (`operation_expired`, `operation_invalidated`, `operation_ended`,
  `operation_not_found`). Nothing is created. The form then lets go of the
  operation and says so; only the user's next Save creates an ordinary task.
  A create that names an operation is never turned into an ordinary create by
  the server.

**Start.** `POST /api/home-assistant/operations/{id}/start` claims Start once:

- an operation already `start_requested` or `started` returns its snapshot and
  starts nothing (duplicate delivery, a second tab, a reload, a retry);
- a task that already shows execution (the user started it from the task's
  own controls first) becomes `started` from the task's own run, without
  calling Start;
- otherwise the task's **own** assignee, as saved and possibly edited since,
  is checked against the roster. A removed agent is refused with
  `operation_assignee` and a `start_refused` receipt; the task is untouched;
- the claim is a compare-and-swap `reviewed → start_requested`, then one call
  to the task owner's first-start method, `StartReviewedTask`. That method
  uses the same path as the manual execute endpoint (reset admission, manual
  workspace admission, the backlog rule, the default execution mode and
  `executeTaskWithDependencies` with its capability gate) and, unlike that
  endpoint, never resets a finished task and refuses a task that already ran,
  so no repeated or late call can become a rerun. Ordinary reruns from the
  task's own controls are unchanged.

A refusal returns the operation to `reviewed` with the reason, and the task
stays under its original ID, startable again through the same claim. Nothing
is deleted as a rollback and nothing uncertain is re-executed.

The drawer follows the bound task through the existing
`WorkspaceExecutionController`: on the task's workspace page it is the page's
own controller and tray; elsewhere it is a controller of the same class that
only reads the task. "Working", "needs your input", "failed" and "finished"
are read from `resolveTaskPresentation`, never inferred from an acceptance or
a timer. The operation is read again when the task moves, every 3 seconds
while a claimed Start waits for its run, and every 15 seconds while a created
task waits for the user.

### The result of the operation's own run

A started operation's snapshot carries `outcome`, resolved from the task on
every read and never stored:

| Field | Values |
| --- | --- |
| `run` | `running`, `waiting` (stopped for the user and still waiting), `stopped` (stopped for the user; continued by a later attempt), `succeeded`, `failed`, `cancelled`, `interrupted` (launched and never recorded an execution), `unknown` (the task has dropped its record from its history) |
| `result` | `current` (the result the task shows now is this run's), `history` (the task ran again; this run's result is in its history), `gone` (finished, but its result is no longer kept), `none` |
| `later_runs` | how many times the task has run since |

Which execution was the operation's is not inferred, and a run identity is
not used for it: one execution may retry itself under several run identities
(the task's record then carries the last), and the task may be run again later
from its own controls. The task carries an exact mark instead,
`assistant_operation_run`:

- `StartReviewedTask` sets it to *pending* when it launches the operation's
  Start, and takes it back if the launch is refused;
- the execution recorder (`RecordTaskExecution`) turns a pending mark into the
  `executed_at` of the record it is writing, in the same write, so no reader
  sees the record without the mark;
- the executor's boot reconciliation clears a pending mark on a task it finds
  left in progress: that start will never record an execution;
- nothing else writes it, a later run never moves it, and a workspace transfer
  drops it.

So: marked and recorded → that record, and every later record is a rerun;
marked and pending → running or waiting; marked and cleared → interrupted;
not marked (the user started the task from its own controls, and the
operation only observed it) → **no outcome**: the operation claims no run and
no result. `result` is `current` only when the marked execution succeeded,
the task has not run again and the task holds a result. **A later run's result
is never presented as this operation's.** `task.run_id` remains the first run
identity the task reported, for reference; it is not what attribution uses.

- `current` → the drawer offers **Open result**. It opens the page's existing
  result view (`workspaceDetail.showTaskResult`), or arrives through the
  page's existing one-shot link (`?task=<id>&result=1`) when this page is not
  the task's workspace or has not loaded the result. Opening is a local view:
  nothing is reported and nothing changes.
- `history` → no **Open result**. The drawer says the task has run again, that
  what the task shows now is a later run's result, and that this request's
  result is in the task's run history, with a link to the task.
- `gone` or `unknown` → the drawer says the result is no longer kept.
- `interrupted` → what a restart leaves: the executor's boot reconciliation
  returns an in-progress task to waiting without recording a run. The drawer
  says the run was interrupted and that nothing was started again. Starting it
  again is the user's own action on the task; the operation's Start stays
  claimed and a late call to it starts nothing.

Needs-input, blocked, failed, timed-out and cancelled are the task's own
states, shown in the task presentation's words with a link to the task, where
the existing answer, repair, retry and cancel controls are. The drawer has no
cancel: **Stop following** ends updates here and its tooltip says the task
keeps going. A monitor that is reconnecting says "Reconnecting…" and what the
task last reported; silence is never read as a finished task.

### Recap

The drawer's **Recap** is a native disclosure (`<details>`) outside the live
region. It lists the operation's receipts and the outcome above, each line
prefixed with where the fact came from: "Seen in this tab" (acknowledged view
steps), "Recorded by Ori" (the draft, the created task, each answer to Start)
or "From the task" (a run was reported; what became of it). It is built by
`operationRecap` from the snapshot alone. Opening, closing or reopening it
sends no request and changes nothing.

### Taking over

The user is always ahead of Ori. Any of these stops this tab's steps at once,
cancels nothing and leaves the task running:

- turning **Watch** off (before the server answers, a step already waiting on
  the page does nothing);
- dismissing the operation;
- asking for something else (the newer request replaces a step still waiting
  for the older one; the older step's outcome is not reported);
- turning the drawer to another conversation (`personal-assistant:conversation`):
  the operation pauses with **Resume**;
- typing into a form (unsaved input pauses the step) or moving to another
  page (a page load never navigates by itself).

Each step asks "is this request still the one in front of the user?" again
just before it changes anything on the page, so a wait that outlasts the
request ends without selecting, opening or navigating. The highlight is
removed as soon as the request is paused, dismissed or taken over.

After a reload the drawer is rebuilt from the server. A draft form that was
open is not silently re-opened: the drawer says that anything typed into it is
gone and offers **Open draft again**.

## HTTP surface

| Method and path | Purpose |
| --- | --- |
| `POST /api/home-assistant/ask` | request field `operation: {tab_id, watch}`; response fields `operation` (snapshot for the asking tab), `operation_error`, `operation_capability` |
| `GET /api/personal-assistant` | `availability.operations: {available, status, reason}` for a relationship that is ready for work |
| `GET /api/home-assistant/operations/capability` | the same capability, directly |
| `GET /api/home-assistant/operations/{id}?tab=` | the operation as that tab may see it |
| `POST /api/home-assistant/operations/{id}/ack` | `{command_id, revision, tab_id, outcome, observed, reason}` from the watching tab |
| `POST /api/home-assistant/operations/{id}/watch` | `{tab_id, revision, watch, adopt}` |
| `POST /api/home-assistant/operations/{id}/cancel` | `{tab_id, revision}`; dismisses the operation only |
| `POST /api/home-assistant/operations/{id}/start` | `{tab_id}`; the user's Start for the operation's task, claimed once |
| `POST /api/orchestration/tasks` | optional `assistant_operation_id` and `assistant_draft_revision`, sent by the task form for a prepared draft; a repeat answers `200` with `already_created: true` and the first task |

Errors are `{code, message}` with `operation_not_found` (404),
`operation_conflict`, `operation_tab_mismatch`, `operation_ended`,
`operation_assignee`, `operation_start_refused`, `operation_create_in_flight`
(409), `operation_expired`, `operation_invalidated` (410), `operation_invalid`,
`operation_not_plain` (400) and `operation_unavailable` (503). A refused Start
also carries `details.operation`, the operation as it now stands. Every
response is `Cache-Control: no-store`.

## Browser delivery

`ori-operation-client.js` keeps three per-tab values in `sessionStorage`: the
tab's identity, its Watch choice and the operation it is following. It carries
out commands only from a snapshot it has just read, through
`ori-operation-targets.js`, which maps `open_workspace` and `select_agent` to
the route helper and the workspace page's own selection owner.

- A full-page navigation is acknowledged by the page that arrives, with the
  path it observed. The page that left reports nothing.
- A page load never navigates by itself. For 60 seconds after the user asked,
  an arriving page may report arrival and then select the agent or open the
  task form; after that, or when the user is on another page or an editor holds
  unsaved input, the operation pauses and the drawer offers Resume.
- A tab that starts as a copy of another (same stored identity) is told so over
  a `BroadcastChannel` and takes a new identity, so it holds no watch.
- The Watch choice is a chip in the composer's chip row, shown only where
  `availability.operations.available` is true. The status line appears only
  while there is an operation and reports observed facts: "Opening…" until
  arrival is acknowledged, "Opened X and selected Y." only after both.
- The control Ori used is outlined for a few seconds. The mark re-resolves the
  live node from its key, because the page re-renders its controls; the pulse
  is dropped under `prefers-reduced-motion`.
- Once the operation has a task, the status line reports three separate
  things in order: "Task created … It has not started." (the server's
  `task_created` receipt), "Start accepted. Waiting for … to begin…" (the
  start owner's answer) and then the task's own state ("… is working on …",
  "… needs your input …", "… failed. …", "… finished …"). **Start task** is the
  only control that starts anything, it is disabled while its request is on
  its way, and it is gone once Start is claimed. A refusal shows its reason
  with **Try starting again**; a Start request that got no answer shows
  **Check again** and never retries by itself. **Open the task** is a plain
  link to the workspace's own Tasks panel for that task.
- The page's own execution tray is opened for a start the user just made in
  this tab, or on the page they arrive at within the 60-second window. A later
  visit follows the task without opening anything; **Show progress** opens the
  tray on request.

## Fallbacks

- Provider without tools: the skill is not loaded, no operate tool is
  advertised, and the reply offers the existing manual navigation actions.
- Watch off: commands are not delivered; the reply offers an explicit
  "Open" action for the same operation, and running-work updates stay compact.
- Missing page control or an unsaved editor: the command settles as
  `unavailable` with a reason; the manual route remains and Resume is explicit.
- Reload, reconnect, duplicated tab or server restart: the tab fetches the
  authorized snapshot and the task/run facts before consuming any pending
  command; a copied tab identity must adopt explicitly.
- No orchestration handler (no task owner): a draft can still be prepared, a
  save that names the operation is refused with `operation_unavailable`, and
  the form's next Save creates an ordinary task.
- A task created but not started, a refused Start and an unresolved Start all
  keep the task under its original ID with an action beside it. Nothing is
  deleted to roll back and nothing uncertain is started again automatically.
