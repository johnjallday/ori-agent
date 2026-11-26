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

// sendFixture is two managed folders side by side on one real disk: a sender
// (Downloads) and a receiver (Documents). Both share one mover, so a hook can
// change either folder at the named point.
type sendFixture struct {
	t         *testing.T
	sender    *moveFixture
	receiver  *moveFixture
	mover     *platformMover
	parentDir string
}

func newSendFixture(t *testing.T) *sendFixture {
	t.Helper()
	parent := t.TempDir()
	mover := &platformMover{}
	mk := func(name string) *moveFixture {
		root := filepath.Join(parent, name)
		if err := os.Mkdir(root, 0o750); err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(root)
		if err != nil {
			t.Fatal(err)
		}
		f := &moveFixture{t: t, root: root, rootID: directoryid.Witness(info), mover: mover}
		if err := mover.Supported(root, f.rootID); err != nil {
			t.Skipf("exclusive rename is not supported under %s: %v", os.TempDir(), err)
		}
		return f
	}
	return &sendFixture{t: t, sender: mk("Downloads"), receiver: mk("Documents"), mover: mover, parentDir: parent}
}

// send is the request for sending name from the sender's top level to the
// receiver's top level.
func (f *sendFixture) send(name string, expect Fingerprint) ConditionalMove {
	return ConditionalMove{
		Root: f.sender.root, RootID: f.sender.rootID,
		DestRoot: f.receiver.root, DestRootID: f.receiver.rootID,
		From: DirRef{Rel: "", ID: f.sender.rootID}, To: DirRef{Rel: "", ID: f.receiver.rootID},
		FromName: name, ToName: name, Expect: expect,
	}
}

// back is the request that undoes a send: the same primitive, folders swapped.
func (f *sendFixture) back(name string, expect Fingerprint) ConditionalMove {
	return ConditionalMove{
		Root: f.receiver.root, RootID: f.receiver.rootID,
		DestRoot: f.sender.root, DestRootID: f.sender.rootID,
		From: DirRef{Rel: "", ID: f.receiver.rootID}, To: DirRef{Rel: "", ID: f.sender.rootID},
		FromName: name, ToName: name, Expect: expect,
	}
}

func (f *sendFixture) move(m ConditionalMove) (Fingerprint, error) {
	return f.mover.MoveIfUnchanged(context.Background(), m)
}

func TestConditionalSend_MovesTopLevelToTopLevelAndBackWithTheSamePrimitive(t *testing.T) {
	f := newSendFixture(t)
	expect := f.sender.write("lease.pdf", "the reviewed lease")

	after, err := f.move(f.send("lease.pdf", expect))
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if after.FileID != expect.FileID || after.Name != "lease.pdf" || after.Size != expect.Size || !after.ModTime.Equal(expect.ModTime) {
		t.Fatalf("a send is a rename and keeps the file's identity: before %+v after %+v", expect, after)
	}
	if f.receiver.read("lease.pdf") != "the reviewed lease" {
		t.Fatal("the bytes changed in the send")
	}
	f.sender.mustBeAbsent("lease.pdf")

	back, err := f.move(f.back("lease.pdf", after))
	if err != nil {
		t.Fatalf("undo: %v", err)
	}
	if back.FileID != expect.FileID || f.sender.read("lease.pdf") != "the reviewed lease" {
		t.Fatalf("undo did not return the same file: %+v", back)
	}
	f.receiver.mustBeAbsent("lease.pdf")
}

