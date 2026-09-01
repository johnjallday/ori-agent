package workspacesurfacehttp

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspacesurface"
)

// stubDashboardSource stands in for the real per-workspace resolver so this
// package's tests stay independent of workspace-folder layout.
type stubDashboardSource struct {
	byWorkspace map[string]workspacesurface.Binding
	failFor     map[string]bool
}

const stubDashboardKey = "user:ori.dashboard:dashboard:main"

func (s stubDashboardSource) descriptor(available bool) workspacesurface.RegisteredSurface {
	surface := workspacesurface.Surface{
		ID: "main", Label: "Dashboard", Description: "Your own dashboard for this workspace.",
		Icon: workspacesurface.Icon{Kind: "host", Value: "grid"}, Placement: "map_modal",
		Modal:   workspacesurface.Modal{Width: 1200, Height: 800},
		Polling: workspacesurface.Polling{MapSeconds: 60, OpenSeconds: 60},
	}
	registered := workspacesurface.RegisteredSurface{
		Key: stubDashboardKey,
		Owner: workspacesurface.Owner{
			Kind: workspacesurface.OwnerUser, ID: "ori.dashboard", Version: "1",
			Generation: 1, ProtocolMin: 1, ProtocolMax: 1,
		},
		Capability: workspacesurface.Capability{
			ID: "dashboard", Version: 1,
			Display:  workspacesurface.Display{Name: "Dashboard"},
			Surfaces: []workspacesurface.Surface{surface},
		},
		Surface: surface, Available: available,
	}
	if !available {
		registered.UnavailableCode = "dashboard_unavailable"
	}
	return registered
}

func (s stubDashboardSource) Resolve(workspaceID string) (workspacesurface.RegisteredSurface, workspacesurface.Binding, bool, error) {
	if s.failFor[workspaceID] {
		return s.descriptor(false), workspacesurface.Binding{}, true, errors.New("dashboard is broken")
	}
	binding, ok := s.byWorkspace[workspaceID]
	if !ok {
		return workspacesurface.RegisteredSurface{}, workspacesurface.Binding{}, false, nil
	}
	return s.descriptor(true), binding, true, nil
}

func dashboardBinding(t *testing.T, assetRoot string, runtime workspacesurface.Runtime) workspacesurface.Binding {
	t.Helper()
	return workspacesurface.Binding{
		CapabilityID: "dashboard", SurfaceID: "main",
		AssetRoot: assetRoot, AssetVersion: "d1-abc123", EntryAsset: "index.html",
		Operations: map[string]workspacesurface.Operation{}, Runtime: runtime,
	}
}

// The whole point of routing Catalog and eligibleSurface through one source: a
// surface that lists must also open, and a surface that does not list must not
// open either.
func TestDashboardCatalogAndEligibilityAgree(t *testing.T) {
	fixture := newPrototypeHTTPFixture(t)
	dashboardRoot := t.TempDir()
	writeDashboardEntry(t, dashboardRoot, "<!doctype html><title>Mine</title>")
	fixture.handler.SetDashboardSource(stubDashboardSource{
		byWorkspace: map[string]workspacesurface.Binding{
			fixture.workspaceID: dashboardBinding(t, dashboardRoot, fixture.runtime),
		},
	})

	catalog := fixture.catalogFor(t, fixture.workspaceID)
	var listed *catalogSurface
	for i := range catalog.Surfaces {
		if catalog.Surfaces[i].Key == stubDashboardKey {
			listed = &catalog.Surfaces[i]
		}
	}
	if listed == nil {
		t.Fatalf("dashboard missing from catalog: %+v", catalog.Surfaces)
	}
	if !listed.Available || listed.Label != "Dashboard" {
		t.Fatalf("catalog dashboard = %+v", *listed)
	}

	// Listed, therefore openable.
	path := "/api/workspaces/" + fixture.workspaceID + "/surfaces/" + url.PathEscape(stubDashboardKey) + "/sessions"
	recorder := fixture.serve(http.MethodPost, path, "")
	if recorder.Code != http.StatusCreated {
		t.Fatalf("open dashboard = %d %s", recorder.Code, recorder.Body.String())
	}

	// Not listed for a workspace with no dashboard, therefore not openable there.
	other := fixture.catalogFor(t, "workspace-unattached")
	for _, item := range other.Surfaces {
		if item.Key == stubDashboardKey {
			t.Fatalf("a workspace without a dashboard listed one: %+v", item)
		}
	}
	otherPath := "/api/workspaces/workspace-unattached/surfaces/" + url.PathEscape(stubDashboardKey) + "/sessions"
	if recorder := fixture.serve(http.MethodPost, otherPath, ""); recorder.Code != http.StatusNotFound {
		t.Fatalf("opened a dashboard in a workspace that has none: %d", recorder.Code)
	}
}

