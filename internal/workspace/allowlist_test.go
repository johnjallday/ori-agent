package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAllowlist_LoadMissingFileIsEmpty(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "workspace_allowlist.json")
	a, err := LoadAllowlist(tmp)
	if err != nil {
		t.Fatalf("LoadAllowlist on missing file: %v", err)
	}
	if got := a.IDs(); len(got) != 0 {
		t.Fatalf("expected empty IDs, got %v", got)
	}
	if a.Contains("anything") {
		t.Fatal("expected Contains to be false for fresh allowlist")
	}
}

func TestAllowlist_AddPersistsAndDedupes(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "workspace_allowlist.json")
	a := NewAllowlist(tmp)

	if err := a.Add("ws-1"); err != nil {
		t.Fatalf("Add ws-1: %v", err)
	}
	if err := a.Add("ws-2"); err != nil {
		t.Fatalf("Add ws-2: %v", err)
	}
	if err := a.Add("ws-1"); err != nil {
		t.Fatalf("Add duplicate ws-1: %v", err)
	}

	reloaded, err := LoadAllowlist(tmp)
	if err != nil {
		t.Fatalf("LoadAllowlist after writes: %v", err)
	}
	if !reloaded.Contains("ws-1") || !reloaded.Contains("ws-2") {
		t.Fatalf("expected ws-1 and ws-2 in reloaded, got %v", reloaded.IDs())
	}
	if got := reloaded.IDs(); len(got) != 2 {
		t.Fatalf("expected exactly 2 IDs after dedupe, got %v", got)
	}
}

func TestAllowlist_RemovePersists(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "workspace_allowlist.json")
	a := NewAllowlist(tmp)
	if err := a.Add("ws-keep"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := a.Add("ws-drop"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := a.Remove("ws-drop"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	// Remove twice should be a silent no-op.
	if err := a.Remove("ws-drop"); err != nil {
		t.Fatalf("Remove (no-op): %v", err)
	}

	reloaded, err := LoadAllowlist(tmp)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if reloaded.Contains("ws-drop") {
		t.Fatal("ws-drop should be gone after Remove")
	}
	if !reloaded.Contains("ws-keep") {
		t.Fatal("ws-keep should still be present")
	}
}

func TestAllowlist_BlankIDsRejected(t *testing.T) {
	a := NewAllowlist(filepath.Join(t.TempDir(), "wl.json"))
	if err := a.Add(""); err == nil {
		t.Fatal("expected error adding empty id")
	}
	if err := a.Add("   "); err == nil {
		t.Fatal("expected error adding whitespace-only id")
	}
	// Contains with empty string is always false.
	if a.Contains("") {
		t.Fatal("Contains(\"\") must be false")
	}
}

func TestAllowlist_FileFormatIsStable(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "workspace_allowlist.json")
	a := NewAllowlist(tmp)
	for _, id := range []string{"c", "a", "b"} {
		if err := a.Add(id); err != nil {
			t.Fatalf("Add %s: %v", id, err)
		}
	}

	data, err := os.ReadFile(tmp)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var parsed allowlistFile
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	want := []string{"a", "b", "c"}
	if len(parsed.WorkspaceIDs) != len(want) {
		t.Fatalf("len mismatch: %v vs %v", parsed.WorkspaceIDs, want)
	}
	for i, id := range want {
		if parsed.WorkspaceIDs[i] != id {
			t.Fatalf("sort order mismatch at %d: got %s want %s", i, parsed.WorkspaceIDs[i], id)
		}
	}
}

func TestAllowlist_AddTrimsWhitespace(t *testing.T) {
	a := NewAllowlist(filepath.Join(t.TempDir(), "wl.json"))
	if err := a.Add("  ws-padded  "); err != nil {
		t.Fatalf("Add padded: %v", err)
	}
	if !a.Contains("ws-padded") {
		t.Fatal("Contains should match trimmed form")
	}
	if !a.Contains("   ws-padded   ") {
		t.Fatal("Contains should also accept padded query")
	}
}
