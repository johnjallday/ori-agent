package workspacecontinuity

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestSpoolCollectsBeyondUIPagesAndPublishesOwnedAppearance(t *testing.T) {
	directory, manifest, _ := fixture(t)
	parent := t.TempDir()
	spool, err := NewSpool(parent, manifest.WorkspaceID)
	must(t, err)
	t.Cleanup(func() { _ = spool.Close() })
	data := []byte(`{"content":"synthetic historical work"}`)
	const records = 10003
	for index := 0; index < records; index++ {
		must(t, spool.AddRecord(t.Context(), "sessions", "messages", Record{ID: "message-" + strconv.Itoa(index), Data: data}))
	}
	data[12] = 'X' // the caller can reuse/mutate its page buffer after AddRecord
	image := []byte("synthetic owned bytes; domain adapter validates image format")
	blob, err := spool.AddBlobReader(t.Context(), "agents", bytes.NewReader(image), int64(len(image)))
	must(t, err)
	record, err := EncodeRecord("appearance-1", map[string]any{"digest": blob.Digest})
	must(t, err)
	must(t, spool.AddRecord(t.Context(), "agents", "appearances", record))
	must(t, spool.SetAvailability(t.Context(), "notes", Empty, ""))
	components, source, err := spool.Seal(t.Context())
	must(t, err)
	manifest.Components = components
	for _, component := range components {
		switch component.Domain {
		case "sessions":
			if component.Counts["messages"] != records || len(component.Chunks) != 21 {
				t.Fatal("collection lost a page or failed to split bounded chunks")
			}
			reader, err := source(t.Context(), component.Chunks[0].Digest)
			must(t, err)
			chunk, err := DecodeChunk(reader, component.Chunks[0], manifest.WorkspaceID, "sessions")
			must(t, err)
			must(t, reader.Close())
			if bytes.Contains(chunk.Records[0].Data, []byte("X")) {
				t.Fatal("collector retained a mutable caller buffer")
			}
		case "notes":
			if component.Availability != Empty {
				t.Fatal("verified empty domain changed")
			}
		case "agents":
			if len(component.Blobs) != 1 || component.Blobs[0] != blob {
				t.Fatal("owned appearance missing")
			}
		default:
			if component.Availability != Unsupported {
				t.Fatal("unimplemented domain fabricated emptiness")
			}
		}
	}
	must(t, Publish(t.Context(), directory, manifest, source, unchanged))
	if _, err := source(t.Context(), "../unowned"); !errors.Is(err, ErrInvalid) {
		t.Fatal("source accepted traversal")
	}
	if _, err := source(t.Context(), Digest([]byte("unowned"))); !errors.Is(err, ErrIncomplete) {
		t.Fatal("source served an unowned object")
	}
	if err := spool.AddRecord(t.Context(), "sessions", "messages", record); !errors.Is(err, ErrInvalid) {
		t.Fatal("sealed collection remained writable")
	}
	info, err := os.Stat(spool.directory)
	must(t, err)
	if info.Mode().Perm() != 0700 {
		t.Fatal("temporary history directory was not private")
	}
	for digest := range spool.objects {
		info, err := os.Stat(filepath.Join(spool.directory, digest))
		must(t, err)
		if info.Mode().Perm() != 0600 {
			t.Fatal("temporary history file was not private")
		}
	}
	must(t, spool.Close())
	entries, err := os.ReadDir(parent)
	must(t, err)
	if len(entries) != 0 {
		t.Fatal("temporary history survived successful cleanup")
	}
}

func TestSpoolFailsClosedOnCollectionErrors(t *testing.T) {
	for _, test := range []struct {
		name string
		fail func(*Spool) error
		want error
	}{
		{"duplicate", func(s *Spool) error {
			record := Record{ID: "same", Data: []byte(`{}`)}
			if err := s.AddRecord(t.Context(), "sessions", "messages", record); err != nil {
				return err
			}
			return s.AddRecord(t.Context(), "sessions", "messages", record)
		}, ErrConflict},
		{"short_blob", func(s *Spool) error {
			_, err := s.AddBlobReader(t.Context(), "uploads", bytes.NewReader([]byte("short")), 6)
			return err
		}, ErrChanged},
		{"long_blob", func(s *Spool) error {
			_, err := s.AddBlobReader(t.Context(), "uploads", bytes.NewReader([]byte("long")), 3)
			return err
		}, ErrChanged},
		{"large_blob", func(s *Spool) error {
			_, err := s.AddBlobReader(t.Context(), "uploads", bytes.NewReader(nil), MaxBlobBytes+1)
			return err
		}, ErrLimit},
		{"declared_unavailable", func(s *Spool) error {
			if err := s.SetAvailability(t.Context(), "agents", Unavailable, "missing_profile"); err != nil {
				return err
			}
			return s.AddRecord(t.Context(), "agents", "profiles", Record{ID: "profile", Data: []byte(`{}`)})
		}, ErrConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			spool, err := NewSpool(t.TempDir(), "workspace")
			must(t, err)
			t.Cleanup(func() { _ = spool.Close() })
			if err := test.fail(spool); !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
			if _, _, err := spool.Seal(t.Context()); !errors.Is(err, test.want) {
				t.Fatal("failed collection could be published")
			}
		})
	}
}

func TestSpoolCancellationAndUnknownFilesAreNotHidden(t *testing.T) {
	spool, err := NewSpool(t.TempDir(), "workspace")
	must(t, err)
	t.Cleanup(func() { _ = spool.Close() })
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := spool.AddBlobReader(ctx, "uploads", bytes.NewReader(nil), 0); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled source was read")
	}
	if _, _, err := spool.Seal(t.Context()); !errors.Is(err, context.Canceled) {
		t.Fatal("a fresh context hid an interrupted collection")
	}
	// Failure cleanup must never recursively remove a file this owner did not
	// create. The caller receives failure and may not acknowledge readiness.
	unknown := filepath.Join(spool.directory, "unexpected")
	must(t, os.WriteFile(unknown, []byte("do not delete"), 0600))
	if err := spool.Close(); err == nil {
		t.Fatal("unknown temporary content was silently deleted")
	}
	file, err := os.Open(unknown)
	must(t, err)
	got, err := io.ReadAll(file)
	must(t, err)
	must(t, file.Close())
	if string(got) != "do not delete" {
		t.Fatal("unknown content changed")
	}
}
