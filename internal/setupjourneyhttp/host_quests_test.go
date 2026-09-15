package setupjourneyhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/hostquests"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
)

func hostQuestHTTPMux(service *setupjourney.Service, user string) *http.ServeMux {
	h := NewHandler(service, fixedUserProvider{id: user})
	mux := questHTTPMux(service, user)
	const root = "/api/host-setup-quests/{questID}"
	mux.HandleFunc("GET "+root, h.ScopeHostQuest((*Handler).GetRoot))
	mux.HandleFunc("GET "+root+"/status", h.ScopeHostQuest((*Handler).Status))
	mux.HandleFunc("GET "+root+"/runs/{runID}", h.ScopeHostQuest((*Handler).GetRun))
	mux.HandleFunc("POST "+root+"/open", h.ScopeHostQuest((*Handler).OpenRoot))
	mux.HandleFunc("POST "+root+"/dismiss", h.ScopeHostQuest((*Handler).DismissRoot))
	mux.HandleFunc("POST "+root+"/runs/{runID}/actions/{actionID}", h.ScopeHostQuest((*Handler).Mutate))
	return mux
}

func hostQuestHTTPFixture(t *testing.T) (*setupjourney.Service, *database.DB) {
	t.Helper()
	service, db := questHTTPFixture(t)
	service.SetQuestCatalog(setupjourney.CombineQuestCatalogs(
		setupjourney.NewInstalledQuestCatalog(emptyQuestPlugins{}),
		setupjourney.NewHostQuestCatalog(hostquests.All()),
	))
	return service, db
}

func journeyRunCount(t *testing.T, db *database.DB) int {
	t.Helper()
	var count int
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM setup_journey_run").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestHostQuestHTTPListsStatusesAndScopesWithoutCreatingProgress(t *testing.T) {
	service, db := hostQuestHTTPFixture(t)
	mux := hostQuestHTTPMux(service, "local")
	request := func(mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(method, path, strings.NewReader(body)))
		return response
	}
	root := "/api/host-setup-quests/" + hostquests.EmailOpsSetupQuestID

	// The shared catalog lists the host quest as inert metadata.
	listed := request(mux, http.MethodGet, "/api/setup-quests", "")
	var list struct {
		Quests []setupjourney.QuestSummary `json:"quests"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &list); err != nil || listed.Code != http.StatusOK {
		t.Fatalf("list: %d %s", listed.Code, listed.Body.String())
	}
	found := false
	for _, quest := range list.Quests {
		found = found || (quest.Source == setupjourney.QuestSourceHost && quest.ID == hostquests.EmailOpsSetupQuestID &&
			quest.TemplateID == "email-ops" && quest.Ownership == "host" && quest.PluginID == "")
	}
	if !found {
		t.Fatalf("host quest missing from the catalog: %s", listed.Body.String())
	}

	// Status, invalid IDs, and query overrides create nothing.
	status := request(mux, http.MethodGet, root+"/status", "")
	if status.Code != http.StatusOK || strings.TrimSpace(status.Body.String()) != `{"exists":false}` {
		t.Fatalf("status before start: %d %s", status.Code, status.Body.String())
	}
	for path, want := range map[string]int{
		"/api/host-setup-quests/unknown_quest":        http.StatusNotFound,
		"/api/host-setup-quests/unknown_quest/status": http.StatusNotFound,
		"/api/host-setup-quests/Bad%20ID":             http.StatusBadRequest,
		root + "/status?owner_user_id=other":          http.StatusBadRequest,
		root + "?owner_user_id=other":                 http.StatusBadRequest,
	} {
		if response := request(mux, http.MethodGet, path, ""); response.Code != want {
			t.Fatalf("%s: %d %s", path, response.Code, response.Body.String())
		}
	}
	if count := journeyRunCount(t, db); count != 0 {
		t.Fatalf("list/status/refusals created %d journey rows", count)
	}

	// The first authorized read creates the one root; status then reports it.
	opened := request(mux, http.MethodGet, root, "")
	var body journeyResponse
	if err := json.Unmarshal(opened.Body.Bytes(), &body); err != nil || opened.Code != http.StatusOK || body.Journey == nil {
		t.Fatalf("root read: %d %s", opened.Code, opened.Body.String())
	}
	if body.Journey.Journey.Source != setupjourney.QuestSourceHost || body.Journey.Journey.TemplateID != "email-ops" ||
		body.Journey.Journey.PluginID != "" || len(body.Journey.Steps) != 4 {
		t.Fatalf("host projection: %s", opened.Body.String())
	}
	status = request(mux, http.MethodGet, root+"/status", "")
	var statusBody statusResponse
	if err := json.Unmarshal(status.Body.Bytes(), &statusBody); err != nil || !statusBody.Exists ||
		statusBody.Journey == nil || statusBody.Journey.RunID != body.Journey.RunID {
		t.Fatalf("status after start: %d %s", status.Code, status.Body.String())
	}
	if count := journeyRunCount(t, db); count != 1 {
		t.Fatalf("host quest created %d roots, want 1", count)
	}

	// Body fields cannot select scope, and another user cannot read this run.
	for _, input := range []string{`{"workspace_id":"foreign"}`, `{"source":"plugin"}`, `{"quest_id":"other"}`} {
		response := request(mux, http.MethodPost, root+"/runs/"+body.Journey.RunID+"/actions/review_mailbox_link",
			`{"if_revision":1,"idempotency_key":"no-override","input":`+input+`}`)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("override accepted: %d %s", response.Code, response.Body.String())
		}
	}
	other := hostQuestHTTPMux(service, "other-user")
	if otherStatus := request(other, http.MethodGet, root+"/status", ""); strings.TrimSpace(otherStatus.Body.String()) != `{"exists":false}` {
		t.Fatalf("other user status: %d %s", otherStatus.Code, otherStatus.Body.String())
	}
	if count := journeyRunCount(t, db); count != 1 {
		t.Fatalf("another user's status created rows: %d", count)
	}
	// A foreign run ID fails closed. Like every quest read, the attempt is the
	// caller's own first authorized read, so it may create only their own root.
	foreign := request(other, http.MethodGet, root+"/runs/"+body.Journey.RunID, "")
	if foreign.Code != http.StatusNotFound || strings.Contains(foreign.Body.String(), body.Journey.RunID) {
		t.Fatalf("cross-user read: %d %s", foreign.Code, foreign.Body.String())
	}
	otherStatus := request(other, http.MethodGet, root+"/status", "")
	var otherBody statusResponse
	if err := json.Unmarshal(otherStatus.Body.Bytes(), &otherBody); err != nil ||
		(otherBody.Journey != nil && otherBody.Journey.RunID == body.Journey.RunID) {
		t.Fatalf("other user reached the first user's run: %s", otherStatus.Body.String())
	}

	// A plugin-scoped route cannot reach the host quest.
	if response := request(mux, http.MethodGet, "/api/setup-quests/reaper-plugin/"+hostquests.EmailOpsSetupQuestID, ""); response.Code == http.StatusOK {
		t.Fatalf("plugin route served a host quest: %s", response.Body.String())
	}
}
