package workspace

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/openai/openai-go/v3"
)

// stubAgentStore implements just enough of store.Store for analyzeMission
// to call formatAgentCapabilities without panicking. GetAgent always returns
// (nil, false) so the function takes the "General purpose agent" fallback.
type stubAgentStore struct{}

func (stubAgentStore) ListAgents() []string                               { return nil }
func (stubAgentStore) CreateAgent(string, *store.CreateAgentConfig) error { return nil }
func (stubAgentStore) DeleteAgent(string) error                           { return nil }
func (stubAgentStore) GetAgent(string) (*agent.Agent, bool)               { return nil, false }
func (stubAgentStore) SetAgent(string, *agent.Agent) error                { return nil }
func (stubAgentStore) UpdateAgent(string, func(*agent.Agent) error) error { return nil }
func (stubAgentStore) ClearAgents() error                                 { return nil }
func (stubAgentStore) Save() error                                        { return nil }

// fakePlannerLLM is a minimal LLMProvider that returns a canned ChatCompletion
// for tests of analyzeMission. ChatWithTools / ChatWithMessages aren't used by
// analyzeMission, so they panic to surface accidental drift.
type fakePlannerLLM struct {
	content string
}

func (f *fakePlannerLLM) ChatCompletion(_ context.Context, _ []openai.ChatCompletionMessageParamUnion, _ []openai.ChatCompletionToolUnionParam) (*openai.ChatCompletion, error) {
	return &openai.ChatCompletion{
		Choices: []openai.ChatCompletionChoice{
			{
				Message: openai.ChatCompletionMessage{
					Role:    "assistant",
					Content: f.content,
				},
				FinishReason: "stop",
				Index:        0,
			},
		},
	}, nil
}

func (f *fakePlannerLLM) ChatWithTools(_ context.Context, _ string, _ string, _ []llm.Tool) (*llm.ChatResponse, error) {
	panic("ChatWithTools not used by analyzeMission")
}
func (f *fakePlannerLLM) ChatWithMessages(_ context.Context, _ []llm.Message, _ []llm.Tool) (*llm.ChatResponse, error) {
	panic("ChatWithMessages not used by analyzeMission")
}

// TestAnalyzeMission_ResolvesIndexDependenciesIntoInputTaskIDs covers the
// previously-broken contract: LLM returns 1-based indices into the task
// array, the orchestrator resolves them to the freshly-minted task UUIDs
// and assigns them to Task.InputTaskIDs (where execution actually picks
// them up via BuildRuntimeInputs).
func TestAnalyzeMission_ResolvesIndexDependenciesIntoInputTaskIDs(t *testing.T) {
	llmContent := `[
        {"description": "fetch data", "assigned_to": "agent-a", "priority": 5, "dependencies": []},
        {"description": "summarize", "assigned_to": "agent-a", "priority": 5, "dependencies": [1]},
        {"description": "publish", "assigned_to": "agent-a", "priority": 5, "dependencies": [1, 2]}
    ]`

	o := &Orchestrator{
		llmProvider: &fakePlannerLLM{content: llmContent},
		agentStore:  stubAgentStore{},
	}

	tasks, err := o.analyzeMission(context.Background(), "ship a thing", []string{"agent-a"})
	if err != nil {
		t.Fatalf("analyzeMission: %v", err)
	}
	if len(tasks) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(tasks))
	}

	if len(tasks[0].InputTaskIDs) != 0 {
		t.Errorf("task[0] should have no inputs, got %v", tasks[0].InputTaskIDs)
	}
	if got := tasks[1].InputTaskIDs; len(got) != 1 || got[0] != tasks[0].ID {
		t.Errorf("task[1] should reference tasks[0]=%s, got %v", tasks[0].ID, got)
	}
	if got := tasks[2].InputTaskIDs; len(got) != 2 || got[0] != tasks[0].ID || got[1] != tasks[1].ID {
		t.Errorf("task[2] should reference [task0, task1] = [%s, %s], got %v",
			tasks[0].ID, tasks[1].ID, got)
	}

	// Confirm the dead Context["dependencies"] key is no longer written —
	// any code reading it would just see nothing.
	for i, task := range tasks {
		if _, present := task.Context["dependencies"]; present {
			t.Errorf("task[%d] still has Context[\"dependencies\"]; analyzeMission should have dropped it after migrating to InputTaskIDs", i)
		}
	}
}

