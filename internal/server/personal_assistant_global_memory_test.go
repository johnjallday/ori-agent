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
	"github.com/johnjallday/ori-agent/internal/toolapi"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func globalMemoryTool(t *testing.T, tools []toolapi.Tool) toolapi.Tool {
	t.Helper()
	for _, tool := range tools {
		if tool.Definition().Name == workspace.MemoryProposeGlobalToolName {
			return tool
		}
	}
	t.Fatalf("%s is not registered", workspace.MemoryProposeGlobalToolName)
	return nil
}

func globalMemoryReply(ctx context.Context, t *testing.T, tool toolapi.Tool, args string) map[string]string {
	t.Helper()
	out, err := tool.Call(ctx, args)
	if err != nil {
		t.Fatalf("memory_propose_global: %v", err)
	}
	var reply map[string]string
	if err := json.Unmarshal([]byte(out), &reply); err != nil {
		t.Fatalf("reply %q: %v", out, err)
	}
	return reply
}

// A task run in a project workspace proposes a fact about the user; it lands
// in the Profile review queue attributed to that workspace, agent and task,
// and no MEMORY.md changes. The model cannot forge any of the attribution.
func TestWorkspaceAgentProposalReachesTheProfileReviewQueue(t *testing.T) {
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
	if err := builder.workspaceStore.Save(&workspace.Workspace{ID: "album-3", Name: "Album 3"}); err != nil {
		t.Fatal(err)
	}
	factory := builder.buildWorkspaceToolFactory()
	if factory == nil {
		t.Fatal("workspace tool factory is not wired")
	}
	propose := globalMemoryTool(t, factory("album-3", "Scout", "task-42"))
	const fact = "John delivers stems at 48 kHz."
	call := `{"text":"` + fact + `","category":"how_you_work","workspace_id":"forged-ws","source_kind":"explicit","agent":"Forged","task_id":"forged-task"}`

	// No hired assistant yet: the shared explanation, not a generic error.
	reply := globalMemoryReply(ctx, t, propose, call)
	if reply["status"] != "unavailable" || !strings.Contains(reply["message"], personalassistant.GlobalMemoryUnavailableMessage) {
		t.Fatalf("before hire = %+v", reply)
	}

	post("/api/personal-assistant/hire", []byte(`{"request_id":"global-hire","if_version":0,"display_name":"Atlas","mandate":"Help plan.","focus_areas":["plan_my_day"]}`), http.StatusCreated)
	state, err := builder.personalAssistantStore.GetState(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	hq, err := json.Marshal(map[string]any{"request_id": "global-hq", "if_version": state.StateVersion, "name": "My HQ", "timezone": "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	post("/api/personal-assistant/hq", hq, http.StatusCreated)
	state, err = builder.personalAssistantStore.GetState(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}

	if reply := globalMemoryReply(ctx, t, propose, call); reply["status"] != "proposed" || !strings.Contains(reply["message"], "memory_write") {
		t.Fatalf("propose = %+v", reply)
	}
	if reply := globalMemoryReply(ctx, t, propose, call); reply["status"] != "already_pending" {
		t.Fatalf("repeat = %+v", reply)
	}

	read := httptest.NewRecorder()
	handler.ServeHTTP(read, httptest.NewRequest(http.MethodGet, "/api/personal-assistant/knowledge", nil))
	if read.Code != http.StatusOK {
		t.Fatalf("knowledge: %d %s", read.Code, read.Body.String())
	}
	if strings.Contains(read.Body.String(), "forged") || strings.Contains(read.Body.String(), "Forged") {
		t.Fatalf("model-supplied attribution reached the review queue: %s", read.Body.String())
	}
	var review struct {
		Items []struct {
			State      string `json:"state"`
			SourceKind string `json:"source_kind"`
			Text       string `json:"text"`
			Evidence   []struct {
				SourceKind string `json:"source_kind"`
				SourceID   string `json:"source_id"`
				Summary    string `json:"summary"`
			} `json:"evidence"`
			Source struct {
				Kind          string `json:"kind"`
				WorkspaceID   string `json:"workspace_id"`
				WorkspaceName string `json:"workspace_name"`
				WorkspaceSlug string `json:"workspace_slug"`
				Agent         string `json:"agent"`
				TaskID        string `json:"task_id"`
				ObservedAt    string `json:"observed_at"`
			} `json:"source"`
		} `json:"items"`
	}
	if err := json.Unmarshal(read.Body.Bytes(), &review); err != nil {
		t.Fatalf("decode %s: %v", read.Body.String(), err)
	}
	found := false
	for _, item := range review.Items {
		if item.Text != fact {
			continue
		}
		found = true
		if item.State != "candidate" || item.SourceKind != personalassistant.WorkspaceAgentSourceKind ||
			len(item.Evidence) != 1 || item.Evidence[0].SourceID != "album-3" ||
			item.Evidence[0].Summary != "Album 3 · Scout · task task-42" {
			t.Fatalf("review item = %+v", item)
		}
		source := item.Source
		if source.Kind != personalassistant.WorkspaceAgentSourceKind || source.WorkspaceID != "album-3" ||
			source.WorkspaceName != "Album 3" || source.WorkspaceSlug == "" || source.Agent != "Scout" ||
			source.TaskID != "task-42" || source.ObservedAt == "" {
			t.Fatalf("review source chip data = %+v", source)
		}
	}
	if !found {
		t.Fatalf("proposal missing from the review queue: %s", read.Body.String())
	}

	memory := workspace.NewMemoryStore(builder.workspaceFileStore)
	for _, workspaceID := range []string{state.HQWorkspaceID, "album-3"} {
		snapshot, err := memory.SnapshotExact(workspaceID)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range snapshot.Entries {
			if entry.Entry.Text == fact {
				t.Fatalf("a proposal wrote %s MEMORY.md: %+v", workspaceID, entry)
			}
		}
	}
}
