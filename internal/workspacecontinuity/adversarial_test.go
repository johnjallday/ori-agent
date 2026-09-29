package workspacecontinuity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestJSONSurrogatesAndEscapedDuplicateKeys(t *testing.T) {
	for _, data := range []string{
		`{"content":"\ud800"}`, `{"content":"\udfff"}`,
		`{"content":"\ud800\u1234"}`, `{"content":"\ud800unterminated`,
		`{"content":"a","cont\u0065nt":"b"}`,
	} {
		var record json.RawMessage
		if err := strictJSON([]byte(data), &record); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid unicode/duplicate accepted: %v", err)
		}
	}
	for _, data := range []string{`{"content":"\ud83d\ude00"}`, `{"content":"\\ud800"}`, `{"content":"� and 😀"}`} {
		var record json.RawMessage
		must(t, strictJSON([]byte(data), &record))
	}
}

func TestCaseInsensitiveJSONAliasesCannotOverrideWireFields(t *testing.T) {
	_, manifest, _ := fixture(t)
	data, pointer, err := EncodeManifest(manifest)
	must(t, err)
	data = bytes.Replace(data, []byte(`"version":1`), []byte(`"version":1,"Version":1`), 1)
	pointer.Digest = Digest(data)
	if _, err := DecodeManifest(bytes.NewReader(data), pointer); !errors.Is(err, ErrInvalid) {
		t.Fatalf("case alias accepted: %v", err)
	}
	var record json.RawMessage
	// User-authored object keys are data, not wire-field aliases.
	must(t, strictJSON([]byte(`{"customKey":1,"CustomKey":2}`), &record))
}

func TestPublisherRefusesPubliclyReadableReusedObjects(t *testing.T) {
	root, manifest, objects := fixture(t)
	must(t, Publish(t.Context(), root, manifest, objectSource(objects), unchanged))
	for digest := range objects {
		path := filepath.Join(root, Directory, "objects", digest)
		must(t, os.Chmod(path, 0o644))
		info, err := os.Stat(path)
		must(t, err)
		if info.Mode().Perm()&0o077 == 0 {
			t.Skip("filesystem does not expose POSIX modes")
		}
		if err := Publish(t.Context(), root, manifest, objectSource(objects), unchanged); !errors.Is(err, ErrUnsafe) {
			t.Fatalf("reused non-private object: %v", err)
		}
	}
}

