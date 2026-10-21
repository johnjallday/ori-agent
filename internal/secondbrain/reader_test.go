package secondbrain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

const outsideSentinel = "OUTSIDE-SENTINEL: this note is not in the vault."

func writeNote(t *testing.T, dir, rel, content string) string {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return full
}

func canonicalTemp(t *testing.T) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return base
}

// hostileTree is a vault beside a folder that is not part of it. The vault
// links into that folder twice and holds a file and a folder that cannot be
// opened.
type hostileTree struct {
	base, vault, outside string
}

func newHostileTree(t *testing.T) hostileTree {
	t.Helper()
	base := canonicalTemp(t)
	tree := hostileTree{base: base, vault: filepath.Join(base, "Second Brain"), outside: filepath.Join(base, "outside")}
	writeNote(t, tree.outside, "secret.md", outsideSentinel+"\n- [ ] outside task\n")
	writeNote(t, tree.vault, ".obsidian/app.json", "{}")
	writeNote(t, tree.vault, ".obsidian/snippets/theme.md", "settings, not a note")
	writeNote(t, tree.vault, "Inbox.md", "# Inbox\n- [ ] call the distributor\n")
	writeNote(t, tree.vault, "Projects/Album.md", "# Album\n- [ ] master track 3\n")
	writeNote(t, tree.vault, ".trash/Deleted.md", "- [ ] deleted task\n")
	writeNote(t, tree.vault, ".git/HEAD.md", "not a note")
	writeNote(t, tree.vault, "node_modules/pkg/README.md", "a dependency")
	writeNote(t, tree.vault, ".hidden/Private.md", "hidden folder")
	writeNote(t, tree.vault, ".dotfile.md", "hidden file")
	writeNote(t, tree.vault, "Attachments/cover.png", "png")
	if err := os.Symlink(filepath.Join(tree.outside, "secret.md"), filepath.Join(tree.vault, "Linked Note.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(tree.outside, filepath.Join(tree.vault, "Linked Folder")); err != nil {
		t.Fatal(err)
	}
	locked := writeNote(t, tree.vault, "Locked.md", "- [ ] cannot be read\n")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	lockedDir := filepath.Join(tree.vault, "Locked Folder")
	writeNote(t, lockedDir, "Inside.md", "- [ ] cannot be listed\n")
	if err := os.Chmod(lockedDir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(lockedDir, 0o750) // #nosec G302 -- restore the test's own folder so TempDir can remove it
		_ = os.Chmod(locked, 0o600)
	})
	return tree
}

// snapshot records every entry under dir: its name, kind, permissions, size,
// modification time, and a regular file's bytes. An entry that cannot be read
// is recorded by its metadata alone, so equal snapshots mean nothing under dir
// was created, removed, renamed, rewritten or touched.
func snapshot(t *testing.T, dir string) string {
	t.Helper()
	var lines []string
	var walk func(path string)
	walk = func(path string) {
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(dir, path)
		line := fmt.Sprintf("%s %v %d %d", rel, info.Mode(), info.Size(), info.ModTime().UnixNano())
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			target, _ := os.Readlink(path)
			line += " -> " + target
		case info.Mode().IsRegular():
			if data, err := os.ReadFile(path); err == nil { // #nosec G304 -- the test's own temp tree
				sum := sha256.Sum256(data)
				line += " " + hex.EncodeToString(sum[:])
			}
		case info.IsDir():
			entries, err := os.ReadDir(path)
			if err == nil {
				for _, entry := range entries {
					walk(filepath.Join(path, entry.Name()))
				}
			}
		}
		lines = append(lines, line)
	}
	walk(dir)
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func relPaths(notes []Note) []string {
	out := make([]string, 0, len(notes))
	for _, note := range notes {
		out = append(out, note.RelPath)
	}
	sort.Strings(out)
	return out
}

