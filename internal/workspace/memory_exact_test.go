package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestMemoryExactEditAndDeletePreserveUnrelatedBytes(t *testing.T) {
	for _, test := range []struct {
		name string
		sep  string
		eof  string
	}{
		{"LF with newline", "\n", "\n"},
		{"LF without newline", "\n", ""},
		{"CRLF with newline", "\r\n", "\r\n"},
		{"CRLF without newline", "\r\n", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, dir := newTestMemoryStore(t)
			before := strings.Join([]string{
				"# Workspace Memory", "", "  hand-authored prose  ",
				"- [fact, 2026-09-01, user] Unrelated first fact",
				"- [fact, 2026-09-02, ori-hq:item-1:rev-1] Original managed fact",
				"- a note [not a fact]", "- [fact, 2026-09-03, user] Unrelated last fact",
			}, test.sep) + test.eof
			path := filepath.Join(dir, MemoryFileName)
			if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
				t.Fatal(err)
			}
			snapshot, err := store.SnapshotExact("ws1")
			if err != nil || len(snapshot.Entries) != 3 {
				t.Fatalf("exact snapshot=%+v err=%v", snapshot, err)
			}
			target := snapshot.Entries[1].Target
			if err := store.EditExact("ws1", target, &MemoryEntry{
				Type: MemoryTypeFact, Date: "2026-09-04", Provenance: "ori-hq:item-1:rev-2", Text: "New managed fact",
			}); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path) // #nosec G304 -- test-only temporary folder
			if err != nil {
				t.Fatal(err)
			}
			want := strings.Replace(before,
				"- [fact, 2026-09-02, ori-hq:item-1:rev-1] Original managed fact",
				"- [fact, 2026-09-04, ori-hq:item-1:rev-2] New managed fact", 1)
			if string(data) != want {
				t.Fatalf("edit changed unrelated bytes:\n got %q\nwant %q", data, want)
			}
			updated, err := store.SnapshotExact("ws1")
			if err != nil {
				t.Fatal(err)
			}
			if err := store.EditExact("ws1", updated.Entries[1].Target, nil); err != nil {
				t.Fatal(err)
			}
			data, err = os.ReadFile(path) // #nosec G304 -- test-only temporary folder
			if err != nil {
				t.Fatal(err)
			}
			want = strings.Replace(want, "- [fact, 2026-09-04, ori-hq:item-1:rev-2] New managed fact"+test.sep, "", 1)
			if string(data) != want {
				t.Fatalf("delete changed unrelated bytes:\n got %q\nwant %q", data, want)
			}
		})
	}
}

func TestMemoryExactWriteFailureAndExternalRaceLeaveCanonicalUnchanged(t *testing.T) {
	store, dir := newTestMemoryStore(t)
	path := filepath.Join(dir, MemoryFileName)
	const original = "# untouched\n- [fact, 2026-09-01, user] legacy\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.SnapshotExact("ws1")
	if err != nil {
		t.Fatal(err)
	}
	store.beforeExactRename = func() error { return errors.New("injected write failure") }
	if err := store.EditExact("ws1", snapshot.Entries[0].Target, nil); err == nil {
		t.Fatal("rename failure was hidden")
	}
	data, err := os.ReadFile(path) // #nosec G304 -- test-only temporary folder
	if err != nil || string(data) != original {
		t.Fatalf("write failure changed canonical memory: %q %v", data, err)
	}
	store.beforeExactRename = func() error {
		return os.WriteFile(path, []byte("# externally edited\n"+original), 0o600)
	}
	if err := store.EditExact("ws1", snapshot.Entries[0].Target, nil); !errors.Is(err, ErrMemoryConflict) {
		t.Fatalf("external write before rename overwritten: %v", err)
	}
	data, err = os.ReadFile(path) // #nosec G304 -- test-only temporary folder
	if err != nil || string(data) != "# externally edited\n"+original {
		t.Fatalf("external edit changed: %q %v", data, err)
	}
}

