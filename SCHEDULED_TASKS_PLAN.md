# Workspace Scheduled Tasks - Implementation Plan

## Overview

This document outlines the plan for adding scheduled and recurring task capabilities to Ori workspaces. This feature enables automated execution of prompts/tasks at specified times or intervals.

## Current Architecture Analysis

### What Already Exists ✅

1. **Task System** (`workspace.go:60-76`)
   - Tasks with status, priority, timeouts
   - Task lifecycle management
   - Task-to-agent assignment

2. **Task Executor** (`executor.go`)
   - Polls every 10 seconds for pending tasks
   - Executes tasks asynchronously
   - Respects concurrency limits (max 5 concurrent)
   - Publishes lifecycle events

3. **Event Bus**
   - Publishes task events (started, completed, failed)
   - Real-time updates via SSE

4. **Task API**
   - Create, retrieve, update tasks
   - REST endpoints in `orchestrationhttp/handlers.go`

### What's Missing ❌

- Recurring/scheduled tasks (cron-like functionality)
- Scheduled execution times (run at specific time vs "now")
- Recurrence patterns (daily, hourly, weekly, etc.)
- Next execution time tracking
- Execution history for scheduled tasks

## Proposed Solution: Option B - Scheduled + Recurring Tasks

### Core Concept

A **ScheduledTask** is a template that automatically creates **Task** instances at specified times. The existing TaskExecutor handles execution of these generated tasks.

```
ScheduledTask (template) → TaskScheduler (checks time) → Task (created) → TaskExecutor (executes)
```

---

## Data Structures

### 1. ScheduledTask

Add to `internal/workspace/workspace.go`:

```go
// ScheduledTask represents a recurring or one-time scheduled task template
type ScheduledTask struct {
    ID              string                 `json:"id"`
    WorkspaceID     string                 `json:"workspace_id"`
    Name            string                 `json:"name"`
    Description     string                 `json:"description"`
    From            string                 `json:"from"`           // Sender agent
    To              string                 `json:"to"`             // Recipient agent
    Prompt          string                 `json:"prompt"`         // Task description/prompt
    Priority        int                    `json:"priority"`
    Context         map[string]interface{} `json:"context"`

    // Scheduling configuration
    Schedule        ScheduleConfig         `json:"schedule"`
    NextRun         *time.Time             `json:"next_run"`
    LastRun         *time.Time             `json:"last_run"`
    Enabled         bool                   `json:"enabled"`

    // Execution tracking
    ExecutionCount  int                    `json:"execution_count"`
    FailureCount    int                    `json:"failure_count"`
    LastResult      string                 `json:"last_result,omitempty"`
    LastError       string                 `json:"last_error,omitempty"`

    // Metadata
    CreatedAt       time.Time              `json:"created_at"`
    UpdatedAt       time.Time              `json:"updated_at"`
}
```

### 2. ScheduleConfig

```go
type ScheduleConfig struct {
    Type       ScheduleType   `json:"type"`         // "once", "interval", "daily", "weekly", "cron"

    // For "once" type
    ExecuteAt  *time.Time     `json:"execute_at,omitempty"`

    // For "interval" type
    Interval   time.Duration  `json:"interval,omitempty"`     // e.g., 5m, 1h, 24h

    // For "cron" type
    CronExpr   string         `json:"cron_expr,omitempty"`   // e.g., "0 9 * * *"

    // For "daily" type
    TimeOfDay  string         `json:"time_of_day,omitempty"` // e.g., "09:00", "14:30"

    // For "weekly" type
    DayOfWeek  int            `json:"day_of_week,omitempty"` // 0=Sunday, 1=Monday, ..., 6=Saturday

    // Limits
    MaxRuns    int            `json:"max_runs,omitempty"`    // 0 = infinite
    EndDate    *time.Time     `json:"end_date,omitempty"`    // nil = no end date
}

type ScheduleType string

const (
    ScheduleOnce     ScheduleType = "once"      // Execute once at specific time
    ScheduleInterval ScheduleType = "interval"  // Every X minutes/hours/days
    ScheduleDaily    ScheduleType = "daily"     // Every day at specific time
    ScheduleWeekly   ScheduleType = "weekly"    // Every week on specific day/time
    ScheduleCron     ScheduleType = "cron"      // Cron expression (advanced)
)
```

