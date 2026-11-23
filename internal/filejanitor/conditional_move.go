package filejanitor

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
)

// A conditional move is how Ori files a reviewed document into a folder the
// person chose, and how it puts that document back. It exists because the
// filesystem connector's `move_file` cannot do either safely: it takes two paths,
// moves whatever sits at the first, and finishes with a rename that replaces
// whatever sits at the second.
//
// This mover moves one file only if all of these still hold at the moment of the
// move: the file at the source is the exact file that was reviewed (device and
// inode, size, modification time), the source and destination folders are the
// folders that were reviewed, nothing already has the destination name, and
// every step stays under the managed folder. The destination is never replaced:
// the kernel refuses an occupied name (RENAME_NOREPLACE / RENAME_EXCL), so there
// is no window between "is the name free?" and "move it".
//
// What it cannot do is stop another program from swapping the source in the
// instant between Ori's last check and the rename: POSIX renames by path. It
// notices afterwards, moves the other item back without replacing anything, and
// says exactly where anything it could not move back now is. See MoveRaceError.
//
// The caller holds lockRoot. Legacy category filing keeps using Mover; only
// reviewed-destination filing and its Undo use this.

// DirRef names a folder inside the managed root and the identity it had when
// the person reviewed it. Rel is a slash path from the root ("" is the root
// itself); ID is the folder's "device:inode".
type DirRef struct {
	Rel string
	ID  string
}

// ConditionalMove is everything the caller knew when it asked. The mover trusts
// none of it: each value is compared with the disk at the moment of the move.
type ConditionalMove struct {
	// Root is the managed folder (absolute, clean). RootID is its "device:inode"
	// as bound when the review began.
	Root   string
	RootID string
	// From and To are the folders of the source and the destination.
	From, To DirRef
	// FromName and ToName are single names inside From and To.
	FromName, ToName string
	// Expect is the file as it was reviewed. Its FileID must be non-empty.
	Expect Fingerprint
}

// ConditionalMover performs one conditional move.
type ConditionalMover interface {
	// Supported reports nil when this folder's filesystem can refuse to replace a
	// name atomically; otherwise an error wrapping ErrMoveUnsupported (or another
	// refusal that says why). It writes nothing into the folder.
	Supported(root, rootID string) error
	// MoveIfUnchanged moves the file and returns what it looks like at its new
	// name. Every error means the file is not at ToName, except a *MoveRaceError
	// whose LeftAt names where a file that could not be moved back now is.
	MoveIfUnchanged(ctx context.Context, m ConditionalMove) (after Fingerprint, err error)
}

var (
	// ErrMoveUnsupported means this platform or filesystem cannot make the move
	// safe, so Ori does not make it. There is no fallback to a plain rename.
	ErrMoveUnsupported = errors.New("this folder cannot be filed into safely on this system")
	// ErrMoveInvalid means the request itself was malformed. Nothing was touched.
	ErrMoveInvalid = errors.New("the move request was not valid")
	// ErrMoveOutsideRoot means a folder on the way is a link or not a folder.
	ErrMoveOutsideRoot = errors.New("a folder on the way is not inside the folder Ori manages")
	// ErrMoveCrossDevice means the move would leave the managed folder's disk.
	ErrMoveCrossDevice = errors.New("the move would leave the disk the folder is on")
	// ErrMoveParentChanged means a reviewed folder is no longer the same folder.
	ErrMoveParentChanged = errors.New("a folder changed since it was reviewed")
	// ErrMoveSourceChanged means the file is gone or is no longer the reviewed file.
	ErrMoveSourceChanged = errors.New("the file changed since it was reviewed")
	// ErrMoveDestinationOccupied means something already has the destination name.
	ErrMoveDestinationOccupied = errors.New("something already has that name")
)

// MoveRaceError reports a move that was started and then found to be wrong, so it
// was moved back or could not be. A move that never started returns the plain
// sentinel instead.
type MoveRaceError struct {
	// Cause is ErrMoveSourceChanged or ErrMoveParentChanged (or the refusal that
	// stopped the check).
	Cause error
	// RolledBack is true when the entry was moved back to its original name. Then
	// nothing moved.
	RolledBack bool
	// LeftAt is where the entry that could not be moved back is now, relative to
	// the managed folder. Empty when RolledBack, or when Ori cannot find it.
	LeftAt string
	// LeftIsReviewed is true when that entry is the reviewed file (the folder it
	// went into changed), false when a different file took the reviewed file's name.
	LeftIsReviewed bool
}

func (e *MoveRaceError) Error() string {
	if e.RolledBack {
		return fmt.Sprintf("%v; Ori moved it back and nothing was changed", e.Cause)
	}
	return fmt.Sprintf("%v; Ori could not move it back and left it in place", e.Cause)
}

func (e *MoveRaceError) Unwrap() error { return e.Cause }

// moveStage names the two points where a test injects a change.
type moveStage int

const (
	// stageBeforeRename is after every check and before the rename.
	stageBeforeRename moveStage = iota + 1
	// stageBeforeRollback is after the rename was found wrong, before moving back.
	stageBeforeRollback
)

// validRelPath reports whether rel is "" (the root) or slash-separated single
// names, none empty, ".", ".." or containing NUL.
func validRelPath(rel string) bool {
	if rel == "" {
		return true
	}
	for _, part := range strings.Split(rel, "/") {
		if part == "" || part == "." || part == ".." || strings.ContainsRune(part, 0) {
			return false
		}
	}
	return true
}

// validate rejects a malformed request before any syscall.
func (m ConditionalMove) validate() error {
	invalid := func(why string) error { return fmt.Errorf("%w: %s", ErrMoveInvalid, why) }
	if m.Root == "" || !strings.HasPrefix(m.Root, "/") || path.Clean(m.Root) != m.Root || strings.ContainsRune(m.Root, 0) {
		return invalid("the managed folder is not an absolute path")
	}
	if m.RootID == "" || m.From.ID == "" || m.To.ID == "" || m.Expect.FileID == "" {
		return invalid("a reviewed identity is missing")
	}
	if !validRelPath(m.From.Rel) || !validRelPath(m.To.Rel) {
		return invalid("a folder path is not a plain path inside the managed folder")
	}
	if ValidateFileName(m.FromName) != nil || ValidateFileName(m.ToName) != nil {
		return invalid("a file name is not a single name")
	}
	// A move inside one folder is a rename, and a case-only rename would look
	// like an occupied name to the kernel. Filing and Undo always change folder.
	if m.From.Rel == m.To.Rel || m.From.ID == m.To.ID {
		return invalid("the file would stay in the same folder")
	}
	if m.Expect.Size < 0 {
		return invalid("the reviewed size is not valid")
	}
	return nil
}

// SetConditionalMover injects the mover. Tests use it; production gets the
// platform mover by default.
func (s *Service) SetConditionalMover(mover ConditionalMover) {
	if s != nil && mover != nil {
		s.conditionalMover = mover
	}
}

func (s *Service) conditionalMoverFor() ConditionalMover {
	if s.conditionalMover == nil {
		s.conditionalMover = newPlatformConditionalMover()
	}
	return s.conditionalMover
}

// ConditionalMoveAvailable reports nil when File and Undo of reviewed
// destinations can run for this folder, and otherwise why not. It is the single
// gate: callers never offer those actions on any other evidence.
func (s *Service) ConditionalMoveAvailable(root, rootID string) error {
	if s == nil {
		return ErrMoveUnsupported
	}
	return s.conditionalMoverFor().Supported(root, rootID)
}
