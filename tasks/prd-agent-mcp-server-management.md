# Product Requirements Document: Agent MCP Server Management

**Version:** 1.0
**Date:** 2025-11-14
**Status:** Draft
**Author:** Product Management

---

## 1. Introduction

### 1.1 Feature Overview

This feature adds the ability to manage Model Context Protocol (MCP) servers for individual agents directly from the Agents modal in the ori-agent web interface. Users will be able to view available MCP servers and toggle them on/off for specific agents without needing to manually edit JSON configuration files.

### 1.2 Problem Statement

Currently, users must manually edit JSON files (`agents/{name}/mcp_servers.json`) to enable or disable MCP servers for individual agents. This process is:
- Error-prone (risk of JSON syntax errors)
- Inefficient (requires leaving the UI to edit files)
- Inconsistent with the existing plugin management UX pattern
- Not accessible to non-technical users

### 1.3 Business Goals

- **Improve User Experience**: Provide a GUI for MCP server management that matches the existing plugin management pattern
- **Reduce Support Burden**: Eliminate user errors from manual JSON editing
- **Increase Feature Adoption**: Make MCP servers more discoverable and easier to use
- **Maintain Consistency**: Follow established UI patterns from plugin management

---

## 2. User Stories

### User Story 1: View Available MCP Servers for an Agent

**As a** user configuring an agent
**I want to** see a list of available MCP servers with their current status and capabilities
**So that** I can understand which external tools I can enable for this agent

**Acceptance Criteria:**
- When viewing an agent's configuration in the Agents modal, I see an "MCP Servers" section
- The section displays all globally enabled MCP servers
- Each server shows: name, description, current status (running/stopped/error), and tool count
- Servers already enabled for this agent are visually indicated (toggle in "on" state)
- The section appears below the plugins section in the agent configuration view

### User Story 2: Enable an MCP Server for an Agent

**As a** user configuring an agent
**I want to** enable an MCP server by clicking a toggle switch
**So that** the agent can use tools provided by that server

**Acceptance Criteria:**
- I can click a toggle switch next to any MCP server to enable it
- The toggle provides immediate visual feedback (switches to "on" state)
- The system saves the configuration to `agents/{name}/mcp_servers.json`
- The system attempts to start the server if it's not already running
- If the server is not running or in an error state, I see a warning message but can still save
- The enabled server's tools become available in chat for this agent

### User Story 3: Disable an MCP Server for an Agent

**As a** user configuring an agent
**I want to** disable an MCP server by clicking the toggle switch
**So that** the agent no longer has access to that server's tools

**Acceptance Criteria:**
- I can click the toggle switch to disable an enabled MCP server
- The toggle provides immediate visual feedback (switches to "off" state)
- The system saves the configuration to `agents/{name}/mcp_servers.json`
- The server is removed from the agent's enabled servers list
- The server's tools are no longer available to this agent in chat
- The server continues running for other agents that have it enabled

### User Story 4: View MCP Server Details

**As a** user exploring MCP servers
**I want to** see detailed information about a server
**So that** I can understand what tools it provides before enabling it

**Acceptance Criteria:**
- I can click on a server name or info icon to view details
- A detail view shows: full description, list of available tools, and server configuration
- Each tool shows its name and description
- I can close the detail view and return to the agent configuration

### User Story 5: Handle Servers Not Running

**As a** user enabling a server that's not running
**I want to** be warned about the server's status
**So that** I understand the server may need attention before it works

**Acceptance Criteria:**
- When I enable a server that is stopped or in error state, I see a warning message
- The warning explains the server will need to be started from Settings
- I can still save the agent configuration with the server enabled
- The agent configuration saves successfully even if the server is not running

---

## 3. Goals

### Primary Goals
1. **Enable GUI-based MCP server management** for individual agents
2. **Match existing plugin management UX patterns** for consistency
3. **Provide clear server status information** to help users troubleshoot
4. **Maintain data integrity** by properly saving to agent configuration files

### Secondary Goals
1. Surface server capabilities (tool count) to aid decision-making
2. Provide helpful warnings for non-running servers
3. Make MCP servers more discoverable to increase adoption

---

## 4. Non-Goals (Out of Scope)

The following items are explicitly **not included** in this release:

1. **Per-Tool Filtering**: Enabling/disabling individual tools within a server (Phase 2 feature)
2. **Global Server Management**: Adding/removing MCP servers from the global registry (remains in Settings page)
3. **Server Configuration**: Editing server command, args, or environment variables (remains in Settings page)
4. **Server Process Management**: Starting/stopping server processes (remains in Settings page)
5. **Server Installation**: Installing new MCP servers from a marketplace or registry
6. **Per-Agent Server Configuration**: Different configuration (command, args) per agent - all agents share global server config