func TestRead_NeverLeavesTheFolderNeverFollowsALinkNeverWrites(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads files whatever their permissions")
	}
	tree := newHostileTree(t)
	before := snapshot(t, tree.base)

	var opened []string
	reading, err := Read(context.Background(), tree.vault, ReadOptions{onOpen: func(rel string) { opened = append(opened, rel) }})
	if err != nil {
		t.Fatal(err)
	}

	if after := snapshot(t, tree.base); after != before {
		t.Fatalf("the read changed the tree.\nbefore:\n%s\nafter:\n%s", before, after)
	}
	for _, rel := range opened {
		info, err := os.Lstat(filepath.Join(tree.vault, filepath.FromSlash(rel)))
		if err != nil || !info.Mode().IsRegular() {
			t.Fatalf("opened %q, which is not a regular file inside the vault (%v)", rel, err)
		}
		if strings.HasPrefix(rel, "Linked") {
			t.Fatalf("opened %q through a link", rel)
		}
	}
	for _, note := range reading.Notes {
		if strings.Contains(string(note.Content), "OUTSIDE-SENTINEL") {
			t.Fatalf("note %q holds text from outside the vault", note.RelPath)
		}
	}

	if got, want := strings.Join(relPaths(reading.Notes), ","), "Inbox.md,Projects/Album.md"; got != want {
		t.Fatalf("notes = %s, want %s", got, want)
	}
	if reading.Links != 2 {
		t.Fatalf("Links = %d, want 2 (a note and a folder, counted, not followed)", reading.Links)
	}
	if reading.Unreadable != 2 {
		t.Fatalf("Unreadable = %d, want 2 (Locked.md and Locked Folder)", reading.Unreadable)
	}
	if reading.Listed != 3 {
		t.Fatalf("Listed = %d, want 3 (Inbox, Album, Locked)", reading.Listed)
	}
	if reading.Partial {
		t.Fatalf("read was partial: %s", reading.PartialReason)
	}
	if reading.Name != "Second Brain" || reading.Root != tree.vault {
		t.Fatalf("Name/Root = %q %q", reading.Name, reading.Root)
	}
}

func TestOpenNote_RefusesALinkAndAPathThroughALinkedFolder(t *testing.T) {
	tree := newHostileTree(t)
	root, err := os.OpenRoot(tree.vault)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	for _, rel := range []string{"Linked Note.md", "Linked Folder/secret.md", "../outside/secret.md"} {
		if file, err := openNote(root, rel); err == nil {
			_ = file.Close()
			t.Errorf("openNote(%q) opened a file outside the vault", rel)
		}
	}
	file, err := openNote(root, "Inbox.md")
	if err != nil {
		t.Fatalf("openNote(Inbox.md) = %v", err)
	}
	_ = file.Close()
}

func TestRead_RefusesARootThatIsNotARealFolder(t *testing.T) {
	base := canonicalTemp(t)
	vault := filepath.Join(base, "Vault")
	writeNote(t, vault, "Inbox.md", "- [ ] x\n")
	link := filepath.Join(base, "Link")
	if err := os.Symlink(vault, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(context.Background(), link, ReadOptions{}); !errors.Is(err, ErrNotDirectory) {
		t.Fatalf("a linked root: err = %v, want ErrNotDirectory", err)
	}
	if _, err := Read(context.Background(), filepath.Join(vault, "Inbox.md"), ReadOptions{}); !errors.Is(err, ErrNotDirectory) {
		t.Fatalf("a file root: err = %v, want ErrNotDirectory", err)
	}
	if _, err := Read(context.Background(), filepath.Join(base, "Gone"), ReadOptions{}); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a missing root: err = %v, want not-exist", err)
	}
}

