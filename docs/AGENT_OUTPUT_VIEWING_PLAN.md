# Agent Output Viewing & Task Chaining - Implementation Plan

## Overview
This document outlines the implementation plan for:
1. Viewing agent execution outputs
2. Creating visual connections between tasks and agents
3. Chaining agent outputs to create workflows

---

## Part 1: Agent Output Viewing

### Key UX Question
**Where should users view agent execution outputs?**

### Option 1: Agent Node Display
Show output directly on/near the agent in the canvas.

**Pros:**
- ✅ Immediate visibility - no clicks needed
- ✅ Contextual to the workflow
- ✅ Good for quick monitoring

**Cons:**
- ❌ Canvas clutter with multiple agents
- ❌ Limited space for long outputs
- ❌ Which output if agent ran 10 tasks?
- ❌ Hard to read structured data

**Visual concept:**
```
┌─────────────────────┐
│   Agent: GPT-4      │
│   ●●● (3 tasks)     │
│                     │
│ Latest output:      │
│ "Analysis comple... │  <- Truncated preview
└─────────────────────┘
```

---

### Option 2: Task Card Output
Show output on the task card itself.

**Pros:**
- ✅ Output naturally belongs to task execution
- ✅ Clear 1:1 relationship (task → result)
- ✅ Can show status with output
- ✅ Works well with task-to-task chaining

**Cons:**
- ❌ Tasks might be small/cluttered
- ❌ Hard to compare outputs across tasks
- ❌ Need to find the task to see output

**Visual concept:**
```
┌──────────────────────┐
│ Task: Analyze data   │
│ user → gpt-4         │
│ [COMPLETED] ✓        │
│                      │
│ Output: "Found 5..." │ <- Preview
│ [View Full Output]   │ <- Click to expand
└──────────────────────┘
```

---

### Option 3: Agent Detail Panel
Show in expanded panel when clicking agent.

**Pros:**
- ✅ Lots of space for detailed output
- ✅ Can show execution history
- ✅ Can show all tasks this agent ran
- ✅ Clean canvas (nothing visible until expanded)

**Cons:**
- ❌ Requires click to view
- ❌ Less immediate feedback
- ❌ Can't compare multiple agents easily

**Visual concept:**
```
Canvas:
┌─────────────┐
│  Agent GPT  │ <-- Click
└─────────────┘

Expands to:
┌────────────────────────────────┐
│ Agent: GPT-4                   │
│ ─────────────────────────────  │
│ Execution History (3 tasks):   │
│                                │
│ ▼ Task 1: "Analyze data"       │
│   Status: Completed ✓          │
│   Output: "Found 5 patterns    │
│   in the dataset..."           │
│   [Copy] [View Full]           │
│                                │
│ ▼ Task 2: "Summarize report"  │
│   Status: Completed ✓          │
│   Output: "Executive summ...   │
│   [Copy] [View Full]           │
└────────────────────────────────┘
```

---

### Option 4: Task Detail Panel ⭐ **RECOMMENDED**
Show in expanded panel when clicking task.

**Pros:**
- ✅ Most logical place (task = execution unit)
- ✅ Can show input AND output
- ✅ Can show execution metadata (time, cost, tokens)
- ✅ Room for rich formatting (JSON, tables, etc.)

**Cons:**
- ❌ Requires clicking the task
- ❌ Can't see output of agent's other tasks

**Visual concept:**
```
Canvas:
┌──────────────────┐
│ Task: Analysis   │ <-- Click
│ user → gpt-4     │
│ [COMPLETED] ✓    │
└──────────────────┘

Expands to:
┌────────────────────────────────────┐
│ Task Details                       │
│ ────────────────────────────────── │
│ Description: Analyze sales data    │
│ From: user                         │
│ To: gpt-4                          │
│ Status: Completed ✓                │
│ Executed: 2 mins ago               │
│ Duration: 3.2s                     │
│                                    │
│ ━━━ INPUT ━━━                      │
│ "Please analyze Q4 sales..."       │
│                                    │
│ ━━━ OUTPUT ━━━                     │
│ {                                  │
│   "total_sales": 1250000,          │
│   "top_products": [...],           │
│   "insights": "Revenue up 15%..."  │
│ }                                  │
│                                    │
│ [Copy Output] [Download] [Retry]   │
└────────────────────────────────────┘
```

