package chathttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestLeadConversationOpen_SpeaksFirstWithoutRunningAnything(t *testing.T) {
	provider := &scriptedChatProvider{name: "ollama", supportsTools: true}
	h, mutations := newCLIChatTestHandler(t, "ollama", "test-model", provider)
	sessions, closeSessions := setupWorkspaceToolSessionStore(t)
	defer closeSessions()
	store := session.NewWorkspaceStoreAdapter(sessions)
	_, _, ws := assessmentFixture(t)
	ws.AgentInstances[0].Name = "cli-agent"
	ws.Tasks = []workspace.Task{
		{ID: "done", Description: "Finished outline", Status: workspace.TaskStatusCompleted, SourceType: workspace.TicketSourceGoalSuggestion, Priority: 10},
		{ID: "review", Description: "Get editor review", Status: workspace.TaskStatusBacklog, SourceType: workspace.TicketSourceGoalSuggestion, Priority: 5, Context: map[string]any{"goal_suggestion": map[string]any{"run_id": "review-one", "why": "Editor approval comes before publication.", "first_step": "Send the existing outline for review."}}},
	}
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	ws, err := store.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(ws)
	h.SetWorkspaceStore(store)
	h.SetSessionStore(sessions)
	ag, _ := h.store.GetAgent("cli-agent")
	h.SetRuntimeResolver(&stubChatRuntimeResolver{resolved: &workspace.ResolvedAgentRuntime{Agent: ag, AgentInstance: &ws.AgentInstances[0], FromWorkspaceCopy: true}})
	var id string
	for i := range 2 {
		r := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+ws.ID+"/lead-conversation/open", nil)
		r.SetPathValue("workspaceID", ws.ID)
		w := httptest.NewRecorder()
		h.LeadConversationOpen(w, r)
		var out session.LeadConversationResult
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Changed != (i == 0) || out.SessionID == "" {
			t.Fatalf("welcome %d %s", w.Code, w.Body.String())
		}
		if id != "" && id != out.SessionID {
			t.Fatal("repeated conversation")
		}
		id = out.SessionID
	}
	conversation, err := sessions.GetSession(t.Context(), id)
	if err != nil || len(conversation.Messages) != 1 {
		t.Fatalf("conversation %+v %v", conversation, err)
	}
	text := conversation.Messages[0].Content
	for _, want := range []string{"Hi, I’m cli-agent", "Get editor review", "Editor approval", "Source: saved assessment completed", "Nothing has been started", "mark these as finished: Finished outline"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %s", want, text)
		}
	}
	fresh, _ := store.Get(ws.ID)
	after, _ := json.Marshal(fresh)
	// Session counters are a separate store projection; native workspace work,
	// goal runs and definitions are unchanged by opening this conversation.
	if string(before) != string(after) || len(provider.requests) != 0 || len(*mutations) != 0 {
		t.Fatal("welcome ran work or mutated workspace")
	}
}

func TestLeadConversationOpen_RefusesOtherOwnersUnreadySetupAndGlobalNamesakes(t *testing.T) {
	for _, mode := range []string{"owner", "setup", "global", "group", "trashed", "selector", "refresh"} {
		t.Run(mode, func(t *testing.T) {
			provider := &scriptedChatProvider{name: "ollama", supportsTools: true}
			h, mutations := newCLIChatTestHandler(t, "ollama", "test-model", provider)
			sessions, cleanup := setupWorkspaceToolSessionStore(t)
			defer cleanup()
			_, store, ws := assessmentFixture(t)
			ws.AgentInstances[0].Name = "cli-agent"
			switch mode {
			case "owner":
				ws.OwnerUserID = "someone-else"
			case "setup":
				ws.SetTemplateProvenance(&workspace.TemplateProvenance{TemplateID: "needs-setup", SetupWizard: &workspace.SetupWizard{Version: 1, Steps: []workspace.SetupWizardStep{{ID: "folder", Kind: workspace.SetupStepKindDirectory, Required: true}}}})
			case "group":
				ws.Kind = "group"
			case "trashed":
				ws.Status = workspace.StatusTrashed
			}
			if err := store.Save(ws); err != nil {
				t.Fatal(err)
			}
			ws, err := store.Get(ws.ID)
			if err != nil {
				t.Fatal(err)
			}
			h.SetWorkspaceStore(store)
			h.SetSessionStore(sessions)
			ag, _ := h.store.GetAgent("cli-agent")
			resolved := &workspace.ResolvedAgentRuntime{Agent: ag, AgentInstance: &ws.AgentInstances[0], FromWorkspaceCopy: true}
			if mode == "global" {
				resolved.AgentInstance = nil
				resolved.FromWorkspaceCopy = false
			}
			h.SetRuntimeResolver(&stubChatRuntimeResolver{resolved: resolved})
			body := ""
			if mode == "selector" {
				body = `{"session_id":"victim"}`
			}
			if mode == "refresh" {
				body = `{"refresh":true}`
			}
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
			r.SetPathValue("workspaceID", ws.ID)
			w := httptest.NewRecorder()
			h.LeadConversationOpen(w, r)
			if strings.Contains(w.Body.String(), `"changed":true`) || len(provider.requests) != 0 || len(*mutations) != 0 {
				t.Fatalf("unauthorized welcome %d %s", w.Code, w.Body.String())
			}
			want := http.StatusOK
			if mode == "owner" {
				want = http.StatusNotFound
			}
			if mode == "selector" || mode == "refresh" {
				want = http.StatusBadRequest
			}
			if w.Code != want {
				t.Fatalf("status %d want %d: %s", w.Code, want, w.Body.String())
			}
			if mode == "setup" && !strings.Contains(w.Body.String(), "waiting_setup") {
				t.Fatalf("missing setup was treated as ready: %s", w.Body.String())
			}
		})
	}
}

func TestLeadWelcomeAssessment_QualifiesChangedGoalsAndIgnoresOldTasks(t *testing.T) {
	_, _, ws := assessmentFixture(t)
	ws.Tasks = []workspace.Task{{Description: "Do not recommend", Status: workspace.TaskStatusBacklog, SourceType: workspace.TicketSourceGoalSuggestion, Context: map[string]any{"goal_suggestion": map[string]any{"run_id": "old-run"}}}}
	if text := leadWelcomeAssessment(ws); strings.Contains(text, "Do not recommend") {
		t.Fatal("recommended an unrelated old suggestion")
	}
	ws.Mission = "A different outcome"
	text := leadWelcomeAssessment(ws)
	if !strings.Contains(text, "goal is older or unverified") || !strings.Contains(text, "goal has since changed") {
		t.Fatalf("freshness lost: %s", text)
	}
	ws.MissionRuns = []workspace.MissionRunRecord{{RunID: "failed", Succeeded: false, Error: "private raw provider output"}}
	text = leadWelcomeAssessment(ws)
	if !strings.Contains(text, "haven’t retried") || strings.Contains(text, "private raw provider output") {
		t.Fatalf("failure fallback: %s", text)
	}
	ws.MissionRuns = nil
	if leadWelcomeAssessment(ws) != "" {
		t.Fatal("invented assessment")
	}
}
