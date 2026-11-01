package personalassistanthttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/connectionshttp"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

type fakeSetupLearning struct {
	calls     []string
	requestID string
	err       error
}

func (f *fakeSetupLearning) List(context.Context, string) (personalassistant.SetupLearningView, error) {
	f.calls = append(f.calls, "list")
	return personalassistant.SetupLearningView{
		Items: []personalassistant.SetupLearningItem{{ID: "item-1", Version: 2, Category: "people", Text: "You deal with Chase (seen in 2 files you filed).", Source: "files"}},
		Total: 1,
	}, f.err
}

func (f *fakeSetupLearning) Dismiss(_ context.Context, _ string, requestID string) (int, error) {
	f.calls = append(f.calls, "dismiss")
	f.requestID = requestID
	return 2, f.err
}

func setupLearningHandler(fake *fakeSetupLearning, withOrigin bool) http.Handler {
	h := NewHandler(nil, userprofile.LocalUserProvider{})
	if withOrigin {
		h.SetSecondBrain(&fakeSecondBrain{}, connectionshttp.NewOriginGuard().Check)
	}
	h.SetSetupLearning(fake)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/personal-assistant/setup-learning", h.GetSetupLearning)
	mux.HandleFunc("POST /api/personal-assistant/setup-learning/dismiss", h.DismissSetupLearning)
	return mux
}

func setupLearningRequest(handler http.Handler, method, path, body, origin string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "http://localhost:8931/api/personal-assistant/setup-learning"+path, strings.NewReader(body))
	request.Host = "localhost:8931"
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestSetupLearningRoutes_RefuseAForeignOriginBeforeTouchingTheQueue(t *testing.T) {
	fake := &fakeSetupLearning{}
	handler := setupLearningHandler(fake, true)
	for _, route := range []struct{ method, path, body string }{
		{http.MethodGet, "", ""},
		{http.MethodPost, "/dismiss", `{"request_id":"d-1"}`},
	} {
		if got := setupLearningRequest(handler, route.method, route.path, route.body, "https://evil.example").Code; got != http.StatusForbidden {
			t.Errorf("%s %s from a foreign origin = %d, want 403", route.method, route.path, got)
		}
		if got := setupLearningRequest(handler, route.method, route.path, route.body, "http://localhost:8931").Code; got != http.StatusOK {
			t.Errorf("%s %s from Ori's page = %d, want 200", route.method, route.path, got)
		}
	}
	if strings.Join(fake.calls, ",") != "list,dismiss" {
		t.Fatalf("calls = %v, want only the two local requests", fake.calls)
	}
}

func TestSetupLearningRoutes_ListCarriesWhatTheApproveActionTakesAndDismissTakesOnlyAnId(t *testing.T) {
	fake := &fakeSetupLearning{}
	handler := setupLearningHandler(fake, true)
	const origin = "http://localhost:8931"
	list := setupLearningRequest(handler, http.MethodGet, "", "", origin)
	for _, want := range []string{`"id":"item-1"`, `"version":2`, `"category":"people"`, `"total":1`, `You deal with Chase`} {
		if !strings.Contains(list.Body.String(), want) {
			t.Errorf("list lacks %s: %s", want, list.Body)
		}
	}
	dismissed := setupLearningRequest(handler, http.MethodPost, "/dismiss", `{"request_id":"setup-dismiss-1"}`, origin)
	if dismissed.Code != http.StatusOK || !strings.Contains(dismissed.Body.String(), `"dismissed":2`) || fake.requestID != "setup-dismiss-1" {
		t.Fatalf("dismiss = %d %s (id %q)", dismissed.Code, dismissed.Body, fake.requestID)
	}
	for _, body := range []string{
		`{"request_id":"d-1","item_ids":["item-1"]}`,
		`{"request_id":"d-1","path":"/Users/sam/Downloads"}`,
		`{"request_id":"d-1"}{"request_id":"d-2"}`,
		`["request_id"]`,
	} {
		fresh := &fakeSetupLearning{}
		if got := setupLearningRequest(setupLearningHandler(fresh, true), http.MethodPost, "/dismiss", body, origin).Code; got != http.StatusBadRequest || len(fresh.calls) != 0 {
			t.Errorf("%s = %d (calls %v), want 400 before the service", body, got, fresh.calls)
		}
	}
	if got := setupLearningRequest(handler, http.MethodPut, "/dismiss", `{"request_id":"d-1"}`, origin).Code; got != http.StatusMethodNotAllowed && got != http.StatusNotFound {
		t.Errorf("PUT = %d", got)
	}
}

func TestSetupLearningRoutes_AnUnwiredCardOrAFailingQueueIsUnavailableNotEmpty(t *testing.T) {
	const origin = "http://localhost:8931"
	if got := setupLearningRequest(setupLearningHandler(&fakeSetupLearning{}, false), http.MethodGet, "", "", origin).Code; got != http.StatusServiceUnavailable {
		t.Errorf("no origin guard = %d, want 503", got)
	}
	failing := &fakeSetupLearning{err: errors.New("queue unreadable")}
	if got := setupLearningRequest(setupLearningHandler(failing, true), http.MethodGet, "", "", origin).Code; got != http.StatusServiceUnavailable {
		t.Errorf("an unreadable queue = %d, want 503", got)
	}
	invalid := &fakeSetupLearning{err: personalassistant.ErrValidation}
	if got := setupLearningRequest(setupLearningHandler(invalid, true), http.MethodPost, "/dismiss", `{"request_id":"d 1"}`, origin).Code; got != http.StatusBadRequest {
		t.Errorf("an invalid id = %d, want 400", got)
	}
	paused := &fakeSetupLearning{err: personalassistant.ErrKnowledgePaused}
	if got := setupLearningRequest(setupLearningHandler(paused, true), http.MethodGet, "", "", origin).Code; got != http.StatusConflict {
		t.Errorf("a paused assistant = %d, want 409", got)
	}
}
