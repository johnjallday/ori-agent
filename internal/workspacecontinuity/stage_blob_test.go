package workspacecontinuity

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestStageInspectedBlobStreamsOnlyDeclaredDomainIntoExclusiveRoot(t *testing.T) {
	source, manifest, objects := fixture(t)
	content := bytes.Repeat([]byte("private copied attachment "), MaxChunkBytes/len("private copied attachment ")+2)
	ref := BlobRef{Digest: Digest(content), Bytes: int64(len(content))}
	encoded, chunk, err := EncodeChunk(Chunk{Version: Version, WorkspaceID: manifest.WorkspaceID, Domain: "uploads", Family: "uploads",
		Records: []Record{{ID: "upload-1", Data: json.RawMessage(`{"state":"copied"}`)}}})
	must(t, err)
	objects[chunk.Digest], objects[ref.Digest] = encoded, content
	for i := range manifest.Components {
		if manifest.Components[i].Domain == "uploads" {
			manifest.Components[i] = Component{Domain: "uploads", Version: Version, Availability: Present,
				Counts: map[string]int64{"uploads": 1}, Chunks: []ChunkRef{chunk}, Blobs: []BlobRef{ref}}
		}
	}
	must(t, Publish(t.Context(), source, manifest, objectSource(objects), unchanged))
	inspected, err := Inspect(t.Context(), source)
	must(t, err)
	stageDir := t.TempDir()
	stage, err := os.OpenRoot(stageDir)
	must(t, err)
	defer func() { _ = stage.Close() }()
	target := "session_files/saved/files/attachment.bin"
	if err := StageInspectedBlobToRoot(t.Context(), source, inspected, "agents", ref, stage, target); !errors.Is(err, ErrIncomplete) {
		t.Fatal("different domain used another owner's object", err)
	}
	if err := StageInspectedBlobToRoot(t.Context(), source, inspected, "uploads", ref, stage, "../escape"); !errors.Is(err, ErrInvalid) {
		t.Fatal("stage traversal accepted", err)
	}
	if err := StageInspectedBlobToRoot(t.Context(), source, inspected, "uploads", ref, stage, target); err != nil {
		t.Fatal(err)
	}
	staged, err := ReadCanonicalFile(t.Context(), stageDir, target, len(content))
	if err != nil || !bytes.Equal(content, staged) {
		t.Fatal("streamed staged object changed", err)
	}
	if err := StageInspectedBlobToRoot(t.Context(), source, inspected, "uploads", ref, stage, target); !errors.Is(err, ErrChanged) {
		t.Fatal("repeat stage overwrote previously staged bytes", err)
	}
	forged := inspected
	forged.Manifest.Components = append([]Component(nil), inspected.Manifest.Components...)
	for i := range forged.Manifest.Components {
		if forged.Manifest.Components[i].Domain == "agents" {
			forged.Manifest.Components[i] = Component{Domain: "agents", Version: Version, Availability: Present, Counts: map[string]int64{"appearances": 1},
				Chunks: []ChunkRef{chunk}, Blobs: []BlobRef{ref}}
		}
	}
	if err := StageInspectedBlobToRoot(t.Context(), source, forged, "agents", ref, stage, "forged.bin"); !errors.Is(err, ErrChanged) && !errors.Is(err, ErrInvalid) {
		t.Fatal("caller-modified inspection used as evidence", err)
	}
	if _, err := ReadInspectedBlob(t.Context(), source, forged, "agents", ref, MaxBlobBytes); !errors.Is(err, ErrChanged) {
		t.Fatal("caller-modified inspection used to read an undeclared object", err)
	}
}

func TestStageInspectedBlobRefusesLinkedObjectAndStageParent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require platform permissions")
	}
	source, manifest, objects := fixture(t)
	content := []byte("synthetic private upload")
	ref := BlobRef{Digest: Digest(content), Bytes: int64(len(content))}
	encoded, chunk, err := EncodeChunk(Chunk{Version: Version, WorkspaceID: manifest.WorkspaceID, Domain: "uploads", Family: "uploads",
		Records: []Record{{ID: "upload-1", Data: json.RawMessage(`{"state":"copied"}`)}}})
	must(t, err)
	objects[chunk.Digest], objects[ref.Digest] = encoded, content
	for i := range manifest.Components {
		if manifest.Components[i].Domain == "uploads" {
			manifest.Components[i] = Component{Domain: "uploads", Version: Version, Availability: Present,
				Counts: map[string]int64{"uploads": 1}, Chunks: []ChunkRef{chunk}, Blobs: []BlobRef{ref}}
		}
	}
	must(t, Publish(t.Context(), source, manifest, objectSource(objects), unchanged))
	inspected, err := Inspect(t.Context(), source)
	must(t, err)
	outside := filepath.Join(t.TempDir(), "external")
	must(t, os.WriteFile(outside, content, 0600))
	stageDir := t.TempDir()
	stage, err := os.OpenRoot(stageDir)
	must(t, err)
	defer func() { _ = stage.Close() }()
	must(t, os.Symlink(filepath.Dir(outside), filepath.Join(stageDir, "linked")))
	if err := StageInspectedBlobToRoot(t.Context(), source, inspected, "uploads", ref, stage, "linked/external"); !errors.Is(err, ErrUnsafe) {
		t.Fatal("stage link accepted", err)
	}
	object := filepath.Join(source, Directory, "objects", ref.Digest)
	must(t, os.Remove(object))
	must(t, os.Symlink(outside, object))
	if err := StageInspectedBlobToRoot(t.Context(), source, inspected, "uploads", ref, stage, "private.bin"); !errors.Is(err, ErrChanged) {
		t.Fatal("linked checkpoint object accepted", err)
	}
	data, err := os.ReadFile(outside)
	if err != nil || !bytes.Equal(data, content) {
		t.Fatal("external bytes changed", err)
	}
}