func TestMemoryExactConcurrentCASOnIndependentStores(t *testing.T) {
	first, dir := newTestMemoryStore(t)
	second := NewMemoryStore(staticFolderResolver{folders: map[string]string{"ws1": dir}})
	snapshot, err := first.SnapshotExact("ws1")
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	results := make(chan error, 2)
	for _, store := range []*MemoryStore{first, second} {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := store.AppendExact("ws1", snapshot.FileHash, MemoryEntry{
				Type: MemoryTypeFact, Date: "2026-09-01", Provenance: "ori-hq:item:revision", Text: "One fact",
			})
			results <- err
		}()
	}
	group.Wait()
	close(results)
	var winners, conflicts int
	for err := range results {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, ErrMemoryConflict):
			conflicts++
		default:
			t.Fatalf("unexpected append failure: %v", err)
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatalf("concurrent append winners=%d conflicts=%d", winners, conflicts)
	}
}

func TestMemoryExactInspectExternalEditAndDeletionNeverRecreatesFact(t *testing.T) {
	store, dir := newTestMemoryStore(t)
	path := filepath.Join(dir, MemoryFileName)
	const original = "# authored\n- [fact, 2026-09-01, ori-hq:item:rev] Confirmed value\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.SnapshotExact("ws1")
	if err != nil {
		t.Fatal(err)
	}
	target := snapshot.Entries[0].Target
	if err := os.WriteFile(path, []byte("Some unrelated prose\n"+original), 0o600); err != nil {
		t.Fatal(err)
	}
	if status, err := store.InspectExact("ws1", target); err != nil || status != MemoryExactUnchanged {
		t.Fatalf("unrelated edit status=%s err=%v", status, err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(original, "Confirmed value", "Outside-edited value", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if status, err := store.InspectExact("ws1", target); err != nil || status != MemoryExactChanged {
		t.Fatalf("edited managed line status=%s err=%v", status, err)
	}
	if err := os.WriteFile(path, []byte("# authored\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if status, err := store.InspectExact("ws1", target); err != nil || status != MemoryExactMissing {
		t.Fatalf("deleted managed line status=%s err=%v", status, err)
	}
	data, err := os.ReadFile(path) // #nosec G304 -- test-only temporary folder
	if err != nil || string(data) != "# authored\n" {
		t.Fatalf("inspection resurrected missing fact: %q %v", data, err)
	}
}

func TestMemoryExactRefusesStaleAndAmbiguousTargets(t *testing.T) {
	store, dir := newTestMemoryStore(t)
	path := filepath.Join(dir, MemoryFileName)
	line := "- [fact, 2026-09-01, user] Duplicate legacy line\n"
	if err := os.WriteFile(path, []byte(line+line), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.SnapshotExact("ws1")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EditExact("ws1", snapshot.Entries[0].Target, nil); !errors.Is(err, ErrMemoryAmbiguousMatch) {
		t.Fatalf("duplicate text selected an arbitrary entry: %v", err)
	}
	if err := os.WriteFile(path, []byte("# external user edit\n"+line+line), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.EditExact("ws1", snapshot.Entries[0].Target, nil); !errors.Is(err, ErrMemoryConflict) {
		t.Fatalf("stale file version overwrote external edit: %v", err)
	}
	data, err := os.ReadFile(path) // #nosec G304 -- test-only temporary folder
	if err != nil || string(data) != "# external user edit\n"+line+line {
		t.Fatalf("stale edit changed file: %q %v", data, err)
	}
}

func TestMemoryExactAppendUsesExpectedSnapshotAndDoesNotRewriteFile(t *testing.T) {
	store, dir := newTestMemoryStore(t)
	before := "# Memory\r\n\r\nlast line without newline"
	path := filepath.Join(dir, MemoryFileName)
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.SnapshotExact("ws1")
	if err != nil {
		t.Fatal(err)
	}
	entry := MemoryEntry{Type: MemoryTypeFact, Date: "2026-09-02", Provenance: "ori-hq:item-1:rev-1", Text: "One reviewed fact"}
	if _, err := store.AppendExact("ws1", snapshot.FileHash, entry); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path) // #nosec G304 -- test-only temporary folder
	if err != nil || string(data) != before+"\r\n"+entry.Render() {
		t.Fatalf("append altered existing bytes: %q %v", data, err)
	}
	if _, err := store.AppendExact("ws1", snapshot.FileHash, entry); !errors.Is(err, ErrMemoryConflict) {
		t.Fatalf("stale append duplicated fact: %v", err)
	}
	// Empty canonical file uses the documented default header and LF ending.
	fresh, _ := newTestMemoryStore(t)
	emptyHash := sha256.Sum256(nil)
	if _, err := fresh.AppendExact("ws1", hex.EncodeToString(emptyHash[:]), entry); err != nil {
		t.Fatalf("append to missing memory: %v", err)
	}
}
