package sessionhttp

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The workspace root's "Agents" folder holds the user's agents, so the API
// must refuse a top-level workspace that would take it — before the SQLite
// record exists, since a folder failure is otherwise treated as non-fatal.
func TestCreatingAWorkspaceNamedAgentsIsABadRequest(t *testing.T) {
	root := t.TempDir()
	handler, _ := newUpdateRenameHandler(t, root)

	rr := httptest.NewRecorder()
	handler.HandleWorkspaces(rr, httptest.NewRequest(http.MethodPost, "/api/workspaces", strings.NewReader(`{"name":"Agents"}`)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("create Agents: status %d body %s, want 400", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `\"Agents\" is reserved for your agents folder. Choose another name.`) {
		t.Errorf("body = %s", rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "agents")); !os.IsNotExist(err) {
		t.Error("the refused create made the folder")
	}

	id := createTestWorkspace(t, handler, "Scouting")
	rr = httptest.NewRecorder()
	handler.HandleWorkspaces(rr, httptest.NewRequest(http.MethodPost, "/api/workspaces/"+id+"/rename", strings.NewReader(`{"name":"AGENTS"}`)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("rename to AGENTS: status %d body %s, want 400", rr.Code, rr.Body.String())
	}
}

// <root>/Skills holds the installed skills and is refused with its own message.
func TestCreatingOrRenamingAWorkspaceToSkillsIsABadRequest(t *testing.T) {
	const message = `\"Skills\" is reserved for your skills folder. Choose another name.`
	root := t.TempDir()
	handler, _ := newUpdateRenameHandler(t, root)

	rr := httptest.NewRecorder()
	handler.HandleWorkspaces(rr, httptest.NewRequest(http.MethodPost, "/api/workspaces", strings.NewReader(`{"name":"Skills"}`)))
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), message) {
		t.Fatalf("create Skills: status %d body %s, want 400 with the Skills message", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "skills")); !os.IsNotExist(err) {
		t.Error("the refused create made the folder")
	}

	id := createTestWorkspace(t, handler, "Scouting")
	rr = httptest.NewRecorder()
	handler.HandleWorkspaces(rr, httptest.NewRequest(http.MethodPost, "/api/workspaces/"+id+"/rename", strings.NewReader(`{"name":"skills"}`)))
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), message) {
		t.Fatalf("rename to skills: status %d body %s, want 400 with the Skills message", rr.Code, rr.Body.String())
	}
}
