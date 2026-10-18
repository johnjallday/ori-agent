package projectlibrary

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// A logical collection holds catalog references, not observations: a fresh
// reviewed rescan of the same root and an archive edit leave its identity,
// revision and exact membership as they were; the archived entry stays a
// member with its own archived status rather than being dropped.
func TestCollectionMembership_SurvivesRescanAndArchive(t *testing.T) {
	f, authority, record := homeArrangementFixture(t)
	saved, _, err := f.library.SaveArrangement(t.Context(), f.scope.OwnerUserID, authority, f.doc(t).Revision, true, record)
	if err != nil {
		t.Fatal(err)
	}
	service := correctionService(t, f, record)
	token := freshCorrectionReview(t, f, service, saved.Record.ID, 1)
	if _, err := service.Commit(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, token, "apply-collection", true); err != nil {
		t.Fatal(err)
	}
	applied := f.doc(t)
	if len(applied.Collections) != 1 || len(applied.Roots) == 0 {
		t.Fatalf("fixture: %d collections, %d roots", len(applied.Collections), len(applied.Roots))
	}
	collection := applied.Collections[0]
	members := slices.Sorted(slices.Values(collection.EntryIDs))

	// Rescan the same root through the reviewed scan route.
	scanRoot(t, f.roots, f.scope, applied.Roots[0].ID, "collection-rescan")
	rescanned := f.doc(t)
	if rescanned.Revision <= applied.Revision || len(rescanned.Collections) != 1 {
		t.Fatalf("rescan: revision %d → %d, %d collections", applied.Revision, rescanned.Revision, len(rescanned.Collections))
	}
	after := rescanned.Collections[0]
	if after.ID != collection.ID || after.Revision != collection.Revision || !reflect.DeepEqual(slices.Sorted(slices.Values(after.EntryIDs)), members) {
		t.Fatalf("rescan changed the collection: %+v → %+v", collection, after)
	}

	// Archive one member through the reviewed field edit.
	archived := members[0]
	var fieldsRevision int64
	for _, entry := range rescanned.Entries {
		if entry.ID == archived {
			fieldsRevision = entry.Fields.Revision
		}
	}
	status := "archived"
	patch := FieldsPatch{Status: &status}
	review, err := f.library.ReviewFields(f.scope, archived, fieldsRevision, patch, "local")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.library.CommitFields(f.scope, archived, review.Token, "archive-member", fieldsRevision, patch, "local"); err != nil {
		t.Fatal(err)
	}
	final := f.doc(t)
	kept := final.Collections[0]
	if kept.ID != collection.ID || kept.Revision != collection.Revision || !reflect.DeepEqual(slices.Sorted(slices.Values(kept.EntryIDs)), members) {
		t.Fatalf("archive changed the collection: %+v", kept)
	}
	if entry := sessionEntry(final, archived); entry == nil || entry.Fields.Status != "archived" {
		t.Fatalf("archived entry: %+v", entry)
	}
	// The completed arrangement and its receipt remain readable history.
	if history, err := f.library.ReadArrangementHistory(t.Context(), f.scope, saved.Record.ID); err != nil || history.CollectionReceipt == nil {
		t.Fatalf("history after rescan/archive: %+v %v", history, err)
	}
}

// Split Home mirrors fail closed for collection writes too: a reviewed commit
// against a v2 envelope whose folder mirror is ahead of the primary writes
// nothing to either mirror.
func TestCollectionCommit_RefusesSplitHomeMirrors(t *testing.T) {
	f, authority, record := homeArrangementFixture(t)
	saved, _, err := f.library.SaveArrangement(t.Context(), f.scope.OwnerUserID, authority, f.doc(t).Revision, true, record)
	if err != nil {
		t.Fatal(err)
	}
	token := freshCorrectionReview(t, f, correctionService(t, f, record), saved.Record.ID, 1)
	primary := workspace.NewInMemoryStore()
	home, err := f.file.Get(f.scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	if err := primary.Save(home); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash after the folder write but before the primary commit.
	if err := f.file.Update(f.scope.HomeID, func(ws *workspace.Workspace) error {
		state := ws.GetAssistantProgramState()
		var ahead Document
		if err := json.Unmarshal(state.ProjectLibrary, &ahead); err != nil {
			return err
		}
		ahead.Revision++
		state.ProjectLibrary, _ = json.Marshal(ahead)
		ws.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	split := NewStore(workspace.NewSyncStore(primary, f.file)).WithProviderEvidence(alwaysProviderEvidence).WithInstalledPlugins(f.installed)
	if _, err := split.Read(f.scope); !errors.Is(err, ErrMirrorDiverged) {
		t.Fatalf("split v2 mirrors were read: %v", err)
	}
	service := NewCollectionReviewService(split, func(ctx context.Context, _ ManagerAuthority) (ArrangementRuntime, error) {
		return ArrangementRuntime{Provider: homePlanningProvider{}, Model: record.Model, ProviderFingerprint: record.ProviderFingerprint, Revalidate: func(ctx context.Context) error { return ctx.Err() }}, ctx.Err()
	})
	if _, err := service.Commit(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, token, "split-commit", true); err == nil {
		t.Fatal("a collection commit wrote across split mirrors")
	}
	for name, backing := range map[string]workspace.Store{"primary": primary, "folder": f.file} {
		ws, err := backing.Get(f.scope.HomeID)
		if err != nil {
			t.Fatal(err)
		}
		var doc Document
		if err := json.Unmarshal(ws.GetAssistantProgramState().ProjectLibrary, &doc); err != nil || len(doc.Collections) != 0 {
			t.Fatalf("%s mirror gained collections: %d %v", name, len(doc.Collections), err)
		}
	}
}
