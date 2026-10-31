package secondbrain

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCountWritten_CountsMarkedNotesInTheOwnedFoldersOnly(t *testing.T) {
	tree := newWriterTree(t)
	// newWriterTree holds: Inbox/ori-note.md (capture, intact), Daily/2026-10-30.md
	// (journal, edited), Inbox/mine.md (the user's), three links, README.md and
	// Projects/Album.md outside.
	writeNote(t, tree.vault, "Projects/Ori-looking.md", string(RenderNote(NoteKindCapture, []byte("not in an owned folder\n"))))
	writeNote(t, tree.vault, "Inbox/notes.txt", string(RenderNote(NoteKindCapture, []byte("not markdown\n"))))
	writeNote(t, tree.vault, "Inbox/.hidden.md", string(RenderNote(NoteKindCapture, []byte("hidden\n"))))
	writeNote(t, tree.vault, "Inbox/pasted.md", "# Pasted\n"+string(RenderNote(NoteKindCapture, []byte("marker not first\n"))))
	// Later than the tree's other files, which carry the time the test ran.
	newest := time.Now().Add(48 * time.Hour).Truncate(time.Second)
	full := writeNote(t, tree.vault, "Inbox/newest.md", string(RenderNote(NoteKindSeed, []byte("newest\n"))))
	if err := os.Chtimes(full, newest, newest); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, tree.base)

	written, err := CountWritten(tree.vault)
	if err != nil {
		t.Fatal(err)
	}
	// ori-note, the edited Daily note (still marked), and newest.
	if written.Count != 3 || !written.LastAt.Equal(newest) || written.Partial {
		t.Fatalf("written = %+v", written)
	}
	if after := snapshot(t, tree.base); after != before {
		t.Fatalf("counting changed the tree.\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestCountWritten_AMissingOwnedFolderOrALinkedOneCountsNothing(t *testing.T) {
	tree := newWriterTree(t)
	if err := os.RemoveAll(filepath.Join(tree.vault, "Daily")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(tree.vault, "Inbox")); err != nil {
		t.Fatal(err)
	}
	writeNote(t, tree.outside, "ori.md", string(RenderNote(NoteKindCapture, []byte("outside\n"))))
	if err := os.Symlink(tree.outside, filepath.Join(tree.vault, "Inbox")); err != nil {
		t.Fatal(err)
	}
	written, err := CountWritten(tree.vault)
	if err != nil || written.Count != 0 || !written.LastAt.IsZero() {
		t.Fatalf("written = %+v, %v", written, err)
	}
	if _, err := CountWritten(filepath.Join(tree.base, "gone")); err == nil {
		t.Fatal("a missing folder was not an error")
	}
}
