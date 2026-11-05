package orchestrationhttp

import (
	"net/http"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Characterization, not a new authority promise: the legacy transition route
// stores caller labels and does not require an actual result. Learning must use
// its own canonical-result decision receipt, never consume this as human proof.
func TestTicketLearningCharacterization_TransitionLabelsAreNotResultEvidence(t *testing.T) {
	mux, store := newTicketTestMux(t)
	ws := newTicketHandlerWorkspace(t, store, "Practice")
	service := workspace.NewTicketService(store)
	ticket, err := service.Create(workspace.TicketCreateInput{
		WorkspaceID: ws.ID, State: workspace.TicketStateReady, Title: "Review without a result",
	})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/workspaces/" + ws.ID + "/tickets/" + ticket.ID + "/transition"
	for _, body := range []string{
		`{"to":"in_progress"}`,
		`{"to":"review"}`,
		`{"to":"done","actor":"user","actor_id":"invented-person","run_id":"invented-run","reason":"Looks good"}`,
	} {
		rec := doTicketRequest(t, mux, http.MethodPost, path, body)
		if rec.Code != http.StatusOK {
			t.Fatalf("transition: %d %s", rec.Code, rec.Body.String())
		}
	}
	got, err := service.Get(ws.ID, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	last := got.StateHistory[len(got.StateHistory)-1]
	if got.State != workspace.TicketStateDone || last.ActorID != "invented-person" || last.RunID != "invented-run" {
		t.Fatalf("legacy transition contract changed; re-evaluate learning adapter: %+v", got)
	}
	if got.CurrentRunID != "" || got.LatestResult != "" {
		t.Fatal("a transition should not manufacture a canonical execution/result")
	}
}
