//go:build darwin || linux

package filejanitor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/directoryid"
)

// moveFixture is a real folder on a real disk. Nothing here fakes the kernel: the
// races are made by changing the disk from inside the mover's hook.
type moveFixture struct {
	t      *testing.T
	root   string
	rootID string
	mover  *platformMover
}

func newMoveFixture(t *testing.T) *moveFixture {
	t.Helper()
	root := t.TempDir()
	info, err := os.Lstat(root)
	if err != nil {
		t.Fatal(err)
	}
	f := &moveFixture{t: t, root: root, rootID: directoryid.Witness(info), mover: &platformMover{}}
	if err := f.mover.Supported(root, f.rootID); err != nil {
		// The filesystem under the test's temp folder cannot do an exclusive rename.
		// That is a real result for this machine, not a pass: say so.
		t.Skipf("exclusive rename is not supported under %s: %v", os.TempDir(), err)
	}
	return f
}

func (f *moveFixture) abs(rel string) string {
	if rel == "" {
		return f.root
	}
	return filepath.Join(f.root, filepath.FromSlash(rel))
}

func (f *moveFixture) mkdir(rel string) DirRef {
	f.t.Helper()
	if err := os.MkdirAll(f.abs(rel), 0o750); err != nil {
		f.t.Fatal(err)
	}
	return f.dir(rel)
}

func (f *moveFixture) dir(rel string) DirRef {
	f.t.Helper()
	info, err := os.Lstat(f.abs(rel))
	if err != nil {
		f.t.Fatal(err)
	}
	return DirRef{Rel: rel, ID: directoryid.Witness(info)}
}

func (f *moveFixture) write(rel, content string) Fingerprint {
	f.t.Helper()
	if err := os.WriteFile(f.abs(rel), []byte(content), 0o600); err != nil {
		f.t.Fatal(err)
	}
	return f.fingerprint(rel)
}

func (f *moveFixture) fingerprint(rel string) Fingerprint {
	f.t.Helper()
	fp, err := currentFingerprintAt(filepath.Dir(f.abs(rel)), filepath.Base(rel))
	if err != nil {
		f.t.Fatal(err)
	}
	return fp
}