---

## 5. Functional Requirements

### 5.1 UI Components

#### 5.1.1 MCP Servers Section in Agent Configuration

**Location:** Agents modal, within agent configuration view, below the Plugins section

**Visual Layout:**
```
┌─────────────────────────────────────────────────────┐
│ MCP Servers (Model Context Protocol)               │
├─────────────────────────────────────────────────────┤
│ Enable external tools and data sources via MCP.    │
│                                                     │
│ ┌─────────────────────────────────────────────┐   │
│ │ filesystem                          [Toggle]│   │
│ │ File operations and directory access        │   │
│ │ Status: Running • 12 tools                  │   │
│ │ [ℹ️ View Details]                           │   │
│ └─────────────────────────────────────────────┘   │
│                                                     │
│ ┌─────────────────────────────────────────────┐   │
│ │ brave-search                        [Toggle]│   │
│ │ Web search capabilities via Brave           │   │
│ │ Status: Stopped • 3 tools                   │   │
│ │ [ℹ️ View Details]                           │   │
│ └─────────────────────────────────────────────┘   │
│                                                     │
│ Need more servers? Configure in Settings > MCP.   │
└─────────────────────────────────────────────────────┘
```

**Requirements:**
- FR-1: Section must have clear heading "MCP Servers" with subtitle "(Model Context Protocol)"
- FR-2: Section must display a brief description explaining MCP servers
- FR-3: Section must show all globally enabled MCP servers
- FR-4: Each server must display: name, description, status, tool count, toggle switch, and info button
- FR-5: Section must include a footer message directing users to Settings for global server management
- FR-6: If no MCP servers are globally configured, display: "No MCP servers configured. Add servers in Settings > MCP."

#### 5.1.2 Server Card Design

Each MCP server must be displayed as a card with the following elements:

**Requirements:**
- FR-7: Server name displayed prominently (16px, semi-bold font)
- FR-8: Server description displayed below name (14px, muted color)
- FR-9: Status indicator with color coding:
  - Running: Green dot + "Running" text
  - Stopped: Gray dot + "Stopped" text
  - Error: Red dot + "Error" text
  - Starting: Yellow dot + "Starting" text
- FR-10: Tool count displayed after status (e.g., "12 tools")
- FR-11: Toggle switch aligned to the right side of the card
- FR-12: Info button (ℹ️ icon) to view server details
- FR-13: Cards must have hover state with subtle shadow/border change

#### 5.1.3 Toggle Switch Behavior

**Requirements:**
- FR-14: Toggle switch must show current enabled/disabled state for this agent
- FR-15: Toggle must respond to clicks with immediate visual feedback
- FR-16: While saving, toggle must show a loading spinner
- FR-17: On success, toggle must remain in new state
- FR-18: On error, toggle must revert to previous state and show error message
- FR-19: Toggle must be disabled (grayed out) while save operation is in progress

#### 5.1.4 Server Details Modal

**Requirements:**
- FR-20: Modal must display server name as title
- FR-21: Modal must show full server description
- FR-22: Modal must list all available tools with names and descriptions
- FR-23: Modal must show server configuration (command and arguments) in read-only format
- FR-24: Modal must have a close button

### 5.2 Data Flow

#### 5.2.1 Loading MCP Servers

**Requirements:**
- FR-25: On agent configuration view load, system must fetch all MCP servers via `GET /api/mcp/servers`
- FR-26: System must fetch agent's enabled servers from agent configuration
- FR-27: System must cross-reference to determine which servers are enabled for this agent
- FR-28: While loading, display loading spinner with text "Loading MCP servers..."
- FR-29: On load error, display error message with retry button

#### 5.2.2 Enabling a Server

**Workflow:**
1. User clicks toggle switch to enable server
2. Toggle switches to "on" state with loading spinner
3. System sends `POST /api/agents/{name}/mcp-servers/{serverName}/enable`
4. Backend validates agent exists
5. Backend validates server exists in global registry
6. Backend adds server to agent's `mcp_servers.json` file
7. Backend checks server status:
   - If stopped/error: attempts to start server
   - If running: no action needed
8. Backend returns success or error response
9. On success: toggle remains enabled
10. On error: toggle reverts, error message displayed

