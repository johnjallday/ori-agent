# Progress Tracking & Monitoring - Implementation Plan

## Overview
This document outlines the implementation plan for tracking and visualizing progress across:
1. Individual task execution
2. Agent activity and workload
3. Workspace/Studio overall progress
4. Chain execution progress
5. Real-time status updates

---

## User Stories

### As a user, I want to...
- ✅ See which tasks are currently running
- ✅ Know how long a task has been executing
- ✅ View the percentage completion of a chain
- ✅ Monitor agent workload (idle/busy/overloaded)
- ✅ See a timeline of all activities in a workspace
- ✅ Get notifications when tasks complete/fail
- ✅ View historical progress and trends
- ✅ Debug stuck or slow tasks

---

## Part 1: Task-Level Progress

### 1.1 Task Status States

**Current states:**
- `pending` - Waiting to execute
- `in_progress` - Currently executing
- `completed` - Successfully finished
- `failed` - Execution failed

**New states to add:**
- `queued` - In execution queue, will run soon
- `blocked` - Waiting for dependencies
- `timeout` - Execution exceeded time limit
- `cancelled` - Manually cancelled by user
- `retrying` - Automatic retry in progress

### 1.2 Progress Metadata

**Backend schema update:**
```go
type Task struct {
    // ... existing fields ...
    Progress *TaskProgress `json:"progress,omitempty"`
}

type TaskProgress struct {
    Status          string    `json:"status"`
    Percentage      int       `json:"percentage"`        // 0-100
    CurrentStep     string    `json:"current_step"`      // e.g. "Analyzing data..."
    TotalSteps      int       `json:"total_steps"`
    CompletedSteps  int       `json:"completed_steps"`
    StartedAt       time.Time `json:"started_at"`
    EstimatedEnd    time.Time `json:"estimated_end,omitempty"`
    ElapsedTime     float64   `json:"elapsed_time_ms"`
    RemainingTime   float64   `json:"remaining_time_ms,omitempty"`
    Logs            []string  `json:"logs,omitempty"`    // Step-by-step logs
}
```

### 1.3 Visual Indicators in Canvas

#### Progress Bar on Task Card
```
┌────────────────────────────┐
│ Task: Data Analysis        │
│ user → gpt-4               │
│ [IN PROGRESS] ⏳          │
│                            │
│ ████████░░░░░░░░ 45%       │ <- Progress bar
│ Step 3/7: Processing...    │ <- Current step
│ Est. 2m 30s remaining      │ <- Time estimate
└────────────────────────────┘
```

**Canvas drawing code:**
```javascript
// In drawTaskFlows() - add progress bar for in_progress tasks
if (task.status === 'in_progress' && task.progress) {
    const progressBarY = cardY + cardHeight - 8;
    const progressBarHeight = 4;
    const progressBarWidth = cardWidth - 16;

    // Background
    this.ctx.fillStyle = '#e5e7eb';
    this.ctx.fillRect(cardX + 8, progressBarY, progressBarWidth, progressBarHeight);

    // Progress fill
    const fillWidth = (progressBarWidth * task.progress.percentage) / 100;
    this.ctx.fillStyle = '#3b82f6';
    this.ctx.fillRect(cardX + 8, progressBarY, fillWidth, progressBarHeight);

    // Percentage text
    this.ctx.fillStyle = '#6b7280';
    this.ctx.font = '9px system-ui';
    this.ctx.fillText(`${task.progress.percentage}%`, cardX + 8, progressBarY - 4);
}
```

#### Animated Progress Indicator
```javascript
// Spinning loader for tasks without percentage
if (task.status === 'in_progress' && !task.progress.percentage) {
    const spinnerX = cardX + cardWidth - 20;
    const spinnerY = cardY + 10;
    const spinnerRadius = 6;

    this.ctx.strokeStyle = '#3b82f6';
    this.ctx.lineWidth = 2;
    this.ctx.beginPath();
    this.ctx.arc(spinnerX, spinnerY, spinnerRadius, 0, Math.PI * 1.5);
    this.ctx.stroke();

    // Rotate over time
    this.ctx.save();
    this.ctx.translate(spinnerX, spinnerY);
    this.ctx.rotate((Date.now() / 1000) % (2 * Math.PI));
    this.ctx.translate(-spinnerX, -spinnerY);
    // ... draw spinner arc
    this.ctx.restore();
}
```

