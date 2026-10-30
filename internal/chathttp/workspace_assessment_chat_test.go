package chathttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/client"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// The actual /api/chat pipeline must deliver the reader and its guidance on
// both ordinary and template-variable prompts, including the CLI broker path.
// Scripted replies prove plumbing and effect counts, not model judgment.
func TestChatHandler_LeadDiscussesAssessmentThroughExistingChat(t *testing.T) {
	for _, providerName := range []string{"openai", "codex", "claude_code", "claude", "ollama", "gemini"} {
		for _, basePrompt := range []string{"You lead this workspace.", "You lead {{workspace.name}}."} {
			t.Run(providerName+"/"+basePrompt, func(t *testing.T) {
				const answer = "The saved review found three ideas but no outline. Draft the customer-interview outline first; the current task list shows it is still pending. You can review that task before starting it."
				provider := &scriptedChatProvider{name: providerName, supportsTools: true, responses: []llm.ChatResponse{
					{ToolCalls: []llm.ToolCall{{ID: "assessment-read", Name: "workspace_goal_assessment", Arguments: `{}`}}, FinishReason: llm.FinishReasonToolCalls},
					{ToolCalls: []llm.ToolCall{{ID: "task-read", Name: "workspace_tasks", Arguments: `{}`}}, FinishReason: llm.FinishReasonToolCalls},
					{Content: answer},
				}}
				h, mutations := newCLIChatTestHandler(t, providerName, "test-model", provider)
				if providerName == "openai" {
					useScriptedAssessmentOpenAI(t, h, provider)
				}
				_, store, ws := assessmentFixture(t)
				if err := store.Update(ws.ID, func(w *workspace.Workspace) error {
					w.AgentInstances[0].Name = "cli-agent"
					w.Tasks = []workspace.Task{{ID: "outline", Description: "Draft the customer-interview outline", Status: workspace.TaskStatusPending, To: "cli-agent"}}
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
				conversation := &session.Session{AgentName: "cli-agent", FolderID: ws.ID, Title: "Focus", CreatedAt: time.Now(), UpdatedAt: time.Now()}
				if err := sessions.CreateSession(t.Context(), conversation); err != nil {
					t.Fatal(err)
				}
				h.SetWorkspaceStore(store)
				h.SetSessionStore(sessions)
				ag, _ := h.store.GetAgent("cli-agent")
				ag.Settings.SystemPrompt = basePrompt
				h.SetRuntimeResolver(&stubChatRuntimeResolver{resolved: &workspace.ResolvedAgentRuntime{
					Agent: ag, AgentInstance: &ws.AgentInstances[0], FromWorkspaceCopy: true,
				}})
				body, _ := json.Marshal(map[string]any{
					"question": "What should I focus on in this workspace?", "agent_name": "cli-agent",
					"route_context": map[string]any{"workspace_id": ws.ID, "surface": "workspace_detail", "page_path": "/workspaces/newsletter", "origin": "ask_ori"},
				})
				rr := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodPost, "/api/chat", bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-Session-ID", conversation.ID)
				h.ChatHandler(rr, req)
				if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), answer) || len(provider.requests) != 3 {
					t.Fatalf("chat = %d %s; model requests = %d", rr.Code, rr.Body.String(), len(provider.requests))
				}
				var reply struct {
					Response string `json:"response"`
				}
				if err := json.Unmarshal(rr.Body.Bytes(), &reply); err != nil {
					t.Fatal(err)
				}
				if !strings.HasPrefix(reply.Response, answer+"\n\nSource: saved assessment completed 2026-10-29 09:01 UTC;") || strings.Count(reply.Response, "Source:") != 1 {
					t.Fatalf("date-free model answer lost host attribution: %s", reply.Response)
				}
				history, err := sessions.GetMessages(t.Context(), conversation.ID)
				if err != nil || len(history) != 2 || history[1].Content != reply.Response {
					t.Fatalf("attribution missing from persisted conversation: %v, %v", history, err)
				}
				for i, request := range provider.requests {
					if !requestHasTool(request, "workspace_goal_assessment") || !strings.Contains(request.SystemPrompt, "## Discussing this workspace's direction") || !strings.Contains(request.SystemPrompt, "Advice is not execution consent") {
						t.Fatalf("round %d lost reader or guidance: %s", i, request.SystemPrompt)
					}
				}
				var evidence workspaceAssessmentView
				for _, message := range provider.requests[1].Messages {
					if message.Role == llm.RoleTool && message.ToolCallID == "assessment-read" {
						if err := json.Unmarshal([]byte(message.Content), &evidence); err != nil {
							t.Fatalf("tool response: %s, %v", message.Content, err)
						}
					}
				}
				if evidence.Assessment == nil || evidence.Assessment.RunID != "review-one" || evidence.Goal != ws.Mission || len(evidence.OpenWork) != 1 || evidence.OpenWork[0].Title != ws.Tasks[0].Description {
					t.Fatalf("model never received canonical assessment and full goal: %+v", evidence)
				}
				currentTasks := false
				for _, message := range provider.requests[2].Messages {
					if message.Role == llm.RoleTool && message.ToolCallID == "task-read" && strings.Contains(message.Content, "customer-interview outline") {
						currentTasks = true
					}
				}
				fresh, _ := store.Get(ws.ID)
				after, _ := json.Marshal(fresh)
				if !currentTasks || len(*mutations) != 0 || string(before) != string(after) {
					t.Fatalf("advice missed current evidence or changed state: tasks=%v mutations=%v", currentTasks, *mutations)
				}
			})
		}
	}
}

