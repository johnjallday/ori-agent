package workspacecontinuity

import (
	"context"
	"io"
	"os"
)

// VerifyFingerprints re-reads each declared canonical file through a confined
// root and checks its size and digest. It is the preparation fence's file
// half: any change since collection makes the attempt ErrChanged/ErrDigest.
func VerifyFingerprints(ctx context.Context, directory string, files []Fingerprint) error {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return ErrUnsafe
	}
	defer func() { _ = root.Close() }()
	for _, file := range files {
		if !canonicalPath(file.Path) {
			return ErrUnsafe
		}
		if err := copyVerified(ctx, root, file.Path, file.Digest, file.Bytes, MaxBlobBytes, io.Discard); err != nil {
			return err
		}
	}
	return nil
}

// CopyVerifiedFile streams one declared canonical file to destination and
// fails unless its bytes still match the fingerprint. The caller must write
// to private scratch and publish only after success.
func CopyVerifiedFile(ctx context.Context, directory string, file Fingerprint, destination io.Writer) error {
	if !canonicalPath(file.Path) {
		return ErrUnsafe
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return ErrUnsafe
	}
	defer func() { _ = root.Close() }()
	return copyVerified(ctx, root, file.Path, file.Digest, file.Bytes, MaxBlobBytes, destination)
}

// CopyInspectedBlob streams one declared object of an inspected component to
// destination, verifying its digest at EOF. On error the caller must discard
// whatever was written.
func CopyInspectedBlob(ctx context.Context, directory string, inspected Inspection, domain string, ref BlobRef, destination io.Writer) error {
	declared := false
	for _, component := range inspected.Manifest.Components {
		if component.Domain != domain {
			continue
		}
		for _, blob := range component.Blobs {
			if blob == ref {
				declared = true
			}
		}
	}
	if !declared || !validDigest(ref.Digest) {
		return ErrInvalid
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return ErrUnsafe
	}
	defer func() { _ = root.Close() }()
	managed, err := openDirectory(root, Directory)
	if err != nil {
		return ErrUnsafe
	}
	defer func() { _ = managed.Close() }()
	return copyVerified(ctx, managed, "objects/"+ref.Digest, ref.Digest, ref.Bytes, MaxBlobBytes, destination)
}
