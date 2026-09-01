package personalassistant

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
)

func newTestStore(t *testing.T) (*SQLiteStore, *database.DB) {
	t.Helper()
	db, err := database.Open(context.Background(), &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewSQLiteStore(db), db
}

func activeTestState(userID, assistantID string) *State {
	state := NewState(userID)
	state.AssistantID = assistantID
	state.Status = StatusActive
	state.DisplayName = "Ada"
	state.HQWorkspaceID = "hq-" + userID
	state.HQEntryAgentInstanceID = "instance-" + userID
	state.GlobalAgentProfileName = "Ada"
	state.Mandate = "Keep the important work visible."
	state.FocusAreas = []FocusArea{FocusPlanMyDay, FocusKeepProjectsMoving}
	state.FirstAssignmentStatus = FirstAssignmentPreviewed
	hiredAt := time.Now().UTC().Round(0)
	state.HiredAt = &hiredAt
	return state
}

func TestSQLiteStore_StateRoundTripAndDefensiveCopies(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	created, err := store.CreateState(ctx, activeTestState("user-a", "assistant-a"))
	if err != nil {
		t.Fatalf("CreateState: %v", err)
	}
	if created.StateVersion != 1 || created.Status != StatusActive || created.DisplayName != "Ada" {
		t.Fatalf("created state = %#v", created)
	}

	created.FocusAreas[0] = FocusHelpWithEmail
	created.Appearance.Generated.Color = "#ffffff"
	loaded, err := store.GetState(ctx, "user-a")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if loaded.FocusAreas[0] != FocusPlanMyDay {
		t.Fatalf("stored focus mutated through returned slice: %v", loaded.FocusAreas)
	}
	if loaded.Appearance.Generated.Color == "#ffffff" {
		t.Fatal("stored appearance mutated through returned pointer")
	}

	loaded.DisplayName = "Grace"
	updated, err := store.UpdateState(ctx, loaded, loaded.StateVersion)
	if err != nil {
		t.Fatalf("UpdateState: %v", err)
	}
	if updated.StateVersion != 2 || updated.DisplayName != "Grace" || updated.AssistantID != "assistant-a" {
		t.Fatalf("updated state = %#v", updated)
	}
}

func TestSQLiteStore_ConcurrentStaleStateWritesExactlyOneWins(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	original, err := store.CreateState(ctx, activeTestState("local", "assistant-a"))
	if err != nil {
		t.Fatalf("CreateState: %v", err)
	}

	first := original.Clone()
	first.Mandate = "First update"
	second := original.Clone()
	second.Mandate = "Second update"
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, candidate := range []*State{first, second} {
		wg.Add(1)
		go func(state *State) {
			defer wg.Done()
			<-start
			_, updateErr := store.UpdateState(ctx, state, original.StateVersion)
			errs <- updateErr
		}(candidate)
	}
	close(start)
	wg.Wait()
	close(errs)

	var successes, conflicts int
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrConflict):
			conflicts++
		default:
			t.Fatalf("unexpected update error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d, want 1/1", successes, conflicts)
	}
}

func TestSQLiteStore_RejectsMalformedPersistedJSON(t *testing.T) {
	ctx := context.Background()
	store, db := newTestStore(t)
	if _, err := store.CreateState(ctx, activeTestState("local", "assistant-a")); err != nil {
		t.Fatalf("CreateState: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE personal_assistant_state SET focus_areas_json = '{broken' WHERE user_id = 'local'`); err != nil {
		t.Fatalf("corrupt persisted JSON: %v", err)
	}
	if _, err := store.GetState(ctx, "local"); err == nil {
		t.Fatal("expected malformed persisted JSON to fail closed")
	}
}

func TestSQLiteStore_EnforcesRelationshipUniqueness(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	if _, err := store.CreateState(ctx, activeTestState("user-a", "assistant-a")); err != nil {
		t.Fatalf("first CreateState: %v", err)
	}
	if _, err := store.CreateState(ctx, activeTestState("user-a", "assistant-b")); !errors.Is(err, ErrConflict) {
		t.Fatalf("same user error = %v, want conflict", err)
	}
	if _, err := store.CreateState(ctx, activeTestState("user-b", "assistant-a")); !errors.Is(err, ErrConflict) {
		t.Fatalf("same assistant error = %v, want conflict", err)
	}
}

func TestSQLiteStore_AssignmentRoundTripCASAndOwnership(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	for _, state := range []*State{
		activeTestState("user-a", "assistant-a"),
		activeTestState("user-b", "assistant-b"),
	} {
		if _, err := store.CreateState(ctx, state); err != nil {
			t.Fatalf("CreateState: %v", err)
		}
	}
	payload := json.RawMessage(`{"priorities":[{"title":"Review launch"}]}`)
	created, err := store.CreateAssignment(ctx, &Assignment{
		PreviewID: "preview-a", UserID: "user-a", AssistantID: "assistant-a",
		NormalizedPayload: payload, NormalizedPayloadHash: PayloadHash(payload),
		Status: AssignmentPreviewed,
	})
	if err != nil {
		t.Fatalf("CreateAssignment: %v", err)
	}
	if created.AssignmentVersion != 1 {
		t.Fatalf("assignment version = %d", created.AssignmentVersion)
	}
	if _, err := store.GetAssignment(ctx, "user-b", "preview-a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign GetAssignment error = %v, want not found", err)
	}

	created.Status = AssignmentCompleted
	created.CreatedCanonicalRefs = []CanonicalRef{{Kind: "ticket", WorkspaceID: "hq-user-a", ID: "ticket-1"}}
	updated, err := store.UpdateAssignment(ctx, created, 1)
	if err != nil {
		t.Fatalf("UpdateAssignment: %v", err)
	}
	if updated.AssignmentVersion != 2 || updated.Status != AssignmentCompleted || len(updated.CreatedCanonicalRefs) != 1 {
		t.Fatalf("updated assignment = %#v", updated)
	}
	updated.CreatedCanonicalRefs[0].ID = "changed"
	reloaded, err := store.GetAssignment(ctx, "user-a", "preview-a")
	if err != nil {
		t.Fatalf("reload assignment: %v", err)
	}
	if reloaded.CreatedCanonicalRefs[0].ID != "ticket-1" {
		t.Fatal("stored canonical refs mutated through returned slice")
	}
	if _, err := store.UpdateAssignment(ctx, created, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale assignment update error = %v, want conflict", err)
	}
}

func TestValidationRejectsUnknownEnumsOversizeAndSecrets(t *testing.T) {
	if _, err := NormalizeRelationshipStatus("retired"); err == nil {
		t.Fatal("unknown relationship status accepted")
	}
	if _, err := NormalizeFocusAreas([]string{"plan my day", "telepathy"}); err == nil {
		t.Fatal("unknown focus area accepted")
	}
	store, _ := newTestStore(t)
	state := activeTestState("local", "assistant-a")
	state.Mandate = "Use API key sk-abcdefghijklmnopqrstuvwxyz"
	if _, err := store.CreateState(context.Background(), state); err == nil {
		t.Fatal("secret-like mandate accepted")
	}
	state = activeTestState("local", "assistant-a")
	state.DisplayName = string(make([]byte, MaxDisplayNameLen+1))
	if _, err := store.CreateState(context.Background(), state); err == nil {
		t.Fatal("oversize display name accepted")
	}
}
