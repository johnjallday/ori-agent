package database

import (
	"path/filepath"
	"testing"
)

func TestMigration092InventoriesFilingClaimsForReset(t *testing.T) {
	db, err := Open(t.Context(), &Config{Path: filepath.Join(t.TempDir(), "expansion.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.migration092FilingExpansion(t.Context()); err != nil {
		t.Fatal(err)
	}
	inspection, err := InspectReset(t.Context(), db.DB)
	if err != nil || len(inspection.Problems) != 0 || inspection.Counts["economy_filing_claims"] == nil {
		t.Fatalf("claim missing from reset inspection: %+v %v", inspection, err)
	}
}
