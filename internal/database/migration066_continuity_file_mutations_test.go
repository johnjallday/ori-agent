package database

import (
	"path/filepath"
	"testing"
)

func TestMigration066FileMutationBarriersAreLocalResetRecords(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "upgrade.db")
	db, err := Open(ctx, &Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, `DROP TABLE continuity_file_mutations; DELETE FROM schema_migrations WHERE version>=66;
		INSERT INTO workspaces(id,name,created_at,updated_at) VALUES ('source','Synthetic',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(ctx, &Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.migration066ContinuityFileMutations(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM continuity_attachments`).Scan(&count); err != nil || count != 0 {
		t.Fatal("schema upgrade granted admission", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO continuity_file_mutations VALUES ('source','workspace.json','synthetic-token','pending',CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	inspection, err := InspectReset(ctx, db.DB)
	if err != nil || len(inspection.Problems) != 0 || inspection.Counts["continuity_file_mutations"] == nil || *inspection.Counts["continuity_file_mutations"] != 1 {
		t.Fatalf("barriers not reset-classified: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM workspaces WHERE id='source'`); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM continuity_file_mutations`).Scan(&count); err != nil || count != 0 {
		t.Fatal("deletion retained barriers", err)
	}
}
