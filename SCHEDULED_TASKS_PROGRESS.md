# Scheduled Tasks Implementation - Progress Tracker

**Project:** Workspace Scheduled Tasks
**Started:** 2025-11-01
**Target Completion:** TBD
**Current Phase:** Planning Complete ✅
**Overall Progress:** 0% (Planning: 100%, Implementation: 0%)

---

## Quick Status

| Phase | Status | Progress | Started | Completed |
|-------|--------|----------|---------|-----------|
| Planning | ✅ Complete | 100% | 2025-11-01 | 2025-11-01 |
| Phase 1: Core Infrastructure | ⏳ Not Started | 0% | - | - |
| Phase 2: API Endpoints | ⏳ Not Started | 0% | - | - |
| Phase 3: Frontend UI | ⏳ Not Started | 0% | - | - |
| Phase 4: Advanced Features | ⏳ Not Started | 0% | - | - |

**Legend:**
- ✅ Complete
- 🚧 In Progress
- ⏳ Not Started
- ❌ Blocked
- ⏸️ Paused

---

## Phase 1: Core Scheduling Infrastructure

**Target:** 3-4 hours
**Status:** ⏳ Not Started
**Progress:** 0/13 tasks complete

### Tasks

#### 1.1 Data Structures (workspace.go)

- [ ] Add `ScheduledTask` struct with all fields
- [ ] Add `ScheduleConfig` struct
- [ ] Add `ScheduleType` constants (Once, Interval, Daily, Weekly, Cron)
- [ ] Add `ScheduledTasks []ScheduledTask` field to Workspace struct
- [ ] Implement `AddScheduledTask(task ScheduledTask) error`
- [ ] Implement `UpdateScheduledTask(task ScheduledTask) error`
- [ ] Implement `GetScheduledTask(id string) (*ScheduledTask, error)`
- [ ] Implement `DeleteScheduledTask(id string) error`
- [ ] Implement `GetEnabledScheduledTasks() []ScheduledTask`

**Files to Modify:**
- `internal/workspace/workspace.go`

**Progress:** 0/9 tasks

---

#### 1.2 Task Scheduler Component (scheduler.go)

- [ ] Create `internal/workspace/scheduler.go`
- [ ] Implement `TaskScheduler` struct
- [ ] Implement `NewTaskScheduler()` constructor
- [ ] Implement `Start()` method (begin polling)
- [ ] Implement `Stop()` method (graceful shutdown)
- [ ] Implement `checkScheduledTasks()` (main loop logic)
- [ ] Implement `executeScheduledTask()` (create Task from ScheduledTask)
- [ ] Implement `calculateNextRun()` for "once" type
- [ ] Implement `calculateNextRun()` for "interval" type
- [ ] Implement `calculateNextRun()` for "daily" type
- [ ] Add EndDate checking logic
- [ ] Add MaxRuns enforcement logic
- [ ] Write unit tests for `calculateNextRun()`

**Files to Create:**
- `internal/workspace/scheduler.go`
- `internal/workspace/scheduler_test.go`

**Progress:** 0/13 tasks

---

#### 1.3 Server Integration

- [ ] Add TaskScheduler field to Server struct
- [ ] Initialize TaskScheduler in `NewServer()`
- [ ] Call `scheduler.SetEventBus()`
- [ ] Call `scheduler.Start()` in `Run()`
- [ ] Call `scheduler.Stop()` in `Shutdown()`

**Files to Modify:**
- `internal/server/server.go`

**Progress:** 0/5 tasks

---

#### 1.4 Testing and Validation

- [ ] Manual test: Create scheduled task with "once" type
- [ ] Verify task created at correct time
- [ ] Manual test: Create scheduled task with "interval" type
- [ ] Verify multiple task executions
- [ ] Manual test: Create scheduled task with "daily" type
- [ ] Verify NextRun calculation
- [ ] Verify MaxRuns enforcement
- [ ] Verify EndDate enforcement
- [ ] Code review and cleanup

**Progress:** 0/9 tasks

---

### Phase 1 Deliverables

- [ ] ScheduledTask data structures complete
- [ ] TaskScheduler polling and executing
- [ ] Next run time calculation working
- [ ] Integration with server lifecycle
- [ ] Unit tests passing
- [ ] Manual integration tests successful

**Sign-off:** _____________
**Date:** _____________

---

## Phase 2: API Endpoints

**Target:** 2 hours
**Status:** ⏳ Not Started
**Progress:** 0/8 endpoints complete

### Endpoints to Implement

- [ ] POST `/api/orchestration/scheduled-tasks` - Create scheduled task
  - [ ] Request validation
  - [ ] Response with created task
  - [ ] Error handling

