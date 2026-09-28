package server

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

func TestWorkspaceAvatarHTTPUsesExactProfileWithoutGlobalFallback(t *testing.T) {
	s, _ := avatarServer(t) // The unrelated global Scout.png deliberately differs.
	files, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = files.Close() })
	s.Storage.WorkspaceStore = workspace.NewAgentSnapshotStore(files, nil)
	for _, name := range []string{"First", "Second"} {
		ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: name, Agents: []string{"Scout"}})
		ws.OwnerUserID = "local"
		if err := files.Save(ws); err != nil {
			t.Fatal(err)
		}
		ag := &agent.Agent{Appearance: types.NewAgentAppearance()}
		ag.Appearance.SetUpload("Scout.png")
		if err := files.SaveWorkspaceAgent(ws.ID, "Scout", ag); err != nil {
			t.Fatal(err)
		}
		folder, err := files.GetFolderPath(ws.ID)
		if err != nil {
			t.Fatal(err)
		}
		img := image.NewRGBA(image.Rect(0, 0, 1, 1))
		img.SetRGBA(0, 0, color.RGBA{R: name[0], A: 255})
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, img); err != nil {
			t.Fatal(err)
		}
		location := "agents/scout/appearance/Scout.png"
		if err := workspacecontinuity.ReplaceCanonicalFile(t.Context(), folder, location, "", encoded.Bytes()); err != nil {
			t.Fatal(err)
		}
		query := url.Values{"studio_id": {ws.ID}, "agent": {"Scout"}}
		uri := "/avatars/Scout.png?" + query.Encode()
		response := getAvatar(s, uri)
		if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), encoded.Bytes()) {
			t.Fatalf("scoped avatar: %d", response.Code)
		}
		if response.Header().Get("Cache-Control") != "private, no-store" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatal("workspace content was served as a public asset")
		}
		head := httptest.NewRecorder()
		s.serveAvatarFiles(head, httptest.NewRequest(http.MethodHead, uri, nil))
		if head.Code != http.StatusOK || head.Body.Len() != 0 {
			t.Fatal("HEAD returned image bytes")
		}
		if err := os.Remove(filepath.Join(folder, filepath.FromSlash(location))); err != nil {
			t.Fatal(err)
		}
		if response := getAvatar(s, uri); response.Code != http.StatusNotFound {
			t.Fatal("missing scoped bytes fell back to the global portrait", response.Code)
		}
	}
	for _, query := range []string{"studio_id=unknown&agent=Scout", "studio_id=", "studio_id=%zz", "agent=Scout", "studio_id=a&studio_id=b&agent=Scout", "studio_id=a&agent=../Scout"} {
		if response := getAvatar(s, "/avatars/Scout.png?"+query); response.Code == http.StatusOK {
			t.Fatal("invalid scope fell back to global", query)
		}
	}
	// The unrelated native/global URL remains compatible.
	if response := getAvatar(s, "/avatars/Scout.png"); response.Code != http.StatusOK || response.Body.String() != "png" {
		t.Fatal("scoped serving changed the global route")
	}
}