**Requirements:**
- FR-30: System must validate agent name exists before enabling server
- FR-31: System must validate server exists in global registry
- FR-32: System must save to `agents/{agentName}/mcp_servers.json` file
- FR-33: System must handle file creation if `mcp_servers.json` doesn't exist
- FR-34: System must preserve existing enabled servers when adding new one
- FR-35: System must not add duplicate entries if server already enabled
- FR-36: If server is not running, system must attempt to start it
- FR-37: System must return success even if server fails to start (with warning)

#### 5.2.3 Disabling a Server

**Workflow:**
1. User clicks toggle switch to disable server
2. Toggle switches to "off" state with loading spinner
3. System sends `POST /api/agents/{name}/mcp-servers/{serverName}/disable`
4. Backend validates agent exists
5. Backend removes server from agent's `mcp_servers.json` file
6. Backend returns success or error response
7. On success: toggle remains disabled
8. On error: toggle reverts, error message displayed

**Requirements:**
- FR-38: System must validate agent name exists before disabling server
- FR-39: System must remove server from `mcp_servers.json` file
- FR-40: System must not modify other agents' configurations
- FR-41: System must not stop the server process (other agents may be using it)
- FR-42: If server is not in enabled list, operation must succeed silently (idempotent)

### 5.3 API Requirements

#### 5.3.1 New API Endpoints

The following endpoints must be created:

**Endpoint 1: Enable MCP Server for Agent**
```
POST /api/agents/{agentName}/mcp-servers/{serverName}/enable

Request Body: None

Success Response (200):
{
  "status": "success",
  "message": "Server enabled for agent",
  "server_status": "running" | "stopped" | "error" | "starting"
}

Error Responses:
400 - Agent not found
400 - Server not found in global registry
500 - Failed to save configuration
```

**Endpoint 2: Disable MCP Server for Agent**
```
POST /api/agents/{agentName}/mcp-servers/{serverName}/disable

Request Body: None

Success Response (200):
{
  "status": "success",
  "message": "Server disabled for agent"
}

Error Responses:
400 - Agent not found
500 - Failed to save configuration
```

**Endpoint 3: Get Agent's Enabled MCP Servers**
```
GET /api/agents/{agentName}/mcp-servers

Success Response (200):
{
  "enabled_servers": ["filesystem", "brave-search"]
}

Error Responses:
400 - Agent not found
500 - Failed to read configuration
```

**Requirements:**
- FR-43: All endpoints must validate agent name from URL path
- FR-44: All endpoints must validate server name from URL path
- FR-45: All endpoints must return JSON responses
- FR-46: All endpoints must use appropriate HTTP status codes
- FR-47: Error responses must include descriptive error messages
- FR-48: Enable endpoint must be idempotent (enabling twice has same effect)
- FR-49: Disable endpoint must be idempotent (disabling twice has same effect)

#### 5.3.2 Existing API Usage

The feature will use these existing endpoints:
- `GET /api/mcp/servers` - List all globally configured MCP servers
- `GET /api/agents/{name}` - Get agent configuration (includes enabled servers)

**Requirements:**
- FR-50: System must handle servers being removed from global config while still in agent config
- FR-51: System must not display globally disabled servers in agent configuration

### 5.4 File System Requirements

**Requirements:**
- FR-52: System must read/write `agents/{agentName}/mcp_servers.json` file
- FR-53: File format must be: `{"enabled_servers": ["server1", "server2"]}`
- FR-54: System must create agent directory if it doesn't exist
- FR-55: System must preserve file permissions (0644 for files, 0755 for directories)
- FR-56: System must handle concurrent writes to the same agent's config file
- FR-57: System must validate JSON syntax before writing
- FR-58: System must create backup of existing file before writing (error recovery)

---

## 6. UI/UX Requirements

### 6.1 Visual Design

**Requirements:**
- UX-1: MCP Servers section must use same card styling as Plugins section
- UX-2: Section must be visually separated from Plugins with margin/padding
- UX-3: Server cards must have 8px border radius matching existing design
- UX-4: Status indicators must use consistent color scheme:
  - Running: `var(--success-color)` or #28a745
  - Stopped: `var(--secondary-color)` or #6c757d
  - Error: `var(--danger-color)` or #dc3545
  - Starting: `var(--warning-color)` or #ffc107
- UX-5: Toggle switches must use Bootstrap toggle component or custom styled checkboxes
- UX-6: Loading states must use existing spinner components
- UX-7: All text must use existing typography scale from design system

### 6.2 Responsive Design

**Requirements:**
- UX-8: Section must work on viewport widths from 320px (mobile) to 1920px (desktop)
- UX-9: Server cards must stack vertically on mobile (<768px)
- UX-10: Server cards may display in grid layout on desktop (≥768px) with 2 columns max
- UX-11: Toggle switches must remain accessible and tappable on mobile (min 44px touch target)
- UX-12: Modal must be responsive and scrollable on mobile devices

