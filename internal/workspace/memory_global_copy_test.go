package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newGlobalCopyStore(t *testing.T) (*MemoryStore, string) {
	t.Helper()
	fs, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := fs.Save(&Workspace{ID: "ws-copy", Name: "Copy"}); err != nil {
		t.Fatal(err)
	}
	folder, err := fs.GetFolderPath("ws-copy")
	if err != nil {
		t.Fatal(err)
	}
	return NewMemoryStore(fs), filepath.Join(folder, MemoryFileName)
}

func TestGlobalCopyProvenanceHelpers(t *testing.T) {
	if GlobalCopyProvenance(" item-1 ") != "global:item-1" || !IsGlobalCopyProvenance("global:item-1") ||
		IsGlobalCopyProvenance("ori-hq:item-1:rev") || IsGlobalCopyProvenance("agent:Scout") {
		t.Fatal("global copy provenance helpers disagree")
	}
}

func TestEntriesWithProvenanceReadsWithoutCreatingMemory(t *testing.T) {
	store, path := newGlobalCopyStore(t)
	entries, err := store.EntriesWithProvenance("ws-copy", "global:item-1")
	if err != nil || len(entries) != 0 {
		t.Fatalf("empty workspace = %+v %v", entries, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a read created MEMORY.md: %v", err)
	}
	for _, entry := range []MemoryEntry{
		{Type: MemoryTypeFact, Date: "2026-10-17", Provenance: "global:item-1", Text: "Copied fact"},
		{Type: MemoryTypeFact, Date: "2026-10-17", Provenance: "global:item-2", Text: "Another copy"},
		{Type: MemoryTypeFact, Date: "2026-10-17", Provenance: "user", Text: "Mentions global:item-1 in text"},
	} {
		if err := store.Append("ws-copy", entry); err != nil {
			t.Fatal(err)
		}
	}
	entries, err = store.EntriesWithProvenance("ws-copy", "GLOBAL:item-1")
	if err != nil || len(entries) != 1 || entries[0].Text != "Copied fact" {
		t.Fatalf("provenance match = %+v %v", entries, err)
	}
}

func TestRemoveByProvenanceRemovesExactlyOneLine(t *testing.T) {
	store, path := newGlobalCopyStore(t)
	for _, entry := range []MemoryEntry{
		{Type: MemoryTypeFact, Date: "2026-10-17", Provenance: "user", Text: "Keep me"},
		{Type: MemoryTypeFact, Date: "2026-10-17", Provenance: "global:item-1", Text: "First copy"},
		{Type: MemoryTypeDecision, Date: "2026-10-18", Provenance: "global:item-1", Text: "Edited second copy"},
	} {
		if err := store.Append("ws-copy", entry); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := store.RemoveByProvenance("ws-copy", "global:item-1")
	if err != nil || removed.Text != "First copy" {
		t.Fatalf("remove = %+v %v", removed, err)
	}
	raw, err := os.ReadFile(path) // #nosec G304 -- disposable test workspace
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "First copy") || !strings.Contains(string(raw), "Keep me") ||
		!strings.Contains(string(raw), "Edited second copy") {
		t.Fatalf("remove touched other lines:\n%s", raw)
	}
	if _, err := store.RemoveByProvenance("ws-copy", "global:missing"); !errors.Is(err, ErrMemoryEntryNotFound) {
		t.Fatalf("missing provenance = %v", err)
	}
	if _, err := store.RemoveByProvenance("ws-copy", "ori-hq:item:rev"); !errors.Is(err, ErrMemoryEntryNotFound) {
		t.Fatalf("a managed marker must never be removable here: %v", err)
	}
	other, _ := newGlobalCopyStore(t)
	if _, err := other.RemoveByProvenance("ws-copy", "global:item-1"); !errors.Is(err, ErrMemoryEntryNotFound) {
		t.Fatalf("workspace without MEMORY.md = %v", err)
	}
}
