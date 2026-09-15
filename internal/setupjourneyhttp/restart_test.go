package setupjourneyhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
)

// restartingStub is a service that also offers Start over.
type restartingStub struct {
	serviceStub
	restartErr error
	restarts   int
}

func (stub *restartingStub) Restart(_ context.Context, userID string) (*setupjourney.JourneyProjection, error) {
	stub.lastOperation = "restart"
	stub.lastUserID = userID
	stub.restarts++
	return stub.projection, stub.restartErr
}

func restartMux(handler *Handler) *http.ServeMux {
	mux := testMux(handler)
	mux.HandleFunc("POST /api/personal-assistant/setup-journey/restart", handler.RestartRoot)
	return mux
}

func failureReason(t *testing.T, response *httptest.ResponseRecorder) setupjourney.ReasonCode {
	t.Helper()
	var body struct {
		Error setupjourney.Failure `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode failure: %v body=%s", err, response.Body.String())
	}
	return body.Error.ReasonCode
}

// FR 37: Start over is a bodyless POST scoped only by the current user.
func TestRestartRootDelegatesForTheCurrentUserOnly(t *testing.T) {
	const path = "/api/personal-assistant/setup-journey/restart"
	stub := &restartingStub{serviceStub: serviceStub{projection: testProjection()}}
	mux := restartMux(NewHandler(stub, fixedUserProvider{id: "current-user"}))

	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, nil))
	var body journeyResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || stub.restarts != 1 || stub.lastUserID != "current-user" ||
		body.Journey == nil || body.Journey.RunID != "run-current" {
		t.Fatalf("restart not delegated: status=%d stub=%#v body=%s", response.Code, stub, response.Body.String())
	}

	for name, request := range map[string]*http.Request{
		"query":       httptest.NewRequest(http.MethodPost, path+"?user_id=other", nil),
		"body":        httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"user_id":"other"}`)),
		"empty JSON":  httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`)),
		"wrong verb":  httptest.NewRequest(http.MethodGet, path, nil),
		"run subpath": httptest.NewRequest(http.MethodPost, "/api/personal-assistant/setup-journey/runs/run-child/restart", nil),
	} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code < 400 || stub.restarts != 1 {
			t.Fatalf("%s reached restart: status=%d restarts=%d", name, response.Code, stub.restarts)
		}
	}

	whitespace := httptest.NewRecorder()
	mux.ServeHTTP(whitespace, httptest.NewRequest(http.MethodPost, path, strings.NewReader(" \n")))
	if whitespace.Code != http.StatusOK || stub.restarts != 2 {
		t.Fatalf("whitespace body refused: status=%d", whitespace.Code)
	}
}

// A refusal is a bounded conflict with fresh state; a service without Start
// over refuses the same way.
func TestRestartRootRefusalsAreBoundedConflicts(t *testing.T) {
	const path = "/api/personal-assistant/setup-journey/restart"
	stub := &restartingStub{
		serviceStub: serviceStub{projection: testProjection()},
		restartErr:  setupjourney.FailureFor(setupjourney.ReasonActionUnavailable, 7),
	}
	response := httptest.NewRecorder()
	restartMux(NewHandler(stub, fixedUserProvider{id: "current-user"})).ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, nil))
	if response.Code != http.StatusConflict || failureReason(t, response) != setupjourney.ReasonActionUnavailable ||
		!strings.Contains(response.Body.String(), `"current"`) {
		t.Fatalf("refusal: status=%d body=%s", response.Code, response.Body.String())
	}

	plain := &serviceStub{projection: testProjection()}
	response = httptest.NewRecorder()
	restartMux(NewHandler(plain, fixedUserProvider{id: "current-user"})).ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, nil))
	if response.Code != http.StatusConflict || failureReason(t, response) != setupjourney.ReasonActionUnavailable {
		t.Fatalf("service without restart: status=%d body=%s", response.Code, response.Body.String())
	}
}

// FR 37: a compatible plugin-quest root is refused and keeps its progress, and
// a user-template quest is refused even when a restart route reaches it.
func TestRestartRootRefusesCompatiblePluginQuestAndUserTemplateQuest(t *testing.T) {
	service, db := questHTTPFixture(t)
	template, err := projecttemplates.LoadFolder("../projecttemplates/testdata/user-setup-quest-eligible")
	if err != nil {
		t.Fatal(err)
	}
	draft := projecttemplates.DefaultUserSetupQuestDraft()
	draft.IntegrationKey = "ori_reaper"
	quest, err := projecttemplates.NewUserSetupQuest(template, nil, draft)
	if err != nil {
		t.Fatal(err)
	}
	template.UserSetupQuest = quest
	template.UserSetupQuestRevision = projecttemplates.UserSetupQuestRevision(quest)
	service.SetQuestCatalog(setupjourney.CombineQuestCatalogs(
		setupjourney.NewInstalledQuestCatalog(reaperQuestPlugins{}),
		setupjourney.NewUserTemplateQuestCatalog(httpUserQuestLibrary{template: template}, installedReaperPlugin{}),
	))
	h := NewHandler(service, fixedUserProvider{id: "local"})
	mux := questHTTPMux(service, "local")
	const pluginRoot = "/api/setup-quests/reaper-plugin/reaper_setup"
	mux.HandleFunc("POST /api/setup-quests/{pluginID}/{questID}/restart", h.ScopeQuest((*Handler).RestartRoot))
	userRoot := "/api/user-template-setup-quests/" + template.ID + "/" + quest.AttachmentID
	mux.HandleFunc("POST /api/user-template-setup-quests/{templateID}/{attachmentID}/restart", h.ScopeUserTemplateQuest((*Handler).RestartRoot))

	for _, root := range []string{pluginRoot, userRoot} {
		read := httptest.NewRecorder()
		mux.ServeHTTP(read, httptest.NewRequest(http.MethodGet, root, nil))
		if read.Code != http.StatusOK {
			t.Fatalf("%s read: %d %s", root, read.Code, read.Body.String())
		}
		before := journeyRunCount(t, db)
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, root+"/restart", nil))
		if response.Code != http.StatusConflict || failureReason(t, response) != setupjourney.ReasonActionUnavailable {
			t.Fatalf("%s restart: %d %s", root, response.Code, response.Body.String())
		}
		if after := journeyRunCount(t, db); after != before {
			t.Fatalf("%s refused restart changed runs: %d -> %d", root, before, after)
		}
	}
}
