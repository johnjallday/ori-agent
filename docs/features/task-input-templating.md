# Task Input Templating - Implementation Plan

## Overview

Enable tasks to reference results from previous tasks in their descriptions, allowing chaining of operations like:
- Task 1: "3 + 1" → Result: "4"
- Task 2: "{input1} * 2" → Uses "4" → Result: "8"

## Goals

1. **Explicit Placeholders**: `{input1}`, `{input2}`, etc. for numbered inputs
2. **Named Shortcuts**: `{previous}` or `{result}` for most recent input
3. **Natural Language**: LLM automatically sees input results in context

## User Stories

### Story 1: Math Chain
```
User creates Task A: "Calculate 3 + 1"
→ Agent executes → Result: "4"

User creates Task B: "{input1} * 2"
→ User connects Task A output to Task B input (purple dotted line)
→ System substitutes {input1} with "4"
→ Agent sees: "4 * 2"
→ Result: "8"
```

### Story 2: Natural Language
```
User creates Task A: "What is the capital of France?"
→ Result: "Paris"

User creates Task B: "What is the population of that city?"
→ System adds to context: "Previous result: Paris"
→ Agent naturally references "Paris" from context
→ Result: "Approximately 2.2 million"
```

### Story 3: Multiple Inputs
```
Task A: "5 + 3" → "8"
Task B: "10 - 2" → "8"
Task C: "{input1} + {input2}" (connected to A and B)
→ System substitutes: "8 + 8"
→ Result: "16"
```

## Technical Architecture

### Data Flow

```
┌─────────────────────────────────────────────────────────────┐
│ 1. Task Execution Request                                   │
│    GET /api/orchestration/tasks/execute                     │
│    { task_id: "abc123" }                                    │
└────────────────┬────────────────────────────────────────────┘
                 │
                 ▼
┌─────────────────────────────────────────────────────────────┐
│ 2. Gather Input Task Results                                │
│    - Load task from workspace                               │
│    - For each ID in task.input_task_ids:                    │
│      • Fetch task by ID                                     │
│      • Extract result field                                 │
│      • Build inputs array: ["4", "8", ...]                  │
└────────────────┬────────────────────────────────────────────┘
                 │
                 ▼
┌─────────────────────────────────────────────────────────────┐
│ 3. Process Task Description                                 │
│    Original: "{input1} * 2"                                 │
│                                                              │
│    a) Substitute numbered placeholders:                     │
│       {input1} → inputs[0] → "4"                            │
│       {input2} → inputs[1] → "8"                            │
│       Result: "4 * 2"                                       │
│                                                              │
│    b) Substitute shortcuts:                                 │
│       {previous} → inputs[0] → "4"                          │
│       {result} → inputs[0] → "4"                            │
│                                                              │
│    c) Build context message:                                │
│       "Previous results:                                    │
│        Input 1: 4                                           │
│        Input 2: 8"                                          │
└────────────────┬────────────────────────────────────────────┘
                 │
                 ▼
┌─────────────────────────────────────────────────────────────┐
│ 4. Send to LLM                                              │
│    System: "You are a helpful assistant..."                │
│    System: "Previous results: Input 1: 4, Input 2: 8"      │
│    User: "4 * 2"                                            │
└────────────────┬────────────────────────────────────────────┘
                 │
                 ▼
┌─────────────────────────────────────────────────────────────┐
│ 5. Return Result                                            │
│    Assistant: "8"                                           │
└─────────────────────────────────────────────────────────────┘
```

## Implementation Steps

### Phase 1: Backend - Input Gathering

**File**: `/Users/jjdev/Projects/ori/ori-agent/internal/orchestrationhttp/task_handler.go`

**Location**: In `ExecuteTask` method, before calling the LLM

