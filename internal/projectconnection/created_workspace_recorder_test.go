package projectconnection

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	agentstore "github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type createdRecorder struct{ ids []string }

func (r *createdRecorder) record(id string) { r.ids = append(r.ids, id) }

func TestCreateHomeRecordsOnlyTheHomeItCreated(t *testing.T) {
	service, _, _ := connectionService(t)
	recorded := &createdRecorder{}
	service.SetCreatedWorkspaceRecorder(recorded.record)
	scope := Scope{OwnerUserID: "owner-1", RunID: "run-home", Template: connectionTemplate(t)}

	home, err := service.CreateHome(scope, "My Studio")
	if err != nil || !home.Exists {
		t.Fatalf("home: %+v %v", home, err)
	}
	if !reflect.DeepEqual(recorded.ids, []string{home.HomeID}) {
		t.Fatalf("recorded = %v, want the new Home %q", recorded.ids, home.HomeID)
	}

	// A retry reuses the canonical Home; it was not created again.
	if _, err := service.CreateHome(scope, "My Studio"); err != nil {
		t.Fatal(err)
	}
	if len(recorded.ids) != 1 {
		t.Fatalf("reuse recorded the Home again: %v", recorded.ids)
	}
}

func TestCreateHomeDoesNotRecordAHomeItReused(t *testing.T) {
	service, store, _ := connectionService(t)
	scope := Scope{OwnerUserID: "owner-1", RunID: "run-reuse", Template: connectionTemplate(t)}
	// Another path made the Home, and this service has no business claiming it.
	home, err := service.CreateHome(scope, "Existing Studio")
	if err != nil {
		t.Fatal(err)
	}

	later := NewService(store, nil)
	recorded := &createdRecorder{}
	later.SetCreatedWorkspaceRecorder(recorded.record)
	again, err := later.CreateHome(scope, "Existing Studio")
	if err != nil || again.HomeID != home.HomeID {
		t.Fatalf("reuse: %+v %v", again, err)
	}
	if len(recorded.ids) != 0 {
		t.Fatalf("a reused Home was recorded as created: %v", recorded.ids)
	}
}

func TestCommitRecordsTheHomeAndProjectItCreated(t *testing.T) {
	service, _, _ := connectionService(t)
	recorded := &createdRecorder{}
	service.SetCreatedWorkspaceRecorder(recorded.record)
	scope := Scope{OwnerUserID: "owner-1", RunID: "run-commit", Template: connectionTemplate(t)}
	request := Request{ModeID: projecttemplates.ProjectConnectionNewProject, WorkspaceName: "New Song", ProjectName: "First Idea"}
	preview, err := service.Preview(context.Background(), scope, request)
	if err != nil || !preview.Projection.HomeWillBeCreated {
		t.Fatalf("preview: %+v %v", preview.Projection, err)
	}

	result, err := service.Commit(context.Background(), scope, request, preview.InputDigest, preview.OwnerDigest)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(recorded.ids, []string{result.HomeWorkspaceID, result.ProjectWorkspaceID}) {
		t.Fatalf("recorded = %v, want Home %q then project %q", recorded.ids, result.HomeWorkspaceID, result.ProjectWorkspaceID)
	}

	// An idempotent replay reuses both. The Home is not re-recorded; the
	// project carries this run's marker, so recording it again is harmless
	// (the allowlist dedupes) and covers an attempt that stopped early.
	recorded.ids = nil
	again, err := service.Commit(context.Background(), scope, request, preview.InputDigest, preview.OwnerDigest)
	if err != nil || again != result {
		t.Fatalf("replay = %+v %v", again, err)
	}
	if !reflect.DeepEqual(recorded.ids, []string{result.ProjectWorkspaceID}) {
		t.Fatalf("replay recorded = %v, want only the project", recorded.ids)
	}
}

