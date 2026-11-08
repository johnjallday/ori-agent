package session

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/testutil/testdb"
)

func TestWorkspaceAgentReferencesIncludeRestorableAndBindingOnlyMetadata(t *testing.T) {
	for _, shape := range []string{"attachment", "entry", "home", "project", "consent", "pending_consent"} {
		t.Run(shape, func(t *testing.T) {
			db := testdb.Open(t)
			t.Cleanup(func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			})
			s := NewSQLiteStore(db)
			ctx := context.Background()
			ws := &Workspace{ID: "retained", Name: "Retained", FolderSlug: "retained", Status: WorkspaceStatusTrashed}
			if err := s.CreateWorkspace(ctx, ws); err != nil {
				t.Fatal(err)
			}
			instances, shared, program := `[]`, `{}`, `{}`
			switch shape {
			case "attachment":
				instances = `[{"name":" SCOUT "}]`
			case "entry":
				shared = `{"entry_agent_name":"Scout"}`
			case "home":
				program = `{"state":{"home_bindings":{"bindings":[{"agent_name":"Scout"}]}}}`
			case "project":
				program = `{"link":{"project_bindings":{"bindings":[{"agent_name":"Scout"}]}}}`
			case "consent":
				program = `{"state":{"project_staffing_consent":{"roles":[{"agent_name":"Scout"}]}}}`
			case "pending_consent":
				program = `{"state":{"project_staffing_consent":{"roles":[{"pending_name":"Scout"}]}}}`
			}
			// Deliberately unreadable heavy state proves this query never
			// hydrates chat/tickets just to check an attachment reference.
			if _, err := db.Exec(`UPDATE workspaces SET agent_instances=?, shared_data=?, assistant_program_json=?, messages_json='not-json', tasks_json='not-json' WHERE id='retained'`, instances, shared, program); err != nil {
				t.Fatal(err)
			}
			found, err := s.HasWorkspaceAgentReference(ctx, "scout", "removed")
			if err != nil || !found {
				t.Fatalf("lost %s reference: found=%v err=%v", shape, found, err)
			}
			found, err = s.HasWorkspaceAgentReference(ctx, "scout", "retained")
			if err != nil || found {
				t.Fatalf("excluded workspace still matched: %v %v", found, err)
			}
		})
	}
}

func TestWorkspaceAgentReferencesMalformedIsUnknown(t *testing.T) {
	db := testdb.Open(t)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	s := NewSQLiteStore(db)
	ctx := context.Background()
	if err := s.CreateWorkspace(ctx, &Workspace{ID: "broken", Name: "Broken", FolderSlug: "broken"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE workspaces SET agent_instances='not-json' WHERE id='broken'`); err != nil {
		t.Fatal(err)
	}
	if found, err := s.HasWorkspaceAgentReference(ctx, "Scout", "removed"); found || err == nil {
		t.Fatalf("malformed references treated as empty: %v %v", found, err)
	}
}