### 1.4 Task Detail Panel Progress View

**Expanded panel with live progress:**
```
┌──────────────────────────────────────┐
│ Task Details                         │
│ ──────────────────────────────────── │
│ Description: Analyze Q4 sales data   │
│ Status: IN PROGRESS ⏳              │
│                                      │
│ ━━━ PROGRESS ━━━                     │
│                                      │
│ ████████████░░░░░░ 65%               │
│                                      │
│ Step 4/7: Analyzing patterns         │
│ Elapsed: 1m 23s                      │
│ Remaining: ~1m 15s                   │
│                                      │
│ ━━━ ACTIVITY LOG ━━━                 │
│ ✓ Step 1: Data loaded (12s)         │
│ ✓ Step 2: Cleaned data (8s)         │
│ ✓ Step 3: Calculated metrics (15s)  │
│ ⏳ Step 4: Analyzing patterns...     │
│ ⏱  Step 5: Generating insights       │
│ ⏱  Step 6: Creating visualizations   │
│ ⏱  Step 7: Preparing report          │
│                                      │
│ [Pause] [Cancel] [View Logs]         │
└──────────────────────────────────────┘
```

---

## Part 2: Agent-Level Progress

### 2.1 Agent Activity States

**Agent states:**
- `idle` - No tasks assigned
- `active` - Currently executing task(s)
- `busy` - Multiple tasks queued
- `overloaded` - Too many tasks (warn user)
- `error` - Last task failed

### 2.2 Agent Metrics

**Backend schema:**
```go
type Agent struct {
    Name            string          `json:"name"`
    Type            string          `json:"type"`
    Status          string          `json:"status"`
    CurrentTasks    []string        `json:"current_tasks"`    // IDs of running tasks
    QueuedTasks     []string        `json:"queued_tasks"`     // IDs of pending tasks
    CompletedTasks  int             `json:"completed_tasks"`
    FailedTasks     int             `json:"failed_tasks"`
    TotalExecutions int             `json:"total_executions"`
    AverageTime     float64         `json:"average_time_ms"`
    LastActive      time.Time       `json:"last_active"`
    Utilization     float64         `json:"utilization"`      // 0-1 (percentage busy)
}
```

### 2.3 Visual Indicators on Agent Nodes

**Agent status badges:**
```
┌───────────────────────┐
│   Agent: GPT-4        │
│   ●●● (3 tasks) 🔥    │ <- Busy indicator
│                       │
│   ⏳ Running: 2       │ <- Current tasks
│   📋 Queued: 5        │ <- Pending tasks
│   ✓ Done: 127        │ <- Completed
│                       │
│   Utilization: 75%    │ <- Load meter
│   ████████░░ 75%      │
└───────────────────────┘
```

**Canvas drawing code:**
```javascript
drawAgent(agent) {
    // ... existing agent drawing ...

    // Add status indicator
    let statusColor = '#10b981'; // green = idle
    if (agent.currentTasks.length > 0) statusColor = '#3b82f6'; // blue = active
    if (agent.queuedTasks.length > 5) statusColor = '#f59e0b'; // orange = busy
    if (agent.status === 'error') statusColor = '#ef4444'; // red = error

    // Pulsing ring for active agents
    if (agent.currentTasks.length > 0) {
        const pulseRadius = agent.radius + 5 + Math.sin(Date.now() / 500) * 3;
        this.ctx.strokeStyle = statusColor + '40';
        this.ctx.lineWidth = 2;
        this.ctx.beginPath();
        this.ctx.arc(agent.x, agent.y, pulseRadius, 0, Math.PI * 2);
        this.ctx.stroke();
    }

    // Task count badge
    if (agent.currentTasks.length > 0 || agent.queuedTasks.length > 0) {
        const badgeX = agent.x + agent.radius - 8;
        const badgeY = agent.y - agent.radius + 8;
        const taskCount = agent.currentTasks.length + agent.queuedTasks.length;

        // Badge background
        this.ctx.fillStyle = statusColor;
        this.ctx.beginPath();
        this.ctx.arc(badgeX, badgeY, 10, 0, Math.PI * 2);
        this.ctx.fill();

        // Badge text
        this.ctx.fillStyle = '#ffffff';
        this.ctx.font = 'bold 10px system-ui';
        this.ctx.textAlign = 'center';
        this.ctx.fillText(taskCount, badgeX, badgeY + 4);
        this.ctx.textAlign = 'left';
    }
}
```

