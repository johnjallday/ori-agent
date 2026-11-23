//go:build darwin || linux

package filejanitor

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// platformMover is the production ConditionalMover on macOS and Linux.
//
// Sequence (the caller holds lockRoot):
//
//  1. Validate the request, then open the root and every folder on the way with
//     one-component, no-follow directory descriptors, so a link anywhere below the
//     root cannot redirect the move.
//  2. Check the root and both folders are the reviewed ones, on one disk.
//  3. Check the source is the reviewed regular file: same device and inode, size
//     and modification time, one link, not a cloud placeholder.
//  4. One kernel rename that refuses an occupied destination name.
//  5. Check what moved is the reviewed file and that the paths still resolve to the
//     same folders.
//  6. If step 5 fails, one more no-replace rename back.
//
// It never unlinks, copies or replaces anything.
type platformMover struct {
	// hook lets a test change the disk at a named point; nil in production.
	hook func(moveStage)
}

func newPlatformConditionalMover() ConditionalMover { return &platformMover{} }

func (p *platformMover) fire(stage moveStage) {
	if p.hook != nil {
		p.hook(stage)
	}
}

// dirOpenFlags opens exactly one directory entry, refusing a symlink.
const dirOpenFlags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC

// statID is the "device:inode" identity, in the same format as platformFileID and
// directoryid.Witness. An entry with no usable identity returns "".
func statID(st *unix.Stat_t) string {
	if uint64(st.Dev) == 0 || st.Ino == 0 { // #nosec G115 -- device numbers are platform-sized; compared only as a number and a string
		return ""
	}
	return fmt.Sprintf("%d:%d", uint64(st.Dev), st.Ino) // #nosec G115 -- see above
}

func statMTime(st *unix.Stat_t) time.Time { return time.Unix(st.Mtim.Unix()) }

// heldDir is an open directory and what it looked like when it was opened.
type heldDir struct {
	fd int
	st unix.Stat_t
}

func (h heldDir) close() {
	if h.fd > 0 {
		_ = unix.Close(h.fd)
	}
}

// dirOpenError maps an open failure on one folder component.
func dirOpenError(err error) error {
	if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) {
		return ErrMoveOutsideRoot
	}
	return fmt.Errorf("%w: %v", ErrMoveParentChanged, err)
}

// openRoot opens the managed folder by path. Its ancestors may be links (macOS
// puts temporary folders under /var -> /private/var); the folder itself may not.
func openRoot(root string) (heldDir, error) {
	fd, err := unix.Open(root, dirOpenFlags, 0)
	if err != nil {
		return heldDir{}, dirOpenError(err)
	}
	held := heldDir{fd: fd}
	if err := unix.Fstat(fd, &held.st); err != nil || held.st.Mode&unix.S_IFMT != unix.S_IFDIR {
		held.close()
		return heldDir{}, ErrMoveParentChanged
	}
	return held, nil
}

// openRel walks rel from rootfd one component at a time. Every folder must be on
// the root's disk.
func openRel(rootfd int, rootDev uint64, rel string) (heldDir, error) {
	parts := []string{"."}
	if rel != "" {
		parts = splitRel(rel)
	}
	cur := rootfd
	owned := false
	var last heldDir
	for _, part := range parts {
		fd, err := unix.Openat(cur, part, dirOpenFlags, 0)
		if owned {
			_ = unix.Close(cur)
		}
		if err != nil {
			return heldDir{}, dirOpenError(err)
		}
		cur, owned = fd, true
		last = heldDir{fd: fd}
		if err := unix.Fstat(fd, &last.st); err != nil || last.st.Mode&unix.S_IFMT != unix.S_IFDIR {
			last.close()
			return heldDir{}, ErrMoveParentChanged
		}
		if uint64(last.st.Dev) != rootDev { // #nosec G115 -- device numbers compare as numbers
			last.close()
			return heldDir{}, ErrMoveCrossDevice
		}
	}
	return last, nil
}

func splitRel(rel string) []string {
	var parts []string
	start := 0
	for i := 0; i <= len(rel); i++ {
		if i == len(rel) || rel[i] == '/' {
			parts = append(parts, rel[start:i])
			start = i + 1
		}
	}
	return parts
}

// lookup stats one name in a held folder without following a link.
func lookup(dirfd int, name string) (unix.Stat_t, error) {
	var st unix.Stat_t
	err := unix.Fstatat(dirfd, name, &st, unix.AT_SYMLINK_NOFOLLOW)
	return st, err
}