### 3. Update Workspace Struct

Add to `Workspace` struct in `workspace.go`:

```go
type Workspace struct {
    // ... existing fields ...
    ScheduledTasks  []ScheduledTask        `json:"scheduled_tasks,omitempty"`
}
```

---

## Implementation Phases

## Phase 1: Core Scheduling Infrastructure ⏱️ 3-4 hours

### Objectives
- Add data structures
- Implement scheduler component
- Calculate next run times
- Generate Task instances from ScheduledTask templates

### Tasks

#### 1.1 Add Data Structures to `workspace.go`
- [ ] Add `ScheduledTask` struct
- [ ] Add `ScheduleConfig` struct
- [ ] Add `ScheduleType` constants
- [ ] Add `ScheduledTasks []ScheduledTask` to `Workspace` struct
- [ ] Add methods:
  - `AddScheduledTask(task ScheduledTask) error`
  - `UpdateScheduledTask(task ScheduledTask) error`
  - `GetScheduledTask(id string) (*ScheduledTask, error)`
  - `DeleteScheduledTask(id string) error`
  - `GetEnabledScheduledTasks() []ScheduledTask`

#### 1.2 Create `scheduler.go`

New file: `internal/workspace/scheduler.go`

**Key Components:**

```go
type TaskScheduler struct {
    workspaceStore  Store
    taskExecutor    *TaskExecutor
    pollInterval    time.Duration  // Check every 1 minute
    eventBus        *EventBus

    mu              sync.RWMutex
    stopChan        chan struct{}
    wg              sync.WaitGroup
}

// Main methods:
// - Start() - Begin polling loop
// - Stop() - Graceful shutdown
// - checkScheduledTasks() - Main loop logic
// - executeScheduledTask() - Create Task from ScheduledTask
// - calculateNextRun() - Calculate next execution time
```

**Algorithm:**

```
Every 1 minute:
  1. Get all workspaces
  2. For each workspace:
     a. Get enabled scheduled tasks
     b. For each scheduled task:
        - If NextRun is nil or NextRun <= now:
          * Create a Task instance
          * Add task to workspace
          * Update LastRun
          * Calculate and set NextRun
          * Increment ExecutionCount
          * Check if should disable (MaxRuns reached, EndDate passed)
```

**Next Run Calculation Logic:**

```go
func calculateNextRun(schedule ScheduleConfig, lastRun time.Time) *time.Time {
    switch schedule.Type {
    case ScheduleOnce:
        return nil  // One-time execution, no next run

    case ScheduleInterval:
        next := lastRun.Add(schedule.Interval)
        return &next

    case ScheduleDaily:
        // Parse TimeOfDay (e.g., "09:00")
        // Start from lastRun, find next occurrence of that time

    case ScheduleWeekly:
        // Find next occurrence of DayOfWeek at TimeOfDay

    case ScheduleCron:
        // Use cron library (github.com/robfig/cron/v3)
        // parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
        // schedule, _ := parser.Parse(schedule.CronExpr)
        // next := schedule.Next(lastRun)
    }

    // Check if next run exceeds EndDate or MaxRuns
    if schedule.EndDate != nil && next.After(*schedule.EndDate) {
        return nil
    }

    return &next
}
```

#### 1.3 Integrate Scheduler in `server.go`

- [ ] Initialize TaskScheduler
- [ ] Start scheduler after TaskExecutor
- [ ] Stop scheduler on shutdown