### 2.4 Agent Detail Panel with Progress

**Expanded agent panel:**
```
┌──────────────────────────────────────┐
│ Agent: GPT-4                         │
│ ──────────────────────────────────── │
│ Type: Tool-Calling Agent             │
│ Status: ACTIVE 🔥                    │
│                                      │
│ ━━━ CURRENT ACTIVITY ━━━             │
│                                      │
│ ⏳ Task: "Analyze data" (45%)        │
│ ⏳ Task: "Generate report" (12%)     │
│                                      │
│ ━━━ QUEUE (5 tasks) ━━━              │
│                                      │
│ 📋 Task: "Review code"               │
│ 📋 Task: "Write tests"               │
│ 📋 Task: "Update docs"               │
│ ... and 2 more                       │
│                                      │
│ ━━━ STATISTICS ━━━                   │
│                                      │
│ Total Executions: 127                │
│ Completed: 120 (94%)                 │
│ Failed: 7 (6%)                       │
│ Avg Duration: 8.3s                   │
│ Current Load: 75%                    │
│                                      │
│ ████████████░░░░ 75%                 │
│                                      │
│ [View History] [Clear Queue]         │
└──────────────────────────────────────┘
```

---

## Part 3: Workspace-Level Progress

### 3.1 Dashboard Overview Panel

**Global progress view (top of canvas):**
```
┌─────────────────────────────────────────────────────────┐
│ Workspace: DevOps Project                               │
│ ─────────────────────────────────────────────────────── │
│                                                         │
│ Tasks: 12 total | 3 running ⏳ | 7 done ✓ | 2 pending  │
│                                                         │
│ ████████████████████░░░░░░░░ 75% Complete              │
│                                                         │
│ Agents: 3 total | 2 active 🔥 | 1 idle 💤             │
│                                                         │
│ Est. completion: 15 minutes                             │
└─────────────────────────────────────────────────────────┘
```

### 3.2 Progress Statistics

**API endpoint:**
```
GET /api/orchestration/workspace/{id}/progress
```

**Response:**
```json
{
  "workspace_id": "111a7b7f...",
  "overall_progress": {
    "total_tasks": 12,
    "completed": 7,
    "in_progress": 3,
    "pending": 2,
    "failed": 0,
    "percentage": 75
  },
  "agents": {
    "total": 3,
    "active": 2,
    "idle": 1,
    "utilization": 0.67
  },
  "timeline": {
    "started_at": "2025-11-08T10:00:00Z",
    "estimated_completion": "2025-11-08T10:15:00Z",
    "elapsed_time_ms": 600000,
    "remaining_time_ms": 300000
  },
  "chains": [
    {
      "chain_id": "chain-1",
      "name": "Data Pipeline",
      "progress": 60,
      "current_step": "Step 3/5"
    }
  ]
}
```

### 3.3 Canvas Implementation

**Top panel with workspace progress:**
```javascript
drawWorkspaceProgress() {
    const panelHeight = 80;
    const panelWidth = this.width - 40;
    const panelX = 20;
    const panelY = 20;

    // Panel background
    this.ctx.fillStyle = 'rgba(255, 255, 255, 0.95)';
    this.ctx.shadowColor = 'rgba(0, 0, 0, 0.1)';
    this.ctx.shadowBlur = 10;
    this.roundRect(panelX, panelY, panelWidth, panelHeight, 8);
    this.ctx.fill();
    this.ctx.shadowColor = 'transparent';

    // Title
    this.ctx.fillStyle = '#1f2937';
    this.ctx.font = 'bold 14px system-ui';
    this.ctx.fillText(`Workspace: ${this.studio.name}`, panelX + 15, panelY + 25);

    // Progress bar
    const progressBarY = panelY + 40;
    const progressBarWidth = panelWidth - 30;
    const progressBarHeight = 8;

    this.ctx.fillStyle = '#e5e7eb';
    this.ctx.fillRect(panelX + 15, progressBarY, progressBarWidth, progressBarHeight);

    const fillWidth = (progressBarWidth * this.workspaceProgress.percentage) / 100;
    this.ctx.fillStyle = '#10b981';
    this.ctx.fillRect(panelX + 15, progressBarY, fillWidth, progressBarHeight);

    // Stats text
    this.ctx.fillStyle = '#6b7280';
    this.ctx.font = '11px system-ui';
    const statsY = panelY + 65;
    this.ctx.fillText(
        `${this.workspaceProgress.completed}/${this.workspaceProgress.total_tasks} tasks complete`,
        panelX + 15,
        statsY
    );

    // Estimated time
    if (this.workspaceProgress.timeline.remaining_time_ms) {
        const minutes = Math.ceil(this.workspaceProgress.timeline.remaining_time_ms / 60000);
        this.ctx.textAlign = 'right';
        this.ctx.fillText(
            `Est. ${minutes} min remaining`,
            panelX + panelWidth - 15,
            statsY
        );
        this.ctx.textAlign = 'left';
    }
}
```