// isReviewedFile reports whether st is a regular file with the reviewed
// identity, size and modification time. A missing identity on either side is a
// refusal; Fingerprint.Matches would accept it. ctime is not compared because a
// rename changes it.
func isReviewedFile(st *unix.Stat_t, expect Fingerprint) bool {
	id := statID(st)
	return st.Mode&unix.S_IFMT == unix.S_IFREG && id != "" && id == expect.FileID &&
		st.Size == expect.Size && statMTime(st).Equal(expect.ModTime)
}

// checkSource requires the reviewed file to be at name: a regular file, one link,
// on the root's disk, not a cloud placeholder, and unchanged.
func checkSource(dirfd int, name string, rootDev uint64, expect Fingerprint) error {
	st, err := lookup(dirfd, name)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return ErrMoveSourceChanged
		}
		return fmt.Errorf("%w: %v", ErrMoveSourceChanged, err)
	}
	switch {
	case uint64(st.Dev) != rootDev: // #nosec G115 -- device numbers compare as numbers
		return ErrMoveCrossDevice
	case st.Mode&unix.S_IFMT != unix.S_IFREG:
		return fmt.Errorf("%w: it is no longer a plain file", ErrMoveSourceChanged)
	case uint64(st.Nlink) != 1: // #nosec G115 -- link counts compare as numbers
		return fmt.Errorf("%w: it has more than one name", ErrMoveSourceChanged)
	case isPlaceholder(&st):
		return fmt.Errorf("%w: it is a cloud placeholder", ErrMoveSourceChanged)
	case !isReviewedFile(&st, expect):
		return ErrMoveSourceChanged
	}
	return nil
}

// resolveAgain re-opens the root and both folders by path and requires the same
// identities. It is the check that a folder was not swapped or moved while the
// file was in flight.
func resolveAgain(m ConditionalMove) error {
	root, err := openRoot(m.Root)
	if err != nil {
		return err
	}
	defer root.close()
	if statID(&root.st) != m.RootID {
		return ErrMoveParentChanged
	}
	rootDev := uint64(root.st.Dev) // #nosec G115 -- device numbers compare as numbers
	for _, ref := range []DirRef{m.From, m.To} {
		dir, err := openRel(root.fd, rootDev, ref.Rel)
		if err != nil {
			return err
		}
		id := statID(&dir.st)
		dir.close()
		if id != ref.ID {
			return ErrMoveParentChanged
		}
	}
	return nil
}

// ---------------------------------------------------------------- support probe

// moveSupport caches, per root identity, whether the root's filesystem can refuse
// to replace a name. Only the filesystem verdict is cached, never a transient
// failure to open the folder.
var moveSupport = struct {
	sync.Mutex
	verdict map[string]error
}{verdict: map[string]error{}}

func cachedVerdict(rootID string, fd int) error {
	moveSupport.Lock()
	defer moveSupport.Unlock()
	if verdict, ok := moveSupport.verdict[rootID]; ok {
		return verdict
	}
	verdict := localExclusiveFS(fd)
	if verdict != nil && !errors.Is(verdict, ErrMoveUnsupported) {
		verdict = fmt.Errorf("%w: %v", ErrMoveUnsupported, verdict)
	}
	moveSupport.verdict[rootID] = verdict
	return verdict
}

// markUnsupported records that the kernel refused the no-replace flag here.
func markUnsupported(rootID string) {
	moveSupport.Lock()
	defer moveSupport.Unlock()
	moveSupport.verdict[rootID] = fmt.Errorf("%w: the folder's filesystem refused a move that cannot replace", ErrMoveUnsupported)
}

func forgetSupport(rootID string) {
	moveSupport.Lock()
	defer moveSupport.Unlock()
	delete(moveSupport.verdict, rootID)
}

// Supported reports whether File and Undo can run for this folder.
func (p *platformMover) Supported(root, rootID string) error {
	if root == "" || rootID == "" || path.Clean(root) != root || root[0] != '/' {
		return fmt.Errorf("%w: the managed folder is not known", ErrMoveInvalid)
	}
	held, err := openRoot(root)
	if err != nil {
		return err
	}
	defer held.close()
	if statID(&held.st) != rootID {
		return ErrMoveParentChanged
	}
	return cachedVerdict(rootID, held.fd)
}

// ---------------------------------------------------------------- the move

