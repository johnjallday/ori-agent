package agenthttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

func settleProposal(t *testing.T, f *readerFixture, action, id string) (int, map[string]any) {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/personal-assistant/hire-proposals/"+id+"/"+action, nil)
	req.SetPathValue("proposalID", id)
	switch action {
	case "decline":
		f.handler.DeclineHireProposalHandler(rr, req)
	case "adjust":
		f.handler.AdjustHireProposalHandler(rr, req)
	default:
		t.Fatalf("unknown action %q", action)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("%s %s: %d %s", action, id, rr.Code, rr.Body.String())
	}
	return rr.Code, body
}

func offeredProposal(t *testing.T, f *readerFixture) string {
	t.Helper()
	// The scripted provider answers in order of requests; a fresh script
	// starts the count over.
	f.provider.requests, f.provider.script = nil, []llm.ChatResponse{hireProposalReply(hireProposalFixture)}
	response := askHire(f, "help me compare three CRMs")
	if response.Confirmation == nil {
		t.Fatalf("no card: %+v", response)
	}
	return response.Confirmation.Arguments["proposal_id"].(string)
}

func TestHireProposalNotNowPutsTheOfferAsideOnceAndTellsTheModel(t *testing.T) {
	f, _, _, proposals := newHireProposalFixture(t)
	id := offeredProposal(t, f)
	status, body := settleProposal(t, f, "decline", id)
	proposal, _ := body["proposal"].(map[string]any)
	if status != http.StatusOK || body["changed"] != true || proposal["state"] != "declined" || proposal["proposal_id"] != id {
		t.Fatalf("decline: %d %v", status, body)
	}
	if saved := proposals.first(); saved.State != personalassistant.HireProposalDeclined {
		t.Fatalf("store: %+v", saved)
	}
	// The same press again changes nothing and answers the same way.
	status, body = settleProposal(t, f, "decline", id)
	if status != http.StatusOK || body["changed"] != false {
		t.Fatalf("second decline: %d %v", status, body)
	}
	// Adjust on a put-aside offer is refused with the state it is in.
	status, body = settleProposal(t, f, "adjust", id)
	if status != http.StatusConflict || body["error"] != PersonalAssistantHireProposalSettled || !strings.Contains(body["message"].(string), "put aside") {
		t.Fatalf("adjust after decline: %d %v", status, body)
	}
	// The next turn is told, and the model offers nothing on the same goal.
	f.provider.requests, f.provider.script = nil, []llm.ChatResponse{{Content: "Understood. The cheapest is usually the one with the fewest seats."}}
	next := askHire(f, "about those CRMs, which is cheapest?")
	if next.Confirmation != nil {
		t.Fatalf("a card after Not now: %+v", next)
	}
	last := f.provider.requests[len(f.provider.requests)-1]
	if user := last.Messages[len(last.Messages)-1].Content; !strings.Contains(user, "put aside by the user") {
		t.Fatalf("declined offer not told to the model: %q", user)
	}
}

func TestHireProposalAdjustRecordsTheHandOffAndRefusesAHiredOffer(t *testing.T) {
	f, _, _, proposals := newHireProposalFixture(t)
	id := offeredProposal(t, f)
	status, body := settleProposal(t, f, "adjust", id)
	if status != http.StatusOK || body["proposal"].(map[string]any)["state"] != "adjusted" {
		t.Fatalf("adjust: %d %v", status, body)
	}
	hired := offeredProposal(t, f)
	if _, err := proposals.Update(context.Background(), hired, func(p *personalassistant.HireProposal) error {
		p.State, p.HireID = personalassistant.HireProposalHired, "hire-1"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	status, body = settleProposal(t, f, "decline", hired)
	if status != http.StatusConflict || !strings.Contains(body["message"].(string), "already hired") {
		t.Fatalf("decline after hire: %d %v", status, body)
	}
}

func TestHireProposalStateRoutesAreOwnerScoped(t *testing.T) {
	f, store, _, proposals := newHireProposalFixture(t)
	status, body := settleProposal(t, f, "decline", "missing")
	if status != http.StatusNotFound || body["error"] != PersonalAssistantHireProposalNotFound {
		t.Fatalf("missing: %d %v", status, body)
	}
	// Another user's offer is as good as missing.
	if _, err := proposals.Create(context.Background(), personalassistant.HireProposal{
		ID: "foreign-user", UserID: "intruder", ConversationID: "canonical", Role: "researcher", Name: "Ruth", Brief: "b", WorkspaceName: "w",
		Blueprint: "blank", BlueprintLabel: "Blank", State: personalassistant.HireProposalOffered,
	}); err != nil {
		t.Fatal(err)
	}
	if status, _ := settleProposal(t, f, "decline", "foreign-user"); status != http.StatusNotFound {
		t.Fatalf("foreign user's offer: %d", status)
	}
	// An offer in a conversation outside this assistant's Personal HQ.
	store.seed("elsewhere", "other-hq", "Other", PersonalAssistantConversationMessage{Role: "user", Content: "hi"})
	if _, err := proposals.Create(context.Background(), personalassistant.HireProposal{
		ID: "foreign-conversation", UserID: "local", ConversationID: "elsewhere", Role: "researcher", Name: "Ruth", Brief: "b", WorkspaceName: "w",
		Blueprint: "blank", BlueprintLabel: "Blank", State: personalassistant.HireProposalOffered,
	}); err != nil {
		t.Fatal(err)
	}
	status, body = settleProposal(t, f, "decline", "foreign-conversation")
	if status != http.StatusConflict || body["error"] != PersonalAssistantConversationOutOfScope {
		t.Fatalf("foreign conversation: %d %v", status, body)
	}
	if kept, _ := proposals.Get(context.Background(), "foreign-conversation"); kept.State != personalassistant.HireProposalOffered {
		t.Fatalf("an out-of-scope offer was changed: %+v", kept)
	}
	// Without a store there is nothing to settle.
	f.handler.HireProposals = nil
	if status, body := settleProposal(t, f, "decline", "anything"); status != http.StatusServiceUnavailable || body["error"] != PersonalAssistantHireProposalUnavailable {
		t.Fatalf("no store: %d %v", status, body)
	}
}
