package settingshttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/llm"
)

// The first confirmation of a Workspace Directory moves what the user made in
// the staging folder into it, before the root is applied; later changes move
// nothing. The move's warnings reach the response.
func TestOnlyTheFirstConfirmationMovesStagedContent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WORKSPACE_DIR", "")
	t.Setenv("ORI_DATA_DIR", dir)
	configManager := config.NewManager(filepath.Join(dir, "settings.json"))
	if err := configManager.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	handler := NewHandler(nil, configManager, nil, llm.NewFactory())

	var calls []string
	handler.SetStagedContentMover(func(staging, root string) []string {
		calls = append(calls, "move "+staging+" -> "+root)
		return []string{"A folder named studio already exists in your Workspace Directory."}
	})
	handler.SetWorkspaceRootUpdater(func(root string) (WorkspaceRootRefresh, error) {
		calls = append(calls, "apply "+root)
		return WorkspaceRootRefresh{Warnings: []string{"applied"}}, nil
	})

	post := func(root string) map[string]any {
		body, _ := json.Marshal(map[string]string{"workspace_root": root})
		rec := httptest.NewRecorder()
		handler.WorkspaceRootSettingsHandler(rec, httptest.NewRequest(http.MethodPost, "/api/settings/workspace-root", bytes.NewReader(body)))
		if rec.Code != http.StatusOK {
			t.Fatalf("POST %s: %d %s", root, rec.Code, rec.Body.String())
		}
		var resp map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		return resp
	}

	first := filepath.Join(dir, "First Root")
	resp := post(first)
	if len(calls) != 2 || calls[0] != "move "+config.UnconfirmedWorkspaceRoot()+" -> "+first || calls[1] != "apply "+first {
		t.Fatalf("calls = %v, want the move before the root is applied", calls)
	}
	warnings := resp["refresh"].(map[string]any)["warnings"].([]any)
	if len(warnings) != 2 || !strings.Contains(warnings[0].(string), "already exists") {
		t.Errorf("warnings = %v, want the move's warning first", warnings)
	}

	calls = nil
	post(filepath.Join(dir, "Second Root"))
	if len(calls) != 1 || !strings.HasPrefix(calls[0], "apply ") {
		t.Errorf("a later change moved content: %v", calls)
	}
}
