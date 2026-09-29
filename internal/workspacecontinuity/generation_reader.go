package workspacecontinuity

import (
	"bytes"
	"context"
	"os"
)

// Generation is one immutable checkpoint generation read by its identity. A
// confirmed import binds its receipt to (generation, digest) at review time;
// this reader lets that operation keep reading the same content-addressed
// records after it has started installing the folder (which changes the
// canonical files the generation fingerprints). It is never used to review
// or authorize anything: only for an operation that already holds a receipt.
type Generation struct {
	Pointer  Pointer
	Manifest Manifest
}

// LoadGeneration reads the manifest of generation from directory and checks
// it against the receipt's manifest digest.
func LoadGeneration(ctx context.Context, directory, generation, digest string) (Generation, error) {
	pointer := Pointer{Version: Version, Generation: generation, Digest: digest}
	if err := pointer.Validate(); err != nil {
		return Generation{}, err
	}
	if err := ctx.Err(); err != nil {
		return Generation{}, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return Generation{}, ErrUnsafe
	}
	defer func() { _ = root.Close() }()
	managed, err := openDirectory(root, Directory)
	if err != nil {
		return Generation{}, ErrChanged
	}
	defer func() { _ = managed.Close() }()
	data, err := readFileBounded(managed, "generations/"+generation+".json", MaxManifestBytes)
	if err != nil {
		return Generation{}, err
	}
	manifest, err := DecodeManifest(bytes.NewReader(data), pointer)
	if err != nil {
		return Generation{}, err
	}
	return Generation{Pointer: pointer, Manifest: manifest}, nil
}

// Inspection returns the generation in the shape the record decoders use.
func (g Generation) Inspection() Inspection { return Inspection(g) }

// ReadGenerationRecords visits one domain's chunks of a loaded generation,
// verifying each chunk object's digest. Unlike ReadComponentRecords it does
// not re-verify the canonical workspace files (the importer is replacing
// them); every chunk is still content-addressed and bounded.
func ReadGenerationRecords(ctx context.Context, directory string, generation Generation, domain string, visit func(Chunk) error) error {
	if !knownDomain(domain) || visit == nil || generation.Manifest.Validate() != nil {
		return ErrInvalid
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
	for _, component := range generation.Manifest.Components {
		if component.Domain != domain {
			continue
		}
		if component.Availability != Present {
			return ErrIncomplete
		}
		for _, ref := range component.Chunks {
			if err := ctx.Err(); err != nil {
				return err
			}
			data, err := readFileBounded(managed, "objects/"+ref.Digest, ref.Bytes)
			if err != nil {
				return err
			}
			chunk, err := DecodeChunk(bytes.NewReader(data), ref, generation.Manifest.WorkspaceID, domain)
			if err != nil {
				return err
			}
			if err := visit(chunk); err != nil {
				return err
			}
		}
		return ctx.Err()
	}
	return ErrIncomplete
}
