# 🌐 API Reference

This document provides comprehensive API documentation for Ori Agent, including all endpoints, request/response formats, and examples.

## Base URL

All API endpoints are relative to the base URL:
```
http://localhost:8080/api
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
curl -X POST -F "plugin=@my_plugin.so" http://localhost:8080/api/plugins
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
  http://localhost:8080/api/agents

# 2. Switch to the new agent
curl -X PUT http://localhost:8080/api/agents?name=my-test-agent

# 3. Upload a plugin
curl -X POST -F "plugin=@calculator.so" \
  http://localhost:8080/api/plugins

# 4. Check if plugin needs initialization
curl http://localhost:8080/api/plugins/init-status

# 5. Initialize plugin if needed
curl -X POST -H "Content-Type: application/json" \
  -d '{"config": {"api_key": "your-key"}}' \
  http://localhost:8080/api/plugins/calculator/initialize

# 6. Test the plugin
curl -X POST -H "Content-Type: application/json" \
  -d '{"plugin_name": "calculator", "args": "{\"operation\": \"add\", \"a\": 5, \"b\": 3}"}' \
  http://localhost:8080/api/plugins/execute

# 7. Chat with the agent
curl -X POST -H "Content-Type: application/json" \
  -d '{"question": "What is 10 + 15?"}' \
  http://localhost:8080/api/chat
```

### JavaScript Client Example

```javascript
class OriAgentClient {
  constructor(baseUrl = 'http://localhost:8080/api') {
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
    def __init__(self, base_url='http://localhost:8080/api'):
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

This API reference provides complete documentation for integrating with Ori Agent programmatically. For additional help or examples, refer to the main [README](README.md) or check the web interface implementation in the `internal/web/` directory.