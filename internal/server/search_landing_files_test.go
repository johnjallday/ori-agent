package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/onboarding"
)

// When Files is the starting page and a workspace exists, a bare visit to Ori
// opens the merged Files (Home's tree with its one search box). With no
// workspace the first-run page is what there is to show, and every other
// starting page, an explicit address, and a browser away from the Mac that runs
// Ori are untouched (merge-tree-into-files FR31).
func TestFilesStartingPageOpensMergedFilesOnceAWorkspaceExists(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the filename index, and with it the starting-page choice, is macOS only")
	}
	handler := newRoutesTestHandler(t)
	root := os.Getenv("ORI_DATA_DIR")
	const local, remote = "127.0.0.1:43210", "192.0.2.5:1010"

	get := func(path, peer string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil)
		req.RemoteAddr = peer
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	setLanding := func(state string) {
		t.Helper()
		if err := onboarding.ResetSearchLanding(root); err != nil {
			t.Fatal(err)
		}
		if state == onboarding.SearchFirst {
			return
		}
		if err := onboarding.DecideSearchLanding(root, onboarding.SearchFirst, state); err != nil {
			t.Fatal(err)
		}
	}
	redirectsToFiles := func(rec *httptest.ResponseRecorder) bool {
		return rec.Code == http.StatusFound && rec.Header().Get("Location") == "/?view=tree"
	}

	// No workspace yet: every state behaves as it did.
	setLanding(onboarding.SearchDesk)
	rec := get("/", local)
	if redirectsToFiles(rec) || rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `id="desk"`) {
		t.Fatalf("Files as the starting page with no workspace: status %d, want the first-run page", rec.Code)
	}

	created := createRouteTestWorkspace(t, handler, "Landing workspace")
	if created.ID == "" {
		t.Fatal("no workspace")
	}

	// Files is the starting page and a workspace exists: the merged Files.
	setLanding(onboarding.SearchDesk)
	rec = get("/", local)
	if !redirectsToFiles(rec) {
		t.Fatalf("status %d, Location %q: want a redirect to /?view=tree", rec.Code, rec.Header().Get("Location"))
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("the redirect must not be cached: Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
	// ...and that address is Home with Files marked current.
	home := get("/?view=tree", local)
	if home.Code != http.StatusOK || !strings.Contains(home.Body.String(), "home-command-bridge") {
		t.Fatalf("/?view=tree: status %d", home.Code)
	}
	if !strings.Contains(openingTagFor(t, home.Body.String(), `data-desk-destination="tree"`), `aria-current="page"`) {
		t.Error("Files is not marked current on /?view=tree")
	}

	// Not for anything else.
	for _, tc := range []struct {
		name  string
		state string
		path  string
		peer  string
	}{
		{"an explicit Map address", onboarding.SearchDesk, "/?view=map", local},
		{"an explicit Files address", onboarding.SearchDesk, "/?view=tree", local},
		{"the standalone page itself", onboarding.SearchDesk, "/search", local},
		{"a browser away from the Mac", onboarding.SearchDesk, "/", remote},
		{"Home as the starting page", onboarding.SearchLater, "/", local},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setLanding(tc.state)
			rec := get(tc.path, tc.peer)
			if redirectsToFiles(rec) {
				t.Fatalf("%s was redirected to Files", tc.name)
			}
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d", rec.Code)
			}
		})
	}

	// The first-run state keeps its own page, workspace or not.
	setLanding(onboarding.SearchFirst)
	rec = get("/", local)
	if redirectsToFiles(rec) {
		t.Fatal("the first-run state was sent to Files")
	}
}
