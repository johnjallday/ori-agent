package workspaceplan

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type resetBlockingTaskWriter struct {
	base    *fakeTaskWriter
	started chan struct{}
	finish  chan struct{}
}

func (w *resetBlockingTaskWriter) Get(id string) (*workspace.Workspace, error) {
	return w.base.Get(id)
}

func (w *resetBlockingTaskWriter) Update(id string, fn func(*workspace.Workspace) error) error {
	close(w.started)
	<-w.finish
	return w.base.Update(id, fn)
}

func TestMaterializerAdmissionOwnsWorkThroughFinalPersistence(t *testing.T) {
	ctx := t.Context()
	service, _, writer, plan, approval := materializable(t, ctx, reviewableContent())
	gate := &resetstate.WorkGate{}
	service.SetAdmissionGate(gate)
	blocking := &resetBlockingTaskWriter{
		base: writer, started: make(chan struct{}), finish: make(chan struct{}),
	}
	materializer := NewMaterializer(service, blocking)
	done := make(chan error, 1)
	go func() {
		_, err := materializer.Materialize(context.Background(), testWorkspaceID, plan.ID, MaterializeInput{ApprovalID: approval.ID})
		done <- err
	}()

	select {
	case <-blocking.started:
	case <-time.After(5 * time.Second):
		t.Fatal("materializer did not reach task persistence")
	}
	if err := gate.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
		t.Fatalf("materializer was not admitted through persistence: %v", err)
	}
	close(blocking.finish)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Materialize: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("materializer did not finish")
	}
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatalf("fence after materialization: %v", err)
	}
}

func TestDirectPlanHelpersRefuseBeforeOwnerAccessWhenFenced(t *testing.T) {
	ctx := t.Context()
	service, materializer, writer, plan, approval := materializable(t, ctx, reviewableContent())
	reconciler := NewReconciler(service, writer, &fakeMutator{writer: writer})
	executor := NewExecutor(service, writer)
	gate := &resetstate.WorkGate{}
	service.SetAdmissionGate(gate)
	if err := gate.TryFence(ctx); err != nil {
		t.Fatal(err)
	}

	checks := []struct {
		name string
		call func() error
	}{
		{"materialize", func() error {
			_, err := materializer.Materialize(ctx, testWorkspaceID, plan.ID, MaterializeInput{ApprovalID: approval.ID})
			return err
		}},
		{"reconcile preview", func() error { _, err := reconciler.Preview(ctx, "", ""); return err }},
		{"reconcile confirm", func() error { _, err := reconciler.Confirm(ctx, "", "", ConfirmInput{}); return err }},
		{"reconcile authorize", func() error { _, _, err := reconciler.Authorize(ctx, "", ""); return err }},
		{"reconcile apply", func() error { return reconciler.Apply(ctx, "", "", nil, nil) }},
		{"gates", func() error { _, err := executor.Gates(ctx, &Plan{}, workspace.Task{}); return err }},
		{"start", func() error { _, err := executor.Start(ctx, "", "", StartInput{}); return err }},
		{"pause", func() error { _, err := executor.Pause(ctx, "", "", PauseInput{}); return err }},
		{"resume", func() error { _, err := executor.Resume(ctx, "", "", ""); return err }},
		{"preview cancel", func() error { _, err := executor.PreviewCancel(ctx, "", ""); return err }},
		{"cancel", func() error { _, err := executor.Cancel(ctx, "", "", "", ""); return err }},
		{"retry", func() error { _, err := executor.Retry(ctx, "", "", "", ""); return err }},
		{"skip", func() error { _, err := executor.Skip(ctx, "", "", "", SkipInput{}); return err }},
		{"complete", func() error { _, err := executor.Complete(ctx, "", "", ""); return err }},
		{"fail", func() error { _, err := executor.Fail(ctx, "", "", "", ""); return err }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.call(); !errors.Is(err, resetstate.ErrWorkFenced) {
				t.Fatalf("error = %v, want ErrWorkFenced", err)
			}
		})
	}
}