---

## Part 4: Chain Execution Progress

### 4.1 Chain Progress Visualization

**Visual representation:**
```
Chain: Data Pipeline (Step 3/5 - 60%)

[Agent A] ──✓──> [Agent B] ──✓──> [Agent C] ──⏳──> [Agent D] ──○──> [Agent E]
   Step 1         Step 2           Step 3          Step 4        Step 5
   Done           Done             Active          Pending       Pending
```

### 4.2 Chain Progress Schema

```go
type ChainProgress struct {
    ChainID         string              `json:"chain_id"`
    Name            string              `json:"name"`
    TotalSteps      int                 `json:"total_steps"`
    CompletedSteps  int                 `json:"completed_steps"`
    CurrentStep     int                 `json:"current_step"`
    Percentage      int                 `json:"percentage"`
    Status          string              `json:"status"` // "running", "completed", "failed", "paused"
    StepStatuses    []ChainStepStatus   `json:"step_statuses"`
    StartedAt       time.Time           `json:"started_at"`
    EstimatedEnd    time.Time           `json:"estimated_end,omitempty"`
}

type ChainStepStatus struct {
    StepID      string    `json:"step_id"`
    StepNumber  int       `json:"step_number"`
    AgentName   string    `json:"agent_name"`
    Status      string    `json:"status"` // "pending", "running", "completed", "failed", "skipped"
    StartedAt   time.Time `json:"started_at,omitempty"`
    CompletedAt time.Time `json:"completed_at,omitempty"`
    Duration    float64   `json:"duration_ms,omitempty"`
    Output      string    `json:"output,omitempty"`
}
```

### 4.3 Chain Progress Visualization in Canvas

**Highlight active chain path:**
```javascript
drawChainProgress(chain) {
    chain.step_statuses.forEach((step, index) => {
        const fromAgent = this.agents.find(a => a.name === step.agent_name);
        const toAgent = this.agents.find(a => a.name === chain.steps[index + 1]?.agent_name);

        // Draw connection with status color
        let color = '#9ca3af'; // gray = pending
        if (step.status === 'completed') color = '#10b981'; // green
        if (step.status === 'running') color = '#3b82f6'; // blue
        if (step.status === 'failed') color = '#ef4444'; // red

        if (fromAgent && toAgent) {
            this.ctx.strokeStyle = color;
            this.ctx.lineWidth = 4;
            this.ctx.setLineDash([]);
            this.drawArrow(fromAgent.x, fromAgent.y, toAgent.x, toAgent.y);
        }

        // Draw step number badge
        if (fromAgent) {
            const badgeX = fromAgent.x;
            const badgeY = fromAgent.y - fromAgent.radius - 20;

            this.ctx.fillStyle = color;
            this.ctx.beginPath();
            this.ctx.arc(badgeX, badgeY, 12, 0, Math.PI * 2);
            this.ctx.fill();

            this.ctx.fillStyle = '#ffffff';
            this.ctx.font = 'bold 10px system-ui';
            this.ctx.textAlign = 'center';
            this.ctx.fillText(step.step_number, badgeX, badgeY + 4);
            this.ctx.textAlign = 'left';
        }

        // Animated particles for running step
        if (step.status === 'running' && fromAgent && toAgent) {
            this.drawFlowingParticles(fromAgent, toAgent, color);
        }
    });
}

drawFlowingParticles(fromAgent, toAgent, color) {
    const particleCount = 3;
    const time = Date.now() / 1000;

    for (let i = 0; i < particleCount; i++) {
        const offset = (time + i / particleCount) % 1;
        const x = fromAgent.x + (toAgent.x - fromAgent.x) * offset;
        const y = fromAgent.y + (toAgent.y - fromAgent.y) * offset;

        this.ctx.fillStyle = color;
        this.ctx.beginPath();
        this.ctx.arc(x, y, 4, 0, Math.PI * 2);
        this.ctx.fill();
    }

    // Request next frame for animation
    this.draw();
}
```