---

## 🎯 Recommended Solution: Hybrid Approach

Combine multiple levels for maximum flexibility:

### Level 1: Quick Preview (Canvas)
Show a **subtle indicator** on completed tasks:
```
┌──────────────────┐
│ Task: Analysis   │
│ user → gpt-4     │
│ [COMPLETED] ✓    │
│ 📄 Has output    │ <- Small badge/icon
└──────────────────┘
```

### Level 2: Task Detail Panel (Primary View) ⭐
**This is where users see full output**
- Click task → Expanded panel shows full input/output
- Rich formatting support (JSON, markdown, code blocks)
- Copy/download buttons
- Execution metadata

### Level 3: Agent History Panel (Secondary View)
**See all outputs from this agent**
- Click agent → Show list of all tasks it executed
- Click any task in the list → See its output
- Good for debugging "What did this agent do?"

### Level 4: Floating Output Viewer (Optional - Future)
**For comparing multiple outputs**
- "Pin" outputs to a floating panel
- Compare side-by-side
- Good for data analysis workflows

---

## Implementation Roadmap

### Phase 1: Task Detail Panel (START HERE)
**Modify existing task panel to show output**

#### Frontend Changes
**File:** `internal/web/static/js/modules/agent-canvas.js`

```javascript
// In drawExpandedTaskPanel() method
drawExpandedTaskPanel() {
    // ... existing code ...

    // Add output section
    if (this.expandedTask.result) {
        currentY += 20;
        this.ctx.fillStyle = '#1f2937';
        this.ctx.font = 'bold 14px system-ui';
        this.ctx.fillText('Output:', panelX + padding, currentY);

        currentY += 20;
        this.ctx.fillStyle = '#374151';
        this.ctx.font = '12px monospace';

        // Format and wrap output
        const output = this.expandedTask.result.output || 'No output';
        const lines = this.wrapText(output, panelWidth - padding * 2);

        lines.forEach((line, i) => {
            this.ctx.fillText(line, panelX + padding, currentY + i * 16);
        });

        // Add copy button
        currentY += lines.length * 16 + 10;
        this.drawCopyButton(panelX + padding, currentY);
    }
}
```

#### Backend Changes
**File:** `internal/agentstudio/workspace.go`

Update Task schema:
```go
type Task struct {
    // ... existing fields ...
    Result *TaskResult `json:"result,omitempty"`
}

type TaskResult struct {
    Output      string                 `json:"output"`
    RawResponse string                 `json:"raw_response,omitempty"`
    Metadata    map[string]interface{} `json:"metadata,omitempty"`
    TokensUsed  int                    `json:"tokens_used,omitempty"`
    Duration    float64                `json:"duration_ms"`
    Timestamp   time.Time              `json:"timestamp"`
}
```

#### API Endpoints
```
GET /api/orchestration/tasks/{id}/output  - Get task output
```

---

### Phase 2: Agent History Panel
**Show task execution history when clicking agent**

#### Frontend Changes
**File:** `internal/web/static/js/modules/agent-canvas.js`

```javascript
drawExpandedAgentPanel() {
    // ... existing code ...

    // Add task history section
    const agentTasks = this.tasks.filter(t => t.to === this.expandedAgent.name);

    this.ctx.fillStyle = '#1f2937';
    this.ctx.font = 'bold 14px system-ui';
    this.ctx.fillText(`Task History (${agentTasks.length})`, panelX + padding, currentY);

    currentY += 20;

    agentTasks.forEach((task, i) => {
        currentY += 30;
        // Draw mini task card with output preview
        this.drawMiniTaskCard(task, panelX + padding, currentY);
    });
}

drawMiniTaskCard(task, x, y) {
    const cardWidth = this.expandedAgentPanelTargetWidth - 40;
    const cardHeight = 60;

    // Card background
    this.ctx.fillStyle = task.status === 'completed' ? '#f0fdf4' : '#f9fafb';
    this.roundRect(x, y, cardWidth, cardHeight, 6);
    this.ctx.fill();

    // Task description
    this.ctx.fillStyle = '#1f2937';
    this.ctx.font = 'bold 11px system-ui';
    this.ctx.fillText(task.description || 'Task', x + 8, y + 16);

    // Status
    this.ctx.fillStyle = '#6b7280';
    this.ctx.font = '9px system-ui';
    this.ctx.fillText(task.status, x + 8, y + 32);

    // Output preview (if completed)
    if (task.result && task.result.output) {
        this.ctx.fillStyle = '#374151';
        this.ctx.font = '9px monospace';
        const preview = task.result.output.substring(0, 40) + '...';
        this.ctx.fillText(preview, x + 8, y + 48);
    }

    // Store bounds for click detection
    if (!task.miniCardBounds) {
        task.miniCardBounds = { x, y, width: cardWidth, height: cardHeight };
    }
}
```

