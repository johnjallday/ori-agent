package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedMemoryCannotBeChangedByGenericToolsAndNoErrorEcho(t *testing.T) {
	store, dir := newTestMemoryStore(t)
	const sentinel = "FORGOTTEN-SENTINEL: never return to an agent"
	const original = "# Keep this prelude\r\n\r\n- [fact, 2026-09-01, ori-hq:item:rev] " + sentinel + "\r\n- [fact, 2026-09-02, user] Other fact"
	writeTestMemoryFile(t, dir, original)
	if _, err := store.Forget("ws1", "FORGOTTEN-SENTINEL"); !errors.Is(err, ErrMemoryEntryNotFound) || strings.Contains(err.Error(), sentinel) {
		t.Fatalf("generic forget leaked or removed managed line: %v", err)
	}
	if err := store.EditAt("ws1", 0, MemoryEntry{Type: MemoryTypeFact, Date: "2026-09-02", Provenance: "user", Text: "replacement"}); !errors.Is(err, ErrMemoryManaged) {
		t.Fatalf("generic index edit changed managed memory: %v", err)
	}
	if err := store.DeleteAt("ws1", 0); !errors.Is(err, ErrMemoryManaged) {
		t.Fatalf("generic index delete changed managed memory: %v", err)
	}
	if err := store.Append("ws1", MemoryEntry{Type: MemoryTypeFact, Date: "2026-09-02", Provenance: "ori-hq:forged", Text: "forged"}); !errors.Is(err, ErrMemoryManaged) {
		t.Fatalf("generic append forged managed memory: %v", err)
	}
	if _, err := store.AppendUnique("ws1", MemoryEntry{Type: MemoryTypeFact, Date: "2026-09-02", Provenance: "ori-hq:forged", Text: "forged"}); !errors.Is(err, ErrMemoryManaged) {
		t.Fatalf("generic append unique forged managed memory: %v", err)
	}
	path := filepath.Join(dir, MemoryFileName)
	current, err := os.ReadFile(path) // #nosec G304 -- test temp folder
	if err != nil || string(current) != original {
		t.Fatalf("denied mutation changed unrelated bytes: %q %v", current, err)
	}
	if err := store.EditAt("ws1", 1, MemoryEntry{Type: MemoryTypeFact, Date: "2026-09-02", Provenance: "user", Text: "New ordinary fact"}); err != nil {
		t.Fatalf("ordinary edit should still work: %v", err)
	}
	current, err = os.ReadFile(path) // #nosec G304 -- test temp folder
	if err != nil || !strings.HasPrefix(string(current), "# Keep this prelude\r\n\r\n- [fact, 2026-09-01, ori-hq:item:rev] "+sentinel+"\r\n") {
		t.Fatalf("ordinary edit rewrote managed bytes: %q %v", current, err)
	}
}