- [ ] GET `/api/orchestration/scheduled-tasks?workspace_id=` - List scheduled tasks
  - [ ] Query parameter parsing
  - [ ] Return array of scheduled tasks
  - [ ] Handle empty results

- [ ] GET `/api/orchestration/scheduled-tasks/:id` - Get single scheduled task
  - [ ] ID extraction from URL
  - [ ] 404 handling
  - [ ] Return full task details

- [ ] PUT `/api/orchestration/scheduled-tasks/:id` - Update scheduled task
  - [ ] Partial update support
  - [ ] Recalculate NextRun if schedule changed
  - [ ] Validation

- [ ] DELETE `/api/orchestration/scheduled-tasks/:id` - Delete scheduled task
  - [ ] Confirmation
  - [ ] Remove from workspace
  - [ ] Save workspace

- [ ] POST `/api/orchestration/scheduled-tasks/:id/enable` - Enable scheduled task
  - [ ] Set enabled=true
  - [ ] Calculate NextRun
  - [ ] Save workspace

- [ ] POST `/api/orchestration/scheduled-tasks/:id/disable` - Disable scheduled task
  - [ ] Set enabled=false
  - [ ] Clear NextRun (optional)
  - [ ] Save workspace

- [ ] POST `/api/orchestration/scheduled-tasks/:id/trigger` - Manually trigger
  - [ ] Create immediate task
  - [ ] Don't affect schedule
  - [ ] Return task ID

### Handler Implementation

**Files to Modify:**
- `internal/orchestrationhttp/handlers.go`

**Tasks:**
- [ ] Implement `ScheduledTasksHandler(w, r)` (list/create router)
- [ ] Implement `ScheduledTaskHandler(w, r)` (get/update/delete router)
- [ ] Implement `handleCreateScheduledTask()`
- [ ] Implement `handleListScheduledTasks()`
- [ ] Implement `handleGetScheduledTask()`
- [ ] Implement `handleUpdateScheduledTask()`
- [ ] Implement `handleDeleteScheduledTask()`
- [ ] Implement `handleEnableScheduledTask()`
- [ ] Implement `handleDisableScheduledTask()`
- [ ] Implement `handleTriggerScheduledTask()`

**Progress:** 0/10 tasks

### Route Registration

**Files to Modify:**
- `internal/server/server.go`

**Tasks:**
- [ ] Register `/api/orchestration/scheduled-tasks` route
- [ ] Register `/api/orchestration/scheduled-tasks/` route (with trailing slash for ID-based endpoints)

**Progress:** 0/2 tasks

### Testing

- [ ] Test CREATE endpoint with curl
- [ ] Test LIST endpoint with curl
- [ ] Test GET endpoint with curl
- [ ] Test UPDATE endpoint with curl
- [ ] Test DELETE endpoint with curl
- [ ] Test ENABLE endpoint with curl
- [ ] Test DISABLE endpoint with curl
- [ ] Test TRIGGER endpoint with curl
- [ ] Test error cases (404, validation errors, etc.)
- [ ] Test with invalid workspace_id
- [ ] Test with malformed JSON

**Progress:** 0/11 tasks

---

### Phase 2 Deliverables

- [ ] All CRUD endpoints working
- [ ] Enable/disable functionality working
- [ ] Manual trigger working
- [ ] Input validation implemented
- [ ] Error handling comprehensive
- [ ] API tests documented
- [ ] Postman/curl examples created

**Sign-off:** _____________
**Date:** _____________

---

## Phase 3: Frontend UI

**Target:** 3-4 hours
**Status:** ⏳ Not Started
**Progress:** 0/15 tasks complete

### 3.1 Scheduled Tasks Module (JavaScript)

**Files to Create:**
- `internal/web/static/js/modules/scheduled-tasks.js`

**Tasks:**
- [ ] Create `ScheduledTasksManager` class
- [ ] Implement `init()` method
- [ ] Implement `loadScheduledTasks()` from API
- [ ] Implement `render()` main UI
- [ ] Implement `renderScheduledTasksTable()`
- [ ] Implement `showCreateForm()` modal
- [ ] Implement `showEditForm(id)` modal
- [ ] Implement `createScheduledTask(data)` API call
- [ ] Implement `updateScheduledTask(id, data)` API call
- [ ] Implement `deleteScheduledTask(id)` API call
- [ ] Implement `toggleEnabled(id, enabled)` API call
- [ ] Implement `triggerNow(id)` API call
- [ ] Implement schedule type selector logic
- [ ] Implement dynamic form fields based on schedule type
- [ ] Add confirmation dialogs for delete/disable

**Progress:** 0/15 tasks

---

### 3.2 Workspace Dashboard Integration

**Files to Modify:**
- `internal/web/static/js/modules/workspace-dashboard.js`