---

## Part 5: Real-Time Updates

### 5.1 Server-Sent Events (SSE)

**Backend event streaming:**
```go
// In internal/orchestrationhttp/handlers.go
func (h *Handler) ProgressStreamHandler(w http.ResponseWriter, r *http.Request) {
    workspaceID := r.URL.Query().Get("workspace_id")

    // Set SSE headers
    w.Header().Set("Content-Type", "text/event-stream")
    w.Header().Set("Cache-Control", "no-cache")
    w.Header().Set("Connection", "keep-alive")

    flusher, ok := w.(http.Flusher)
    if !ok {
        http.Error(w, "Streaming not supported", http.StatusInternalServerError)
        return
    }

    // Subscribe to progress events
    events := h.eventBus.Subscribe(workspaceID)
    defer h.eventBus.Unsubscribe(workspaceID, events)

    for {
        select {
        case event := <-events:
            // Send progress update
            data, _ := json.Marshal(event)
            fmt.Fprintf(w, "data: %s\n\n", data)
            flusher.Flush()

        case <-r.Context().Done():
            return
        }
    }
}
```

**Frontend connection:**
```javascript
connectProgressStream() {
    this.progressEventSource = new EventSource(
        `/api/orchestration/progress/stream?workspace_id=${this.studioId}`
    );

    this.progressEventSource.onmessage = (event) => {
        const progressUpdate = JSON.parse(event.data);
        this.handleProgressUpdate(progressUpdate);
    };

    this.progressEventSource.onerror = () => {
        console.error('Progress stream connection lost, reconnecting...');
        setTimeout(() => this.connectProgressStream(), 5000);
    };
}

handleProgressUpdate(update) {
    if (update.type === 'task_progress') {
        const task = this.tasks.find(t => t.id === update.task_id);
        if (task) {
            task.progress = update.progress;
            this.draw();
        }
    } else if (update.type === 'workspace_progress') {
        this.workspaceProgress = update.progress;
        this.draw();
    } else if (update.type === 'chain_progress') {
        this.updateChainProgress(update.chain_id, update.progress);
        this.draw();
    }
}
```

### 5.2 Progress Event Types

**Event structure:**
```json
{
  "type": "task_progress",
  "timestamp": "2025-11-08T10:05:23Z",
  "workspace_id": "111a7b7f...",
  "task_id": "task-123",
  "progress": {
    "percentage": 45,
    "current_step": "Analyzing patterns",
    "completed_steps": 3,
    "total_steps": 7,
    "elapsed_time_ms": 5230,
    "remaining_time_ms": 6450
  }
}
```

```json
{
  "type": "agent_status",
  "timestamp": "2025-11-08T10:05:24Z",
  "workspace_id": "111a7b7f...",
  "agent_name": "gpt-4",
  "status": "active",
  "current_tasks": ["task-123", "task-456"],
  "utilization": 0.75
}
```

```json
{
  "type": "chain_progress",
  "timestamp": "2025-11-08T10:05:25Z",
  "workspace_id": "111a7b7f...",
  "chain_id": "chain-1",
  "progress": {
    "percentage": 60,
    "current_step": 3,
    "total_steps": 5,
    "step_statuses": [...]
  }
}
```

---

## Part 6: Activity Timeline

### 6.1 Timeline Panel

