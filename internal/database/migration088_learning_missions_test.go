package database

import (
	"path/filepath"
	"slices"
	"testing"
)

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
