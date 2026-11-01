package secondbrain

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func createdSource(path string) Source {
	source := testSource(path)
	source.Origin = OriginCreatedByOri
	return source
}

func newWriterFor(t *testing.T, root string) *Writer {
	t.Helper()
	source := createdSource(root)
	writer, err := NewWriter(&source)
	if err != nil {
		t.Fatal(err)
	}
	return writer
}

// writerTree is a folder Ori created, holding the user's notes, an Ori note
// the user has edited, an intact Ori note, and links pointing out of the folder
// and across it, beside a folder that is not part of it.
type writerTree struct {
	base, vault, outside string
}

func newWriterTree(t *testing.T) writerTree {
	t.Helper()
	base := canonicalTemp(t)
	tree := writerTree{base: base, vault: filepath.Join(base, "Second Brain"), outside: filepath.Join(base, "outside")}
	writeNote(t, tree.outside, "secret.md", outsideSentinel)
	writeNote(t, tree.vault, ".obsidian/app.json", "{}")
	writeNote(t, tree.vault, "README.md", "how this works\n")
	writeNote(t, tree.vault, "Projects/Album.md", "# Album\n")
	writeNote(t, tree.vault, "Inbox/mine.md", "# Written by the user\n")
	writeNote(t, tree.vault, "Inbox/ori-note.md", string(RenderNote(NoteKindCapture, []byte("a capture\n"))))
	writeNote(t, tree.vault, "Daily/2026-10-30.md", string(RenderNote(NoteKindJournal, []byte("the journal\n")))+"and a line the user added\n")
	if err := os.Symlink(filepath.Join(tree.outside, "secret.md"), filepath.Join(tree.vault, "Inbox", "linked-note.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(tree.vault, "README.md"), filepath.Join(tree.vault, "Inbox", "linked-inside.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(tree.outside, filepath.Join(tree.vault, "Inbox", "linked-folder")); err != nil {
		t.Fatal(err)
	}
	return tree
}

// addFillInTargets gives a tree the user's own notes, and links, where the
// fill-in writes: People and a project folder.
func addFillInTargets(t *testing.T, tree writerTree) {
	t.Helper()
	writeNote(t, tree.vault, "People/README.md", "# People\n")
	writeNote(t, tree.vault, "People/Mine.md", "# Someone the user wrote about\n")
	writeNote(t, tree.vault, "Projects/README.md", "# Projects\n")
	writeNote(t, tree.vault, "Projects/Album/README.md", "# The user's project\n")
	if err := os.Symlink(filepath.Join(tree.outside, "secret.md"), filepath.Join(tree.vault, "People", "linked-note.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(tree.outside, filepath.Join(tree.vault, "Projects", "Linked Out")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(tree.vault, "Projects", "Album"), filepath.Join(tree.vault, "Projects", "Linked In")); err != nil {
		t.Fatal(err)
	}
}

// TestWrite_StaysInsideOwnedFoldersNeverFollowsALinkNeverTouchesUserNotes is
// the write-side twin of TestRead_NeverLeavesTheFolderNeverFollowsALinkNeverWrites
// (FR21). Each refusal leaves a byte, mode and mtime snapshot of the whole tree
// unchanged, and each allowed write changes only its own path.
func TestWrite_StaysInsideOwnedFoldersNeverFollowsALinkNeverTouchesUserNotes(t *testing.T) {
	tree := newWriterTree(t)
	addFillInTargets(t, tree)
	writer := newWriterFor(t, tree.vault)
	body := []byte("new text\n")

	refusals := map[string]struct {
		rel  string
		want error
	}{
		"a user's note":                 {"Inbox/mine.md", ErrUserModified},
		"an Ori note the user edited":   {"Daily/2026-10-30.md", ErrUserModified},
		"a link pointing out":           {"Inbox/linked-note.md", ErrOutsideOwnedFolders},
		"a link pointing inside":        {"Inbox/linked-inside.md", ErrOutsideOwnedFolders},
		"through a linked folder":       {"Inbox/linked-folder/new.md", ErrOutsideOwnedFolders},
		"the root README":               {"README.md", ErrOutsideOwnedFolders},
		"another folder":                {"Projects/new.md", ErrOutsideOwnedFolders},
		"Obsidian's settings":           {".obsidian/new.md", ErrOutsideOwnedFolders},
		"climbing out":                  {"Inbox/../../outside/new.md", ErrOutsideOwnedFolders},
		"climbing across":               {"Inbox/../Projects/new.md", ErrOutsideOwnedFolders},
		"an absolute path":              {filepath.Join(tree.outside, "new.md"), ErrOutsideOwnedFolders},
		"the folder itself":             {"Inbox", ErrOutsideOwnedFolders},
		"a nested path":                 {"Inbox/sub/new.md", ErrOutsideOwnedFolders},
		"a hidden note":                 {"Inbox/.hidden.md", ErrOutsideOwnedFolders},
		"not Markdown":                  {"Inbox/script.sh", ErrOutsideOwnedFolders},
		"a backslash":                   {`Inbox\new.md`, ErrOutsideOwnedFolders},
		"a different case of the owned": {"inbox/new.md", ErrOutsideOwnedFolders},
		"a control character":           {"Inbox/a\nb.md", ErrOutsideOwnedFolders},
		"nothing":                       {"", ErrOutsideOwnedFolders},

		// The fill-in's targets hold the same line.
		"a person the user wrote":      {"People/Mine.md", ErrUserModified},
		"a project the user wrote":     {"Projects/Album/README.md", ErrUserModified},
		"People's own README":          {"People/README.md", ErrOutsideOwnedFolders},
		"People's README in lower":     {"People/readme.md", ErrOutsideOwnedFolders},
		"a link in People":             {"People/linked-note.md", ErrOutsideOwnedFolders},
		"nested in People":             {"People/sub/new.md", ErrOutsideOwnedFolders},
		"hidden in People":             {"People/.hidden.md", ErrOutsideOwnedFolders},
		"not Markdown in People":       {"People/run.sh", ErrOutsideOwnedFolders},
		"a different case of People":   {"people/new.md", ErrOutsideOwnedFolders},
		"Projects' own README":         {"Projects/README.md", ErrOutsideOwnedFolders},
		"another note in a project":    {"Projects/Album/notes.md", ErrOutsideOwnedFolders},
		"nested in a project":          {"Projects/Album/sub/README.md", ErrOutsideOwnedFolders},
		"a hidden project":             {"Projects/.hidden/README.md", ErrOutsideOwnedFolders},
		"a project linked out":         {"Projects/Linked Out/README.md", ErrOutsideOwnedFolders},
		"a project linked across":      {"Projects/Linked In/README.md", ErrOutsideOwnedFolders},
		"climbing out of a project":    {"Projects/../../outside/README.md", ErrOutsideOwnedFolders},
		"another root note":            {"Other.md", ErrOutsideOwnedFolders},
		"the facts note in lower case": {"what ori knows about you.md", ErrOutsideOwnedFolders},
		"the facts note in a folder":   {"Inbox/../" + FactsNoteRel, ErrOutsideOwnedFolders},
		"Areas":                        {"Areas/new.md", ErrOutsideOwnedFolders},
		"Resources":                    {"Resources/new.md", ErrOutsideOwnedFolders},
		"Archive":                      {"Archive/new.md", ErrOutsideOwnedFolders},
	}
	for name, tc := range refusals {
		t.Run(name, func(t *testing.T) {
			before := snapshot(t, tree.base)
			if _, err := writer.Write(tc.rel, NoteKindCapture, body); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if after := snapshot(t, tree.base); after != before {
				t.Fatalf("a refused write changed the tree.\nbefore:\n%s\nafter:\n%s", before, after)
			}
		})
	}

	allowed := func(t *testing.T, rel string, wantReplaced bool) {
		t.Helper()
		changes := ownPaths(tree.vault, rel)
		protected := withoutPath(snapshot(t, tree.base), changes...)
		result, err := writer.Write(rel, NoteKindCapture, body)
		if err != nil {
			t.Fatalf("Write(%s): %v", rel, err)
		}
		if result.Rel != rel || result.Replaced != wantReplaced {
			t.Fatalf("result = %+v", result)
		}
		after := withoutPath(snapshot(t, tree.base), changes...)
		if after != protected {
			t.Fatalf("writing %s changed something else.\nbefore:\n%s\nafter:\n%s", rel, protected, after)
		}
		content, err := os.ReadFile(filepath.Join(tree.vault, filepath.FromSlash(rel))) // #nosec G304 -- the test's own temp tree
		if err != nil {
			t.Fatal(err)
		}
		if mark := ReadMark(content); !mark.OwnedByOri || mark.Kind != NoteKindCapture {
			t.Fatalf("written note mark = %+v", mark)
		}
		info, err := os.Lstat(filepath.Join(tree.vault, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o644 {
			t.Fatalf("mode = %v, want 0644", info.Mode().Perm())
		}
	}
	allowed(t, "Inbox/new.md", false)
	allowed(t, "Daily/2026-10-31.md", false)
	allowed(t, "Inbox/ori-note.md", true)
	allowed(t, "People/Dana Lee.md", false)
	allowed(t, "People/Dana Lee.md", true)
	allowed(t, "Projects/Launch/README.md", false)
	allowed(t, "Projects/Launch/README.md", true)
	allowed(t, FactsNoteRel, false)
	allowed(t, FactsNoteRel, true)

	assertNoTempFiles(t, tree.vault)
}

func TestWrite_AConnectedSourceIsNeverWritten(t *testing.T) {
	tree := newWriterTree(t)
	before := snapshot(t, tree.base)
	for name, origin := range map[string]string{"connected": OriginConnected, "missing": "", "unknown": "somebody_else"} {
		t.Run(name, func(t *testing.T) {
			source := testSource(tree.vault)
			source.Origin = origin
			if _, err := NewWriter(&source); !errors.Is(err, ErrNotOwned) {
				t.Fatalf("err = %v, want ErrNotOwned", err)
			}
		})
	}
	if _, err := NewWriter(nil); !errors.Is(err, ErrNotOwned) {
		t.Fatalf("nil source: err = %v", err)
	}
	if after := snapshot(t, tree.base); after != before {
		t.Fatalf("the tree changed.\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestWrite_RefusesAnOwnedFolderThatIsALink(t *testing.T) {
	for name, target := range map[string]func(tree writerTree) string{
		"to a folder outside": func(tree writerTree) string { return tree.outside },
		"to Projects, inside": func(tree writerTree) string { return filepath.Join(tree.vault, "Projects") },
	} {
		t.Run(name, func(t *testing.T) {
			tree := newWriterTree(t)
			if err := os.RemoveAll(filepath.Join(tree.vault, "Daily")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target(tree), filepath.Join(tree.vault, "Daily")); err != nil {
				t.Fatal(err)
			}
			before := snapshot(t, tree.base)
			if _, err := newWriterFor(t, tree.vault).Write("Daily/2026-10-31.md", NoteKindJournal, []byte("x")); !errors.Is(err, ErrOutsideOwnedFolders) {
				t.Fatalf("err = %v, want ErrOutsideOwnedFolders", err)
			}
			if after := snapshot(t, tree.base); after != before {
				t.Fatalf("the tree changed.\nbefore:\n%s\nafter:\n%s", before, after)
			}
		})
	}
}

func TestWrite_RefusesARootThatIsALink(t *testing.T) {
	tree := newWriterTree(t)
	link := filepath.Join(tree.base, "Linked Brain")
	if err := os.Symlink(tree.vault, link); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, tree.base)
	if _, err := newWriterFor(t, link).Write("Inbox/new.md", NoteKindCapture, []byte("x")); !errors.Is(err, ErrNotDirectory) {
		t.Fatalf("err = %v, want ErrNotDirectory", err)
	}
	if after := snapshot(t, tree.base); after != before {
		t.Fatalf("the tree changed.\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestWrite_RecreatesAnOwnedFolderTheUserDeleted(t *testing.T) {
	tree := newWriterTree(t)
	for folder, rel := range map[string]string{
		FolderInbox: "Inbox/note.md", FolderDaily: "Daily/note.md", FolderPeople: "People/note.md", FolderProjects: "Projects/Launch/README.md",
	} {
		if err := os.RemoveAll(filepath.Join(tree.vault, folder)); err != nil {
			t.Fatal(err)
		}
		if _, err := newWriterFor(t, tree.vault).Write(rel, NoteKindCapture, []byte("x\n")); err != nil {
			t.Fatalf("Write(%s): %v", rel, err)
		}
		if _, err := os.Lstat(filepath.Join(tree.vault, filepath.FromSlash(rel))); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWrite_RefusesAFactsNoteThatIsALink(t *testing.T) {
	tree := newWriterTree(t)
	if err := os.Symlink(filepath.Join(tree.outside, "secret.md"), filepath.Join(tree.vault, FactsNoteRel)); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, tree.base)
	if _, err := newWriterFor(t, tree.vault).Write(FactsNoteRel, NoteKindFacts, []byte("x")); !errors.Is(err, ErrOutsideOwnedFolders) {
		t.Fatalf("err = %v, want ErrOutsideOwnedFolders", err)
	}
	if after := snapshot(t, tree.base); after != before {
		t.Fatalf("the tree changed.\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestInspect_ReadsWithoutChangingAnything(t *testing.T) {
	tree := newWriterTree(t)
	addFillInTargets(t, tree)
	writer := newWriterFor(t, tree.vault)
	if err := os.RemoveAll(filepath.Join(tree.vault, FolderDaily)); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, tree.base)
	for rel, want := range map[string]struct{ exists, owned bool }{
		"Inbox/ori-note.md":             {true, true},
		"Inbox/mine.md":                 {true, false},
		"Inbox/linked-note.md":          {true, false},
		"Inbox/absent.md":               {false, false},
		"Daily/2026-10-31.md":           {false, false},
		"People/Mine.md":                {true, false},
		"Projects/Album/README.md":      {true, false},
		"Projects/Absent/README.md":     {false, false},
		"Projects/Linked Out/README.md": {true, false},
		FactsNoteRel:                    {false, false},
	} {
		status, err := writer.Inspect(rel)
		if err != nil {
			t.Fatalf("Inspect(%s): %v", rel, err)
		}
		if status.Exists != want.exists || status.Owned != want.owned {
			t.Errorf("Inspect(%s) = %+v, want %+v", rel, status, want)
		}
	}
	if status, _ := writer.Inspect("Inbox/ori-note.md"); status.Kind != NoteKindCapture || status.Hash == "" {
		t.Errorf("an owned note's status = %+v", status)
	}
	if _, err := writer.Inspect("Areas/new.md"); !errors.Is(err, ErrOutsideOwnedFolders) {
		t.Errorf("a path Ori never writes: err = %v", err)
	}
	if after := snapshot(t, tree.base); after != before {
		t.Fatalf("Inspect changed the tree.\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestWrite_AReplacementIsRefusedOnceTheUserEditsTheNote(t *testing.T) {
	tree := newWriterTree(t)
	writer := newWriterFor(t, tree.vault)
	rel := "Daily/2026-10-31.md"
	full := filepath.Join(tree.vault, "Daily", "2026-10-31.md")

	if _, err := writer.Write(rel, NoteKindJournal, []byte("first\n")); err != nil {
		t.Fatal(err)
	}
	result, err := writer.Write(rel, NoteKindJournal, []byte("re-saved\n"))
	if err != nil || !result.Replaced {
		t.Fatalf("a re-save of an intact note = %+v, %v", result, err)
	}
	content, err := os.ReadFile(full) // #nosec G304 -- the test's own temp tree
	if err != nil || !strings.HasSuffix(string(content), "re-saved\n") {
		t.Fatalf("content = %q, %v", content, err)
	}

	edited := string(content) + "the user's line\n"
	if err := os.WriteFile(full, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(rel, NoteKindJournal, []byte("again\n")); !errors.Is(err, ErrUserModified) {
		t.Fatalf("err = %v, want ErrUserModified", err)
	}
	content, err = os.ReadFile(full) // #nosec G304 -- the test's own temp tree
	if err != nil || string(content) != edited {
		t.Fatalf("the user's edit was lost: %q, %v", content, err)
	}
}

func TestWrite_RefusesAnOverSizeNoteAndAnOverSizeExistingOne(t *testing.T) {
	tree := newWriterTree(t)
	writer := newWriterFor(t, tree.vault)
	before := snapshot(t, tree.base)
	if _, err := writer.Write("Inbox/huge.md", NoteKindCapture, make([]byte, MaxNoteBytes)); !errors.Is(err, ErrNoteTooLarge) {
		t.Fatalf("err = %v, want ErrNoteTooLarge", err)
	}
	if after := snapshot(t, tree.base); after != before {
		t.Fatalf("the tree changed.\nbefore:\n%s\nafter:\n%s", before, after)
	}
	writeNote(t, tree.vault, "Inbox/big.md", strings.Repeat("x", MaxNoteBytes+1))
	if _, err := writer.Write("Inbox/big.md", NoteKindCapture, []byte("x")); !errors.Is(err, ErrUserModified) {
		t.Fatalf("err = %v, want ErrUserModified", err)
	}
}

func TestCreate_NeverReplacesWhateverIsAtTheName(t *testing.T) {
	tree := newWriterTree(t)
	writer := newWriterFor(t, tree.vault)
	// The user's note, Ori's intact note, an edited one, and links.
	for _, rel := range []string{"Inbox/mine.md", "Inbox/ori-note.md", "Daily/2026-10-30.md", "Inbox/linked-note.md", "Inbox/linked-inside.md"} {
		before := snapshot(t, tree.base)
		if _, err := writer.Create(rel, NoteKindCapture, []byte("new\n")); !errors.Is(err, ErrNoteExists) {
			t.Errorf("Create(%s): err = %v, want ErrNoteExists", rel, err)
		}
		if after := snapshot(t, tree.base); after != before {
			t.Fatalf("a refused Create(%s) changed the tree.\nbefore:\n%s\nafter:\n%s", rel, before, after)
		}
	}
	// A free name is written, and only that path changes; the boundary still holds.
	result, err := writer.Create("Inbox/fresh.md", NoteKindCapture, []byte("new\n"))
	if err != nil || result.Replaced || result.Rel != "Inbox/fresh.md" {
		t.Fatalf("Create = %+v, %v", result, err)
	}
	for _, rel := range []string{"Projects/new.md", "Inbox/sub/new.md", "README.md", "People/README.md"} {
		if _, err := writer.Create(rel, NoteKindCapture, []byte("x")); !errors.Is(err, ErrOutsideOwnedFolders) {
			t.Errorf("Create(%s): err = %v", rel, err)
		}
	}
	source := testSource(tree.vault)
	connected, err := NewWriter(&source)
	if err == nil || connected != nil {
		t.Fatalf("a connected source got a writer: %v", err)
	}
	assertNoTempFiles(t, tree.vault)
}

func TestWrite_RefusesAnUnknownKind(t *testing.T) {
	tree := newWriterTree(t)
	if _, err := newWriterFor(t, tree.vault).Write("Inbox/new.md", "other", []byte("x")); !errors.Is(err, ErrInvalidNote) {
		t.Fatalf("err = %v, want ErrInvalidNote", err)
	}
}

func TestPublish_NeverClobbersANoteSavedDuringTheWrite(t *testing.T) {
	tree := newWriterTree(t)
	root, err := pinFolder(tree.vault)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	dir, err := pinOwnedFolder(root, FolderInbox)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dir.Close() }()
	temp, err := writeTemp(dir, RenderNote(NoteKindCapture, []byte("Ori's\n")))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dir.Remove(temp) }()

	// The checks said the name was free; the user saves a note there before
	// the write is published.
	full := writeNote(t, tree.vault, "Inbox/raced.md", "the user's note\n")
	if err := publish(dir, temp, "raced.md", false, false); !errors.Is(err, ErrUserModified) {
		t.Fatalf("err = %v, want ErrUserModified", err)
	}
	if err := publish(dir, temp, "raced.md", false, true); !errors.Is(err, ErrNoteExists) {
		t.Fatalf("create: err = %v, want ErrNoteExists", err)
	}
	content, err := os.ReadFile(full) // #nosec G304 -- the test's own temp tree
	if err != nil || string(content) != "the user's note\n" {
		t.Fatalf("the note was clobbered: %q, %v", content, err)
	}
}

// ownPaths are the snapshot paths one write may change: the note and every
// folder above it, up to and including the root the note is in.
func ownPaths(vault, rel string) []string {
	base := filepath.Base(vault)
	paths := []string{filepath.Join(base, filepath.FromSlash(rel))}
	for dir := filepath.Dir(filepath.FromSlash(rel)); ; dir = filepath.Dir(dir) {
		paths = append(paths, filepath.Join(base, dir))
		if dir == "." {
			return paths
		}
	}
}

// withoutPath drops the lines for one note and its folders (whose size and
// mtime a new entry changes) from a snapshot.
func withoutPath(snapshot string, paths ...string) string {
	var kept []string
	for _, line := range strings.Split(snapshot, "\n") {
		skip := false
		for _, p := range paths {
			if strings.HasPrefix(line, p+" ") {
				skip = true
			}
		}
		if !skip {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

func assertNoTempFiles(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(entry.Name(), writeTempPrefix) {
			t.Errorf("temp file left behind: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