**Visual timeline of all events:**
```
┌────────────────────────────────────────┐
│ ACTIVITY TIMELINE                      │
│ ──────────────────────────────────────│
│                                        │
│ 10:05:30  ✓ Task "Analyze" completed   │
│           Agent: GPT-4 | Duration: 3s  │
│                                        │
│ 10:05:15  ⏳ Task "Process" started    │
│           Agent: GPT-4 | Progress: 45% │
│                                        │
│ 10:05:00  🔗 Chain "Pipeline" started  │
│           5 steps                      │
│                                        │
│ 10:04:30  📋 Task "Review" created     │
│           Assigned to: GPT-4           │
│                                        │
│ [Load More] [Export Timeline]          │
└────────────────────────────────────────┘
```

### 6.2 Timeline Data Model

```go
type TimelineEvent struct {
    ID          string                 `json:"id"`
    WorkspaceID string                 `json:"workspace_id"`
    Type        string                 `json:"type"` // "task_created", "task_started", "task_completed", etc.
    Timestamp   time.Time              `json:"timestamp"`
    ActorType   string                 `json:"actor_type"` // "user", "agent", "system"
    ActorID     string                 `json:"actor_id"`
    EntityType  string                 `json:"entity_type"` // "task", "chain", "agent"
    EntityID    string                 `json:"entity_id"`
    Message     string                 `json:"message"`
    Metadata    map[string]interface{} `json:"metadata,omitempty"`
}
```

**API endpoint:**
```
GET /api/orchestration/timeline?workspace_id=xxx&limit=50&offset=0
```

### 6.3 Canvas Timeline Panel

**Add timeline panel to side of canvas:**
```javascript
drawTimeline() {
    const panelWidth = 300;
    const panelHeight = this.height - 40;
    const panelX = this.width - panelWidth - 20;
    const panelY = 100;

    // Panel background
    this.ctx.fillStyle = 'rgba(255, 255, 255, 0.95)';
    this.ctx.shadowColor = 'rgba(0, 0, 0, 0.1)';
    this.ctx.shadowBlur = 10;
    this.roundRect(panelX, panelY, panelWidth, panelHeight, 8);
    this.ctx.fill();
    this.ctx.shadowColor = 'transparent';

    // Title
    this.ctx.fillStyle = '#1f2937';
    this.ctx.font = 'bold 14px system-ui';
    this.ctx.fillText('Activity Timeline', panelX + 15, panelY + 25);

    // Timeline events
    let currentY = panelY + 50;
    this.timelineEvents.slice(0, 10).forEach(event => {
        this.drawTimelineEvent(event, panelX + 15, currentY, panelWidth - 30);
        currentY += 60;
    });
}

drawTimelineEvent(event, x, y, width) {
    // Icon based on event type
    const icon = this.getEventIcon(event.type);
    this.ctx.font = '16px system-ui';
    this.ctx.fillText(icon, x, y + 12);

    // Time
    const time = new Date(event.timestamp).toLocaleTimeString();
    this.ctx.fillStyle = '#6b7280';
    this.ctx.font = '10px system-ui';
    this.ctx.fillText(time, x + 25, y + 8);

    // Message
    this.ctx.fillStyle = '#1f2937';
    this.ctx.font = '11px system-ui';
    const lines = this.wrapText(event.message, width - 30);
    lines.forEach((line, i) => {
        this.ctx.fillText(line, x + 25, y + 22 + i * 14);
    });
}

getEventIcon(type) {
    const icons = {
        'task_created': '📋',
        'task_started': '⏳',
        'task_completed': '✓',
        'task_failed': '❌',
        'chain_started': '🔗',
        'agent_active': '🔥',
        'agent_idle': '💤'
    };
    return icons[type] || '•';
}
```

---

## Part 7: Notifications

### 7.1 Toast Notifications

**Show brief notifications for important events:**
```javascript
showNotification(message, type = 'info') {
    const notification = {
        id: Date.now(),
        message,
        type, // 'info', 'success', 'warning', 'error'
        timestamp: Date.now()
    };

    this.notifications.push(notification);

    // Auto-dismiss after 5 seconds
    setTimeout(() => {
        this.dismissNotification(notification.id);
    }, 5000);

    this.draw();
}

drawNotifications() {
    const notificationWidth = 300;
    const notificationHeight = 60;
    const padding = 10;

    this.notifications.forEach((notification, index) => {
        const x = this.width - notificationWidth - 20;
        const y = this.height - (notificationHeight + padding) * (index + 1) - 20;

        // Background color based on type
        const colors = {
            'info': '#3b82f6',
            'success': '#10b981',
            'warning': '#f59e0b',
            'error': '#ef4444'
        };
        this.ctx.fillStyle = colors[notification.type];
        this.ctx.shadowColor = 'rgba(0, 0, 0, 0.2)';
        this.ctx.shadowBlur = 10;
        this.roundRect(x, y, notificationWidth, notificationHeight, 8);
        this.ctx.fill();
        this.ctx.shadowColor = 'transparent';

        // Message
        this.ctx.fillStyle = '#ffffff';
        this.ctx.font = '12px system-ui';
        const lines = this.wrapText(notification.message, notificationWidth - 20);
        lines.forEach((line, i) => {
            this.ctx.fillText(line, x + 10, y + 20 + i * 16);
        });
    }
}
```

