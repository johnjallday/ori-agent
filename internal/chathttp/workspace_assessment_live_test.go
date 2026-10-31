package chathttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/client"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Opt-in model-quality check, separate from the deterministic provider matrix.
// Uses only invented workspace evidence and temporary stores; no MCP, external
// folders, skills or application state are connected. Never runs in -short.
// ORI_LIVE_ASSESSMENT=1 go test ./internal/chathttp -run '^TestLeadAssessmentConversation_Live$' -v
func TestLeadAssessmentConversation_Live(t *testing.T) {
	if testing.Short() || os.Getenv("ORI_LIVE_ASSESSMENT") != "1" {
		t.Skip("opt in with ORI_LIVE_ASSESSMENT=1 and OPENAI_API_KEY")
	}
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		t.Skip("OPENAI_API_KEY is not set")
	}
	model := os.Getenv("ORI_LIVE_ASSESSMENT_MODEL")
	if model == "" {
		model = "gpt-4.1-mini"
	}
	// OpenAI's real chat path uses the SDK client, not this stand-in factory.
	h, mutations := newCLIChatTestHandler(t, "openai", model, &scriptedChatProvider{})
	h.clientFactory = client.NewFactory(key)
	_, store, ws := assessmentFixture(t)
	if err := store.Update(ws.ID, func(w *workspace.Workspace) error {
		w.AgentInstances[0].Name = "cli-agent"
		w.MissionRuns[0].StartedAt = time.Now().UTC().Add(-48 * time.Hour)
		w.MissionRuns[0].FinishedAt = w.MissionRuns[0].StartedAt.Add(time.Minute)
		w.MissionRuns[0].Assessment = "Outcome: The cobalt-pilot story is the best supported idea, but it has no outline yet.\n\nBoundaries: Editor review is required before sending."
		w.Tasks = []workspace.Task{
			{ID: "outline", Description: "Draft the cobalt-pilot outline", Status: workspace.TaskStatusCompleted, Result: "Outline finished today.", To: "cli-agent"},
			{ID: "editor", Description: "Get editor review of the cobalt-pilot draft", Status: workspace.TaskStatusPending, To: "cli-agent"},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ws, _ = store.Get(ws.ID)
	before, _ := json.Marshal(ws)
	sessions, closeSessions := setupWorkspaceToolSessionStore(t)
	defer closeSessions()
	if err := sessions.CreateWorkspace(t.Context(), &session.Workspace{ID: ws.ID, Name: ws.Name}); err != nil {
		t.Fatal(err)
	}
	conversation := &session.Session{AgentName: "cli-agent", FolderID: ws.ID, Title: "Focus conversation", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := sessions.CreateSession(t.Context(), conversation); err != nil {
		t.Fatal(err)
	}
	h.SetWorkspaceStore(store)
	h.SetSessionStore(sessions)
	h.SetToolCallStore(sessions.ToolCallStore())
	ag, _ := h.store.GetAgent("cli-agent")
	ag.Settings.SystemPrompt = "You lead {{workspace.name}}. Be concise and useful."
	ag.Settings.ReasoningEffort = ""
	h.SetRuntimeResolver(&stubChatRuntimeResolver{resolved: &workspace.ResolvedAgentRuntime{
		Agent: ag, AgentInstance: &ws.AgentInstances[0], FromWorkspaceCopy: true,
	}})
	for turn, question := range []string{"What should I focus on in this workspace?", "Why that rather than starting another outline?"} {
		body, _ := json.Marshal(map[string]any{
			"question": question, "agent_name": "cli-agent",
			"route_context": map[string]any{"workspace_id": ws.ID, "surface": "workspace_detail", "page_path": "/workspaces/newsletter", "origin": "ask_ori"},
		})
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/chat", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Session-ID", conversation.ID)
		h.ChatHandler(rr, req)
		var reply struct {
			Response string `json:"response"`
		}
		if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &reply) != nil || reply.Response == "" {
			t.Fatalf("chat failed: %d", rr.Code)
		}
		t.Logf("%s\n%s", question, reply.Response)
		if turn == 0 && !strings.Contains(reply.Response, ws.MissionRuns[0].FinishedAt.Format("2006-01-02")) {
			t.Error("initial answer omitted the saved assessment's date")
		}
		lower := strings.ToLower(reply.Response)
		acknowledgesFinished := strings.Contains(lower, "complet") || strings.Contains(lower, "finished") || strings.Contains(lower, "already") || strings.Contains(lower, "done")
		if !strings.Contains(lower, "editor") || !strings.Contains(lower, "outline") || !acknowledgesFinished {
			t.Errorf("reply did not reconcile the completed outline with the pending editor review")
		}
	}
	calls, err := sessions.ToolCallStore().GetToolCalls(t.Context(), conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	assessmentReads, tasksRead := 0, false
	for _, call := range calls {
		t.Logf("tool: %s", call.ToolName)
		if call.ToolName == "workspace_goal_assessment" {
			assessmentReads++
			var evidence workspaceAssessmentView
			if json.Unmarshal([]byte(call.Result), &evidence) == nil && len(evidence.RecentlyFinished) == 1 && len(evidence.OpenWork) == 1 {
				tasksRead = evidence.RecentlyFinished[0].State == "done" && strings.Contains(evidence.OpenWork[0].Title, "editor review")
			}
		}
		if se, ok := workspace.NativeToolSideEffect(call.ToolName); !ok || se != workspace.SideEffectRead {
			t.Errorf("advice invoked a non-read tool: %s", call.ToolName)
		}
	}
	fresh, _ := store.Get(ws.ID)
	after, _ := json.Marshal(fresh)
	notes, err := sessions.ListNotesByWorkspace(t.Context(), ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Explaining an unchanged recommendation can reuse evidence from this
	// conversation; it need not make a redundant second read to be grounded.
	if assessmentReads < 1 || !tasksRead || string(before) != string(after) || len(notes) != 0 || len(*mutations) != 0 {
		t.Fatalf("missing grounding or unexpected write: assessment reads=%d tasks=%v notes=%d mutations=%d", assessmentReads, tasksRead, len(notes), len(*mutations))
	}
}