```go
// In NewServer():
scheduler := workspace.NewTaskScheduler(
    workspaceStore,
    taskExecutor,
    workspace.SchedulerConfig{
        PollInterval: 1 * time.Minute,
    },
)
scheduler.SetEventBus(eventBus)
s.scheduler = scheduler

// In Run():
s.scheduler.Start()

// In Shutdown():
s.scheduler.Stop()
```

#### 1.4 Initial Schedule Types

Start with these three types:

1. **Once** - Simplest, execute at specific time
2. **Interval** - Most flexible, every X duration
3. **Daily** - Most useful, every day at specific time

Defer for later:
- Weekly (can use interval with 7 days)
- Cron (advanced, requires library)

### Deliverables
- ✅ ScheduledTask data structures in `workspace.go`
- ✅ TaskScheduler implementation in `scheduler.go`
- ✅ Integration in `server.go`
- ✅ Unit tests for next run calculation
- ✅ Working scheduler polling for scheduled tasks

---

## Phase 2: API Endpoints ⏱️ 2 hours

### Objectives
- Create REST API for managing scheduled tasks
- CRUD operations
- Enable/disable functionality
- Manual trigger capability

### API Endpoints

#### 2.1 Create Scheduled Task

```
POST /api/orchestration/scheduled-tasks

Request Body:
{
  "workspace_id": "uuid",
  "name": "Daily Standup Report",
  "description": "Generate daily team status",
  "from": "manager-agent",
  "to": "report-agent",
  "prompt": "Generate a summary of yesterday's work",
  "priority": 0,
  "schedule": {
    "type": "daily",
    "time_of_day": "09:00"
  },
  "enabled": true
}

Response: 201 Created
{
  "success": true,
  "scheduled_task": { ... }
}
```

#### 2.2 List Scheduled Tasks

```
GET /api/orchestration/scheduled-tasks?workspace_id=uuid

Response: 200 OK
{
  "scheduled_tasks": [
    {
      "id": "uuid",
      "name": "Daily Standup Report",
      "enabled": true,
      "next_run": "2025-11-02T09:00:00Z",
      "last_run": "2025-11-01T09:00:00Z",
      "execution_count": 42,
      ...
    }
  ]
}
```

#### 2.3 Get Scheduled Task

```
GET /api/orchestration/scheduled-tasks/:id

Response: 200 OK
{
  "scheduled_task": { ... }
}
```

#### 2.4 Update Scheduled Task

```
PUT /api/orchestration/scheduled-tasks/:id

Request Body:
{
  "name": "Updated name",
  "schedule": {
    "type": "interval",
    "interval": "2h"
  }
}

Response: 200 OK
{
  "success": true,
  "scheduled_task": { ... }
}
```

#### 2.5 Delete Scheduled Task

```
DELETE /api/orchestration/scheduled-tasks/:id

Response: 200 OK
{
  "success": true
}
```

#### 2.6 Enable/Disable Scheduled Task

```
POST /api/orchestration/scheduled-tasks/:id/enable
POST /api/orchestration/scheduled-tasks/:id/disable

Response: 200 OK
{
  "success": true,
  "enabled": true
}
```

#### 2.7 Manually Trigger Scheduled Task

```
POST /api/orchestration/scheduled-tasks/:id/trigger

Response: 200 OK
{
  "success": true,
  "task_id": "uuid"  // ID of created task
}
```

### Implementation

Add to `internal/orchestrationhttp/handlers.go`:

```go
func (h *Handler) ScheduledTasksHandler(w http.ResponseWriter, r *http.Request) {
    switch r.Method {
    case http.MethodGet:
        h.handleListScheduledTasks(w, r)
    case http.MethodPost:
        h.handleCreateScheduledTask(w, r)
    default:
        http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
    }
}

func (h *Handler) ScheduledTaskHandler(w http.ResponseWriter, r *http.Request) {
    // Extract ID from URL
    parts := strings.Split(r.URL.Path, "/")
    id := parts[len(parts)-1]

    switch r.Method {
    case http.MethodGet:
        h.handleGetScheduledTask(w, r, id)
    case http.MethodPut:
        h.handleUpdateScheduledTask(w, r, id)
    case http.MethodDelete:
        h.handleDeleteScheduledTask(w, r, id)
    default:
        http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
    }
}

// Additional handlers:
// - handleEnableScheduledTask
// - handleDisableScheduledTask
// - handleTriggerScheduledTask
```

