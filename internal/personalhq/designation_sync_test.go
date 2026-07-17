package personalhq

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/session"
)

// recordingSyncer captures the designation projections the service emits so
// tests can assert designate/replace/clear move the field between workspaces.
type recordingSyncer struct {
	calls []syncCall
	// current mirrors the latest designation written per workspace, so tests
	// can assert the net projected state after a sequence of transitions.
	current map[string]string
}

type syncCall struct {
	workspaceID string
	designation string
}

func newRecordingSyncer() *recordingSyncer {
	return &recordingSyncer{current: map[string]string{}}
}

func (r *recordingSyncer) SetWorkspaceDesignation(_ context.Context, workspaceID, designation string) error {
	r.calls = append(r.calls, syncCall{workspaceID: workspaceID, designation: designation})
	r.current[workspaceID] = designation
	return nil
}

const hqDesignation = string(session.WorkspaceDesignationPersonalHQ)

func TestDesignateProjectsDesignationOntoWorkspace(t *testing.T) {
	svc, _, workspaces := newTestHarness(t)
	ctx := context.Background()
	createWorkspace(t, workspaces, "ws-1", "local", session.WorkspaceKindWorkspace, session.WorkspaceStatusActive)

	syncer := newRecordingSyncer()
	svc.SetDesignationSyncer(syncer)

	if _, err := svc.Designate(ctx, "local", "ws-1"); err != nil {
		t.Fatalf("Designate: %v", err)
	}
	if got := syncer.current["ws-1"]; got != hqDesignation {
		t.Fatalf("expected ws-1 projected as %q, got %q", hqDesignation, got)
	}
}

func TestReplaceMovesDesignationProjection(t *testing.T) {
	svc, _, workspaces := newTestHarness(t)
	ctx := context.Background()
	createWorkspace(t, workspaces, "ws-1", "local", session.WorkspaceKindWorkspace, session.WorkspaceStatusActive)
	createWorkspace(t, workspaces, "ws-2", "local", session.WorkspaceKindWorkspace, session.WorkspaceStatusActive)

	syncer := newRecordingSyncer()
	svc.SetDesignationSyncer(syncer)

	if _, err := svc.Designate(ctx, "local", "ws-1"); err != nil {
		t.Fatalf("Designate: %v", err)
	}
	if _, err := svc.Replace(ctx, "local", "ws-2"); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	if got := syncer.current["ws-2"]; got != hqDesignation {
		t.Fatalf("expected ws-2 projected as %q, got %q", hqDesignation, got)
	}
	if got := syncer.current["ws-1"]; got != "" {
		t.Fatalf("expected ws-1 projection cleared after replace, got %q", got)
	}
}

func TestClearRemovesDesignationProjection(t *testing.T) {
	svc, _, workspaces := newTestHarness(t)
	ctx := context.Background()
	createWorkspace(t, workspaces, "ws-1", "local", session.WorkspaceKindWorkspace, session.WorkspaceStatusActive)

	syncer := newRecordingSyncer()
	svc.SetDesignationSyncer(syncer)

	if _, err := svc.Designate(ctx, "local", "ws-1"); err != nil {
		t.Fatalf("Designate: %v", err)
	}
	if _, err := svc.Clear(ctx, "local"); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if got := syncer.current["ws-1"]; got != "" {
		t.Fatalf("expected ws-1 projection cleared, got %q", got)
	}
}

func TestDesignatedWorkspaceIDsReflectsRecord(t *testing.T) {
	svc, _, workspaces := newTestHarness(t)
	ctx := context.Background()
	createWorkspace(t, workspaces, "ws-1", "local", session.WorkspaceKindWorkspace, session.WorkspaceStatusActive)

	ids, err := svc.DesignatedWorkspaceIDs(ctx)
	if err != nil {
		t.Fatalf("DesignatedWorkspaceIDs (empty): %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("expected no designated IDs before designation, got %#v", ids)
	}

	if _, err := svc.Designate(ctx, "local", "ws-1"); err != nil {
		t.Fatalf("Designate: %v", err)
	}
	ids, err = svc.DesignatedWorkspaceIDs(ctx)
	if err != nil {
		t.Fatalf("DesignatedWorkspaceIDs (designated): %v", err)
	}
	if !ids["ws-1"] || len(ids) != 1 {
		t.Fatalf("expected {ws-1:true}, got %#v", ids)
	}
}
