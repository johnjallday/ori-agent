package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type contextFixture struct {
	store    *FileStore
	ws       *Workspace
	folder   string
	root     string
	writer   *MemoryContextWriter
	settings *MemoryContextSettings
}

func newContextFixture(t *testing.T) *contextFixture {
	t.Helper()
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Chart Lab"})
	ws.ID = "chart-lab"
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	folder, err := store.GetFolderPath(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	settings := NewMemoryContextSettings(filepath.Join(t.TempDir(), "workspace_memory_context.json"))
	writer := &MemoryContextWriter{
		Folders: store, Workspaces: store, Settings: settings,
		RootFolder: func(*Workspace) string { return root },
	}
	return &contextFixture{store: store, ws: ws, folder: folder, root: root, writer: writer, settings: settings}
}

func (f *contextFixture) remember(t *testing.T, text string) {
	t.Helper()
	if err := NewMemoryStore(f.store).Append(f.ws.ID, MemoryEntry{Type: MemoryTypeFact, Date: "2026-10-19", Provenance: "user", Text: text}); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) (string, bool) {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- test temp files
	if err != nil {
		if os.IsNotExist(err) {
			return "", false
		}
		t.Fatal(err)
	}
	return string(data), true
}

func TestMemoryContextWriter_RendersTheSameFileForEachCLI(t *testing.T) {
	f := newContextFixture(t)
	f.remember(t, "Studies use 0.6% per-side fees.")
	f.writer.Render(f.ws.ID)
	var first string
	for _, name := range MemoryContextFileNames {
		text, ok := readFile(t, filepath.Join(f.folder, ".ori", "context", name))
		if !ok {
			t.Fatalf("%s was not written", name)
		}
		if !strings.Contains(text, "Studies use 0.6% per-side fees.") || !strings.Contains(text, `the workspace "Chart Lab"`) || !strings.Contains(text, "## Workspace Memory") {
			t.Fatalf("%s:\n%s", name, text)
		}
		if strings.Contains(text, "memory_write") {
			t.Fatalf("a file for a CLI must not carry Ori's tool guidance:\n%s", text)
		}
		if first == "" {
			first = text
		} else if text != first {
			t.Fatal("the three files differ")
		}
	}
	// Root files stay untouched while the switch is off.
	for _, name := range MemoryContextFileNames {
		if _, ok := readFile(t, filepath.Join(f.root, name)); ok {
			t.Fatalf("root %s written without the switch", name)
		}
	}
}

func TestMemoryContextWriter_NoMemoryNoFilesAndGlobalLinesNeverRendered(t *testing.T) {
	f := newContextFixture(t)
	f.writer.Render(f.ws.ID)
	if _, ok := readFile(t, filepath.Join(f.folder, ".ori", "context", "AGENTS.md")); ok {
		t.Fatal("an empty memory should give no file")
	}
	// Memory emptied again: the files go away.
	f.remember(t, "Temporary fact.")
	f.writer.Render(f.ws.ID)
	if _, ok := readFile(t, filepath.Join(f.folder, ".ori", "context", "AGENTS.md")); !ok {
		t.Fatal("a fact should give a file")
	}
	if err := NewMemoryStore(f.store).DeleteAt(f.ws.ID, 0); err != nil {
		t.Fatal(err)
	}
	f.writer.Render(f.ws.ID)
	if _, ok := readFile(t, filepath.Join(f.folder, ".ori", "context", "AGENTS.md")); ok {
		t.Fatal("files should be removed once the memory has nothing to render")
	}
	// A managed global-memory line is never rendered, as in the prompt.
	path := filepath.Join(f.folder, MemoryFileName)
	if err := os.WriteFile(path, []byte("# Memory\n\n- [fact, 2026-10-17, user] Ordinary fact.\n- [fact, 2026-10-17, ori-hq:item-1:rev-1] GLOBAL-ONLY-LINE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.writer.Render(f.ws.ID)
	text, ok := readFile(t, filepath.Join(f.folder, ".ori", "context", "AGENTS.md"))
	if !ok || !strings.Contains(text, "Ordinary fact.") || strings.Contains(text, "GLOBAL-ONLY-LINE") {
		t.Fatalf("global memory leaked into, or the ordinary line is missing from, a context file:\n%s", text)
	}
}

func TestMemoryContextWriter_RootSwitchAppendsAndRemovesTheBlock(t *testing.T) {
	f := newContextFixture(t)
	const userText = "# Repo guide\n\nRun `make test` before committing.\n"
	agents := filepath.Join(f.root, "AGENTS.md")
	if err := os.WriteFile(agents, []byte(userText), 0o644); err != nil {
		t.Fatal(err)
	}
	f.remember(t, "Studies use 0.6% per-side fees.")
	if err := f.settings.SetRootFiles(f.ws.ID, true); err != nil {
		t.Fatal(err)
	}
	f.writer.Render(f.ws.ID)
	text, _ := readFile(t, agents)
	if !strings.HasPrefix(text, userText) || !strings.Contains(text, MemoryBlockStart) || !strings.Contains(text, "0.6% per-side") {
		t.Fatalf("AGENTS.md after switching on:\n%s", text)
	}
	if info, _ := os.Stat(agents); info.Mode().Perm() != 0o644 {
		t.Fatalf("the user's file mode changed to %v", info.Mode().Perm())
	}
	claude, created := readFile(t, filepath.Join(f.root, "CLAUDE.md"))
	if !created || !strings.HasPrefix(claude, MemoryBlockStart) {
		t.Fatalf("a missing CLAUDE.md should be created with only the block:\n%s", claude)
	}

	// A memory change re-renders the block in place.
	f.remember(t, "Exits on EMA20 below EMA50.")
	f.writer.Render(f.ws.ID)
	if text, _ := readFile(t, agents); !strings.Contains(text, "EMA20 below EMA50") || strings.Count(text, MemoryBlockStart) != 1 {
		t.Fatalf("block not updated in place:\n%s", text)
	}

	if err := f.settings.SetRootFiles(f.ws.ID, false); err != nil {
		t.Fatal(err)
	}
	f.writer.RemoveRootBlocks(f.ws.ID)
	if text, _ := readFile(t, agents); text != userText {
		t.Fatalf("switching off did not restore AGENTS.md:\n%q", text)
	}
	if _, ok := readFile(t, filepath.Join(f.root, "CLAUDE.md")); ok {
		t.Fatal("a file that held only the block should be deleted")
	}
	// The .ori/context copies stay: they are always written.
	if _, ok := readFile(t, filepath.Join(f.folder, ".ori", "context", "AGENTS.md")); !ok {
		t.Fatal("the .ori/context files are not part of the switch")
	}
}

func TestMemoryContextWriter_SkipsMissingDisallowedAndMalformed(t *testing.T) {
	f := newContextFixture(t)
	f.remember(t, "A fact.")
	if err := f.settings.SetRootFiles(f.ws.ID, true); err != nil {
		t.Fatal(err)
	}

	f.writer.Allowed = func(string) error { return os.ErrPermission }
	f.writer.Render(f.ws.ID)
	if _, ok := readFile(t, filepath.Join(f.root, "AGENTS.md")); ok {
		t.Fatal("a disallowed folder was written")
	}
	f.writer.Allowed = nil

	gone := filepath.Join(t.TempDir(), "gone")
	f.writer.RootFolder = func(*Workspace) string { return gone }
	f.writer.Render(f.ws.ID)
	if _, err := os.Stat(gone); err == nil {
		t.Fatal("a missing root folder was created")
	}

	f.writer.RootFolder = func(*Workspace) string { return f.root }
	broken := "user text\n" + MemoryBlockStart + "\nunclosed\n"
	if err := os.WriteFile(filepath.Join(f.root, "GEMINI.md"), []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	f.writer.Render(f.ws.ID)
	if text, _ := readFile(t, filepath.Join(f.root, "GEMINI.md")); text != broken {
		t.Fatalf("a file with malformed markers must be left alone:\n%q", text)
	}
	if text, _ := readFile(t, filepath.Join(f.root, "AGENTS.md")); !strings.Contains(text, "A fact.") {
		t.Fatal("one malformed file must not stop the others")
	}

	// A workspace without a folder on disk is skipped, not an error.
	if err := os.RemoveAll(f.folder); err != nil {
		t.Fatal(err)
	}
	f.writer.Render(f.ws.ID)
}

func TestMemoryContextSettings_PersistPerWorkspace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	settings := NewMemoryContextSettings(path)
	if settings.RootFiles("a") {
		t.Fatal("default must be off")
	}
	if err := settings.SetRootFiles("a", true); err != nil {
		t.Fatal(err)
	}
	reopened := NewMemoryContextSettings(path)
	if !reopened.RootFiles("a") || reopened.RootFiles("b") {
		t.Fatal("switch did not persist per workspace")
	}
	if err := reopened.SetRootFiles("a", false); err != nil {
		t.Fatal(err)
	}
	if NewMemoryContextSettings(path).RootFiles("a") {
		t.Fatal("switch off did not persist")
	}
	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if NewMemoryContextSettings(path).RootFiles("a") {
		t.Fatal("a corrupt file must read as off")
	}
}

func TestMemoryWriteObserver_RunsAfterEveryMutationOutsideTheLock(t *testing.T) {
	f := newContextFixture(t)
	var seen []string
	SetMemoryWriteObserver(func(id string) {
		// Reading memory here would deadlock if the lock were still held.
		if _, err := NewMemoryStore(f.store).Read(id); err != nil {
			t.Errorf("observer could not read memory: %v", err)
		}
		seen = append(seen, id)
	})
	t.Cleanup(func() { SetMemoryWriteObserver(nil) })

	memory := NewMemoryStore(f.store)
	entry := MemoryEntry{Type: MemoryTypeFact, Date: "2026-10-19", Provenance: "user", Text: "One."}
	if err := memory.Append(f.ws.ID, entry); err != nil {
		t.Fatal(err)
	}
	if _, err := memory.AppendUnique(f.ws.ID, MemoryEntry{Type: MemoryTypeFact, Date: "2026-10-19", Provenance: "user", Text: "Two."}); err != nil {
		t.Fatal(err)
	}
	entry.Text = "One, edited."
	if err := memory.EditAt(f.ws.ID, 0, entry); err != nil {
		t.Fatal(err)
	}
	if _, err := memory.Forget(f.ws.ID, "Two."); err != nil {
		t.Fatal(err)
	}
	if err := memory.DeleteAt(f.ws.ID, 0); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 5 {
		t.Fatalf("observer ran %d times, want 5: %v", len(seen), seen)
	}
}
