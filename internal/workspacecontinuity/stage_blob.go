package workspacecontinuity

import (
	"context"
	"os"
	"reflect"
)

// StageInspectedBlobToRoot streams an exact domain-declared immutable object
// into an exclusive, caller-owned private staging Root. The target path is
// selected by the domain owner, NOT read from portable data. The caller owns
// stage isolation, destination reconciliation and operation/reset admission;
// these inert bytes are not an installed session upload or a registered folder.
// A failed source recheck leaves only inert staging for that owner to reconcile.
func StageInspectedBlobToRoot(ctx context.Context, sourceDir string, inspected Inspection, domain string, ref BlobRef, stage *os.Root, targetPath string) error {
	if stage == nil || !knownDomain(domain) || !canonicalPath(targetPath) || !validDigest(ref.Digest) || ref.Bytes < 0 || ref.Bytes > MaxBlobBytes ||
		inspected.Pointer.Validate() != nil || inspected.Manifest.Validate() != nil || inspected.Pointer.Generation != inspected.Manifest.Generation {
		return ErrInvalid
	}
	declared := false
	for _, component := range inspected.Manifest.Components {
		if component.Domain != domain || component.Availability != Present {
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
		return ErrIncomplete
	}
	check := func() error {
		fresh, err := Inspect(ctx, sourceDir)
		if err != nil || fresh.Pointer != inspected.Pointer || !reflect.DeepEqual(fresh.Manifest, inspected.Manifest) {
			return ErrChanged
		}
		return nil
	}
	if err := check(); err != nil {
		return err
	}
	// #nosec G703 -- sourceDir is the inspected workspace owner, not a path from
	// a record; openDirectory confines the immutable managed object root.
	root, err := os.OpenRoot(sourceDir)
	if err != nil {
		return ErrUnsafe
	}
	defer func() { _ = root.Close() }()
	managed, err := openDirectory(root, Directory)
	if err != nil {
		return ErrChanged
	}
	defer func() { _ = managed.Close() }()
	if err := stageVerifiedFile(ctx, managed, stage, "objects/"+ref.Digest, targetPath, ref.Digest, ref.Bytes); err != nil {
		return err
	}
	return check()
}