**Add new function**:
```go
// gatherInputResults fetches results from all input tasks
func (th *TaskHandler) gatherInputResults(ws *agentstudio.Workspace, task *agentstudio.Task) ([]string, error) {
	if len(task.InputTaskIDs) == 0 {
		return nil, nil
	}

	var results []string
	for _, inputTaskID := range task.InputTaskIDs {
		inputTask, err := ws.GetTask(inputTaskID)
		if err != nil {
			log.Printf("⚠️  Input task %s not found, skipping", inputTaskID)
			continue
		}

		// Only use completed tasks
		if inputTask.Status != agentstudio.TaskStatusCompleted {
			log.Printf("⚠️  Input task %s not completed (status: %s), skipping", inputTaskID, inputTask.Status)
			continue
		}

		results = append(results, inputTask.Result)
	}

	return results, nil
}
```

### Phase 2: Backend - Placeholder Substitution

**File**: Same as Phase 1

**Add new function**:
```go
import (
	"regexp"
	"strings"
)

// substituteInputPlaceholders replaces {inputN}, {previous}, {result} with actual values
func substituteInputPlaceholders(description string, inputs []string) string {
	if len(inputs) == 0 {
		return description
	}

	result := description

	// Replace numbered placeholders: {input1}, {input2}, etc.
	for i, input := range inputs {
		placeholder := fmt.Sprintf("{input%d}", i+1)
		result = strings.ReplaceAll(result, placeholder, input)
	}

	// Replace shortcuts: {previous} and {result} (both map to first input)
	result = strings.ReplaceAll(result, "{previous}", inputs[0])
	result = strings.ReplaceAll(result, "{result}", inputs[0])

	return result
}
```

### Phase 3: Backend - Context Building

**Add new function**:
```go
// buildInputContext creates a system message with input context
func buildInputContext(inputs []string) string {
	if len(inputs) == 0 {
		return ""
	}

	var parts []string
	parts = append(parts, "Previous task results:")

	for i, input := range inputs {
		parts = append(parts, fmt.Sprintf("  Input %d: %s", i+1, input))
	}

	return strings.Join(parts, "\n")
}
```

### Phase 4: Backend - Integration

**Modify** `ExecuteTask` method:

```go
func (th *TaskHandler) ExecuteTask(ctx context.Context, agentName string, task agentstudio.Task) (string, error) {
	// ... existing code to load workspace and task ...

	// NEW: Gather input results
	inputs, err := th.gatherInputResults(foundWorkspace, foundTask)
	if err != nil {
		return "", err
	}

	// NEW: Substitute placeholders in description
	processedDescription := substituteInputPlaceholders(foundTask.Description, inputs)

	// NEW: Build context message
	contextMsg := buildInputContext(inputs)

	// ... existing code to get agent and load plugins ...

	// Build messages for LLM
	messages := []Message{
		{Role: "system", Content: agentSystemPrompt},
	}

	// NEW: Add input context if available
	if contextMsg != "" {
		messages = append(messages, Message{
			Role: "system",
			Content: contextMsg,
		})
	}

	// Add user message with processed description
	messages = append(messages, Message{
		Role: "user",
		Content: processedDescription, // Use processed, not original
	})

	// ... rest of LLM call ...
}
```

### Phase 5: Frontend - Agent-to-Task Connections

**File**: `/Users/jjdev/Projects/ori/ori-agent/internal/web/static/js/modules/agent-canvas-interactions.js`

**Modify**: `handleMouseDown` to allow dragging from agent nodes

**Current**: Only task nodes can be dragged for connections
**New**: Agent nodes can also be dragged to connect their latest result

```javascript
handleMouseDown(x, y) {
  // ... existing code ...

  // Check if clicking on agent node
  const clickedAgent = this.parent.agents.find(agent => {
    const dx = x - agent.x;
    const dy = y - agent.y;
    return Math.hypot(dx, dy) < 50; // Within agent circle
  });

  if (clickedAgent) {
    // Enable result connection mode
    this.state.resultConnectionMode = true;
    this.state.resultSourceAgent = clickedAgent;
    this.state.resultConnectionStartX = x;
    this.state.resultConnectionStartY = y;
    this.canvas.style.cursor = 'crosshair';
    return;
  }

  // ... rest of existing code ...
}
```