**Tasks:**
- [ ] Add "Scheduled Tasks" tab to nav tabs
- [ ] Add tab content container
- [ ] Initialize ScheduledTasksManager in `init()`
- [ ] Add event listener for tab switching
- [ ] Refresh scheduled tasks when tab activated

**Progress:** 0/5 tasks

---

### 3.3 UI Components

**Create Form Components:**
- [ ] Name input field
- [ ] Description textarea
- [ ] From agent selector (dropdown)
- [ ] To agent selector (dropdown)
- [ ] Prompt/task textarea
- [ ] Priority selector
- [ ] Schedule type selector
- [ ] Dynamic schedule configuration fields:
  - [ ] Once: Date/time picker
  - [ ] Interval: Duration input
  - [ ] Daily: Time picker
  - [ ] Weekly: Day + time picker
- [ ] Advanced options (collapsible):
  - [ ] Max runs input
  - [ ] End date picker
- [ ] Form validation
- [ ] Submit button with loading state
- [ ] Cancel button

**Progress:** 0/15 tasks

**Table Components:**
- [ ] Table header (Name, Schedule, Next Run, Last Run, Status, Executions, Actions)
- [ ] Table rows with data binding
- [ ] Enable/disable toggle switch
- [ ] Edit button (opens edit modal)
- [ ] Delete button (with confirmation)
- [ ] Trigger Now button
- [ ] Empty state (no scheduled tasks)
- [ ] Loading state
- [ ] Status badges (enabled/disabled)
- [ ] Schedule formatting (human-readable)
- [ ] Timestamp formatting (relative or absolute)

**Progress:** 0/11 tasks

---

### 3.4 Styling and Polish

**Tasks:**
- [ ] Match existing UI design system
- [ ] Responsive layout (mobile-friendly)
- [ ] Hover effects on table rows
- [ ] Button states (hover, active, disabled)
- [ ] Modal animations
- [ ] Toast notifications for success/error
- [ ] Loading spinners
- [ ] Icon integration (SVG icons)
- [ ] Color coding (enabled=green, disabled=gray)

**Progress:** 0/9 tasks

---

### 3.5 Real-time Updates

**Tasks:**
- [ ] Subscribe to scheduled task events via SSE
- [ ] Update UI when scheduled task executes
- [ ] Update UI when scheduled task is enabled/disabled
- [ ] Update NextRun in real-time
- [ ] Refresh task list when changes occur
- [ ] Show notification when scheduled task triggers

**Progress:** 0/6 tasks

---

### 3.6 Testing and Validation

**Manual Tests:**
- [ ] Create scheduled task via UI
- [ ] Verify task appears in table
- [ ] Edit scheduled task
- [ ] Verify changes saved
- [ ] Delete scheduled task
- [ ] Verify task removed
- [ ] Toggle enable/disable
- [ ] Verify status changes
- [ ] Trigger task manually
- [ ] Verify task created immediately
- [ ] Test form validation (empty fields, invalid times)
- [ ] Test all schedule types
- [ ] Test responsive design on mobile
- [ ] Test with multiple scheduled tasks
- [ ] Test real-time updates

**Progress:** 0/15 tasks

---

### Phase 3 Deliverables

- [ ] Scheduled Tasks tab functional
- [ ] Create/edit form working
- [ ] Table view displaying scheduled tasks
- [ ] Enable/disable toggle working
- [ ] Manual trigger working
- [ ] Real-time updates via SSE
- [ ] Responsive design
- [ ] UI matches existing design system
- [ ] All manual tests passing

**Sign-off:** _____________
**Date:** _____________

---

## Phase 4: Advanced Features (Optional)

**Target:** 2-3 hours
**Status:** ⏳ Not Started
**Progress:** 0% (Optional phase)

### Features

- [ ] **4.1 Cron Expression Support**
  - [ ] Add `github.com/robfig/cron/v3` dependency
  - [ ] Implement cron parsing in backend
  - [ ] Add cron validation
  - [ ] Add cron input field in UI
  - [ ] Add cron expression helper/examples

- [ ] **4.2 Execution History**
  - [ ] Add ExecutionHistory to ScheduledTask
  - [ ] Store last 100 executions
  - [ ] Display history in expandable row
  - [ ] Show execution status, result, timestamp

- [ ] **4.3 Scheduled Task Templates**
  - [ ] Create predefined templates
  - [ ] Template API endpoint
  - [ ] Template gallery in UI
  - [ ] "Create from Template" button

- [ ] **4.4 Notifications**
  - [ ] Publish scheduled task events
  - [ ] Toast notifications on execution
  - [ ] Email notifications (optional)

- [ ] **4.5 Bulk Operations**
  - [ ] Multi-select checkboxes
  - [ ] Bulk enable/disable
  - [ ] Bulk delete
  - [ ] "Select All" checkbox

