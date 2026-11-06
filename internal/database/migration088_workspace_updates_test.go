package database

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestMigration088UpgradePreservesHistoryWithoutEnrollingOrUnreadBackfill(t *testing.T) {
	ctx := t.Context()
	cfg := &Config{Path: filepath.Join(t.TempDir(), "upstream-87.db"), BusyTimeout: 1000}
	raw, err := sql.Open("sqlite", cfg.Path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	raw.SetMaxOpenConns(1)
	legacy := &DB{DB: raw, path: cfg.Path}
	if err := legacy.applyPragmas(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	for version := 1; version <= 87; version++ {
		if err := legacy.runMigration(ctx, version); err != nil {
			t.Fatalf("migration %d: %v", version, err)
		}
		if _, err := raw.ExecContext(ctx, `INSERT INTO schema_migrations(version) VALUES(?)`, version); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`INSERT INTO workspaces(id,name,created_at,updated_at) VALUES('ws','Desk',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP),('suppressed','Other',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP),('deleted','Deleted',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
		`INSERT INTO sessions(id,title,agent_name,workspace_id,created_at,updated_at) VALUES('chat','Getting started','Mira','ws',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
		`INSERT INTO messages(id,session_id,role,content,created_at) VALUES('hello','chat','assistant','Historical welcome',CURRENT_TIMESTAMP)`,
		`INSERT INTO workspace_lead_conversations(workspace_id,owner_user_id,agent_instance_id,agent_name,session_id,assessment_delivered) VALUES('ws','local','mira','Mira','chat',1),('suppressed','local','other','Other','',0),('deleted','local','gone','Gone','missing-chat',1)`,
	} {
		if _, err := raw.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = upgraded.Close() })
	for range 2 {
		if err := upgraded.migration088WorkspaceUpdates(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var messages, enrollments, receipts int
	if err := upgraded.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM workspace_welcome_messages), (SELECT COUNT(*) FROM workspace_welcome_enrollments), (SELECT COUNT(*) FROM workspace_lead_conversations)`).Scan(&messages, &enrollments, &receipts); err != nil || messages != 0 || enrollments != 0 || receipts != 3 {
		t.Fatalf("upgrade manufactured updates or lost suppression: %d %d %d %v", messages, enrollments, receipts, err)
	}
	var content string
	if err := upgraded.QueryRowContext(ctx, `SELECT content FROM messages WHERE id='hello'`).Scan(&content); err != nil || content != "Historical welcome" {
		t.Fatal("history changed", err)
	}
}

func TestMigration088FreshConstraintsRollbackAndReset(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "updates.db")
	db, err := Open(ctx, &Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, statement := range []string{
		`INSERT INTO workspaces(id,name,created_at,updated_at) VALUES('ws','Desk',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
		`INSERT INTO workspace_lead_conversations(workspace_id,owner_user_id,agent_instance_id,agent_name) VALUES('ws','local','mira','Mira')`,
		`INSERT INTO workspace_welcome_enrollments(workspace_id,owner_user_id,agent_instance_id,agent_name) VALUES('ws','local','mira','Mira')`,
		`INSERT INTO workspace_welcome_messages VALUES('ws',1,'chat','hello',CURRENT_TIMESTAMP,NULL)`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`INSERT INTO workspace_welcome_messages VALUES('ws',1,'replacement','other',CURRENT_TIMESTAMP,NULL)`,
		`INSERT INTO workspace_welcome_messages VALUES('ws',2,'chat','hello',CURRENT_TIMESTAMP,NULL)`,
		`INSERT INTO workspace_welcome_messages VALUES('ws',3,'chat','other',CURRENT_TIMESTAMP,NULL)`,
		`INSERT INTO workspace_welcome_messages VALUES('foreign',1,'chat','other',CURRENT_TIMESTAMP,NULL)`,
		`UPDATE workspace_welcome_enrollments SET settled=2`,
	} {
		if _, err := db.ExecContext(ctx, statement); err == nil {
			t.Fatalf("accepted invalid write: %s", statement)
		}
	}
	err = db.InTransaction(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_welcome_messages VALUES('ws',2,'chat','assessment',CURRENT_TIMESTAMP,NULL)`); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO workspace_welcome_messages VALUES('ws',2,'chat','duplicate',CURRENT_TIMESTAMP,NULL)`)
		return err
	})
	if err == nil {
		t.Fatal("duplicate revision committed")
	}
	inspection, err := InspectReset(ctx, db.DB)
	if err != nil || len(inspection.Problems) != 0 {
		t.Fatalf("unclassified tables: %v %v", err, inspection.Problems)
	}
	for _, name := range []string{"workspace_welcome_enrollments", "workspace_welcome_messages", "workspace_lead_conversations"} {
		if inspection.Counts[name] == nil || *inspection.Counts[name] != 1 {
			t.Fatalf("reset inventory/rollback %s: %v", name, inspection.Counts[name])
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	deleted, err := ResetAppRecords(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"workspace_welcome_enrollments", "workspace_welcome_messages", "workspace_lead_conversations"} {
		if deleted[name] != 1 {
			t.Fatalf("reset missed %s: %v", name, deleted)
		}
	}
}
