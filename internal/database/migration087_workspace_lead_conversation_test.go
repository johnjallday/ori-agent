package database

import (
	"path/filepath"
	"testing"
)

func TestMigration087LeadConversationIsLocalResetState(t *testing.T) {
	db, err := Open(t.Context(), &Config{Path: filepath.Join(t.TempDir(), "welcome.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for range 2 {
		if err := db.migration087WorkspaceLeadConversation(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO workspaces(id,name,created_at,updated_at) VALUES('ws','Desk',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO workspace_lead_conversations(workspace_id,owner_user_id,agent_instance_id,agent_name) VALUES('ws','local','lead','Mira')`); err != nil {
		t.Fatal(err)
	}
	inspection, err := InspectReset(t.Context(), db.DB)
	if err != nil || len(inspection.Problems) != 0 {
		t.Fatalf("unclassified local state: %v %v", err, inspection.Problems)
	}
	if count := inspection.Counts["workspace_lead_conversations"]; count == nil || *count != 1 {
		t.Fatal("welcome receipt absent from reset inventory")
	}
}