Register routes in `internal/server/server.go`:

```go
mux.HandleFunc("/api/orchestration/scheduled-tasks", s.orchestrationHandler.ScheduledTasksHandler)
mux.HandleFunc("/api/orchestration/scheduled-tasks/", s.orchestrationHandler.ScheduledTaskHandler)
```

### Deliverables
- ✅ All CRUD endpoints implemented
- ✅ Enable/disable functionality
- ✅ Manual trigger capability
- ✅ Input validation
- ✅ Error handling
- ✅ API tests (curl or Postman)

---

## Phase 3: Frontend UI ⏱️ 3-4 hours

### Objectives
- Add "Scheduled Tasks" tab to workspace dashboard
- Create/edit scheduled tasks UI
- View execution history
- Enable/disable toggle
- Manual trigger button

### Components

#### 3.1 Scheduled Tasks Module

New file: `internal/web/static/js/modules/scheduled-tasks.js`

```javascript
class ScheduledTasksManager {
    constructor(workspaceId, containerId) {
        this.workspaceId = workspaceId;
        this.container = document.getElementById(containerId);
        this.scheduledTasks = [];
    }

    async init() {
        await this.loadScheduledTasks();
        this.render();
    }

    async loadScheduledTasks() {
        const response = await fetch(`/api/orchestration/scheduled-tasks?workspace_id=${this.workspaceId}`);
        const data = await response.json();
        this.scheduledTasks = data.scheduled_tasks || [];
    }

    render() {
        // Main UI with table of scheduled tasks
    }

    renderScheduledTasksTable() {
        // Table columns:
        // - Name
        // - Schedule (formatted)
        // - Next Run
        // - Last Run
        // - Status (enabled/disabled)
        // - Executions (count)
        // - Actions (enable/disable, edit, delete, trigger)
    }

    showCreateForm() {
        // Modal with schedule configuration
    }

    async createScheduledTask(data) {
        // POST to API
    }

    async toggleEnabled(id, enabled) {
        // POST to enable/disable endpoint
    }

    async triggerNow(id) {
        // POST to trigger endpoint
    }
}
```

#### 3.2 UI Design

**Scheduled Tasks Tab Layout:**

```
┌─────────────────────────────────────────────────────────────┐
│ Scheduled Tasks                          [+ Create Schedule] │
├─────────────────────────────────────────────────────────────┤
│                                                               │
│ ┌───────────────────────────────────────────────────────┐   │
│ │ Name            │ Schedule  │ Next Run │ Last Run │ …│   │
│ ├───────────────────────────────────────────────────────┤   │
│ │ Daily Report    │ Daily 9AM │ Nov 2    │ Nov 1    │⚙️│   │
│ │ Hourly Check    │ Every 1h  │ 3:00 PM  │ 2:00 PM  │⚙️│   │
│ │ Weekly Cleanup  │ Mon 8AM   │ Nov 4    │ Oct 28   │⚙️│   │
│ └───────────────────────────────────────────────────────┘   │
│                                                               │
│ No scheduled tasks yet. Create one to automate workflows.    │
└─────────────────────────────────────────────────────────────┘
```

**Create/Edit Form:**