### 6.3 Accessibility

**Requirements:**
- UX-13: All interactive elements must be keyboard accessible
- UX-14: Toggle switches must have ARIA labels: "Enable {serverName} for {agentName}"
- UX-15: Server status must have ARIA live region for screen reader announcements
- UX-16: All icons must have descriptive alt text or ARIA labels
- UX-17: Focus indicators must be visible on all interactive elements
- UX-18: Modal must trap focus and return focus on close
- UX-19: Color must not be sole indicator of status (use icons + text)

### 6.4 Interaction Design

**Requirements:**
- UX-20: Toggle switch changes must feel instantaneous (optimistic UI update)
- UX-21: Success feedback must be subtle (no disruptive alerts for successful toggles)
- UX-22: Error feedback must be prominent and actionable
- UX-23: Loading states must appear after 200ms delay to avoid flashing for fast operations
- UX-24: Hover states must provide visual feedback on all clickable elements
- UX-25: Server details modal must open with smooth fade animation
- UX-26: Server details modal must close when clicking backdrop or close button

### 6.5 Empty States

**Requirements:**
- UX-27: If no MCP servers configured globally, display:
  ```
  No MCP servers configured yet.
  Add MCP servers in Settings > MCP to enable external tools.
  [Go to Settings] button
  ```
- UX-28: Empty state must include an icon (connection/server icon)
- UX-29: Empty state must use muted text color
- UX-30: "Go to Settings" button must navigate to Settings page MCP section

---

## 7. Technical Considerations

### 7.1 Backend Implementation

**Technology Stack:**
- Language: Go
- Existing Components: `internal/mcp/config.go` (ConfigManager)
- New Components: Agent-specific MCP handlers in `internal/agenthttp/`

**Implementation Notes:**
- TECH-1: Leverage existing `ConfigManager.EnableServerForAgent()` and `DisableServerForAgent()` methods
- TECH-2: Add new handler methods in `internal/agenthttp/handler.go`
- TECH-3: Register new routes in `internal/server/server.go` route setup
- TECH-4: Use existing file locking mechanisms if available to prevent race conditions
- TECH-5: Add logging for all enable/disable operations for debugging

**Code References:**
- `internal/mcp/config.go` lines 192-226: EnableServerForAgent, DisableServerForAgent methods
- `internal/mcphttp/handlers.go` lines 116-211: Existing enable/disable handlers for current agent
- `internal/agent/agent.go` line 71: Agent struct with MCPServers field

### 7.2 Frontend Implementation

**Technology Stack:**
- HTML/CSS: Bootstrap 5
- JavaScript: Vanilla JS (no frameworks)
- Template Engine: Go templates

**Implementation Notes:**
- TECH-6: Add new section to agent configuration template in `internal/web/templates/components/modals.tmpl`
- TECH-7: Add JavaScript functions to `internal/web/static/js/app.js`:
  - `loadAgentMCPServers(agentName)`
  - `toggleMCPServerForAgent(agentName, serverName, enabled)`
  - `showMCPServerDetails(serverName)`
- TECH-8: Reuse existing modal patterns from plugin management
- TECH-9: Use fetch API for all HTTP requests
- TECH-10: Implement optimistic UI updates with rollback on error

**Code References:**
- `internal/web/templates/pages/settings.tmpl` lines 113-146: Existing MCP servers section for reference
- `internal/web/templates/components/modals.tmpl`: Agent modal template

### 7.3 Error Handling

**Requirements:**
- TECH-11: Handle agent not found: Display "Agent does not exist" error
- TECH-12: Handle server not found: Display "Server not found in registry" error
- TECH-13: Handle file write errors: Display "Failed to save configuration. Please try again."
- TECH-14: Handle network errors: Display "Network error. Please check your connection."
- TECH-15: Handle server start failures: Display warning "Server enabled but not running. Start it in Settings."
- TECH-16: Log all errors to console for debugging
- TECH-17: Provide retry mechanism for transient errors

### 7.4 Edge Cases

**Requirements:**
- TECH-18: **Server deleted from global config**: If agent has server enabled but server no longer exists in global config:
  - Display server name with "(Unavailable)" label
  - Show warning icon
  - Allow user to disable (remove from agent config)
  - Show tooltip: "This server has been removed from global configuration"

- TECH-19: **Concurrent modifications**: If two users modify same agent's MCP servers simultaneously:
  - Last write wins (accept data loss - low probability scenario)
  - Log warning on server
  - Future: Consider optimistic locking with version numbers

