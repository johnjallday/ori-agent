package database

import (
	"path/filepath"
	"testing"
)

// Retained incoming-assistant records live and die with their workspace row,
// and only the two retained domains are accepted.
func TestMigration071RetainedRecordsFollowTheirWorkspace(t *testing.T) {
	ctx := t.Context()
	db, err := Open(ctx, &Config{Path: filepath.Join(t.TempDir(), "retained.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := db.migration071ContinuityRetainedRecords(ctx); err != nil {
		t.Fatal("idempotent migration:", err)
	}
	for _, statement := range []string{
		`INSERT INTO workspaces(id,name,created_at,updated_at) VALUES ('kept','Kept',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
		`INSERT INTO workspaces(id,name,created_at,updated_at) VALUES ('gone','Gone',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
		`INSERT INTO continuity_retained_records(workspace_id,domain,family,record_id,data,operation_id) VALUES ('kept','assistant','agreements','a','{}','op')`,
		`INSERT INTO continuity_retained_records(workspace_id,domain,family,record_id,data,operation_id) VALUES ('gone','setup','assignments','s','{}','op')`,
		`DELETE FROM workspaces WHERE id='gone'`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(statement, err)
		}
	}
	var kept, gone int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM continuity_retained_records WHERE workspace_id='kept'`).Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM continuity_retained_records WHERE workspace_id='gone'`).Scan(&gone); err != nil {
		t.Fatal(err)
	}
	if kept != 1 || gone != 0 {
		t.Fatalf("retained rows did not follow their workspace: kept=%d gone=%d", kept, gone)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO continuity_retained_records(workspace_id,domain,family,record_id,data,operation_id)
		VALUES ('kept','sessions','sessions','x','{}','op')`); err == nil {
		t.Fatal("a non-retained domain was accepted")
	}
}
