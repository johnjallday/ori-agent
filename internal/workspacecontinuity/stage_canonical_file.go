package workspacecontinuity

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/google/uuid"
)

// StageVerifiedCanonicalFile streams one manifest-declared files/ asset from
// a confined source into a caller-owned, private destination Root. It never
// follows a link, overwrites an existing file, or accepts a path outside the
// snapshot format. The caller retains the reset/operation owner, checks the
// complete physical inventory, and re-inspects after staging. A failure leaves
// inert staging for that owner to reconcile; this is not folder registration.
func StageVerifiedCanonicalFile(ctx context.Context, sourceDir string, stage *os.Root, fingerprint Fingerprint) error {
	if stage == nil || !canonicalPath(fingerprint.Path) || !strings.HasPrefix(fingerprint.Path, "files/") || !validDigest(fingerprint.Digest) || fingerprint.Bytes < 0 || fingerprint.Bytes > MaxBlobBytes {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// #nosec G703 -- sourceDir is an inspected workspace root supplied by the import owner; copyVerified opens only the validated relative manifest path below this confined handle.
	source, err := os.OpenRoot(sourceDir)
	if err != nil {
		return ErrUnsafe
	}
	defer func() { _ = source.Close() }()
	return stageVerifiedFile(ctx, source, stage, fingerprint.Path, fingerprint.Path, fingerprint.Digest, fingerprint.Bytes)
}

// stageVerifiedFile handles source/destination paths separately: an inspected
// immutable object has a reserved source path, but its staged location must be
// independently selected by the owner, never taken from the checkpoint.
func stageVerifiedFile(ctx context.Context, source, stage *os.Root, sourcePath, targetPath, digest string, size int64) error {
	if source == nil || stage == nil || !safeRelativePath(sourcePath) || !canonicalPath(targetPath) || !validDigest(digest) || size < 0 || size > MaxBlobBytes {
		return ErrInvalid
	}
	parts := strings.Split(targetPath, "/")
	parent := stage
	for _, part := range parts[:len(parts)-1] {
		mkdirErr := parent.Mkdir(part, 0750)
		if mkdirErr != nil && !errors.Is(mkdirErr, os.ErrExist) {
			if parent != stage {
				_ = parent.Close()
			}
			return ErrUnsafe
		}
		if mkdirErr == nil {
			if err := syncDirectory(parent); err != nil {
				if parent != stage {
					_ = parent.Close()
				}
				return ErrIncomplete
			}
		}
		next, err := openDirectory(parent, part)
		if parent != stage {
			_ = parent.Close()
		}
		if err != nil {
			return ErrUnsafe
		}
		parent = next
	}
	if parent != stage {
		defer func() { _ = parent.Close() }()
	}
	name := parts[len(parts)-1]
	if _, err := parent.Lstat(name); err == nil {
		return ErrChanged
	} else if !errors.Is(err, os.ErrNotExist) {
		return ErrUnsafe
	}
	temporary := ".ori-stage-" + uuid.NewString()
	file, err := parent.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return ErrUnsafe
	}
	defer func() { _ = parent.Remove(temporary) }()
	copyErr := copyVerified(ctx, source, sourcePath, digest, size, MaxBlobBytes, file)
	syncErr := error(nil)
	if copyErr == nil {
		syncErr = file.Sync()
	}
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if syncErr != nil || closeErr != nil {
		return ErrIncomplete
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := parent.Link(temporary, name); err != nil {
		return ErrChanged
	}
	if err := parent.Remove(temporary); err != nil {
		return ErrIncomplete
	}
	return syncDirectory(parent)
}