// FR9: workspace A's dashboard must be unreachable from workspace B, including
// by presenting A's key while asking about B.
func TestDashboardIsScopedToItsOwnWorkspace(t *testing.T) {
	fixture := newPrototypeHTTPFixture(t)
	dashboardRoot := t.TempDir()
	writeDashboardEntry(t, dashboardRoot, "<!doctype html><title>Owned</title>")
	fixture.handler.SetDashboardSource(stubDashboardSource{
		byWorkspace: map[string]workspacesurface.Binding{
			fixture.workspaceID: dashboardBinding(t, dashboardRoot, fixture.runtime),
		},
	})

	surface, _, ok := fixture.handler.eligibleSurface(t.Context(), "workspace-unattached", stubDashboardKey)
	if ok || surface.Key != "" {
		t.Fatalf("eligibleSurface leaked a dashboard across workspaces: %+v", surface)
	}
}

// A broken dashboard is listed as unavailable and refuses to open. The user has
// to see that their file was found and rejected.
func TestBrokenDashboardIsVisibleButUnopenable(t *testing.T) {
	fixture := newPrototypeHTTPFixture(t)
	fixture.handler.SetDashboardSource(stubDashboardSource{
		failFor: map[string]bool{fixture.workspaceID: true},
	})

	catalog := fixture.catalogFor(t, fixture.workspaceID)
	var listed *catalogSurface
	for i := range catalog.Surfaces {
		if catalog.Surfaces[i].Key == stubDashboardKey {
			listed = &catalog.Surfaces[i]
		}
	}
	if listed == nil {
		t.Fatal("a broken dashboard vanished from the catalog")
	}
	if listed.Available || listed.Unavailable != "dashboard_unavailable" {
		t.Fatalf("broken dashboard = %+v", *listed)
	}

	path := "/api/workspaces/" + fixture.workspaceID + "/surfaces/" + url.PathEscape(stubDashboardKey) + "/sessions"
	if recorder := fixture.serve(http.MethodPost, path, ""); recorder.Code != http.StatusNotFound {
		t.Fatalf("opened a broken dashboard: %d %s", recorder.Code, recorder.Body.String())
	}
}

// FR4: a handler with no dashboard source behaves exactly as it did before user
// dashboards existed.
func TestHandlerWithoutDashboardSourceIsUnchanged(t *testing.T) {
	fixture := newPrototypeHTTPFixture(t)
	catalog := fixture.catalogFor(t, fixture.workspaceID)
	if len(catalog.Surfaces) != 1 || catalog.Surfaces[0].Key != fixture.surface.Key {
		t.Fatalf("catalog without a dashboard source = %+v", catalog.Surfaces)
	}
	path := "/api/workspaces/" + fixture.workspaceID + "/surfaces/" + url.PathEscape(stubDashboardKey) + "/sessions"
	if recorder := fixture.serve(http.MethodPost, path, ""); recorder.Code != http.StatusNotFound {
		t.Fatalf("dashboard key resolved with no source installed: %d", recorder.Code)
	}
}

// The frame asset route is reused unchanged, so a dashboard's HTML must arrive
// with the same sandboxing headers a plugin surface gets.
func TestDashboardFrameAssetKeepsSandboxHeaders(t *testing.T) {
	fixture := newPrototypeHTTPFixture(t)
	dashboardRoot := t.TempDir()
	writeDashboardEntry(t, dashboardRoot, "<!doctype html><title>Mine</title>")
	fixture.handler.SetDashboardSource(stubDashboardSource{
		byWorkspace: map[string]workspacesurface.Binding{
			fixture.workspaceID: dashboardBinding(t, dashboardRoot, fixture.runtime),
		},
	})

	path := "/api/workspaces/" + fixture.workspaceID + "/surfaces/" + url.PathEscape(stubDashboardKey) + "/sessions"
	recorder := fixture.serve(http.MethodPost, path, "")
	if recorder.Code != http.StatusCreated {
		t.Fatalf("open dashboard = %d %s", recorder.Code, recorder.Body.String())
	}
	var session openSessionResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}

	frame := fixture.serve(http.MethodGet, session.FrameURL, "")
	if frame.Code != http.StatusOK {
		t.Fatalf("frame asset = %d %s", frame.Code, frame.Body.String())
	}
	if body := frame.Body.String(); body != "<!doctype html><title>Mine</title>" {
		t.Fatalf("frame body = %q", body)
	}
	headers := map[string]string{
		"Content-Type":                "text/html; charset=utf-8",
		"X-Content-Type-Options":      "nosniff",
		"Referrer-Policy":             "no-referrer",
		"Cache-Control":               "no-store",
		"Access-Control-Allow-Origin": "null",
	}
	for header, want := range headers {
		if got := frame.Header().Get(header); got != want {
			t.Fatalf("%s = %q, want %q", header, got, want)
		}
	}
	// connect-src 'none' is the reason a pasted dashboard cannot exfiltrate.
	csp := frame.Header().Get("Content-Security-Policy")
	for _, directive := range []string{"default-src 'none'", "connect-src 'none'", "frame-ancestors 'self'"} {
		if !strings.Contains(csp, directive) {
			t.Fatalf("CSP %q is missing %q", csp, directive)
		}
	}
}

func writeDashboardEntry(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *prototypeHTTPFixture) catalogFor(t *testing.T, workspaceID string) catalogResponse {
	t.Helper()
	recorder := f.serve(http.MethodGet, "/api/workspaces/"+workspaceID+"/surfaces", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("catalog(%s) = %d %s", workspaceID, recorder.Code, recorder.Body.String())
	}
	var response catalogResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response
}
