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
	db, err := database.Open(t.Context(), &database.Config{Path: path, WALMode: false})
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO setup_journey_run (
			id, run_kind, owner_user_id, relationship_id, specialist_slug, journey_id,
			declaration_schema_version, declaration_version, lifecycle_state,
			step_states_json, created_at, updated_at
		) VALUES ('reset-run', 'root', 'local', 'relationship', 'specialist', 'journey',
			1, 1, 'not_started', '[]', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		`INSERT INTO sample_library_state (
			home_workspace_id, schema_version, lifecycle, catalog_revision, updated_at
		) VALUES ('reset-home', 1, 'active', 0, CURRENT_TIMESTAMP)`,
		`INSERT INTO agent_map_layouts (
			user_id, schema_version, revision, snap_to_grid, created_at, updated_at
		) VALUES ('local', 1, 1, 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
	} {
		if _, err := db.ExecContext(t.Context(), statement); err != nil {
			_ = db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := database.InspectResetFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"sessions", "messages", "setup_journey_run", "sample_library_state", "agent_map_layouts"} {
		if before.Counts[name] == nil || *before.Counts[name] == 0 {
			t.Fatalf("fixture app-record domain %s is empty: %#v", name, before.Counts)
		}
	}

	deleted, err := database.ResetAppRecords(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"sessions", "messages", "setup_journey_run", "sample_library_state", "agent_map_layouts"} {
		if deleted[name] == 0 {
			t.Fatalf("deletion count for %s = 0: %#v", name, deleted)
		}
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
