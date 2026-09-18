package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

// avatarServer serves /avatars/ for a root agent Scout whose appearance names
// Scout.png, kept in its own folder.
func avatarServer(t *testing.T) (*Server, string) {
	t.Helper()
	dataDir := t.TempDir()
	t.Setenv("ORI_DATA_DIR", dataDir)
	root := t.TempDir()
	defaults := types.Settings{Model: "gpt-4o-mini"}
	system, err := store.NewFileStore(filepath.Join(dataDir, "agents.json"), defaults)
	if err != nil {
		t.Fatalf("system store: %v", err)
	}
	c, err := store.NewCompositeStore(system, root, filepath.Join(dataDir, "agent_state"), defaults)
	if err != nil {
		t.Fatalf("composite: %v", err)
	}
	if err := c.CreateAgent("Scout", nil); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	ag, _ := c.GetAgent("Scout")
	ag.EnsureAppearance()
	ag.Appearance.SetUpload("Scout.png")
	if err := c.SetAgent("Scout", ag); err != nil {
		t.Fatalf("SetAgent: %v", err)
	}
	folder := filepath.Join(root, "Agents", "Scout")
	if err := os.WriteFile(filepath.Join(folder, "Scout.png"), []byte("png"), 0o600); err != nil {
		t.Fatalf("write image: %v", err)
	}
	return &Server{Storage: &StorageSystemFacade{AgentStore: c}}, folder
}

func getAvatar(s *Server, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	s.serveAvatarFiles(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestAvatarsAreServedFromTheAgentsFolder(t *testing.T) {
	s, folder := avatarServer(t)
	rec := getAvatar(s, "/avatars/Scout.png")
	if rec.Code != http.StatusOK || rec.Body.String() != "png" || rec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("GET = %d %q %q", rec.Code, rec.Body.String(), rec.Header().Get("Content-Type"))
	}

	if err := os.Remove(filepath.Join(folder, "Scout.png")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if rec := getAvatar(s, "/avatars/Scout.png"); rec.Code != http.StatusNotFound {
		t.Errorf("a missing image = %d, want 404", rec.Code)
	}
}

func TestAvatarServingRefusesTraversalAndNonImages(t *testing.T) {
	s, _ := avatarServer(t)
	if rec := getAvatar(s, "/avatars/../Scout/Scout.png"); rec.Code != http.StatusBadRequest && rec.Code != http.StatusNotFound {
		t.Errorf("traversal = %d", rec.Code)
	}
	if rec := getAvatar(s, "/avatars/agent_settings.json"); rec.Code != http.StatusNotFound {
		t.Errorf("the definition file = %d, want 404", rec.Code)
	}
	if rec := getAvatar(s, "/avatars/a%2F..%2Fb.png"); rec.Code == http.StatusOK {
		t.Errorf("an encoded separator was served")
	}
}
