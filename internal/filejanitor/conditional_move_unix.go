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
	// Both are wrapped: callers match the sentinel, and errors.Is(err,
	// fs.ErrNotExist) tells "that folder is not there" from "it changed".
	return fmt.Errorf("%w: %w", ErrMoveParentChanged, err)
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
// file was in flight. For a send, To is re-opened under the receiving folder.
func resolveAgain(m ConditionalMove) error {
	if err := resolveUnder(m.Root, m.RootID, m.From); err != nil {
		return err
	}
	toRoot, toRootID := m.toRoot()
	return resolveUnder(toRoot, toRootID, m.To)
}

// resolveUnder opens root by path, requires its identity, and requires ref to
// resolve under it to the same folder.
func resolveUnder(rootPath, rootID string, ref DirRef) error {
	root, err := openRoot(rootPath)
	if err != nil {
		return err
	}
	defer root.close()
	if statID(&root.st) != rootID {
		return ErrMoveParentChanged
	}
	dir, err := openRel(root.fd, uint64(root.st.Dev), ref.Rel) // #nosec G115 -- device numbers compare as numbers
	if err != nil {
		return err
	}
	id := statID(&dir.st)
	dir.close()
	if id != ref.ID {
		return ErrMoveParentChanged
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

// ---------------------------------------------------------------- folders

// openVerifiedRoot opens the root by path and checks its identity and filesystem.
func openVerifiedRoot(root, rootID string) (heldDir, uint64, error) {
	if root == "" || root[0] != '/' || path.Clean(root) != root || rootID == "" {
		return heldDir{}, 0, fmt.Errorf("%w: the managed folder is not known", ErrMoveInvalid)
	}
	held, err := openRoot(root)
	if err != nil {
		return heldDir{}, 0, err
	}
	if statID(&held.st) != rootID {
		held.close()
		return heldDir{}, 0, ErrMoveParentChanged
	}
	if err := cachedVerdict(rootID, held.fd); err != nil {
		held.close()
		return heldDir{}, 0, err
	}
	return held, uint64(held.st.Dev), nil // #nosec G115 -- device numbers compare as numbers
}

// Folder implements ConditionalMover. It only observes.
func (p *platformMover) Folder(root, rootID, rel string) (DirRef, error) {
	if !validRelPath(rel) {
		return DirRef{}, fmt.Errorf("%w: a folder path is not a plain path inside the managed folder", ErrMoveInvalid)
	}
	held, rootDev, err := openVerifiedRoot(root, rootID)
	if err != nil {
		return DirRef{}, err
	}
	defer held.close()
	dir, err := openRel(held.fd, rootDev, rel)
	if err != nil {
		return DirRef{}, err
	}
	defer dir.close()
	id := statID(&dir.st)
	if id == "" {
		return DirRef{}, ErrMoveParentChanged
	}
	return DirRef{Rel: rel, ID: id}, nil
}

// MakeFolder implements ConditionalMover.
func (p *platformMover) MakeFolder(ctx context.Context, root, rootID string, parent DirRef, name string) (DirRef, error) {
	if ValidateFileName(name) != nil || !validRelPath(parent.Rel) || parent.ID == "" {
		return DirRef{}, fmt.Errorf("%w: a folder name or path is not plain", ErrMoveInvalid)
	}
	if err := ctx.Err(); err != nil {
		return DirRef{}, err
	}
	held, rootDev, err := openVerifiedRoot(root, rootID)
	if err != nil {
		return DirRef{}, err
	}
	defer held.close()
	dir, err := openRel(held.fd, rootDev, parent.Rel)
	if err != nil {
		return DirRef{}, err
	}
	defer dir.close()
	if statID(&dir.st) != parent.ID {
		return DirRef{}, ErrMoveParentChanged
	}
	if err := unix.Mkdirat(dir.fd, name, 0o750); err != nil {
		if errors.Is(err, unix.EEXIST) {
			return DirRef{}, ErrMoveDestinationOccupied
		}
		return DirRef{}, fmt.Errorf("%w: %v", ErrMoveFolderNotCreated, err)
	}
	fd, err := unix.Openat(dir.fd, name, dirOpenFlags, 0)
	if err != nil {
		return DirRef{}, fmt.Errorf("%w: %v", ErrMoveFolderNotCreated, err)
	}
	made := heldDir{fd: fd}
	defer made.close()
	if err := unix.Fstat(fd, &made.st); err != nil || made.st.Mode&unix.S_IFMT != unix.S_IFDIR || uint64(made.st.Dev) != rootDev { // #nosec G115 -- device numbers compare as numbers
		return DirRef{}, ErrMoveFolderNotCreated
	}
	// The parent must still be where it was reviewed, or the folder was made inside
	// a folder that has since moved away. It is reported, never removed.
	again, err := openRel(held.fd, rootDev, parent.Rel)
	if err != nil {
		return DirRef{}, ErrMoveParentChanged
	}
	parentUnchanged := statID(&again.st) == parent.ID
	again.close()
	if !parentUnchanged {
		return DirRef{}, ErrMoveParentChanged
	}
	return DirRef{Rel: path.Join(parent.Rel, name), ID: statID(&made.st)}, nil
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

	// For a send, To lives under the receiving folder, opened and checked the same
	// way. It must be on the sending folder's disk: a move between disks would be a
	// copy, and the kernel rename below would refuse it anyway.
	toRootFD := root.fd
	if m.crossRoot() {
		dest, err := openRoot(m.DestRoot)
		if err != nil {
			return Fingerprint{}, err
		}
		defer dest.close()
		if statID(&dest.st) != m.DestRootID {
			return Fingerprint{}, ErrMoveParentChanged
		}
		if err := cachedVerdict(m.DestRootID, dest.fd); err != nil {
			return Fingerprint{}, err
		}
		if uint64(dest.st.Dev) != rootDev { // #nosec G115 -- device numbers compare as numbers
			return Fingerprint{}, ErrMoveCrossDevice
		}
		toRootFD = dest.fd
	}

	from, err := openRel(root.fd, rootDev, m.From.Rel)
	if err != nil {
		return Fingerprint{}, err
	}
	defer from.close()
	to, err := openRel(toRootFD, rootDev, m.To.Rel)
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
		if m.crossRoot() {
			markUnsupported(m.DestRootID)
		}
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
