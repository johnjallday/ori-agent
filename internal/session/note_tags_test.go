package session

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func createNoteTagsTestWorkspace(t *testing.T, store HybridStore) *Workspace {
	t.Helper()
	workspace := &Workspace{ID: "note-tags-ws", Name: "Note Tags"}
	if err := store.CreateWorkspace(context.Background(), workspace); err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}
	return workspace
}

func TestNoteTags_CreateGetRoundTrip(t *testing.T) {
	store, cleanup := setupHybridStore(t, 10)
	defer cleanup()
	ctx := context.Background()
	ws := createNoteTagsTestWorkspace(t, store)

	now := time.Now()
	note := &WorkspaceNote{
		ID:          "note-1",
		WorkspaceID: ws.ID,
		Name:        "Tagged Note",
		Content:     "body",
		Tags:        []string{" Music ", "mixing", "music"},
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := store.CreateNote(ctx, note); err != nil {
		t.Fatalf("Failed to create note: %v", err)
	}

	got, err := store.GetNote(ctx, note.ID)
	if err != nil {
		t.Fatalf("Failed to get note: %v", err)
	}
	want := []string{"music", "mixing"}
	if !noteTagSetsEqual(got.Tags, want) {
		t.Fatalf("Tags mismatch: got %v, want %v", got.Tags, want)
	}
}

func TestNoteTags_UpdateReplacesTags(t *testing.T) {
	store, cleanup := setupHybridStore(t, 10)
	defer cleanup()
	ctx := context.Background()
	ws := createNoteTagsTestWorkspace(t, store)

	now := time.Now()
	note := &WorkspaceNote{
		ID: "note-2", WorkspaceID: ws.ID, Name: "N", Content: "c",
		Tags: []string{"old", "stale"}, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateNote(ctx, note); err != nil {
		t.Fatalf("Failed to create note: %v", err)
	}

	note.Tags = []string{"fresh"}
	note.UpdatedAt = time.Now()
	if err := store.UpdateNote(ctx, note); err != nil {
		t.Fatalf("Failed to update note: %v", err)
	}

	got, err := store.GetNote(ctx, note.ID)
	if err != nil {
		t.Fatalf("Failed to get note: %v", err)
	}
	if !reflect.DeepEqual(got.Tags, []string{"fresh"}) {
		t.Fatalf("Tags mismatch after update: got %v", got.Tags)
	}

	// Clearing tags persists too.
	note.Tags = nil
	if err := store.UpdateNote(ctx, note); err != nil {
		t.Fatalf("Failed to clear tags: %v", err)
	}
	got, err = store.GetNote(ctx, note.ID)
	if err != nil {
		t.Fatalf("Failed to get note: %v", err)
	}
	if len(got.Tags) != 0 {
		t.Fatalf("Expected no tags after clearing, got %v", got.Tags)
	}
}

func TestNoteTags_ListHydratesTags(t *testing.T) {
	store, cleanup := setupHybridStore(t, 10)
	defer cleanup()
	ctx := context.Background()
	ws := createNoteTagsTestWorkspace(t, store)

	now := time.Now()
	tagged := &WorkspaceNote{
		ID: "note-3", WorkspaceID: ws.ID, Name: "Tagged", Content: "c",
		Tags: []string{"alpha", "beta"}, CreatedAt: now, UpdatedAt: now,
	}
	plain := &WorkspaceNote{
		ID: "note-4", WorkspaceID: ws.ID, Name: "Plain", Content: "c",
		CreatedAt: now, UpdatedAt: now,
	}
	for _, n := range []*WorkspaceNote{tagged, plain} {
		if err := store.CreateNote(ctx, n); err != nil {
			t.Fatalf("Failed to create note %s: %v", n.ID, err)
		}
	}

	items, err := store.ListNotesByWorkspace(ctx, ws.ID)
	if err != nil {
		t.Fatalf("Failed to list notes: %v", err)
	}
	byID := map[string][]string{}
	for _, item := range items {
		byID[item.ID] = item.Tags
	}
	if !noteTagSetsEqual(byID["note-3"], []string{"alpha", "beta"}) {
		t.Errorf("Tagged note list tags mismatch: got %v", byID["note-3"])
	}
	if len(byID["note-4"]) != 0 {
		t.Errorf("Plain note should have no tags, got %v", byID["note-4"])
	}
}

func TestNoteTags_GetAllNoteTagsCountsAndDeleteCascade(t *testing.T) {
	store, cleanup := setupHybridStore(t, 10)
	defer cleanup()
	ctx := context.Background()
	ws := createNoteTagsTestWorkspace(t, store)

	now := time.Now()
	notes := []*WorkspaceNote{
		{ID: "note-5", WorkspaceID: ws.ID, Name: "A", Content: "c", Tags: []string{"shared", "solo"}, CreatedAt: now, UpdatedAt: now},
		{ID: "note-6", WorkspaceID: ws.ID, Name: "B", Content: "c", Tags: []string{"shared"}, CreatedAt: now, UpdatedAt: now},
	}
	for _, n := range notes {
		if err := store.CreateNote(ctx, n); err != nil {
			t.Fatalf("Failed to create note %s: %v", n.ID, err)
		}
	}

	tags, err := store.GetAllNoteTags(ctx)
	if err != nil {
		t.Fatalf("Failed to get all note tags: %v", err)
	}
	counts := map[string]int{}
	for _, tag := range tags {
		counts[tag.Name] = tag.UsageCount
	}
	if counts["shared"] != 2 || counts["solo"] != 1 {
		t.Fatalf("Unexpected note tag counts: %v", counts)
	}

	// Deleting a note removes its tag rows via ON DELETE CASCADE.
	if err := store.DeleteNote(ctx, "note-5"); err != nil {
		t.Fatalf("Failed to delete note: %v", err)
	}
	tags, err = store.GetAllNoteTags(ctx)
	if err != nil {
		t.Fatalf("Failed to get all note tags: %v", err)
	}
	counts = map[string]int{}
	for _, tag := range tags {
		counts[tag.Name] = tag.UsageCount
	}
	if counts["shared"] != 1 || counts["solo"] != 0 {
		t.Fatalf("Unexpected note tag counts after delete: %v", counts)
	}
}

func noteTagSetsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, tag := range a {
		seen[tag]++
	}
	for _, tag := range b {
		if seen[tag] == 0 {
			return false
		}
		seen[tag]--
	}
	return true
}