func TestConditionalSend_NeverReplacesAFileAlreadyInTheReceiver(t *testing.T) {
	t.Run("the name is used before the call", func(t *testing.T) {
		f := newSendFixture(t)
		expect := f.sender.write("lease.pdf", "reviewed")
		f.receiver.write("lease.pdf", "the receiver's own lease")
		if _, err := f.move(f.send("lease.pdf", expect)); !errors.Is(err, ErrMoveDestinationOccupied) {
			t.Fatalf("want an occupied name, got %v", err)
		}
		if f.sender.read("lease.pdf") != "reviewed" || f.receiver.read("lease.pdf") != "the receiver's own lease" {
			t.Fatal("a file moved or was replaced")
		}
	})
	t.Run("the name is taken after every check, before the rename", func(t *testing.T) {
		f := newSendFixture(t)
		expect := f.sender.write("lease.pdf", "reviewed")
		f.mover.hook = func(stage moveStage) {
			if stage == stageBeforeRename {
				f.receiver.write("lease.pdf", "arrived meanwhile")
			}
		}
		if _, err := f.move(f.send("lease.pdf", expect)); !errors.Is(err, ErrMoveDestinationOccupied) {
			t.Fatalf("want the kernel to refuse the occupied name, got %v", err)
		}
		if f.sender.read("lease.pdf") != "reviewed" || f.receiver.read("lease.pdf") != "arrived meanwhile" {
			t.Fatal("a file moved or was replaced")
		}
	})
}

func TestConditionalSend_OnlyTheReviewedFileIsSent(t *testing.T) {
	t.Run("the file changed since the review", func(t *testing.T) {
		f := newSendFixture(t)
		expect := f.sender.write("deed.pdf", "reviewed")
		f.sender.write("deed.pdf", "edited after the review, longer")
		if _, err := f.move(f.send("deed.pdf", expect)); !errors.Is(err, ErrMoveSourceChanged) {
			t.Fatalf("want a changed source, got %v", err)
		}
		f.receiver.mustBeAbsent("deed.pdf")
	})
	t.Run("a different file took the name at the last instant", func(t *testing.T) {
		f := newSendFixture(t)
		expect := f.sender.write("lease.pdf", "reviewed")
		f.mover.hook = func(stage moveStage) {
			if stage == stageBeforeRename {
				f.sender.replaceWithImpostor("lease.pdf", "impostor", "kept-original.pdf")
			}
		}
		_, err := f.move(f.send("lease.pdf", expect))
		race := raceOf(t, err)
		if !race.RolledBack || !errors.Is(err, ErrMoveSourceChanged) {
			t.Fatalf("want a rolled-back source change, got %+v", race)
		}
		if f.sender.read("lease.pdf") != "impostor" || f.sender.read("kept-original.pdf") != "reviewed" {
			t.Fatal("the impostor was not put back, or the reviewed file was touched")
		}
		f.receiver.mustBeAbsent("lease.pdf")
	})
	t.Run("when the move back is blocked it says where the entry is, in the receiver", func(t *testing.T) {
		f := newSendFixture(t)
		expect := f.sender.write("lease.pdf", "reviewed")
		f.mover.hook = func(stage moveStage) {
			switch stage {
			case stageBeforeRename:
				f.sender.replaceWithImpostor("lease.pdf", "impostor", "kept-original.pdf")
			case stageBeforeRollback:
				f.sender.write("lease.pdf", "took the name meanwhile")
			}
		}
		_, err := f.move(f.send("lease.pdf", expect))
		race := raceOf(t, err)
		if race.RolledBack || race.LeftAt != "lease.pdf" || race.LeftIsReviewed {
			t.Fatalf("want left at lease.pdf in the receiver, not the reviewed file, got %+v", race)
		}
		if f.receiver.read("lease.pdf") != "impostor" || f.sender.read("lease.pdf") != "took the name meanwhile" || f.sender.read("kept-original.pdf") != "reviewed" {
			t.Fatal("a file was replaced or lost")
		}
	})
}

