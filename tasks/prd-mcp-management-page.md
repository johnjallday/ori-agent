# Product Requirements Document: MCP Management Page

## Introduction/Overview

This PRD describes a new dedicated MCP (Model Context Protocol) management page for the ori-agent frontend. The feature will provide users with a centralized interface to discover, add, configure, and manage MCP servers that extend agent capabilities. The page will support both global MCP configurations (shared across all agents) and per-agent MCP configurations, with a focus on simplifying MCP server setup for end users while maintaining flexibility for technical users.

**Problem Statement**: Currently, MCP server management in ori-agent requires manual configuration file editing or API calls. End users need a user-friendly interface to discover available MCP servers, add them to their agents, and monitor their status without touching configuration files.

**Goal**: Create an intuitive MCP management page that enables users to easily discover, install, configure, and monitor MCP servers through the web UI, supporting both global and per-agent scopes.

## Goals

1. **Simplify MCP Server Discovery**: Provide a marketplace/registry view where users can browse and discover available MCP servers with descriptions and capabilities
2. **Enable Multiple Configuration Methods**: Support manual configuration, file import (JSON/YAML), and marketplace-based installation
3. **Centralize MCP Operations**: Provide a single page for all MCP-related operations (add, remove, view status, test connection)
4. **Support Dual Scope**: Allow users to configure MCP servers at both global level (shared) and per-agent level
5. **Improve Reliability**: Implement status monitoring and retry mechanisms for failed MCP servers
6. **Maintain Simplicity**: Keep the initial version focused on core functionality (add/remove/status) to ship quickly

## User Stories

1. **As an end user**, I want to browse available MCP servers in a marketplace view so that I can discover tools that extend my agent's capabilities without searching external documentation.

2. **As an end user**, I want to add an MCP server by selecting it from a list and clicking "Install" so that I don't have to manually write configuration files.

3. **As a technical user**, I want to manually configure an MCP server by entering its command, arguments, and environment variables so that I can use custom or private MCP servers not in the marketplace.

4. **As an end user**, I want to import MCP server configurations from a JSON/YAML file so that I can quickly set up multiple servers from shared configuration templates.

5. **As an agent operator**, I want to see the status of all my MCP servers (running, stopped, error) at a glance so that I can quickly identify and fix issues.

6. **As an agent operator**, I want to remove MCP servers I no longer need so that I can keep my configuration clean and reduce resource usage.

7. **As an advanced user**, I want to configure some MCP servers globally (shared across all agents) and others per-agent so that I can optimize for both shared tools and agent-specific capabilities.

8. **As an end user**, I want to test the connection to an MCP server before fully enabling it so that I can verify it's configured correctly.

9. **As an end user**, I want the system to automatically retry connecting to a failed MCP server so that temporary network issues don't permanently break my setup.

10. **As an agent operator**, I want to switch between global and per-agent views so that I can manage MCP servers at the appropriate scope.

## Functional Requirements

### 1. MCP Management Page UI

**FR-1.1**: The system must provide a dedicated "/mcp" route in the frontend that displays the MCP management page.

**FR-1.2**: The page must have a toggle or tab interface to switch between "Global MCP Servers" and "Per-Agent MCP Servers" views.

**FR-1.3**: When in "Per-Agent" view, the system must provide a dropdown to select which agent's MCP configuration to manage.

**FR-1.4**: The page must display a list of currently configured MCP servers with the following information for each:
- Server name
- Status (running, stopped, error)
- Scope (global or agent-specific)
- Command/executable path
- Brief description (if available)

**FR-1.5**: Each MCP server entry must have action buttons: "Remove" and "Test Connection".

### 2. Add MCP Server - Marketplace Discovery

**FR-2.1**: The page must have an "Add MCP Server" button that opens a modal/dialog.

**FR-2.2**: The add dialog must have tabs for three input methods: "Marketplace", "Manual Configuration", "Import File".

**FR-2.3**: The "Marketplace" tab must display a searchable/filterable list of available MCP servers from a registry/marketplace.

**FR-2.4**: Each marketplace entry must show:
- Server name
- Description
- Maintainer/source
- Category/tags (optional)
- "Install" button

**FR-2.5**: Clicking "Install" on a marketplace entry must pre-fill the configuration form with the server's recommended settings.

### 3. Add MCP Server - Manual Configuration

**FR-3.1**: The "Manual Configuration" tab must provide a form with the following fields:
- Server Name (required, text input)
- Command/Executable Path (required, text input)
- Arguments (optional, text input or JSON array)
- Environment Variables (optional, key-value pairs)
- Scope selector (radio buttons: "Global" or "Per-Agent")
- Agent selector (dropdown, only visible if "Per-Agent" is selected)

