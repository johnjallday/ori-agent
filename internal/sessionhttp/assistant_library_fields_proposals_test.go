package sessionhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// bindHTTPManager binds a Home Manager ("Guide") the way the actions test
// does, and returns a proposal store with the handler's provider evidence.
func bindHTTPManager(t *testing.T, handler *Handler, store *workspace.InMemoryStore, home *workspace.Workspace) (*projectlibrary.Store, projectlibrary.ManagerAuthority) {
	t.Helper()
	if err := store.Update(home.ID, func(ws *workspace.Workspace) error {
		ws.AgentInstances = append(ws.AgentInstances, workspace.AgentInstance{ID: "http-manager", Name: "Guide", RoleID: "guide"})
		state := ws.GetAssistantProgramState()
		state.HomeBindings = workspace.AssistantRoleBindingSet{StateRevision: 1, Bindings: []workspace.AssistantRoleBinding{{
			RoleID: "guide", AgentInstanceID: "http-manager", AgentName: "Guide",
		}}}
		ws.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveWorkspaceAgent(home.ID, "Guide", &agent.Agent{}); err != nil {
		t.Fatal(err)
	}
	return projectlibrary.NewStore(store).WithProviderEvidence(handler.assistantLibraryProviderEvidence),
		projectlibrary.ManagerAuthority{HomeID: home.ID, AgentInstanceID: "http-manager", AgentName: "Guide"}
}

func linkedEntry(t *testing.T, store *workspace.InMemoryStore, scope projectlibrary.Scope, projectID string) projectlibrary.Entry {
	t.Helper()
	doc, err := projectlibrary.NewStore(store).Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range doc.Entries {
		if entry.Link != nil && entry.Link.WorkspaceID == projectID {
			return entry
		}
	}
	t.Fatal("no linked entry")
	return projectlibrary.Entry{}
}

func TestFieldsProposalHTTP_CardSuggestionReviewCommitAndList(t *testing.T) {
	handler, store, station, project, scope, _ := songCardHTTPFixture(t)
	initializeSongCardLibrary(t, store, scope)
	pinProjectCardPackage(t, handler, store, station, musicProjectCard())
	proposals, authority := bindHTTPManager(t, handler, store, station)
	entry := linkedEntry(t, store, scope, project.ID)
	next := "Send stems to the mixer"
	saved, _, err := proposals.ProposeFieldsAsManager(authority, entry.ID, entry.Fields.Revision,
		projectlibrary.ProposedFields{NextAction: &next, Extra: map[string][]string{"key": {"F minor"}}},
		"Untrusted <b>note</b>", "http-fields-1")
	if err != nil {
		t.Fatal(err)
	}

	// The song's own card lists the suggestion; nothing else of the Home.
	response := songCardRequest(t, handler, project.ID, "")
	var card struct {
		HomeID      string                              `json:"home_id"`
		Suggestions []projectlibrary.SongCardSuggestion `json:"suggestions"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &card); err != nil || card.HomeID != station.ID || len(card.Suggestions) != 1 {
		t.Fatalf("card suggestions: %s", response.Body.String())
	}
	suggestion := card.Suggestions[0]
	if suggestion.ID != saved.ID || suggestion.AgentName != "Guide" || suggestion.ProposerKind != "manager" ||
		suggestion.Fields.NextAction == nil || *suggestion.Fields.NextAction != next || suggestion.Fields.Extra["key"][0] != "F minor" {
		t.Fatalf("suggestion: %+v", suggestion)
	}

	// The shelf's list carries the kind, the patch and who proposed it.
	list := httptest.NewRecorder()
	handler.ListAssistantLibraryProposals(list, assistantProgramRequest(http.MethodGet, "/library/proposals", station.ID, ""))
	for _, want := range []string{`"kind":"fields"`, `"proposer_kind":"manager"`, `"proposed_fields"`, `"status":"ready"`} {
		if !strings.Contains(list.Body.String(), want) {
			t.Fatalf("list lacks %s: %s", want, list.Body.String())
		}
	}

	review := func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("proposalID", saved.ID)
		handler.ReviewAssistantLibraryProposal(w, r)
	}
	commit := func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("proposalID", saved.ID)
		handler.CommitAssistantLibraryProposal(w, r)
	}
	if code, _ := libraryAction(t, review, station.ID, "", `{"next_action":"Forged"}`); code != http.StatusBadRequest {
		t.Fatalf("the browser changed the saved suggestion: %d", code)
	}
	code, ownerReview := libraryAction(t, review, station.ID, "", `{}`)
	if code != http.StatusOK || ownerReview["after"].(map[string]any)["next_action"] != next {
		t.Fatalf("review: %d %+v", code, ownerReview)
	}
	token := ownerReview["token"].(string)
	if code, _ := libraryAction(t, commit, station.ID, "", `{"confirm":false,"review_token":"`+token+`","idempotency_key":"k"}`); code != http.StatusBadRequest {
		t.Fatalf("an unconfirmed Accept committed: %d", code)
	}
	code, committed := libraryAction(t, commit, station.ID, "", `{"confirm":true,"review_token":"`+token+`","idempotency_key":"http-fields-accept"}`)
	if code != http.StatusOK || committed["fields"].(map[string]any)["next_action"] != next {
		t.Fatalf("commit: %d %+v", code, committed)
	}
	after := linkedEntry(t, store, scope, project.ID)
	if after.Fields.NextAction != next || after.Fields.Extra["key"][0] != "F minor" || after.Fields.Source != "reviewed_user" {
		t.Fatalf("stored: %+v", after.Fields)
	}
	// Accepted: the card offers it no more, and the roster shows the new next action.
	if refreshed := songCardRequest(t, handler, project.ID, ""); strings.Contains(refreshed.Body.String(), `"suggestions"`) {
		t.Fatalf("an accepted suggestion stayed on the card: %s", refreshed.Body.String())
	}
	if roster := rosterRequest(t, handler, station.ID, ""); !strings.Contains(roster.Body.String(), next) {
		t.Fatalf("roster: %s", roster.Body.String())
	}
}

func TestFieldsProposalHTTP_DismissAndReadyCount(t *testing.T) {
	handler, store, station, project, scope, _ := songCardHTTPFixture(t)
	initializeSongCardLibrary(t, store, scope)
	pinProjectCardPackage(t, handler, store, station, nil)
	proposals, authority := bindHTTPManager(t, handler, store, station)
	entry := linkedEntry(t, store, scope, project.ID)
	next := "Comp vocals"
	saved, _, err := proposals.ProposeFieldsAsManager(authority, entry.ID, entry.Fields.Revision,
		projectlibrary.ProposedFields{NextAction: &next}, "", "http-fields-dismiss")
	if err != nil {
		t.Fatal(err)
	}
	summary, err := proposals.Summary(scope)
	if err != nil || summary.ReadyProposals != 1 {
		t.Fatalf("ready count: %d %v", summary.ReadyProposals, err)
	}
	dismiss := func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("proposalID", saved.ID)
		handler.DismissAssistantLibraryProposal(w, r)
	}
	if code, body := libraryAction(t, dismiss, station.ID, "", `{"confirm":true,"idempotency_key":"dismiss-fields"}`); code != http.StatusOK {
		t.Fatalf("dismiss: %d %+v", code, body)
	}
	if card := songCardRequest(t, handler, project.ID, ""); strings.Contains(card.Body.String(), `"suggestions"`) {
		t.Fatalf("a dismissed suggestion stayed on the card: %s", card.Body.String())
	}
	if summary, _ := proposals.Summary(scope); summary.ReadyProposals != 0 {
		t.Fatalf("ready count after dismiss: %d", summary.ReadyProposals)
	}
}