func TestConditionalSend_BothFoldersMustStillBeTheReviewedFolders(t *testing.T) {
	t.Run("the receiver's folder was replaced by a different folder", func(t *testing.T) {
		f := newSendFixture(t)
		expect := f.sender.write("lease.pdf", "reviewed")
		if err := os.Rename(f.receiver.root, f.receiver.root+".old"); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(f.receiver.root, 0o750); err != nil {
			t.Fatal(err)
		}
		if _, err := f.move(f.send("lease.pdf", expect)); !errors.Is(err, ErrMoveParentChanged) {
			t.Fatalf("want a changed folder, got %v", err)
		}
		f.receiver.mustBeAbsent("lease.pdf")
		if _, err := os.Lstat(filepath.Join(f.receiver.root+".old", "lease.pdf")); err == nil {
			t.Fatal("the file went into the folder that was moved away")
		}
		if f.sender.read("lease.pdf") != "reviewed" {
			t.Fatal("the source moved")
		}
	})
	t.Run("the receiver's folder is swapped during the send", func(t *testing.T) {
		f := newSendFixture(t)
		expect := f.sender.write("lease.pdf", "reviewed")
		f.mover.hook = func(stage moveStage) {
			if stage == stageBeforeRename {
				if err := os.Rename(f.receiver.root, f.receiver.root+".old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(f.receiver.root, 0o750); err != nil {
					t.Fatal(err)
				}
			}
		}
		_, err := f.move(f.send("lease.pdf", expect))
		if race := raceOf(t, err); !race.RolledBack || !errors.Is(err, ErrMoveParentChanged) {
			t.Fatalf("want a rolled-back folder change, got %+v", race)
		}
		if f.sender.read("lease.pdf") != "reviewed" {
			t.Fatal("the file is not back in the sender")
		}
		if _, err := os.Lstat(filepath.Join(f.receiver.root+".old", "lease.pdf")); err == nil {
			t.Fatal("the file was left in the folder that was moved away")
		}
	})
	t.Run("the sender's folder is not the reviewed folder", func(t *testing.T) {
		f := newSendFixture(t)
		expect := f.sender.write("lease.pdf", "reviewed")
		m := f.send("lease.pdf", expect)
		m.RootID, m.From.ID = "1:2", "1:2"
		if _, err := f.move(m); !errors.Is(err, ErrMoveParentChanged) {
			t.Fatalf("want a changed folder, got %v", err)
		}
		f.receiver.mustBeAbsent("lease.pdf")
	})
	t.Run("the receiver's folder is a link to another folder", func(t *testing.T) {
		f := newSendFixture(t)
		expect := f.sender.write("lease.pdf", "reviewed")
		elsewhere := filepath.Join(f.parentDir, "Elsewhere")
		if err := os.Mkdir(elsewhere, 0o750); err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(elsewhere)
		if err != nil {
			t.Fatal(err)
		}
		linked := filepath.Join(f.parentDir, "Linked")
		if err := os.Symlink(elsewhere, linked); err != nil {
			t.Fatal(err)
		}
		m := f.send("lease.pdf", expect)
		m.DestRoot, m.DestRootID = linked, directoryid.Witness(info)
		m.To.ID = m.DestRootID
		if _, err := f.move(m); !errors.Is(err, ErrMoveOutsideRoot) {
			t.Fatalf("want a link refused, got %v", err)
		}
		if _, err := os.Lstat(filepath.Join(elsewhere, "lease.pdf")); err == nil {
			t.Fatal("the send followed the link")
		}
	})
}

