# Studios Refactoring - Quick Reference

## File Structure

```
ori-agent/internal/web/
├── static/
│   ├── css/
│   │   └── studio.css                      ✅ Workspace & canvas styles
│   └── js/modules/
│       ├── studios-workspace.js            ✅ Core workspace management
│       ├── studios-agent-modals.js         ✅ Agent creation/deletion
│       ├── studios-workspace-create.js     ✅ Workspace creation
│       └── studios-canvas-helpers.js       ✅ Canvas visualization
└── templates/pages/
    └── studios.tmpl                        ✅ Updated with external refs
```

## Module Responsibilities

### `studios-workspace.js`
**Purpose**: Core workspace CRUD & connection management
**Key Functions**:
- `loadWorkspaces()` - Fetch & render workspaces
- `deleteWorkspace(id)` - Delete a workspace
- `renderWorkspaces(data)` - Render workspace grid
- `handleConnectionFailure()` - Handle server offline
- `startWorkspacePolling()` - Auto-refresh every 10s
- `switchView(view)` - Toggle grid/canvas view

**Exports**: All functions above + server connection utilities

---

### `studios-agent-modals.js`
**Purpose**: Agent management modal functionality
**Key Functions**:
- `openManageAgentsModal()` - Open agent management UI
- `loadStudiosSystemAgents()` - Load agents list
- `renderStudiosAgentsListModal()` - Render agents in modal
- `deleteStudiosSystemAgent(name)` - Delete an agent
- `addAgentToSelectedWorkspace(name)` - Add agent to workspace
- `loadStudiosProviders()` - Load LLM providers
- `populateStudiosModelSelect(select, type)` - Populate model dropdown

**Event Listeners**:
- Agent type change → update model dropdown
- Temperature slider → update display
- Create agent form submission

**Exports**: All functions above + event initialization

---

### `studios-workspace-create.js`
**Purpose**: Workspace creation modal
**Key Functions**:
- `openCreateWorkspaceModal()` - Show creation modal
- `toggleAgent(name)` - Toggle agent selection
- `createWorkspace()` - Submit new workspace
- `setAvailableAgents(agents)` - Update agent list

**Event Listeners**:
- Create workspace button click

**Exports**: All functions above

---

### `studios-canvas-helpers.js`
**Purpose**: Canvas visualization & interaction
**Key Functions**:
- `loadCanvasStudio(id)` - Load workspace in canvas
- `executeMission()` - Execute mission on agents
- `addAgentToCanvas()` - Add agent to workspace
- `removeAgentFromCanvas(name)` - Remove agent
- `createTask()` - Create new task
- `showAgentDetails(agent)` - Display agent info panel
- `updateCurrentAgentsList()` - Refresh agents list
- `changeCanvasBackground(color)` - Update background

**Exports**: All functions above + view switching

---

## Global Variables

### Shared State
```javascript
window.availableAgents      // Array of all agents (studios-workspace.js)
window.selectedAgents       // Set of selected agents (studios-workspace-create.js)
window.agentCanvas          // Canvas instance (studios-canvas-helpers.js)
window.escapeHtml           // HTML escaping utility (studios-workspace.js)
```

### Module-Specific State
```javascript
// studios-workspace.js
workspaceRefreshInterval
isServerConnected
consecutiveFailures

// studios-agent-modals.js
studiosSystemAgents
studiosAvailableProviders

// studios-canvas-helpers.js
currentStudioId
currentWorkspaceDashboard
```

---

## API Endpoints Used

### Workspaces
- `GET /api/orchestration/workspace` - List workspaces
- `POST /api/orchestration/workspace` - Create workspace
- `DELETE /api/orchestration/workspace?id=<id>` - Delete workspace

### Agents
- `GET /api/agents` - List agents
- `POST /api/agents` - Create agent
- `DELETE /api/agents?name=<name>` - Delete agent
- `POST /api/studios/<id>/agents` - Add agent to workspace
- `DELETE /api/studios/<id>/agents/<name>` - Remove agent

### Canvas
- `POST /api/studios/<id>/mission` - Execute mission
- `POST /api/studios/<id>/tasks` - Create task
- `GET /agents/<name>/agent_settings.json` - Agent settings