- TECH-20: **File system errors**: If `agents/{name}/` directory is deleted while agent exists:
  - Recreate directory on next enable operation
  - Log warning
  - Continue operation successfully

- TECH-21: **Malformed JSON**: If `mcp_servers.json` contains invalid JSON:
  - Log error with file contents
  - Attempt to backup corrupted file
  - Overwrite with new valid configuration
  - Show warning to user: "Configuration file was corrupted and has been reset"

- TECH-22: **Server process crashes during enable**: If server process crashes while enabling:
  - Save configuration successfully (server enabled)
  - Return success with warning message
  - Server will be restarted on next agent chat that needs it

### 7.5 Performance Considerations

**Requirements:**
- TECH-23: MCP server list loading must complete in <500ms on average
- TECH-24: Enable/disable operations must complete in <1 second
- TECH-25: UI must remain responsive during all operations (use async/await)
- TECH-26: Limit server details modal tool list to first 50 tools (paginate if needed)
- TECH-27: Cache server list for 5 seconds to avoid redundant API calls

### 7.6 Security Considerations

**Requirements:**
- TECH-28: Validate all agent names to prevent path traversal attacks
- TECH-29: Validate all server names match regex: `^[a-zA-Z0-9_-]+$`
- TECH-30: Restrict file operations to `agents/` directory only
- TECH-31: Do not expose server environment variables or credentials in API responses
- TECH-32: Rate limit enable/disable operations (max 10 requests per minute per IP)
- TECH-33: Sanitize all user-facing error messages to avoid information disclosure

---

## 8. Success Metrics

### 8.1 Usage Metrics

The following metrics will be tracked to measure feature success:

1. **Adoption Rate**
   - Metric: Percentage of agents with at least one MCP server enabled
   - Target: 30% of agents within 30 days of release
   - Measurement: Count agents with non-empty `mcp_servers.json`

2. **Feature Usage**
   - Metric: Number of enable/disable operations per week
   - Target: 50+ operations per week
   - Measurement: Log and count API calls to enable/disable endpoints

3. **User Satisfaction**
   - Metric: Reduction in support tickets related to MCP configuration
   - Target: 50% reduction within 60 days
   - Measurement: Compare support ticket volume before/after release

### 8.2 Technical Metrics

1. **Performance**
   - Metric: Average API response time for enable/disable operations
   - Target: <500ms at p95
   - Measurement: Application performance monitoring

2. **Reliability**
   - Metric: Error rate for enable/disable operations
   - Target: <1% error rate
   - Measurement: Error logging and monitoring

3. **UI Responsiveness**
   - Metric: Time to render MCP servers section
   - Target: <300ms at p95
   - Measurement: Browser performance profiling

---

## 9. Testing Requirements

### 9.1 Unit Tests

The following components must have unit tests with ≥80% code coverage:

1. **Backend Tests** (`internal/agenthttp/mcp_handlers_test.go`)
   - Test enable server for agent (success case)
   - Test enable server for non-existent agent (error case)
   - Test enable server not in global registry (error case)
   - Test disable server for agent (success case)
   - Test disable server for non-existent agent (error case)
   - Test get enabled servers for agent
   - Test concurrent enable/disable operations
   - Test file system errors during save

2. **Frontend Tests** (manual testing checklist)
   - Test toggle switch visual states
   - Test loading states
   - Test error message display
   - Test server details modal open/close
   - Test responsive layout on different screen sizes

### 9.2 Integration Tests

1. **End-to-End Workflow Tests**
   - Enable server → Verify file written → Verify server started → Verify tools available in chat
   - Disable server → Verify file updated → Verify tools removed from chat
   - Enable server when not running → Verify warning displayed → Verify configuration saved

2. **API Integration Tests**
   - Test all API endpoints with valid inputs
   - Test all API endpoints with invalid inputs
   - Test API response times under load
   - Test concurrent API requests

### 9.3 User Acceptance Testing

**Test Scenarios:**

1. **Scenario 1: Enable first MCP server for an agent**
   - Given: Agent has no MCP servers enabled
   - When: User opens agent configuration and enables filesystem server
   - Then: Toggle switches to enabled, no errors, file is created, server starts

2. **Scenario 2: Disable an enabled MCP server**
   - Given: Agent has filesystem server enabled
   - When: User disables filesystem server
   - Then: Toggle switches to disabled, file is updated, no errors

3. **Scenario 3: View server details**
   - Given: Agent configuration is open with MCP servers listed
   - When: User clicks info button on a server
   - Then: Modal opens showing server details and tools list