// Exercise the SDK's real HTTP serialization as well as the generic provider
// loop. Only the model is a fixture; all chat/session/tool code stays real.
func useScriptedAssessmentOpenAI(t *testing.T, h *Handler, provider *scriptedChatProvider) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var wire struct {
			Messages []struct {
				Role       string `json:"role"`
				Content    string `json:"content"`
				ToolCallID string `json:"tool_call_id"`
			} `json:"messages"`
			Tools []struct {
				Function llm.Tool `json:"function"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
			t.Error(err)
			http.Error(w, "invalid request", 400)
			return
		}
		req := llm.ChatRequest{}
		for _, msg := range wire.Messages {
			if msg.Role == llm.RoleSystem {
				req.SystemPrompt += msg.Content + "\n"
			} else {
				req.Messages = append(req.Messages, llm.Message{Role: msg.Role, Content: msg.Content, ToolCallID: msg.ToolCallID})
			}
		}
		for _, tool := range wire.Tools {
			req.Tools = append(req.Tools, tool.Function)
		}
		reply, err := provider.Chat(r.Context(), req)
		if err != nil {
			t.Error(err)
			http.Error(w, "fixture error", 500)
			return
		}
		toolCalls := []map[string]any{}
		for _, tc := range reply.ToolCalls {
			toolCalls = append(toolCalls, map[string]any{"id": tc.ID, "type": "function", "function": map[string]any{"name": tc.Name, "arguments": tc.Arguments}})
		}
		writeJSONResponse(w, map[string]any{"id": "fixture-completion", "object": "chat.completion", "choices": []map[string]any{{"index": 0, "finish_reason": reply.FinishReason, "message": map[string]any{"role": "assistant", "content": reply.Content, "tool_calls": toolCalls}}}})
	}))
	t.Cleanup(server.Close)
	t.Setenv("OPENAI_BASE_URL", server.URL+"/")
	h.clientFactory = client.NewFactory("fixture-key")
}

func TestWorkspaceAssessmentPrompt_OnlyVerifiedLeadAndNoAutomaticActions(t *testing.T) {
	p, _, _ := assessmentFixture(t)
	prompt := workspaceAssessmentPrompt(&resolvedChatAgent{WorkspaceTools: p})
	for _, required := range []string{
		"one recommended next step", "relevant current workspace readers", "historical model output",
		"For changed or unknown goal matches", "status=missing", "do not save a note or memory",
		"ambiguous \"yes\" grants no authority", "Keep ordinary greetings", "existing controls",
		"re-read workspace_goal_assessment", "current task records now show", "Never recommend doing a task already finished",
	} {
		if !strings.Contains(prompt, required) {
			t.Errorf("missing conversation rule: %q", required)
		}
	}
	if workspaceAssessmentPrompt(nil) != "" || workspaceAssessmentPrompt(&resolvedChatAgent{}) != "" {
		t.Fatal("global chat received assessment instructions")
	}
}
