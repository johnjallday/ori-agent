package workspace

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type linkedOpenFixture struct {
	h        *HTTPHandler
	store    *FileStore
	id, root string
	opened   []string
}

func newLinkedOpenFixture(t *testing.T, id string) *linkedOpenFixture {
	t.Helper()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("requires supported file identity")
	}
	store, ws, h := newFolderHandlerTest(t, id, id)
	f := &linkedOpenFixture{h: h, store: store, id: ws.ID, root: t.TempDir()}
	for _, name := range []string{"Song.rpp", "Alternate.RPP", "Notes.txt", ".hidden.rpp", "Executable.rpp", "Song.als", "Song.logicx", "Song.rpp-bak"} {
		if err := os.WriteFile(filepath.Join(f.root, name), []byte("project"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(f.root, "Executable.rpp"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("Song.rpp", filepath.Join(f.root, "Pointer.rpp")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(f.root, "Bundle.rpp"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(ws.ID, func(w *Workspace) error {
		if err := w.AddDirectoryReference(DirectoryReference{ID: "project", Name: "Original", Path: f.root}); err != nil {
			return err
		}
		return SetProjectEntryLocator(w.SharedData, ProjectEntryLocator{SchemaVersion: 1, Kind: ProjectEntryDirectoryReference, DirectoryReferenceID: "project", RelativePath: "Song.rpp"})
	}); err != nil {
		t.Fatal(err)
	}
	h.linkedDesktop = true // use the Mac policy with a recording opener on CI
	h.openFile = func(path string) error { f.opened = append(f.opened, path); return nil }
	return f
}

func (f *linkedOpenFixture) list(t *testing.T, remote bool) map[string]string {
	t.Helper()
	req := httptest.NewRequest("GET", "http://localhost/entries", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	if remote {
		req.RemoteAddr = "192.0.2.1:1234"
	}
	req.SetPathValue("workspaceID", f.id)
	req.SetPathValue("dirId", "project")
	out := httptest.NewRecorder()
	f.h.ListLinkedFilePage(out, req)
	var result struct {
		Entries []struct{ Path, Selection string }
	}
	if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &result) != nil || strings.Contains(out.Body.String(), f.root) {
		t.Fatalf("listing %d %s", out.Code, out.Body.String())
	}
	items := map[string]string{}
	for _, e := range result.Entries {
		items[e.Path] = e.Selection
	}
	return items
}

func (f *linkedOpenFixture) post(t *testing.T, path, token string, change func(*http.Request)) int {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"path": path, "selection": token})
	req := httptest.NewRequest("POST", "http://localhost/open", strings.NewReader(string(body)))
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Origin", "http://localhost")
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("workspaceID", f.id)
	req.SetPathValue("dirId", "project")
	if change != nil {
		change(req)
	}
	out := httptest.NewRecorder()
	f.h.OpenLinkedFile(out, req)
	if strings.Contains(out.Body.String(), f.root) {
		t.Fatal("leaked project path")
	}
	return out.Code
}

func TestLinkedOpenDispatchesExactSelection(t *testing.T) {
	f := newLinkedOpenFixture(t, "track-2")
	rows := f.list(t, false)
	token := rows["Alternate.RPP"]
	if token == "" || rows["Song.rpp"] == "" {
		t.Fatal("eligible rows have no selection")
	}
	for _, name := range []string{"Notes.txt", "Executable.rpp", "Bundle.rpp", "Pointer.rpp", "Song.als", "Song.logicx", "Song.rpp-bak"} {
		if selection, ok := rows[name]; !ok || selection != "" {
			t.Errorf("%s must be inert", name)
		}
	}
	if _, ok := rows[".hidden.rpp"]; ok {
		t.Fatal("listed hidden project")
	}
	if remote := f.list(t, true); len(remote) != len(rows) || remote["Song.rpp"] != "" {
		t.Fatal("remote listing lost names or allows native open")
	}
	if len(f.opened) != 0 || f.post(t, "Alternate.RPP", token, nil) != 200 || len(f.opened) != 1 || f.opened[0] != filepath.Join(f.root, "Alternate.RPP") {
		t.Fatalf("dispatch: %v", f.opened)
	}
	ws, err := f.store.Get(f.id)
	if err != nil {
		t.Fatal(err)
	}
	loc, err := GetProjectEntryLocator(ws.SharedData)
	if err != nil || loc.RelativePath != "Song.rpp" {
		t.Fatalf("primary changed: %v %v", loc, err)
	}
	f.h.openFile = func(string) error { return errors.New(f.root) }
	if f.post(t, "Alternate.RPP", token, nil) != 500 {
		t.Fatal("opener failure not reported")
	}
}

func TestLinkedOpenRejectsUntrustedOrStaleSelections(t *testing.T) {
	f := newLinkedOpenFixture(t, "track-2")
	token := f.list(t, false)["Song.rpp"]
	for _, change := range []func(*http.Request){func(r *http.Request) { r.RemoteAddr = "192.0.2.1:1234" }, func(r *http.Request) { r.Header.Set("Forwarded", "for=192.0.2.1") }, func(r *http.Request) { r.Header.Set("X-Forwarded-For", "192.0.2.1") }, func(r *http.Request) { r.Header.Del("Origin") }, func(r *http.Request) { r.Header.Set("Origin", "http://localhost:8766") }, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }, func(r *http.Request) { r.Host = "example.org"; r.Header.Set("Origin", "http://example.org") }} {
		if code := f.post(t, "Song.rpp", token, change); code != 403 {
			t.Fatalf("untrusted: %d", code)
		}
	}
	// Another workspace really has a document of the same name. Never reuse
	// one selection across owners even if their reference IDs also coincide.
	other := newLinkedOpenFixture(t, "track-1")
	other.h.linkedSelectionKey = f.h.linkedSelectionKey
	if code := other.post(t, "Song.rpp", token, nil); code != 409 || len(other.opened) != 0 {
		t.Fatalf("wrong owner: %d", code)
	}
	for _, p := range []string{"Alternate.RPP", ".hidden.rpp", "Notes.txt", "Pointer.rpp", "/absolute.rpp"} {
		if code := f.post(t, p, token, nil); code != 409 {
			t.Fatalf("wrong path %s: %d", p, code)
		}
	}
	if code := f.post(t, "Song.rpp", token, func(r *http.Request) { r.SetPathValue("dirId", "other") }); code != 404 {
		t.Fatalf("wrong ref: %d", code)
	}
	ref, err := f.h.linkedFileSource(f.id, "project")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.h.resolveLinkedSelection(ref, "Song.rpp", token, time.Now().Add(2*linkedSelectionTTL)); ok {
		t.Fatal("accepted expired token")
	}
	if err := os.Rename(filepath.Join(f.root, "Song.rpp"), filepath.Join(f.root, "Saved.rpp")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("Saved.rpp", filepath.Join(f.root, "Song.rpp")); err != nil {
		t.Fatal(err)
	}
	if f.post(t, "Song.rpp", token, nil) != 409 {
		t.Fatal("accepted replacement link")
	}
	if err := os.Remove(filepath.Join(f.root, "Song.rpp")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.root, "Song.rpp"), []byte("project"), 0600); err != nil {
		t.Fatal(err)
	}
	if f.post(t, "Song.rpp", token, nil) != 409 {
		t.Fatal("accepted replacement regular file")
	}
	token = f.list(t, false)["Song.rpp"]
	if err := f.store.Update(f.id, func(w *Workspace) error { ref.Path = other.root; return w.UpdateDirectoryReference(ref) }); err != nil {
		t.Fatal(err)
	}
	if f.post(t, "Song.rpp", token, nil) != 409 {
		t.Fatal("accepted relink")
	}
	if err := f.store.Update(f.id, func(w *Workspace) error { return w.DeleteDirectoryReference("project") }); err != nil {
		t.Fatal(err)
	}
	if f.post(t, "Song.rpp", token, nil) != 404 || len(f.opened) != 0 {
		t.Fatal("stale selection launched")
	}
}