```
┌─────────────────────────────────────┐
│ Create Scheduled Task               │
├─────────────────────────────────────┤
│ Name: [Daily Status Report_____]    │
│                                     │
│ Description:                        │
│ [Generate team status summary___]   │
│                                     │
│ From Agent: [manager ▼]            │
│ To Agent:   [reporter ▼]           │
│                                     │
│ Prompt/Task:                        │
│ [Analyze yesterday's work and___]   │
│ [create a summary report_______]    │
│                                     │
│ Schedule Type: [Daily ▼]            │
│   ┌─────────────────────────────┐  │
│   │ Time: [09:00]               │  │
│   └─────────────────────────────┘  │
│                                     │
│ Priority: [Normal ▼]                │
│                                     │
│ Advanced:                           │
│ [ ] Max Runs: [____] (0=infinite)  │
│ [ ] End Date: [____]               │
│                                     │
│ [Cancel]  [Create Schedule]         │
└─────────────────────────────────────┘
```

**Schedule Type Options:**

1. **Once** - Date & time picker
2. **Interval** - Duration input (e.g., "30 minutes", "2 hours", "1 day")
3. **Daily** - Time picker (e.g., "09:00")
4. **Weekly** - Day selector + time picker
5. **Cron** - Text input with helper (defer to Phase 4)

#### 3.3 Integration with Workspace Dashboard

Modify `internal/web/static/js/modules/workspace-dashboard.js`:

```javascript
renderWorkspaceDetails() {
    // Add new tab
    <ul class="nav nav-tabs mb-3" role="tablist">
        <li class="nav-item">
            <a class="nav-link active" data-bs-toggle="tab" href="#overview-tab">Overview</a>
        </li>
        <li class="nav-item">
            <a class="nav-link" data-bs-toggle="tab" href="#tasks-tab">Tasks</a>
        </li>
        <li class="nav-item">
            <a class="nav-link" data-bs-toggle="tab" href="#scheduled-tab">Scheduled Tasks</a>
        </li>
        <li class="nav-item">
            <a class="nav-link" data-bs-toggle="tab" href="#messages-tab">Messages</a>
        </li>
    </ul>

    // Tab content
    <div class="tab-content">
        ...
        <div class="tab-pane fade" id="scheduled-tab">
            <div id="scheduled-tasks-container"></div>
        </div>
    </div>
}

async init() {
    // Initialize scheduled tasks manager
    this.scheduledTasksManager = new ScheduledTasksManager(
        this.workspaceId,
        'scheduled-tasks-container'
    );
    await this.scheduledTasksManager.init();
}
```

### Deliverables
- ✅ ScheduledTasksManager JavaScript class
- ✅ Scheduled Tasks tab in workspace dashboard
- ✅ Create/edit form with schedule configuration
- ✅ Table view with enable/disable toggles
- ✅ Manual trigger button
- ✅ Real-time updates (via SSE when tasks execute)
- ✅ Responsive design with modern UI

---

## Phase 4: Advanced Features (Optional) ⏱️ 2-3 hours

### 4.1 Cron Expression Support

**Backend:**
- Add dependency: `github.com/robfig/cron/v3`
- Implement cron parsing in `calculateNextRun()`
- Validate cron expressions

**Frontend:**
- Cron expression text input
- Validation feedback
- Optional: Visual cron builder (using library like `react-cron-generator` adapted to vanilla JS)

### 4.2 Execution History

**Backend:**
- Add `ExecutionHistory []TaskExecution` to ScheduledTask
- Limit history to last 100 executions
- Store task_id, timestamp, status, result/error

**Frontend:**
- Expandable row in table
- Show last 10 executions
- Status badges (success/failed)
- Click to view full result

### 4.3 Scheduled Task Templates

**Backend:**
- Predefined templates: "Daily Report", "Hourly Health Check", "Weekly Cleanup"
- Template API endpoint

**Frontend:**
- "Create from Template" button
- Template gallery with descriptions
- Pre-fill form from template

### 4.4 Notifications

**Backend:**
- Publish scheduled task events to EventBus
- Send notifications on:
  - Task execution started
  - Task execution completed
  - Task execution failed
  - Scheduled task disabled (MaxRuns or EndDate reached)

**Frontend:**
- Toast notifications
- Real-time updates via SSE

### 4.5 Bulk Operations

