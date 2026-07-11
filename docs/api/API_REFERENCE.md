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
- [Scheduler Nodes API](#scheduler-nodes-api)
- [Custom Workflows API](#custom-workflows-api)
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
  "type": "tool-calling",
  "model": "gpt-4o-mini",
  "temperature": 0.7,
  "system_prompt": "You are a helpful assistant."
}
```

**Parameters:**
- `name` (required): Name of the agent
- `type` (optional): Agent type - `"tool-calling"`, `"general"`, or `"research"`. Defaults to `"tool-calling"`. If `model` is provided without `type`, the type is auto-detected based on the model.
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
  "suggested_agent_name": "Travel Planner",
  "suggested_agent_type": "research"
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
- `suggested_agent_type`: Suggested type when creating a new agent.

**No-match example:**
```json
{
  "intent": "email_check",
  "intent_label": "email triage",
  "score": 0,
  "requires_creation": true,
  "suggested_agent_name": "Email Assistant",
  "suggested_agent_type": "tool-calling"
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
- `type`: One of `navigate`, `open_workspace`, `open_task`, `open_session`, `create_workspace`, `create_task`, `start_task`, `assign_agent`, `create_agent`, `remove_agent`, `ask_followup`.
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
  "entry_agent_name": "Client Manager"   // optional; must be an existing agent
}
```

When `entry_agent_name` is omitted, the server auto-creates a `"<Group Name> Manager"` agent (type `general`, role `orchestrator`, workspace-manager system prompt) and sets it as the group's entry agent, so new groups are chat-ready immediately. Name collisions get a numeric suffix (`"<Name> Manager 2"`).

Group folders are provisioned with `sub-workspaces/` (members), plus their own `files/` and `notes/` directories. The auto-provisioned `workspace-files` filesystem MCP binding is **scoped to `files/` and `notes/` only** — member sub-workspaces are never exposed to the group's agents. Groups created before this behavior existed are upgraded automatically by an idempotent backfill at server startup.

**Listing:** the flat `GET /api/workspaces` list includes groups (check `kind` to distinguish them); `GET /api/workspaces?tree=true` returns the nested tree.

**Deleting a group** uses a two-mode flow:

```http
DELETE /api/workspaces/:id?confirm=true&delete_mode=group_only   (default)
DELETE /api/workspaces/:id?confirm=true&delete_mode=contents
```

- `group_only` — un-nests direct members back to the workspaces root (they stay active), then moves the group folder — with its own sessions, notes, and files — to the system Trash. Responds `{ "success": true, "id": "...", "trashed": true }`; restore with `POST /api/workspaces/:id/restore`. With `delete_sessions=true` (or on platforms without Trash support) the group is removed permanently instead.
- `contents` — moves the whole folder tree (group + members) to the Trash and marks every row trashed; restore reactivates the entire subtree. `delete_sessions=true` forces a permanent delete of everything including sessions.

Both modes hard-block with `409 Conflict` while any workspace in the group has active task work.

## Workspace Memory API

Each workspace keeps a curated `MEMORY.md` of durable operational knowledge (facts, decisions, dead ends, watch-state) at the root of its folder. The file on disk is canonical — there is no database copy. Memory is injected into every mission run and workspace chat (capped at ~2,000 tokens) and can be written by agents via the `memory_write` / `memory_forget` tools under every autonomy policy, including Watch.

Each entry is one line: `- [<type>, <YYYY-MM-DD>, <provenance>] <text>`, where `<type>` is one of `fact`, `feedback`, `decision`, `dead-end`, `watch`, `thread`. Entries are capped at 500 characters and obvious secrets are refused (store credentials in the Vault).

### Get Workspace Memory

**Endpoint:** `GET /api/workspaces/{workspaceID}/memory`

```json
{
  "entries": [
    { "index": 0, "type": "watch", "date": "2026-06-13", "provenance": "run:abc (Scout)", "text": "build baseline ~7 min; flag if >10" }
  ],
  "unstructured": ["# Workspace Memory"],
  "raw_size": 149,
  "char_budget": 8000,
  "token_budget": 2000,
  "over_budget": false
}
```

`entries` are the structured lines (with their file-order `index`); `unstructured` holds non-entry lines (the header, hand-written prose) for display. `404` if the workspace doesn't exist; `503` if folder storage is unavailable.

### Add Memory Entry

**Endpoint:** `POST /api/workspaces/{workspaceID}/memory/entries`

```json
{ "text": "releases are human-triggered from main", "type": "fact" }
```

`type` defaults to `fact`. The server fills in today's date and provenance `user`. Returns the full updated memory document (same shape as GET). `400` on empty/over-length/secret-looking text.

### Update Memory Entry

**Endpoint:** `PUT /api/workspaces/{workspaceID}/memory/entries/{index}`

Body identical to add. `index` is the structured-entry index from GET. Returns the updated document. `400` for a non-numeric index or invalid text; `404` when the index is out of range.

### Delete Memory Entry

**Endpoint:** `DELETE /api/workspaces/{workspaceID}/memory/entries/{index}`

Removes one entry by index and returns the updated document. `404` when the index is out of range.

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
    "type": "tool-calling",
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

This API reference provides complete documentation for integrating with Ori Agent programmatically. For additional help or examples, refer to the main [README](README.md) or check the web interface implementation in the `internal/web/` directory.
