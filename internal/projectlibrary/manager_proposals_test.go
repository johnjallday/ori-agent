package projectlibrary

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestManagerProposal_ProjectReviewNavigationIsInertBoundAndStalesOnEntryChange(t *testing.T) {
	a, scope, _, file, tree, _ := activationFixture(t)
	store := a.library
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
	authority := ManagerAuthority{HomeID: scope.HomeID, AgentInstanceID: "local-manager", AgentName: "Manager"}
	before := fileDigest(t, filepath.Join(tree.single, "Song.rpp"))
	doc, err := store.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	rev := sessionEntry(doc, "single").Revision
	nav, replay, err := store.ProposeProjectReview(authority, "single", rev, "Inspect setup, do not open the DAW", "navigate-song")
	if err != nil || replay || nav.Kind != "project_review" || nav.NextAction != "" {
		t.Fatalf("navigation suggestion: %+v %t %v", nav, replay, err)
	}
	if again, replay, err := store.ProposeProjectReview(authority, "single", rev, nav.Reason, "navigate-song"); err != nil || !replay || again.ID != nav.ID {
		t.Fatalf("exact navigation replay: %+v %t %v", again, replay, err)
	}
	if _, _, err := store.ProposeProjectReview(authority, "single", rev, "changed reason", "navigate-song"); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed navigation payload reused a request key: %v", err)
	}
	if _, _, err := store.ProposeProjectReview(authority, "single", rev+1, "", "stale-navigation"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale entry revision produced navigation: %v", err)
	}
	if _, _, err := store.ProposeNextAction(authority, "single", 0, "Mutate a note", "", "navigate-song"); !errors.Is(err, ErrConflict) {
		t.Fatalf("navigation request key was reused for an edit: %v", err)
	}
	page, err := store.ListManagerProposals(scope)
	if err != nil || page.Total != 1 || page.Rows[0].Status != "ready" || page.Rows[0].Name != "Single" {
		t.Fatalf("owner navigation shelf: %+v %v", page, err)
	}
	if _, err := store.ReviewProposedNextAction(scope, nav.ID, scope.OwnerUserID); !errors.Is(err, ErrConflict) {
		t.Fatalf("navigation suggestion produced a field review: %v", err)
	}
	if _, _, err := store.CommitProposedNextAction(scope, nav.ID, "fake", "fake-commit", scope.OwnerUserID); !errors.Is(err, ErrConflict) {
		t.Fatalf("navigation suggestion used an owner field commit: %v", err)
	}
	path, err := file.GetFolderPath(scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := workspace.NewFileStore(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	available := true
	store = NewStore(reopened).WithProviderEvidence(func(_ Scope, _ *workspace.Workspace) bool { return available })
	page, err = store.ListManagerProposals(scope)
	if err != nil || page.Rows[0].Status != "ready" {
		t.Fatalf("navigation lost on restart: %+v %v", page, err)
	}
	available = false
	page, err = store.ListManagerProposals(scope)
	if err != nil || page.Rows[0].Status != "unavailable" {
		t.Fatalf("lost provider left navigation active: %+v %v", page, err)
	}
	if _, _, err := store.ProposeProjectReview(authority, "single", rev, "", "disabled-navigation"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("lost provider accepted navigation: %v", err)
	}
	available = true
	doc, err = store.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = store.mutate(scope, doc.Revision, operation{key: "changed-entry", action: "fixture", digest: "fixture"}, func(current *Document) (string, error) {
		sessionEntry(*current, "single").Revision++
		return "single", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	page, err = store.ListManagerProposals(scope)
	if err != nil || page.Rows[0].Status != "stale" || fileDigest(t, filepath.Join(tree.single, "Song.rpp")) != before {
		t.Fatalf("stale navigation became authority or edited source: %+v %v", page, err)
	}
}

func TestManagerProposal_SessionGoalIsEditableDraftUntilSeparateOwnerReviewAndCommit(t *testing.T) {
	a, scope, _, file, tree, _ := activationFixture(t)
	store := a.library
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
	authority := ManagerAuthority{HomeID: scope.HomeID, AgentInstanceID: "local-manager", AgentName: "Manager"}
	before := fileDigest(t, filepath.Join(tree.single, "Song.rpp"))
	doc, err := store.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	rev := sessionEntry(doc, "single").Revision
	input := GoalInput{Goal: "Plan a short vocal practice", Outcome: "Write a take list", TimeMinutes: 25}
	proposal, replay, err := store.ProposeSessionGoal(authority, "single", rev, input, "User might want a short plan", "manager-goal")
	if err != nil || replay || proposal.Kind != "session_goal" || proposal.Goal == nil || proposal.Goal.Goal != input.Goal {
		t.Fatalf("inert Manager session draft: %+v %t %v", proposal, replay, err)
	}
	if _, err := store.ReviewProposedNextAction(scope, proposal.ID, scope.OwnerUserID); !errors.Is(err, ErrConflict) {
		t.Fatalf("goal suggestion produced a field review: %v", err)
	}
	if _, _, err := store.CommitProposedNextAction(scope, proposal.ID, "fake", "fake-goal-commit", scope.OwnerUserID); !errors.Is(err, ErrConflict) {
		t.Fatalf("goal suggestion became an agent field commit: %v", err)
	}
	if _, _, err := store.ProposeSessionGoal(authority, "single", rev, GoalInput{Goal: input.Goal, PlannedDate: "2026-09-27"}, "", "date"); !errors.Is(err, ErrConflict) {
		t.Fatalf("Manager chose the owner's planned date: %v", err)
	}
	if _, _, err := store.ProposeNextAction(authority, "single", 0, "Mutate Home", "", "manager-goal"); !errors.Is(err, ErrConflict) {
		t.Fatalf("goal request key crossed into a field suggestion: %v", err)
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
	if err != nil || page.Total != 1 || page.Rows[0].Status != "ready" {
		t.Fatalf("draft did not survive restart: %+v %v", page, err)
	}
	if again, replay, err := store.ProposeSessionGoal(authority, "single", rev, input, proposal.Reason, "manager-goal"); err != nil || !replay || again.ID != proposal.ID {
		t.Fatalf("exact suggestion retry duplicated the draft: %+v %t %v", again, replay, err)
	}
	if _, _, err := store.ProposeSessionGoal(authority, "single", rev, GoalInput{Goal: "Changed"}, proposal.Reason, "manager-goal"); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed suggestion reused a key: %v", err)
	}
	rows, err := store.ListSessions(scope, "single", 0, 0)
	if err != nil || rows.Total != 0 {
		t.Fatalf("Manager suggestion created a session without user review: %+v %v", rows, err)
	}
	goalReview, err := store.ReviewGoal(scope, "single", rev, input, scope.OwnerUserID)
	if err != nil {
		t.Fatal(err)
	}
	rows, err = store.ListSessions(scope, "single", 0, 0)
	if err != nil || rows.Total != 0 {
		t.Fatalf("owner review created a session without confirmation: %+v %v", rows, err)
	}
	accepted, replay, err := store.CommitGoal(scope, "single", goalReview.Token, "confirmed-user-goal", rev, input, scope.OwnerUserID)
	if err != nil || replay || accepted.Author != scope.OwnerUserID || accepted.Source != "reviewed_user" {
		t.Fatalf("canonical user goal was not accepted: %+v %t %v", accepted, replay, err)
	}
	page, err = store.ListManagerProposals(scope)
	if err != nil || page.Rows[0].Status != "stale" {
		t.Fatalf("accepted goal left duplicate suggestion actionable: %+v %v", page, err)
	}
	doc, err = store.Read(scope)
	if err != nil || len(doc.Sessions) != 1 || sessionEntry(doc, "single").Fields.NextAction != "" ||
		fileDigest(t, filepath.Join(tree.single, "Song.rpp")) != before {
		t.Fatalf("goal altered notes, files or session count: %+v %v", doc, err)
	}
}

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
