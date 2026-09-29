package workspacecontinuity

import (
	"bytes"
	"context"
	"os"
	"reflect"
)

// ReadComponentRecords visits one verified, bounded chunk at a time from the
// exact inspected generation. It supplies evidence to typed domain reviewers,
// never generic database instructions or execution authority. A source change
// requires a fresh Inspect; callers cannot use this as a filesystem lease for
// a later restore transaction.
func ReadComponentRecords(ctx context.Context, directory string, inspected Inspection, domain string, visit func(Chunk) error) error {
	if !knownDomain(domain) || visit == nil || inspected.Pointer.Validate() != nil || inspected.Manifest.Validate() != nil ||
		inspected.Pointer.Generation != inspected.Manifest.Generation {
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
	managed, err := openDirectory(root, Directory)
	if err != nil {
		return ErrChanged
	}
	defer func() { _ = managed.Close() }()
	checkPointer := func() error {
		// Reopen from the selected root, not a detached managed directory.
		current, err := openDirectory(root, Directory)
		if err != nil {
			return ErrChanged
		}
		defer func() { _ = current.Close() }()
		pointer, err := readPointer(current)
		if err != nil || pointer != inspected.Pointer {
			return ErrChanged
		}
		return nil
	}
	if err := checkPointer(); err != nil {
		return err
	}
	manifestData, err := readFileBounded(managed, "generations/"+inspected.Pointer.Generation+".json", MaxManifestBytes)
	if err != nil {
		return err
	}
	manifest, err := DecodeManifest(bytes.NewReader(manifestData), inspected.Pointer)
	if err != nil || !reflect.DeepEqual(manifest, inspected.Manifest) {
		return ErrChanged
	}
	var component *Component
	for i := range manifest.Components {
		if manifest.Components[i].Domain == domain {
			component = &manifest.Components[i]
			break
		}
	}
	if component == nil || component.Availability != Present {
		return ErrIncomplete // Empty is evidence of no records, not one record.
	}
	for _, ref := range component.Chunks {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, err := readFileBounded(managed, "objects/"+ref.Digest, ref.Bytes)
		if err != nil {
			return err
		}
		chunk, err := DecodeChunk(bytes.NewReader(data), ref, manifest.WorkspaceID, domain)
		if err != nil {
			return err
		}
		if err := visit(chunk); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := verifyFiles(ctx, root, manifest); err != nil {
		return err
	}
	if err := checkPointer(); err != nil {
		return err
	}
	return ctx.Err()
}