#### API Endpoints
```
GET /api/orchestration/agents/{name}/history?studio_id=xxx - Get all tasks by agent
```

---

### Phase 3: Quick Indicators
**Add output badges to completed task cards**

#### Frontend Changes
**File:** `internal/web/static/js/modules/agent-canvas.js`

```javascript
// In drawTaskFlows() - after drawing task card
if (task.status === 'completed' && task.result) {
    // Draw small "has output" indicator
    this.ctx.fillStyle = '#28a745';
    this.ctx.font = '10px system-ui';
    this.ctx.fillText('📄', cardX + cardWidth - 20, cardY + cardHeight - 8);
}
```

---

## Part 2: Agent-to-Agent Task Chaining

### Goal
Allow users to create visual workflows by connecting tasks and agents.

### Architecture Overview

```
Task 1 (Agent A) → Output → Task 2 (Agent B) → Output → Task 3 (Agent C)
```

---

### Phase 1: Visual Connection System

#### 1.1 Connection Mode UI
- Add **"Connect Mode" toggle button** in canvas toolbar
- When in connection mode:
  - Click a task → then click an agent (assigns task)
  - Click an agent → then click another agent (chains output)
  - Show visual feedback (highlighted borders, cursor changes)
  - ESC to cancel connection

#### 1.2 Connection Drawing
**Different line styles:**
- Task → Agent: Blue solid line with arrow (assignment)
- Agent → Agent: Purple dashed line (output chaining)
- Task → Task: Green dotted line (result dependency)

**Canvas drawing code:**
```javascript
// In agent-canvas.js
this.connections = [
    { from: 'task-123', to: 'agent-1', type: 'assignment' },
    { from: 'agent-1', to: 'agent-2', type: 'output-chain' }
]

drawConnections() {
    this.connections.forEach(conn => {
        const from = this.getElement(conn.from)
        const to = this.getElement(conn.to)

        // Different styles for different connection types
        if (conn.type === 'assignment') {
            this.ctx.strokeStyle = '#0d6efd'
            this.ctx.setLineDash([])
        } else if (conn.type === 'output-chain') {
            this.ctx.strokeStyle = '#9b59b6'
            this.ctx.setLineDash([5, 5])
        }

        this.ctx.lineWidth = 3
        this.drawArrow(from.x, from.y, to.x, to.y)
    })
}

drawArrow(fromX, fromY, toX, toY) {
    // Draw line
    this.ctx.beginPath()
    this.ctx.moveTo(fromX, fromY)
    this.ctx.lineTo(toX, toY)
    this.ctx.stroke()

    // Draw arrow head
    const angle = Math.atan2(toY - fromY, toX - fromX)
    const headLength = 15

    this.ctx.beginPath()
    this.ctx.moveTo(toX, toY)
    this.ctx.lineTo(
        toX - headLength * Math.cos(angle - Math.PI / 6),
        toY - headLength * Math.sin(angle - Math.PI / 6)
    )
    this.ctx.lineTo(
        toX - headLength * Math.cos(angle + Math.PI / 6),
        toY - headLength * Math.sin(angle + Math.PI / 6)
    )
    this.ctx.closePath()
    this.ctx.fill()
}
```

