# 🌐 API Reference

This document provides comprehensive API documentation for Ori Agent, including all endpoints, request/response formats, and examples.

## Base URL

All API endpoints are relative to the base URL:
```
http://localhost:8765/api
```

## Table of Contents

- [Authentication](#authentication)
- [Response Formats](#response-formats)
- [Error Handling](#error-handling)
- [Agents API](#agents-api)
- [Home Assistant Routing API](#home-assistant-routing-api)
- [Settings API](#settings-api)
- [Vault API](#vault-api)
- [Chat API](#chat-api)
- [Updates API](#updates-api)
- [Tags API](#tags-api)
- [Workspace Groups](#workspace-groups)
- [Workspace Build Sessions API](#workspace-build-sessions-api)
- [Personal Assistant Folder Digest API](#personal-assistant-folder-digest-api)
- [Home Library: Open a Song and the Shared Assistant](#home-library-open-a-song-and-the-shared-assistant)
- [Workspace Outputs API](#workspace-outputs-api)
- [Scheduler Nodes API](#scheduler-nodes-api)
- [Workspace Map Activity API](#workspace-map-activity-api)
- [Custom Workflows API](#custom-workflows-api)
- [Blueprint Intake API](#blueprint-intake-api)
- [Examples](#examples)

## Authentication

Currently, Ori Agent does not require authentication for local development. API keys are managed through the settings endpoints and stored locally, with new writes preferring the configured secure secret store over plaintext settings files when available.

## Response Formats

All API responses use JSON format. Successful responses typically include:

```json
{
  "success": true,
  "data": { /* response data */ },
  "message": "Operation completed successfully"
}
```

Error responses include:
```json
{
  "success": false,
  "error": "Error message",
  "code": "ERROR_CODE"
}
```

## Error Handling

Common HTTP status codes:
- `200` - Success
- `400` - Bad Request (invalid input)
- `404` - Not Found
- `500` - Internal Server Error
- `502` - Bad Gateway (plugin errors)

## Agents API

### List Agents

Get all available agents.

**Endpoint:** `GET /api/agents`

**Response:**
```json
{
  "agents": ["Ori", "my-agent", "test-agent"]
}
```

### Create Agent

Create a new agent with the specified name and optional configuration.

**Endpoint:** `POST /api/agents`

**Request Body:**
```json
{
  "name": "my-new-agent",
  "model": "gpt-4o-mini",
  "temperature": 0.7,
  "system_prompt": "You are a helpful assistant."
}
```

**Parameters:**
- `name` (required): Name of the agent
- `model` (optional): Model to use (e.g., `"gpt-4o-mini"`, `"claude-3-haiku-20240307"`). Defaults to system defaults.
- `temperature` (optional): Temperature setting (0.0-2.0). Defaults to system defaults.
- `system_prompt` (optional): Custom system prompt. Defaults to the agent template or empty.

**Response:**
```json
{
  "success": true,
  "message": "Agent 'my-new-agent' created successfully"
}
```

### Switch Agent

Switch to a different agent.

**Endpoint:** `PUT /api/agents?name=<agent_name>`

**Parameters:**
- `name` (query): Name of the agent to switch to

**Response:**
```json
{
  "success": true,
  "message": "Switched to agent 'my-agent'"
}
```

### Delete Agent

Delete an existing agent and all its configuration.

**Endpoint:** `DELETE /api/agents?name=<agent_name>`

**Parameters:**
- `name` (query): Name of the agent to delete

**Response:**
```json
{
  "success": true,
  "message": "Agent 'my-agent' deleted successfully"
}
```

### Where agents live, and the roster list

The user's agents are stored in the Workspace Directory at
`<root>/Agents/<Name>/` (`agent_settings.json` plus sidecar files); the
built-in assistant stays in the app data dir. Runtime state (status,
statistics, evolution) is kept in `<data dir>/agent_state/`. Agents that only
a trusted workspace holds (its own copy under `<workspace>/agents/`) are read
in place and appear in the roster too.

**Endpoint:** `GET /api/agents/dashboard/list` (also `GET /api/agents` and
`GET /api/agents/{name}/detail` for the `origin` object)

Each stored agent carries an `origin` object. `source` on the entry itself is
unchanged (`user` or `cli`) because it also seeds the generated appearance.

```json
{
  "agents": [
    {
      "name": "Scout",
      "source": "user",
      "origin": {
        "source": "roster",
        "customised_in": [{ "id": "ws-1", "name": "Studio" }]
      }
    },
    {
      "name": "Stranger",
      "source": "user",
      "origin": { "source": "workspace", "workspace_id": "ws-1", "workspace_name": "Studio" }
    }
  ],
  "agent_root": {
    "path": "/Users/me/Ori Workspaces/Agents",
    "available": true,
    "migration": { "status": "done", "skipped": [] }
  }
}
```

- `origin.source`: `system` (the built-in assistant), `roster` (the user's
  own agent), or `workspace` (only a trusted workspace holds it).
- `origin.customised_in`: workspaces holding their own copy of a `system` or
  `roster` agent.
- `agent_root.available` is false with `reason` `missing` when the Workspace
  Directory cannot be found, or `occupied_by_workspace` when a workspace named
  "Agents" holds that folder. `agent_root.migration.status` is one of `done`,
  `blocked_by_workspace`, `not_writable`, `backup_failed`, `incomplete`,
  `root_missing`.
- An agent whose `agent_settings.json` is not valid JSON is still listed, with
  `"state": "unreadable"` and `"file": "<path>"`, so it can be fixed by hand.
  Ori never loads, overwrites, or deletes it.

**Errors on writes to an agent:**
- `409` `{"code": "agent_changed_on_disk"}` — the definition file was edited
  outside Ori; Ori reloaded it. Review it and try again.
- `409` `{"code": "agent_unreadable", "file"}` — the definition file is not
  valid JSON; fix or remove that file.
- `409` `{"code": "workspace_owned_agent", "workspace_id", "workspace_name"}` —
  only a workspace holds this agent; edit it there, or add it to your agents.
- `503` `{"code": "agent_root_unavailable"}` — the Workspace Directory was not
  found.

### Show an Agent's Folder

Opens `<root>/Agents/<Name>/` in Finder (macOS only). The folder comes from the
agent store by name; no path is taken from the request.

**Endpoint:** `POST /api/agents/{name}/reveal`

**Responses:** `200` `{"success": true, "path": "/Users/me/Ori Workspaces/Agents/Scout"}`;
`404` for an agent with no folder in the Workspace Directory (the built-in
assistant, a workspace-only agent); `501` on other platforms.

### Agent Images

`POST|DELETE /api/agents/{name}/appearance/upload` stores an uploaded image in
the agent's own folder (`<root>/Agents/<Name>/<Name>.png`) for the user's
agents, or in `<data dir>/agent_avatars/` for the built-in assistant.
`GET /avatars/{filename}` finds the agent whose appearance names that file and
serves it from that agent's folder; only image files are served, and a missing
file is a `404` (the page then shows the generated appearance).

### Add a Workspace's Agent to Your Agents

Copy an agent that only a trusted workspace holds into the user's own agents
(`<root>/Agents/<Name>/`). Nothing does this automatically.

**Endpoint:** `POST /api/agents/add-from-workspace`

**Request Body:**
```json
{ "workspace_id": "ws-1", "name": "Stranger" }
```

**Responses:** `201` `{"success": true, "name": "Stranger", "origin": {...}}`;
`404` when that workspace holds no such agent; `409` `{"code": "agent_exists"}`
when one of your agents already has the name.

## Home Assistant Routing API

### Route Home Assistant Task

Classify a home page assistant prompt and find the best matching existing agent.

**Endpoint:** `POST /api/home-assistant/route`

**Request Body:**
```json
{
  "prompt": "Plan my 3 day trip in LA"
}
```

**Parameters:**
- `prompt` (required): Natural language task prompt from the home page assistant.

**Response:**
```json
{
  "intent": "travel_planning",
  "intent_label": "travel planning",
  "matched_agent": "Travel Planner",
  "score": 8,
  "requires_creation": false,
  "reasons": [
    "matches \"trip\"",
    "has plugin support for weather"
  ],
  "suggested_agent_name": "Travel Planner"
}
```

**Response fields:**
- `intent`: Detected intent key (`travel_planning`, `email_check`, `general_task`).
- `intent_label`: Human-readable label for the detected intent.
- `matched_agent`: Name of the best existing agent (present when a match is found).
- `score`: Match score for the selected agent.
- `requires_creation`: `true` when no suitable existing agent is found.
- `reasons`: Short explanation list for why the agent was matched.
- `suggested_agent_name`: Suggested name when creating a new agent.

**No-match example:**
```json
{
  "intent": "email_check",
  "intent_label": "email triage",
  "score": 0,
  "requires_creation": true,
  "suggested_agent_name": "Email Assistant"
}
```

### Ask Home Assistant (Inline Harness)

Answer an app-introspection or app-navigation prompt inline. The server builds a
cross-workspace **home snapshot** (the agent roster, workspaces, windowed task
activity, recent sessions, open Action Center opportunities, usage), runs the
system model with read-only `home_*` tools, and returns a written answer plus
grounded next-step actions. Used by the home page "Ask Ori" panel when
`/api/home-assistant/route` classifies the prompt as `app_introspection` or
`app_navigation` (route mode `home_inline`).

The agent section lists each agent's type, role, model, and the workspaces that
use it, so the assistant can answer questions like "what agents do I have", "what
can agent X do", and "which agents aren't used anywhere". The read-only tools the
model may call are `home_workspaces`, `home_tasks`, `home_sessions`,
`home_opportunities`, `home_usage`, and `home_agents`.

**Endpoint:** `POST /api/home-assistant/ask`

**Request Body:**
```json
{
  "prompt": "Summarize this week's task activity",
  "intent": "app_introspection",
  "date_window": "this_week",
  "context": { "surface": "home", "page_path": "/" }
}
```

**Parameters:**
- `prompt` (required): Natural language question about the user's own Ori data, or a navigation request.
- `intent` (optional): `app_introspection` or `app_navigation`. Defaults to `app_introspection`.
- `date_window` (optional): `today`, `this_week`, or `this_month`. When omitted, the server picks a default from prompt phrasing (recap/summary asks → `this_week`; status asks → `today`).
- `context` (optional): Same shape as `/api/home-assistant/route` (`surface`, `page_path`, `workspace_id`, …).
- `confirmed_action` (optional): Present only when re-submitting a confirmed mutation (see confirmation flow below).

**Response:**
```json
{
  "response": "This week you have 2 active workspaces and 5 tasks: 3 completed, 2 in progress…",
  "intent": "app_introspection",
  "snapshot_meta": {
    "window": "this_week",
    "window_label": "this week (Jun 1 – Jun 8)",
    "workspace_count": 2,
    "task_count": 5,
    "session_count": 4,
    "opportunity_count": 1,
    "agent_count": 3,
    "degraded": [],
    "truncated": []
  },
  "actions": [
    { "id": "open-ws-ws-1", "type": "open_workspace", "label": "Open Launch Plan", "href": "/workspaces/ws-1", "workspace_id": "ws-1" },
    { "id": "nav-action-center", "type": "navigate", "label": "Review 1 opportunity", "href": "/action-center" }
  ]
}
```

**Response fields:**
- `response`: The assistant's written answer. Always present and renderable, even on degraded data or when the model is unavailable.
- `intent`: The intent the harness answered as.
- `snapshot_meta`: Section counts plus `degraded` (sections whose data source was unavailable) and `truncated` (sections capped — more is available via the tools).
- `actions`: Validated next-step action descriptors (see schema below). Navigation/`open_*` actions are read-only; `create_*` / `start_task` carry `requires_confirmation`.
- `requires_confirmation` / `confirmation`: Present when the prompt is an explicit mutation request awaiting confirmation.

**Action schema:**
- `id`: Stable action id.
- `type`: One of `navigate`, `open_workspace`, `open_task`, `open_session`, `create_workspace`, `create_task`, `start_task`, `assign_agent`, `create_agent`, `remove_agent`, `ask_followup`, `open_setup_quest`.
- `open_setup_quest` opens a guided setup and is attached by the host, never by the model: when a hired-assistant turn's message names an integration Ori has reviewed (whole word, case-insensitive, from the reviewed registry), or the model read the reviewed list and its reply names an entry, and that integration still has a guided setup to open. It carries no `href`; `arguments` name the quest by key: `quest_id`, `display_name`, `source` (`host` for the install quest, `plugin` for the installed plugin's own quest) and, for `plugin`, `plugin_id`. The label is "Open Guided Setup: <name>" or, once the plugin is installed, "Continue setup: <name>". The reply itself opens with a sentence naming the integration and its install state, and that sentence is part of the saved conversation; the action is not. A panel message that names such an integration is routed to the assistant conversation even when it starts with "set up", "install" or "connect".
- `label`: Button label.
- `href` (optional): Destination for navigation/`open_*` actions.
- `workspace_id` / `task_id` / `session_id` (optional): Resolved target ids.
- `requires_confirmation` (optional): `true` for state-changing actions.
- `confirmation_summary` / `arguments` (optional): Confirmation copy and payload.

**Confirmation flow (mutations):**
An explicit mutation request returns a confirmation instead of acting. Three
mutations are recognized from natural language, each requiring confirmation:

| Action | Example prompt | Arguments |
| --- | --- | --- |
| `create_workspace` | `"create a workspace called Q3 Planning"` | `name` |
| `create_task` | `"create a task to summarize Q2 sales in Q3 Planning"` | `workspace_id`, `description` |
| `start_task` | `"start the deploy task in Operations"` | `workspace_id`, `task_id` |
| `assign_agent` | `"add agent Scout to Operations"` / `"assign Scout to Operations"` | `workspace_id`, `agent_name` |
| `create_agent` | `"create an agent called Atlas"` | `name` |
| `remove_agent` | `"remove agent Scout from Operations"` | `workspace_id`, `agent_name` |

Task and agent mutations resolve the named workspace (and the target task or
agent) against real state before proposing; an unresolved workspace/task/agent, an
empty description, or an ambiguous task match falls through to the normal answer
path rather than guessing. `start_task` uses the workspace's sole runnable task
when the prompt doesn't name one. `assign_agent` / `remove_agent` only resolve
agents that exist in the user's roster, and the server re-validates before
mutating (`create_agent` rejects a duplicate name; `remove_agent` cannot remove a
workspace's required entry agent). Agent management covers the lifecycle: create a
new agent, add it to a workspace, and remove it.

```json
{
  "response": "Create a new workspace named \"Q3 Planning\"?",
  "intent": "app_introspection",
  "requires_confirmation": true,
  "confirmation": {
    "action_id": "create-workspace",
    "action_type": "create_workspace",
    "summary": "Create a new workspace named \"Q3 Planning\"?",
    "arguments": { "name": "Q3 Planning" }
  }
}
```
The client confirms by re-calling the endpoint with `confirmed_action` set to a `HomeAction` of that type and arguments. The server executes only known mutation types after confirmation; the model is never given write tools. `start_task` runs the task through the same orchestrator path as the workspace UI (coordinator assignment and the delegation loop apply), executing asynchronously so the response returns immediately.

When the Personal Assistant can build a workspace with the user (see [Workspace Build Sessions API](#workspace-build-sessions-api) — hired, with a model), a `create_workspace` request is proposed as `build_workspace` instead, and the store is never written:

```json
{
  "requires_confirmation": true,
  "confirmation": {
    "action_id": "build-workspace",
    "action_type": "build_workspace",
    "summary": "Build this workspace with Atlas? You'll see it set up the form and check it before anything is created.",
    "arguments": { "name": "Q3 Planning", "first_message": "create a workspace called Q3 Planning" }
  }
}
```

The client does not send `build_workspace` back. Confirm opens Create Workspace in build mode (entry point `personal_assistant_ask`) with `first_message` as the assistant's first turn. If build mode stopped being available in the meantime, the client opens the ordinary dialog seeded with `name` instead.

### Assistant Conversations, Saved Drafts, and Remember…

With a hired assistant and a Personal HQ, an everyday writing request (route
intent `assistant_conversation`) is answered as a turn of a stored
conversation. The behavior and its limits are specified in
[`docs/architecture/personal-assistant-foundation-contract.md`](../architecture/personal-assistant-foundation-contract.md)
("Assistant conversations", "Saving a draft to the HQ backlog", "Remembering a
fact from a conversation"); this section lists the routes.

**Conversation turn:** `POST /api/home-assistant/ask` with two more optional
request fields:

- `conversation`: `{ "id": "<conversation id>" }` to continue a conversation, or `{}` to start one. The ID is validated against the current assistant and Personal HQ on every request; it is never taken from the browser's chat session.
- `draft`: `{ "ticket_id": "<ticket id>" }` when the conversation is working on a saved draft, so the turn reads that draft's current text. A turn never writes it.

and these response fields:

- `conversation`: `id`, `title`, `started`, `stored`, `user_message_id`, `assistant_message_id`, `history_truncated`, or `error` (`conversation_not_found`, `conversation_out_of_scope`, `conversation_unavailable`, `link_context_unavailable`). Only an answered turn is stored. When `stored` is false nothing was saved and the client returns the text to the composer.
- `model_unavailable`: `true` when no model could answer. Arrangement mode also uses this input-preservation flag for a rejected result or failed save; inspect `arrangement_error` for the reason.
- `draft_review`: present when the prompt asked to save the draft ("save this draft and put it in my todo list"). It is the same object `POST …/drafts/review` returns. Nothing has been written.
- `memory_review`: present when the prompt asked to remember something. `{ "text", "destination", "max_bytes" }` — editable starting text (empty when no fact was stated). Nothing has been written, no model was called, and the request is not stored as a turn.
- `draft_context`: `{ "ticket_id", "available", "display_number", "version" }` — whether the saved draft named in `draft` could be read for this turn.

**Conversations (read only):**

| Endpoint | Returns |
|---|---|
| `GET /api/home-assistant/conversations` | `conversations` (`id`, `title`, `message_count`, `updated_at`) that belong to the current assistant in the current Personal HQ, and `manage_href`. |
| `GET /api/home-assistant/conversations/{id}` | `conversation`, `messages` (`id`, `role`, `content`, `created_at`, `imported`), `truncated`, and `saved` — the Ticket drafts saved from it (or `saved_unavailable`). Canonical arrangement history is separate: `arrangements` (bounded record/message references) or `arrangements_unavailable`. |
| `GET /api/home-assistant/conversations/{id}/arrangements/{arrangement_id}` | One saved `arrangement` summary, `conversation_id`, `message_id`, and `context_current`; no query arguments. A PA proposal also returns `execution`: `{ state, reviewable, declinable, groups[] }`, each group `{ operation, name, members, state, offer_id?, route?, workspaces? }`. |

Conversations are canonical Sessions; they are renamed and deleted through the
Sessions API. Deleting one never deletes a saved Ticket or a remembered fact.

**Organize inside a Home (handoff):** `GET /api/assistant-program/library-homes`
(owner-only, `no-store`, no arguments) returns `homes: [{ id, name, route }]` for
the owner's active program Homes whose library is initialized. The PA's Suggest
arrangement panel links to each Home's **Organize library**; nothing is shared,
planned, initialized or read from a catalog.

**PA arranged groups (owner-only, `Cache-Control: no-store`):**

| Endpoint | Request | Effect |
|---|---|---|
| `POST /api/home-assistant/conversations/{id}/arrangements/{arrangement_id}/group-review` | `{ "revision": "<current folder revision>", "expected_record_revision": 1, "operation": 0 }` | Opens the existing conversation-bound folder setup review for one `create_group` intention and returns `conversation`, `folder_context` (new revision and `offer_id`) and `arrangement_review`. Creates nothing and calls no model. |
| `POST /api/home-assistant/conversations/{id}/arrangements/{arrangement_id}/decline` | `{ "expected_record_revision": 1 }` | Marks a current, unapplied proposal `cancelled` (revision + 1), closes its open group review, and returns the cancelled history. Nothing is undone. |

Unknown fields are refused. A group review requires: the proposal is open
(`draft`/`needs_more_evidence`) at that record revision, it is local and not
imported, the assistant state version is current, the conversation's current
folder event is the proposal's own observation, no other setup review is open
(`folder_review_pending`), the model-authored record's provider/model/registered
instance is unchanged (`arrangement_runtime_changed`), and the held scan still
holds every selected item (`arrangement_source_unavailable`). The review is a
version-2 `structure` with an `arrangement` binding (`record_id`,
`record_revision`, `operation`): exactly the selected immediate subfolders, all
plain workspaces, no threshold, no omitted tail and no Adjust. A member that is
already a workspace, moved, or became a link, a taken group name, the root, or
more than 24 folders returns `arrangement_group_unavailable` with that reason.
**Set up** on that review is the only confirmation: the existing
`POST /api/personal-assistant/folder-digest/offers/{offerID}/setup` digest check
covers the arrangement binding, and its guard rechecks the canonical record,
revision, binding and runtime before the run. A confirmed group is never
re-reviewed (`arrangement_group_confirmed`); declining after any group was
confirmed returns `arrangement_effects_applied`, and declining while a group's
review cannot be read back returns `arrangement_effects_unverified`. Only a
still-open review blocks the next group; a finished one does not. History's
`execution.declinable` says whether a decline may be requested. Group `state` is read from that
group's own review: `not_reviewed`, `awaiting_user_review`, `applying`,
`partially_applied` (use Continue on its card), `completed` (with the read-back
group `route` and `workspaces` count), or `closed` (nothing created).

**PA arrangement planning:** Add folder → Suggest arrangement → enter a goal →
explicitly choose bounded metadata sharing → Send organizing goal. Ordinary Send
remains discussion. Ask may explicitly request
`arrangement: { "share_held_metadata": true }` with a current conversation/draft
and live `folder_context`. This acknowledges additional bounded held-metadata
sharing, not discovery or execution. Picking a folder alone calls no model.
Only supported API transports run; CLI, historical/focused references and mixed
approvals are refused. A successful typed proposal is saved with its canonical
answered turn before the response includes an `arrangement` summary. No Apply
capability is exposed. The sharing object accepts only an exact boolean
`share_held_metadata` field and optional paired nonempty `expected_provider` /
`expected_model` strings (at most 160 bytes each). The PA browser sends both
runtime pins from its disclosure; a changed configured provider/model returns
`stale_context` before metadata sharing. Duplicate/aliased keys, an incomplete
pin pair and unrelated approvals are rejected. An explicit null, duplicated or case-aliased top-level `arrangement`
field returns HTTP 400 instead of falling through to ordinary chat/tools. An
absent field preserves ordinary Ask behavior. Goals follow the single-line,
2,000-UTF-8-byte intent bound.

Once arrangement mode is entered, refusals return `arrangement_error: { "code", "message" }` with safe host-authored
recovery guidance, never raw provider/database errors. Codes distinguish
`sharing_required`, `invalid_request`, `invalid_goal`, `source_required`,
`historical_source`, `focused_source`, `source_unavailable`,
`provider_unavailable`, `unsupported_transport`, `storage_unavailable`, `busy`,
`stale_context`, `cancelled`, `timed_out`, `limit_reached`, `storage_limit`,
`invalid_reply`, `save_failed`, and `planning_failed`. No refusal switches
providers, scans a source, applies effects or automatically retries. After an
uncertain save, reopen canonical history before retrying. Cleanup may remove only
the unchanged, unanswered conversation created by that failed first turn—not a
committed draft or another tab's work. See [the arrangement contract](../architecture/workspace-arrangement.md).

Owner-only Home history uses
`GET /api/workspaces/{workspaceID}/assistant-program/library/arrangements` and
`GET /api/workspaces/{workspaceID}/assistant-program/library/arrangements/{arrangementID}`.
**Home Organize library:** the initialized library shelf has an explicit planning
entry. `GET /api/workspaces/{workspaceID}/assistant-program/library/arrangements/context`
returns `home_id`, `revision`, `schema_version`, `agent_name`, `provider`, `model`
and `context_pin` for the owner's exact bound Manager. It checks pinned installed
guidance but calls no model, reads no catalog source files and performs no setup
or migration. Missing prerequisites refuse; role staffing alone does not silently
trust a workspace skill binding. The response is `no-store` and accepts no query arguments.

`POST /api/workspaces/{workspaceID}/assistant-program/library/arrangements/plan`
accepts only `goal`, `expected_revision`, `share_catalog_metadata` (must be `true`),
`allow_upgrade` (required `true` for a v1 library), and optional `expected_context`
(64 hexadecimal characters). To revise an existing Home proposal, also supply the
paired `revise_arrangement_id` and positive `expected_record_revision` fields.
These identify canonical intent, never a caller-supplied draft. The browser always sends the disclosed context pin;
a different current runtime/catalog refuses before metadata reaches the model.
This pin is a comparison value, not approval or a replacement for owner authorization. Duplicate/aliased fields,
null choices, caller-supplied identities and query arguments are rejected.
The authenticated owner invokes the actual bound local Manager's supported API
model with pinned installed role/skill guidance and the shared bounded Planner.
No general/native tools, global-agent fallback, scan, initialization or effects
are enabled. A successful response is the same canonical history projection as
GET; clarification is saved as discussion, not a proposal. Failed preparation,
drift or save returns a safe `arrangement_error`, never raw provider errors or an
unsaved result. A lost-response retry with the old revision conflicts without a
second model call; reopen history. The browser keeps the goal, separately requests
upgrade consent, and renders only canonical saved outcomes. Planning never applies
anything; the separate metadata and activation-selection review/confirmation path is documented below.

A revision rebinds the authorized prior proposal's exact membership to fresh
bounded selection handles and shares that proposal plus any pending question
with the disclosed model. Both catalog and proposal revisions are CAS-fenced;
a late response cannot overwrite a newer correction/commit or invalidate an
unrelated current review. A new proposal retains its canonical ID and increments
its revision, invalidating old approval. `revision_diff` contains only compact
operation-slot/member-reference change counts, exclusion/question flags and
base/new revisions—not old labels or member IDs, and not executed effects.
A clarification instead preserves the complete typed proposal and saves a
`revision_discussion` (requested goal, answer, provider/model, revision and time)
in the same envelope. The original `arrangement.goal` still describes the retained
intent; `revision_discussion.goal` describes the current request. Such references
carry `revision_discussion_pending: true` and cannot enter collection review
until a subsequent proposal resolves the question. Forget clears discussion;
removing a referenced collection stales its proposal and clears the pending
question. No model calls or automatic retries occur when choosing a revision
or reopening its history; fresh explicit sharing is required.

The list returns at most 32 record references; detail returns `arrangement` and
`catalog_current`. Home list/detail also includes `kind` (`proposal` or
`clarification`); a clarification detail includes bounded `clarification` text
unless Forget has redacted it. Clarifications contain no selected members or
proposed effects, and do not imply a saved actionable draft. The complete Home
detail projection is capped at 64 KiB. PA and Home arrangement history responses
use `Cache-Control: no-store`. These reads accept no query arguments, never initialize or
upgrade a library, and do not run or impersonate its Manager. An uninitialized
library returns 409.

The shared summary contains origin, saved timestamps, ID/revision/state, goal, rationale,
unknowns, coverage, proposed-operation names/counts and review questions. Explicit
unchanged/needs-review counts are not a partition of the full inventory. The
projection is capped at 64 KiB and omits member IDs, live handles and authority
fingerprints. `context_current`/`catalog_current` compare saved context only;
**`review_available` remains false**, even for a current record. Historical or
redacted records never restore source permission. This is not a compiled review.
Home references additionally carry `catalog_revision`. Completed metadata/selection
history may include `collection_receipt`: opaque per-effect consequences tied to
canonical operation/review witnesses, without copied entry labels or membership.
A `select_activation` consequence has `queue_id`, `selected_count` (1–100), an
empty `collection_id` and zero before/after collection revisions. It records queue
preparation, not project activation. History replay never resumes a discarded queue.

**Browse saved logical collections:** owner-only, no-store GETs under
`/api/workspaces/{workspaceID}/assistant-program/library/collections`.
The list returns at most 20 `collections` (ID, name, revision, member count and
optional originating arrangement ID), `revision`, `context_pin`, `offset`,
`total` and optional `next_offset`. Further list pages require
`?context_pin=…&offset=20` (offset multiples of 20).
`GET /collections/{collectionID}?context_pin=…&offset=0` returns at most 50
exact members, the collection card, and the same paging fields; member offsets
are multiples of 50. Rows contain ID, saved catalog name, format,
`last_observed_availability` and optional observation timestamp—not paths,
workspace-private state, song facts, grants or activation controls. Responses
are capped at 64 KiB and stale pins fail rather than mixing snapshots.
Refresh the list to obtain a current comparison pin; it grants no authority.
Unknown/duplicate query fields are rejected. These reads do not initialize,
migrate, scan or resolve a Manager/model; provider loss does not hide saved
metadata. Legacy v1 libraries return an empty collection list without upgrading.

**Home collection metadata and activation-selection review/confirmation:** these owner-only, no-store
routes do not call a model, scan sources, create workspaces/links or activate
projects. The actual Manager and current provider/runtime are independently
reauthorized; callers cannot supply them. Collection changes and one
`select_activation` intention may be saved atomically. Unsupported creator intent,
multiple activation selections, an existing active queue or selected linked
entries are rejected whole. Queue preparation grants no per-project approval. Base: `/api/workspaces/{workspaceID}/assistant-program/library/arrangements/{arrangementID}`.

- `POST /decline`: exact JSON `{ "expected_revision": N,
  "expected_record_revision": N }`. Closes a current unapplied proposal as
  `cancelled`, retaining its typed intent and any saved revision question. It
  never resolves a model, changes collections/queues, or undoes confirmed effects.
  Owner/Manager binding and both current catalog/proposal pins are checked; stale,
  foreign, completed and already-declined requests fail. An older proposal cannot
  invalidate another live review. Late model replies and old approval tokens
  cannot revive the declined proposal. Returns canonical history; uncertain
  responses require reopening history, not an automatic retry. Plugin/Manager
  authorization is still required, even though no model is needed.
- `POST /collection-review`: exact JSON `{ "expected_revision": N,
  "expected_record_revision": N }`. Persists a ten-minute review witness and
  returns its first membership page; does not change collections.
- `GET /collection-review/{reviewToken}?operation=0&offset=0`: read an exact
  effect's before/after membership, at most 50 entries. Offset must be a multiple
  of 50; `next_offset` identifies the next page. Unknown/duplicate query fields
  are rejected. All pages include exact effect names/counts, goal/rationale,
  coverage, unknowns, unchanged counts and outstanding review reasons. Member
  rows contain only ID, name, last-observed availability and before/after flags.
  A `select_activation` effect has `queue_id`, an empty `collection_id`, zero before
  members and the exact selected count as after members. Its pages show the future
  queue in stable ID order. Each project still needs independent exact-file,
  provider/grant/staffing review and explicit creation confirmation.
- `GET /collection-review/{reviewToken}/candidates?operation=0&offset=0&query=…`:
  owner-only saved-member picker, 20 entries per page with literal name search
  (at most 160 UTF-8 bytes). Offsets are multiples of 20. Returns review/catalog
  pins, operation, echoed query, total, ID/name/availability rows and `next_offset`.
  Supports create/set-members collection intentions and activation selections.
  Excludes existing proposed members, global unchanged selections and unresolved
  selections. Activation candidates additionally exclude already-linked entries.
  The token freezes the catalog and proposal; reads never discover,
  scan, grant access, call a model or mutate state. Unknown/duplicate fields fail.
- `POST /collection-correction`: exact JSON `{ "review_token": "…",
  "operation": 0, "name": "Optional new name", "exclude_ids": ["…"],
  "add_ids": ["…"] }`.
  Saves a **new proposal revision**, not collection effects. Name edits apply
  only to create/rename intentions; exclusions apply to create/set-members or
  activation-selection intentions and must be at most 50 distinct currently proposed IDs.
  Additions apply to those same membership intentions and accept at most 50
  distinct current Home catalog IDs not already selected. Activation corrections
  must retain 1–100 catalog-only entries; linked additions and an empty final
  selection are rejected atomically. They neither prepare a queue nor approve
  project creation, and leave other collection intentions unchanged. Global unchanged and
  unresolved selections cannot be overridden by this local editor. Local exclusions
  can be re-added in a later revision. Add/exclude overlap is refused. Any edit
  field may be omitted, but no-op, foreign-member and unsupported edits fail whole. A current, unconsumed review is required. The old token becomes
  unusable after correction; obtain and explicitly confirm a fresh review.
  There is no implicit expansion, model call, filesystem access or activation.
  Instead of member edits, exactly one structural intent change may be sent:
  `"convert": "catalog_only"` removes the activation-selection intention named
  by `operation` (those projects stay catalog-only; collections are unchanged);
  `"convert": "select_activation"` adds one activation selection of the named
  create/set-members intention's current catalog-only members (1–100, linked
  entries excluded; refused if any activation or group intention exists);
  `"resolve_question": N, "resolution": "unchanged" | "dismiss"` settles
  unresolved question N, moving its items to explicit unchanged or leaving them
  unmentioned; `"release_unchanged": true` releases every explicit global
  unchanged item so a later correction may add it. Each saves a new revision
  with a count/flag-only `collection_correction` (`converted`, `resolved`,
  `released`) and requires a fresh review; none prepares a queue or applies.
  The response is canonical history with `collection_correction` (latest revision,
  operation index, rename flag, exclusion/addition counts and timestamp), retaining original
  authorship with distinct owner-correction attribution and no copied old names
  or excluded IDs. A failed/lost response requires canonical history recovery.
- `POST /collection-commit`: exact JSON `{ "review_token": "…",
  "idempotency_key": "…", "confirm": true }`. Atomically consumes the review,
  writes compiled collection metadata and an optional activation-review queue atomically,
  then returns canonical arrangement
  history with a receipt. Same-key retries return the stored outcome; a different
  key cannot reuse approval. No partial application of unsupported effects.

POSTs reject query arguments, duplicate/unknown/case-aliased fields, nulls,
coercions and trailing JSON. Review pages are bounded to 64 KiB; stale/expired
reviews refuse. A failed/lost commit response is not evidence that nothing was
saved: reopen canonical history before a fresh review. History remains readable
after provider loss; historical receipts never authorize another apply.

**Attached link (the composer's Add link):** one link per conversation, read
once by the host and held in memory, by id, for 30 minutes after its last use
(at most 20 per user). The observation is the same record the build pane keeps
(see [Attach a Link](#attach-a-link)); nothing behind the address is read.

| Endpoint | Body | Returns |
|---|---|---|
| `GET /api/home-assistant/link-context/availability` | — | `{ "available": true }` when the link observer is wired. |
| `POST /api/home-assistant/link-context/select` | `{ "url", "replaces"? }` | `{ "link_id", "observation" }`; `replaces` lets the previous link go. A refused address is `422` with `code: link` and a sentence to show. |
| `POST /api/home-assistant/link-context/detach` | `{ "link_id" }` | `{ "detached": true }`. Harmless twice. |

A turn carries `link_context`: `{ "link_id" }` on both `POST /api/home-assistant/route`
(which then routes it to the conversation, like a folder) and `POST /api/home-assistant/ask`.
The model gets the observation as untrusted reference data. An answered reply
returns `link_context` (`link_id`, `observation`) and one host action,
`{ "type": "workspace_build_from_link", "label": "Set up a workspace from this link", "arguments": { "url" } }`,
which the browser runs: it starts or joins the user's build (`POST /api/workspaces/build-sessions`
with no first message), attaches the same address (`POST …/{id}/link`), and opens
Create Workspace on it; when the build was already in progress the pane says the
link was added to it. The server never executes it. A route refusal other than a
lost link is `409 conversation_unavailable`, and the drawer keeps the link. A link that is no longer
held (expired, detached, a restart) refuses the turn before any model call:
`conversation.error` is `link_context_unavailable` and the client keeps the message.
A link never rides a confirmed action.

**Assistant operations ("Operate Ori"):** an explicit request such as "open the
Album workspace and select Mixer" lets the assistant ask Ori to carry out a
named operation in the app. The contract is
[`docs/architecture/assistant-operations.md`](../architecture/assistant-operations.md).

A conversation turn may send `operation`: `{ "tab_id", "watch" }` — which tab is
asking and whether it chose to let its view be moved. Neither is authority. The
response may carry:

- `operation`: the operation the stored turn requested, as the asking tab may
  see it (`id`, `kind`, `status`, `revision`, `context`, `watching`, `pending`,
  `commands`, `draft`, `receipts`, `expires_at`). `commands` are named actions
  (`open_workspace`, `select_agent`, `open_task_draft`) with host-resolved
  references; they are present only for the watching tab. A `kind: task`
  operation is `prepared` and carries `draft` (`revision`, `title`, `brief`,
  `agent_name`, `agent_node_id`): a proposal shown in the task form, not a
  task. No task exists until the user saves that form.
- `operation_error`: why a requested operation was not recorded
  (`turn_not_saved`, or an operation error code). Nothing will open.
- `operation_capability`: `{ "available", "reason" }`.

| Endpoint | Body | Effect |
|---|---|---|
| `GET /api/home-assistant/operations/capability` | — | `{ available, reason }`: `operations_unavailable`, `model_not_configured` or `provider_cannot_run_ori_tools` when unavailable. The same answer is in `GET /api/personal-assistant` as `availability.operations`. |
| `GET /api/home-assistant/operations/{id}?tab=<tab id>` | — | Returns `operation` as that tab may see it. An ended operation stays readable for a day. |
| `POST /api/home-assistant/operations/{id}/ack` | `command_id`, `revision`, `tab_id`, `outcome` (`acknowledged` or `unavailable`), `observed` (`path`, `agent_key`, `draft_revision`), `reason` | Records what the watching tab observed for one step. Steps are reported in order; a repeat of a recorded outcome changes nothing. It is view evidence only. |
| `POST /api/home-assistant/operations/{id}/watch` | `tab_id`, `revision`, `watch`, `adopt` | Turns watching on or off for that tab. Taking it from another tab needs `adopt`. Off cancels nothing. |
| `POST /api/home-assistant/operations/{id}/cancel` | `tab_id`, `revision` | Dismisses the operation. No workspace, agent or task is touched. |
| `POST /api/home-assistant/operations/{id}/start` | `tab_id` | The user's Start for the task this operation created. Start is claimed once: a repeat, another tab or a retry returns the current `operation` and starts nothing. `200` means claimed (`start_requested`) or already running (`started`), never "finished". A refusal is `409` with `operation_start_refused` or `operation_assignee`, the reason as `message` and `details.operation`; the task is untouched. |

**Saving a prepared task.** The task form saves a prepared draft through
`POST /api/orchestration/tasks` with two extra fields, `assistant_operation_id`
and `assistant_draft_revision`. They name the operation to bind and authorize
nothing. The create is validated as any other, the assignee must be one agent
in that workspace's own roster (never added, never replaced by the entry
agent), and the created task carries `assistant_operation_id`. One operation
creates one task: a repeated request answers `200` with
`{ "success": true, "task", "already_created": true }`. A create that names an
operation and carries a schedule, a parent or input tasks is refused with
`operation_not_plain`; one whose operation ended is refused and creates
nothing. After a bound save the operation is `reviewed` and its snapshot
carries `task` (`task_id`, then `run_id` and `claimed_at`). A task's run is
reported only from the task: `run_id` is the first run the task itself showed.

A started operation's snapshot also carries `outcome`, read from the task for
that response and never stored: `run` (`running`, `waiting`, `stopped`,
`succeeded`, `failed`, `cancelled`, `interrupted`, `unknown`), `result`
(`current`, `history`, `gone`, `none`) and `later_runs`. `result` is `current`
only when the task's present result is that of the operation's own execution;
after a rerun it is `history` or `gone`, so a later run's output is never
presented as the operation's. The operation's own execution is marked on the
task as `assistant_operation_run` (`pending`, then `executed_at` of its
record), written only by the task owner. A task the user started from its own
controls has no mark, and the operation reports no `outcome` for it.

Refusals are `{ "code", "message" }`: `operation_not_found` (404, also another
user's operation), `operation_conflict`, `operation_tab_mismatch`,
`operation_ended`, `operation_assignee`, `operation_start_refused`,
`operation_create_in_flight` (409), `operation_expired`,
`operation_invalidated` (410), `operation_invalid`, `operation_not_plain` (400)
and `operation_unavailable` (503). All responses are `Cache-Control: no-store`.

**Saved drafts:**

| Endpoint | Body | Effect |
|---|---|---|
| `POST /api/home-assistant/drafts/review` | `conversation_id`, `message_id` | Writes nothing. Returns `review`: `operation_id`, suggested `title`, exact `body`, `target` (the current Personal HQ), `source`, `placement`, `notes`, `limits`. |
| `POST /api/home-assistant/drafts/save` | `operation_id`, `title`, `body`, `target_workspace_id`, `source` | Creates one unassigned, unscheduled Backlog Ticket. Returns `receipt` (`ticket_id`, `display_number`, `version`, `href`, `created`, `changed_since`). The same request again returns the same Ticket with `created: false`. |
| `GET /api/home-assistant/drafts/{ticketID}` | — | Read only. Returns `draft` — the Ticket's current title, text, `digest`, state, `version`, `editable`, its source conversation and message, `newer_replies`, and `matches_source` (absent when the source reply could not be read) — and `conversation` (`id`, `available`, `reason`): whether its source conversation can still be continued. |
| `POST /api/home-assistant/drafts/{ticketID}/review` | `conversation_id`, `message_id` | Writes nothing. Returns `update`: `current` (what is saved now, with its `version` and `digest`) beside the proposed `title` and `body`. |
| `POST /api/home-assistant/drafts/{ticketID}/update` | `if_version`, `if_digest`, `title`, `body`, `target_workspace_id` | Changes the title and text of that Ticket only. `if_version` and `if_digest` — the reviewed `current.version` and `current.digest` — are both required. Returns `receipt` with `applied` (false when this exact update had already been applied). |

Refusals are JSON with `error` (a code) and a `message` written for the user,
plus `field` when one input is at fault: `assistant_not_ready`,
`draft_save_unavailable`, `target_unavailable`, `target_changed`,
`source_message_not_found`, `source_not_savable`, `invalid_draft`,
`operation_conflict` (409: the same review ID with different text),
`saved_draft_not_found`, `saved_draft_changed` (409: the Ticket's version or
text changed since the review; `current` carries what is saved now and nothing
is overwritten), and `saved_draft_not_editable` (409: work has started or the
Ticket is closed). An `operation_conflict` carries `saved`, the Ticket the
earlier save created. These routes read and edit saved assistant drafts only; an
ordinary Ticket is not found through them.

**Remember…** adds no route. The reviewed fact is saved with the existing
`POST /api/personal-assistant/knowledge/explicit` (`state_version`,
`request_id`, `category`, `text`) and is edited or forgotten through the
existing reviewed-memory routes.

## Settings API

### Get Agent Settings

Get configuration settings for the requested agent context. When `agent` is omitted, the server uses the Assistant runtime (`Ori`) if present, otherwise the first available agent.

**Endpoint:** `GET /api/settings`

**Response:**
```json
{
  "model": "gpt-4o",
  "temperature": 0.7,
  "plugins": {
    "math": {
      "version": "1.0.0",
      "path": "uploaded_plugins/math.so"
    }
  }
}
```

### Update Agent Settings

Update configuration for the requested agent context. When `agent` is omitted, the server uses the Assistant runtime (`Ori`) if present, otherwise the first available agent.

**Endpoint:** `POST /api/settings`

**Request Body:**
```json
{
  "model": "gpt-4o-mini",
  "temperature": 0.5
}
```

**Response:**
```json
{
  "success": true,
  "message": "Settings updated successfully"
}
```

### Get API Key Info

Get masked information about the current API key.

**Endpoint:** `GET /api/api-key`

**Response:**
```json
{
  "api_key_set": true,
  "api_key_preview": "sk-...abc123",
  "source": "settings_file"
}
```

### Update API Key

Set or update the OpenAI API key.

**Endpoint:** `POST /api/api-key`

**Request Body:**
```json
{
  "api_key": "sk-your-new-api-key-here"
}
```

**Response:**
```json
{
  "success": true,
  "message": "API key updated successfully"
}
```

### Reset: Confirming the Agents Folder

A reset is reviewed first (`GET /api/reset/preview?intent=...&category=...`)
and then confirmed (`POST /api/reset` with `preview_id`, `request_id`, and
`"confirmation": "RESET"`). When the reviewed scope removes the user's agents
from their Workspace Directory (the `agents` category, or Start Fresh), the
preview carries:

```json
"agents_folder": {
  "path": "/Users/me/Ori Workspaces/Agents",
  "notice": "This folder is in your Workspace Directory, ... Other files there are kept.",
  "confirmation_required": true
}
```

and the confirmation must repeat that path:

```json
{ "preview_id": "...", "request_id": "...", "confirmation": "RESET",
  "confirm_agents_folder": "/Users/me/Ori Workspaces/Agents" }
```

Without it, or with any other path, the reset is refused before anything is
admitted: `409` `{"code": "agents_folder_confirmation_required"}`. The reset
removes each agent folder (one holding `agent_settings.json`) and all runtime
state in `<data dir>/agent_state/`; every other file in `Agents/` is kept.

## Vault API

Vault endpoints manage encrypted private records, workspace-scoped persistent grants, and password-protected exports.

### Get Vault Status

Return the current vault status, secure-storage backend, lock state, and record count.

**Endpoint:** `GET /api/vault/status`

**Response:**
```json
{
  "available": true,
  "locked": false,
  "writable": true,
  "requires_passphrase": false,
  "message": "using passphrase-protected fallback secret store",
  "record_count": 3,
  "secret_store": {
    "backend": "passphrase_fallback",
    "available": true,
    "writable": true,
    "locked": false
  }
}
```

### Unlock Vault

Unlock the vault when passphrase fallback mode is active.

**Endpoint:** `POST /api/vault/unlock`

**Request Body:**
```json
{
  "vault_password": "my-vault-password"
}
```

### Lock Vault

Clear the current in-memory vault unlock state.

**Endpoint:** `POST /api/vault/lock`

### List Vault Records

List saved vault entries. Results include decrypted metadata such as label and tags, but not the sensitive payload body.

**Endpoint:** `GET /api/vault/records`

**Optional Query Parameters:**
- `workspace_id` or `studio_id`: Restrict records to one workspace
- `type`: Restrict records to a single record type
- `actor_type`, `actor_id`: Apply workspace-grant enforcement for a specific agent or plugin actor

**Response:**
```json
{
  "records": [
    {
      "id": "record-123",
      "type": "email_snippet",
      "workspace_id": "ws-finance",
      "label": "Tax Email",
      "tags": ["email", "private"],
      "source": "manual",
      "retention_policy": "keep_until_revoked",
      "created_at": "2026-03-24T10:30:00Z",
      "updated_at": "2026-03-24T10:30:00Z"
    }
  ],
  "count": 1
}
```

### Create Vault Record

Create a new encrypted vault entry.

**Endpoint:** `POST /api/vault/records`

**Request Body:**
```json
{
  "type": "personal_note",
  "workspace_id": "ws-1",
  "label": "Passport",
  "tags": ["travel", "private"],
  "source": "manual",
  "retention_policy": "keep_until_revoked",
  "payload": {
    "number": "X1234567"
  }
}
```

### Get Vault Record

Fetch a single vault record, including the decrypted payload.

**Endpoint:** `GET /api/vault/records/{id}`

**Optional Query Parameters:**
- `workspace_id` or `studio_id`
- `actor_type`
- `actor_id`

### Update Vault Record

Update encrypted record metadata and, optionally, the payload body.

**Endpoint:** `PATCH /api/vault/records/{id}`

**Request Body Example:**
```json
{
  "label": "Passport Updated",
  "tags": ["travel", "private"],
  "payload": {
    "number": "X7654321"
  }
}
```

### Delete Vault Record

Delete a saved vault record.

**Endpoint:** `DELETE /api/vault/records/{id}`

### List Persistent Grants

List workspace-scoped persistent grants.

**Endpoint:** `GET /api/vault/grants`

**Optional Query Parameters:**
- `workspace_id` or `studio_id`

### Create Persistent Grant

Grant a workspace agent or plugin persistent access to a vault capability.

**Endpoint:** `POST /api/vault/grants`

**Request Body:**
```json
{
  "workspace_id": "ws-finance",
  "actor_type": "agent",
  "actor_id": "finance-agent",
  "capability": "vault.email.read_saved",
  "record_type": "email_snippet"
}
```

### Delete Persistent Grant

Revoke a persistent grant by ID.

**Endpoint:** `DELETE /api/vault/grants/{id}`

### Export Vault

Create an encrypted export bundle. Exports require explicit confirmation and a vault password.

**Endpoint:** `POST /api/vault/export`

**Request Body:**
```json
{
  "workspace_id": "ws-finance",
  "vault_password": "my-vault-password",
  "confirm": true
}
```

**Response:**
```json
{
  "version": 1,
  "workspace_id": "ws-finance",
  "salt": "base64-salt",
  "nonce": "base64-nonce",
  "ciphertext": "base64-ciphertext",
  "exported_at": "2026-03-24T10:45:00Z",
  "record_count": 1,
  "grant_count": 1
}
```

## Chat API

### Send Message

Send a message to the Assistant or a session-pinned specialist and get a response.

**Endpoint:** `POST /api/chat`

**Request Body:**
```json
{
  "question": "What is 2 + 2?"
}
```

**Response:**
```json
{
  "response": "2 + 2 equals 4.",
  "toolCalls": [
    {
      "function": "math",
      "args": "{\"operation\": \"add\", \"a\": 2, \"b\": 2}",
      "result": "4"
    }
  ]
}
```

**Special Commands:**

Send special commands for system information:

```json
{
  "question": "/agent"
}
```

Response includes agent status dashboard.

```json
{
  "question": "/tools"
}
```

Response includes available tools and functions.

## Updates API

### Check for Updates

Check if software updates are available.

**Endpoint:** `GET /api/updates/check`

**Response:**
```json
{
  "update_available": true,
  "current_version": "v0.0.2",
  "latest_version": "v0.0.3",
  "release_notes": "Bug fixes and performance improvements"
}
```

### List Available Releases

Get a list of all available releases.

**Endpoint:** `GET /api/updates/releases`

**Response:**
```json
{
  "releases": [
    {
      "version": "v0.0.3",
      "release_date": "2024-01-15",
      "notes": "Bug fixes and performance improvements"
    },
    {
      "version": "v0.0.2",
      "release_date": "2024-01-01",
      "notes": "Initial release"
    }
  ]
}
```

### Download Update

Download and install a software update.

**Endpoint:** `POST /api/updates/download`

**Request Body:**
```json
{
  "version": "v0.0.3"
}
```

**Response:**
```json
{
  "success": true,
  "message": "Update downloaded and installed successfully"
}
```

### Get Version Information

Get current version information.

**Endpoint:** `GET /api/updates/version`

**Response:**
```json
{
  "version": "v0.0.2",
  "build_time": "2024-01-01T12:00:00Z",
  "git_commit": "abc123def456"
}
```

## Blueprint Intake API

Blueprint Intake routes are workspace-scoped. `{intakeKey}` must name an intake
copied into the workspace's template provenance. A manifest selects no runtime
behavior: collection, parsing, consent, execution, validation, review, and
application are host-owned. File text, skill output, and labelled text/link
setup answers are untrusted input; only a reviewed proposal hash can authorize
record creation. Text and link setup answers never reach scaffolded files. A
URL setup answer that names this intake appears as a prefilled link but is not
fetched until consent.
Proposals may contain `ticket`, `memory`, `note`, and `calendar_event` items. Invalid memory entries remain visible as unusable. Calendar items remain visible but disabled when the workspace has no connected calendar with `create_event` mapped. Re-intake proposals classify items as `new`, `changed`, `unchanged`, or `no_longer_found`; changed records may require an explicit `conflict_resolution` of `keep_current` or `use_proposed`. Applying selected items returns an independent `created`, `updated`, `failed`, or `skipped` result for each item.

| Method | Endpoint | Purpose |
| --- | --- | --- |
| `GET` | `/workspaces/{workspaceID}/blueprint-intakes/pending` | List pending proposals for the Command-view review banner. |
| `GET` | `/workspaces/{workspaceID}/blueprint-intakes/{intakeKey}` | Read sources, consent, skill readiness, run progress, and the current proposal. |
| `POST` | `/workspaces/{workspaceID}/blueprint-intakes/{intakeKey}/sources/files` | Add one multipart file source. |
| `POST` | `/workspaces/{workspaceID}/blueprint-intakes/{intakeKey}/sources/links` | Fetch and snapshot one consented public HTTP(S) page. Body: `{"url":"https://…"}`. |
| `POST` | `/workspaces/{workspaceID}/blueprint-intakes/{intakeKey}/sources/folder` | Read supported immediate files from a consented native-picker selection. Body: `{"selection_token":"…"}`. |
| `POST` | `/workspaces/{workspaceID}/blueprint-intakes/{intakeKey}/consent` | Accept the host-authored provider disclosure. |
| `POST` | `/workspaces/{workspaceID}/blueprint-intakes/{intakeKey}/skill/trust` | Trust and enable the reviewed bundled skill, with an explicit `existing` or `bundled` choice on collision. |
| `POST` | `/workspaces/{workspaceID}/blueprint-intakes/{intakeKey}/run` | Start one tool-less entry-agent task per parsed source. |
| `POST` | `/workspaces/{workspaceID}/blueprint-intakes/{intakeKey}/run/cancel` | Cancel unfinished source runs while preserving returned results. |
| `GET` | `/workspaces/{workspaceID}/blueprint-intakes/{intakeKey}/proposal` | Read the current pending, applied, or skipped proposal. |
| `POST` | `/workspaces/{workspaceID}/blueprint-intakes/{intakeKey}/proposal/apply` | Apply selected reviewed items. Requires `proposal_hash` and an `items` array. |
| `POST` | `/workspaces/{workspaceID}/blueprint-intakes/{intakeKey}/proposal/skip` | Explicitly skip the reviewed proposal. Requires `proposal_hash`. |

## Examples

### Complete Workflow Example

Here's a complete example of creating an agent and chatting with it:

```bash
# 1. Create a new agent
curl -X POST -H "Content-Type: application/json" \
  -d '{"name": "my-test-agent"}' \
  http://localhost:8765/api/agents

# 2. Switch to the new agent
curl -X PUT http://localhost:8765/api/agents?name=my-test-agent

# 3. Chat with the agent
curl -X POST -H "Content-Type: application/json" \
  -d '{"question": "What is 10 + 15?"}' \
  http://localhost:8765/api/chat
```

### JavaScript Client Example

```javascript
class OriAgentClient {
  constructor(baseUrl = 'http://localhost:8765/api') {
    this.baseUrl = baseUrl;
  }

  async createAgent(name) {
    const response = await fetch(`${this.baseUrl}/agents`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name })
    });
    return response.json();
  }

  async chat(question) {
    const response = await fetch(`${this.baseUrl}/chat`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ question })
    });
    return response.json();
  }

  async getAgentStatus() {
    return this.chat('/agent');
  }

  async getAvailableTools() {
    return this.chat('/tools');
  }
}

// Usage
const client = new OriAgentClient();

// Create and use agent
await client.createAgent('my-agent');
const response = await client.chat('Hello, how can you help me?');
console.log(response.response);
```

### Python Client Example

```python
import requests
import json

class OriAgentClient:
    def __init__(self, base_url='http://localhost:8765/api'):
        self.base_url = base_url

    def create_agent(self, name):
        response = requests.post(
            f'{self.base_url}/agents',
            json={'name': name}
        )
        return response.json()

    def chat(self, question):
        response = requests.post(
            f'{self.base_url}/chat',
            json={'question': question}
        )
        return response.json()

    def get_agent_status(self):
        return self.chat('/agent')

    def get_available_tools(self):
        return self.chat('/tools')

# Usage
client = OriAgentClient()

# Create agent and chat
client.create_agent('python-agent')
response = client.chat('What tools do you have available?')
print(response['response'])
```

## Error Handling

When working with the API, always check for errors:

```javascript
async function safeApiCall(apiFunction) {
  try {
    const response = await apiFunction();
    if (!response.success) {
      console.error('API Error:', response.error);
      return null;
    }
    return response.data || response;
  } catch (error) {
    console.error('Network Error:', error);
    return null;
  }
}

// Usage
const result = await safeApiCall(() => client.chat('Hello'));
if (result) {
  console.log(result.response);
}
```

## Project Templates

Project templates are folder skeletons that can be instantiated into a new workspace as its **project** (recorded as the workspace's `project_path`, a path relative to the workspace folder). The mechanism is domain-blind: the server copies bytes and substitutes `{{name}}` and `{{date}}` in file/folder **names only** — all domain specificity lives in the template's contents. See [Project Templates](../features/project-templates.md) for authoring details.

**List the template library:**

```http
GET /api/project-templates
```

```json
{
  "templates": [
    {
      "id": "research-project", "name": "Research Project", "description": "Synthesis docs...",
      "icon": "📚", "behavior_profile": "research", "builtin": true, "has_skeleton": false,
      "tags": ["research"],
      "starter_tasks": [{ "description": "Build the synthesis doc", "details": "..." }],
      "tools": { "skills": [], "mcp_servers": [], "plugins": [] }
    },
    {
      "id": "reaper-song", "name": "Reaper Song", "description": "A minimal REAPER project...",
      "icon": "🎵", "behavior_profile": "general", "builtin": true, "has_skeleton": true,
      "project_entry": {
        "relative_path": "{{name}}.rpp",
        "open_after_create_default": true
      }
    }
  ],
  "templates_root": "/path/to/templates"
}
```

The library directory is configurable via the `templates_root` setting (then the `ORI_TEMPLATES_DIR` environment variable, then `<data dir>/templates`). Every immediate subfolder is a template. The optional `template.json` carries declarative metadata: `name`, `description`, `tags`, `icon`, `behavior_profile` (`general` | `research` | `software_project`), `starter_tasks` (each task may set `setup: true`; at most one per template — it auto-starts on the workspace's first open), `tools`, `agents`, `project_entry`, `builtin`, and `builtin_version`. `project_entry.relative_path` must be a portable path to a regular scaffold file and may use only `{{name}}` and `{{date}}`; `open_after_create_default` controls the initial Create Workspace checkbox state. Invalid hand-authored entry metadata is warned and omitted from normalized output. A legacy `onboarding` block from the removed intake engine is ignored, surfaced as a load-time warning, and stripped on the next authoring save. `has_skeleton` is derived (false ⇒ a metadata-only template that scaffolds no project folder). Built-ins (`builtin: true`) are read-only.

**Create a workspace with a project** — `POST /api/workspaces` accepts three additional optional fields:

```json
{
  "name": "Song X",
  "description": "...",
  "template_id": "reaper-song",        // library template, or:
  "template_path": "/any/folder",      // arbitrary folder as template (mutually exclusive with template_id)
  "project_name": "Midnight"           // optional; defaults to the workspace name
}
```

The project folder is created inside the workspace folder as a sibling of `files/` and `notes/`, and `project_path` is persisted in `workspace.json` (its canonical store). A verified entry is resolved with the same filename tokens and stored as the portable relative value `shared_data.project_entry_path` in that same canonical workspace record. Instantiation failures are **non-fatal**: the workspace is still created and the response carries a `project_warning` string. An entry-only verification or persistence failure also returns `project_warning` but keeps the successfully scaffolded project. The field is absent when there is no warning. Group workspaces reject template fields with `400`. A `project.created` event is published on success.

**Chat tools** — workspace chats expose `workspace_project_templates` (list) and `workspace_create_project` (`template_id`, optional `name`), which instantiate into the *current* workspace through the same engine. The create tool refuses when the workspace is a group or already has a project.

**Add a project to an existing workspace:**

```http
POST /api/workspaces/:id/project
{ "template_id": "reaper-song", "project_name": "Midnight" }   // or "template_path"
```

Responds `201` with `project_path` and the refreshed workspace; when entry-only verification fails, the successful response also includes `project_warning`. Returns `409` when the workspace already has a project and `400` for groups/invalid templates. The created project is also registered as the workspace's primary linked directory. This endpoint records entry metadata but never launches an application.

**Open the persisted project entry in the system default application:**

```http
POST /api/workspaces/:id/project/open
```

The request must come from a direct IPv4 or IPv6 loopback peer and must have no request body or caller-supplied path. Forwarded headers cannot turn a remote request into a local one; a loopback proxy that explicitly identifies a remote client is rejected. The server reads only canonical `project_path` and `shared_data.project_entry_path`, resolves the workspace folder through workspace storage, and immediately revalidates relative paths, containment, regular-file type, and symlink boundaries before invoking the operating-system file opener.

A successful request returns `200` with an acceptance message. Acceptance means the OS open request was issued; it does not prove the target application finished loading or that its automation/control interface is ready. Common errors are `400` for a body or unsafe/malformed persisted metadata, `403` for a non-local peer, `404` for missing workspace/project/entry data or file, `405` for another method, `503` when folder/opening support is unavailable, and `500` when the operating-system opener fails. Errors never mutate or delete the workspace.

**Template setup task (first-open auto-start):**

```http
POST /api/workspaces/:id/template-setup/start
```

Called by the workspace detail page on load. Finds the seeded starter task marked `setup: true` that has not yet auto-started, stamps a once-only consumed marker in the task's context (`template_setup_autostart_consumed_at`), and starts it through the same path as a manual task start. Responds `{ "success": true, "started": true, "task_id": "..." }` on the first open, and `{ "started": false, "reason": "already_consumed" | "no_setup_task" | "unassigned" | "start_failed" | ... }` otherwise — idempotent by design, so reloads and concurrent tabs never re-run setup. An unassigned setup task (created with `create_template_agents: false`) is left unconsumed until an agent joins and the claim sweep assigns it.

**Managing the library:**

```http
POST   /api/project-templates                 { "name": "Display Name" }   // create a blank (metadata-only) template
POST   /api/project-templates/import          { "path": "/any/folder", "name": "Display Name" }
POST   /api/project-templates/:id/duplicate   { "name": "..." }   // editable copy (a built-in's copy is never builtin)
PUT    /api/project-templates/:id             { "name", "description", "tags", "icon", "behavior_profile", "starter_tasks", "project_entry" }
DELETE /api/project-templates/:id             → { "success": true, "trashed": true|false }
POST   /api/project-templates/reveal          { "id": "..." }   // empty id opens the library root (local-first)
```

Mutating a built-in template (`PUT`/`DELETE`, file edits, tools/agents) is rejected with `403` — duplicate it first. For `project_entry`, omission preserves the current value, an object validates/replaces it, and `null` clears it; invalid values return `400`. The same `template.json` config (`behavior_profile`, `starter_tasks`, `tools`, `agents`, `project_entry`) is also editable via the `/templates` page; see [Project Templates](../features/project-templates.md).

Import copies the folder **verbatim** (no token substitution — `{{name}}` in file names is preserved for instantiation time; symlinks skipped). Delete prefers the system Trash; deleted starter templates are re-materialized on the next server start. Metadata updates preserve unknown `template.json` fields.

**Settings:** `GET`/`POST /api/settings/templates-root` mirrors the workspace/vault root endpoints (`templates_root`, `effective_templates_root`, `default_templates_root`, `source`). Changing the root materializes the library (including absent starters) in the new location.

## Tags API

Tags form one normalized vocabulary (trimmed, lowercased, ≤64 chars, ≤20 per entity) shared by **workspaces**, **chat sessions**, **notes**, **tasks**, and **project template manifests**. Workspaces, notes, and tasks accept a `tags` array on their create/update endpoints; sessions keep their dedicated `PUT /api/sessions/:id/tags`. Workspace tags live canonically in `workspace.json`, note tags are mirrored into the note file's YAML frontmatter (`tags:` list, Obsidian-compatible), and task tags appear in the task markdown metadata (`tags=` key).

**List tags:**

```http
GET /api/tags                  // sessions-only (legacy shape, unchanged)
GET /api/tags?scope=all        // unified pool across all sources
```

```json
{
  "tags": [
    {
      "name": "music",
      "counts": { "workspaces": 2, "sessions": 1, "notes": 3, "tasks": 1, "templates": 1 },
      "total": 8
    }
  ]
}
```

The unified pool is computed on request (no caches to refresh), counts each entity once per tag, and is sorted by `total` descending, then name. It powers the shared tag-input suggestions and the Settings → Tags management table.

**Usage preview** (for confirmation dialogs):

```http
GET /api/tags/usage?tag=music
```

```json
{ "tag": "music", "counts": { "...": 0 }, "total": 8, "templates": ["Reaper Song"] }
```

`templates` lists the display names of library templates whose manifest declares the tag.

**Rename a tag globally:**

```http
POST /api/tags/rename
{ "from": "musci", "to": "music" }
```

```json
{ "success": true, "from": "musci", "to": "music", "renamed": { "workspaces": 1, "sessions": 2, "notes": 1, "tasks": 0 } }
```

Renaming applies to workspaces, sessions, notes, and tasks — including their synced files (`workspace.json`, note frontmatter, task markdown). Renaming onto an existing tag **merges** (per-entity dedupe). Template manifests are **read-only**: they are never modified, so new workspaces created from a declaring template reintroduce the original tag.

**Delete a tag globally:**

```http
POST /api/tags/delete
{ "tag": "obsolete" }
```

```json
{ "success": true, "tag": "obsolete", "removed": { "workspaces": 1, "sessions": 0, "notes": 2, "tasks": 1 } }
```

Both mutations return per-source affected counts; an unknown tag is a no-op `200` with zero counts. `400` for missing fields, an over-long `to`, or `from == to`.

## Workspace Groups

A **group** is a workspace whose `kind` is `"group"`. Groups are full workspaces that can additionally contain member workspaces (physical folder nesting under `sub-workspaces/`). They support everything a concrete workspace supports — chat sessions, notes, tasks, agents, MCP/skill bindings, settings, and `project_path` — with one structural rule: **only groups can be parents** (`parent_id` must reference a group).

**Creating a group:**

```http
POST /api/workspaces
Content-Type: application/json

{
  "name": "Client Work",
  "kind": "group",
  "description": "Shared client initiative",
  "group_roster": true,
  "create_template_agents": true,
  "template_agent_review": {
    "version": 1,
    "plan_revision": "<revision returned by template-agent-plan>",
    "expectations": [
      { "index": 0, "name": "Client Work Manager", "action": "create" }
    ]
  }
}
```

Ordinary **Create Group** uses **Details → Group Roster → Review**. It first requests `POST /api/workspaces/template-agent-plan` with `{"group_roster":true,"group_name":"Client Work"}`, then presents the required, editable `"<Group Name> Manager"` entry agent (type `general`, role `orchestrator`) and optional saved teammates. No agent is created while the roster is edited. The final Create request supplies `group_roster: true`, `create_template_agents: true`, the plan revision/expectations, any `template_agent_overrides`, and selected `existing_agent_names`.

Groups require that reviewed roster. The server rejects a missing roster, `create_template_agents: false`, missing/stale review, project `team_intent`/`role_staffing`, or project template before creating anything. It then atomically creates or reuses exactly the reviewed definitions; an existing matching manager is an explicit reviewed reuse, not a collision-suffixed automatic agent. `create_template_agents` retains its ordinary template-team meaning for non-Group workspaces.

Group folders are provisioned with `sub-workspaces/` (members), plus their own `files/` and `notes/` directories. The `workspace-files` filesystem MCP binding is **scoped to `files/` and `notes/` only** — member sub-workspaces are never exposed to the group's agents. Groups created before this behavior existed are upgraded automatically by an idempotent backfill at server startup.

**Group Templates:** Create Group can also create — or reuse — the one Assistant Program Home an eligible project blueprint declares. See [Group Templates](../architecture/group-templates.md) for eligibility, identity and lifecycle.

```http
GET  /api/workspaces/group-templates
POST /api/workspaces/group-templates/review   {"group_template_id","revision","name"}
POST /api/workspaces/group-templates/commit   {"group_template_id","revision","name","group_review_token","idempotency_key"}
GET  /api/workspaces/{workspaceID}/group-template
```

- `GET /api/workspaces/group-templates` is read-only. It returns `group_templates` (General first, then `managed_home` entries with `id`, `revision`, `name`, `description`, `provider`, `proposed_group_name`, `home_roles` (each with `role_id`, `label`, `description`, `required`, `primary` and `default_name`), `project_roles_note`, `availability {state, reason, actions}`, `home {state, workspace_id, name}` and `required_home_roles`), plus `catalog_unavailable` and `dependency_state_unavailable`.
- Review and commit bodies are strict (4 KiB, unknown fields and trailing data → `400`). The server derives owner, source and program; General, a parent, a project template or team fields → `400`; a stale revision or source → `409`. Review creates nothing and returns `group_template_review {reuse, home_workspace_id?, home_name, summary, review_token, expires_at}`. Commit creates or reuses exactly one Home with no agents or project and returns `group_template {state: "home_ready", home_workspace_id, home_name, home_created, created_by_this_operation, name_applied, idempotent_replay}`. Retry a lost response with the identical body.
- `GET /api/workspaces/{workspaceID}/group-template` is read-only. For a group it returns `group_template {kind, name, template, provider, group, team, integration}`: `team.state` is `ready|incomplete|unverified|migration_required` from verified bindings only, and `integration.state` is `available|unavailable|unknown` with a `reason`. Ordinary groups report `kind: "ordinary_group"` (General). Non-groups → `400`.
- Group entries in `GET /api/workspaces` carry `group_template {kind, name, provider_kind, plugin_id}` only for program Homes.

**Listing and membership:** the flat `GET /api/workspaces` list includes groups (check `kind` to distinguish them); `GET /api/workspaces?tree=true` returns the nested tree. The UI's **Group selected** action first reviews and creates one rostered Group, then patches only its reviewed top-level member snapshot using `PATCH /api/workspaces/:id` with `parent_id` and `order_index`. A partial member move leaves the created group, its reviewed roster, and every successful move intact; a lost response is reconciled by refresh rather than retrying group creation.

**Renaming through the generic update:** a `name` field on `PUT`/`PATCH /api/workspaces/:id` renames the workspace exactly like the dedicated `POST /api/workspaces/:id/rename` endpoint — it moves the backing folder (and any nested/linked members) to the new slug, rewrites path-keyed references, and rolls SQLite back to the pre-rename name and slug if the folder rename fails. A blank name (after trimming) is rejected with `400 name is required`; a slug already owned by another workspace is rejected with `409` and leaves the existing name, slug, and folder untouched. Sending `name` together with `parent_id` in one request applies the move first, then the rename, so the workspace ends up in exactly one folder at the new parent with the new slug.

**Deleting a group** uses a two-mode flow:

```http
DELETE /api/workspaces/:id?confirm=true&delete_mode=group_only   (default)
DELETE /api/workspaces/:id?confirm=true&delete_mode=contents
```

- `group_only` — un-nests direct members back to the workspaces root (they stay active), then moves the group folder — with its own sessions, notes, and files — to the system Trash. Responds `{ "success": true, "id": "...", "trashed": true }`; restore with `POST /api/workspaces/:id/restore`. With `delete_sessions=true` (or on platforms without Trash support) the group is removed permanently instead.
- `contents` — moves the whole folder tree (group + members) to the Trash and marks every row trashed; restore reactivates the entire subtree. `delete_sessions=true` forces a permanent delete of everything including sessions.

Both modes hard-block with `409 Conflict` while any workspace in the group has active task work.

**Deleting a detached Required-template workspace** adds one lifecycle review without creating a separate deletion owner:

```http
POST   /api/workspaces/{id}/group-requirement/delete/review
       {"delete_sessions":false}
DELETE /api/workspaces/{id}?confirm=true
       X-Ori-Group-Requirement-Review: <review_token>
```

An unreviewed `DELETE` returns `409` with code `group_requirement_review_required`. The review is inert and expires after 15 minutes. It succeeds only when the canonical Required contract is structurally valid and the project has no live child link, Home reciprocal membership, or Assistant Home state. The returned impact names whether the exact request will use Trash or permanent deletion. The repeated `DELETE` consumes a token bound to the owner, workspace ID, complete contract snapshot, immutable contract operation digest, `delete_sessions` choice, and Trash/permanent mode. Changing any of those facts requires a new review. Ordinary Assistant disconnect/Home-removal review still runs first, and the existing external-folder deletion boundary remains unchanged.

## Workspace Build Sessions API

"Build with your assistant" (`tasks/prd-build-with-your-assistant.md`, architecture in `docs/architecture/workspace-build-sessions.md`): the Personal Assistant fills the Create Workspace wizard through a conversation in a pane beside it. A build session is a server-held draft that **is** the create request. The model proposes; the host validates every field; the wizard renders it through its own setters; the user confirms with the ordinary Create (`POST /api/workspaces` with `build_session_id`). Nothing here creates a workspace.

Bodies are capped at 256 KiB and must be one JSON object with only the fields named below (`400` otherwise). Errors carry a stable `code`:

| Status | `code` | Meaning |
| --- | --- | --- |
| `409` | `unavailable` | The assistant cannot build now (`reason`: `assistant_not_ready` or `no_model`), or the session store failed outside a turn. The client steps aside and the wizard stays manual. |
| `409` | `version` | The session changed since this `version` was read. Reload it (`POST /api/workspaces/build-sessions` returns the open one) and retry. |
| `409` | `closed` | The session was created or abandoned. |
| `409` | `choice` | The chip id is not the one currently offered. |
| `404` | `not_found` | Unknown session id. |
| `503` | `unavailable` | A turn could not be stored; it can be retried. |

### Check Availability

**Endpoint:** `GET /api/workspaces/build-sessions/availability`

```json
{ "available": false, "reason": "no_model" }
```

`available` is true when the assistant's relationship is `active` or `paused` and a model resolves: the assistant's own global agent profile (provider and model), then the system model. `reason` (only when unavailable) is `assistant_not_ready` or `no_model`. When available, `folders` says whether the pane may offer Add folder (the composer's folder chooser is wired for builds), and `links` whether it may offer Add link (the link observer is wired). The dialog reads this and `GET /api/personal-assistant` when an eligible opener shows it; nothing else is requested before build mode starts.

### Create or Resume

**Endpoint:** `POST /api/workspaces/build-sessions`

```json
{ "entry_point": "home_cockpit_create", "parent_id": "group-7", "first_message": "a newsletter from my research notes" }
```

- `entry_point` (required): `home_cockpit_create`, `workspace_map_build`, `workspace_hub_create`, or `personal_assistant_ask`. Any other opener is `400`.
- `parent_id` (optional): the group the opener was inside; kept only when it is one of the user's groups.
- `first_message` (optional, ≤ 2,000 characters): run as the first turn (the sentence typed in the assistant's composer).

There is at most one open build per user. When one is open it is returned unchanged with `200` and `"resumed": true`, and `first_message` is not run (the client asks Resume or Start over). Otherwise `201` with the new session:

```json
{
  "session": {
    "id": "7f0c…",
    "status": "open",
    "version": 3,
    "entry_point": "home_cockpit_create",
    "assistant": { "display_name": "Atlas", "appearance": { "…": "…" } },
    "draft": { "template_id": "content-production", "name": "Newsletter Desk", "description": "…" },
    "transcript": [
      { "role": "user", "text": "a newsletter from my research notes", "at": "…" },
      {
        "role": "assistant",
        "text": "I picked Content Production. Who should staff it?",
        "choices": [ { "id": "c1-1", "label": "Assign Luna as Content Lead" } ],
        "at": "…"
      }
    ],
    "pending_question": { "question": "Who should staff it?", "choices": [ "…" ], "allow_free_text": true },
    "applied": ["blueprint", "name", "description"],
    "team_patch": null,
    "team_state": null,
    "why": [ { "section": "blueprint", "text": "…" } ],
    "rejections": [],
    "needs_home": null,
    "furthest_step": 2,
    "ready": false,
    "create_now": false,
    "turn_count": 1
  }
}
```

- `draft` holds only create-request keys: `template_id`/`blank`, `name`, `description`, `mission` (the form's Goal), `blueprint_inputs`, `parent_id`, `color`, `tags`, `workspace_preset`, `workspace_bootstrap`, `project_connection`, `team_intent`, `role_staffing`, `template_agent_overrides`, `template_agent_review`, `existing_agent_names`, `entry_agent_name`, `create_template_agents`.
- `transcript` entries have `role` `user`, `assistant`, or `form` (the user's own form edits, said in plain words). Chip ids are the server's (`c<turn>-<n>`, plus the reserved `try_again`, `folder_choose`, `folder_none`); an answered question records `chosen`. The transcript keeps the last 40 entries and 32 KiB.
- `applied` names what the last turn set on the form (`blueprint`, `name`, `description`, `goal`, `inputs`, `parent`, `color`, `tags`, `team`); a `goal` is at most 300 characters and lands in `draft.mission`; `team_patch` is the assistant's staffing (`mode`, `roles[]` with `role_id`, `mode` `assign`/`create`, `agent_name`, and optional `provider`/`model`/`system_prompt`, `saved_agents[]`). `team_state` is the serialized Team step, restored on resume.
- `rejections` are the fields the host refused on the last turn, with a reason; the assistant sees them on its next turn and the transcript says so.
- `ready` is the model's opinion only; the wizard's own gates decide Create. `create_now` means the user said "create it": the client runs the ordinary Create submit and reports a closed gate in the pane instead of submitting. It is never set on the first turn, even when the first message asks to create — the user always sees a filled form first.

### Send a Turn

**Endpoint:** `POST /api/workspaces/build-sessions/{id}/turns`

```json
{ "text": "only a Content Lead", "version": 3 }
```

or

```json
{ "choice_id": "c1-1", "version": 3 }
```

Exactly one of `text` (≤ 2,000 characters) or `choice_id`. A chip is answered with its label. `"auto": true` marks the turn the client sends on its own after the user switched the blueprint on the form; it is never recorded as the user's request. Fields the user edited on the form while the model was working keep the user's value; the rest of the reply still applies. The user's turn is stored before the model runs, so a failed model call keeps it: the transcript then carries the fixed line "I couldn’t reach my model just now. Keep going on the form, or try again." with a `try_again` chip. A reply that neither changes the form nor asks a question is retried once, then replaced with "Which of these is closest?" and three blueprint chips — except a reply that answers "create it" with `create_now` alone after the first turn, which is the user's instruction carried out and stands as it is. `folder_choose` is `400` (choosing a folder happens on the form). Returns `200` with `{ "session": … }`.

### Report Form Edits

**Endpoint:** `PATCH /api/workspaces/build-sessions/{id}/draft`

```json
{ "draft": { "template_id": "research-project", "name": "Field Notes" }, "team_state": { "…": "…" }, "step": 2, "version": 4 }
```

The client sends the form's current create payload (only the draft keys above) about half a second after the user stops editing. The server keeps a blueprint only if this user can create from it. Each change is written once to the transcript as a `form` entry ("You renamed it to Field Notes"), which the assistant reads on its next turn. `"sync": true` marks a write that only reports what applying the assistant's own turn did to the form; it is recorded but never said back. `step` (1–4) records the furthest wizard step for resume; `team_state` is at most 32 KiB. The response is `{ "session": …, "blueprint_changed": true|false }`; the client answers a changed blueprint with an automatic turn so the assistant re-staffs the team.

### Folder Choices

**Endpoint:** `GET /api/workspaces/build-sessions/folder-choices`

```json
{ "available": true, "chips": [{ "id": "documents", "label": "Documents" }], "picker_available": false, "picker_note": "Pick a folder from the list for now." }
```

What the pane's Add folder sheet can offer: the composer's known-folder chips, whether the native folder dialog can open here, and the sentence that explains a missing one. `available` is false when the folder chooser is not wired for builds. Read only.

### Attach a Folder

**Endpoint:** `POST /api/workspaces/build-sessions/{id}/folder`

```json
{ "mode": "chip", "chip": "documents", "version": 3 }
```

`mode` is `chip` (with `chip`), `picker` (the server opens its folder dialog), or `selection` (with `selection_token`, the receipt `POST /api/folder-picker/select-path` issued for a typed path it checked). **No path is accepted** (`400`). The server looks at the folder the way the assistant's composer does — names, kinds, counts, project markers, last-changed dates, coverage; it never reads a file — and stores the path-free observation on the session as `folder.observation`, with a `form` transcript entry ("You attached the folder “X”. It will be linked to the workspace when you create it.") the assistant hears on its next turn. The model then sees the observation and may not ask its own folder question. `version` is checked before the dialog opens; the pick itself then stands. A cancelled dialog returns `200` with `{ "session": …, "cancelled": true }` and changes nothing. A refused folder (too broad, unreadable, the dialog off, a pick still running) is `409` with `code: folder` and a sentence to show; `code: version`, `closed`, and `not_found` as elsewhere. Returns `{ "session": … }`.

### Remove the Folder

**Endpoint:** `POST /api/workspaces/build-sessions/{id}/folder/detach`

```json
{ "version": 4 }
```

Clears the attached folder and says so in the transcript. Removing twice is harmless. Returns `{ "session": … }`.

### Attach a Link

**Endpoint:** `POST /api/workspaces/build-sessions/{id}/link`

```json
{ "url": "https://github.com/golang/go", "version": 3 }
```

One link per build; attaching another replaces it. The server reads what the address says about itself, once, and stores the bounded observation on the session as `link.observation`:

```json
{
  "version": 1, "id": "…", "url": "https://github.com/golang/go", "host": "github.com",
  "kind": "github_repo", "title": "golang/go", "description": "The Go programming language",
  "readable": true, "fetched_at": "2026-10-17T09:00:00Z",
  "repo": { "owner": "golang", "name": "go", "full_name": "golang/go", "language": "Go",
            "topics": ["go"], "default_branch": "master", "open_issues": 10354,
            "pushed_at": "2026-10-16", "reachable": "yes" }
}
```

- `kind` is `github_repo` (`github.com/owner/name`, any trailing path, or `git@github.com:owner/name.git`; read through the GitHub REST API with the stored GitHub token when connected and unlocked, anonymously otherwise; `reachable` is `yes`, `no` for a 404/403, or `unknown` for a rate limit or outage, and the link still attaches by name), `document` (a PDF, known by its address and never downloaded), or `page` (anything else: the `<title>` and meta or Open Graph description from the document head only).
- `readable: false` means only the address could be read (an image, an empty or script-only page, an error).
- Nothing behind the address — page text, README, issues — is read or kept. The observation is at most 4 KiB; title and description at most 200 characters.
- Refused (`422`, `code: link`, with a sentence to show): not http/https, longer than 2,000 characters, a private, loopback, link-local or `.internal`-style host, a non-default port, credentials or a token/key/signature in the address, or a redirect to any of those (each of up to three hops is checked).

The transcript gets a `form` entry naming the link by validated parts only — "You attached the GitHub repository owner/name." or "You attached a link to host." — never by its title, because the model hears form entries as the user's own words; the title appears only inside the untrusted observation. The model sees the observation as untrusted data and is told to name the workspace after it (never with a slash), set the Goal naming the source, and, for a repository, ask "Triage its issues" or "Work on the code" (Code Project asks for the folder; nothing is ever cloned). `code: version`, `closed`, and `not_found` as elsewhere. Returns `{ "session": … }`.

**At Create:** a `github_repo` link on a GitHub Ops build binds that repository to the new workspace's GitHub MCP binding (`scope.repo`), after the template's tool step, whether or not GitHub is connected yet; every link is added to the workspace's `Sources` note (its title with all Markdown punctuation escaped, its address, its kind, the date; never the page's description); and "How this was set up" gets a `link` decision that names the link by its validated parts. None of these can fail the create.

### Remove the Link

**Endpoint:** `POST /api/workspaces/build-sessions/{id}/link/detach`

```json
{ "version": 4 }
```

Clears the attached link and says so in the transcript. Removing twice is harmless. Returns `{ "session": … }`.

### Abandon

**Endpoint:** `POST /api/workspaces/build-sessions/{id}/abandon`

```json
{ "version": 5 }
```

Ends an open build without creating anything (Start over, or Discard from Today). Abandoning twice is harmless. A created or abandoned build keeps only its id, status, name, and counters; its conversation and draft are dropped. Closing the dialog never abandons: the build stays open for a week from its last change and is offered again from any eligible opener and from the assistant's Today view ("Finish building …").

### Create from a Build

`POST /api/workspaces` accepts `build_session_id`. After the workspace is created the session is marked `created` and a short summary is stored in the workspace's `template_provenance.build_summary`: `assistant_name`, `session_id`, `created_at`, `turn_count`, `user_request` (the first thing the user asked for), and `decisions` (one `{section, text}` per section — blueprint, details, team, placement). The team line describes the team in the create request, not the model's claim, and the assistant's reason for a section the user later changed on the form is left out. The build is named in the create request only once its draft is on the form: while "Resume building …?" is unanswered, a workspace made by hand is not tied to the paused build. `GET /api/orchestration/workspace?id=…` and the sessionhttp workspace detail return it as `build_summary`, and the workspace page shows it under "How this was set up". An unknown or closed `build_session_id` never blocks the create.

When the named build has an attached folder, Create links that folder to the new workspace in place — as a linked directory reference, and as its primary directory unless the create already set one outside the workspace folder — and the summary gains a `folder` decision. The folder is resolved through the server's own hold on the pick (the same 30-minute window the assistant's composer has) before anything is created: a pick the server no longer holds is `409` with `conflict.type: "build_folder"` and an `error` to show, and nothing is created. Nothing is copied, moved, or read in the linked folder.

## Personal Assistant Folder Digest API

"Show me a folder" (`tasks/prd-show-me-a-folder.md`): the user points the personal assistant at a folder, the server looks at the folder's shape (names, dates, sizes, kinds, project markers — it never opens a file) and makes one explained offer. Every folder is chosen on the server, from a chip identifier or the native dialog. **No endpoint here accepts a filesystem path**: a body with any `path`- or `folder`-named field is refused with `400` before anything is looked at. Available only for an `active` or `paused` assistant with a built HQ (`409` otherwise); `503` when the feature is not wired. Bodies are capped at 4 KiB and must be one JSON object with only the fields named below.

### Get Folder Digest

**Endpoint:** `GET /api/personal-assistant/folder-digest`

```json
{
  "folder_digest": {
    "offer": { "id": "…", "status": "pending", "verdict": "project", "…": "…" },
    "chips": [
      { "id": "downloads", "label": "Downloads" },
      { "id": "documents", "label": "Documents" },
      { "id": "desktop", "label": "Desktop" }
    ],
    "picker_available": true,
    "picker_note": "",
    "paused": false
  }
}
```

`first_task` and `first_look` carry where a folder's first look stands. `first_task` is on a **resolved project offer** whose workspace has a first task, in the plain read and in the `?offer_id=` read; `first_look` is on the plain read only, for the most recent folder workspace that still has one, whichever offer is shown. Both are read live and never stored on the offer, and with nothing to say they are absent. Shape:

```json
{
  "state": "seeded | running | waiting | finished | failed",
  "offer_id": "…", "task_id": "…", "workspace_id": "…",
  "workspace_name": "Thesis", "workspace_route": "/workspaces/thesis", "folder_name": "thesis-draft",
  "description": "Summarize the current draft…", "agent": "Writing Coach",
  "started_at": "…", "finished_at": "…",
  "result_excerpt": "one line, at most 280 characters",
  "ticket_route": "/workspaces/thesis?ticket=<task id>",
  "can_start": true,
  "reason": "setup_wizard_opening | unassigned | no_model | local_activation_required | assessment_unavailable | needs_input | start_failed | run_failed",
  "message": "one plain sentence for the reason",
  "detail": "what the receipt's task row says now",
  "recipe": "software_assessment (present only for a task that runs under a host recipe)",
  "can_retry": true,
  "repair_route": "/agents", "repair_label": "Choose a model",
  "next_request": "the one next task a finished assessment suggested, as plain text",
  "next_route": "/workspaces/thesis/plans?next=assessment",
  "report_route": "/workspaces/thesis/task/<task id>",
  "readiness": [ { "id": "folder | model | github", "label": "…", "state": "verified | configured | absent | unknown", "required": true, "detail": "…", "source": "…", "route": "…" } ]
}
```

`can_start` is true only for a `seeded` look that `POST …/folder-first-task/start` would start now (`no_model` is added when no model is configured). Routes are built from the folder slug, never an internal id, and no field carries a filesystem path. While a look runs, the Home card re-reads `?offer_id=` only while the drawer is open.

The last six fields are sent only for a task with a `recipe` (a software project's first assessment; see "The first assessment" under Review a Software Project's Setup). For such a task `reason` is also one of `assistant_unavailable`, `model_unavailable`, `model_unsupported`, `project_folder_unavailable`, `workspace_unavailable` (each must be repaired before a start or a retry) or `tool_call_refused`, `context_too_small`, `nothing_to_assess`, `timed_out`, `provider_configuration_required`, `provider_quota_exhausted` (how one run ended). `can_retry` is true only for a `failed` assessment that could be run again now; when something must be repaired first it is false and `repair_route`/`repair_label` name the existing page that repairs it. `detail` for one that cannot start says what it waits for ("Needs a model before it can start", "Needs the project folder back before it can start") and never that it is ready. `readiness` is read live without opening a project file or calling a model or GitHub: a model that is set reads `configured` ("set, not checked"), never `verified`, and a saved GitHub connection does not claim the repository can be reached. `next_request` is text the model wrote; `next_route` opens the workspace's Plans page with it placed, unsubmitted, in the "Start a plan" box. `report_route` is sent for a finished assessment and opens the task's own page, where the whole result is shown (the `ticket_route` detail lists the work and its runs but not what a run wrote).

`offer` is the one current offer (pending, or a decided one whose outcome is still being shown) or `null`. `chips` names the folders under the user's home that exist (none in a sandboxed home). `picker_available` is false where the native dialog cannot run — outside macOS, or under `ORI_NO_DESKTOP_OPEN` as every sandboxed demo server runs. `picker_note` explains what can still be chosen: it is empty only when there are chips and a dialog; otherwise it names the missing folders, the switched-off or unsupported dialog, or both.

### Scan a Folder

**Endpoint:** `POST /api/personal-assistant/folder-digest/scan`

```json
{ "chip": "downloads" }
```

or

```json
{ "picker": true }
```

or

```json
{ "selection_token": "<token from POST /api/folder-picker/select-path>" }
```

Exactly one of `chip`, `picker`, `file` or `selection_token`. Chips are `music`, `documents`, `desktop` and `downloads`, offered in that order for the folders that exist. With `picker`, the server opens the operating system's folder dialog and scans the chosen folder; a cancelled dialog returns `{ "cancelled": true }`. A `selection_token` names a folder the folder-picker endpoint already accepted, which is how a path the user typed gets here: `POST /api/folder-picker/select-path` with `{ "path": "~/Music/Projects" }` checks the path on a same-origin request (absolute or `~/`, an existing directory, not the home folder itself, not Ori's data or workspace root, not a credential folder) and answers `{ "success": true, "selected": true, "path": "…", "selection_token": "…" }`, with `warning: "outside_home"` and a `warning_note` sentence for a folder outside the home folder, or `422` with the refusal's sentence in `error`. The scan route itself still answers `400` to any `path` or `folder` key; an expired or unknown token is `400` with "That folder selection expired. Paste the path again." The response is `{ "offer": … }`: the new offer with its `verdict` (`project`, `dump`, `mixed`, `ambiguous`, `empty`, or `declined` for a folder the user asked never to be asked about), `reason` (counts only), `subject` (the ranked project, when any), `blueprint`/`blueprint_label`/`blueprint_note`, `partial`, and `remember`. A previous pending offer is moved to later. `400` for an unknown chip, a chip whose folder is missing, or a root that cannot be looked at (with `"repair": "choose_folder"`); `409` while another scan is running or when the dialog is unavailable.

### Open the Picker

**Endpoint:** `POST /api/personal-assistant/folder-digest/picker`

No body (any request data is `400`). Same as a scan with `{ "picker": true }`.

### Decide an Offer

**Endpoint:** `POST /api/personal-assistant/folder-digest/offers/{offerID}/decide`

```json
{ "decision": "yes", "choice": "project", "create": true, "request_id": "…" }
```

A recognized software project adds two refusals to a `yes` with `project`: `400` while the offer's `software.needs_candidate` is true (choose a candidate first, below), and `409` with `"project_exists": true` and the offer when a workspace of the user's already has this exact folder as its primary linked folder (the offer's `software.existing` lists it; no second workspace is made). `409` with `"needs_pick": true` also covers a folder that was deleted and made again at the same path since it was shown.

`decision` is `yes`, `no`, or `later`. `choice` (`project` or `tidy`) is required with `yes` when the verdict leaves both open. `no` never asks about that folder again; `later` asks again in a week. A `yes` with `tidy` runs the File Janitor setup for the folder on the server and the returned offer is `resolved` with `outcome.route` (the first review batch, or the workspace that already manages the folder). A `yes` with `project` and `create: true` — the card's confirmed plan ("Set up Thesis as a Writing project workspace?") — has the server set the workspace up itself (the folder's name, the shape's installed blueprint or blank, the folder linked as the primary directory, the first task seeded) and the returned offer is `resolved` with `outcome.workspace_id`, `outcome.route` and `outcome.blueprint`; a retried click reuses the workspace already made for the offer. It is offered only while the offer's `create_available` is true. A `yes` with `project` and no `create` (the card's Adjust…) leaves the offer `awaiting_outcome` until the resolve below. `request_id` makes a retry return the same result. `404` for an unknown offer; `409` when it was already answered; `503` when the workspace could not be created (the offer stays pending).

### Choose a Software Project

**Endpoint:** `POST /api/personal-assistant/folder-digest/offers/{offerID}/candidate`

```json
{ "candidate_id": "<id from software.candidates>", "request_id": "…" }
```

An offer about a recognized software project carries `software`: `recipe` (`software_project`), `marker` (the plain label of what marked it, such as `Go module`), `repository` (`directory` for an ordinary Git checkout, `file` for a worktree or submodule, absent when the folder is not under Git), and, when relevant, `needs_candidate` with `candidates`, `existing`, or `existing_unavailable`. Recognition reads directory entries only; no file in the folder is opened.

When the shown folder has no marker of its own and holds two or more software projects, `needs_candidate` is true and `candidates` lists them (`id`, `name`, `marker`, `repository`, and `current` on the provisional subject). Nothing can be set up until one is chosen. This action makes the named candidate the offer's subject and returns the offer; the others stay queued for later offers. It accepts exactly the two fields above. A candidate `id` is opaque and valid only for its own offer; there is no path or folder field, nothing is scanned again, and nothing is created. `400` for an id the offer did not list; `409` when the offer is no longer pending, was made from a conversation review (which already names one candidate), or has no choice outstanding; `409` with `"needs_pick": true` when the server no longer holds the folder.

`software.existing` lists the user's active workspaces whose primary linked folder is this exact directory (`workspace_id`, `name`, `route`, `software`, and `replaced` when the workspace was set up for a directory at this path that has since been replaced). A sibling worktree or a clone of the same repository is a different directory and is never listed. `existing_unavailable` means the server could not read which workspaces cover the folder, so it offers no setup.

An offer may also carry `enclosing` (`levels`, `name`) when the subject sits inside a larger repository. The server finds that by checking for a repository marker's name in at most six folders above the selection, stopping below the user's home folder; it lists nothing and opens nothing there, and the user must show that folder for Ori to look at it.

### Review a Software Project's Setup

**Endpoint:** `POST /api/personal-assistant/folder-digest/offers/{offerID}/review`

```json
{ "request_id": "…" }
```

A recognized software project is never set up from its name alone: a `yes` with `project` on the decide action answers `409` ("Review this project's setup first") and the offer stays pending; its setup is confirmed on the reviewed plan instead. Review reads a short, fixed list of the project's own files (its README, instruction files such as `AGENTS.md` and `CLAUDE.md`, build manifests, a `Makefile` or `justfile`, up to four CI workflow files, and Git's own branch and remote records) within fixed size and time limits. It runs nothing, follows no link out of the folder, never opens `.env` or key files, and replaces any credential-shaped line it meets with a placeholder. It creates nothing. It accepts exactly the field above; there is no path field.

The returned offer carries `software.review`:

- `inspected_at`, `evidence_revision` (changes when what was read changes), `partial` and `partial_reason` when a limit cut the read short
- `stack` (what the manifests declare), `guides` (instruction files read), `files` (everything read, as paths relative to the project)
- `git`: `layout` (`repository`, `worktree`, `none`, or `unverified` when the marker points somewhere that was not followed), `branch`, `remote` (host and path only, never credentials)
- `tools`: development tools the project carries configuration for; a configuration file is not proof the tool is installed
- `gaps`: what was missing or not read (`code`, `source`)
- `purpose`, `purpose_source` and `commands` (`kind`, `command`, `source`, `declared`): excerpts held in server memory only. `declared` is false for a command that is merely usual for the project's files. `evidence_held` is false after a restart, when the facts above remain and the excerpts need another review

It also carries `plan`, the same shape as the one-card plan below with five lines (`workspace`, `folder`, `agents`, `mode`, `task`) and no integration line, and `plan.prerequisites`: rows of `id`, `label`, `state`, `detail`, `source`, `required` and an optional `route` to an existing settings page. `state` is `verified` (Ori checked it), `configured` (a setting exists and was not exercised), `absent`, or `unknown`; nothing is reported as ready unless it was checked. Prerequisites are not part of the plan's digest. `software.workspace_name` and `software.assistant` are the names the plan uses, and `software.setup_available` says whether this server can carry the plan out; when false the review is shown and no setup is offered.

`400` while a candidate has not been chosen; `409` when the offer is no longer pending; `409` with `"needs_pick": true` when the server no longer holds the folder or the folder was replaced since it was shown; `503` when the project could not be read (nothing was changed, and the review can be asked for again).

### Adjust a Software Project's Names

**Endpoint:** `POST /api/personal-assistant/folder-digest/offers/{offerID}/adjust`

```json
{ "request_id": "…", "workspace_name": "Project notebook", "assistant_name": "Dev Pat" }
```

Changes the workspace name, the assistant's name, or both, on a reviewed software offer. Either field may be left out to keep its current value, and an empty string returns that name to the suggested one; a body with neither is `400`. It reads nothing from the project and creates nothing; the returned offer's plan, and so its digest, reflects the new names. `400` for a name longer than 120 characters or containing a slash or a line break; `409` when the offer has not been reviewed or is no longer pending.

The setup action below takes a reviewed software offer's `plan_digest` like any other. Before it runs, the server reads the project again; when what it reads differs from the review the plan was drawn from, it answers `409` with `"plan_changed": true` and the fresh offer, and nothing is created.

A software setup is the server's own: nothing is installed and there is no step-by-step journey behind it. The run creates the workspace from the Code Project blueprint with exactly the reviewed assistant (or completes the one existing workspace the review named), links the folder where it is as the workspace's primary directory, records which directory was confirmed (`software_project` in the workspace's shared data: the linked directory's id and the directory's own identity, never a path), gives a new workspace the planning-first workflow (the software profile's planner preset with planning documents kept in Ori), and seeds one first assessment. It runs nothing in the project, calls no model, and writes nothing into the folder. Every pass starts from what is saved, so a repeated click, a second offer about the same folder, or a pass after a restart continues on the same workspace instead of making another; two offers about one folder end with one workspace, and the offer that did not create it resolves to it with `outcome.existing: true`. A sibling worktree or a clone of the same repository is a different folder and is never matched.

A software run stops with one of these `stop_reason`s and keeps what it finished: `needs_model` (the assistant has no model; for a new workspace this is before anything is created — choose a model and send the same digest again), `needs_pick` (the server no longer holds the folder, or the directory there was replaced), `plan_changed` (what is saved no longer matches the review, for example a saved agent of the reviewed name appeared), `failed`, or `interrupted`. After `plan_changed` the review action above may be called again on the offer: it puts the stopped run aside and returns a plan drawn from what is saved, whose digest must be confirmed anew; the old digest no longer starts anything.

A workspace with a `software_project` record resolves its code root from that record alone: when the folder is missing or a different directory is at that path, the workspace has no code root, and neither repository checks nor planning documents fall back to Ori's own folder for the workspace.

#### The first assessment

The seeded first assessment is the workspace's first task (see `first_task` under Get Folder Digest) with `recipe: "software_assessment"`. A task under a recipe is run by that recipe or not at all: it is never started as an ordinary agent task, and the recipe marker cannot be set or removed through the task API. It starts only on **Start assessment** (`POST /api/workspaces/{id}/folder-first-task/start`, once) on Home's receipt or mission card or on the workspace page's banner; setup, a page load, a refresh and polling never start it or call a model.

A run is one model call and nothing else. The server checks that the workspace still points at the directory the user confirmed (the `software_project` record; a missing or replaced folder is `project_folder_unavailable` and nothing is read), reads the project itself with the same bounded, read-only inspector the review uses (README, repository instructions, declared build and test files, and the names at the top level, leaving out dot-entries other than `.github`, dependency folders and key-like names), and sends that text to the assistant's model as evidence, marked as data and not as instructions. Lines that look like credentials (token formats, `NAME_SECRET=value` assignments, a password in a URL, a bearer header, a whole PEM block) are withheld from it; a reference such as `${{ secrets.X }}` is not a credential and is kept. The model is given no tools, no MCP servers, no agent system prompt and no access to the folder; the request is flagged isolated, carries Ori's rules as its system prompt and the evidence as its one message, and a provider that cannot guarantee a tool-free call refuses it before anything is sent (`model_unsupported`). API and local providers support it. The Codex CLI is used only in a posture built from that CLI's own feature list: read-only sandbox, user config, rules and instruction files ignored, an empty working directory, and every feature that is on switched off except a short list that only concerns transport, sign-in and the CLI's own guard, so a feature a newer CLI adds is off by default; a CLI that no longer lists its command tools as features is refused. The Claude Code CLI is refused. A reply that asks for a tool is discarded and the run ends `tool_call_refused`. The assistant's own provider is used or none: when it is not set up on this computer the run is `model_unavailable`, never moved to another provider. The evidence is cut to fit the model's context window (`context_too_small` when even the compact form does not fit).

These hold whichever part of the server runs the task, because they are enforced where the model is called. One task is run once at a time (`already_running` for a second start). A run is never retried, split into steps, run as a sequence of sub-tasks, or handed to a coordinating agent. It is never started by the background executor, by a schedule of other work, or by another task that lists it as an input (that task is told the assessment must be started by the user first), and starting it runs none of the tasks listed as its own inputs. Whatever stops a run is recorded as one plain sentence with a code; a provider's own error text is written to the server log and nowhere else.

The result is the task's result: the project's purpose and structure, detected stack, declared (never "tested") build and test commands, repository instructions, readiness gaps, and one line `Next task: …`, followed by Ori's own note of which files it was based on and what was not read. Nothing is written into the project folder. A run that does not finish leaves the task `failed` with one plain sentence and its `reason`; **Try again** is the ordinary manual run of the same task (`POST /api/orchestration/tasks/execute` with `{"task_id": …}`), offered only while `can_retry` is true, and it never creates a second task. Concurrent starts of one task are refused by the executor, so two clicks or two tabs do not produce two runs.

**Endpoint:** `GET /api/workspaces/{id}/folder-first-task`

Answers `200` with `{ "first_task": <the shape above> }` for the workspace's first task as it stands now, or `{ "first_task": null }` when the workspace has none. It is a read: it starts nothing and spends nothing. The workspace page's banner and the Plans page (`?next=assessment`) use it so every surface shows the server's one reading of the task.

### Resolve a Project Offer

**Endpoint:** `POST /api/personal-assistant/folder-digest/offers/{offerID}/resolve`

```json
{ "workspace_id": "…", "request_id": "…" }
```

After the Create Workspace modal (opened pre-filled, sent with `entry_point: "folder_digest"` and `folder_offer_id`) reports the workspace it made. The server links the offer's folder as the workspace's primary project directory, superseding a blueprint scaffold inside the workspace, seeds one first task naming the read-only tools below, and returns the `resolved` offer. `409` when the workspace was not created for this offer or already has an outside linked folder; `409` with `"needs_pick": true` when the server no longer holds the picked folder (pick it again); `503` when the folder store is unavailable.

### Set Up a Recognized Project (one card)

**Endpoint:** `POST /api/personal-assistant/folder-digest/offers/{offerID}/setup`

```json
{ "request_id": "…", "plan_digest": "<64 hex>", "entry_name": "optional picked project file" }
```

A pending project offer whose shape needs a reviewed integration carries `plan` (`lines`: label, detail, `state`, and a `digest`) and `setup` (the run: `status`, `stop_reason`, `lines`). The digest of the plan is the user's consent: it covers every line except its state. The server runs the plan itself (plugin installs, Home, workspace, folder link, File-only mode, agents, first task) and answers `200` with the offer (the run continues on the server; poll the offer). Allowed fields are exactly the three above; there is no path field, and the intent behind the plan is pinned on the server. `409` with `"plan_changed": true` and the fresh offer when the plan moved since the card was drawn; `409` while another run holds the offer; `404` unknown offer. A run that cannot finish stops with a `stop_reason` (for example `needs_model`, `needs_pick`, `install_failed`) and keeps what it finished; sending the same digest again continues from there. A finished run makes the offer `resolved` with a receipt (`outcome.workspace_id`, `outcome.route`). The first read-only task (the "first look") starts only when the user presses **Start first look**, on Home's receipt or mission card or on the workspace page's banner (`POST /api/workspaces/{id}/folder-first-task/start`, once); opening a workspace never starts it. See `first_task` and `first_look` under Get Folder Digest.

An offer the user sent to the step-by-step journey (**Adjust…**, so `awaiting_outcome` with no run yet) still carries `plan`, and the setup action accepts it the same way, so the one-click path is not lost.

`GET /api/personal-assistant/folder-digest?offer_id=<id>` returns that offer as `offer` (a running or finished one included) instead of the current one, which is how the card follows its own run when another offer is waiting.

The same action sets up a **collection** offer (`portfolio`) when its reviewed Home provider resolves: the plan lists the provider, the integration (only when some folders are projects it opens), the Home, its agents, the library listing and the shared assistant, and the run builds the Home, staffs its required roles, starts its library, connects the folder, scans it once and records the Home's standing staffing consent. It lists the projects and makes **no** project workspace. The finished offer's `outcome.receipt` rows (`home`, `library`, `assistant`) name what was made; the `library` row says "Listed N … so far" after a partial scan. A single-song run records the same consent on its Home, so its project role is filled by the Home's one shared assistant: created by the first song (receipt detail `added`), bound by later ones (`joined`).

### A Folder of Projects: the proposed group

A folder with no project marker of its own and two or more immediate subfolders that are each a project (software repositories aside, which keep the candidate choice above) is a **collection**. Ori answers it with one proposal: a workspace group and one linked workspace per subfolder. The proposal is the `structure` record, built from directory entries only, and it is the same record wherever the folder is shown:

- `POST /api/home-assistant/folder-context/select` (the assistant's **Add folder**) returns it as `structure` beside `folder_context`, and the turn's review option carries it too.
- `POST /api/personal-assistant/folder-digest/scan` and the digest read (Home's **Explore a folder**) return it as `offer.structure`. The offer's subject is then the folder itself and no subfolder is queued one by one.

```json
{
  "version": 1,
  "folder": "Albums",
  "group": { "name": "Albums", "type": "group", "destination_state": "new", "home_name": "Music Production Home", "home_available": true, "availability": "provider_not_installed" },
  "members": [
    { "candidate_id": "…", "name": "Album-1", "folder": "Album-1", "type_label": "REAPER session", "project": true, "files": 2, "last_changed": "2026-09-29", "included": true, "integration": "REAPER", "availability": "integration_not_installed" }
  ],
  "min_members": 2,
  "projects": 5,
  "omitted": 0,
  "notes": ["Music Production Home is not installed; a plain group is proposed instead."]
}
```

`group.type` is `group` (a new plain group, the default whenever the shape's Home is not in place) or `library` (the collection is listed in the shape's Home, which is the collection setup above and makes no project workspace). An offer whose structure is `library` is that collection setup in every respect (`portfolio`, `capability`, its own plan and journey); the attach response carries no `structure` for it, and the plain group is reached by sending `group_type: "group"` to the adjust action below. `members` lists at most 24 subfolders (`omitted` counts the rest); a member carries no path, only its folder's name and an opaque `candidate_id` that is valid for this proposal alone. A member whose folder is already a workspace's primary folder carries `existing_workspace` and is not included. Default names are made free before they are shown: a member whose name is taken reads "Album-1 (Albums)", and a group whose name is taken reads "Albums group".

Nothing about a plugin blocks the proposal. A Home or an integration that cannot be used is reported, never dropped: `availability` on the group or the member is one of `integration_not_installed`, `provider_not_installed`, `provider_unverified`, `declined_earlier` or `plan_unavailable`, and `notes` says each reason once as a plain sentence. Availability is read from what is installed on this computer; showing a folder makes no network request. The same codes appear on a review option that cannot be chosen now (`availability` on the option), so an option the server cannot plan is listed with its reason and not left out.

A bare `yes` on the decide action is refused for a proposed group (`409`, "Review this project's setup first"), as for a software project: the group is confirmed on its plan.

**Adjust the proposal (assistant):** `POST /api/home-assistant/folder-context/review` accepts `structure` in its body.

**Adjust the proposal (Home):** `POST /api/personal-assistant/folder-digest/offers/{offerID}/structure`

```json
{
  "request_id": "…",
  "structure": {
    "group_name": "Studio",
    "group_type": "group",
    "members": [{ "candidate_id": "…", "included": false }, { "candidate_id": "…", "included": true, "name": "First album" }]
  }
}
```

Every field of `structure` is optional. `group_type` is `group` or `library` (`library` only while `group.home_available` is true). A member that is left out keeps its current state. The server applies the change to the proposal it holds; it scans nothing, reads no file and creates nothing. The response is `{ "offer": … }` with the adjusted `structure` and a fresh `plan`, whose digest is what **Set up** then confirms. The body is at most 16 KiB and has no path field. `400` (with the part that cannot be applied named in `message`, ending "Nothing was saved") for an id the proposal did not list, no folder left included, a folder that is already a workspace, a name that is longer than 96 characters or contains a slash or a line break, two workspaces of one name, a name a workspace already has (workspace names are one namespace, whatever group they are in), or a Home that cannot be used; `404` for an unknown offer; `409` when the offer is no longer pending, has no proposed group, or was made from a conversation review (that review is adjusted through the assistant's review action instead).

**Set up** is the one-card action above with the plan's digest. The plan's lines are the group (`group`), the count of workspaces (`workspaces`) with one `member` line each, "Nothing is moved, copied or read", one `unavailable` line per capability that is not in place, and "No task is created or started". The run is the server's own and calls no model: it creates the group with its reviewed Group Manager, then one blank workspace per included member inside it, links each member's folder where it is as that workspace's primary directory, and records what the folder was recognized as (`folder_shape`, `folder_marker` in the workspace's shared data) so the workspace can take its integration later. It moves, copies and opens nothing in the folder, installs nothing and seeds no task.

The group is recorded under the offer and each workspace under the offer and its member, so a repeated click, a retry after a restart, or a second pass after a stop continues on what exists and makes nothing twice. A workspace that cannot be created stops the run with `stop_reason: "member_failed"` and keeps the group and every workspace already made; sending the same digest again carries on from that member. The other stop reasons are `plan_changed`, `needs_pick`, `interrupted` and `failed`. A finished run makes the offer `resolved` with `outcome.kind: "group"`, `outcome.workspace_id` and `outcome.route` for the group, and `outcome.receipt`: one `workspace` row for the group (`detail: "group"`) and one per workspace (`detail: "linked to <folder>"`), each with its `route`.

### Read-only tools over linked directories

Chat and task runs in a workspace with a linked directory get two tools:

- `workspace_directory_list` — `{ "directory_id", "path"?, "depth"? }` lists entries (name, relative path, kind `file`/`folder`/`link`, size, modified time) up to depth 3 and 500 entries, never following symlinks outside the directory.
- `workspace_directory_read` — `{ "directory_id", "path", "offset"? }` returns parsed text for PDF, DOCX, and text-like files in 40,000-character pages with `next_offset`; binary and unsupported files are refused. The `content` field is labelled as file content: reference data, not instructions.

Both are read-only; nothing here writes to a linked directory.

## Personal Assistant Interview API

The optional three-question interview (contract in `docs/architecture/personal-assistant-foundation-contract.md`). Its first question, "What's the main thing you're working on right now?", can be answered by showing the assistant a folder: the scan proposes wording, the user confirms or edits it, and nothing is saved before the wizard's Save (`POST /api/personal-assistant/knowledge/interview/save`). Available only once Personal HQ is built (`409` otherwise).

### Get the Interview

**Endpoint:** `GET /api/personal-assistant/knowledge/interview`

```json
{
  "questions": [{ "id": "priority", "prompt": "…", "hint": "…", "category": "projects", "destination": "personal_hq" }],
  "status": "offered",
  "state_version": 3,
  "profile": { "updated_at": "…", "preferences": { "response_style": "", "units": "", "language": "" } },
  "suggestion": {
    "text": "Thesis, a LaTeX manuscript",
    "folder": "Thesis",
    "alternates": [{ "text": "website, a Node.js package", "folder": "website" }]
  },
  "remembered_project": "You are working on a project in the folder Thesis."
}
```

A pure read: it offers nothing, saves no draft and scans no folder. Two fields are optional and come from folders the user already showed the assistant:

- `suggestion` — wording for question 1 from a folder offer still waiting on Home (pending, or set aside with **Later**). It is sent only when the offer names a project; a dump, an empty or unclassified folder and a whole collection give none, and so does a whole Downloads, Documents or Desktop named as the project itself. `alternates` (at most three) are the folder's other projects. `folder` is a base name, never a path. A project the user has since said **no** to is not proposed.
- `remembered_project` — the current text of the newest project fact approved from a folder (a **yes** on Home) that is still live: the dossier's own test, so a fact shown there as needing review (its folder or workspace is gone, or its canonical line changed) is not reported. A remembered project is never proposed again: the folder's next project takes its place as the `suggestion`, and it is left out of `alternates`.

Both are left out when there is nothing to report or the record behind them cannot be read; the rest of the response is unaffected. The wizard prefills question 1 from `suggestion` only on a fresh open: a stored draft always wins.

### Suggest an Answer from a Folder

**Endpoint:** `POST /api/personal-assistant/knowledge/interview/suggest`

```json
{ "chip": "documents" }
```

Exactly one of `{ "chip": "<id>" }`, `{ "picker": true }` or `{ "file": true }`: the same body as [Scan a Folder](#scan-a-folder), with the same refusal of every other field (a `path`- or `folder`-named field is `400` before anything is looked at). It runs the one folder scan Home runs, so the scan reads file names, types and dates only, **and it records the same offer**: the folder's setup suggestion is then waiting on Home, and an offer that was already pending there moves to later. No workspace is created and no fact is saved.

```json
{ "suggestion": { "text": "Thesis, a LaTeX manuscript", "folder": "Thesis", "alternates": [] } }
```

A folder with no project to propose answers `200` with an explicit null and a message to show:

```json
{ "suggestion": null, "message": "I couldn't tell what this folder is for. Type your answer instead." }
```

The same rules as the snapshot's `suggestion` apply, so a project already remembered from a folder is skipped in favour of the folder's next project. When every project in the folder is already remembered the message is "I already remember the project in this folder. Add anything that matters more right now, or skip."

A cancelled native dialog answers `{ "cancelled": true }` and records nothing. Errors read as they do for a scan (`400` unknown chip, missing folder or a root that cannot be looked at; `409` when the dialog is unavailable or Personal HQ is not built), except a scan that is still running, which is `409` with "I'm still looking at the last folder. Try again in a moment."

## Global Memory API

Global memory is the Personal HQ's `MEMORY.md` together with its reviewed-knowledge sidecar (`.ori/personal-assistant-knowledge.json`). There is no other store. Only the hired personal assistant reads it (Home, the HQ page, and the assistant drawer on workspace pages), under the prompt heading `## Global memory (Personal HQ, user-approved)`. Workspace agents never receive it; to give a workspace's agents a fact, copy it into that workspace (below).

Without a hired assistant and a Personal HQ, every global-memory control answers with the same message: "Global memory needs a personal assistant with a Personal HQ. Set one up from Home."

### Propose a fact from any workspace (agent tool)

Agents in every workspace's chat and task runs have the workspace tool `memory_propose_global`, allowed under every autonomy policy, including Watch, because it writes no canonical memory. The model supplies only `text` (one line, at most 500 characters, the same rules as `memory_write`) and `category` (`how_you_work`, `people`, `projects`, `routines`, `sources`). The server fills the workspace, agent and task from the verified execution context; any other argument is ignored.

A proposal becomes a review candidate (source kind `workspace_agent`) with evidence `{source_kind: "workspace_agent", source_id: <workspace ID>, summary: "<workspace> · <agent> · task <id>" | "… · chat"}`. It never writes `MEMORY.md`. Proposals are keyed by normalized text, so the same fact from two workspaces is one item. Workspace proposals have their own quota: 10 pending and 10 per rolling 24 hours, separate from the folder-scan, saved-app and File Janitor sources.

The tool replies `{"status", "message"}`:

| `status` | Meaning |
|---|---|
| `proposed` | Sent for the user's review on the Profile page; not active until approved |
| `already_pending` | The same fact is already waiting for review |
| `already_known` | Global memory already holds the fact (an approved item or an HQ `MEMORY.md` line) |
| `refused` | The user rejected or forgot this wording, the queue is full, or the assistant is paused; the message says not to retry |
| `unavailable` | No hired assistant with a Personal HQ |

Agents on the CLI providers (`codex`, `claude_code`) call it through the brokered tool protocol, in workspace chat and in task runs alike: the CLI names the tool and Ori runs it. A CLI's own native MCP servers have no route to it.

### Review items carry their source

`GET /api/personal-assistant/knowledge` items have a `source` object: `kind` for every item (`interview` marks an explicit fact saved from the assistant's interview). For a workspace agent's proposal, or a fact the user promoted from a workspace, it also has `workspace_id`, the workspace's current `workspace_name` and `workspace_slug` (omitted when the workspace is gone), and, for a proposal, `agent`, `task_id` (absent for a chat) and `observed_at`. Approving a `workspace_agent` proposal is not revalidated against its source workspace: the user approved the exact wording.

### Copy a fact into a workspace

**Endpoint:** `POST /api/personal-assistant/knowledge/{item_id}/copy`

```json
{ "workspace_id": "album-3", "if_version": 2 }
```

Appends one ordinary entry `- [fact, <today>, global:<item-id>] <text>` to that workspace's `MEMORY.md`, with the text read from the canonical HQ line. `if_version` must be the version of the approved fact the user is looking at. Repeating the copy changes nothing. The copy is ordinary workspace memory from then on: editable, deletable, and never updated or removed when the global fact changes.

```json
{ "created": true, "workspace_id": "album-3", "href": "/workspaces/album-3?station=memory" }
```

`400` for the Personal HQ itself or a malformed request, `404` for an unknown or unavailable workspace or fact, `409` when the fact is not approved or its version changed, and `409` with the shared message when there is no Personal HQ.

### List the workspaces holding a copy

**Endpoint:** `GET /api/personal-assistant/knowledge/{item_id}/copies`

```json
{ "copies": [{ "workspace_id": "album-3", "name": "Album 3", "slug": "album-3", "href": "/workspaces/album-3?station=memory" }] }
```

Scans active workspaces' `MEMORY.md` files for `global:<item-id>` provenance, on demand and read-only. It also works after the fact is forgotten, so the Profile page can show where copies live on.

### Remove a copy

**Endpoint:** `DELETE /api/personal-assistant/knowledge/{item_id}/copies/{workspace_id}`

Deletes that workspace's copied line and nothing else. Returns `{"removed": true, "workspace_id": …}`; `404` when the workspace holds no copy.

## Home Project Library Notifications API

After a reviewed library scan completes, a program Home keeps a small, model-free digest of what the scan found, may ask its bound Manager for at most three inert suggestions, and shows both through navigation-only entry points (PRD `library-manager-notifications`; contract in `docs/architecture/project-library.md`). Every route below is for the authenticated owner of that exact Home: any other owner, a linked project's ID, or a Home without an assistant program gets `404`. No route here scans, reads a folder, grants anything or changes notes.

### Get the Library Summary

**Endpoint:** `GET /api/workspaces/{workspaceID}/assistant-program/library/summary`

No query string (any is `400`).

```json
{
  "initialized": true,
  "revision": 12,
  "digest": {
    "scan_id": "…", "root_id": "…", "scanned_at": "2026-09-30T00:09:19Z", "coverage": "complete",
    "projects": 6, "new": 6, "updated": 0, "unavailable": 0,
    "unsupported_format": 1, "activatable": 5
  },
  "ready_proposals": 1,
  "proposal_run": { "scan_id": "…", "status": "skipped", "reason": "no_model", "proposals": 0, "started_at": "…", "finished_at": "…" },
  "feedback": [ { "kind": "next_action", "accepted": 1, "dismissed": 1, "updated_at": "…" } ],
  "route": "/workspaces/music-production-home/assistant#projectLibraryProposals"
}
```

An uninitialized library returns `{"initialized": false, "digest": null, "ready_proposals": 0, …}`. `digest` is the latest `complete` or `partial` scan's counts, or `null` when there is none or its folder was disconnected; it never carries a path or file name. When no single compatible installed integration can be checked, `activatable` and `unsupported_format` are `0` and `setup_note` names why (`setup_check_unavailable`, `home_provider_unavailable`, `project_provider_unavailable`, `provider_ambiguous`). `activatable` is guidance from saved state; a setup review repeats every check. `ready_proposals` counts ready suggestions among the shelf's newest 20. `proposal_run` is the Manager review receipt for the digest's scan: `started`, `finished` (reason empty or `proposal_limit`, `time_limit`, `token_limit`, `step_limit`, `model_error`) or `skipped` (`no_manager`, `no_model`, `provider_unavailable`, `unavailable`, `superseded`, `model_error`, `time_limit`, `interrupted`). `feedback` counts the owner's answers per suggestion kind; it is shown only and never reaches a prompt.

### Dismiss a Suggestion

**Endpoint:** `POST /api/workspaces/{workspaceID}/assistant-program/library/proposals/{proposalID}/dismiss`

```json
{ "confirm": true, "idempotency_key": "…" }
```

Strict JSON: exactly these fields, no duplicates. Marks one waiting suggestion dismissed and remembers its (project, kind) for seven days so a scan review does not suggest it again; nothing else changes. Allowed while the Home provider or Manager is unavailable (the suggestion then reads `unavailable`). Returns `{ "proposal_id", "dismissed_at", "replay" }`; repeating the same key replays. `400` without `confirm: true` or for any other body; `404` for an unknown suggestion; `409` when it is no longer waiting (already dismissed under another key, confirmed, stale or expired).

### List Home Library Cards

**Endpoint:** `GET /api/action-center/library`

```json
{
  "items": [
    {
      "home_id": "…", "home_name": "Music Production Home",
      "route": "/workspaces/music-production-home/assistant#projectLibraryProposals",
      "scanned_at": "…", "coverage": "complete",
      "projects": 6, "new": 6, "activatable": 5, "ready_proposals": 0
    }
  ],
  "total": 1
}
```

One derived card per Home the current owner holds whose digest has `activatable > 0` or that has ready suggestions, newest scan first, at most 20. Cards are not opportunities: they have no dismiss, snooze or resolve and disappear once nothing is ready. A missing or failing source returns an empty list.

### Event and Setting

A completed or partial scan publishes one in-process `library.scan_completed` event (`home_id`, `scan_id`, `root_id`, `coverage` and the digest counts only); failed, cancelled, interrupted and replayed scans publish nothing. The optional `library_manager_token_budget` in `settings.json` lowers the token cap of the Manager's review turn below the default and maximum of 20,000.

## Home Library: Open a Song and the Shared Assistant

Every route is for the authenticated owner of that exact Home (`404` otherwise) and takes a strict JSON body: unknown fields are `400`, and any `path` or `folder` field is refused (the browser never names a folder).

### Open a Listed Project

**Endpoint:** `POST /api/workspaces/{homeID}/assistant-program/library/projects/{entryID}/open`

```json
{ "request_id": "open-7f3a", "selected_file": "Song 001 alt.rpp" }
```

Makes the project's workspace (or uses the one it already has), records File-only mode, fills its required project roles under the Home's standing staffing consent and seeds its first task. `selected_file` is only for a folder with several project files. Answers `200` with `{ entry_id, workspace_id, route, created, staffing, agent_name, first_task }`, where `staffing` is `added` (the shared assistant was created), `joined` (bound), `already` (the role was filled), `off` (switched off: no agent), `none` (no consent), `not_shared`, `consent_stale` or `assistant_missing`. The same `request_id` finishes a half-done open instead of making a second workspace. `409 needs_choice` with `project_files` when the folder has several project files; `409 consent_stale` (before anything is made) when the installed blueprint's team changed since the consent; `409 needs_model` before the workspace is made when the assistant would be created and no model is set. Search rows carry `can_open` for the rows this can open.

### The Staffing Switch

`GET /api/workspaces/{homeID}/assistant-program/library/roots` includes `sharing`: `{ state: none | on | off | stale | unavailable, role_label, agent_name, assistant_missing, team_digest }`.

**Endpoint:** `POST /api/workspaces/{homeID}/assistant-program/library/sharing`

```json
{ "request_id": "switch-1", "enabled": false, "team_digest": "…" }
```

`enabled: false` switches the consent off (it keeps its assistant; opening a song then adds no agent). `enabled: true` records a fresh consent (`source: home_switch`) and must echo the `team_digest` the switch showed (`409 sharing_changed` otherwise); this is also how a Home made before the consent existed opts in, and how a changed team is re-confirmed once.

**Endpoint:** `POST /api/workspaces/{homeID}/assistant-program/library/sharing/assistant`

`{ "request_id": "…" }` — after the shared assistant was deleted, clears it from the consent so the next song opened creates a new one (the next free name) and records it.

### Song Details (tempo, length, track count)

`GET /api/workspaces/{homeID}/assistant-program/library/roots` also includes `song_details`: `{ state: none | on | off, app_name }`. `none` means the Home never agreed (it was made before this, or step by step) and has no switch. Search rows (`GET …/library/projects`) and detail sources carry `facts: { tempo_bpm, tempo_varies, length_seconds, track_count }` when known and the switch is on; unknown parts are left out.

**Endpoint:** `POST /api/workspaces/{homeID}/assistant-program/library/song-details`

```json
{ "request_id": "song-details-1", "enabled": false }
```

`enabled: false` stops later scans from reading project files and clears every stored fact and the collection brief in the same Home write. `enabled: true` turns it back on (no scan starts; facts return with the next one). Answers `200` with `{ song_details: { state, app_name } }`; setting the current state is a no-op. `409 song_details_not_granted` when the Home has no consent record; `409 provider_unavailable` for `enabled: true` on a read-only Home (off still works). A scan review on a Home whose switch is on discloses the read: `reads_song_facts: true` and a `scope` that names the three facts.

### The Manager's Collection Brief

`GET /api/workspaces/{homeID}/assistant-program/library/proposals` also returns `manager_label` (the Home's Manager role label) and, on a Home whose switch is on, `brief: { scan_id, text, agent_name, model, created_at }` when the Manager's post-scan turn wrote one for the latest scan. The brief is plain text (at most three sentences and 500 characters, no paths or links), never a suggestion: it adds nothing to `ready_proposals`. `GET …/library/summary`'s `proposal_run` carries `mode: "brief"` for such a turn.

### Edits to the Shared Assistant

`PATCH /api/agents/{name}` that changes the model, provider, reasoning effort, temperature, max tokens or system prompt is carried into every project copy the consent tracks for that agent, except a copy changed in its own project. The response then includes `carried: { updated: [{id, name}], customised: [{id, name}] }`. `GET /api/agents/{name}/detail` adds `workspace_count` and `workspaces`, and leaves copies that are still in step out of `origin.customised_in`. A chat answered by a workspace's own copy never writes that copy back to the user's agent.

## Home Profile API

What a Home knows about where its owner works, under the rows the Home's installed package declares in `home_profile` (contract: `docs/architecture/independent-program-homes.md` §3.2; states: `docs/architecture/music-setup-onboarding.md` S4c). Every route is for the authenticated owner of that exact Home: a linked project, another person's Home or any other workspace is `404`. Request bodies are strict JSON (an unknown or duplicate key is `400`) and carry a `request_id`; repeating one replays the earlier result (`replayed: true`) instead of writing again. Nothing here is an agent tool.

**Endpoint:** `GET /api/workspaces/{homeID}/assistant-program/profile`

Detects nothing and reads no folder. Takes no query string.

```json
{
  "available": true,
  "read_only": false,
  "title": "Your studio",
  "intro": "What Ori knows about where you make music. Detected values are hints until you confirm them.",
  "fields": [{ "id": "apps", "kind": "apps", "label": "DAWs on this Mac" }],
  "revision": 3,
  "profile": {
    "schema_version": 1,
    "revision": 3,
    "declared_by": { "plugin_id": "music-project-management", "version": "0.2.0", "title": "Your studio", "labels": { "main_app": "Main DAW" } },
    "detected_at": "…",
    "apps": [{ "id": "reaper", "name": "REAPER", "detected": true, "detected_at": "…", "version": "7.28", "confirmed_at": "…" }],
    "main_app": { "id": "reaper", "source": "detected", "reason": "only_app", "confirmed_at": "…" },
    "templates": {
      "consent": { "granted_at": "…", "source": "home_review" },
      "read_at": "…",
      "app_id": "reaper",
      "items": [{ "name": "Band Session", "kind": "project", "file": "Band Session.RPP", "modified_at": "…" }]
    },
    "defaults": { "tempo_bpm": 120, "time_signature": "4 4", "sample_rate_hz": 48000, "bit_depth": 24, "source": "owner", "confirmed_at": "…" }
  },
  "choices": { "min_tempo": 40, "max_tempo": 240, "time_signatures": [{ "value": "4 4", "label": "4/4" }], "sample_rates": [44100, 48000, 88200, 96000, 176400, 192000], "bit_depths": [16, 24, 32] },
  "templates": { "state": "listed", "app_id": "reaper", "app_name": "REAPER", "folders": ["ProjectTemplates", "TrackTemplates"], "consented": true },
  "facts_operation": true
}
```

- `available: false` (and nothing else) for an owner's Home whose installed package declares no profile.
- `read_only: true` when the Home's provider is unavailable for changes: values stay readable and every write below is `409 home_read_only`.
- `profile` is `null` and `revision` is `0` before anything was detected or saved. Request receipts are never returned.
- Every value says where it came from: `source: "detected"` is a hint until `confirmed_at` is set; `source: "owner"` is the owner's instruction. `main_app.reason` is `only_app` or `library_majority` on a detected value. A hidden application has `hidden: true` and is never shown to agents.
- `templates.state` is one of `detect_first`, `other_app`, `plugin_missing`, `update_plugin`, `not_read`, `listed`, `empty`, `problem` (with `problem: read_failed | operation_unavailable`) or `unsupported`. `items` holds at most 64 names with a bare file name each, never a path; `truncated: true` means the list is not everything: the folders hold more, some sit in subfolders, a name could not be shown as one plain visible line, or a folder could not be read. With no items at all, `empty` plus `truncated` reads "Nothing could be listed…" on the card, not "No templates were found".
- `profile.templates.empty_reason` says why a read that worked listed nothing, when that is not simply empty folders: `no_folders` (the application has no templates folders at all; the card says "<app> has no templates folders yet… Save a template in <app>, then press Read again.") or `app_not_found` (the project plugin did not find the application where it looks). It is cleared by the next read that lists something and by Forget.
- `GET /api/workspaces/{id}/assistant-program` carries `home_profile_title` on the Home itself when its installed package declares a card. The Home's workspace page uses it for a link, named by that title, to `/workspaces/{slug}/assistant#homeProfilePanel`; arriving there scrolls to the card and focuses its heading.
- `templates.consented` is `true` while the record holds an agreement that was not taken back, whatever the state. A list read earlier stays on the record (and in the agents' context) when no new read can happen (`other_app`, `plugin_missing`, `update_plugin`, `unsupported`); the card shows it and offers Forget in every one of those states. `problem` with `operation_unavailable` is reached only once the plugin can list templates again: it is a setup card's read that met a plugin too old to list them, and Read again retries it under the same consent.
- `time_signatures` are the options of the `time_signature` input on the blueprint the Home's projects are created from; empty when that plugin is not installed.

**Endpoint:** `POST /api/workspaces/{homeID}/assistant-program/profile/detect`

`{ "request_id": "…" }` — looks for installed applications (bundle names from the host tool table in `/Applications` and `~/Applications`; no symlink is followed and nothing inside a bundle is read) and asks the project plugin's facts operation for the application's version only. Records what it finds as hints, keeps everything the owner confirmed, hid or chose, and proposes a main application when the owner has not said: the only one found, or with several the one whose project format has strictly the most entries in the Home's library, otherwise none. Answers with the card.

**Endpoint:** `POST /api/workspaces/{homeID}/assistant-program/profile/fields`

```json
{ "request_id": "…", "if_revision": 3, "main_app": "reaper", "confirm_apps": ["reaper"], "hide_apps": ["logic-pro"], "show_apps": [], "defaults": { "tempo_bpm": 96, "time_signature": "3 4" } }
```

Every part but `request_id` and `if_revision` is optional; what is absent is left as it is. `main_app` equal to the proposed one confirms it; another id replaces it with `source: owner`; `""` clears it. `confirm_apps` marks a found application as the owner's, `hide_apps` is "Not mine" (it also clears a main application or templates list that was that application's), `show_apps` brings a hidden one back as a hint. `defaults` replaces the whole set: a part left out is no longer set, and `{}` clears them. `409 home_profile_changed` on a stale `if_revision`; `400` for an application that was not detected, an id named twice, a tempo outside 40 to 240, a time signature that is not one of the choices, or a sample rate or bit depth outside the fixed lists.

**Endpoint:** `POST /api/workspaces/{homeID}/assistant-program/profile/templates/review`

`{ "request_id": "…" }` — reads nothing and stores nothing. Answers `{ review_id, app_id, app_name, folders, sentence }` for the consent dialog. `409 plugin_operation_unavailable` when no installed project plugin offers a facts operation; `400` when templates cannot be read for this Home's applications (nothing detected yet, or the application is not found, hidden or not the main one).

**Endpoint:** `POST /api/workspaces/{homeID}/assistant-program/profile/templates/commit`

`{ "request_id": "…", "review_id": "…" }` — records the consent (`source: home_review`; an active consent is kept) and calls the facts operation once with `include_templates: true`. Answers with the card. `409 home_profile_changed` when the review no longer describes the Home (for example after Forget). That is checked before the read and again on the record being written, so a Forget, another consent or a main-application change that lands while the plugin is answering wins and nothing from that read is stored. `409 plugin_operation_unavailable` when there is no operation, and nothing is recorded. A read that fails after the owner agreed keeps the consent and answers `200` with `templates.state: "problem"`.

**Endpoint:** `POST /api/workspaces/{homeID}/assistant-program/profile/templates/forget`

`{ "request_id": "…" }` — clears the list and sets `consent.revoked_at` in one write. A later read needs a new review.

**Related.** `POST /api/workspaces/template-agent-plan` adds `group_requirement.home.input_defaults: { values: { tempo, time_signature }, note }` when the destination Home has saved defaults the selected blueprint's own inputs accept; the Create dialog shows them in fields the person has not changed. The Home's agents receive the profile in their context block, and its primary Manager has the read-only tool `home_profile_read`.

## Home Package Upgrade API

Moves every Home pinned to an installed Home provider package, and the projects linked to those Homes, onto one exact newer release of that package (contract: `docs/architecture/independent-program-homes.md` §6.1). Two kinds of change are accepted: guidance (Home role prompts and packaged skill text), and a release that adds a `home_profile` card to a Home that has none (the review then lists it under `additions`). The target is the release the Plugins page's Update would install — the newest reviewed release for a reviewed install, otherwise the plugin's recorded source. Every route is for the authenticated owner of that exact Home, and the Home must record a package pin: anything else is `404`. Refusals are `409` with a stable `code`:

| Code | Meaning |
| --- | --- |
| `home_upgrade_current` | Nothing newer to install (moving back to an older release is not offered). |
| `home_upgrade_no_homes` | No live Home uses the package; update it from the Plugins page. |
| `home_upgrade_not_guidance_only` | The release changes more than role prompts and skill text. |
| `home_upgrade_other_owner` | A Home using the package belongs to another owner. |
| `home_upgrade_pin_mismatch` | A Home or linked project is not pinned to exactly the installed release. |
| `home_upgrade_mirrors_disagree` | A Home's or linked project's saved copies disagree. |
| `home_upgrade_repair_active` | A project role repair under one of the Homes is claimed or unreconciled. |
| `home_upgrade_active` | Another upgrade of the package is in progress or needs reconciliation. |
| `home_upgrade_review_stale` | The review expired (10 minutes), was used, or the plan changed since. |
| `home_upgrade_cancelled` | The new release could not be installed; nothing changed. |
| `home_upgrade_reconcile_required` | The upgrade stopped with records it cannot account for; `details` is the operation. |
| `home_upgrade_unavailable` | The upgrade could not be completed; nothing was changed (`503` when there is no operation). |

### Get Upgrade Status

**Endpoint:** `GET /api/workspaces/{workspaceID}/assistant-program/provider-upgrade`

```json
{
  "plugin_id": "music-project-management",
  "installed_version": "0.1.0",
  "available_version": "0.1.1",
  "available": true,
  "operation": { "id": "…", "status": "reconcile_required", "from_version": "0.1.0", "to_version": "0.1.1", "reason": "…", "updated_at": "…" }
}
```

`installed_version` is the Home's pin. `available` comes from the Plugins page's cached update check and never reads a source. `operation` appears only while an operation holds the package (`claimed`, `replaced`, `reconcile_required`); reading this route first settles an operation a crash interrupted. The Home's `GET …/assistant-program` summary carries the same object as `provider_upgrade`.

### Review an Upgrade

**Endpoint:** `POST /api/workspaces/{workspaceID}/assistant-program/provider-upgrade/review`

Body `{}` (strict JSON). Inspects the target without installing it and records a ten-minute review bound to a digest of the whole plan.

```json
{
  "token": "…", "expires_at": "…",
  "plugin_id": "music-project-management", "from_version": "0.1.0", "to_version": "0.1.1",
  "roles": [ { "role_id": "portfolio_manager", "label": "Music Portfolio Manager", "old": "…", "new": "…" } ],
  "homes": [ {
    "name": "Music Production Home", "projects": ["Album 1"],
    "agents": [ { "role_id": "portfolio_manager", "role_label": "Music Portfolio Manager", "agent_name": "Portfolio Manager", "profile": "replace", "home_copy": "replace" } ]
  } ],
  "trashed_homes": 0,
  "skills": ["music-project-management"]
}
```

`profile` and `home_copy` are `replace` (the agent's prompt still equals the old role prompt), `keep` (the owner edited it) or `missing`. Homes in the Trash keep the old release. Approved library folders stay approved; pending library reviews and scans are cancelled.

### Commit an Upgrade

**Endpoint:** `POST /api/workspaces/{workspaceID}/assistant-program/provider-upgrade/commit`

```json
{ "token": "…" }
```

Re-derives the plan and requires the reviewed digest, then installs exactly the reviewed release (the replacement guard allows only this claimed replacement), moves each Home and linked project, and replaces unedited agent prompts. Returns the finished operation:

```json
{ "id": "…", "status": "succeeded", "from_version": "0.1.0", "to_version": "0.1.1",
  "agents": [ { "home_id": "…", "role_id": "portfolio_manager", "agent_name": "Portfolio Manager", "profile": "replace", "home_copy": "replace" } ],
  "updated_at": "…" }
```

### Plugins Page Refusal

`POST /api/plugins/{name}/update` (preview and confirm) and a same-name install refuse to replace a package a live Home is pinned to with `409 {"code":"home_upgrade_required","details":{"home_name":"…","home_path":"/workspaces/{slug}/assistant?upgrade=review"}}` before any trust dialog. The Home page opened at `home_path` starts the review above. The folder digest's `POST …/offers/{offerID}/home-provider` answers the same way when an older reviewed release it would update is still pinned by a Home; with no such Home, its preview reports `"update": true` and `installed_version`, and confirming updates to the newest reviewed release.

## Workspace Memory API

Each workspace keeps a curated `MEMORY.md` of durable operational knowledge (facts, decisions, dead ends, watch-state) at the root of its folder. The file on disk is canonical — there is no database copy. Memory is injected into every mission run and workspace chat (capped at ~2,000 tokens) and can be written by agents via the `memory_write` / `memory_forget` tools under every autonomy policy, including Watch. A fact about the user rather than the workspace belongs in global memory instead; see the [Global Memory API](#global-memory-api).

Each entry is one line: `- [<type>, <YYYY-MM-DD>, <provenance>] <text>`, where `<type>` is one of `fact`, `feedback`, `decision`, `dead-end`, `watch`, `thread`. Entries are capped at 500 characters and obvious secrets are refused (store credentials in the Vault).

### Get Workspace Memory

**Endpoint:** `GET /api/workspaces/{workspaceID}/memory`

```json
{
  "entries": [
    { "index": 0, "type": "watch", "date": "2026-06-13", "provenance": "run:abc (Scout)", "text": "build baseline ~7 min; flag if >10", "in_prompt": true }
  ],
  "unstructured": ["# Workspace Memory"],
  "raw_size": 149,
  "char_budget": 8000,
  "token_budget": 2000,
  "over_budget": false,
  "global_memory": { "available": true, "review_href": "/profile#personalHQKnowledge" }
}
```

`entries` are the structured lines (with their file-order `index`); `unstructured` holds non-entry lines (the header, hand-written prose) for display. An entry with `"managed": true` is a reviewed global-memory line (`ori-hq:` provenance, only in the Personal HQ); it changes only through review on the Profile page. An entry with `global:<item-id>` provenance was copied from global memory and is ordinary memory. `global_memory` says whether global memory exists (`available`), whether this workspace is the Personal HQ (`is_personal_hq`), and otherwise carries the shared `message` and a `setup_href`. `404` if the workspace doesn't exist; `503` if folder storage is unavailable.

`in_prompt` (boolean, on every entry) says whether the entry is in the `## Workspace Memory` prompt section agents receive. It is computed with the same selection the prompt builder uses (watch and thread entries first, then newest first, within `char_budget`), so `false` marks exactly the entries the prompt drops when memory is over budget. A `"managed": true` line is always `false`: reviewed global-memory lines are never part of that section, and they do not count against its budget.

### Add Memory Entry

**Endpoint:** `POST /api/workspaces/{workspaceID}/memory/entries`

```json
{ "text": "releases are human-triggered from main", "type": "fact" }
```

`type` defaults to `fact`. The server fills in today's date and provenance `user`. Returns the full updated memory document (same shape as GET). `400` on empty/over-length/secret-looking text.

### Update Memory Entry

**Endpoint:** `PUT /api/workspaces/{workspaceID}/memory/entries/{index}`

Body identical to add. `index` is the structured-entry index from GET. Returns the updated document. `400` for a non-numeric index or invalid text; `404` when the index is out of range; `409` with code `memory_entry_managed` for a reviewed global-memory line.

### Delete Memory Entry

**Endpoint:** `DELETE /api/workspaces/{workspaceID}/memory/entries/{index}`

Removes one entry by index and returns the updated document. `404` when the index is out of range; `409` with code `memory_entry_managed` for a reviewed global-memory line.

### Promote an Entry to Global Memory

**Endpoint:** `POST /api/workspaces/{workspaceID}/memory/entries/{index}/promote`

```json
{ "text": "I reference my mixes at -14 LUFS.", "category": "how_you_work" }
```

Copies one ordinary entry into global memory as an approved fact, because the user performed the action. Both fields are optional: `text` defaults to the entry's text, and `category` (`how_you_work`, `people`, `projects`, `routines`) defaults to `how_you_work` for a `feedback` entry and `projects` otherwise. The workspace entry stays where it is. The approved fact records the source workspace and the original entry as evidence and never consumes proposal quota.

```json
{ "status": "promoted", "created": true, "item_id": "…", "href": "/profile#personalHQKnowledge", "message": "Promoted to global memory. Your assistant now knows it everywhere." }
```

`status` is `already_known` when global memory already holds the text (repeating a promote changes nothing; an entry in the Personal HQ itself is already global), or `already_pending` when the same fact is waiting for review. `409 memory_entry_not_promotable` for a reviewed (`ori-hq:`) or copied (`global:`) entry, `409 global_memory_unavailable` with the shared message when there is no Personal HQ, `400` for invalid text or category, `404` for an index out of range.

## Workspace Outputs API

Task runs save their results under the workspace's own `outputs/` folder. These two endpoints read that folder; neither writes anything, and neither creates the folder. Both accept a group's id as they accept a workspace's. Home's file tree uses them for its **Outputs** section. To open the folder in the file manager, use the existing `POST /api/workspaces/{workspaceID}/output-dir/open`.

### List Outputs

**Endpoint:** `GET /api/workspaces/{workspaceID}/outputs/tree`

```json
{
  "files": [
    { "id": "folder:reports", "source": "workspace_output", "name": "reports", "relative_path": "reports", "size": 96, "is_dir": true, "mod_time": "2026-10-05T08:00:00+09:00" },
    { "id": "file:reports/weekly.md", "source": "workspace_output", "name": "weekly.md", "relative_path": "reports/weekly.md", "url": "/api/workspaces/{workspaceID}/outputs/reports/weekly.md", "size": 1280, "is_dir": false, "mod_time": "2026-10-05T08:00:00+09:00" }
  ],
  "workspace": "{workspaceID}"
}
```

`files` is one flat list of every folder and file, sorted by `relative_path`, in the shape `GET /api/workspaces/{workspaceID}/files/tree` answers with. Symbolic links are left out, and so is anything whose path has a part starting with `.`. A workspace with no `outputs/` folder, or an empty one, answers `200` with `"files": []`. `404` if the workspace doesn't exist.

### Read One Output

**Endpoint:** `GET /api/workspaces/{workspaceID}/outputs/{relativePath...}`

Answers with the file's bytes. `relativePath` is the listing's `relative_path` with each part URL-encoded (the listing's `url` is already built that way).

| Status | When |
|---|---|
| `400` | The path is empty, absolute, or has a `..` part anywhere (encoded or not), or a symbolic link carries it outside `outputs/`. |
| `404` | The workspace, the `outputs/` folder or the file doesn't exist; the path is a folder; or a part of it starts with `.` (a hidden file answers exactly as a missing one). |

Every answer carries `X-Content-Type-Options: nosniff` and `Content-Security-Policy: default-src 'none'; style-src 'unsafe-inline'; sandbox`. The `Content-Type` comes from the file's extension, except that nothing a browser would run keeps its type: HTML, XML, JavaScript and every other `text/*` type are sent as `text/plain; charset=utf-8`. An SVG keeps `image/svg+xml` so an `<img>` can draw it; the policy stops its scripts if it is opened directly. An unknown extension is `application/octet-stream`.

A file named `tree` at the top of `outputs/` is listed but cannot be read: that address is the listing.

## Scheduler Nodes API

Scheduler nodes enable automatic execution of tasks at scheduled times on the workspace canvas. They support various schedule types including cron expressions, intervals, daily/weekly schedules, and relative delays.

### Create Scheduler Node

Create a new scheduler node on the workspace canvas.

**Endpoint:** `POST /api/orchestration/workspaces/:workspace_id/scheduler-nodes`

**Request Body:**
```json
{
  "name": "Daily Data Sync",
  "prompt": "Sync data from external API",
  "to": "data-agent",
  "schedule_type": "cron",
  "cron_expression": "0 9 * * *",
  "enabled": true,
  "x": 400,
  "y": 300
}
```

**Parameters:**
- `name` (required): Display name for the scheduler node
- `prompt` (required): Task description/prompt to execute
- `to` (required): Target agent name to execute the task
- `schedule_type` (required): One of: `"interval"`, `"daily"`, `"weekly"`, `"cron"`, `"relative_delay"`
- `enabled` (optional): Whether scheduler is enabled (default: `true`)
- `x`, `y` (optional): Canvas position coordinates

**Schedule Type Parameters:**
- For `interval`: `interval_minutes` (integer)
- For `daily`: `time_of_day` (string, format: "HH:MM")
- For `weekly`: `day_of_week` (integer, 0-6), `time_of_day` (string)
- For `cron`: `cron_expression` (string, 5-field cron format)
- For `relative_delay`: `delay_duration` (duration string, e.g., "5m", "1h")

**Optional Parameters:**
- `max_runs` (integer): Maximum number of executions (0 = unlimited)
- `end_date` (string, ISO 8601): Stop executing after this date
- `priority` (string): Task priority (`"low"`, `"medium"`, `"high"`)

**Response:**
```json
{
  "id": "sched-abc123",
  "name": "Daily Data Sync",
  "enabled": true,
  "next_run": "2025-12-05T09:00:00Z",
  "execution_count": 0,
  "created_at": "2025-12-04T22:00:00Z"
}
```

### Update Scheduler Node

Update an existing scheduler node configuration.

**Endpoint:** `PUT /api/orchestration/workspaces/:workspace_id/scheduler-nodes/:node_id`

**Request Body:**
```json
{
  "name": "Updated Name",
  "enabled": false,
  "cron_expression": "0 10 * * *"
}
```

**Response:**
```json
{
  "id": "sched-abc123",
  "name": "Updated Name",
  "enabled": false,
  "next_run": null,
  "execution_count": 5
}
```

### Delete Scheduler Node

Remove a scheduler node from the workspace.

**Endpoint:** `DELETE /api/orchestration/workspaces/:workspace_id/scheduler-nodes/:node_id`

**Response:**
```json
{
  "success": true,
  "message": "Scheduler node deleted"
}
```

### Trigger Scheduler Node Manually

Execute a scheduler node immediately, regardless of its schedule.

**Endpoint:** `POST /api/orchestration/workspaces/:workspace_id/scheduler-nodes/:node_id/trigger`

**Response:**
```json
{
  "task_id": "task-xyz789",
  "message": "Scheduler triggered successfully"
}
```

### List Scheduler Nodes

Get all scheduler nodes for a workspace.

**Endpoint:** `GET /api/orchestration/workspaces/:workspace_id/scheduler-nodes`

**Response:**
```json
{
  "scheduler_nodes": [
    {
      "id": "sched-abc123",
      "name": "Daily Data Sync",
      "enabled": true,
      "schedule_type": "cron",
      "cron_expression": "0 9 * * *",
      "next_run": "2025-12-05T09:00:00Z",
      "last_run": "2025-12-04T09:00:00Z",
      "execution_count": 5,
      "x": 400,
      "y": 300
    }
  ]
}
```

### Cron Expression Format

Scheduler nodes support standard 5-field cron expressions:

```
* * * * *
│ │ │ │ │
│ │ │ │ └─ Day of week (0-6, 0=Sunday)
│ │ │ └─── Month (1-12)
│ │ └───── Day of month (1-31)
│ └─────── Hour (0-23)
└───────── Minute (0-59)
```

**Examples:**
- `0 9 * * *` - Daily at 9:00 AM
- `*/15 * * * *` - Every 15 minutes
- `0 0 * * 0` - Every Sunday at midnight
- `0 14 * * 1-5` - Weekdays at 2:00 PM
- `30 6 1 * *` - First day of month at 6:30 AM

**Special Characters:**
- `*` - Any value
- `*/n` - Every n units (e.g., `*/5` = every 5 minutes)
- `n-m` - Range (e.g., `1-5` = Monday through Friday)
- `n,m` - List (e.g., `1,3,5` = Monday, Wednesday, Friday)

### Real-Time Events

Scheduler nodes emit Server-Sent Events (SSE) for real-time updates:

**Event Types:**
- `scheduled_task.triggered` - Scheduler executed and created a task
- `scheduled_task.completed` - Scheduler execution completed successfully
- `scheduled_task.failed` - Scheduler execution failed

**Event Data:**
```json
{
  "type": "scheduled_task.triggered",
  "data": {
    "scheduled_task_id": "sched-abc123",
    "scheduled_task": {
      "id": "sched-abc123",
      "name": "Daily Data Sync",
      "execution_count": 6,
      "next_run": "2025-12-05T09:00:00Z",
      "last_execution": "2025-12-04T09:00:00Z"
    },
    "task_id": "task-xyz789",
    "timestamp": "2025-12-04T09:00:00Z"
  }
}
```

## Workspace Map Activity API

The maps show work while it happens and deliver each finished result as a
**parcel** that waits until it is opened. Work is a task run, a Daily Brief
generation, or an unattended File Janitor scan.

The feed carries names, ids, phases, outcomes and counts only. It never carries
tool arguments, tool results, task descriptions, brief content, file names or
error text.

**Feature flags**

| Variable | Default | Effect |
|---|---|---|
| `ORI_MAP_SHOW_ENABLED` | on | `0`, `false`, `no`, `off` or `disabled` turns the feature off: every endpoint below answers 404, no parcels are made, and the maps look as they did before. |
| `ORI_DEV_SCRIPTED_TASK_RUNS` | off | **Development only.** `1` makes every task run play a fixed script of real events (thinking, two tool calls, thinking, done) instead of calling a model. `[fail]` in a task's description makes it fail after its first tool call. Never set it outside a demo. |

### Get Activity Snapshot

**Endpoint:** `GET /api/workspace-map/activity`

What is running now, and every unopened parcel.

**Response:**
```json
{
  "running": [
    {
      "kind": "task",
      "activity_id": "task:ws-1:t-1",
      "workspace_id": "ws-1",
      "agent_name": "Theo",
      "task_id": "t-1",
      "started_at": "2026-09-17T10:00:00Z",
      "blocked": false,
      "step": { "type": "tool_call", "tool_name": "web_search" },
      "at": "2026-09-17T10:00:04Z"
    }
  ],
  "parcels": [
    {
      "id": "p-1",
      "workspace_id": "ws-1",
      "kind": "task",
      "title": "Compare the launch notes",
      "agent_name": "Theo",
      "outcome": "succeeded",
      "produced_at": "2026-09-17T09:58:00Z"
    }
  ]
}
```

### Activity Stream

**Endpoint:** `GET /api/workspace-map/activity/stream` (Server-Sent Events)

The first event is always a `snapshot` with the same body as the snapshot
endpoint. After that the stream sends `activity` and `parcel` events, and a
comment line every 15 seconds to keep the connection open. A client that falls
behind may miss events; reconnecting sends a fresh snapshot.

**`activity` event**
```json
{
  "kind": "task",
  "phase": "finished",
  "activity_id": "task:ws-1:t-1",
  "workspace_id": "ws-1",
  "agent_name": "Theo",
  "task_id": "t-1",
  "outcome": "succeeded",
  "parcel_id": "p-1",
  "at": "2026-09-17T10:00:12Z"
}
```

| Field | Values |
|---|---|
| `kind` | `task`, `daily_brief`, `file_janitor` |
| `phase` | `started`, `step`, `blocked`, `resumed`, `finished` |
| `step.type` | `thinking`, `tool_call`, `tool_result` (with `ok`), `delegation` — task steps only |
| `outcome` | `succeeded`, `partial`, `failed`, `timeout`; empty when a run stopped without finishing (cancelled, deleted, or silent for 10 minutes) |
| `count` | File Janitor finishes only: new files waiting for review |
| `parcel_id` | Set when the finish produced a parcel |

Daily Brief and File Janitor activities send only `started` and `finished`.

**`parcel` event** — a parcel row (as in the snapshot). With `"opened": true` the
parcel is no longer waiting: it was opened, swept after 14 days, or deleted with
its task or workspace.

**Which finishes produce a parcel**

- A task run that completed, failed or timed out — except a Farm run while the
  City Economy is on (that is its Harvest) and a ticket accepted by hand.
- A Daily Brief generated **by the schedule**. A brief the user opened or
  refreshed gets none.
- A File Janitor scan that found **at least one** new file to review.

### Open a Parcel

**Endpoint:** `POST /api/workspace-map/parcels/{id}/open`

Marks the parcel opened (the first call only) and returns the result card. Safe
to call more than once. 404 when the parcel does not exist.

**Response:**
```json
{
  "parcel": {
    "id": "p-1",
    "workspace_id": "ws-1",
    "kind": "task",
    "ref_id": "t-1",
    "title": "Compare the launch notes",
    "agent_name": "Theo",
    "outcome": "succeeded",
    "started_at": "2026-09-17T09:56:18Z",
    "produced_at": "2026-09-17T09:58:00Z",
    "opened_at": "2026-09-17T10:02:00Z"
  },
  "duration_seconds": 102,
  "summary": "Two dates disagree.",
  "rewards": {
    "xp": {
      "awarded": 50,
      "level_before": 1,
      "level_after": 2,
      "progress_before": 0.8,
      "progress_after": 0.3,
      "stage_before": "spark",
      "stage_after": "infant"
    },
    "craft": 5
  }
}
```

`summary` is present for a successful run and `failure_reason` for a failed one.
Brief and janitor parcels carry a fixed sentence as their summary ("Your brief
for Tuesday is ready.", "3 files are ready to review."). `rewards` and each of
its fields are left out when nothing was awarded.

### Open Parcels by Reference

**Endpoint:** `POST /api/workspace-map/parcels/open-by-ref`

Called by any other place a result is seen, so its parcel stops waiting.

**Request Body:**
```json
{ "kind": "task", "workspace_id": "ws-1", "ref_id": "t-1" }
```

`kind` is `task`, `daily_brief` or `file_janitor`. `ref_id` is required for a
task. The Daily Brief and the File Janitor console show everything that is
waiting, so they leave `ref_id` out and open every parcel of their kind in the
workspace.

**Response:**
```json
{ "opened": 1 }
```

## Custom Workflows API

Custom workflows allow users to save selections of canvas nodes as reusable templates. These workflows can then be instantiated in any studio to recreate the node configuration.

### List Workflows

Get all custom workflows.

**Endpoint:** `GET /api/workflows`

**Response:**
```json
{
  "workflows": [
    {
      "id": "wf-abc123",
      "name": "My Data Pipeline",
      "description": "A workflow for processing data",
      "category": "Data Processing",
      "source": "custom",
      "node_count": 5,
      "agent_names": ["data-agent", "processor-agent"],
      "created_at": "2025-12-04T22:00:00Z",
      "updated_at": "2025-12-04T22:00:00Z"
    }
  ],
  "count": 1
}
```

### Create Workflow

Create a new custom workflow from selected nodes.

**Endpoint:** `POST /api/workflows`

**Request Body:**
```json
{
  "name": "My Data Pipeline",
  "description": "A workflow for processing data",
  "category": "Data Processing",
  "nodes": [
    {
      "id": "node-1",
      "type": "task",
      "config": {
        "description": "Process incoming data",
        "to": "data-agent",
        "from": "user"
      },
      "relative_x": -100,
      "relative_y": 0
    },
    {
      "id": "node-2",
      "type": "agent",
      "config": {
        "name": "data-agent"
      },
      "relative_x": 100,
      "relative_y": 0
    }
  ],
  "internal_connections": [
    {
      "id": "conn-1",
      "from_node": "node-1",
      "from_port": "out",
      "to_node": "node-2",
      "to_port": "in"
    }
  ],
  "layout": {
    "width": 400,
    "height": 300,
    "node_positions": {
      "node-1": {"x": -100, "y": 0},
      "node-2": {"x": 100, "y": 0}
    }
  }
}
```

**Parameters:**
- `name` (required): Display name for the workflow
- `description` (optional): Description of the workflow
- `category` (optional): Category for organization
- `nodes` (required): Array of workflow nodes (max 20)
  - `id`: Unique node identifier
  - `type`: Node type - `"task"`, `"agent"`, `"scheduler"`, `"store"`, `"attachment"`
  - `config`: Type-specific configuration object
  - `relative_x`, `relative_y`: Position relative to workflow center
- `internal_connections` (optional): Array of connections between nodes
- `input_ports`, `output_ports` (optional): External connection points
- `layout` (optional): Layout dimensions and positions

**Response:**
```json
{
  "id": "wf-abc123",
  "name": "My Data Pipeline",
  "message": "Workflow created successfully"
}
```

### Get Workflow

Get a specific workflow by ID.

**Endpoint:** `GET /api/workflows/:id`

**Response:**
```json
{
  "id": "wf-abc123",
  "name": "My Data Pipeline",
  "description": "A workflow for processing data",
  "category": "Data Processing",
  "source": "custom",
  "nodes": [...],
  "internal_connections": [...],
  "layout": {...},
  "created_at": "2025-12-04T22:00:00Z",
  "updated_at": "2025-12-04T22:00:00Z"
}
```

### Delete Workflow

Delete a custom workflow. Only custom workflows can be deleted.

**Endpoint:** `DELETE /api/workflows/:id`

**Response:** `204 No Content`

**Error Responses:**
- `403 Forbidden` - Cannot delete built-in workflows
- `404 Not Found` - Workflow not found

### Check Agent Availability

Check if all agents required by a workflow are available in a specific studio.

**Endpoint:** `POST /api/workflows/:id/check-agents`

**Request Body:**
```json
{
  "studio_id": "studio-xyz789"
}
```

**Response:**
```json
{
  "available": false,
  "missing_agents": ["processor-agent"],
  "required_agents": ["data-agent", "processor-agent"]
}
```

### Node Types and Configuration

#### Task Node
```json
{
  "type": "task",
  "config": {
    "description": "Task description",
    "to": "agent-name",
    "from": "user",
    "priority": 0,
    "status": "pending"
  }
}
```

#### Agent Node
```json
{
  "type": "agent",
  "config": {
    "name": "agent-name",
    "model": "gpt-4o"
  }
}
```

#### Scheduler Node
```json
{
  "type": "scheduler",
  "config": {
    "name": "Daily Sync",
    "schedule_type": "cron",
    "cron_expression": "0 9 * * *",
    "enabled": true
  }
}
```

#### Store Node
```json
{
  "type": "store",
  "config": {
    "name": "Data Store",
    "store_type": "file",
    "file_path": "/path/to/data"
  }
}
```

#### Attachment Node
```json
{
  "type": "attachment",
  "config": {
    "title": "Reference Doc",
    "type": "note",
    "body": "Content here",
    "link_url": ""
  }
}
```

## Event Triggers API

Event triggers start a run when something happens externally — an incoming webhook or a local file change — instead of only on the mission cadence. A trigger's **action** either runs the workspace mission (`mission_run`, same path and autonomy gate as cadence) or creates a workspace task from a stored prompt (`task_prompt`). Trigger definitions persist in each workspace's folder (`triggers.json`); disk is the source of truth.

### Webhook Ingestion (public)

```
POST /api/hooks/{token}
```

The `{token}` is the per-trigger, unguessable identifier returned when a webhook trigger is created (or regenerated). This is the only authentication: keep the URL secret. If the trigger defines a shared secret, callers must also send it.

**Headers:**
- `Content-Type`: one of `application/json`, `text/plain`, `application/x-www-form-urlencoded`, `multipart/form-data` (others → `415`).
- `X-Ori-Webhook-Secret`: required only when the trigger has a shared secret.

**Responses:**
- `202 Accepted` — `{ "accepted": true, "fire_id": "fire-…" }`. The event is processed asynchronously; correlate the eventual run/task via the `fires` endpoint using `fire_id`.
- `401 Unauthorized` — missing or wrong shared secret.
- `404 Not Found` — unknown **or** disabled token (deliberately indistinguishable).
- `413 Payload Too Large` — body over 64 KB.
- `415 Unsupported Media Type` — unsupported content type.
- `429 Too Many Requests` — over the per-trigger rate limit (default 60/min).

```bash
curl -X POST http://localhost:8765/api/hooks/zn5_abitiyNmgT9vEdqbAZPKJtk_6SlP3sScpElkLho \
  -H 'Content-Type: application/json' \
  -d '{"action":"opened","number":42}'
```

Bursts of events within a trigger's debounce window (default 2 s) coalesce into a single run. While a run is in flight, further events merge into one pending follow-up run (persisted, so it survives a restart).

> **Security note — untrusted payloads reach the agent prompt.** A webhook body (and file event detail) is injected into the triggered run's prompt, capped at 64 KB, so a caller can attempt prompt injection (e.g. a body full of "ignore previous instructions…"). There is no content sanitization layer; the mitigations are the unguessable token, the optional shared secret, the per-trigger rate limit, and — critically — the workspace autonomy policy, which gates what the run is actually allowed to do. Only point triggers you control (or trust) at a workspace, and keep the autonomy policy no higher than the workspace needs.

### List Triggers

```
GET /api/workspaces/{workspaceID}/triggers
```

Returns `{ "triggers": [ … ] }`. Each trigger includes a computed `webhook_url` and `has_secret`; the shared secret itself is never returned.

### Create Trigger

```
POST /api/workspaces/{workspaceID}/triggers
```

Webhook example (token is generated server-side):
```json
{
  "name": "pr-opened",
  "type": "webhook",
  "enabled": true,
  "action": { "kind": "mission_run" },
  "webhook": { "secret": "optional-shared-secret" }
}
```

File-watch example:
```json
{
  "name": "invoice-drop",
  "type": "file_watch",
  "enabled": true,
  "action": { "kind": "task_prompt", "agent": "bookkeeper", "prompt": "File the dropped invoice." },
  "file_watch": { "path": "/Users/you/Downloads/invoices", "glob": "*.pdf", "events": ["create"] },
  "debounce_seconds": 2
}
```

`201 Created` returns the trigger view; for webhooks the full `webhook_url` is included so it can be copied once.

### Get / Update / Delete Trigger

```
GET    /api/workspaces/{workspaceID}/triggers/{triggerID}
PUT    /api/workspaces/{workspaceID}/triggers/{triggerID}
DELETE /api/workspaces/{workspaceID}/triggers/{triggerID}
```

`PUT` accepts any subset of `name`, `enabled`, `action`, `file_watch`, `webhook` (secret only — omit to keep the existing token), and `debounce_seconds`.

### Enable / Disable / Regenerate / Test / History

```
POST /api/workspaces/{workspaceID}/triggers/{triggerID}/enable
POST /api/workspaces/{workspaceID}/triggers/{triggerID}/disable
POST /api/workspaces/{workspaceID}/triggers/{triggerID}/regenerate-token   # new URL; old stops working immediately
POST /api/workspaces/{workspaceID}/triggers/{triggerID}/test-fire          # dispatch a synthetic event now
GET  /api/workspaces/{workspaceID}/triggers/{triggerID}/fires              # recent fire history (run/task IDs, errors)
```

Trigger failures (mission disabled, watched folder removed, run-creation error) are recorded on the trigger and surfaced as Action Center findings for the workspace.

---

## Workspace Plans

A **Plan** is the durable, versioned record of work Ori intends to do: it moves
from clarification through drafting, review, explicit approval, materialization
into Tasks, execution, and completion. Compiled Ori code owns every lifecycle
transition — a model may propose content and nothing else.

The one rule worth learning first: **an approval is the only thing that creates
work.** No preset, confirmation mode, chat message, skill, or agent output
substitutes for it.

### Lifecycle statuses

```
draft → needs_input → draft → in_review → approved → executing → completed
                                       ↘ (rejected / changes requested) → draft
                                         executing → paused → executing
                                         any → cancelled / failed
```

- `draft` — being written. Nothing exists yet.
- `needs_input` — waiting on the user's answers to clarifying questions.
- `in_review` — an immutable version is snapshotted and awaiting a decision.
- `approved` — an approval was consumed; Tasks exist.
- `executing` / `paused` — work is running, or stopped at a safe point.
- `completed` / `failed` / `cancelled` — terminal.

Cancelled Plans move to History immediately. Inactive `draft` and `needs_input`
Plans move to History after 30 days. **Archiving is a placement, never a
deletion** — versions, approvals, Task links, and history all survive it.

### Plans

```
GET    /api/workspaces/{workspaceID}/plans?scope=active|history|all
POST   /api/workspaces/{workspaceID}/plans
GET    /api/workspaces/{workspaceID}/plans/{planID}
DELETE /api/workspaces/{workspaceID}/plans/{planID}
POST   /api/workspaces/{workspaceID}/plans/{planID}/archive
POST   /api/workspaces/{workspaceID}/plans/{planID}/reopen
```

`DELETE` hard-deletes **only** a Plan that was never approved and has no linked
Tasks, Runs, or artifacts. Anything else returns `plan_not_deletable`; archive
it instead. Materialized work is never silently removed.

### Drafting and clarification

```
PATCH  /api/workspaces/{workspaceID}/plans/{planID}/draft
GET    /api/workspaces/{workspaceID}/plans/{planID}/revision?section=<id>
POST   /api/workspaces/{workspaceID}/plans/{planID}/clarifications/{questionID}
GET    /api/workspaces/{workspaceID}/plans/{planID}/snapshots
POST   /api/workspaces/{workspaceID}/plans/{planID}/snapshots/{snapshotID}/recover
```

`PATCH .../draft` takes a `revision` for optimistic concurrency. A stale write
returns `stale_draft` **with the winning revision**, so the client can offer
recovery rather than silently losing an edit. Editing needs no model.

Autosave snapshots are pruned to the latest 10 and 30 days. They are recovery
points, never review versions.

### Review, approval, and materialization

```
GET    /api/workspaces/{workspaceID}/plans/{planID}/versions
POST   /api/workspaces/{workspaceID}/plans/{planID}/versions      # snapshot for review
GET    /api/workspaces/{workspaceID}/plans/{planID}/versions/{n}
GET    /api/workspaces/{workspaceID}/plans/{planID}/compare?from=1&to=2
POST   /api/workspaces/{workspaceID}/plans/{planID}/decision      # request_changes | reject
GET    /api/workspaces/{workspaceID}/plans/{planID}/approvals
POST   /api/workspaces/{workspaceID}/plans/{planID}/approvals
POST   /api/workspaces/{workspaceID}/plans/{planID}/materialize
```

An approval binds to one exact `version` **and** its `content_hash`. If the
version, hash, workspace, or declared effect no longer matches, it is refused.

**Approval effects** — the label the user sees is the behavior they get:

| Effect | Button | What it does |
|---|---|---|
| `create_tasks` | Approve and Create Tasks | Creates the Tasks. Starts nothing. |
| `create_tasks_and_start` | Approve and Start | Creates the Tasks and begins automatic execution. |

Approval and materialization are separate calls on purpose: a retried
materialization **replays** the original result rather than doing the work
twice. Task IDs are deterministic, so a crash mid-materialization is safe to
retry.

For software-project workspaces, compiled artifact policy replaces model-chosen
PRD/task-list paths with stable repository paths derived from the Plan's
immutable request and ID: `<tasks_dir>/prd-<feature>.md` and
`<tasks_dir>/tasks-<feature>.md`. When that project is Ori's `dev` worktree, the
materialization response also includes a `handoff` (`kind`, `feature`,
`command`). The command enters `wt start` directly; the approved task list is
already complete, so callers must not run `wt plan` again.

Maximum 50 review versions per Plan. The 51st is refused with split/supersede
guidance — history is never evicted.

### Revision and reconciliation

```
POST   /api/workspaces/{workspaceID}/plans/{planID}/revise-approved   # intent: additive|corrective|superseding
GET    /api/workspaces/{workspaceID}/plans/{planID}/reconcile         # preview
POST   /api/workspaces/{workspaceID}/plans/{planID}/reconcile         # confirm one exact preview
```

The preview reports what approving a revision would do to work the earlier
approval created: `retained`, `created`, `cancel`, `replace`, `immutable`,
`follow_up`. It carries a `token` hashing both versions **and every linked
Task's status**; a confirmation whose token no longer matches is refused with
`stale_preview`, because a Task may have started since.

An additive revision cancels nothing and needs no separate confirmation.
Corrective and superseding revisions do. Work that already ran is never
cancelled — it gets follow-up work beside it.

### Execution

```
POST   /api/workspaces/{workspaceID}/plans/{planID}/execution
GET    /api/workspaces/{workspaceID}/plans/{planID}/cancel-preview
GET    /api/workspaces/{workspaceID}/plan-execution-slot
```

`execution` takes an `action`: `start`, `retry`, `pause`, `resume`, `skip`,
`cancel`, `complete`, `fail`.

**One Plan executes per workspace.** A competing Plan queues visibly and is told
what it waits behind and its position. Pausing stops future dispatch
immediately but releases the slot only once in-flight work reaches a safe stop;
resuming rejoins the queue rather than displacing the current holder.

Standalone Tasks — the ones no Plan materialized — keep their own scheduler,
global maximum, and provider limits. The slot sits above the Task executor and
does not touch them.

### Reverse lookups

```
GET    /api/workspaces/{workspaceID}/plan-for-task/{taskID}
GET    /api/workspaces/{workspaceID}/plan-for-run/{runID}
```

Returns a compact summary plus a canonical `url`. Build links from that field
rather than composing the route, so every entry point lands on the same
surface.

### Policy and diagnostics

```
GET    /api/workspaces/{workspaceID}/planning-policy[?preset=<name>]
GET    /api/workspaces/{workspaceID}/plan-diagnostics
```

`planning-policy` separates advisory guidance, compiled artifact outputs, and
enforced controls; those boundaries are truth claims:

- **`guidance`** — style, clarification depth, preferred artifacts, tone. These
  are *asked* of the planner. Nothing verifies them, and the UI must not
  describe them with "required", "guaranteed", or "will".
- **`artifacts`** — compiled output directory and PRD/task-list write choices.
  These values are applied to Plan content before review; they are not merely
  requested from the model.
- **`enforced`** — each control names a compiled adapter that actually runs.
  Every one reports `enabled` (the user's setting) and `available` (whether
  this workspace can honor it) as **separate facts**, plus a machine-readable
  `reason` when unavailable.

Enforced control keys: `plan_approval`, `task_materialization`,
`execution_mode`, `repo_scan`, `safe_branch`, `handoff_confirmation`,
`artifact_write`, `note_creation`, `destructive_confirmation`.

Unavailable reasons: `not_a_repository`, `no_workspace_folder`,
`not_applicable_to_profile`.

The `preset` query previews a preset **without saving it**, running the same
computation against the same workspace capabilities.

`plan-diagnostics` reports which components are wired and whether generation is
available. Reading, editing, reviewing, approving, materializing, and executing
all work **offline**; only drafting needs a model.

#### Profile defaults

| Profile | Default preset | Planning | Notes |
|---|---|---|---|
| General | Guided | disabled | Enabling assumes no repository, PRD, task list, or branch |
| Research | Guided | disabled | Enabling selects investigation style, deep clarification, no branch |
| Software Project | Planner | enabled | Repo inspection, PRD + task list, `step_through` |

A branch precondition is enforced **only** in a recognized version-controlled
repository. Autonomous may select automatic execution but retains approval and
every invariant safety gate.

### Error codes

Every failure carries a stable `code` alongside its message:

| Code | HTTP | Meaning |
|---|---|---|
| `plan_not_found` | 404 | Unknown Plan, or one owned by another workspace |
| `plan_version_not_found` | 404 | Unknown version |
| `plan_approval_not_found` | 404 | Unknown approval |
| `reconciliation_not_found` | 404 | A revision needs a confirmed reconciliation first |
| `stale_draft` | 409 | Draft revision moved; carries the winning revision |
| `stale_version` | 409 | Decision targeted a superseded version |
| `stale_preview` | 409 | Work changed since the reconciliation preview |
| `approval_mismatch` | 409 | Version, hash, workspace, or effect no longer matches |
| `approval_consumed` | 409 | Already spent; retry replays its result |
| `reconciliation_consumed` | 409 | Confirmation already applied |
| `invalid_transition` | 409 | Not a legal status edge |
| `materialization_conflict` | 409 | Another caller is materializing |
| `execution_conflict` | 409 | Another Plan holds the workspace slot |
| `plan_not_deletable` | 409 | Has approvals, Tasks, Runs, or artifacts |
| `approval_authority_required` | 403 | Only an explicit user action may approve |
| `validation_failed` | 422 | Typed content validation |
| `credential_in_content` | 422 | Content carries something shaped like a credential |
| `limit_exceeded` | 422 | Over a hard bound; content is never truncated |
| `unavailable_capability` | 422 | A required agent or capability is missing |
| `unsafe_path` | 422 | Artifact path escapes the workspace |
| `model_unavailable` | 503 | Generation only; everything else still works |

### Action Preview (not a Plan)

Chat may preview **one immediate action** the user already named — a `/tool`
command or a single routed utility tool — and approve it inline. Its ID is
prefixed `preview_`, never `plan_`.

Anything that creates Tasks, carries dependencies, spans agents, or hands off to
a specialist is Plan work: chat opens a durable Plan and returns its canonical
link instead. Read-only requests and flows with their own confirmation (mail
send, destructive-action confirmation) stay as they are.

### Legacy planning skill

The repository-local `workspace-planning` skill and its `planning_profile`
contract are **removed**. A workspace upgraded from that design may still carry
the binding; its values have **zero effect** and are not migrated. The settings
endpoint reports a one-time `legacy_planning_notice`, and the next settings save
discards that binding only — unrelated skill bindings are untouched.

---

This API reference provides complete documentation for integrating with Ori Agent programmatically. For additional help or examples, refer to the main [README](README.md) or check the web interface implementation in the `internal/web/` directory.
