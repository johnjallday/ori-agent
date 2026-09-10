package setupjourneyhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
	"github.com/johnjallday/ori-agent/internal/specialist"
)

type absentQuestRelationship struct{}

func (absentQuestRelationship) GetState(context.Context, string) (*personalassistant.State, error) {
	return nil, errors.New("no accepted relationship")
}

type emptyQuestPlugins struct{}

func (emptyQuestPlugins) List() ([]plugin.InstalledPlugin, error) { return nil, nil }

type httpUserQuestLibrary struct{ template projecttemplates.Template }

func (l httpUserQuestLibrary) ListUserSetupQuestTemplates(context.Context) ([]projecttemplates.Template, error) {
	return []projecttemplates.Template{l.template}, nil
}
func (l httpUserQuestLibrary) FindUserSetupQuestTemplate(context.Context, string) (projecttemplates.Template, error) {
	return l.template, nil
}
func (httpUserQuestLibrary) WithUserSetupQuestMutationLock(_ context.Context, operation func() error) error {
	return operation()
}

func questHTTPFixture(t *testing.T) (*setupjourney.Service, *database.DB) {
	t.Helper()
	db, err := database.Open(context.Background(), &database.Config{InMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	readers := make(map[specialist.SetupStepKind]setupjourney.CanonicalReader)
	for _, kind := range []specialist.SetupStepKind{specialist.SetupStepIntegrationInstall, specialist.SetupStepProjectConnect, specialist.SetupStepWorkspaceSetup, specialist.SetupStepAssistantProgramStaffing, specialist.SetupStepSummary} {
		readers[kind] = setupjourney.CanonicalReaderFunc(func(context.Context, setupjourney.ReadScope) (setupjourney.CanonicalStepRead, error) {
			return setupjourney.CanonicalStepRead{}, nil
		})
	}
	registry, err := setupjourney.NewReaderRegistry(readers)
	if err != nil {
		t.Fatal(err)
	}
	service, err := setupjourney.NewService(setupjourney.NewSQLiteStore(db), absentQuestRelationship{}, registry)
	if err != nil {
		t.Fatal(err)
	}
	service.SetQuestCatalog(setupjourney.NewInstalledQuestCatalog(emptyQuestPlugins{}))
	return service, db
}

func questHTTPMux(service *setupjourney.Service, user string) *http.ServeMux {
	h := NewHandler(service, fixedUserProvider{id: user})
	mux := testMux(h)
	const root = "/api/setup-quests/{pluginID}/{questID}"
	mux.HandleFunc("GET /api/setup-quests", h.ListQuests)
	mux.HandleFunc("GET "+root, h.ScopeQuest((*Handler).GetRoot))
	mux.HandleFunc("GET "+root+"/runs/{runID}", h.ScopeQuest((*Handler).GetRun))
	mux.HandleFunc("POST "+root+"/open", h.ScopeQuest((*Handler).OpenRoot))
	mux.HandleFunc("POST "+root+"/runs/{runID}/actions/{actionID}", h.ScopeQuest((*Handler).Mutate))
	const userRoot = "/api/user-template-setup-quests/{templateID}/{attachmentID}"
	mux.HandleFunc("GET "+userRoot, h.ScopeUserTemplateQuest((*Handler).GetRoot))
	mux.HandleFunc("POST "+userRoot+"/runs/{runID}/actions/{actionID}", h.ScopeUserTemplateQuest((*Handler).Mutate))
	return mux
}

func TestQuestHTTPDiscoveryIsReadOnlyAndScopeComesOnlyFromTrustedPathAndUser(t *testing.T) {
	service, db := questHTTPFixture(t)
	mux := questHTTPMux(service, "local")
	const root = "/api/setup-quests/reaper-plugin/reaper_setup"
	request := func(method, path, body string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(method, path, strings.NewReader(body)))
		return response
	}
	for _, path := range []string{"/api/setup-quests", root + "?owner_user_id=other", "/api/setup-quests/foreign-plugin/reaper_setup", "/api/setup-quests/reaper-plugin/unknown"} {
		response := request(http.MethodGet, path, "")
		if (path == "/api/setup-quests") != (response.Code == http.StatusOK) {
			t.Fatalf("%s: %d %s", path, response.Code, response.Body.String())
		}
		var count int
		if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM setup_journey_run").Scan(&count); err != nil || count != 0 {
			t.Fatalf("read/refusal created progress: %d err=%v", count, err)
		}
	}
	response := request(http.MethodGet, root, "")
	var body journeyResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || body.Journey == nil || body.Journey.Journey.PluginID != "reaper-plugin" {
		t.Fatalf("independent root: %d %s", response.Code, response.Body.String())
	}
	savedID := body.Journey.RunID
	for _, input := range []string{`{"plugin_id":"foreign"}`, `{"quest_id":"other"}`, `{"owner_user_id":"other"}`, `{"source":"https://other.test"}`} {
		response = request(http.MethodPost, root+"/runs/"+savedID+"/actions/review_install", `{"if_revision":1,"idempotency_key":"no-override","input":`+input+`}`)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("override accepted: %d %s", response.Code, response.Body.String())
		}
	}
	response = request(http.MethodGet, "/api/personal-assistant/setup-journey", "")
	if response.Code == http.StatusOK {
		t.Fatal("quest created an assistant acceptance")
	}
	foreign := httptest.NewRecorder()
	questHTTPMux(service, "other-user").ServeHTTP(foreign, httptest.NewRequest(http.MethodGet, root+"/runs/"+savedID, nil))
	if foreign.Code != http.StatusNotFound || strings.Contains(foreign.Body.String(), savedID) {
		t.Fatalf("cross-user read: %d %s", foreign.Code, foreign.Body.String())
	}
}

func TestUserTemplateQuestHTTPRouteUsesAttachmentIdentityWithoutPluginOwnership(t *testing.T) {
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
		setupjourney.NewInstalledQuestCatalog(emptyQuestPlugins{}),
		setupjourney.NewUserTemplateQuestCatalog(httpUserQuestLibrary{template: template}),
	))
	mux := questHTTPMux(service, "local")
	root := "/api/user-template-setup-quests/" + template.ID + "/" + quest.AttachmentID

	list := httptest.NewRecorder()
	mux.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/setup-quests", nil))
	var catalogBody struct {
		Quests []setupjourney.QuestSummary `json:"quests"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &catalogBody); err != nil {
		t.Fatal(err)
	}
	foundUser := false
	for _, item := range catalogBody.Quests {
		if item.Source == setupjourney.QuestSourceUserTemplate {
			foundUser = item.PluginID == "" && item.TemplateID == template.ID && item.AttachmentID == quest.AttachmentID
		}
	}
	if list.Code != http.StatusOK || !foundUser {
		t.Fatalf("list=%d %s", list.Code, list.Body.String())
	}
	var before int
	_ = db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM setup_journey_run").Scan(&before)
	if before != 0 {
		t.Fatalf("catalog read created %d progress rows", before)
	}

	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, root, nil))
	var body journeyResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || body.Journey == nil || body.Journey.Journey.Source != setupjourney.QuestSourceUserTemplate ||
		body.Journey.Journey.PluginID != "" || body.Journey.Journey.AttachmentID != quest.AttachmentID {
		t.Fatalf("user route=%d %s", response.Code, response.Body.String())
	}
	var bindings int
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM setup_user_template_binding").Scan(&bindings); err != nil || bindings != 1 {
		t.Fatalf("bindings=%d err=%v", bindings, err)
	}
}