func TestCommitIntoAnExistingHomeRecordsOnlyTheProject(t *testing.T) {
	service, _, _ := connectionService(t)
	scope := Scope{OwnerUserID: "owner-1", RunID: "run-existing-home", Template: connectionTemplate(t)}
	home, err := service.CreateHome(scope, "Prepared Studio")
	if err != nil {
		t.Fatal(err)
	}
	recorded := &createdRecorder{}
	service.SetCreatedWorkspaceRecorder(recorded.record)
	request := Request{ModeID: projecttemplates.ProjectConnectionNewProject, WorkspaceName: "Second Song", ProjectName: "Second Idea"}
	preview, err := service.Preview(context.Background(), scope, request)
	if err != nil || preview.Projection.HomeWillBeCreated {
		t.Fatalf("preview: %+v %v", preview.Projection, err)
	}
	result, err := service.Commit(context.Background(), scope, request, preview.InputDigest, preview.OwnerDigest)
	if err != nil || result.HomeWorkspaceID != home.HomeID {
		t.Fatalf("commit: %+v %v", result, err)
	}
	if !reflect.DeepEqual(recorded.ids, []string{result.ProjectWorkspaceID}) {
		t.Fatalf("recorded = %v, want only the project", recorded.ids)
	}
}

func TestFailedCommitRecordsNothing(t *testing.T) {
	service, _, selections := connectionService(t)
	recorded := &createdRecorder{}
	service.SetCreatedWorkspaceRecorder(recorded.record)
	external := t.TempDir()
	for _, name := range []string{"A.rpp", "B.RPP"} {
		if err := os.WriteFile(filepath.Join(external, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	token, _ := selections.Issue(external)
	scope := Scope{OwnerUserID: "owner-1", RunID: "run-fail", Template: connectionTemplate(t)}
	request := Request{ModeID: projecttemplates.ProjectConnectionExistingProject, SelectionToken: token, WorkspaceName: "Ambiguous"}
	preview, err := service.Preview(context.Background(), scope, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Commit(context.Background(), scope, request, preview.InputDigest, preview.OwnerDigest); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ambiguous commit error = %v", err)
	}
	if len(recorded.ids) != 0 {
		t.Fatalf("a failed commit recorded %v", recorded.ids)
	}
}

// The bug this recorder exists for: a Home created by guided setup and then
// staffed kept its coordinator only until the next restart, because the
// startup wipe removes agents that belong solely to workspaces the data
// directory has no record of. Recording the Home keeps the agent; the control
// run without a recorder shows the agent is wiped.
func TestGuidedHomeCoordinatorSurvivesTheStartupAgentWipe(t *testing.T) {
	for _, tc := range []struct {
		name     string
		record   bool
		wantKept bool
	}{
		{name: "recorded Home keeps its coordinator", record: true, wantKept: true},
		{name: "unrecorded Home loses its coordinator", record: false, wantKept: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, store, _ := connectionService(t)
			allowlist := workspace.NewAllowlist(filepath.Join(t.TempDir(), workspace.DefaultAllowlistFilename))
			if tc.record {
				service.SetCreatedWorkspaceRecorder(func(id string) {
					if err := allowlist.Add(id); err != nil {
						t.Fatalf("allowlist add: %v", err)
					}
				})
			}
			scope := Scope{OwnerUserID: "owner-1", RunID: "run-staffed", Template: connectionTemplate(t)}
			home, err := service.CreateHome(scope, "Staffed Studio")
			if err != nil {
				t.Fatal(err)
			}

			// Staff a coordinator the way the setup journey does: a global
			// agent definition mirrored into a snapshot on the Home.
			agents, err := agentstore.NewFileStore(filepath.Join(t.TempDir(), "agents.json"), types.Settings{})
			if err != nil {
				t.Fatal(err)
			}
			coordinator := &agent.Agent{}
			if err := agents.SetAgent("Studio Coordinator", coordinator); err != nil {
				t.Fatal(err)
			}
			if err := store.SaveWorkspaceAgent(home.HomeID, "Studio Coordinator", coordinator); err != nil {
				t.Fatal(err)
			}
			if err := store.Update(home.HomeID, func(current *workspace.Workspace) error {
				current.AgentInstances = workspace.AgentInstancesFromNames("Studio Coordinator")
				return nil
			}); err != nil {
				t.Fatal(err)
			}

			workspace.WipeNonAllowlistedAgentSnapshots(store, agents, allowlist)

			if _, kept := agents.GetAgent("Studio Coordinator"); kept != tc.wantKept {
				t.Fatalf("coordinator kept = %v, want %v", kept, tc.wantKept)
			}
		})
	}
}
