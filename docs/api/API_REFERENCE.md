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
- [Plugins API](#plugins-api)
- [Plugin Registry API](#plugin-registry-api)
- [Settings API](#settings-api)
- [Chat API](#chat-api)
- [Updates API](#updates-api)
- [Scheduler Nodes API](#scheduler-nodes-api)
- [Custom Workflows API](#custom-workflows-api)
- [Examples](#examples)

## Authentication

Currently, Ori Agent does not require authentication for local development. API keys are managed through the settings endpoints and stored locally.

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

Get all available agents and the current active agent.

**Endpoint:** `GET /api/agents`

**Response:**
```json
{
  "agents": ["default", "my-agent", "test-agent"],
  "current": "default"
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
- `model` (optional): Model to use (e.g., `"gpt-4o-mini"`, `"claude-3-haiku-20240307"`). Defaults to current agent's model or system default.
- `temperature` (optional): Temperature setting (0.0-2.0). Defaults to current agent's temperature or system default.
- `system_prompt` (optional): Custom system prompt. Defaults to current agent's system prompt or empty.

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

## Plugins API

### List Loaded Plugins

Get all plugins loaded for the current agent.

**Endpoint:** `GET /api/plugins`

**Response:**
```json
{
  "plugins": {
    "math": {
      "definition": {
        "name": "math",
        "description": "Perform basic math operations",
        "parameters": { /* OpenAI function parameters */ }
      },
      "version": "1.0.0",
      "path": "uploaded_plugins/math.so"
    }
  }
}
```

### Upload Plugin

Upload and load a new plugin from a `.so` file.

**Endpoint:** `POST /api/plugins`

**Request:** `multipart/form-data`
- `plugin` (file): The `.so` plugin file

**Example using curl:**
```bash
curl -X POST -F "plugin=@my_plugin.so" http://localhost:8765/api/plugins
```

**Response:**
```json
{
  "success": true,
  "message": "Plugin uploaded and loaded successfully",
  "plugin_name": "my_plugin"
}
```

### Unload Plugin

Remove a plugin from the current agent.

**Endpoint:** `DELETE /api/plugins?name=<plugin_name>`

**Parameters:**
- `name` (query): Name of the plugin to unload

**Response:**
```json
{
  "success": true,
  "message": "Plugin 'math' unloaded successfully"
}
```

### Save Plugin Settings

Save configuration settings for plugins.

**Endpoint:** `POST /api/plugins/save-settings`

**Request Body:**
```json
{
  "plugin_name": {
    "setting_key": "setting_value",
    "api_key": "your-api-key"
  }
}
```

**Response:**
```json
{
  "success": true,
  "message": "Plugin settings saved successfully"
}
```

### Get Plugin Configuration

Get the configuration schema for a specific plugin.

**Endpoint:** `GET /api/plugins/{plugin_name}/config`

**Response:**
```json
{
  "required_config": [
    {
      "name": "api_key",
      "type": "string",
      "description": "Your API key",
      "required": true
    }
  ]
}
```

### Initialize Plugin

Initialize a plugin with configuration values.

**Endpoint:** `POST /api/plugins/{plugin_name}/initialize`

**Request Body:**
```json
{
  "config": {
    "api_key": "your-api-key",
    "endpoint_url": "https://api.example.com"
  }
}
```

**Response:**
```json
{
  "success": true,
  "message": "Plugin initialized successfully"
}
```

### Execute Plugin Function

Execute a plugin function directly (for testing purposes).

**Endpoint:** `POST /api/plugins/execute`

**Request Body:**
```json
{
  "plugin_name": "math",
  "args": "{\"operation\": \"add\", \"a\": 5, \"b\": 3}"
}
```

**Response:**
```json
{
  "result": "8",
  "success": true
}
```

### Check Plugin Initialization Status

Check which plugins need initialization.

**Endpoint:** `GET /api/plugins/init-status`

**Response:**
```json
{
  "uninitialized_plugins": [
    {
      "name": "weather",
      "description": "Weather information plugin",
      "required_config": [
        {
          "name": "api_key",
          "type": "string",
          "description": "Weather API key",
          "required": true
        }
      ]
    }
  ]
}
```

### Plugin Management API (Dedicated Plugins Page)

The following endpoints provide comprehensive plugin management capabilities for the dedicated plugins page at `/plugins`.

#### List All Plugins with Management Metadata

Get all plugins with complete management information including status, categories, permissions, and version history.

**Endpoint:** `GET /api/plugins?management=true`

**Response:**
```json
{
  "plugins": [
    {
      "name": "math",
      "description": "Perform basic math operations",
      "version": "1.0.0",
      "path": "/path/to/plugin",
      "category": "System Tools",
      "permissions": {
        "file_access": false,
        "network_access": false,
        "system_commands": false
      },
      "permissions_approved": true,
      "enabled": true,
      "health_status": "healthy",
      "last_used": "2025-12-03T10:30:00Z",
      "version_history": [
        {
          "version": "1.0.0",
          "timestamp": "2025-12-01T09:00:00Z"
        }
      ],
      "agents": ["default", "test-agent"]
    }
  ]
}
```

#### Get Plugin Details

Get detailed information about a specific plugin.

**Endpoint:** `GET /api/plugins/:name`

**Response:**
```json
{
  "name": "math",
  "description": "Perform basic math operations",
  "version": "1.0.0",
  "path": "/path/to/plugin",
  "category": "System Tools",
  "permissions": {
    "file_access": false,
    "network_access": true,
    "system_commands": false
  },
  "permissions_approved": true,
  "enabled": true,
  "health_status": "healthy",
  "last_used": "2025-12-03T10:30:00Z",
  "version_history": [
    {
      "version": "1.0.0",
      "timestamp": "2025-12-01T09:00:00Z"
    },
    {
      "version": "0.9.0",
      "timestamp": "2025-11-15T14:20:00Z"
    }
  ],
  "agents": ["default", "test-agent"]
}
```

#### Enable Plugin

Enable a plugin for use.

**Endpoint:** `POST /api/plugins/:name/enable`

**Response:**
```json
{
  "success": true,
  "message": "Plugin math enabled"
}
```

#### Disable Plugin

Disable a plugin.

**Endpoint:** `POST /api/plugins/:name/disable`

**Response:**
```json
{
  "success": true,
  "message": "Plugin math disabled"
}
```

#### Update Plugin Configuration

Update plugin configuration settings.

**Endpoint:** `PUT /api/plugins/:name/config`

**Request Body:**
```json
{
  "config": {
    "api_key": "your-api-key",
    "timeout": 30
  }
}
```

**Response:**
```json
{
  "success": true,
  "message": "Configuration updated for plugin math"
}
```

#### Test Plugin

Execute a test call to the plugin with custom arguments.

**Endpoint:** `POST /api/plugins/:name/test`

**Request Body:**
```json
{
  "args": "{\"operation\": \"add\", \"a\": 5, \"b\": 3}"
}
```

**Response:**
```json
{
  "success": true,
  "result": "8",
  "execution_time_ms": 15
}
```

#### Get Plugin Logs

Retrieve recent logs for a plugin.

**Endpoint:** `GET /api/plugins/:name/logs`

**Response:**
```json
{
  "logs": [
    {
      "timestamp": "2025-12-03T10:30:00Z",
      "level": "info",
      "message": "Plugin executed successfully"
    },
    {
      "timestamp": "2025-12-03T10:29:00Z",
      "level": "error",
      "message": "Connection timeout"
    }
  ]
}
```

#### Delete Plugin

Remove a plugin from the system.

**Endpoint:** `DELETE /api/plugins/:name`

**Response:**
```json
{
  "success": true,
  "message": "Plugin math deleted"
}
```

#### Reload Plugin

Reload a plugin (restart the plugin process).

**Endpoint:** `POST /api/plugins/:name/reload`

**Response:**
```json
{
  "success": true,
  "message": "Plugin math reloaded"
}
```

#### Get Plugin Agents

Get list of agents using a plugin.

**Endpoint:** `GET /api/plugins/:name/agents`

**Response:**
```json
{
  "agents": ["default", "test-agent", "research-agent"]
}
```

#### Rollback Plugin Version

Rollback a plugin to a previous version.

**Endpoint:** `POST /api/plugins/:name/rollback`

**Request Body:**
```json
{
  "version": "0.9.0"
}
```

**Response:**
```json
{
  "success": true,
  "message": "Plugin math rolled back to version 0.9.0",
  "version": "0.9.0"
}
```

#### Get Plugin Permissions

Get permission information for a plugin.

**Endpoint:** `GET /api/plugins/:name/permissions`

**Response:**
```json
{
  "plugin_name": "math",
  "permissions": {
    "file_access": false,
    "network_access": true,
    "system_commands": false
  },
  "approved": false,
  "requested_at": "2025-12-03T09:00:00Z"
}
```

#### Approve Plugin Permissions

Approve requested permissions for a plugin.

**Endpoint:** `POST /api/plugins/:name/permissions/approve`

**Response:**
```json
{
  "success": true,
  "message": "Permissions approved for plugin math"
}
```

#### Export Plugin Configuration

Export configuration for one or all plugins.

**Endpoint:** `GET /api/plugins/export?plugin=<name>`

Query Parameters:
- `plugin` (optional): Plugin name to export. If omitted, exports all plugins.

**Response:** JSON file download

**Example:**
```bash
curl -O http://localhost:8765/api/plugins/export?plugin=math
# Downloads: math-config.json

curl -O http://localhost:8765/api/plugins/export
# Downloads: all-plugins-config.json
```

#### Import Plugin Configuration

Import plugin configuration from a JSON file.

**Endpoint:** `POST /api/plugins/import`

**Request:** `multipart/form-data`
- `config` (file): JSON configuration file

**Response:**
```json
{
  "success": true,
  "message": "Configuration imported successfully",
  "imported_plugins": ["math", "weather"]
}
```

#### Get Notifications

Get all plugin-related notifications.

**Endpoint:** `GET /api/plugins/notifications`

**Response:**
```json
{
  "notifications": [
    {
      "id": "notif-123",
      "type": "PluginError",
      "plugin_name": "weather",
      "message": "Failed to connect to weather API",
      "timestamp": "2025-12-03T10:00:00Z",
      "read": false,
      "dismissed": false
    },
    {
      "id": "notif-124",
      "type": "UpdateAvailable",
      "plugin_name": "math",
      "message": "Version 1.1.0 is available",
      "timestamp": "2025-12-03T09:30:00Z",
      "read": true,
      "dismissed": false
    }
  ],
  "unread_count": 1
}
```

**Notification Types:**
- `PluginError` - Plugin execution or health check failures
- `UpdateAvailable` - New version available in registry
- `HealthCheckFailed` - Plugin health check failed
- `PermissionRequired` - Plugin requires permission approval

#### Dismiss Notification

Mark a notification as dismissed.

**Endpoint:** `POST /api/plugins/notifications/:id/dismiss`

**Response:**
```json
{
  "success": true,
  "message": "Notification dismissed"
}
```

## Plugin Registry API

### List Available Plugins

Get all plugins available in the registry.

**Endpoint:** `GET /api/plugin-registry`

**Response:**
```json
{
  "plugins": {
    "weather": {
      "name": "weather",
      "version": "1.0.0",
      "description": "Weather information plugin",
      "url": "https://example.com/plugins/weather.so",
      "checksum": "sha256:abc123..."
    }
  }
}
```

### Load Plugin from Registry

Download and load a plugin from the registry.

**Endpoint:** `POST /api/plugin-registry`

**Request Body:**
```json
{
  "name": "weather"
}
```

**Response:**
```json
{
  "success": true,
  "message": "Plugin 'weather' loaded from registry"
}
```

### Delete Plugin from Local Registry

Remove a plugin from the local registry.

**Endpoint:** `DELETE /api/plugin-registry?name=<plugin_name>`

**Parameters:**
- `name` (query): Name of the plugin to delete

**Response:**
```json
{
  "success": true,
  "message": "Plugin 'weather' deleted from registry"
}
```

### Check for Plugin Updates

Check if updates are available for installed plugins.

**Endpoint:** `GET /api/plugin-updates`

**Response:**
```json
{
  "updates_available": {
    "math": {
      "current_version": "1.0.0",
      "latest_version": "1.1.0",
      "update_available": true
    }
  }
}
```

### Update Plugins

Update all plugins that have available updates.

**Endpoint:** `POST /api/plugin-updates`

**Response:**
```json
{
  "success": true,
  "updated_plugins": ["math", "weather"],
  "message": "2 plugins updated successfully"
}
```

### Download Plugin

Download a specific plugin from the registry.

**Endpoint:** `POST /api/plugins/download`

**Request Body:**
```json
{
  "name": "weather",
  "version": "1.0.0"
}
```

**Response:**
```json
{
  "success": true,
  "message": "Plugin 'weather' downloaded successfully"
}
```

## Settings API

### Get Agent Settings

Get the current agent's configuration settings.

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

Update the current agent's configuration.

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

## Chat API

### Send Message

Send a message to the current agent and get a response.

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

Here's a complete example of creating an agent, uploading a plugin, and using it:

```bash
# 1. Create a new agent
curl -X POST -H "Content-Type: application/json" \
  -d '{"name": "my-test-agent"}' \
  http://localhost:8765/api/agents

# 2. Switch to the new agent
curl -X PUT http://localhost:8765/api/agents?name=my-test-agent

# 3. Upload a plugin
curl -X POST -F "plugin=@calculator.so" \
  http://localhost:8765/api/plugins

# 4. Check if plugin needs initialization
curl http://localhost:8765/api/plugins/init-status

# 5. Initialize plugin if needed
curl -X POST -H "Content-Type: application/json" \
  -d '{"config": {"api_key": "your-key"}}' \
  http://localhost:8765/api/plugins/calculator/initialize

# 6. Test the plugin
curl -X POST -H "Content-Type: application/json" \
  -d '{"plugin_name": "calculator", "args": "{\"operation\": \"add\", \"a\": 5, \"b\": 3}"}' \
  http://localhost:8765/api/plugins/execute

# 7. Chat with the agent
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

  async uploadPlugin(file) {
    const formData = new FormData();
    formData.append('plugin', file);

    const response = await fetch(`${this.baseUrl}/plugins`, {
      method: 'POST',
      body: formData
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

    def upload_plugin(self, file_path):
        with open(file_path, 'rb') as f:
            files = {'plugin': f}
            response = requests.post(f'{self.base_url}/plugins', files=files)
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
