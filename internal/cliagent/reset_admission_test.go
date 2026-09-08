package cliagent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/resetstate"
)

type resetPausedPlanner struct {
	*multiMockProvider
	started chan context.Context
	release chan struct{}
	once    sync.Once
}

func (p *resetPausedPlanner) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	p.once.Do(func() { p.started <- ctx; <-p.release })
	return p.multiMockProvider.Chat(ctx, req)
}

func TestResetAdmissionCLIRefusesBeforeAdapterDiscovery(t *testing.T) {
	g := &resetstate.WorkGate{}
	if err := g.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Nil owners are intentional: fenced execution must not discover a CLI,
	// call a provider, inspect a project, or persist a log.
	e := NewMicroStepExecutor(nil, nil, nil, nil, nil)
	e.SetAdmissionGate(g)
	result, err := e.Execute(t.Context(), TaskConfig{CLIBackend: BackendClaude, Prompt: "owned task", WorkingDir: t.TempDir()})
	if !errors.Is(err, resetstate.ErrWorkFenced) || result != nil || e.RunningCount() != 0 {
		t.Fatalf("fenced execution: %+v %v", result, err)
	}
}

func TestResetAdmissionCLIRefusalDoesNotCancelAndNormalCompletionPersists(t *testing.T) {
	// DiffDetector's optional git probe must not discover any real executable.
	// The only adapter/provider below is test-owned, never external CLI auth.
	t.Setenv("PATH", t.TempDir())
	g := &resetstate.WorkGate{}
	p := &resetPausedPlanner{
		multiMockProvider: &multiMockProvider{responses: []string{
			`[{"step_number":1,"description":"fixture step","expected_outcome":"done"}]`,
			"fixture summary", `{"done":true,"rationale":"done"}`,
		}},
		started: make(chan context.Context, 1), release: make(chan struct{}),
	}
	logs := NewEventLogger(t.TempDir())
	e := NewMicroStepExecutor(NewRegistry(&testAdapter{}), NewStepPlanner(p, "fixture"), logs, NewDiffDetector(), nil)
	e.SetAdmissionGate(g)
	config := TaskConfig{CLIBackend: BackendClaude, Prompt: "owned task", WorkingDir: t.TempDir()}
	type outcome struct {
		result *TaskResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() { result, err := e.Execute(t.Context(), config); done <- outcome{result, err} }()
	release := sync.OnceFunc(func() { close(p.release) })
	var got outcome
	join := sync.OnceFunc(func() {
		select {
		case got = <-done:
		case <-time.After(5 * time.Second):
			t.Error("owned CLI fixture did not finish")
		}
	})
	t.Cleanup(func() { release(); join() })
	var ctx context.Context
	select {
	case ctx = <-p.started:
	case <-time.After(5 * time.Second):
		t.Fatal("owned planner did not start")
	}
	if e.RunningCount() != 1 {
		t.Fatal("CLI task not registered")
	}
	if err := g.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
		t.Fatalf("active CLI admission: %v", err)
	}
	if ctx.Err() != nil || g.Snapshot().Fenced {
		t.Fatal("reset cancelled or fenced active CLI work")
	}
	release()
	join()
	if got.err != nil || got.result == nil || got.result.Status != TaskCompleted {
		t.Fatalf("normal completion: %+v %v", got.result, got.err)
	}
	if e.RunningCount() != 0 {
		t.Fatal("completion retained running slot")
	}
	if _, err := logs.LoadEvents(got.result.TaskID); err != nil {
		t.Fatalf("final events not persisted: %v", err)
	}
	if err := g.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Execute(t.Context(), config); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatalf("second execution after fence: %v", err)
	}
}