**Modify**: `handleMouseUp` to complete agent-to-task connection

```javascript
handleMouseUp(x, y) {
  // ... existing code ...

  // Handle result connection from agent to task
  if (this.state.resultConnectionMode && this.state.resultSourceAgent) {
    const targetTask = this.findTaskAtPosition(x, y);

    if (targetTask) {
      // Find agent's most recent completed task
      const agentTasks = this.state.tasks.filter(t =>
        t.to === this.state.resultSourceAgent.name &&
        t.assigned_node_id === this.state.resultSourceAgent.nodeId &&
        t.status === 'completed'
      ).sort((a, b) =>
        new Date(b.completed_at) - new Date(a.completed_at)
      );

      if (agentTasks.length > 0) {
        const latestTask = agentTasks[0];
        // Use existing linkTaskResult function
        this.parent.linkTaskResult(latestTask.id, targetTask.id);
      } else {
        this.parent.showNotification('Agent has no completed tasks', 'warning');
      }
    }

    // Reset connection mode
    this.state.resultConnectionMode = false;
    this.state.resultSourceAgent = null;
    this.canvas.style.cursor = 'default';
    this.parent.draw();
  }

  // ... rest of existing code ...
}
```

### Phase 6: Frontend - Template Hints

**File**: `/Users/jjdev/Projects/ori/ori-agent/internal/web/static/js/modules/agent-canvas-ui.js`

**Add**: Tooltip/hint when creating task that has inputs

```javascript
// Show template hint in task creation modal
function showTemplateHints(taskInputCount) {
  if (taskInputCount === 0) return '';

  let hints = '<div class="template-hints">';
  hints += '<small class="text-muted">Available placeholders:</small><br>';
  hints += '<code>{previous}</code> or <code>{result}</code> - most recent input<br>';

  for (let i = 1; i <= taskInputCount; i++) {
    hints += `<code>{input${i}}</code> - input #${i}<br>`;
  }

  hints += '<small>Or use natural language - inputs are in context</small>';
  hints += '</div>';

  return hints;
}
```

## Testing Plan

### Unit Tests

**File**: `/Users/jjdev/Projects/ori/ori-agent/internal/orchestrationhttp/task_handler_test.go`

```go
func TestSubstituteInputPlaceholders(t *testing.T) {
	tests := []struct {
		name        string
		description string
		inputs      []string
		expected    string
	}{
		{
			name:        "Single numbered placeholder",
			description: "{input1} * 2",
			inputs:      []string{"4"},
			expected:    "4 * 2",
		},
		{
			name:        "Multiple numbered placeholders",
			description: "{input1} + {input2}",
			inputs:      []string{"4", "8"},
			expected:    "4 + 8",
		},
		{
			name:        "Previous shortcut",
			description: "multiply {previous} by 3",
			inputs:      []string{"5"},
			expected:    "multiply 5 by 3",
		},
		{
			name:        "No placeholders",
			description: "just text",
			inputs:      []string{"4"},
			expected:    "just text",
		},
		{
			name:        "No inputs",
			description: "{input1} test",
			inputs:      []string{},
			expected:    "{input1} test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := substituteInputPlaceholders(tt.description, tt.inputs)
			if result != tt.expected {
				t.Errorf("got %q, want %q", result, tt.expected)
			}
		})
	}
}
```

### Integration Tests

**Test Scenario 1: Simple Chain**
1. Create workspace
2. Create Task A: "Calculate 3 + 1"
3. Execute Task A → verify result "4"
4. Create Task B: "{input1} * 2" with input_task_ids=[Task A ID]
5. Execute Task B → verify result "8"

**Test Scenario 2: Natural Language**
1. Create Task A: "What is 5 + 5?"
2. Execute Task A → verify result contains "10"
3. Create Task B: "Double that number" with input_task_ids=[Task A ID]
4. Execute Task B → verify result contains "20"

### Manual Testing Checklist

- [ ] Test {input1} placeholder substitution
- [ ] Test {input2} with multiple inputs
- [ ] Test {previous} shortcut
- [ ] Test {result} shortcut
- [ ] Test natural language (no placeholders)
- [ ] Test with missing/incomplete input tasks
- [ ] Test UI: drag from agent to task creates connection
- [ ] Test UI: template hints show in modal
- [ ] Test UI: purple dotted line appears between connected tasks

## Edge Cases to Handle

1. **Missing Input Task**: Log warning, skip that input
2. **Incomplete Input Task**: Only use completed tasks
3. **Circular Dependencies**: Detect and reject (future enhancement)
4. **Empty Results**: Handle gracefully, pass empty string
5. **Placeholder Not Found**: Leave as-is (don't substitute)

## Future Enhancements

1. **Typed Inputs**: Specify input type (number, text, JSON)
2. **Input Validation**: Ensure all required inputs are present
3. **Dependency Graph**: Visual representation of task dependencies
4. **Parallel Execution**: Smart scheduling based on dependencies
5. **Custom Placeholders**: User-defined variable names

## Example Usage

### Math Operations Chain
```
Task 1: "3 + 1"           → Result: "4"
         ↓ (purple line)