4. **Scenario 4: Enable server that is stopped**
   - Given: MCP server "brave-search" is configured but stopped
   - When: User enables brave-search for an agent
   - Then: Warning message displayed, configuration saved, toggle remains enabled

5. **Scenario 5: No MCP servers configured**
   - Given: No MCP servers in global registry
   - When: User opens agent configuration
   - Then: Empty state with "No MCP servers configured" message and link to Settings

---

## 10. Open Questions

The following questions require decisions before implementation:

1. **Q1: Should we show a confirmation dialog when disabling a server that's actively being used?**
   - Context: If an agent is in the middle of a chat that uses MCP tools, disabling could be disruptive
   - Options:
     - A) Show confirmation: "This agent is currently using tools from this server. Disable anyway?"
     - B) No confirmation, allow instant disable (tools fail on next call)
     - C) Prevent disable if agent has active chat session
   - **Recommendation**: Option B (no confirmation) - simpler UX, existing behavior for plugins

2. **Q2: Should we display tool names from each server in the agent configuration view?**
   - Context: Users might want to see available tools without clicking into details
   - Options:
     - A) Show first 3 tool names in collapsed view (e.g., "read_file, write_file, list_directory...")
     - B) Only show tool count, require clicking for details
     - C) Show all tools in expandable accordion
   - **Recommendation**: Option B (tool count only) - cleaner UI, details in modal

3. **Q3: How should we handle MCP servers that fail to start?**
   - Context: Server might fail to start due to missing dependencies
   - Options:
     - A) Block enabling until server successfully starts
     - B) Allow enabling, show persistent warning about server not running
     - C) Allow enabling, attempt auto-restart on next agent chat
   - **Recommendation**: Option B (allow enabling with warning) - specified in requirements

4. **Q4: Should we add bulk enable/disable for MCP servers?**
   - Context: Users might want to enable/disable multiple servers at once
   - Options:
     - A) Add "Enable All" / "Disable All" buttons
     - B) Add checkboxes for multi-select with bulk action buttons
     - C) Keep individual toggles only
   - **Recommendation**: Option C (individual only) - simpler for v1, add later if requested

