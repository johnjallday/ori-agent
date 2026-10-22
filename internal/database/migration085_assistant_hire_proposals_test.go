package database

import (
	"path/filepath"
	"testing"
)

func TestMigration085AssistantHireProposalsIsIdempotentBoundedAndResetInventoried(t *testing.T) {
	db, err := Open(t.Context(), &Config{Path: filepath.Join(t.TempDir(), "proposals.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for range 2 {
		if err := db.migration085AssistantHireProposals(t.Context()); err != nil {
			t.Fatal(err)
		}
	}

	insert := `INSERT INTO assistant_hire_proposals (id, user_id, conversation_id, message_id, role, name, brief, workspace_name, blueprint, blueprint_label, open_roles, state, created_at, updated_at)
		VALUES (?, 'local', 'conv-1', 'msg-1', 'researcher', 'Ruth', 'Compare the three CRMs.', 'CRM Comparison', 'research-project', 'Research Project', 2, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`
	if _, err := db.ExecContext(t.Context(), insert, "proposal-1", "offered"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), insert, "proposal-2", "looking"); err == nil {
		t.Fatal("a state outside the proposal's states was stored")
	}

	inspection, err := InspectReset(t.Context(), db.DB)
	if err != nil || len(inspection.Problems) != 0 {
		t.Fatalf("unclassified proposal state: %v %v", err, inspection.Problems)
	}
	if count := inspection.Counts["assistant_hire_proposals"]; count == nil || *count != 1 {
		t.Fatalf("assistant_hire_proposals missing from the reset inventory: %v", count)
	}
}
