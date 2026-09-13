package grouprequirements

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
)

func TestSQLiteStoreDomainsAreClassifiedForAppRecordReset(t *testing.T) {
	db, err := database.Open(t.Context(), &database.Config{InMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Errorf("close database: %v", closeErr)
		}
	})
	if _, err := NewSQLiteStore(db); err != nil {
		t.Fatal(err)
	}

	inspection, err := database.InspectReset(t.Context(), db.DB)
	if err != nil {
		t.Fatal(err)
	}
	if len(inspection.Problems) != 0 {
		t.Fatalf("group requirement store blocks app-record reset: %v", inspection.Problems)
	}
	for _, table := range []string{"group_requirement_reviews", "group_requirement_operations"} {
		count, classified := inspection.Counts[table]
		if !classified || count == nil {
			t.Fatalf("%s is not a classified app-record domain", table)
		}
	}
}