**Frontend:**
- Checkbox selection in table
- Bulk actions: Enable, Disable, Delete
- "Enable All" / "Disable All" buttons

### 4.6 Export/Import Schedules

**Backend:**
- Export scheduled tasks as JSON
- Import scheduled tasks from JSON

**Frontend:**
- "Export Schedules" button
- "Import Schedules" file upload

---

## Example Use Cases

### 1. Daily Status Report

```json
{
  "name": "Daily Standup Report",
  "description": "Generate team status summary every morning",
  "from": "orchestrator",
  "to": "reporting-agent",
  "prompt": "Generate a summary of yesterday's completed tasks, ongoing work, and blockers for the team standup",
  "priority": 1,
  "schedule": {
    "type": "daily",
    "time_of_day": "09:00"
  },
  "enabled": true
}
```

**Execution:**
- Every day at 9:00 AM
- Creates a Task assigned to reporting-agent
- Task appears in workspace task list
- TaskExecutor picks it up and executes

### 2. Hourly System Health Check

```json
{
  "name": "System Health Monitor",
  "description": "Check all services every hour",
  "from": "orchestrator",
  "to": "monitoring-agent",
  "prompt": "Check the health of all registered services and alert if any are down or degraded",
  "priority": 2,
  "schedule": {
    "type": "interval",
    "interval": "1h"
  },
  "enabled": true
}
```

**Execution:**
- Every 1 hour
- High priority (executes before normal tasks)
- Continuous monitoring

### 3. Weekly Code Review Reminder

```json
{
  "name": "Weekly PR Review",
  "description": "Review open pull requests every Monday",
  "from": "lead-agent",
  "to": "code-reviewer-agent",
  "prompt": "Review all open PRs, provide feedback, and suggest merges or changes",
  "priority": 0,
  "schedule": {
    "type": "weekly",
    "day_of_week": 1,
    "time_of_day": "14:00"
  },
  "enabled": true
}
```

**Execution:**
- Every Monday at 2:00 PM
- Regular priority
- Weekly cadence

### 4. One-Time Deployment

```json
{
  "name": "Weekend Deployment",
  "description": "Deploy new version this Saturday night",
  "from": "orchestrator",
  "to": "devops-agent",
  "prompt": "Deploy version 2.0 to production environment",
  "priority": 2,
  "schedule": {
    "type": "once",
    "execute_at": "2025-11-02T02:00:00Z"
  },
  "enabled": true
}
```

**Execution:**
- One-time execution on Nov 2, 2025 at 2:00 AM
- Automatically disabled after execution
- High priority

---

## Technical Considerations

### 1. Time Zone Handling

**Approach:**
- Store all times in UTC in database
- Add optional `Timezone` field to Workspace
- Convert display times to user's local timezone in UI
- When parsing "09:00" for daily schedule, interpret in workspace timezone

**Implementation:**
```go
// In ScheduleConfig
Timezone string `json:"timezone,omitempty"` // e.g., "America/New_York"

// When calculating next run for daily schedule:
loc, _ := time.LoadLocation(schedule.Timezone)
next := time.Date(now.Year(), now.Month(), now.Day()+1, hour, minute, 0, 0, loc)
```

### 2. Missed Executions

**Problem:** If scheduler is down, what happens to missed scheduled tasks?

**Solutions:**

**Option A: Catch Up**
- On startup, check if NextRun < now
- Execute immediately
- Pros: Ensures task runs, no data loss
- Cons: May execute many tasks at once

**Option B: Skip and Reschedule**
- On startup, calculate new NextRun from current time
- Skip missed execution
- Pros: Prevents backlog
- Cons: Loses scheduled execution

**Recommendation:**
- Add `MissedExecutionPolicy` field to ScheduleConfig
- Options: "catch_up", "skip"
- Default: "skip" for recurring, "catch_up" for once

### 3. Concurrency and Task Executor