### 7.2 Notification Triggers

**Automatically show notifications for:**
- ✅ Task completed successfully
- ❌ Task failed
- ⏰ Task taking longer than expected
- 🔗 Chain completed
- ⚠️ Agent overloaded (too many queued tasks)
- 🎉 Workspace milestone reached (50%, 100% complete)

---

## Part 8: Historical Progress Tracking

### 8.1 Progress History

**Store progress snapshots over time:**
```go
type ProgressSnapshot struct {
    ID          string    `json:"id"`
    WorkspaceID string    `json:"workspace_id"`
    Timestamp   time.Time `json:"timestamp"`
    Metrics     struct {
        TotalTasks       int     `json:"total_tasks"`
        CompletedTasks   int     `json:"completed_tasks"`
        FailedTasks      int     `json:"failed_tasks"`
        ActiveAgents     int     `json:"active_agents"`
        AverageTaskTime  float64 `json:"average_task_time_ms"`
        Throughput       float64 `json:"throughput_tasks_per_min"`
    } `json:"metrics"`
}
```

### 8.2 Progress Chart

**Show progress over time in a graph:**
```javascript
drawProgressChart(history) {
    const chartWidth = 400;
    const chartHeight = 200;
    const chartX = 50;
    const chartY = this.height - chartHeight - 50;

    // Chart background
    this.ctx.fillStyle = 'rgba(255, 255, 255, 0.9)';
    this.roundRect(chartX, chartY, chartWidth, chartHeight, 8);
    this.ctx.fill();

    // Draw line graph
    this.ctx.strokeStyle = '#3b82f6';
    this.ctx.lineWidth = 2;
    this.ctx.beginPath();

    history.forEach((snapshot, index) => {
        const x = chartX + (index / history.length) * chartWidth;
        const percentage = (snapshot.metrics.completed_tasks / snapshot.metrics.total_tasks) * 100;
        const y = chartY + chartHeight - (percentage / 100) * chartHeight;

        if (index === 0) {
            this.ctx.moveTo(x, y);
        } else {
            this.ctx.lineTo(x, y);
        }
    });

    this.ctx.stroke();

    // Draw data points
    history.forEach((snapshot, index) => {
        const x = chartX + (index / history.length) * chartWidth;
        const percentage = (snapshot.metrics.completed_tasks / snapshot.metrics.total_tasks) * 100;
        const y = chartY + chartHeight - (percentage / 100) * chartHeight;

        this.ctx.fillStyle = '#3b82f6';
        this.ctx.beginPath();
        this.ctx.arc(x, y, 4, 0, Math.PI * 2);
        this.ctx.fill();
    });
}
```

---

## Implementation Roadmap

### Week 1: Basic Task Progress
- [ ] Add task progress schema to backend
- [ ] Update task execution to emit progress events
- [ ] Display progress bar on in_progress tasks
- [ ] Show elapsed time on task cards
- [ ] Add progress section to task detail panel

### Week 2: Agent Monitoring
- [ ] Track agent activity states
- [ ] Display agent workload indicators
- [ ] Add utilization meter to agent nodes
- [ ] Create agent history panel with stats
- [ ] Implement pulsing animation for active agents

### Week 3: Workspace Overview
- [ ] Add workspace progress API endpoint
- [ ] Create top panel with overall progress
- [ ] Display task completion percentage
- [ ] Show estimated time remaining
- [ ] Add agent status summary

### Week 4: Real-Time Updates
- [ ] Implement SSE for progress streaming
- [ ] Connect frontend to progress events
- [ ] Add activity timeline panel
- [ ] Create toast notification system
- [ ] Implement auto-refresh for stale data

