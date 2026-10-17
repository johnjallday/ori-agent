package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

type globalMemoryReviewItem struct {
	ID      string `json:"id"`
	Version int64  `json:"version"`
	State   string `json:"state"`
	Text    string `json:"text"`
	Source  struct {
		Kind          string `json:"kind"`
		WorkspaceName string `json:"workspace_name"`
		WorkspaceSlug string `json:"workspace_slug"`
	} `json:"source"`
}

func readGlobalMemoryItem(t *testing.T, handler http.Handler, text string) globalMemoryReviewItem {
	t.Helper()
	read := httptest.NewRecorder()
	handler.ServeHTTP(read, httptest.NewRequest(http.MethodGet, "/api/personal-assistant/knowledge", nil))
	if read.Code != http.StatusOK {
		t.Fatalf("knowledge: %d %s", read.Code, read.Body.String())
	}
	var review struct {
		Items []globalMemoryReviewItem `json:"items"`
	}
	if err := json.Unmarshal(read.Body.Bytes(), &review); err != nil {
		t.Fatal(err)
	}
	for _, item := range review.Items {
		if item.Text == text {
			return item
		}
	}
	t.Fatalf("%q is not in the review: %s", text, read.Body.String())
	return globalMemoryReviewItem{}
}

// Approving a workspace agent's proposal makes it global memory: the
// assistant's work context (Home ask and the workspace-page drawer both
// resolve it) carries the fact under the global heading, and it survives the
// deletion of the workspace that proposed it (global-memory FR 3, 4, 13).
func TestApprovedWorkspaceFactReachesTheAssistantAndOutlivesItsWorkspace(t *testing.T) {
	builder, handler := newDailyBriefTestServer(t)
	ctx := context.Background()
	post := func(path string, body []byte, want int) {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, request)
		if result.Code != want {
			t.Fatalf("%s: %d %s", path, result.Code, result.Body.String())
		}
	}
	post("/api/settings/workspace-root", []byte(`{"workspace_root":""}`), http.StatusOK)
	post("/api/personal-assistant/hire", []byte(`{"request_id":"approve-hire","if_version":0,"display_name":"Atlas","mandate":"Help plan.","focus_areas":["plan_my_day"]}`), http.StatusCreated)
	state, err := builder.personalAssistantStore.GetState(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	hq, err := json.Marshal(map[string]any{"request_id": "approve-hq", "if_version": state.StateVersion, "name": "My HQ", "timezone": "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	post("/api/personal-assistant/hq", hq, http.StatusCreated)
	state, err = builder.personalAssistantStore.GetState(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if err := builder.workspaceStore.Save(&workspace.Workspace{ID: "album-3", Name: "Album 3"}); err != nil {
		t.Fatal(err)
	}
	const fact = "John delivers stems at 48 kHz."
	propose := globalMemoryTool(t, builder.buildWorkspaceToolFactory()("album-3", "Scout", "task-42"))
	call := `{"text":"` + fact + `","category":"how_you_work"}`
	if reply := globalMemoryReply(ctx, t, propose, call); reply["status"] != "proposed" {
		t.Fatalf("propose = %+v", reply)
	}

	adapter := personalAssistantContextAdapter{
		relationship: builder.personalAssistantService, profiles: builder.userStore,
		workspaces: builder.workspaceStore, knowledge: builder.server.Storage.PersonalAssistantKnowledge,
	}
	pending, err := adapter.ResolvePersonalAssistantContext(ctx, "local")
	if err != nil || strings.Contains(pending.HQMemory, fact) {
		t.Fatalf("a pending proposal reached the assistant: %+v %v", pending, err)
	}

	candidate := readGlobalMemoryItem(t, handler, fact)
	approve, err := json.Marshal(map[string]any{"version": candidate.Version, "request_id": "approve-workspace-fact"})
	if err != nil {
		t.Fatal(err)
	}
	post("/api/personal-assistant/knowledge/"+candidate.ID+"/approve", approve, http.StatusOK)

	assertGlobal := func(when string) {
		t.Helper()
		resolved, err := adapter.ResolvePersonalAssistantContext(ctx, "local")
		if err != nil || !strings.Contains(resolved.HQMemory, "## Global memory (Personal HQ, user-approved)") ||
			!strings.Contains(resolved.HQMemory, fact) ||
			resolved.Sources["reviewed_personal_hq_memory"].Status != "available" {
			t.Fatalf("%s: approved fact missing from the assistant's context: %+v %v", when, resolved, err)
		}
	}
	assertGlobal("after approval")
	snapshot, err := workspace.NewMemoryStore(builder.workspaceFileStore).SnapshotExact(state.HQWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	marked := false
	for _, entry := range snapshot.Entries {
		if entry.Entry.Text == fact && workspace.IsHQManagedProvenance(entry.Entry.Provenance) {
			marked = true
		}
	}
	if !marked {
		t.Fatalf("approval did not write a marked HQ line: %+v", snapshot.Entries)
	}
	if reply := globalMemoryReply(ctx, t, propose, call); reply["status"] != "already_known" {
		t.Fatalf("re-proposing an approved fact = %+v", reply)
	}

	if err := builder.workspaceStore.Delete("album-3"); err != nil {
		t.Fatal(err)
	}
	assertGlobal("after the source workspace was deleted")
	approved := readGlobalMemoryItem(t, handler, fact)
	if approved.State != "approved" || approved.Source.Kind != "workspace_agent" ||
		approved.Source.WorkspaceName != "Album 3" || approved.Source.WorkspaceSlug != "" {
		t.Fatalf("approved fact after its workspace was deleted = %+v", approved)
	}
}
