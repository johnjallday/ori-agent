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
cross-workspace **home snapshot** (workspaces, windowed task activity, recent
sessions, open Action Center opportunities, usage), runs the system model with
read-only `home_*` tools, and returns a written answer plus grounded next-step
actions. Used by the home page "Ask Ori" panel when
`/api/home-assistant/route` classifies the prompt as `app_introspection` or
`app_navigation` (route mode `home_inline`).

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
- `type`: One of `navigate`, `open_workspace`, `open_task`, `open_session`, `create_workspace`, `create_task`, `start_task`, `ask_followup`.
- `label`: Button label.
- `href` (optional): Destination for navigation/`open_*` actions.
- `workspace_id` / `task_id` / `session_id` (optional): Resolved target ids.
- `requires_confirmation` (optional): `true` for state-changing actions.
- `confirmation_summary` / `arguments` (optional): Confirmation copy and payload.

**Confirmation flow (mutations):**
An explicit mutation request (e.g. `"create a workspace called Q3 Planning"`) returns a confirmation instead of acting:
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
The client confirms by re-calling the endpoint with `confirmed_action` set to a `HomeAction` of that type and arguments. The server executes only known mutation types after confirmation; the model is never given write tools.

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

This API reference provides complete documentation for integrating with Ori Agent programmatically. For additional help or examples, refer to the main [README](README.md) or check the web interface implementation in the `internal/web/` directory.