func TestRead_ListsMarkdownToDepthSix(t *testing.T) {
	vault := canonicalTemp(t)
	writeNote(t, vault, "One.md", "x")
	writeNote(t, vault, "Two.markdown", "x")
	writeNote(t, vault, "Three.MD", "x")
	writeNote(t, vault, "a/b/c/d/e/Six.md", "x")
	writeNote(t, vault, "a/b/c/d/e/f/Seven.md", "x")
	writeNote(t, vault, "Plain.txt", "x")
	writeNote(t, vault, "Board.canvas", "x")
	reading, err := Read(context.Background(), vault, ReadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(relPaths(reading.Notes), ",")
	if want := "One.md,Three.MD,Two.markdown,a/b/c/d/e/Six.md"; got != want {
		t.Fatalf("notes = %s, want %s", got, want)
	}
}

func setModTime(t *testing.T, path string, at time.Time) {
	t.Helper()
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

func TestRead_OpensTheMostRecentlyModifiedFirstAndStopsAtTheOpenCap(t *testing.T) {
	vault := canonicalTemp(t)
	day := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	for i, name := range []string{"Oldest.md", "Older.md", "Newer.md", "Newest.md"} {
		setModTime(t, writeNote(t, vault, name, "x"), day.Add(time.Duration(i)*24*time.Hour))
	}
	reading, err := Read(context.Background(), vault, ReadOptions{MaxOpened: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(reading.Notes) != 2 || reading.Notes[0].RelPath != "Newest.md" || reading.Notes[1].RelPath != "Newer.md" {
		t.Fatalf("notes = %v, want Newest then Newer", relPaths(reading.Notes))
	}
	if !reading.Partial || reading.PartialReason != PartialOpened || reading.Unopened != 2 || reading.Listed != 4 {
		t.Fatalf("partial=%v reason=%q unopened=%d listed=%d", reading.Partial, reading.PartialReason, reading.Unopened, reading.Listed)
	}
}

func TestRead_StopsListingAtTheListCap(t *testing.T) {
	vault := canonicalTemp(t)
	for i := range 5 {
		writeNote(t, vault, fmt.Sprintf("Note %d.md", i), "x")
	}
	reading, err := Read(context.Background(), vault, ReadOptions{MaxListed: 3})
	if err != nil {
		t.Fatal(err)
	}
	if reading.Listed != 3 || len(reading.Notes) != 3 || reading.PartialReason != PartialListed {
		t.Fatalf("listed=%d notes=%d reason=%q", reading.Listed, len(reading.Notes), reading.PartialReason)
	}
}

func TestRead_SkipsAndCountsAFileOverThePerFileCap(t *testing.T) {
	vault := canonicalTemp(t)
	writeNote(t, vault, "Small.md", "0123456789")
	writeNote(t, vault, "Large.md", strings.Repeat("x", 11))
	reading, err := Read(context.Background(), vault, ReadOptions{MaxFileBytes: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(relPaths(reading.Notes), ","); got != "Small.md" || reading.TooLarge != 1 || reading.Partial {
		t.Fatalf("notes=%s tooLarge=%d partial=%v", got, reading.TooLarge, reading.Partial)
	}
}

func TestRead_StopsAtTheTotalByteCap(t *testing.T) {
	vault := canonicalTemp(t)
	day := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	for i := range 3 {
		setModTime(t, writeNote(t, vault, fmt.Sprintf("N%d.md", i), "0123456789"), day.Add(time.Duration(i)*time.Hour))
	}
	reading, err := Read(context.Background(), vault, ReadOptions{MaxTotalBytes: 25})
	if err != nil {
		t.Fatal(err)
	}
	if len(reading.Notes) != 2 || reading.PartialReason != PartialBytes || reading.Unopened != 1 {
		t.Fatalf("notes=%d reason=%q unopened=%d", len(reading.Notes), reading.PartialReason, reading.Unopened)
	}
}

func TestRead_KeepsWhatItHasWhenTheTimeBudgetRunsOut(t *testing.T) {
	vault := canonicalTemp(t)
	for i := range 20 {
		writeNote(t, vault, fmt.Sprintf("N%02d.md", i), "x")
	}
	clock := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	tick := func() time.Time {
		clock = clock.Add(time.Second)
		return clock
	}
	reading, err := Read(context.Background(), vault, ReadOptions{Budget: 10 * time.Second, Now: tick})
	if err != nil {
		t.Fatal(err)
	}
	if !reading.Partial || reading.PartialReason != PartialTime {
		t.Fatalf("partial=%v reason=%q, want the time bound", reading.Partial, reading.PartialReason)
	}
	if len(reading.Notes) >= 20 {
		t.Fatalf("read %d notes, want fewer than all 20", len(reading.Notes))
	}
}

func TestRead_StopsWhenTheCallerGivesUp(t *testing.T) {
	vault := canonicalTemp(t)
	writeNote(t, vault, "Inbox.md", "x")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reading, err := Read(ctx, vault, ReadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !reading.Partial || len(reading.Notes) != 0 {
		t.Fatalf("partial=%v notes=%d, want an empty partial read", reading.Partial, len(reading.Notes))
	}
}
