# Agent Orchestration Plan: Multi-Agent Research & Analysis

## Overview

This document outlines the architecture and implementation plan for multi-agent orchestration in the Ori agent system. The goal is to enable agents to collaborate on complex research and analysis tasks through coordinated workflows.

## 1. Architecture Overview

### Coordination Pattern: Coordinator-Worker Hybrid

- One agent acts as the **orchestrator** (coordinates tasks)
- Multiple **specialist agents** handle specific aspects (research, analysis, synthesis, validation)
- Agents can delegate subtasks to each other peer-to-peer

### Communication Flow

```
User Request → Orchestrator Agent
    ↓
Orchestrator delegates to specialists:
    → Research Agent (gathers information)
    → Analysis Agent (processes data)
    → Synthesis Agent (combines findings)
    → Validator Agent (checks quality)
    ↓
Results aggregated → User
```

## 2. Core Components to Build

### A. Shared Workspace System (`internal/workspace/`)

**Purpose**: Agents need a shared space to exchange information, pass subtask results, and maintain collective context.

```go
// Workspace stores shared context between agents
type Workspace struct {
    ID          string
    ParentAgent string
    Agents      []string              // participating agents
    SharedData  map[string]interface{} // key-value shared state
    Messages    []AgentMessage         // inter-agent messages
    Status      WorkspaceStatus
    CreatedAt   time.Time
    UpdatedAt   time.Time
}

type AgentMessage struct {
    From      string
    To        string    // empty = broadcast
    Type      MessageType // task_request, result, question, status
    Content   string
    Metadata  map[string]interface{}
    Timestamp time.Time
}

type WorkspaceStatus string
const (
    StatusActive    WorkspaceStatus = "active"
    StatusCompleted WorkspaceStatus = "completed"
    StatusFailed    WorkspaceStatus = "failed"
    StatusCancelled WorkspaceStatus = "cancelled"
)

type MessageType string
const (
    MessageTaskRequest MessageType = "task_request"
    MessageResult      MessageType = "result"
    MessageQuestion    MessageType = "question"
    MessageStatus      MessageType = "status"
)
```

### B. Agent Communication Protocol (`internal/agentcomm/`)

**Purpose**: Standardized communication ensures agents can reliably delegate tasks and share results.

```go
// Protocol for inter-agent communication
type AgentCommunicator interface {
    SendMessage(workspace string, msg AgentMessage) error
    GetMessages(workspace string, agentName string) ([]AgentMessage, error)
    BroadcastToWorkspace(workspace string, msg AgentMessage) error
    DelegateTask(workspace string, to string, task Task) (string, error)
}

type Task struct {
    ID          string
    Description string
    RequiredBy  string
    Priority    int
    Context     map[string]interface{}
    Timeout     time.Duration
}
```

### C. Orchestration Engine (`internal/orchestration/`)

**Purpose**: Central orchestration logic for managing complex multi-agent workflows.

```go
// Orchestrator manages multi-agent workflows
type Orchestrator struct {
    workspaceStore WorkspaceStore
    agentStore     store.Store
    communicator   AgentCommunicator
}

// ExecuteCollaborativeTask coordinates multiple agents
func (o *Orchestrator) ExecuteCollaborativeTask(
    ctx context.Context,
    mainAgent string,
    task CollaborativeTask,
) (*CollaborativeResult, error) {
    // 1. Create shared workspace
    // 2. Identify required agent types
    // 3. Delegate subtasks
    // 4. Monitor progress
    // 5. Aggregate results
    // 6. Return synthesized response
}

type CollaborativeTask struct {
    Goal        string
    RequiredRoles []AgentRole // research, analysis, synthesis, validation
    Context     map[string]interface{}
    MaxDuration time.Duration
}

type CollaborativeResult struct {
    WorkspaceID string
    FinalOutput string
    SubResults  map[string]interface{} // agent -> result
    Duration    time.Duration
    Status      string
}
```

### D. Agent Role System (extend `internal/types/types.go`)

**Purpose**: Agents need defined roles to know their responsibilities in collaborative workflows.