// MoveIfUnchanged implements ConditionalMover.
func (p *platformMover) MoveIfUnchanged(ctx context.Context, m ConditionalMove) (Fingerprint, error) {
	if err := m.validate(); err != nil {
		return Fingerprint{}, err
	}
	if err := ctx.Err(); err != nil {
		return Fingerprint{}, err
	}

	root, err := openRoot(m.Root)
	if err != nil {
		return Fingerprint{}, err
	}
	defer root.close()
	if statID(&root.st) != m.RootID {
		return Fingerprint{}, ErrMoveParentChanged
	}
	if err := cachedVerdict(m.RootID, root.fd); err != nil {
		return Fingerprint{}, err
	}
	rootDev := uint64(root.st.Dev) // #nosec G115 -- device numbers compare as numbers

	from, err := openRel(root.fd, rootDev, m.From.Rel)
	if err != nil {
		return Fingerprint{}, err
	}
	defer from.close()
	to, err := openRel(root.fd, rootDev, m.To.Rel)
	if err != nil {
		return Fingerprint{}, err
	}
	defer to.close()
	if statID(&from.st) != m.From.ID || statID(&to.st) != m.To.ID {
		return Fingerprint{}, ErrMoveParentChanged
	}

	if err := checkSource(from.fd, m.FromName, rootDev, m.Expect); err != nil {
		return Fingerprint{}, err
	}
	// An early, friendly answer. The kernel flag below is the guarantee.
	if _, err := lookup(to.fd, m.ToName); err == nil {
		return Fingerprint{}, ErrMoveDestinationOccupied
	} else if !errors.Is(err, unix.ENOENT) {
		return Fingerprint{}, fmt.Errorf("%w: %v", ErrMoveParentChanged, err)
	}

	p.fire(stageBeforeRename)

	if err := renameNoReplace(from.fd, m.FromName, to.fd, m.ToName); err != nil {
		return Fingerprint{}, p.refused(m, from, to, err)
	}

	// Check what moved is what was reviewed, and that the folders are still where
	// they were reviewed. Either failure is put right below.
	var verifyErr error
	st, lookupErr := lookup(to.fd, m.ToName)
	switch {
	case lookupErr != nil || !isReviewedFile(&st, m.Expect):
		verifyErr = ErrMoveSourceChanged
	default:
		verifyErr = resolveAgain(m)
	}
	if verifyErr != nil {
		return Fingerprint{}, p.rollback(m, from, to, verifyErr)
	}
	return Fingerprint{Name: m.ToName, Size: st.Size, ModTime: statMTime(&st), FileID: statID(&st)}, nil
}

// refused maps a failed rename. Nothing moved unless the entry is found at the
// destination with the reviewed identity.
func (p *platformMover) refused(m ConditionalMove, from, to heldDir, err error) error {
	switch {
	case errors.Is(err, unix.EEXIST):
		return ErrMoveDestinationOccupied
	case errors.Is(err, unix.EXDEV):
		return ErrMoveCrossDevice
	case errors.Is(err, unix.ENOENT):
		// The source is gone, or the folder it was going into is.
		if _, srcErr := lookup(from.fd, m.FromName); srcErr == nil {
			return ErrMoveParentChanged
		}
		return ErrMoveSourceChanged
	case unsupportedRename(err):
		markUnsupported(m.RootID)
		return fmt.Errorf("%w: the folder's filesystem refused a move that cannot replace", ErrMoveUnsupported)
	}
	// An unexpected error: trust the disk, not the error.
	if st, destErr := lookup(to.fd, m.ToName); destErr == nil && isReviewedFile(&st, m.Expect) {
		if _, srcErr := lookup(from.fd, m.FromName); errors.Is(srcErr, unix.ENOENT) {
			return p.rollback(m, from, to, fmt.Errorf("%w: %v", ErrMoveParentChanged, err))
		}
	}
	return fmt.Errorf("%w: %v", ErrMoveParentChanged, err)
}

// rollback moves the entry back without replacing anything. If the original name
// has been taken in the meantime it leaves the entry where it is and says where.
func (p *platformMover) rollback(m ConditionalMove, from, to heldDir, cause error) error {
	p.fire(stageBeforeRollback)
	if err := renameNoReplace(to.fd, m.ToName, from.fd, m.FromName); err == nil {
		return &MoveRaceError{Cause: cause, RolledBack: true}
	}
	race := &MoveRaceError{Cause: cause}
	if st, err := lookup(to.fd, m.ToName); err == nil {
		race.LeftAt = path.Join(m.To.Rel, m.ToName)
		race.LeftIsReviewed = isReviewedFile(&st, m.Expect)
	}
	return race
}
