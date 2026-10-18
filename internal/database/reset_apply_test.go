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
		`INSERT INTO assistant_arrangements(id,session_id,message_id,user_id,workspace_id,agent_name,state_version,context_revision,folder_revision,revision,state,record_json)
			SELECT 'reset-arrangement',session_id,id,'local','reset-hq','Atlas',1,'context',id,1,'draft','{"version":1,"id":"reset-arrangement","revision":1,"state":"draft"}' FROM messages LIMIT 1`,
		`INSERT INTO assistant_conversation_checkpoints(session_id,owner_user_id,hq_workspace_id,profile_name,state_version,source_epoch,through_rowid,generation,recap_json,updated_at)
			SELECT id,'local','reset-hq','Atlas',1,0,1,1,'{"version":1,"items":[]}',CURRENT_TIMESTAMP FROM sessions LIMIT 1`,
		`INSERT INTO assistant_operations(id,contract_version,kind,status,revision,user_id,assistant_id,state_version,hq_workspace_id,profile_name,
			conversation_id,workspace_id,context_json,commands_json,receipts_json,created_at_ms,updated_at_ms,expires_at_ms)
			SELECT 'reset-operation',1,'navigation','requested',1,'local','reset-assistant',1,'reset-hq','Atlas',id,'reset-workspace','{}','[]','[]',1000,1000,2000 FROM sessions LIMIT 1`,
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
		`INSERT INTO group_requirement_reviews(token, payload_json, expires_at)
			VALUES ('reset-review', '{}', '2026-09-13T00:00:00Z')`,
		`INSERT INTO group_requirement_operations(
			owner_user_id, operation_kind, idempotency_key, input_digest,
			review_digest, child_workspace_id, payload_json, updated_at
		) VALUES ('local', 'create_workspace', 'reset-operation', 'input',
			'review', 'child', '{}', '2026-09-13T00:00:00Z')`,
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
	for _, name := range []string{
		"sessions", "messages", "setup_journey_run", "sample_library_state", "agent_map_layouts",
		"group_requirement_reviews", "group_requirement_operations", "assistant_conversation_checkpoints",
		"assistant_operations", "assistant_arrangements",
	} {
		if before.Counts[name] == nil || *before.Counts[name] == 0 {
			t.Fatalf("fixture app-record domain %s is empty: %#v", name, before.Counts)
		}
	}

	deleted, err := database.ResetAppRecords(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"sessions", "messages", "setup_journey_run", "sample_library_state", "agent_map_layouts",
		"group_requirement_reviews", "group_requirement_operations", "assistant_conversation_checkpoints",
		"assistant_operations", "assistant_arrangements",
	} {
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