```go
// Extend existing Agent struct
type Agent struct {
    Name         string
    Type         string // existing: tool-calling, general, research
    Role         AgentRole // NEW: orchestrator, researcher, analyzer, synthesizer, validator
    Capabilities []string  // NEW: web_search, code_analysis, data_processing, etc.
    Settings     Settings
    CreatedAt    time.Time
}

type AgentRole string
const (
    RoleOrchestrator AgentRole = "orchestrator"
    RoleResearcher   AgentRole = "researcher"
    RoleAnalyzer     AgentRole = "analyzer"
    RoleSynthesizer  AgentRole = "synthesizer"
    RoleValidator    AgentRole = "validator"
    RoleSpecialist   AgentRole = "specialist"
)

// Capability constants for agent capabilities
const (
    CapabilityWebSearch      = "web_search"
    CapabilityCodeAnalysis   = "code_analysis"
    CapabilityDataProcessing = "data_processing"
    CapabilityFileOperations = "file_operations"
    CapabilityAPIIntegration = "api_integration"
)
```

## 3. API Endpoints to Add

### POST `/api/orchestration/workspace`

Create a new collaborative workspace

**Request**:
```json
{
  "name": "research-task-001",
  "parent_agent": "main-agent",
  "participating_agents": ["researcher-1", "analyzer-1", "synthesizer-1"],
  "initial_context": {
    "topic": "...",
    "constraints": "..."
  }
}
```

**Response**:
```json
{
  "workspace_id": "ws-abc123",
  "status": "active",
  "created_at": "2025-10-29T10:00:00Z"
}
```

### GET `/api/orchestration/workspace/:id`

Get workspace state and messages

**Response**:
```json
{
  "id": "ws-abc123",
  "parent_agent": "main-agent",
  "agents": ["researcher-1", "analyzer-1"],
  "status": "active",
  "messages": [
    {
      "from": "orchestrator",
      "to": "researcher-1",
      "type": "task_request",
      "content": "Find papers on AI impact",
      "timestamp": "2025-10-29T10:01:00Z"
    }
  ],
  "shared_data": {...}
}
```

### POST `/api/orchestration/workspace/:id/messages`

Send message to workspace

**Request**:
```json
{
  "from": "researcher-1",
  "to": "analyzer-1",
  "type": "result",
  "content": "Found 15 relevant papers...",
  "metadata": {
    "sources": [...]
  }
}
```

### POST `/api/orchestration/delegate`

Delegate task to another agent

**Request**:
```json
{
  "workspace_id": "workspace-123",
  "from_agent": "orchestrator",
  "to_agent": "researcher-1",
  "task": {
    "description": "Find academic papers on topic X",
    "priority": 1,
    "context": {...}
  }
}
```

**Response**:
```json
{
  "task_id": "task-xyz789",
  "status": "delegated",
  "assigned_to": "researcher-1"
}
```

### GET `/api/agents/:name/capabilities`

Get agent capabilities

**Response**:
```json
{
  "agent": "researcher-1",
  "capabilities": ["web_search", "data_processing"],
  "role": "researcher"
}
```

### PUT `/api/agents/:name/capabilities`

Update agent capabilities

**Request**:
```json
{
  "capabilities": ["web_search", "code_analysis"],
  "role": "analyzer"
}
```

## 4. Research & Analysis Workflow Implementation

### Workflow Pattern: Research Pipeline

```
1. Orchestrator receives research request
2. Creates workspace with participating agents
3. Delegates tasks:
   - Research Agent: "Gather information on topic X"
   - Research Agent: "Find supporting data"
4. Research agents post findings to workspace
5. Orchestrator delegates to Analysis Agent:
   - "Analyze findings from research phase"
6. Analysis Agent posts insights
7. Orchestrator delegates to Synthesis Agent:
   - "Create comprehensive report from research + analysis"
8. Synthesis Agent posts draft
9. Orchestrator delegates to Validator Agent:
   - "Verify claims and check for inconsistencies"
10. Validator provides feedback
11. If issues found, loop back to relevant step
12. Final result returned to user
```

### Implementation in Chat Handler

Location: `internal/chathttp/handler.go`

