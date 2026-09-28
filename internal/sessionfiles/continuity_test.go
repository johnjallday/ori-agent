package sessionfiles

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

func TestCollectContinuityUploadsCopiesOnlyOwnedBytesAndKeepsLinksInert(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	db, err := database.Open(ctx, &database.Config{Path: filepath.Join(root, "data", "sessions.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, `INSERT INTO workspaces(id,name,owner_user_id,created_at,updated_at) VALUES('owner','Owner','local',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO sessions(id,title,agent_name,workspace_id,created_at,updated_at) VALUES('session-owned','Test','same-name','owner',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO sessions(id,title,agent_name,workspace_id,created_at,updated_at) VALUES('other','Global','same-name',NULL,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(root, "session_files")
	store, err := NewStore(base)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("private synthetic report content")
	copied, err := store.AddFileFromReader("session-owned", bytes.NewReader(payload), "report.txt", int64(len(payload)))
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside.txt")
	if err := os.WriteFile(outside, []byte("external contents that must not be copied"), 0600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		t.Skip("linked-file evidence needs symlink support")
	}
	linked, err := store.LinkFile("session-owned", outside, "external.txt")
	if err != nil {
		t.Fatal(err)
	}
	spool, err := workspacecontinuity.NewSpool(t.TempDir(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = spool.Close() }()
	if err := db.InTransaction(ctx, func(tx *sql.Tx) error { return CollectContinuityUploads(ctx, tx, base, "owner", spool) }); err != nil {
		t.Fatal(err)
	}
	components, readObject, err := spool.Seal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var records []workspacecontinuity.Record
	for _, component := range components {
		if component.Domain != "uploads" {
			continue
		}
		if component.Availability != workspacecontinuity.Present || component.Counts["uploads"] != 2 || len(component.Blobs) != 1 || component.Blobs[0].Digest != workspacecontinuity.Digest(payload) {
			t.Fatal("linked external bytes were included or owned bytes omitted", component)
		}
		for _, ref := range component.Chunks {
			stream, err := readObject(ctx, ref.Digest)
			if err != nil {
				t.Fatal(err)
			}
			chunk, err := workspacecontinuity.DecodeChunk(stream, ref, "owner", "uploads")
			if closeErr := stream.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
			if err != nil {
				t.Fatal(err)
			}
			records = append(records, chunk.Records...)
		}
	}
	if len(records) != 2 {
		t.Fatal("bounded upload evidence missing", len(records))
	}
	for _, record := range records {
		value, err := DecodeContinuityUpload(record, "owner")
		if err != nil {
			t.Fatal(err)
		}
		switch record.ID {
		case copied.ID:
			if value.State != "copied" || value.Blob == nil || value.Blob.Digest != workspacecontinuity.Digest(payload) {
				t.Fatal("copied asset changed", value)
			}
		case linked.ID:
			if value.State != "linked" || value.Blob != nil || value.Entry.OriginalPath != outside {
				t.Fatal("external link became owned bytes", value)
			}
		default:
			t.Fatal("unreviewed upload included", record.ID)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := CollectContinuityUploads(canceled, db, base, "owner", nil); !errors.Is(err, workspacecontinuity.ErrInvalid) {
		t.Fatal("nil collector accepted", err)
	}
	if err := CollectContinuityUploads(ctx, db, base, "missing", spool); !errors.Is(err, workspacecontinuity.ErrConflict) {
		t.Fatal("unknown owner treated as empty", err)
	}
}

func TestContinuityUploadRefusesForgedBlobDigest(t *testing.T) {
	value := ContinuityUpload{Version: 1, WorkspaceID: "owner", SessionID: "session-owned", State: "copied",
		Entry: FileEntry{ID: "owned-file", Path: "file.txt", Name: "file.txt", Size: 0, Status: FileStatusOK, AddedAt: time.Now()},
		Blob:  &workspacecontinuity.BlobRef{Digest: strings.Repeat("z", 64), Bytes: 0}}
	if _, err := SnapshotContinuityUpload(value, "owner"); !errors.Is(err, workspacecontinuity.ErrInvalid) {
		t.Fatal("invalid object digest accepted", err)
	}
}

func TestCollectContinuityUploadsRejectsUnclaimedAndLinkedCopies(t *testing.T) {
	root := t.TempDir()
	ctx := t.Context()
	db, err := database.Open(ctx, &database.Config{Path: filepath.Join(root, "data", "sessions.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, `INSERT INTO workspaces(id,name,owner_user_id,created_at,updated_at) VALUES('owner','Owner','local',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO sessions(id,title,agent_name,workspace_id,created_at,updated_at) VALUES('owned-session','Test','synthetic','owner',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(root, "uploads")
	store, err := NewStore(base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddFileFromReader("owned-session", bytes.NewReader([]byte("authored")), "report.txt", 7); err != nil {
		t.Fatal(err)
	}
	collect := func() error {
		spool, err := workspacecontinuity.NewSpool(t.TempDir(), "owner")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = spool.Close() }()
		return db.InTransaction(ctx, func(tx *sql.Tx) error { return CollectContinuityUploads(ctx, tx, base, "owner", spool) })
	}
	if err := os.WriteFile(filepath.Join(base, "owned-session", "files", "unclaimed.txt"), []byte("extra"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := collect(); !errors.Is(err, workspacecontinuity.ErrIncomplete) {
		t.Fatal("unclaimed upload was silently omitted", err)
	}
	if err := os.Remove(filepath.Join(base, "owned-session", "files", "unclaimed.txt")); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	if err := os.Remove(filepath.Join(base, "owned-session", "files", "report.txt")); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside")
	if err := os.WriteFile(outside, []byte("authored"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(base, "owned-session", "files", "report.txt")); err != nil {
		t.Fatal(err)
	}
	if err := collect(); !errors.Is(err, workspacecontinuity.ErrUnsafe) {
		t.Fatal("linked copied upload escaped", err)
	}
}
