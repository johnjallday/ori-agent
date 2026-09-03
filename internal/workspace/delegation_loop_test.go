package workspace

import (
	"context"
	"strings"
	"testing"
	"time"
)

type fakeAdapter struct {
	steps []CoordinatorAdaptResult
	calls int
}

func (f *fakeAdapter) Adapt(_ context.Context, _ CoordinatorAdaptRequest) (CoordinatorAdaptResult, error) {
	i := f.calls
	f.calls++
	if i < len(f.steps) {
		return f.steps[i], nil
	}
	return CoordinatorAdaptResult{}, nil // never resolves -> drives the iteration cap
}

type fakeExecutor struct {
	results map[string]string
}

func (f *fakeExecutor) ExecuteTask(_ context.Context, _ string, task Task) (string, error) {
	if r, ok := f.results[task.ID]; ok {
		return r, nil
	}
	return "executed " + task.ID, nil
}

func loopWorkspace(subtasks ...Task) *Workspace {
	return &Workspace{
		ID:     "ws",
		Status: StatusActive,
		AgentInstances: []AgentInstance{
			{Name: "Manager", NodeID: "manager-node-1", EntryPoint: true},
			{Name: "Writer", NodeID: "writer-node-1"},
		},
		Tasks: subtasks,
	}
}

