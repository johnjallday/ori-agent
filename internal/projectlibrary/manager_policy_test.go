package projectlibrary

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestManagerPolicy_RequiresExactLocalPrimaryRoleAndLiveProvider(t *testing.T) {
	file, scope := libraryHome(t)
	available := true
	store := NewStore(file).WithProviderEvidence(func(_ Scope, _ *workspace.Workspace) bool { return available })
	initializeLibrary(t, store, scope)
	if err := file.Update(scope.HomeID, func(home *workspace.Workspace) error {
		home.AgentInstances = []workspace.AgentInstance{{ID: "manager-instance", Name: "Manager", RoleID: "portfolio_manager"},
			{ID: "sample-instance", Name: "Sample", RoleID: "sample_library_manager"},
			{ID: "unbound-instance", Name: "Unbound", RoleID: ""}}
		state := home.GetAssistantProgramState()
		state.Declaration = &workspace.AssistantProgramDeclaration{Roles: []workspace.AssistantProgramRoleSpec{
			{ID: "portfolio_manager", Scope: workspace.AssistantRoleScopeHome, Required: true, Primary: true},
			{ID: "sample_library_manager", Scope: workspace.AssistantRoleScopeHome},
		}}
		state.HomeBindings = workspace.AssistantRoleBindingSet{StateRevision: 1, Bindings: []workspace.AssistantRoleBinding{
			{RoleID: "portfolio_manager", AgentInstanceID: "manager-instance", AgentName: "Manager"},
			{RoleID: "sample_library_manager", AgentInstanceID: "sample-instance", AgentName: "Sample"},
		}}
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Manager", "Sample"} {
		if err := file.SaveWorkspaceAgent(scope.HomeID, name, &agent.Agent{}); err != nil {
			t.Fatal(err)
		}
	}
	child := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Linked song"})
	child.OwnerUserID = scope.OwnerUserID
	child.SetAssistantProjectLink(&workspace.AssistantProjectLink{ID: workspace.AssistantProjectLinkID(scope.HomeID, child.ID),
		SchemaVersion: 1, StationWorkspaceID: scope.HomeID, StateRevision: 1,
		Key: workspace.AssistantProgramKey{OwnerUserID: scope.OwnerUserID, PluginID: scope.ProviderID, ProgramID: scope.ProgramID}})
	if err := file.Save(child); err != nil {
		t.Fatal(err)
	}
	if err := file.Update(scope.HomeID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.LinkedProjectIDs = []string{child.ID}
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	doc, err := store.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = store.mutate(scope, doc.Revision, operation{key: "manager-fixture-entry", action: "fixture", digest: "fixture"}, func(current *Document) (string, error) {
		current.Entries = append(current.Entries, Entry{ID: "song", Revision: 1,
			Link:   &ExactLink{WorkspaceID: child.ID, LinkID: workspace.AssistantProjectLinkID(scope.HomeID, child.ID), Revision: 1},
			Fields: Fields{DisplayName: "Private user note", Purpose: "Untrusted lyrics"}})
		return "song", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	doc, err = store.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = store.mutate(scope, doc.Revision, operation{key: "manager-fixture-sessions", action: "fixture", digest: "fixture"}, func(current *Document) (string, error) {
		at := time.Now().UTC().Add(-time.Hour)
		for i := 0; i < 4; i++ {
			current.Sessions = append(current.Sessions, StudioSession{ID: fmt.Sprintf("session-%d", i),
				EntryID: "song", Revision: 1, Goal: fmt.Sprintf("User goal %d", i),
				Decisions: []string{"Private decision"}, Blockers: []string{"Private blocker"},
				Author: scope.OwnerUserID, State: "accepted", CreatedAt: at, AcceptedAt: &at,
				UpdatedAt: at.Add(time.Duration(i) * time.Minute)})
		}
		return "song", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Legacy confirmed handoff receipts stay in the Home after explicit
	// library initialization. They do not transfer the child Ticket body.
	if err := file.Update(scope.HomeID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		for i := 0; i < 4; i++ {
			state.Portfolio.HandoffOperationReceipts = append(state.Portfolio.HandoffOperationReceipts,
				workspace.AssistantPortfolioHandoffOperationReceipt{LinkID: child.GetAssistantProjectLink().ID,
					ProjectWorkspaceID: child.ID, TicketID: fmt.Sprintf("home-ticket-%d", i), TicketNumber: int64(i + 1),
					IdempotencyKey: "private handoff key", InputDigest: "private child text",
					RecordedAt: time.Now().UTC().Add(time.Duration(i) * time.Minute)})
		}
		state.Portfolio.HandoffOperationReceipts = append(state.Portfolio.HandoffOperationReceipts,
			workspace.AssistantPortfolioHandoffOperationReceipt{LinkID: "foreign-link", ProjectWorkspaceID: child.ID,
				TicketID: "foreign-ticket", RecordedAt: time.Now().UTC()})
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	manager := ManagerAuthority{HomeID: scope.HomeID, AgentInstanceID: "manager-instance", AgentName: "Manager"}
	// Historical owner-accepted citations stay on the Home, but Manager
	// session summaries must not bypass the exact-link receipt-only tool.
	currentHome, err := file.Get(scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	priorReceipt := currentHome.GetAssistantProgramState().Portfolio.HandoffOperationReceipts[3]
	doc, err = store.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = store.mutate(scope, doc.Revision, operation{key: "fixture-historical-citation", action: "fixture", digest: "fixture"}, func(current *Document) (string, error) {
		current.Sessions[3].Recap = "Owner accepted this historical citation"
		current.Sessions[3].SharedFrom = &ExactLink{WorkspaceID: child.ID, LinkID: priorReceipt.LinkID, Revision: 1}
		current.Sessions[3].Handoff = &ManagerHandoffReceipt{TicketID: priorReceipt.TicketID,
			TicketNumber: priorReceipt.TicketNumber, RecordedAt: priorReceipt.RecordedAt}
		return "song", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	handoffs, err := store.HandoffsForManager(manager, "song")
	if err != nil || handoffs.Total != 4 || len(handoffs.Rows) != 3 || handoffs.Rows[0].TicketID != "home-ticket-3" {
		t.Fatalf("bounded Home-only handoff receipt read: %+v %v", handoffs, err)
	}
	if _, err := store.HandoffsForManager(manager, child.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("child ID read a Home handoff receipt: %v", err)
	}
	page, err := store.SearchForManager(manager, Search{PageSize: 100, Sort: "name"})
	if err != nil || len(page.Rows) != 1 || page.Rows[0].Name != "Private user note" {
		t.Fatalf("valid local Manager read: %+v %v", page, err)
	}
	detail, err := store.DetailForManager(manager, "song")
	if err != nil || detail.Row.ID != "song" || len(detail.Sources) != 0 || detail.Fields.Purpose != "Untrusted lyrics" {
		t.Fatalf("bounded Home-only detail: %+v %v", detail, err)
	}
	sessions, err := store.SessionsForManager(manager, "song")
	if err != nil || sessions.Total != 4 || len(sessions.Rows) != 3 || sessions.Rows[0].Goal != "User goal 3" || sessions.Rows[0].Handoff != nil {
		t.Fatalf("Manager read more or fewer than the latest three Home notes, or leaked a Ticket ID: %+v %v", sessions, err)
	}
	if _, err := store.SessionsForManager(manager, child.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("child workspace ID obtained Home sessions: %v", err)
	}
	proposal, replay, err := store.ProposeNextAction(manager, "song", 0, "Review verse two", "User's note suggests a chorus", "suggestion-1")
	if err != nil || replay || proposal.ID == "" {
		t.Fatalf("locally bound Manager failed to save inert suggestion: %+v replay=%v %v", proposal, replay, err)
	}
	doc, err = store.Read(scope)
	if err != nil || sessionEntry(doc, "song").Fields.NextAction != "" || len(doc.Proposals) != 1 {
		t.Fatalf("suggestion edited a Home field without owner review: %+v %v", doc, err)
	}
	again, replay, err := store.ProposeNextAction(manager, "song", 0, "Review verse two", "User's note suggests a chorus", "suggestion-1")
	if err != nil || !replay || again.ID != proposal.ID {
		t.Fatalf("exact suggestion replay duplicated proposal: %+v %v %v", again, replay, err)
	}
	if _, _, err := store.ProposeNextAction(manager, "song", 0, "Different action", "", "suggestion-1"); !errors.Is(err, ErrConflict) {
		t.Fatalf("colliding suggestion key accepted changed payload: %v", err)
	}
	listed, err := store.ListManagerProposals(scope)
	if err != nil || listed.Total != 1 || listed.Rows[0].Status != "ready" {
		t.Fatalf("owner cannot review bounded suggestions: %+v %v", listed, err)
	}
	if _, err := store.ReviewProposedNextAction(scope, "foreign", scope.OwnerUserID); !errors.Is(err, ErrConflict) {
		t.Fatalf("foreign proposal got a review: %v", err)
	}
	// An unrelated Home revision must not make a current field proposal
	// appear stale. Its exact entry field revision remains the authority.
	unrelated, err := store.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.mutate(scope, unrelated.Revision, operation{key: "unrelated-home-revision", action: "fixture", digest: "fixture"},
		func(*Document) (string, error) { return "song", nil }); err != nil {
		t.Fatal(err)
	}
	fieldReview, err := store.ReviewProposedNextAction(scope, proposal.ID, scope.OwnerUserID)
	if err != nil || fieldReview.Before.NextAction != "" || fieldReview.After.NextAction != proposal.NextAction {
		t.Fatalf("owner review did not use canonical fields: %+v %v", fieldReview, err)
	}
	if _, _, err := store.CommitProposedNextAction(scope, proposal.ID, "forged", "commit-suggestion", scope.OwnerUserID); !errors.Is(err, ErrConflict) {
		t.Fatalf("forged field review committed suggestion: %v", err)
	}
	entry, replay, err := store.CommitProposedNextAction(scope, proposal.ID, fieldReview.Token, "commit-suggestion", scope.OwnerUserID)
	if err != nil || replay || entry.Fields.NextAction != proposal.NextAction || entry.Fields.Author != scope.OwnerUserID {
		t.Fatalf("user confirmation did not write exact reviewed Home note: %+v %v %v", entry, replay, err)
	}
	entry, replay, err = store.CommitProposedNextAction(scope, proposal.ID, fieldReview.Token, "commit-suggestion", scope.OwnerUserID)
	if err != nil || !replay || entry.Fields.NextAction != proposal.NextAction {
		t.Fatalf("lost owner reply failed canonical replay: %+v %v %v", entry, replay, err)
	}
	listed, err = store.ListManagerProposals(scope)
	if err != nil || listed.Rows[0].Status != "stale" {
		t.Fatalf("confirmed suggestion still actionable: %+v %v", listed, err)
	}
	for name, denied := range map[string]ManagerAuthority{
		"global fallback without instance": {HomeID: scope.HomeID, AgentName: "Manager"},
		"another Home":                     {HomeID: "other-home", AgentInstanceID: "manager-instance", AgentName: "Manager"},
		"wrong instance":                   {HomeID: scope.HomeID, AgentInstanceID: "sample-instance", AgentName: "Manager"},
		"optional Sample":                  {HomeID: scope.HomeID, AgentInstanceID: "sample-instance", AgentName: "Sample"},
		"unbound local":                    {HomeID: scope.HomeID, AgentInstanceID: "unbound-instance", AgentName: "Unbound"},
	} {
		if _, err := store.SearchForManager(denied, Search{}); !errors.Is(err, ErrUnavailable) {
			t.Errorf("%s acquired Manager access: %v", name, err)
		}
		if _, err := store.SessionsForManager(denied, "song"); !errors.Is(err, ErrUnavailable) {
			t.Errorf("%s acquired session history: %v", name, err)
		}
		if _, err := store.HandoffsForManager(denied, "song"); !errors.Is(err, ErrUnavailable) {
			t.Errorf("%s acquired Home handoff receipts: %v", name, err)
		}
		if _, _, err := store.ProposeNextAction(denied, "song", entry.Fields.Revision, "Unsafe", "", "denied-"+name); !errors.Is(err, ErrUnavailable) {
			t.Errorf("%s stored a Manager proposal: %v", name, err)
		}
	}
	pending, _, err := store.ProposeNextAction(manager, "song", entry.Fields.Revision,
		"Later unapproved suggestion", "", "suggestion-before-binding-removal")
	if err != nil {
		t.Fatal(err)
	}
	pendingReview, err := store.ReviewProposedNextAction(scope, pending.ID, scope.OwnerUserID)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Update(scope.HomeID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.HomeBindings.StateRevision++
		state.HomeBindings.Bindings = state.HomeBindings.Bindings[1:] // remove only Manager binding
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SearchForManager(manager, Search{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("removed role binding retained access: %v", err)
	}
	if _, err := store.HandoffsForManager(manager, "song"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("removed role binding exposed handoff receipts: %v", err)
	}
	if _, _, err := store.CommitProposedNextAction(scope, pending.ID, pendingReview.Token,
		"removed-binding-commit", scope.OwnerUserID); !errors.Is(err, ErrConflict) {
		t.Fatalf("removed binding allowed previously reviewed proposal to commit: %v", err)
	}
	if err := file.Update(scope.HomeID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.HomeBindings.StateRevision++
		state.HomeBindings.Bindings = append(state.HomeBindings.Bindings, workspace.AssistantRoleBinding{
			RoleID: "portfolio_manager", AgentInstanceID: "manager-instance", AgentName: "Manager"})
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReviewProposedNextAction(scope, pending.ID, scope.OwnerUserID); !errors.Is(err, ErrConflict) {
		t.Fatalf("restored but changed binding inherited old suggestion: %v", err)
	}
	available = false
	listed, err = store.ListManagerProposals(scope)
	if err != nil || listed.Rows[0].Status != "unavailable" {
		t.Fatalf("disabled provider exposed an actionable Manager suggestion: %+v %v", listed, err)
	}
	if _, _, err := store.ProposeNextAction(manager, "song", entry.Fields.Revision,
		"Unavailable", "", "after-provider-loss"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("provider loss allowed Manager to save a suggestion: %v", err)
	}
	if _, err := store.ReviewProposedNextAction(scope, pending.ID, scope.OwnerUserID); !errors.Is(err, ErrConflict) {
		t.Fatalf("provider loss allowed owner to review a stale Manager suggestion: %v", err)
	}
	if _, err := store.SearchForManager(manager, Search{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("provider loss retained tool read: %v", err)
	}
	if _, err := store.HandoffsForManager(manager, "song"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("provider loss exposed handoff receipts: %v", err)
	}
	available = true
	if _, err := store.DetailForManager(manager, strings.Repeat("x", 161)); !errors.Is(err, ErrConflict) {
		t.Fatalf("malformed entry ID acquired details: %v", err)
	}
	// Disconnect *after* the first exact-link read and before the final
	// post-authorization check. No stale child receipt may escape this path.
	race := &disconnectOnSecondReceiptLinkRead{Store: file, childID: child.ID}
	race.disconnect = func() error {
		return file.Update(child.ID, func(project *workspace.Workspace) error {
			link := project.GetAssistantProjectLink()
			link.StationWorkspaceID = "other-home"
			project.SetAssistantProjectLink(link)
			return nil
		})
	}
	if _, err := NewStore(race).WithProviderEvidence(store.providerEvidence).HandoffsForManager(manager, "song"); !errors.Is(err, ErrConflict) || race.reads != 2 {
		t.Fatalf("disconnect after first receipt link check leaked a child ID: reads=%d err=%v", race.reads, err)
	}
	if _, err := store.HandoffsForManager(manager, "song"); !errors.Is(err, ErrConflict) {
		t.Fatalf("disconnected child exposed historical handoff receipts: %v", err)
	}
}

type disconnectOnSecondReceiptLinkRead struct {
	workspace.Store
	childID    string
	reads      int
	disconnect func() error
}

func (s *disconnectOnSecondReceiptLinkRead) Get(id string) (*workspace.Workspace, error) {
	if id == s.childID {
		s.reads++
		if s.reads == 2 {
			if err := s.disconnect(); err != nil {
				return nil, err
			}
		}
	}
	return s.Store.Get(id)
}
