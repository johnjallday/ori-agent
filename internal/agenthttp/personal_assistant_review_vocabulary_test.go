package agenthttp

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

type reviewElsewhereReader struct {
	reviewContextReader
	pending *personalassistant.FolderOfferView
	err     error
}

func (s *reviewElsewhereReader) PendingElsewhere(context.Context, foldercontext.Target) (*personalassistant.FolderOfferView, error) {
	return s.pending, s.err
}

// A conversation that has no review of its own shows no review line when all
// that is known is that reviews in other conversations could not be looked up.
// Under a reply about something else that line read as a fault in the reply.
// The model is still told the state is unavailable, so it does not say "there
// is no setup" when asked. A conversation that has its own review still says
// when that review cannot be read.
func TestReviewContext_FailedLookupElsewhereIsNotAnnouncedUnderAnUnrelatedReply(t *testing.T) {
	elsewhere := &reviewElsewhereReader{err: foldercontext.ErrInvalid}
	h := &HomeAssistantAskHandler{UserID: "local", FolderSetups: elsewhere}
	for _, id := range []string{"", "conversation"} {
		got := h.prepareReviewContext(context.Background(), &openConversation{id: id, scope: personalAssistantConversationScope{workspaceID: "hq", agentName: "atlas"}}, nil)
		if got.Status != "state_unavailable" || !strings.Contains(string(reviewContextJSON(got)), `"status":"state_unavailable"`) {
			t.Fatalf("conversation %q: the model was not told the state is unavailable: %+v", id, got)
		}
		if shown := got.forDrawer(); shown == nil || shown.Status != "no_proposal" || shown.elsewhereRef() != nil {
			t.Fatalf("conversation %q: the drawer was given %+v", id, shown)
		}
	}
	// This conversation's own review that cannot be read is still stated.
	own := &personalAssistantReviewContext{Version: 1, Status: "state_unavailable"}
	if shown := own.forDrawer(); shown != own {
		t.Fatalf("a conversation's own unreadable review was hidden: %+v", shown)
	}
	found := &personalAssistantReviewContext{Version: 1, Status: "pending_elsewhere", elsewhere: "other"}
	if shown := found.forDrawer(); shown != found || shown.elsewhereRef() == nil {
		t.Fatalf("a review found elsewhere was hidden: %+v", shown)
	}
	var none *personalAssistantReviewContext
	if none.forDrawer() != nil {
		t.Fatal("no conversation, no review line")
	}
}

// The drawer has one sentence per status in this list. A status the projection
// can produce but the list lacks would reach the user as "could not be read".
func TestReviewContext_ProducesExactlyTheSharedStatusVocabulary(t *testing.T) {
	data, err := os.ReadFile("testdata/review_status_vocabulary.json")
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		Statuses []string `json:"statuses"`
	}
	if err := json.Unmarshal(data, &contract); err != nil {
		t.Fatal(err)
	}
	subject := personalassistant.FolderSubjectView{Name: "Album-5"}
	pending := personalassistant.FolderOfferPending
	awaiting := personalassistant.FolderOfferAwaitingOutcome
	produced := map[string]bool{}
	for name, test := range map[string]struct {
		view   personalassistant.FolderOfferView
		active bool
		want   string
	}{
		"review ready":        {personalassistant.FolderOfferView{Status: pending, CreateAvailable: true}, true, "awaiting_confirmation"},
		"confirmed, not done": {personalassistant.FolderOfferView{Status: awaiting}, true, "awaiting_outcome"},
		"running":             {personalassistant.FolderOfferView{Status: awaiting, Setup: &personalassistant.FolderSetupView{Status: personalassistant.FolderSetupRunning}}, true, "setup_running"},
		"needs a choice":      {personalassistant.FolderOfferView{Status: awaiting, Setup: &personalassistant.FolderSetupView{Status: personalassistant.FolderSetupStopped, StopReason: personalassistant.FolderStopNeedsChoice}}, true, "setup_stopped"},
		"access expired":      {personalassistant.FolderOfferView{Status: pending, CreateAvailable: true, NeedsPick: true}, true, "reselection_needed"},
		"unsupported here":    {personalassistant.FolderOfferView{Status: pending}, true, "setup_unavailable"},
		"destination changed": {personalassistant.FolderOfferView{Status: pending, CreateAvailable: true, DestinationStatus: "changed"}, true, "setup_unavailable"},
		"completed":           {personalassistant.FolderOfferView{Status: personalassistant.FolderOfferResolved, Outcome: &personalassistant.FolderOutcome{Receipt: []personalassistant.FolderReceiptRow{{Name: "Created project"}}}}, true, "completed"},
		"closed":              {personalassistant.FolderOfferView{Status: personalassistant.FolderOfferClosed}, false, "closed"},
		"declined":            {personalassistant.FolderOfferView{Status: personalassistant.FolderOfferDeclined}, false, "declined"},
		"postponed":           {personalassistant.FolderOfferView{Status: personalassistant.FolderOfferLater}, false, "postponed"},
		"receipt missing":     {personalassistant.FolderOfferView{Status: personalassistant.FolderOfferResolved}, true, "state_unavailable"},
	} {
		test.view.Subject = subject
		got := projectReviewContext(&test.view, test.active)
		if got.Status != test.want {
			t.Fatalf("%s: status %q, want %q", name, got.Status, test.want)
		}
		if test.want == "setup_unavailable" && got.Blocker == "" {
			t.Fatalf("%s: a blocked review must say why", name)
		}
		produced[got.Status] = true
	}
	// A review in another conversation is not "unsupported", and a conversation
	// that is not saved yet can still be told about it.
	elsewhere := &reviewElsewhereReader{pending: &personalassistant.FolderOfferView{ID: "other-review", ConversationID: "other-conversation", Status: pending, Subject: subject, CreateAvailable: true}}
	h := &HomeAssistantAskHandler{UserID: "local", FolderSetups: elsewhere}
	for _, id := range []string{"", "conversation"} {
		got := h.prepareReviewContext(context.Background(), &openConversation{id: id, scope: personalAssistantConversationScope{workspaceID: "hq", agentName: "atlas"}}, nil)
		if got.Status != "pending_elsewhere" || !slices.Equal(got.Controls, []string{"Open existing conversation"}) || got.elsewhereRef() == nil || got.elsewhereRef().ConversationID != "other-conversation" {
			t.Fatalf("conversation %q: %+v", id, got)
		}
		encoded := string(reviewContextJSON(got))
		for _, private := range []string{"other-review", "other-conversation"} {
			if strings.Contains(encoded, private) {
				t.Fatalf("model input carries a reference: %s", encoded)
			}
		}
		produced[got.Status] = true
	}
	elsewhere.pending = nil
	none := h.prepareReviewContext(context.Background(), &openConversation{scope: personalAssistantConversationScope{workspaceID: "hq", agentName: "atlas"}}, nil)
	if none.Status != "no_proposal" || none.elsewhereRef() != nil {
		t.Fatalf("a new conversation with nothing pending: %+v", none)
	}
	produced[none.Status] = true

	for status := range produced {
		if !slices.Contains(contract.Statuses, status) {
			t.Errorf("projection produced %q, which the drawer has no sentence for", status)
		}
	}
	for _, status := range contract.Statuses {
		if !produced[status] {
			t.Errorf("vocabulary lists %q, which no projection case produces", status)
		}
	}
}