// The coordinator is told who it may delegate to. Without this the model has to
// guess an agent name, and a wrong guess costs a rejected call plus an iteration.
func TestDelegationLoopPassesRosterToCoordinator(t *testing.T) {
	store := NewInMemoryStore()
	if err := store.Save(loopWorkspace()); err != nil {
		t.Fatalf("save: %v", err)
	}

	spy := &rosterSpyAdapter{}
	loop := NewDelegationLoop(store, &fakeExecutor{}, spy, DelegationCaps{})

	if _, err := loop.Run(context.Background(), "ws", Task{ID: "t1", Description: "do x"},
		DelegationTrigger{Trigger: true, Code: DelegationTriggerFailed}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(spy.seen) != 1 || spy.seen[0] != "Writer" {
		t.Fatalf("coordinator should be handed the specialist roster, got %v", spy.seen)
	}

	// The same roster must reach the prompt text, not just the request struct.
	prompt := buildCoordinatorAdaptPrompt(CoordinatorAdaptRequest{
		Coordinator: "Manager",
		FailedTask:  Task{ID: "t1", Description: "do x"},
		Trigger:     DelegationTrigger{Trigger: true, Code: DelegationTriggerFailed},
		Specialists: []string{"Writer"},
	})
	if !strings.Contains(prompt, "Writer") {
		t.Fatalf("adapt prompt should name the specialists:\n%s", prompt)
	}

	soloPrompt := buildCoordinatorAdaptPrompt(CoordinatorAdaptRequest{
		Coordinator: "Manager",
		FailedTask:  Task{ID: "t1", Description: "do x"},
		Trigger:     DelegationTrigger{Trigger: true, Code: DelegationTriggerFailed},
	})
	if !strings.Contains(soloPrompt, "nobody to delegate to") {
		t.Fatalf("adapt prompt should say when there is no roster:\n%s", soloPrompt)
	}
}

type rosterSpyAdapter struct {
	seen []string
}

func (s *rosterSpyAdapter) Adapt(_ context.Context, req CoordinatorAdaptRequest) (CoordinatorAdaptResult, error) {
	s.seen = req.Specialists
	return CoordinatorAdaptResult{Resolved: true, DirectResult: "done"}, nil
}

func TestDelegationLoopResolvesImmediately(t *testing.T) {
	store := NewInMemoryStore()
	if err := store.Save(loopWorkspace()); err != nil {
		t.Fatalf("save: %v", err)
	}
	loop := NewDelegationLoop(store, &fakeExecutor{},
		&fakeAdapter{steps: []CoordinatorAdaptResult{{Resolved: true, DirectResult: "fixed it"}}},
		DelegationCaps{})

	res, err := loop.Run(context.Background(), "ws", Task{Description: "do x"}, DelegationTrigger{Trigger: true, Code: DelegationTriggerFailed})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Resolved || res.Result != "fixed it" || res.Iterations != 1 {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestDelegationLoopDelegatesThenResolves(t *testing.T) {
	store := NewInMemoryStore()
	if err := store.Save(loopWorkspace(Task{ID: "sub1", WorkspaceID: "ws", To: "Writer", Description: "write"})); err != nil {
		t.Fatalf("save: %v", err)
	}
	loop := NewDelegationLoop(store,
		&fakeExecutor{results: map[string]string{"sub1": "subtask done"}},
		&fakeAdapter{steps: []CoordinatorAdaptResult{
			{DelegatedTaskIDs: []string{"sub1"}},
			{Resolved: true},
		}},
		DelegationCaps{})

	res, err := loop.Run(context.Background(), "ws", Task{Description: "do x"}, DelegationTrigger{Trigger: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Resolved || res.Result != "subtask done" || res.SubtaskCount != 1 || res.Iterations != 2 {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestDelegationLoopIterationCapBlocks(t *testing.T) {
	store := NewInMemoryStore()
	if err := store.Save(loopWorkspace()); err != nil {
		t.Fatalf("save: %v", err)
	}
	loop := NewDelegationLoop(store, &fakeExecutor{},
		&fakeAdapter{}, // never resolves
		DelegationCaps{MaxIterations: 2, Timeout: time.Minute})

	_, err := loop.Run(context.Background(), "ws", Task{Description: "do x"}, DelegationTrigger{Trigger: true, Reason: "boom"})
	blocked, ok := AsTaskBlockedError(err)
	if !ok || blocked.ReasonCode != "delegation_cap_exceeded" {
		t.Fatalf("expected delegation_cap_exceeded blocked error, got %v", err)
	}
}

func TestDelegationLoopNeedsInputReturnsBlock(t *testing.T) {
	store := NewInMemoryStore()
	if err := store.Save(loopWorkspace()); err != nil {
		t.Fatalf("save: %v", err)
	}
	loop := NewDelegationLoop(store, &fakeExecutor{},
		&fakeAdapter{steps: []CoordinatorAdaptResult{
			{NeedsInput: true, Question: "Which data source should I use?", SuggestedActions: []string{"a", "b"}},
		}},
		DelegationCaps{})

	_, err := loop.Run(context.Background(), "ws", Task{Description: "do x"}, DelegationTrigger{Trigger: true})
	blocked, ok := AsTaskBlockedError(err)
	if !ok || blocked.ReasonCode != "delegation_needs_input" || blocked.Question != "Which data source should I use?" {
		t.Fatalf("expected needs-input block, got %v", err)
	}
}

func TestShouldPauseForDelegationBlock(t *testing.T) {
	if !shouldPauseForDelegationBlock(Task{Description: "interactive"}) {
		t.Fatal("a regular task should pause-to-ask (interactive)")
	}
	if shouldPauseForDelegationBlock(Task{ScheduleEnabled: true}) {
		t.Fatal("a scheduled task must not pause (unattended)")
	}
	mission := Task{Context: map[string]any{MissionTaskContextOriginKey: MissionTaskContextOriginValue}}
	if shouldPauseForDelegationBlock(mission) {
		t.Fatal("a mission task must not pause (unattended)")
	}
}

func TestDelegationLoopSubtaskCapBlocks(t *testing.T) {
	store := NewInMemoryStore()
	ws := loopWorkspace(
		Task{ID: "sub1", WorkspaceID: "ws", To: "Writer", Description: "a"},
		Task{ID: "sub2", WorkspaceID: "ws", To: "Writer", Description: "b"},
	)
	if err := store.Save(ws); err != nil {
		t.Fatalf("save: %v", err)
	}
	loop := NewDelegationLoop(store, &fakeExecutor{},
		&fakeAdapter{steps: []CoordinatorAdaptResult{{DelegatedTaskIDs: []string{"sub1", "sub2"}}}},
		DelegationCaps{MaxSubtasks: 1, Timeout: time.Minute})

	_, err := loop.Run(context.Background(), "ws", Task{Description: "do x"}, DelegationTrigger{Trigger: true})
	blocked, ok := AsTaskBlockedError(err)
	if !ok || blocked.ReasonCode != "delegation_cap_exceeded" {
		t.Fatalf("expected delegation_cap_exceeded blocked error, got %v", err)
	}
}
