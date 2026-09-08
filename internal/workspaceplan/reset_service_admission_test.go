package workspaceplan

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
)

type blockingResetPlanModel struct {
	started chan context.Context
	finish  chan struct{}
	calls   atomic.Int32
}

func (m *blockingResetPlanModel) GenerateStructured(ctx context.Context, _ StructuredRequest) (string, error) {
	m.calls.Add(1)
	m.started <- ctx
	select {
	case <-m.finish:
		return validDraftResponse, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func TestResetAdmissionPlanServiceOwnsModelThroughFinalDraft(t *testing.T) {
	service := NewService(NewMemoryStore())
	gate := &resetstate.WorkGate{}
	service.SetAdmissionGate(gate)
	plan, err := service.Create(t.Context(), "ws", CreateInput{Request: "Plan it"})
	if err != nil {
		t.Fatal(err)
	}
	model := &blockingResetPlanModel{started: make(chan context.Context, 1), finish: make(chan struct{})}
	service.SetGenerator(NewGenerator(model))
	done := make(chan error, 1)
	go func() { _, err := service.Draft(context.Background(), "ws", plan.ID, DraftingOptions{}); done <- err }()
	var workCtx context.Context
	select {
	case workCtx = <-model.started:
	case <-time.After(5 * time.Second):
		t.Fatal("plan model did not start")
	}
	if err := gate.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
		t.Fatalf("plan draft not tracked: %v", err)
	}
	select {
	case <-workCtx.Done():
		t.Fatalf("busy reset cancelled plan draft: %v", workCtx.Err())
	default:
	}
	release, err := gate.Enter()
	if err != nil {
		t.Fatalf("busy refusal fenced new work: %v", err)
	}
	release()
	close(model.finish)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("plan draft did not finish")
	}
	updated, err := service.Get(t.Context(), "ws", plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Objective != "Migrate reporting safely" {
		t.Fatalf("final draft was not persisted: %+v", updated)
	}
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestResetAdmissionPlanServiceRefusesBeforeClockOrModel(t *testing.T) {
	service := NewService(NewMemoryStore())
	gate := &resetstate.WorkGate{}
	service.SetAdmissionGate(gate)
	var clockCalls atomic.Int32
	service.now = func() time.Time { clockCalls.Add(1); return time.Now() }
	model := &blockingResetPlanModel{started: make(chan context.Context, 1), finish: make(chan struct{})}
	service.SetGenerator(NewGenerator(model))
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(t.Context(), "ws", CreateInput{Request: "must not create"}); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatalf("fenced create = %v", err)
	}
	if _, err := service.Draft(t.Context(), "ws", "missing", DraftingOptions{}); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatalf("fenced draft = %v", err)
	}
	if clockCalls.Load() != 0 || model.calls.Load() != 0 {
		t.Fatalf("fenced plan reached owners: clock=%d model=%d", clockCalls.Load(), model.calls.Load())
	}
}
