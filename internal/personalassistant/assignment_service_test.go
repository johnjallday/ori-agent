package personalassistant

import (
	"context"
	"errors"
	"testing"
)

func previewInput(title string) AssignmentInput {
	return AssignmentInput{Rows: []AssignmentInputRow{{Type: AssignmentRowPriority, Title: title}}}
}

func TestAssignmentService_PreviewSupersedesUnappliedJournalAtomically(t *testing.T) {
	ctx := context.Background()
	store, db := newTestStore(t)
	state, err := store.CreateState(ctx, activeTestState("local", "assistant-1"))
	if err != nil {
		t.Fatalf("CreateState: %v", err)
	}
	service := NewAssignmentService(store)

	first, err := service.Preview(ctx, "local", state.StateVersion, previewInput("First priority"))
	if err != nil {
		t.Fatalf("first Preview: %v", err)
	}
	if first.Preview.AssignmentVersion != 1 || first.StateVersion != 2 || first.Status != FirstAssignmentPreviewed {
		t.Fatalf("first result = %#v", first)
	}
	second, err := service.Preview(ctx, "local", first.StateVersion, previewInput("Edited priority"))
	if err != nil {
		t.Fatalf("second Preview: %v", err)
	}
	if second.Preview.AssignmentVersion != 2 || second.StateVersion != 3 ||
		second.Preview.PreviewID == first.Preview.PreviewID || second.Preview.Items[0].Title != "Edited priority" {
		t.Fatalf("second result = %#v", second)
	}
	old, err := store.GetAssignment(ctx, "local", first.Preview.PreviewID)
	if err != nil || old.Status != AssignmentSuperseded || old.AssignmentVersion != 2 {
		t.Fatalf("superseded preview = %#v, %v", old, err)
	}
	latest, err := store.GetLatestAssignment(ctx, "local", "assistant-1")
	if err != nil || latest.PreviewID != second.Preview.PreviewID || latest.Status != AssignmentPreviewed {
		t.Fatalf("latest = %#v, %v", latest, err)
	}
	var applicable int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM personal_assistant_assignment WHERE status != 'superseded'`).Scan(&applicable); err != nil || applicable != 1 {
		t.Fatalf("applicable previews = %d, %v", applicable, err)
	}
}

func TestAssignmentService_StalePreviewReturnsCurrentVersionWithoutWriting(t *testing.T) {
	ctx := context.Background()
	store, db := newTestStore(t)
	state, err := store.CreateState(ctx, activeTestState("local", "assistant-1"))
	if err != nil {
		t.Fatal(err)
	}
	service := NewAssignmentService(store)
	current, err := service.Preview(ctx, "local", state.StateVersion, previewInput("Current"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.Preview(ctx, "local", state.StateVersion, previewInput("Stale edit"))
	var conflict *AssignmentPreviewConflictError
	if !errors.As(err, &conflict) || conflict.StateVersion != current.StateVersion ||
		conflict.Preview == nil || conflict.Preview.PreviewID != current.Preview.PreviewID {
		t.Fatalf("stale error = %#v", err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM personal_assistant_assignment`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("assignment count = %d, %v", count, err)
	}
}

func TestAssignmentService_AllEmptyPreviewPersistsAndApplyingCannotBeSuperseded(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	state, err := store.CreateState(ctx, activeTestState("local", "assistant-1"))
	if err != nil {
		t.Fatal(err)
	}
	service := NewAssignmentService(store)
	empty, err := service.Preview(ctx, "local", state.StateVersion, AssignmentInput{})
	if err != nil || empty.Preview.Count != 0 {
		t.Fatalf("empty Preview = %#v, %v", empty, err)
	}
	assignment, err := store.GetAssignment(ctx, "local", empty.Preview.PreviewID)
	if err != nil {
		t.Fatal(err)
	}
	assignment.Status = AssignmentApplying
	if _, err := store.UpdateAssignment(ctx, assignment, assignment.AssignmentVersion); err != nil {
		t.Fatal(err)
	}
	currentState, err := store.GetState(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Preview(ctx, "local", currentState.StateVersion, previewInput("Too late")); !errors.Is(err, ErrConflict) {
		t.Fatalf("applying supersede error = %v", err)
	}
}

func TestAssignmentService_RequiresActiveCurrentRelationshipBeforeValidationWrite(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	state := activeTestState("local", "assistant-1")
	state.Status = StatusPaused
	created, err := store.CreateState(ctx, state)
	if err != nil {
		t.Fatal(err)
	}
	service := NewAssignmentService(store)
	if _, err := service.Preview(ctx, "local", created.StateVersion, previewInput("No write")); !errors.Is(err, ErrConflict) {
		t.Fatalf("paused Preview error = %v", err)
	}
	if _, err := store.GetLatestAssignment(ctx, "local", "assistant-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("paused relationship wrote a preview: %v", err)
	}
}
