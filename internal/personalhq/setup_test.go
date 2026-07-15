package personalhq

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/session"
)

// fakeCreator simulates sessionhttp.Handler.CreateFromTemplate without
// standing up a full workspace/session store: it creates a bare workspace
// record directly through the same session store the test harness already
// uses, so Designate (which reads it back) sees a real, eligible workspace.
type fakeCreator struct {
	workspaces *session.SQLiteStore
	nextErr    error
	created    []string // names of workspaces created, in order
}

func (f *fakeCreator) CreateFromTemplate(ctx context.Context, name, templateID string) (string, error) {
	if f.nextErr != nil {
		err := f.nextErr
		f.nextErr = nil
		return "", err
	}
	f.created = append(f.created, name)
	id := "hq-" + name
	ws := &session.Workspace{
		ID:          id,
		Name:        name,
		Kind:        session.WorkspaceKindWorkspace,
		OwnerUserID: "local",
		Status:      session.WorkspaceStatusActive,
	}
	if err := f.workspaces.CreateWorkspace(ctx, ws); err != nil {
		return "", err
	}
	return id, nil
}

func newSetupTestHarness(t *testing.T) (*SetupCoordinator, *Service, *session.SQLiteStore, *fakeCreator) {
	t.Helper()
	svc, _, workspaces := newTestHarness(t)
	creator := &fakeCreator{workspaces: workspaces}
	coord := NewSetupCoordinator(svc, creator, workspaces)
	return coord, svc, workspaces, creator
}

func TestSetup_DefaultsCompleteWithNameOnly(t *testing.T) {
	coord, svc, _, _ := newSetupTestHarness(t)
	ctx := context.Background()

	result, err := coord.Setup(ctx, "local", SetupRequest{Name: "My HQ"})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if result.Status == nil || !result.Status.Valid {
		t.Fatalf("expected a valid designated HQ, got %#v", result.Status)
	}
	if result.BriefConfig.Timezone != "UTC" {
		t.Fatalf("expected default timezone UTC, got %q", result.BriefConfig.Timezone)
	}
	if len(result.BriefConfig.ScheduleDays) != 5 {
		t.Fatalf("expected 5 default schedule days, got %v", result.BriefConfig.ScheduleDays)
	}
	if result.BriefConfig.ScheduleTime != "08:00" {
		t.Fatalf("expected default schedule time 08:00, got %q", result.BriefConfig.ScheduleTime)
	}
	if result.BriefConfig.Scope != "all" {
		t.Fatalf("expected default scope all, got %q", result.BriefConfig.Scope)
	}

	status, err := svc.Status(ctx, "local")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.OnboardingState != "completed" {
		t.Fatalf("expected onboarding state completed, got %q", status.OnboardingState)
	}
}

func TestSetup_DefaultsNameWhenBlank(t *testing.T) {
	coord, _, _, creator := newSetupTestHarness(t)
	if _, err := coord.Setup(context.Background(), "local", SetupRequest{}); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if len(creator.created) != 1 || creator.created[0] != "My HQ" {
		t.Fatalf("expected default workspace name 'My HQ', got %v", creator.created)
	}
}

func TestSetup_RejectsInvalidTimezone(t *testing.T) {
	coord, _, _, creator := newSetupTestHarness(t)
	_, err := coord.Setup(context.Background(), "local", SetupRequest{Name: "My HQ", Timezone: "Not/AZone"})
	if !errors.Is(err, ErrInvalidTimezone) {
		t.Fatalf("expected ErrInvalidTimezone, got %v", err)
	}
	if len(creator.created) != 0 {
		t.Fatal("workspace must not be created when validation fails before creation")
	}
}

func TestSetup_AcceptsValidTimezone(t *testing.T) {
	coord, _, _, _ := newSetupTestHarness(t)
	result, err := coord.Setup(context.Background(), "local", SetupRequest{Name: "My HQ", Timezone: "America/New_York"})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if result.BriefConfig.Timezone != "America/New_York" {
		t.Fatalf("expected timezone to round-trip, got %q", result.BriefConfig.Timezone)
	}
}

