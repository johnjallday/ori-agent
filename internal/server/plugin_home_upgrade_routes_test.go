package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// The fixture package's name must not match a reviewed integration, so its
// update target is its own local export and never a network release.
const inPlaceUpgradeFixturePlugin = "in-place-upgrade-fixture"

var pluginHomeUpgradeRoutes = []struct{ method, path, pattern string }{
	{http.MethodGet, "/api/plugins/" + inPlaceUpgradeFixturePlugin + "/home-upgrade", "GET /api/plugins/{name}/home-upgrade"},
	{http.MethodPost, "/api/plugins/" + inPlaceUpgradeFixturePlugin + "/home-upgrade/review", "POST /api/plugins/{name}/home-upgrade/review"},
}

// The plugin-addressed upgrade routes are ordinary work on the production
// router: a reset fence refuses them like any other request, because reading
// or reviewing an upgrade can settle an interrupted operation and records a
// review.
func TestPluginHomeUpgradeRoutesAreOrdinaryAdmittedWork(t *testing.T) {
	s := newRoutesTestServer(t)
	mux := http.NewServeMux()
	registerRoutes(mux, s)
	for _, route := range pluginHomeUpgradeRoutes {
		if _, pattern := mux.Handler(httptest.NewRequest(route.method, route.path, nil)); pattern != route.pattern {
			t.Errorf("%s %s resolved to %q, want %q", route.method, route.path, pattern, route.pattern)
		}
		if slices.Contains(resetStreamPatterns, route.pattern) {
			t.Errorf("%s is classified as a stream that does not block a reset fence", route.pattern)
		}
	}

	gate := &resetstate.WorkGate{}
	requireResetNoError(t, gate.TryFence(t.Context()))
	fenced := (&Server{resetWork: gate}).resetAdmissionMiddleware(mux)
	for _, route := range pluginHomeUpgradeRoutes {
		w := httptest.NewRecorder()
		fenced.ServeHTTP(w, httptest.NewRequest(route.method, route.path, strings.NewReader("{}")))
		if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "reset_pending") {
			t.Fatalf("fenced %s %s = %d %s", route.method, route.path, w.Code, w.Body.String())
		}
	}
}

