package database

import (
	"path/filepath"
	"testing"
)

func TestMigration065LocalConfigurationIsInstallationOnly(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "upgrade.db")
	db, err := Open(ctx, &Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, `DROP TABLE continuity_file_mutations; DROP TABLE workspace_local_config; DELETE FROM schema_migrations WHERE version>=65`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, &Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	db = reopened
	if err := db.migration065WorkspaceLocalConfig(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO workspaces(id,name,created_at,updated_at) VALUES ('private-owner','Synthetic',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO workspace_local_config VALUES ('private-owner','agent','guide','native','synthetic-slot',X'1234',CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	inspection, err := InspectReset(ctx, db.DB)
	if err != nil || len(inspection.Problems) != 0 || inspection.Counts["workspace_local_config"] == nil || *inspection.Counts["workspace_local_config"] != 1 {
		t.Fatalf("new private owner not classified: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM workspaces WHERE id='private-owner'`); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_local_config`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("workspace deletion retained private configuration: count=%d err=%v", count, err)
	}
}
