package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

func TestPersonalHQPageEntryLinksRequireCurrentHiredHQAndEntryAgent(t *testing.T) {
	builder, handler := newDailyBriefTestServer(t)
	ctx := context.Background()
	get := func(path string) string {
		t.Helper()
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s: %d %s", path, response.Code, response.Body.String())
		}
		return response.Body.String()
	}
	post := func(path, payload string) []byte {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(payload))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusCreated {
			t.Fatalf("POST %s: %d %s", path, response.Code, response.Body.String())
		}
		return response.Body.Bytes()
	}
	root := httptest.NewRecorder()
	rootRequest := httptest.NewRequest(http.MethodPost, "/api/settings/workspace-root", bytes.NewBufferString(`{"workspace_root":""}`))
	rootRequest.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(root, rootRequest) // Confirm isolated test HOME before building HQ.
	if root.Code != http.StatusOK {
		t.Fatalf("confirm workspace root: %d %s", root.Code, root.Body.String())
	}
	hired := post("/api/personal-assistant/hire", `{"request_id":"entry-hire","if_version":0,"display_name":"Atlas","mandate":"Help plan.","focus_areas":["plan_my_day"]}`)
	var hire struct {
		PersonalAssistant struct {
			StateVersion int64 `json:"state_version"`
		} `json:"personal_assistant"`
	}
	if err := json.Unmarshal(hired, &hire); err != nil || hire.PersonalAssistant.StateVersion < 1 {
		t.Fatalf("hire state: %s (%v)", hired, err)
	}
	payload, err := json.Marshal(map[string]any{"request_id": "entry-hq", "if_version": hire.PersonalAssistant.StateVersion, "name": "My HQ", "timezone": "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	post("/api/personal-assistant/hq", string(payload))

	projection, err := builder.personalAssistantService.Get(ctx, "local")
	if err != nil || projection.State != personalassistant.APIStateActive {
		t.Fatalf("relationship not active: %+v %v", projection, err)
	}
	ws, err := builder.workspaceFileStore.GetFolderWorkspace(projection.HQWorkspaceID)
	if err != nil || ws == nil || ws.FolderSlug == "" {
		t.Fatalf("HQ workspace missing: %+v %v", ws, err)
	}
	workspacePath := "/workspaces/" + ws.FolderSlug
	if !strings.Contains(get(workspacePath), `id="personalHQKnowledgeEntry"`) ||
		!strings.Contains(get(workspacePath), `href="/profile#personalHQInterview"`) {
		t.Fatal("designated HQ workspace must offer the dossier and interview")
	}
	entry := workspacePath + "/agents/Atlas"
	if !strings.Contains(get(entry), `id="personalHQAgentKnowledgeEntry"`) ||
		!strings.Contains(get(entry), `href="/profile#personalHQKnowledge"`) {
		t.Fatal("the current HQ entry agent must link to its reviewed dossier")
	}
	if strings.Contains(get(workspacePath+"/agents/Scout"), `id="personalHQAgentKnowledgeEntry"`) {
		t.Fatal("a different agent name must not inherit the HQ entry link")
	}
	if !strings.Contains(get("/agents/Atlas"), `id="hiredAssistantKnowledgeEntry"`) ||
		strings.Contains(get("/agents/Scout"), `id="hiredAssistantKnowledgeEntry"`) {
		t.Fatal("only the current hired global assistant may expose the dossier entry link")
	}
	srv := &Server{Storage: &StorageSystemFacade{PersonalAssistant: builder.personalAssistantService}}
	if srv.currentPersonalHQForPage("foreign-workspace", "") {
		t.Fatal("another workspace inherited the HQ dossier link")
	}
	state, err := builder.personalAssistantStore.GetState(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	state.Status = personalassistant.StatusPaused
	if _, err := builder.personalAssistantStore.UpdateState(ctx, state, state.StateVersion); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(get(entry), `id="personalHQAgentKnowledgeEntry"`) {
		t.Fatal("paused users must still be able to review or forget their knowledge")
	}
	if err := builder.userStore.(*userprofile.SQLiteStore).SetPersonalWorkspaceID(ctx, "local", ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(get(entry), `id="personalHQAgentKnowledgeEntry"`) ||
		strings.Contains(get(workspacePath), `id="personalHQKnowledgeEntry"`) ||
		strings.Contains(get("/agents/Atlas"), `id="hiredAssistantKnowledgeEntry"`) {
		t.Fatal("a revoked HQ designation must not leave stale dossier entry links")
	}
}
