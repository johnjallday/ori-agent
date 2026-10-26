package server

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func seedHireProject(t *testing.T) string {
	t.Helper()
	widget := filepath.Join(os.Getenv("HOME"), "Documents", "widget") // sandboxed by the fixture
	for _, name := range []string{"go.mod", "main.go", "README.md", ".git/HEAD"} {
		full := filepath.Join(widget, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("module widget\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return widget
}

// Attaching one project Ori can set up as a hire names it, so the drawer opens
// its review straight away; a collection proposes its group instead and names
// none. Attaching creates nothing either way.
func TestFolderHireAttach_ARecognizedProjectIsNamedForItsReview(t *testing.T) {
	f := newDraftServerFixture(t)
	seedHireProject(t)
	before := len(f.builder.workspaceFileStore.CachedWorkspaces())

	status, body := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/select",
		map[string]any{"draft_id": uuid.NewString(), "revision": "", "mode": "chip", "chip": "documents"})
	if status != http.StatusOK {
		t.Fatalf("attach: %d %v", status, body)
	}
	candidate, _ := body["hire_candidate"].(string)
	observation, _ := body["observation"].(map[string]any)
	projects, _ := observation["projects"].([]any)
	named := ""
	for _, raw := range projects {
		if project, _ := raw.(map[string]any); project["name"] == "widget" {
			named, _ = project["id"].(string)
		}
	}
	if candidate == "" || candidate != named || body["structure"] != nil {
		t.Fatalf("hire candidate = %q, widget is %q: %v", candidate, named, body)
	}
	if len(f.builder.workspaceFileStore.CachedWorkspaces()) != before {
		t.Fatal("attaching created a workspace")
	}

	// A folder of several songs is a collection: its group is proposed, and no
	// single project is reviewed for it.
	seedAlbumsCollection(t)
	status, body = f.call(t, http.MethodPost, "/api/home-assistant/folder-context/select",
		map[string]any{"draft_id": uuid.NewString(), "revision": "", "mode": "chip", "chip": "music"})
	if status != http.StatusOK || body["structure"] == nil || body["hire_candidate"] != nil {
		t.Fatalf("collection attach: %d %v", status, body)
	}
}