func TestConditionalSend_AShapeThatIsNotASendIsRefusedBeforeTouchingTheDisk(t *testing.T) {
	// Neither folder exists: a refusal for the wrong reason would be a "folder
	// changed" error, not "invalid".
	base := ConditionalMove{
		Root: "/does/not/exist/Downloads", RootID: "1:1",
		DestRoot: "/does/not/exist/Documents", DestRootID: "1:2",
		From: DirRef{Rel: "", ID: "1:1"}, To: DirRef{Rel: "", ID: "1:2"},
		FromName: "a.pdf", ToName: "a.pdf",
		Expect: Fingerprint{Name: "a.pdf", Size: 1, ModTime: time.Unix(1, 0), FileID: "1:3"},
	}
	cases := map[string]func(*ConditionalMove){
		"a folder inside the receiver": func(m *ConditionalMove) { m.To = DirRef{Rel: "Taxes", ID: "1:4"} },
		"a folder inside the sender":   func(m *ConditionalMove) { m.From = DirRef{Rel: "Inbox", ID: "1:4"} },
		"a rename":                     func(m *ConditionalMove) { m.ToName = "b.pdf" },
		"the receiver's identity only": func(m *ConditionalMove) { m.DestRoot = "" },
		"the receiver's path only":     func(m *ConditionalMove) { m.DestRootID = "" },
		"a relative receiver":          func(m *ConditionalMove) { m.DestRoot = "does/not/exist/Documents" },
		"an unclean receiver":          func(m *ConditionalMove) { m.DestRoot = "/does/not/exist/../Documents" },
		"the same folder twice":        func(m *ConditionalMove) { m.DestRoot = m.Root },
		"the same identity twice": func(m *ConditionalMove) {
			m.DestRootID, m.To.ID = m.RootID, m.RootID
		},
		"the receiver inside the sender": func(m *ConditionalMove) { m.DestRoot = m.Root + "/Documents" },
		"the sender inside the receiver": func(m *ConditionalMove) { m.Root = m.DestRoot + "/Downloads" },
		"To names the sender's folder":   func(m *ConditionalMove) { m.To.ID = m.RootID },
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
	// A send that forgot the receiving folder is a move inside one folder, from its
	// top level to its top level: refused as "the same folder".
	forgot := base
	forgot.DestRoot, forgot.DestRootID = "", ""
	if _, err := (&platformMover{}).MoveIfUnchanged(context.Background(), forgot); !errors.Is(err, ErrMoveInvalid) {
		t.Fatalf("want an invalid request when the receiver is missing, got %v", err)
	}
}

func TestConditionalSend_DifferentDisksAreRefused(t *testing.T) {
	cases := []struct {
		a, b string
		same bool
	}{
		{"16777220:100", "16777220:200", true},
		{"16777220:100", "16777221:200", false},
		{"16777220:100", "", false},
		{"", "", false},
		{"1:2:3", "1:4", false},
		{"nodevice", "nodevice", false},
		{":5", ":6", false},
	}
	for _, tc := range cases {
		if got := sameDevice(tc.a, tc.b); got != tc.same {
			t.Errorf("sameDevice(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.same)
		}
	}

	// A real second disk is not available to a unit test. The service check is
	// driven with identities the mover accepts but whose devices differ.
	s := &Service{}
	s.SetConditionalMover(sendSupportStub{})
	if err := s.ConditionalMoveBetween("/a", "1:10", "/b", "2:20"); !errors.Is(err, ErrMoveCrossDevice) {
		t.Fatalf("want different disks refused, got %v", err)
	}
	if err := s.ConditionalMoveBetween("/a", "1:10", "/b", "1:20"); err != nil {
		t.Fatalf("one disk should be allowed: %v", err)
	}
	s.SetConditionalMover(sendSupportStub{refuse: "/b"})
	if err := s.ConditionalMoveBetween("/a", "1:10", "/b", "1:20"); !errors.Is(err, ErrMoveUnsupported) {
		t.Fatalf("want the receiver's file system refused, got %v", err)
	}
}

func TestConditionalSend_BothFoldersOnARealDiskPassTheServiceCheck(t *testing.T) {
	f := newSendFixture(t)
	s := &Service{}
	s.SetConditionalMover(f.mover)
	if err := s.ConditionalMoveBetween(f.sender.root, f.sender.rootID, f.receiver.root, f.receiver.rootID); err != nil {
		t.Fatalf("two folders side by side on one disk: %v", err)
	}
	if err := s.ConditionalMoveBetween(f.sender.root, f.sender.rootID, f.receiver.root, "1:2"); err == nil {
		t.Fatal("a receiver that is not the reviewed folder must be refused")
	}
}

// sendSupportStub accepts every folder except refuse; it never moves anything.
type sendSupportStub struct{ refuse string }

func (s sendSupportStub) Supported(root, _ string) error {
	if root == s.refuse {
		return ErrMoveUnsupported
	}
	return nil
}

func (sendSupportStub) Folder(string, string, string) (DirRef, error) {
	return DirRef{}, ErrMoveUnsupported
}

func (sendSupportStub) MakeFolder(context.Context, string, string, DirRef, string) (DirRef, error) {
	return DirRef{}, ErrMoveUnsupported
}

func (sendSupportStub) MoveIfUnchanged(context.Context, ConditionalMove) (Fingerprint, error) {
	return Fingerprint{}, ErrMoveUnsupported
}
