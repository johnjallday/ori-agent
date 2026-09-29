package workspacecontinuity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func fixture(t *testing.T) (string, Manifest, map[string][]byte) {
	t.Helper()
	root := t.TempDir()
	workspace := []byte(`{"id":"workspace-1","name":"Synthetic project"}`)
	must(t, os.WriteFile(filepath.Join(root, "workspace.json"), workspace, 0o600))
	manifest := Manifest{
		Version: Version, Generation: uuid.NewString(), WorkspaceID: "workspace-1",
		CheckpointAt: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC), SourceRevision: 7,
		Files: []Fingerprint{{Path: "workspace.json", Digest: Digest(workspace), Bytes: int64(len(workspace))}},
	}
	manifest.SourceFingerprint = FilesDigest(manifest.Files)
	objects := map[string][]byte{}
	for _, domain := range DomainNames() {
		manifest.Components = append(manifest.Components, Component{Domain: domain, Version: Version, Availability: Empty})
	}
	data, ref, err := EncodeChunk(Chunk{
		Version: Version, WorkspaceID: manifest.WorkspaceID, Domain: "sessions", Family: "messages",
		Records: []Record{{ID: "message-1", Data: json.RawMessage(`{"content":"Private synthetic history","sequence":0}`)}},
	})
	must(t, err)
	for index := range manifest.Components {
		if manifest.Components[index].Domain == "sessions" {
			manifest.Components[index] = Component{Domain: "sessions", Version: Version, Availability: Present,
				Counts: map[string]int64{"messages": 1}, Chunks: []ChunkRef{ref}}
		}
	}
	objects[ref.Digest] = data
	return root, manifest, objects
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func objectSource(objects map[string][]byte) ObjectSource {
	return func(_ context.Context, digest string) (io.ReadCloser, error) {
		data, ok := objects[digest]
		if !ok {
			return nil, ErrIncomplete
		}
		return io.NopCloser(bytes.NewReader(data)), nil
	}
}

func unchanged(ctx context.Context) error { return ctx.Err() }

func TestFormatManifestRejectsDamageAndFutureVersions(t *testing.T) {
	_, original, _ := fixture(t)
	tests := []struct {
		name string
		edit func(*Manifest)
		want error
	}{
		{"future", func(m *Manifest) { m.Version++ }, ErrVersion},
		{"future_component", func(m *Manifest) { m.Components[0].Version++ }, ErrVersion},
		{"missing_component", func(m *Manifest) { m.Components = m.Components[1:] }, ErrIncomplete},
		{"duplicate_component", func(m *Manifest) { m.Components[1] = m.Components[0] }, ErrInvalid},
		{"bad_id", func(m *Manifest) { m.WorkspaceID = "../private" }, ErrInvalid},
		{"bad_generation", func(m *Manifest) { m.Generation = "../escape" }, ErrInvalid},
		{"duplicate_child", func(m *Manifest) { m.Children = []string{"child", "child"} }, ErrInvalid},
		{"self_child", func(m *Manifest) { m.Children = []string{m.WorkspaceID} }, ErrInvalid},
		{"too_many_children", func(m *Manifest) { m.Children = make([]string, MaxChildren+1) }, ErrLimit},
		{"absolute_file", func(m *Manifest) { m.Files[0].Path = "/private/history" }, ErrInvalid},
		{"traversal_file", func(m *Manifest) { m.Files[0].Path = "a/../../private" }, ErrInvalid},
		{"backslash_file", func(m *Manifest) { m.Files[0].Path = `a\private` }, ErrInvalid},
		{"child_file", func(m *Manifest) { m.Files[0].Path = "sub_workspaces/child/workspace.json" }, ErrInvalid},
		{"actual_child_file", func(m *Manifest) { m.Files[0].Path = "sub-workspaces/child/workspace.json" }, ErrInvalid},
		{"actual_child_directory", func(m *Manifest) { m.Files[0].Path = "sub-workspaces" }, ErrInvalid},
		{"actual_child_case_alias", func(m *Manifest) { m.Files[0].Path = "SUB-WORKSPACES/child/workspace.json" }, ErrInvalid},
		{"managed_file", func(m *Manifest) { m.Files[0].Path = Directory + "/current.json" }, ErrInvalid},
		{"case_alias_child", func(m *Manifest) { m.Files[0].Path = "SUB_WORKSPACES/child/workspace.json" }, ErrInvalid},
		{"case_alias_managed", func(m *Manifest) { m.Files[0].Path = ".ORI/Continuity/current.json" }, ErrInvalid},
		{"windows_dot_alias", func(m *Manifest) { m.Files[0].Path = "sub_workspaces./child/workspace.json" }, ErrInvalid},
		{"case_alias_file", func(m *Manifest) {
			alias := m.Files[0]
			alias.Path = "WORKSPACE.JSON"
			m.Files = append(m.Files, alias)
		}, ErrInvalid},
		{"negative_size", func(m *Manifest) { m.Files[0].Bytes = -1 }, ErrLimit},
		{"oversize", func(m *Manifest) { m.Files[0].Bytes = 1 << 62 }, ErrLimit},
		{"wrong_fingerprint", func(m *Manifest) { m.SourceFingerprint = Digest(nil) }, ErrDigest},
		{"unavailable_reason", func(m *Manifest) { m.Components[0].Availability = Unavailable }, ErrInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data, err := json.Marshal(original)
			must(t, err)
			var manifest Manifest
			must(t, json.Unmarshal(data, &manifest))
			test.edit(&manifest)
			if err := manifest.Validate(); !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
		})
	}
	data, pointer, err := EncodeManifest(original)
	must(t, err)
	decoded, err := DecodeManifest(bytes.NewReader(data), pointer)
	must(t, err)
	if decoded.SourceRevision != 7 || !decoded.CheckpointAt.Equal(original.CheckpointAt) {
		t.Fatal("source metadata changed")
	}
}