// Through the production router and the real plugin manager, the Plugins
// page sees the upgrade of exactly the current owner's Homes: none for a
// package no Home of theirs uses, theirs by name and page (never by ID), and
// another owner's Home neither listed nor named.
func TestPluginHomeUpgradeRoutesServeOnlyTheOwnersHomes(t *testing.T) {
	s := newRoutesTestServer(t)
	mux := http.NewServeMux()
	registerRoutes(mux, s)
	serve := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		return w
	}
	statusPath := "/api/plugins/" + inPlaceUpgradeFixturePlugin + "/home-upgrade"

	root := t.TempDir()
	writeNamedHomePackage(t, root, inPlaceUpgradeFixturePlugin, "0.1.0", "Coordinate only reviewed Home work.")
	manager := s.Handlers.Plugin.Manager()
	confirm := func(plugin.TrustReport) bool { return true }
	installed, err := manager.Install(root, plugin.FormatClaude, confirm)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SetEnabled(installed.Name, true); err != nil {
		t.Fatal(err)
	}
	records, err := manager.List()
	if err != nil {
		t.Fatal(err)
	}
	installed = records[slices.IndexFunc(records, func(record plugin.InstalledPlugin) bool { return record.Name == inPlaceUpgradeFixturePlugin })]

	if w := serve(http.MethodGet, statusPath, ""); w.Code != http.StatusNotFound {
		t.Fatalf("a package no Home uses = %d %s", w.Code, w.Body.String())
	}
	if w := serve(http.MethodGet, "/api/plugins/not-installed/home-upgrade", ""); w.Code != http.StatusNotFound {
		t.Fatalf("an uninstalled package = %d %s", w.Code, w.Body.String())
	}

	store := s.Storage.WorkspaceStore
	if err := s.Storage.UserStore.Upsert(t.Context(), &userprofile.UserProfile{ID: "someone-else"}); err != nil {
		t.Fatal(err)
	}
	theirs := seedPinnedHome(t, store, installed, "someone-else", "Their Studio")
	for _, route := range pluginHomeUpgradeRoutes {
		w := serve(route.method, route.path, "{}")
		if w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), "Their Studio") || strings.Contains(w.Body.String(), theirs.ID) {
			t.Fatalf("only another owner's Home: %s = %d %s", route.pattern, w.Code, w.Body.String())
		}
	}

	ours := seedPinnedHome(t, store, installed, userprofile.LocalUserID, "Music Production Home")
	w := serve(http.MethodGet, statusPath, "")
	var status struct {
		PluginID         string `json:"plugin_id"`
		InstalledVersion string `json:"installed_version"`
		Available        bool   `json:"available"`
		Homes            []struct {
			Name string `json:"name"`
			Link string `json:"link"`
		} `json:"homes"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &status) != nil {
		t.Fatalf("status = %d %s", w.Code, w.Body.String())
	}
	wantLink := "/workspaces/" + ours.FolderSlug + "/assistant"
	if status.PluginID != inPlaceUpgradeFixturePlugin || status.InstalledVersion != "0.1.0" || status.Available ||
		len(status.Homes) != 1 || status.Homes[0].Name != "Music Production Home" || ours.FolderSlug == "" || status.Homes[0].Link != wantLink {
		t.Fatalf("status = %s (want one Home linked at %q)", w.Body.String(), wantLink)
	}
	for _, private := range []string{ours.ID, theirs.ID, "Their Studio"} {
		if strings.Contains(w.Body.String(), private) {
			t.Fatalf("status disclosed %q: %s", private, w.Body.String())
		}
	}

	// A newer local export exists; the review covers every Home the package
	// backs, so another owner's Home refuses it without naming that Home.
	writeNamedHomePackage(t, root, inPlaceUpgradeFixturePlugin, "0.1.1", "Coordinate reviewed Home work and the approved project library.")
	w = serve(http.MethodPost, statusPath+"/review", "{}")
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"code":"home_upgrade_other_owner"`) ||
		strings.Contains(w.Body.String(), "Their Studio") || strings.Contains(w.Body.String(), theirs.ID) {
		t.Fatalf("review with another owner's Home = %d %s", w.Code, w.Body.String())
	}
	if after, _ := manager.List(); after[slices.IndexFunc(after, func(record plugin.InstalledPlugin) bool { return record.Name == inPlaceUpgradeFixturePlugin })].Version != "0.1.0" {
		t.Fatal("a refused review installed the newer release")
	}
}

// seedPinnedHome creates an independent Home owned by owner and pinned to the
// installed package, as a group template commit would.
func seedPinnedHome(t *testing.T, store workspace.Store, installed plugin.InstalledPlugin, owner, name string) *workspace.Workspace {
	t.Helper()
	declaration := installed.WorkspaceSurfaces.AssistantProgramHomes[0]
	pin := workspace.AssistantProgramHomeOwner{
		PluginID: installed.Name, PluginVersion: installed.Version, ProgramID: declaration.ID,
		HomeSchemaVersion: declaration.SchemaVersion, HomeVersion: declaration.Version,
		DeclarationDigest: projecttemplates.AssistantProgramHomeDigest(declaration),
		PluginGeneration:  installed.EvidenceGeneration(), ComponentFingerprint: installed.ComponentFingerprint,
	}
	key := workspace.AssistantProgramKey{OwnerUserID: owner, PluginID: installed.Name, ProgramID: declaration.ID}
	home, _, err := workspace.NewAssistantProgramStore(store).EnsureNamedIndependentStation(key, declaration.AssistantProgram(), name, pin)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := store.Get(home.ID)
	if err != nil || stored.OwnerUserID != owner {
		t.Fatalf("seeded Home %q owner = %q, %v", name, stored.OwnerUserID, err)
	}
	return stored
}