Task 2: "{input1} * 2"    → Becomes: "4 * 2" → Result: "8"
         ↓ (purple line)
Task 3: "{input1} - 3"    → Becomes: "8 - 3" → Result: "5"
```

### Data Processing Chain
```
Task 1: "Get weather for SF"                    → "Sunny, 72°F"
         ↓
Task 2: "Convert {input1} to Celsius"          → "22°C"
         ↓
Task 3: "Is {input1} good beach weather?"      → "Yes"
```

### Multi-Input Merge
```
Task A: "Population of NYC"      → "8.3M"
Task B: "Population of LA"       → "4M"
         ↓            ↓
Task C: "Compare {input1} and {input2}" → "NYC is larger by 4.3M"
```

## File Locations Summary

### Backend Changes
- `/Users/jjdev/Projects/ori/ori-agent/internal/orchestrationhttp/task_handler.go`
  - Add: `gatherInputResults()`
  - Add: `substituteInputPlaceholders()`
  - Add: `buildInputContext()`
  - Modify: `ExecuteTask()` to use new functions

### Frontend Changes
- `/Users/jjdev/Projects/ori/ori-agent/internal/web/static/js/modules/agent-canvas-interactions.js`
  - Modify: `handleMouseDown()` - enable agent dragging
  - Modify: `handleMouseUp()` - complete agent-to-task connection

- `/Users/jjdev/Projects/ori/ori-agent/internal/web/static/js/modules/agent-canvas-ui.js`
  - Add: `showTemplateHints()` for task modal

### Test Files
- `/Users/jjdev/Projects/ori/ori-agent/internal/orchestrationhttp/task_handler_test.go`
  - Add unit tests for placeholder substitution

## Estimated Effort

- **Backend Implementation**: 2-3 hours
- **Frontend Implementation**: 1-2 hours
- **Testing**: 1 hour
- **Documentation**: 30 minutes
- **Total**: 4-6 hours

## Dependencies

- Requires existing `input_task_ids` field on Task struct ✅
- Requires existing purple dotted line rendering ✅
- Requires task-to-task connection system ✅

## Success Criteria

1. Users can use `{input1}`, `{input2}`, `{previous}`, `{result}` in task descriptions
2. Placeholders are replaced with actual values before LLM sees them
3. Input results are included in system context automatically
4. Users can drag from agent nodes to tasks to connect latest result
5. Template hints appear when creating tasks with inputs
6. All test scenarios pass

---

**Status**: Ready for Implementation
**Priority**: Medium
**Complexity**: Medium
**Created**: 2025-12-01
