package database_test

import (
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/testutil/resetfixture"
)

func TestResetAppRecordsClearsFixedDomainsAndPreservesSchemaAndBackingFiles(t *testing.T) {
	fixture := resetfixture.NewSeeded(t)
	path := filepath.Join(fixture.Paths().DataDir, "sessions.db")
	before, err := database.InspectResetFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if before.Counts["sessions"] == nil || *before.Counts["sessions"] == 0 || before.Counts["messages"] == nil || *before.Counts["messages"] == 0 {
		t.Fatalf("fixture app records are empty: %#v", before.Counts)
	}

	deleted, err := database.ResetAppRecords(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if deleted["sessions"] == 0 || deleted["messages"] == 0 {
		t.Fatalf("deletion counts = %#v", deleted)
	}
	after, err := database.InspectResetFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if after.SchemaDigest != before.SchemaDigest || after.Version != before.Version {
		t.Fatal("record reset changed the database schema")
	}
	for name, count := range after.Counts {
		if count == nil || *count != 0 {
			t.Fatalf("domain %s did not verify empty: %v", name, count)
		}
	}
	fixture.AssertPreserved(t)

	if _, err := database.ResetAppRecords(t.Context(), path); err != nil {
		t.Fatalf("idempotent reset: %v", err)
	}
}
