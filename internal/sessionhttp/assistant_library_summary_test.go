package sessionhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type librarySummaryBody struct {
	Initialized    bool                          `json:"initialized"`
	Revision       int64                         `json:"revision"`
	Digest         *projectlibrary.LibraryDigest `json:"digest"`
	ReadyProposals int                           `json:"ready_proposals"`
	Route          string                        `json:"route"`
}

func TestAssistantLibraryHTTP_SummaryIsOwnerScopedBoundedAndInert(t *testing.T) {
	handler, store, station, project := assistantPortfolioHTTPFixture(t)
	handler.currentUserID = func(context.Context) (string, error) { return station.OwnerUserID, nil }
	summary := func(homeID, query string) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		handler.GetAssistantLibrarySummary(response, assistantProgramRequest(http.MethodGet,
			"/library/summary"+query, homeID, ""))
		return response
	}
	decode := func(response *httptest.ResponseRecorder) librarySummaryBody {
		t.Helper()
		var body librarySummaryBody
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil {
			t.Fatalf("summary: %d %s", response.Code, response.Body.String())
		}
		return body
	}
	// An uninitialized Home reads as zeros, never an error the badge must handle.
	if body := decode(summary(station.ID, "")); body.Initialized || body.Digest != nil || body.ReadyProposals != 0 {
		t.Fatalf("uninitialized summary: %+v", body)
	}
	state := station.GetAssistantProgramState()
	scope := projectlibrary.Scope{OwnerUserID: station.OwnerUserID, HomeID: station.ID,
		ProviderID: state.Key.PluginID, ProgramID: state.Key.ProgramID}
	library := projectlibrary.NewStore(store)
	review, err := library.ReviewInitialize(scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := library.CommitInitialize(scope, review.Token, "summary-init"); err != nil {
		t.Fatal(err)
	}
	before, err := library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	response := summary(station.ID, "")
	body := decode(response)
	if !body.Initialized || body.Revision != before.Revision || body.Digest != nil || body.ReadyProposals != 0 {
		t.Fatalf("initialized summary: %+v", body)
	}
	if len(response.Body.Bytes()) > 1024 || strings.Contains(response.Body.String(), "project_library") {
		t.Fatalf("summary is not a small projection: %s", response.Body.String())
	}
	home, err := store.Get(station.ID)
	if err != nil {
		t.Fatal(err)
	}
	if workspace.IsCanonicalWorkspaceSlug(home.FolderSlug) {
		if body.Route != "/workspaces/"+home.FolderSlug+"/assistant#projectLibraryProposals" {
			t.Fatalf("summary route: %q", body.Route)
		}
	} else if body.Route != "" {
		t.Fatalf("summary invented a route without a canonical slug: %q", body.Route)
	}
	if after, err := library.Read(scope); err != nil || after.Revision != before.Revision {
		t.Fatalf("summary mutated the Home: %v", err)
	}
	if result := summary(station.ID, "?refresh=1"); result.Code != http.StatusBadRequest {
		t.Fatalf("summary accepted a query: %d", result.Code)
	}
	if result := summary(project.ID, ""); result.Code != http.StatusNotFound {
		t.Fatalf("linked project route leaked the Home summary: %d %s", result.Code, result.Body.String())
	}
	handler.currentUserID = func(context.Context) (string, error) { return "other-owner", nil }
	if result := summary(station.ID, ""); result.Code != http.StatusNotFound {
		t.Fatalf("other owner saw the Home summary: %d %s", result.Code, result.Body.String())
	}
}

// The library Roots are configured before the event bus is wired, so the
// adapter must reach the bus that exists when a scan completes.
func TestAssistantLibraryEvents_ResolveTheBusAtPublishTime(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	t.Cleanup(cleanup)
	events := libraryEvents{h: handler}
	payload := workspace.LibraryScanCompleted{HomeID: "home", ScanID: "scan", RootID: "root", Coverage: "complete"}
	events.Publish(payload.Event("project_library")) // no bus yet: a no-op, not a panic
	bus := workspace.DefaultEventBus()
	handler.SetEventBus(bus)
	events.Publish(payload.Event("project_library"))
	history := bus.GetHistory(func(event workspace.Event) bool {
		return event.Type == workspace.EventLibraryScanCompleted
	}, 10)
	if len(history) != 1 {
		t.Fatalf("want one delivered event, got %d", len(history))
	}
	if _, err := (libraryInstalledPlugins{h: &Handler{}}).List(); err == nil {
		t.Fatal("a missing plugin manager must read as unavailable, not as an empty install")
	}
}