### Providers
- `GET /api/providers` - List LLM providers & models

---

## Usage Examples

### Load Workspaces on Page Load
```javascript
// Automatically called by studios-workspace.js on DOMContentLoaded
// Manual call:
await loadWorkspaces();
```

### Create a New Workspace
```javascript
// Button onclick:
openCreateWorkspaceModal();

// Programmatically:
selectedAgents = new Set(['agent1', 'agent2']);
await createWorkspace();
```

### Open Agent Management
```javascript
// Button onclick:
openManageAgentsModal();
```

### Load Canvas View
```javascript
// Switch to canvas:
switchView('canvas');

// Load specific workspace:
loadCanvasStudio('workspace-123');
```

### Execute Mission
```javascript
// From UI (button calls executeMission())
// Or programmatically:
document.getElementById('mission-input').value = 'Analyze data';
await executeMission();
```

---

## Event Flow

### Page Load
```
1. DOMContentLoaded fires
2. studios-workspace.js initializes
   → loadWorkspaces()
   → loadWorkspaceAgents()
   → startWorkspacePolling()
3. studios-agent-modals.js sets up form listeners
4. studios-workspace-create.js sets up create button
5. URL params checked for canvas view
```

### Create Workspace
```
1. User clicks "Create Workspace"
2. openCreateWorkspaceModal() called
3. Agent checkboxes rendered
4. User selects agents & fills form
5. createWorkspace() called on submit
6. POST /api/orchestration/workspace
7. Modal closes, workspaces reload
```

### Canvas Interaction
```
1. User clicks "Canvas" on workspace card
2. openWorkspaceCanvas(id) called
3. switchView('canvas') toggles views
4. loadCanvasStudio(id) initializes canvas
5. AgentCanvas instance created
6. Event listeners attached (clicks, updates)
7. Canvas renders workspace visualization
```

---

## Testing Checklist

After deploying these modules, verify:

- [x] CSS extracted (styles load correctly)
- [x] Workspace grid renders
- [x] Create workspace modal opens
- [x] Agent selection works
- [x] Workspace creation succeeds
- [x] Manage agents modal opens
- [x] Agent creation form works
- [x] Canvas view loads
- [x] Mission execution works
- [x] Server offline detection works
- [ ] All onclick handlers work
- [ ] No JavaScript errors in console
- [ ] Dark mode compatible

---

## Troubleshooting

### Functions Not Found
**Symptom**: `ReferenceError: functionName is not defined`
**Solution**: Check that module is loaded in `studios.tmpl` and function is exported to `window`

### Duplicate Functions
**Symptom**: Function defined in multiple places
**Solution**: Remove from inline `<script>` block, keep in module

### State Not Shared
**Symptom**: Module can't access data from another module
**Solution**: Use `window.variableName` for shared state

### Styles Not Applied
**Symptom**: Workspace cards look wrong
**Solution**: Verify `<link rel="stylesheet" href="css/studio.css">` is present

---

## Migration Checklist for Developers

When working with studios.tmpl:

1. ✅ Prefer using exported modules over inline code
2. ✅ Add new functions to appropriate module files
3. ✅ Export new functions via `window.functionName`
4. ✅ Use shared state via `window` object
5. ✅ Test all functionality after changes
6. ⚠️ Don't add new inline functions (use modules)
7. ⚠️ Don't duplicate code between inline & modules

---

## Next Steps

1. **Remove Duplicate Inline Code** (~1,200 lines)
   - Test all modules work correctly
   - Remove duplicate functions from `<script>` block
   - Keep only unique initialization code

2. **Extract Modal Templates**
   - Create `components/studios-manage-agents-modal.tmpl`
   - Create `components/studios-create-workspace-modal.tmpl`
   - Create `components/studios-workspace-details-modal.tmpl`

3. **Add JSDoc Comments**
   - Document all exported functions
   - Add parameter/return types

4. **Write Unit Tests**
   - Test workspace CRUD operations
   - Test agent management
   - Test canvas interactions

---

**Last Updated**: 2024-11-26
**Version**: 2.0 (Phase 2 Complete)
**Maintainer**: Development Team
