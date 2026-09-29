package workspacecontinuity

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestReadComponentRecordsBindsExactInspectedGenerationAndCanonicalFile(t *testing.T) {
	root, manifest, objects := fixture(t)
	must(t, Publish(t.Context(), root, manifest, objectSource(objects), unchanged))
	view, err := Inspect(t.Context(), root)
	must(t, err)
	visited := 0
	read := func(ctx context.Context, inspected Inspection) error {
		return ReadComponentRecords(ctx, root, inspected, "sessions", func(chunk Chunk) error {
			visited += len(chunk.Records)
			if chunk.WorkspaceID != "workspace-1" || chunk.Family != "messages" || chunk.Records[0].ID != "message-1" {
				t.Fatal("mixed source record identity", chunk)
			}
			return nil
		})
	}
	must(t, read(t.Context(), view))
	if visited != 1 {
		t.Fatal("record was not visited exactly once", visited)
	}
	if err := ReadComponentRecords(t.Context(), root, view, "assistant", func(Chunk) error { t.Fatal("empty domain visited"); return nil }); !errors.Is(err, ErrIncomplete) {
		t.Fatal("empty domain invented a record", err)
	}
	if err := ReadComponentRecords(t.Context(), root, view, "unknown", func(Chunk) error { return nil }); !errors.Is(err, ErrInvalid) {
		t.Fatal("unknown domain was accepted", err)
	}
	changed := view
	changed.Manifest.WorkspaceID = "not-owner"
	if err := read(t.Context(), changed); !errors.Is(err, ErrChanged) {
		t.Fatal("fabricated inspection metadata was accepted", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := read(cancelled, view); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled evidence read visited records", err)
	}

	manifest.Generation = uuid.NewString()
	must(t, Publish(t.Context(), root, manifest, objectSource(objects), unchanged))
	if err := read(t.Context(), view); !errors.Is(err, ErrChanged) {
		t.Fatal("an old review read another generation", err)
	}
	current, err := Inspect(t.Context(), root)
	must(t, err)
	path := filepath.Join(root, "workspace.json")
	must(t, os.WriteFile(path, []byte(`{"id":"workspace-1","name":"Synthetic projecT"}`), 0600))
	if err := read(t.Context(), current); !errors.Is(err, ErrDigest) {
		t.Fatal("a changed canonical file supported the old review", err)
	}
}

func TestReadComponentRecordsPropagatesCallbackFailureWithoutFallback(t *testing.T) {
	root, manifest, objects := fixture(t)
	must(t, Publish(t.Context(), root, manifest, objectSource(objects), unchanged))
	view, err := Inspect(t.Context(), root)
	must(t, err)
	failure := errors.New("stop reviewed enumeration")
	if err := ReadComponentRecords(t.Context(), root, view, "sessions", func(Chunk) error { return failure }); !errors.Is(err, failure) {
		t.Fatal("callback failure was hidden", err)
	}
	for _, component := range manifest.Components {
		if component.Domain != "sessions" {
			continue
		}
		object := filepath.Join(root, Directory, "objects", component.Chunks[0].Digest)
		must(t, os.WriteFile(object, []byte(`{"changed":true}`), 0600))
		called := false
		err := ReadComponentRecords(t.Context(), root, view, "sessions", func(Chunk) error { called = true; return nil })
		if called || err == nil {
			t.Fatal("changed object reached the typed callback", err)
		}
	}
}
