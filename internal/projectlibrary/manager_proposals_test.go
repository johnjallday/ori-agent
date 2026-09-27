package projectlibrary

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestManagerProposal_RestartExpiryPruningAndLostRetryNeverEditNotes(t *testing.T) {
	file, scope := libraryHome(t)
	store := NewStore(file).WithProviderEvidence(func(_ Scope, _ *workspace.Workspace) bool { return true })
	initializeLibrary(t, store, scope)
	if err := file.Update(scope.HomeID, func(home *workspace.Workspace) error {
		home.AgentInstances = []workspace.AgentInstance{{ID: "local-manager", Name: "Manager", RoleID: "manager"}}
		state := home.GetAssistantProgramState()
		state.Declaration = &workspace.AssistantProgramDeclaration{Roles: []workspace.AssistantProgramRoleSpec{{
			ID: "manager", Scope: workspace.AssistantRoleScopeHome, Required: true, Primary: true,
		}}}
		state.HomeBindings = workspace.AssistantRoleBindingSet{StateRevision: 1, Bindings: []workspace.AssistantRoleBinding{{
			RoleID: "manager", AgentInstanceID: "local-manager", AgentName: "Manager",
		}}}
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := file.SaveWorkspaceAgent(scope.HomeID, "Manager", &agent.Agent{}); err != nil {
		t.Fatal(err)
	}
	doc, err := store.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = store.mutate(scope, doc.Revision, operation{key: "test-entry", action: "fixture", digest: "fixture"},
		func(current *Document) (string, error) {
			current.Entries = append(current.Entries, Entry{ID: "entry", Revision: 1, Fields: Fields{DisplayName: "Draft"},
				Link: &ExactLink{WorkspaceID: "child", LinkID: "link", Revision: 1}})
			return "entry", nil
		})
	if err != nil {
		t.Fatal(err)
	}
	authority := ManagerAuthority{HomeID: scope.HomeID, AgentInstanceID: "local-manager", AgentName: "Manager"}
	first, _, err := store.ProposeNextAction(authority, "entry", 0, "Make a scratch mix", "Only a suggestion", "first-proposal")
	if err != nil {
		t.Fatal(err)
	}
	path, err := file.GetFolderPath(scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := workspace.NewFileStore(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	store = NewStore(reopened).WithProviderEvidence(func(_ Scope, _ *workspace.Workspace) bool { return true })
	page, err := store.ListManagerProposals(scope)
	if err != nil || page.Total != 1 || page.Rows[0].Status != "ready" || page.Rows[0].Proposal.ID != first.ID {
		t.Fatalf("proposal was not durable across restart: %+v %v", page, err)
	}
	store.now = func() time.Time { return first.ExpiresAt.Add(time.Second) }
	page, err = store.ListManagerProposals(scope)
	if err != nil || page.Rows[0].Status != "expired" {
		t.Fatalf("expired proposal became actionable: %+v %v", page, err)
	}
	if _, err := store.ReviewProposedNextAction(scope, first.ID, scope.OwnerUserID); !errors.Is(err, ErrConflict) {
		t.Fatalf("expired suggestion got an owner review: %v", err)
	}
	if _, _, err := store.ProposeNextAction(authority, "entry", 0, "Make a scratch mix", "Only a suggestion", "first-proposal"); !errors.Is(err, ErrConflict) {
		t.Fatalf("expired request key revived a suggestion: %v", err)
	}
	second, _, err := store.ProposeNextAction(authority, "entry", 0, "Plan a vocal", "", "second-proposal")
	if err != nil || second.ID == first.ID {
		t.Fatalf("failed to prune old suggestion on the next append: %+v %v", second, err)
	}
	if _, _, err := store.ProposeNextAction(authority, "entry", 0, "Make a scratch mix", "Only a suggestion", "first-proposal"); !errors.Is(err, ErrConflict) {
		t.Fatalf("pruned request key recreated an expired suggestion: %v", err)
	}
	doc, err = store.Read(scope)
	if err != nil || len(doc.Proposals) != 1 || doc.Proposals[0].ID != second.ID || sessionEntry(doc, "entry").Fields.NextAction != "" {
		t.Fatalf("proposal expiry/pruning changed user-authored Home state: %+v %v", doc, err)
	}
}