### Week 5: Chain Progress
- [ ] Add chain progress tracking
- [ ] Highlight active chain paths
- [ ] Animate particles along connections
- [ ] Show step-by-step progress
- [ ] Add chain completion notifications

### Week 6: Polish & Advanced Features
- [ ] Historical progress tracking
- [ ] Progress charts and graphs
- [ ] Export timeline/reports
- [ ] Performance optimizations
- [ ] Mobile-responsive progress views

---

## API Endpoints Summary

```
# Progress Tracking
GET  /api/orchestration/workspace/{id}/progress           - Overall workspace progress
GET  /api/orchestration/tasks/{id}/progress              - Individual task progress
GET  /api/orchestration/agents/{name}/stats              - Agent statistics
GET  /api/orchestration/chains/{id}/progress             - Chain execution progress
GET  /api/orchestration/timeline?workspace_id=xxx        - Activity timeline

# Real-Time Streaming
GET  /api/orchestration/progress/stream?workspace_id=xxx - SSE progress updates

# Historical Data
GET  /api/orchestration/history/progress?workspace_id=xxx&from=...&to=... - Progress history
GET  /api/orchestration/history/metrics?workspace_id=xxx                  - Performance metrics
```

---

## Testing Checklist

### Task Progress
- [ ] Progress bar appears on running tasks
- [ ] Percentage updates in real-time
- [ ] Elapsed time is accurate
- [ ] Step information displays correctly
- [ ] Progress persists across page refresh

### Agent Monitoring
- [ ] Agent status changes based on activity
- [ ] Task count badges are accurate
- [ ] Utilization meter reflects actual load
- [ ] Pulsing animation smooth on active agents
- [ ] History panel shows all executed tasks

### Workspace Overview
- [ ] Overall percentage is accurate
- [ ] Agent counts are correct
- [ ] Estimated time is reasonable
- [ ] Stats update in real-time

### Real-Time Updates
- [ ] SSE connection establishes successfully
- [ ] Progress updates arrive in real-time
- [ ] Connection auto-reconnects on failure
- [ ] No memory leaks from event listeners

### Notifications
- [ ] Toast notifications appear for events
- [ ] Auto-dismiss after 5 seconds
- [ ] Multiple notifications stack properly
- [ ] User can manually dismiss

---

## Performance Considerations

### Optimization Strategies

1. **Debounce rapid updates**
   ```javascript
   this.progressUpdateDebounce = debounce(() => {
       this.draw();
   }, 100);
   ```

2. **Batch SSE events**
   - Buffer multiple events
   - Send as batch every 200ms
   - Reduce re-render frequency

3. **Virtual scrolling for timeline**
   - Only render visible events
   - Lazy load older events
   - Limit to 100 events in memory

4. **Canvas layer optimization**
   - Draw static elements to off-screen canvas
   - Only redraw animated/changing elements
   - Use requestAnimationFrame wisely

5. **Throttle progress snapshots**
   - Store snapshot every 30 seconds (not every update)
   - Aggregate metrics before storing
   - Clean up old snapshots (keep last 24 hours)

---

## Future Enhancements

### Version 2.0
- [ ] Predictive ETA using ML
- [ ] Anomaly detection (unusually slow tasks)
- [ ] Resource utilization graphs (CPU, memory)
- [ ] Cost tracking per task/agent
- [ ] SLA monitoring and alerts

### Version 3.0
- [ ] Custom progress dashboards
- [ ] Exportable progress reports
- [ ] Slack/Discord integration for notifications
- [ ] Mobile app for monitoring
- [ ] Voice alerts for critical events

---

## Resources

### Files to Create

**Backend:**
- `internal/progress/tracker.go` - Progress tracking logic
- `internal/progress/history.go` - Historical data management
- `internal/orchestrationhttp/progress_handlers.go` - Progress API endpoints

**Frontend:**
- `internal/web/static/js/modules/progress-tracker.js` - Progress tracking module
- `internal/web/static/js/modules/notifications.js` - Notification system

### Dependencies
- None required for basic implementation
- Consider: Chart.js for advanced visualizations

---

**Document Version:** 1.0
**Last Updated:** 2025-11-08
**Author:** AI Assistant & User Collaboration
