//go:build darwin || linux || freebsd

package projectlibrary

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

func unixIdentity(info *unix.Stat_t) string {
	return fmt.Sprintf("%d:%d", info.Dev, info.Ino)
}

// openPinnedDirectory performs only a no-follow descriptor walk. It can be
// used under the Home store lock for the final source/scope identity check
// without re-entering workspace.Store.Get. The caller owns the returned fd.
func directoryMatches(root Root, relative, expectedIdentity string) bool {
	fd, err := openPinnedDirectory(root, relative, expectedIdentity)
	if err != nil {
		return false
	}
	_ = unix.Close(fd)
	return true
}

func openPinnedDirectory(root Root, relative, expectedIdentity string) (int, error) {
	if !validRelative(relative) {
		return -1, ErrUnavailable
	}
	fd, err := unix.Open(root.Path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0) // #nosec G304 G703 -- persisted reviewed root, pinned by device/inode before any child use.
	if err != nil {
		return -1, ErrUnavailable
	}
	var rootStat unix.Stat_t
	if unix.Fstat(fd, &rootStat) != nil || unixIdentity(&rootStat) != root.FileIdentity {
		_ = unix.Close(fd)
		return -1, ErrUnavailable
	}
	if relative != "" {
		for _, part := range strings.Split(relative, string(os.PathSeparator)) {
			child, openErr := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			_ = unix.Close(fd)
			if openErr != nil {
				return -1, ErrUnavailable
			}
			fd = child
		}
	}
	var selectedStat unix.Stat_t
	if unix.Fstat(fd, &selectedStat) != nil ||
		(expectedIdentity != "" && unixIdentity(&selectedStat) != expectedIdentity) {
		_ = unix.Close(fd)
		return -1, ErrUnavailable
	}
	return fd, nil
}

// DirectoryRow carries only names and metadata, never project/audio bytes.
// IsLink is explicit so discovery can report skipped symlinks, not follow them.
// ModifiedAt comes from the same no-follow stat as Size and Identity: it says
// when the entry was last saved on disk, never that anyone worked on it.
type DirectoryRow struct {
	Name       string
	IsDir      bool
	IsLink     bool
	Size       int64
	Identity   string
	ModifiedAt time.Time
	Unreadable bool
}

// ReadDirectory pins the exact approved root by device+inode and walks one
// component at a time via no-follow directory descriptors. A caller must
// derive relative from server-known candidate records, never request JSON.
// Every call checks current Home permission/revocation before and after its
// one bounded metadata read; results from a revoked/swapped root are dropped.
func (r *Roots) ReadDirectory(ctx context.Context, scope Scope, rootID, relative string, max int) ([]DirectoryRow, bool, error) {
	return r.readDirectoryWithIdentity(ctx, scope, rootID, relative, "", max)
}

// readDirectoryWithIdentity pins the exact child observed during an earlier
// authorized listing. A same-named replacement is not the reviewed scope.
func (r *Roots) readDirectoryWithIdentity(ctx context.Context, scope Scope, rootID, relative, expectedIdentity string, max int) ([]DirectoryRow, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if !validRelative(relative) || max < 1 || max > 5000 {
		return nil, false, ErrUnavailable
	}
	gate := rootAccessGate(scope)
	gate.RLock()
	defer gate.RUnlock()
	root, err := r.verifyConnectedRootNoGate(scope, rootID)
	if err != nil {
		return nil, false, err
	}
	rows, partial, err := readPinnedDirectory(ctx, root, relative, expectedIdentity, max)
	if err != nil {
		return nil, false, err
	}
	if _, err := r.verifyConnectedRootNoGate(scope, rootID); err != nil {
		return nil, false, ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	return rows, partial, nil
}

// readPinnedDirectory is only called while the caller holds the exact Home's
// root gate and has verified the current grant. This non-reentrant form lets a
// reviewed creator keep revocation out from its final attach boundary.
func readPinnedDirectory(ctx context.Context, root Root, relative, expectedIdentity string, max int) ([]DirectoryRow, bool, error) {
	if err := ctx.Err(); err != nil || !validRelative(relative) || max < 1 || max > 5000 {
		return nil, false, ErrUnavailable
	}
	fd, err := openPinnedDirectory(root, relative, expectedIdentity)
	if err != nil {
		return nil, false, err
	}
	// os.File.ReadDir operates on the pinned descriptor; Fstatat obtains
	// metadata from the same descriptor, not a potentially replaced pathname.
	dir := os.NewFile(uintptr(fd), "approved-root-directory")
	defer func() { _ = dir.Close() }()
	entries, err := dir.ReadDir(max + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, false, ErrUnavailable
	}
	partial := len(entries) > max
	if partial {
		entries = entries[:max]
	}
	rows := make([]DirectoryRow, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		name := entry.Name()
		if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
			return nil, false, ErrUnavailable
		}
		var st unix.Stat_t
		if unix.Fstatat(int(dir.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW) != nil {
			// A disappeared/inaccessible child is unknown, never evidence that
			// a previously observed entry should be deleted. It still counts
			// against the scan budget and contributes to visible skip counts.
			partial = true
			rows = append(rows, DirectoryRow{Name: name, Unreadable: true})
			continue
		}
		rows = append(rows, DirectoryRow{Name: name, IsDir: st.Mode&unix.S_IFMT == unix.S_IFDIR,
			IsLink: st.Mode&unix.S_IFMT == unix.S_IFLNK, Size: st.Size, Identity: unixIdentity(&st),
			ModifiedAt: time.Unix(st.Mtim.Unix()).UTC()})
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	return rows, partial, nil
}