**Integration:**
- Scheduled tasks create regular Task instances
- TaskExecutor treats them identically to manual tasks
- Respects `maxConcurrent` limit
- If executor is full, tasks queue as "pending"

**Considerations:**
- Scheduled tasks compete with manual tasks for executor slots
- High priority scheduled tasks execute before low priority manual tasks
- May want separate queue or reserved slots for scheduled tasks (future enhancement)

### 4. Persistence and Data Model

**Storage:**
- ScheduledTasks stored in workspace JSON file
- `NextRun` calculated and persisted
- On workspace load, validate NextRun (recalculate if needed)

**Initialization:**
```go
// When loading workspace from disk
for i := range ws.ScheduledTasks {
    st := &ws.ScheduledTasks[i]
    if st.NextRun == nil && st.Enabled {
        // Calculate initial NextRun
        st.NextRun = calculateNextRun(st.Schedule, time.Now())
    }
}
```

### 5. Error Handling and Retry

**Failure Scenarios:**
1. Task creation fails
2. Task execution fails
3. Workspace save fails

**Handling:**
```go
// In scheduler
if err := createTaskFromSchedule(st); err != nil {
    st.FailureCount++
    st.LastError = err.Error()

    // Disable after N consecutive failures?
    if st.FailureCount >= 5 {
        st.Enabled = false
        log.Printf("⚠️ Disabled scheduled task %s after %d failures", st.ID, st.FailureCount)
    }

    // Still calculate NextRun (don't skip schedule)
    st.NextRun = calculateNextRun(st.Schedule, time.Now())
}
```

**Configuration:**
- Add `MaxConsecutiveFailures` to ScheduleConfig
- Add `RetryPolicy` (immediate, exponential backoff, etc.)

### 6. Performance Considerations

**Polling Frequency:**
- 1-minute polling is reasonable for most use cases
- For sub-minute scheduling, reduce pollInterval
- Trade-off: CPU usage vs scheduling precision

**Optimization:**
- Index workspaces by next scheduled task time
- Skip workspaces with no enabled scheduled tasks
- Batch database operations

**Scaling:**
- Current design: Single scheduler instance
- Future: Distributed scheduling with leader election
- Use locking to prevent duplicate execution

---

## Testing Strategy

### Unit Tests

**workspace.go:**
- Test AddScheduledTask validation
- Test UpdateScheduledTask
- Test GetScheduledTask
- Test DeleteScheduledTask

**scheduler.go:**
- Test calculateNextRun for each schedule type
- Test edge cases (DST transitions, leap years, etc.)
- Test MaxRuns enforcement
- Test EndDate enforcement
- Test enable/disable logic

### Integration Tests

**Scheduler Integration:**
- Create scheduled task with interval
- Verify Task created at correct time
- Verify NextRun updated
- Verify ExecutionCount incremented

**API Integration:**
- Test CRUD endpoints
- Test enable/disable
- Test manual trigger
- Test validation and error handling

### End-to-End Tests

**Scenarios:**
1. Create daily scheduled task → Wait for execution → Verify task created
2. Create interval task → Verify multiple executions
3. Disable scheduled task → Verify no new tasks created
4. Delete scheduled task → Verify removed from workspace
5. Manual trigger → Verify immediate task creation

---

## Migration and Deployment

### Database Migration

**Step 1: Add ScheduledTasks field to Workspace**
- Existing workspaces: `ScheduledTasks` defaults to empty array `[]`
- No data migration needed
- Backward compatible

**Step 2: Update Workspace JSON Schema**
- Version workspace format: Add `schema_version` field
- V1: Original format
- V2: Includes scheduled_tasks

### Deployment Steps

1. **Deploy Backend**
   - Build with new scheduler code
   - Scheduler starts automatically
   - No existing workspaces affected

2. **Deploy Frontend**
   - Add new "Scheduled Tasks" tab
   - Feature discovery: Banner or tooltip

3. **Documentation**
   - Update API docs with new endpoints
   - Add user guide for scheduled tasks
   - Add examples and use cases