#### 1.3 Connection Management
- Hover over connection line → Highlight and show tooltip
- Click connection line → Show context menu (Delete, Edit)
- Store connection bounds for hit detection

---

### Phase 2: Data Model Updates

#### Backend Schema Extensions

**File:** `internal/agentstudio/workspace.go`

```go
type Task struct {
    // ... existing fields ...
    InputTaskIDs   []string `json:"input_task_ids,omitempty"`   // Tasks whose results are inputs
    InputAgentID   string   `json:"input_agent_id,omitempty"`   // Which agent's output to use
    OutputAgentID  string   `json:"output_agent_id,omitempty"`  // Pass result to this agent
    ChainID        string   `json:"chain_id,omitempty"`         // ID of the chain this task belongs to
}

type AgentChain struct {
    ID             string      `json:"id"`
    WorkspaceID    string      `json:"workspace_id"`
    Name           string      `json:"name"`
    Description    string      `json:"description"`
    Steps          []ChainStep `json:"steps"`
    ExecutionOrder []string    `json:"execution_order"`
    CreatedAt      time.Time   `json:"created_at"`
    UpdatedAt      time.Time   `json:"updated_at"`
}

type ChainStep struct {
    StepID         string                 `json:"step_id"`
    AgentName      string                 `json:"agent_name"`
    InputFrom      string                 `json:"input_from"`      // Previous step ID
    TaskID         string                 `json:"task_id"`         // Task to execute
    OutputTo       string                 `json:"output_to"`       // Next step ID
    Condition      string                 `json:"condition,omitempty"` // Optional: conditional branching
    Metadata       map[string]interface{} `json:"metadata,omitempty"`
}

type Connection struct {
    ID          string    `json:"id"`
    WorkspaceID string    `json:"workspace_id"`
    FromType    string    `json:"from_type"`    // "task" or "agent"
    FromID      string    `json:"from_id"`
    ToType      string    `json:"to_type"`      // "task" or "agent"
    ToID        string    `json:"to_id"`
    Type        string    `json:"type"`         // "assignment", "output-chain", "result-dependency"
    CreatedAt   time.Time `json:"created_at"`
}
```

#### API Endpoints

**Connection Management:**
```
POST   /api/orchestration/connections              - Create connection
DELETE /api/orchestration/connections/{id}         - Delete connection
GET    /api/orchestration/connections?studio_id=xxx - List connections
```

**Chain Management:**
```
POST   /api/orchestration/chains                   - Create chain
GET    /api/orchestration/chains/{workspace_id}    - Get all chains
POST   /api/orchestration/chains/{id}/execute      - Execute chain
DELETE /api/orchestration/chains/{id}              - Delete chain
```

---

### Phase 3: Execution Engine

#### Workflow Executor

**File:** `internal/orchestration/chain_executor.go`

