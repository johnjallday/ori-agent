package secondbrain

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// treeSnapshot records every entry's bytes, mode and modification time.
func treeSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		content := ""
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			content = string(data)
		}
		out[path] = fmt.Sprintf("%s|%s|%s", info.Mode(), info.ModTime().Format(time.RFC3339Nano), content)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func writeAged(t *testing.T, path, content string, age time.Duration) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	at := fixtureNow.Add(-age)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

func TestListInbox_ListsNewestFirstWithKindAndTitleAndWritesNothing(t *testing.T) {
	root := canonicalTemp(t)
	inbox := filepath.Join(root, FolderInbox)
	writeAged(t, filepath.Join(inbox, "README.md"), "# Inbox\n", time.Hour)
	writeAged(t, filepath.Join(inbox, "capture.md"), string(RenderNote(NoteKindCapture, []byte("Call <Dana> back\n"))), time.Minute)
	writeAged(t, filepath.Join(inbox, "seed.md"), string(RenderNote(NoteKindSeed, []byte("# Renew the lease\n\n- [ ] Renew the lease\n"))), 2*time.Hour)
	writeAged(t, filepath.Join(inbox, "edited.md"), strings.Replace(string(RenderNote(NoteKindCapture, []byte("Mine\n"))), "Mine", "Mine now", 1), 3*time.Hour)
	writeAged(t, filepath.Join(inbox, "mine.markdown"), "\n\nFirst line\n# The heading\n", 4*time.Hour)
	writeAged(t, filepath.Join(inbox, "secret.md"), "gh"+"p_"+strings.Repeat("Vv7", 8)+"\n", 5*time.Hour)
	writeAged(t, filepath.Join(inbox, "empty.md"), "", 6*time.Hour)
	writeAged(t, filepath.Join(inbox, ".hidden.md"), "# hidden\n", time.Second)
	writeAged(t, filepath.Join(inbox, "picture.png"), "not a note", time.Second)
	writeAged(t, filepath.Join(inbox, "nested", "deep.md"), "# deep\n", time.Second)
	outside := filepath.Join(canonicalTemp(t), "outside.md")
	writeAged(t, outside, "# outside the folder\n", time.Second)
	if err := os.Symlink(outside, filepath.Join(inbox, "link.md")); err != nil {
		t.Fatal(err)
	}
	before := treeSnapshot(t, root)

	got, err := ListInbox(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		name, kind, title string
		owned             bool
	}{
		{"capture.md", NoteKindCapture, "Call Dana back", true},
		{"seed.md", NoteKindSeed, "Renew the lease", true},
		{"edited.md", NoteKindUser, "Mine now", false},
		{"mine.markdown", NoteKindUser, "The heading", false},
		{"secret.md", NoteKindUser, "secret", false},
		{"empty.md", NoteKindUser, "empty", false},
	}
	if len(got.Notes) != len(want) || got.Total != len(want) || got.Partial {
		t.Fatalf("listing = %+v", got)
	}
	for index, note := range got.Notes {
		if w := want[index]; note.Name != w.name || note.Kind != w.kind || note.Title != w.title || note.OwnedByOri != w.owned || note.Modified.IsZero() {
			t.Errorf("note %d = %+v, want %+v", index, note, w)
		}
	}
	if after := treeSnapshot(t, root); fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatal("listing the Inbox changed the folder")
	}
}

func TestListInbox_ReadsABoundedPrefixAndABoundedList(t *testing.T) {
	root := canonicalTemp(t)
	inbox := filepath.Join(root, FolderInbox)
	// The title sits past the peek: it must not be seen, and a long heading folds.
	writeAged(t, filepath.Join(inbox, "long.md"), "\n"+strings.Repeat(" \n", inboxPeekBytes)+"# Past the bound\n", 0)
	writeAged(t, filepath.Join(inbox, "wide.md"), "# "+strings.Repeat("é", maxInboxTitleRunes+50)+"\n", time.Minute)
	got, err := ListInbox(root)
	if err != nil {
		t.Fatal(err)
	}
	if got.Notes[0].Title != "long" {
		t.Fatalf("a title past the %d-byte peek was read: %q", inboxPeekBytes, got.Notes[0].Title)
	}
	if utf8.RuneCountInString(got.Notes[1].Title) > maxInboxTitleRunes {
		t.Fatalf("title = %d runes", utf8.RuneCountInString(got.Notes[1].Title))
	}

	for index := range MaxInboxNotes + 5 {
		writeAged(t, filepath.Join(inbox, fmt.Sprintf("n%03d.md", index)), "x\n", time.Duration(index+2)*time.Minute)
	}
	got, err = ListInbox(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Notes) != MaxInboxNotes || !got.Partial || got.Total != MaxInboxNotes+7 {
		t.Fatalf("notes = %d, total %d, partial %v", len(got.Notes), got.Total, got.Partial)
	}
}

func TestListInbox_NoInboxIsAnEmptyListAndNoFolderIsAnError(t *testing.T) {
	got, err := ListInbox(canonicalTemp(t))
	if err != nil || len(got.Notes) != 0 || got.Notes == nil {
		t.Fatalf("no Inbox = %+v, %v", got, err)
	}
	if _, err := ListInbox(filepath.Join(canonicalTemp(t), "gone")); err == nil {
		t.Fatal("a missing folder listed")
	}
}

func TestListInbox_RefusesAnInboxThatIsALink(t *testing.T) {
	root := canonicalTemp(t)
	elsewhere := canonicalTemp(t)
	writeAged(t, filepath.Join(elsewhere, "private.md"), "# private\n", 0)
	if err := os.Symlink(elsewhere, filepath.Join(root, FolderInbox)); err != nil {
		t.Fatal(err)
	}
	got, err := ListInbox(root)
	if err != nil || len(got.Notes) != 0 {
		t.Fatalf("a linked Inbox was followed: %+v, %v", got, err)
	}
}

func TestReadTodayNote_SaysWhoOwnsTheDay(t *testing.T) {
	root := canonicalTemp(t)
	daily := filepath.Join(root, FolderDaily)
	const date = "2026-10-21"
	state := func() string {
		t.Helper()
		got, err := ReadTodayNote(root, date)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := state(); got != TodayNoteNone {
		t.Fatalf("no Daily folder = %q", got)
	}
	path := filepath.Join(daily, date+".md")
	writeAged(t, path, string(RenderNote(NoteKindJournal, []byte("Shipped it.\n"))), 0)
	before := treeSnapshot(t, root)
	if got := state(); got != TodayNoteOri {
		t.Fatalf("Ori's note = %q", got)
	}
	if fmt.Sprint(treeSnapshot(t, root)) != fmt.Sprint(before) {
		t.Fatal("reading the day changed the folder")
	}
	writeAged(t, path, string(RenderNote(NoteKindJournal, []byte("Shipped it.\n")))+"And my own line.\n", 0)
	if got := state(); got != TodayNoteUser {
		t.Fatalf("an edited note = %q", got)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(canonicalTemp(t), "x.md")
	writeAged(t, outside, string(RenderNote(NoteKindJournal, []byte("x\n"))), 0)
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if got := state(); got != TodayNoteUser {
		t.Fatalf("a link = %q; want it read as the user's, never followed", got)
	}
}