### Rollback Plan

If issues arise:
1. Disable scheduler: Set `ENABLE_SCHEDULER=false` env var
2. Scheduled tasks remain in workspace JSON
3. Can re-enable after fix

---

## Success Metrics

### Phase 1
- ✅ Scheduler polls every 1 minute
- ✅ Tasks created at correct times (±1 minute accuracy)
- ✅ NextRun calculated correctly for all schedule types

### Phase 2
- ✅ All API endpoints functional
- ✅ CRUD operations work correctly
- ✅ Validation prevents invalid schedules

### Phase 3
- ✅ UI allows creating scheduled tasks
- ✅ Scheduled tasks visible in dashboard
- ✅ Enable/disable works via UI
- ✅ Manual trigger creates task immediately

### Overall Success
- ✅ Users can create recurring tasks without manual intervention
- ✅ Scheduled tasks execute reliably
- ✅ UI is intuitive and easy to use
- ✅ System remains stable under load

---

## Future Enhancements

### V2 Features
1. **Conditional Execution**
   - Only run if condition met (e.g., "if workspace has pending tasks")
   - Query-based triggers

2. **Dependencies**
   - Schedule task B to run after task A completes
   - Chain scheduled tasks

3. **Parameterized Prompts**
   - Use variables in prompts: `{{date}}`, `{{workspace.name}}`
   - Dynamic prompt generation

4. **Schedule Calendar View**
   - Visual calendar showing upcoming scheduled executions
   - Drag-and-drop rescheduling

5. **Analytics**
   - Execution time trends
   - Failure rate monitoring
   - Performance metrics

6. **Multi-Agent Workflows**
   - Schedule complex multi-step workflows
   - Orchestrate multiple agents

---

## Resources and Dependencies

### Go Libraries
- `github.com/google/uuid` - UUID generation (already used)
- `github.com/robfig/cron/v3` - Cron expression parsing (Phase 4)

### Frontend Libraries
- Bootstrap (already used)
- No additional dependencies for Phase 1-3

### Documentation References
- Cron syntax: https://en.wikipedia.org/wiki/Cron
- Time zones: https://en.wikipedia.org/wiki/List_of_tz_database_time_zones

---

## Questions and Decisions

### Open Questions
1. Should scheduled tasks have separate concurrency limit from manual tasks?
2. Should we support sub-minute scheduling (e.g., every 30 seconds)?
3. Should execution history be unlimited or capped?
4. Should we expose scheduler metrics (via /metrics endpoint)?

### Decisions Made
- ✅ Start with 3 schedule types (once, interval, daily)
- ✅ Defer cron expressions to Phase 4
- ✅ Default to 1-minute polling frequency
- ✅ Store all times in UTC
- ✅ Skip missed executions by default (configurable later)
- ✅ Scheduled tasks create regular Task instances
- ✅ Use existing TaskExecutor (no separate executor)

---

## Contact and Ownership

**Owner:** [Your Name]
**Created:** 2025-11-01
**Last Updated:** 2025-11-01
**Status:** Planning

---

## Appendix

### A. Example Schedule Configurations

**Daily at 9 AM:**
```json
{
  "type": "daily",
  "time_of_day": "09:00"
}
```

**Every 30 minutes:**
```json
{
  "type": "interval",
  "interval": "30m"
}
```

**Every Monday at 2 PM:**
```json
{
  "type": "weekly",
  "day_of_week": 1,
  "time_of_day": "14:00"
}
```

**Once on specific date:**
```json
{
  "type": "once",
  "execute_at": "2025-12-25T10:00:00Z"
}
```

**Cron expression (every weekday at 8 AM):**
```json
{
  "type": "cron",
  "cron_expr": "0 8 * * 1-5"
}
```

### B. API Request/Response Examples

See Phase 2 section for detailed examples.

### C. UI Mockups

See Phase 3 section for ASCII mockups.