```go
// Extend existing chat handler to detect orchestration requests
func (h *Handler) handleOrchestrationRequest(
    agent *types.Agent,
    message string,
) (string, error) {
    // Detect if request requires multiple agents
    requiresOrchestration := h.detectOrchestrationNeed(message)

    if requiresOrchestration {
        // Create collaborative task
        task := orchestration.CollaborativeTask{
            Goal: message,
            RequiredRoles: h.identifyRequiredRoles(message),
            Context: make(map[string]interface{}),
        }

        // Execute orchestrated workflow
        result, err := h.orchestrator.ExecuteCollaborativeTask(
            context.Background(),
            agent.Name,
            task,
        )

        // Return synthesized result
        return result.FinalOutput, err
    }

    // Fall back to normal single-agent handling
    return h.handleNormalChat(agent, message)
}

// detectOrchestrationNeed uses heuristics or LLM to determine if
// a request requires multiple agents
func (h *Handler) detectOrchestrationNeed(message string) bool {
    // Keywords suggesting complexity: "research and analyze", "comprehensive",
    // "investigate", "compare multiple", etc.
    keywords := []string{
        "research and",
        "comprehensive analysis",
        "investigate and",
        "compare multiple",
        "analyze data from",
    }

    messageLower := strings.ToLower(message)
    for _, keyword := range keywords {
        if strings.Contains(messageLower, keyword) {
            return true
        }
    }

    return false
}

// identifyRequiredRoles determines which agent roles are needed
func (h *Handler) identifyRequiredRoles(message string) []types.AgentRole {
    roles := []types.AgentRole{}

    messageLower := strings.ToLower(message)

    if strings.Contains(messageLower, "research") ||
       strings.Contains(messageLower, "find information") {
        roles = append(roles, types.RoleResearcher)
    }

    if strings.Contains(messageLower, "analyze") ||
       strings.Contains(messageLower, "process") {
        roles = append(roles, types.RoleAnalyzer)
    }

    if strings.Contains(messageLower, "comprehensive") ||
       strings.Contains(messageLower, "report") ||
       strings.Contains(messageLower, "synthesize") {
        roles = append(roles, types.RoleSynthesizer)
    }

    // Always include validator for quality assurance
    roles = append(roles, types.RoleValidator)

    return roles
}
```

## 5. File Structure Changes

```
ori-agent/
├── internal/
│   ├── workspace/           # NEW
│   │   ├── workspace.go     # Workspace management
│   │   ├── store.go         # Persistence
│   │   └── workspace_test.go
│   ├── agentcomm/           # NEW
│   │   ├── communicator.go  # Inter-agent messaging
│   │   ├── protocol.go      # Message protocol
│   │   └── agentcomm_test.go
│   ├── orchestration/       # NEW
│   │   ├── orchestrator.go  # Main orchestration logic
│   │   ├── workflow.go      # Workflow patterns
│   │   ├── delegation.go    # Task delegation
│   │   └── orchestration_test.go
│   ├── orchestrationhttp/   # NEW
│   │   └── handlers.go      # HTTP handlers for orchestration API
│   ├── types/
│   │   └── types.go         # ADD: AgentRole, Capabilities
│   └── chathttp/
│       └── handler.go       # MODIFY: Add orchestration detection
└── workspaces/              # NEW (data directory)
    └── workspace-{id}.json
```

## 6. Frontend Updates

### Workspace Visualization

New module: `internal/web/static/js/modules/workspace.js`

**Features**:
- Show active workspaces in sidebar
- Display participating agents
- Real-time message stream between agents
- Task delegation tree view
- Progress tracking for collaborative tasks

**UI Components**:
```javascript
// Workspace panel in sidebar
<div class="workspace-panel">
  <h5>Active Workspaces</h5>
  <div class="workspace-list">
    <!-- workspace items -->
  </div>
</div>

// Workspace detail view
<div class="workspace-detail">
  <h4>Workspace: research-task-001</h4>
  <div class="participating-agents">
    <span class="badge">researcher-1</span>
    <span class="badge">analyzer-1</span>
  </div>
  <div class="message-stream">
    <!-- inter-agent messages -->
  </div>
  <div class="task-tree">
    <!-- delegation hierarchy -->
  </div>
</div>
```

### Agent Configuration UI

Extend: `internal/web/static/js/modules/agents.js`

**New Fields**:
- "Role" dropdown (orchestrator, researcher, analyzer, synthesizer, validator, specialist)
- "Capabilities" multi-select (web_search, code_analysis, data_processing, etc.)
- "Compatible Agents" display showing which agents can collaborate based on capabilities