**FR-3.2**: The form must validate that required fields are filled before allowing submission.

**FR-3.3**: The system must provide helpful placeholder text and tooltips explaining each field.

### 4. Add MCP Server - Import File

**FR-4.1**: The "Import File" tab must provide a file upload interface accepting `.json` and `.yaml`/`.yml` files.

**FR-4.2**: The system must parse the uploaded configuration file and validate its schema.

**FR-4.3**: Upon successful validation, the system must display a preview of the servers to be imported.

**FR-4.4**: The user must be able to select which servers to import (checkboxes) and choose the scope (global or per-agent) for each.

**FR-4.5**: If the file contains invalid configuration, the system must display clear error messages indicating what needs to be fixed.

### 5. MCP Server Status & Monitoring

**FR-5.1**: The system must periodically check the status of all configured MCP servers (polling interval: 10-30 seconds, configurable).

**FR-5.2**: The UI must display real-time status indicators using color coding:
- Green/Running: Server is connected and responding
- Yellow/Stopped: Server is configured but not running
- Red/Error: Server failed to start or lost connection

**FR-5.3**: Hovering over or clicking a status indicator must show additional details (e.g., error message, last check time).

### 6. Test Connection & Retry Mechanism

**FR-6.1**: Clicking "Test Connection" on an MCP server entry must trigger a connection test and display the result (success/failure) in a toast notification or inline message.

**FR-6.2**: When an MCP server status changes to "Error", the system must automatically attempt to reconnect using an exponential backoff strategy (e.g., retry after 5s, 10s, 30s, 60s).

**FR-6.3**: The system must display the number of retry attempts and next retry time in the status details.

**FR-6.4**: Users must be able to manually trigger a retry by clicking a "Retry Now" button on failed servers.

### 7. Remove MCP Server

**FR-7.1**: Clicking "Remove" on an MCP server entry must show a confirmation dialog.

**FR-7.2**: The confirmation dialog must clearly state whether the server is global or per-agent and which agent(s) will be affected.

**FR-7.3**: Upon confirmation, the system must remove the MCP server configuration and stop any running processes.

**FR-7.4**: The UI must update immediately to reflect the removal.

### 8. Backend API Integration

**FR-8.1**: The system must provide API endpoints to support all frontend operations:
- `GET /api/mcp/global` - List global MCP servers
- `GET /api/mcp/agent/:agentName` - List per-agent MCP servers
- `POST /api/mcp/global` - Add global MCP server
- `POST /api/mcp/agent/:agentName` - Add per-agent MCP server
- `DELETE /api/mcp/:id` - Remove MCP server
- `POST /api/mcp/:id/test` - Test MCP server connection
- `POST /api/mcp/:id/retry` - Manually retry failed server
- `GET /api/mcp/marketplace` - Fetch available MCP servers from registry

**FR-8.2**: The backend must persist MCP configurations to appropriate files:
- Global: `mcp_config.json` or `settings.json`
- Per-agent: `agents/<agent-name>/mcp_config.json`

## Non-Goals (Out of Scope)

The following features are explicitly **not** included in the initial version:

1. **Advanced Debugging**: Detailed logs, trace views, or performance metrics for MCP servers
2. **Tool Browsing**: Viewing available tools/capabilities exposed by each MCP server
3. **Health Monitoring Dashboard**: Detailed uptime statistics, error rate graphs, or alerting
4. **Version Management**: Upgrading/downgrading MCP server versions
5. **Auto-Updates**: Automatically updating MCP servers when new versions are available
6. **Configuration Parameters**: Advanced per-server configuration options beyond command/args/env
7. **Batch Operations**: Bulk enable/disable, bulk remove, or bulk configuration changes
8. **MCP Server Development Tools**: Creating or packaging custom MCP servers from the UI
9. **Permission Management**: Fine-grained access control for who can manage MCP servers
10. **Export Configuration**: Exporting current MCP configuration to shareable files (may be added later)

## Design Considerations

### UI/UX Requirements

1. **Layout**: Use a card-based or table-based layout for the MCP server list, consistent with the existing ori-agent design patterns (Bootstrap-based).

2. **Responsive Design**: Ensure the page works well on desktop browsers (primary target). Mobile responsiveness is nice-to-have but not critical for v1.

3. **Visual Hierarchy**:
   - Primary action: "Add MCP Server" button (prominent, top-right or top-left)
   - Secondary actions: Test Connection, Remove (per-entry)
   - Scope toggle/selector should be clearly visible but not overwhelming