func TestSetup_PreservesCustomScheduleAndScope(t *testing.T) {
	coord, _, _, _ := newSetupTestHarness(t)
	result, err := coord.Setup(context.Background(), "local", SetupRequest{
		Name:                    "My HQ",
		ScheduleDays:            []string{"Mon", " Wed ", "FRI"},
		ScheduleTime:            "07:30",
		Scope:                   "selected",
		SelectedWorkspaceIDs:    []string{"ws-a", "ws-b"},
		IncludeFutureWorkspaces: true,
		NotifyOnReady:           true,
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	b := result.BriefConfig
	if want := []string{"mon", "wed", "fri"}; !equalStrings(b.ScheduleDays, want) {
		t.Fatalf("schedule days = %v, want %v", b.ScheduleDays, want)
	}
	if b.ScheduleTime != "07:30" {
		t.Fatalf("schedule time = %q, want 07:30", b.ScheduleTime)
	}
	if b.Scope != "selected" {
		t.Fatalf("scope = %q, want selected", b.Scope)
	}
	if !equalStrings(b.SelectedWorkspaceIDs, []string{"ws-a", "ws-b"}) {
		t.Fatalf("selected workspace ids = %v", b.SelectedWorkspaceIDs)
	}
	if !b.IncludeFutureWorkspaces || !b.NotifyOnReady {
		t.Fatalf("expected both opt-in flags preserved, got %#v", b)
	}
}

// TestSetup_PersistsBriefConfigOntoWorkspace covers task 4.6: the brief
// configuration collected during setup must actually be stored somewhere
// durable (workspace SharedData, pending task 5.0's dedicated store), not
// discarded after the response is returned.
func TestSetup_PersistsBriefConfigOntoWorkspace(t *testing.T) {
	coord, _, workspaces, _ := newSetupTestHarness(t)
	ctx := context.Background()

	result, err := coord.Setup(ctx, "local", SetupRequest{Name: "My HQ", Timezone: "America/Chicago"})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	ws, err := workspaces.GetWorkspace(ctx, result.Status.WorkspaceID)
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	raw, ok := ws.SharedData[briefConfigSharedDataKey]
	if !ok {
		t.Fatal("expected brief config stored in workspace SharedData")
	}
	stored, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("expected a map, got %T", raw)
	}
	if stored["timezone"] != "America/Chicago" {
		t.Fatalf("stored timezone = %v, want America/Chicago", stored["timezone"])
	}
}

// TestSetup_CreationFailureLeavesNoDesignation covers PRD FR29: a failed
// creation must never leave a designation pointing at a missing workspace.
func TestSetup_CreationFailureLeavesNoDesignation(t *testing.T) {
	coord, svc, _, creator := newSetupTestHarness(t)
	creator.nextErr = errors.New("disk full")

	if _, err := coord.Setup(context.Background(), "local", SetupRequest{Name: "My HQ"}); err == nil {
		t.Fatal("expected Setup to fail")
	}
	status, err := svc.Status(context.Background(), "local")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.HasDesignation() {
		t.Fatalf("expected no designation after a failed creation, got %#v", status)
	}
}

// TestSetup_DesignationFailureReportsPartialFailure covers PRD FR30: if
// workspace creation succeeds but designation fails, the caller must learn
// the workspace ID so it can retry or keep the workspace as a normal one —
// not just get a bare error.
func TestSetup_DesignationFailureReportsPartialFailure(t *testing.T) {
	coord, svc, _, creator := newSetupTestHarness(t)
	ctx := context.Background()

	// Pre-designate a different, valid workspace so the coordinator's
	// Designate call is rejected with ErrAlreadyDesignated, simulating a
	// designation-step failure after workspace creation already succeeded.
	existing := &fakeCreator{workspaces: creator.workspaces}
	otherID, err := existing.CreateFromTemplate(ctx, "Other", "personal-ops")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Designate(ctx, "local", otherID); err != nil {
		t.Fatal(err)
	}

	_, err = coord.Setup(ctx, "local", SetupRequest{Name: "My HQ"})
	var partial *SetupPartialFailureError
	if !errors.As(err, &partial) {
		t.Fatalf("expected SetupPartialFailureError, got %v", err)
	}
	if partial.WorkspaceID == "" {
		t.Fatal("expected the partial failure to name the created workspace id")
	}

	// The workspace from the failed setup attempt must still exist (never
	// silently deleted) even though it isn't designated.
	ws, err := creator.workspaces.GetWorkspace(ctx, partial.WorkspaceID)
	if err != nil {
		t.Fatalf("the created workspace must survive a designation failure: %v", err)
	}
	if ws.Name != "My HQ" {
		t.Fatalf("unexpected workspace: %#v", ws)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