**Form Addition**:
```html
<div class="form-group">
  <label>Agent Role</label>
  <select id="agentRole" class="form-control">
    <option value="orchestrator">Orchestrator</option>
    <option value="researcher">Researcher</option>
    <option value="analyzer">Analyzer</option>
    <option value="synthesizer">Synthesizer</option>
    <option value="validator">Validator</option>
    <option value="specialist">Specialist</option>
  </select>
</div>

<div class="form-group">
  <label>Capabilities</label>
  <select id="agentCapabilities" class="form-control" multiple>
    <option value="web_search">Web Search</option>
    <option value="code_analysis">Code Analysis</option>
    <option value="data_processing">Data Processing</option>
    <option value="file_operations">File Operations</option>
    <option value="api_integration">API Integration</option>
  </select>
</div>
```

## 7. Implementation Phases

### Phase 1: Foundation (1-2 weeks)

- [ ] Implement Workspace system (`internal/workspace/`)
  - [ ] `workspace.go` - Core workspace struct and methods
  - [ ] `store.go` - File-based persistence
  - [ ] `workspace_test.go` - Unit tests
- [ ] Add AgentRole and Capabilities to Agent struct (`internal/types/types.go`)
- [ ] Create workspace API endpoints (`internal/orchestrationhttp/handlers.go`)
- [ ] Basic frontend workspace viewer (`internal/web/static/js/modules/workspace.js`)

**Files to Create**:
- `internal/workspace/workspace.go`
- `internal/workspace/store.go`
- `internal/workspace/workspace_test.go`
- `internal/orchestrationhttp/handlers.go`
- `internal/web/static/js/modules/workspace.js`

**Files to Modify**:
- `internal/types/types.go` (add Role and Capabilities)
- `internal/server/server.go` (register workspace endpoints)

### Phase 2: Communication (1 week)

- [ ] Implement AgentCommunicator (`internal/agentcomm/`)
  - [ ] `communicator.go` - Message passing implementation
  - [ ] `protocol.go` - Message type definitions
  - [ ] `agentcomm_test.go` - Unit tests
- [ ] Message passing between agents
- [ ] Task delegation API
- [ ] Message persistence

**Files to Create**:
- `internal/agentcomm/communicator.go`
- `internal/agentcomm/protocol.go`
- `internal/agentcomm/agentcomm_test.go`

**Files to Modify**:
- `internal/orchestrationhttp/handlers.go` (add message endpoints)

### Phase 3: Orchestration (2 weeks)

- [ ] Build Orchestrator engine (`internal/orchestration/`)
  - [ ] `orchestrator.go` - Main orchestration logic
  - [ ] `workflow.go` - Workflow patterns
  - [ ] `delegation.go` - Task delegation logic
  - [ ] `orchestration_test.go` - Integration tests
- [ ] Implement workflow patterns
- [ ] Integrate with existing chat handler
- [ ] Result aggregation and synthesis logic

**Files to Create**:
- `internal/orchestration/orchestrator.go`
- `internal/orchestration/workflow.go`
- `internal/orchestration/delegation.go`
- `internal/orchestration/orchestration_test.go`

**Files to Modify**:
- `internal/chathttp/handler.go` (add orchestration detection and handling)
- `internal/server/server.go` (initialize orchestrator)

### Phase 4: UI & Monitoring (1 week)

- [ ] Frontend workspace visualization
- [ ] Real-time message updates (WebSocket/SSE)
- [ ] Task progress tracking
- [ ] Agent capability configuration UI

**Files to Create/Modify**:
- `internal/web/static/js/modules/workspace.js` (enhance)
- `internal/web/static/js/modules/agents.js` (add role/capabilities)
- `internal/web/static/css/workspace.css` (styling)
- `internal/web/static/index.html` (add workspace panel)

### Phase 5: Research Workflows (1 week)

- [ ] Pre-built research pipeline
- [ ] Analysis workflow template
- [ ] Quality validation workflow
- [ ] Example configurations
- [ ] Documentation and examples

**Files to Create**:
- `internal/orchestration/workflows/research.go`
- `internal/orchestration/workflows/analysis.go`
- `internal/orchestration/workflows/validation.go`
- `examples/orchestration/research_workflow.json`
- `docs/ORCHESTRATION_GUIDE.md`

