package personalassistanthttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

type fakeFirstBatchActions struct {
	calls    []string
	retryErr error
}

func (f *fakeFirstBatchActions) DismissFirstBatch(_ context.Context, userID, offerID string) error {
	f.calls = append(f.calls, "dismiss "+userID+"/"+offerID)
	return nil
}

func (f *fakeFirstBatchActions) RetryFirstBatch(_ context.Context, userID, offerID string) (personalassistant.FolderFirstBatch, error) {
	f.calls = append(f.calls, "retry "+userID+"/"+offerID)
	return personalassistant.FolderFirstBatch{Outcome: personalassistant.FolderFirstBatchReady}, f.retryErr
}

func firstBatchCall(t *testing.T, h *Handler, action, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/personal-assistant/first-batch/offer-1/"+action, strings.NewReader(body))
	r.SetPathValue("offerID", "offer-1")
	w := httptest.NewRecorder()
	if action == "retry" {
		h.RetryFirstBatch(w, r)
	} else {
		h.DismissFirstBatch(w, r)
	}
	return w
}

// An inbox hire's report names only its setup; the owner is the signed-in user
// and the body carries nothing.
func TestFirstBatchRoutes_AreOwnerScopedAndTakeNoRequestData(t *testing.T) {
	actions := &fakeFirstBatchActions{}
	h := NewHandler(nil, fakeUserProvider{userID: "owner-1"})
	h.SetFirstBatchActions(actions)
	if w := firstBatchCall(t, h, "dismiss", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"dismissed":true`) {
		t.Fatalf("dismiss = %d %s", w.Code, w.Body.String())
	}
	if w := firstBatchCall(t, h, "retry", "{}"); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"outcome":"batch"`) {
		t.Fatalf("retry = %d %s", w.Code, w.Body.String())
	}
	if got := strings.Join(actions.calls, ","); got != "dismiss owner-1/offer-1,retry owner-1/offer-1" {
		t.Fatalf("calls = %s", got)
	}
	if w := firstBatchCall(t, h, "retry", `{"workspace_id":"other"}`); w.Code != http.StatusBadRequest || len(actions.calls) != 2 {
		t.Fatalf("a body reached the service: %d %v", w.Code, actions.calls)
	}
}

func TestFirstBatchRoutes_AnswerRefusalsWithCodes(t *testing.T) {
	for err, want := range map[error]struct {
		code int
		body string
	}{
		personalassistant.ErrFirstBatchNotFailed: {http.StatusConflict, `"code":"not_failed"`},
		personalassistant.ErrFirstBatchRetryBusy: {http.StatusConflict, `"code":"retry_running"`},
		personalassistant.ErrFirstLookNotFound:   {http.StatusNotFound, ""},
	} {
		h := NewHandler(nil, fakeUserProvider{userID: "owner-1"})
		h.SetFirstBatchActions(&fakeFirstBatchActions{retryErr: err})
		if w := firstBatchCall(t, h, "retry", "{}"); w.Code != want.code || !strings.Contains(w.Body.String(), want.body) {
			t.Fatalf("%v => %d %s", err, w.Code, w.Body.String())
		}
	}
	unwired := NewHandler(nil, fakeUserProvider{userID: "owner-1"})
	if w := firstBatchCall(t, unwired, "dismiss", "{}"); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("unwired => %d", w.Code)
	}
}