// TestAnalyzeMission_HandlesStringifiedIndices covers the defensive parsing
// path for LLMs that wrap dependency indices in JSON strings.
func TestAnalyzeMission_HandlesStringifiedIndices(t *testing.T) {
	llmContent := `[
        {"description": "first", "assigned_to": "agent-a", "priority": 5, "dependencies": []},
        {"description": "second", "assigned_to": "agent-a", "priority": 5, "dependencies": ["1"]}
    ]`

	o := &Orchestrator{llmProvider: &fakePlannerLLM{content: llmContent}, agentStore: stubAgentStore{}}
	tasks, err := o.analyzeMission(context.Background(), "m", []string{"agent-a"})
	if err != nil {
		t.Fatalf("analyzeMission: %v", err)
	}
	if got := tasks[1].InputTaskIDs; len(got) != 1 || got[0] != tasks[0].ID {
		t.Errorf("expected stringified dep '1' to resolve to tasks[0].ID, got %v", got)
	}
}

// TestAnalyzeMission_DropsBadDependencies verifies graceful degradation: an
// out-of-range index, a self-reference, and a non-numeric value should each
// drop silently rather than producing an invalid graph.
func TestAnalyzeMission_DropsBadDependencies(t *testing.T) {
	// Task 1 references itself (1) and an out-of-range index (99) and a
	// non-numeric value. Task 2 depends on a valid index 1. The good edge
	// must land; the bad ones must drop.
	llmContent := `[
        {"description": "first", "assigned_to": "agent-a", "priority": 5, "dependencies": [1, 99, "abc"]},
        {"description": "second", "assigned_to": "agent-a", "priority": 5, "dependencies": [1]}
    ]`

	o := &Orchestrator{llmProvider: &fakePlannerLLM{content: llmContent}, agentStore: stubAgentStore{}}
	tasks, err := o.analyzeMission(context.Background(), "m", []string{"agent-a"})
	if err != nil {
		t.Fatalf("analyzeMission: %v", err)
	}
	if len(tasks[0].InputTaskIDs) != 0 {
		t.Errorf("task[0] bad deps should all drop, got %v", tasks[0].InputTaskIDs)
	}
	if got := tasks[1].InputTaskIDs; len(got) != 1 || got[0] != tasks[0].ID {
		t.Errorf("task[1] should still resolve its valid dep, got %v", got)
	}
}

// TestAnalyzeMission_FallsBackOnUnparseableJSON covers the existing fallback
// branch (single-task fallback when JSON parsing fails). Dependencies don't
// apply on this path; just confirm the fallback still produces a runnable
// task without panicking on the new resolver code.
func TestAnalyzeMission_FallsBackOnUnparseableJSON(t *testing.T) {
	o := &Orchestrator{llmProvider: &fakePlannerLLM{content: "this is not JSON"}, agentStore: stubAgentStore{}}
	tasks, err := o.analyzeMission(context.Background(), "fallback case", []string{"agent-a"})
	if err != nil {
		t.Fatalf("analyzeMission: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("fallback path should produce one task, got %d", len(tasks))
	}
	if tasks[0].To != "agent-a" {
		t.Errorf("fallback should assign first available agent, got %q", tasks[0].To)
	}
	if len(tasks[0].InputTaskIDs) != 0 {
		t.Errorf("fallback task should have no inputs, got %v", tasks[0].InputTaskIDs)
	}
}