```go
package orchestration

import (
    "context"
    "fmt"
    "log"
    "time"
)

type ChainExecutor struct {
    workspaceStore agentstudio.Store
    taskHandler    agentstudio.TaskHandler
}

func (e *ChainExecutor) ExecuteChain(ctx context.Context, chainID string) error {
    chain, err := e.workspaceStore.GetChain(chainID)
    if err != nil {
        return fmt.Errorf("failed to get chain: %w", err)
    }

    log.Printf("🔗 Starting chain execution: %s (%d steps)", chain.Name, len(chain.Steps))

    var previousOutput string
    stepResults := make(map[string]string) // Store results by step ID

    for i, step := range chain.Steps {
        log.Printf("📍 Executing step %d/%d: %s (Agent: %s)",
            i+1, len(chain.Steps), step.StepID, step.AgentName)

        // Get input from previous step if specified
        var input string
        if step.InputFrom != "" {
            input = stepResults[step.InputFrom]
        } else {
            input = previousOutput
        }

        // Execute task with context from previous step
        result, err := e.executeTaskWithContext(ctx, step.TaskID, input, step.AgentName)
        if err != nil {
            log.Printf("❌ Step %s failed: %v", step.StepID, err)
            return e.handleChainError(chain, step, err)
        }

        // Store result
        stepResults[step.StepID] = result.Output
        previousOutput = result.Output

        // Notify step completion
        e.notifyStepComplete(chain, step, result)

        log.Printf("✅ Step %s completed: %s", step.StepID, truncate(result.Output, 50))
    }

    log.Printf("🎉 Chain execution completed: %s", chain.Name)
    return nil
}

func (e *ChainExecutor) executeTaskWithContext(
    ctx context.Context,
    taskID string,
    previousOutput string,
    agentName string,
) (*TaskResult, error) {
    // Get task
    task, err := e.workspaceStore.GetTask(taskID)
    if err != nil {
        return nil, err
    }

    // Append previous output to task context
    if previousOutput != "" {
        if task.Context == nil {
            task.Context = make(map[string]interface{})
        }
        task.Context["previous_output"] = previousOutput
    }

    // Execute task
    result, err := e.taskHandler.ExecuteTask(ctx, task, agentName)
    if err != nil {
        return nil, err
    }

    return result, nil
}

func (e *ChainExecutor) handleChainError(chain *AgentChain, step ChainStep, err error) error {
    // Mark subsequent tasks as blocked
    // Send notification
    // Update chain status
    return fmt.Errorf("chain execution failed at step %s: %w", step.StepID, err)
}

func (e *ChainExecutor) notifyStepComplete(chain *AgentChain, step ChainStep, result *TaskResult) {
    // Send event to frontend via SSE
    // Update task status
    // Log metrics
}

func truncate(s string, maxLen int) string {
    if len(s) <= maxLen {
        return s
    }
    return s[:maxLen] + "..."
}
```

---

### Phase 4: Advanced Features

#### 4.1 Conditional Chains
Branch based on agent output:
```go
type ChainStep struct {
    // ... existing fields ...
    Conditions []Condition `json:"conditions,omitempty"`
}

type Condition struct {
    Field    string `json:"field"`     // Field to check in output
    Operator string `json:"operator"`  // "equals", "contains", "greater_than"
    Value    string `json:"value"`
    NextStep string `json:"next_step"` // Step ID to jump to if condition is true
}
```

#### 4.2 Parallel Execution
Execute multiple agents simultaneously:
```go
type ChainStep struct {
    // ... existing fields ...
    Parallel   bool     `json:"parallel"`
    ParallelIDs []string `json:"parallel_ids"` // Other steps to execute in parallel
}
```

#### 4.3 Chain Templates
Pre-built workflows:
- "Data Processing Pipeline"
- "Multi-Agent Research"
- "Code Review Workflow"
- "Content Generation Pipeline"

#### 4.4 Debugging Tools
- Step-through execution
- Pause/Resume chain
- View data at each step
- Execution timeline

---

## UI/UX Best Practices

### 1. Rich Output Formatting

Support different output types:
- **Plain text**: Wrapped, monospace font
- **JSON**: Syntax highlighting, collapsible sections
- **Markdown**: Rendered with formatting
- **Code blocks**: Language-specific highlighting
- **Tables**: Interactive, sortable
- **Images**: Base64 encoded images displayed inline

**Implementation:**
```javascript
formatOutput(output, type) {
    switch(type) {
        case 'json':
            return this.renderJSON(JSON.parse(output))
        case 'markdown':
            return marked.parse(output)
        case 'code':
            return this.highlightCode(output)
        default:
            return output
    }
}
```

### 2. Copy/Download Actions

**Action buttons in task panel:**
```
[📋 Copy Output] [💾 Download JSON] [🔗 Share Link] [🔄 Retry]
```

### 3. Truncation for Long Outputs

```javascript
if (output.length > 1000) {
    showTruncated = output.substring(0, 1000) + '...'
    showButton = '[Show Full Output]'
}
```

### 4. Search/Filter in Agent History

```
[🔍 Search outputs...] [Filter: ▼ All | Completed | Failed]
```

### 5. Execution Animation

Visualize chain execution with animated particles flowing along connections:
```javascript
animateChainExecution(chain) {
    chain.connections.forEach(conn => {
        this.drawFlowingParticles(conn.from, conn.to)
    })
}
```

---

## Quick Win: Minimal Viable Feature

### Goal: Get basic chaining working in 1-2 hours

