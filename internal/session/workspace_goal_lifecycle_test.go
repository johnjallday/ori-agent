package session

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/testutil/testdb"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestWorkspaceGoalLifecycleComposedStoreRestart(t *testing.T) {
	for _, state := range []string{"", workspace.GoalFirstRunBlocked, workspace.GoalFirstRunStarting, workspace.GoalFirstRunAccepted, workspace.GoalFirstRunFailed, workspace.GoalFirstRunCleared} {
		t.Run("state-"+state, func(t *testing.T) {
			db := testdb.Open(t)
			primary := NewHybridStoreWithDB(db, 50)
			defer cleanupTestOwner(t, primary)()
			root := t.TempDir()
			files, err := workspace.NewFileStore(root)
			if err != nil {
				t.Fatal(err)
			}
			composed := workspace.NewSyncStore(NewWorkspaceStoreAdapter(primary), files)
			ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Goal " + state})
			ws.Mission = "Understand the copied project"
			ws.GoalFirstRunPending = state == ""
			ws.GoalFirstRunState = state
			ws.TemplateProvenance = &workspace.TemplateProvenance{TemplateID: "renamed-code-copy", BehaviorProfile: "software_project"}
			if state == workspace.GoalFirstRunCleared {
				ws.Mission = ""
			}
			if err := composed.Save(ws); err != nil {
				t.Fatal(err)
			}
			if err := composed.Update(ws.ID, func(w *workspace.Workspace) error { w.Description = "unrelated edit"; return nil }); err != nil {
				t.Fatal(err)
			}
			// Fresh caches over both durable sources, not the object just saved.
			restartedPrimary := NewHybridStoreWithDB(db, 50)
			defer cleanupTestOwner(t, restartedPrimary)()
			restartedFiles, err := workspace.NewFileStore(root)
			if err != nil {
				t.Fatal(err)
			}
			restarted := workspace.NewSyncStore(NewWorkspaceStoreAdapter(restartedPrimary), restartedFiles)
			for _, read := range []func(string) (*workspace.Workspace, error){restarted.Get, restarted.GetFolderWorkspace} {
				got, err := read(ws.ID)
				if err != nil {
					t.Fatal(err)
				}
				if got.GoalFirstRunPending != ws.GoalFirstRunPending || got.GoalFirstRunState != state || got.Mission != ws.Mission {
					t.Fatalf("lost lifecycle: pending=%t state=%q goal=%q", got.GoalFirstRunPending, got.GoalFirstRunState, got.Mission)
				}
				if got.MissionEnabled || got.Cadence != nil {
					t.Fatal("lifecycle enabled execution")
				}
			}
			canonical, err := restarted.GetFolderWorkspace(ws.ID)
			if err != nil || !workspace.GoalRequiresReadConsent(canonical) {
				t.Fatalf("copy lost consent: %v", err)
			}
		})
	}
}

func TestWorkspaceGoalLifecycleLegacyAbsentField(t *testing.T) {
	adapter := &WorkspaceStoreAdapter{}
	got := adapter.toAgentWorkspace(&Workspace{ID: "legacy", MissionStateJSON: []byte(`{"mission":"Existing goal","goal_first_run_pending":true}`)})
	if got.GoalFirstRunState != "" || !got.GoalFirstRunPending {
		t.Fatal("legacy pending intent changed")
	}
	got = adapter.toAgentWorkspace(&Workspace{ID: "legacy", MissionStateJSON: []byte(`{"mission":""}`)})
	if got.Mission != "" || got.GoalFirstRunPending || got.GoalFirstRunState != "" {
		t.Fatal("legacy deliberate empty armed a run")
	}
}