func TestStrictJSONAndChunkBinding(t *testing.T) {
	_, manifest, objects := fixture(t)
	var ref ChunkRef
	for _, component := range manifest.Components {
		if component.Domain == "sessions" {
			ref = component.Chunks[0]
		}
	}
	for _, data := range []string{
		`{"version":1,"version":2}`,
		`{"version":1,"unknown":true}`,
		`{} {}`,
		"{\"version\":\"\xff\"}",
		strings.Repeat("[", 66) + strings.Repeat("]", 66),
	} {
		if _, err := DecodePointer(strings.NewReader(data)); err == nil {
			t.Fatal("accepted malformed pointer")
		}
	}
	if _, err := DecodePointer(strings.NewReader(strings.Repeat(" ", MaxPointerBytes+1))); !errors.Is(err, ErrLimit) {
		t.Fatalf("unbounded pointer: %v", err)
	}
	if _, err := DecodeChunk(bytes.NewReader(objects[ref.Digest]), ref, "other-workspace", "sessions"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("foreign owner accepted: %v", err)
	}
	chunk, err := DecodeChunk(bytes.NewReader(objects[ref.Digest]), ref, manifest.WorkspaceID, "sessions")
	must(t, err)
	chunk.Records = append(chunk.Records, chunk.Records[0])
	if _, _, err := EncodeChunk(chunk); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate ID accepted: %v", err)
	}
	chunk.Records = chunk.Records[:1]
	chunk.Records[0].Data = json.RawMessage(`{"content":"first","content":"last"}`)
	if _, _, err := EncodeChunk(chunk); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nested duplicate key accepted: %v", err)
	}
	chunk.Records[0].Data = json.RawMessage(`{"content":"` + strings.Repeat("a", MaxRecordBytes) + `"}`)
	if _, _, err := EncodeChunk(chunk); !errors.Is(err, ErrLimit) {
		t.Fatalf("oversized record accepted: %v", err)
	}
}

func TestPublishInspectPrivateAtomicAndPruned(t *testing.T) {
	root, manifest, objects := fixture(t)
	must(t, Publish(t.Context(), root, manifest, objectSource(objects), unchanged))
	inspected, err := Inspect(t.Context(), root)
	must(t, err)
	if inspected.Manifest.Generation != manifest.Generation {
		t.Fatal("wrong generation")
	}
	must(t, filepath.WalkDir(filepath.Join(root, ".ori"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0o007 != 0 {
			t.Fatalf("world accessible checkpoint: %s", path)
		}
		if !entry.IsDir() && runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
			t.Fatal("checkpoint file is not private")
		}
		return nil
	}))
	oldGeneration := manifest.Generation
	manifest.Generation = uuid.NewString()
	for index := range manifest.Components {
		if manifest.Components[index].Domain == "sessions" {
			manifest.Components[index] = Component{Domain: "sessions", Version: Version, Availability: Empty}
		}
	}
	must(t, Publish(t.Context(), root, manifest, objectSource(objects), unchanged))
	_, err = Inspect(t.Context(), root)
	must(t, err)
	if _, err := os.Stat(filepath.Join(root, Directory, "generations", oldGeneration+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("obsolete manifest retains deleted history")
	}
	entries, err := os.ReadDir(filepath.Join(root, Directory, "objects"))
	must(t, err)
	if len(entries) != 0 {
		t.Fatal("deleted history survives in unreferenced objects")
	}
}

