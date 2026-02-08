package agenthttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

func newHomeRouteTestStore(t *testing.T) store.Store {
	t.Helper()

	path := filepath.Join(t.TempDir(), "agents_index.json")
	st, err := store.NewFileStore(path, types.Settings{
		Model:       "gpt-5-nano",
		Temperature: 1.0,
	})
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}
	return st
}

func addHomeRouteTestAgent(t *testing.T, st store.Store, name string, cfg *store.CreateAgentConfig, status types.AgentStatus, description string, tags []string, plugins []string) {
	t.Helper()

	if cfg == nil {
		cfg = &store.CreateAgentConfig{Type: "general"}
	}
	if err := st.CreateAgent(name, cfg); err != nil {
		t.Fatalf("failed to create agent %q: %v", name, err)
	}

	ag, ok := st.GetAgent(name)
	if !ok || ag == nil {
		t.Fatalf("agent %q not found after creation", name)
	}

	ag.Status = status
	if ag.Metadata == nil {
		ag.Metadata = &types.AgentMetadata{}
	}
	ag.Metadata.Description = description
	ag.Metadata.Tags = tags
	ag.Metadata.Favorite = false

	if ag.Plugins == nil {
		ag.Plugins = map[string]types.LoadedPlugin{}
	}
	for _, plugin := range plugins {
		ag.Plugins[plugin] = types.LoadedPlugin{}
	}

	if err := st.SetAgent(name, ag); err != nil {
		t.Fatalf("failed to persist agent %q: %v", name, err)
	}
}

func postRouteRequest(t *testing.T, handler *HomeAssistantRouteHandler, payload map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal request body: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/home-assistant/route", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.RouteHandler(rr, req)
	return rr
}

func TestHomeAssistantRouteHandler_MethodNotAllowed(t *testing.T) {
	st := newHomeRouteTestStore(t)
	handler := NewHomeAssistantRouteHandler(st)

	req := httptest.NewRequest(http.MethodGet, "/api/home-assistant/route", nil)
	rr := httptest.NewRecorder()
	handler.RouteHandler(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status %d, got %d", http.StatusMethodNotAllowed, rr.Code)
	}
}

func TestHomeAssistantRouteHandler_EmptyPrompt(t *testing.T) {
	st := newHomeRouteTestStore(t)
	handler := NewHomeAssistantRouteHandler(st)

	rr := postRouteRequest(t, handler, map[string]string{"prompt": "   "})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rr.Code)
	}
}

func TestHomeAssistantRouteHandler_TravelMatch(t *testing.T) {
	st := newHomeRouteTestStore(t)
	handler := NewHomeAssistantRouteHandler(st)

	addHomeRouteTestAgent(t, st, "Travel Planner", &store.CreateAgentConfig{Type: "research"}, types.AgentStatusActive,
		"Plans multi-day travel itineraries", []string{"travel", "itinerary"}, []string{"weather-tool", "web-search"})
	addHomeRouteTestAgent(t, st, "Code Helper", &store.CreateAgentConfig{Type: "general"}, types.AgentStatusActive,
		"Helps with code review", []string{"coding"}, []string{"git"})

	rr := postRouteRequest(t, handler, map[string]string{"prompt": "Plan my 3 day trip in LA"})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}

	var resp HomeAssistantRouteResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Intent != "travel_planning" {
		t.Fatalf("expected intent travel_planning, got %q", resp.Intent)
	}
	if resp.RequiresCreation {
		t.Fatalf("expected requires_creation false, got true")
	}
	if resp.MatchedAgent != "Travel Planner" {
		t.Fatalf("expected matched agent Travel Planner, got %q", resp.MatchedAgent)
	}
	if resp.Score < 4 {
		t.Fatalf("expected score >= 4, got %d", resp.Score)
	}
	if len(resp.Reasons) == 0 {
		t.Fatalf("expected non-empty reasons")
	}
}