func (f *moveFixture) read(rel string) string {
	f.t.Helper()
	b, err := os.ReadFile(f.abs(rel))
	if err != nil {
		f.t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

func (f *moveFixture) exists(rel string) bool {
	_, err := os.Lstat(f.abs(rel))
	return err == nil
}

func (f *moveFixture) mustBeAbsent(rel string) {
	f.t.Helper()
	if f.exists(rel) {
		f.t.Fatalf("%s exists, but nothing should be there", rel)
	}
}

// replaceWithImpostor puts a different file (different inode, same size, same
// modification time) at rel. The original is kept under keepAs when given. The
// replacement is created before the original is moved, so the two inodes can
// never be the same one.
func (f *moveFixture) replaceWithImpostor(rel, content, keepAs string) {
	f.t.Helper()
	original, err := os.Lstat(f.abs(rel))
	if err != nil {
		f.t.Fatal(err)
	}
	tmp := f.abs(rel) + ".impostor"
	if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Chtimes(tmp, original.ModTime(), original.ModTime()); err != nil {
		f.t.Fatal(err)
	}
	if info, err := os.Lstat(tmp); err != nil || !info.ModTime().Equal(original.ModTime()) || info.Size() != original.Size() {
		f.t.Fatal("the impostor must match the original's size and modification time, so only the inode differs")
	}
	if keepAs != "" {
		if err := os.Rename(f.abs(rel), f.abs(keepAs)); err != nil {
			f.t.Fatal(err)
		}
	}
	if err := os.Rename(tmp, f.abs(rel)); err != nil {
		f.t.Fatal(err)
	}
}

// swapDir puts a different folder (different inode) at rel, keeping the old one
// under keepAs. The new folder exists before the old one moves.
func (f *moveFixture) swapDir(rel, keepAs string) {
	f.t.Helper()
	fresh := f.abs(rel) + ".fresh"
	if err := os.Mkdir(fresh, 0o750); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Rename(f.abs(rel), f.abs(keepAs)); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Rename(fresh, f.abs(rel)); err != nil {
		f.t.Fatal(err)
	}
}

func (f *moveFixture) request(from, to DirRef, fromName, toName string, expect Fingerprint) ConditionalMove {
	return ConditionalMove{Root: f.root, RootID: f.rootID, From: from, To: to, FromName: fromName, ToName: toName, Expect: expect}
}

func (f *moveFixture) move(m ConditionalMove) (Fingerprint, error) {
	return f.mover.MoveIfUnchanged(context.Background(), m)
}

func raceOf(t *testing.T, err error) *MoveRaceError {
	t.Helper()
	var race *MoveRaceError
	if !errors.As(err, &race) {
		t.Fatalf("expected a *MoveRaceError, got %T: %v", err, err)
	}
	return race
}

func TestConditionalMove_FileThenUndoUseTheSamePrimitive(t *testing.T) {
	f := newMoveFixture(t)
	taxes := f.mkdir("Taxes")
	rootRef := f.dir("")
	expect := f.write("report.pdf", "the reviewed bytes")

	after, err := f.move(f.request(rootRef, taxes, "report.pdf", "report.pdf", expect))
	if err != nil {
		t.Fatalf("file: %v", err)
	}
	if after.FileID != expect.FileID || after.Name != "report.pdf" || after.Size != expect.Size || !after.ModTime.Equal(expect.ModTime) {
		t.Fatalf("a rename keeps the file's identity: before %+v after %+v", expect, after)
	}
	if f.read("Taxes/report.pdf") != "the reviewed bytes" {
		t.Fatal("the bytes changed in the move")
	}
	f.mustBeAbsent("report.pdf")

	// Undo is the same primitive, driven by what the filing move returned.
	back, err := f.move(f.request(taxes, rootRef, "report.pdf", "report.pdf", after))
	if err != nil {
		t.Fatalf("undo: %v", err)
	}
	if back.FileID != expect.FileID || f.read("report.pdf") != "the reviewed bytes" {
		t.Fatalf("undo did not restore the same file: %+v", back)
	}
	f.mustBeAbsent("Taxes/report.pdf")
}

func TestConditionalMove_NeverReplacesAnOccupiedName(t *testing.T) {
	t.Run("occupied before the call", func(t *testing.T) {
		f := newMoveFixture(t)
		taxes := f.mkdir("Taxes")
		expect := f.write("report.pdf", "reviewed")
		f.write("Taxes/report.pdf", "someone else's file")

		_, err := f.move(f.request(f.dir(""), taxes, "report.pdf", "report.pdf", expect))
		if !errors.Is(err, ErrMoveDestinationOccupied) {
			t.Fatalf("want occupied, got %v", err)
		}
		if f.read("Taxes/report.pdf") != "someone else's file" || f.read("report.pdf") != "reviewed" {
			t.Fatal("a file changed")
		}
	})

	t.Run("a file appears after every check, before the rename", func(t *testing.T) {
		f := newMoveFixture(t)
		taxes := f.mkdir("Taxes")
		expect := f.write("report.pdf", "reviewed")
		f.mover.hook = func(stage moveStage) {
			if stage == stageBeforeRename {
				f.write("Taxes/report.pdf", "arrived at the last instant")
			}
		}

		_, err := f.move(f.request(f.dir(""), taxes, "report.pdf", "report.pdf", expect))
		if !errors.Is(err, ErrMoveDestinationOccupied) {
			t.Fatalf("the kernel must refuse the occupied name, got %v", err)
		}
		if f.read("Taxes/report.pdf") != "arrived at the last instant" || f.read("report.pdf") != "reviewed" {
			t.Fatal("a file was replaced or lost")
		}
	})
}

// A volume that treats names as equal when they differ only in case or in Unicode
// composition (APFS by default) must still refuse them: "occupied" is decided by
// the filesystem, not by comparing strings.
func TestConditionalMove_NamesTheFilesystemTreatsAsEqualAreOccupied(t *testing.T) {
	cases := []struct {
		name, occupant, wanted string
	}{
		{"case", "Report.pdf", "report.pdf"},
		{"unicode composition", "café.pdf", "café.pdf"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newMoveFixture(t)
			taxes := f.mkdir("Taxes")
			f.write("Taxes/"+tc.occupant, "occupant")
			if !f.exists("Taxes/" + tc.wanted) {
				t.Skipf("this volume treats %q and %q as different names, so there is no collision to refuse", tc.occupant, tc.wanted)
			}
			expect := f.write("source.pdf", "reviewed")

			_, err := f.move(f.request(f.dir(""), taxes, "source.pdf", tc.wanted, expect))
			if !errors.Is(err, ErrMoveDestinationOccupied) {
				t.Fatalf("want occupied, got %v", err)
			}
			if f.read("Taxes/"+tc.occupant) != "occupant" || f.read("source.pdf") != "reviewed" {
				t.Fatal("a file changed")
			}
		})
	}
}

func TestConditionalMove_SourceMustStillBeTheReviewedFile(t *testing.T) {
	t.Run("size changed", func(t *testing.T) {
		f := newMoveFixture(t)
		taxes := f.mkdir("Taxes")
		expect := f.write("report.pdf", "reviewed")
		f.write("report.pdf", "reviewed, then edited")
		assertNotMoved(t, f, taxes, expect, ErrMoveSourceChanged)
	})
	t.Run("modification time changed", func(t *testing.T) {
		f := newMoveFixture(t)
		taxes := f.mkdir("Taxes")
		expect := f.write("report.pdf", "reviewed")
		later := expect.ModTime.Add(time.Hour)
		if err := os.Chtimes(f.abs("report.pdf"), later, later); err != nil {
			t.Fatal(err)
		}
		assertNotMoved(t, f, taxes, expect, ErrMoveSourceChanged)
	})
	t.Run("different file, same size and time", func(t *testing.T) {
		f := newMoveFixture(t)
		taxes := f.mkdir("Taxes")
		expect := f.write("report.pdf", "reviewed")
		f.replaceWithImpostor("report.pdf", "impostor", "")
		assertNotMoved(t, f, taxes, expect, ErrMoveSourceChanged)
	})
	t.Run("gone", func(t *testing.T) {
		f := newMoveFixture(t)
		taxes := f.mkdir("Taxes")
		expect := f.write("report.pdf", "reviewed")
		if err := os.Remove(f.abs("report.pdf")); err != nil {
			t.Fatal(err)
		}
		assertRefused(t, f, taxes, expect, ErrMoveSourceChanged)
	})
	t.Run("a symlink", func(t *testing.T) {
		f := newMoveFixture(t)
		taxes := f.mkdir("Taxes")
		expect := f.write("report.pdf", "reviewed")
		f.write("target.txt", "link target")
		if err := os.Remove(f.abs("report.pdf")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("target.txt", f.abs("report.pdf")); err != nil {
			t.Fatal(err)
		}
		assertNotMoved(t, f, taxes, expect, ErrMoveSourceChanged)
		if f.read("target.txt") != "link target" {
			t.Fatal("the link's target changed")
		}
	})
	t.Run("a folder", func(t *testing.T) {
		f := newMoveFixture(t)
		taxes := f.mkdir("Taxes")
		expect := f.write("report.pdf", "reviewed")
		if err := os.Remove(f.abs("report.pdf")); err != nil {
			t.Fatal(err)
		}
		f.mkdir("report.pdf")
		assertNotMoved(t, f, taxes, expect, ErrMoveSourceChanged)
	})
	t.Run("a second hard link", func(t *testing.T) {
		f := newMoveFixture(t)
		taxes := f.mkdir("Taxes")
		expect := f.write("report.pdf", "reviewed")
		if err := os.Link(f.abs("report.pdf"), f.abs("alias.pdf")); err != nil {
			t.Fatal(err)
		}
		assertNotMoved(t, f, taxes, expect, ErrMoveSourceChanged)
		if f.read("alias.pdf") != "reviewed" {
			t.Fatal("the other name changed")
		}
	})
}

// assertRefused requires the move to be refused before any rename: the named
// error, no race error, and nothing at the destination.
func assertRefused(t *testing.T, f *moveFixture, to DirRef, expect Fingerprint, want error) {
	t.Helper()
	_, err := f.move(f.request(f.dir(""), to, "report.pdf", "report.pdf", expect))
	if !errors.Is(err, want) {
		t.Fatalf("want %v, got %v", want, err)
	}
	var race *MoveRaceError
	if errors.As(err, &race) {
		t.Fatalf("nothing should have been attempted, but got a race error: %v", err)
	}
	f.mustBeAbsent(to.Rel + "/report.pdf")
}

// assertNotMoved is assertRefused for a source that must still be there after.
func assertNotMoved(t *testing.T, f *moveFixture, to DirRef, expect Fingerprint, want error) {
	t.Helper()
	assertRefused(t, f, to, expect, want)
	if !f.exists("report.pdf") {
		t.Fatal("the source disappeared")
	}
}

// A different file arriving at the source name after the last check is moved
// instead of the reviewed one. The mover notices, and moves it back.
func TestConditionalMove_AnImpostorSwappedInAtTheLastInstantIsMovedBack(t *testing.T) {
	f := newMoveFixture(t)
	taxes := f.mkdir("Taxes")
	rootRef := f.dir("")
	expect := f.write("report.pdf", "reviewed")
	f.mover.hook = func(stage moveStage) {
		if stage == stageBeforeRename {
			f.replaceWithImpostor("report.pdf", "impostor", "kept-original.pdf")
		}
	}

	_, err := f.move(f.request(rootRef, taxes, "report.pdf", "report.pdf", expect))
	race := raceOf(t, err)
	if !race.RolledBack || !errors.Is(err, ErrMoveSourceChanged) {
		t.Fatalf("want a rolled-back source change, got %+v", race)
	}
	if f.read("report.pdf") != "impostor" {
		t.Fatal("the impostor was not put back under its name")
	}
	if f.read("kept-original.pdf") != "reviewed" {
		t.Fatal("the reviewed file was touched")
	}
	f.mustBeAbsent("Taxes/report.pdf")
}

// If the original name was taken again before the move back, Ori leaves the
// entry where it went and says where, and loses nothing.
func TestConditionalMove_WhenItCannotMoveBackItSaysWhereTheEntryIs(t *testing.T) {
	f := newMoveFixture(t)
	taxes := f.mkdir("Taxes")
	rootRef := f.dir("")
	expect := f.write("report.pdf", "reviewed")
	f.mover.hook = func(stage moveStage) {
		switch stage {
		case stageBeforeRename:
			f.replaceWithImpostor("report.pdf", "impostor", "kept-original.pdf")
		case stageBeforeRollback:
			f.write("report.pdf", "took the name meanwhile")
		}
	}

	_, err := f.move(f.request(rootRef, taxes, "report.pdf", "report.pdf", expect))
	race := raceOf(t, err)
	if race.RolledBack || race.LeftAt != "Taxes/report.pdf" || race.LeftIsReviewed {
		t.Fatalf("want left at Taxes/report.pdf and not the reviewed file, got %+v", race)
	}
	if f.read("Taxes/report.pdf") != "impostor" || f.read("report.pdf") != "took the name meanwhile" || f.read("kept-original.pdf") != "reviewed" {
		t.Fatal("a file was replaced or lost")
	}
}

func TestConditionalMove_ALinkSwappedInAtTheLastInstantIsMovedBackAndNeverFollowed(t *testing.T) {
	f := newMoveFixture(t)
	taxes := f.mkdir("Taxes")
	expect := f.write("report.pdf", "reviewed")
	f.write("target.txt", "link target")
	f.mover.hook = func(stage moveStage) {
		if stage == stageBeforeRename {
			if err := os.Rename(f.abs("report.pdf"), f.abs("kept-original.pdf")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("target.txt", f.abs("report.pdf")); err != nil {
				t.Fatal(err)
			}
		}
	}

	_, err := f.move(f.request(f.dir(""), taxes, "report.pdf", "report.pdf", expect))
	if race := raceOf(t, err); !race.RolledBack {
		t.Fatalf("want rolled back, got %+v", race)
	}
	if target, err := os.Readlink(f.abs("report.pdf")); err != nil || target != "target.txt" {
		t.Fatalf("the link was not put back as it was: %q %v", target, err)
	}
	if f.read("target.txt") != "link target" || f.read("kept-original.pdf") != "reviewed" {
		t.Fatal("a file was touched")
	}
}

func TestConditionalMove_FoldersMustStillBeTheReviewedFolders(t *testing.T) {
	t.Run("destination replaced by a different folder", func(t *testing.T) {
		f := newMoveFixture(t)
		taxes := f.mkdir("Taxes")
		expect := f.write("report.pdf", "reviewed")
		f.swapDir("Taxes", "Taxes.old")
		assertNotMoved(t, f, taxes, expect, ErrMoveParentChanged)
	})
	t.Run("source folder replaced by a different folder", func(t *testing.T) {
		f := newMoveFixture(t)
		inbox := f.mkdir("Inbox")
		taxes := f.mkdir("Taxes")
		expect := f.write("Inbox/report.pdf", "reviewed")
		f.swapDir("Inbox", "Inbox.old")
		f.write("Inbox/report.pdf", "reviewed") // a different file with the right name
		_, err := f.move(f.request(inbox, taxes, "report.pdf", "report.pdf", expect))
		if !errors.Is(err, ErrMoveParentChanged) {
			t.Fatalf("want parent changed, got %v", err)
		}
		f.mustBeAbsent("Taxes/report.pdf")
	})
	t.Run("root is not the reviewed root", func(t *testing.T) {
		f := newMoveFixture(t)
		taxes := f.mkdir("Taxes")
		expect := f.write("report.pdf", "reviewed")
		m := f.request(f.dir(""), taxes, "report.pdf", "report.pdf", expect)
		m.RootID = "1:2"
		if _, err := f.move(m); !errors.Is(err, ErrMoveParentChanged) {
			t.Fatalf("want parent changed, got %v", err)
		}
		if !f.exists("report.pdf") {
			t.Fatal("the source moved")
		}
	})
	t.Run("a link on the way is outside the root", func(t *testing.T) {
		f := newMoveFixture(t)
		real := f.mkdir("Real")
		expect := f.write("report.pdf", "reviewed")
		if err := os.Symlink("Real", f.abs("Taxes")); err != nil {
			t.Fatal(err)
		}
		linked := DirRef{Rel: "Taxes", ID: real.ID}
		if _, err := f.move(f.request(f.dir(""), linked, "report.pdf", "report.pdf", expect)); !errors.Is(err, ErrMoveOutsideRoot) {
			t.Fatalf("want outside root, got %v", err)
		}
		f.mustBeAbsent("Real/report.pdf")
	})
	t.Run("the destination folder leaves the root during the move", func(t *testing.T) {
		f := newMoveFixture(t)
		taxes := f.mkdir("Taxes")
		expect := f.write("report.pdf", "reviewed")
		elsewhere := t.TempDir() // another folder on the same disk, outside the root
		f.mover.hook = func(stage moveStage) {
			if stage == stageBeforeRename {
				if err := os.Rename(f.abs("Taxes"), filepath.Join(elsewhere, "Taxes")); err != nil {
					t.Fatal(err)
				}
			}
		}
		_, err := f.move(f.request(f.dir(""), taxes, "report.pdf", "report.pdf", expect))
		if race := raceOf(t, err); !race.RolledBack || !errors.Is(err, ErrMoveParentChanged) {
			t.Fatalf("want a rolled-back parent change, got %+v", race)
		}
		if f.read("report.pdf") != "reviewed" {
			t.Fatal("the file is not back")
		}
		if _, err := os.Lstat(filepath.Join(elsewhere, "Taxes", "report.pdf")); err == nil {
			t.Fatal("the file was left in the folder that left the root")
		}
	})
}

func TestConditionalMove_ARequestThatIsNotPlainIsRefusedBeforeTouchingTheDisk(t *testing.T) {
	// The root does not exist: a refusal for the wrong reason would be a
	// "folder changed" error, not "invalid".
	base := ConditionalMove{
		Root: "/does/not/exist", RootID: "1:1",
		From: DirRef{Rel: "", ID: "1:1"}, To: DirRef{Rel: "Taxes", ID: "1:2"},
		FromName: "a.pdf", ToName: "a.pdf",
		Expect: Fingerprint{Name: "a.pdf", Size: 1, ModTime: time.Unix(1, 0), FileID: "1:3"},
	}
	cases := map[string]func(*ConditionalMove){
		"parent segment in a folder": func(m *ConditionalMove) { m.To.Rel = "../Taxes" },
		"empty segment":              func(m *ConditionalMove) { m.To.Rel = "Taxes//2024" },
		"a name with a slash":        func(m *ConditionalMove) { m.ToName = "a/b.pdf" },
		"a dotdot name":              func(m *ConditionalMove) { m.ToName = ".." },
		"an empty name":              func(m *ConditionalMove) { m.FromName = "" },
		"no reviewed identity":       func(m *ConditionalMove) { m.Expect.FileID = "" },
		"no root identity":           func(m *ConditionalMove) { m.RootID = "" },
		"relative root":              func(m *ConditionalMove) { m.Root = "does/not/exist" },
		"same folder":                func(m *ConditionalMove) { m.To = m.From },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m := base
			mutate(&m)
			if _, err := (&platformMover{}).MoveIfUnchanged(context.Background(), m); !errors.Is(err, ErrMoveInvalid) {
				t.Fatalf("want an invalid request, got %v", err)
			}
		})
	}
}

func TestFolder_ObservesIdentityWithoutWriting(t *testing.T) {
	f := newMoveFixture(t)
	taxes := f.mkdir("Taxes/2024")
	before := f.dir("Taxes")

	got, err := f.mover.Folder(f.root, f.rootID, "Taxes/2024")
	if err != nil || got != taxes {
		t.Fatalf("want %+v, got %+v %v", taxes, got, err)
	}
	if _, err := f.mover.Folder(f.root, f.rootID, "Taxes/2025"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a missing folder must match ErrNotExist, got %v", err)
	}
	if f.exists("Taxes/2025") || f.dir("Taxes") != before {
		t.Fatal("observing a folder wrote something")
	}
	if root, err := f.mover.Folder(f.root, f.rootID, ""); err != nil || root.ID != f.rootID {
		t.Fatalf("the root itself: %+v %v", root, err)
	}
	if _, err := f.mover.Folder(f.root, "1:1", "Taxes"); !errors.Is(err, ErrMoveParentChanged) {
		t.Fatalf("a different root identity must be refused, got %v", err)
	}
	if err := os.Symlink("Taxes", f.abs("Linked")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.mover.Folder(f.root, f.rootID, "Linked"); !errors.Is(err, ErrMoveOutsideRoot) {
		t.Fatalf("a link is outside the root, got %v", err)
	}
	if _, err := f.mover.Folder(f.root, f.rootID, "../Taxes"); !errors.Is(err, ErrMoveInvalid) {
		t.Fatalf("a parent segment is invalid, got %v", err)
	}
}

func TestMakeFolder_CreatesExactlyOneFolderAndNeverAdoptsOne(t *testing.T) {
	f := newMoveFixture(t)
	taxes := f.mkdir("Taxes")

	made, err := f.mover.MakeFolder(context.Background(), f.root, f.rootID, taxes, "2025")
	if err != nil {
		t.Fatal(err)
	}
	if made.Rel != "Taxes/2025" || made != f.dir("Taxes/2025") {
		t.Fatalf("the returned identity must be the new folder's: %+v", made)
	}
	if info, err := os.Lstat(f.abs("Taxes/2025")); err != nil || !info.IsDir() || info.Mode().Perm()&0o027 != 0 {
		t.Fatalf("the folder must not be group-writable or open to others: %v %v", info, err)
	}

	// A name that exists is refused, never adopted: it may hold someone's files.
	f.write("Taxes/2025/keep.txt", "mine")
	if _, err := f.mover.MakeFolder(context.Background(), f.root, f.rootID, taxes, "2025"); !errors.Is(err, ErrMoveDestinationOccupied) {
		t.Fatalf("an existing folder is occupied, got %v", err)
	}
	f.write("Taxes/note.txt", "a file")
	if _, err := f.mover.MakeFolder(context.Background(), f.root, f.rootID, taxes, "note.txt"); !errors.Is(err, ErrMoveDestinationOccupied) {
		t.Fatalf("an existing file is occupied, got %v", err)
	}
	if f.read("Taxes/2025/keep.txt") != "mine" {
		t.Fatal("a file changed")
	}

	t.Run("parent replaced", func(t *testing.T) {
		g := newMoveFixture(t)
		parent := g.mkdir("Taxes")
		g.swapDir("Taxes", "Taxes.old")
		if _, err := g.mover.MakeFolder(context.Background(), g.root, g.rootID, parent, "2025"); !errors.Is(err, ErrMoveParentChanged) {
			t.Fatalf("want parent changed, got %v", err)
		}
		g.mustBeAbsent("Taxes/2025")
	})
	t.Run("invalid names", func(t *testing.T) {
		for _, name := range []string{"", "..", "a/b"} {
			if _, err := f.mover.MakeFolder(context.Background(), f.root, f.rootID, taxes, name); !errors.Is(err, ErrMoveInvalid) {
				t.Fatalf("%q: want invalid, got %v", name, err)
			}
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := f.mover.MakeFolder(ctx, f.root, f.rootID, taxes, "2026"); !errors.Is(err, context.Canceled) {
			t.Fatalf("want cancelled, got %v", err)
		}
		f.mustBeAbsent("Taxes/2026")
	})
}

func TestConditionalMove_ACancelledRequestDoesNothing(t *testing.T) {
	f := newMoveFixture(t)
	taxes := f.mkdir("Taxes")
	expect := f.write("report.pdf", "reviewed")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.mover.MoveIfUnchanged(ctx, f.request(f.dir(""), taxes, "report.pdf", "report.pdf", expect)); !errors.Is(err, context.Canceled) {
		t.Fatalf("want cancelled, got %v", err)
	}
	if !f.exists("report.pdf") {
		t.Fatal("the source moved")
	}
}

// The identity format must match what the rest of the package records, or every
// real move would be refused as "changed".
func TestConditionalMove_IdentityMatchesWhatTheScannerRecords(t *testing.T) {
	f := newMoveFixture(t)
	fp := f.write("report.pdf", "x")
	st, err := lookup(openFD(t, f.root), "report.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if got := statID(&st); got == "" || got != fp.FileID {
		t.Fatalf("statID %q is not the scanner's FileID %q", got, fp.FileID)
	}
	if got := statID(&st); !isReviewedFile(&st, fp) || got == "" {
		t.Fatal("a fingerprint taken by the scanner must be recognised")
	}
}

func openFD(t *testing.T, dir string) int {
	t.Helper()
	held, err := openRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(held.close)
	return held.fd
}

func TestConditionalMove_UnsupportedFilesystemIsRememberedAndRefused(t *testing.T) {
	const id = "test-unsupported-root"
	t.Cleanup(func() { forgetSupport(id) })

	err := (&platformMover{}).refused(ConditionalMove{RootID: id}, heldDir{}, heldDir{}, errNoReplaceUnsupported)
	if !errors.Is(err, ErrMoveUnsupported) {
		t.Fatalf("want unsupported, got %v", err)
	}
	// The verdict is cached, so no other call on this root is even attempted.
	if verdict := cachedVerdict(id, -1); !errors.Is(verdict, ErrMoveUnsupported) {
		t.Fatalf("the refusal was not remembered: %v", verdict)
	}
}

func TestService_ConditionalMoveIsOnlyAvailableWhenTheMoverSaysSo(t *testing.T) {
	var nilService *Service
	if err := nilService.ConditionalMoveAvailable("/x", "1:1"); !errors.Is(err, ErrMoveUnsupported) {
		t.Fatalf("a missing service must fail closed, got %v", err)
	}

	svc := &Service{}
	svc.SetConditionalMover(refusingMover{})
	if err := svc.ConditionalMoveAvailable("/x", "1:1"); !errors.Is(err, ErrMoveUnsupported) {
		t.Fatalf("a refusing mover must close the gate, got %v", err)
	}

	f := newMoveFixture(t)
	fresh := &Service{}
	if err := fresh.ConditionalMoveAvailable(f.root, f.rootID); err != nil {
		t.Fatalf("the default platform mover should accept this temp folder: %v", err)
	}
	if err := fresh.ConditionalMoveAvailable(f.root, "1:1"); !errors.Is(err, ErrMoveParentChanged) {
		t.Fatalf("a different folder identity must be refused, got %v", err)
	}
}

type refusingMover struct{}

func (refusingMover) Supported(string, string) error { return ErrMoveUnsupported }
func (refusingMover) Folder(string, string, string) (DirRef, error) {
	return DirRef{}, ErrMoveUnsupported
}
func (refusingMover) MakeFolder(context.Context, string, string, DirRef, string) (DirRef, error) {
	return DirRef{}, ErrMoveUnsupported
}
func (refusingMover) MoveIfUnchanged(context.Context, ConditionalMove) (Fingerprint, error) {
	return Fingerprint{}, ErrMoveUnsupported
}
