package sessionhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

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

// seedLibraryDigest writes a valid completed scan and its digest straight into
// the Home document, standing in for a real scan so the card rules can be
// exercised without a native picker.
func seedLibraryDigest(t *testing.T, store workspace.Store, scope projectlibrary.Scope, digest projectlibrary.LibraryDigest) {
	t.Helper()
	if err := store.Update(scope.HomeID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		var doc projectlibrary.Document
		if err := json.Unmarshal(state.ProjectLibrary, &doc); err != nil {
			return err
		}
		now := time.Now().UTC()
		rootID, scanID := "seed-root", "seed-scan-"+strconv.Itoa(len(doc.Scans))
		if len(doc.Roots) == 0 {
			doc.Roots = append(doc.Roots, projectlibrary.Root{ID: rootID, Path: t.TempDir(),
				FileIdentity: "seed-identity", Revision: 1, ApprovedAt: now})
		}
		doc.Scans = append(doc.Scans, projectlibrary.Scan{ID: scanID, RootID: rootID, RootRevision: 1,
			RootDigest: strings.Repeat("a", 64), ResultDigest: strings.Repeat("b", 64), Status: "complete",
			StartedAt: now, FinishedAt: &now})
		digest.ScanID, digest.RootID, digest.ScannedAt, digest.Coverage = scanID, rootID, now, "complete"
		doc.Digest = &digest
		doc.Revision++
		encoded, err := json.Marshal(doc)
		if err != nil {
			return err
		}
		state.ProjectLibrary = encoded
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := projectlibrary.NewStore(store).Read(scope); err != nil {
		t.Fatalf("seeded digest is not a valid Home document: %v", err)
	}
}

func TestAssistantLibraryCards_OneNavigationCardPerOwnedHomeWithWorkReady(t *testing.T) {
	handler, store, station, _ := assistantPortfolioHTTPFixture(t)
	handler.currentUserID = func(context.Context) (string, error) { return station.OwnerUserID, nil }
	state := station.GetAssistantProgramState()
	scope := projectlibrary.Scope{OwnerUserID: station.OwnerUserID, HomeID: station.ID,
		ProviderID: state.Key.PluginID, ProgramID: state.Key.ProgramID}
	if cards, err := handler.LibraryCards(t.Context()); err != nil || len(cards) != 0 {
		t.Fatalf("uninitialized Home produced a card: %+v %v", cards, err)
	}
	library := projectlibrary.NewStore(store)
	review, err := library.ReviewInitialize(scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := library.CommitInitialize(scope, review.Token, "cards-init"); err != nil {
		t.Fatal(err)
	}
	if cards, err := handler.LibraryCards(t.Context()); err != nil || len(cards) != 0 {
		t.Fatalf("a library with no digest and no suggestions produced a card: %+v %v", cards, err)
	}
	// Setup evidence missing: projects were found but nothing is ready, so the
	// scan is information on the shelf, not a card (PRD open question 2).
	seedLibraryDigest(t, store, scope, projectlibrary.LibraryDigest{Projects: 6, New: 6,
		SetupNote: "project_provider_unavailable"})
	if cards, err := handler.LibraryCards(t.Context()); err != nil || len(cards) != 0 {
		t.Fatalf("zero-ready digest produced a card: %+v %v", cards, err)
	}
	seedLibraryDigest(t, store, scope, projectlibrary.LibraryDigest{Projects: 6, New: 6, Activatable: 5,
		UnsupportedFormat: 1})
	cards, err := handler.LibraryCards(t.Context())
	if err != nil || len(cards) != 1 {
		t.Fatalf("want one card for the Home: %+v %v", cards, err)
	}
	card := cards[0]
	home, _ := store.Get(station.ID)
	if card.HomeID != station.ID || card.HomeName != home.Name || card.Activatable != 5 || card.New != 6 ||
		card.Projects != 6 || card.ReadyProposals != 0 || card.ScannedAt == nil || card.Coverage != "complete" ||
		card.Route != librarySuggestionsRoute(home) || card.Route == "" {
		t.Fatalf("card: %+v", card)
	}
	encoded, _ := json.Marshal(cards)
	if strings.Contains(string(encoded), `"path"`) || strings.Contains(string(encoded), "seed-root") {
		t.Fatalf("card leaks a path or root: %s", encoded)
	}
	handler.currentUserID = func(context.Context) (string, error) { return "other-owner", nil }
	if cards, err := handler.LibraryCards(t.Context()); err != nil || len(cards) != 0 {
		t.Fatalf("another owner saw this Home's card: %+v %v", cards, err)
	}
	handler.currentUserID = func(context.Context) (string, error) { return "", nil }
	if cards, err := handler.LibraryCards(t.Context()); err != nil || len(cards) != 0 {
		t.Fatalf("an anonymous request saw a card: %+v %v", cards, err)
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
