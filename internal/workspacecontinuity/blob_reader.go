package workspacecontinuity

import (
	"bytes"
	"context"
	"os"
	"reflect"
)

// ReadInspectedBlob reads a bounded immutable object only when it is declared
// by the exact inspected generation and domain. Canonical-file APIs reject
// reserved checkpoint paths; private object reads belong here, never to a
// caller-composed path or a guessed hash from a typed record alone.
func ReadInspectedBlob(ctx context.Context, directory string, inspected Inspection, domain string, ref BlobRef, limit int64) ([]byte, error) {
	if !knownDomain(domain) || !validDigest(ref.Digest) || ref.Bytes < 0 || limit <= 0 || limit > MaxBlobBytes || ref.Bytes > limit {
		return nil, ErrInvalid
	}
	declared := false
	for _, component := range inspected.Manifest.Components {
		if component.Domain != domain {
			continue
		}
		for _, blob := range component.Blobs {
			if blob == ref {
				declared = true
				break
			}
		}
	}
	if !declared {
		return nil, ErrIncomplete
	}
	fresh, err := Inspect(ctx, directory)
	if err != nil || fresh.Pointer != inspected.Pointer || !reflect.DeepEqual(fresh.Manifest, inspected.Manifest) {
		return nil, ErrChanged
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, ErrUnsafe
	}
	defer func() { _ = root.Close() }()
	managed, err := openDirectory(root, Directory)
	if err != nil {
		return nil, ErrChanged
	}
	defer func() { _ = managed.Close() }()
	var output bytes.Buffer
	if err := copyVerified(ctx, managed, "objects/"+ref.Digest, ref.Digest, ref.Bytes, limit, &output); err != nil {
		return nil, err
	}
	fresh, err = Inspect(ctx, directory)
	if err != nil || fresh.Pointer != inspected.Pointer || !reflect.DeepEqual(fresh.Manifest, inspected.Manifest) {
		return nil, ErrChanged
	}
	return output.Bytes(), nil
}
