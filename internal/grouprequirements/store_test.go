package grouprequirements

import (
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
)

func TestSQLiteReceiptCanOnlyBeConsumedOnce(t *testing.T) {
	db, err := database.Open(t.Context(), &database.Config{InMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, err := NewSQLiteStore(db)
	if err != nil {
		t.Fatal(err)
	}
	receipt := Receipt{Token: "one-time-review", ExpiresAt: time.Now().UTC().Add(time.Minute)}
	if err := store.SaveReceipt(t.Context(), receipt); err != nil {
		t.Fatal(err)
	}
	if err := store.ConsumeReceipt(t.Context(), receipt.Token, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := store.ConsumeReceipt(t.Context(), receipt.Token, time.Now().UTC()); !errors.Is(err, ErrReviewStale) {
		t.Fatalf("second receipt consumption = %v, want stale", err)
	}
}

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
