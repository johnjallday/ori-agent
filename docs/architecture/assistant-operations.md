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
    │           │  ▲         │              │  │
    │           └──┘         │              │  └─► unresolved ─► started | reviewed
    └───────────────────────┴──────────────┴──────► cancelled | expired | invalidated
```

- `requested`: a validated tool call created it; commands may be pending.
- `prepared`: a draft revision was issued and handed to the composer. A
  repeat prepare is a new revision of the same operation.
- `reviewed`: the composer's save created the task bound to this operation.
- `start_requested`: Start was claimed exactly once (compare-and-swap).
- `started`: a run was observed on the task (`current_run_id` or execution
  history), not merely an accepted request.
- `unresolved`: the claim's outcome could not be proven (crash or timeout
  between execution admission and the status write). Only inspection or an
  explicit retry through the claim path may follow; nothing starts fresh.
- `cancelled`, `expired`, `invalidated` are terminal. None of them cancels a
  task: cancelling work stays an explicit existing task action.

Task outcome (completed, failed, needs-input, interrupted) is never copied
into the operation. It is read from the task each time, so a quiet event
stream or a stale snapshot cannot invent a finish.

## Commands and acknowledgements

| Action | Arguments | Acknowledged when |
| --- | --- | --- |
| `open_workspace` | `workspace_id`, `workspace_slug` | the **arrived** page's path equals the URL built from the slug; a queued navigation is not acknowledged |
| `select_agent` | `workspace_id`, `agent_name`, `agent_node_id` | the workspace page controller reports that key selected and its target control is mounted (bounded mount wait, as the Help coachmarks do) |
| `open_task_draft` | `workspace_id`, `draft_revision` | the composer is open, populated from that revision, and the assignment select holds the exact node value |
| `open_task` | `workspace_id`, `task_id` | the task/result view for that task is open |

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
  `start_accepted`, `start_refused`);
- task state establishes **execution** (`run_established` is recorded only
  when the task itself reports a run).

Optional recaps are rendered from these recorded facts, labelled as recaps,
and never replay a write or delay a result.

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
`transition_not_allowed`. Responses carry the category and a bounded
user-safe reason, never storage or provider text.

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

Errors are `{code, message}` with `operation_not_found` (404),
`operation_conflict`, `operation_tab_mismatch`, `operation_ended` (409),
`operation_expired`, `operation_invalidated` (410), `operation_invalid` (400)
and `operation_unavailable` (503). Every response is `Cache-Control: no-store`.

## Browser delivery

`ori-operation-client.js` keeps three per-tab values in `sessionStorage`: the
tab's identity, its Watch choice and the operation it is following. It carries
out commands only from a snapshot it has just read, through
`ori-operation-targets.js`, which maps `open_workspace` and `select_agent` to
the route helper and the workspace page's own selection owner.

- A full-page navigation is acknowledged by the page that arrives, with the
  path it observed. The page that left reports nothing.
- A page load never navigates by itself. For 60 seconds after the user asked,
  an arriving page may report arrival and select the agent; after that, or
  when the user is on another page or an editor holds unsaved input, the
  operation pauses and the drawer offers Resume.
- A tab that starts as a copy of another (same stored identity) is told so over
  a `BroadcastChannel` and takes a new identity, so it holds no watch.
- The Watch choice is a chip in the composer's chip row, shown only where
  `availability.operations.available` is true. The status line appears only
  while there is an operation and reports observed facts: "Opening…" until
  arrival is acknowledged, "Opened X and selected Y." only after both.
- The control Ori used is outlined for a few seconds. The mark re-resolves the
  live node from its key, because the page re-renders its controls; the pulse
  is dropped under `prefers-reduced-motion`.

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