## 8. Example Usage Scenario

### User Request

"Research the impact of AI on software development and create a comprehensive analysis"

### Behind the Scenes Workflow

1. **Main agent** detects this requires orchestration
2. **Creates workspace** with 4 specialist agents
3. **Researcher Agent** (research-type):
   - Uses web search tools to find articles, papers, statistics
   - Posts findings to shared workspace
   - Message: "Found 15 relevant papers on AI impact in software development"
4. **Code Analyzer Agent** (general-type):
   - Examines code examples and tools mentioned
   - Posts technical analysis
   - Message: "Analyzed 8 AI-powered dev tools, key findings: ..."
5. **Data Analyzer Agent** (general-type):
   - Processes statistics and trends
   - Creates data summaries
   - Message: "Processed 50 data points, trends show 40% productivity increase"
6. **Synthesizer Agent** (research-type):
   - Combines all findings into coherent report
   - Structures information logically
   - Message: "Created comprehensive 5-section report combining research, code analysis, and data"
7. **Validator Agent** (tool-calling-type):
   - Fact-checks claims
   - Verifies sources
   - Flags inconsistencies
   - Message: "Verified all claims, 2 sources need clarification"
8. **Orchestrator** aggregates final validated report
9. **User receives** comprehensive, multi-perspective analysis

### Message Timeline

```
10:00:00 [Orchestrator → Researcher] "Find papers and articles on AI impact in software development"
10:00:00 [Orchestrator → Code Analyzer] "Analyze AI development tools and their capabilities"
10:00:00 [Orchestrator → Data Analyzer] "Gather and process statistics on AI adoption"

10:02:30 [Researcher → Workspace] "Completed: Found 15 papers, 20 articles. Summary: ..."
10:03:15 [Code Analyzer → Workspace] "Completed: Analyzed 8 tools. Key findings: ..."
10:03:45 [Data Analyzer → Workspace] "Completed: Processed 50 data points. Trends: ..."

10:04:00 [Orchestrator → Synthesizer] "Create comprehensive report from all findings"

10:06:00 [Synthesizer → Workspace] "Completed: Generated 5-section report"

10:06:15 [Orchestrator → Validator] "Verify all claims and check sources"

10:08:00 [Validator → Workspace] "Completed: All verified, 2 minor clarifications needed"

10:08:30 [Orchestrator → Synthesizer] "Incorporate validator feedback"

10:09:00 [Synthesizer → Workspace] "Final report ready"

10:09:05 [Orchestrator → User] "Analysis complete. Here's your comprehensive report: ..."
```

## 9. Technical Considerations

### Concurrency
- Use goroutines with context for parallel agent execution
- Example: Launch researcher and code analyzer simultaneously
- Implement worker pools to limit concurrent operations

### Timeouts
- Each subtask has configurable timeout
- Default: 5 minutes per subtask
- Workspace-level timeout for entire workflow
- Graceful degradation if subtask times out

### Error Handling
- Failed subtasks don't crash entire workflow
- Retry logic for transient failures
- Fallback strategies when agent unavailable
- Detailed error reporting in workspace messages

### State Recovery
- Workspace state persisted to disk after each message
- Location: `workspaces/workspace-{id}.json`
- Enable crash recovery and resume
- Periodic state snapshots

### Circular Dependencies
- Detect and prevent infinite delegation loops
- Track delegation chain depth
- Maximum depth limit (e.g., 5 levels)
- Alert orchestrator if circular dependency detected

### Resource Limits
- Cap number of concurrent agents per workspace (e.g., 10)
- Maximum workspace lifetime (e.g., 1 hour)
- Message queue size limits
- Memory usage monitoring

### Security
- Agent isolation - prevent unauthorized cross-workspace access
- Validate all inter-agent messages
- Audit trail of all delegations and communications
- Rate limiting on workspace creation

## 10. Testing Strategy

### Unit Tests
- Workspace creation and management
- Message passing and routing
- Task delegation logic
- Role and capability validation

### Integration Tests
- Multi-agent collaboration flows
- Research pipeline end-to-end
- Error handling and recovery
- Timeout and cancellation scenarios

### Performance Tests
- Concurrent workspace handling
- Message throughput
- Large-scale agent coordination (10+ agents)
- Memory usage under load

