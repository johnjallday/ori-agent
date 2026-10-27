package database

import (
	"path/filepath"
	"testing"
)

func TestMigration086WorkspaceCreationOperationsIsIdempotentAndResetInventoried(t *testing.T) {
	db, err := Open(t.Context(), &Config{Path: filepath.Join(t.TempDir(), "creation.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for range 2 {
		if err := db.migration086WorkspaceCreationOperations(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	insert := `INSERT INTO workspace_creation_operations(id,owner_user_id,request_digest,source_digest,request_json,workspace_id,state)
		VALUES (?, 'local', 'digest', 'source', '{}', ?, ?)`
	if _, err := db.ExecContext(t.Context(), insert, "review", "reserved", "reviewed"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), insert, "invalid", "other", "made_up"); err == nil {
		t.Fatal("unknown operation state accepted")
	}
	if _, err := db.ExecContext(t.Context(), insert, "second", "reserved", "reviewed"); err == nil {
		t.Fatal("reserved resource shared between operations")
	}
	inspection, err := InspectReset(t.Context(), db.DB)
	if err != nil || len(inspection.Problems) != 0 {
		t.Fatalf("unclassified creation state: %v %v", err, inspection.Problems)
	}
	if count := inspection.Counts["workspace_creation_operations"]; count == nil || *count != 1 {
		t.Fatalf("creation operations missing from reset inventory: %v", count)
	}
}