func TestHomeAssistantRouteHandler_NoMatchRequiresCreation(t *testing.T) {
	st := newHomeRouteTestStore(t)
	handler := NewHomeAssistantRouteHandler(st)

	addHomeRouteTestAgent(t, st, "Code Assistant", &store.CreateAgentConfig{Type: "general"}, types.AgentStatusActive,
		"Helps with code and tests", []string{"coding"}, []string{"git", "filesystem"})

	rr := postRouteRequest(t, handler, map[string]string{"prompt": "Plan my 3 day trip in LA"})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}

	var resp HomeAssistantRouteResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Intent != "travel_planning" {
		t.Fatalf("expected intent travel_planning, got %q", resp.Intent)
	}
	if !resp.RequiresCreation {
		t.Fatalf("expected requires_creation true")
	}
	if resp.MatchedAgent != "" {
		t.Fatalf("expected no matched agent, got %q", resp.MatchedAgent)
	}
	if resp.SuggestedAgentName != "Travel Planner" {
		t.Fatalf("expected suggested name Travel Planner, got %q", resp.SuggestedAgentName)
	}
	if resp.SuggestedAgentType != "research" {
		t.Fatalf("expected suggested type research, got %q", resp.SuggestedAgentType)
	}
}

func TestHomeAssistantRouteHandler_EmailMatch(t *testing.T) {
	st := newHomeRouteTestStore(t)
	handler := NewHomeAssistantRouteHandler(st)

	addHomeRouteTestAgent(t, st, "Inbox Triage", &store.CreateAgentConfig{Type: "tool-calling"}, types.AgentStatusActive,
		"Summarizes unread emails and drafts replies", []string{"email", "inbox"}, []string{"gmail-reader"})

	rr := postRouteRequest(t, handler, map[string]string{"prompt": "Check my email and summarize unread messages"})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}

	var resp HomeAssistantRouteResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Intent != "email_check" {
		t.Fatalf("expected intent email_check, got %q", resp.Intent)
	}
	if resp.RequiresCreation {
		t.Fatalf("expected requires_creation false")
	}
	if resp.MatchedAgent != "Inbox Triage" {
		t.Fatalf("expected matched agent Inbox Triage, got %q", resp.MatchedAgent)
	}
}

func TestHomeAssistantRouteHandler_GeneralPrompt_NoLowSignalReuse(t *testing.T) {
	st := newHomeRouteTestStore(t)
	handler := NewHomeAssistantRouteHandler(st)

	addHomeRouteTestAgent(t, st, "Travel Planner", &store.CreateAgentConfig{Type: "research"}, types.AgentStatusActive,
		"Plans multi-day travel itineraries", []string{"travel", "itinerary"}, []string{"weather-tool", "web-search"})

	rr := postRouteRequest(t, handler, map[string]string{"prompt": "open reaper"})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}

	var resp HomeAssistantRouteResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Intent != "general_task" {
		t.Fatalf("expected intent general_task, got %q", resp.Intent)
	}
	if !resp.RequiresCreation {
		t.Fatalf("expected requires_creation true for low-signal general prompt")
	}
	if resp.MatchedAgent != "" {
		t.Fatalf("expected no matched agent, got %q", resp.MatchedAgent)
	}
}

func TestHomeAssistantRouteHandler_GeneralPrompt_ContextualMatch(t *testing.T) {
	st := newHomeRouteTestStore(t)
	handler := NewHomeAssistantRouteHandler(st)

	addHomeRouteTestAgent(t, st, "Desktop Launcher", &store.CreateAgentConfig{Type: "tool-calling"}, types.AgentStatusActive,
		"Opens desktop applications like reaper and finder", []string{"desktop", "automation"}, []string{"os-shell"})
	addHomeRouteTestAgent(t, st, "Travel Planner", &store.CreateAgentConfig{Type: "research"}, types.AgentStatusActive,
		"Plans travel itineraries", []string{"travel"}, []string{"weather-tool"})

	rr := postRouteRequest(t, handler, map[string]string{"prompt": "open reaper"})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}

	var resp HomeAssistantRouteResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Intent != "general_task" {
		t.Fatalf("expected intent general_task, got %q", resp.Intent)
	}
	if resp.RequiresCreation {
		t.Fatalf("expected contextual general match, got requires_creation=true")
	}
	if resp.MatchedAgent != "Desktop Launcher" {
		t.Fatalf("expected matched agent Desktop Launcher, got %q", resp.MatchedAgent)
	}
	if resp.Score < 3 {
		t.Fatalf("expected score >= 3, got %d", resp.Score)
	}
}