4. **Color Coding**: Use existing ori-agent color scheme. Status indicators should use semantic colors (green=good, yellow=warning, red=error).

5. **Empty States**: When no MCP servers are configured, show a helpful empty state with a clear call-to-action to add the first server.

6. **Loading States**: Show loading spinners during async operations (testing connection, adding server, fetching marketplace).

7. **Error Handling**: Display user-friendly error messages for common failure scenarios (invalid config, server not found, connection timeout).

### Marketplace/Registry Integration

1. **Registry Source**: The marketplace should fetch from a configurable registry URL (similar to `plugin_registry_cache.json` pattern used for plugins).

2. **Caching**: Cache the marketplace data locally to reduce network requests. Refresh on page load or manual refresh button.

3. **Fallback**: If the marketplace is unavailable, users should still be able to manually configure servers.

4. **Curated List**: Initially, the marketplace can be a simple JSON file with well-known MCP servers (filesystem, github, brave-search, etc.).

## Technical Considerations

### Backend Implementation

1. **MCP Manager Module**: Create a new `internal/mcp/manager.go` module to handle MCP server lifecycle (start, stop, monitor, test).

2. **Configuration Storage**:
   - Global config: Store in `settings.json` under a new `mcp_servers` key, OR create dedicated `mcp_config.json`
   - Per-agent config: Store in `agents/<agent-name>/mcp_config.json`

3. **Process Management**: Use existing patterns from `internal/pluginloader/` for managing MCP server processes.

4. **Health Checks**: Implement periodic health checks using goroutines and context-based cancellation.

5. **Retry Logic**: Use exponential backoff with configurable max retries (e.g., 5 attempts).

6. **Handler Module**: Create `internal/mcphttp/handler.go` for all MCP-related HTTP endpoints.

### Frontend Implementation

1. **Route**: Add `/mcp` route to the frontend router (likely in `internal/web/templates/` or similar).

2. **JavaScript**: Create dedicated JS module for MCP page logic (e.g., `static/js/mcp.js`).

3. **API Client**: Reuse existing API client patterns from other pages (agents, plugins, chat).

4. **State Management**: Use local component state or simple global state for scope selection and current agent selection.

5. **Polling**: Implement client-side polling (every 15-30 seconds) to refresh MCP server statuses.

### Integration with Existing System

1. **Agent Context**: When an agent makes a chat request, the backend must load both global and per-agent MCP servers into the agent's context.

2. **Tool Registration**: MCP servers expose tools that should be available to the LLM provider (similar to plugins). Ensure the `internal/llm/` provider layer can access MCP tools.

3. **Existing MCP Code**: The project already has `internal/mcphttp/` - review and extend this code rather than duplicating functionality.

4. **Settings Page**: Optionally add a link from the existing Settings page to the new MCP management page.

## Success Metrics

1. **Adoption**: 70%+ of users with at least one MCP server configured within 30 days of feature launch
2. **Usability**: Users can successfully add an MCP server from the marketplace in < 2 minutes (measured via user testing)
3. **Reliability**: MCP server connection success rate > 95% after retry mechanism is implemented
4. **Discoverability**: 50%+ of new MCP server additions come from the marketplace (vs. manual configuration)
5. **Error Reduction**: < 5% of MCP server configurations result in persistent errors (measured by error status after retries)

## Open Questions

1. **Marketplace Hosting**: Where should the MCP server registry/marketplace be hosted? (GitHub repo, dedicated API, embedded in codebase?)

2. **Authentication/Security**: Should MCP servers that require authentication (API keys, tokens) have a secure input mechanism in the UI?

3. **Resource Limits**: Should there be a limit on the number of MCP servers per agent or globally?

4. **Tool Name Conflicts**: What happens if two MCP servers expose tools with the same name? How should the UI/backend handle this?

5. **Startup Behavior**: Should all configured MCP servers auto-start when ori-agent starts, or should users manually start/stop them?

6. **Agent Deletion**: When an agent is deleted, what should happen to its per-agent MCP servers? (Auto-remove? Warn user?)

7. **Backwards Compatibility**: If users have existing MCP configurations (e.g., in `settings.json`), how should they be migrated to the new system?

8. **Marketplace Updates**: How often should the marketplace cache be refreshed? Should there be a manual "Refresh Marketplace" button?

9. **Testing Requirements**: Should there be integration tests for the MCP management page, or are unit tests sufficient for v1?

10. **Documentation**: Should there be in-app help/tooltips explaining what MCP is for users unfamiliar with the concept?