func TestPublicationFailureDoesNotReplaceCompletedPointer(t *testing.T) {
	for _, failure := range []string{"fence", "file_change", "source", "cancel"} {
		t.Run(failure, func(t *testing.T) {
			root, manifest, objects := fixture(t)
			must(t, Publish(t.Context(), root, manifest, objectSource(objects), unchanged))
			path := filepath.Join(root, Directory, "current.json")
			before, err := os.ReadFile(path)
			must(t, err)
			manifest.Generation = uuid.NewString()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			fence := func(context.Context) error {
				calls++
				if calls == 2 && failure == "fence" {
					return ErrChanged
				}
				if calls == 1 && failure == "file_change" {
					must(t, os.WriteFile(filepath.Join(root, "workspace.json"), []byte(`{"id":"foreign"}`), 0o600))
				}
				return nil
			}
			if failure == "source" {
				for index := range manifest.Components {
					if manifest.Components[index].Domain == "sessions" {
						manifest.Components[index].Chunks[0].Digest = Digest([]byte("absent"))
					}
				}
			}
			if failure == "cancel" {
				cancel()
			}
			if err := Publish(ctx, root, manifest, objectSource(objects), fence); err == nil {
				t.Fatal("expected publication failure")
			}
			after, err := os.ReadFile(path)
			must(t, err)
			if !bytes.Equal(before, after) {
				t.Fatal("failed publication changed current pointer")
			}
		})
	}
}

func TestInspectSeparatesLegacyFromDamagedAndRejectsLinks(t *testing.T) {
	root, manifest, objects := fixture(t)
	if _, err := Inspect(t.Context(), root); !errors.Is(err, ErrLegacy) {
		t.Fatalf("legacy classified as %v", err)
	}
	must(t, os.MkdirAll(filepath.Join(root, Directory, "staging"), 0o750))
	if _, err := Inspect(t.Context(), root); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("partial modern classified as %v", err)
	}
	if err := Publish(t.Context(), root, manifest, objectSource(objects), unchanged); !errors.Is(err, ErrCollision) {
		t.Fatalf("overwrote unowned reserved subtree: %v", err)
	}
	root, manifest, objects = fixture(t)
	must(t, Publish(t.Context(), root, manifest, objectSource(objects), unchanged))
	for digest := range objects {
		path := filepath.Join(root, Directory, "objects", digest)
		must(t, os.Remove(path))
		if _, err := Inspect(t.Context(), root); !errors.Is(err, ErrIncomplete) {
			t.Fatalf("missing modern object classified as %v", err)
		}
		external := filepath.Join(t.TempDir(), "private")
		must(t, os.WriteFile(external, objects[digest], 0o600))
		if err := os.Symlink(external, path); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if _, err := Inspect(t.Context(), root); !errors.Is(err, ErrUnsafe) {
			t.Fatalf("external symlink accepted: %v", err)
		}
		if err := Publish(t.Context(), root, manifest, objectSource(objects), unchanged); !errors.Is(err, ErrUnsafe) {
			t.Fatalf("publisher followed object symlink: %v", err)
		}
	}
}

func TestConcurrentPublicationsLeaveOneCompleteGeneration(t *testing.T) {
	root, manifest, objects := fixture(t)
	var group sync.WaitGroup
	results := make(chan error, 4)
	for range 4 {
		copy := manifest
		copy.Generation = uuid.NewString()
		group.Go(func() {
			results <- Publish(t.Context(), root, copy, objectSource(objects), unchanged)
		})
	}
	group.Wait()
	close(results)
	for err := range results {
		must(t, err)
	}
	_, err := Inspect(t.Context(), root)
	must(t, err)
	generations, err := os.ReadDir(filepath.Join(root, Directory, "generations"))
	must(t, err)
	if len(generations) != 1 {
		t.Fatal("concurrent cleanup left old generations")
	}
}
