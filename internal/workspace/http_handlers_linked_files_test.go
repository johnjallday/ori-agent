package workspace

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLinkedFileSourceBoundary(t *testing.T) {
	store, ws, handler := newFolderHandlerTest(t, "track-2", "Track-2")
	own, err := store.GetFolderPath(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := store.Update(ws.ID, func(w *Workspace) error {
		w.SharedData = map[string]any{AnchorDirectoryReferenceIDKey: "anchor"}
		w.DirectoryReferences = []DirectoryReference{
			{ID: "anchor", WorkspaceID: w.ID, Path: root, Name: "Music"},
			{ID: "own", WorkspaceID: w.ID, Path: own},
			{ID: "files", WorkspaceID: w.ID, Path: filepath.Join(own, "files")},
			{ID: "foreign", WorkspaceID: "track-1", Path: root},
			{ID: "catalog", WorkspaceID: w.ID, Path: root, Purpose: "sample_library"},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	response := map[string]any{}
	handler.addLinkedFileSources(ws.ID, response)
	body, _ := json.Marshal(response)
	if string(body) != `{"linked_sources":[{"id":"anchor","name":"Music"}]}` {
		t.Fatalf("sources = %s", body)
	}
	for _, id := range []string{"own", "foreign", "catalog", "files", "unknown"} {
		if _, err := handler.linkedFileSource(ws.ID, id); err == nil {
			t.Fatalf("admitted %s", id)
		}
	}
	page := func(path string) (int, string) {
		r := httptest.NewRequest("GET", "/?path="+path, nil)
		r.SetPathValue("workspaceID", ws.ID)
		r.SetPathValue("dirId", "anchor")
		out := httptest.NewRecorder()
		handler.ListLinkedFilePage(out, r)
		return out.Code, out.Body.String()
	}
	if err := os.Mkdir(filepath.Join(root, "Album"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".secret", "workspace.json", "Album/Song.rpp"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	code, listing := page("")
	if code != 200 || !strings.Contains(listing, `"Album"`) || strings.Contains(listing, ".secret") || strings.Contains(listing, "workspace.json") || strings.Contains(listing, "Song.rpp") {
		t.Fatalf("root %d %s", code, listing)
	}
	if code, listing := page("Album"); code != 200 || !strings.Contains(listing, "Album/Song.rpp") {
		t.Fatalf("child %d %s", code, listing)
	}
	if err := os.Remove(filepath.Join(root, "Album/Song.rpp")); err != nil {
		t.Fatal(err)
	}
	if code, listing := page("Album"); code != 200 || strings.Contains(listing, "Song.rpp") {
		t.Fatalf("empty %d %s", code, listing)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if code, listing := page(""); code != 404 || strings.Contains(listing, root) {
		t.Fatalf("missing %d %s", code, listing)
	}
}

func TestLinkedFilePageCapsInspectedEntries(t *testing.T) {
	root := t.TempDir()
	for i := 0; i <= DirectoryListMaxEntries; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("%04d", i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	listing, err := listContainedPage(root, "")
	if err != nil || !listing.Truncated || len(listing.Entries) != DirectoryListMaxEntries {
		t.Fatalf("partial=%v count=%d err=%v", listing.Truncated, len(listing.Entries), err)
	}
}
