//go:build darwin || linux || freebsd

package sessionhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/projectconnection"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestAssistantLibraryBrowserHTTP_ExactScopeStrictQueriesAndInertPreview(t *testing.T) {
	handler, store, home, child := assistantPortfolioHTTPFixture(t)
	handler.currentUserID = func(context.Context) (string, error) { return home.OwnerUserID, nil }
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"Project.rpp": "fixture", "page.html": "<script>window.leaked=true</script>", "large.txt": strings.Repeat("x", (512<<10)+1), "active.svg": "<svg onload=\"alert(1)\"/>"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Update(child.ID, func(ws *workspace.Workspace) error {
		return projectconnection.RecordAttachedProject(ws, "Project folder", root, "Project.rpp", "reference")
	}); err != nil {
		t.Fatal(err)
	}
	state := home.GetAssistantProgramState()
	scope := projectlibrary.Scope{OwnerUserID: home.OwnerUserID, HomeID: home.ID, ProviderID: state.Key.PluginID, ProgramID: state.Key.ProgramID}
	library := projectlibrary.NewStore(store)
	review, err := library.ReviewInitialize(scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := library.CommitInitialize(scope, review.Token, "browser-http-init"); err != nil {
		t.Fatal(err)
	}
	doc, err := library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	stat := info.Sys().(*syscall.Stat_t)
	identity, now := fmt.Sprintf("%d:%d", stat.Dev, stat.Ino), time.Now().UTC()
	doc.Roots = []projectlibrary.Root{{ID: "root", Path: root, FileIdentity: identity, Revision: 1, ApprovedAt: now, RevokedAt: &now}}
	doc.Scans = []projectlibrary.Scan{{ID: "scan", RootID: "root", RootRevision: 1, RootDigest: strings.Repeat("0", 64), ResultDigest: strings.Repeat("0", 64), Status: "complete", StartedAt: now, FinishedAt: &now}}
	doc.Entries[0].Observations = []projectlibrary.Observation{{RootID: "root", FileIdentity: identity, Format: "reaper", ScanID: "scan", ScannedAt: now, Availability: "available"}}
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(home.ID, func(ws *workspace.Workspace) error {
		state := ws.GetAssistantProgramState()
		state.ProjectLibrary = encoded
		ws.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	entryID := doc.Entries[0].ID
	get := func(route, query string, preview bool, workspaceID, entry string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, route+query, nil)
		r.SetPathValue("workspaceID", workspaceID)
		r.SetPathValue("entryID", entry)
		w := httptest.NewRecorder()
		if preview {
			handler.PreviewAssistantLibraryFile(w, r)
		} else {
			handler.ListAssistantLibraryFiles(w, r)
		}
		if strings.Contains(w.Body.String(), root) {
			t.Fatal("HTTP error leaked absolute root")
		}
		if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatal("content may be cached or sniffed")
		}
		return w
	}
	listed := get("/files", "", false, home.ID, entryID)
	if listed.Code != http.StatusOK || listed.Body.Len() > 128<<10 {
		t.Fatalf("listing: %d %s", listed.Code, listed.Body.String())
	}
	var listing projectlibrary.BrowserListing
	if err := json.Unmarshal(listed.Body.Bytes(), &listing); err != nil || len(listing.Entries) != 4 {
		t.Fatalf("listing: %+v %v", listing, err)
	}
	for _, entry := range listing.Entries {
		query := "?" + url.Values{"path": {entry.Path}, "version": {entry.Version}}.Encode()
		w := get("/preview", query, true, home.ID, entryID)
		want := http.StatusOK
		if entry.Name == "large.txt" {
			want = http.StatusRequestEntityTooLarge
		}
		if entry.Name == "Project.rpp" || entry.Name == "active.svg" {
			want = http.StatusUnsupportedMediaType
		}
		if w.Code != want {
			t.Fatalf("%s: %d %s", entry.Name, w.Code, w.Body.String())
		}
		if entry.Name == "page.html" && (w.Header().Get("Content-Type") != "text/plain; charset=utf-8" || !strings.Contains(w.Body.String(), "<script>")) {
			t.Fatal("HTML must stay inert text")
		}
	}
	for _, query := range []string{"?root=/etc", "?directory_id=reference", "?path=a&path=b", "?path=%zz", "?workspace_id=other"} {
		if w := get("/files", query, false, home.ID, entryID); w.Code != http.StatusBadRequest {
			t.Fatalf("accepted query %s: %d", query, w.Code)
		}
	}
	if w := get("/files", "?path=..%2F", false, home.ID, entryID); w.Code != http.StatusForbidden {
		t.Fatalf("traversal: %d", w.Code)
	}
	if w := get("/files", "", false, child.ID, entryID); w.Code != http.StatusNotFound {
		t.Fatalf("child used as Home: %d", w.Code)
	}
	if w := get("/files", "", false, home.ID, "foreign"); w.Code != http.StatusForbidden {
		t.Fatalf("foreign entry: %d", w.Code)
	}
	if w := get("/preview", "?path=page.html", true, home.ID, entryID); w.Code != http.StatusBadRequest {
		t.Fatalf("missing comparison witness: %d", w.Code)
	}
	if err := store.Update(child.ID, func(ws *workspace.Workspace) error { ws.DirectoryReferences = nil; return nil }); err != nil {
		t.Fatal(err)
	}
	if w := get("/files", "", false, home.ID, entryID); w.Code != http.StatusForbidden {
		t.Fatalf("lost reference: %d", w.Code)
	}
}
