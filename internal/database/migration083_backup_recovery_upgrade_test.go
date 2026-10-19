package database

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestMigration083PreservesUpstreamArrangementsAndResetsBothDomains(t *testing.T) {
	ctx := t.Context()
	cfg := &Config{Path: filepath.Join(t.TempDir(), "upstream-82.db"), BusyTimeout: 1000}
	raw, err := sql.Open("sqlite", cfg.Path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	raw.SetMaxOpenConns(1)
	raw.SetMaxIdleConns(1)
	legacy := &DB{DB: raw, path: cfg.Path}
	if err := legacy.applyPragmas(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.ExecContext(ctx, `CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at DATETIME DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	// Construct the real upstream sequence, not a current schema with its
	// version counter rewound. Published migrations 81 and 82 must stay intact.
	for version := 1; version <= 82; version++ {
		if err := legacy.runMigration(ctx, version); err != nil {
			t.Fatalf("upstream migration %d: %v", version, err)
		}
		if _, err := legacy.ExecContext(ctx, `INSERT INTO schema_migrations(version) VALUES (?)`, version); err != nil {
			t.Fatal(err)
		}
	}
	const record = `{"version":1,"id":"existing-arrangement","revision":1,"state":"draft"}`
	for _, statement := range []string{
		`INSERT INTO sessions(id,title,agent_name,created_at,updated_at) VALUES('existing-chat','Existing','Atlas',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
		`INSERT INTO messages(id,session_id,role,content,created_at) VALUES('existing-answer','existing-chat','assistant','Keep this answer',CURRENT_TIMESTAMP)`,
		`INSERT INTO assistant_arrangements(id,session_id,message_id,user_id,workspace_id,agent_name,state_version,context_revision,folder_revision,revision,state,record_json)
		 VALUES('existing-arrangement','existing-chat','existing-answer','local','hq','Atlas',1,'context','existing-answer',1,'draft','` + record + `')`,
		`INSERT INTO assistant_arrangement_effects(arrangement_id,session_id,operation,record_revision,offer_id,message_id,state,created_at,updated_at)
		 VALUES('existing-arrangement','existing-chat',0,1,'existing-offer','existing-answer','awaiting_user_review',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
	} {
		if _, err := legacy.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := legacy.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE name IN ('backup_recovery_local','backup_recovery_receipts','backup_recovery_authority')`).Scan(&count); err != nil || count != 0 {
		t.Fatal("upstream schema already contains the unpublished backup migration", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = upgraded.DB.Close() })
	var saved, answer, effect string
	if err := upgraded.QueryRowContext(ctx, `SELECT record_json FROM assistant_arrangements WHERE id='existing-arrangement'`).Scan(&saved); err != nil || saved != record {
		t.Fatal("backup upgrade changed the existing arrangement", err)
	}
	if err := upgraded.QueryRowContext(ctx, `SELECT content FROM messages WHERE id='existing-answer'`).Scan(&answer); err != nil || answer != "Keep this answer" {
		t.Fatal("backup upgrade changed conversation history", err)
	}
	if err := upgraded.QueryRowContext(ctx, `SELECT state FROM assistant_arrangement_effects WHERE offer_id='existing-offer'`).Scan(&effect); err != nil || effect != "awaiting_user_review" {
		t.Fatal("backup upgrade lost the existing review correlation", err)
	}
	if err := upgraded.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version IN (81,82,83)`).Scan(&count); err != nil || count != 3 {
		t.Fatal("upstream and backup migrations did not each run", err)
	}
	if err := upgraded.QueryRowContext(ctx, `SELECT
	 (SELECT COUNT(*) FROM backup_recovery_local) +
	 (SELECT COUNT(*) FROM backup_recovery_receipts) +
	 (SELECT COUNT(*) FROM backup_recovery_authority)`).Scan(&count); err != nil || count != 0 {
		t.Fatal("migration manufactured backup state or authority", err)
	}
	if _, err := upgraded.ExecContext(ctx, `INSERT INTO workspaces(id,name,created_at,updated_at) VALUES('backup-owner','Invented',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP);
	 INSERT INTO backup_recovery_local VALUES('backup-owner','local',1,?,1,0,0,zeroblob(28),CURRENT_TIMESTAMP);
	 INSERT INTO backup_recovery_authority VALUES('backup-owner',0)`, backupFixtureGeneration); err != nil {
		t.Fatal(err)
	}
	seedBackupReceipt(t, upgraded)
	before, err := InspectReset(ctx, upgraded.DB)
	if err != nil || len(before.Problems) != 0 || before.Version != schemaVersion {
		t.Fatal("merged app domains are not fully reset-inventoried", err)
	}
	domains := []string{"assistant_arrangements", "assistant_arrangement_effects", "backup_recovery_local", "backup_recovery_receipts", "backup_recovery_authority"}
	for _, table := range domains {
		if before.Counts[table] == nil || *before.Counts[table] != 1 {
			t.Fatalf("missing populated reset domain: %s", table)
		}
	}
	if err := upgraded.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ResetAppRecords(ctx, cfg.Path); err != nil {
		t.Fatal(err)
	}
	after, err := InspectResetFile(ctx, cfg.Path)
	if err != nil || len(after.Problems) != 0 || after.Version != before.Version || after.SchemaDigest != before.SchemaDigest {
		t.Fatal("reset changed the merged schema or left unclassified data", err)
	}
	for _, table := range domains {
		if after.Counts[table] == nil || *after.Counts[table] != 0 {
			t.Fatalf("reset retained merged app records: %s", table)
		}
	}
}
