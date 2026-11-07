package workspace

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceAgentReferenceReadsCurrentMetadataWithoutRepair(t *testing.T) {
	s, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Other"})
	if err := s.Save(ws); err != nil {
		t.Fatal(err)
	}
	folder, err := s.GetFolderPath(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(folder, WorkspaceConfigFile)
	// Change the authoritative file behind the lean cache; no rescan/import is
	// needed to protect the new reference. Heavy fields are not decoded.
	data := []byte(`{"id":"` + ws.ID + `","name":"Other","agent_instances":[{"name":"SCOUT"}],"messages":"not-an-array","tasks":"not-an-array"}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	found, err := s.HasWorkspaceAgentReference(context.Background(), "Scout", "removed")
	if err != nil || !found {
		t.Fatalf("current reference not protected: %v %v", found, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, after) {
		t.Fatal("reference check repaired/wrote metadata")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if found, err := s.HasWorkspaceAgentReference(context.Background(), "Scout", "removed"); found || err == nil {
		t.Fatal("missing indexed file treated as no references")
	}
	if _, present := s.CachedWorkspaces()[ws.ID]; !present {
		t.Fatal("reference check pruned the cache")
	}
}

func TestWorkspaceAgentReferencesDoNotOpenTrashedMetadata(t *testing.T) {
	s, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	// The primary reader is responsible for restorable rows. Even a stale
	// index pointing at a readable folder must not make this reader open Trash.
	s.mu.Lock()
	s.idToPath["retained"] = "missing-do-not-open"
	s.cache["retained"] = &Workspace{ID: "retained", Status: WorkspaceStatus("trashed")}
	s.mu.Unlock()
	if _, err := s.HasWorkspaceAgentReference(context.Background(), "Scout", "removed"); err == nil {
		t.Fatal("trashed metadata accepted as an authoritative live folder")
	}
}
