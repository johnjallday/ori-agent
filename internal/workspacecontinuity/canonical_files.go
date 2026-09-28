package workspacecontinuity

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/google/uuid"
)

// OpenCanonicalFile opens one confined regular file, with the same no-follow /
// nonblocking checks as checkpoint reads. It is for canonical native readers
// that stream their own format; import readers must use bounded read APIs.
// The caller owns the returned handle. A missing file remains os.ErrNotExist.
func OpenCanonicalFile(directory, path string) (*os.File, error) {
	if !canonicalPath(path) {
		return nil, ErrInvalid
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		return nil, ErrUnsafe
	}
	defer func() { _ = root.Close() }()
	file, err := openRegular(root, path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		return nil, ErrUnsafe
	}
	return file, nil
}

// ReadCanonicalFile observes one bounded regular workspace-owned file without
// following symlinks. It is also used before local credential separation, when
// no continuity generation exists yet. Absence is ErrIncomplete.
func ReadCanonicalFile(ctx context.Context, directory, path string, limit int) ([]byte, error) {
	if !canonicalPath(path) || limit <= 0 || int64(limit) > MaxBlobBytes {
		return nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrIncomplete
		}
		return nil, ErrUnsafe
	}
	defer func() { _ = root.Close() }()
	data, err := readFileBounded(root, path, int64(limit))
	if err != nil {
		return nil, err
	}
	return data, ctx.Err()
}

// ListCanonicalDirectory bounds enumeration of one workspace-owned directory.
// It does not recurse or follow links. Callers must decide which listed files
// belong to their domain; listing is not authority to delete unknown files.
func ListCanonicalDirectory(ctx context.Context, directory, path string, limit int) ([]os.DirEntry, error) {
	if !canonicalPath(path) || limit <= 0 || limit > MaxFiles {
		return nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, ErrUnsafe
	}
	defer func() { _ = root.Close() }()
	dir, err := openDirectory(root, path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrIncomplete
	}
	if err != nil {
		return nil, ErrUnsafe
	}
	defer func() { _ = dir.Close() }()
	file, err := dir.Open(".")
	if err != nil {
		return nil, ErrUnsafe
	}
	defer func() { _ = file.Close() }()
	entries, err := file.ReadDir(limit + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, ErrUnsafe
	}
	if len(entries) > limit {
		return nil, ErrLimit
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

// ReplaceCanonicalFile is for an admitted canonical file writer, not an import
// inspector. The caller must serialize its own writers. expected is the current
// digest, or empty for an absent file. External edits are checked again before
// rename; this is not an atomic CAS against arbitrary external filesystem tools.
// No secret bytes should be passed here: local configuration is staged elsewhere
// before publishing a clean projection. Files are synced/private, parent paths
// confined, and an unexpected existing file is never overwritten by creation.
func ReplaceCanonicalFile(ctx context.Context, directory, path, expected string, data []byte) error {
	if !canonicalPath(path) || int64(len(data)) > MaxBlobBytes || (expected != "" && !validDigest(expected)) {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return ErrUnsafe
	}
	defer func() { _ = root.Close() }()
	return ReplaceCanonicalFileInRoot(ctx, root, path, expected, data)
}

// ReplaceCanonicalFileInRoot applies the same exclusive-create/CAS protocol to
// an already opened, caller-owned directory handle. Import staging uses this
// form so a rename or path swap cannot redirect a verified private stage write
// between identity checks. The caller owns and closes root.
func ReplaceCanonicalFileInRoot(ctx context.Context, root *os.Root, path, expected string, data []byte) error {
	if root == nil || !canonicalPath(path) || int64(len(data)) > MaxBlobBytes || (expected != "" && !validDigest(expected)) {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	parent := root
	parts := strings.Split(path, "/")
	for _, part := range parts[:len(parts)-1] {
		mkdirErr := parent.Mkdir(part, 0750)
		if mkdirErr != nil && !errors.Is(mkdirErr, os.ErrExist) {
			if parent != root {
				_ = parent.Close()
			}
			return ErrUnsafe
		}
		if mkdirErr == nil {
			if err := syncDirectory(parent); err != nil {
				if parent != root {
					_ = parent.Close()
				}
				return ErrIncomplete
			}
		}
		next, err := openDirectory(parent, part)
		if parent != root {
			_ = parent.Close()
		}
		if err != nil {
			return ErrUnsafe
		}
		parent = next
	}
	if parent != root {
		defer func() { _ = parent.Close() }()
	}
	name := parts[len(parts)-1]
	check := func() error {
		if expected == "" {
			if _, err := parent.Lstat(name); errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return ErrChanged
		}
		current, err := readFileBounded(parent, name, MaxBlobBytes)
		if err != nil {
			return err
		}
		if Digest(current) != expected {
			return ErrChanged
		}
		return nil
	}
	if err := check(); err != nil {
		return err
	}
	temporary := ".ori-config-" + uuid.NewString()
	file, err := parent.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return ErrUnsafe
	}
	defer func() { _ = parent.Remove(temporary) }()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return ErrIncomplete
	}
	syncErr, closeErr := file.Sync(), file.Close()
	if syncErr != nil || closeErr != nil {
		return ErrIncomplete
	}
	if err := check(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if expected == "" {
		// Hard-link publication is exclusive; rename would overwrite a file
		// another writer created after check. Same directory, same filesystem.
		if err := parent.Link(temporary, name); err != nil {
			return ErrChanged
		}
		if err := parent.Remove(temporary); err != nil {
			return ErrIncomplete
		}
	} else if err := parent.Rename(temporary, name); err != nil {
		return ErrIncomplete
	}
	return syncDirectory(parent)
}