func TestDeclaredCountsAndCrossChunkDuplicates(t *testing.T) {
	root, manifest, objects := fixture(t)
	for index := range manifest.Components {
		component := &manifest.Components[index]
		if component.Domain != "sessions" {
			continue
		}
		component.Counts["messages"]++
		if err := manifest.Validate(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("wrong family count accepted: %v", err)
		}
		// Different bytes/digest, but the same stable record ID in another chunk.
		data, ref, err := EncodeChunk(Chunk{
			Version: Version, WorkspaceID: manifest.WorkspaceID, Domain: "sessions", Family: "messages",
			Records: []Record{{ID: "message-1", Data: json.RawMessage(`{"content":"duplicate"}`)}},
		})
		must(t, err)
		objects[ref.Digest] = data
		component.Chunks = append(component.Chunks, ref)
	}
	must(t, manifest.Validate())
	if err := Publish(t.Context(), root, manifest, objectSource(objects), unchanged); !errors.Is(err, ErrInvalid) {
		t.Fatalf("publisher accepted duplicate: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, Directory, "current.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid generation was published")
	}
}

func TestFutureMarkerCorruptObjectsAndForeignCanonicalOwner(t *testing.T) {
	for _, mutation := range []string{"future_marker", "future_pointer", "corrupt", "foreign", "directory_link"} {
		t.Run(mutation, func(t *testing.T) {
			root, manifest, objects := fixture(t)
			must(t, Publish(t.Context(), root, manifest, objectSource(objects), unchanged))
			want := ErrInvalid
			switch mutation {
			case "future_marker":
				must(t, os.WriteFile(filepath.Join(root, Directory, "format.json"), []byte(`{"format":"ori.workspace-continuity","version":2}`), 0o600))
				want = ErrVersion
			case "future_pointer":
				pointerPath := filepath.Join(root, Directory, "current.json")
				data, err := os.ReadFile(pointerPath)
				must(t, err)
				must(t, os.WriteFile(pointerPath, bytes.Replace(data, []byte(`"version":1`), []byte(`"version":2`), 1), 0o600))
				want = ErrVersion
			case "corrupt":
				for digest, original := range objects {
					data := bytes.Clone(original)
					data[len(data)-1] = 'x'
					must(t, os.WriteFile(filepath.Join(root, Directory, "objects", digest), data, 0o600))
				}
				want = ErrDigest
			case "foreign":
				data := []byte(`{"id":"unrelated"}`)
				must(t, os.WriteFile(filepath.Join(root, "workspace.json"), data, 0o600))
				manifest.Files[0].Digest, manifest.Files[0].Bytes = Digest(data), int64(len(data))
				manifest.SourceFingerprint = FilesDigest(manifest.Files)
				encoded, pointer, err := EncodeManifest(manifest)
				must(t, err)
				must(t, os.WriteFile(filepath.Join(root, Directory, "generations", manifest.Generation+".json"), encoded, 0o600))
				encoded, err = json.Marshal(pointer)
				must(t, err)
				must(t, os.WriteFile(filepath.Join(root, Directory, "current.json"), encoded, 0o600))
			case "directory_link":
				path := filepath.Join(root, Directory, "objects")
				external := filepath.Join(t.TempDir(), "external")
				must(t, os.Rename(path, external))
				if err := os.Symlink(external, path); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
				want = ErrUnsafe
			}
			if _, err := Inspect(t.Context(), root); !errors.Is(err, want) {
				t.Fatalf("got %v, want %v", err, want)
			}
		})
	}
}

func TestBlobStreamingAndCancellation(t *testing.T) {
	root, manifest, objects := fixture(t)
	blob := []byte(strings.Repeat("private synthetic upload\n", 20000))
	meta, ref, err := EncodeChunk(Chunk{
		Version: Version, WorkspaceID: manifest.WorkspaceID, Domain: "uploads", Family: "uploads",
		Records: []Record{{ID: "file-1", Data: json.RawMessage(`{"name":"owned.txt"}`)}},
	})
	must(t, err)
	for index := range manifest.Components {
		if manifest.Components[index].Domain == "uploads" {
			manifest.Components[index] = Component{Domain: "uploads", Version: Version, Availability: Present,
				Counts: map[string]int64{"uploads": 1}, Chunks: []ChunkRef{ref},
				Blobs: []BlobRef{{Digest: Digest(blob), Bytes: int64(len(blob))}}}
		}
	}
	objects[ref.Digest], objects[Digest(blob)] = meta, blob
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	source := func(ctx context.Context, digest string) (io.ReadCloser, error) {
		if digest == Digest(blob) {
			cancel()
		}
		return objectSource(objects)(ctx, digest)
	}
	if err := Publish(ctx, root, manifest, source, unchanged); !errors.Is(err, context.Canceled) {
		t.Fatalf("upload not cancelled: %v", err)
	}
	if _, err := Inspect(t.Context(), root); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("cancelled first publication looks complete: %v", err)
	}
	must(t, Publish(t.Context(), root, manifest, objectSource(objects), unchanged))
	_, err = Inspect(t.Context(), root)
	must(t, err)
}

func TestInterruptedInitializationCanRetryWithoutClaimingAnotherDirectory(t *testing.T) {
	root, manifest, objects := fixture(t)
	// Simulate a crash after allocating private initialization scratch, before
	// the exclusive directory publication. No history has been written yet.
	scratch := filepath.Join(root, ".ori", initializationPrefix+uuid.NewString())
	must(t, os.MkdirAll(scratch, 0o750))
	must(t, os.WriteFile(filepath.Join(scratch, "format.json"), []byte(formatMarker), 0o600))
	if _, err := Inspect(t.Context(), root); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("interrupted initialization downgraded to legacy: %v", err)
	}
	must(t, Publish(t.Context(), root, manifest, objectSource(objects), unchanged))
	_, err := Inspect(t.Context(), root)
	must(t, err)
	ori, err := os.OpenRoot(filepath.Join(root, ".ori"))
	must(t, err)
	defer func() { _ = ori.Close() }()
	name := initializationPrefix + uuid.NewString()
	must(t, ori.Mkdir(name, 0o750))
	if err := renameNewDirectory(ori, name, "continuity"); err == nil {
		t.Fatal("exclusive rename replaced an existing managed directory")
	}
	_, err = Inspect(t.Context(), root)
	must(t, err)
}

func TestCleanupFailureIsNotReadyAndRetryPreservesUnknownFiles(t *testing.T) {
	root, manifest, objects := fixture(t)
	must(t, Publish(t.Context(), root, manifest, objectSource(objects), unchanged))
	unknown := filepath.Join(root, Directory, "staging", "not-ours.txt")
	must(t, os.WriteFile(unknown, []byte("not ours"), 0o600))
	manifest.Generation = uuid.NewString()
	if err := Publish(t.Context(), root, manifest, objectSource(objects), unchanged); !errors.Is(err, ErrCollision) {
		t.Fatalf("cleanup collision hidden: %v", err)
	}
	data, err := os.ReadFile(unknown)
	must(t, err)
	if string(data) != "not ours" {
		t.Fatal("cleanup deleted an unknown file")
	}
	// Simulate the owner removing only this test-created collision, then retry.
	must(t, os.Remove(unknown))
	must(t, Publish(t.Context(), root, manifest, objectSource(objects), unchanged))
	_, err = Inspect(t.Context(), root)
	must(t, err)
}
