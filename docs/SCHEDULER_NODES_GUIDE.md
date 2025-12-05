# Scheduler Nodes Guide

Scheduler nodes enable automatic execution of tasks at scheduled times on the workspace canvas. This guide covers creating, configuring, and managing scheduler nodes.

## Table of Contents

- [Overview](#overview)
- [Creating a Scheduler Node](#creating-a-scheduler-node)
- [Schedule Types](#schedule-types)
  - [Once (One-Time Execution)](#once-one-time-execution)
  - [Interval (Repeating)](#interval-repeating)
  - [Daily (Specific Time)](#daily-specific-time)
  - [Weekly (Specific Day/Time)](#weekly-specific-datetime)
  - [Cron Expressions](#cron-expressions)
  - [Relative Delay](#relative-delay)
- [Advanced Configuration](#advanced-configuration)
- [Monitoring Executions](#monitoring-executions)
- [Best Practices](#best-practices)

## Overview

Scheduler nodes allow you to automate task execution based on time-based triggers. Each scheduler node:

- **Creates tasks automatically** at scheduled times
- **Connects to task or agent nodes** to specify what should run
- **Tracks execution history** for monitoring and debugging
- **Supports multiple schedule types** for different automation needs
- **Provides visual status indicators** on the canvas

## Creating a Scheduler Node

### Via Canvas UI

1. **Open a workspace** in the canvas view
2. **Drag "Scheduler Node" from the left palette** onto the canvas
3. **Click the scheduler node** to open the configuration modal
4. **Fill in the required fields**:
   - **Name**: Descriptive name for the scheduled task
   - **Prompt**: The task description/instructions
   - **To**: Target agent or task node ID
   - **Schedule Type**: Choose from once, interval, daily, weekly, cron, relative_delay
5. **Configure schedule-specific settings** (see sections below)
6. **Click "Save"**

### Via API

```bash
curl -X POST http://localhost:8765/api/orchestration/workspaces/{workspace_id}/scheduler-nodes \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Daily Backup",
    "prompt": "Run database backup",
    "to": "backup-agent",
    "schedule_type": "daily",
    "time_of_day": "02:00",
    "enabled": true,
    "x": 400,
    "y": 300
  }'
```

## Schedule Types

### Once (One-Time Execution)

Execute a task **once at a specific date/time**.

**Use Cases:**
- One-time migrations
- Scheduled announcements
- Delayed task execution

**Configuration:**
```json
{
  "schedule_type": "once",
  "start_date": "2025-01-15T14:00:00Z"
}
```

**Example:**
```bash
# Run data migration on January 15, 2025 at 2:00 PM
{
  "name": "Database Migration",
  "prompt": "Run schema migration v2.0",
  "to": "admin-agent",
  "schedule_type": "once",
  "start_date": "2025-01-15T14:00:00Z"
}
```

**Behavior:**
- Executes once at `start_date`
- Automatically disables after execution
- `next_run` becomes null after completion

---

### Interval (Repeating)

Execute a task **repeatedly at fixed intervals**.

**Use Cases:**
- Periodic health checks
- Regular data syncs
- Continuous monitoring

**Configuration:**
```json
{
  "schedule_type": "interval",
  "interval_duration": "15m"  // Format: "30s", "5m", "2h", "1d"
}
```

**Examples:**

**Every 15 Minutes:**
```json
{
  "name": "Health Check",
  "prompt": "Check system health",
  "to": "monitor-agent",
  "schedule_type": "interval",
  "interval_duration": "15m"
}
```

**Every 6 Hours:**
```json
{
  "name": "Data Sync",
  "prompt": "Sync external data",
  "to": "sync-agent",
  "schedule_type": "interval",
  "interval_duration": "6h"
}
```

**Behavior:**
- Executes every `interval_duration`
- Continues indefinitely unless `end_date` or `max_runs` set
- `next_run` = `last_run` + `interval_duration`

---

### Daily (Specific Time)

Execute a task **every day at a specific time**.

**Use Cases:**
- Daily reports
- Nightly backups
- Daily data processing

**Configuration:**
```json
{
  "schedule_type": "daily",
  "time_of_day": "09:00"  // Format: "HH:MM" (24-hour)
}
```

**Examples:**

**Daily at 9:00 AM:**
```json
{
  "name": "Morning Report",
  "prompt": "Generate daily sales report",
  "to": "reporting-agent",
  "schedule_type": "daily",
  "time_of_day": "09:00"
}
```

**Daily at 2:00 AM (Backup):**
```json
{
  "name": "Nightly Backup",
  "prompt": "Run full database backup",
  "to": "backup-agent",
  "schedule_type": "daily",
  "time_of_day": "02:00"
}
```

**Behavior:**
- Executes every day at `time_of_day`
- Uses local timezone of server
- `next_run` = tomorrow at `time_of_day`

---

### Weekly (Specific Day/Time)

Execute a task **every week on a specific day and time**.

**Use Cases:**
- Weekly reports
- Weekly maintenance
- Weekly cleanups

**Configuration:**
```json
{
  "schedule_type": "weekly",
  "day_of_week": 1,      // 0=Sunday, 1=Monday, ..., 6=Saturday
  "time_of_day": "14:00"
}
```

**Examples:**

**Every Monday at 2:00 PM:**
```json
{
  "name": "Weekly Team Report",
  "prompt": "Generate weekly team summary",
  "to": "reporting-agent",
  "schedule_type": "weekly",
  "day_of_week": 1,
  "time_of_day": "14:00"
}
```

**Every Sunday at Midnight (Cleanup):**
```json
{
  "name": "Weekly Cleanup",
  "prompt": "Clean up old logs and temp files",
  "to": "maintenance-agent",
  "schedule_type": "weekly",
  "day_of_week": 0,
  "time_of_day": "00:00"
}
```

**Day of Week Values:**
- `0` = Sunday
- `1` = Monday
- `2` = Tuesday
- `3` = Wednesday
- `4` = Thursday
- `5` = Friday
- `6` = Saturday

**Behavior:**
- Executes every week on `day_of_week` at `time_of_day`
- `next_run` = next occurrence of target weekday

---

### Cron Expressions

Execute a task using **cron expressions** for maximum flexibility.

**Use Cases:**
- Complex schedules (e.g., "every weekday at 9 AM")
- Multiple times per day
- Specific dates/months

**Configuration:**
```json
{
  "schedule_type": "cron",
  "cron_expression": "0 9 * * 1-5"  // Weekdays at 9:00 AM
}
```

#### Cron Expression Format

```
* * * * *
│ │ │ │ │
│ │ │ │ └─ Day of week (0-6, 0=Sunday)
│ │ │ └─── Month (1-12)
│ │ └───── Day of month (1-31)
│ └─────── Hour (0-23)
└───────── Minute (0-59)
```

#### Common Examples

**Every Day at 9:00 AM:**
```json
{
  "cron_expression": "0 9 * * *"
}
```

**Every 15 Minutes:**
```json
{
  "cron_expression": "*/15 * * * *"
}
```

**Weekdays at 2:00 PM:**
```json
{
  "cron_expression": "0 14 * * 1-5"
}
```

**Every Sunday at Midnight:**
```json
{
  "cron_expression": "0 0 * * 0"
}
```

**First Day of Every Month at 8:00 AM:**
```json
{
  "cron_expression": "0 8 1 * *"
}
```

**Every 30 Minutes Between 9 AM and 5 PM:**
```json
{
  "cron_expression": "*/30 9-17 * * *"
}
```

#### Special Characters

- `*` - Every value (e.g., `* * * * *` = every minute)
- `*/N` - Every N units (e.g., `*/15 * * * *` = every 15 minutes)
- `N-M` - Range (e.g., `1-5` = Monday through Friday)
- `N,M` - List (e.g., `1,3,5` = Monday, Wednesday, Friday)

**Examples:**

**Business Hours Report (Every Hour 9 AM - 5 PM, Weekdays):**
```json
{
  "name": "Hourly Business Report",
  "prompt": "Generate hourly business metrics",
  "to": "reporting-agent",
  "schedule_type": "cron",
  "cron_expression": "0 9-17 * * 1-5"
}
```

**Behavior:**
- Executes based on cron expression
- Uses `robfig/cron` library for parsing
- `next_run` calculated by cron library

---

### Relative Delay

Execute a task **after a delay from when it was last triggered**.

**Use Cases:**
- "Run 5 minutes after previous task completes"
- Adaptive scheduling based on execution time
- One-time delayed execution

**Configuration:**
```json
{
  "schedule_type": "relative_delay",
  "delay_duration": "5m",     // Delay after last execution
  "trigger_once": false       // true = one-time only, false = repeating
}
```

**Examples:**

**Repeating: Run 10 Minutes After Each Execution:**
```json
{
  "name": "Adaptive Sync",
  "prompt": "Sync data based on load",
  "to": "sync-agent",
  "schedule_type": "relative_delay",
  "delay_duration": "10m",
  "trigger_once": false
}
```

**One-Time: Run Once After 30 Minutes:**
```json
{
  "name": "Delayed Notification",
  "prompt": "Send follow-up notification",
  "to": "notify-agent",
  "schedule_type": "relative_delay",
  "delay_duration": "30m",
  "trigger_once": true
}
```

**Behavior:**
- `next_run` = `last_run` + `delay_duration`
- If `trigger_once` = true, executes once then disables
- If `trigger_once` = false, repeats indefinitely

---

## Advanced Configuration

### Max Runs

Limit the **total number of executions**.

```json
{
  "schedule_type": "interval",
  "interval_duration": "1h",
  "max_runs": 10  // Stop after 10 executions
}
```

**Behavior:**
- Scheduler disables after `max_runs` executions
- `max_runs: 0` = unlimited (default)

### End Date

Stop execution after a **specific date/time**.

```json
{
  "schedule_type": "daily",
  "time_of_day": "09:00",
  "end_date": "2025-12-31T23:59:59Z"  // Stop after Dec 31, 2025
}
```

**Behavior:**
- Scheduler disables after `end_date`
- No new tasks created after this date

### Priority

Set task priority for created tasks.

```json
{
  "schedule_type": "cron",
  "cron_expression": "0 9 * * *",
  "priority": "high"  // "low", "medium", "high"
}
```

### Context

Provide additional context for created tasks.

```json
{
  "schedule_type": "daily",
  "time_of_day": "02:00",
  "context": {
    "backup_type": "full",
    "retention_days": 7
  }
}
```

---

## Monitoring Executions

### Execution History

View the last 20 executions in the scheduler node modal:

- **Task ID**: ID of created task
- **Executed At**: Timestamp of execution
- **Status**: "success" or "failed"
- **Error**: Error message (if failed)

### Failure Tracking

Scheduler nodes track consecutive failures:

- **Failure Count**: Increments on each failure
- **Last Error**: Most recent error message
- **Auto-Disable**: Disables after **5 consecutive failures**

### Real-Time Updates

Subscribe to SSE events:

```javascript
const eventSource = new EventSource('/api/orchestration/progress/stream?workspace_id={workspace_id}');

eventSource.addEventListener('scheduled_task.triggered', (event) => {
  const data = JSON.parse(event.data);
  console.log('Task executed:', data.task_id, data.execution_count);
});

eventSource.addEventListener('scheduled_task.failed', (event) => {
  const data = JSON.parse(event.data);
  console.log('Task failed:', data.error, data.failure_count);
});
```

---

## Best Practices

### 1. Use Descriptive Names

✅ **Good:**
```json
{"name": "Daily Sales Report at 9 AM"}
```

❌ **Bad:**
```json
{"name": "Task 1"}
```

### 2. Choose the Right Schedule Type

- **Simple repeating tasks** → Use `interval` or `daily`
- **Complex schedules** → Use `cron` expressions
- **Adaptive scheduling** → Use `relative_delay`

### 3. Set Max Runs for Finite Tasks

```json
{
  "schedule_type": "interval",
  "interval_duration": "1h",
  "max_runs": 24  // Run for 24 hours then stop
}
```

### 4. Use End Date for Time-Bound Tasks

```json
{
  "schedule_type": "daily",
  "time_of_day": "09:00",
  "end_date": "2025-03-01T00:00:00Z"  // Campaign ends March 1st
}
```

### 5. Monitor Failure Counts

- Check execution history regularly
- Investigate tasks with high failure counts
- Fix underlying issues before re-enabling

### 6. Test with Manual Trigger

Before enabling a scheduler node, use the **Manual Trigger** button to test the task creation.

### 7. Use Relative Delay for Dependent Tasks

When task duration varies, use `relative_delay` instead of fixed intervals:

```json
{
  "schedule_type": "relative_delay",
  "delay_duration": "5m",  // Wait 5 min after completion
  "trigger_once": false
}
```

### 8. Avoid Overlapping Executions

For long-running tasks, ensure the interval is longer than typical execution time.

❌ **Bad:** Task takes 10 minutes, interval is 5 minutes
```json
{"interval_duration": "5m"}  // Will overlap!
```

✅ **Good:** Allow buffer time
```json
{"interval_duration": "15m"}  // Safe spacing
```

### 9. Use Context for Dynamic Behavior

Pass configuration via context:

```json
{
  "prompt": "Run data sync",
  "context": {
    "source": "external-api",
    "batch_size": 100,
    "full_sync": false
  }
}
```

### 10. Leverage Cron Presets in UI

The UI provides common cron presets:
- Daily at 9:00 AM
- Weekdays at 2:00 PM
- Every Hour
- Every 15 Minutes

Use these as starting points.

---

## Common Patterns

### Pattern 1: Daily Morning Report

```json
{
  "name": "Daily Morning Sales Report",
  "prompt": "Generate yesterday's sales summary",
  "to": "reporting-agent",
  "schedule_type": "daily",
  "time_of_day": "09:00",
  "priority": "high"
}
```

### Pattern 2: Weekday Business Hours Monitoring

```json
{
  "name": "Business Hours Health Check",
  "prompt": "Check system health",
  "to": "monitor-agent",
  "schedule_type": "cron",
  "cron_expression": "*/30 9-17 * * 1-5"  // Every 30 min, 9 AM-5 PM, weekdays
}
```

### Pattern 3: Nightly Maintenance

```json
{
  "name": "Nightly Database Maintenance",
  "prompt": "Run database optimization and cleanup",
  "to": "maintenance-agent",
  "schedule_type": "daily",
  "time_of_day": "02:00"
}
```

### Pattern 4: Weekly Cleanup

```json
{
  "name": "Weekly Log Cleanup",
  "prompt": "Archive and delete old logs",
  "to": "cleanup-agent",
  "schedule_type": "weekly",
  "day_of_week": 0,  // Sunday
  "time_of_day": "01:00"
}
```

### Pattern 5: Campaign with End Date

```json
{
  "name": "Holiday Campaign Reminder",
  "prompt": "Send holiday promotion notification",
  "to": "marketing-agent",
  "schedule_type": "daily",
  "time_of_day": "10:00",
  "start_date": "2025-12-01T00:00:00Z",
  "end_date": "2025-12-25T23:59:59Z"  // Dec 1-25 only
}
```

---

## Troubleshooting

### Scheduler Node Not Executing

1. **Check if enabled**: Ensure `enabled: true`
2. **Check next_run**: Verify `next_run` is in the future
3. **Check workspace status**: Workspace must be "active"
4. **Check failure count**: Auto-disables after 5 consecutive failures

### Cron Expression Errors

- Use the **Cron Expression Helper** in the UI
- Validate expressions at [crontab.guru](https://crontab.guru/)
- Check server logs for parsing errors

### Tasks Not Being Created

- Verify `to` field points to valid agent/task node
- Check execution history for error messages
- Ensure target agent is active

### Next Run Not Updating

- Check if `max_runs` limit reached
- Check if `end_date` has passed
- Verify schedule configuration is valid

---

## API Reference

See [API_REFERENCE.md](api/API_REFERENCE.md#scheduler-nodes-api) for complete endpoint documentation.

---

## Related Documentation

- [Workspace Canvas Guide](WORKSPACE_CANVAS_GUIDE.md) - Canvas basics
- [Task Orchestration](TASK_ORCHESTRATION_GUIDE.md) - Multi-agent workflows
- [API Reference](api/API_REFERENCE.md) - Complete API documentation
