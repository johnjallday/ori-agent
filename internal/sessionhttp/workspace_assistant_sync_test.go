package sessionhttp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func assistantStateJSON(t *testing.T, home bool) json.RawMessage {
	t.Helper()
	state := workspaceAssistantState{}
	if home {
		state.State = &workspace.AssistantProgramState{
			SchemaVersion: 2, StateRevision: 7, LinkedProjectIDs: []string{"song"},
			Key: workspace.AssistantProgramKey{OwnerUserID: "local", PluginID: "reaper-plugin", ProgramID: "music"},
		}
	} else {
		state.Link = &workspace.AssistantProjectLink{SchemaVersion: 2, ID: "exact-link", StationWorkspaceID: "home", StateRevision: 3}
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestBuildFileStoreWorkspacePreservesAssistantState(t *testing.T) {
	for _, home := range []bool{false, true} {
		raw := assistantStateJSON(t, home)
		built, err := buildFileStoreWorkspace(&session.Workspace{ID: "test", AssistantProgramJSON: raw})
		if err != nil {
			t.Fatal(err)
		}
		want, err := decodeWorkspaceAssistantState(raw)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(built.AssistantProgramState, want.State) || !reflect.DeepEqual(built.AssistantProjectLink, want.Link) {
			t.Fatalf("assistant state lost in folder rebuild: %+v", built)
		}
	}
	if _, err := buildFileStoreWorkspace(&session.Workspace{AssistantProgramJSON: json.RawMessage(`{"state":false}`)}); err == nil {
		t.Fatal("malformed assistant state was silently discarded")
	}
}

func TestPortableSyncAssistantStateDistinguishesAbsentAndCleared(t *testing.T) {
	for _, home := range []bool{false, true} {
		h, cleanup := createTestHandler(t)
		t.Cleanup(cleanup)
		fs, _ := newTestFileStore(t, h)
		t.Cleanup(func() { _ = fs.Close() })
		row := &session.Workspace{ID: "test", Name: "Test", FolderSlug: "test", AssistantProgramJSON: assistantStateJSON(t, home)}
		if err := fs.Save(&workspace.Workspace{ID: row.ID, Name: row.Name, FolderSlug: row.FolderSlug}); err != nil {
			t.Fatal(err)
		}
		if err := h.syncWorkspacePortableStateToFileStore(row); err != nil {
			t.Fatal(err)
		}
		before, err := fs.Get(row.ID)
		if err != nil {
			t.Fatal(err)
		}
		if before.AssistantProgramState == nil && before.AssistantProjectLink == nil {
			t.Fatal("assistant state never reached disk")
		}
		row.AssistantProgramJSON = nil
		if err := h.syncWorkspacePortableStateToFileStore(row); err != nil {
			t.Fatal(err)
		}
		after, err := fs.Get(row.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before.AssistantProgramState, after.AssistantProgramState) || !reflect.DeepEqual(before.AssistantProjectLink, after.AssistantProjectLink) {
			t.Fatal("partial row erased assistant state")
		}
		row.AssistantProgramJSON = json.RawMessage(`{}`)
		if err := h.syncWorkspacePortableStateToFileStore(row); err != nil {
			t.Fatal(err)
		}
		after, err = fs.Get(row.ID)
		if err != nil {
			t.Fatal(err)
		}
		if after.AssistantProgramState != nil || after.AssistantProjectLink != nil {
			t.Fatal("explicitly cleared assistant state was resurrected")
		}
	}
}

func TestGroupBackfillPreservesAndRepairsAssistantHome(t *testing.T) {
	for _, existingFolder := range []bool{false, true} {
		h, cleanup := createTestHandler(t)
		t.Cleanup(cleanup)
		fs, _ := newTestFileStore(t, h)
		t.Cleanup(func() { _ = fs.Close() })
		ctx := context.Background()
		row := &session.Workspace{ID: "home", Name: "Music Home", Kind: session.WorkspaceKindGroup, FolderSlug: "music-home", AssistantProgramJSON: assistantStateJSON(t, true)}
		if err := h.store.CreateWorkspace(ctx, row); err != nil {
			t.Fatal(err)
		}
		if existingFolder {
			// Reproduce an earlier backfill's plain group folder. Fully scaffold
			// it first so the second run must repair metadata even with no new dirs.
			plain := *row
			plain.AssistantProgramJSON = nil
			if _, err := h.backfillGroupScaffolding(ctx, &plain); err != nil {
				t.Fatal(err)
			}
			full, err := h.store.GetWorkspace(ctx, row.ID)
			if err != nil {
				t.Fatal(err)
			}
			full.AssistantProgramJSON = row.AssistantProgramJSON
			if err := h.store.UpdateWorkspace(ctx, full); err != nil {
				t.Fatal(err)
			}
		}
		for i := 0; i < 2; i++ {
			if err := h.BackfillGroupScaffolding(ctx); err != nil {
				t.Fatal(err)
			}
			full, err := h.store.GetWorkspace(ctx, row.ID)
			if err != nil {
				t.Fatal(err)
			}
			if string(full.AssistantProgramJSON) != string(row.AssistantProgramJSON) {
				t.Fatalf("backfill changed authoritative assistant state (existing=%v): got %s, want %s", existingFolder, full.AssistantProgramJSON, row.AssistantProgramJSON)
			}
			path, err := fs.GetFolderPath(row.ID)
			if err != nil {
				t.Fatal(err)
			}
			// Read the serialized record, not only the cache, to cover restart.
			raw, err := os.ReadFile(filepath.Join(path, workspace.WorkspaceConfigFile))
			if err != nil {
				t.Fatal(err)
			}
			var disk workspace.Workspace
			if err := json.Unmarshal(raw, &disk); err != nil {
				t.Fatal(err)
			}
			if disk.AssistantProgramState == nil || disk.AssistantProgramState.StateRevision != 7 || len(disk.AssistantProgramState.LinkedProjectIDs) != 1 {
				t.Fatalf("Home state lost after backfill: %+v", disk.AssistantProgramState)
			}
		}
	}
}