### Example Test Cases

```go
func TestWorkspaceCreation(t *testing.T) {
    // Test workspace creation with multiple agents
}

func TestMessageRouting(t *testing.T) {
    // Test message delivery to correct agents
}

func TestOrchestrationWorkflow(t *testing.T) {
    // Test complete research workflow
}

func TestCircularDependencyDetection(t *testing.T) {
    // Test prevention of infinite loops
}

func TestWorkspaceRecovery(t *testing.T) {
    // Test state recovery after crash
}
```

## 11. Future Enhancements

### Advanced Features (Post-MVP)
- **Dynamic agent creation**: Orchestrator spawns specialized agents on-demand
- **Learning from workflows**: Track successful patterns and optimize
- **Agent reputation system**: Prefer high-performing agents for critical tasks
- **Cost optimization**: Route tasks to cheapest capable agent
- **Parallel workflow branches**: Execute independent subtasks simultaneously
- **Human-in-the-loop**: Request user input when agents disagree
- **Workflow templates**: Pre-built patterns for common tasks
- **Agent marketplace**: Discover and install specialized agents

### Monitoring and Analytics
- Workspace performance metrics
- Agent utilization statistics
- Cost tracking per workflow
- Success rate by workflow type
- Bottleneck identification

### UI Enhancements
- Visual workflow designer (drag-and-drop)
- Real-time agent activity graph
- Workspace templates library
- Collaboration history viewer
- Cost estimator before execution

## 12. Getting Started (After Implementation)

### Creating Your First Multi-Agent Workflow

1. **Configure Agents with Roles**:
```bash
# Create specialized agents
curl -X POST http://localhost:8080/api/agents \
  -d '{"name": "researcher-1"}'

curl -X PUT http://localhost:8080/api/agents/researcher-1/capabilities \
  -d '{"role": "researcher", "capabilities": ["web_search"]}'
```

2. **Create Workspace**:
```bash
curl -X POST http://localhost:8080/api/orchestration/workspace \
  -d '{
    "name": "my-research-task",
    "parent_agent": "main-agent",
    "participating_agents": ["researcher-1", "analyzer-1"]
  }'
```

3. **Send Research Request**:
```bash
# The orchestrator will automatically delegate to specialist agents
curl -X POST http://localhost:8080/api/chat \
  -d '{
    "agent": "main-agent",
    "message": "Research the impact of AI on software development"
  }'
```

4. **Monitor Progress**:
```bash
# View workspace activity
curl http://localhost:8080/api/orchestration/workspace/{workspace-id}
```

## 13. Success Metrics

### Key Performance Indicators

- **Workflow Completion Rate**: % of orchestrated tasks that complete successfully
- **Time Savings**: Comparison of multi-agent vs single-agent task duration
- **Result Quality**: User satisfaction scores for orchestrated outputs
- **Agent Utilization**: % of agents actively participating in workflows
- **Cost Efficiency**: Cost per completed workflow
- **Error Recovery Rate**: % of failed subtasks successfully recovered

### Target Goals (3 months post-launch)

- 90% workflow completion rate
- 40% time reduction for research tasks
- 85% user satisfaction
- 75% agent utilization during peak hours
- 95% error recovery rate

## 14. Documentation Requirements

### User-Facing Documentation
- [ ] Orchestration overview guide
- [ ] Agent role selection guide
- [ ] Capability configuration tutorial
- [ ] Workflow best practices
- [ ] Troubleshooting common issues

### Developer Documentation
- [ ] Architecture deep-dive
- [ ] API reference for orchestration endpoints
- [ ] Creating custom workflow patterns
- [ ] Extending agent capabilities
- [ ] Testing orchestration features

## 15. Next Steps

To begin implementation:

1. **Start with Phase 1**: Build workspace foundation
2. **Create prototype**: Simple 2-agent collaboration example
3. **Iterate**: Gather feedback and refine
4. **Scale**: Add more sophisticated workflows

**Recommendation**: Begin with a minimal viable implementation:
- Basic workspace system
- Simple message passing
- One pre-built workflow (research pipeline)
- Basic UI visualization

This allows early validation of the architecture before investing in full implementation.

---

**Document Version**: 1.0
**Created**: 2025-10-29
**Status**: Planning Phase
**Owner**: Ori Development Team
