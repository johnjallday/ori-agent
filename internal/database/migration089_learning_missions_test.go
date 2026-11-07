package database

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
)

func TestLearningMissionsUpgradePreservesOrdinaryAndWorkspaceUpdateRecordsWithoutEnrollment(t *testing.T) {
	for _, previous := range []int{87, 88} {
		t.Run(fmt.Sprintf("from_%03d", previous), func(t *testing.T) {
			ctx := t.Context()
			cfg := &Config{Path: filepath.Join(t.TempDir(), "upgrade.db"), BusyTimeout: 1000}
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
			// Build the actual predecessor, not a newer schema with
			// its migration marker deleted. Both upgrade paths must coexist.
			for version := 1; version <= previous; version++ {
				if err := legacy.runMigration(ctx, version); err != nil {
					t.Fatalf("migration %d: %v", version, err)
				}
				if _, err := raw.ExecContext(ctx, `INSERT INTO schema_migrations(version) VALUES(?)`, version); err != nil {
					t.Fatal(err)
				}
			}
			for _, statement := range []string{
				`INSERT INTO workspaces(id,name,created_at,updated_at) VALUES('retained','Existing work',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
				`INSERT INTO sessions(id,title,agent_name,workspace_id,created_at,updated_at) VALUES('chat','Existing conversation','Mira','retained',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
				`INSERT INTO messages(id,session_id,role,content,created_at) VALUES('welcome','chat','assistant','Existing welcome',CURRENT_TIMESTAMP)`,
				`INSERT INTO workspace_lead_conversations(workspace_id,owner_user_id,agent_instance_id,agent_name,session_id,assessment_delivered) VALUES('retained','local','mira','Mira','chat',1)`,
			} {
				if _, err := raw.ExecContext(ctx, statement); err != nil {
					t.Fatal(err)
				}
			}
			if previous == 88 {
				for _, statement := range []string{
					`INSERT INTO workspace_welcome_enrollments(workspace_id,owner_user_id,agent_instance_id,agent_name,settled) VALUES('retained','local','mira','Mira',1)`,
					`INSERT INTO workspace_welcome_messages(workspace_id,revision,session_id,message_id,delivered_at,seen_at) VALUES('retained',1,'chat','welcome','2026-11-01T12:00:00Z','2026-11-01T12:01:00Z')`,
				} {
					if _, err := raw.ExecContext(ctx, statement); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := legacy.Close(); err != nil {
				t.Fatal(err)
			}
			db, err := Open(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			var name, content string
			if err := db.QueryRowContext(ctx, `SELECT name FROM workspaces WHERE id='retained'`).Scan(&name); err != nil || name != "Existing work" {
				t.Fatal("upgrade changed ordinary records", name, err)
			}
			if err := db.QueryRowContext(ctx, `SELECT content FROM messages WHERE id='welcome'`).Scan(&content); err != nil || content != "Existing welcome" {
				t.Fatal("upgrade changed existing conversation", content, err)
			}
			for _, table := range []string{"learning_owners", "learning_sources", "learning_instances", "learning_operations", "learning_reviews", "learning_evidence"} {
				var count int
				if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil || count != 0 {
					t.Fatal("migration enrolled or backfilled learning", table, count, err)
				}
			}
			var version, migrations, enrollments, messages int
			if err := db.QueryRowContext(ctx, `SELECT MAX(version),COUNT(*) FILTER(WHERE version IN (88,89)) FROM schema_migrations`).Scan(&version, &migrations); err != nil || version != schemaVersion || migrations != 2 {
				t.Fatal("both migrations must be registered", version, migrations, err)
			}
			want := 0
			if previous == 88 {
				want = 1
			}
			if err := db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM workspace_welcome_enrollments),(SELECT COUNT(*) FROM workspace_welcome_messages)`).Scan(&enrollments, &messages); err != nil || enrollments != want || messages != want {
				t.Fatal("upgrade changed or invented workspace updates", enrollments, messages, err)
			}
			if previous == 88 {
				var id, seen string
				var settled int
				if err := db.QueryRowContext(ctx, `SELECT message_id,seen_at FROM workspace_welcome_messages WHERE workspace_id='retained' AND revision=1`).Scan(&id, &seen); err != nil || id != "welcome" || seen != "2026-11-01T12:01:00Z" {
					t.Fatal("upgrade changed exact delivery/read state", id, seen, err)
				}
				if err := db.QueryRowContext(ctx, `SELECT settled FROM workspace_welcome_enrollments WHERE workspace_id='retained'`).Scan(&settled); err != nil || settled != 1 {
					t.Fatal("upgrade reopened settled delivery", settled, err)
				}
			}
		})
	}
}

func TestLearningMissionsMigration089InventoryAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "learning.db")
	db, err := Open(t.Context(), &Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := db.migration089LearningMissions(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	inspection, err := InspectReset(t.Context(), db.DB)
	if err != nil || len(inspection.Problems) != 0 {
		t.Fatalf("reset inventory: %v %v", err, inspection.Problems)
	}
	for _, table := range []string{"learning_owners", "learning_sources", "learning_instances", "learning_operations", "learning_reviews", "learning_evidence"} {
		if !slices.Contains(ResetRecordTables(), table) || inspection.Counts[table] == nil || *inspection.Counts[table] != 0 {
			t.Fatalf("missing reset inventory: %s", table)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(t.Context(), &Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	var version int
	if err := db.QueryRowContext(t.Context(), `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("reopen version: %d %v", version, err)
	}
}
