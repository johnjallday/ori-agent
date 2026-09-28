package workspacecontinuity

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
)

// Inspection contains metadata only. Inspect verifies every declared byte but
// neither registers the folder nor opens a domain database/provider. Absence is
// legacy only when the entire reserved directory is absent; damaged modern data
// never falls back to a previous generation or a legacy-success interpretation.
type Inspection struct {
	Pointer  Pointer
	Manifest Manifest
}

func Inspect(ctx context.Context, workspaceDirectory string) (Inspection, error) {
	var result Inspection
	if err := ctx.Err(); err != nil {
		return result, err
	}
	root, err := os.OpenRoot(workspaceDirectory)
	if err != nil {
		return result, ErrUnsafe
	}
	defer func() { _ = root.Close() }()
	managed, err := openDirectory(root, Directory)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			pending, probeErr := hasUnpublishedInitialization(root)
			if probeErr != nil {
				return result, probeErr
			}
			if pending {
				return result, ErrIncomplete
			}
			return result, ErrLegacy
		}
		return result, ErrUnsafe
	}
	defer func() { _ = managed.Close() }()
	markerBytes, err := readFileBounded(managed, "format.json", MaxPointerBytes)
	if err != nil {
		return result, err
	}
	var marker struct {
		Format  string `json:"format"`
		Version int    `json:"version"`
	}
	if err := strictJSON(markerBytes, &marker); err != nil {
		return result, err
	}
	if marker.Format != "ori.workspace-continuity" {
		return result, ErrInvalid
	}
	if marker.Version != Version {
		return result, ErrVersion
	}
	pointer, err := readPointer(managed)
	if err != nil {
		return result, err
	}
	data, err := readFileBounded(managed, "generations/"+pointer.Generation+".json", MaxManifestBytes)
	if err != nil {
		return result, err
	}
	manifest, err := DecodeManifest(bytes.NewReader(data), pointer)
	if err != nil {
		return result, err
	}
	if err := verifyFiles(ctx, root, manifest); err != nil {
		return result, err
	}
	for _, component := range manifest.Components {
		// Hash keys keep duplicate-ID validation memory bounded and independent
		// of private record content. The manifest caps total records/references.
		seen := map[[sha256.Size]byte]bool{}
		for _, ref := range component.Chunks {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			data, err := readFileBounded(managed, "objects/"+ref.Digest, ref.Bytes)
			if err != nil {
				return result, err
			}
			chunk, err := DecodeChunk(bytes.NewReader(data), ref, manifest.WorkspaceID, component.Domain)
			if err != nil {
				return result, err
			}
			for _, record := range chunk.Records {
				key := sha256.Sum256([]byte(chunk.Family + "\x00" + record.ID))
				if seen[key] {
					return result, ErrInvalid
				}
				seen[key] = true
			}
		}
		for _, blob := range component.Blobs {
			if err := copyVerified(ctx, managed, "objects/"+blob.Digest, blob.Digest, blob.Bytes, MaxBlobBytes, io.Discard); err != nil {
				return result, err
			}
		}
	}
	if err := verifyFiles(ctx, root, manifest); err != nil {
		return result, err
	}
	// Reopen from the workspace, not the possibly detached managed directory
	// handle, so replacement of .ori/continuity during inspection is detected.
	currentRoot, err := openDirectory(root, Directory)
	if err != nil {
		return result, ErrChanged
	}
	defer func() { _ = currentRoot.Close() }()
	current, err := readPointer(currentRoot)
	if err != nil || current != pointer {
		return result, ErrChanged
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	return Inspection{Pointer: pointer, Manifest: manifest}, nil
}

func readPointer(root *os.Root) (Pointer, error) {
	data, err := readFileBounded(root, "current.json", MaxPointerBytes)
	if err != nil {
		return Pointer{}, err
	}
	return DecodePointer(bytes.NewReader(data))
}

func readFileBounded(root *os.Root, path string, limit int64) ([]byte, error) {
	file, err := openRegular(root, path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrIncomplete
		}
		if errors.Is(err, ErrChanged) {
			return nil, ErrChanged // replaced while opening, e.g. by an atomic save
		}
		return nil, ErrUnsafe
	}
	defer func() { _ = file.Close() }()
	before, err := file.Stat()
	if err != nil {
		return nil, ErrUnsafe
	}
	if before.Size() > limit {
		return nil, ErrLimit
	}
	data, err := readBounded(file, limit)
	if err != nil {
		return nil, err
	}
	after, err := file.Stat()
	if err != nil || before.Size() != int64(len(data)) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return nil, ErrChanged
	}
	return data, nil
}

func verifyFiles(ctx context.Context, root *os.Root, manifest Manifest) error {
	for _, file := range manifest.Files {
		if !canonicalPath(file.Path) {
			return ErrUnsafe
		}
		if file.Path == "workspace.json" {
			if file.Bytes > MaxChunkBytes {
				return ErrLimit
			}
			var buffer bytes.Buffer
			if err := copyVerified(ctx, root, file.Path, file.Digest, file.Bytes, MaxChunkBytes, &buffer); err != nil {
				return err
			}
			var fields map[string]json.RawMessage
			if err := strictJSON(buffer.Bytes(), &fields); err != nil {
				return err
			}
			for key := range fields {
				if strings.EqualFold(key, "id") && key != "id" {
					return ErrInvalid
				}
			}
			var id string
			if err := json.Unmarshal(fields["id"], &id); err != nil || id != manifest.WorkspaceID {
				return ErrInvalid
			}
			continue
		}
		if err := copyVerified(ctx, root, file.Path, file.Digest, file.Bytes, MaxBlobBytes, io.Discard); err != nil {
			return err
		}
	}
	return nil
}
