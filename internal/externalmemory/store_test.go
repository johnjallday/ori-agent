package externalmemory

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestStore_RoundTripsSourcesAndEntries(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	now := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)

	_, err := store.UpdateSources(func(doc *SourcesDocument) error {
		doc.LastCompletedSync = &now
		doc.Sources[SourceCodex] = SourceState{
			Enabled:          true,
			Exclusions:       []string{"/Users/someone/Projects/private"},
			LastSyncFinished: &now,
			LastResult:       ResultOK,
			Counts:           SyncCounts{Entries: 2, Folders: 1, Added: 2},
		}
		return nil
	})
	if err != nil {
		t.Fatalf("UpdateSources: %v", err)
	}
	entries := []Entry{
		newEntry(SourceCodex, "/codex/memories/MEMORY.md", "group/task-1", Entry{Title: "Task one", Body: "body", Folder: "/p", Category: "task"}),
		newEntry(SourceCodex, "/codex/memories/MEMORY.md", "group/task-2", Entry{Title: "Task two", Body: "한국어 본문", Folder: "/p", Category: "task", Date: "2026-10-01"}),
	}
	if err := store.SaveEntries(SourceCodex, entries); err != nil {
		t.Fatalf("SaveEntries: %v", err)
	}

	reopened := NewStore(dir)
	doc := reopened.LoadSources()
	got := doc.Source(SourceCodex)
	if !got.Enabled || got.LastResult != ResultOK || got.Counts.Entries != 2 || !reflect.DeepEqual(got.Exclusions, []string{"/Users/someone/Projects/private"}) {
		t.Fatalf("source state did not round-trip: %+v", got)
	}
	if doc.LastCompletedSync == nil || !doc.LastCompletedSync.Equal(now) {
		t.Fatalf("last completed sync = %v, want %v", doc.LastCompletedSync, now)
	}
	if loaded := reopened.LoadEntries(SourceCodex); !reflect.DeepEqual(loaded, entries) {
		t.Fatalf("entries did not round-trip:\n got %+v\nwant %+v", loaded, entries)
	}
	if off := doc.Source(SourceGeminiCLI); off.Enabled || off.Exclusions == nil {
		t.Fatalf("absent source should read as off with an empty exclusion list: %+v", off)
	}
}

func TestStore_MissingAndCorruptFilesLoadAsEmpty(t *testing.T) {
	cases := []struct {
		name  string
		write func(t *testing.T, dir string)
	}{
		{name: "missing", write: func(*testing.T, string) {}},
		{name: "corrupt", write: func(t *testing.T, dir string) {
			mustWrite(t, filepath.Join(dir, sourcesFileName), "{not json")
			mustWrite(t, filepath.Join(dir, entriesDirName, SourceCodex+".json"), `{"entries": [`)
		}},
		{name: "wrong source in file", write: func(t *testing.T, dir string) {
			mustWrite(t, filepath.Join(dir, entriesDirName, SourceCodex+".json"),
				`{"version":1,"source":"codex","entries":[{"id":"x","source":"claude_code","title":"t"}]}`)
		}},
		{name: "unknown source in sources.json", write: func(t *testing.T, dir string) {
			mustWrite(t, filepath.Join(dir, sourcesFileName), `{"version":1,"sources":{"../evil":{"enabled":true}}}`)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.write(t, dir)
			store := NewStore(dir)
			doc := store.LoadSources()
			if len(doc.Sources) != 0 {
				t.Fatalf("sources = %+v, want none", doc.Sources)
			}
			if entries := store.LoadEntries(SourceCodex); len(entries) != 0 {
				t.Fatalf("entries = %+v, want none", entries)
			}
		})
	}
}

func TestStore_CorruptFileIsMovedAsideAndReplacedOnWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, sourcesFileName)
	mustWrite(t, path, "{not json")

	store := NewStore(dir)
	_ = store.LoadSources()
	if _, err := os.Stat(path + corruptSuffix); err != nil {
		t.Fatalf("corrupt file was not moved aside: %v", err)
	}
	if _, err := store.UpdateSources(func(doc *SourcesDocument) error {
		doc.Sources[SourceCodex] = SourceState{Enabled: true}
		return nil
	}); err != nil {
		t.Fatalf("UpdateSources after corruption: %v", err)
	}
	if !NewStore(dir).LoadSources().Source(SourceCodex).Enabled {
		t.Fatal("write after corruption did not persist")
	}
}

func TestStore_UpdateSourcesErrorLeavesFileUnchanged(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	if _, err := store.UpdateSources(func(doc *SourcesDocument) error {
		doc.Sources[SourceCodex] = SourceState{Enabled: true}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateSources(func(doc *SourcesDocument) error {
		doc.Sources[SourceCodex] = SourceState{Enabled: false}
		return os.ErrInvalid
	}); err == nil {
		t.Fatal("expected the mutate error")
	}
	if !NewStore(dir).LoadSources().Source(SourceCodex).Enabled {
		t.Fatal("a failed update changed the file")
	}
}

func TestStore_RejectsUnknownSourceFileNames(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.SaveEntries("../escape", nil); err == nil {
		t.Fatal("SaveEntries accepted a path-like source id")
	}
}

func TestStore_LeavesNoTempFilesBehind(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	if err := store.SaveEntries(SourceCodex, []Entry{newEntry(SourceCodex, "/f", "k", Entry{Title: "t"})}); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(filepath.Join(dir, entriesDirName))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Name() != SourceCodex+".json" {
		t.Fatalf("entries folder holds %v, want only codex.json", files)
	}
	info, err := os.Stat(filepath.Join(dir, entriesDirName, SourceCodex+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != storeFilePerm {
		t.Fatalf("entries file mode = %v, want %v", perm, os.FileMode(storeFilePerm))
	}
}

func TestExclusions_NormalizeAndMatchOnFolderBoundaries(t *testing.T) {
	prefixes := normalizeExclusions([]string{" ~/Projects/crypto ", "relative/path", "/abs/dir/", "/abs/dir", ""}, "/Users/me")
	if want := []string{"/Users/me/Projects/crypto", "/abs/dir"}; !reflect.DeepEqual(prefixes, want) {
		t.Fatalf("normalized = %v, want %v", prefixes, want)
	}
	cases := map[string]bool{
		"/Users/me/Projects/crypto":                 true,
		"/Users/me/Projects/crypto/starter/btc-lab": true,
		"/Users/me/Projects/crypto-other":           false,
		"/abs/dir/x":                                true,
		"":                                          false,
	}
	for folder, want := range cases {
		if got := excluded(folder, prefixes); got != want {
			t.Errorf("excluded(%q) = %v, want %v", folder, got, want)
		}
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
