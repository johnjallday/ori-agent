package server

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type resetHomeWorkspaceStore struct {
	workspace.Store
	value *workspace.Workspace
	gets  atomic.Int32
}

func (s *resetHomeWorkspaceStore) Get(string) (*workspace.Workspace, error) {
	s.gets.Add(1)
	return s.value, nil
}

type resetHomeOrchestrator struct {
	started chan context.Context
	finish  chan struct{}
	done    chan struct{}
}

func (o *resetHomeOrchestrator) ExecuteTask(ctx context.Context, _ string, _ workspace.Task) error {
	o.started <- ctx
	<-o.finish
	close(o.done)
	return nil
}

func TestResetAdmissionHomeTaskTransfersBeforeAcknowledgement(t *testing.T) {
	store := &resetHomeWorkspaceStore{value: &workspace.Workspace{ID: "ws", FolderSlug: "owned", Tasks: []workspace.Task{{ID: "task", Status: workspace.TaskStatusAssigned}}}}
	orchestrator := &resetHomeOrchestrator{started: make(chan context.Context, 1), finish: make(chan struct{}), done: make(chan struct{})}
	gate := &resetstate.WorkGate{}
	mutator := homeActionMutator{admissionGate: gate, workspaces: store, orchestrator: orchestrator}
	if href, err := mutator.StartTask(context.Background(), "ws", "task"); err != nil || href != "/workspaces/owned" {
		t.Fatalf("StartTask = %q, %v", href, err)
	}
	var workCtx context.Context
	select {
	case workCtx = <-orchestrator.started:
	case <-time.After(5 * time.Second):
		t.Fatal("home task did not start")
	}
	if err := gate.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
		t.Fatalf("home child not tracked: %v", err)
	}
	select {
	case <-workCtx.Done():
		t.Fatalf("busy reset cancelled home task: %v", workCtx.Err())
	default:
	}
	close(orchestrator.finish)
	select {
	case <-orchestrator.done:
	case <-time.After(5 * time.Second):
		t.Fatal("home task did not finish")
	}
	deadline := time.Now().Add(5 * time.Second)
	for gate.Snapshot().Active != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestResetAdmissionHomeTaskRefusesBeforeWorkspaceAccess(t *testing.T) {
	gate := &resetstate.WorkGate{}
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	store := &resetHomeWorkspaceStore{value: &workspace.Workspace{}}
	mutator := homeActionMutator{admissionGate: gate, workspaces: store, orchestrator: &resetHomeOrchestrator{}}
	if _, err := mutator.StartTask(t.Context(), "ws", "task"); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatalf("fenced StartTask = %v", err)
	}
	if store.gets.Load() != 0 {
		t.Fatal("fenced home task read workspace")
	}
}