5. **Q5: Should we show which other agents have a server enabled?**
   - Context: Useful to see "filesystem is enabled for 3 other agents"
   - Options:
     - A) Show count of agents using each server
     - B) Show names of agents using each server
     - C) Don't show cross-agent information
   - **Recommendation**: Option C (don't show) - privacy concern, not essential for v1

---

## 11. Future Enhancements (Phase 2)

The following features are deferred to future releases:

### 11.1 Per-Tool Filtering

**Description:** Allow users to enable/disable individual tools from an MCP server for an agent, rather than all-or-nothing.

**User Story:** As a user, I want to enable only specific tools from an MCP server (e.g., only read_file but not write_file) so that I can control exactly what capabilities my agent has.

**Technical Requirements:**
- Modify `agents/{name}/mcp_servers.json` schema to include tool filters
- Update agent tool loading logic to filter tools based on configuration
- Add UI for tool selection in server details modal or expanded card view

### 11.2 Server Usage Analytics

**Description:** Show which MCP servers are most frequently used and which tools are called most often.

**User Story:** As a user, I want to see which MCP servers my agents use most frequently so that I can optimize my configuration.

**Technical Requirements:**
- Add instrumentation to track tool calls
- Create analytics dashboard or section in agent configuration
- Store usage metrics in database or log aggregation system

### 11.3 Server Recommendations

**Description:** Suggest MCP servers to enable based on agent type or conversation context.

**User Story:** As a user, I want intelligent suggestions for which MCP servers to enable based on my agent's purpose so that I can quickly configure optimal capabilities.

**Technical Requirements:**
- Define mapping between agent types and recommended servers
- Add ML model to analyze conversation patterns (long-term)
- Display recommendations in MCP servers section with "Enable Recommended" button

### 11.4 Server Presets

**Description:** Create and save presets of enabled MCP servers that can be applied to new agents.

**User Story:** As a user, I want to save my MCP server configuration as a preset (e.g., "Developer Agent") so that I can quickly apply the same configuration to new agents.

**Technical Requirements:**
- Add preset management UI in Settings
- Store presets in `mcp_presets.json`
- Add "Apply Preset" dropdown in agent configuration

### 11.5 Real-Time Server Status Updates

**Description:** Update server status indicators in real-time without requiring page refresh.

**User Story:** As a user, I want to see when an MCP server starts or stops in real-time so that I know immediately when servers become available.

**Technical Requirements:**
- Implement WebSocket connection for server status events
- Add event emitter in backend MCP registry
- Update UI components to subscribe to status changes

---

## 12. Design Mockups

### 12.1 Agent Configuration View with MCP Servers Section

```
┌───────────────────────────────────────────────────────────────┐
│ Edit Agent: Research Assistant                    [Save] [×] │
├───────────────────────────────────────────────────────────────┤
│                                                               │
│ Basic Settings                                                │
│ ┌─────────────────────────────────────────────────────────┐ │
│ │ Agent Name: Research Assistant                          │ │
│ │ Model: gpt-4o                                            │ │
│ │ Temperature: [====•---------] 0.7                       │ │
│ └─────────────────────────────────────────────────────────┘ │
│                                                               │
│ Plugins                                                       │
│ ┌─────────────────────────────────────────────────────────┐ │
│ │ [✓] Web Search                                          │ │
│ │ [✓] File Operations                                     │ │
│ │ [ ] Email Tools                                         │ │
│ └─────────────────────────────────────────────────────────┘ │
│                                                               │
│ MCP Servers (Model Context Protocol)                         │
│ ┌─────────────────────────────────────────────────────────┐ │
│ │ Enable external tools and data sources via MCP.         │ │
│ │                                                          │ │
│ │ ┌────────────────────────────────────────────────────┐ │ │
│ │ │ filesystem                          [●─○] ON      │ │ │
│ │ │ File operations and directory access              │ │ │
│ │ │ ● Running • 12 tools • [ℹ️ Details]             │ │ │
│ │ └────────────────────────────────────────────────────┘ │ │
│ │                                                          │ │
│ │ ┌────────────────────────────────────────────────────┐ │ │
│ │ │ brave-search                        [○─○] OFF     │ │ │
│ │ │ Web search capabilities via Brave API             │ │ │
│ │ │ ○ Stopped • 3 tools • [ℹ️ Details]              │ │ │
│ │ └────────────────────────────────────────────────────┘ │ │
│ │                                                          │ │
│ │ ┌────────────────────────────────────────────────────┐ │ │
│ │ │ github                              [●─○] ON      │ │ │
│ │ │ GitHub repository operations and queries          │ │ │
│ │ │ ● Running • 18 tools • [ℹ️ Details]             │ │ │
│ │ └────────────────────────────────────────────────────┘ │ │
│ │                                                          │ │
│ │ Need more servers? Configure in Settings > MCP.        │ │
│ └─────────────────────────────────────────────────────────┘ │
│                                                               │
│                                           [Cancel] [Save]    │
└───────────────────────────────────────────────────────────────┘
```

### 12.2 Server Details Modal

```
┌───────────────────────────────────────────────────────────────┐
│ MCP Server: filesystem                                    [×] │
├───────────────────────────────────────────────────────────────┤
│                                                               │
│ Description                                                   │
│ ┌─────────────────────────────────────────────────────────┐ │
│ │ Provides secure file system operations including       │ │
│ │ reading, writing, and listing files and directories.   │ │
│ │ All operations are sandboxed to configured paths.      │ │
│ └─────────────────────────────────────────────────────────┘ │
│                                                               │
│ Configuration                                                 │
│ ┌─────────────────────────────────────────────────────────┐ │
│ │ Command: npx                                            │ │
│ │ Arguments: -y, @modelcontextprotocol/server-filesystem │ │
│ └─────────────────────────────────────────────────────────┘ │
│                                                               │
│ Available Tools (12)                                          │
│ ┌─────────────────────────────────────────────────────────┐ │
│ │ • read_file - Read contents of a file                  │ │
│ │ • write_file - Write contents to a file                │ │
│ │ • list_directory - List contents of a directory        │ │
│ │ • create_directory - Create a new directory            │ │
│ │ • delete_file - Delete a file                          │ │
│ │ • move_file - Move or rename a file                    │ │
│ │ • copy_file - Copy a file                              │ │
│ │ • file_info - Get file metadata                        │ │
│ │ • search_files - Search for files by pattern           │ │
│ │ • read_multiple_files - Read multiple files at once    │ │
│ │ ... (2 more)                                            │ │
│ └─────────────────────────────────────────────────────────┘ │
│                                                               │
│                                                      [Close]  │
└───────────────────────────────────────────────────────────────┘
```

### 12.3 Empty State (No MCP Servers)

```
┌───────────────────────────────────────────────────────────────┐
│ MCP Servers (Model Context Protocol)                         │
├───────────────────────────────────────────────────────────────┤
│                                                               │
│                    ╔═══════════════════╗                     │
│                    ║                   ║                     │
│                    ║     🔌  ⚡        ║                     │
│                    ║                   ║                     │
│                    ╚═══════════════════╝                     │
│                                                               │
│              No MCP servers configured yet.                   │
│                                                               │
│     Add MCP servers in Settings to enable external tools     │
│              and data sources for your agents.                │
│                                                               │
│                    [Go to Settings]                           │
│                                                               │
└───────────────────────────────────────────────────────────────┘
```

### 12.4 Warning State (Server Not Running)

```
┌────────────────────────────────────────────────────┐
│ brave-search                        [●─○] ON      │
│ Web search capabilities via Brave API             │
│ ⚠️ Stopped • 3 tools • [ℹ️ Details]             │
│                                                    │
│ ┌─────────────────────────────────────────────┐  │
│ │ ⚠️ Warning: This server is not running.     │  │
│ │ Start it in Settings > MCP to use tools.    │  │
│ └─────────────────────────────────────────────┘  │
└────────────────────────────────────────────────────┘
```

---

## 13. Dependencies

### 13.1 Internal Dependencies

- **MCP Registry**: Must be initialized and running
- **Agent Store**: Must support reading/writing agent configurations
- **File System Access**: Must have write permissions to `agents/` directory
- **Existing API Endpoints**: `GET /api/mcp/servers` must be functional

### 13.2 External Dependencies

None. This feature uses existing infrastructure.

---

## 14. Risks and Mitigation

| Risk | Impact | Probability | Mitigation |
|------|--------|-------------|------------|
| **File system permissions issues** | High - Feature won't work | Low | Add permission checks on startup, provide clear error messages |
| **Concurrent writes to config files** | Medium - Data loss | Low | Document limitation, consider file locking in future |
| **Server won't start when enabled** | Medium - Poor UX | Medium | Allow enabling anyway, show helpful error, provide troubleshooting link |
| **Breaking changes to MCP protocol** | High - Feature breaks | Low | Follow MCP spec closely, add version checking |
| **Performance issues with many servers** | Medium - Slow UI | Low | Implement pagination, lazy loading of server details |
| **Confusion with global vs per-agent** | Medium - User errors | Medium | Clear labeling, prominent link to Settings, documentation |

---

## 15. Documentation Requirements

The following documentation must be created or updated:

1. **User Guide**
   - Section: "Managing MCP Servers for Agents"
   - Content: Step-by-step guide with screenshots
   - Location: `docs/user-guide/agent-mcp-servers.md`

2. **API Documentation**
   - Document new API endpoints with request/response examples
   - Location: `docs/api/API_REFERENCE.md`

3. **Release Notes**
   - Feature announcement with key benefits
   - Location: `CHANGELOG.md`

4. **Inline Help**
   - Tooltip text for MCP servers section
   - Help icon with link to documentation

---

## 16. Release Plan

### 16.1 Rollout Strategy

**Phase 1: Internal Testing (Week 1)**
- Deploy to development environment
- Internal QA testing
- Fix critical bugs

**Phase 2: Beta Release (Week 2)**
- Deploy to staging environment
- Invite 5-10 power users for beta testing
- Collect feedback and iterate

**Phase 3: General Availability (Week 3)**
- Deploy to production
- Announce feature in release notes
- Monitor error rates and usage metrics

### 16.2 Rollback Plan

If critical issues are discovered:
1. Disable new API endpoints via feature flag
2. Revert UI changes (hide MCP servers section)
3. Existing manual configuration method remains functional
4. No data loss - configuration files remain intact

---

## 17. Approval

This PRD requires approval from:

- [ ] Product Manager
- [ ] Engineering Lead
- [ ] UX Designer
- [ ] QA Lead

**Approval Date:** _________________

**Approved By:** _________________

---

## Appendix A: Related Documents

- **Technical Spec**: [Link to detailed technical implementation document]
- **MCP Protocol Spec**: https://modelcontextprotocol.io/docs
- **Existing Plugin Management Code**: `internal/web/templates/components/modals.tmpl`
- **MCP Configuration Manager**: `internal/mcp/config.go`

---

## Appendix B: Glossary

- **MCP (Model Context Protocol)**: A protocol for connecting AI assistants to external tools and data sources
- **MCP Server**: An external process that provides tools via the MCP protocol
- **Global MCP Registry**: System-wide configuration of available MCP servers (`mcp_registry.json`)
- **Per-Agent MCP Configuration**: Agent-specific list of enabled servers (`agents/{name}/mcp_servers.json`)
- **Tool**: A function provided by an MCP server that an agent can call
- **Agent**: A configured AI assistant with specific settings, plugins, and MCP servers

---

**Document End**
