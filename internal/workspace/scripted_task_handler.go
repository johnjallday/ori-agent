package workspace

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"
)

// ScriptedTaskRunsEnv turns on the dev-only scripted task handler. It exists so
// the map's task-run show can be demoed and tested end to end without a model:
// a sandboxed demo usually has no API key, so a real task fails at once and never
// calls a tool (tasks/prd-task-run-show.md §7).
//
// It is read in exactly one place, the server builder, and only the exact value
// "1" enables it, so a stray "true" in a real deployment cannot quietly replace
// every task run with a script.
const ScriptedTaskRunsEnv = "ORI_DEV_SCRIPTED_TASK_RUNS"

// ScriptedTaskRunsEnabled reports whether ORI_DEV_SCRIPTED_TASK_RUNS=1 is set.
func ScriptedTaskRunsEnabled() bool {
	return strings.TrimSpace(os.Getenv(ScriptedTaskRunsEnv)) == "1"
}

// ScriptedTaskFailMarker in a task description makes the scripted run fail
// right after its first tool call, so the failure path can be demoed too.
const ScriptedTaskFailMarker = "[fail]"

// ScriptedTaskResult is the fixed result every successful scripted run returns.
const ScriptedTaskResult = "Scripted demo run: read the workspace notes and looked two things up. No model was called."

// ErrScriptedTaskFailed is the error a scripted run returns when its description
// contains ScriptedTaskFailMarker.
var ErrScriptedTaskFailed = errors.New("scripted demo run failed on purpose (the description contains [fail])")

const defaultScriptedStepDelay = 2 * time.Second

type scriptedEventPublisher interface {
	Publish(event Event)
}

type scriptedStep struct {
	eventType EventType
	data      map[string]any
}

// ScriptedTaskHandler is a TaskHandler that calls no model. It publishes a
// fixed, realistic sequence of real task events on the bus — thinking, a file
// read, a web search, their results — spaced about two seconds apart, then
// returns ScriptedTaskResult. Everything downstream of the handler (task status,
// XP, Craft, the activity feed, parcels) is the real code path.
//
// Development only: the builder installs it only when ScriptedTaskRunsEnabled.
type ScriptedTaskHandler struct {
	bus       scriptedEventPublisher
	stepDelay time.Duration
}

// NewScriptedTaskHandler builds the scripted handler over the given bus. A nil
// bus is allowed; the run then publishes nothing but still returns its result.
func NewScriptedTaskHandler(bus *EventBus) *ScriptedTaskHandler {
	h := &ScriptedTaskHandler{stepDelay: defaultScriptedStepDelay}
	if bus != nil {
		h.bus = bus
	}
	return h
}

func scriptedTaskSteps() []scriptedStep {
	return []scriptedStep{
		{EventTaskThinking, map[string]any{"message": "Planning the scripted run"}},
		{EventTaskToolCall, map[string]any{
			"tool_name": "read_file",
			"arguments": map[string]any{"path": "notes/README.md"},
		}},
		{EventTaskToolResult, map[string]any{
			"tool_name":      "read_file",
			"success":        true,
			"result_preview": "Scripted file contents",
		}},
		{EventTaskToolCall, map[string]any{
			"tool_name": "web_search",
			"arguments": map[string]any{"query": "scripted demo query"},
		}},
		{EventTaskToolResult, map[string]any{
			"tool_name":      "web_search",
			"success":        true,
			"result_preview": "Scripted search results",
		}},
		{EventTaskThinking, map[string]any{"message": "Writing up the scripted result"}},
	}
}

// ExecuteTask plays the scripted sequence for one task. It honours ctx
// cancellation between steps.
func (h *ScriptedTaskHandler) ExecuteTask(ctx context.Context, agentName string, task Task) (string, error) {
	fail := strings.Contains(strings.ToLower(task.Description), ScriptedTaskFailMarker)

	for i, step := range scriptedTaskSteps() {
		if i > 0 {
			if err := h.wait(ctx); err != nil {
				return "", err
			}
		} else if err := ctx.Err(); err != nil {
			return "", err
		}
		h.publish(NewTaskEvent(step.eventType, task.WorkspaceID, task.ID, agentName, step.data))
		if fail && step.eventType == EventTaskToolCall {
			return "", ErrScriptedTaskFailed
		}
	}

	if err := h.wait(ctx); err != nil {
		return "", err
	}
	return ScriptedTaskResult, nil
}

func (h *ScriptedTaskHandler) publish(event Event) {
	if h.bus != nil {
		h.bus.Publish(event)
	}
}

func (h *ScriptedTaskHandler) wait(ctx context.Context) error {
	if h.stepDelay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(h.stepDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
