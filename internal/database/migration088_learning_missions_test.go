package database

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestLearningMissionsUpgradeFrom087PreservesOrdinaryRecordsWithoutEnrollment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade.db")
	db, err := Open(t.Context(), &Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO workspaces(id,name,created_at,updated_at) VALUES('retained','Existing work',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	// Reconstruct the immediately preceding schema only in this owned fixture.
	for _, table := range []string{"learning_evidence", "learning_reviews", "learning_operations", "learning_instances", "learning_sources", "learning_owners"} {
		if _, err := db.ExecContext(t.Context(), `DROP TABLE `+table); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(t.Context(), `DELETE FROM schema_migrations WHERE version=88`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(t.Context(), &Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	var name string
	if err := db.QueryRowContext(t.Context(), `SELECT name FROM workspaces WHERE id='retained'`).Scan(&name); err != nil || name != "Existing work" {
		t.Fatal("upgrade changed ordinary records", name, err)
	}
	for _, table := range []string{"learning_owners", "learning_sources", "learning_instances", "learning_operations", "learning_reviews", "learning_evidence"} {
		var count int
		if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil || count != 0 {
			t.Fatal("migration enrolled or backfilled learning", table, count, err)
		}
	}
}

func TestLearningMissionsMigration088InventoryAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "learning.db")
	db, err := Open(t.Context(), &Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := db.migration088LearningMissions(t.Context()); err != nil {
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
