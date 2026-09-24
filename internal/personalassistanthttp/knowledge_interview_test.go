package personalassistanthttp

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

type fakeInterviewOffer struct {
	userID string
	calls  int
	err    error
}

func (f *fakeInterviewOffer) Offer(_ context.Context, userID string) (*personalassistant.KnowledgeInterview, error) {
	f.calls++
	f.userID = userID
	return nil, f.err
}

func TestInterviewOfferedOnlyAfterHQActivationAndNeverBlocksIt(t *testing.T) {
	offer := &fakeInterviewOffer{err: errors.New("sidecar unavailable")}
	apps := &fakeAppSuggestions{err: errors.New("no source available")}
	h := hqHandler(t, &fakeHQSetupService{result: activeHQResult()})
	h.SetInterviewOffer(offer)
	h.SetSavedAppSuggestions(apps)
	response := postHQ(t, h, `{}`)
	if response.Code != http.StatusCreated || offer.calls != 1 || offer.userID != "user-a" || apps.calls != 1 || apps.userID != "user-a" {
		t.Fatalf("activation must succeed despite optional hooks: status=%d offer=%d apps=%d user=%s", response.Code, offer.calls, apps.calls, offer.userID)
	}

	failed := hqHandler(t, &fakeHQSetupService{err: personalassistant.ErrConflict})
	failed.SetInterviewOffer(offer)
	failed.SetSavedAppSuggestions(apps)
	response = postHQ(t, failed, `{}`)
	if response.Code != http.StatusConflict || offer.calls != 1 || apps.calls != 1 {
		t.Fatalf("hooks ran on failed HQ setup: status=%d offer=%d apps=%d", response.Code, offer.calls, apps.calls)
	}
}