- [ ] **4.6 Export/Import**
  - [ ] Export scheduled tasks as JSON
  - [ ] Import scheduled tasks from JSON
  - [ ] Download/upload UI

**Progress:** 0/6 features

---

## Issues and Blockers

**Current Blockers:** None

### Issue Log

| ID | Issue | Severity | Status | Assigned | Date Reported |
|----|-------|----------|--------|----------|---------------|
| - | - | - | - | - | - |

---

## Testing Status

### Unit Tests

- [ ] workspace.go - ScheduledTask CRUD methods
- [ ] scheduler.go - calculateNextRun() for all types
- [ ] scheduler.go - MaxRuns enforcement
- [ ] scheduler.go - EndDate enforcement
- [ ] handlers.go - API endpoint validation

**Progress:** 0/5 test suites

### Integration Tests

- [ ] End-to-end: Create → Execute → Verify
- [ ] API: All endpoints with various inputs
- [ ] Scheduler: Multiple schedule types
- [ ] Edge cases: DST, leap years, timezones

**Progress:** 0/4 test scenarios

### Manual Testing

- [ ] Phase 1 validation complete
- [ ] Phase 2 validation complete
- [ ] Phase 3 validation complete
- [ ] Phase 4 validation complete

**Progress:** 0/4 phases

---

## Documentation Status

- [x] Implementation plan (SCHEDULED_TASKS_PLAN.md)
- [x] Progress tracker (this file)
- [ ] API documentation updated
- [ ] User guide created
- [ ] Example use cases documented
- [ ] Code comments added
- [ ] README updated

**Progress:** 2/7 documents

---

## Code Review Checklist

**Phase 1:**
- [ ] Code follows Go conventions
- [ ] Error handling comprehensive
- [ ] Logging appropriate
- [ ] No race conditions
- [ ] Memory leaks addressed
- [ ] Performance acceptable

**Phase 2:**
- [ ] API follows REST conventions
- [ ] Input validation thorough
- [ ] Error responses helpful
- [ ] Status codes correct
- [ ] Security considerations addressed

**Phase 3:**
- [ ] JavaScript follows conventions
- [ ] No console errors
- [ ] Responsive design verified
- [ ] Accessibility considered
- [ ] Browser compatibility tested

---

## Metrics and KPIs

### Phase 1 Success Metrics
- [ ] Scheduler polls every 1 minute (±5s)
- [ ] Tasks created within 1 minute of NextRun
- [ ] NextRun calculation 100% accurate
- [ ] No memory leaks after 24h runtime
- [ ] No race conditions detected

### Phase 2 Success Metrics
- [ ] All endpoints return correct status codes
- [ ] API response time < 100ms
- [ ] Validation catches 100% of invalid inputs
- [ ] Error messages are helpful

### Phase 3 Success Metrics
- [ ] UI is intuitive (user feedback)
- [ ] Form completion time < 2 minutes
- [ ] Zero JavaScript errors
- [ ] Mobile-friendly (tested on 3+ devices)

---

## Timeline and Milestones

| Milestone | Target Date | Actual Date | Status |
|-----------|-------------|-------------|--------|
| Planning Complete | 2025-11-01 | 2025-11-01 | ✅ |
| Phase 1 Start | TBD | - | ⏳ |
| Phase 1 Complete | TBD | - | ⏳ |
| Phase 2 Start | TBD | - | ⏳ |
| Phase 2 Complete | TBD | - | ⏳ |
| Phase 3 Start | TBD | - | ⏳ |
| Phase 3 Complete | TBD | - | ⏳ |
| Beta Release | TBD | - | ⏳ |
| Phase 4 (Optional) | TBD | - | ⏳ |
| Final Release | TBD | - | ⏳ |

---

## Notes and Decisions

### 2025-11-01
- ✅ Chose Option B (Scheduled + Recurring Tasks) over Option A (Simple Scheduled Tasks)
- ✅ Decided to start with 3 schedule types: once, interval, daily
- ✅ Defer cron expressions to Phase 4
- ✅ Default polling interval: 1 minute
- ✅ Scheduled tasks create regular Task instances (reuse TaskExecutor)
- ✅ NextRun stored in UTC, converted to local time in UI

---

## Team and Resources

**Developer:** TBD
**Reviewer:** TBD
**QA:** TBD
**Documentation:** TBD

---

## Links and References

- [Implementation Plan](SCHEDULED_TASKS_PLAN.md)
- [Workspace Improvement Plan](WORKSPACE_IMPROVEMENT_PLAN.md)
- [API Reference](API_REFERENCE.md)
- [Cron Expression Syntax](https://en.wikipedia.org/wiki/Cron)
- [Time Zones List](https://en.wikipedia.org/wiki/List_of_tz_database_time_zones)

---

**Last Updated:** 2025-11-01
**Next Review:** TBD