**What to build:**
1. Add **"Chain Result"** button to expanded task panel
2. When clicked: Prompt user to select next agent
3. Store `output_agent` field in task
4. On task completion: Auto-create new task for next agent with result as input
5. Draw dashed line from completed task to next agent

**Code:**
```javascript
// Add to expanded task panel
if (task.status === 'completed') {
    this.drawButton('Chain to Agent', () => {
        const agent = this.promptAgentSelection()
        if (agent) {
            this.createChainedTask(task, agent)
        }
    })
}

async createChainedTask(sourceTask, targetAgent) {
    const newTask = {
        studio_id: this.studioId,
        from: sourceTask.to, // Output agent becomes sender
        to: targetAgent.name,
        description: `Follow-up from: ${sourceTask.description}`,
        input_task_ids: [sourceTask.id],
        priority: 0
    }

    await this.createTask(newTask)
}
```

---

## Testing Checklist

### Output Viewing
- [ ] Click task → Panel shows output section
- [ ] Long outputs are truncated with "Show More"
- [ ] JSON outputs are formatted and syntax-highlighted
- [ ] Copy button copies full output to clipboard
- [ ] Click agent → Shows task history with outputs
- [ ] Completed tasks show "📄 Has output" badge

### Task Chaining
- [ ] Enable connection mode → Cursor changes
- [ ] Click task → Click agent → Connection drawn
- [ ] Click agent → Click agent → Output chain drawn
- [ ] Hover connection → Tooltip shows details
- [ ] Click connection → Delete option appears
- [ ] Execute chained tasks → Output passes correctly

### Error Handling
- [ ] Failed task stops chain execution
- [ ] Error message shows which step failed
- [ ] Can retry from failed step
- [ ] Long outputs don't crash browser

---

## Future Enhancements

### Version 2.0
- [ ] Drag-and-drop to create connections
- [ ] Multi-select tasks/agents
- [ ] Bulk operations
- [ ] Export chain as JSON template
- [ ] Import chain from template

### Version 3.0
- [ ] Conditional branching (if/else logic)
- [ ] Parallel execution
- [ ] Loop/iteration support
- [ ] Variables and data transformation
- [ ] Built-in library of chain templates

### Version 4.0
- [ ] Visual programming interface
- [ ] No-code workflow builder
- [ ] AI-suggested chain optimizations
- [ ] Performance analytics dashboard
- [ ] Cost tracking per chain

---

## Resources

### Files to Modify

**Frontend:**
- `internal/web/static/js/modules/agent-canvas.js` - Main canvas logic
- `internal/web/templates/pages/studios.tmpl` - Canvas page

**Backend:**
- `internal/agentstudio/workspace.go` - Data models
- `internal/orchestrationhttp/handlers.go` - API endpoints
- `internal/orchestration/chain_executor.go` - Chain execution (new file)

### Dependencies
- None required for basic implementation
- Consider adding syntax highlighting library for JSON (e.g., Prism.js)

---

## Decision Log

| Date | Decision | Rationale |
|------|----------|-----------|
| 2025-11-08 | Use Task Detail Panel as primary output view | Most intuitive, shows input+output together |
| 2025-11-08 | Add Agent History Panel as secondary view | Good for debugging, seeing all agent outputs |
| 2025-11-08 | Support hybrid approach with multiple views | Flexibility for different use cases |
| 2025-11-08 | Store connections as separate entities | Easier to manage, query, and visualize |

---

## Next Steps

1. **Implement Task Detail Panel with Output** (Phase 1)
   - Update backend Task schema with Result field
   - Modify canvas to show output in expanded panel
   - Add copy/download buttons

2. **Add Agent History Panel** (Phase 2)
   - Show all tasks executed by agent
   - Click task in history to see details

3. **Quick Win: Basic Chaining** (1-2 hours)
   - Add "Chain to Agent" button
   - Auto-create linked task on completion

4. **Full Connection System** (Phase 3+)
   - Connection mode UI
   - Visual connection drawing
   - Chain execution engine

---

**Document Version:** 1.0
**Last Updated:** 2025-11-08
**Author:** AI Assistant & User Collaboration
