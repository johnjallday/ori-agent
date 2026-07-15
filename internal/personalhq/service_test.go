package personalhq

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

// testHarness wires a real userprofile.SQLiteStore and session.SQLiteStore
// against the same in-memory database, matching how the service is wired in
// production (internal/server/builder_handlers.go), rather than hand-rolled
// fakes that could drift from the real persistence contracts.
func newTestHarness(t *testing.T) (*Service, *userprofile.SQLiteStore, *session.SQLiteStore) {
	t.Helper()
	db, err := database.Open(context.Background(), &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	profiles := userprofile.NewSQLiteStore(db)
	workspaces := session.NewSQLiteStore(db)
	return NewService(profiles, workspaces), profiles, workspaces
}

func createWorkspace(t *testing.T, store *session.SQLiteStore, id, owner string, kind session.WorkspaceKind, status session.WorkspaceStatus) *session.Workspace {
	t.Helper()
	ws := &session.Workspace{
		ID:          id,
		Name:        id,
		Kind:        kind,
		OwnerUserID: owner,
		Status:      status,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := store.CreateWorkspace(context.Background(), ws); err != nil {
		t.Fatalf("CreateWorkspace(%s): %v", id, err)
	}
	return ws
}

func TestStatusReportsNoDesignationByDefault(t *testing.T) {
	svc, _, _ := newTestHarness(t)
	ctx := context.Background()

	status, err := svc.Status(ctx, "local")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.HasDesignation() {
		t.Fatalf("expected no designation, got %#v", status)
	}
	if status.Valid {
		t.Fatalf("expected Valid=false with no designation, got %#v", status)
	}
	if status.NeedsRepair() {
		t.Fatalf("no designation should not need repair, got %#v", status)
	}
	if status.OnboardingState != userprofile.HQOnboardingUnseen {
		t.Fatalf("expected unseen onboarding state, got %q", status.OnboardingState)
	}
}

func TestDesignateThenStatusResolvesWorkspace(t *testing.T) {
	svc, _, workspaces := newTestHarness(t)
	ctx := context.Background()
	createWorkspace(t, workspaces, "ws-1", "local", session.WorkspaceKindWorkspace, session.WorkspaceStatusActive)

	status, err := svc.Designate(ctx, "local", "ws-1")
	if err != nil {
		t.Fatalf("Designate: %v", err)
	}
	if !status.Valid || status.Workspace == nil || status.Workspace.ID != "ws-1" {
		t.Fatalf("expected valid resolved designation, got %#v", status)
	}

	// Renaming the workspace must not affect the designation: the relation
	// is keyed by stable ID, never by display name (FR2, FR5).
	ws, err := workspaces.GetWorkspace(ctx, "ws-1")
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	ws.Name = "Renamed HQ"
	if err := workspaces.UpdateWorkspace(ctx, ws); err != nil {
		t.Fatalf("UpdateWorkspace: %v", err)
	}
	status, err = svc.Status(ctx, "local")
	if err != nil {
		t.Fatalf("Status after rename: %v", err)
	}
	if !status.Valid || status.Workspace.Name != "Renamed HQ" {
		t.Fatalf("expected designation to survive rename, got %#v", status)
	}
}

func TestDesignateRejectsSecondDesignationWithoutReplace(t *testing.T) {
	svc, _, workspaces := newTestHarness(t)
	ctx := context.Background()
	createWorkspace(t, workspaces, "ws-1", "local", session.WorkspaceKindWorkspace, session.WorkspaceStatusActive)
	createWorkspace(t, workspaces, "ws-2", "local", session.WorkspaceKindWorkspace, session.WorkspaceStatusActive)

	if _, err := svc.Designate(ctx, "local", "ws-1"); err != nil {
		t.Fatalf("first Designate: %v", err)
	}
	if _, err := svc.Designate(ctx, "local", "ws-2"); !errors.Is(err, ErrAlreadyDesignated) {
		t.Fatalf("expected ErrAlreadyDesignated, got %v", err)
	}
}

func TestReplaceIsAtomicZeroOrOne(t *testing.T) {
	svc, _, workspaces := newTestHarness(t)
	ctx := context.Background()
	createWorkspace(t, workspaces, "ws-1", "local", session.WorkspaceKindWorkspace, session.WorkspaceStatusActive)
	createWorkspace(t, workspaces, "ws-2", "local", session.WorkspaceKindWorkspace, session.WorkspaceStatusActive)

	if _, err := svc.Designate(ctx, "local", "ws-1"); err != nil {
		t.Fatalf("Designate: %v", err)
	}
	status, err := svc.Replace(ctx, "local", "ws-2")
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if status.WorkspaceID != "ws-2" {
		t.Fatalf("expected replacement to designate ws-2, got %#v", status)
	}

	// Replace also works from a zero-HQ state (no prior designation).
	if _, err := svc.Clear(ctx, "local"); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	status, err = svc.Replace(ctx, "local", "ws-1")
	if err != nil {
		t.Fatalf("Replace from zero state: %v", err)
	}
	if status.WorkspaceID != "ws-1" {
		t.Fatalf("expected replace-from-zero to designate ws-1, got %#v", status)
	}
}

// TestReplaceLeavesFormerHQContentUntouched covers FR38/FR40 (subtask 1.5):
// replacing the designation must change only the user->workspace relation.
// The former HQ workspace itself (its tasks and everything else it owns) is
// a normal workspace that the service never mutates or deletes.
func TestReplaceLeavesFormerHQContentUntouched(t *testing.T) {
	svc, _, workspaces := newTestHarness(t)
	ctx := context.Background()
	former := createWorkspace(t, workspaces, "ws-former", "local", session.WorkspaceKindWorkspace, session.WorkspaceStatusActive)
	former.SharedData = map[string]any{"note": "keep me"}
	if err := workspaces.UpdateWorkspace(ctx, former); err != nil {
		t.Fatalf("seed former HQ content: %v", err)
	}
	createWorkspace(t, workspaces, "ws-new", "local", session.WorkspaceKindWorkspace, session.WorkspaceStatusActive)

	if _, err := svc.Designate(ctx, "local", "ws-former"); err != nil {
		t.Fatalf("Designate: %v", err)
	}
	if _, err := svc.Replace(ctx, "local", "ws-new"); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	reloaded, err := workspaces.GetWorkspace(ctx, "ws-former")
	if err != nil {
		t.Fatalf("former HQ workspace must still exist: %v", err)
	}
	if reloaded.Status != session.WorkspaceStatusActive {
		t.Fatalf("former HQ workspace status must be unchanged, got %q", reloaded.Status)
	}
	if reloaded.SharedData["note"] != "keep me" {
		t.Fatalf("former HQ workspace content must be untouched, got %#v", reloaded.SharedData)
	}
}

func TestClearPreservesWorkspaceAndOnboardingState(t *testing.T) {
	svc, _, workspaces := newTestHarness(t)
	ctx := context.Background()
	createWorkspace(t, workspaces, "ws-1", "local", session.WorkspaceKindWorkspace, session.WorkspaceStatusActive)

	if _, err := svc.Designate(ctx, "local", "ws-1"); err != nil {
		t.Fatalf("Designate: %v", err)
	}
	if _, err := svc.SetOnboardingState(ctx, "local", userprofile.HQOnboardingCompleted); err != nil {
		t.Fatalf("SetOnboardingState: %v", err)
	}

	status, err := svc.Clear(ctx, "local")
	if err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if status.HasDesignation() {
		t.Fatalf("expected designation cleared, got %#v", status)
	}
	if status.OnboardingState != userprofile.HQOnboardingCompleted {
		t.Fatalf("expected onboarding state preserved through clear, got %q", status.OnboardingState)
	}

	// The workspace itself must be untouched.
	ws, err := workspaces.GetWorkspace(ctx, "ws-1")
	if err != nil {
		t.Fatalf("workspace should still exist after clear: %v", err)
	}
	if ws.Status != session.WorkspaceStatusActive {
		t.Fatalf("expected workspace status unchanged, got %q", ws.Status)
	}
}

func TestDesignateRejectsGroupWorkspace(t *testing.T) {
	svc, _, workspaces := newTestHarness(t)
	ctx := context.Background()
	createWorkspace(t, workspaces, "group-1", "local", session.WorkspaceKindGroup, session.WorkspaceStatusActive)

	if _, err := svc.Designate(ctx, "local", "group-1"); !errors.Is(err, ErrGroupNotEligible) {
		t.Fatalf("expected ErrGroupNotEligible, got %v", err)
	}
}

func TestDesignateRejectsTrashedOrMissingWorkspace(t *testing.T) {
	svc, _, workspaces := newTestHarness(t)
	ctx := context.Background()
	createWorkspace(t, workspaces, "trashed-1", "local", session.WorkspaceKindWorkspace, session.WorkspaceStatusTrashed)
	createWorkspace(t, workspaces, "missing-1", "local", session.WorkspaceKindWorkspace, session.WorkspaceStatusMissing)

	if _, err := svc.Designate(ctx, "local", "trashed-1"); !errors.Is(err, ErrWorkspaceUnavailable) {
		t.Fatalf("expected ErrWorkspaceUnavailable for trashed workspace, got %v", err)
	}
	if _, err := svc.Designate(ctx, "local", "missing-1"); !errors.Is(err, ErrWorkspaceUnavailable) {
		t.Fatalf("expected ErrWorkspaceUnavailable for missing workspace, got %v", err)
	}
}

func TestDesignateRejectsWrongOwner(t *testing.T) {
	svc, profiles, workspaces := newTestHarness(t)
	ctx := context.Background()
	// owner_user_id carries a foreign key to users(id), so the owning user
	// must exist before a workspace can reference it.
	if err := profiles.Upsert(ctx, &userprofile.UserProfile{ID: "someone-else"}); err != nil {
		t.Fatalf("seed owner profile: %v", err)
	}
	createWorkspace(t, workspaces, "ws-1", "someone-else", session.WorkspaceKindWorkspace, session.WorkspaceStatusActive)

	if _, err := svc.Designate(ctx, "local", "ws-1"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
}

func TestDesignateRejectsUnknownWorkspace(t *testing.T) {
	svc, _, _ := newTestHarness(t)
	ctx := context.Background()

	if _, err := svc.Designate(ctx, "local", "does-not-exist"); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("expected ErrWorkspaceNotFound, got %v", err)
	}
}

func TestDesignateRequiresWorkspaceID(t *testing.T) {
	svc, _, _ := newTestHarness(t)
	ctx := context.Background()

	if _, err := svc.Designate(ctx, "local", "   "); !errors.Is(err, ErrWorkspaceIDRequired) {
		t.Fatalf("expected ErrWorkspaceIDRequired, got %v", err)
	}
}

// TestStatusReportsInvalidReasonAfterWorkspaceDeletion covers requirement
// 1.7/1.9: deleting the designated workspace out from under a stored
// reference must be detected lazily on read (no delete-time hook is wired
// into workspace_handler.go on purpose, so deletion never depends on the
// personal HQ / user-profile stores being available), and Status must
// report it as needing repair rather than erroring.
func TestStatusReportsInvalidReasonAfterWorkspaceDeletion(t *testing.T) {
	svc, _, workspaces := newTestHarness(t)
	ctx := context.Background()
	createWorkspace(t, workspaces, "ws-1", "local", session.WorkspaceKindWorkspace, session.WorkspaceStatusActive)

	if _, err := svc.Designate(ctx, "local", "ws-1"); err != nil {
		t.Fatalf("Designate: %v", err)
	}
	if err := workspaces.DeleteWorkspace(ctx, "ws-1"); err != nil {
		t.Fatalf("DeleteWorkspace: %v", err)
	}

	status, err := svc.Status(ctx, "local")
	if err != nil {
		t.Fatalf("Status after deletion should not error: %v", err)
	}
	if status.Valid {
		t.Fatalf("expected invalid status after deletion, got %#v", status)
	}
	if !status.NeedsRepair() {
		t.Fatalf("expected NeedsRepair after deletion, got %#v", status)
	}
	if status.InvalidReason != InvalidReasonMissing {
		t.Fatalf("expected missing reason, got %q", status.InvalidReason)
	}

	// Clear (one of the offered repair actions) removes the stale reference.
	status, err = svc.Clear(ctx, "local")
	if err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if status.HasDesignation() {
		t.Fatalf("expected designation cleared, got %#v", status)
	}
}

// TestStatusReportsInvalidReasonAfterWorkspaceTrashed covers task 8.3: a
// designated HQ that is later trashed (not deleted outright) must be
// reported as needing repair, not silently treated as still valid.
func TestStatusReportsInvalidReasonAfterWorkspaceTrashed(t *testing.T) {
	svc, _, workspaces := newTestHarness(t)
	ctx := context.Background()
	createWorkspace(t, workspaces, "ws-1", "local", session.WorkspaceKindWorkspace, session.WorkspaceStatusActive)

	if _, err := svc.Designate(ctx, "local", "ws-1"); err != nil {
		t.Fatalf("Designate: %v", err)
	}
	ws, err := workspaces.GetWorkspace(ctx, "ws-1")
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	ws.Status = session.WorkspaceStatusTrashed
	if err := workspaces.UpdateWorkspace(ctx, ws); err != nil {
		t.Fatalf("UpdateWorkspace (trash): %v", err)
	}

	status, err := svc.Status(ctx, "local")
	if err != nil {
		t.Fatalf("Status after trashing should not error: %v", err)
	}
	if status.Valid || !status.NeedsRepair() {
		t.Fatalf("expected NeedsRepair after trashing the designated HQ, got %#v", status)
	}
	if status.InvalidReason != InvalidReasonTrashed {
		t.Fatalf("expected trashed reason, got %q", status.InvalidReason)
	}
}

// TestStatusReportsInvalidReasonAfterWorkspaceBecomesGroup covers task 8.3:
// a designated HQ workspace that is later converted into a group is no
// longer eligible (groups can contain member workspaces) and must be
// reported as needing repair.
func TestStatusReportsInvalidReasonAfterWorkspaceBecomesGroup(t *testing.T) {
	svc, _, workspaces := newTestHarness(t)
	ctx := context.Background()
	createWorkspace(t, workspaces, "ws-1", "local", session.WorkspaceKindWorkspace, session.WorkspaceStatusActive)

	if _, err := svc.Designate(ctx, "local", "ws-1"); err != nil {
		t.Fatalf("Designate: %v", err)
	}
	ws, err := workspaces.GetWorkspace(ctx, "ws-1")
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	ws.Kind = session.WorkspaceKindGroup
	if err := workspaces.UpdateWorkspace(ctx, ws); err != nil {
		t.Fatalf("UpdateWorkspace (convert to group): %v", err)
	}

	status, err := svc.Status(ctx, "local")
	if err != nil {
		t.Fatalf("Status after conversion to group should not error: %v", err)
	}
	if status.Valid || !status.NeedsRepair() {
		t.Fatalf("expected NeedsRepair after the designated HQ became a group, got %#v", status)
	}
	if status.InvalidReason != InvalidReasonGroup {
		t.Fatalf("expected group reason, got %q", status.InvalidReason)
	}
}

// TestStatusReportsInvalidReasonAfterOwnershipChanges covers task 8.3: a
// designated HQ whose ownership later changes away from the designating
// user must be reported as inaccessible/needing repair, not silently
// resolved as if still valid.
func TestStatusReportsInvalidReasonAfterOwnershipChanges(t *testing.T) {
	svc, profiles, workspaces := newTestHarness(t)
	ctx := context.Background()
	createWorkspace(t, workspaces, "ws-1", "local", session.WorkspaceKindWorkspace, session.WorkspaceStatusActive)

	if _, err := svc.Designate(ctx, "local", "ws-1"); err != nil {
		t.Fatalf("Designate: %v", err)
	}
	// owner_user_id has a FOREIGN KEY on users(id), so the new owner needs a
	// real profile row first.
	if err := profiles.Upsert(ctx, &userprofile.UserProfile{ID: "someone-else"}); err != nil {
		t.Fatalf("seed someone-else profile: %v", err)
	}
	ws, err := workspaces.GetWorkspace(ctx, "ws-1")
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	ws.OwnerUserID = "someone-else"
	if err := workspaces.UpdateWorkspace(ctx, ws); err != nil {
		t.Fatalf("UpdateWorkspace (change owner): %v", err)
	}

	status, err := svc.Status(ctx, "local")
	if err != nil {
		t.Fatalf("Status after ownership change should not error: %v", err)
	}
	if status.Valid || !status.NeedsRepair() {
		t.Fatalf("expected NeedsRepair after ownership changed away, got %#v", status)
	}
	if status.InvalidReason != InvalidReasonWrongOwner {
		t.Fatalf("expected wrong_owner reason, got %q", status.InvalidReason)
	}
}

func TestSetOnboardingStateRejectsUnknownValue(t *testing.T) {
	svc, _, _ := newTestHarness(t)
	ctx := context.Background()

	if _, err := svc.SetOnboardingState(ctx, "local", userprofile.HQOnboardingState("bogus")); !errors.Is(err, ErrInvalidOnboardingState) {
		t.Fatalf("expected ErrInvalidOnboardingState, got %v", err)
	}
}

func TestOnboardingStateTransitionsSurviveDesignationChanges(t *testing.T) {
	svc, _, workspaces := newTestHarness(t)
	ctx := context.Background()
	createWorkspace(t, workspaces, "ws-1", "local", session.WorkspaceKindWorkspace, session.WorkspaceStatusActive)

	if _, err := svc.SetOnboardingState(ctx, "local", userprofile.HQOnboardingSkipped); err != nil {
		t.Fatalf("SetOnboardingState skipped: %v", err)
	}
	status, err := svc.Designate(ctx, "local", "ws-1")
	if err != nil {
		t.Fatalf("Designate after skip (resume path): %v", err)
	}
	// Designating does not implicitly flip onboarding state; callers (task
	// 4.x HTTP/UI layer) decide when to mark completed.
	if status.OnboardingState != userprofile.HQOnboardingSkipped {
		t.Fatalf("expected onboarding state unchanged by Designate, got %q", status.OnboardingState)
	}

	if _, err := svc.SetOnboardingState(ctx, "local", userprofile.HQOnboardingCompleted); err != nil {
		t.Fatalf("SetOnboardingState completed: %v", err)
	}
	status, err = svc.Status(ctx, "local")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.OnboardingState != userprofile.HQOnboardingCompleted || !status.Valid {
		t.Fatalf("expected completed onboarding state with valid HQ, got %#v", status)
	}
}
