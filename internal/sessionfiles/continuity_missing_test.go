package sessionfiles

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

func TestCollectContinuityUploadsReportsMissingAndRejectsManifestTraversal(t *testing.T) {
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
	base := filepath.Join(root, "session-files")
	store, err := NewStore(base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddFileFromReader("session-owned", bytes.NewReader([]byte("synthetic")), "file.txt", 9); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(base, "session-owned", "files", "file.txt")); err != nil {
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
	var upload *workspacecontinuity.Component
	for index := range components {
		if components[index].Domain == "uploads" {
			upload = &components[index]
		}
	}
	if upload == nil || upload.Availability != workspacecontinuity.Present || upload.Counts["uploads"] != 1 || len(upload.Blobs) != 0 {
		t.Fatal("missing owned bytes were incorrectly claimed", components)
	}
	for _, chunkRef := range upload.Chunks {
		stream, err := readObject(ctx, chunkRef.Digest)
		if err != nil {
			t.Fatal(err)
		}
		chunk, err := workspacecontinuity.DecodeChunk(stream, chunkRef, "owner", "uploads")
		if closeErr := stream.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, record := range chunk.Records {
			value, err := DecodeContinuityUpload(record, "owner")
			if err != nil || value.State != "missing" || value.Blob != nil {
				t.Fatal("missing upload was not explicit", value, err)
			}
		}
	}
	manifestPath := filepath.Join(base, "session-owned", "manifest.json")
	manifest, err := LoadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Files[0].Path = "../../outside.txt"
	if err := SaveManifest(manifest, manifestPath); err != nil {
		t.Fatal(err)
	}
	other, err := workspacecontinuity.NewSpool(t.TempDir(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	if err := db.InTransaction(ctx, func(tx *sql.Tx) error { return CollectContinuityUploads(ctx, tx, base, "owner", other) }); !errors.Is(err, workspacecontinuity.ErrInvalid) {
		t.Fatal("external filename was not refused", err)
	}
}
