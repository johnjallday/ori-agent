package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/externalmemory"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// The external memory mirror reaches the hired personal assistant, wired the
// way the server wires it, and never a task run (PRD FR18, FR19): the real
// work-context adapter carries the block, the real task tool factory never
// offers memory_recall, and a paused assistant gets nothing.
func TestExternalMemoryReachesTheHiredAssistantAndNoTaskRun(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "externalmemory", "testdata", "codex"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", root)
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
	post("/api/personal-assistant/hire", []byte(`{"request_id":"em-hire","if_version":0,"display_name":"Atlas","mandate":"Help plan.","focus_areas":["plan_my_day"]}`), http.StatusCreated)
	state, err := builder.personalAssistantStore.GetState(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	hq, err := json.Marshal(map[string]any{"request_id": "em-hq", "if_version": state.StateVersion, "name": "My HQ", "timezone": "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	post("/api/personal-assistant/hq", hq, http.StatusCreated)
	if state, err = builder.personalAssistantStore.GetState(ctx, "local"); err != nil {
		t.Fatal(err)
	}
	if err := builder.workspaceStore.Save(&workspace.Workspace{ID: "album-3", Name: "Album 3"}); err != nil {
		t.Fatal(err)
	}

	// Every source on, the Codex fixture synced, through the HTTP API.
	for _, id := range externalmemory.SourceIDs {
		request := httptest.NewRequest(http.MethodPatch, "/api/external-memory/sources/"+id, strings.NewReader(`{"enabled":true}`))
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, request)
		if id != externalmemory.SourceClaudeAccount && result.Code != http.StatusOK {
			t.Fatalf("enable %s: %d %s", id, result.Code, result.Body.String())
		}
	}
	builder.externalMemoryRunner.WaitIdle()
	post("/api/external-memory/sync", nil, http.StatusOK)
	reader := builder.externalMemoryRunner.Reader()
	if !strings.Contains(reader.ContextBlock(8000), "trading research tools") {
		t.Fatal("the synced fixture should give a profile-grade block")
	}

	adapter := personalAssistantContextAdapter{
		relationship: builder.personalAssistantService, profiles: builder.userStore,
		workspaces: builder.workspaceStore, knowledge: builder.server.Storage.PersonalAssistantKnowledge,
		external: reader,
	}
	resolved, err := adapter.ResolvePersonalAssistantContext(ctx, "local")
	if err != nil || !strings.Contains(resolved.ExternalMemory, "trading research tools") ||
		resolved.Sources["external_memory"].Status != "available" {
		t.Fatalf("the hired assistant's context lacks the external block: %+v %v", resolved, err)
	}
	if !builder.personalAssistantKnowledge.IsPersonalAssistant(ctx, "local", state.HQWorkspaceID, state.HQEntryAgentInstanceID) {
		t.Fatal("the chat gate does not recognize the hired assistant in its HQ")
	}

	// Task runs (manual, scheduled, orchestrated) all build tools here.
	factory := builder.buildWorkspaceToolFactory()
	for _, workspaceID := range []string{state.HQWorkspaceID, "album-3"} {
		for _, tool := range factory(workspaceID, "Atlas", "task-1") {
			if tool.Definition().Name == workspace.MemoryRecallToolName {
				t.Fatalf("a task run in %s was offered memory_recall", workspaceID)
			}
		}
	}

	state.Status = personalassistant.StatusPaused
	if _, err := builder.personalAssistantStore.UpdateState(ctx, state, state.StateVersion); err != nil {
		t.Fatal(err)
	}
	paused, err := adapter.ResolvePersonalAssistantContext(ctx, "local")
	if err != nil || paused.ExternalMemory != "" || paused.Sources["external_memory"].Reason != "assistant_paused" {
		t.Fatalf("a paused assistant still read external memory: %+v %v", paused, err)
	}
	if paused.MayRecallExternalMemory() {
		t.Fatal("a paused assistant may still recall")
	}
}
