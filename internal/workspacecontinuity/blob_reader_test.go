package workspacecontinuity

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestReadInspectedBlobRequiresExactDeclaredGenerationAndSafeBytes(t *testing.T) {
	root, manifest, objects := fixture(t)
	blob := []byte("synthetic private appearance bytes")
	ref := BlobRef{Digest: Digest(blob), Bytes: int64(len(blob))}
	encoded, chunk, err := EncodeChunk(Chunk{Version: Version, WorkspaceID: manifest.WorkspaceID, Domain: "agents", Family: "appearances",
		Records: []Record{{ID: "profile-1", Data: json.RawMessage(`{"state":"present"}`)}}})
	must(t, err)
	objects[chunk.Digest] = encoded
	objects[ref.Digest] = blob
	for i := range manifest.Components {
		if manifest.Components[i].Domain == "agents" {
			manifest.Components[i] = Component{Domain: "agents", Version: Version, Availability: Present,
				Counts: map[string]int64{"appearances": 1}, Chunks: []ChunkRef{chunk}, Blobs: []BlobRef{ref}}
		}
	}
	must(t, Publish(t.Context(), root, manifest, objectSource(objects), unchanged))
	inspected, err := Inspect(t.Context(), root)
	must(t, err)
	data, err := ReadInspectedBlob(t.Context(), root, inspected, "agents", ref, MaxChunkBytes)
	if err != nil || !bytes.Equal(blob, data) {
		t.Fatal("owned object changed", err)
	}
	if _, err := ReadInspectedBlob(t.Context(), root, inspected, "uploads", ref, MaxChunkBytes); !errors.Is(err, ErrIncomplete) {
		t.Fatal("unowned blob read", err)
	}
	if _, err := ReadInspectedBlob(t.Context(), root, inspected, "agents", ref, 4); !errors.Is(err, ErrInvalid) {
		t.Fatal("blob limit ignored", err)
	}
	must(t, os.WriteFile(filepath.Join(root, Directory, "objects", ref.Digest), []byte("changed"), 0600))
	if _, err := ReadInspectedBlob(t.Context(), root, inspected, "agents", ref, MaxChunkBytes); !errors.Is(err, ErrChanged) {
		t.Fatal("changed immutable object read", err)
	}
}

func TestReadInspectedBlobRejectsChangedPointerAndLinkedObject(t *testing.T) {
	root, manifest, objects := fixture(t)
	blob := []byte("synthetic owned blob")
	ref := BlobRef{Digest: Digest(blob), Bytes: int64(len(blob))}
	encoded, chunk, err := EncodeChunk(Chunk{Version: Version, WorkspaceID: manifest.WorkspaceID, Domain: "agents", Family: "appearances",
		Records: []Record{{ID: "profile-1", Data: json.RawMessage(`{"state":"present"}`)}}})
	must(t, err)
	objects[chunk.Digest], objects[ref.Digest] = encoded, blob
	for i := range manifest.Components {
		if manifest.Components[i].Domain == "agents" {
			manifest.Components[i] = Component{Domain: "agents", Version: Version, Availability: Present,
				Counts: map[string]int64{"appearances": 1}, Chunks: []ChunkRef{chunk}, Blobs: []BlobRef{ref}}
		}
	}
	must(t, Publish(t.Context(), root, manifest, objectSource(objects), unchanged))
	inspected, err := Inspect(t.Context(), root)
	must(t, err)
	manifest.Generation = uuid.NewString()
	must(t, Publish(t.Context(), root, manifest, objectSource(objects), unchanged))
	if _, err := ReadInspectedBlob(t.Context(), root, inspected, "agents", ref, MaxChunkBytes); !errors.Is(err, ErrChanged) {
		t.Fatal("stale generation accepted", err)
	}
	current, err := Inspect(t.Context(), root)
	must(t, err)
	object := filepath.Join(root, Directory, "objects", ref.Digest)
	must(t, os.Remove(object))
	outside := filepath.Join(t.TempDir(), "outside")
	must(t, os.WriteFile(outside, blob, 0600))
	if err := os.Symlink(outside, object); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := ReadInspectedBlob(t.Context(), root, current, "agents", ref, MaxChunkBytes); !errors.Is(err, ErrChanged) {
		t.Fatal("linked object was read", err)
	}
}
